package automation

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/wanted"
)

// addSite puts a site in the library the way library.AddSite does: a series of
// kind adult, its release year as the season, one scene as the episode.
func addSite(t *testing.T, ctx context.Context, st *store.Store, title string, released time.Time) (core.Series, core.Episode) {
	t.Helper()
	series := core.Series{
		StashID: "site-" + title, Title: title, SortTitle: strings.ToLower(title),
		Kind: core.SeriesKindAdult, Monitored: true,
	}
	if err := st.UpsertSeries(ctx, &series); err != nil {
		t.Fatalf("upsert site: %v", err)
	}
	episode := core.Episode{
		SeriesID: series.ID, SeasonNumber: released.Year(), EpisodeNumber: 1,
		StashID: "scene-" + title, Title: "A Scene", AirDate: released, Monitored: true,
	}
	if err := st.UpsertEpisode(ctx, &episode); err != nil {
		t.Fatalf("upsert scene: %v", err)
	}
	return series, episode
}

func enableAdult(t *testing.T, ctx context.Context, st *store.Store) {
	t.Helper()
	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
}

// A scene search must carry the ADULT library's categories and only those.
// This is the acceptance criterion "a scene release is found via a search that
// sends only 6000-series categories", asserted against the `cat` parameter that
// actually goes out on the wire.
func TestSceneSearchSendsOnlyTheAdultLibrarysCategories(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdult(t, ctx, st)

	fake := startFakeTorznab(t)
	cfg := addTorznabIndexer(t, ctx, st, fake, "shared", 5000, 6000)
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindTV, cfg.ID, true, []int{5000})
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindAdult, cfg.ID, true, []int{6000})

	_, scene := addSite(t, ctx, st, "Brazzers", time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC))
	episode := addEpisode(t, ctx, st, "Example Series")
	runner := NewRunner(st, fake.factory(), func(context.Context, int64, string) core.Engine { return &fakeEngine{} })

	searchEpisodeJob(t, ctx, runner, st, scene.ID)
	got := fake.recorded()
	// A scene search asks twice — by date, then by title — and the categories
	// are the point here: every one of them carries the adult library's and
	// only those.
	if len(got) == 0 {
		t.Fatal("scene search made no request")
	}
	for _, req := range got {
		if req.cats != "6000" {
			t.Fatalf("scene search sent cat=%q, want only the adult library's 6000", req.cats)
		}
		if strings.Contains(req.cats, "5000") {
			t.Fatalf("scene search leaked the TV categories: %s", formatRequests(got))
		}
	}

	// And the television search is unaffected by the adult library existing.
	fake.reset()
	searchEpisodeJob(t, ctx, runner, st, episode.ID)
	got = fake.recorded()
	if len(got) != 1 || got[0].cats != "5000" {
		t.Fatalf("television search made %s, want cat=5000", formatRequests(got))
	}
}

// With the module off, a queued scene search is dropped rather than run. That
// is the one path that can reach an indexer for a scene on a server whose owner
// has switched adult content off — a job enqueued before the switch was flipped.
func TestSceneSearchIsDroppedWhenTheModuleIsDisabled(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdult(t, ctx, st)

	fake := startFakeTorznab(t)
	cfg := addTorznabIndexer(t, ctx, st, fake, "shared", 6000)
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindAdult, cfg.ID, true, []int{6000})
	_, scene := addSite(t, ctx, st, "Brazzers", time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC))

	if err := st.SetAdultEnabled(ctx, false); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	runner := NewRunner(st, fake.factory(), func(context.Context, int64, string) core.Engine { return &fakeEngine{} })
	searchEpisodeJob(t, ctx, runner, st, scene.ID)

	if got := fake.recorded(); len(got) != 0 {
		t.Fatalf("a disabled module still searched: %s", formatRequests(got))
	}
}

