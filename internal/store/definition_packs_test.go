package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	storemigrations "github.com/watzon/caravan/internal/store/migrations"
)

func TestInstallDefinitionPackRevisionIsImmutableAndIdempotent(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	revision, entries := syntheticPackRevision()
	if err := st.InstallDefinitionPackRevision(ctx, &revision, entries); err != nil {
		t.Fatalf("InstallDefinitionPackRevision: %v", err)
	}
	if err := st.InstallDefinitionPackRevision(ctx, &revision, entries); err != nil {
		t.Fatalf("idempotent InstallDefinitionPackRevision: %v", err)
	}

	got, err := st.GetDefinitionPackRevision(ctx, revision.Source, revision.Revision)
	if err != nil {
		t.Fatalf("GetDefinitionPackRevision: %v", err)
	}
	if got.ManifestDigest != revision.ManifestDigest || got.ArchiveDigest != revision.ArchiveDigest || got.InstallState != core.DefinitionPackInstalled || !got.AcceptedAt.Equal(revision.AcceptedAt) {
		t.Fatalf("stored revision = %+v", got)
	}
	gotEntries, err := st.ListDefinitionPackEntries(ctx, revision.Source, revision.Revision)
	if err != nil {
		t.Fatalf("ListDefinitionPackEntries: %v", err)
	}
	if len(gotEntries) != 2 || gotEntries[0].DefinitionRef != "community:first" || gotEntries[1].State != core.DefinitionPackEntryUnsupported {
		t.Fatalf("stored entries = %+v", gotEntries)
	}

	conflict := revision
	conflict.ManifestDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	if err := st.InstallDefinitionPackRevision(ctx, &conflict, entries); !errors.Is(err, ErrConflict) {
		t.Fatalf("revision collision error = %v, want ErrConflict", err)
	}
	otherSigner := revision
	otherSigner.Revision = "2026.08.15"
	otherSigner.ManifestDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	otherSigner.ArchiveDigest = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	otherSigner.ArchiveRelPath = "archives/sha256/dd/dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd.caravan-indexer-pack"
	otherSigner.SignerKeyID = "other-key"
	otherSigner.SignerPublicKey = []byte("abcdefghijklmnopqrstuvwxyz012345")
	otherSignerFingerprint := sha256.Sum256(otherSigner.SignerPublicKey)
	otherSigner.SignerKeyFingerprint = "sha256:" + hex.EncodeToString(otherSignerFingerprint[:])
	otherEntries := append([]core.DefinitionPackEntry(nil), entries...)
	for i := range otherEntries {
		otherEntries[i].Revision = otherSigner.Revision
	}
	if err := st.InstallDefinitionPackRevision(ctx, &otherSigner, otherEntries); !errors.Is(err, ErrConflict) {
		t.Fatalf("source signer collision error = %v, want ErrConflict", err)
	}
}

func TestDefinitionPackInstallBindsSourceToExactPublicKey(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	revision, entries := syntheticPackRevision()
	revision.SignerPublicKey = []byte("0123456789abcdefghijklmnopqrstuv")
	fingerprint := sha256.Sum256(revision.SignerPublicKey)
	revision.SignerKeyFingerprint = "sha256:" + hex.EncodeToString(fingerprint[:])
	if err := st.InstallDefinitionPackRevision(ctx, &revision, entries); err != nil {
		t.Fatalf("InstallDefinitionPackRevision: %v", err)
	}
	var stored []byte
	if err := st.DB().QueryRowContext(ctx, "SELECT owner_signer_public_key FROM definition_pack_sources WHERE source = ?", revision.Source).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(stored, revision.SignerPublicKey) {
		t.Fatalf("stored public key = %x, want %x", stored, revision.SignerPublicKey)
	}
	other := revision
	other.Revision = "other"
	other.ManifestDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	other.ArchiveDigest = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	other.ArchiveRelPath = "archives/sha256/dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd.zip"
	other.SignerPublicKey = []byte("abcdefghijklmnopqrstuvwxyz012345")
	otherFingerprint := sha256.Sum256(other.SignerPublicKey)
	other.SignerKeyFingerprint = "sha256:" + hex.EncodeToString(otherFingerprint[:])
	entries = append([]core.DefinitionPackEntry(nil), entries...)
	for i := range entries {
		entries[i].Revision = other.Revision
	}
	if err := st.InstallDefinitionPackRevision(ctx, &other, entries); !errors.Is(err, ErrConflict) {
		t.Fatalf("source accepted a different public key: %v", err)
	}
}

