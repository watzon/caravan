package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	"github.com/watzon/caravan/internal/core"
	storemigrations "github.com/watzon/caravan/internal/store/migrations"
)

var (
	// ErrRestoreTooLarge reports that a restore source exceeded the limit its
	// caller supplied. The store never reads beyond that limit plus one byte.
	ErrRestoreTooLarge = errors.New("store: restore is too large")
	// ErrInvalidRestore reports a corrupt database or one that was not made by
	// a compatible Caravan schema.
	ErrInvalidRestore = errors.New("store: invalid restore database")
)

const (
	restorePendingSuffix  = ".restore-pending"
	restoreRecoverySuffix = ".before-restore"
)

// renameRestoreSidecar lets tests simulate an interrupted sidecar cutover.
var renameRestoreSidecar = os.Rename

var (
	restoreFileSync      = func(file *os.File) error { return file.Sync() }
	restoreDirectorySync = func(dir *os.File) error { return dir.Sync() }
)

// DefinitionPackSnapshot is the immutable pack inventory read from one verified,
// read-only SQLite snapshot. Entries are keyed by source+"\x00"+revision.
type DefinitionPackSnapshot struct {
	Revisions []core.DefinitionPackRevision
	Entries   map[string][]core.DefinitionPackEntry
}

func DefinitionPackSnapshotKey(source, revision string) string { return source + "\x00" + revision }

// BackupAndInspect writes one bounded SQLite snapshot and returns only pack
// receipt data read from that exact file. It never reads live pack rows after
// VACUUM INTO, and it never migrates or writes the snapshot during inspection.
func (s *Store) BackupAndInspect(ctx context.Context, dst io.Writer, maxBytes int64) (DefinitionPackSnapshot, error) {
	if maxBytes <= 0 {
		return DefinitionPackSnapshot{}, fmt.Errorf("store: backup size limit must be positive")
	}
	dir, err := os.MkdirTemp(filepath.Dir(s.path), "."+filepath.Base(s.path)+".backup-")
	if err != nil {
		return DefinitionPackSnapshot{}, fmt.Errorf("store: create backup directory: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return DefinitionPackSnapshot{}, fmt.Errorf("store: chmod backup directory: %w", err)
	}
	snapshot := filepath.Join(dir, "snapshot.sqlite")
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		return DefinitionPackSnapshot{}, fmt.Errorf("store: create backup snapshot: %w", err)
	}
	if err := os.Chmod(snapshot, 0o600); err != nil {
		return DefinitionPackSnapshot{}, fmt.Errorf("store: chmod backup snapshot: %w", err)
	}
	inventory, err := InspectDefinitionPackSnapshot(ctx, snapshot)
	if err != nil {
		return DefinitionPackSnapshot{}, err
	}
	file, err := os.Open(snapshot)
	if err != nil {
		return DefinitionPackSnapshot{}, fmt.Errorf("store: open backup snapshot: %w", err)
	}
	defer file.Close()
	written, err := io.Copy(dst, io.LimitReader(file, maxBytes+1))
	if err != nil {
		return DefinitionPackSnapshot{}, fmt.Errorf("store: write backup snapshot: %w", err)
	}
	if written > maxBytes {
		return DefinitionPackSnapshot{}, ErrRestoreTooLarge
	}
	return inventory, nil
}

// Backup writes a transactionally consistent SQLite snapshot to dst. VACUUM
// INTO copies the database as SQLite sees it, including committed WAL content;
// copying the main file would silently omit those committed pages.
func (s *Store) Backup(ctx context.Context, dst io.Writer) error {
	dir, err := os.MkdirTemp(filepath.Dir(s.path), "."+filepath.Base(s.path)+".backup-")
	if err != nil {
		return fmt.Errorf("store: create backup directory: %w", err)
	}
	defer os.RemoveAll(dir)
	if err := os.Chmod(dir, 0o700); err != nil {
		return fmt.Errorf("store: chmod backup directory: %w", err)
	}
	snapshot := filepath.Join(dir, "snapshot.sqlite")
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO ?", snapshot); err != nil {
		return fmt.Errorf("store: create backup snapshot: %w", err)
	}
	file, err := os.Open(snapshot)
	if err != nil {
		return fmt.Errorf("store: open backup snapshot: %w", err)
	}
	defer file.Close()
	if _, err := io.Copy(dst, file); err != nil {
		return fmt.Errorf("store: write backup snapshot: %w", err)
	}
	return nil
}