// The wanted list is what the backlog sweep and the RSS matcher both read, so
// an adult item that never enters it cannot leak out of either.
func TestDisabledAdultItemsNeverEnterTheWantedList(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdult(t, ctx, st)
	addSite(t, ctx, st, "Brazzers", time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC))
	addEpisode(t, ctx, st, "Example Series")

	lists, err := wanted.Compute(ctx, st)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(lists.Episodes) != 2 {
		t.Fatalf("wanted episodes with the module on = %d, want the scene and the episode", len(lists.Episodes))
	}

	if err := st.SetAdultEnabled(ctx, false); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	lists, err = wanted.Compute(ctx, st)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(lists.Episodes) != 1 {
		t.Fatalf("wanted episodes with the module off = %d, want only the television one", len(lists.Episodes))
	}
	if lists.Episodes[0].SeriesKind == core.SeriesKindAdult {
		t.Errorf("the wanted list still carries a scene with the module disabled")
	}
}

// The backlog sweep enqueues one search job per wanted item. With the module
// off there is no scene among them, so nothing is ever scheduled that could
// reach an indexer or the stash-box endpoint on a scene's behalf.
func TestBacklogSweepEnqueuesNoSceneSearchWhenDisabled(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdult(t, ctx, st)
	_, scene := addSite(t, ctx, st, "Brazzers", time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC))
	episode := addEpisode(t, ctx, st, "Example Series")
	if err := st.SetAdultEnabled(ctx, false); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}

	runner := NewRunner(st, nil, nil)
	if err := runner.handleBacklogSweep(ctx, st, json.RawMessage("{}")); err != nil {
		t.Fatalf("handle backlog sweep: %v", err)
	}

	scenePayload, _ := json.Marshal(core.JobSearchEpisodePayload{EpisodeID: scene.ID})
	open, err := st.HasOpenJob(ctx, core.JobSearchEpisode, string(scenePayload))
	if err != nil {
		t.Fatalf("HasOpenJob: %v", err)
	}
	if open {
		t.Error("the backlog sweep queued a scene search with the module disabled")
	}

	episodePayload, _ := json.Marshal(core.JobSearchEpisodePayload{EpisodeID: episode.ID})
	open, err = st.HasOpenJob(ctx, core.JobSearchEpisode, string(episodePayload))
	if err != nil {
		t.Fatalf("HasOpenJob: %v", err)
	}
	if !open {
		t.Error("the television backlog stopped working")
	}
}

// A scene release is matched by DATE, not by season and episode numbers: those
// two are Caravan's own mapping and no indexer publishes them.
func TestRSSMatchesScenesByReleaseDate(t *testing.T) {
	released := time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC)
	target := wanted.Episode{
		Episode:     core.Episode{SeasonNumber: 2022, EpisodeNumber: 1, AirDate: released},
		SeriesTitle: "Brazzers",
		SeriesKind:  core.SeriesKindAdult,
	}

	match := core.Release{Parsed: core.ParsedRelease{
		Title: "Brazzers", Year: 2022, Season: 2022, SceneDate: released,
	}}
	if !matchesRSSEpisode(match, target) {
		t.Error("a scene released on the target's date did not match")
	}

	// A different day is a different scene, even though the site and the year
	// — and therefore the season — are identical.
	otherDay := core.Release{Parsed: core.ParsedRelease{
		Title: "Brazzers", Year: 2022, Season: 2022,
		SceneDate: released.AddDate(0, 0, 1),
	}}
	if matchesRSSEpisode(otherDay, target) {
		t.Error("a scene from another day matched")
	}

	// An episode-shaped release for the same site is not a scene at all. Its
	// S2022E01 would otherwise pass the television rule exactly.
	episodeShaped := core.Release{Parsed: core.ParsedRelease{
		Title: "Brazzers", Season: 2022, Episodes: []int{1},
	}}
	if matchesRSSEpisode(episodeShaped, target) {
		t.Error("a season/episode release matched a scene, which no indexer publishes")
	}
}

