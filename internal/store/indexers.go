package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

var indexerReadColumns = []string{
	"id", "name", "url", "api_key", "protocol", "categories", "priority", "enabled",
}

var indexerWriteColumns = []string{
	"name", "url", "api_key", "protocol", "categories", "priority", "enabled", "updated_at",
}

// UpsertIndexer inserts or updates c and writes back the assigned ID.
// Identity is c.ID when set; otherwise a new indexer is inserted.
func (s *Store) UpsertIndexer(ctx context.Context, c *core.IndexerConfig) error {
	categories, err := json.Marshal(c.Categories)
	if err != nil {
		return fmt.Errorf("store: encode categories of indexer %q: %w", c.Name, err)
	}
	ts := formatTime(now())
	model := indexerModelFromCore(c, string(categories))
	model.UpdatedAt = ts

	if c.ID != 0 {
		res, err := s.db.NewUpdate().Model(model).
			Column(indexerWriteColumns...).
			Where("id = ?", c.ID).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("store: update indexer %d: %w", c.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: update indexer %d: %w", c.ID, err)
		}
		if n == 0 {
			return fmt.Errorf("store: update indexer %d: %w", c.ID, ErrNotFound)
		}
		return nil
	}

	model.CreatedAt = ts
	if _, err := s.db.NewInsert().Model(model).Exec(ctx); err != nil {
		return fmt.Errorf("store: insert indexer %q: %w", c.Name, err)
	}
	c.ID = model.ID
	return nil
}

// GetIndexer returns the indexer with the given id, or ErrNotFound.
func (s *Store) GetIndexer(ctx context.Context, id int64) (*core.IndexerConfig, error) {
	var model indexerModel
	err := s.db.NewSelect().Model(&model).
		Column(indexerReadColumns...).
		Where("id = ?", id).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: indexer %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get indexer %d: %w", id, err)
	}
	indexer, err := model.coreConfig()
	if err != nil {
		return nil, fmt.Errorf("store: get indexer %d: %w", id, err)
	}
	return &indexer, nil
}

// ListIndexers returns every configured indexer in search order.
func (s *Store) ListIndexers(ctx context.Context) ([]core.IndexerConfig, error) {
	return s.listIndexers(ctx, false)
}

// ListEnabledIndexers returns only the indexers search fans out to, in search
// order. A disabled indexer keeps its configuration but is skipped.
func (s *Store) ListEnabledIndexers(ctx context.Context) ([]core.IndexerConfig, error) {
	return s.listIndexers(ctx, true)
}

func (s *Store) listIndexers(ctx context.Context, enabledOnly bool) ([]core.IndexerConfig, error) {
	models := make([]indexerModel, 0)
	query := s.db.NewSelect().Model(&models).
		Column(indexerReadColumns...).
		Order("priority ASC", "name ASC")
	if enabledOnly {
		query = query.Where("enabled = ?", true)
	}
	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list indexers: %w", err)
	}

	out := make([]core.IndexerConfig, 0, len(models))
	for i := range models {
		indexer, err := models[i].coreConfig()
		if err != nil {
			return nil, fmt.Errorf("store: scan indexer: %w", err)
		}
		out = append(out, indexer)
	}
	return out, nil
}

// DeleteIndexer removes the indexer row. Cached releases keep their
// indexer_id and denormalized name: the reference is soft, so a deleted
// indexer never invalidates history.
func (s *Store) DeleteIndexer(ctx context.Context, id int64) error {
	if _, err := s.db.NewDelete().Model((*indexerModel)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("store: delete indexer %d: %w", id, err)
	}
	return nil
}

func indexerModelFromCore(c *core.IndexerConfig, categories string) *indexerModel {
	return &indexerModel{
		ID:         c.ID,
		Name:       c.Name,
		URL:        c.URL,
		APIKey:     c.APIKey,
		Protocol:   c.Type,
		Categories: categories,
		Priority:   c.Priority,
		Enabled:    c.Enabled,
	}
}

func (m *indexerModel) coreConfig() (core.IndexerConfig, error) {
	indexer := core.IndexerConfig{
		ID:       m.ID,
		Name:     m.Name,
		URL:      m.URL,
		APIKey:   m.APIKey,
		Type:     m.Protocol,
		Priority: m.Priority,
		Enabled:  m.Enabled,
	}
	if m.Categories != "" {
		if err := json.Unmarshal([]byte(m.Categories), &indexer.Categories); err != nil {
			return core.IndexerConfig{}, fmt.Errorf("decode categories of indexer %q: %w", m.Name, err)
		}
	}
	return indexer, nil
}
