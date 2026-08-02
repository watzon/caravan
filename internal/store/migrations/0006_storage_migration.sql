-- Phase 5: moving the storage root (SPEC §10, PLAN phase 5 task 4).
--
-- Re-pointing the root needs no row at all: every stored path is relative, so
-- it is one settings update. Migrating does, because it is the one operation
-- in Caravan that moves media, and a progress bar over hours of copying has to
-- survive the browser being closed, the job being redelivered, and the process
-- being restarted mid-move.
--
-- Same reasoning as `conversions`: the jobs table gives at-least-once delivery
-- with leases and backoff, but it cannot answer "how far along is the move and
-- what did it do when it broke" without parsing payloads out of a queue that
-- also holds RSS syncs.
--
-- The roots are absolute paths. They are the same documented exception the
-- storage_root setting is (SPEC §10) — describing a move between two roots is
-- the one thing a root-relative path cannot do.
CREATE TABLE storage_migrations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source_root TEXT NOT NULL,
    target_root TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'queued', -- queued|running|done|failed|rolled_back
    files_total INTEGER NOT NULL DEFAULT 0,
    files_done  INTEGER NOT NULL DEFAULT 0,
    bytes_total INTEGER NOT NULL DEFAULT 0,
    bytes_done  INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

-- At most one migration may be open at a time, process-wide. Two movers over
-- the same trees would each see the other's half-finished work as "already
-- moved" and delete the source out from under it, so the constraint is in the
-- schema rather than only in the handler that checks first.
--
-- The indexed expression is constant-true over every row the WHERE clause
-- admits, which is how a partial unique index expresses "at most one row"
-- rather than "at most one per value".
CREATE UNIQUE INDEX idx_storage_migrations_open
    ON storage_migrations (status IN ('queued', 'running'))
    WHERE status IN ('queued', 'running');
