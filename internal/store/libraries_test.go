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

	// Every one of the four seeded kinds answers, dormant ones included: this
	// lookup is about which shelf a row belongs to, not about who may see it.
	for _, kind := range []string{core.LibraryKindMovie, core.LibraryKindAnime, core.LibraryKindAdult} {
		if _, err := st.GetLibraryByKind(ctx, kind); err != nil {
			t.Errorf("GetLibraryByKind(%s): %v", kind, err)
		}
	}
	if _, err := st.GetLibraryByKind(ctx, "nonesuch"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetLibraryByKind(nonesuch) error = %v, want ErrNotFound", err)
	}
}

// An adult library is deleted under the two ordinary guards and no others. It
// once had a third, refusing the kind outright, because the module switch owned
// the row and promised that switching off destroyed nothing; `active` keeps
// that promise now and is the deliberate "off". A kind-shaped exception here
// would only mean the one shelf an owner can never tidy away — and it would
// leave them deleting the row by hand, which takes the grants and the items
// with it silently.
func TestDeleteLibraryAcceptsAnAdultLibrary(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	lib := &core.Library{
		Kind: core.LibraryKindAdult, Name: "Scenes", RootPath: "library/Scenes",
		Providers: []string{core.ProviderStashbox}, Restricted: true,
	}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		t.Fatalf("CreateLibrary(adult): %v", err)
	}

	// The emptiness guard applies to it exactly as it does to any other shelf:
	// its sites are items, and deleting the row under them would strand them.
	site := &core.Series{Kind: core.SeriesKindAdult, StashID: "site-1", Title: "Example Site",
		LibraryID: lib.ID}
	if err := st.UpsertSeries(ctx, site); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := st.DeleteLibrary(ctx, lib.ID); !errors.Is(err, ErrLibraryNotEmpty) {
		t.Errorf("DeleteLibrary(non-empty adult) = %v, want ErrLibraryNotEmpty", err)
	}

	if err := st.DeleteSeries(ctx, site.ID); err != nil {
		t.Fatalf("DeleteSeries: %v", err)
	}
	if err := st.DeleteLibrary(ctx, lib.ID); err != nil {
		t.Fatalf("DeleteLibrary(empty adult) = %v, want the delete to go through", err)
	}
	if _, err := st.GetLibrary(ctx, lib.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetLibrary after delete = %v, want ErrNotFound", err)
	}
}

// The default guard is NOT the adult exception wearing a different name, and
// this test exists so that nobody reading the one above deletes this one as a
// leftover. ErrLibraryIsDefault is about by-kind lookups keeping an answer:
// GetLibraryByKind and GetDefaultLibrary are what a scan, an import and a
// scene request resolve their shelf through, and a kind whose default was
// deleted answers ErrNotFound to all three. The refusal is per row and is
// lifted by demoting it, which is a thing an owner can actually do — that is
// what makes it a guard rather than the ban that was removed.
func TestDeleteLibraryRefusesADefaultAdultLibrary(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	// The seeded row, which is its kind's default (migration 0011).
	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind(adult): %v", err)
	}

	if err := st.DeleteLibrary(ctx, lib.ID); !errors.Is(err, ErrLibraryIsDefault) {
		t.Errorf("DeleteLibrary(default adult) = %v, want ErrLibraryIsDefault", err)
	}
	if _, err := st.GetLibrary(ctx, lib.ID); err != nil {
		t.Errorf("the refused delete removed the library anyway: %v", err)
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

func TestSeededLibrarySlugs(t *testing.T) {
	st, _ := openTemp(t)
	want := map[string]string{
		core.LibraryKindMovie: "movies",
		core.LibraryKindTV:    "series",
		core.LibraryKindAnime: "anime",
		core.LibraryKindAdult: "adult",
	}
	for kind, slug := range want {
		lib, err := st.GetLibraryByKind(t.Context(), kind)
		if err != nil {
			t.Fatalf("GetLibraryByKind(%s): %v", kind, err)
		}
		if lib.Slug != slug {
			t.Errorf("seeded %s slug = %q, want %q", kind, lib.Slug, slug)
		}
		got, err := st.GetLibraryBySlug(t.Context(), slug)
		if err != nil {
			t.Fatalf("GetLibraryBySlug(%q): %v", slug, err)
		}
		if got.ID != lib.ID {
			t.Errorf("GetLibraryBySlug(%q).ID = %d, want %d", slug, got.ID, lib.ID)
		}
	}
	if _, err := st.GetLibraryBySlug(t.Context(), "nonesuch"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetLibraryBySlug(nonesuch) = %v, want ErrNotFound", err)
	}
}

func TestCreateLibraryAllocatesUniqueSlug(t *testing.T) {
	st, _ := openTemp(t)
	ctx := t.Context()

	kids := &core.Library{
		Kind: core.LibraryKindMovie, Name: "Kids", RootPath: "library/Kids",
		Providers: []string{core.ProviderTMDB},
	}
	if err := st.CreateLibrary(ctx, kids); err != nil {
		t.Fatalf("CreateLibrary(Kids): %v", err)
	}
	if kids.Slug != "kids" {
		t.Errorf("Kids slug = %q, want %q", kids.Slug, "kids")
	}

	// The seeded movie library already owns "movies", so a second shelf of that
	// name must not collide.
	dup := &core.Library{
		Kind: core.LibraryKindMovie, Name: "Movies", RootPath: "library/Movies2",
		Providers: []string{core.ProviderTMDB},
	}
	if err := st.CreateLibrary(ctx, dup); err != nil {
		t.Fatalf("CreateLibrary(Movies): %v", err)
	}
	if dup.Slug != "movies-2" {
		t.Errorf("second Movies slug = %q, want %q", dup.Slug, "movies-2")
	}

	// A rename must not move the URL: bookmarks point at the slug.
	kids.Name = "Family"
	if err := st.UpdateLibrary(ctx, kids); err != nil {
		t.Fatalf("UpdateLibrary rename: %v", err)
	}
	got, err := st.GetLibrary(ctx, kids.ID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	if got.Name != "Family" || got.Slug != "kids" {
		t.Errorf("renamed library = name %q slug %q, want Family / kids", got.Name, got.Slug)
	}
}
