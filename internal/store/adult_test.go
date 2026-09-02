package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// enableAdultLibrary says directly what a module-wide enable used to say for a
// test: the Adult library exists and is switched on. An adult library is the
// module now (there is no server-wide flag left to set) so a test that wants
// adult content reachable creates one, exactly as the create form does.
//
// It is idempotent the way the enable it replaces was: an existing row is
// switched back on rather than duplicated, because the kind admits several
// libraries and a second Adult shelf appearing mid-test would be a fixture bug
// that reads as a product one.
//
// The created row carries the seed values the module always gave it: hidden
// from DLNA and restricted, because a shelf of scenes was only ever reachable
// by a named account and the LAN has no accounts to name. IsDefault is true
// only because this branch runs when no adult library exists at all, and the
// partial unique index admits exactly one default per kind.
func enableAdultLibrary(t *testing.T, s *Store) core.Library {
	t.Helper()
	ctx := context.Background()

	lib, err := s.GetLibraryByKind(ctx, core.LibraryKindAdult)
	if errors.Is(err, ErrNotFound) {
		lib = &core.Library{
			Kind: core.LibraryKindAdult, Name: AdultLibraryName, RootPath: AdultLibraryRoot,
			Providers: []string{core.ProviderStashbox}, DLNAVisible: false,
			Restricted: true, IsDefault: true,
		}
		if err := s.CreateLibrary(ctx, lib); err != nil {
			t.Fatalf("CreateLibrary(adult): %v", err)
		}
		return *lib
	}
	if err != nil {
		t.Fatalf("GetLibraryByKind(adult): %v", err)
	}
	if err := s.SetLibraryActive(ctx, lib.ID, true); err != nil {
		t.Fatalf("SetLibraryActive(%d, true): %v", lib.ID, err)
	}
	lib.Active = true
	return *lib
}

// setAdultLibrariesActive is the other half of what the module switch did: it
// bound every adult library at once, and that is now spelled per library.
//
// Off is a visibility answer and never a retention one, which is why this walks
// the rows rather than deleting them. A test that turns the module off and
// expects its scenes back is testing the promise, not the fixture.
func setAdultLibrariesActive(t *testing.T, s *Store, active bool) {
	t.Helper()
	ctx := context.Background()

	libs, err := s.ListLibrariesByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("ListLibrariesByKind(adult): %v", err)
	}
	for _, lib := range libs {
		if err := s.SetLibraryActive(ctx, lib.ID, active); err != nil {
			t.Fatalf("SetLibraryActive(%d, %t): %v", lib.ID, active, err)
		}
	}
}

// The baseline must leave every foreign key sound and keep the cascades its
// runtime behavior relies on. This asserts the resulting schema as well as the
// ordinary store operations that pass through it.
func TestBaselineLeavesTheForeignKeysSound(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	rows, err := st.DB().QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}
	defer rows.Close()
	if rows.Next() {
		t.Error("foreign_key_check reported a violation after the rebuild")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("foreign_key_check: %v", err)
	}

	var ddl string
	if err := st.DB().QueryRowContext(ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'library_indexers'").
		Scan(&ddl); err != nil {
		t.Fatalf("read library_indexers ddl: %v", err)
	}
	if strings.Contains(ddl, "_rebuild") {
		t.Errorf("library_indexers still references a rebuild table:\n%s", ddl)
	}
	if !strings.Contains(ddl, "libraries") {
		t.Errorf("library_indexers lost its reference to libraries:\n%s", ddl)
	}

	// The cascade the reference exists for still fires.
	lib := enableAdultLibrary(t, st)
	ix := &core.IndexerConfig{
		Name: "Nzbee", Type: core.IndexerTypeNewznab, URL: "http://nzb.example", Enabled: true,
	}
	if err := st.UpsertIndexer(ctx, ix); err != nil {
		t.Fatalf("UpsertIndexer: %v", err)
	}
	if err := st.SetLibraryIndexer(ctx, &core.LibraryIndexer{
		LibraryID: lib.ID, IndexerID: ix.ID, Enabled: false,
	}); err != nil {
		t.Fatalf("SetLibraryIndexer: %v", err)
	}
	if err := st.DeleteIndexer(ctx, ix.ID); err != nil {
		t.Fatalf("DeleteIndexer: %v", err)
	}
	overrides, err := st.ListLibraryIndexers(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListLibraryIndexers: %v", err)
	}
	if len(overrides) != 0 {
		t.Errorf("deleting the indexer left %d override rows, want the cascade to clear them", len(overrides))
	}
}

