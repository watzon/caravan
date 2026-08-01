package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const unmatchedColumns = `id, path, size, parsed, reason, seen_at`

// UpsertUnmatchedFile parks a file in the scan-review queue, or refreshes the
// entry if it is already parked. Identity is the storage-root-relative Path,
// so rescanning the same messy folder does not multiply the queue.
func (s *Store) UpsertUnmatchedFile(ctx context.Context, u *core.UnmatchedFile) error {
	parsed, err := json.Marshal(u.Parsed)
	if err != nil {
		return fmt.Errorf("store: encode parsed release for %q: %w", u.Path, err)
	}
	if u.SeenAt.IsZero() {
		u.SeenAt = now()
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO unmatched_files (path, size, parsed, reason, seen_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (path) DO UPDATE SET
			size = excluded.size, parsed = excluded.parsed,
			reason = excluded.reason, seen_at = excluded.seen_at`,
		u.Path, u.Size, string(parsed), u.Reason, formatTime(u.SeenAt))
	if err != nil {
		return fmt.Errorf("store: upsert unmatched file %q: %w", u.Path, err)
	}
	if u.ID != 0 {
		return nil
	}
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM unmatched_files WHERE path = ?", u.Path).Scan(&u.ID); err != nil {
		return fmt.Errorf("store: upsert unmatched file %q: %w", u.Path, err)
	}
	return nil
}

// GetUnmatchedFile returns the parked file with the given id, or ErrNotFound.
// This is the lookup behind manual-match import.
func (s *Store) GetUnmatchedFile(ctx context.Context, id int64) (*core.UnmatchedFile, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+unmatchedColumns+" FROM unmatched_files WHERE id = ?", id)
	u, err := scanUnmatchedFile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: unmatched file %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get unmatched file %d: %w", id, err)
	}
	return u, nil
}

// ListUnmatchedFiles returns the scan-review queue ordered by path.
func (s *Store) ListUnmatchedFiles(ctx context.Context) ([]core.UnmatchedFile, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+unmatchedColumns+" FROM unmatched_files ORDER BY path")
	if err != nil {
		return nil, fmt.Errorf("store: list unmatched files: %w", err)
	}
	defer rows.Close()

	out := []core.UnmatchedFile{}
	for rows.Next() {
		u, err := scanUnmatchedFile(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan unmatched file: %w", err)
		}
		out = append(out, *u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list unmatched files: %w", err)
	}
	return out, nil
}

// DeleteUnmatchedFileByPath clears a file from the review queue, which is what
// a successful manual match does. Deleting an absent path is not an error.
func (s *Store) DeleteUnmatchedFileByPath(ctx context.Context, path string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM unmatched_files WHERE path = ?", path); err != nil {
		return fmt.Errorf("store: delete unmatched file %q: %w", path, err)
	}
	return nil
}

func scanUnmatchedFile(sc scanner) (*core.UnmatchedFile, error) {
	var (
		u      core.UnmatchedFile
		parsed string
		seenAt string
	)
	if err := sc.Scan(&u.ID, &u.Path, &u.Size, &parsed, &u.Reason, &seenAt); err != nil {
		return nil, err
	}
	if parsed != "" {
		// A row whose JSON no longer decodes must still list: the point of
		// this queue is visibility, and the user can re-parse from the UI.
		_ = json.Unmarshal([]byte(parsed), &u.Parsed)
	}
	u.SeenAt = parseTime(seenAt)
	return &u, nil
}