func TestIndexerPackPinIsTransactionalAndExact(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	revision, entries := syntheticPackRevision()
	if err := st.InstallDefinitionPackRevision(ctx, &revision, entries); err != nil {
		t.Fatal(err)
	}

	configured := core.IndexerConfig{
		DefinitionID:       "community:first",
		DefinitionSource:   revision.Source,
		DefinitionRevision: revision.Revision,
		DefinitionDigest:   entries[0].Digest,
		Name:               "Pinned pack indexer",
		URL:                "https://tracker.example",
		Type:               core.IndexerTypeTorznab,
		Categories:         []int{2000},
		Priority:           25,
		Enabled:            false,
	}
	if err := st.UpsertIndexer(ctx, &configured); err != nil {
		t.Fatalf("UpsertIndexer pinned: %v", err)
	}
	got, err := st.GetIndexer(ctx, configured.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.DefinitionSource != revision.Source || got.DefinitionRevision != revision.Revision || got.DefinitionDigest != entries[0].Digest {
		t.Fatalf("stored pin = %+v", got)
	}

	invalid := configured
	invalid.ID = 0
	invalid.Name = "Invalid pin"
	invalid.DefinitionDigest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if err := st.UpsertIndexer(ctx, &invalid); err == nil {
		t.Fatal("UpsertIndexer accepted a pin to missing definition bytes")
	}
	var invalidRows int
	if err := st.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM indexers WHERE name = ?", invalid.Name).Scan(&invalidRows); err != nil {
		t.Fatal(err)
	}
	if invalidRows != 0 {
		t.Fatalf("failed pin left %d indexer rows", invalidRows)
	}

	invalidUpdate := *got
	invalidUpdate.Name = "Should roll back"
	invalidUpdate.DefinitionDigest = "sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	if err := st.UpsertIndexer(ctx, &invalidUpdate); err == nil {
		t.Fatal("UpsertIndexer accepted invalid replacement pin")
	}
	preserved, err := st.GetIndexer(ctx, configured.ID)
	if err != nil {
		t.Fatal(err)
	}
	if preserved.Name != configured.Name || preserved.DefinitionDigest != entries[0].Digest {
		t.Fatalf("failed pin update changed stored indexer: %+v", preserved)
	}

	if err := st.DeleteIndexer(ctx, configured.ID); err != nil {
		t.Fatal(err)
	}
	var pins int
	if err := st.DB().QueryRowContext(ctx, "SELECT COUNT(*) FROM indexer_definition_pins WHERE indexer_id = ?", configured.ID).Scan(&pins); err != nil {
		t.Fatal(err)
	}
	if pins != 0 {
		t.Fatalf("deleted indexer left %d pins", pins)
	}
}

func TestDefinitionPackInstallRejectsCallerLifecycleFlagsAndPendingCannotTargetActive(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	revision, entries := syntheticPackRevision()
	revision.Pending = true
	if err := st.InstallDefinitionPackRevision(ctx, &revision, entries); err == nil {
		t.Fatal("install accepted caller-controlled pending lifecycle flag")
	}
	revision.Pending = false
	if err := st.InstallDefinitionPackRevision(ctx, &revision, entries); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDefinitionPackPending(ctx, revision.Source, revision.Revision); err != nil {
		t.Fatal(err)
	}
	if err := st.PromotePendingDefinitionPack(ctx, revision.Source, revision.Revision); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDefinitionPackPending(ctx, revision.Source, revision.Revision); err == nil {
		t.Fatal("MarkDefinitionPackPending accepted an active/LKG revision")
	}
}