// Migration 0011 seeds the Adult library, and it must arrive dormant. The row
// existing is not the trace this phase forbids (a shelf nobody can see is) so
// what matters is that `active = 0` and `restricted = 1` make
// core.LibraryVisible answer no to an admin as flatly as to a member. Switching
// it on is the deliberate act that turns the module on, in place of the create
// form that used to be the only door.
func TestFreshInstallSeedsADormantAdultLibrary(t *testing.T) {
	st, _ := openTemp(t)

	lib, err := st.GetLibraryByKind(t.Context(), core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind(adult) on a fresh install: %v", err)
	}
	if lib.Active {
		t.Errorf("seeded adult library is active, want it dormant")
	}
	if !lib.Restricted || lib.DLNAVisible || !lib.IsDefault {
		t.Errorf("seeded adult library = %+v, want restricted, unshared and its kind's default", *lib)
	}
	if core.LibraryVisible(*lib, core.RoleAdmin, true) {
		t.Errorf("the seeded adult library is visible to an admin, want the module off")
	}
}

// Switching the module off is a visibility promise, not a retention policy: it
// hides the shelf and deletes nothing, so switching it back on finds the
// library, its sites, and its scenes exactly as they were left.
//
// The rows deletion would take are the ones nothing else can rebuild. A scan
// can find the files again; the stash-box ids matched onto them, and the
// requests that named them, it cannot.
func TestSwitchingAdultOffDeletesNothing(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	enableAdultLibrary(t, st)
	site := &core.Series{Kind: core.SeriesKindAdult, StashID: "site-1", Title: "Example Site"}
	if err := st.UpsertSeries(ctx, site); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	scene := &core.Episode{
		SeriesID: site.ID, SeasonNumber: 2024, EpisodeNumber: 1, StashID: "scene-1",
		Title: "A Scene", Scene: &core.SceneInfo{Studio: "Example"},
	}
	if err := st.UpsertEpisode(ctx, scene); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}

	setAdultLibrariesActive(t, st, false)

	off, err := st.GetLibraryByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("switching the module off removed the adult library: %v", err)
	}
	if off.Active {
		t.Fatal("adult library is still active after being switched off")
	}
	if _, err := st.GetSeriesByStashID(ctx, "site-1"); err != nil {
		t.Errorf("switching the module off removed the site: %v", err)
	}
	if _, err := st.GetEpisodeByStashID(ctx, "scene-1"); err != nil {
		t.Errorf("switching the module off removed the scene: %v", err)
	}
}

// A caller built before kinds existed still writes a television series, and one
// that invents a kind is refused rather than filed somewhere arbitrary.
func TestUpsertSeriesDefaultsToTVAndRejectsAnUnknownKind(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	sr := &core.Series{TMDBID: 1399, Title: "Game of Thrones"}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if sr.Kind != core.SeriesKindTV {
		t.Errorf("kind after upsert = %q, want %q", sr.Kind, core.SeriesKindTV)
	}
	stored, err := st.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if stored.Kind != core.SeriesKindTV {
		t.Errorf("stored kind = %q, want %q", stored.Kind, core.SeriesKindTV)
	}

	if err := st.UpsertSeries(ctx, &core.Series{Kind: "documentary", Title: "Nope"}); err == nil {
		t.Error("UpsertSeries accepted an unknown kind")
	}
}

