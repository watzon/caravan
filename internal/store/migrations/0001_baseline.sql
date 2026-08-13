-- +goose Up

-- Caravan's first public database schema. The project was greenfield when Bun
-- and Goose replaced the development-era persistence stack, so the previous 29 migrations
-- were intentionally collapsed into this one clean baseline.

PRAGMA application_id = 1129469518;
PRAGMA user_version = 1;

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE quality_profiles (
    id                          INTEGER PRIMARY KEY AUTOINCREMENT,
    name                        TEXT NOT NULL UNIQUE,
    cutoff                      TEXT NOT NULL,
    items                       TEXT NOT NULL,
    upgrade_allowed             INTEGER NOT NULL DEFAULT 1,
    preferred_sources           TEXT NOT NULL DEFAULT '[]',
    proper_repack_preference    TEXT NOT NULL DEFAULT 'prefer',
    min_seeders                 INTEGER NOT NULL DEFAULT 0,
    min_size_mb                 INTEGER NOT NULL DEFAULT 0,
    max_size_mb                 INTEGER NOT NULL DEFAULT 0,
    custom_formats              TEXT NOT NULL DEFAULT '[]',
    tv_profile                  TEXT NOT NULL DEFAULT 'safe',
    tv_compatibility_policy     TEXT NOT NULL DEFAULT 'ignore',
    created_at                  TEXT NOT NULL,
    updated_at                  TEXT NOT NULL
);