func TestDefinitionPackLifecyclePromotesPendingAndRollsBackToLastKnownGood(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	first, firstEntries := syntheticPackRevision()
	if err := st.InstallDefinitionPackRevision(ctx, &first, firstEntries); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDefinitionPackPending(ctx, first.Source, first.Revision); err != nil {
		t.Fatal(err)
	}
	if err := st.PromotePendingDefinitionPack(ctx, first.Source, first.Revision); err != nil {
		t.Fatal(err)
	}

	second := first
	second.Revision = "2026.08.15"
	second.ManifestDigest = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	second.ArchiveDigest = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	second.ArchiveRelPath = "archives/sha256/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.zip"
	second.InstalledAt = second.InstalledAt.Add(time.Second)
	secondEntries := append([]core.DefinitionPackEntry(nil), firstEntries...)
	for i := range secondEntries {
		secondEntries[i].Revision = second.Revision
	}
	if err := st.InstallDefinitionPackRevision(ctx, &second, secondEntries); err != nil {
		t.Fatal(err)
	}
	if err := st.MarkDefinitionPackPending(ctx, second.Source, second.Revision); err != nil {
		t.Fatal(err)
	}
	if err := st.RollbackPendingDefinitionPack(ctx, second.Source, second.Revision, "pack.registry.resolution_failed"); err != nil {
		t.Fatal(err)
	}
	active, err := st.GetActiveDefinitionPackRevisions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].Revision != first.Revision || !active[0].Active || !active[0].LastKnownGood {
		t.Fatalf("active revisions = %+v", active)
	}
	rolledBack, err := st.GetDefinitionPackRevision(ctx, second.Source, second.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if rolledBack.InstallState != core.DefinitionPackFailed || rolledBack.Pending || rolledBack.Active || rolledBack.LastKnownGood || rolledBack.ValidationError == "" {
		t.Fatalf("rolled back revision = %+v", rolledBack)
	}
}

func TestDefinitionPackSchemaRejectsInvalidLifecycleCombinations(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	revision, entries := syntheticPackRevision()
	if err := st.InstallDefinitionPackRevision(ctx, &revision, entries); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		"UPDATE definition_pack_revisions SET is_pending = 1, is_active = 1 WHERE source = 'community' AND revision = '2026.08.14'",
		"UPDATE definition_pack_revisions SET install_state = 'failed', validation_error = 'pack.failed', is_last_known_good = 1 WHERE source = 'community' AND revision = '2026.08.14'",
	} {
		if _, err := st.DB().ExecContext(ctx, statement); err == nil {
			t.Fatalf("invalid lifecycle combination accepted: %s", statement)
		}
	}
}

func TestDefinitionPackSchemaProtectsOwnershipIdentityAndSecurityTriggers(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	revision, entries := syntheticPackRevision()
	if err := st.InstallDefinitionPackRevision(ctx, &revision, entries); err != nil {
		t.Fatal(err)
	}
	updates := []string{
		"UPDATE definition_pack_sources SET owner_signer_key_id = 'other' WHERE source = 'community'",
		"UPDATE definition_pack_revisions SET manifest_digest = 'sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff' WHERE source = 'community' AND revision = '2026.08.14'",
		"UPDATE definition_pack_entries SET digest = 'sha256:ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff' WHERE definition_ref = 'community:first'",
	}
	for _, statement := range updates {
		if _, err := st.DB().ExecContext(ctx, statement); err == nil {
			t.Fatalf("immutable pack row accepted direct update: %s", statement)
		}
	}
	if err := validateCurrentSchema(ctx, st.DB()); err != nil {
		t.Fatalf("valid v5 schema rejected: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx, "DROP TRIGGER definition_pack_revision_signer_matches_source"); err != nil {
		t.Fatal(err)
	}
	if err := validateCurrentSchema(ctx, st.DB()); !errors.Is(err, ErrUnrecognizedSchema) {
		t.Fatalf("schema without security trigger error = %v, want ErrUnrecognizedSchema", err)
	}
}

func TestDefinitionPackRestoreIdentityRejectsRowsThatBypassedTriggers(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	revision, entries := syntheticPackRevision()
	if err := st.InstallDefinitionPackRevision(ctx, &revision, entries); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, "DROP TRIGGER definition_pack_source_owner_is_immutable; DROP TRIGGER definition_pack_source_public_key_is_immutable"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, "UPDATE definition_pack_sources SET owner_signer_key_id = 'replacement' WHERE source = 'community'"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `
		CREATE TRIGGER definition_pack_source_owner_is_immutable
		BEFORE UPDATE OF owner_signer_key_id, owner_signer_key_fingerprint ON definition_pack_sources
		FOR EACH ROW
		WHEN OLD.owner_signer_key_id != NEW.owner_signer_key_id
		  OR OLD.owner_signer_key_fingerprint != NEW.owner_signer_key_fingerprint
		BEGIN
		    SELECT RAISE(ABORT, 'definition pack source owner is immutable');
		END`); err != nil {
		t.Fatal(err)
	}
	if err := validateDefinitionPackPersistence(ctx, st.DB()); !errors.Is(err, ErrUnrecognizedSchema) {
		t.Fatalf("owner/revision mismatch error = %v, want ErrUnrecognizedSchema", err)
	}
}

