package library

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// stubAdultProvider is an in-memory core.AdultMetadataProvider that COUNTS
// every call. The count is the point: "zero stash-box traffic when the module
// is off" is an acceptance criterion, and a stub that only answers questions
// cannot prove a question was never asked.
type stubAdultProvider struct {
	sites  []core.SiteMeta
	scenes map[string][]core.SceneMeta

	calls             int
	pageSize          int
	searchErr, getErr error
}

func (s *stubAdultProvider) SearchSites(_ context.Context, q string) ([]core.SiteMeta, error) {
	s.calls++
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	return s.sites, nil
}

func (s *stubAdultProvider) GetSite(_ context.Context, stashID string) (*core.SiteMeta, error) {
	s.calls++
	if s.getErr != nil {
		return nil, s.getErr
	}
	for _, site := range s.sites {
		if site.StashID == stashID {
			return &site, nil
		}
	}
	return nil, errors.New("stub: no such site")
}

// The filter rail's typeaheads are part of the provider interface but nothing
// in the library layer reaches them: a refresh walks a site's catalogue, it
// does not browse. They count a call like every other method so that "zero
// stash-box traffic" keeps meaning zero.
func (s *stubAdultProvider) SearchPerformers(_ context.Context, q string) ([]core.ScenePerformerMeta, error) {
	s.calls++
	return nil, s.searchErr
}

func (s *stubAdultProvider) SearchTags(_ context.Context, q string) ([]core.SceneFilterRef, error) {
	s.calls++
	return nil, s.searchErr
}

func (s *stubAdultProvider) SearchScenes(_ context.Context, q core.SceneQuery) (*core.ScenePage, error) {
	s.calls++
	if s.searchErr != nil {
		return nil, s.searchErr
	}
	all := s.scenes[q.SiteStashID]
	perPage := s.pageSize
	if perPage <= 0 {
		perPage = q.PerPage
	}
	if perPage <= 0 {
		perPage = 25
	}
	page := max(q.Page, 1)
	start := (page - 1) * perPage
	if start > len(all) {
		start = len(all)
	}
	end := min(start+perPage, len(all))
	return &core.ScenePage{
		Page: page, PerPage: perPage, Total: len(all), Scenes: all[start:end],
	}, nil
}

func (s *stubAdultProvider) GetScene(_ context.Context, stashID string) (*core.SceneMeta, error) {
	s.calls++
	if s.getErr != nil {
		return nil, s.getErr
	}
	for _, scenes := range s.scenes {
		for _, scene := range scenes {
			if scene.StashID == stashID {
				return &scene, nil
			}
		}
	}
	return nil, errors.New("stub: no such scene")
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// siteRef is a stash id on the LEGACY instance, which is what a single-box
// install and every pre-instances client name. adultchain_test.go is where the
// second box appears.
func siteRef(stashID string) core.ItemRef {
	return core.ItemRef{Provider: core.ProviderStashbox, Ref: stashID}
}

// enableAdultLibrary says the whole enable directly: the Adult library exists
// and is switched on. An adult library IS the module — every gate below asks
// AnyActiveLibraryOfKind, never a setting (see adultReady) — so a test that
// wants scenes reachable has to own a row rather than a flag.
//
// The row it writes is the one an install carries, and each field is load
// bearing. Restricted and NOT dlna_visible because the LAN tree has no accounts:
// a shelf on it is readable by every device in the house, which is the one
// mistake this module may not make. The legacy `stashbox` chain because that is
// what a single-box install is named by, here and in every pre-instances client
// for compatibility. IsDefault only where no adult library exists yet — the partial unique
// index admits one default per kind — and Active is CreateLibrary's own doing,
// so nothing here sets it.
//
// Idempotent, so a harness may call it on a store some earlier step already
// switched off: an existing row is switched back on rather than duplicated,
// which would leave two shelves fighting over one root path.
func enableAdultLibrary(t *testing.T, st *store.Store) core.Library {
	t.Helper()
	ctx := context.Background()
	lib, err := st.GetDefaultLibrary(ctx, core.LibraryKindAdult)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetDefaultLibrary(adult): %v", err)
	}
	if lib != nil {
		if err := st.SetLibraryActive(ctx, lib.ID, true); err != nil {
			t.Fatalf("SetLibraryActive(%d, true): %v", lib.ID, err)
		}
		lib.Active = true
		return *lib
	}
	created := &core.Library{
		Kind: core.LibraryKindAdult, Name: store.AdultLibraryName,
		RootPath: store.AdultLibraryRoot, Providers: []string{core.ProviderStashbox},
		DLNAVisible: false, Restricted: true, IsDefault: true,
	}
	if err := st.CreateLibrary(ctx, created); err != nil {
		t.Fatalf("CreateLibrary(adult): %v", err)
	}
	return *created
}

// setAdultLibrariesActive is the other half of the same switch, spelled per
// library because that is where it lives now.
//
// It has to reach EVERY adult library or it says nothing: the module gate is
// "is any adult library active", so an off that left a sibling on would leave
// the module reachable while the test believed it had shut it. The rows, the
// files and the grants all survive the flip — switching off hides, it never
// deletes — which is what the tests below then check.
func setAdultLibrariesActive(t *testing.T, st *store.Store, active bool) {
	t.Helper()
	ctx := context.Background()
	libs, err := st.ListLibrariesByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("ListLibrariesByKind(adult): %v", err)
	}
	for _, lib := range libs {
		if err := st.SetLibraryActive(ctx, lib.ID, active); err != nil {
			t.Fatalf("SetLibraryActive(%d, %t): %v", lib.ID, active, err)
		}
	}
}

// adultHarness is the library harness plus an adult provider and the module
// switched on.
type adultHarness struct {
	*harness
	adult *stubAdultProvider
}

func newAdultHarness(t *testing.T, enabled bool) *adultHarness {
	t.Helper()
	h := newHarness(t)
	a := &adultHarness{harness: h, adult: &stubAdultProvider{scenes: map[string][]core.SceneMeta{}}}
	if enabled {
		enableAdultLibrary(t, h.st)
	}
	h.mgr = a.newManager(h.st, h.provider)
	return a
}

// newManager overrides the harness' builder so every Manager it makes carries
// the adult provider — and the REAL scene parser, because the adult path's
// whole disposability story is that a rescan re-reads the organizer's own
// filenames, and a fake parser cannot prove that.
func (a *adultHarness) newManager(st *store.Store, mp core.MetadataProvider) *Manager {
	a.t.Helper()
	mgr := a.harness.newManager(st, mp)
	mgr.adult = a.adult
	return mgr
}

// seedBrazzers gives the provider one site with three scenes across two years,
// deliberately returned newest-first the way a DATE/DESC query does.
func (a *adultHarness) seedBrazzers() {
	a.adult.sites = []core.SiteMeta{{StashID: "site-1", Name: "Brazzers", ImageURL: a.posterURL}}
	a.adult.scenes["site-1"] = []core.SceneMeta{
		{StashID: "scene-c", SiteStashID: "site-1", SiteName: "Brazzers", Title: "Third", Date: date(2023, time.February, 1)},
		{StashID: "scene-b", SiteStashID: "site-1", SiteName: "Brazzers", Title: "Second", Date: date(2022, time.June, 9),
			Performers: []core.ScenePerformer{{Name: "Jane Doe"}, {Name: "Legal Name", As: "Stage Name"}}},
		{StashID: "scene-a", SiteStashID: "site-1", SiteName: "Brazzers", Title: "Deep Impact", Date: date(2022, time.March, 14),
			URL: "https://example.test/scene-a"},
	}
}

// addSite adds a site AND walks its catalogue, which is what the tests below
// are almost always about. The ordinary AddSite defers the walk to a job (see
// TestAddSiteDefersTheCatalogueWalk); this helper is the waiting variant so
// that every test written against the old, always-walking AddSite still asserts
// the same thing about the same walk.
func (a *adultHarness) addSite(id string) *core.Series {
	a.t.Helper()
	sr, err := a.mgr.AddSiteAndWait(context.Background(), siteRef(id), nil, 0)
	if err != nil {
		a.t.Fatalf("AddSiteAndWait: %v", err)
	}
	return sr
}

func (a *adultHarness) episodes(seriesID int64) []core.Episode {
	a.t.Helper()
	eps, err := a.st.ListEpisodes(context.Background(), seriesID)
	if err != nil {
		a.t.Fatalf("ListEpisodes: %v", err)
	}
	sort.Slice(eps, func(i, j int) bool {
		if eps[i].SeasonNumber != eps[j].SeasonNumber {
			return eps[i].SeasonNumber < eps[j].SeasonNumber
		}
		return eps[i].EpisodeNumber < eps[j].EpisodeNumber
	})
	return eps
}

