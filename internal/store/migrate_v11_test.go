package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/core"
	storemigrations "github.com/watzon/caravan/internal/store/migrations"
)

// openAtVersionTen builds a real pre-0011 database: Goose applied up to 10 and
// no further. It is the migrate_v5/migrate_v8 pattern, and it is the only honest
// way to test an upgrade — a current database edited backwards would prove
// nothing about the statements 0011 actually runs.
func openAtVersionTen(t *testing.T) (*sql.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "v10.sqlite")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	provider, err := migrationProvider(db, storemigrations.FS())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), 10); err != nil {
		t.Fatalf("apply v10: %v", err)
	}
	return db, path
}

// A fresh install ends up with the four first-class libraries, and each one
// arrives with the flags the shelf depends on. Movies and Series are on, Anime
// and Adult are dormant until an owner says otherwise, and every one of them is
// its kind's default so a by-kind lookup always has an answer.
func TestMigrationElevenSeedsFourLibraries(t *testing.T) {
	st, _ := openTemp(t)

	libs, err := st.ListLibraries(t.Context())
	if err != nil {
		t.Fatalf("ListLibraries: %v", err)
	}
	if len(libs) != 4 {
		t.Fatalf("libraries = %+v, want four seeded rows", libs)
	}
	want := []core.Library{
		{ID: 1, Kind: core.LibraryKindMovie, Name: "Movies", RootPath: "library/Movies",
			DLNAVisible: true, Provider: core.ProviderTMDB, Providers: []string{core.ProviderTMDB},
			IsDefault: true, Active: true},
		{ID: 2, Kind: core.LibraryKindTV, Name: "Series", RootPath: "library/TV",
			DLNAVisible: true, Provider: core.ProviderTMDB, Providers: []string{core.ProviderTMDB},
			IsDefault: true, Active: true},
		{ID: 3, Kind: core.LibraryKindAnime, Name: "Anime", RootPath: "library/Anime",
			DLNAVisible: true, Provider: core.ProviderAniList, Providers: []string{core.ProviderAniList},
			IsDefault: true, Active: false},
		{ID: 4, Kind: core.LibraryKindAdult, Name: "Adult", RootPath: "library/Adult",
			DLNAVisible: false, Provider: core.ProviderStashbox, Providers: []string{core.ProviderStashbox},
			IsDefault: true, Active: false, Restricted: true},
	}
	for i, w := range want {
		got := libs[i]
		if got.ID != w.ID || got.Kind != w.Kind || got.Name != w.Name || got.RootPath != w.RootPath ||
			got.DLNAVisible != w.DLNAVisible || got.Provider != w.Provider ||
			got.IsDefault != w.IsDefault || got.Active != w.Active || got.Restricted != w.Restricted {
			t.Errorf("library %d = %+v, want %+v", i, got, w)
		}
		if got.Icon != "" {
			t.Errorf("library %d icon = %q, want empty (the kind default)", i, got.Icon)
		}
	}
}