// InspectDefinitionPackSnapshot validates a database read-only as the exact
// current Caravan schema and returns its pack inventory. Older, repairable, and
// newer databases are rejected; this seam never invokes Store.Open or Goose.
func InspectDefinitionPackSnapshot(ctx context.Context, path string) (DefinitionPackSnapshot, error) {
	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		return DefinitionPackSnapshot{}, invalidRestore(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return DefinitionPackSnapshot{}, invalidRestore(err)
	}
	if err := integrityCheck(ctx, db); err != nil {
		return DefinitionPackSnapshot{}, invalidRestore(err)
	}
	if err := foreignKeyCheck(ctx, db); err != nil {
		return DefinitionPackSnapshot{}, invalidRestore(err)
	}
	version, err := schemaVersion(ctx, db)
	if err != nil {
		return DefinitionPackSnapshot{}, invalidRestore(err)
	}
	if int64(version) != storemigrations.LatestVersion {
		return DefinitionPackSnapshot{}, invalidRestore(fmt.Errorf("database is not current schema v%d", storemigrations.LatestVersion))
	}
	if err := validateCurrentSchema(ctx, db); err != nil {
		return DefinitionPackSnapshot{}, invalidRestore(err)
	}
	bunDB := bun.NewDB(db, sqlitedialect.New())
	view := &Store{db: bunDB, path: path}
	revisions, err := view.ListDefinitionPackRevisions(ctx)
	if err != nil {
		return DefinitionPackSnapshot{}, invalidRestore(err)
	}
	result := DefinitionPackSnapshot{Revisions: revisions, Entries: make(map[string][]core.DefinitionPackEntry, len(revisions))}
	for _, revision := range revisions {
		entries, err := view.ListDefinitionPackEntries(ctx, revision.Source, revision.Revision)
		if err != nil {
			return DefinitionPackSnapshot{}, invalidRestore(err)
		}
		result.Entries[DefinitionPackSnapshotKey(revision.Source, revision.Revision)] = entries
	}
	return result, nil
}

// StageRestore validates src and atomically makes it the restore that Open
// applies next. It never touches the currently open database.
func (s *Store) StageRestore(ctx context.Context, src io.Reader, maxBytes int64) error {
	if maxBytes <= 0 {
		return fmt.Errorf("store: restore size limit must be positive")
	}
	s.restoreMu.Lock()
	defer s.restoreMu.Unlock()

	stage, err := os.CreateTemp(filepath.Dir(s.path), "."+filepath.Base(s.path)+".restore-")
	if err != nil {
		return fmt.Errorf("store: create restore stage: %w", err)
	}
	stagePath := stage.Name()
	defer os.Remove(stagePath)

	if err := stage.Chmod(0o600); err != nil {
		stage.Close()
		return fmt.Errorf("store: chmod restore stage: %w", err)
	}
	written, copyErr := io.Copy(stage, io.LimitReader(src, maxBytes+1))
	if syncErr := restoreFileSync(stage); copyErr == nil {
		copyErr = syncErr
	}
	if closeErr := stage.Close(); copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return fmt.Errorf("store: copy restore stage: %w", copyErr)
	}
	if written > maxBytes {
		return ErrRestoreTooLarge
	}
	if err := validateRestore(ctx, stagePath); err != nil {
		return err
	}

	pending := stagedRestorePath(s.path)
	previous := pending + ".previous"
	if err := removeSQLiteArtifacts(previous); err != nil {
		return fmt.Errorf("store: remove previous staged restore backup: %w", err)
	}
	hadPrevious := false
	if _, err := os.Stat(pending); err == nil {
		if err := os.Rename(pending, previous); err != nil {
			return fmt.Errorf("store: retain previous staged restore: %w", err)
		}
		hadPrevious = true
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("store: inspect previous staged restore: %w", err)
	}
	if err := removeSQLiteSidecars(pending); err != nil {
		if hadPrevious {
			_ = os.Rename(previous, pending)
		}
		return fmt.Errorf("store: remove previous staged restore sidecars: %w", err)
	}
	if err := os.Rename(stagePath, pending); err != nil {
		if hadPrevious {
			_ = os.Rename(previous, pending)
		}
		return fmt.Errorf("store: stage restore: %w", err)
	}
	if err := syncRestoreParent(s.path); err != nil {
		rollbackErr := removeSQLiteArtifacts(pending)
		if hadPrevious {
			rollbackErr = errors.Join(rollbackErr, os.Rename(previous, pending))
		}
		rollbackErr = errors.Join(rollbackErr, syncRestoreParent(s.path))
		return fmt.Errorf("store: sync staged restore parent: %w", errors.Join(err, rollbackErr))
	}
	// The new pending restore is durable. A retained predecessor is now only a
	// rollback scratch file; best-effort cleanup cannot change success semantics.
	_ = removeSQLiteArtifacts(previous)
	return nil
}

