package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/core"
	storemigrations "github.com/watzon/caravan/internal/store/migrations"
)

const interimVersionFiveFingerprint = "13a989b874b43a87032285fefe4892194ac383e7cce79871d69675ced5931af6"
const finalizedVersionFiveFingerprint = "97f402e89f85778e8cea7c9408b94ec93ab9694947b5f00f66b9112f8dd9591e"

func TestInterimVersionFiveMigratesToLatestWithoutDataLoss(t *testing.T) {
	ctx := context.Background()
	path := createVersionFiveFixture(t, true)

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open interim v5: %v", err)
	}
	defer st.Close()
	version, err := st.SchemaVersion()
	if err != nil {
		t.Fatal(err)
	}
	if version != int(storemigrations.LatestVersion) {
		t.Fatalf("schema version = %d, want %d", version, storemigrations.LatestVersion)
	}
	revision, err := st.GetDefinitionPackRevision(ctx, "community", "2026.08.14")
	if err != nil {
		t.Fatal(err)
	}
	if revision.ArchiveDigest != "sha256:1111111111111111111111111111111111111111111111111111111111111111" {
		t.Fatalf("archive digest = %q", revision.ArchiveDigest)
	}
	entries, err := st.ListDefinitionPackEntries(ctx, "community", "2026.08.14")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].DefinitionRef != "community:first" {
		t.Fatalf("migrated entries = %+v", entries)
	}
	indexer, err := st.GetIndexer(ctx, 41)
	if err != nil {
		t.Fatal(err)
	}
	if indexer.DefinitionSource != "community" || indexer.DefinitionRevision != "2026.08.14" ||
		indexer.DefinitionDigest != "sha256:4444444444444444444444444444444444444444444444444444444444444444" {
		t.Fatalf("migrated exact definition pin = %+v", indexer)
	}
	if indexer.DefinitionID != "community:first" || indexer.Type != core.IndexerTypeTorznab {
		t.Fatalf("migrated indexer identity = %+v", indexer)
	}
}

func TestVersionFiveRejectsInvalidExactPinsBeforeMigration(t *testing.T) {
	variants := []struct {
		name    string
		interim bool
	}{
		{name: "interim", interim: true},
		{name: "finalized", interim: false},
	}
	mutations := []struct {
		name string
		sql  string
	}{
		{name: "mismatched definition", sql: `UPDATE indexers SET definition_id = 'community:other' WHERE id = 41`},
		{name: "orphaned indexer", sql: `DELETE FROM indexers WHERE id = 41`},
	}
	for _, variant := range variants {
		for _, mutation := range mutations {
			t.Run(variant.name+"/"+mutation.name, func(t *testing.T) {
				path := createVersionFiveFixture(t, variant.interim)
				db, err := sql.Open("sqlite", dsn(path))
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec("PRAGMA foreign_keys = OFF; " + mutation.sql); err != nil {
					db.Close()
					t.Fatal(err)
				}
				if err := db.Close(); err != nil {
					t.Fatal(err)
				}

				if _, err := Open(path); !errors.Is(err, ErrUnrecognizedSchema) {
					t.Fatalf("Open invalid v5 pin error = %v, want ErrUnrecognizedSchema", err)
				}
				db, err = sql.Open("sqlite", readOnlySchemaDSN(path))
				if err != nil {
					t.Fatal(err)
				}
				version, err := schemaVersion(context.Background(), db)
				db.Close()
				if err != nil {
					t.Fatal(err)
				}
				if version != 5 {
					t.Fatalf("rejected database migrated to version %d, want 5", version)
				}
			})
		}
	}
}

