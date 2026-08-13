package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"syscall"
	"testing"
	"testing/fstest"
	"time"

	"github.com/watzon/caravan/internal/core"
	_ "modernc.org/sqlite"
)

// openTemp opens a store in a fresh temp directory and closes it on cleanup.
func openTemp(t *testing.T) (*Store, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "caravan.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	t.Cleanup(func() { st.Close() })
	return st, path
}

func TestOpenAppliesGooseBaselineMigration(t *testing.T) {
	st, _ := openTemp(t)

	version, err := st.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", version)
	}

	// The clean pre-release baseline contains the entire current model. Future
	// schema changes are new Goose migrations; none of the development-era schema
	// steps are part of the public upgrade history.
	want := []string{
		"settings", "movies", "series", "seasons", "episodes", "episode_files",
		"quality_profiles", "indexers", "releases", "grabs", "downloads",
		"download_clients", "media_files", "events", "jobs", "unmatched_files",
		"conversions", "storage_migrations", "usenet_servers", "requests", "users",
		"libraries", "library_indexers", "remote_path_mappings", "notification_webhooks",
		"stashbox_instances", "library_access", "sessions", "caravan_schema_migrations",
	}
	for _, table := range want {
		var name string
		err := st.DB().QueryRow(
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %q missing after migration: %v", table, err)
		}
	}

	var legacyMetadata int
	if err := st.DB().QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'schema_version'",
	).Scan(&legacyMetadata); err != nil {
		t.Fatalf("check legacy migration metadata: %v", err)
	}
	if legacyMetadata != 0 {
		t.Errorf("legacy schema_version tables = %d, want 0", legacyMetadata)
	}
}

func TestOpenRetriesAfterGooseMetadataBootstrap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caravan.db")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	provider, err := migrationProvider(db, fstest.MapFS{
		"0001_placeholder.sql": {Data: []byte("-- +goose Up\nSELECT 1;")},
	})
	if err != nil {
		db.Close()
		t.Fatalf("migrationProvider: %v", err)
	}
	if _, err := provider.Status(t.Context()); err != nil {
		db.Close()
		t.Fatalf("bootstrap Goose metadata: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close bootstrap database: %v", err)
	}

	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open after interrupted Goose bootstrap: %v", err)
	}
	defer st.Close()
	version, err := st.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if version != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", version)
	}
}

func TestMigrationProviderRejectsMalformedSQLFilename(t *testing.T) {
	db, err := sql.Open("sqlite", dsn(filepath.Join(t.TempDir(), "malformed.sqlite")))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	_, err = migrationProvider(db, fstest.MapFS{
		"feature.sql": {Data: []byte("-- +goose Up\nSELECT 1;")},
	})
	if err == nil {
		t.Fatal("migrationProvider accepted an embedded SQL file without a migration version")
	}
}

