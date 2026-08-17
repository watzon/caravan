-- +goose Up

-- Migration 0005 was exercised by development databases before its schema was
-- hardened. Rebuild the pack tables into the reviewed contract so both that
-- exact interim schema and the final v5 schema converge without losing rows.
DROP TRIGGER IF EXISTS definition_pack_revision_signer_matches_source;
DROP TRIGGER IF EXISTS definition_pack_source_owner_is_immutable;
DROP TRIGGER IF EXISTS definition_pack_revision_identity_is_immutable;
DROP TRIGGER IF EXISTS definition_pack_entry_is_immutable;

CREATE TABLE definition_pack_sources_v6 (
    source                         TEXT PRIMARY KEY,
    owner_signer_key_id            TEXT NOT NULL,
    owner_signer_key_fingerprint   TEXT NOT NULL,
    created_at                     TEXT NOT NULL,
    updated_at                     TEXT NOT NULL,
    CHECK (length(source) BETWEEN 1 AND 64),
    CHECK (source NOT IN ('builtin', 'user')),
    CHECK (owner_signer_key_id != ''),
    CHECK (length(owner_signer_key_fingerprint) = 71 AND substr(owner_signer_key_fingerprint, 1, 7) = 'sha256:' AND substr(owner_signer_key_fingerprint, 8) NOT GLOB '*[^0-9a-f]*')
);

CREATE TABLE definition_pack_revisions_v6 (
    source                       TEXT NOT NULL REFERENCES definition_pack_sources_v6(source) ON DELETE RESTRICT,
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
    CHECK (length(manifest_digest) = 71 AND substr(manifest_digest, 1, 7) = 'sha256:' AND substr(manifest_digest, 8) NOT GLOB '*[^0-9a-f]*'),
    CHECK (length(archive_digest) = 71 AND substr(archive_digest, 1, 7) = 'sha256:' AND substr(archive_digest, 8) NOT GLOB '*[^0-9a-f]*'),
    CHECK (archive_relpath LIKE 'archives/sha256/%' AND archive_relpath NOT LIKE '/%' AND archive_relpath NOT LIKE '%..%'),
    CHECK (license_expression != ''),
    CHECK (license_path != ''),
    CHECK (length(license_digest) = 71 AND substr(license_digest, 1, 7) = 'sha256:' AND substr(license_digest, 8) NOT GLOB '*[^0-9a-f]*'),
    CHECK ((notice_path = '' AND notice_digest = '') OR (notice_path != '' AND length(notice_digest) = 71 AND substr(notice_digest, 1, 7) = 'sha256:' AND substr(notice_digest, 8) NOT GLOB '*[^0-9a-f]*')),
    CHECK (signer_key_id != ''),
    CHECK (length(signer_key_fingerprint) = 71 AND substr(signer_key_fingerprint, 1, 7) = 'sha256:' AND substr(signer_key_fingerprint, 8) NOT GLOB '*[^0-9a-f]*'),
    CHECK (minimum_caravan_version != ''),
    CHECK (install_state IN ('installing', 'installed', 'failed')),
    CHECK (is_pending IN (0, 1)),
    CHECK (is_active IN (0, 1)),
    CHECK (is_last_known_good IN (0, 1)),
    CHECK (definition_count >= 1 AND runnable_count >= 0 AND runnable_count <= definition_count),
    CHECK (accepted_at != '' AND created_at != '' AND updated_at != ''),
    CHECK ((install_state = 'installed' AND installed_at != '') OR (install_state = 'installing' AND installed_at = '') OR install_state = 'failed'),
    CHECK (install_state = 'installed' OR (is_pending = 0 AND is_active = 0 AND is_last_known_good = 0)),
    CHECK (install_state != 'failed' OR validation_error != '')
);

CREATE TABLE definition_pack_entries_v6 (
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
        REFERENCES definition_pack_revisions_v6(source, revision) ON DELETE RESTRICT,
    CHECK (definition_ref LIKE source || ':%'),
    CHECK (metadata_id != ''),
    CHECK (path != ''),
    CHECK (length(digest) = 71 AND substr(digest, 1, 7) = 'sha256:' AND substr(digest, 8) NOT GLOB '*[^0-9a-f]*'),
    CHECK (state IN ('unsupported', 'runnable-unverified'))
);

