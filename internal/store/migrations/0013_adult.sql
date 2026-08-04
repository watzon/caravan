-- Phase 9: the adult module's data model.
--
-- The shape of this phase is "reuse, do not fork": a site is a series, a
-- release year is a season, a scene is an episode. That is a deliberate schema
-- decision and it is why this migration is mostly columns rather than tables —
-- the wanted list, the backlog sweep, RSS matching, the calendar and the import
-- pipeline all keep working on `series`/`seasons`/`episodes` without learning
-- that adult content exists.
--
-- What they DO need is a discriminator, so that the code which must behave
-- differently (which metadata provider refreshes a title, which library root it
-- organizes into, who is allowed to see it) can ask. That is `series.kind`.
--
-- Nothing here creates the Adult library row. Enabling the module does, once,
-- in Go (store.SetAdultEnabled): a library row is a shelf in the UI and a
-- container in the DLNA tree, and a shelf that exists on every install the
-- moment they upgrade is exactly the "trace when disabled" this phase forbids.

-- ---------------------------------------------------------------------------
-- series.kind — the discriminator the whole phase turns on.
-- ---------------------------------------------------------------------------
--
-- Defaulting to 'tv' is what makes an upgrade a no-op: every row that exists
-- today is a television series and says so, and the column can be added in
-- place because SQLite allows a CHECK on ADD COLUMN (0010 does the same for
-- movies.min_availability).
ALTER TABLE series ADD COLUMN kind TEXT NOT NULL DEFAULT 'tv'
    CHECK (kind IN ('tv', 'adult'));

-- ---------------------------------------------------------------------------
-- stash ids on series and episodes.
-- ---------------------------------------------------------------------------
--
-- stash-box ids are UUID strings, not the integers TMDB hands out, so these are
-- TEXT and their "unmatched" value is the empty string rather than 0. The
-- partial unique index is the exact treatment tmdb_id gets in 0001: matched
-- rows are unique per provider id, and any number of unmatched rows may coexist
-- — which is what a scan that found files before it found metadata produces.
ALTER TABLE series   ADD COLUMN stash_id TEXT NOT NULL DEFAULT '';
ALTER TABLE episodes ADD COLUMN stash_id TEXT NOT NULL DEFAULT '';

CREATE UNIQUE INDEX idx_series_stash_id   ON series   (stash_id) WHERE stash_id != '';
CREATE UNIQUE INDEX idx_episodes_stash_id ON episodes (stash_id) WHERE stash_id != '';

-- ---------------------------------------------------------------------------
-- Scene-side metadata on episodes.
-- ---------------------------------------------------------------------------
--
-- Studio, performers and the scene's own page have no counterpart on a
-- television episode and no query ever filters on them — they are rendered on a
-- scene row and nothing else — so they ride in one JSON column instead of three
-- columns that are empty on every `tv` row in the table. Same convention as
-- `releases.parsed` and `grabs.episode_ids`: JSON text, empty string for "none"
-- (see core.SceneInfo for the encoded shape).
ALTER TABLE episodes ADD COLUMN scene TEXT NOT NULL DEFAULT '';

-- ---------------------------------------------------------------------------
-- users.adult_access — the per-account grant.
-- ---------------------------------------------------------------------------
--
-- Off for everybody, including accounts that already exist: a permission that
-- arrives switched on by an upgrade is a permission nobody granted. Admins are
-- implicitly granted and never read this column (see core.AdultVisible); it is
-- stored on their row anyway so a demotion to member does not silently carry
-- access along with it.
ALTER TABLE users ADD COLUMN adult_access INTEGER NOT NULL DEFAULT 0;

-- ---------------------------------------------------------------------------
-- libraries.kind gains 'adult'.
-- ---------------------------------------------------------------------------
--
-- SQLite cannot ALTER a CHECK constraint, so the table is rebuilt: new table,
-- INSERT SELECT, drop, rename. Two things make this more than boilerplate.
--
-- First, `library_indexers` holds a foreign key into `libraries` with ON DELETE
-- CASCADE, and foreign keys are ON for every connection (see store.dsn). With
-- them on, DROP TABLE performs an implicit DELETE of every row first, and that
-- delete cascades: rebuilding `libraries` alone silently erases every
-- per-library indexer override in the install. So the child is rebuilt too, and
-- the invariant that matters is that its rows are copied out BEFORE the parent
-- is dropped. After that the cascade has nothing left to take that has not
-- already been saved.
--
-- Second, a migration runs inside a transaction, and `PRAGMA foreign_keys` is a
-- no-op inside one. The usual "turn the pragma off, rebuild, turn it on" recipe
-- is therefore not available here; rebuilding the pair is what replaces it.
CREATE TABLE libraries_rebuild (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    kind               TEXT    NOT NULL UNIQUE CHECK (kind IN ('movie', 'tv', 'adult')),
    name               TEXT    NOT NULL,
    root_path          TEXT    NOT NULL,
    dlna_visible       INTEGER NOT NULL DEFAULT 1,
    route_torrent      TEXT,
    route_usenet       TEXT,
    quality_profile_id INTEGER
);