func TestStoreAndBunShareDatabaseAndRepresentation(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	if err := st.SetSetting(ctx, "written-by-store", "store-value"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	var fromStore settingModel
	if err := st.ORM().NewSelect().Model(&fromStore).
		Where("key = ?", "written-by-store").Scan(ctx); err != nil {
		t.Fatalf("Bun read after Store write: %v", err)
	}
	if fromStore.Value != "store-value" {
		t.Fatalf("Bun value = %q, want %q", fromStore.Value, "store-value")
	}

	fromBun := settingModel{
		Key: "written-by-bun", Value: "bun-value", UpdatedAt: formatTime(now()),
	}
	if _, err := st.ORM().NewInsert().Model(&fromBun).Exec(ctx); err != nil {
		t.Fatalf("Bun insert: %v", err)
	}
	got, err := st.GetSetting(ctx, fromBun.Key)
	if err != nil {
		t.Fatalf("GetSetting after Bun write: %v", err)
	}
	if got != fromBun.Value {
		t.Fatalf("Store value = %q, want %q", got, fromBun.Value)
	}
}

func TestOpenRejectsLegacyDatabaseWithoutMutatingIt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`
		CREATE TABLE schema_version (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL);
		CREATE TABLE legacy_data (value TEXT NOT NULL);
		INSERT INTO schema_version VALUES (29, 'sessions', '2026-01-01T00:00:00Z');
		INSERT INTO legacy_data VALUES ('keep me');
	`); err != nil {
		db.Close()
		t.Fatalf("seed legacy database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatalf("chmod legacy database: %v", err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read legacy database before Open: %v", err)
	}
	beforeInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat legacy database before Open: %v", err)
	}

	if _, err := Open(path); !errors.Is(err, ErrLegacySchema) {
		t.Fatalf("Open legacy database error = %v, want ErrLegacySchema", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read legacy database after Open: %v", err)
	}
	if !bytes.Equal(after, before) {
		t.Fatal("rejected Open changed legacy database bytes")
	}
	afterInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat legacy database after Open: %v", err)
	}
	if afterInfo.Mode().Perm() != beforeInfo.Mode().Perm() {
		t.Fatalf("rejected Open changed legacy database mode from %04o to %04o",
			beforeInfo.Mode().Perm(), afterInfo.Mode().Perm())
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !os.IsNotExist(err) {
			t.Fatalf("rejected Open created SQLite sidecar %q: %v", path+suffix, err)
		}
	}

	db, err = sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		t.Fatalf("reopen legacy database: %v", err)
	}
	defer db.Close()
	var value string
	if err := db.QueryRow("SELECT value FROM legacy_data").Scan(&value); err != nil {
		t.Fatalf("legacy row after rejected Open: %v", err)
	}
	if value != "keep me" {
		t.Fatalf("legacy row = %q, want unchanged", value)
	}
	var migrationTables int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'caravan_schema_migrations'`,
	).Scan(&migrationTables); err != nil {
		t.Fatalf("count migration tables: %v", err)
	}
	if migrationTables != 0 {
		t.Fatalf("rejected legacy database has %d migration metadata tables, want 0", migrationTables)
	}
}

func TestOpenRejectsUnidentifiedSQLiteDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-caravan.sqlite")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE other_app (value TEXT)"); err != nil {
		db.Close()
		t.Fatalf("create unrelated schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close unrelated database: %v", err)
	}

	if _, err := Open(path); !errors.Is(err, ErrUnrecognizedSchema) {
		t.Fatalf("Open unrelated database error = %v, want ErrUnrecognizedSchema", err)
	}
}

func TestOpenRejectsForgedCaravanIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "forged.sqlite")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	if _, err := db.Exec(`
		PRAGMA application_id = 1129469518;
		CREATE TABLE caravan_schema_migrations (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			version_id INTEGER NOT NULL,
			is_applied INTEGER NOT NULL,
			tstamp TIMESTAMP DEFAULT (datetime('now'))
		);
		INSERT INTO caravan_schema_migrations (version_id, is_applied) VALUES (0, 1), (1, 1);
		CREATE TABLE other_app (value TEXT);
	`); err != nil {
		db.Close()
		t.Fatalf("seed forged database: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close forged database: %v", err)
	}

	if _, err := Open(path); !errors.Is(err, ErrUnrecognizedSchema) {
		t.Fatalf("Open forged database error = %v, want ErrUnrecognizedSchema", err)
	}
}

func TestOpenRejectsCurrentSchemaMissingRequiredSeedContracts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing-seed.sqlite")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open current database: %v", err)
	}
	if _, err := st.DB().Exec("DELETE FROM settings WHERE key = 'default_quality_profile_id'"); err != nil {
		st.Close()
		t.Fatalf("delete required seed contract: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close current database: %v", err)
	}

	if _, err := Open(path); !errors.Is(err, ErrUnrecognizedSchema) {
		t.Fatalf("Open missing required seed error = %v, want ErrUnrecognizedSchema", err)
	}
}

func TestSchemaVersionAcceptsKnownPendingSuffix(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known-prefix.sqlite")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	first := fstest.MapFS{
		"0001_first.sql": {Data: []byte("-- +goose Up\nCREATE TABLE first (id INTEGER PRIMARY KEY);")},
	}
	if err := runMigrations(context.Background(), db, first); err != nil {
		t.Fatalf("apply first migration: %v", err)
	}
	all := fstest.MapFS{
		"0001_first.sql":  {Data: []byte("-- +goose Up\nCREATE TABLE first (id INTEGER PRIMARY KEY);")},
		"0002_second.sql": {Data: []byte("-- +goose Up\nCREATE TABLE second (id INTEGER PRIMARY KEY);")},
	}
	version, err := schemaVersionFor(context.Background(), db, all)
	if err != nil {
		t.Fatalf("schemaVersionFor known prefix: %v", err)
	}
	if version != 1 {
		t.Fatalf("schemaVersionFor known prefix = %d, want 1", version)
	}
}

func TestOpenRejectsUnknownMigrationHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unknown-migration.sqlite")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open current database: %v", err)
	}
	if _, err := st.DB().Exec(`INSERT INTO caravan_schema_migrations (version_id, is_applied) VALUES (2, 1)`); err != nil {
		st.Close()
		t.Fatalf("insert unknown migration marker: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close current database: %v", err)
	}

	if _, err := Open(path); !errors.Is(err, ErrUnrecognizedSchema) {
		t.Fatalf("Open unknown migration error = %v, want ErrUnrecognizedSchema", err)
	}
}

func TestOpenRejectsNoncanonicalGooseHistory(t *testing.T) {
	tests := []struct {
		name   string
		mutate string
	}{
		{name: "duplicate known version", mutate: `INSERT INTO caravan_schema_migrations (version_id, is_applied) VALUES (1, 1)`},
		{name: "duplicate zero version", mutate: `INSERT INTO caravan_schema_migrations (version_id, is_applied) VALUES (0, 1)`},
		{name: "missing zero version", mutate: `DELETE FROM caravan_schema_migrations WHERE version_id = 0`},
		{name: "known version marked rolled back", mutate: `UPDATE caravan_schema_migrations SET is_applied = 0 WHERE version_id = 1`},
		{name: "out of order versions", mutate: `
			DELETE FROM caravan_schema_migrations;
			INSERT INTO caravan_schema_migrations (version_id, is_applied) VALUES (1, 1);
			INSERT INTO caravan_schema_migrations (version_id, is_applied) VALUES (0, 1);`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "noncanonical.sqlite")
			st, err := Open(path)
			if err != nil {
				t.Fatalf("Open current database: %v", err)
			}
			if _, err := st.DB().Exec(tt.mutate); err != nil {
				st.Close()
				t.Fatalf("mutate migration history: %v", err)
			}
			if err := st.Close(); err != nil {
				t.Fatalf("close current database: %v", err)
			}

			if _, err := Open(path); !errors.Is(err, ErrUnrecognizedSchema) {
				t.Fatalf("Open noncanonical history error = %v, want ErrUnrecognizedSchema", err)
			}
		})
	}
}

func TestConcurrentFirstOpenSerializesMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caravan.db")
	const opens = 12

	start := make(chan struct{})
	results := make(chan error, opens)
	stores := make(chan *Store, opens)
	var wg sync.WaitGroup
	for range opens {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			st, err := Open(path)
			if err == nil {
				stores <- st
			}
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	close(stores)
	for st := range stores {
		defer st.Close()
	}
	for err := range results {
		if err != nil {
			t.Errorf("concurrent Open: %v", err)
		}
	}

	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		t.Fatalf("open final database: %v", err)
	}
	defer db.Close()
	version, err := schemaVersion(context.Background(), db)
	if err != nil {
		t.Fatalf("final schema version: %v", err)
	}
	if version != 1 {
		t.Fatalf("final schema version = %d, want 1", version)
	}
}

func TestFailedTransactionalGooseMigrationRollsBackAndIsNotMarkedApplied(t *testing.T) {
	path := filepath.Join(t.TempDir(), "failed.sqlite")
	sqldb, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	t.Cleanup(func() { sqldb.Close() })

	migrations := fstest.MapFS{
		"0002_failure.sql": {Data: []byte(
			"-- +goose Up\n" +
				"CREATE TABLE partial_write (key TEXT PRIMARY KEY);\n" +
				"CREATE TABLE partial_write (key TEXT PRIMARY KEY);",
		)},
	}
	if err := runMigrations(context.Background(), sqldb, migrations); err == nil {
		t.Fatal("runMigrations with failing migration = nil, want error")
	}

	var applied int
	if err := sqldb.QueryRow(`
		SELECT COUNT(*) FROM caravan_schema_migrations
		WHERE version_id = 2 AND is_applied = 1`).Scan(&applied); err != nil {
		t.Fatalf("count failed migration markers: %v", err)
	}
	if applied != 0 {
		t.Fatalf("failed migration markers = %d, want 0", applied)
	}

	var partialTables int
	if err := sqldb.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = 'partial_write'`,
	).Scan(&partialTables); err != nil {
		t.Fatalf("count partial migration tables: %v", err)
	}
	if partialTables != 0 {
		t.Fatalf("partial migration tables = %d, want transaction rollback", partialTables)
	}
}