CREATE TABLE indexer_definition_pins_v6 (
    indexer_id       INTEGER PRIMARY KEY REFERENCES indexers(id) ON DELETE CASCADE,
    source           TEXT NOT NULL,
    revision         TEXT NOT NULL,
    definition_ref   TEXT NOT NULL,
    digest           TEXT NOT NULL,
    FOREIGN KEY (source, revision, definition_ref, digest)
        REFERENCES definition_pack_entries_v6(source, revision, definition_ref, digest) ON DELETE RESTRICT
);

INSERT INTO definition_pack_sources_v6
SELECT source, owner_signer_key_id, owner_signer_key_fingerprint, created_at, updated_at
FROM definition_pack_sources;

INSERT INTO definition_pack_revisions_v6
SELECT source, revision, manifest_digest, archive_digest, archive_relpath,
       license_expression, license_path, license_digest, notice_path,
       notice_digest, provenance, signer_key_id, signer_key_fingerprint,
       minimum_caravan_version, install_state, is_pending, is_active,
       is_last_known_good, validation_error, definition_count, runnable_count,
       accepted_at, accepted_by_user_id, installed_at, created_at, updated_at
FROM definition_pack_revisions;

INSERT INTO definition_pack_entries_v6
SELECT source, revision, definition_ref, metadata_id, path, digest, state,
       unsupported_json, approved_origins_json
FROM definition_pack_entries;

INSERT INTO indexer_definition_pins_v6
SELECT indexer_id, source, revision, definition_ref, digest
FROM indexer_definition_pins;

DROP TABLE indexer_definition_pins;
DROP TABLE definition_pack_entries;
DROP TABLE definition_pack_revisions;
DROP TABLE definition_pack_sources;

ALTER TABLE definition_pack_sources_v6 RENAME TO definition_pack_sources;
ALTER TABLE definition_pack_revisions_v6 RENAME TO definition_pack_revisions;
ALTER TABLE definition_pack_entries_v6 RENAME TO definition_pack_entries;
ALTER TABLE indexer_definition_pins_v6 RENAME TO indexer_definition_pins;

CREATE UNIQUE INDEX idx_definition_pack_one_pending
    ON definition_pack_revisions(source) WHERE is_pending = 1;
CREATE UNIQUE INDEX idx_definition_pack_one_active
    ON definition_pack_revisions(source) WHERE is_active = 1;
CREATE UNIQUE INDEX idx_definition_pack_one_lkg
    ON definition_pack_revisions(source) WHERE is_last_known_good = 1;

-- +goose StatementBegin
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
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER definition_pack_source_owner_is_immutable
BEFORE UPDATE OF owner_signer_key_id, owner_signer_key_fingerprint ON definition_pack_sources
FOR EACH ROW
WHEN OLD.owner_signer_key_id != NEW.owner_signer_key_id
  OR OLD.owner_signer_key_fingerprint != NEW.owner_signer_key_fingerprint
BEGIN
    SELECT RAISE(ABORT, 'definition pack source owner is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER definition_pack_revision_identity_is_immutable
BEFORE UPDATE ON definition_pack_revisions
FOR EACH ROW
WHEN OLD.source != NEW.source
  OR OLD.revision != NEW.revision
  OR OLD.manifest_digest != NEW.manifest_digest
  OR OLD.archive_digest != NEW.archive_digest
  OR OLD.archive_relpath != NEW.archive_relpath
  OR OLD.license_expression != NEW.license_expression
  OR OLD.license_path != NEW.license_path
  OR OLD.license_digest != NEW.license_digest
  OR OLD.notice_path != NEW.notice_path
  OR OLD.notice_digest != NEW.notice_digest
  OR OLD.provenance != NEW.provenance
  OR OLD.signer_key_id != NEW.signer_key_id
  OR OLD.signer_key_fingerprint != NEW.signer_key_fingerprint
  OR OLD.minimum_caravan_version != NEW.minimum_caravan_version
  OR OLD.definition_count != NEW.definition_count
  OR OLD.runnable_count != NEW.runnable_count
  OR OLD.accepted_at != NEW.accepted_at
  OR OLD.accepted_by_user_id != NEW.accepted_by_user_id
  OR OLD.created_at != NEW.created_at
BEGIN
    SELECT RAISE(ABORT, 'definition pack revision identity is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER definition_pack_entry_is_immutable
BEFORE UPDATE ON definition_pack_entries
FOR EACH ROW
BEGIN
    SELECT RAISE(ABORT, 'definition pack entry is immutable');
END;
-- +goose StatementEnd