func createVersionFiveFixture(t *testing.T, interim bool) string {
	t.Helper()
	ctx := context.Background()
	name := "finalized-v5.sqlite"
	if interim {
		name = "interim-v5.sqlite"
	}
	path := filepath.Join(t.TempDir(), name)
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	provider, err := migrationProvider(db, storemigrations.FS())
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	wantFingerprint := finalizedVersionFiveFingerprint
	if interim {
		if _, err := provider.UpTo(ctx, 4); err != nil {
			db.Close()
			t.Fatalf("apply v4: %v", err)
		}
		interimSchema, err := os.ReadFile(filepath.Join("testdata", "interim_v5_definition_packs.sql"))
		if err != nil {
			db.Close()
			t.Fatal(err)
		}
		if _, err := db.ExecContext(ctx, string(interimSchema)); err != nil {
			db.Close()
			t.Fatalf("apply interim v5 schema: %v", err)
		}
		if _, err := db.ExecContext(ctx, `INSERT INTO caravan_schema_migrations (version_id, is_applied) VALUES (5, 1)`); err != nil {
			db.Close()
			t.Fatal(err)
		}
		wantFingerprint = interimVersionFiveFingerprint
	} else if _, err := provider.UpTo(ctx, 5); err != nil {
		db.Close()
		t.Fatalf("apply finalized v5: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO definition_pack_sources (
			source, owner_signer_key_id, owner_signer_key_fingerprint, created_at, updated_at
		) VALUES (
			'community', 'test-key', 'sha256:3333333333333333333333333333333333333333333333333333333333333333',
			'2026-08-14T22:00:00Z', '2026-08-14T22:00:00Z'
		);
		INSERT INTO definition_pack_revisions (
			source, revision, manifest_digest, archive_digest, archive_relpath,
			license_expression, license_path, license_digest, provenance,
			signer_key_id, signer_key_fingerprint, minimum_caravan_version,
			install_state, definition_count, runnable_count, accepted_at,
			accepted_by_user_id, installed_at, created_at, updated_at
		) VALUES (
			'community', '2026.08.14',
			'sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa',
			'sha256:1111111111111111111111111111111111111111111111111111111111111111',
			'archives/sha256/11/1111111111111111111111111111111111111111111111111111111111111111.caravan-indexer-pack',
			'MIT', 'LICENSE',
			'sha256:2222222222222222222222222222222222222222222222222222222222222222',
			'synthetic migration fixture', 'test-key',
			'sha256:3333333333333333333333333333333333333333333333333333333333333333',
			'0.1.0', 'installed', 1, 1, '2026-08-14T22:00:00Z', 7,
			'2026-08-14T22:00:00Z', '2026-08-14T22:00:00Z', '2026-08-14T22:00:00Z'
		);
		INSERT INTO definition_pack_entries (
			source, revision, definition_ref, metadata_id, path, digest, state,
			unsupported_json, approved_origins_json
		) VALUES (
			'community', '2026.08.14', 'community:first', 'first-site',
			'definitions/first.yml',
			'sha256:4444444444444444444444444444444444444444444444444444444444444444',
			'runnable-unverified', '[]', '["https://tracker.example"]'
		);
		INSERT INTO indexers (
			id, name, protocol, url, api_key, categories, priority, enabled,
			definition_id, settings, created_at, updated_at
		) VALUES (
			41, 'preserved-pack-indexer', 'torznab', 'https://tracker.example', '',
			'[2000]', 7, 0, 'community:first', '{}',
			'2026-08-14T22:00:00Z', '2026-08-14T22:00:00Z'
		);
		INSERT INTO indexer_definition_pins (
			indexer_id, source, revision, definition_ref, digest
		) VALUES (
			41, 'community', '2026.08.14', 'community:first',
			'sha256:4444444444444444444444444444444444444444444444444444444444444444'
		);
	`); err != nil {
		db.Close()
		t.Fatalf("seed v5: %v", err)
	}
	fingerprint, err := schemaFingerprint(ctx, db)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if fingerprint != wantFingerprint {
		db.Close()
		t.Fatalf("v5 fingerprint = %s, want %s", fingerprint, wantFingerprint)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}
