-- +goose Up

-- V8 could leave historical failed rows keyless when their earlier validation
-- reason was not the specific missing-key code. They are audit records, not
-- executable trust material. Normalize every keyless source revision without
-- changing its immutable receipt, entry, pin, or timestamp data.
UPDATE definition_pack_revisions
SET install_state = 'failed',
    is_pending = 0,
    is_active = 0,
    is_last_known_good = 0,
    validation_error = 'pack.trust.missing_public_key'
WHERE EXISTS (
    SELECT 1 FROM definition_pack_sources source
    WHERE source.source = definition_pack_revisions.source
      AND length(source.owner_signer_public_key) = 0
);

-- New source claims always include the exact Ed25519 public key. The narrow
-- empty-key historical exception is migration-only and cannot be created anew.
-- +goose StatementBegin
CREATE TRIGGER definition_pack_source_public_key_is_exact_on_insert
BEFORE INSERT ON definition_pack_sources
FOR EACH ROW
WHEN length(NEW.owner_signer_public_key) != 32
BEGIN
    SELECT RAISE(ABORT, 'definition pack source requires an exact public key');
END;
-- +goose StatementEnd

-- Accepted source, revision, and entry records are immutable audit evidence.
-- +goose StatementBegin
CREATE TRIGGER definition_pack_source_delete_is_immutable
BEFORE DELETE ON definition_pack_sources
FOR EACH ROW
BEGIN
    SELECT RAISE(ABORT, 'definition pack source is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER definition_pack_revision_delete_is_immutable
BEFORE DELETE ON definition_pack_revisions
FOR EACH ROW
BEGIN
    SELECT RAISE(ABORT, 'definition pack revision is immutable');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER definition_pack_entry_delete_is_immutable
BEFORE DELETE ON definition_pack_entries
FOR EACH ROW
BEGIN
    SELECT RAISE(ABORT, 'definition pack entry is immutable');
END;
-- +goose StatementEnd

-- An exact pin must always agree with its configured indexer definition. The
-- update trigger permits the store's transactional replacement sequence, which
-- first removes an old pin and then updates definition_id before inserting the
-- matching new pin.
-- +goose StatementBegin
CREATE TRIGGER indexer_definition_pin_matches_indexer_insert
BEFORE INSERT ON indexer_definition_pins
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1 FROM indexers indexer
    WHERE indexer.id = NEW.indexer_id
      AND indexer.definition_id = NEW.definition_ref
)
BEGIN
    SELECT RAISE(ABORT, 'indexer definition pin does not match indexer definition');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER indexer_definition_pin_matches_indexer_update
BEFORE UPDATE OF indexer_id, definition_ref ON indexer_definition_pins
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1 FROM indexers indexer
    WHERE indexer.id = NEW.indexer_id
      AND indexer.definition_id = NEW.definition_ref
)
BEGIN
    SELECT RAISE(ABORT, 'indexer definition pin does not match indexer definition');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER indexer_definition_update_matches_pin
BEFORE UPDATE OF definition_id ON indexers
FOR EACH ROW
WHEN EXISTS (
    SELECT 1 FROM indexer_definition_pins pin
    WHERE pin.indexer_id = OLD.id
      AND pin.definition_ref != NEW.definition_id
)
BEGIN
    SELECT RAISE(ABORT, 'indexer definition does not match exact pin');
END;
-- +goose StatementEnd
