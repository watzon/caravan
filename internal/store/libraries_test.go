package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// openAtSchemaVersion builds a database frozen at an older schema version, so
// a test can watch a later migration run against a populated install rather
// than against a fresh file.
func openAtSchemaVersion(t *testing.T, path string, version int) {
	t.Helper()
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("open %q at schema %d: %v", path, version, err)
	}
	defer db.Close()
	if err := (&Store{db: db}).migrateTo(version); err != nil {
		t.Fatalf("migrate %q to schema %d: %v", path, version, err)
	}
}

// The migration has to describe an install that already exists, not just a
// fresh one: an upgrade seeds the two libraries that were implied by the
// folders on disk, and seeds no overrides at all.
func TestMigrateSeedsLibrariesIntoExistingDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "caravan.db")

	// 0011 is the last schema that knew nothing about libraries.
	openAtSchemaVersion(t, path, 11)

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after upgrade: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	got, err := st.ListLibraries(ctx)
	if err != nil {
		t.Fatalf("ListLibraries: %v", err)
	}
	want := []core.Library{
		{ID: 1, Kind: core.LibraryKindMovie, Name: "Movies", RootPath: "library/Movies", DLNAVisible: true},
		{ID: 2, Kind: core.LibraryKindTV, Name: "Series", RootPath: "library/TV", DLNAVisible: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ListLibraries = %+v, want %+v", got, want)
	}

	// Absence is the default, so the join table starts empty however many
	// indexers the install already had.
	for _, l := range got {
		rows, err := st.ListLibraryIndexers(ctx, l.ID)
		if err != nil {
			t.Fatalf("ListLibraryIndexers(%d): %v", l.ID, err)
		}
		if len(rows) != 0 {
			t.Errorf("library %d seeded %d indexer overrides, want 0", l.ID, len(rows))
		}
	}
}

func TestGetLibraryByKind(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	l, err := st.GetLibraryByKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetLibraryByKind(tv): %v", err)
	}
	if l.RootPath != "library/TV" {
		t.Errorf("tv library root = %q, want %q", l.RootPath, "library/TV")
	}

	if _, err := st.GetLibraryByKind(ctx, "adult"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetLibraryByKind(adult) error = %v, want ErrNotFound", err)
	}
}

func TestUpdateLibraryRoundTrips(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	l, err := st.GetLibraryByKind(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("GetLibraryByKind(movie): %v", err)
	}
	l.DLNAVisible = false
	l.RouteTorrent = RouteEmbedded
	l.QualityProfileID = 1
	if err := st.UpdateLibrary(ctx, l); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}

	got, err := st.GetLibrary(ctx, l.ID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	if !reflect.DeepEqual(got, l) {
		t.Errorf("GetLibrary = %+v, want %+v", got, l)
	}

	// Clearing an override has to reach NULL, not "", or the fallback below
	// would never fire again.
	got.RouteTorrent = ""
	got.QualityProfileID = 0
	if err := st.UpdateLibrary(ctx, got); err != nil {
		t.Fatalf("UpdateLibrary clearing overrides: %v", err)
	}
	var (
		route     sql.NullString
		profileID sql.NullInt64
	)
	err = st.DB().QueryRowContext(ctx,
		"SELECT route_torrent, quality_profile_id FROM libraries WHERE id = ?", got.ID).
		Scan(&route, &profileID)
	if err != nil {
		t.Fatalf("read cleared overrides: %v", err)
	}
	if route.Valid || profileID.Valid {
		t.Errorf("cleared overrides stored as (%v, %v), want NULL", route, profileID)
	}

	if err := st.UpdateLibrary(ctx, &core.Library{ID: 999}); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateLibrary(absent) error = %v, want ErrNotFound", err)
	}
}

// seedIndexer adds an enabled indexer with the given default categories.
func seedIndexer(t *testing.T, st *Store, name string, categories []int) core.IndexerConfig {
	t.Helper()
	ix := core.IndexerConfig{
		Name:       name,
		URL:        "http://indexer.invalid/" + name,
		Type:       core.IndexerTypeTorznab,
		Categories: categories,
		Enabled:    true,
	}
	if err := st.UpsertIndexer(context.Background(), &ix); err != nil {
		t.Fatalf("UpsertIndexer(%q): %v", name, err)
	}
	return ix
}

