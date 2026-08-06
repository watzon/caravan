-- Multiple libraries per kind (plan: multi-library media roots, part A).
--
-- 0012 made libraries rows; this migration makes them plural. `kind` stops
-- being a library's identity — an install may now hold a "Series" library and
-- an "Anime" library, both kind 'tv', each with its own root, provider and
-- overrides — so three things replace the old UNIQUE(kind):
--
--   * `is_default`: exactly one library per kind is the default (partial
--     unique index below). It is what every legacy by-kind lookup resolves to,
--     which is how call sites not yet swept keep answering exactly as before.
--   * `root_path` gains UNIQUE: with kind no longer unique it is the one
--     column that still identifies a library on disk, and it is the constraint
--     store.ensureAdultLibrary's race guard now leans on.
--   * `movies.library_id` / `series.library_id`: ownership becomes a column
--     instead of an inference. Soft references like quality_profile_id — no
--     FK, deletion is guarded in Go, and 0 means "unknown, heal from the
--     item's path on the next rescan" — which keeps SPEC §7's recovery
--     contract (rows reconstructable from a scan) true.
--
-- `provider` names the metadata provider that refreshes the library's items
-- (core.ProviderDescriptor). Backfilled to what each kind hardcoded before
-- this migration existed, so an upgraded install behaves identically.
--
-- The rebuild is 0013's, for 0013's reasons: a CHECK/UNIQUE cannot be altered
-- in place; library_indexers' ON DELETE CASCADE would empty itself when the
-- parent is dropped, so the child is rebuilt too and dropped FIRST; and the
-- foreign_keys pragma is a no-op inside the migration transaction, so
-- rebuilding the pair is the only recipe available.
CREATE TABLE libraries_rebuild (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    kind               TEXT    NOT NULL CHECK (kind IN ('movie', 'tv', 'adult')),
    name               TEXT    NOT NULL,
    root_path          TEXT    NOT NULL UNIQUE,
    dlna_visible       INTEGER NOT NULL DEFAULT 1,
    route_torrent      TEXT,
    route_usenet       TEXT,
    quality_profile_id INTEGER,
    provider           TEXT    NOT NULL DEFAULT '',
    is_default         INTEGER NOT NULL DEFAULT 0
);

-- Every existing row was the only library of its kind, so each is its kind's
-- default, wired to the provider its kind always used.
INSERT INTO libraries_rebuild
    (id, kind, name, root_path, dlna_visible, route_torrent, route_usenet,
     quality_profile_id, provider, is_default)
SELECT id, kind, name, root_path, dlna_visible, route_torrent, route_usenet,
       quality_profile_id,
       CASE kind WHEN 'adult' THEN 'stashbox' ELSE 'tmdb' END,
       1
FROM libraries;

CREATE TABLE library_indexers_rebuild (
    library_id INTEGER NOT NULL REFERENCES libraries_rebuild(id) ON DELETE CASCADE,
    indexer_id INTEGER NOT NULL REFERENCES indexers(id) ON DELETE CASCADE,
    enabled    INTEGER NOT NULL DEFAULT 1,
    categories TEXT,
    PRIMARY KEY (library_id, indexer_id)
);

INSERT INTO library_indexers_rebuild (library_id, indexer_id, enabled, categories)
SELECT library_id, indexer_id, enabled, categories FROM library_indexers;

-- Child first, so the cascade never runs rather than running over rows that
-- have already been copied (0013's hard-won ordering).
DROP TABLE library_indexers;
DROP TABLE libraries;

ALTER TABLE libraries_rebuild        RENAME TO libraries;
ALTER TABLE library_indexers_rebuild RENAME TO library_indexers;

-- One default per kind, enforced where it cannot be forgotten. SetDefault is a
-- transactional clear-then-set in Go; this index is what makes a second
-- default a loud constraint failure instead of a quiet ambiguity.
CREATE UNIQUE INDEX idx_libraries_default_per_kind ON libraries (kind) WHERE is_default = 1;

-- Ownership columns. The backfill keys off what the old mapping inferred:
-- every movie belonged to the movie library, every series to the library its
-- kind mapped to. Guarded with a subquery that returns NULL-coalesced 0 on an
-- install missing the row (never-enabled adult module with a stray adult
-- series cannot happen, but a 0 here is exactly the "heal on rescan" value,
-- not a broken reference).
ALTER TABLE movies ADD COLUMN library_id INTEGER NOT NULL DEFAULT 0;
UPDATE movies SET library_id =
    COALESCE((SELECT id FROM libraries WHERE kind = 'movie' AND is_default = 1), 0);

ALTER TABLE series ADD COLUMN library_id INTEGER NOT NULL DEFAULT 0;
UPDATE series SET library_id = COALESCE(
    (SELECT l.id FROM libraries l WHERE is_default = 1 AND l.kind =
        CASE series.kind WHEN 'adult' THEN 'adult' ELSE 'tv' END),
    0);

CREATE INDEX idx_movies_library ON movies (library_id);
CREATE INDEX idx_series_library ON series (library_id);
