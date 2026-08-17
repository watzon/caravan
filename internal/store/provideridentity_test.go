package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

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

	headOnly := &core.Library{Kind: core.LibraryKindTV, Name: "Kids",
		RootPath: "library/Kids", Provider: core.ProviderTMDB}
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