CREATE TABLE movies (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    provider           TEXT NOT NULL DEFAULT '',
    provider_ref       TEXT NOT NULL DEFAULT '',
    tmdb_id            INTEGER NOT NULL DEFAULT 0,
    imdb_id            TEXT NOT NULL DEFAULT '',
    title              TEXT NOT NULL,
    sort_title         TEXT NOT NULL DEFAULT '',
    year               INTEGER NOT NULL DEFAULT 0,
    overview           TEXT NOT NULL DEFAULT '',
    path               TEXT NOT NULL DEFAULT '',
    poster_path        TEXT NOT NULL DEFAULT '',
    poster_url         TEXT NOT NULL DEFAULT '',
    monitored          INTEGER NOT NULL DEFAULT 1,
    quality_profile_id INTEGER NOT NULL DEFAULT 0,
    release_date       TEXT NOT NULL DEFAULT '',
    digital_release    TEXT NOT NULL DEFAULT '',
    physical_release   TEXT NOT NULL DEFAULT '',
    min_availability   TEXT NOT NULL DEFAULT 'released'
        CHECK (min_availability IN ('announced', 'in_cinemas', 'released')),
    added_at           TEXT NOT NULL,
    updated_at         TEXT NOT NULL,
    library_id         INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX idx_movies_provider_ref ON movies (provider, provider_ref)
    WHERE provider_ref != '';
CREATE UNIQUE INDEX idx_movies_tmdb_id ON movies (tmdb_id) WHERE tmdb_id > 0;
CREATE INDEX idx_movies_sort_title ON movies (sort_title);
CREATE INDEX idx_movies_library ON movies (library_id);

CREATE TABLE series (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    provider           TEXT NOT NULL DEFAULT '',
    provider_ref       TEXT NOT NULL DEFAULT '',
    tmdb_id            INTEGER NOT NULL DEFAULT 0,
    tvdb_id            INTEGER NOT NULL DEFAULT 0,
    imdb_id            TEXT NOT NULL DEFAULT '',
    kind               TEXT NOT NULL DEFAULT 'tv' CHECK (kind IN ('tv', 'adult')),
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

CREATE TABLE seasons (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id     INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    season_number INTEGER NOT NULL,
    title         TEXT NOT NULL DEFAULT '',
    overview      TEXT NOT NULL DEFAULT '',
    poster_path   TEXT NOT NULL DEFAULT '',
    air_date      TEXT NOT NULL DEFAULT '',
    monitored     INTEGER NOT NULL DEFAULT 1,
    UNIQUE (series_id, season_number)
);

CREATE TABLE episodes (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    series_id       INTEGER NOT NULL REFERENCES series(id) ON DELETE CASCADE,
    season_number   INTEGER NOT NULL,
    episode_number  INTEGER NOT NULL,
    absolute_number INTEGER NOT NULL DEFAULT 0,
    tmdb_id         INTEGER NOT NULL DEFAULT 0,
    stash_id        TEXT NOT NULL DEFAULT '',
    title           TEXT NOT NULL DEFAULT '',
    overview        TEXT NOT NULL DEFAULT '',
    air_date        TEXT NOT NULL DEFAULT '',
    monitored       INTEGER NOT NULL DEFAULT 1,
    scene           TEXT NOT NULL DEFAULT '',
    UNIQUE (series_id, season_number, episode_number)
);
CREATE INDEX idx_episodes_air_date ON episodes (air_date);
CREATE INDEX idx_episodes_absolute ON episodes (series_id, absolute_number)
    WHERE absolute_number != 0;
CREATE UNIQUE INDEX idx_episodes_stash_id ON episodes (series_id, stash_id)
    WHERE stash_id != '';

CREATE TABLE media_files (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    path          TEXT NOT NULL UNIQUE,
    size          INTEGER NOT NULL DEFAULT 0,
    movie_id      INTEGER NOT NULL DEFAULT 0,
    quality       TEXT NOT NULL DEFAULT 'unknown',
    source        TEXT NOT NULL DEFAULT 'unknown',
    codec         TEXT NOT NULL DEFAULT '',
    audio         TEXT NOT NULL DEFAULT '',
    release_group TEXT NOT NULL DEFAULT '',
    added_at      TEXT NOT NULL,
    modified_at   TEXT NOT NULL
);
CREATE INDEX idx_media_files_movie_id ON media_files (movie_id);

CREATE TABLE episode_files (
    episode_id    INTEGER NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
    media_file_id INTEGER NOT NULL REFERENCES media_files(id) ON DELETE CASCADE,
    PRIMARY KEY (episode_id, media_file_id)
);
CREATE INDEX idx_episode_files_media_file_id ON episode_files (media_file_id);

CREATE TABLE unmatched_files (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    path       TEXT NOT NULL UNIQUE,
    size       INTEGER NOT NULL DEFAULT 0,
    parsed     TEXT NOT NULL DEFAULT '',
    reason     TEXT NOT NULL DEFAULT '',
    seen_at    TEXT NOT NULL,
    library_id INTEGER
);

CREATE TABLE indexers (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    name       TEXT NOT NULL UNIQUE,
    protocol   TEXT NOT NULL,
    url        TEXT NOT NULL,
    api_key    TEXT NOT NULL DEFAULT '',
    categories TEXT NOT NULL DEFAULT '',
    priority   INTEGER NOT NULL DEFAULT 25 CHECK (priority >= 0),
    enabled    INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE releases (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    indexer_id   INTEGER NOT NULL DEFAULT 0,
    indexer_name TEXT NOT NULL DEFAULT '',
    guid         TEXT NOT NULL,
    title        TEXT NOT NULL,
    download_url TEXT NOT NULL DEFAULT '',
    info_url     TEXT NOT NULL DEFAULT '',
    info_hash    TEXT NOT NULL DEFAULT '',
    protocol     TEXT NOT NULL DEFAULT '',
    size         INTEGER NOT NULL DEFAULT 0,
    seeders      INTEGER NOT NULL DEFAULT 0,
    leechers     INTEGER NOT NULL DEFAULT 0,
    published_at TEXT NOT NULL DEFAULT '',
    parsed       TEXT NOT NULL DEFAULT '',
    seen_at      TEXT NOT NULL,
    categories   TEXT NOT NULL DEFAULT '',
    UNIQUE (indexer_id, guid)
);

CREATE TABLE download_clients (
    id             INTEGER PRIMARY KEY AUTOINCREMENT,
    name           TEXT NOT NULL UNIQUE,
    kind           TEXT NOT NULL,
    url            TEXT NOT NULL DEFAULT '',
    username       TEXT NOT NULL DEFAULT '',
    password       TEXT NOT NULL DEFAULT '',
    api_key        TEXT NOT NULL DEFAULT '',
    category       TEXT NOT NULL DEFAULT '',
    priority       INTEGER NOT NULL DEFAULT 25,
    max_concurrent INTEGER NOT NULL DEFAULT 0,
    enabled        INTEGER NOT NULL DEFAULT 1,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);

CREATE TABLE grabs (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    release_id    INTEGER NOT NULL DEFAULT 0,
    movie_id      INTEGER NOT NULL DEFAULT 0,
    series_id     INTEGER NOT NULL DEFAULT 0,
    season_number INTEGER NOT NULL DEFAULT 0,
    episode_ids   TEXT NOT NULL DEFAULT '',
    release_title TEXT NOT NULL DEFAULT '',
    reason        TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    library_id    INTEGER
);
CREATE INDEX idx_grabs_movie_id ON grabs (movie_id);
CREATE INDEX idx_grabs_series_id ON grabs (series_id);
CREATE INDEX idx_grabs_status ON grabs (status);

CREATE TABLE downloads (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    grab_id       INTEGER NOT NULL DEFAULT 0,
    client_id     INTEGER NOT NULL DEFAULT 0,
    engine        TEXT NOT NULL DEFAULT '',
    engine_id     TEXT NOT NULL DEFAULT '',
    title         TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL DEFAULT '',
    progress      REAL NOT NULL DEFAULT 0,
    output_path   TEXT NOT NULL DEFAULT '',
    error         TEXT NOT NULL DEFAULT '',
    size          INTEGER NOT NULL DEFAULT 0,
    bytes_done    INTEGER NOT NULL DEFAULT 0,
    max_down_rate INTEGER NOT NULL DEFAULT 0,
    max_up_rate   INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
CREATE UNIQUE INDEX idx_downloads_engine_id ON downloads (engine_id);
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

CREATE TABLE jobs (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    kind             TEXT NOT NULL,
    payload          TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL DEFAULT 'pending',
    attempts         INTEGER NOT NULL DEFAULT 0,
    run_after        TEXT NOT NULL DEFAULT '',
    lease_expires_at TEXT NOT NULL DEFAULT '',
    last_error       TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);
CREATE INDEX idx_jobs_state_run_after ON jobs (state, run_after);

CREATE TABLE conversions (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    media_file_id INTEGER NOT NULL DEFAULT 0,
    source_path   TEXT NOT NULL,
    output_path   TEXT NOT NULL DEFAULT '',
    strategy      TEXT NOT NULL DEFAULT '',
    profile_id    TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'queued',
    error         TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);
CREATE INDEX idx_conversions_status ON conversions (status);
CREATE UNIQUE INDEX idx_conversions_open_file ON conversions (media_file_id)
    WHERE status IN ('queued', 'running');

CREATE TABLE storage_migrations (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    source_root TEXT NOT NULL,
    target_root TEXT NOT NULL,
    status      TEXT NOT NULL DEFAULT 'queued',
    files_total INTEGER NOT NULL DEFAULT 0,
    files_done  INTEGER NOT NULL DEFAULT 0,
    bytes_total INTEGER NOT NULL DEFAULT 0,
    bytes_done  INTEGER NOT NULL DEFAULT 0,
    error       TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE UNIQUE INDEX idx_storage_migrations_open
    ON storage_migrations (status IN ('queued', 'running'))
    WHERE status IN ('queued', 'running');

CREATE TABLE usenet_servers (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    name            TEXT NOT NULL UNIQUE,
    host            TEXT NOT NULL,
    port            INTEGER NOT NULL DEFAULT 0,
    tls             INTEGER NOT NULL DEFAULT 1,
    username        TEXT NOT NULL DEFAULT '',
    password        TEXT NOT NULL DEFAULT '',
    max_connections INTEGER NOT NULL DEFAULT 0,
    priority        INTEGER NOT NULL DEFAULT 25,
    enabled         INTEGER NOT NULL DEFAULT 1,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE TABLE requests (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    media_type       TEXT NOT NULL CHECK (media_type IN ('movie', 'series', 'scene')),
    tmdb_id          INTEGER NOT NULL DEFAULT 0,
    stash_id         TEXT NOT NULL DEFAULT '',
    title            TEXT NOT NULL,
    year             INTEGER NOT NULL DEFAULT 0,
    poster_path      TEXT,
    seasons          TEXT,
    min_availability TEXT NOT NULL DEFAULT ''
        CHECK (min_availability IN ('', 'announced', 'in_cinemas', 'released')),
    status           TEXT NOT NULL CHECK (status IN ('pending', 'approved', 'dismissed')),
    requested_by     INTEGER NOT NULL DEFAULT 0,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    CHECK ((media_type = 'scene' AND tmdb_id = 0 AND stash_id != '')
        OR (media_type != 'scene' AND tmdb_id > 0 AND stash_id = ''))
);
CREATE UNIQUE INDEX idx_requests_pending ON requests (media_type, tmdb_id)
    WHERE status = 'pending' AND tmdb_id > 0;
CREATE UNIQUE INDEX idx_requests_pending_scene ON requests (stash_id)
    WHERE status = 'pending' AND stash_id != '';
CREATE INDEX idx_requests_status ON requests (status, created_at DESC);

CREATE TABLE users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE TABLE libraries (
    id                 INTEGER PRIMARY KEY AUTOINCREMENT,
    kind               TEXT NOT NULL CHECK (kind IN ('movie', 'tv', 'adult')),
    name               TEXT NOT NULL,
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

CREATE TABLE library_indexers (
    library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    indexer_id INTEGER NOT NULL REFERENCES indexers(id) ON DELETE CASCADE,
    enabled    INTEGER NOT NULL DEFAULT 1,
    categories TEXT,
    PRIMARY KEY (library_id, indexer_id)
);

CREATE TABLE remote_path_mappings (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    remote_path     TEXT NOT NULL COLLATE NOCASE UNIQUE,
    local_path      TEXT NOT NULL,
    match_count     INTEGER NOT NULL DEFAULT 0,
    last_matched_at TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    CHECK (length(trim(remote_path)) > 0),
    CHECK (length(trim(local_path)) > 0)
);

CREATE TABLE notification_webhooks (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    name          TEXT NOT NULL,
    url           TEXT NOT NULL,
    on_grab       INTEGER NOT NULL DEFAULT 1,
    on_import     INTEGER NOT NULL DEFAULT 1,
    on_health     INTEGER NOT NULL DEFAULT 1,
    enabled       INTEGER NOT NULL DEFAULT 1,
    last_event_id INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    CHECK (length(trim(name)) > 0),
    CHECK (on_grab IN (0, 1)),
    CHECK (on_import IN (0, 1)),
    CHECK (on_health IN (0, 1)),
    CHECK (enabled IN (0, 1)),
    CHECK (on_grab = 1 OR on_import = 1 OR on_health = 1),
    CHECK (last_event_id >= 0)
);
CREATE INDEX idx_notification_webhooks_enabled ON notification_webhooks (enabled);

CREATE TABLE stashbox_instances (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    provider_id TEXT NOT NULL UNIQUE,
    name        TEXT NOT NULL UNIQUE,
    endpoint    TEXT NOT NULL,
    api_key     TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE library_access (
    library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (library_id, user_id)
);
CREATE INDEX idx_library_access_user ON library_access (user_id);

CREATE TABLE sessions (
    token_hash TEXT NOT NULL PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at TEXT NOT NULL
);
CREATE INDEX idx_sessions_user ON sessions (user_id);

INSERT INTO quality_profiles (
    id, name, cutoff, items, upgrade_allowed, preferred_sources,
    proper_repack_preference, min_seeders, min_size_mb, max_size_mb,
    custom_formats, tv_profile, tv_compatibility_policy, created_at, updated_at
) VALUES (
    1, 'Standard', '1080p', '["2160p","1080p","720p","480p"]', 1, '[]',
    'prefer', 0, 0, 0, '[]', 'safe', 'ignore',
    '1970-01-01T00:00:00Z', '1970-01-01T00:00:00Z'
);

INSERT INTO settings (key, value, updated_at)
VALUES ('default_quality_profile_id', '1', '1970-01-01T00:00:00Z');

INSERT INTO libraries (
    id, kind, name, root_path, dlna_visible, provider, providers,
    is_default, active, restricted
) VALUES
    (1, 'movie', 'Movies', 'library/Movies', 1, 'tmdb', '["tmdb"]', 1, 1, 0),
    (2, 'tv', 'Series', 'library/TV', 1, 'tmdb', '["tmdb"]', 1, 1, 0);