func TestGooseMarkerFailureRollsBackSchemaAndCanRetry(t *testing.T) {
	path := filepath.Join(t.TempDir(), "marker-failure.sqlite")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	failing := fstest.MapFS{
		"0001_marker_failure.sql": {Data: []byte(
			"-- +goose Up\n" +
				"CREATE TABLE marker_atomicity (id INTEGER PRIMARY KEY);\n" +
				"DROP TABLE caravan_schema_migrations;",
		)},
	}
	if err := runMigrations(context.Background(), db, failing); err == nil {
		t.Fatal("runMigrations with marker failure = nil, want error")
	}
	assertTableCount(t, db, "marker_atomicity", 0)

	succeeding := fstest.MapFS{
		"0001_marker_failure.sql": {Data: []byte(
			"-- +goose Up\n" +
				"CREATE TABLE marker_atomicity (id INTEGER PRIMARY KEY);",
		)},
	}
	if err := runMigrations(context.Background(), db, succeeding); err != nil {
		t.Fatalf("retry migration after marker failure: %v", err)
	}
	assertTableCount(t, db, "marker_atomicity", 1)
}

func TestGooseCommitFailureRollsBackSchemaAndMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "commit-failure.sqlite")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	migrations := fstest.MapFS{
		"0001_commit_failure.sql": {Data: []byte(
			"-- +goose Up\n" +
				"CREATE TABLE deferred_parent (id INTEGER PRIMARY KEY);\n" +
				"CREATE TABLE deferred_child (parent_id INTEGER REFERENCES deferred_parent(id) DEFERRABLE INITIALLY DEFERRED);\n" +
				"INSERT INTO deferred_child (parent_id) VALUES (1);",
		)},
	}
	if err := runMigrations(context.Background(), db, migrations); err == nil {
		t.Fatal("runMigrations with deferred FK commit failure = nil, want error")
	}
	assertTableCount(t, db, "deferred_parent", 0)
	assertTableCount(t, db, "deferred_child", 0)

	var applied int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM caravan_schema_migrations
		WHERE version_id = 1 AND is_applied = 1`).Scan(&applied); err != nil {
		t.Fatalf("count commit-failed migration markers: %v", err)
	}
	if applied != 0 {
		t.Fatalf("commit-failed migration markers = %d, want 0", applied)
	}
}

func assertTableCount(t *testing.T, db *sql.DB, table string, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type = 'table' AND name = ?`, table).Scan(&got); err != nil {
		t.Fatalf("count table %q: %v", table, err)
	}
	if got != want {
		t.Fatalf("table %q count = %d, want %d", table, got, want)
	}
}

func TestOpenEnablesWALAndForeignKeys(t *testing.T) {
	st, _ := openTemp(t)

	var mode string
	if err := st.DB().QueryRow("PRAGMA journal_mode").Scan(&mode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want %q", mode, "wal")
	}

	var fk int
	if err := st.DB().QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("foreign_keys = %d, want 1", fk)
	}
}

func TestNewSQLiteFilesArePrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file modes are not Windows ACL semantics")
	}

	oldUmask := syscall.Umask(0)
	defer syscall.Umask(oldUmask)

	path := filepath.Join(t.TempDir(), "caravan.db")
	st, err := Open(path)
	if err != nil {
		t.Fatalf("Open(%q): %v", path, err)
	}
	defer st.Close()

	if err := st.SetSetting(context.Background(), "permission-test", "value"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	assertSQLiteFilesPrivate(t, path)
}

func TestExistingSQLiteFilesAreHardened(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caravan.db")
	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open(%q): %v", path, err)
	}
	if err := first.SetSetting(context.Background(), "permission-test", "value"); err != nil {
		first.Close()
		t.Fatalf("SetSetting: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}

	artifacts := sqliteFiles(t, path)
	for _, artifact := range artifacts {
		if runtime.GOOS == "windows" {
			continue
		}
		if err := os.Chmod(artifact, 0o644); err != nil {
			t.Fatalf("Chmod(%q): %v", artifact, err)
		}
	}

	second, err := Open(path)
	if err != nil {
		t.Fatalf("reopen %q: %v", path, err)
	}
	defer second.Close()

	got, err := second.GetSetting(context.Background(), "permission-test")
	if err != nil {
		t.Fatalf("GetSetting after reopen: %v", err)
	}
	if got != "value" {
		t.Errorf("setting after reopen = %q, want %q", got, "value")
	}
	assertSQLiteFilesPrivate(t, path)
}

func sqliteFiles(t *testing.T, path string) []string {
	t.Helper()
	files := []string{path}
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar := path + suffix
		if _, err := os.Stat(sidecar); err == nil {
			files = append(files, sidecar)
		} else if !os.IsNotExist(err) {
			t.Fatalf("Stat(%q): %v", sidecar, err)
		}
	}
	return files
}

