package store

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// validateDefinitionPackPersistence rejects a structurally valid v5 backup
// whose existing rows bypass the v5 triggers or CHECK constraints. Restore
// validation must check data relationships as well as the sqlite_master DDL.
func definitionPackSourceHasPublicKey(ctx context.Context, db *sql.DB) (bool, error) {
	rows, err := db.QueryContext(ctx, "PRAGMA table_info(definition_pack_sources)")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var cid, notNull, pk int
		var name, typ string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &defaultValue, &pk); err != nil {
			return false, err
		}
		if name == "owner_signer_public_key" {
			return true, nil
		}
	}
	return false, rows.Err()
}

func validateDefinitionPackPersistence(ctx context.Context, db *sql.DB) error {
	return validateDefinitionPackPersistenceMode(ctx, db, false)
}

// validateDefinitionPackPersistenceMode has one migration-preflight exception:
// exact V8 historical keyless failed rows. The exception is deliberately
// narrower than general keyless persistence and is never used after V9.
func validateDefinitionPackPersistenceMode(ctx context.Context, db *sql.DB, allowHistoricalKeylessFailed bool) error {
	hasPublicKey, err := definitionPackSourceHasPublicKey(ctx, db)
	if err != nil {
		return packPersistenceError("inspect source trust key", err)
	}
	owners := make(map[string]definitionPackSourceModel)
	ownerQuery := `SELECT source, owner_signer_key_id, owner_signer_key_fingerprint, created_at, updated_at FROM definition_pack_sources`
	if hasPublicKey {
		ownerQuery = `SELECT source, owner_signer_key_id, owner_signer_key_fingerprint, owner_signer_public_key, created_at, updated_at FROM definition_pack_sources`
	}
	ownerRows, err := db.QueryContext(ctx, ownerQuery)
	if err != nil {
		return packPersistenceError("read source owners", err)
	}
	for ownerRows.Next() {
		var owner definitionPackSourceModel
		if hasPublicKey {
			err = ownerRows.Scan(&owner.Source, &owner.OwnerSignerKeyID, &owner.OwnerSignerKeyFingerprint, &owner.OwnerSignerPublicKey, &owner.CreatedAt, &owner.UpdatedAt)
		} else {
			err = ownerRows.Scan(&owner.Source, &owner.OwnerSignerKeyID, &owner.OwnerSignerKeyFingerprint, &owner.CreatedAt, &owner.UpdatedAt)
		}
		if err != nil {
			ownerRows.Close()
			return packPersistenceError("scan source owner", err)
		}
		if !validDefinitionPackSource(owner.Source) || owner.Source == "builtin" || owner.Source == "user" || owner.Source == "managed" || strings.TrimSpace(owner.OwnerSignerKeyID) == "" || strings.TrimSpace(owner.OwnerSignerKeyID) != owner.OwnerSignerKeyID || !validSHA256Digest(owner.OwnerSignerKeyFingerprint) || (len(owner.OwnerSignerPublicKey) != 0 && !validDefinitionPackPublicKey(owner.OwnerSignerPublicKey, owner.OwnerSignerKeyFingerprint)) {
			ownerRows.Close()
			return packPersistenceError("validate source owner", fmt.Errorf("invalid source %q", owner.Source))
		}
		if _, err := parseRequiredDefinitionPackTime("source created_at", owner.CreatedAt); err != nil {
			ownerRows.Close()
			return packPersistenceError("validate source owner", err)
		}
		if _, err := parseRequiredDefinitionPackTime("source updated_at", owner.UpdatedAt); err != nil {
			ownerRows.Close()
			return packPersistenceError("validate source owner", err)
		}
		owners[owner.Source] = owner
	}
	if err := ownerRows.Close(); err != nil {
		return packPersistenceError("close source owners", err)
	}

	type revisionCounts struct {
		definitions int
		runnable    int
		seenDefs    int
		seenRun     int
	}
	revisions := make(map[string]*revisionCounts)
	sourceRevisionCounts := make(map[string]int, len(owners))
	revisionRows, err := db.QueryContext(ctx, `
		SELECT source, revision, manifest_digest, archive_digest, archive_relpath,
		       license_expression, license_path, license_digest, notice_path, notice_digest,
		       provenance, signer_key_id, signer_key_fingerprint, minimum_caravan_version,
		       install_state, is_pending, is_active, is_last_known_good, validation_error,
		       definition_count, runnable_count, accepted_at, accepted_by_user_id,
		       installed_at, created_at, updated_at
		FROM definition_pack_revisions`)
	if err != nil {
		return packPersistenceError("read revisions", err)
	}
	for revisionRows.Next() {
		var model definitionPackRevisionModel
		if err := revisionRows.Scan(
			&model.Source, &model.Revision, &model.ManifestDigest, &model.ArchiveDigest, &model.ArchiveRelPath,
			&model.LicenseExpression, &model.LicensePath, &model.LicenseDigest, &model.NoticePath, &model.NoticeDigest,
			&model.Provenance, &model.SignerKeyID, &model.SignerKeyFingerprint, &model.MinimumCaravanVersion,
			&model.InstallState, &model.Pending, &model.Active, &model.LastKnownGood, &model.ValidationError,
			&model.DefinitionCount, &model.RunnableCount, &model.AcceptedAt, &model.AcceptedByUserID,
			&model.InstalledAt, &model.CreatedAt, &model.UpdatedAt,
		); err != nil {
			revisionRows.Close()
			return packPersistenceError("scan revision", err)
		}
		revision, err := model.core()
		if err != nil {
			revisionRows.Close()
			return packPersistenceError("validate revision", err)
		}
		owner, ok := owners[revision.Source]
		if !ok || owner.OwnerSignerKeyID != revision.SignerKeyID || owner.OwnerSignerKeyFingerprint != revision.SignerKeyFingerprint {
			revisionRows.Close()
			return packPersistenceError("validate revision owner", fmt.Errorf("revision %s:%s does not match source owner", revision.Source, revision.Revision))
		}
		keylessMissingKeyTombstone := revision.InstallState == core.DefinitionPackFailed && !revision.Pending && !revision.Active && !revision.LastKnownGood && revision.ValidationError == "pack.trust.missing_public_key"
		keylessHistoricalFailed := allowHistoricalKeylessFailed && revision.InstallState == core.DefinitionPackFailed && !revision.Pending && !revision.Active && !revision.LastKnownGood && sanitizeDefinitionPackValidationCode(revision.ValidationError) != ""
		if hasPublicKey && len(owner.OwnerSignerPublicKey) == 0 && !keylessMissingKeyTombstone && !keylessHistoricalFailed {
			revisionRows.Close()
			return packPersistenceError("validate revision trust", fmt.Errorf("revision %s:%s lacks accepted public key", revision.Source, revision.Revision))
		}
		if revision.DefinitionCount < 1 || revision.RunnableCount < 0 || revision.RunnableCount > revision.DefinitionCount {
			revisionRows.Close()
			return packPersistenceError("validate revision counts", fmt.Errorf("revision %s:%s has invalid counts", revision.Source, revision.Revision))
		}
		if strings.TrimSpace(revision.Revision) != revision.Revision || revision.Revision == "" || len(revision.Revision) > 256 ||
			strings.TrimSpace(revision.SignerKeyID) == "" || strings.TrimSpace(revision.SignerKeyID) != revision.SignerKeyID ||
			strings.TrimSpace(revision.LicenseExpression) == "" || strings.TrimSpace(revision.Provenance) == "" ||
			strings.TrimSpace(revision.MinimumCaravanVersion) == "" || (revision.NoticePath == "") != (revision.NoticeDigest == "") {
			revisionRows.Close()
			return packPersistenceError("validate revision identity", fmt.Errorf("revision %s:%s has invalid identity fields", revision.Source, revision.Revision))
		}
		key := revision.Source + "\x00" + revision.Revision
		revisions[key] = &revisionCounts{definitions: revision.DefinitionCount, runnable: revision.RunnableCount}
		sourceRevisionCounts[revision.Source]++
	}
	if err := revisionRows.Close(); err != nil {
		return packPersistenceError("close revisions", err)
	}
	for source := range owners {
		if sourceRevisionCounts[source] == 0 {
			return packPersistenceError("validate source revisions", fmt.Errorf("source %q owns no revision", source))
		}
	}

	entryRows, err := db.QueryContext(ctx, `
		SELECT source, revision, definition_ref, metadata_id, path, digest, state,
		       unsupported_json, approved_origins_json
		FROM definition_pack_entries`)
	if err != nil {
		return packPersistenceError("read entries", err)
	}
	for entryRows.Next() {
		var model definitionPackEntryModel
		if err := entryRows.Scan(&model.Source, &model.Revision, &model.DefinitionRef, &model.MetadataID, &model.Path, &model.Digest, &model.State, &model.UnsupportedJSON, &model.ApprovedOriginsJSON); err != nil {
			entryRows.Close()
			return packPersistenceError("scan entry", err)
		}
		entry, err := model.core()
		if err != nil {
			entryRows.Close()
			return packPersistenceError("validate entry", err)
		}
		if !validDefinitionPackReference(entry.DefinitionRef, entry.Source) || !validSHA256Digest(entry.Digest) || !validPackRelativePath(entry.Path) || !strings.HasPrefix(entry.Path, "definitions/") || strings.TrimSpace(entry.MetadataID) == "" || (entry.State != core.DefinitionPackEntryUnsupported && entry.State != core.DefinitionPackEntryRunnableUnverified) {
			entryRows.Close()
			return packPersistenceError("validate entry identity", fmt.Errorf("entry %q is invalid", entry.DefinitionRef))
		}
		for _, origin := range entry.ApprovedOrigins {
			if !validDefinitionPackOrigin(origin) {
				entryRows.Close()
				return packPersistenceError("validate entry origin", fmt.Errorf("entry %q has invalid origin", entry.DefinitionRef))
			}
		}
		if entry.State == core.DefinitionPackEntryRunnableUnverified && len(entry.Unsupported) != 0 {
			entryRows.Close()
			return packPersistenceError("validate entry state", fmt.Errorf("entry %q is runnable with blockers", entry.DefinitionRef))
		}
		counts, ok := revisions[entry.Source+"\x00"+entry.Revision]
		if !ok {
			entryRows.Close()
			return packPersistenceError("validate entry revision", fmt.Errorf("entry %q has no revision", entry.DefinitionRef))
		}
		counts.seenDefs++
		if entry.State == core.DefinitionPackEntryRunnableUnverified {
			counts.seenRun++
		}
	}
	if err := entryRows.Close(); err != nil {
		return packPersistenceError("close entries", err)
	}
	for key, counts := range revisions {
		if counts.seenDefs != counts.definitions || counts.seenRun != counts.runnable {
			return packPersistenceError("validate revision entry counts", fmt.Errorf("revision %q count mismatch", key))
		}
	}
	return validateDefinitionPackPins(ctx, db)
}