// The organizer's adult root and the store's adult library root are the same
// directory said twice. A test rather than a shared constant because the two
// packages are deliberately independent — but a drift would file scenes
// somewhere the DLNA filter and `caravan prepare` do not look.
func TestAdultRootMatchesTheAdultLibraryRow(t *testing.T) {
	if got := path.Join(LibraryDir, AdultDir); got != store.AdultLibraryRoot {
		t.Errorf("library adult root = %q, store.AdultLibraryRoot = %q", got, store.AdultLibraryRoot)
	}
	if got := adultSeriesDir(stockAdultLib(), "Brazzers"); got != "library/Adult/Brazzers" {
		t.Errorf("adultSeriesDir = %q, want library/Adult/Brazzers", got)
	}
	// A site with characters no filesystem accepts still lands under the adult
	// root rather than escaping it.
	if got := adultSeriesDir(stockAdultLib(), "Bad/Name:*"); !strings.HasPrefix(got, "library/Adult/") ||
		strings.Count(got, "/") != 2 {
		t.Errorf("adultSeriesDir(stockAdultLib(), unsafe) = %q, want a single component under library/Adult", got)
	}
}

func TestAddSiteMapsScenesOntoSeasonsAndEpisodes(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()

	sr := h.addSite("site-1")
	if sr.Kind != core.SeriesKindAdult {
		t.Errorf("series kind = %q, want %q", sr.Kind, core.SeriesKindAdult)
	}
	if sr.StashID != "site-1" {
		t.Errorf("series stash id = %q, want site-1", sr.StashID)
	}
	if sr.Path != "library/Adult/Brazzers" {
		t.Errorf("series path = %q, want library/Adult/Brazzers", sr.Path)
	}
	if sr.TMDBID != 0 {
		t.Errorf("series tmdb id = %d, want 0", sr.TMDBID)
	}

	seasons, err := h.st.ListSeasons(context.Background(), sr.ID)
	if err != nil {
		t.Fatalf("ListSeasons: %v", err)
	}
	gotYears := []int{}
	for _, s := range seasons {
		gotYears = append(gotYears, s.Number)
	}
	sort.Ints(gotYears)
	if fmt.Sprint(gotYears) != "[2022 2023]" {
		t.Errorf("seasons = %v, want the release years [2022 2023]", gotYears)
	}

	eps := h.episodes(sr.ID)
	if len(eps) != 3 {
		t.Fatalf("episodes = %d, want 3", len(eps))
	}
	// Oldest first within a year, whatever order the provider paged them back.
	want := []struct {
		season, number int
		stashID, title string
	}{
		{2022, 1, "scene-a", "Deep Impact"},
		{2022, 2, "scene-b", "Second"},
		{2023, 1, "scene-c", "Third"},
	}
	for i, w := range want {
		got := eps[i]
		if got.SeasonNumber != w.season || got.EpisodeNumber != w.number ||
			got.StashID != w.stashID || got.Title != w.title {
			t.Errorf("episode %d = S%dE%d %s %q, want S%dE%d %s %q",
				i, got.SeasonNumber, got.EpisodeNumber, got.StashID, got.Title,
				w.season, w.number, w.stashID, w.title)
		}
	}
	if got := eps[0].AirDate.UTC().Format("2006-01-02"); got != "2022-03-14" {
		t.Errorf("air date = %s, want the scene's release date 2022-03-14", got)
	}

	// Scene-side metadata rides in the JSON column, performers credited under
	// the alias the scene used.
	if scene := eps[1].Scene; scene == nil {
		t.Fatal("episode has no scene metadata")
	} else if fmt.Sprint(scene.Performers) != "[Jane Doe Stage Name]" || scene.Studio != "Brazzers" {
		t.Errorf("scene info = %+v, want studio Brazzers and the credited aliases", scene)
	}
	if got := eps[0].Scene.URL; got != "https://example.test/scene-a" {
		t.Errorf("scene url = %q", got)
	}
}

// Numbering has to survive a back-filled scene, because the number is the
// address a file on disk was named after. Renumbering would rename every later
// file in the year and orphan the ones already linked.
func TestSceneNumbersAreStableWhenTheProviderBackFills(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	sr := h.addSite("site-1")

	before := h.episodes(sr.ID)

	// A scene from the middle of 2022 turns up later than the ones around it.
	h.adult.scenes["site-1"] = append(h.adult.scenes["site-1"], core.SceneMeta{
		StashID: "scene-late", SiteStashID: "site-1", SiteName: "Brazzers",
		Title: "Back Filled", Date: date(2022, time.April, 1),
	})
	if err := h.mgr.syncSiteScenes(context.Background(), sr); err != nil {
		t.Fatalf("syncSiteScenes: %v", err)
	}

	after := h.episodes(sr.ID)
	if len(after) != len(before)+1 {
		t.Fatalf("episodes = %d, want %d", len(after), len(before)+1)
	}
	byStash := map[string]core.Episode{}
	for _, e := range after {
		byStash[e.StashID] = e
	}
	for _, prior := range before {
		got := byStash[prior.StashID]
		if got.SeasonNumber != prior.SeasonNumber || got.EpisodeNumber != prior.EpisodeNumber {
			t.Errorf("scene %s moved from S%dE%d to S%dE%d", prior.StashID,
				prior.SeasonNumber, prior.EpisodeNumber, got.SeasonNumber, got.EpisodeNumber)
		}
	}
	// The new one is appended after the highest number that year already used,
	// not inserted at its chronological position.
	if got := byStash["scene-late"]; got.SeasonNumber != 2022 || got.EpisodeNumber != 3 {
		t.Errorf("back-filled scene = S%dE%d, want S2022E03", got.SeasonNumber, got.EpisodeNumber)
	}
}

// Numbering a catalogue from scratch has to be deterministic, or the DB is not
// disposable: deleting it and re-walking the same catalogue would produce
// different episode numbers than the filenames on disk were named after.
func TestSceneNumberingIsDeterministicFromTheCatalogue(t *testing.T) {
	scenes := []core.SceneMeta{
		{StashID: "c", Date: date(2022, time.March, 14), Code: "B"},
		{StashID: "a", Date: date(2022, time.March, 14), Code: "A"},
		{StashID: "b", Date: date(2022, time.January, 2)},
	}
	render := func(eps []core.Episode) string {
		var b strings.Builder
		for _, e := range eps {
			fmt.Fprintf(&b, "S%dE%d=%s ", e.SeasonNumber, e.EpisodeNumber, e.StashID)
		}
		return b.String()
	}
	first := render(numberScenes(scenes, nil))
	// Same catalogue, different provider paging order.
	shuffled := []core.SceneMeta{scenes[2], scenes[0], scenes[1]}
	second := render(numberScenes(shuffled, nil))
	if first != second {
		t.Errorf("numbering depends on provider order:\n%s\n%s", first, second)
	}

	got := numberScenes(scenes, nil)
	want := []string{"b", "a", "c"} // date, then code as the same-day tie-break
	for i, stashID := range want {
		if got[i].StashID != stashID || got[i].EpisodeNumber != i+1 {
			t.Errorf("episode %d = %s E%d, want %s E%d", i, got[i].StashID, got[i].EpisodeNumber, stashID, i+1)
		}
	}
}

// A scene the provider cannot date cannot be filed: the date IS the season.
func TestUndatedScenesAreDropped(t *testing.T) {
	got := numberScenes([]core.SceneMeta{
		{StashID: "dated", Date: date(2022, time.March, 14)},
		{StashID: "undated"},
	}, nil)
	if len(got) != 1 || got[0].StashID != "dated" {
		t.Errorf("numberScenes = %+v, want only the dated scene", got)
	}
}

// ---- the gate -------------------------------------------------------------

func TestAddSiteRefusesWhenTheModuleIsDisabled(t *testing.T) {
	h := newAdultHarness(t, false)
	h.seedBrazzers()

	if _, err := h.mgr.AddSite(context.Background(), siteRef("site-1"), nil, 0); !errors.Is(err, ErrAdultDisabled) {
		t.Errorf("AddSite error = %v, want ErrAdultDisabled", err)
	}
	if h.adult.calls != 0 {
		t.Errorf("provider was called %d times with the module disabled, want 0", h.adult.calls)
	}
}

