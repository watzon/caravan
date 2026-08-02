package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const storageMigrationColumns = `id, source_root, target_root, status,
	files_total, files_done, bytes_total, bytes_done, error, created_at, updated_at`

// ErrStorageMigrationOpen is returned by CreateStorageMigration when one is
// already queued or running. It is a distinct error rather than a generic
// constraint failure because the HTTP layer answers it with 409, not 500.
var ErrStorageMigrationOpen = errors.New("store: a storage migration is already open")

// CreateStorageMigration inserts a queued migration and writes back its ID.
//
// The partial unique index over open statuses is what makes this safe against a
// double submit: the second insert loses, and the caller sees
// ErrStorageMigrationOpen rather than two movers over the same trees.
func (s *Store) CreateStorageMigration(ctx context.Context, m *core.StorageMigration) error {
	ts := now()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = ts
	}
	m.UpdatedAt = ts
	if m.Status == "" {
		m.Status = core.StorageMigrationQueued
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO storage_migrations (source_root, target_root, status,
			files_total, files_done, bytes_total, bytes_done, error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.SourceRoot, m.TargetRoot, m.Status, m.FilesTotal, m.FilesDone,
		m.BytesTotal, m.BytesDone, m.Error, formatTime(m.CreatedAt), formatTime(m.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("store: create storage migration: %w", ErrStorageMigrationOpen)
		}
		return fmt.Errorf("store: create storage migration: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: create storage migration: %w", err)
	}
	m.ID = id
	return nil
}

// UpdateStorageMigration writes the mutable half of a migration back. Updating
// an absent migration is ErrNotFound.
func (s *Store) UpdateStorageMigration(ctx context.Context, m *core.StorageMigration) error {
	m.UpdatedAt = now()
	res, err := s.db.ExecContext(ctx, `
		UPDATE storage_migrations
		SET status = ?, files_total = ?, files_done = ?, bytes_total = ?,
			bytes_done = ?, error = ?, updated_at = ?
		WHERE id = ?`,
		m.Status, m.FilesTotal, m.FilesDone, m.BytesTotal, m.BytesDone,
		m.Error, formatTime(m.UpdatedAt), m.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("store: update storage migration %d: %w", m.ID, ErrStorageMigrationOpen)
		}
		return fmt.Errorf("store: update storage migration %d: %w", m.ID, err)
	}
	return affectedOne(res, "update storage migration", m.ID)
}

// GetStorageMigration returns one migration, or ErrNotFound.
func (s *Store) GetStorageMigration(ctx context.Context, id int64) (*core.StorageMigration, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+storageMigrationColumns+" FROM storage_migrations WHERE id = ?", id)
	m, err := scanStorageMigration(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: storage migration %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get storage migration %d: %w", id, err)
	}
	return m, nil
}

// OpenStorageMigration returns the queued or running migration, or ErrNotFound
// when there is none. It is the check every operation that touches the roots
// runs first.
func (s *Store) OpenStorageMigration(ctx context.Context) (*core.StorageMigration, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+storageMigrationColumns+" FROM storage_migrations WHERE status IN (?, ?) ORDER BY id DESC LIMIT 1",
		core.StorageMigrationQueued, core.StorageMigrationRunning)
	m, err := scanStorageMigration(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: open storage migration: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: open storage migration: %w", err)
	}
	return m, nil
}

// LatestStorageMigration returns the most recent migration whatever its status,
// or ErrNotFound when none has ever run. It is what the settings screen polls:
// a finished move has to stay on screen long enough to be read.
func (s *Store) LatestStorageMigration(ctx context.Context) (*core.StorageMigration, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+storageMigrationColumns+" FROM storage_migrations ORDER BY id DESC LIMIT 1")
	m, err := scanStorageMigration(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: latest storage migration: %w", ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: latest storage migration: %w", err)
	}
	return m, nil
}

func scanStorageMigration(sc scanner) (*core.StorageMigration, error) {
	var (
		m         core.StorageMigration
		createdAt string
		updatedAt string
	)
	err := sc.Scan(&m.ID, &m.SourceRoot, &m.TargetRoot, &m.Status,
		&m.FilesTotal, &m.FilesDone, &m.BytesTotal, &m.BytesDone, &m.Error,
		&createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	m.CreatedAt = parseTime(createdAt)
	m.UpdatedAt = parseTime(updatedAt)
	return &m, nil
}
