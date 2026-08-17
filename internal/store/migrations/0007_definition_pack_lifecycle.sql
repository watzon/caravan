-- +goose Up

-- Lifecycle is a server-side state machine. 0006 preserved immutable identity;
-- this migration makes invalid flag combinations impossible even for direct SQL.
-- A fresh installed receipt is intentionally inert. Promotion is an UPDATE only
-- after isolated exact-byte revalidation.
-- +goose StatementBegin
CREATE TRIGGER definition_pack_revision_lifecycle_insert_is_fresh
BEFORE INSERT ON definition_pack_revisions
FOR EACH ROW
WHEN (NEW.install_state = 'installed' AND (NEW.is_pending != 0 OR NEW.is_active != 0 OR NEW.is_last_known_good != 0))
  OR (NEW.is_pending != 0 AND (NEW.is_active != 0 OR NEW.is_last_known_good != 0))
  OR (NEW.install_state = 'failed' AND (NEW.is_pending != 0 OR NEW.is_active != 0 OR NEW.is_last_known_good != 0))
  OR length(NEW.validation_error) > 96
  OR (NEW.validation_error != '' AND NEW.validation_error GLOB '*[^a-z0-9._-]*')
BEGIN
    SELECT RAISE(ABORT, 'definition pack lifecycle insert is invalid');
END;
-- +goose StatementEnd

-- +goose StatementBegin
CREATE TRIGGER definition_pack_revision_lifecycle_update_is_valid
BEFORE UPDATE OF install_state, is_pending, is_active, is_last_known_good, validation_error
ON definition_pack_revisions
FOR EACH ROW
WHEN (NEW.is_pending != 0 AND (NEW.is_active != 0 OR NEW.is_last_known_good != 0))
  OR (NEW.install_state = 'failed' AND (NEW.is_pending != 0 OR NEW.is_active != 0 OR NEW.is_last_known_good != 0))
  OR length(NEW.validation_error) > 96
  OR (NEW.validation_error != '' AND NEW.validation_error GLOB '*[^a-z0-9._-]*')
BEGIN
    SELECT RAISE(ABORT, 'definition pack lifecycle update is invalid');
END;
-- +goose StatementEnd
