-- +goose Up

-- First-class libraries: a per-library icon, the unified `anime` kind, and the
-- four shelves every install now starts with.
--
-- Two tables carry a CHECK that names the kinds they accept, and SQLite cannot
-- alter a CHECK. Both are therefore rebuilt below, and both rebuilds are shaped
-- by one constraint that the obvious create-copy-drop-rename recipe gets wrong:
-- `foreign_keys` is ON for this connection and PRAGMA cannot turn it off inside
-- a transaction, so DROP TABLE performs an implicit DELETE that fires every
-- ON DELETE CASCADE pointing at the table. Dropping `libraries` that way would
-- silently take every per-library indexer override and every access grant with
-- it; dropping `series` would take every season, episode and episode-file link.
--
-- So each rebuild parks its dependants in a staging table, empties them, drops
-- and recreates the parent, and puts them back. The rows are restored with their
-- own ids, so nothing that references them by id has to be rewritten, and
-- sqlite_sequence re-learns its high-water mark from the largest id inserted.

------------------------------------------------------------------------------
-- 1. libraries: widen the kind CHECK and add `icon`.
------------------------------------------------------------------------------

CREATE TABLE _0011_libraries AS SELECT * FROM libraries;
CREATE TABLE _0011_library_indexers AS SELECT * FROM library_indexers;
CREATE TABLE _0011_library_access AS SELECT * FROM library_access;

DELETE FROM library_indexers;
DELETE FROM library_access;
DROP TABLE libraries;

