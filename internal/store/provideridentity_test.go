package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// 0024 gives every provider the identity shape stash_id had, and gives every
// library a chain whose head is the provider it already used. An upgraded
// install must come out the other side describing exactly what it was already
// doing: every matched row pinned to the provider that actually matched it,
// every library answering the same provider it answered before, and the
// uniqueness the two old columns enforced now enforced once, for all of them.
func TestMigrate0024BackfillsProviderIdentity(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "caravan.db")
	openAtSchemaVersion(t, path, 23)

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("open seeded database: %v", err)
	}
	exec(t, db, `INSERT INTO movies (id, tmdb_id, title, sort_title, year, added_at, updated_at)
		VALUES (5, 603, 'The Matrix', 'matrix', 1999,
		        '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`)
	exec(t, db, `INSERT INTO series (id, kind, tmdb_id, title, sort_title, year, added_at, updated_at)
		VALUES (11, 'tv', 1399, 'Game of Thrones', 'game of thrones', 2011,
		        '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`)
	// A site as the adult module wrote it, matched by stash id.
	exec(t, db, `INSERT INTO series (id, kind, stash_id, title, sort_title, year, added_at, updated_at)
		VALUES (12, 'adult', 'uuid-site', 'Some Site', 'some site', 0,
		        '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`)
	// An adult row with a stray tmdb_id and NO stash id: the case the backfill's
	// kind guard exists for. The stash-box pass cannot claim it back, so without
	// the guard it is pinned forever to a provider that has never heard of it,
	// and every refresh of it would ask TMDB about a site.
	exec(t, db, `INSERT INTO series (id, kind, tmdb_id, title, sort_title, year,
		added_at, updated_at)
		VALUES (13, 'adult', 424242, 'Unmatched Site', 'unmatched site', 0,
		        '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`)
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded database: %v", err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after upgrade: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	mv, err := st.GetMovie(ctx, 5)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if mv.Provider != core.ProviderTMDB || mv.ProviderRef != "603" {
		t.Errorf("movie identity = %q/%q, want tmdb/603", mv.Provider, mv.ProviderRef)
	}

	wantSeries := map[int64]core.ItemRef{
		11: {Provider: core.ProviderTMDB, Ref: "1399"},
		12: {Provider: core.ProviderStashbox, Ref: "uuid-site"},
		// Unidentified rather than pinned to TMDB: an adult row's stray tmdb_id
		// is not an identity, and leaving it blank is what lets the module
		// match it to a site later.
		13: {},
	}
	for id, want := range wantSeries {
		sr, err := st.GetSeries(ctx, id)
		if err != nil {
			t.Fatalf("GetSeries(%d): %v", id, err)
		}
		got := core.ItemRef{Provider: sr.Provider, Ref: sr.ProviderRef}
		if got != want {
			t.Errorf("series %d identity = %+v, want %+v", id, got, want)
		}
	}

	// Every library's chain is the provider it already had, so a chain walker
	// added later starts by doing exactly what the single column did.
	wantChains := map[string][]string{
		core.LibraryKindMovie: {core.ProviderTMDB},
		core.LibraryKindTV:    {core.ProviderTMDB},
	}
	libs, err := st.ListLibraries(ctx)
	if err != nil {
		t.Fatalf("ListLibraries: %v", err)
	}
	for _, lib := range libs {
		want, ok := wantChains[lib.Kind]
		if !ok {
			t.Errorf("unexpected library %+v", lib)
			continue
		}
		if !reflect.DeepEqual(lib.ProviderChain(), want) {
			t.Errorf("library %q chain = %v, want %v", lib.Name, lib.ProviderChain(), want)
		}
	}

	// The adult library is not seeded by a migration — it is created like any
	// other shelf — so nothing backfills its chain and the write door is the
	// only thing that can put one on it. An adult library that came out of the
	// create with no chain would ask no provider anything.
	enableAdultLibrary(t, st)
	adult, err := st.ListLibrariesByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("ListLibrariesByKind(adult): %v", err)
	}
	if len(adult) != 1 {
		t.Fatalf("adult libraries = %+v, want exactly one", adult)
	}
	if want := []string{core.ProviderStashbox}; !reflect.DeepEqual(adult[0].ProviderChain(), want) {
		t.Errorf("adult chain = %v, want %v", adult[0].ProviderChain(), want)
	}
}

// The partial index is 0001's rule for tmdb_id and 0013's for stash_id, stated
// once: a ref is unique per provider, and unidentified rows do not collide with
// each other — which is what a scan that found files before it found metadata
// produces, over and over.
func TestProviderRefUniquenessIsPartial(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caravan.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("open for raw inserts: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	insert := func(id int64, provider, ref string) error {
		_, err := db.Exec(`INSERT INTO movies (id, provider, provider_ref, tmdb_id, title,
			sort_title, year, added_at, updated_at)
			VALUES (?, ?, ?, 0, 'x', 'x', 0, '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`,
			id, provider, ref)
		return err
	}

	if err := insert(1, core.ProviderTMDB, "603"); err != nil {
		t.Fatalf("first tmdb/603: %v", err)
	}
	if err := insert(2, core.ProviderTMDB, "603"); err == nil {
		t.Error("a second tmdb/603 was admitted; the ref index is not unique")
	}
	// Same ref, different provider: two providers numbering their own catalogues
	// is not a collision.
	if err := insert(3, core.ProviderStashbox, "603"); err != nil {
		t.Errorf("stashbox/603 alongside tmdb/603: %v", err)
	}
	if err := insert(4, "", ""); err != nil {
		t.Fatalf("first unidentified row: %v", err)
	}
	if err := insert(5, "", ""); err != nil {
		t.Errorf("second unidentified row was refused: %v", err)
	}
}