func syncRestoreParent(path string) error {
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return fmt.Errorf("open restore parent for sync: %w", err)
	}
	syncErr, closeErr := restoreDirectorySync(parent), parent.Close()
	return errors.Join(syncErr, closeErr)
}

func stagedRestorePath(path string) string { return path + restorePendingSuffix }

func recoveryRestorePath(path string) string { return path + restoreRecoverySuffix }

func restoreOpenFailure(path string, applied bool, cause error) error {
	if !applied {
		return cause
	}
	if err := rollbackAppliedRestore(path); err != nil {
		return fmt.Errorf("%w; store: restore previous database: %v", cause, err)
	}
	return cause
}

// recoverInterruptedRestore completes a cutover or rollback stopped by a
// process crash. It moves artifacts only when their locations identify an
// incomplete current restore, not a previous restore kept for rollback.
func recoverInterruptedRestore(path string) error {
	recovery := recoveryRestorePath(path)
	recoveryMain, err := sqliteArtifactPresent(recovery)
	if err != nil {
		return err
	}
	currentMain, err := sqliteArtifactPresent(path)
	if err != nil {
		return err
	}

	switch {
	case recoveryMain && !currentMain:
		if err := os.Rename(recovery, path); err != nil {
			return err
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			if err := moveIfPresent(recovery+suffix, path+suffix); err != nil {
				return err
			}
		}
	case !recoveryMain && currentMain:
		for _, suffix := range []string{"-wal", "-shm"} {
			if err := moveIfPresent(recovery+suffix, path+suffix); err != nil {
				return err
			}
		}
	}
	return nil
}

func sqliteArtifactPresent(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}

// applyStagedRestore is called before Open creates a connection, so it never
// replaces a database that this process has open. A failed install is rolled
// back before Open returns.
func applyStagedRestore(path string) (bool, error) {
	pending := stagedRestorePath(path)
	if _, err := os.Stat(pending); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("store: stat staged restore: %w", err)
	}
	if err := recoverInterruptedRestore(path); err != nil {
		return false, fmt.Errorf("store: recover interrupted restore: %w", err)
	}
	if err := removeSQLiteSidecars(pending); err != nil {
		return false, fmt.Errorf("store: remove staged restore sidecars: %w", err)
	}
	if err := validateRestore(context.Background(), pending); err != nil {
		return false, fmt.Errorf("store: validate staged restore: %w", err)
	}
	if _, err := os.Stat(path); err != nil {
		return false, fmt.Errorf("store: stat current database before restore: %w", err)
	}

	recovery := recoveryRestorePath(path)
	if err := removeSQLiteArtifacts(recovery); err != nil {
		return false, fmt.Errorf("store: remove previous recovery database: %w", err)
	}
	if err := os.Rename(path, recovery); err != nil {
		return false, fmt.Errorf("store: retain database before restore: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := moveIfPresent(path+suffix, recovery+suffix); err != nil {
			return false, rollbackRestoreApply(path, err)
		}
	}
	if err := removeSQLiteSidecars(path); err != nil {
		return false, rollbackRestoreApply(path, err)
	}
	if err := os.Rename(pending, path); err != nil {
		return false, rollbackRestoreApply(path, err)
	}
	return true, nil
}