CREATE TABLE libraries (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    kind               TEXT NOT NULL CHECK (kind IN ('movie', 'tv', 'anime', 'adult')),
    name               TEXT NOT NULL,
    icon               TEXT NOT NULL DEFAULT '',
    root_path          TEXT NOT NULL UNIQUE,
    dlna_visible       INTEGER NOT NULL DEFAULT 1,
    route_torrent      TEXT,
    route_usenet       TEXT,
    quality_profile_id INTEGER,
    provider           TEXT NOT NULL DEFAULT '',
    providers          TEXT NOT NULL DEFAULT '',
    is_default         INTEGER NOT NULL DEFAULT 0,
    active             INTEGER NOT NULL DEFAULT 1,
    restricted         INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX idx_libraries_default_per_kind ON libraries (kind)
    WHERE is_default = 1;

INSERT INTO libraries (
    id, kind, name, root_path, dlna_visible, route_torrent, route_usenet,
    quality_profile_id, provider, providers, is_default, active, restricted
)
SELECT id, kind, name, root_path, dlna_visible, route_torrent, route_usenet,
       quality_profile_id, provider, providers, is_default, active, restricted
FROM _0011_libraries;

INSERT INTO library_indexers (library_id, indexer_id, enabled, categories)
SELECT library_id, indexer_id, enabled, categories FROM _0011_library_indexers;

INSERT INTO library_access (library_id, user_id)
SELECT library_id, user_id FROM _0011_library_access;

DROP TABLE _0011_library_access;
DROP TABLE _0011_library_indexers;
DROP TABLE _0011_libraries;

------------------------------------------------------------------------------
-- 2. series: admit the `anime` kind beside `tv` and `adult`.
------------------------------------------------------------------------------

CREATE TABLE _0011_series AS SELECT * FROM series;
CREATE TABLE _0011_seasons AS SELECT * FROM seasons;
CREATE TABLE _0011_episodes AS SELECT * FROM episodes;
CREATE TABLE _0011_episode_files AS SELECT * FROM episode_files;

-- Emptying `episodes` cascades `episode_files` away, which is why it was parked
-- above: it hangs off the episodes rather than off the series.
DELETE FROM episodes;
DELETE FROM seasons;
DROP TABLE series;

CREATE TABLE series (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    provider           TEXT NOT NULL DEFAULT '',
    provider_ref       TEXT NOT NULL DEFAULT '',
    tmdb_id            INTEGER NOT NULL DEFAULT 0,
    tvdb_id            INTEGER NOT NULL DEFAULT 0,
    imdb_id            TEXT NOT NULL DEFAULT '',
    kind               TEXT NOT NULL DEFAULT 'tv' CHECK (kind IN ('tv', 'anime', 'adult')),
    title              TEXT NOT NULL,
    sort_title         TEXT NOT NULL DEFAULT '',
    year               INTEGER NOT NULL DEFAULT 0,
    overview           TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT '',
    path               TEXT NOT NULL DEFAULT '',
    poster_path        TEXT NOT NULL DEFAULT '',
    poster_url         TEXT NOT NULL DEFAULT '',
    monitored          INTEGER NOT NULL DEFAULT 1,
    quality_profile_id INTEGER NOT NULL DEFAULT 0,
    first_aired        TEXT NOT NULL DEFAULT '',
    stash_id           TEXT NOT NULL DEFAULT '',
    added_at           TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    library_id         INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX idx_series_provider_ref ON series (provider, provider_ref)
    WHERE provider_ref != '';
CREATE UNIQUE INDEX idx_series_tmdb_id ON series (tmdb_id) WHERE tmdb_id > 0;
CREATE INDEX idx_series_stash_id ON series (stash_id) WHERE stash_id != '';
CREATE INDEX idx_series_sort_title ON series (sort_title);
CREATE INDEX idx_series_library ON series (library_id);

INSERT INTO series (
    id, provider, provider_ref, tmdb_id, tvdb_id, imdb_id, kind, title, sort_title,
    year, overview, status, path, poster_path, poster_url, monitored,
    quality_profile_id, first_aired, stash_id, added_at, updated_at, library_id
)
SELECT id, provider, provider_ref, tmdb_id, tvdb_id, imdb_id, kind, title, sort_title,
       year, overview, status, path, poster_path, poster_url, monitored,
       quality_profile_id, first_aired, stash_id, added_at, updated_at, library_id
FROM _0011_series;

INSERT INTO seasons (
    id, series_id, season_number, title, overview, poster_path, air_date, monitored
)
SELECT id, series_id, season_number, title, overview, poster_path, air_date, monitored
FROM _0011_seasons;

INSERT INTO episodes (
    id, series_id, season_number, episode_number, absolute_number, tmdb_id,
    stash_id, title, overview, air_date, monitored, scene
)
SELECT id, series_id, season_number, episode_number, absolute_number, tmdb_id,
       stash_id, title, overview, air_date, monitored, scene
FROM _0011_episodes;

INSERT INTO episode_files (episode_id, media_file_id)
SELECT episode_id, media_file_id FROM _0011_episode_files;

DROP TABLE _0011_episode_files;
DROP TABLE _0011_episodes;
DROP TABLE _0011_seasons;
DROP TABLE _0011_series;

------------------------------------------------------------------------------
-- 3. Seed the two shelves that were never seeded: Anime and Adult.
------------------------------------------------------------------------------
--
-- Both are seeded INACTIVE. A dormant library is invisible to everyone, admins
-- included (core.LibraryVisible), advertises no DLNA container and runs no
-- background work, so an install that never asked for anime or for adult content
-- is unchanged by their existence — while an owner who wants one flips `active`
-- in Libraries settings instead of building a library from scratch.
--
-- Both inserts are idempotent on the KIND, because an upgrading install may
-- already hold a library of either: the adult module's own library, or a
-- hand-made anime shelf. `is_default = 1` is therefore only ever written when
-- the kind has no row at all, so the partial unique index cannot be violated.
--
-- The root path is the one thing that can already be taken by a library of
-- ANOTHER kind — a user's hand-made tv-kind 'library/Anime' is the case this was
-- written for — so it falls back to a suffixed path. If both spellings are taken
-- the seed is skipped rather than failing the migration: a missing optional shelf
-- is a thing an admin can create by hand, and a refused upgrade is not.
--
-- The last guard asks whether the path the CASE just chose is free, rather than
-- whether the fallback spelling is: an install that had taken only
-- 'library/Anime (default)' would otherwise lose a seed whose preferred path was
-- never occupied. Repeating the CASE is the price of SQLite having no way to
-- name the expression once. `caravan serve` warns on a kind that ends up with no
-- library at all, which is the only way this skip can show itself.

INSERT INTO libraries (
    kind, name, icon, root_path, dlna_visible, provider, providers,
    is_default, active, restricted
)
SELECT 'anime', 'Anime', '',
       CASE WHEN EXISTS (SELECT 1 FROM libraries WHERE root_path = 'library/Anime')
            THEN 'library/Anime (default)' ELSE 'library/Anime' END,
       1, 'anilist', '["anilist"]', 1, 0, 0
WHERE NOT EXISTS (SELECT 1 FROM libraries WHERE kind = 'anime')
  AND NOT EXISTS (
      SELECT 1 FROM libraries WHERE root_path =
          CASE WHEN EXISTS (SELECT 1 FROM libraries WHERE root_path = 'library/Anime')
               THEN 'library/Anime (default)' ELSE 'library/Anime' END
  );

INSERT INTO libraries (
    kind, name, icon, root_path, dlna_visible, provider, providers,
    is_default, active, restricted
)
SELECT 'adult', 'Adult', '',
       CASE WHEN EXISTS (SELECT 1 FROM libraries WHERE root_path = 'library/Adult')
            THEN 'library/Adult (default)' ELSE 'library/Adult' END,
       0, 'stashbox', '["stashbox"]', 1, 0, 1
WHERE NOT EXISTS (SELECT 1 FROM libraries WHERE kind = 'adult')
  AND NOT EXISTS (
      SELECT 1 FROM libraries WHERE root_path =
          CASE WHEN EXISTS (SELECT 1 FROM libraries WHERE root_path = 'library/Adult')
               THEN 'library/Adult (default)' ELSE 'library/Adult' END
  );

------------------------------------------------------------------------------
-- 4. Stamp every item row that still names no library.
------------------------------------------------------------------------------
--
-- `library_id = 0` used to mean "resolve me through my kind's default", and every
-- reader carried a branch for it: the visibility gate, the DLNA tree, the RSS
-- match, the upsert heal. One meaning held in nine places is nine chances to
-- disagree, and the anime kind made the disagreement real — an anime library
-- holds films, so a film naming no library belonged to two kinds' defaults at
-- once and hung under two DLNA containers.
--
-- So the meaning is spent here, once, and the readers lose their branch: after
-- this migration a movie or series row names its shelf outright. This runs LAST
-- because the shelves it stamps rows onto include the two the section above
-- seeds — an adult site with no library_id has nowhere to go until the adult
-- library exists.
--
-- The subselects reproduce store.GetLibraryByKind's ordering (is_default first,
-- then id) rather than filtering on is_default alone: that is the row a by-kind
-- lookup would have answered with, so the stamp lands exactly where the readers
-- being deleted used to resolve to. The EXISTS guard leaves a row at 0 when its
-- kind has no library at all — a shelf that does not exist is not a place to
-- file anything, and `caravan serve` already warns about the kind.
--
-- Series are stamped per kind because `series.kind` is what says which shelf
-- answers for a row (core.LibraryKindForSeries). A v10 database can only hold
-- 'tv' and 'adult' — the anime kind arrives in this same migration — but the
-- third statement is written anyway: it costs one no-op UPDATE and it is the
-- statement a reader looks for when asking whether every kind was covered.
--
-- UPDATEs do not disturb the v11 schema fingerprint: store.schemaFingerprint
-- hashes sqlite_master's type/name/tbl_name/sql only, which is DDL and holds no
-- row data.

UPDATE movies
SET library_id = (
    SELECT id FROM libraries WHERE kind = 'movie'
    ORDER BY is_default DESC, id ASC LIMIT 1
)
WHERE library_id = 0
  AND EXISTS (SELECT 1 FROM libraries WHERE kind = 'movie');

UPDATE series
SET library_id = (
    SELECT id FROM libraries WHERE kind = 'tv'
    ORDER BY is_default DESC, id ASC LIMIT 1
)
WHERE library_id = 0 AND kind = 'tv'
  AND EXISTS (SELECT 1 FROM libraries WHERE kind = 'tv');

UPDATE series
SET library_id = (
    SELECT id FROM libraries WHERE kind = 'anime'
    ORDER BY is_default DESC, id ASC LIMIT 1
)
WHERE library_id = 0 AND kind = 'anime'
  AND EXISTS (SELECT 1 FROM libraries WHERE kind = 'anime');

UPDATE series
SET library_id = (
    SELECT id FROM libraries WHERE kind = 'adult'
    ORDER BY is_default DESC, id ASC LIMIT 1
)
WHERE library_id = 0 AND kind = 'adult'
  AND EXISTS (SELECT 1 FROM libraries WHERE kind = 'adult');