func TestDefinitionPackRestoreIdentityRejectsMalformedEntryRows(t *testing.T) {
	tests := []struct {
		name      string
		statement string
	}{
		{name: "non-yml path", statement: "UPDATE definition_pack_entries SET path = 'definitions/first.json' WHERE definition_ref = 'community:first'"},
		{name: "non-origin URL", statement: `UPDATE definition_pack_entries SET approved_origins_json = '["https://tracker.example/path"]' WHERE definition_ref = 'community:first'`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			st, _ := openTemp(t)
			ctx := context.Background()
			revision, entries := syntheticPackRevision()
			if err := st.InstallDefinitionPackRevision(ctx, &revision, entries); err != nil {
				t.Fatal(err)
			}
			if _, err := st.DB().ExecContext(ctx, "DROP TRIGGER definition_pack_entry_is_immutable"); err != nil {
				t.Fatal(err)
			}
			if _, err := st.DB().ExecContext(ctx, test.statement); err != nil {
				t.Fatal(err)
			}
			if err := validateDefinitionPackPersistence(ctx, st.DB()); !errors.Is(err, ErrUnrecognizedSchema) {
				t.Fatalf("semantic restore validation error = %v, want ErrUnrecognizedSchema", err)
			}
		})
	}
}

func TestDefinitionPackValidationRejectsReservedAndMalformedIdentity(t *testing.T) {
	base, baseEntries := syntheticPackRevision()
	tests := []struct {
		name   string
		mutate func(*core.DefinitionPackRevision, []core.DefinitionPackEntry)
	}{
		{name: "reserved source", mutate: func(revision *core.DefinitionPackRevision, entries []core.DefinitionPackEntry) {
			revision.Source = "builtin"
			for i := range entries {
				entries[i].Source = "builtin"
				entries[i].DefinitionRef = "builtin:" + strings.TrimPrefix(entries[i].DefinitionRef, "community:")
			}
		}},
		{name: "short digest", mutate: func(revision *core.DefinitionPackRevision, _ []core.DefinitionPackEntry) {
			revision.ManifestDigest = "sha256:x"
		}},
		{name: "uppercase digest", mutate: func(revision *core.DefinitionPackRevision, _ []core.DefinitionPackEntry) {
			revision.ArchiveDigest = "sha256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		}},
		{name: "installed without timestamp", mutate: func(revision *core.DefinitionPackRevision, _ []core.DefinitionPackEntry) {
			revision.InstalledAt = time.Time{}
		}},
		{name: "installing with timestamp", mutate: func(revision *core.DefinitionPackRevision, _ []core.DefinitionPackEntry) {
			revision.InstallState = core.DefinitionPackInstalling
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			revision := base
			entries := append([]core.DefinitionPackEntry(nil), baseEntries...)
			test.mutate(&revision, entries)
			if err := validateDefinitionPackRevision(revision, entries); err == nil {
				t.Fatal("malformed definition pack identity was accepted")
			}
		})
	}
}

func TestDefinitionPackRestoreIdentityRejectsMismatchedIndexerPin(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)
	revision, entries := syntheticPackRevision()
	if err := st.InstallDefinitionPackRevision(ctx, &revision, entries); err != nil {
		t.Fatal(err)
	}
	configured := core.IndexerConfig{
		DefinitionID: "community:first", DefinitionSource: revision.Source,
		DefinitionRevision: revision.Revision, DefinitionDigest: entries[0].Digest,
		Name: "mismatched pin", URL: "https://tracker.example", Type: core.IndexerTypeTorznab,
	}
	if err := st.UpsertIndexer(ctx, &configured); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `
		UPDATE indexers SET definition_id = 'community:second' WHERE id = ?`, configured.ID); err == nil {
		t.Fatal("direct mismatched indexer definition was accepted")
	}
}