func validateDefinitionPackPins(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `
		SELECT pin.indexer_id, pin.source, pin.revision, pin.definition_ref, pin.digest,
		       indexer.id IS NOT NULL, COALESCE(indexer.definition_id, ''),
		       entry.definition_ref IS NOT NULL
		FROM indexer_definition_pins pin
		LEFT JOIN indexers indexer ON indexer.id = pin.indexer_id
		LEFT JOIN definition_pack_entries entry
		  ON entry.source = pin.source
		 AND entry.revision = pin.revision
		 AND entry.definition_ref = pin.definition_ref
		 AND entry.digest = pin.digest`)
	if err != nil {
		return packPersistenceError("read indexer pins", err)
	}
	defer rows.Close()
	for rows.Next() {
		var indexerID int64
		var source, revision, definitionRef, digest, indexerDefinition string
		var indexerExists, entryExists bool
		if err := rows.Scan(&indexerID, &source, &revision, &definitionRef, &digest, &indexerExists, &indexerDefinition, &entryExists); err != nil {
			return packPersistenceError("scan indexer pin", err)
		}
		if indexerID <= 0 || !indexerExists || !entryExists ||
			!validDefinitionPackReference(definitionRef, source) ||
			strings.TrimSpace(revision) != revision || revision == "" || len(revision) > 256 ||
			!validSHA256Digest(digest) || definitionRef != indexerDefinition {
			return packPersistenceError("validate indexer pin", fmt.Errorf("indexer %d has an invalid exact definition pin", indexerID))
		}
	}
	if err := rows.Err(); err != nil {
		return packPersistenceError("read indexer pins", err)
	}
	return nil
}

func packPersistenceError(action string, err error) error {
	return fmt.Errorf("%w: definition pack persistence: %s: %v", ErrUnrecognizedSchema, action, err)
}
