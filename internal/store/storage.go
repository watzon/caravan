package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

// ErrStorageMigrationOpen is returned by CreateStorageMigration when one is
// already queued or running.
var ErrStorageMigrationOpen = errors.New("store: a storage migration is already open")

// CreateStorageMigration inserts a queued migration and writes back its ID.
func (s *Store) CreateStorageMigration(ctx context.Context, m *core.StorageMigration) error {
	ts := now()
	if m.CreatedAt.IsZero() {
		m.CreatedAt = ts
	}
	m.UpdatedAt = ts
	if m.Status == "" {
		m.Status = core.StorageMigrationQueued
	}

	model := storageMigrationModelFromCore(m)
	err := s.db.NewInsert().Model(&model).Returning("id").Scan(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("store: create storage migration: %w", ErrStorageMigrationOpen)
		}
		return fmt.Errorf("store: create storage migration: %w", err)
	}
	m.ID = model.ID
	return nil
}

// UpdateStorageMigration writes the mutable half of a migration back. Updating
// an absent migration is ErrNotFound.
func (s *Store) UpdateStorageMigration(ctx context.Context, m *core.StorageMigration) error {
	m.UpdatedAt = now()
	model := storageMigrationModelFromCore(m)
	res, err := s.db.NewUpdate().Model(&model).
		Column("status", "files_total", "files_done", "bytes_total", "bytes_done", "error", "updated_at").
		WherePK().Exec(ctx)
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
	return s.storageMigration(ctx, s.db.NewSelect().Model((*storageMigrationModel)(nil)).Where("id = ?", id),
		fmt.Sprintf("storage migration %d", id))
}

// OpenStorageMigration returns the queued or running migration, or ErrNotFound.
func (s *Store) OpenStorageMigration(ctx context.Context) (*core.StorageMigration, error) {
	query := s.db.NewSelect().Model((*storageMigrationModel)(nil)).
		Where("status IN (?)", bun.In([]string{core.StorageMigrationQueued, core.StorageMigrationRunning})).
		OrderExpr("id DESC").Limit(1)
	return s.storageMigration(ctx, query, "open storage migration")
}

// LatestStorageMigration returns the most recent migration whatever its status,
// or ErrNotFound when none has ever run.
func (s *Store) LatestStorageMigration(ctx context.Context) (*core.StorageMigration, error) {
	query := s.db.NewSelect().Model((*storageMigrationModel)(nil)).OrderExpr("id DESC").Limit(1)
	return s.storageMigration(ctx, query, "latest storage migration")
}

func (s *Store) storageMigration(ctx context.Context, query *bun.SelectQuery, what string) (*core.StorageMigration, error) {
	var model storageMigrationModel
	err := query.Model(&model).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: %s: %w", what, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: %s: %w", what, err)
	}
	out := model.core()
	return &out, nil
}