// The migrated state must resolve to exactly what the globals already said —
// that is the whole "zero observable behavior change" promise.
func TestResolveLibrarySettingsFallsBackToGlobals(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	if err := st.SetSetting(ctx, SettingRouteTorrent, "7"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	tv := seedIndexer(t, st, "tv-indexer", []int{5000, 5030})

	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	got, err := st.ResolveLibrarySettings(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ResolveLibrarySettings: %v", err)
	}

	want := &core.LibrarySettings{
		LibraryID:    lib.ID,
		Kind:         core.LibraryKindTV,
		RouteTorrent: "7",
		// Never set globally and never overridden: still nothing configured.
		RouteUsenet:      "",
		DLNAVisible:      true,
		QualityProfileID: 0,
		Indexers:         []core.IndexerConfig{tv},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveLibrarySettings = %+v, want %+v", got, want)
	}
}

func TestResolveLibrarySettingsPrefersLibraryOverrides(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	if err := st.SetSetting(ctx, SettingRouteTorrent, "7"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := st.SetSetting(ctx, SettingRouteUsenet, "9"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	ix := seedIndexer(t, st, "shared", []int{2000, 5000})

	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	lib.RouteTorrent = RouteEmbedded
	lib.DLNAVisible = false
	lib.QualityProfileID = 1
	if err := st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	if err := st.SetLibraryIndexer(ctx, &core.LibraryIndexer{
		LibraryID: lib.ID, IndexerID: ix.ID, Enabled: true, Categories: []int{5000},
	}); err != nil {
		t.Fatalf("SetLibraryIndexer: %v", err)
	}

	got, err := st.ResolveLibrarySettings(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ResolveLibrarySettings: %v", err)
	}
	want := &core.LibrarySettings{
		LibraryID:    lib.ID,
		Kind:         core.LibraryKindTV,
		RouteTorrent: RouteEmbedded,
		// Not overridden, so the global still answers.
		RouteUsenet:      "9",
		DLNAVisible:      false,
		QualityProfileID: 1,
		Indexers: []core.IndexerConfig{{
			ID: ix.ID, Name: ix.Name, URL: ix.URL, Type: ix.Type,
			Categories: []int{5000}, Enabled: true,
		}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("ResolveLibrarySettings = %+v, want %+v", got, want)
	}

	// The override belongs to one library. The other must be untouched by it,
	// which is the point of the join table.
	movies, err := st.GetLibraryByKind(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("GetLibraryByKind(movie): %v", err)
	}
	other, err := st.ResolveLibrarySettings(ctx, movies.ID)
	if err != nil {
		t.Fatalf("ResolveLibrarySettings(movie): %v", err)
	}
	if !reflect.DeepEqual(other.Indexers, []core.IndexerConfig{ix}) {
		t.Errorf("movie library indexers = %+v, want the indexer's own categories %+v",
			other.Indexers, []core.IndexerConfig{ix})
	}
	if other.RouteTorrent != "7" {
		t.Errorf("movie library route_torrent = %q, want the global %q", other.RouteTorrent, "7")
	}
}

// An absent row is enabled with the indexer's own categories; only the
// indexer that has a row deviates.
func TestResolveLibrarySettingsAbsentRowIsEnabledWithDefaults(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	kept := seedIndexer(t, st, "aaa-kept", []int{5000})
	dropped := seedIndexer(t, st, "bbb-dropped", []int{5000})

	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	if err := st.SetLibraryIndexer(ctx, &core.LibraryIndexer{
		LibraryID: lib.ID, IndexerID: dropped.ID, Enabled: false,
	}); err != nil {
		t.Fatalf("SetLibraryIndexer: %v", err)
	}

	got, err := st.ResolveLibrarySettings(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ResolveLibrarySettings: %v", err)
	}
	if !reflect.DeepEqual(got.Indexers, []core.IndexerConfig{kept}) {
		t.Errorf("Indexers = %+v, want only the indexer with no override row %+v",
			got.Indexers, []core.IndexerConfig{kept})
	}

	// Disabling it globally is a separate switch, and it removes the indexer
	// from every library rather than just this one.
	kept.Enabled = false
	if err := st.UpsertIndexer(ctx, &kept); err != nil {
		t.Fatalf("UpsertIndexer: %v", err)
	}
	got, err = st.ResolveLibrarySettings(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ResolveLibrarySettings: %v", err)
	}
	if len(got.Indexers) != 0 {
		t.Errorf("Indexers = %+v, want none", got.Indexers)
	}
}

// A non-nil empty override is not the same as no override: it means "search
// this indexer unfiltered", which is what an empty category list has always
// meant, so it has to survive the round trip through a nullable column.
func TestSetLibraryIndexerDistinguishesEmptyFromAbsentCategories(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	ix := seedIndexer(t, st, "indexer", []int{5000})
	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	if err := st.SetLibraryIndexer(ctx, &core.LibraryIndexer{
		LibraryID: lib.ID, IndexerID: ix.ID, Enabled: true, Categories: []int{},
	}); err != nil {
		t.Fatalf("SetLibraryIndexer: %v", err)
	}

	rows, err := st.ListLibraryIndexers(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListLibraryIndexers: %v", err)
	}
	if len(rows) != 1 || rows[0].Categories == nil || len(rows[0].Categories) != 0 {
		t.Fatalf("ListLibraryIndexers = %+v, want one row with an empty non-nil override", rows)
	}

	got, err := st.ResolveLibrarySettings(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ResolveLibrarySettings: %v", err)
	}
	if len(got.Indexers) != 1 || len(got.Indexers[0].Categories) != 0 {
		t.Errorf("resolved categories = %+v, want empty", got.Indexers)
	}
}

// Deleting an indexer must not leave overrides pointing at a hole an
// autoincremented id could later reuse.
func TestDeleteIndexerCascadesToLibraryOverrides(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	ix := seedIndexer(t, st, "indexer", []int{5000})
	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	if err := st.SetLibraryIndexer(ctx, &core.LibraryIndexer{
		LibraryID: lib.ID, IndexerID: ix.ID, Enabled: false,
	}); err != nil {
		t.Fatalf("SetLibraryIndexer: %v", err)
	}
	if err := st.DeleteIndexer(ctx, ix.ID); err != nil {
		t.Fatalf("DeleteIndexer: %v", err)
	}

	rows, err := st.ListLibraryIndexers(ctx, lib.ID)
	if err != nil {
		t.Fatalf("ListLibraryIndexers: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("ListLibraryIndexers = %+v, want none after the indexer was deleted", rows)
	}
}

// A library's default quality profile is what items that name none of their own
// are graded against. Resolving straight to the store-wide default instead makes
// the setting save, render as an override, and change nothing.
func TestResolveItemQualityProfilePrefersTheLibraryDefault(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	seeded, err := st.ListQualityProfiles(ctx)
	if err != nil {
		t.Fatalf("list quality profiles: %v", err)
	}
	if len(seeded) != 1 {
		t.Fatalf("seeded profiles = %d, want the single store-wide default", len(seeded))
	}
	hd := core.QualityProfile{Name: "HD only", Cutoff: core.Quality1080p, Items: []string{core.Quality1080p}}
	if err := st.CreateQualityProfile(ctx, &hd); err != nil {
		t.Fatalf("create quality profile: %v", err)
	}

	tv, err := st.GetLibraryByKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("get tv library: %v", err)
	}
	tv.QualityProfileID = hd.ID
	if err := st.UpdateLibrary(ctx, tv); err != nil {
		t.Fatalf("update tv library: %v", err)
	}

	// An item naming no profile takes its own library's default...
	got, err := st.ResolveItemQualityProfile(ctx, core.LibraryKindTV, 0)
	if err != nil {
		t.Fatalf("resolve tv item profile: %v", err)
	}
	if got.ID != hd.ID {
		t.Errorf("tv item with no profile resolved to %q, want the library default %q", got.Name, hd.Name)
	}
	// ...and so does one pointing at a profile that has since been deleted,
	// which is the same "this item names nothing usable" state.
	got, err = st.ResolveItemQualityProfile(ctx, core.LibraryKindTV, hd.ID+999)
	if err != nil {
		t.Fatalf("resolve dangling tv item profile: %v", err)
	}
	if got.ID != hd.ID {
		t.Errorf("tv item with a dangling profile resolved to %q, want the library default %q", got.Name, hd.Name)
	}

	// The library that set no default is untouched.
	got, err = st.ResolveItemQualityProfile(ctx, core.LibraryKindMovie, 0)
	if err != nil {
		t.Fatalf("resolve movie item profile: %v", err)
	}
	if got.ID != seeded[0].ID {
		t.Errorf("movie item resolved to %q, want the store-wide default %q", got.Name, seeded[0].Name)
	}

	// An item that names a profile keeps it, whatever the library defaults to.
	got, err = st.ResolveItemQualityProfile(ctx, core.LibraryKindTV, seeded[0].ID)
	if err != nil {
		t.Fatalf("resolve named tv item profile: %v", err)
	}
	if got.ID != seeded[0].ID {
		t.Errorf("tv item naming %q resolved to %q", seeded[0].Name, got.Name)
	}
}
