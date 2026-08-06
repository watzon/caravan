ALTER TABLE remote_path_mappings ADD COLUMN match_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE remote_path_mappings ADD COLUMN last_matched_at TEXT;