func TestAddSiteReportsAMissingProvider(t *testing.T) {
	h := newAdultHarness(t, true)
	h.mgr.adult = nil
	if _, err := h.mgr.AddSite(context.Background(), siteRef("site-1"), nil, 0); !errors.Is(err, core.ErrNoAdultProvider) {
		t.Errorf("AddSite error = %v, want ErrNoAdultProvider", err)
	}
}

// The refresh sweep is the recurring job that would otherwise reach the
// endpoint on a schedule. With the module off it must make no request AND
// report no error: a sweep that logged a failure about a disabled module every
// twelve hours is its own kind of trace.
func TestRefreshMakesNoAdultRequestWhenDisabled(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	sr := h.addSite("site-1")

	setAdultLibrariesActive(t, h.st, false)
	h.adult.calls = 0

	res := &RefreshResult{}
	if err := h.mgr.refreshSites(context.Background(), res); err != nil {
		t.Fatalf("refreshSites: %v", err)
	}
	if h.adult.calls != 0 {
		t.Errorf("provider was called %d times, want 0", h.adult.calls)
	}
	if res.Sites != 0 || len(res.Errors) != 0 {
		t.Errorf("result = %+v, want an untouched no-op", res)
	}

	// And the rows are all still there: disabling hides, it never deletes.
	if eps := h.episodes(sr.ID); len(eps) != 3 {
		t.Errorf("episodes after disabling = %d, want 3", len(eps))
	}
}

func TestRefreshSitesRewalksTheCatalogue(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	sr, err := h.mgr.AddSiteAndWait(context.Background(), siteRef("site-1"), ptr(true), 0)
	if err != nil {
		t.Fatalf("AddSiteAndWait: %v", err)
	}

	h.adult.scenes["site-1"] = append(h.adult.scenes["site-1"], core.SceneMeta{
		StashID: "scene-new", SiteStashID: "site-1", SiteName: "Brazzers",
		Title: "Brand New", Date: date(2023, time.December, 1),
	})
	res := &RefreshResult{}
	if err := h.mgr.refreshSites(context.Background(), res); err != nil {
		t.Fatalf("refreshSites: %v", err)
	}
	if res.Sites != 1 {
		t.Errorf("refreshed %d sites, want 1", res.Sites)
	}
	if eps := h.episodes(sr.ID); len(eps) != 4 {
		t.Errorf("episodes = %d, want 4", len(eps))
	}
}

// A television refresh must not send a site to TMDB, whatever else changes.
func TestRefreshLibraryDoesNotSendSitesToTMDB(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	if _, err := h.mgr.AddSiteAndWait(context.Background(), siteRef("site-1"), ptr(true), 0); err != nil {
		t.Fatalf("AddSiteAndWait: %v", err)
	}

	// The stub answers no TMDB id, so a site reaching GetSeries would surface
	// as a refresh error rather than as silence.
	res, err := h.mgr.RefreshLibrary(context.Background())
	if err != nil {
		t.Fatalf("RefreshLibrary: %v", err)
	}
	if res.Series != 0 {
		t.Errorf("refreshed %d television series, want 0", res.Series)
	}
	if res.Sites != 1 {
		t.Errorf("refreshed %d sites, want 1", res.Sites)
	}
	if len(res.Errors) != 0 {
		t.Errorf("refresh errors = %v, want none", res.Errors)
	}
}

// ---- paging ---------------------------------------------------------------

// collectScenes runs a catalogue walk and returns everything it published. The
// walk hands scenes over a year at a time (see walkSiteScenes); the paging
// tests below care about what came back in total, not when.
func (a *adultHarness) collectScenes(id string) []core.SceneMeta {
	a.t.Helper()
	var out []core.SceneMeta
	err := a.mgr.walkSiteScenes(context.Background(), a.mgr.adult, id, func(batch []core.SceneMeta) error {
		out = append(out, batch...)
		return nil
	})
	if err != nil {
		a.t.Fatalf("walkSiteScenes: %v", err)
	}
	return out
}

func TestSiteScenesPagesTheWholeCatalogue(t *testing.T) {
	h := newAdultHarness(t, true)
	h.adult.sites = []core.SiteMeta{{StashID: "site-1", Name: "Brazzers"}}
	h.adult.pageSize = 2
	for i := range 7 {
		h.adult.scenes["site-1"] = append(h.adult.scenes["site-1"], core.SceneMeta{
			StashID: fmt.Sprintf("scene-%d", i),
			Date:    date(2022, time.January, i+1),
		})
	}

	if scenes := h.collectScenes("site-1"); len(scenes) != 7 {
		t.Errorf("scenes = %d, want 7", len(scenes))
	}
}

// A provider that answers the same page forever must not be walked forever,
// and must not multiply its catalogue into the numbering.
func TestSiteScenesSurvivesAProviderThatNeverAdvances(t *testing.T) {
	h := newAdultHarness(t, true)
	h.adult.scenes["site-1"] = []core.SceneMeta{
		{StashID: "scene-a", Date: date(2022, time.March, 14)},
		{StashID: "scene-b", Date: date(2022, time.March, 15)},
	}
	// Total says there is much more, and every page returns the same two.
	h.adult.pageSize = 2
	stuck := &stuckPager{inner: h.adult}
	h.mgr.adult = stuck

	if scenes := h.collectScenes("site-1"); len(scenes) != 2 {
		t.Errorf("scenes = %d, want 2 distinct", len(scenes))
	}
	if stuck.inner.calls > maxScenePages {
		t.Errorf("walked %d pages, want at most %d", stuck.inner.calls, maxScenePages)
	}
}

type stuckPager struct{ inner *stubAdultProvider }

func (s *stuckPager) SearchSites(ctx context.Context, q string) ([]core.SiteMeta, error) {
	return s.inner.SearchSites(ctx, q)
}
func (s *stuckPager) GetSite(ctx context.Context, id string) (*core.SiteMeta, error) {
	return s.inner.GetSite(ctx, id)
}
func (s *stuckPager) GetScene(ctx context.Context, id string) (*core.SceneMeta, error) {
	return s.inner.GetScene(ctx, id)
}
func (s *stuckPager) SearchPerformers(ctx context.Context, q string) ([]core.ScenePerformerMeta, error) {
	return s.inner.SearchPerformers(ctx, q)
}
func (s *stuckPager) SearchTags(ctx context.Context, q string) ([]core.SceneFilterRef, error) {
	return s.inner.SearchTags(ctx, q)
}
func (s *stuckPager) SearchScenes(ctx context.Context, q core.SceneQuery) (*core.ScenePage, error) {
	q.Page = 1
	page, err := s.inner.SearchScenes(ctx, q)
	if err != nil {
		return nil, err
	}
	page.Total = 1000
	return page, nil
}

// ---- scan and import ------------------------------------------------------

func TestScanImportsASceneUnderTheAdultRoot(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	h.writeVideo("library/Adult/Brazzers.22.03.14.Abella.Danger.Deep.Impact.XXX.1080p.MP4-KTR.mkv", "scene payload")

	res := h.scan()
	if res.Unmatched != 0 {
		t.Fatalf("scan parked %d files: %v", res.Unmatched, res.Errors)
	}
	if res.Added != 1 {
		t.Fatalf("added %d files, want 1 (errors: %v)", res.Added, res.Errors)
	}

	const want = "library/Adult/Brazzers/Season 2022/Brazzers - 2022-03-14 - Deep Impact.mkv"
	if !h.exists(want) {
		t.Fatalf("scene did not land at %s", want)
	}
	if h.read(want) != "scene payload" {
		t.Error("imported file does not hold the payload")
	}

	// The row exists, is linked to the right episode, and kept the parsed tags.
	file, err := h.st.GetMediaFileByPath(context.Background(), want)
	if err != nil {
		t.Fatalf("GetMediaFileByPath: %v", err)
	}
	if file.Quality != core.Quality1080p || file.ReleaseGroup != "KTR" {
		t.Errorf("media file = %+v, want 1080p/KTR from the scene name", file)
	}
	sites, err := h.st.ListSeriesByKind(context.Background(), core.SeriesKindAdult)
	if err != nil || len(sites) != 1 {
		t.Fatalf("adult series = %v, %v", sites, err)
	}
	eps := h.episodes(sites[0].ID)
	linked, err := h.st.ListMediaFilesForEpisode(context.Background(), eps[0].ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForEpisode: %v", err)
	}
	if len(linked) != 1 || linked[0].ID != file.ID {
		t.Errorf("episode links = %v, want the imported file", linked)
	}
}

