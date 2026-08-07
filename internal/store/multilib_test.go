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

// 0022 rebuilds `libraries` again — the same cascade trap 0013 documents — and
// backfills ownership onto every media row. An upgraded install must come out
// the other side describing exactly what it was already doing: same library
// rows with their edits, same overrides, every item owned by the library its
// kind used to imply, and the adult enable still idempotent.
func TestMigrate0022PreservesExistingInstall(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "caravan.db")
	openAtSchemaVersion(t, path, 21)

	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("open seeded database: %v", err)
	}
	exec(t, db, `INSERT INTO indexers (id, name, protocol, url, api_key, categories,
		enabled, created_at, updated_at)
		VALUES (7, 'Nzbee', 'newznab', 'http://nzb.example', 'k', '[5000]', 1,
		        '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`)
	exec(t, db, `UPDATE libraries SET route_torrent = 'embedded' WHERE kind = 'tv'`)
	exec(t, db, `INSERT INTO library_indexers (library_id, indexer_id, enabled, categories)
		VALUES ((SELECT id FROM libraries WHERE kind = 'tv'), 7, 0, '[5030,5040]')`)
	// The adult row as pre-0022 SetAdultEnabled created it.
	exec(t, db, `INSERT INTO libraries (kind, name, root_path, dlna_visible)
		VALUES ('adult', 'Adult', 'library/Adult', 0)`)
	exec(t, db, `INSERT INTO settings (key, value, updated_at)
		VALUES ('adult_enabled', 'true', '2024-01-01T00:00:00Z')`)

	exec(t, db, `INSERT INTO movies (id, tmdb_id, title, sort_title, year, added_at, updated_at)
		VALUES (5, 603, 'The Matrix', 'matrix', 1999,
		        '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`)
	exec(t, db, `INSERT INTO series (id, kind, tmdb_id, title, sort_title, year, added_at, updated_at)
		VALUES (11, 'tv', 1399, 'Game of Thrones', 'game of thrones', 2011,
		        '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`)
	exec(t, db, `INSERT INTO series (id, kind, stash_id, title, sort_title, year, added_at, updated_at)
		VALUES (12, 'adult', 'uuid-site', 'Some Site', 'some site', 0,
		        '2024-01-01T00:00:00Z', '2024-01-01T00:00:00Z')`)
	if err := db.Close(); err != nil {
		t.Fatalf("close seeded database: %v", err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after upgrade: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	// Libraries kept their ids and edits, and gained the provider their kind
	// always used plus the default flag — each was the only one of its kind.
	libs, err := st.ListLibraries(ctx)
	if err != nil {
		t.Fatalf("ListLibraries: %v", err)
	}
	wantLibs := []core.Library{
		{ID: 1, Kind: core.LibraryKindMovie, Name: "Movies", RootPath: "library/Movies",
			DLNAVisible: true, Provider: core.ProviderTMDB,
			Providers: []string{core.ProviderTMDB}, IsDefault: true},
		{ID: 2, Kind: core.LibraryKindTV, Name: "Series", RootPath: "library/TV",
			DLNAVisible: true, RouteTorrent: "embedded", Provider: core.ProviderTMDB,
			Providers: []string{core.ProviderTMDB}, IsDefault: true},
		{ID: 3, Kind: core.LibraryKindAdult, Name: "Adult", RootPath: "library/Adult",
			DLNAVisible: false, Provider: core.ProviderStashbox,
			Providers: []string{core.ProviderStashbox}, IsDefault: true},
	}
	if len(libs) != len(wantLibs) {
		t.Fatalf("ListLibraries = %+v, want %+v", libs, wantLibs)
	}
	for i := range wantLibs {
		if !reflect.DeepEqual(libs[i], wantLibs[i]) {
			t.Errorf("library[%d] = %+v, want %+v", i, libs[i], wantLibs[i])
		}
	}

	// The override the cascade would have eaten.
	overrides, err := st.ListLibraryIndexers(ctx, 2)
	if err != nil {
		t.Fatalf("ListLibraryIndexers(2): %v", err)
	}
	if len(overrides) != 1 || overrides[0].IndexerID != 7 || overrides[0].Enabled {
		t.Errorf("library 2 overrides = %+v, want the disabled Nzbee row", overrides)
	}

	// Ownership backfilled from what kind used to imply.
	mv, err := st.GetMovie(ctx, 5)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if mv.LibraryID != 1 {
		t.Errorf("movie library_id = %d, want 1", mv.LibraryID)
	}
	for id, want := range map[int64]int64{11: 2, 12: 3} {
		sr, err := st.GetSeries(ctx, id)
		if err != nil {
			t.Fatalf("GetSeries(%d): %v", id, err)
		}
		if sr.LibraryID != want {
			t.Errorf("series %d library_id = %d, want %d", id, sr.LibraryID, want)
		}
	}

	// Re-enabling the module must reuse the migrated row, not add a second one
	// now that UNIQUE(kind) is gone.
	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	adult, err := st.ListLibrariesByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("ListLibrariesByKind(adult): %v", err)
	}
	if len(adult) != 1 {
		t.Errorf("adult libraries = %+v, want exactly the migrated row", adult)
	}
}

