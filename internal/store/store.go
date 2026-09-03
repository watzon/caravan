// Package store owns Caravan's sqlite database: connection setup, the embedded
// migration runner, and the CRUD the rest of the application needs.
//
// SPEC §1.2 pillar 2 makes the database a rebuildable cache, not the source of
// truth, the filesystem is. That shapes the schema: structural parent/child
// links cascade, but cross-subsystem references are loose integer ids so a
// stale row can never block an import or a rescan.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/sqlitedialect"
	_ "modernc.org/sqlite" // pure-Go sqlite driver; no CGO (SPEC §4)
)

// ErrNotFound is returned by the Get* methods when no row matches.
var (
	ErrNotFound           = errors.New("store: not found")
	ErrConflict           = errors.New("store: conflict")
	ErrLegacySchema       = errors.New("store: prerelease database schema is unsupported; start with a new database")
	ErrUnrecognizedSchema = errors.New("store: database schema is not recognized as Caravan")
)

// ChangeHook is an optional, non-blocking hint that a sidebar-visible
// resource just changed. The API stream uses it; the store does not wait
// on it. Resource names are the activity-stream invalidate keys:
// downloads, requests, jobs, library.
type ChangeHook func(resource string)

// Store is a handle on the Caravan database.
type Store struct {
	db   *bun.DB
	path string

	// restoreMu serializes StageRestore for this handle. The last successful
	// caller wins by replacing the single pending sidecar; callers never report
	// two uncoordinated staged restores in one process.
	restoreMu sync.Mutex

	// changeHook is read by every goroutine that writes through this handle
	// and installed once the API server exists, which is after the automation
	// runner and download engines have started writing. An atomic keeps that
	// install from racing the readers, and its happens-before edge is what
	// makes the hub behind the hook safe to call the moment it is visible.
	changeHook atomic.Pointer[ChangeHook]
}

// SetChangeHook installs the live-update hint. Nil disables it.
func (s *Store) SetChangeHook(hook ChangeHook) {
	if hook == nil {
		s.changeHook.Store(nil)
		return
	}
	s.changeHook.Store(&hook)
}

func (s *Store) note(resource string) {
	if s == nil || resource == "" {
		return
	}
	if hook := s.changeHook.Load(); hook != nil {
		(*hook)(resource)
	}
}

// Open opens (creating if needed) the sqlite database at path and runs every
// pending migration. If a validated restore is staged, it is installed before
// the database is opened and the previous main file is retained for recovery.
// WAL journaling is enabled so readers never block the writer, and foreign keys
// are enforced so the cascade rules in the schema actually fire.
//
// The returned Store must be closed with Close.
func Open(path string) (*Store, error) {
	if exists, err := databaseFileExists(path); err != nil {
		return nil, err
	} else if exists {
		// Reject unsupported files before chmod, WAL pragmas, or even creating the
		// migration lock sidecar. Rejection is a genuinely read-only operation.
		if err := preflightDatabaseIdentity(context.Background(), path); err != nil {
			return nil, err
		}
	}

	initLock, err := acquireDatabaseInitLock(path)
	if err != nil {
		return nil, err
	}
	defer initLock.Close()

	appliedRestore, err := applyStagedRestore(path)
	if err != nil {
		return nil, err
	}
	if exists, err := databaseFileExists(path); err != nil {
		return nil, restoreOpenFailure(path, appliedRestore, err)
	} else if exists {
		// Recheck after taking the lock: another opener may have created or
		// migrated the file while this caller was waiting.
		if err := validateDatabaseBeforeOpen(context.Background(), path); err != nil {
			return nil, restoreOpenFailure(path, appliedRestore, err)
		}
	}
	if err := hardenSQLiteArtifacts(path, true); err != nil {
		return nil, restoreOpenFailure(path, appliedRestore, err)
	}

	sqldb, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, restoreOpenFailure(path, appliedRestore, fmt.Errorf("store: open %s: %w", path, err))
	}

	// sqlite takes a single writer. Serializing at the pool removes
	// SQLITE_BUSY as a class of failure at the cost of write concurrency we
	// do not have anyway.
	sqldb.SetMaxOpenConns(1)

	if err := sqldb.Ping(); err != nil {
		sqldb.Close()
		return nil, restoreOpenFailure(path, appliedRestore, fmt.Errorf("store: connect %s: %w", path, err))
	}
	db := bun.NewDB(sqldb, sqlitedialect.New())
	s := &Store{db: db, path: path}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, restoreOpenFailure(path, appliedRestore, err)
	}
	if err := hardenSQLiteArtifacts(path, false); err != nil {
		db.Close()
		return nil, restoreOpenFailure(path, appliedRestore, err)
	}
	return s, nil
}

func databaseFileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	switch {
	case err == nil:
		return true, nil
	case os.IsNotExist(err):
		return false, nil
	default:
		return false, fmt.Errorf("store: stat database %s: %w", path, err)
	}
}

// hardenSQLiteArtifacts creates the main database with a private mode and
// repairs the main database and SQLite's WAL/SHM sidecars on every open.
func hardenSQLiteArtifacts(path string, createMain bool) error {
	if createMain {
		file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
		if err != nil {
			return fmt.Errorf("store: create database %s: %w", path, err)
		}
		if err := file.Close(); err != nil {
			return fmt.Errorf("store: close database bootstrap %s: %w", path, err)
		}
	}

	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("store: chmod database %s: %w", path, err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar := path + suffix
		if err := os.Chmod(sidecar, 0o600); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("store: chmod SQLite sidecar %s: %w", sidecar, err)
		}
	}
	return nil
}

// dsn builds the modernc.org/sqlite connection string. The pragmas must travel
// in the DSN rather than being executed afterwards: busy_timeout and
// foreign_keys are per-connection state, so every connection the pool opens
// has to set them itself.
func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "foreign_keys(1)")
	q.Add("_pragma", "busy_timeout(5000)")
	// normal is the documented safe pairing with WAL: durable across process
	// crashes, and a fsync per checkpoint rather than per commit.
	q.Add("_pragma", "synchronous(NORMAL)")
	return "file:" + path + "?" + q.Encode()
}

// Close closes the database. Portable mode checkpoints the WAL before this
// (SPEC §2.3); that is the caller's job because it is a shutdown-policy
// decision, not a connection one.
func (s *Store) Close() error {
	return s.db.Close()
}

// DB exposes the underlying handle for callers that need a transaction or a
// query this package does not wrap yet.
func (s *Store) DB() *sql.DB {
	return s.db.DB
}

// ORM exposes Caravan's Bun handle for typed models and query construction.
// SQLite-specific operational queries can continue through DB when needed.
func (s *Store) ORM() *bun.DB {
	return s.db
}

// Checkpoint flushes the write-ahead log into the main database file and
// truncates it. Portable mode runs this before ejecting a drive (SPEC §2.3).
func (s *Store) Checkpoint() error {
	if _, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		return fmt.Errorf("store: wal checkpoint: %w", err)
	}
	return nil
}

// IntegrityCheck runs sqlite's own consistency check over the whole database
// and reports the first problem it finds.
//
// The dirty-eject recovery flow (SPEC §13) runs this before letting downloads
// resume: a database that came back from a yanked drive with torn pages must
// not be written to. It is deliberately the full check rather than quick_check.
// A portable library is small, and the cheap check skips exactly the index
// corruption a half-written page produces.
func (s *Store) IntegrityCheck(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return fmt.Errorf("store: integrity check: %w", err)
	}
	defer rows.Close()

	// A healthy database answers with the single row "ok"; a damaged one
	// answers with up to 100 rows describing the damage.
	problems := []string{}
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return fmt.Errorf("store: integrity check: %w", err)
		}
		if line != "ok" {
			problems = append(problems, line)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: integrity check: %w", err)
	}
	if len(problems) > 0 {
		return fmt.Errorf("store: integrity check: %s", strings.Join(problems, "; "))
	}
	return nil
}

// timeFormat is how timestamps are stored: RFC3339 in UTC, so string ordering
// matches chronological ordering.
const timeFormat = time.RFC3339Nano

// formatTime renders t for storage. The zero time becomes the empty string so
// "unset" is distinguishable from "the epoch".
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(timeFormat)
}

// parseTime reads a stored timestamp. Unparseable values yield the zero time
// rather than an error: a corrupt cache row must not break a library listing.
func parseTime(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// now is the clock used for created/updated stamps.
func now() time.Time { return time.Now().UTC() }

// isUniqueViolation reports whether err is sqlite's uniqueness complaint. The
// driver does not export a typed error for it, so the message is the only
// handle; a false negative degrades to a generic 500, never to a lost write.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}