// Where a site's identity is enforced, after 0026 moved it.
//
// 0013 made stash_id itself globally unique, because there was one box and its
// UUIDs were therefore unambiguous. 0026 demoted that index: the public boxes
// are forks of one another and mint identical UUIDs, so a global rule would
// refuse the second box's copy of a site outright. The rule did not go away, it
// moved to 0024's UNIQUE (provider, provider_ref), which is where a site's
// identity has actually lived since every matched row started carrying both.
//
// Unmatched rows stay unconstrained either way: a scan that found scene files
// before it found metadata produces any number of them.
func TestAdultSeriesIdentityIsUniquePerProvider(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	for i := range 2 {
		sr := &core.Series{Kind: core.SeriesKindAdult, Title: "Unmatched"}
		if err := st.UpsertSeries(ctx, sr); err != nil {
			t.Fatalf("UpsertSeries(unmatched %d): %v", i, err)
		}
	}

	first := &core.Series{Kind: core.SeriesKindAdult, StashID: "site-1", Title: "Site"}
	if err := st.UpsertSeries(ctx, first); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if first.Provider != core.ProviderStashbox || first.ProviderRef != "site-1" {
		t.Fatalf("site identity = %q/%q, want the write door to fill in stashbox/site-1",
			first.Provider, first.ProviderRef)
	}
	// A different row claiming the same site on the same instance is the
	// duplicate that must still be refused.
	_, err := st.DB().ExecContext(ctx, `
		INSERT INTO series (kind, stash_id, provider, provider_ref, title, added_at, updated_at)
		VALUES ('adult', 'site-1', 'stashbox', 'site-1', 'Impostor', '', '')`)
	if err == nil {
		t.Error("a second series claimed the same site on the same instance")
	}
}

// A refresh knows the site's id and must land on the row it wrote last time.
func TestUpsertSeriesMatchesAnAdultSeriesByStashID(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	first := &core.Series{Kind: core.SeriesKindAdult, StashID: "site-1", Title: "Site"}
	if err := st.UpsertSeries(ctx, first); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	refreshed := &core.Series{Kind: core.SeriesKindAdult, StashID: "site-1", Title: "Site (renamed)"}
	if err := st.UpsertSeries(ctx, refreshed); err != nil {
		t.Fatalf("UpsertSeries(refresh): %v", err)
	}
	if refreshed.ID != first.ID {
		t.Errorf("refresh created series %d, want an update of %d", refreshed.ID, first.ID)
	}

	got, err := st.GetSeriesByStashID(ctx, "site-1")
	if err != nil {
		t.Fatalf("GetSeriesByStashID: %v", err)
	}
	if got.Title != "Site (renamed)" {
		t.Errorf("title = %q, want the refreshed one", got.Title)
	}
	if _, err := st.GetSeriesByStashID(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSeriesByStashID(\"\") = %v, want ErrNotFound", err)
	}
}

func TestListSeriesByKind(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	if err := st.UpsertSeries(ctx, &core.Series{TMDBID: 1399, Title: "Show", SortTitle: "show"}); err != nil {
		t.Fatalf("UpsertSeries(tv): %v", err)
	}
	if err := st.UpsertSeries(ctx, &core.Series{
		Kind: core.SeriesKindAdult, StashID: "site-1", Title: "Site", SortTitle: "site",
	}); err != nil {
		t.Fatalf("UpsertSeries(adult): %v", err)
	}

	all, err := st.ListSeries(ctx)
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if len(all) != 2 {
		t.Errorf("ListSeries returned %d rows, want both kinds", len(all))
	}

	for _, tc := range []struct{ kind, title string }{
		{core.SeriesKindTV, "Show"},
		{core.SeriesKindAdult, "Site"},
	} {
		got, err := st.ListSeriesByKind(ctx, tc.kind)
		if err != nil {
			t.Fatalf("ListSeriesByKind(%s): %v", tc.kind, err)
		}
		if len(got) != 1 || got[0].Title != tc.title {
			t.Errorf("ListSeriesByKind(%s) = %+v, want just %q", tc.kind, got, tc.title)
		}
	}
}

