-- +goose Up

-- V7 stored only a fingerprint. A fingerprint is not trust material: startup
-- must be able to re-open every archive with the exact accepted Ed25519 key.
-- Historical rows cannot prove that key, so preserve them for audit but make
-- them inert/failed rather than silently trusting or activating them.
ALTER TABLE definition_pack_sources
    ADD COLUMN owner_signer_public_key BLOB NOT NULL DEFAULT X'';

UPDATE definition_pack_revisions
SET install_state = 'failed',
    is_pending = 0,
    is_active = 0,
    is_last_known_good = 0,
    validation_error = 'pack.trust.missing_public_key'
WHERE install_state != 'failed';

-- Active and LKG are one structural pointer in this state model. Direct SQL
-- may not create active-only or LKG-only rows.
-- +goose StatementBegin
CREATE TRIGGER definition_pack_revision_active_matches_lkg_insert
BEFORE INSERT ON definition_pack_revisions
FOR EACH ROW
WHEN NEW.is_active != NEW.is_last_known_good
BEGIN
    SELECT RAISE(ABORT, 'definition pack active and LKG must match');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER definition_pack_revision_active_matches_lkg_update
BEFORE UPDATE OF is_active, is_last_known_good ON definition_pack_revisions
FOR EACH ROW
WHEN NEW.is_active != NEW.is_last_known_good
BEGIN
    SELECT RAISE(ABORT, 'definition pack active and LKG must match');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER definition_pack_source_public_key_is_immutable
BEFORE UPDATE OF owner_signer_key_id, owner_signer_key_fingerprint, owner_signer_public_key
ON definition_pack_sources
FOR EACH ROW
WHEN OLD.owner_signer_key_id != NEW.owner_signer_key_id
  OR OLD.owner_signer_key_fingerprint != NEW.owner_signer_key_fingerprint
  OR OLD.owner_signer_public_key != NEW.owner_signer_public_key
BEGIN
    SELECT RAISE(ABORT, 'definition pack source trust key is immutable');
END;
-- +goose StatementEnd
