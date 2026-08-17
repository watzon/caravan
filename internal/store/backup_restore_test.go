package store

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	storemigrations "github.com/watzon/caravan/internal/store/migrations"
)

func storeBackup(t *testing.T, st *Store) []byte {
	t.Helper()
	var backup bytes.Buffer
	if err := st.Backup(context.Background(), &backup); err != nil {
		t.Fatalf("Backup: %v", err)
	}
	return backup.Bytes()
}

func TestBackupIncludesCommittedData(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)
	if err := st.SetSetting(ctx, "backup-test", "committed"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	snapshotPath := filepath.Join(t.TempDir(), "backup.sqlite")
	if err := os.WriteFile(snapshotPath, storeBackup(t, st), 0o600); err != nil {
		t.Fatalf("WriteFile snapshot: %v", err)
	}
	snapshot, err := Open(snapshotPath)
	if err != nil {
		t.Fatalf("Open snapshot: %v", err)
	}
	defer snapshot.Close()

	got, err := snapshot.GetSetting(ctx, "backup-test")
	if err != nil {
		t.Fatalf("GetSetting from snapshot: %v", err)
	}
	if got != "committed" {
		t.Fatalf("snapshot setting = %q, want %q", got, "committed")
	}
}

func TestStageRestoreRejectsCorruptDatabaseWithoutChangingCurrentData(t *testing.T) {
	ctx := context.Background()
	st, path := openTemp(t)
	if err := st.SetSetting(ctx, "restore-test", "current"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	err := st.StageRestore(ctx, bytes.NewReader([]byte("not a sqlite database")), 1024)
	if !errors.Is(err, ErrInvalidRestore) {
		t.Fatalf("StageRestore corrupt database error = %v, want ErrInvalidRestore", err)
	}
	if _, err := os.Stat(stagedRestorePath(path)); !os.IsNotExist(err) {
		t.Fatalf("corrupt restore left pending file: %v", err)
	}
	got, err := st.GetSetting(ctx, "restore-test")
	if err != nil {
		t.Fatalf("GetSetting after rejected restore: %v", err)
	}
	if got != "current" {
		t.Fatalf("setting after rejected restore = %q, want %q", got, "current")
	}
}

func TestStageRestoreRejectsNewerSchemaWithoutChangingCurrentData(t *testing.T) {
	ctx := context.Background()
	st, path := openTemp(t)
	if err := st.SetSetting(ctx, "restore-test", "current"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	future, err := Open(filepath.Join(t.TempDir(), "future.sqlite"))
	if err != nil {
		t.Fatalf("Open future database: %v", err)
	}
	if _, err := future.DB().Exec(
		"INSERT INTO caravan_schema_migrations (version_id, is_applied) VALUES (?, ?)",
		99999999999999, true,
	); err != nil {
		future.Close()
		t.Fatalf("insert future schema version: %v", err)
	}
	backup := storeBackup(t, future)
	if err := future.Close(); err != nil {
		t.Fatalf("Close future database: %v", err)
	}

	err = st.StageRestore(ctx, bytes.NewReader(backup), int64(len(backup)))
	if !errors.Is(err, ErrInvalidRestore) {
		t.Fatalf("StageRestore newer schema error = %v, want ErrInvalidRestore", err)
	}
	if _, err := os.Stat(stagedRestorePath(path)); !os.IsNotExist(err) {
		t.Fatalf("newer restore left pending file: %v", err)
	}
	got, err := st.GetSetting(ctx, "restore-test")
	if err != nil {
		t.Fatalf("GetSetting after rejected newer restore: %v", err)
	}
	if got != "current" {
		t.Fatalf("setting after rejected newer restore = %q, want %q", got, "current")
	}
}

func TestStageRestoreRejectsNoncanonicalMigrationHistoryWithoutChangingCurrentData(t *testing.T) {
	ctx := context.Background()
	target, targetPath := openTemp(t)
	if err := target.SetSetting(ctx, "restore-test", "current"); err != nil {
		t.Fatalf("SetSetting current: %v", err)
	}

	source, err := Open(filepath.Join(t.TempDir(), "rolled-back-history.sqlite"))
	if err != nil {
		t.Fatalf("Open source database: %v", err)
	}
	if _, err := source.DB().Exec(
		"UPDATE caravan_schema_migrations SET is_applied = 0 WHERE version_id = 1",
	); err != nil {
		source.Close()
		t.Fatalf("mark source migration rolled back: %v", err)
	}
	backup := storeBackup(t, source)
	if err := source.Close(); err != nil {
		t.Fatalf("Close source database: %v", err)
	}

	err = target.StageRestore(ctx, bytes.NewReader(backup), int64(len(backup)))
	if !errors.Is(err, ErrInvalidRestore) {
		t.Fatalf("StageRestore noncanonical history error = %v, want ErrInvalidRestore", err)
	}
	if _, err := os.Stat(stagedRestorePath(targetPath)); !os.IsNotExist(err) {
		t.Fatalf("noncanonical restore left pending file: %v", err)
	}
	got, err := target.GetSetting(ctx, "restore-test")
	if err != nil {
		t.Fatalf("GetSetting after rejected restore: %v", err)
	}
	if got != "current" {
		t.Fatalf("setting after rejected restore = %q, want %q", got, "current")
	}
}

func TestStageRestoreRejectsForeignKeyInvalidDatabase(t *testing.T) {
	ctx := context.Background()
	target, targetPath := openTemp(t)

	source, err := Open(filepath.Join(t.TempDir(), "foreign-key-invalid.sqlite"))
	if err != nil {
		t.Fatalf("Open source database: %v", err)
	}
	if _, err := source.DB().Exec("PRAGMA foreign_keys = OFF"); err != nil {
		source.Close()
		t.Fatalf("disable source foreign keys: %v", err)
	}
	if _, err := source.DB().Exec(
		"INSERT INTO library_access (library_id, user_id) VALUES (?, ?)", 1, 999999,
	); err != nil {
		source.Close()
		t.Fatalf("insert dangling library access: %v", err)
	}
	backup := storeBackup(t, source)
	if err := source.Close(); err != nil {
		t.Fatalf("Close source database: %v", err)
	}

	err = target.StageRestore(ctx, bytes.NewReader(backup), int64(len(backup)))
	if !errors.Is(err, ErrInvalidRestore) {
		t.Fatalf("StageRestore foreign-key-invalid database error = %v, want ErrInvalidRestore", err)
	}
	if _, err := os.Stat(stagedRestorePath(targetPath)); !os.IsNotExist(err) {
		t.Fatalf("foreign-key-invalid restore left pending file: %v", err)
	}
}

func TestStagedRestoreAppliesOnNextOpenAndKeepsRecoveryDatabase(t *testing.T) {
	ctx := context.Background()
	targetPath := filepath.Join(t.TempDir(), "caravan.db")
	target, err := Open(targetPath)
	if err != nil {
		t.Fatalf("Open target: %v", err)
	}
	if err := target.SetSetting(ctx, "restore-test", "current"); err != nil {
		target.Close()
		t.Fatalf("SetSetting current: %v", err)
	}

	source, err := Open(filepath.Join(t.TempDir(), "source.sqlite"))
	if err != nil {
		target.Close()
		t.Fatalf("Open source: %v", err)
	}
	if err := source.SetSetting(ctx, "restore-test", "restored"); err != nil {
		source.Close()
		target.Close()
		t.Fatalf("SetSetting restored: %v", err)
	}
	backup := storeBackup(t, source)
	if err := source.Close(); err != nil {
		target.Close()
		t.Fatalf("Close source: %v", err)
	}

	if err := target.StageRestore(ctx, bytes.NewReader(backup), int64(len(backup))); err != nil {
		target.Close()
		t.Fatalf("StageRestore: %v", err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(stagedRestorePath(targetPath))
		if err != nil {
			t.Fatalf("Stat staged restore: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("staged restore mode = %04o, want 0600", got)
		}
	}
	got, err := target.GetSetting(ctx, "restore-test")
	if err != nil {
		target.Close()
		t.Fatalf("GetSetting before reopen: %v", err)
	}
	if got != "current" {
		target.Close()
		t.Fatalf("setting before reopen = %q, want %q", got, "current")
	}
	if err := target.Close(); err != nil {
		t.Fatalf("Close target: %v", err)
	}

	restored, err := Open(targetPath)
	if err != nil {
		t.Fatalf("Open staged restore: %v", err)
	}
	defer restored.Close()
	got, err = restored.GetSetting(ctx, "restore-test")
	if err != nil {
		t.Fatalf("GetSetting restored database: %v", err)
	}
	if got != "restored" {
		t.Fatalf("restored setting = %q, want %q", got, "restored")
	}
	if _, err := os.Stat(stagedRestorePath(targetPath)); !os.IsNotExist(err) {
		t.Fatalf("applied restore left pending file: %v", err)
	}

	recovery, err := Open(recoveryRestorePath(targetPath))
	if err != nil {
		t.Fatalf("Open recovery database: %v", err)
	}
	defer recovery.Close()
	got, err = recovery.GetSetting(ctx, "restore-test")
	if err != nil {
		t.Fatalf("GetSetting recovery database: %v", err)
	}
	if got != "current" {
		t.Fatalf("recovery setting = %q, want %q", got, "current")
	}
}

func TestApplyStagedRestoreRestoresSQLiteArtifactsAfterSidecarMoveFailure(t *testing.T) {
	for _, failedSuffix := range []string{"-wal", "-shm"} {
		t.Run(failedSuffix, func(t *testing.T) {
			ctx := context.Background()
			targetPath := filepath.Join(t.TempDir(), "caravan.db")
			target, err := Open(targetPath)
			if err != nil {
				t.Fatalf("Open target: %v", err)
			}
			if err := target.SetSetting(ctx, "restore-test", "current"); err != nil {
				target.Close()
				t.Fatalf("SetSetting current: %v", err)
			}

			source, err := Open(filepath.Join(t.TempDir(), "source.sqlite"))
			if err != nil {
				target.Close()
				t.Fatalf("Open source: %v", err)
			}
			backup := storeBackup(t, source)
			if err := source.Close(); err != nil {
				target.Close()
				t.Fatalf("Close source: %v", err)
			}
			if err := target.StageRestore(ctx, bytes.NewReader(backup), int64(len(backup))); err != nil {
				target.Close()
				t.Fatalf("StageRestore: %v", err)
			}
			if err := target.Close(); err != nil {
				t.Fatalf("Close target: %v", err)
			}

			main, err := os.ReadFile(targetPath)
			if err != nil {
				t.Fatalf("ReadFile main database: %v", err)
			}
			artifacts := map[string][]byte{
				"":     main,
				"-wal": []byte("live WAL"),
				"-shm": []byte("live SHM"),
			}
			for suffix, want := range artifacts {
				if suffix == "" {
					continue
				}
				if err := os.WriteFile(targetPath+suffix, want, 0o600); err != nil {
					t.Fatalf("WriteFile %s: %v", suffix, err)
				}
			}

			rename := renameRestoreSidecar
			renameRestoreSidecar = func(from, to string) error {
				if from == targetPath+failedSuffix {
					return errors.New("injected sidecar move failure")
				}
				return rename(from, to)
			}
			t.Cleanup(func() { renameRestoreSidecar = rename })

			applied, err := applyStagedRestore(targetPath)
			if err == nil {
				t.Fatal("applyStagedRestore succeeded after injected sidecar move failure")
			}
			if applied {
				t.Fatal("applyStagedRestore reported a restore after rollback")
			}
			for suffix, want := range artifacts {
				got, err := os.ReadFile(targetPath + suffix)
				if err != nil {
					t.Fatalf("ReadFile restored %q artifact: %v", suffix, err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("restored %q artifact = %q, want %q", suffix, got, want)
				}
				if _, err := os.Stat(recoveryRestorePath(targetPath) + suffix); !os.IsNotExist(err) {
					t.Fatalf("recovery %q artifact remains after rollback: %v", suffix, err)
				}
			}
		})
	}
}

func TestApplyStagedRestoreRecoversInterruptedSQLiteArtifactMoves(t *testing.T) {
	for _, state := range []string{"cutover", "rollback"} {
		t.Run(state, func(t *testing.T) {
			ctx := context.Background()
			targetPath := filepath.Join(t.TempDir(), "caravan.db")
			target, err := Open(targetPath)
			if err != nil {
				t.Fatalf("Open target: %v", err)
			}
			if err := target.SetSetting(ctx, "restore-test", "current"); err != nil {
				target.Close()
				t.Fatalf("SetSetting current: %v", err)
			}

			source, err := Open(filepath.Join(t.TempDir(), "source.sqlite"))
			if err != nil {
				target.Close()
				t.Fatalf("Open source: %v", err)
			}
			backup := storeBackup(t, source)
			if err := source.Close(); err != nil {
				target.Close()
				t.Fatalf("Close source: %v", err)
			}
			if err := target.StageRestore(ctx, bytes.NewReader(backup), int64(len(backup))); err != nil {
				target.Close()
				t.Fatalf("StageRestore: %v", err)
			}
			if err := target.Close(); err != nil {
				t.Fatalf("Close target: %v", err)
			}

			main, err := os.ReadFile(targetPath)
			if err != nil {
				t.Fatalf("ReadFile main database: %v", err)
			}
			artifacts := map[string][]byte{
				"":     main,
				"-wal": []byte("live WAL"),
				"-shm": []byte("live SHM"),
			}
			for suffix, want := range artifacts {
				if suffix == "" {
					continue
				}
				if err := os.WriteFile(targetPath+suffix, want, 0o600); err != nil {
					t.Fatalf("WriteFile %s: %v", suffix, err)
				}
			}

			recovery := recoveryRestorePath(targetPath)
			if state == "cutover" {
				if err := os.Rename(targetPath, recovery); err != nil {
					t.Fatalf("move main to recovery: %v", err)
				}
			}
			if err := os.Rename(targetPath+"-wal", recovery+"-wal"); err != nil {
				t.Fatalf("move WAL to recovery: %v", err)
			}

			applied, err := applyStagedRestore(targetPath)
			if err != nil {
				t.Fatalf("applyStagedRestore: %v", err)
			}
			if !applied {
				t.Fatal("applyStagedRestore did not apply the staged restore")
			}
			if _, err := os.Stat(stagedRestorePath(targetPath)); !os.IsNotExist(err) {
				t.Fatalf("applied restore left pending file: %v", err)
			}
			for suffix, want := range artifacts {
				got, err := os.ReadFile(recovery + suffix)
				if err != nil {
					t.Fatalf("ReadFile recovered %q artifact: %v", suffix, err)
				}
				if !bytes.Equal(got, want) {
					t.Fatalf("recovered %q artifact = %q, want %q", suffix, got, want)
				}
			}
		})
	}
}

type blockingRestoreReader struct {
	reader  *bytes.Reader
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (r *blockingRestoreReader) Read(p []byte) (int, error) {
	r.once.Do(func() {
		close(r.entered)
		<-r.release
	})
	return r.reader.Read(p)
}

func restoreBackupWithSetting(t *testing.T, value string) []byte {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), value+".sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetSetting(context.Background(), "serialized-restore", value); err != nil {
		st.Close()
		t.Fatal(err)
	}
	backup := storeBackup(t, st)
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	return backup
}

// versionEightBackup is a real v8 database as bytes: Goose applied up to 8 and
// no further, one setting written, and the file snapshotted the way Store.Backup
// snapshots one.
//
// It is built by MIGRATING UP rather than by editing a current database back
// into shape. Un-migrating by hand only ever worked while every later migration
// was trigger-shaped; 0011 rebuilds two tables and seeds two rows, and a fixture
// that reverted the parts somebody remembered would be a v8 database in its
// migration history and a v11 one in its schema — which is precisely the state
// this test exists to prove is rejected.
func versionEightBackup(t *testing.T) []byte {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	path := filepath.Join(dir, "v8.sqlite")
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	provider, err := migrationProvider(db, storemigrations.FS())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(ctx, 8); err != nil {
		t.Fatalf("apply v8: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at)
		VALUES ('older-restore', 'preserved', '2026-08-17T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(dir, "snapshot.sqlite")
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		t.Fatalf("snapshot v8 database: %v", err)
	}
	backup, err := os.ReadFile(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	return backup
}

func TestStageRestorePreservesOlderSchemaMigrationOnReopen(t *testing.T) {
	ctx := context.Background()
	backup := versionEightBackup(t)

	targetPath := filepath.Join(t.TempDir(), "target.sqlite")
	target, err := Open(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := target.StageRestore(ctx, bytes.NewReader(backup), int64(len(backup))); err != nil {
		target.Close()
		t.Fatalf("StageRestore v8 backup: %v", err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(targetPath)
	if err != nil {
		t.Fatalf("reopen and migrate v8 restore: %v", err)
	}
	defer restored.Close()
	version, err := restored.SchemaVersion()
	if err != nil || int64(version) != storemigrations.LatestVersion {
		t.Fatalf("restored schema version=%d err=%v, want %d", version, err, storemigrations.LatestVersion)
	}
	got, err := restored.GetSetting(ctx, "older-restore")
	if err != nil || got != "preserved" {
		t.Fatalf("older restore setting=%q err=%v", got, err)
	}
}

func TestStageRestoreSerializesConcurrentCallsWithLastSuccessWinning(t *testing.T) {
	ctx := context.Background()
	first := restoreBackupWithSetting(t, "first")
	second := restoreBackupWithSetting(t, "second")
	targetPath := filepath.Join(t.TempDir(), "target.sqlite")
	target, err := Open(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	blocking := &blockingRestoreReader{reader: bytes.NewReader(first), entered: make(chan struct{}), release: make(chan struct{})}
	firstResult := make(chan error, 1)
	go func() { firstResult <- target.StageRestore(ctx, blocking, int64(len(first))) }()
	<-blocking.entered
	secondStarted := make(chan struct{})
	secondResult := make(chan error, 1)
	go func() {
		close(secondStarted)
		secondResult <- target.StageRestore(ctx, bytes.NewReader(second), int64(len(second)))
	}()
	<-secondStarted
	close(blocking.release)
	if err := <-firstResult; err != nil {
		t.Fatalf("first StageRestore: %v", err)
	}
	if err := <-secondResult; err != nil {
		t.Fatalf("second StageRestore: %v", err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.GetSetting(ctx, "serialized-restore")
	if err != nil || got != "second" {
		t.Fatalf("serialized restore value=%q err=%v, want second", got, err)
	}
}

func TestStageRestoreSyncsParentAndFailedSyncDoesNotReplacePriorSuccess(t *testing.T) {
	ctx := context.Background()
	first := restoreBackupWithSetting(t, "first")
	second := restoreBackupWithSetting(t, "second")
	targetPath := filepath.Join(t.TempDir(), "target.sqlite")
	target, err := Open(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	originalSync := restoreDirectorySync
	syncCalls := 0
	restoreDirectorySync = func(dir *os.File) error {
		syncCalls++
		return originalSync(dir)
	}
	if err := target.StageRestore(ctx, bytes.NewReader(first), int64(len(first))); err != nil {
		t.Fatalf("StageRestore with successful parent sync: %v", err)
	}
	if syncCalls == 0 {
		t.Fatal("StageRestore did not sync its parent directory")
	}
	injected := errors.New("injected parent sync failure")
	failed := false
	restoreDirectorySync = func(dir *os.File) error {
		if !failed {
			failed = true
			return injected
		}
		return originalSync(dir)
	}
	t.Cleanup(func() { restoreDirectorySync = originalSync })
	if err := target.StageRestore(ctx, bytes.NewReader(second), int64(len(second))); !errors.Is(err, injected) {
		t.Fatalf("StageRestore parent sync error=%v, want injected failure", err)
	}
	if err := target.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.GetSetting(ctx, "serialized-restore")
	if err != nil || got != "first" {
		t.Fatalf("failed sync replaced prior successful restore: value=%q err=%v", got, err)
	}
}
