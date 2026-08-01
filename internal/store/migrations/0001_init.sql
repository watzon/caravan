-- Caravan initial schema (SPEC §7).
--
-- Conventions:
--   * Every path column is RELATIVE to the storage root. No absolute paths.
--   * Timestamps are RFC3339 UTC strings; the empty string means "unset".
--   * Structural parent/child links carry ON DELETE CASCADE foreign keys.
--     Soft references between subsystems (quality profiles, indexers, download
--     clients) are plain integer columns where 0 means "none", because the DB
--     is a rebuildable cache and a dangling profile id must never block an
--     import.

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE quality_profiles (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL UNIQUE,
    cutoff          TEXT NOT NULL,
    items           TEXT NOT NULL,                -- JSON array, best-first
    upgrade_allowed INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE TABLE movies (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    tmdb_id            INTEGER NOT NULL DEFAULT 0,
    imdb_id            TEXT NOT NULL DEFAULT '',
    title              TEXT NOT NULL,
    sort_title         TEXT NOT NULL DEFAULT '',
    year               INTEGER NOT NULL DEFAULT 0,
    overview           TEXT NOT NULL DEFAULT '',
    path               TEXT NOT NULL DEFAULT '',
    poster_path        TEXT NOT NULL DEFAULT '',
    monitored          INTEGER NOT NULL DEFAULT 1,
    quality_profile_id INTEGER NOT NULL DEFAULT 0,
    release_date       TEXT NOT NULL DEFAULT '',
    added_at           TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);

-- Partial unique index: matched movies are unique per TMDB id, but any number
-- of not-yet-matched movies (tmdb_id 0) may coexist.
CREATE UNIQUE INDEX idx_movies_tmdb_id ON movies (tmdb_id) WHERE tmdb_id > 0;
CREATE INDEX idx_movies_sort_title ON movies (sort_title);

CREATE TABLE series (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    tmdb_id            INTEGER NOT NULL DEFAULT 0,
    tvdb_id            INTEGER NOT NULL DEFAULT 0,
    imdb_id            TEXT NOT NULL DEFAULT '',
    title              TEXT NOT NULL,
    sort_title         TEXT NOT NULL DEFAULT '',
    year               INTEGER NOT NULL DEFAULT 0,
    overview           TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT '',
    path               TEXT NOT NULL DEFAULT '',
    poster_path        TEXT NOT NULL DEFAULT '',
    monitored          INTEGER NOT NULL DEFAULT 1,
    quality_profile_id INTEGER NOT NULL DEFAULT 0,
    first_aired        TEXT NOT NULL DEFAULT '',
    added_at           TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);

CREATE UNIQUE INDEX idx_series_tmdb_id ON series (tmdb_id) WHERE tmdb_id > 0;
CREATE INDEX idx_series_sort_title ON series (sort_title);

CREATE TABLE seasons (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id     INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    season_number INTEGER NOT NULL,           -- 0 is the specials season
    title         TEXT NOT NULL DEFAULT '',
    overview      TEXT NOT NULL DEFAULT '',
    poster_path   TEXT NOT NULL DEFAULT '',
    air_date      TEXT NOT NULL DEFAULT '',
    monitored     INTEGER NOT NULL DEFAULT 1,
    UNIQUE (series_id, season_number)
);

CREATE TABLE episodes (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id      INTEGER NOT NULL REFERENCES series (id) ON DELETE CASCADE,
    season_number  INTEGER NOT NULL,
    episode_number INTEGER NOT NULL,
    tmdb_id        INTEGER NOT NULL DEFAULT 0,
    title          TEXT NOT NULL DEFAULT '',
    overview       TEXT NOT NULL DEFAULT '',
    air_date       TEXT NOT NULL DEFAULT '',
    monitored      INTEGER NOT NULL DEFAULT 1,
    UNIQUE (series_id, season_number, episode_number)
);

CREATE INDEX idx_episodes_air_date ON episodes (air_date);

CREATE TABLE media_files (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    path          TEXT NOT NULL UNIQUE,
    size          INTEGER NOT NULL DEFAULT 0,
    movie_id      INTEGER NOT NULL DEFAULT 0,  -- 0 for episode files
    quality       TEXT NOT NULL DEFAULT 'unknown',
    source        TEXT NOT NULL DEFAULT 'unknown',
    codec         TEXT NOT NULL DEFAULT '',
    audio         TEXT NOT NULL DEFAULT '',
    release_group TEXT NOT NULL DEFAULT '',
    added_at      TEXT NOT NULL,
    modified_at   TEXT NOT NULL
);

CREATE INDEX idx_media_files_movie_id ON media_files (movie_id);

-- One file can satisfy several episodes (S01E01E02), and an episode can have
-- at most one current file but several over its history, so the link is a
-- many-to-many join (SPEC §7).
CREATE TABLE episode_files (
    episode_id    INTEGER NOT NULL REFERENCES episodes (id) ON DELETE CASCADE,
    media_file_id INTEGER NOT NULL REFERENCES media_files (id) ON DELETE CASCADE,
    PRIMARY KEY (episode_id, media_file_id)
);

CREATE INDEX idx_episode_files_media_file_id ON episode_files (media_file_id);

-- Files the scanner found but could not confidently match. SPEC §13: import
-- failures are visible, never silent drops.
CREATE TABLE unmatched_files (
    id      INTEGER PRIMARY KEY AUTOINCREMENT,
    path    TEXT NOT NULL UNIQUE,
    size    INTEGER NOT NULL DEFAULT 0,
    parsed  TEXT NOT NULL DEFAULT '',      -- JSON core.ParsedRelease
    reason  TEXT NOT NULL DEFAULT '',
    seen_at TEXT NOT NULL
);

CREATE TABLE indexers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    protocol   TEXT NOT NULL,                 -- torznab | newznab
    url        TEXT NOT NULL,
    api_key    TEXT NOT NULL DEFAULT '',
    categories TEXT NOT NULL DEFAULT '',      -- JSON array of ints
    enabled    INTEGER NOT NULL DEFAULT 1,
    priority   INTEGER NOT NULL DEFAULT 25,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE releases (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    indexer_id   INTEGER NOT NULL DEFAULT 0,
    guid         TEXT NOT NULL,
    title        TEXT NOT NULL,
    download_url TEXT NOT NULL DEFAULT '',
    info_url     TEXT NOT NULL DEFAULT '',
    protocol     TEXT NOT NULL DEFAULT '',    -- torrent | usenet
    size         INTEGER NOT NULL DEFAULT 0,
    seeders      INTEGER NOT NULL DEFAULT 0,
    leechers     INTEGER NOT NULL DEFAULT 0,
    published_at TEXT NOT NULL DEFAULT '',
    parsed       TEXT NOT NULL DEFAULT '',    -- JSON core.ParsedRelease
    seen_at      TEXT NOT NULL,
    UNIQUE (indexer_id, guid)
);

CREATE TABLE download_clients (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    kind       TEXT NOT NULL,                 -- embedded | qbittorrent | sabnzbd | nzbget
    host       TEXT NOT NULL DEFAULT '',
    port       INTEGER NOT NULL DEFAULT 0,
    username   TEXT NOT NULL DEFAULT '',
    password   TEXT NOT NULL DEFAULT '',
    category   TEXT NOT NULL DEFAULT '',
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE grabs (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    release_id INTEGER NOT NULL DEFAULT 0,
    movie_id   INTEGER NOT NULL DEFAULT 0,
    episode_id INTEGER NOT NULL DEFAULT 0,
    reason     TEXT NOT NULL DEFAULT '',      -- why this release won, or why one was skipped
    status     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);

CREATE TABLE downloads (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    grab_id     INTEGER NOT NULL DEFAULT 0,
    client_id   INTEGER NOT NULL DEFAULT 0,
    engine      TEXT NOT NULL DEFAULT '',
    engine_id   TEXT NOT NULL DEFAULT '',     -- engine-native handle (infohash, nzo_id, …)
    title       TEXT NOT NULL DEFAULT '',
    state       TEXT NOT NULL DEFAULT '',
    progress    REAL NOT NULL DEFAULT 0,
    output_path TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE INDEX idx_downloads_state ON downloads (state);

CREATE TABLE events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    level      TEXT NOT NULL DEFAULT 'info',
    category   TEXT NOT NULL DEFAULT '',
    message    TEXT NOT NULL,
    detail     TEXT NOT NULL DEFAULT '',
    movie_id   INTEGER NOT NULL DEFAULT 0,
    series_id  INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);

CREATE INDEX idx_events_id_desc ON events (id DESC);

-- Durable at-least-once job queue (SPEC §7). Workers claim with a lease;
-- expired leases are reclaimed at startup after a crash.
CREATE TABLE jobs (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    kind             TEXT NOT NULL,
    payload          TEXT NOT NULL DEFAULT '',   -- JSON
    state            TEXT NOT NULL DEFAULT 'pending',
    attempts         INTEGER NOT NULL DEFAULT 0,
    run_after        TEXT NOT NULL DEFAULT '',
    lease_expires_at TEXT NOT NULL DEFAULT '',
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);

CREATE INDEX idx_jobs_state_run_after ON jobs (state, run_after);

-- Default quality profile so a first run has something to assign.
INSERT INTO quality_profiles (name, cutoff, items, upgrade_allowed, created_at, updated_at)
VALUES (
    'Standard',
    '1080p',
    '["2160p","1080p","720p","480p"]',
    1,
    '1970-01-01T00:00:00Z',
    '1970-01-01T00:00:00Z'
);
