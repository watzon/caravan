CREATE TABLE remote_path_mappings (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    remote_path TEXT NOT NULL COLLATE NOCASE UNIQUE,
    local_path  TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    CHECK (length(trim(remote_path)) > 0),
    CHECK (length(trim(local_path)) > 0)
);