// The reverse direction: a scene release must never satisfy a television item.
func TestRSSDoesNotOfferASceneReleaseToATelevisionEpisode(t *testing.T) {
	target := wanted.Episode{
		Episode:     core.Episode{SeasonNumber: 1, EpisodeNumber: 1},
		SeriesTitle: "Example Series",
		SeriesKind:  core.SeriesKindTV,
	}
	scene := core.Release{Parsed: core.ParsedRelease{
		Title:     "Example Series",
		SceneDate: time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC),
		Season:    2022,
	}}
	if matchesRSSEpisode(scene, target) {
		t.Error("a dated scene release matched a television episode")
	}
}

// Out of the box there is no per-library category override: enabling the module
// creates the Adult library row and nothing else. The search must still send
// 6000, because inheriting the indexer's own categories would send the movie
// and television ones — and that fails SILENTLY. indexer.parseTitle picks the
// date-based scene parser only for a 6000-series result, so everything a
// 5000/2000 search returns parses with a zero scene date and is dropped by
// searchScene's date match: the job records "no release found" forever, on an
// indexer that carries the scene.
func TestSceneSearchSendsAdultCategoriesWithNoOverrideConfigured(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdult(t, ctx, st)

	fake := startFakeTorznab(t)
	// The indexer's own categories, exactly as an install that has only ever
	// tracked movies and television would have configured them. No
	// overrideLibraryIndexer call: that row is what nothing creates.
	addTorznabIndexer(t, ctx, st, fake, "shared", 5000, 2000)

	_, scene := addSite(t, ctx, st, "Brazzers", time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC))
	runner := NewRunner(st, fake.factory(), func(context.Context, int64, string) core.Engine { return &fakeEngine{} })

	searchEpisodeJob(t, ctx, runner, st, scene.ID)
	got := fake.recorded()
	if len(got) == 0 {
		t.Fatal("scene search made no request")
	}
	for _, req := range got {
		if req.cats != "6000" {
			t.Fatalf("scene search sent cat=%q, want the adult block", formatRequests(got))
		}
	}
}

// An indexer already narrowed to specific adult subcategories keeps exactly
// those: its owner named which flavours of XXX this install wants, and widening
// them back out to the 6000 parent would undo that.
func TestSceneSearchKeepsTheIndexersOwnAdultSubcategories(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdult(t, ctx, st)

	fake := startFakeTorznab(t)
	addTorznabIndexer(t, ctx, st, fake, "shared", 5000, 6040, 6090)

	_, scene := addSite(t, ctx, st, "Brazzers", time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC))
	runner := NewRunner(st, fake.factory(), func(context.Context, int64, string) core.Engine { return &fakeEngine{} })

	searchEpisodeJob(t, ctx, runner, st, scene.ID)
	got := fake.recorded()
	if len(got) == 0 {
		t.Fatal("scene search made no request")
	}
	for _, req := range got {
		if req.cats != "6040,6090" {
			t.Fatalf("scene search made %s, want the indexer's own adult subcategories", formatRequests(got))
		}
	}
}

