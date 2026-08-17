-- +goose Up

-- 0008 left pre-existing source rows with an empty owner_signer_public_key
-- and made the row fully immutable, so those namespaces could never be
-- reinstalled: the empty stored key can never equal a real signer key.
-- Permit exactly one transition — empty key to a 32-byte key — while the key
-- id and fingerprint stay immutable forever. The store layer additionally
-- requires the adopted key to hash to the stored fingerprint, so the
-- original owner (and only the original owner) can reclaim the namespace.
DROP TRIGGER definition_pack_source_public_key_is_immutable;

-- +goose StatementBegin
CREATE TRIGGER definition_pack_source_public_key_is_immutable
BEFORE UPDATE OF owner_signer_key_id, owner_signer_key_fingerprint, owner_signer_public_key
ON definition_pack_sources
FOR EACH ROW
WHEN OLD.owner_signer_key_id != NEW.owner_signer_key_id
  OR OLD.owner_signer_key_fingerprint != NEW.owner_signer_key_fingerprint
  OR (OLD.owner_signer_public_key != NEW.owner_signer_public_key
      AND (length(OLD.owner_signer_public_key) != 0 OR length(NEW.owner_signer_public_key) != 32))
BEGIN
    SELECT RAISE(ABORT, 'definition pack source trust key is immutable');
END;
-- +goose StatementEnd
