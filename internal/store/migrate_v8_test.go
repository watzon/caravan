package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"path/filepath"
	"testing"

	storemigrations "github.com/watzon/caravan/internal/store/migrations"
)

// Regression for two V8 upgrade defects:
//  1. A real v8 database holding a keyless failed revision whose validation
//     code is NOT the missing-key tombstone (the shape 0009 exists to repair)
//     must open and migrate instead of being rejected as unrecognized.
//  2. After the upgrade the keyless source namespace must be reclaimable by
//     the original owner: the key that hashes to the stored fingerprint may
//     be adopted; any other signer stays rejected.
func TestVersionEightKeylessDatabaseMigratesAndReclaimsSource(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "v8-keyless.sqlite")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	provider, err := migrationProvider(db, storemigrations.FS())
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 7); err != nil {
		db.Close()
		t.Fatalf("apply v7: %v", err)
	}

	ownerKey := []byte("0123456789abcdefghijklmnopqrstuv")
	ownerFingerprint := sha256.Sum256(ownerKey)
	fingerprint := "sha256:" + hex.EncodeToString(ownerFingerprint[:])
	if _, err := db.ExecContext(ctx, `
		INSERT INTO definition_pack_sources (
			source, owner_signer_key_id, owner_signer_key_fingerprint, created_at, updated_at
		) VALUES ('community', 'test-key', ?, '2026-08-13T22:00:00Z', '2026-08-13T22:00:00Z');
		INSERT INTO definition_pack_revisions (
			source, revision, manifest_digest, archive_digest, archive_relpath,
			license_expression, license_path, license_digest, provenance,
			signer_key_id, signer_key_fingerprint, minimum_caravan_version,
			install_state, validation_error, definition_count, runnable_count, accepted_at,
			accepted_by_user_id, installed_at, created_at, updated_at
		) VALUES (
			'community', '2026.08.13',
			'sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb',
			'sha256:6666666666666666666666666666666666666666666666666666666666666666',
			'archives/sha256/66/6666666666666666666666666666666666666666666666666666666666666666.caravan-indexer-pack',
			'MIT', 'LICENSE',
			'sha256:2222222222222222222222222222222222222222222222222222222222222222',
			'synthetic migration fixture', 'test-key', ?, '0.1.0',
			'failed', 'pack.archive.digest_mismatch', 1, 1, '2026-08-13T22:00:00Z', 7,
			'2026-08-13T22:00:00Z', '2026-08-13T22:00:00Z', '2026-08-13T22:00:00Z'
		);
		INSERT INTO definition_pack_entries (
			source, revision, definition_ref, metadata_id, path, digest, state,
			unsupported_json, approved_origins_json
		) VALUES (
			'community', '2026.08.13', 'community:first', 'first-site',
			'definitions/first.yml',
			'sha256:7777777777777777777777777777777777777777777777777777777777777777',
			'runnable-unverified', '[]', '["https://tracker.example"]'
		);
	`, fingerprint, fingerprint); err != nil {
		db.Close()
		t.Fatalf("seed v7 pack rows: %v", err)
	}
	if _, err := provider.UpTo(ctx, 8); err != nil {
		db.Close()
		t.Fatalf("apply v8: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("open v8 keyless database: %v", err)
	}
	defer st.Close()
	version, err := st.SchemaVersion()
	if err != nil || int64(version) != storemigrations.LatestVersion {
		t.Fatalf("schema version=%d err=%v, want %d", version, err, storemigrations.LatestVersion)
	}
	var code string
	if err := st.DB().QueryRowContext(ctx, `
		SELECT validation_error FROM definition_pack_revisions
		WHERE source = 'community' AND revision = '2026.08.13'`).Scan(&code); err != nil {
		t.Fatal(err)
	}
	if code != "pack.trust.missing_public_key" {
		t.Fatalf("repaired validation_error = %q, want missing-key tombstone", code)
	}

	// A different signer must not claim the keyless namespace.
	intruder, intruderEntries := syntheticPackRevision()
	intruder.Revision = "2026.08.15"
	intruder.ManifestDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	intruder.ArchiveDigest = "sha256:8888888888888888888888888888888888888888888888888888888888888888"
	intruder.ArchiveRelPath = "archives/sha256/88/8888888888888888888888888888888888888888888888888888888888888888.caravan-indexer-pack"
	intruder.SignerPublicKey = []byte("vutsrqponmlkjihgfedcba9876543210")
	intruderSum := sha256.Sum256(intruder.SignerPublicKey)
	intruder.SignerKeyFingerprint = "sha256:" + hex.EncodeToString(intruderSum[:])
	for i := range intruderEntries {
		intruderEntries[i].Revision = intruder.Revision
	}
	if err := st.InstallDefinitionPackRevision(ctx, &intruder, intruderEntries); !errors.Is(err, ErrConflict) {
		t.Fatalf("intruder install error = %v, want ErrConflict", err)
	}

	// The original owner (the key hashing to the stored fingerprint) can.
	owner, ownerEntries := syntheticPackRevision()
	owner.Revision = "2026.08.15"
	owner.ManifestDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	owner.ArchiveDigest = "sha256:8888888888888888888888888888888888888888888888888888888888888888"
	owner.ArchiveRelPath = "archives/sha256/88/8888888888888888888888888888888888888888888888888888888888888888.caravan-indexer-pack"
	for i := range ownerEntries {
		ownerEntries[i].Revision = owner.Revision
	}
	if err := st.InstallDefinitionPackRevision(ctx, &owner, ownerEntries); err != nil {
		t.Fatalf("owner reclaim install: %v", err)
	}
	var storedKey []byte
	if err := st.DB().QueryRowContext(ctx, `
		SELECT owner_signer_public_key FROM definition_pack_sources WHERE source = 'community'`).Scan(&storedKey); err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(storedKey) != hex.EncodeToString(ownerKey) {
		t.Fatalf("stored key = %x, want adopted owner key", storedKey)
	}
}
