-- Phase 4: the convert-for-TV queue (SPEC §8, PLAN phase 4 task 4).
--
-- The jobs table already gives at-least-once delivery with leases and backoff,
-- and the conversion job rides on it. What it cannot do is answer "what is
-- this file's conversion doing, and why did it fail" without parsing payloads
-- out of a queue that also holds RSS syncs — so conversions gets its own row,
-- exactly as `downloads` sits alongside the import job.
--
-- media_file_id is a loose id rather than a foreign key, for the reason the
-- header of store.go gives: this is history, and a rescan that rewrites
-- media_files must not silently erase the record of a conversion that already
-- happened.
CREATE TABLE conversions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    media_file_id INTEGER NOT NULL DEFAULT 0,
    source_path   TEXT NOT NULL,
    output_path   TEXT NOT NULL DEFAULT '',
    strategy      TEXT NOT NULL DEFAULT '',   -- '', 'none', 'remux', 'transcode'
    profile_id    TEXT NOT NULL DEFAULT '',   -- core.TVProfile id targeted
    status        TEXT NOT NULL DEFAULT 'queued',
    error         TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE INDEX idx_conversions_status ON conversions (status);

-- One open conversion per file. Two ffmpeg runs writing the same output would
-- race over the file the library points at, so the constraint is in the
-- schema rather than only in the handler that checks first.
CREATE UNIQUE INDEX idx_conversions_open_file
    ON conversions (media_file_id) WHERE status IN ('queued', 'running');