// The upgrade a real install runs. The pre-0011 database already holds the two
// things the seeds can collide with — a hand-made tv-kind library rooted at
// 'library/Anime', and an adult library of the owner's own — plus the child rows
// the table rebuilds must not destroy.
//
// This is the whole risk of 0011 in one test: `foreign_keys` is on, so a naive
// create-copy-drop-rename of `libraries` or `series` would silently cascade away
// the indexer overrides, the access grants, the seasons, the episodes and the
// episode-file links.
func TestMigrationElevenUpgradesWithoutLosingAnything(t *testing.T) {
	ctx := context.Background()
	db, path := openAtVersionTen(t)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO users (id, username, password_hash, role, created_at, updated_at)
		VALUES (7, 'housemate', 'x', 'member', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
		INSERT INTO indexers (id, name, protocol, url, api_key, categories, enabled, created_at, updated_at)
		VALUES (5, 'jackett', 'torznab', 'http://jackett.test', 'k', '[5000]', 1,
		        '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');

		-- The hand-made shelf that owns the seed's preferred root path.
		INSERT INTO libraries (id, kind, name, root_path, dlna_visible, provider, providers,
		                       is_default, active, restricted)
		VALUES (3, 'tv', 'Anime', 'library/Anime', 1, 'tmdb', '["tmdb"]', 0, 1, 0),
		       (4, 'adult', 'Scenes', 'library/Scenes', 0, 'stashbox', '["stashbox"]', 1, 1, 1);

		INSERT INTO library_indexers (library_id, indexer_id, enabled, categories)
		VALUES (3, 5, 0, '[5070]');
		INSERT INTO library_access (library_id, user_id) VALUES (4, 7);

		INSERT INTO series (id, kind, title, added_at, updated_at, library_id)
		VALUES (11, 'tv', 'Frieren', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 3);
		INSERT INTO seasons (id, series_id, season_number) VALUES (21, 11, 1);
		INSERT INTO episodes (id, series_id, season_number, episode_number, title)
		VALUES (31, 11, 1, 1, 'The Journey''s End');
		INSERT INTO media_files (id, path, size, added_at, modified_at)
		VALUES (41, 'library/Anime/Frieren/S01E01.mkv', 10,
		        '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
		INSERT INTO episode_files (episode_id, media_file_id) VALUES (31, 41);
	`); err != nil {
		t.Fatalf("seed a v10 install: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open and migrate a v10 install: %v", err)
	}
	defer st.Close()
	version, err := st.SchemaVersion()
	if err != nil || int64(version) != storemigrations.LatestVersion {
		t.Fatalf("schema version = %d err = %v, want %d", version, err, storemigrations.LatestVersion)
	}

	// The pre-existing rows are untouched, ids included.
	handmade, err := st.GetLibrary(ctx, 3)
	if err != nil {
		t.Fatalf("GetLibrary(3): %v", err)
	}
	if handmade.Kind != core.LibraryKindTV || handmade.RootPath != "library/Anime" || !handmade.Active {
		t.Errorf("hand-made library = %+v, want it unchanged", *handmade)
	}

	// The children of both rebuilt tables survived.
	overrides, err := st.ListLibraryIndexers(ctx, 3)
	if err != nil {
		t.Fatalf("ListLibraryIndexers: %v", err)
	}
	if len(overrides) != 1 || overrides[0].Enabled || len(overrides[0].Categories) != 1 {
		t.Errorf("library indexer overrides = %+v, want the one stored row", overrides)
	}
	granted, err := st.ListLibraryAccess(ctx, 4)
	if err != nil {
		t.Fatalf("ListLibraryAccess: %v", err)
	}
	if len(granted) != 1 || granted[0] != 7 {
		t.Errorf("library access = %v, want the one grant", granted)
	}
	seasons, err := st.ListSeasons(ctx, 11)
	if err != nil {
		t.Fatalf("ListSeasons: %v", err)
	}
	if len(seasons) != 1 {
		t.Errorf("seasons = %+v, want the one stored row", seasons)
	}
	pairs, err := st.ListEpisodeMediaFilesForSeries(ctx, 11)
	if err != nil {
		t.Fatalf("ListEpisodeMediaFilesForSeries: %v", err)
	}
	if len(pairs) != 1 || pairs[0].EpisodeID != 31 || pairs[0].File.ID != 41 {
		t.Errorf("episode files = %+v, want the one stored link", pairs)
	}

	// The anime seed took the fallback root rather than violating UNIQUE, and
	// the adult seed did not run at all: the install already had that shelf.
	anime, err := st.GetLibraryByKind(ctx, core.LibraryKindAnime)
	if err != nil {
		t.Fatalf("GetLibraryByKind(anime): %v", err)
	}
	if anime.RootPath != "library/Anime (default)" || anime.Active {
		t.Errorf("seeded anime library = %+v, want the fallback root and dormant", *anime)
	}
	adultLibs, err := st.ListLibrariesByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("ListLibrariesByKind(adult): %v", err)
	}
	if len(adultLibs) != 1 || adultLibs[0].Name != "Scenes" {
		t.Errorf("adult libraries = %+v, want only the install's own", adultLibs)
	}

	// And the widened CHECKs actually admit the new kind on both tables.
	lib := &core.Library{Kind: core.LibraryKindAnime, Name: "Second Anime",
		RootPath: "library/Anime 2", Providers: []string{core.ProviderAniList}}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		t.Fatalf("CreateLibrary(anime): %v", err)
	}
	sr := &core.Series{Kind: core.SeriesKindAnime, Title: "Bocchi the Rock!", LibraryID: lib.ID}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries(anime): %v", err)
	}
}

// The fallback spelling occupied and the preferred one free. The seed must take
// the path it prefers rather than reading "the fallback is gone" as "there is
// nowhere to put this": the suffixed root is a way OUT of a collision, not a
// second condition for seeding at all.
func TestMigrationElevenSeedsThePreferredRootWhenOnlyTheFallbackIsTaken(t *testing.T) {
	ctx := context.Background()
	db, path := openAtVersionTen(t)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, kind, name, root_path, dlna_visible, provider, providers,
		                       is_default, active, restricted)
		VALUES (3, 'tv', 'Odds and ends', 'library/Anime (default)', 1, 'tmdb', '["tmdb"]', 0, 1, 0);
	`); err != nil {
		t.Fatalf("seed a v10 install: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open and migrate a v10 install: %v", err)
	}
	defer st.Close()

	anime, err := st.GetLibraryByKind(ctx, core.LibraryKindAnime)
	if err != nil {
		t.Fatalf("GetLibraryByKind(anime): %v", err)
	}
	if anime.RootPath != "library/Anime" {
		t.Errorf("seeded anime root = %q, want the preferred path: only the fallback was taken",
			anime.RootPath)
	}
}

// Both spellings occupied. The migration must still apply — a refused upgrade is
// a worse answer than a missing optional shelf — and the kind is then simply
// absent, which is what the startup warning in `caravan serve` reports.
func TestMigrationElevenSkipsTheAnimeSeedWhenBothRootsAreTaken(t *testing.T) {
	ctx := context.Background()
	db, path := openAtVersionTen(t)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO libraries (id, kind, name, root_path, dlna_visible, provider, providers,
		                       is_default, active, restricted)
		VALUES (3, 'tv', 'Anime', 'library/Anime', 1, 'tmdb', '["tmdb"]', 0, 1, 0),
		       (4, 'tv', 'Anime again', 'library/Anime (default)', 1, 'tmdb', '["tmdb"]', 0, 1, 0);
	`); err != nil {
		t.Fatalf("seed a v10 install: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open and migrate a v10 install: %v", err)
	}
	defer st.Close()

	version, err := st.SchemaVersion()
	if err != nil || int64(version) != storemigrations.LatestVersion {
		t.Fatalf("schema version = %d err = %v, want %d", version, err, storemigrations.LatestVersion)
	}
	animeLibs, err := st.ListLibrariesByKind(ctx, core.LibraryKindAnime)
	if err != nil {
		t.Fatalf("ListLibrariesByKind(anime): %v", err)
	}
	if len(animeLibs) != 0 {
		t.Errorf("anime libraries = %+v, want none: both candidate roots were taken", animeLibs)
	}
	// The adult seed is independent of the anime one and must still have run.
	if _, err := st.GetLibraryByKind(ctx, core.LibraryKindAdult); err != nil {
		t.Errorf("GetLibraryByKind(adult): %v, want the adult seed to be unaffected", err)
	}
}

// The cascade the rebuild has to leave working. A test that only counted rows
// after the upgrade would pass on a schema whose foreign keys had quietly been
// rewritten to point at a staging table that no longer exists.
func TestMigrationElevenLeavesTheSeriesCascadeIntact(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	sr := &core.Series{Kind: core.SeriesKindTV, Title: "Frieren"}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := st.UpsertSeason(ctx, &core.Season{SeriesID: sr.ID, Number: 1}); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	if err := st.DeleteSeries(ctx, sr.ID); err != nil {
		t.Fatalf("DeleteSeries: %v", err)
	}
	seasons, err := st.ListSeasons(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListSeasons: %v", err)
	}
	if len(seasons) != 0 {
		t.Errorf("seasons after deleting the series = %+v, want the cascade to have cleared them", seasons)
	}

	// An orphan must still be refused outright, which is the other half of a
	// live foreign key.
	if _, err := st.DB().ExecContext(ctx,
		"INSERT INTO seasons (series_id, season_number) VALUES (999999, 1)"); err == nil {
		t.Error("an orphan season was accepted, want the foreign key to refuse it")
	}
}

// The backfill. A v10 install could file an item under no library at all —
// `library_id = 0` meant "resolve me through my kind's default", and every
// reader carried a branch for it — so 0011 spends that meaning once and stamps
// the rows. Everything downstream (the visibility gate, the DLNA tree, the RSS
// matcher, the upsert heal) then resolves ownership by id alone.
//
// The adult site is the case that has to run LAST: its shelf is one 0011 itself
// seeds, so a backfill placed before the seeds would leave it homeless.
func TestMigrationElevenStampsRowsThatNameNoLibrary(t *testing.T) {
	ctx := context.Background()
	db, path := openAtVersionTen(t)

	if _, err := db.ExecContext(ctx, `
		INSERT INTO movies (id, tmdb_id, title, added_at, updated_at, library_id)
		VALUES (11, 603, 'The Matrix', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 0);

		INSERT INTO series (id, kind, title, added_at, updated_at, library_id)
		VALUES (21, 'tv', 'Planet Earth II', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 0),
		       (22, 'adult', 'Brazzers', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 0);

		-- A row that already named its shelf must be left exactly where it is:
		-- the backfill is a stamp on the unstamped, not a re-file.
		INSERT INTO libraries (id, kind, name, root_path, dlna_visible, provider, providers,
		                       is_default, active, restricted)
		VALUES (3, 'movie', 'Kids', 'library/Kids', 1, 'tmdb', '["tmdb"]', 0, 1, 0);
		INSERT INTO movies (id, tmdb_id, title, added_at, updated_at, library_id)
		VALUES (12, 12477, 'Grave of the Fireflies', '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z', 3);
	`); err != nil {
		t.Fatalf("seed a v10 install: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open and migrate a v10 install: %v", err)
	}
	defer st.Close()

	wantDefault := func(kind string) int64 {
		t.Helper()
		lib, err := st.GetDefaultLibrary(ctx, kind)
		if err != nil {
			t.Fatalf("GetDefaultLibrary(%s): %v", kind, err)
		}
		return lib.ID
	}

	matrix, err := st.GetMovie(ctx, 11)
	if err != nil {
		t.Fatalf("GetMovie(11): %v", err)
	}
	if matrix.LibraryID != wantDefault(core.LibraryKindMovie) {
		t.Errorf("unstamped movie library_id = %d, want the movie default %d",
			matrix.LibraryID, wantDefault(core.LibraryKindMovie))
	}

	fireflies, err := st.GetMovie(ctx, 12)
	if err != nil {
		t.Fatalf("GetMovie(12): %v", err)
	}
	if fireflies.LibraryID != 3 {
		t.Errorf("already-stamped movie library_id = %d, want its own shelf 3", fireflies.LibraryID)
	}

	// Series are stamped per kind, because `series.kind` is what says which
	// shelf answers for a row: a site must not land on the television shelf.
	for _, c := range []struct {
		id   int64
		kind string
	}{
		{21, core.LibraryKindTV},
		{22, core.LibraryKindAdult},
	} {
		sr, err := st.GetSeries(ctx, c.id)
		if err != nil {
			t.Fatalf("GetSeries(%d): %v", c.id, err)
		}
		if sr.LibraryID != wantDefault(c.kind) {
			t.Errorf("series %d library_id = %d, want the %s default %d",
				c.id, sr.LibraryID, c.kind, wantDefault(c.kind))
		}
	}

	// Nothing is left behind: the whole point is that no reader downstream has
	// to carry a zero branch any more.
	var zeros int
	if err := st.DB().QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM movies WHERE library_id = 0)
		     + (SELECT COUNT(*) FROM series WHERE library_id = 0)`).Scan(&zeros); err != nil {
		t.Fatalf("count unstamped rows: %v", err)
	}
	if zeros != 0 {
		t.Errorf("%d item rows still name no library after 0011", zeros)
	}
}
