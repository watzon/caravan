-- Phase 2: the acquisition tables get the columns their types actually need.
--
-- 0001 sketched `releases`, `grabs` and `downloads` ahead of the code that
-- fills them. This migration reconciles them with core.Release, core.Grab and
-- core.Download. Same conventions as 0001: relative paths, RFC3339 UTC
-- timestamps, soft integer references between subsystems.

-- The info hash is what the embedded engine needs to start a torrent from a
-- cached result, and the indexer name is denormalized because indexers.id is a
-- soft reference: a deleted indexer must not erase where a result came from.
ALTER TABLE releases ADD COLUMN info_hash    TEXT NOT NULL DEFAULT '';
ALTER TABLE releases ADD COLUMN indexer_name TEXT NOT NULL DEFAULT '';

-- A grab targets a movie, an episode, or a whole season pack, so the single
-- `episode_id` column of 0001 is replaced by a JSON list plus the season and
-- series it belongs to. `release_title` is denormalized for the same reason as
-- `indexer_name`: a stuck import must still be able to say what it was trying
-- to import after the release cache is pruned.
ALTER TABLE grabs DROP COLUMN episode_id;
ALTER TABLE grabs ADD COLUMN series_id     INTEGER NOT NULL DEFAULT 0;
ALTER TABLE grabs ADD COLUMN season_number INTEGER NOT NULL DEFAULT 0;
ALTER TABLE grabs ADD COLUMN episode_ids   TEXT NOT NULL DEFAULT '';  -- JSON array of episodes.id
ALTER TABLE grabs ADD COLUMN release_title TEXT NOT NULL DEFAULT '';

CREATE INDEX idx_grabs_movie_id ON grabs (movie_id);
CREATE INDEX idx_grabs_series_id ON grabs (series_id);

-- Byte counters so a restart can render the queue before the engine has
-- reported in.
ALTER TABLE downloads ADD COLUMN size       INTEGER NOT NULL DEFAULT 0;
ALTER TABLE downloads ADD COLUMN bytes_done INTEGER NOT NULL DEFAULT 0;

-- The engine's own handle is the download's identity: it is what the queue API
-- addresses and what resume-after-restart reconciles against. Unconditionally
-- unique, so the store can upsert on it; a row without a handle is rejected
-- before it reaches sqlite.
CREATE UNIQUE INDEX idx_downloads_engine_id ON downloads (engine_id);
