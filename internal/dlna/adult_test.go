package dlna

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// enableAdultLibrary makes adult content reachable: the Adult library exists
// and is switched on. An adult library IS the module — there is no server-wide
// switch above it — so a test that needs scenes to be visible creates one.
//
// It is idempotent, because the enable it stands for was: a library that is
// already there is switched back on rather than duplicated, keeping whatever
// the owner has since done to it. Switching on is a whole-kind act for the same
// reason the off half is (see setAdultLibrariesActive), so a second adult
// library is switched on beside the seed one.
//
// The seed row is born with dlna_visible OFF and restricted ON. DLNA has no
// accounts, so a container advertised on the LAN is readable by every device on
// it, and sharing the shelf is a second, separate decision the owner makes; the
// restriction is the account-side half of the same caution. It is the kind's
// default only on the branch that creates it, because
// idx_libraries_default_per_kind allows exactly one and the row is never
// deleted, so a later enable would contend with the one it made itself.
func enableAdultLibrary(t *testing.T, st *store.Store) core.Library {
	t.Helper()
	ctx := context.Background()

	existing, err := st.ListLibrariesByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("ListLibrariesByKind(adult): %v", err)
	}
	if len(existing) == 0 {
		library := &core.Library{
			Kind:        core.LibraryKindAdult,
			Name:        store.AdultLibraryName,
			RootPath:    store.AdultLibraryRoot,
			Providers:   []string{core.ProviderStashbox},
			DLNAVisible: false,
			Restricted:  true,
			IsDefault:   true,
		}
		if err := st.CreateLibrary(ctx, library); err != nil {
			t.Fatalf("CreateLibrary(adult): %v", err)
		}
	}
	setAdultLibrariesActive(t, st, true)

	library, err := st.GetLibraryByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind(adult): %v", err)
	}
	return *library
}

// setAdultLibrariesActive switches every adult library at once, which is the
// only way to say "no adult content anywhere" now that the state lives per
// library: one library left on is one shelf still on the LAN, and a test that
// switched off only the default would be asserting against a server that still
// serves scenes.
//
// Switching off deletes nothing — not the row, not the series, not the files.
// Off is a visibility promise, not a retention policy, so each library keeps
// the dlna_visible its owner last chose and finds it again when it comes back.
func setAdultLibrariesActive(t *testing.T, st *store.Store, active bool) {
	t.Helper()
	ctx := context.Background()

	libraries, err := st.ListLibrariesByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("ListLibrariesByKind(adult): %v", err)
	}
	for _, library := range libraries {
		if err := st.SetLibraryActive(ctx, library.ID, active); err != nil {
			t.Fatalf("SetLibraryActive(%d, %v): %v", library.ID, active, err)
		}
	}
}

// seedSite puts a site in the library the way library.AddSite does — a series
// of kind adult with a release year for a season and one scene with a file on
// disk — plus the Adult library row it hangs under.
func seedSite(t *testing.T, st *store.Store) *core.MediaFile {
	t.Helper()
	ctx := context.Background()

	adult := enableAdultLibrary(t, st)
	series := &core.Series{
		StashID: "site-1", Title: "Brazzers", SortTitle: "brazzers",
		Kind: core.SeriesKindAdult, Monitored: true, Path: store.AdultLibraryRoot + "/Brazzers",
		LibraryID: adult.ID,
	}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := st.UpsertSeason(ctx, &core.Season{SeriesID: series.ID, Number: 2022, Title: "2022"}); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	episode := &core.Episode{
		SeriesID: series.ID, SeasonNumber: 2022, EpisodeNumber: 1, StashID: "scene-1",
		Title: "Deep Impact", AirDate: time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC),
		Monitored: true,
	}
	if err := st.UpsertEpisode(ctx, episode); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	file := &core.MediaFile{
		Path: store.AdultLibraryRoot + "/Brazzers/Season 2022/Brazzers - 2022-03-14 - Deep Impact.mkv",
		Size: 1 << 20,
	}
	if err := st.UpsertMediaFile(ctx, file); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	if err := st.LinkEpisodeFile(ctx, episode.ID, file.ID); err != nil {
		t.Fatalf("LinkEpisodeFile: %v", err)
	}
	return file
}

