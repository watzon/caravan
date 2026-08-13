package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

var downloadClientReadColumns = []string{
	"id", "kind", "name", "url", "username", "password", "api_key",
	"category", "priority", "enabled", "max_concurrent",
}

var downloadClientWriteColumns = []string{
	"kind", "name", "url", "username", "password", "api_key", "category",
	"priority", "enabled", "max_concurrent", "updated_at",
}

// UpsertDownloadClient inserts or updates c and writes back the assigned ID.
// Identity is c.ID when set; otherwise a new client is inserted.
func (s *Store) UpsertDownloadClient(ctx context.Context, c *core.DownloadClientConfig) error {
	ts := formatTime(now())
	model := downloadClientModelFromCore(c)
	model.UpdatedAt = ts

	if c.ID != 0 {
		res, err := s.db.NewUpdate().Model(model).
			Column(downloadClientWriteColumns...).
			Where("id = ?", c.ID).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("store: update download client %d: %w", c.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: update download client %d: %w", c.ID, err)
		}
		if n == 0 {
			return fmt.Errorf("store: update download client %d: %w", c.ID, ErrNotFound)
		}
		return nil
	}

	model.CreatedAt = ts
	if _, err := s.db.NewInsert().Model(model).Exec(ctx); err != nil {
		return fmt.Errorf("store: insert download client %q: %w", c.Name, err)
	}
	c.ID = model.ID
	return nil
}

// GetDownloadClient returns the client with the given id, or ErrNotFound.
func (s *Store) GetDownloadClient(ctx context.Context, id int64) (*core.DownloadClientConfig, error) {
	var model downloadClientModel
	err := s.db.NewSelect().Model(&model).
		Column(downloadClientReadColumns...).
		Where("id = ?", id).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: download client %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get download client %d: %w", id, err)
	}
	client := model.coreConfig()
	return &client, nil
}

// ListDownloadClients returns every configured client ordered by name, which
// is the order the settings screen renders.
func (s *Store) ListDownloadClients(ctx context.Context) ([]core.DownloadClientConfig, error) {
	return s.listDownloadClients(ctx, false)
}

// ListEnabledDownloadClients returns only the clients a grab may be routed to,
// best candidate first: lowest priority wins, name breaks the tie so the order
// is stable. A disabled client keeps its configuration but is skipped.
func (s *Store) ListEnabledDownloadClients(ctx context.Context) ([]core.DownloadClientConfig, error) {
	return s.listDownloadClients(ctx, true)
}

func (s *Store) listDownloadClients(ctx context.Context, enabledOnly bool) ([]core.DownloadClientConfig, error) {
	models := make([]downloadClientModel, 0)
	query := s.db.NewSelect().Model(&models).Column(downloadClientReadColumns...)
	if enabledOnly {
		query = query.Where("enabled = ?", true).Order("priority ASC", "name ASC")
	} else {
		query = query.Order("name ASC")
	}
	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list download clients: %w", err)
	}

	out := make([]core.DownloadClientConfig, 0, len(models))
	for i := range models {
		out = append(out, models[i].coreConfig())
	}
	return out, nil
}

// DeleteDownloadClient removes the configuration row. Downloads it started
// keep their engine name and engine id, so history survives the delete for the
// same reason a deleted indexer never erases where a release came from.
func (s *Store) DeleteDownloadClient(ctx context.Context, id int64) error {
	if _, err := s.db.NewDelete().Model((*downloadClientModel)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("store: delete download client %d: %w", id, err)
	}
	return nil
}

func downloadClientModelFromCore(c *core.DownloadClientConfig) *downloadClientModel {
	return &downloadClientModel{
		ID:            c.ID,
		Kind:          c.Type,
		Name:          c.Name,
		URL:           c.URL,
		Username:      c.Username,
		Password:      c.Password,
		APIKey:        c.APIKey,
		Category:      c.Category,
		Priority:      c.Priority,
		Enabled:       c.Enabled,
		MaxConcurrent: c.MaxConcurrent,
	}
}

func (m *downloadClientModel) coreConfig() core.DownloadClientConfig {
	return core.DownloadClientConfig{
		ID:            m.ID,
		Type:          m.Kind,
		Name:          m.Name,
		URL:           m.URL,
		Username:      m.Username,
		Password:      m.Password,
		APIKey:        m.APIKey,
		Category:      m.Category,
		Priority:      m.Priority,
		Enabled:       m.Enabled,
		MaxConcurrent: m.MaxConcurrent,
	}
}