// The scene column is the one piece of an episode that is not a column, so it
// has to survive the encode/decode round trip intact, including the difference
// between "no scene metadata" and "an empty one".
func TestEpisodeSceneRoundTrips(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	site := &core.Series{Kind: core.SeriesKindAdult, StashID: "site-1", Title: "Site"}
	if err := st.UpsertSeries(ctx, site); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}

	scene := &core.Episode{
		SeriesID: site.ID, SeasonNumber: 2024, EpisodeNumber: 12, StashID: "scene-1",
		Title: "A Scene", AirDate: time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),
		Monitored: true,
		Scene: &core.SceneInfo{
			Studio:     "Example Studio",
			Performers: []string{"Ada Lovelace", "Grace Hopper"},
			URL:        "https://example.test/scenes/1",
		},
	}
	if err := st.UpsertEpisode(ctx, scene); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}

	got, err := st.GetEpisodeByStashID(ctx, "scene-1")
	if err != nil {
		t.Fatalf("GetEpisodeByStashID: %v", err)
	}
	if got.ID != scene.ID {
		t.Errorf("GetEpisodeByStashID returned episode %d, want %d", got.ID, scene.ID)
	}
	if !reflect.DeepEqual(got.Scene, scene.Scene) {
		t.Errorf("scene = %+v, want %+v", got.Scene, scene.Scene)
	}

	// A television episode carries no scene at all, and reads back as nil
	// rather than as an empty struct.
	show := &core.Series{TMDBID: 1399, Title: "Show"}
	if err := st.UpsertSeries(ctx, show); err != nil {
		t.Fatalf("UpsertSeries(tv): %v", err)
	}
	tv := &core.Episode{SeriesID: show.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"}
	if err := st.UpsertEpisode(ctx, tv); err != nil {
		t.Fatalf("UpsertEpisode(tv): %v", err)
	}
	if got, err = st.GetEpisode(ctx, tv.ID); err != nil {
		t.Fatalf("GetEpisode: %v", err)
	}
	if got.Scene != nil {
		t.Errorf("television episode scene = %+v, want nil", got.Scene)
	}
	if _, err := st.GetEpisodeByStashID(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetEpisodeByStashID(\"\") = %v, want ErrNotFound", err)
	}
}

