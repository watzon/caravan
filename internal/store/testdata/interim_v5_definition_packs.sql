CREATE TABLE definition_pack_sources (
    source                         TEXT PRIMARY KEY,
    owner_signer_key_id            TEXT NOT NULL,
    owner_signer_key_fingerprint   TEXT NOT NULL,
    created_at                     TEXT NOT NULL,
    updated_at                     TEXT NOT NULL,
    CHECK (length(source) BETWEEN 1 AND 64),
    CHECK (owner_signer_key_id != ''),
    CHECK (owner_signer_key_fingerprint LIKE 'sha256:%')
);

CREATE TABLE definition_pack_revisions (
    source                       TEXT NOT NULL REFERENCES definition_pack_sources(source) ON DELETE RESTRICT,
    revision                     TEXT NOT NULL,
    manifest_digest              TEXT NOT NULL,
    archive_digest               TEXT NOT NULL,
    archive_relpath              TEXT NOT NULL,
    license_expression           TEXT NOT NULL,
    license_path                 TEXT NOT NULL,
    license_digest               TEXT NOT NULL,
    notice_path                  TEXT NOT NULL DEFAULT '',
    notice_digest                TEXT NOT NULL DEFAULT '',
    provenance                   TEXT NOT NULL,
    signer_key_id                TEXT NOT NULL,
    signer_key_fingerprint       TEXT NOT NULL,
    minimum_caravan_version      TEXT NOT NULL,
    install_state                TEXT NOT NULL,
    is_pending                   INTEGER NOT NULL DEFAULT 0,
    is_active                    INTEGER NOT NULL DEFAULT 0,
    is_last_known_good           INTEGER NOT NULL DEFAULT 0,
    validation_error             TEXT NOT NULL DEFAULT '',
    definition_count             INTEGER NOT NULL,
    runnable_count               INTEGER NOT NULL,
    accepted_at                  TEXT NOT NULL,
    accepted_by_user_id          INTEGER NOT NULL DEFAULT 0,
    installed_at                 TEXT NOT NULL DEFAULT '',
    created_at                   TEXT NOT NULL,
    updated_at                   TEXT NOT NULL,
    PRIMARY KEY (source, revision),
    UNIQUE (manifest_digest),
    UNIQUE (archive_digest),
    UNIQUE (archive_relpath),
    CHECK (length(revision) BETWEEN 1 AND 256),
    CHECK (manifest_digest LIKE 'sha256:%'),
    CHECK (archive_digest LIKE 'sha256:%'),
    CHECK (archive_relpath != '' AND archive_relpath NOT LIKE '/%' AND archive_relpath NOT LIKE '%..%'),
    CHECK (license_expression != ''),
    CHECK (license_path != ''),
    CHECK (license_digest LIKE 'sha256:%'),
    CHECK ((notice_path = '' AND notice_digest = '') OR (notice_path != '' AND notice_digest LIKE 'sha256:%')),
    CHECK (signer_key_id != ''),
    CHECK (signer_key_fingerprint LIKE 'sha256:%'),
    CHECK (minimum_caravan_version != ''),
    CHECK (install_state IN ('installing', 'installed', 'failed')),
    CHECK (is_pending IN (0, 1)),
    CHECK (is_active IN (0, 1)),
    CHECK (is_last_known_good IN (0, 1)),
    CHECK (definition_count >= 1 AND runnable_count >= 0 AND runnable_count <= definition_count),
    CHECK (accepted_at != '')
);

CREATE UNIQUE INDEX idx_definition_pack_one_pending
    ON definition_pack_revisions(source) WHERE is_pending = 1;
CREATE UNIQUE INDEX idx_definition_pack_one_active
    ON definition_pack_revisions(source) WHERE is_active = 1;
CREATE UNIQUE INDEX idx_definition_pack_one_lkg
    ON definition_pack_revisions(source) WHERE is_last_known_good = 1;

CREATE TRIGGER definition_pack_revision_signer_matches_source
BEFORE INSERT ON definition_pack_revisions
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1 FROM definition_pack_sources source
    WHERE source.source = NEW.source
      AND source.owner_signer_key_id = NEW.signer_key_id
      AND source.owner_signer_key_fingerprint = NEW.signer_key_fingerprint
)
BEGIN
    SELECT RAISE(ABORT, 'definition pack signer does not own source namespace');
END;

CREATE TABLE definition_pack_entries (
    source               TEXT NOT NULL,
    revision             TEXT NOT NULL,
    definition_ref       TEXT NOT NULL,
    metadata_id          TEXT NOT NULL,
    path                 TEXT NOT NULL,
    digest               TEXT NOT NULL,
    state                TEXT NOT NULL,
    unsupported_json     TEXT NOT NULL DEFAULT '[]',
    approved_origins_json TEXT NOT NULL DEFAULT '[]',
    PRIMARY KEY (source, revision, definition_ref),
    UNIQUE (source, revision, metadata_id),
    UNIQUE (source, revision, definition_ref, digest),
    FOREIGN KEY (source, revision)
        REFERENCES definition_pack_revisions(source, revision) ON DELETE RESTRICT,
    CHECK (definition_ref LIKE source || ':%'),
    CHECK (metadata_id != ''),
    CHECK (path != ''),
    CHECK (digest LIKE 'sha256:%'),
    CHECK (state IN ('unsupported', 'runnable-unverified'))
);

CREATE TABLE indexer_definition_pins (
    indexer_id       INTEGER PRIMARY KEY REFERENCES indexers(id) ON DELETE CASCADE,
    source           TEXT NOT NULL,
    revision         TEXT NOT NULL,
    definition_ref   TEXT NOT NULL,
    digest           TEXT NOT NULL,
    FOREIGN KEY (source, revision, definition_ref, digest)
        REFERENCES definition_pack_entries(source, revision, definition_ref, digest) ON DELETE RESTRICT
);
