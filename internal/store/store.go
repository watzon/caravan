// Package store owns Caravan's sqlite database: connection setup, the
// embedded migration runner, and the CRUD the rest of the application needs.
//
// SPEC §1.2 pillar 2 makes the database a rebuildable cache, not the source of
// truth — the filesystem is. That shapes the schema: structural parent/child
// links cascade, but cross-subsystem references are loose integer ids so a
// stale row can never block an import or a rescan.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure-Go sqlite driver; no CGO (SPEC §4)
)

// ErrNotFound is returned by the Get* methods when no row matches.
var ErrNotFound = errors.New("store: not found")

// Store is a handle on the Caravan database.
type Store struct {
	db *sql.DB
}

// Open opens (creating if needed) the sqlite database at path and runs every
// pending migration. WAL journaling is enabled so readers never block the
// writer, and foreign keys are enforced so the cascade rules in the schema
// actually fire.
//
// The returned Store must be closed with Close.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}

	// sqlite takes a single writer. Serializing at the pool removes
	// SQLITE_BUSY as a class of failure at the cost of write concurrency we
	// do not have anyway.
	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: connect %s: %w", path, err)
	}

	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
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
	// NORMAL is the documented safe pairing with WAL: durable across process
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
// not be written to. It is deliberately the full check rather than
// quick_check — a portable library is small, and the cheap check skips exactly
// the index corruption a half-written page produces.
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