func rollbackRestoreApply(path string, cause error) error {
	if err := rollbackRestoreCutover(path); err != nil {
		return fmt.Errorf("store: apply staged restore: %w; rollback previous database: %v", cause, err)
	}
	return fmt.Errorf("store: apply staged restore: %w", cause)
}

// rollbackRestoreCutover restores a previous database before the staged file
// replaces it. Do not remove sidecars at path. A failed move can leave a
// previous sidecar there, and deleting it loses committed SQLite data.
func rollbackRestoreCutover(path string) error {
	if err := removeFile(path); err != nil {
		return err
	}
	recovery := recoveryRestorePath(path)
	if err := os.Rename(recovery, path); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := moveIfPresent(recovery+suffix, path+suffix); err != nil {
			return err
		}
	}
	return nil
}

// rollbackAppliedRestore restores the previous database after the staged copy
// was installed but could not be opened or migrated.
func rollbackAppliedRestore(path string) error {
	if err := removeSQLiteArtifacts(path); err != nil {
		return err
	}
	recovery := recoveryRestorePath(path)
	if err := os.Rename(recovery, path); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := moveIfPresent(recovery+suffix, path+suffix); err != nil {
			return err
		}
	}
	return nil
}

func moveIfPresent(from, to string) error {
	if err := renameRestoreSidecar(from, to); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func removeSQLiteArtifacts(path string) error {
	if err := removeFile(path); err != nil {
		return err
	}
	return removeSQLiteSidecars(path)
}

func removeSQLiteSidecars(path string) error {
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := removeFile(path + suffix); err != nil {
			return err
		}
	}
	return nil
}

func removeFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// validateRestore requires both SQLite's integrity verdict and the exact
// migration history this binary knows. A random SQLite database, or a backup
// from a newer Caravan, cannot be staged as a restore.
func validateRestore(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", readOnlyDSN(path))
	if err != nil {
		return invalidRestore(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		return invalidRestore(err)
	}
	if err := integrityCheck(ctx, db); err != nil {
		return invalidRestore(err)
	}
	if err := foreignKeyCheck(ctx, db); err != nil {
		return invalidRestore(err)
	}

	if err := validateSchemaIdentity(ctx, db, false); err != nil {
		if repairableErr := validateRepairableV8Preflight(ctx, db); repairableErr != nil {
			return invalidRestore(err)
		}
	}
	return nil
}

func readOnlyDSN(path string) string {
	q := url.Values{}
	q.Set("mode", "ro")
	q.Add("_pragma", "query_only(1)")
	return "file:" + path + "?" + q.Encode()
}

func integrityCheck(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return err
		}
		if result != "ok" {
			return errors.New(result)
		}
	}
	return rows.Err()
}

func foreignKeyCheck(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return err
	}
	defer rows.Close()
	if rows.Next() {
		var table string
		var rowID sql.NullInt64
		var parent string
		var constraint int
		if err := rows.Scan(&table, &rowID, &parent, &constraint); err != nil {
			return err
		}
		return fmt.Errorf("foreign key violation in table %s row %v referencing %s", table, rowID, parent)
	}
	return rows.Err()
}

func invalidRestore(err error) error {
	return fmt.Errorf("%w: %v", ErrInvalidRestore, err)
}