INSERT INTO libraries_rebuild
    (id, kind, name, root_path, dlna_visible, route_torrent, route_usenet, quality_profile_id)
SELECT id, kind, name, root_path, dlna_visible, route_torrent, route_usenet, quality_profile_id
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

-- Child first, so the cascade above never runs at all rather than running over
-- rows that have already been copied.
DROP TABLE library_indexers;
DROP TABLE libraries;

-- Renaming the parent rewrites library_indexers_rebuild's REFERENCES clause to
-- name `libraries` for us — SQLite has fixed up references across a rename
-- since 3.25, so the pair lands under its real names with the foreign key
-- intact rather than pointing at a table called libraries_rebuild.
ALTER TABLE libraries_rebuild        RENAME TO libraries;
ALTER TABLE library_indexers_rebuild RENAME TO library_indexers;

-- ---------------------------------------------------------------------------
-- requests.media_type gains 'scene'.
-- ---------------------------------------------------------------------------
--
-- Same rebuild, and the same reason: a CHECK cannot be altered in place.
--
-- Admitting 'scene' forces a second change. 0009's `tmdb_id INTEGER NOT NULL
-- CHECK (tmdb_id > 0)` is the row's identity, and a scene has no TMDB id at
-- all — its identity is a stash-box UUID. A media_type the table can name but
-- never hold would be a data model in name only, so `stash_id` lands here with
-- the CHECK that says which of the two identifies which kind of wish. Exactly
-- one is set, always, and it is decided by media_type rather than by the caller.
--
-- Every row that exists today is a movie or a series with tmdb_id > 0 (the old
-- CHECK guaranteed it) and gets stash_id '', so the new constraint holds over
-- the whole table before a single new row is written.
CREATE TABLE requests_rebuild (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    media_type  TEXT    NOT NULL CHECK (media_type IN ('movie', 'series', 'scene')),
    tmdb_id     INTEGER NOT NULL DEFAULT 0,
    stash_id    TEXT    NOT NULL DEFAULT '',
    title       TEXT    NOT NULL,
    year        INTEGER NOT NULL DEFAULT 0,
    poster_path TEXT,
    seasons     TEXT,
    min_availability TEXT NOT NULL DEFAULT ''
        CHECK (min_availability IN ('', 'announced', 'in_cinemas', 'released')),
    status      TEXT    NOT NULL CHECK (status IN ('pending', 'approved', 'dismissed')),
    requested_by INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT    NOT NULL,
    updated_at  TEXT    NOT NULL,
    CHECK ((media_type =  'scene' AND tmdb_id =  0 AND stash_id != '')
        OR (media_type != 'scene' AND tmdb_id >  0 AND stash_id =  ''))
);

INSERT INTO requests_rebuild
    (id, media_type, tmdb_id, title, year, poster_path, seasons,
     min_availability, status, requested_by, created_at, updated_at)
SELECT id, media_type, tmdb_id, title, year, poster_path, seasons,
       min_availability, status, requested_by, created_at, updated_at
FROM requests;

DROP TABLE requests;
ALTER TABLE requests_rebuild RENAME TO requests;

-- The indexes went with the old table and are recreated verbatim, save for the
-- split that 'scene' forces on the pending constraint: movie and series rows
-- are still one-pending-per-tmdb_id (the added `tmdb_id > 0` is implied by the
-- identity CHECK, so this index admits exactly the rows the old one did), and
-- scene rows get the same promise keyed on the id they actually carry.
CREATE UNIQUE INDEX idx_requests_pending ON requests (media_type, tmdb_id)
    WHERE status = 'pending' AND tmdb_id > 0;
CREATE UNIQUE INDEX idx_requests_pending_scene ON requests (stash_id)
    WHERE status = 'pending' AND stash_id != '';
CREATE INDEX idx_requests_status ON requests (status, created_at DESC);