// The schema admits several libraries per kind but exactly one default among
// them, and store-level CRUD keeps every guard: root uniqueness, guarded
// deletion, and the clear-then-set default handoff.
func TestMultipleLibrariesPerKind(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	anime := &core.Library{Kind: core.LibraryKindTV, Name: "Anime",
		RootPath: "library/Anime", DLNAVisible: true, Provider: core.ProviderTMDB}
	if err := st.CreateLibrary(ctx, anime); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	if anime.ID == 0 {
		t.Fatal("CreateLibrary assigned no id")
	}

	// A second root at the same path is the schema's refusal, not a guard
	// anyone has to remember.
	dup := &core.Library{Kind: core.LibraryKindMovie, Name: "Dup",
		RootPath: "library/Anime", Provider: core.ProviderTMDB}
	if err := st.CreateLibrary(ctx, dup); err == nil {
		t.Error("CreateLibrary accepted a duplicate root_path")
	}

	// By-kind lookups keep answering with the seeded default, not the newcomer.
	def, err := st.GetDefaultLibrary(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetDefaultLibrary: %v", err)
	}
	if def.Name != "Series" || !def.IsDefault {
		t.Errorf("default tv library = %+v, want the seeded Series row", def)
	}

	// Items pin the newcomer open: deletion refuses until it is empty.
	sr := &core.Series{TMDBID: 100, Title: "Frieren", Kind: core.SeriesKindTV, LibraryID: anime.ID}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := st.DeleteLibrary(ctx, anime.ID); !errors.Is(err, ErrLibraryNotEmpty) {
		t.Errorf("DeleteLibrary(non-empty) = %v, want ErrLibraryNotEmpty", err)
	}
	if err := st.DeleteSeries(ctx, sr.ID); err != nil {
		t.Fatalf("DeleteSeries: %v", err)
	}

	// The default flag moves transactionally and the old default survives as a
	// plain library; deleting a default is refused however empty it is.
	if err := st.SetDefaultLibrary(ctx, anime.ID); err != nil {
		t.Fatalf("SetDefaultLibrary: %v", err)
	}
	def, err = st.GetDefaultLibrary(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetDefaultLibrary after handoff: %v", err)
	}
	if def.ID != anime.ID {
		t.Errorf("default tv library = %+v, want Anime", def)
	}
	if err := st.DeleteLibrary(ctx, anime.ID); !errors.Is(err, ErrLibraryIsDefault) {
		t.Errorf("DeleteLibrary(default) = %v, want ErrLibraryIsDefault", err)
	}

	// A series bound for the wrong shelf is refused at the write.
	wrong := &core.Series{StashID: "uuid-x", Title: "Nope", Kind: core.SeriesKindAdult, LibraryID: anime.ID}
	if err := st.UpsertSeries(ctx, wrong); err == nil {
		t.Error("UpsertSeries accepted an adult series in a tv library")
	}

	// A refresh that carries no library keeps the stored one.
	sr2 := &core.Series{TMDBID: 200, Title: "Mushishi", Kind: core.SeriesKindTV, LibraryID: anime.ID}
	if err := st.UpsertSeries(ctx, sr2); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	refresh := &core.Series{ID: sr2.ID, TMDBID: 200, Title: "Mushishi", Kind: core.SeriesKindTV}
	if err := st.UpsertSeries(ctx, refresh); err != nil {
		t.Fatalf("UpsertSeries(refresh): %v", err)
	}
	got, err := st.GetSeries(ctx, sr2.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.LibraryID != anime.ID {
		t.Errorf("refresh moved series to library %d, want it kept in %d", got.LibraryID, anime.ID)
	}
}