// A scene request is identified by its stash-box id, and the table refuses to
// hold a row that names the wrong identifier for its media type.
func TestSceneRequestsAreIdentifiedByStashID(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	r := &core.Request{
		MediaType: core.MediaTypeScene, StashID: "scene-1", Title: "A Scene",
		Year: 2024, RequestedBy: 3,
	}
	if err := st.CreateRequest(ctx, r); err != nil {
		t.Fatalf("CreateRequest(scene): %v", err)
	}
	got, err := st.GetRequest(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.MediaType != core.MediaTypeScene || got.StashID != "scene-1" || got.TMDBID != 0 {
		t.Errorf("stored scene request = %+v, want stash-identified with no tmdb id", got)
	}

	// The one-pending-per-title rule holds for scenes too, keyed on the id they
	// actually carry rather than on the zero every scene shares.
	second := &core.Request{
		MediaType: core.MediaTypeScene, StashID: "scene-1", Title: "A Scene", RequestedBy: 9,
	}
	if err := st.CreateRequest(ctx, second); err != nil {
		t.Fatalf("CreateRequest(scene again): %v", err)
	}
	if second.ID != r.ID {
		t.Errorf("a second request for the same scene made row %d, want a merge into %d",
			second.ID, r.ID)
	}
	if second.RequestedBy != 3 {
		t.Errorf("the merge reassigned the request to %d, want the first asker 3", second.RequestedBy)
	}

	// Two different scenes are two different pending rows: the index must key
	// on stash_id, not merely exist.
	other := &core.Request{MediaType: core.MediaTypeScene, StashID: "scene-2", Title: "Another"}
	if err := st.CreateRequest(ctx, other); err != nil {
		t.Fatalf("CreateRequest(other scene): %v", err)
	}
	if other.ID == r.ID {
		t.Error("a request for a different scene merged into the first")
	}

	// And the CHECK is what stops a caller from mixing the two namespaces.
	bad := &core.Request{MediaType: core.MediaTypeScene, TMDBID: 5, Title: "Wrong"}
	if err := st.CreateRequest(ctx, bad); err == nil {
		t.Error("CreateRequest accepted a scene request identified by a TMDB id")
	}
	bad = &core.Request{MediaType: core.MediaTypeSeries, StashID: "scene-3", Title: "Wrong"}
	if err := st.CreateRequest(ctx, bad); err == nil {
		t.Error("CreateRequest accepted a series request identified by a stash id")
	}
}

// The rule the pending index has always enforced must survive being split in
// two: movies and series still merge on their TMDB id.
func TestPendingTMDBRequestsStillMerge(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	first := &core.Request{MediaType: core.MediaTypeSeries, TMDBID: 1399, Title: "GoT", Seasons: []int{1}}
	if err := st.CreateRequest(ctx, first); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	second := &core.Request{MediaType: core.MediaTypeSeries, TMDBID: 1399, Title: "GoT", Seasons: []int{2}}
	if err := st.CreateRequest(ctx, second); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second request made row %d, want a merge into %d", second.ID, first.ID)
	}
	if !reflect.DeepEqual(second.Seasons, []int{1, 2}) {
		t.Errorf("merged seasons = %v, want [1 2]", second.Seasons)
	}
}