// The disposability rule, for the adult library: delete the database, rescan,
// and the library comes back — same folder, same filename, same episode
// numbers, with no file modified.
func TestAdultLibrarySurvivesADatabaseWipe(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	h.writeVideo("library/Adult/Brazzers.22.03.14.Abella.Danger.Deep.Impact.XXX.1080p.MP4-KTR.mkv", "scene payload")
	h.scan()

	const organized = "library/Adult/Brazzers/Season 2022/Brazzers - 2022-03-14 - Deep Impact.mkv"
	if !h.exists(organized) {
		t.Fatalf("first scan did not organize the scene")
	}

	fresh := h.openStore(t.TempDir() + "/caravan.db")
	enableAdultLibrary(t, fresh)
	mgr := h.newManager(fresh, h.provider)

	res, err := mgr.Scan(context.Background())
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if res.Unmatched != 0 || res.Added != 1 {
		t.Fatalf("rescan: added=%d unmatched=%d errors=%v", res.Added, res.Unmatched, res.Errors)
	}
	if !h.exists(organized) {
		t.Errorf("rescan moved the already-organized file away from %s", organized)
	}

	sites, err := fresh.ListSeriesByKind(context.Background(), core.SeriesKindAdult)
	if err != nil || len(sites) != 1 {
		t.Fatalf("rebuilt adult series = %v, %v", sites, err)
	}
	if sites[0].StashID != "site-1" || sites[0].Path != "library/Adult/Brazzers" {
		t.Errorf("rebuilt site = %+v", sites[0])
	}
	eps, err := fresh.ListEpisodes(context.Background(), sites[0].ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(eps) != 3 {
		t.Errorf("rebuilt episodes = %d, want the whole catalogue (3)", len(eps))
	}
	file, err := fresh.GetMediaFileByPath(context.Background(), organized)
	if err != nil {
		t.Fatalf("rebuilt media file: %v", err)
	}
	if file.Path != organized {
		t.Errorf("rebuilt file path = %q", file.Path)
	}
}

// With the module off the adult tree is not walked at all — so nothing is
// imported, nothing is parked (a parked scene filename is a UI trace), and
// nothing already there is reconciled away.
func TestScanIgnoresTheAdultTreeWhenDisabled(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	h.writeVideo("library/Adult/Brazzers.22.03.14.Abella.Danger.Deep.Impact.XXX.1080p.MP4-KTR.mkv", "scene payload")
	h.scan()

	const organized = "library/Adult/Brazzers/Season 2022/Brazzers - 2022-03-14 - Deep Impact.mkv"
	setAdultLibrariesActive(t, h.st, false)
	h.writeVideo("library/Adult/Brazzers.23.02.01.Third.XXX.1080p.MP4-KTR.mkv", "another scene")
	h.adult.calls = 0

	res := h.scan()
	if h.adult.calls != 0 {
		t.Errorf("scan made %d provider calls with the module disabled, want 0", h.adult.calls)
	}
	if res.Scanned != 0 || res.Unmatched != 0 || res.Removed != 0 {
		t.Errorf("scan touched the adult tree: %+v", res)
	}
	if _, err := h.st.GetMediaFileByPath(context.Background(), organized); err != nil {
		t.Errorf("the disabled scan dropped an already-imported adult file: %v", err)
	}
	parked, err := h.st.ListUnmatchedFiles(context.Background())
	if err != nil {
		t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	if len(parked) != 0 {
		t.Errorf("disabled scan parked %v — that is a UI trace of a disabled module", parked)
	}
}

func TestScanParksASceneItCannotDate(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	h.writeVideo("library/Adult/Brazzers.Some.Compilation.XXX.1080p.MP4-KTR.mkv", "x")

	res := h.scan()
	if res.Unmatched != 1 {
		t.Fatalf("parked %d, want 1", res.Unmatched)
	}
	parked, err := h.st.ListUnmatchedFiles(context.Background())
	if err != nil {
		t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	if parked[0].Reason != reasonNoSceneDate {
		t.Errorf("park reason = %q, want %q", parked[0].Reason, reasonNoSceneDate)
	}
	if h.adult.calls != 0 {
		t.Errorf("an undatable file cost %d provider calls, want 0", h.adult.calls)
	}
}

func TestScanImportsASceneDatedOneDayOff(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	h.writeVideo("library/Adult/Brazzers.22.03.13.Abella.Danger.Deep.Impact.XXX.1080p.MP4-KTR.mkv", "scene payload")

	res := h.scan()
	if res.Unmatched != 0 {
		t.Fatalf("scan parked %d files: %v", res.Unmatched, res.Errors)
	}
	if res.Added != 1 {
		t.Fatalf("added %d files, want 1 (errors: %v)", res.Added, res.Errors)
	}

	const want = "library/Adult/Brazzers/Season 2022/Brazzers - 2022-03-14 - Deep Impact.mkv"
	if !h.exists(want) {
		t.Fatalf("one-day-off scene did not land at %s", want)
	}
}

func TestScanParksAnAmbiguousNearbyScene(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	h.adult.scenes["site-1"] = append(h.adult.scenes["site-1"], core.SceneMeta{
		StashID: "scene-before", SiteStashID: "site-1", SiteName: "Brazzers",
		Title: "Day Before", Date: date(2022, time.March, 12),
	})
	h.writeVideo("library/Adult/Brazzers.22.03.13.Whichever.XXX.1080p.MP4-KTR.mkv", "x")

	res := h.scan()
	if res.Unmatched != 1 {
		t.Fatalf("parked %d, want 1", res.Unmatched)
	}
	parked, _ := h.st.ListUnmatchedFiles(context.Background())
	if parked[0].Reason != reasonAmbiguousScene {
		t.Errorf("park reason = %q, want %q", parked[0].Reason, reasonAmbiguousScene)
	}
}

func TestScanParksASceneTheSiteNeverReleased(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	h.writeVideo("library/Adult/Brazzers.19.01.01.Unknown.XXX.1080p.MP4-KTR.mkv", "x")

	res := h.scan()
	if res.Unmatched != 1 {
		t.Fatalf("parked %d, want 1 (errors %v)", res.Unmatched, res.Errors)
	}
	parked, _ := h.st.ListUnmatchedFiles(context.Background())
	if parked[0].Reason != reasonNoSceneMatch {
		t.Errorf("park reason = %q, want %q", parked[0].Reason, reasonNoSceneMatch)
	}
}

// Two scenes on one day cannot be told apart by a filename, and guessing would
// import one as the other and then supersede the right one's file.
func TestScanParksAnAmbiguousSameDayScene(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	h.adult.scenes["site-1"] = append(h.adult.scenes["site-1"], core.SceneMeta{
		StashID: "scene-twin", SiteStashID: "site-1", SiteName: "Brazzers",
		Title: "Same Day", Date: date(2022, time.March, 14),
	})
	h.writeVideo("library/Adult/Brazzers.22.03.14.Whichever.XXX.1080p.MP4-KTR.mkv", "x")

	res := h.scan()
	if res.Unmatched != 1 {
		t.Fatalf("parked %d, want 1", res.Unmatched)
	}
	parked, _ := h.st.ListUnmatchedFiles(context.Background())
	if parked[0].Reason != reasonAmbiguousScene {
		t.Errorf("park reason = %q, want %q", parked[0].Reason, reasonAmbiguousScene)
	}
}

// A scan walks the catalogue once per site, not once per unresolvable file.
func TestScanWalksASiteCatalogueOncePerScan(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	for i := range 4 {
		h.writeVideo(fmt.Sprintf("library/Adult/Brazzers.19.01.0%d.Unknown.XXX.1080p.MP4-KTR.mkv", i+1), "x")
	}

	res := h.scan()
	if res.Unmatched != 4 {
		t.Fatalf("parked %d, want 4", res.Unmatched)
	}
	// SearchSites + SearchScenes pages for one site. Four files must not mean
	// four catalogue walks.
	if h.adult.calls > 3 {
		t.Errorf("provider calls = %d, want the one site walked once", h.adult.calls)
	}
}

// A television file must never be read with the scene parser, and a scene file
// must never be read with the television one. The section decides, and this is
// the test that says the section decides.
func TestTheSectionDecidesWhichParserReadsTheName(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()

	// The very same name in two places.
	const name = "Brazzers.22.03.14.Abella.Danger.Deep.Impact.XXX.1080p.MP4-KTR.mkv"
	h.parser[name] = episodeParse("Brazzers", 1, 4)
	h.provider.series = []core.SeriesMeta{{TMDBID: 77, Title: "Brazzers", Year: 2000}}
	h.provider.seriesByID[77] = core.SeriesMeta{
		TMDBID: 77, Title: "Brazzers", Year: 2000,
		Seasons: []core.SeasonMeta{{Number: 1, Episodes: []core.EpisodeMeta{{Season: 1, Number: 4, Title: "TV Four"}}}},
	}
	h.writeVideo("library/TV/"+name, "tv payload")
	h.writeVideo("library/Adult/"+name, "scene payload")

	h.scan()

	if !h.exists("library/TV/Brazzers (2000)/Season 01/Brazzers (2000) - S01E04 - TV Four.mkv") {
		t.Error("the file under TV/ was not read as a television episode")
	}
	if !h.exists("library/Adult/Brazzers/Season 2022/Brazzers - 2022-03-14 - Deep Impact.mkv") {
		t.Error("the file under Adult/ was not read as a scene")
	}
}

// stash-box models sub-studios and stashbox.SearchScenes asks with the INCLUDES
// modifier, so a parent site's catalogue legitimately contains its sub-sites'
// scenes and the two overlap. episodes.stash_id is globally unique
// (0013_adult.sql), so adding the second site must not try to write the shared
// scene again: that is a constraint violation, not a duplicate row, and it
// aborts the catalogue walk.
//
// The failure it guards is permanent and unrecoverable through the UI —
// upsertSiteRow has already written the second series before syncSiteScenes
// fails, so every retry of POST /adult/sites fails identically — and because
// numberScenes hands its output back oldest-first, the abort also stops the
// sub-site's OWN, non-conflicting scenes from ever being written.
func TestAddSiteToleratesASceneAnotherSiteAlreadyOwns(t *testing.T) {
	h := newAdultHarness(t, true)
	shared := core.SceneMeta{
		StashID: "scene-shared", SiteStashID: "parent", SiteName: "Bang Bros",
		Title: "Shared Scene", Date: date(2022, time.March, 14),
	}
	h.adult.sites = []core.SiteMeta{
		{StashID: "parent", Name: "Bang Bros"},
		{StashID: "sub", Name: "BangBros18"},
	}
	h.adult.scenes["parent"] = []core.SceneMeta{shared}
	// The sub-site's catalogue carries the shared scene AND one of its own.
	h.adult.scenes["sub"] = []core.SceneMeta{
		shared,
		{StashID: "scene-own", SiteStashID: "sub", SiteName: "BangBros18",
			Title: "Own Scene", Date: date(2022, time.May, 2)},
	}

	parent := h.addSite("parent")
	sub := h.addSite("sub")

	// The scene stays with the site that filed it first.
	parentEpisodes := h.episodes(parent.ID)
	if len(parentEpisodes) != 1 || parentEpisodes[0].StashID != "scene-shared" {
		t.Fatalf("parent site episodes = %+v, want the shared scene", parentEpisodes)
	}

	// And the sub-site keeps its own scene, which the aborted walk used to lose.
	subEpisodes := h.episodes(sub.ID)
	if len(subEpisodes) != 1 {
		t.Fatalf("sub-site episodes = %+v, want only its own scene", subEpisodes)
	}
	if subEpisodes[0].StashID != "scene-own" {
		t.Errorf("sub-site episode stash id = %q, want scene-own", subEpisodes[0].StashID)
	}
	if subEpisodes[0].SeasonNumber != 2022 || subEpisodes[0].EpisodeNumber != 1 {
		t.Errorf("sub-site scene numbered S%dE%d, want S2022E01",
			subEpisodes[0].SeasonNumber, subEpisodes[0].EpisodeNumber)
	}
}

// A refresh of the site that DOES own the scene keeps it: dropForeignScenes
// drops what another series owns, never what this one already holds.
func TestRefreshKeepsASiteOwnScenesWhenAnotherSiteAlsoListsThem(t *testing.T) {
	h := newAdultHarness(t, true)
	shared := core.SceneMeta{
		StashID: "scene-shared", SiteStashID: "parent", SiteName: "Bang Bros",
		Title: "Shared Scene", Date: date(2022, time.March, 14),
	}
	h.adult.sites = []core.SiteMeta{
		{StashID: "parent", Name: "Bang Bros"},
		{StashID: "sub", Name: "BangBros18"},
	}
	h.adult.scenes["parent"] = []core.SceneMeta{shared}
	h.adult.scenes["sub"] = []core.SceneMeta{shared}

	parent := h.addSite("parent")
	h.addSite("sub")

	// Re-adding the parent walks its catalogue again. Its own scene is still
	// its own, so it must survive a walk made after the sub-site listed it too.
	h.addSite("parent")
	parentEpisodes := h.episodes(parent.ID)
	if len(parentEpisodes) != 1 || parentEpisodes[0].StashID != "scene-shared" {
		t.Fatalf("parent site episodes after refresh = %+v, want the shared scene", parentEpisodes)
	}
}

func TestImportUnmatchedSceneAddsItsSiteUnmonitored(t *testing.T) {
	h := newAdultHarness(t, true)
	h.adult.sites = []core.SiteMeta{{StashID: "site-1", Name: "Brazzers"}}
	released := date(2022, time.March, 14)
	h.adult.scenes["site-1"] = []core.SceneMeta{
		{StashID: "scene-other", SiteStashID: "site-1", SiteName: "Brazzers",
			Title: "Other Scene", Date: released},
		{StashID: "scene-chosen", SiteStashID: "site-1", SiteName: "Brazzers",
			Title: "Chosen Scene", Date: released},
	}
	lib, err := h.st.GetDefaultLibrary(context.Background(), core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetDefaultLibrary(adult): %v", err)
	}
	const source = "incomplete/release/hash.mp4"
	h.writeVideo(source, "scene")
	parked := &core.UnmatchedFile{
		Path: source, Size: 5, LibraryID: lib.ID, Reason: reasonAmbiguousScene,
		Parsed: core.ParsedRelease{
			Title: "Brazzers", SceneDate: released, Quality: core.Quality1080p,
		},
	}
	if err := h.st.UpsertUnmatchedFile(context.Background(), parked); err != nil {
		t.Fatalf("UpsertUnmatchedFile: %v", err)
	}

	result, err := h.mgr.ImportUnmatched(context.Background(), parked.ID,
		core.ItemRef{Provider: core.ProviderStashbox, Ref: "scene-chosen"}, MediaTypeScene)
	if err != nil {
		t.Fatalf("ImportUnmatched(scene): %v", err)
	}
	const organized = "library/Adult/Brazzers/Season 2022/Brazzers - 2022-03-14 - Chosen Scene.mp4"
	if result.Path != organized || result.SeriesID == 0 {
		t.Errorf("result = %+v, want chosen scene at %q", result, organized)
	}
	site, err := h.st.GetSeries(context.Background(), result.SeriesID)
	if err != nil {
		t.Fatalf("GetSeries(matched site): %v", err)
	}
	if site.Monitored {
		t.Fatal("matching one scene added its new site as monitored")
	}
	if !h.exists(organized) {
		t.Errorf("chosen scene was not organized")
	}
	if h.exists("library/Adult/Brazzers/Season 2022/Brazzers - 2022-03-14 - Other Scene.mp4") {
		t.Errorf("same-day scene was chosen by date instead of explicit id")
	}
	if _, err := h.st.GetUnmatchedFile(context.Background(), parked.ID); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("parked row survived successful scene match: %v", err)
	}
}

// TestImportDownloadImportsObfuscatedSceneByReleaseTitle is the adult twin of
// TestImportDownloadImportsObfuscatedEpisodeByReleaseTitle: usenet posts
// obfuscate the payload's file name, the scene parser finds no date in the
// noise, and the grab's release title — which does carry the date — must vouch
// for the feature-sized file. Before the rescue existed this parked with
// reasonNoSceneDate despite the grab naming the exact scene.
func TestImportDownloadImportsObfuscatedSceneByReleaseTitle(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	sr := h.addSite("site-1")

	var target core.Episode
	for _, ep := range h.episodes(sr.ID) {
		if ep.Title == "Deep Impact" {
			target = ep
		}
	}
	if target.ID == 0 {
		t.Fatal("seed scene not found")
	}

	const release = "Brazzers.22.03.14.Abella.Danger.Deep.Impact.XXX.1080p.MP4-KTR"
	const dir = "incomplete/" + release
	const obfuscated = dir + "/Lf1duvkV19P1TaODMYsyktStVJoeGEUf.mp4"
	h.writeVideo(obfuscated, "the actual scene payload")
	// A smaller obfuscated extra: the release title must not vouch for it.
	h.writeVideo(dir+"/sample.mp4", "sample")

	grab := h.grabFor(core.GrabInfo{
		SeriesID:     sr.ID,
		EpisodeIDs:   []int64{target.ID},
		ReleaseTitle: release,
	})
	dl := core.DownloadStatus{ID: "u-scene", State: core.DownloadCompleted, SavePath: dir}

	ctx := context.Background()
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	const organized = "library/Adult/Brazzers/Season 2022/Brazzers - 2022-03-14 - Deep Impact.mp4"
	if !h.exists(organized) {
		t.Fatalf("obfuscated scene was not imported to %s", organized)
	}
	if !h.sameFile(obfuscated, organized) {
		t.Errorf("import did not use the feature-sized file")
	}
	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusImported {
		t.Errorf("grab status = %q, want %q", got, core.GrabStatusImported)
	}
	parked := h.unmatched()
	if len(parked) != 1 || !strings.Contains(parked[0].Path, "sample.mp4") {
		t.Errorf("unmatched queue = %+v, want only the obfuscated extra", parked)
	}
}

// The rescue must not fire when the grab's title resolves to a scene the grab
// does not cover: a wrong-scene import would silently satisfy a different
// wanted item.
func TestObfuscatedSceneStaysParkedWhenTheTitleNamesAnotherScene(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	sr := h.addSite("site-1")

	var second core.Episode
	for _, ep := range h.episodes(sr.ID) {
		if ep.Title == "Second" {
			second = ep
		}
	}

	// The grab targets "Second" (2022-06-09) but its title carries Deep
	// Impact's date.
	const release = "Brazzers.22.03.14.Abella.Danger.Deep.Impact.XXX.1080p.MP4-KTR"
	const dir = "incomplete/wrong-" + release
	h.writeVideo(dir+"/Zq8LmNoise.mp4", "payload")

	grab := h.grabFor(core.GrabInfo{
		SeriesID:     sr.ID,
		EpisodeIDs:   []int64{second.ID},
		ReleaseTitle: release,
	})
	dl := core.DownloadStatus{ID: "u-wrong", State: core.DownloadCompleted, SavePath: dir}

	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	parked := h.unmatched()
	if len(parked) != 1 {
		t.Fatalf("unmatched = %+v, want the payload parked", parked)
	}
	parsed := parked[0].Parsed
	if parsed.Title != "Brazzers" ||
		!parsed.SceneDate.Equal(date(2022, time.March, 14)) ||
		parsed.Quality != core.Quality1080p ||
		parsed.Group != "KTR" {
		t.Errorf("parked parser guess = %+v, want the grabbed release title parsed", parsed)
	}
	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusFailed {
		t.Errorf("grab status = %q, want %q", got, core.GrabStatusFailed)
	}
}

// ---- the deferred catalogue walk -------------------------------------------

// AddSite is the modal's path and answers after ONE provider round trip. The
// scenes arrive later, from the core.JobSyncSite the API queues; SyncSite is
// that job's body, and running it lands exactly what the old synchronous
// AddSite used to.
func TestAddSiteDefersTheCatalogueWalk(t *testing.T) {
	a := newAdultHarness(t, true)
	a.seedBrazzers()
	ctx := context.Background()

	sr, err := a.mgr.AddSite(ctx, siteRef("site-1"), nil, 0)
	if err != nil {
		t.Fatalf("AddSite: %v", err)
	}
	if sr.ID == 0 || sr.Title != "Brazzers" || sr.Kind != core.SeriesKindAdult {
		t.Fatalf("site row = %+v", sr)
	}
	// One call: GetSite. A page of SearchScenes would be the walk happening
	// inside the request, which is the whole thing this split removed.
	if a.adult.calls != 1 {
		t.Fatalf("AddSite made %d provider calls, want only the GetSite", a.adult.calls)
	}
	if eps := a.episodes(sr.ID); len(eps) != 0 {
		t.Fatalf("AddSite filed %d scenes, want the walk deferred: %+v", len(eps), eps)
	}

	if err := a.mgr.SyncSite(ctx, sr.ID); err != nil {
		t.Fatalf("SyncSite: %v", err)
	}
	eps := a.episodes(sr.ID)
	if len(eps) != 3 {
		t.Fatalf("SyncSite filed %d scenes, want the seeded 3: %+v", len(eps), eps)
	}
	seasons, err := a.st.ListSeasons(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListSeasons: %v", err)
	}
	if len(seasons) != 2 {
		t.Fatalf("years = %+v, want 2022 and 2023", seasons)
	}
}

// A second add while the first walk is still pending is a metadata refresh of
// the same row, not a second site. The queue's own dedupe is the API's half of
// this (see TestAddSiteQueuesTheCatalogueWalkOnce); this is the row's half.
func TestAddSiteTwiceIsOneSite(t *testing.T) {
	a := newAdultHarness(t, true)
	a.seedBrazzers()
	ctx := context.Background()

	first, err := a.mgr.AddSite(ctx, siteRef("site-1"), nil, 0)
	if err != nil {
		t.Fatalf("AddSite: %v", err)
	}
	second, err := a.mgr.AddSite(ctx, siteRef("site-1"), nil, 0)
	if err != nil {
		t.Fatalf("AddSite again: %v", err)
	}
	if first.ID != second.ID {
		t.Fatalf("a second add made site %d beside %d", second.ID, first.ID)
	}
	sites, err := a.st.ListSeriesByKind(ctx, core.SeriesKindAdult)
	if err != nil {
		t.Fatalf("ListSeriesByKind: %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("sites = %+v, want one row", sites)
	}
}

// AddSiteAndWait is what approving a scene request calls, and it must have the
// episode rows in place by the time it returns — the approval is granting one
// of them.
func TestAddSiteAndWaitLandsTheCatalogueBeforeReturning(t *testing.T) {
	a := newAdultHarness(t, true)
	a.seedBrazzers()

	sr, err := a.mgr.AddSiteAndWait(context.Background(), siteRef("site-1"), nil, 0)
	if err != nil {
		t.Fatalf("AddSiteAndWait: %v", err)
	}
	eps := a.episodes(sr.ID)
	if len(eps) != 3 {
		t.Fatalf("episodes = %+v, want the whole catalogue filed", eps)
	}
}

// A site the walk can no longer find is not a failure. The job outlives the
// request that made it, so the site can be deleted, or the module switched off,
// between the two — and a job that failed for either would retry forever.
func TestSyncSiteIsANoOpWhenThereIsNothingToWalk(t *testing.T) {
	a := newAdultHarness(t, true)
	a.seedBrazzers()
	ctx := context.Background()

	if err := a.mgr.SyncSite(ctx, 4242); err != nil {
		t.Errorf("SyncSite for an absent site = %v, want nil", err)
	}
	// A television series id is not a site, and walking it would file a stash
	// catalogue under a TMDB show.
	tv := &core.Series{TMDBID: 1396, Title: "Breaking Bad", SortTitle: "breaking bad", Monitored: true}
	if err := a.st.UpsertSeries(ctx, tv); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := a.mgr.SyncSite(ctx, tv.ID); err != nil {
		t.Errorf("SyncSite for a television series = %v, want nil", err)
	}
	if eps := a.episodes(tv.ID); len(eps) != 0 {
		t.Errorf("SyncSite filed %d scenes under a television series", len(eps))
	}

	sr, err := a.mgr.AddSite(ctx, siteRef("site-1"), nil, 0)
	if err != nil {
		t.Fatalf("AddSite: %v", err)
	}
	setAdultLibrariesActive(t, a.st, false)
	before := a.adult.calls
	if err := a.mgr.SyncSite(ctx, sr.ID); err != nil {
		t.Errorf("SyncSite with the module off = %v, want a silent no-op", err)
	}
	if a.adult.calls != before {
		t.Errorf("SyncSite reached the provider %d times with the module off", a.adult.calls-before)
	}
}

// An unmonitored site's scenes land unmonitored, so nothing it publishes is
// wanted. Without this the "Add and monitor" checkbox would be decorative: the
// wanted list reads episodes.monitored, not the site's.
func TestAddSiteUnmonitoredLeavesItsScenesUnmonitored(t *testing.T) {
	a := newAdultHarness(t, true)
	a.seedBrazzers()
	ctx := context.Background()

	sr, err := a.mgr.AddSiteAndWait(ctx, siteRef("site-1"), ptr(false), 0)
	if err != nil {
		t.Fatalf("AddSiteAndWait: %v", err)
	}
	if sr.Monitored {
		t.Fatal("the site row is monitored after an unmonitored add")
	}
	eps := a.episodes(sr.ID)
	if len(eps) != 3 {
		t.Fatalf("episodes = %+v, want the catalogue filed", eps)
	}
	for _, e := range eps {
		if e.Monitored {
			t.Errorf("scene %q is monitored under an unmonitored site", e.StashID)
		}
	}
	seasons, err := a.st.ListSeasons(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListSeasons: %v", err)
	}
	for _, se := range seasons {
		if se.Monitored {
			t.Errorf("year %d is monitored under an unmonitored site", se.Number)
		}
	}

	// Omission is also unmonitored: matching and other implicit additions do
	// not opt the user into automation.
	b := newAdultHarness(t, true)
	b.seedBrazzers()
	kept := b.addSite("site-1")
	if kept.Monitored {
		t.Fatal("an add with no opinion monitored the site")
	}
	for _, e := range b.episodes(kept.ID) {
		if e.Monitored {
			t.Errorf("scene %q is monitored without an explicit opt-in", e.StashID)
		}
	}
}

// ---- streaming: the catalogue appears while the walk is still running ------

// pageObserver wraps the stub provider and runs a hook just before answering
// each page, so a test can assert what the STORE held part-way through a walk.
// That is the only honest way to test streaming: the property is about what is
// visible before the walk returns, and after it returns everything is visible.
type pageObserver struct {
	inner  *stubAdultProvider
	before func(page int)
}

func (p *pageObserver) SearchSites(ctx context.Context, q string) ([]core.SiteMeta, error) {
	return p.inner.SearchSites(ctx, q)
}
func (p *pageObserver) GetSite(ctx context.Context, id string) (*core.SiteMeta, error) {
	return p.inner.GetSite(ctx, id)
}
func (p *pageObserver) GetScene(ctx context.Context, id string) (*core.SceneMeta, error) {
	return p.inner.GetScene(ctx, id)
}
func (p *pageObserver) SearchPerformers(ctx context.Context, q string) ([]core.ScenePerformerMeta, error) {
	return p.inner.SearchPerformers(ctx, q)
}
func (p *pageObserver) SearchTags(ctx context.Context, q string) ([]core.SceneFilterRef, error) {
	return p.inner.SearchTags(ctx, q)
}
func (p *pageObserver) SearchScenes(ctx context.Context, q core.SceneQuery) (*core.ScenePage, error) {
	if p.before != nil {
		p.before(max(q.Page, 1))
	}
	return p.inner.SearchScenes(ctx, q)
}

// seedThreeYears gives the provider a site with two scenes in each of three
// years, newest first — the order the DATE/DESC query actually returns.
func (a *adultHarness) seedThreeYears() {
	a.adult.sites = []core.SiteMeta{{StashID: "site-1", Name: "Brazzers"}}
	a.adult.scenes["site-1"] = []core.SceneMeta{
		{StashID: "s2024b", SiteStashID: "site-1", Title: "2024 late", Date: date(2024, time.November, 2)},
		{StashID: "s2024a", SiteStashID: "site-1", Title: "2024 early", Date: date(2024, time.February, 3)},
		{StashID: "s2023b", SiteStashID: "site-1", Title: "2023 late", Date: date(2023, time.October, 5)},
		{StashID: "s2023a", SiteStashID: "site-1", Title: "2023 early", Date: date(2023, time.January, 9)},
		{StashID: "s2022b", SiteStashID: "site-1", Title: "2022 late", Date: date(2022, time.August, 1)},
		{StashID: "s2022a", SiteStashID: "site-1", Title: "2022 early", Date: date(2022, time.April, 6)},
	}
	a.adult.pageSize = 2
}

func (a *adultHarness) stashIDsOf(seriesID int64) []string {
	a.t.Helper()
	out := []string{}
	for _, e := range a.episodes(seriesID) {
		out = append(out, e.StashID)
	}
	sort.Strings(out)
	return out
}

// The regression the whole restructuring exists for: a year's scenes are in the
// store BEFORE the walk that found the later years has returned.
//
// It fails against a walk that collects every page and writes once at the end —
// there, the store is empty at every page and only fills in afterwards.
func TestCatalogueWalkPublishesEachYearBeforeItFinishes(t *testing.T) {
	a := newAdultHarness(t, true)
	a.seedThreeYears()
	ctx := context.Background()

	sr, err := a.mgr.AddSite(ctx, siteRef("site-1"), nil, 0)
	if err != nil {
		t.Fatalf("AddSite: %v", err)
	}

	// What the store held when each page was asked for.
	atPage := map[int][]string{}
	a.mgr.adult = &pageObserver{inner: a.adult, before: func(page int) {
		atPage[page] = a.stashIDsOf(sr.ID)
	}}

	if err := a.mgr.SyncSite(ctx, sr.ID); err != nil {
		t.Fatalf("SyncSite: %v", err)
	}

	// Page 1 is asked for before anything can have been found.
	if got := atPage[1]; len(got) != 0 {
		t.Errorf("store held %v before the first page, want nothing", got)
	}
	// Page 1 returned only 2024 scenes, so 2024 is not yet known to be
	// complete and nothing may be numbered: the year is held, not written.
	if got := atPage[2]; len(got) != 0 {
		t.Errorf("at page 2 the store held %v, want the open year still held back", got)
	}
	// Page 2 returned 2023, which is what proves 2024 complete — so 2024 is
	// filed before page 3 is even requested. This is the streaming property:
	// a whole year is readable while the walk is still running.
	if got := fmt.Sprint(atPage[3]); got != "[s2024a s2024b]" {
		t.Errorf("at page 3 the store held %v, want 2024 already filed", atPage[3])
	}
	// And the finished walk has everything.
	if got := fmt.Sprint(a.stashIDsOf(sr.ID)); got != "[s2022a s2022b s2023a s2023b s2024a s2024b]" {
		t.Errorf("after the walk the store held %v, want the whole catalogue", a.stashIDsOf(sr.ID))
	}
}

// Streaming must not cost the numbering. A year is numbered oldest-first
// however many pages it arrived over, because it is held until complete — a
// naive page-by-page write would number the DESC-ordered newest scene as
// episode 1 and stand every year on its head.
func TestStreamedCatalogueStillNumbersEachYearOldestFirst(t *testing.T) {
	a := newAdultHarness(t, true)
	a.seedThreeYears()
	// One scene per page, so every year spans two pages and no year can be
	// numbered from a single response.
	a.adult.pageSize = 1

	sr := a.addSite("site-1")
	want := []struct {
		season, number int
		stashID        string
	}{
		{2022, 1, "s2022a"}, {2022, 2, "s2022b"},
		{2023, 1, "s2023a"}, {2023, 2, "s2023b"},
		{2024, 1, "s2024a"}, {2024, 2, "s2024b"},
	}
	got := a.episodes(sr.ID)
	if len(got) != len(want) {
		t.Fatalf("episodes = %+v, want %d", got, len(want))
	}
	for i, w := range want {
		if got[i].SeasonNumber != w.season || got[i].EpisodeNumber != w.number || got[i].StashID != w.stashID {
			t.Errorf("episode %d = S%dE%d %s, want S%dE%d %s", i,
				got[i].SeasonNumber, got[i].EpisodeNumber, got[i].StashID,
				w.season, w.number, w.stashID)
		}
	}
}

// The re-walk the refresh sweep makes goes through the same streaming walk, and
// still ends with today's semantics: a number already assigned is kept, and a
// scene the provider back-filled is appended after the year's highest.
func TestRewalkKeepsExistingNumbersAndAppendsBackfills(t *testing.T) {
	a := newAdultHarness(t, true)
	a.seedThreeYears()
	ctx := context.Background()

	sr := a.addSite("site-1")
	before := map[string]int{}
	for _, e := range a.episodes(sr.ID) {
		before[e.StashID] = e.EpisodeNumber
	}

	// The provider gains a 2023 scene older than both it already published —
	// the back-fill numberScenes' stability rule is written for.
	a.adult.scenes["site-1"] = append(a.adult.scenes["site-1"], core.SceneMeta{
		StashID: "s2023backfill", SiteStashID: "site-1", Title: "found later",
		Date: date(2023, time.January, 2),
	})
	if err := a.mgr.SyncSite(ctx, sr.ID); err != nil {
		t.Fatalf("SyncSite again: %v", err)
	}

	after := map[string]int{}
	for _, e := range a.episodes(sr.ID) {
		after[e.StashID] = e.EpisodeNumber
	}
	for id, number := range before {
		if after[id] != number {
			t.Errorf("scene %s renumbered %d -> %d on a re-walk", id, number, after[id])
		}
	}
	// Appended after the year's highest rather than inserted at its date.
	if after["s2023backfill"] != 3 {
		t.Errorf("back-filled scene numbered %d, want 3 (after the year's highest)",
			after["s2023backfill"])
	}
	if len(after) != 7 {
		t.Errorf("episodes = %d, want the original 6 plus the back-fill", len(after))
	}
}

// Monitor and search can land while a catalogue walk is still paging. Later
// years must follow the site flag as it is now, not the unmonitored pointer
// the walk captured when the job started.
func TestWriteScenesFollowsASiteMonitoredMidWalk(t *testing.T) {
	a := newAdultHarness(t, true)
	a.adult.sites = []core.SiteMeta{{StashID: "site-1", Name: "Studio"}}
	ctx := context.Background()

	off := false
	sr, err := a.mgr.AddSite(ctx, siteRef("site-1"), &off, 0)
	if err != nil {
		t.Fatalf("AddSite: %v", err)
	}

	first := []core.SceneMeta{{
		StashID: "s2022", SiteStashID: "site-1", Title: "First",
		Date: date(2022, time.March, 14),
	}}
	if err := a.mgr.writeScenes(ctx, sr, first); err != nil {
		t.Fatalf("writeScenes first year: %v", err)
	}

	updated := *sr
	updated.Monitored = true
	if err := a.st.UpsertSeries(ctx, &updated); err != nil {
		t.Fatalf("monitor site: %v", err)
	}
	if err := a.st.CascadeSeriesMonitored(ctx, sr.ID, true); err != nil {
		t.Fatalf("cascade monitored: %v", err)
	}
	if sr.Monitored {
		t.Fatal("the walk's series pointer must stay stale for this test")
	}

	second := []core.SceneMeta{{
		StashID: "s2023", SiteStashID: "site-1", Title: "Second",
		Date: date(2023, time.February, 1),
	}}
	if err := a.mgr.writeScenes(ctx, sr, second); err != nil {
		t.Fatalf("writeScenes second year: %v", err)
	}

	byID := map[string]core.Episode{}
	for _, episode := range a.episodes(sr.ID) {
		byID[episode.StashID] = episode
	}
	if got := byID["s2022"]; !got.Monitored {
		t.Errorf("s2022 monitored = false, want the cascade to have kept it on")
	}
	if got := byID["s2023"]; !got.Monitored {
		t.Errorf("s2023 monitored = false, want a new year to follow the site as it is now")
	}
}

// A walk that fails part-way keeps what it had already published. That is the
// point of writing additively: writeScenes only ever upserts, so an interrupted
// walk leaves a partial catalogue rather than none, and the job's retry fills
// in the rest.
func TestFailedWalkKeepsTheYearsItAlreadyPublished(t *testing.T) {
	a := newAdultHarness(t, true)
	a.seedThreeYears()
	ctx := context.Background()

	sr, err := a.mgr.AddSite(ctx, siteRef("site-1"), nil, 0)
	if err != nil {
		t.Fatalf("AddSite: %v", err)
	}
	// Fail once 2024 has been published — the third page, by which point page
	// two has proven 2024 complete.
	a.mgr.adult = &pageObserver{inner: a.adult, before: func(page int) {
		if page >= 3 {
			a.adult.searchErr = errors.New("provider went away")
		}
	}}
	if err := a.mgr.SyncSite(ctx, sr.ID); err == nil {
		t.Fatal("SyncSite succeeded against a provider that failed mid-walk")
	}
	if got := fmt.Sprint(a.stashIDsOf(sr.ID)); got != "[s2024a s2024b]" {
		t.Errorf("a failed walk left %v, want the year it had already published", a.stashIDsOf(sr.ID))
	}

	// And the retry completes it, with the published year's numbers intact.
	a.adult.searchErr = nil
	a.mgr.adult = a.adult
	if err := a.mgr.SyncSite(ctx, sr.ID); err != nil {
		t.Fatalf("SyncSite retry: %v", err)
	}
	if got := fmt.Sprint(a.stashIDsOf(sr.ID)); got != "[s2022a s2022b s2023a s2023b s2024a s2024b]" {
		t.Errorf("the retry left %v, want the whole catalogue", a.stashIDsOf(sr.ID))
	}
	for _, e := range a.episodes(sr.ID) {
		if e.StashID == "s2024a" && e.EpisodeNumber != 1 {
			t.Errorf("s2024a is episode %d after the retry, want 1", e.EpisodeNumber)
		}
	}
}

// The gate is the TARGET library's own switch, not the kind's. A second adult
// library still being on is not permission to add into a dormant one — that is
// exactly what the module-wide switch could not express, and getting it wrong
// would reach the endpoint on behalf of a shelf the owner turned off.
func TestAddSiteRefusesAnInactiveLibraryWhileASiblingIsOn(t *testing.T) {
	ctx := context.Background()
	a := newAdultHarness(t, true)
	a.seedBrazzers()

	second := &core.Library{Kind: core.LibraryKindAdult, Name: "Studios",
		RootPath: "library/Studios", Provider: core.ProviderStashbox}
	if err := a.st.CreateLibrary(ctx, second); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	if err := a.st.SetLibraryActive(ctx, second.ID, false); err != nil {
		t.Fatalf("SetLibraryActive: %v", err)
	}
	a.adult.calls = 0

	if _, err := a.mgr.AddSite(ctx, siteRef("site-1"), nil, second.ID); !errors.Is(err, ErrAdultDisabled) {
		t.Errorf("AddSite into an inactive library = %v, want ErrAdultDisabled", err)
	}
	if a.adult.calls != 0 {
		t.Errorf("the refused add made %d provider calls, want 0", a.adult.calls)
	}

	// The still-active default library is provably unaffected.
	if _, err := a.mgr.AddSite(ctx, siteRef("site-1"), nil, 0); err != nil {
		t.Fatalf("AddSite into the active library: %v", err)
	}
}

// The refresh sweep names no library, so its gate can only ask whether ANY
// adult shelf is on. A site under a dormant one is skipped individually, or the
// sweep would walk its catalogue because a sibling happened to be on.
func TestRefreshSitesSkipsSitesUnderAnInactiveLibrary(t *testing.T) {
	ctx := context.Background()
	a := newAdultHarness(t, true)
	a.seedBrazzers()
	sr := a.addSite("site-1")

	lib, err := a.st.GetLibrary(ctx, sr.LibraryID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	// A second, active adult library, so the sweep's own gate stays open.
	second := &core.Library{Kind: core.LibraryKindAdult, Name: "Studios",
		RootPath: "library/Studios", Provider: core.ProviderStashbox}
	if err := a.st.CreateLibrary(ctx, second); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	if err := a.st.SetLibraryActive(ctx, lib.ID, false); err != nil {
		t.Fatalf("SetLibraryActive: %v", err)
	}
	a.adult.calls = 0

	res := &RefreshResult{}
	if err := a.mgr.refreshSites(ctx, res); err != nil {
		t.Fatalf("refreshSites: %v", err)
	}
	if a.adult.calls != 0 {
		t.Errorf("the sweep made %d provider calls for a dormant library's site, want 0", a.adult.calls)
	}
	if res.Sites != 0 || len(res.Errors) != 0 {
		t.Errorf("result = %+v, want an untouched no-op", res)
	}
	// The queued catalogue walk agrees, so the other door onto the same rows is
	// shut too.
	if err := a.mgr.SyncSite(ctx, sr.ID); err != nil {
		t.Errorf("SyncSite under an inactive library = %v, want a silent no-op", err)
	}
	if a.adult.calls != 0 {
		t.Errorf("SyncSite reached the provider %d times for a dormant library, want 0", a.adult.calls)
	}
	// And the rows are all still there: switching off hides, it never deletes.
	if eps := a.episodes(sr.ID); len(eps) != 3 {
		t.Errorf("episodes after the switch = %d, want 3", len(eps))
	}
}
