-- Discover: the `requests` table (PLAN discover track).
--
-- A request is a wish for something that is NOT in the library, so it carries
-- its own copy of the title, year and poster path rather than a foreign key —
-- there is no row to point at yet. When the title is added, by approval or by
-- any other path, the request is absorbed: it flips to 'approved' and the
-- library row becomes the record of truth.
--
-- `poster_path` here is the PROVIDER's path ("/abc.jpg"), not a storage-root
-- path. Nothing has been downloaded at request time, so the relative-path rule
-- (SPEC §1.2, pillar 3) has nothing to bite on; the column is nullable because
-- plenty of titles have no artwork.
CREATE TABLE requests (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    media_type  TEXT    NOT NULL CHECK (media_type IN ('movie', 'series')),
    tmdb_id     INTEGER NOT NULL CHECK (tmdb_id > 0),
    title       TEXT    NOT NULL,
    year        INTEGER NOT NULL DEFAULT 0,
    poster_path TEXT,
    -- JSON array of season numbers, ascending. NULL means the whole title:
    -- every movie request, and a series request that names no seasons.
    seasons     TEXT,
    status      TEXT    NOT NULL CHECK (status IN ('pending', 'approved', 'dismissed')),
    created_at  TEXT    NOT NULL,
    updated_at  TEXT    NOT NULL
);

-- One pending request per title, enforced by the database rather than by a
-- read-then-write in the handler: a second request for the same title unions
-- its seasons into the first instead of creating a duplicate. Approved and
-- dismissed rows are history and are deliberately outside the constraint, so
-- a dismissed title can be requested again.
CREATE UNIQUE INDEX idx_requests_pending ON requests (media_type, tmdb_id)
    WHERE status = 'pending';

-- The requests screen lists newest first within a status.
CREATE INDEX idx_requests_status ON requests (status, created_at DESC);
