package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

// remotePathMappingModel is the database representation of a remote path
// mapping. Timestamps remain strings so the store keeps its RFC3339Nano/empty
// string persistence semantics at the ORM boundary.
type remotePathMappingModel struct {
	bun.BaseModel `bun:"table:remote_path_mappings,alias:remote_path_mapping"`

	ID            int64 `bun:",pk,autoincrement"`
	RemotePath    string
	LocalPath     string
	MatchCount    int64
	LastMatchedAt *string
	CreatedAt     string
	UpdatedAt     string
}

func (m remotePathMappingModel) coreValue() core.RemotePathMapping {
	out := core.RemotePathMapping{
		ID:         m.ID,
		RemotePath: m.RemotePath,
		LocalPath:  m.LocalPath,
		MatchCount: m.MatchCount,
		CreatedAt:  parseTime(m.CreatedAt),
		UpdatedAt:  parseTime(m.UpdatedAt),
	}
	if m.LastMatchedAt != nil {
		out.LastMatchedAt = parseTime(*m.LastMatchedAt)
	}
	return out
}

// CreateRemotePathMapping inserts m and writes back its assigned ID and times.
func (s *Store) CreateRemotePathMapping(ctx context.Context, m *core.RemotePathMapping) error {
	ts := formatTime(now())
	model := &remotePathMappingModel{
		RemotePath: m.RemotePath,
		LocalPath:  m.LocalPath,
		CreatedAt:  ts,
		UpdatedAt:  ts,
	}
	if _, err := s.db.NewInsert().Model(model).Exec(ctx); err != nil {
		return fmt.Errorf("store: create remote path mapping %q: %w", m.RemotePath, err)
	}
	m.ID = model.ID
	m.CreatedAt = parseTime(ts)
	m.UpdatedAt = m.CreatedAt
	return nil
}

// UpdateRemotePathMapping replaces the paths for an existing mapping.
func (s *Store) UpdateRemotePathMapping(ctx context.Context, m *core.RemotePathMapping) error {
	ts := formatTime(now())
	model := &remotePathMappingModel{
		ID:         m.ID,
		RemotePath: m.RemotePath,
		LocalPath:  m.LocalPath,
		UpdatedAt:  ts,
	}
	res, err := s.db.NewUpdate().Model(model).
		Column("remote_path", "local_path", "updated_at").
		WherePK().Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: update remote path mapping %d: %w", m.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update remote path mapping %d: %w", m.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("store: update remote path mapping %d: %w", m.ID, ErrNotFound)
	}
	m.UpdatedAt = parseTime(ts)
	return nil
}

// GetRemotePathMapping returns one mapping, or ErrNotFound.
func (s *Store) GetRemotePathMapping(ctx context.Context, id int64) (*core.RemotePathMapping, error) {
	var model remotePathMappingModel
	err := s.db.NewSelect().Model(&model).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: remote path mapping %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get remote path mapping %d: %w", id, err)
	}
	out := model.coreValue()
	return &out, nil
}

// ListRemotePathMappings returns every mapping in stable remote-path order.
func (s *Store) ListRemotePathMappings(ctx context.Context) ([]core.RemotePathMapping, error) {
	models := make([]remotePathMappingModel, 0)
	if err := s.db.NewSelect().Model(&models).
		OrderExpr("remote_path COLLATE NOCASE ASC").
		Order("id ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list remote path mappings: %w", err)
	}

	out := make([]core.RemotePathMapping, 0, len(models))
	for _, model := range models {
		out = append(out, model.coreValue())
	}
	return out, nil
}

// RecordRemotePathMappingMatch records one successful translation. It runs
// only when an import actually selects the mapping, so the settings screen can
// distinguish configured roots from roots that Caravan has observed in use.
func (s *Store) RecordRemotePathMappingMatch(ctx context.Context, id int64) error {
	res, err := s.db.NewUpdate().Model((*remotePathMappingModel)(nil)).
		Set("match_count = match_count + 1").
		Set("last_matched_at = ?", formatTime(now())).
		Where("id = ?", id).
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: record remote path mapping %d match: %w", id, err)
	}
	if err := affectedOne(res, "remote path mapping", id); err != nil {
		return err
	}
	return nil
}

// DeleteRemotePathMapping removes a mapping. Missing rows are already deleted.
func (s *Store) DeleteRemotePathMapping(ctx context.Context, id int64) error {
	if _, err := s.db.NewDelete().Model((*remotePathMappingModel)(nil)).
		Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("store: delete remote path mapping %d: %w", id, err)
	}
	return nil
}