func TestDefinitionPackReadsRejectMalformedTimesAndJSONShapes(t *testing.T) {
	revision, entries := syntheticPackRevision()
	model := definitionPackRevisionModelFromCore(revision, revision.AcceptedAt, revision.AcceptedAt)
	model.AcceptedAt = "not-a-time"
	if _, err := model.core(); err == nil {
		t.Fatal("malformed accepted_at was accepted")
	}
	entry, err := definitionPackEntryModelFromCore(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	entry.ApprovedOriginsJSON = "null"
	if _, err := entry.core(); err == nil {
		t.Fatal("non-array approved origins were accepted")
	}
	entry, err = definitionPackEntryModelFromCore(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	entry.ApprovedOriginsJSON = `["https://tracker.example/path"]`
	if _, err := entry.core(); err == nil {
		t.Fatal("non-origin URL in approved origins was accepted")
	}
	entry, err = definitionPackEntryModelFromCore(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	entry.Path = "definitions/first.json"
	if _, err := entry.core(); err == nil {
		t.Fatal("non-yml definition entry path was accepted")
	}
	entry, err = definitionPackEntryModelFromCore(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	entry.Source = "builtin"
	entry.DefinitionRef = "builtin:first"
	if _, err := entry.core(); err == nil {
		t.Fatal("reserved standalone definition entry source was accepted")
	}
}

func TestDefinitionPackGlobalIdentityCollisionsAreConflicts(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	revision, entries := syntheticPackRevision()
	if err := st.InstallDefinitionPackRevision(ctx, &revision, entries); err != nil {
		t.Fatal(err)
	}
	collision := revision
	collision.Source = "other"
	collision.Revision = "other-revision"
	collision.ManifestDigest = "sha256:6666666666666666666666666666666666666666666666666666666666666666"
	collision.ArchiveRelPath = "archives/sha256/77/7777777777777777777777777777777777777777777777777777777777777777.caravan-indexer-pack"
	collision.SignerKeyID = "other-key"
	collision.SignerPublicKey = []byte("abcdefghijklmnopqrstuvwxyz012345")
	collisionFingerprint := sha256.Sum256(collision.SignerPublicKey)
	collision.SignerKeyFingerprint = "sha256:" + hex.EncodeToString(collisionFingerprint[:])
	collisionEntries := append([]core.DefinitionPackEntry(nil), entries...)
	for i := range collisionEntries {
		collisionEntries[i].Source = collision.Source
		collisionEntries[i].Revision = collision.Revision
		collisionEntries[i].DefinitionRef = collision.Source + ":" + strings.TrimPrefix(entries[i].DefinitionRef, revision.Source+":")
	}
	if err := st.InstallDefinitionPackRevision(ctx, &collision, collisionEntries); !errors.Is(err, ErrConflict) {
		t.Fatalf("archive digest collision error = %v, want ErrConflict", err)
	}
}

func TestDefinitionPackRevisionAndPinSurviveBackup(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()
	revision, entries := syntheticPackRevision()
	if err := st.InstallDefinitionPackRevision(ctx, &revision, entries); err != nil {
		t.Fatal(err)
	}
	configured := core.IndexerConfig{
		DefinitionID: "community:first", DefinitionSource: revision.Source,
		DefinitionRevision: revision.Revision, DefinitionDigest: entries[0].Digest,
		Name: "backup pin", URL: "https://tracker.example", Type: core.IndexerTypeTorznab,
	}
	if err := st.UpsertIndexer(ctx, &configured); err != nil {
		t.Fatal(err)
	}
	snapshotPath := filepath.Join(t.TempDir(), "pack-backup.sqlite")
	if err := os.WriteFile(snapshotPath, storeBackup(t, st), 0o600); err != nil {
		t.Fatal(err)
	}
	snapshot, err := Open(snapshotPath)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	if _, err := snapshot.GetDefinitionPackRevision(ctx, revision.Source, revision.Revision); err != nil {
		t.Fatal(err)
	}
	loaded, err := snapshot.GetIndexer(ctx, configured.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.DefinitionSource != revision.Source || loaded.DefinitionRevision != revision.Revision || loaded.DefinitionDigest != entries[0].Digest {
		t.Fatalf("backup lost exact definition pin: %+v", loaded)
	}
}

func TestPopulatedVersionOneMigratesThroughDefinitionPacksWithoutDataLoss(t *testing.T) {
	path := filepath.Join(t.TempDir(), "populated-v1.sqlite")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	provider, err := migrationProvider(db, storemigrations.FS())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(context.Background(), 1); err != nil {
		t.Fatalf("apply v1: %v", err)
	}
	if _, err := db.Exec(`
		INSERT INTO indexers (id, name, protocol, url, api_key, categories, priority, enabled, created_at, updated_at)
		VALUES (41, 'preserved-indexer', 'torznab', 'https://feed.example', 'secret', '[2000]', 7, 1, '2026-01-01T00:00:00Z', '2026-01-01T00:00:00Z');
		INSERT INTO releases (id, indexer_id, indexer_name, guid, title, download_url, seen_at)
		VALUES (42, 41, 'preserved-indexer', 'guid-42', 'preserved-release', 'magnet:?xt=urn:btih:abc', '2026-01-01T00:00:00Z');
	`); err != nil {
		t.Fatalf("seed v1: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open populated v1: %v", err)
	}
	defer st.Close()
	indexer, err := st.GetIndexer(context.Background(), 41)
	if err != nil {
		t.Fatal(err)
	}
	if indexer.Name != "preserved-indexer" || indexer.APIKey != "secret" || indexer.DefinitionID != "" || len(indexer.Settings) != 0 {
		t.Fatalf("migrated indexer = %+v", indexer)
	}
	var title, attributes string
	if err := st.DB().QueryRow("SELECT title, attributes FROM releases WHERE id = 42").Scan(&title, &attributes); err != nil {
		t.Fatal(err)
	}
	if title != "preserved-release" || attributes != "" {
		t.Fatalf("migrated release title=%q attributes=%q", title, attributes)
	}
}

func syntheticPackRevision() (core.DefinitionPackRevision, []core.DefinitionPackEntry) {
	accepted := time.Date(2026, 8, 14, 22, 0, 0, 0, time.UTC)
	publicKey := []byte("0123456789abcdefghijklmnopqrstuv")
	fingerprint := sha256.Sum256(publicKey)
	revision := core.DefinitionPackRevision{
		Source: "community", Revision: "2026.08.14",
		ManifestDigest:    "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		ArchiveDigest:     "sha256:1111111111111111111111111111111111111111111111111111111111111111",
		ArchiveRelPath:    "archives/sha256/11/1111111111111111111111111111111111111111111111111111111111111111.caravan-indexer-pack",
		LicenseExpression: "MIT", LicensePath: "LICENSE",
		LicenseDigest: "sha256:2222222222222222222222222222222222222222222222222222222222222222",
		Provenance:    "synthetic test pack", SignerKeyID: "test-key",
		SignerKeyFingerprint:  "sha256:" + hex.EncodeToString(fingerprint[:]),
		SignerPublicKey:       publicKey,
		MinimumCaravanVersion: "0.1.0", InstallState: core.DefinitionPackInstalled,
		DefinitionCount: 2, RunnableCount: 1, AcceptedAt: accepted, AcceptedByUserID: 7,
		InstalledAt: accepted,
	}
	entries := []core.DefinitionPackEntry{
		{Source: revision.Source, Revision: revision.Revision, DefinitionRef: "community:first", MetadataID: "first-site", Path: "definitions/first.yml", Digest: "sha256:4444444444444444444444444444444444444444444444444444444444444444", State: core.DefinitionPackEntryRunnableUnverified, ApprovedOrigins: []string{"https://tracker.example"}},
		{Source: revision.Source, Revision: revision.Revision, DefinitionRef: "community:second", MetadataID: "second-site", Path: "definitions/second.yml", Digest: "sha256:5555555555555555555555555555555555555555555555555555555555555555", State: core.DefinitionPackEntryUnsupported, Unsupported: []string{"login.required"}, ApprovedOrigins: []string{"https://tracker.example"}},
	}
	return revision, entries
}