// A site is stored as a series row, so an unfiltered series listing would hang
// the adult library inside the TELEVISION container — where the adult library's
// own dlna_visible flag has no say, because it is not that library's container.
// DLNA has no accounts: anything in that tree is readable by every device on
// the network.
func TestDLNATVContainerNeverHoldsASite(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	seedSite(t, st)
	ctx := context.Background()

	tv, err := svc.children(ctx, testURLs, tvID)
	if err != nil {
		t.Fatalf("children(tv): %v", err)
	}
	if len(tv.Containers) != 1 {
		t.Fatalf("tv container = %+v, want only the television series", tv.Containers)
	}
	if strings.Contains(tv.Containers[0].Title, "Brazzers") {
		t.Fatalf("the TV container holds a site: %q", tv.Containers[0].Title)
	}

	// The root advertises the same count, so a television that caches the
	// child count is not told there is one more shelf than it can browse.
	root, err := svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	for _, ct := range root.Containers {
		if ct.ID == tvID && ct.ChildCount != 1 {
			t.Errorf("TV childCount = %d, want 1", ct.ChildCount)
		}
		if strings.Contains(strings.ToLower(ct.Title), "adult") {
			t.Errorf("the DLNA root advertises %q — the adult library is born hidden", ct.Title)
		}
	}
}

// Search is the other way into the tree, and a television that searches for a
// performer's name must not find one because the browse path was filtered and
// the search path was not.
func TestDLNASearchNeverReturnsAScene(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	seedSite(t, st)
	ctx := context.Background()

	for _, query := range []string{
		`dc:title contains "Brazzers"`,
		`dc:title contains "Deep Impact"`,
		`upnp:class derivedfrom "object.item.videoItem"`,
		`upnp:class derivedfrom "object.container"`,
	} {
		got, err := svc.search(ctx, testURLs, rootID, query)
		if err != nil {
			t.Fatalf("search(%s): %v", query, err)
		}
		for _, ct := range got.Containers {
			if strings.Contains(ct.Title, "Brazzers") {
				t.Errorf("search %s returned the site container %q", query, ct.Title)
			}
		}
		for _, it := range got.Items {
			if strings.Contains(it.Title, "Brazzers") || strings.Contains(it.Title, "Deep Impact") {
				t.Errorf("search %s returned the scene %q", query, it.Title)
			}
		}
	}
}

// The Adult library row is born with dlna_visible off. A fresh enable must not
// change what the DLNA tree advertises, which is what "a fresh enable of the
// adult module leaves DLNA exposure off" means.
func TestFreshAdultEnableLeavesTheAdultLibraryHiddenFromDLNA(t *testing.T) {
	_, st, _ := newTestService(t)
	ctx := context.Background()

	enableAdultLibrary(t, st)
	library, err := st.GetLibraryByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	if library.DLNAVisible {
		t.Error("a freshly created Adult library is advertised on the LAN")
	}
	if library.RootPath != store.AdultLibraryRoot {
		t.Errorf("adult root = %q, want %q", library.RootPath, store.AdultLibraryRoot)
	}
}

// showAdultOnDLNA flips the Adult library's dlna_visible, which is what the
// DLNA card's nested sub-toggle does through the phase-8 libraries PATCH.
func showAdultOnDLNA(t *testing.T, st *store.Store, visible bool) {
	t.Helper()
	ctx := context.Background()
	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	lib.DLNAVisible = visible
	if err := st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
}

func requestDirectMedia(t *testing.T, svc *Service, id int64) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(
		http.MethodGet,
		MountPath+"/media/"+strconv.FormatInt(id, 10)+".mkv",
		nil,
	)
	rec := httptest.NewRecorder()
	svc.Handler().ServeHTTP(rec, req)
	return rec
}