// Disabling the module does not delete the Adult library row — that is
// deliberate, so re-enabling finds the library as it was left. What it must not
// leave behind is 6000 in the query string of every RSS poll, once per sync
// interval, forever: that is a durable trace of a module the phase promises is
// absent, visible in the indexer's own request log, and a wider fetch than the
// enabled libraries asked for.
func TestRSSSyncDropsTheAdultLibraryWhenTheModuleIsDisabled(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdult(t, ctx, st)

	fake := startFakeTorznab(t)
	cfg := addTorznabIndexer(t, ctx, st, fake, "shared", 5000, 6000)
	// Every library is given an override, so the union under test is exactly
	// what the three libraries asked for and nothing is inherited.
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindMovie, cfg.ID, true, []int{2000})
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindTV, cfg.ID, true, []int{5000})
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindAdult, cfg.ID, true, []int{6000})
	runner := NewRunner(st, fake.factory(), func(context.Context, int64, string) core.Engine { return &fakeEngine{} })

	// While it is on, the adult library is a subscriber like any other.
	if err := runner.handleRSSSync(ctx, st, json.RawMessage("{}")); err != nil {
		t.Fatalf("handle rss sync: %v", err)
	}
	if got := fake.recorded(); len(got) != 1 || got[0].cats != "2000,5000,6000" {
		t.Fatalf("rss fetch with the module on = %s, want the union", formatRequests(got))
	}

	if err := st.SetAdultEnabled(ctx, false); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	fake.reset()
	if err := runner.handleRSSSync(ctx, st, json.RawMessage("{}")); err != nil {
		t.Fatalf("handle rss sync: %v", err)
	}
	got := fake.recorded()
	if len(got) != 1 {
		t.Fatalf("rss cycle made %s, want one fetch", formatRequests(got))
	}
	if got[0].cats != "2000,5000" {
		t.Fatalf("rss fetch with the module off = %s, want only the enabled libraries' categories", formatRequests(got))
	}
	if strings.Contains(got[0].cats, "6000") {
		t.Errorf("a disabled module still asks every indexer for 6000: %s", formatRequests(got))
	}
}

// addSceneWithTitle puts a site and one scene with a known title and cast in
// the library, which is what the title-variant search and its matcher read.
func addSceneWithTitle(t *testing.T, ctx context.Context, st *store.Store, site, title string, released time.Time, performers ...string) (core.Series, core.Episode) {
	t.Helper()
	series := core.Series{
		StashID: "site-" + site, Title: site, SortTitle: strings.ToLower(site),
		Kind: core.SeriesKindAdult, Monitored: true,
	}
	if err := st.UpsertSeries(ctx, &series); err != nil {
		t.Fatalf("upsert site: %v", err)
	}
	episode := core.Episode{
		SeriesID: series.ID, SeasonNumber: released.Year(), EpisodeNumber: 1,
		StashID: "scene-" + title, Title: title, AirDate: released, Monitored: true,
		Scene: &core.SceneInfo{Studio: site, Performers: performers},
	}
	if err := st.UpsertEpisode(ctx, &episode); err != nil {
		t.Fatalf("upsert scene: %v", err)
	}
	return series, episode
}

// grabbedTitles are the releases the runner actually grabbed.
func grabbedTitles(t *testing.T, ctx context.Context, st *store.Store) []string {
	t.Helper()
	grabs, err := st.ListGrabs(ctx, 50)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	out := []string{}
	for _, grab := range grabs {
		if grab.Status == core.GrabStatusGrabbed {
			out = append(out, grab.ReleaseTitle)
		}
	}
	return out
}

// The date query is the one that finds a scene release named the standard way,
// and when it does there is no reason to ask anything else.
func TestSceneSearchStopsAtTheDateVariantWhenItFinds(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdult(t, ctx, st)

	fake := startFakeTorznab(t)
	cfg := addTorznabIndexer(t, ctx, st, fake, "shared", 6000)
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindAdult, cfg.ID, true, []int{6000})

	released := time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC)
	_, scene := addSceneWithTitle(t, ctx, st, "Brazzers", "Deep Impact", released, "Abella Danger")
	fake.serves("Brazzers 22.03.14", torznabItem{
		title: "Brazzers.22.03.14.Abella.Danger.Deep.Impact.XXX.1080p.MP4-KTR",
		guid:  "by-date",
	})

	runner := NewRunner(st, fake.factory(), func(context.Context, int64, string) core.Engine { return &fakeEngine{} })
	searchEpisodeJob(t, ctx, runner, st, scene.ID)

	if got := fake.queries(); len(got) != 1 || got[0] != "Brazzers 22.03.14" {
		t.Fatalf("queries = %v, want only the date variant", got)
	}
	if got := grabbedTitles(t, ctx, st); len(got) != 1 || !strings.Contains(got[0], "22.03.14") {
		t.Fatalf("grabbed %v, want the date-named release", got)
	}
}