func assertSQLiteFilesPrivate(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Log("skipping POSIX mode assertions on Windows")
		return
	}
	for _, artifact := range sqliteFiles(t, path) {
		info, err := os.Stat(artifact)
		if err != nil {
			t.Fatalf("Stat(%q): %v", artifact, err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("%s mode = %04o, want 0600", artifact, got)
		}
	}
}

// The disposable-cache pillar (SPEC §1.2) depends on reopening being a no-op:
// a second Open must not re-run the baseline migration and duplicate the seeded
// profile, and data written before the close must still be there.
func TestReopenIsIdempotent(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "caravan.db")

	first, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	if err := first.SetSetting(ctx, SettingStorageRoot, "/data"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	firstVersion, err := first.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer second.Close()

	secondVersion, err := second.SchemaVersion()
	if err != nil {
		t.Fatalf("SchemaVersion after reopen: %v", err)
	}
	if secondVersion != firstVersion {
		t.Errorf("schema version changed on reopen: %d -> %d", firstVersion, secondVersion)
	}

	got, err := second.GetSetting(ctx, SettingStorageRoot)
	if err != nil {
		t.Fatalf("GetSetting after reopen: %v", err)
	}
	if got != "/data" {
		t.Errorf("storage root after reopen = %q, want %q", got, "/data")
	}

	profiles, err := second.ListQualityProfiles(ctx)
	if err != nil {
		t.Fatalf("ListQualityProfiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("got %d quality profiles after reopen, want 1 (migration re-applied?)", len(profiles))
	}
}

func TestDefaultQualityProfileSeeded(t *testing.T) {
	st, _ := openTemp(t)

	profiles, err := st.ListQualityProfiles(context.Background())
	if err != nil {
		t.Fatalf("ListQualityProfiles: %v", err)
	}
	if len(profiles) != 1 {
		t.Fatalf("got %d profiles, want 1", len(profiles))
	}

	p := profiles[0]
	if p.Name != DefaultQualityProfileName {
		t.Errorf("Name = %q, want %q", p.Name, DefaultQualityProfileName)
	}
	if p.Cutoff != core.Quality1080p {
		t.Errorf("Cutoff = %q, want %q", p.Cutoff, core.Quality1080p)
	}
	if !p.UpgradeAllowed {
		t.Error("UpgradeAllowed = false, want true")
	}
	if len(p.Items) != len(core.QualityLadder) {
		t.Fatalf("Items = %v, want the full ladder %v", p.Items, core.QualityLadder)
	}
	for i, q := range core.QualityLadder {
		if p.Items[i] != q {
			t.Errorf("Items[%d] = %q, want %q", i, p.Items[i], q)
		}
	}

	// The seed must be reachable by id, since library items reference it.
	byID, err := st.GetQualityProfile(context.Background(), p.ID)
	if err != nil {
		t.Fatalf("GetQualityProfile(%d): %v", p.ID, err)
	}
	if byID.Name != p.Name {
		t.Errorf("GetQualityProfile name = %q, want %q", byID.Name, p.Name)
	}
}

func TestOpenRejectsUnwritablePath(t *testing.T) {
	// A path whose parent directory does not exist cannot be created; Open
	// must report that rather than returning a Store that fails later.
	path := filepath.Join(t.TempDir(), "no-such-dir", "caravan.db")
	if st, err := Open(path); err == nil {
		st.Close()
		t.Errorf("Open(%q) = nil error, want error", path)
	}
}

func TestCheckpoint(t *testing.T) {
	st, _ := openTemp(t)
	if err := st.SetSetting(context.Background(), "k", "v"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := st.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}
}

func TestSettingsCRUD(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	if _, err := st.GetSetting(ctx, "absent"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSetting(absent) = %v, want ErrNotFound", err)
	}

	baseline := map[string]string{SettingDefaultQualityProfileID: "1"}
	all, err := st.AllSettings(ctx)
	if err != nil {
		t.Fatalf("AllSettings: %v", err)
	}
	if !reflect.DeepEqual(all, baseline) {
		t.Errorf("AllSettings on a fresh db = %v, want seeded baseline %v", all, baseline)
	}

	if err := st.SetSetting(ctx, "naming_template", "{title} ({year})"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	// Overwriting must replace, not duplicate or fail.
	if err := st.SetSetting(ctx, "naming_template", "{title}"); err != nil {
		t.Fatalf("SetSetting overwrite: %v", err)
	}

	got, err := st.GetSetting(ctx, "naming_template")
	if err != nil {
		t.Fatalf("GetSetting: %v", err)
	}
	if got != "{title}" {
		t.Errorf("GetSetting = %q, want %q", got, "{title}")
	}

	all, err = st.AllSettings(ctx)
	if err != nil {
		t.Fatalf("AllSettings: %v", err)
	}
	want := map[string]string{
		SettingDefaultQualityProfileID: "1",
		"naming_template":              "{title}",
	}
	if !reflect.DeepEqual(all, want) {
		t.Errorf("AllSettings = %v, want %v", all, want)
	}

	if err := st.DeleteSetting(ctx, "naming_template"); err != nil {
		t.Fatalf("DeleteSetting: %v", err)
	}
	if _, err := st.GetSetting(ctx, "naming_template"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSetting after delete = %v, want ErrNotFound", err)
	}
	if err := st.DeleteSetting(ctx, "naming_template"); err != nil {
		t.Errorf("DeleteSetting on an absent key = %v, want nil", err)
	}

	all, err = st.AllSettings(ctx)
	if err != nil {
		t.Fatalf("AllSettings after delete: %v", err)
	}
	if !reflect.DeepEqual(all, baseline) {
		t.Errorf("AllSettings after delete = %v, want seeded baseline %v", all, baseline)
	}
}

func TestMovieCRUD(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	movies, err := st.ListMovies(ctx)
	if err != nil {
		t.Fatalf("ListMovies: %v", err)
	}
	if len(movies) != 0 {
		t.Fatalf("ListMovies on a fresh db = %v, want empty", movies)
	}

	released := time.Date(2008, 5, 20, 0, 0, 0, 0, time.UTC)
	m := core.Movie{
		TMDBID:           10378,
		IMDBID:           "tt1254207",
		Title:            "Big Buck Bunny",
		SortTitle:        "big buck bunny",
		Year:             2008,
		Overview:         "A giant rabbit.",
		Path:             "Movies/Big Buck Bunny (2008)",
		PosterPath:       "Movies/Big Buck Bunny (2008)/poster.jpg",
		Monitored:        true,
		QualityProfileID: 1,
		ReleaseDate:      released,
	}
	if err := st.UpsertMovie(ctx, &m); err != nil {
		t.Fatalf("UpsertMovie insert: %v", err)
	}
	if m.ID == 0 {
		t.Fatal("UpsertMovie did not assign an ID")
	}
	if m.AddedAt.IsZero() || m.UpdatedAt.IsZero() {
		t.Error("UpsertMovie did not stamp AddedAt/UpdatedAt")
	}

	got, err := st.GetMovie(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if got.Title != m.Title || got.Path != m.Path || got.TMDBID != m.TMDBID {
		t.Errorf("GetMovie = %+v, want title/path/tmdb of %+v", got, m)
	}
	if !got.ReleaseDate.Equal(released) {
		t.Errorf("ReleaseDate = %v, want %v", got.ReleaseDate, released)
	}
	if !got.Monitored {
		t.Error("Monitored = false, want true")
	}

	// A second upsert with the same TMDB id must update in place, not insert.
	update := core.Movie{TMDBID: 10378, Title: "Big Buck Bunny", Year: 2008, Monitored: false}
	if err := st.UpsertMovie(ctx, &update); err != nil {
		t.Fatalf("UpsertMovie update: %v", err)
	}
	if update.ID != m.ID {
		t.Errorf("upsert by tmdb_id produced id %d, want the existing %d", update.ID, m.ID)
	}
	if !update.AddedAt.Equal(got.AddedAt) {
		t.Errorf("AddedAt = %v, want the original %v preserved", update.AddedAt, got.AddedAt)
	}

	movies, err = st.ListMovies(ctx)
	if err != nil {
		t.Fatalf("ListMovies: %v", err)
	}
	if len(movies) != 1 {
		t.Fatalf("got %d movies after re-upsert, want 1", len(movies))
	}
	if movies[0].Monitored {
		t.Error("Monitored = true after update to false")
	}

	byTMDB, err := st.GetMovieByTMDBID(ctx, 10378)
	if err != nil {
		t.Fatalf("GetMovieByTMDBID: %v", err)
	}
	if byTMDB.ID != m.ID {
		t.Errorf("GetMovieByTMDBID id = %d, want %d", byTMDB.ID, m.ID)
	}

	if _, err := st.GetMovie(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetMovie(9999) = %v, want ErrNotFound", err)
	}
	if _, err := st.GetMovieByTMDBID(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetMovieByTMDBID(9999) = %v, want ErrNotFound", err)
	}

	if err := st.DeleteMovie(ctx, m.ID); err != nil {
		t.Fatalf("DeleteMovie: %v", err)
	}
	if _, err := st.GetMovie(ctx, m.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetMovie after delete = %v, want ErrNotFound", err)
	}
}

// Unmatched movies have no TMDB id; the partial unique index must let many of
// them coexist rather than collapsing them onto a single tmdb_id = 0 row.
func TestUpsertMovieWithoutTMDBIDAlwaysInserts(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	first := core.Movie{Title: "Unknown A"}
	second := core.Movie{Title: "Unknown B"}
	if err := st.UpsertMovie(ctx, &first); err != nil {
		t.Fatalf("UpsertMovie first: %v", err)
	}
	if err := st.UpsertMovie(ctx, &second); err != nil {
		t.Fatalf("UpsertMovie second: %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("both unmatched movies got id %d, want distinct ids", first.ID)
	}

	movies, err := st.ListMovies(ctx)
	if err != nil {
		t.Fatalf("ListMovies: %v", err)
	}
	if len(movies) != 2 {
		t.Errorf("got %d movies, want 2", len(movies))
	}
}

func TestUpsertMovieWithUnknownIDFails(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	m := core.Movie{ID: 4242, Title: "Ghost"}
	if err := st.UpsertMovie(ctx, &m); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpsertMovie with an unknown id = %v, want ErrNotFound", err)
	}
}

func TestSeriesSeasonEpisodeCRUD(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	aired := time.Date(2016, 11, 6, 0, 0, 0, 0, time.UTC)
	sr := core.Series{
		TMDBID:     62226,
		Title:      "Planet Earth II",
		SortTitle:  "planet earth ii",
		Year:       2016,
		Status:     "Ended",
		Path:       "TV/Planet Earth II (2016)",
		Monitored:  true,
		FirstAired: aired,
	}
	if err := st.UpsertSeries(ctx, &sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if sr.ID == 0 {
		t.Fatal("UpsertSeries did not assign an ID")
	}

	gotSeries, err := st.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if gotSeries.Title != sr.Title || !gotSeries.FirstAired.Equal(aired) {
		t.Errorf("GetSeries = %+v, want title %q and first aired %v", gotSeries, sr.Title, aired)
	}

	byTMDB, err := st.GetSeriesByTMDBID(ctx, 62226)
	if err != nil {
		t.Fatalf("GetSeriesByTMDBID: %v", err)
	}
	if byTMDB.ID != sr.ID {
		t.Errorf("GetSeriesByTMDBID id = %d, want %d", byTMDB.ID, sr.ID)
	}

	// Specials (season 0) and a regular season.
	for _, number := range []int{0, 1} {
		se := core.Season{SeriesID: sr.ID, Number: number, Monitored: true}
		if err := st.UpsertSeason(ctx, &se); err != nil {
			t.Fatalf("UpsertSeason %d: %v", number, err)
		}
		if se.ID == 0 {
			t.Fatalf("UpsertSeason %d did not assign an ID", number)
		}
	}

	// Re-upserting a season must update in place.
	again := core.Season{SeriesID: sr.ID, Number: 1, Title: "Season 1", Monitored: false}
	if err := st.UpsertSeason(ctx, &again); err != nil {
		t.Fatalf("UpsertSeason re-upsert: %v", err)
	}
	seasons, err := st.ListSeasons(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListSeasons: %v", err)
	}
	if len(seasons) != 2 {
		t.Fatalf("got %d seasons, want 2", len(seasons))
	}
	if seasons[0].Number != 0 {
		t.Errorf("seasons[0].Number = %d, want the specials season first", seasons[0].Number)
	}
	if seasons[1].Title != "Season 1" || seasons[1].Monitored {
		t.Errorf("seasons[1] = %+v, want the updated title and monitored false", seasons[1])
	}

	gotSeason, err := st.GetSeason(ctx, again.ID)
	if err != nil {
		t.Fatalf("GetSeason: %v", err)
	}
	if gotSeason.Number != 1 {
		t.Errorf("GetSeason number = %d, want 1", gotSeason.Number)
	}

	ep := core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Islands", Monitored: true}
	if err := st.UpsertEpisode(ctx, &ep); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	if ep.ID == 0 {
		t.Fatal("UpsertEpisode did not assign an ID")
	}
	renamed := core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Islands (HD)"}
	if err := st.UpsertEpisode(ctx, &renamed); err != nil {
		t.Fatalf("UpsertEpisode re-upsert: %v", err)
	}
	if renamed.ID != ep.ID {
		t.Errorf("re-upsert produced id %d, want %d", renamed.ID, ep.ID)
	}

	episodes, err := st.ListEpisodes(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(episodes) != 1 || episodes[0].Title != "Islands (HD)" {
		t.Errorf("ListEpisodes = %+v, want one episode titled %q", episodes, "Islands (HD)")
	}

	byNumber, err := st.GetEpisodeByNumber(ctx, sr.ID, 1, 1)
	if err != nil {
		t.Fatalf("GetEpisodeByNumber: %v", err)
	}
	if byNumber.ID != ep.ID {
		t.Errorf("GetEpisodeByNumber id = %d, want %d", byNumber.ID, ep.ID)
	}
	if _, err := st.GetEpisodeByNumber(ctx, sr.ID, 9, 9); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetEpisodeByNumber(S09E09) = %v, want ErrNotFound", err)
	}

	if err := st.DeleteEpisode(ctx, ep.ID); err != nil {
		t.Fatalf("DeleteEpisode: %v", err)
	}
	if _, err := st.GetEpisode(ctx, ep.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetEpisode after delete = %v, want ErrNotFound", err)
	}

	if err := st.DeleteSeason(ctx, again.ID); err != nil {
		t.Fatalf("DeleteSeason: %v", err)
	}
	seasons, err = st.ListSeasons(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListSeasons after delete: %v", err)
	}
	if len(seasons) != 1 {
		t.Errorf("got %d seasons after delete, want 1", len(seasons))
	}
}

// Deleting a series must take its seasons and episodes with it; that cascade
// is what makes "remove from library" a single operation.
func TestDeleteSeriesCascades(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	sr := core.Series{TMDBID: 1, Title: "Cascade"}
	if err := st.UpsertSeries(ctx, &sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	se := core.Season{SeriesID: sr.ID, Number: 1}
	if err := st.UpsertSeason(ctx, &se); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	ep := core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: 1}
	if err := st.UpsertEpisode(ctx, &ep); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}

	if err := st.DeleteSeries(ctx, sr.ID); err != nil {
		t.Fatalf("DeleteSeries: %v", err)
	}

	if _, err := st.GetSeries(ctx, sr.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetSeries after delete = %v, want ErrNotFound", err)
	}
	seasons, err := st.ListSeasons(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListSeasons: %v", err)
	}
	if len(seasons) != 0 {
		t.Errorf("got %d orphaned seasons, want 0", len(seasons))
	}
	episodes, err := st.ListEpisodes(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(episodes) != 0 {
		t.Errorf("got %d orphaned episodes, want 0", len(episodes))
	}
}

func TestMediaFileCRUD(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	m := core.Movie{TMDBID: 10378, Title: "Big Buck Bunny"}
	if err := st.UpsertMovie(ctx, &m); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	const path = "Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008).mp4"
	f := core.MediaFile{
		Path:         path,
		Size:         158008374,
		MovieID:      m.ID,
		Quality:      core.Quality1080p,
		Source:       core.SourceBluray,
		Codec:        "x264",
		Audio:        "AAC",
		ReleaseGroup: "CARAVAN",
	}
	if err := st.UpsertMediaFile(ctx, &f); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	if f.ID == 0 {
		t.Fatal("UpsertMediaFile did not assign an ID")
	}

	got, err := st.GetMediaFileByPath(ctx, path)
	if err != nil {
		t.Fatalf("GetMediaFileByPath: %v", err)
	}
	if got.Size != f.Size || got.Quality != core.Quality1080p || got.MovieID != m.ID {
		t.Errorf("GetMediaFileByPath = %+v, want size/quality/movie of %+v", got, f)
	}

	// Rescanning the same path must update the row, not add a second one.
	rescanned := core.MediaFile{Path: path, Size: 999, MovieID: m.ID, Quality: core.Quality720p}
	if err := st.UpsertMediaFile(ctx, &rescanned); err != nil {
		t.Fatalf("UpsertMediaFile rescan: %v", err)
	}
	if rescanned.ID != f.ID {
		t.Errorf("rescan produced id %d, want %d", rescanned.ID, f.ID)
	}

	all, err := st.ListMediaFiles(ctx)
	if err != nil {
		t.Fatalf("ListMediaFiles: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d media files, want 1", len(all))
	}
	if all[0].Size != 999 || all[0].Quality != core.Quality720p {
		t.Errorf("media file after rescan = %+v, want size 999 and 720p", all[0])
	}

	forMovie, err := st.ListMediaFilesForMovie(ctx, m.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForMovie: %v", err)
	}
	if len(forMovie) != 1 || forMovie[0].ID != f.ID {
		t.Errorf("ListMediaFilesForMovie = %+v, want the one file", forMovie)
	}

	if _, err := st.GetMediaFileByPath(ctx, "nope.mkv"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetMediaFileByPath(nope.mkv) = %v, want ErrNotFound", err)
	}

	if err := st.DeleteMediaFileByPath(ctx, path); err != nil {
		t.Fatalf("DeleteMediaFileByPath: %v", err)
	}
	if _, err := st.GetMediaFileByPath(ctx, path); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetMediaFileByPath after delete = %v, want ErrNotFound", err)
	}
	if err := st.DeleteMediaFileByPath(ctx, path); err != nil {
		t.Errorf("DeleteMediaFileByPath on an absent path = %v, want nil", err)
	}
}

// SPEC §7: one file can cover S01E01E02, so the episode link is many-to-many
// and deleting the file must clear both links.
func TestMultiEpisodeFileLinking(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	sr := core.Series{TMDBID: 1, Title: "Two Parter"}
	if err := st.UpsertSeries(ctx, &sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	e1 := core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: 1}
	e2 := core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: 2}
	for _, e := range []*core.Episode{&e1, &e2} {
		if err := st.UpsertEpisode(ctx, e); err != nil {
			t.Fatalf("UpsertEpisode: %v", err)
		}
	}

	const path = "TV/Two Parter/Season 01/Two Parter - S01E01-E02.mkv"
	f := core.MediaFile{Path: path, Quality: core.Quality1080p}
	if err := st.UpsertMediaFile(ctx, &f); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}

	for _, id := range []int64{e1.ID, e2.ID} {
		if err := st.LinkEpisodeFile(ctx, id, f.ID); err != nil {
			t.Fatalf("LinkEpisodeFile(%d): %v", id, err)
		}
		// Re-linking must be a no-op so re-imports stay idempotent.
		if err := st.LinkEpisodeFile(ctx, id, f.ID); err != nil {
			t.Fatalf("LinkEpisodeFile(%d) second call: %v", id, err)
		}
	}

	for _, id := range []int64{e1.ID, e2.ID} {
		files, err := st.ListMediaFilesForEpisode(ctx, id)
		if err != nil {
			t.Fatalf("ListMediaFilesForEpisode(%d): %v", id, err)
		}
		if len(files) != 1 || files[0].Path != path {
			t.Errorf("episode %d files = %+v, want the one shared file", id, files)
		}
	}

	if err := st.DeleteMediaFileByPath(ctx, path); err != nil {
		t.Fatalf("DeleteMediaFileByPath: %v", err)
	}
	for _, id := range []int64{e1.ID, e2.ID} {
		files, err := st.ListMediaFilesForEpisode(ctx, id)
		if err != nil {
			t.Fatalf("ListMediaFilesForEpisode(%d) after delete: %v", id, err)
		}
		if len(files) != 0 {
			t.Errorf("episode %d still linked to %+v after the file was deleted", id, files)
		}
	}
}

func TestLinkEpisodeFileRejectsUnknownEpisode(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	f := core.MediaFile{Path: "TV/x.mkv"}
	if err := st.UpsertMediaFile(ctx, &f); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	if err := st.LinkEpisodeFile(ctx, 12345, f.ID); err == nil {
		t.Error("LinkEpisodeFile with an unknown episode = nil error, want a foreign-key error")
	}
}

func TestEventsInsertAndList(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	events, err := st.ListEvents(ctx, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("ListEvents on a fresh db = %v, want empty", events)
	}

	for _, msg := range []string{"first", "second", "third"} {
		e := core.Event{Category: "scan", Message: msg}
		if err := st.InsertEvent(ctx, &e); err != nil {
			t.Fatalf("InsertEvent(%q): %v", msg, err)
		}
		if e.ID == 0 {
			t.Fatalf("InsertEvent(%q) did not assign an ID", msg)
		}
		if e.Level != core.EventLevelInfo {
			t.Errorf("Level = %q, want the %q default", e.Level, core.EventLevelInfo)
		}
		if e.CreatedAt.IsZero() {
			t.Error("InsertEvent did not stamp CreatedAt")
		}
	}

	events, err = st.ListEvents(ctx, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}
	// Newest first, even though all three land in the same clock tick.
	want := []string{"third", "second", "first"}
	for i, msg := range want {
		if events[i].Message != msg {
			t.Errorf("events[%d].Message = %q, want %q", i, events[i].Message, msg)
		}
	}

	limited, err := st.ListEvents(ctx, 2)
	if err != nil {
		t.Fatalf("ListEvents(2): %v", err)
	}
	if len(limited) != 2 || limited[0].Message != "third" {
		t.Errorf("ListEvents(2) = %+v, want the two newest", limited)
	}

	warn := core.Event{Level: core.EventLevelError, Category: "import", Message: "boom", Detail: "stack"}
	if err := st.InsertEvent(ctx, &warn); err != nil {
		t.Fatalf("InsertEvent(error): %v", err)
	}
	newest, err := st.ListEvents(ctx, 1)
	if err != nil {
		t.Fatalf("ListEvents(1): %v", err)
	}
	if newest[0].Level != core.EventLevelError || newest[0].Detail != "stack" {
		t.Errorf("newest event = %+v, want the error event with its detail", newest[0])
	}
}

func TestUnmatchedFileCRUD(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	list, err := st.ListUnmatchedFiles(ctx)
	if err != nil {
		t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("ListUnmatchedFiles on a fresh db = %v, want empty", list)
	}

	const path = "incoming/Some.Movie.2019.1080p.BluRay.x264-GRP.mkv"
	u := core.UnmatchedFile{
		Path:   path,
		Size:   4096,
		Reason: "no metadata match",
		Parsed: core.ParsedRelease{
			Title:      "Some Movie",
			Year:       2019,
			Quality:    core.Quality1080p,
			Source:     core.SourceBluray,
			Codec:      "x264",
			Group:      "GRP",
			Confidence: 0.82,
		},
	}
	if err := st.UpsertUnmatchedFile(ctx, &u); err != nil {
		t.Fatalf("UpsertUnmatchedFile: %v", err)
	}
	if u.ID == 0 {
		t.Fatal("UpsertUnmatchedFile did not assign an ID")
	}

	got, err := st.GetUnmatchedFile(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUnmatchedFile: %v", err)
	}
	if got.Path != path || got.Reason != u.Reason || got.Size != u.Size {
		t.Errorf("GetUnmatchedFile = %+v, want path/reason/size of %+v", got, u)
	}
	// The parsed guess must survive the JSON round trip: it is what the
	// scan-review screen shows the user.
	if !reflect.DeepEqual(got.Parsed, u.Parsed) {
		t.Errorf("Parsed = %+v, want %+v", got.Parsed, u.Parsed)
	}

	// Rescanning the same path refreshes rather than duplicating.
	again := core.UnmatchedFile{Path: path, Size: 8192, Reason: "low parser confidence"}
	if err := st.UpsertUnmatchedFile(ctx, &again); err != nil {
		t.Fatalf("UpsertUnmatchedFile rescan: %v", err)
	}
	if again.ID != u.ID {
		t.Errorf("rescan produced id %d, want %d", again.ID, u.ID)
	}
	list, err = st.ListUnmatchedFiles(ctx)
	if err != nil {
		t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("got %d unmatched files after rescan, want 1", len(list))
	}
	if list[0].Reason != "low parser confidence" || list[0].Size != 8192 {
		t.Errorf("unmatched file after rescan = %+v, want the refreshed reason and size", list[0])
	}

	if _, err := st.GetUnmatchedFile(ctx, 9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetUnmatchedFile(9999) = %v, want ErrNotFound", err)
	}

	if err := st.DeleteUnmatchedFileByPath(ctx, path); err != nil {
		t.Fatalf("DeleteUnmatchedFileByPath: %v", err)
	}
	list, err = st.ListUnmatchedFiles(ctx)
	if err != nil {
		t.Fatalf("ListUnmatchedFiles after delete: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("got %d unmatched files after delete, want 0", len(list))
	}
}

// An unmatched row retains its original insertion position when a rescan
// refreshes seen_at: scan activity is not queue arrival time.
func TestListUnmatchedFilesOrdersByOriginalInsertion(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)
	seenAt := time.Date(2025, time.January, 1, 12, 0, 0, 0, time.UTC)

	older := &core.UnmatchedFile{Path: "incoming/a-older.mkv", SeenAt: seenAt}
	newer := &core.UnmatchedFile{Path: "incoming/z-newer.mkv", SeenAt: seenAt.Add(time.Minute)}
	for _, file := range []*core.UnmatchedFile{older, newer} {
		if err := st.UpsertUnmatchedFile(ctx, file); err != nil {
			t.Fatalf("UpsertUnmatchedFile(%q): %v", file.Path, err)
		}
	}

	refreshedAt := seenAt.Add(2 * time.Hour)
	refreshedOlder := &core.UnmatchedFile{
		Path: older.Path, SeenAt: refreshedAt,
	}
	if err := st.UpsertUnmatchedFile(ctx, refreshedOlder); err != nil {
		t.Fatalf("UpsertUnmatchedFile refresh: %v", err)
	}
	if refreshedOlder.ID != older.ID {
		t.Fatalf("refreshed file id = %d, want original id %d", refreshedOlder.ID, older.ID)
	}

	got, err := st.ListUnmatchedFiles(ctx)
	if err != nil {
		t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	want := []struct {
		id   int64
		path string
	}{
		{id: newer.ID, path: newer.Path},
		{id: older.ID, path: older.Path},
	}
	if len(got) != len(want) {
		t.Fatalf("ListUnmatchedFiles returned %d files, want %d: %+v", len(got), len(want), got)
	}
	for i, file := range got {
		if file.ID != want[i].id || file.Path != want[i].path {
			t.Fatalf("file %d = id %d path %q, want id %d path %q",
				i, file.ID, file.Path, want[i].id, want[i].path)
		}
	}
	if !got[1].SeenAt.Equal(refreshedAt) {
		t.Fatalf("refreshed older file seen_at = %s, want %s", got[1].SeenAt, refreshedAt)
	}
}

// Multi-episode parses go through the same JSON column; a slice field is the
// one shape a naive round trip can lose.
func TestUnmatchedFileParsedEpisodesRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	u := core.UnmatchedFile{
		Path:   "incoming/Show.S01E01E02.mkv",
		Parsed: core.ParsedRelease{Title: "Show", Season: 1, Episodes: []int{1, 2}},
	}
	if err := st.UpsertUnmatchedFile(ctx, &u); err != nil {
		t.Fatalf("UpsertUnmatchedFile: %v", err)
	}

	got, err := st.GetUnmatchedFile(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUnmatchedFile: %v", err)
	}
	if got.Parsed.Season != 1 {
		t.Errorf("Season = %d, want 1", got.Parsed.Season)
	}
	if len(got.Parsed.Episodes) != 2 || got.Parsed.Episodes[0] != 1 || got.Parsed.Episodes[1] != 2 {
		t.Errorf("Episodes = %v, want [1 2]", got.Parsed.Episodes)
	}
}