func TestDirectAdultMediaRequiresVisibleEnabledLibrary(t *testing.T) {
	svc, st, root := newTestService(t)
	file := seedSite(t, st)
	body := []byte("adult-media-bytes")
	writeMedia(t, root, file.Path, body)

	unknown := requestDirectMedia(t, svc, file.ID+9999)
	if unknown.Code != http.StatusNotFound {
		t.Fatalf("unknown media status = %d, want 404", unknown.Code)
	}
	rec := requestDirectMedia(t, svc, file.ID)
	if rec.Code != unknown.Code || rec.Body.String() != unknown.Body.String() {
		t.Fatalf(
			"hidden Adult media differs from unknown media: status = %d, body = %q",
			rec.Code,
			rec.Body.String(),
		)
	}

	showAdultOnDLNA(t, st, true)
	rec = requestDirectMedia(t, svc, file.ID)
	if rec.Code != http.StatusOK || rec.Body.String() != string(body) {
		t.Fatalf("visible Adult media: status = %d, body = %q", rec.Code, rec.Body.String())
	}

	setAdultLibrariesActive(t, st, false)
	rec = requestDirectMedia(t, svc, file.ID)
	if rec.Code != unknown.Code || rec.Body.String() != unknown.Body.String() {
		t.Fatalf(
			"disabled Adult media differs from unknown media: status = %d, body = %q",
			rec.Code,
			rec.Body.String(),
		)
	}
	library, err := st.GetLibraryByKind(t.Context(), core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	if !library.DLNAVisible {
		t.Fatal("disabling Adult forgot the remembered DLNA visibility toggle")
	}

	enableAdultLibrary(t, st)
	rec = requestDirectMedia(t, svc, file.ID)
	if rec.Code != http.StatusOK || rec.Body.String() != string(body) {
		t.Fatalf("re-enabled Adult media: status = %d, body = %q", rec.Code, rec.Body.String())
	}
}

func TestDirectOrdinaryMediaRemainsAvailable(t *testing.T) {
	svc, st, root := newTestService(t)
	body := []byte("ordinary-media-bytes")
	path := "Movies/ordinary.mkv"
	writeMedia(t, root, path, body)
	file := seedMovieMedia(t, st, path, int64(len(body)))

	rec := requestDirectMedia(t, svc, file.ID)
	if rec.Code != http.StatusOK || rec.Body.String() != string(body) {
		t.Fatalf("ordinary media: status = %d, body = %q", rec.Code, rec.Body.String())
	}
}

// containerTitles is the shelf names a DIDL document advertises.
func containerTitles(doc *didlLite) []string {
	out := make([]string, 0, len(doc.Containers))
	for _, c := range doc.Containers {
		out = append(out, c.Title)
	}
	return out
}

// The phase acceptance, both halves in one test: the Adult shelf is absent from
// a freshly enabled server, and it appears when — and only when — its own
// dlna_visible is turned on. Nothing here is adult-specific machinery: the same
// libraries row and the same flag decide the Movies and TV shelves.
func TestAdultShelfAppearsOnlyWhenItsLibraryIsVisible(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	seedSite(t, st)
	ctx := context.Background()

	root, err := svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	for _, c := range root.Containers {
		if c.ID == adultID {
			t.Fatalf("a fresh enable put %q on the DLNA root", c.Title)
		}
	}
	// And the shelf's own id resolves to nothing, so a client that guessed it
	// cannot browse past the root.
	if _, err := svc.children(ctx, testURLs, adultID); !errors.Is(err, errNoObject) {
		t.Fatalf("children(adult) while hidden = %v, want errNoObject", err)
	}

	showAdultOnDLNA(t, st, true)

	root, err = svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	var adultContainer *didlContainer
	for i := range root.Containers {
		if root.Containers[i].ID == adultID {
			adultContainer = &root.Containers[i]
		}
	}
	if adultContainer == nil {
		t.Fatalf("the Adult shelf is still absent after its toggle went on: %v", containerTitles(root))
	}
	if adultContainer.Title != "Adult" || adultContainer.ChildCount != 1 {
		t.Errorf("adult container = %+v, want one site", *adultContainer)
	}

	// The whole way down: shelf, site, year, scene.
	sites, err := svc.children(ctx, testURLs, adultID)
	if err != nil {
		t.Fatalf("children(adult): %v", err)
	}
	if len(sites.Containers) != 1 || sites.Containers[0].Title != "Brazzers" {
		t.Fatalf("adult children = %v", containerTitles(sites))
	}
	years, err := svc.children(ctx, testURLs, sites.Containers[0].ID)
	if err != nil {
		t.Fatalf("children(site): %v", err)
	}
	// A site's season number IS its release year, so "2022" and not "Season 2022".
	if len(years.Containers) != 1 || years.Containers[0].Title != "2022" {
		t.Fatalf("site children = %v, want the release year", containerTitles(years))
	}
	scenes, err := svc.children(ctx, testURLs, years.Containers[0].ID)
	if err != nil {
		t.Fatalf("children(year): %v", err)
	}
	if len(scenes.Items) != 1 || !strings.Contains(scenes.Items[0].Title, "2022-03-14") {
		t.Fatalf("year children = %+v, want the scene named by its release date", scenes.Items)
	}

	// Search agrees with Browse, which is the half most clients actually use.
	found, err := svc.search(ctx, testURLs, rootID, `dc:title contains "Brazzers"`)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(found.Containers) == 0 && len(found.Items) == 0 {
		t.Error("the visible Adult shelf is browsable but not searchable")
	}

	// And turning it back off takes it away again, ids and all.
	showAdultOnDLNA(t, st, false)
	if _, err := svc.children(ctx, testURLs, sites.Containers[0].ID); !errors.Is(err, errNoObject) {
		t.Errorf("a cached site id still resolves after the shelf was hidden: %v", err)
	}
	if _, err := svc.metadata(ctx, testURLs, scenes.Items[0].ID); !errors.Is(err, errNoObject) {
		t.Errorf("a cached scene id still resolves after the shelf was hidden: %v", err)
	}
}

// Switching the module off has to take the shelf off the LAN too.
//
// Disabling deliberately deletes nothing, so the Adult library row keeps
// whatever dlna_visible the owner last set — and DLNA is the one surface with
// no accounts on it. An owner who shares the shelf, then decides the module
// should be gone, sees the API, the SPA, the calendar and the wanted list all
// go quiet; if the tree kept advertising "Adult", every television in the house
// would still list it. The sharing decision has to be REMEMBERED (turning the
// module back on must not silently unshare it) but must not APPLY while the
// module is off.
func TestDisablingTheModuleTakesTheAdultShelfOffTheLAN(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	seedSite(t, st)
	showAdultOnDLNA(t, st, true)
	ctx := context.Background()

	// Precondition: it really is on the LAN before the switch is flipped, or
	// the assertions below would pass on a shelf that was never there.
	root, err := svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	sites, err := svc.children(ctx, testURLs, adultID)
	if err != nil {
		t.Fatalf("children(adult): %v", err)
	}
	if len(sites.Containers) != 1 {
		t.Fatalf("adult children = %v, want the site", containerTitles(sites))
	}
	siteID := sites.Containers[0].ID

	setAdultLibrariesActive(t, st, false)

	root, err = svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	for _, c := range root.Containers {
		if c.ID == adultID {
			t.Errorf("the DLNA root still advertises %q with the module off: %v",
				c.Title, containerTitles(root))
		}
	}
	// Browse by cached id, both levels.
	for _, objectID := range []string{adultID, siteID} {
		if _, err := svc.children(ctx, testURLs, objectID); !errors.Is(err, errNoObject) {
			t.Errorf("children(%s) with the module off = %v, want errNoObject", objectID, err)
		}
	}
	// And Search, which is the half most clients actually use.
	found, err := svc.search(ctx, testURLs, rootID, `dc:title contains "Brazzers"`)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, ct := range found.Containers {
		if strings.Contains(ct.Title, "Brazzers") {
			t.Errorf("search returned the site container %q with the module off", ct.Title)
		}
	}
	for _, it := range found.Items {
		if strings.Contains(it.Title, "Brazzers") || strings.Contains(it.Title, "Deep Impact") {
			t.Errorf("search returned the scene %q with the module off", it.Title)
		}
	}

	// Turning it back on finds the owner's sharing decision exactly as they
	// left it: the switch hides the shelf, it does not unshare it.
	enableAdultLibrary(t, st)
	root, err = svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	var back bool
	for _, c := range root.Containers {
		back = back || c.ID == adultID
	}
	if !back {
		t.Errorf("re-enabling the module forgot the shelf was shared: %v", containerTitles(root))
	}
}

// A site and a television series are rows in one table, so "s:12" and "as:12"
// address the same id. Each shelf must refuse the other's rows, or the
// television shelf's dlna_visible — which says nothing about the adult library
// — would become a way to reach it.
func TestTheTwoShelvesRefuseEachOthersRows(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	seedSite(t, st)
	showAdultOnDLNA(t, st, true)
	ctx := context.Background()

	sites, err := svc.st.ListSeriesByKind(ctx, core.SeriesKindAdult)
	if err != nil || len(sites) != 1 {
		t.Fatalf("ListSeriesByKind(adult) = %v, %v", sites, err)
	}
	shows, err := svc.st.ListSeriesByKind(ctx, core.SeriesKindTV)
	if err != nil || len(shows) != 1 {
		t.Fatalf("ListSeriesByKind(tv) = %v, %v", shows, err)
	}

	// The site through the television prefix, and the show through the adult
	// one. Both are "no such object".
	for _, objectID := range []string{
		tvIDSpace.seriesObjectID(sites[0].ID),
		tvIDSpace.seasonObjectID(sites[0].ID, 2022),
		adultIDSpace.seriesObjectID(shows[0].ID),
		adultIDSpace.seasonObjectID(shows[0].ID, 1),
	} {
		if _, err := svc.children(ctx, testURLs, objectID); !errors.Is(err, errNoObject) {
			t.Errorf("children(%s) = %v, want errNoObject", objectID, err)
		}
		if _, err := svc.metadata(ctx, testURLs, objectID); !errors.Is(err, errNoObject) {
			t.Errorf("metadata(%s) = %v, want errNoObject", objectID, err)
		}
	}
}

// The prefixes have to stay mutually exclusive, or shelfSpaceOf would answer
// for the wrong id space and every check above it would be reasoning about the
// wrong rows. Container ids are not in this test any more: which library a
// container id names is a row lookup now, not a prefix match.
func TestShelfPrefixesAreUnambiguous(t *testing.T) {
	for _, tc := range []struct {
		objectID string
		want     string
	}{
		{"s:1", core.SeriesKindTV},
		{"s:1:2", core.SeriesKindTV},
		{"e:1:2", core.SeriesKindTV},
		{"as:1", core.SeriesKindAdult},
		{"as:1:2022", core.SeriesKindAdult},
		{"ae:1:2", core.SeriesKindAdult},
	} {
		space, ok := shelfSpaceOf(tc.objectID)
		if !ok || space.seriesKind != tc.want {
			t.Errorf("shelfSpaceOf(%q) = %q,%v, want %q", tc.objectID, space.seriesKind, ok, tc.want)
		}
	}
	for _, objectID := range []string{rootID, moviesID, tvID, adultID, "lib:3", "m:1", "nonsense", ""} {
		if space, ok := shelfSpaceOf(objectID); ok {
			t.Errorf("shelfSpaceOf(%q) = %q, want no id space", objectID, space.seriesKind)
		}
	}
}