// The two bulk stash-id lookups the discover screen is built from. Both take a
// caller-supplied list, and both would be a leak if a blank entry reached the
// SQL: every television episode has an empty stash_id, and every movie and
// series request has one too, so a blank in the IN-list matches the entire
// non-adult half of each table.
func TestStashIDLookupsIgnoreTheBlankID(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	// A television series with one ordinary episode: stash_id '' on both.
	show := &core.Series{TMDBID: 1, Title: "A Show", SortTitle: "a show", Monitored: true}
	if err := st.UpsertSeries(ctx, show); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := st.UpsertEpisode(ctx, &core.Episode{
		SeriesID: show.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot", Monitored: true,
	}); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	// A pending series request: stash_id '' as well.
	if err := st.CreateRequest(ctx, &core.Request{
		MediaType: core.MediaTypeSeries, TMDBID: 99, Title: "Wanted Show",
	}); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	// A site with one scene, and a pending scene request.
	site := &core.Series{
		Provider: core.ProviderStashbox, ProviderRef: "site-1",
		StashID: "site-1", Title: "Brazzers", SortTitle: "brazzers",
		Kind: core.SeriesKindAdult, Monitored: true,
	}
	if err := st.UpsertSeries(ctx, site); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	scene := &core.Episode{
		SeriesID: site.ID, SeasonNumber: 2022, EpisodeNumber: 1, StashID: "scene-1",
		Title: "Deep Impact", Monitored: true,
	}
	if err := st.UpsertEpisode(ctx, scene); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	if err := st.CreateRequest(ctx, &core.Request{
		MediaType: core.MediaTypeScene, StashID: "scene-2", Title: "Another Scene",
	}); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	t.Run("EpisodeIDsByStashID", func(t *testing.T) {
		got, err := st.EpisodeIDsByStashID(ctx, []string{"", "scene-1", "", "scene-missing"})
		if err != nil {
			t.Fatalf("EpisodeIDsByStashID: %v", err)
		}
		if len(got) != 1 || got["scene-1"] != scene.ID {
			t.Fatalf("got = %v, want exactly the one scene", got)
		}

		// Only blanks: no query, no rows, no error.
		got, err = st.EpisodeIDsByStashID(ctx, []string{"", ""})
		if err != nil || len(got) != 0 {
			t.Fatalf("EpisodeIDsByStashID(blanks) = %v, %v, want an empty result", got, err)
		}
		if got, err := st.EpisodeIDsByStashID(ctx, nil); err != nil || got == nil {
			t.Fatalf("EpisodeIDsByStashID(nil) = %v, %v, want an empty map", got, err)
		}
	})

	t.Run("EpisodeFileIDsByStashID", func(t *testing.T) {
		got, err := st.EpisodeFileIDsByStashID(ctx, []string{"", "scene-1"})
		if err != nil {
			t.Fatalf("EpisodeFileIDsByStashID before file: %v", err)
		}
		if len(got) != 0 {
			t.Fatalf("placeholder scene reported as held: %v", got)
		}

		file := &core.MediaFile{Path: "library/Adult/Brazzers/scene-1.mkv", Size: 42}
		if err := st.UpsertMediaFile(ctx, file); err != nil {
			t.Fatalf("UpsertMediaFile: %v", err)
		}
		if err := st.LinkEpisodeFile(ctx, scene.ID, file.ID); err != nil {
			t.Fatalf("LinkEpisodeFile: %v", err)
		}
		got, err = st.EpisodeFileIDsByStashID(ctx, []string{"scene-1", "scene-missing"})
		if err != nil {
			t.Fatalf("EpisodeFileIDsByStashID after file: %v", err)
		}
		if len(got) != 1 || got["scene-1"] != scene.ID {
			t.Fatalf("got = %v, want exactly the scene with a file", got)
		}
		scoped, err := st.EpisodeFileIDsByStashIDForProvider(
			ctx, core.ProviderStashbox, []string{"scene-1"},
		)
		if err != nil || scoped["scene-1"] != scene.ID {
			t.Fatalf("legacy provider lookup = %v, %v, want scene %d", scoped, err, scene.ID)
		}
		scoped, err = st.EpisodeFileIDsByStashIDForProvider(
			ctx, core.ProviderStashbox+":other", []string{"scene-1"},
		)
		if err != nil || len(scoped) != 0 {
			t.Fatalf("other provider lookup = %v, %v, want empty", scoped, err)
		}
	})

	t.Run("ListPendingRequestsForStashIDs", func(t *testing.T) {
		got, err := st.ListPendingRequestsForStashIDs(ctx, []string{"", "scene-2", ""})
		if err != nil {
			t.Fatalf("ListPendingRequestsForStashIDs: %v", err)
		}
		if len(got) != 1 || got[0].StashID != "scene-2" {
			t.Fatalf("got = %+v, want exactly the one scene request", got)
		}

		got, err = st.ListPendingRequestsForStashIDs(ctx, []string{""})
		if err != nil || len(got) != 0 {
			t.Fatalf("ListPendingRequestsForStashIDs(blank) = %+v, %v, want nothing", got, err)
		}
		if got, err := st.ListPendingRequestsForStashIDs(ctx, nil); err != nil || got == nil {
			t.Fatalf("ListPendingRequestsForStashIDs(nil) = %v, %v, want an empty slice", got, err)
		}
	})

	// A decided request is not pending, so it decorates nothing.
	t.Run("only pending rows", func(t *testing.T) {
		rows, err := st.ListPendingRequestsForStashIDs(ctx, []string{"scene-2"})
		if err != nil || len(rows) != 1 {
			t.Fatalf("setup: %+v, %v", rows, err)
		}
		if err := st.SetRequestStatus(ctx, rows[0].ID, core.RequestDismissed); err != nil {
			t.Fatalf("SetRequestStatus: %v", err)
		}
		got, err := st.ListPendingRequestsForStashIDs(ctx, []string{"scene-2"})
		if err != nil || len(got) != 0 {
			t.Fatalf("a dismissed request still reads as pending: %+v, %v", got, err)
		}
	})
}