// The improvement Whisparr does not have (its issue #115): when the date query
// comes back with nothing grabbable, ask again by title. A release named after
// its title and performers is invisible to the first query no matter how good
// the matching is.
func TestSceneSearchFallsBackToTheTitleVariant(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdult(t, ctx, st)

	fake := startFakeTorznab(t)
	cfg := addTorznabIndexer(t, ctx, st, fake, "shared", 6000)
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindAdult, cfg.ID, true, []int{6000})

	released := time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC)
	_, scene := addSceneWithTitle(t, ctx, st, "Brazzers", "Deep Impact", released, "Abella Danger")
	// Nothing under the date query; the release exists, but its packager named
	// it after the scene instead.
	fake.serves("Brazzers Deep Impact", torznabItem{
		title: "Brazzers.Deep.Impact.Abella.Danger.XXX.1080p.HEVC-GROUP",
		guid:  "by-title",
	})

	runner := NewRunner(st, fake.factory(), func(context.Context, int64, string) core.Engine { return &fakeEngine{} })
	searchEpisodeJob(t, ctx, runner, st, scene.ID)

	want := []string{"Brazzers 22.03.14", "Brazzers Deep Impact"}
	if got := fake.queries(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("queries = %v, want %v in that order", got, want)
	}
	if got := grabbedTitles(t, ctx, st); len(got) != 1 || !strings.Contains(got[0], "Deep.Impact") {
		t.Fatalf("grabbed %v, want the title-named release", got)
	}
}

// A scene nobody released is still two searches, and the record says which were
// tried — "no release" means something different depending on the question.
func TestSceneSearchRecordsWhichVariantsItTried(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdult(t, ctx, st)

	fake := startFakeTorznab(t)
	cfg := addTorznabIndexer(t, ctx, st, fake, "shared", 6000)
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindAdult, cfg.ID, true, []int{6000})

	released := time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC)
	_, scene := addSceneWithTitle(t, ctx, st, "Brazzers", "Deep Impact", released)

	runner := NewRunner(st, fake.factory(), func(context.Context, int64, string) core.Engine { return &fakeEngine{} })
	searchEpisodeJob(t, ctx, runner, st, scene.ID)

	if got := fake.queries(); len(got) != 2 {
		t.Fatalf("queries = %v, want both variants tried", got)
	}
	events, err := st.ListEvents(ctx, 50)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	var message string
	for _, event := range events {
		if strings.Contains(event.Message, "no acceptable release") {
			message = event.Message
		}
	}
	if message == "" {
		t.Fatal("no no-release event was recorded")
	}
	if !strings.Contains(message, "tried date, title") {
		t.Errorf("event = %q, want it to name the variants that were tried", message)
	}
}

// A scene with no date is still searchable by title — and a scene with neither
// is not searchable at all, which stays a silent no-op rather than a query for
// the whole site.
func TestSceneSearchWithoutADateUsesTheTitleAlone(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdult(t, ctx, st)

	fake := startFakeTorznab(t)
	cfg := addTorznabIndexer(t, ctx, st, fake, "shared", 6000)
	overrideLibraryIndexer(t, ctx, st, core.LibraryKindAdult, cfg.ID, true, []int{6000})

	_, dated := addSceneWithTitle(t, ctx, st, "Brazzers", "Deep Impact", time.Time{})
	runner := NewRunner(st, fake.factory(), func(context.Context, int64, string) core.Engine { return &fakeEngine{} })
	searchEpisodeJob(t, ctx, runner, st, dated.ID)

	if got := fake.queries(); len(got) != 1 || got[0] != "Brazzers Deep Impact" {
		t.Fatalf("queries = %v, want the title variant alone", got)
	}

	fake.reset()
	_, untitled := addSceneWithTitle(t, ctx, st, "Vixen", "", time.Time{})
	searchEpisodeJob(t, ctx, runner, st, untitled.ID)
	if got := fake.queries(); len(got) != 0 {
		t.Fatalf("queries = %v, want none: there is nothing to search for", got)
	}
}