// The ref rung must come before the tmdb rung in both upserts. Reversed, a
// re-fetched title matches on its legacy id and its ref is never consulted —
// which works only for as long as every provider writes a tmdb_id.
func TestUpsertMatchesOnProviderRefFirst(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	// A row identified by a provider that writes no legacy id at all: only the
	// ref rung can find it again.
	sr := &core.Series{Provider: "anilist", ProviderRef: "21", Title: "One Piece",
		SortTitle: "one piece", Kind: core.SeriesKindTV}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	again := &core.Series{Provider: "anilist", ProviderRef: "21", Title: "One Piece",
		SortTitle: "one piece", Kind: core.SeriesKindTV}
	if err := st.UpsertSeries(ctx, again); err != nil {
		t.Fatalf("UpsertSeries refresh: %v", err)
	}
	if again.ID != sr.ID {
		t.Errorf("refresh landed on series %d, want %d — the ref rung did not match",
			again.ID, sr.ID)
	}

	mv := &core.Movie{Provider: "anilist", ProviderRef: "431", Title: "Spirited Away",
		SortTitle: "spirited away"}
	if err := st.UpsertMovie(ctx, mv); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	mvAgain := &core.Movie{Provider: "anilist", ProviderRef: "431", Title: "Spirited Away",
		SortTitle: "spirited away"}
	if err := st.UpsertMovie(ctx, mvAgain); err != nil {
		t.Fatalf("UpsertMovie refresh: %v", err)
	}
	if mvAgain.ID != mv.ID {
		t.Errorf("refresh landed on movie %d, want %d — the ref rung did not match",
			mvAgain.ID, mv.ID)
	}
}

// A caller written before 0024 sets only the legacy id. The write door fills
// the identity in, so "every matched row carries a ref" is a property of the
// table rather than a habit of its callers.
func TestUpsertNormalizesLegacyIdentityAtTheWriteDoor(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	mv := &core.Movie{TMDBID: 603, Title: "The Matrix", SortTitle: "matrix", Year: 1999}
	if err := st.UpsertMovie(ctx, mv); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	byRef, err := st.GetMovieByProviderRef(ctx, core.ProviderTMDB, "603")
	if err != nil {
		t.Fatalf("GetMovieByProviderRef: %v", err)
	}
	if byRef.ID != mv.ID {
		t.Errorf("movie by ref = %d, want %d", byRef.ID, mv.ID)
	}

	tv := &core.Series{TMDBID: 1399, Title: "Game of Thrones", SortTitle: "game of thrones",
		Kind: core.SeriesKindTV}
	if err := st.UpsertSeries(ctx, tv); err != nil {
		t.Fatalf("UpsertSeries(tv): %v", err)
	}
	if tv.Provider != core.ProviderTMDB || tv.ProviderRef != "1399" {
		t.Errorf("tv identity = %q/%q, want tmdb/1399", tv.Provider, tv.ProviderRef)
	}

	enableAdultLibrary(t, st)
	site := &core.Series{StashID: "uuid-site", Title: "Some Site", SortTitle: "some site",
		Kind: core.SeriesKindAdult}
	if err := st.UpsertSeries(ctx, site); err != nil {
		t.Fatalf("UpsertSeries(adult): %v", err)
	}
	if site.Provider != core.ProviderStashbox || site.ProviderRef != "uuid-site" {
		t.Errorf("site identity = %q/%q, want stashbox/uuid-site", site.Provider, site.ProviderRef)
	}
	byRef2, err := st.GetSeriesByProviderRef(ctx, core.ProviderStashbox, "uuid-site")
	if err != nil {
		t.Fatalf("GetSeriesByProviderRef: %v", err)
	}
	if byRef2.ID != site.ID {
		t.Errorf("site by ref = %d, want %d", byRef2.ID, site.ID)
	}

	// A blank ref matches nothing rather than matching every unidentified row.
	if _, err := st.GetMovieByProviderRef(ctx, core.ProviderTMDB, ""); err == nil {
		t.Error("GetMovieByProviderRef(blank) found a row")
	}
	if _, err := st.GetSeriesByProviderRef(ctx, core.ProviderStashbox, ""); err == nil {
		t.Error("GetSeriesByProviderRef(blank) found a row")
	}
}

// The two library columns are written from one place, so they cannot disagree:
// a caller that sets only the head gets a chain, and a caller that sets only
// the chain gets a head that is its first entry.
func TestLibraryChainAndHeadStayInSync(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	headOnly := &core.Library{Kind: core.LibraryKindTV, Name: "Anime",
		RootPath: "library/Anime", Provider: core.ProviderTMDB}
	if err := st.CreateLibrary(ctx, headOnly); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	got, err := st.GetLibrary(ctx, headOnly.ID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	if want := []string{core.ProviderTMDB}; !reflect.DeepEqual(got.Providers, want) {
		t.Errorf("chain from a head-only create = %v, want %v", got.Providers, want)
	}

	got.Providers = []string{"anilist", core.ProviderTMDB}
	got.Provider = core.ProviderTMDB // deliberately contradicts the chain's head
	if err := st.UpdateLibrary(ctx, got); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	after, err := st.GetLibrary(ctx, headOnly.ID)
	if err != nil {
		t.Fatalf("GetLibrary after update: %v", err)
	}
	if after.Provider != "anilist" {
		t.Errorf("head = %q, want the chain's first entry %q", after.Provider, "anilist")
	}
	if want := []string{"anilist", core.ProviderTMDB}; !reflect.DeepEqual(after.Providers, want) {
		t.Errorf("chain = %v, want %v", after.Providers, want)
	}
	if !reflect.DeepEqual(after.ProviderChain(), after.Providers) {
		t.Errorf("ProviderChain() = %v, want %v", after.ProviderChain(), after.Providers)
	}
}