// The conservative matcher, which is what makes the title variant safe to have
// at all. A false grab is worse than a miss: a wrong scene under a right
// scene's name is a file somebody has to find and delete, and the library will
// believe it is complete.
func TestMatchesSceneTitle(t *testing.T) {
	released := time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC)
	series := core.Series{Title: "Brazzers"}
	scene := core.Episode{
		Title:   "Deep Impact",
		AirDate: released,
		Scene:   &core.SceneInfo{Performers: []string{"Abella Danger"}},
	}

	tests := []struct {
		name    string
		release string
		parsed  core.ParsedRelease
		series  core.Series
		episode core.Episode
		want    bool
	}{
		{
			name:    "site and title, welded the way release names are",
			release: "Brazzers.Deep.Impact.Abella.Danger.XXX.1080p.HEVC-GROUP",
			want:    true,
		},
		{
			name:    "the site written as one word still matches",
			release: "RealityKings.Deep.Impact.XXX.1080p",
			series:  core.Series{Title: "Reality Kings"},
			want:    true,
		},
		{
			name:    "a title match with the scene's own date is still a match",
			release: "Brazzers.22.03.14.Deep.Impact.XXX.1080p",
			parsed:  core.ParsedRelease{SceneDate: released},
			want:    true,
		},
		{
			// The rule that matters most: words can line up and the release
			// still be a different scene, and the date says so.
			name:    "a contradicting date beats any title match",
			release: "Brazzers.22.09.01.Deep.Impact.XXX.1080p",
			parsed: core.ParsedRelease{
				SceneDate: time.Date(2022, time.September, 1, 0, 0, 0, 0, time.UTC),
			},
			want: false,
		},
		{
			name:    "another site's release with the same title is refused",
			release: "Tushy.Deep.Impact.XXX.1080p",
			want:    false,
		},
		{
			name:    "a different scene from the right site is refused",
			release: "Brazzers.Shallow.Waters.XXX.1080p",
			want:    false,
		},
		{
			name:    "the title's words must be together, not merely present",
			release: "Brazzers.Deep.In.The.Impact.Zone.XXX.1080p",
			want:    false,
		},
		{
			// A one-word title matches half a catalogue on its own, so it needs
			// a performer named in the release as well.
			name:    "a one-word title needs a performer beside it",
			release: "Brazzers.Impact.XXX.1080p",
			episode: core.Episode{Title: "Impact", AirDate: released, Scene: scene.Scene},
			want:    false,
		},
		{
			name:    "a one-word title with the performer is accepted",
			release: "Brazzers.Impact.Abella.Danger.XXX.1080p",
			episode: core.Episode{Title: "Impact", AirDate: released, Scene: scene.Scene},
			want:    true,
		},
		{
			name:    "a scene with no title matches nothing",
			release: "Brazzers.Something.XXX.1080p",
			episode: core.Episode{AirDate: released},
			want:    false,
		},
		{
			name:    "an empty release name matches nothing",
			release: "",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sr := tt.series
			if sr.Title == "" {
				sr = series
			}
			ep := tt.episode
			if ep.Title == "" && ep.AirDate.IsZero() {
				ep = scene
			}
			release := core.Release{Title: tt.release, Parsed: tt.parsed}
			if got := matchesSceneTitle(release, sr, ep); got != tt.want {
				t.Errorf("matchesSceneTitle(%q) = %v, want %v", tt.release, got, tt.want)
			}
		})
	}
}
