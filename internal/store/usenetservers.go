package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

var usenetServerReadColumns = []string{
	"id", "name", "host", "port", "tls", "username", "password",
	"max_connections", "priority", "enabled",
}

var usenetServerWriteColumns = []string{
	"name", "host", "port", "tls", "username", "password", "max_connections",
	"priority", "enabled", "updated_at",
}

// UpsertUsenetServer inserts or updates s and writes back the assigned ID.
// Identity is s.ID when set; otherwise a new server is inserted.
func (s *Store) UpsertUsenetServer(ctx context.Context, srv *core.UsenetServerConfig) error {
	ts := formatTime(now())
	model := usenetServerModelFromCore(srv)
	model.UpdatedAt = ts

	if srv.ID != 0 {
		res, err := s.db.NewUpdate().Model(model).
			Column(usenetServerWriteColumns...).
			Where("id = ?", srv.ID).
			Exec(ctx)
		if err != nil {
			return fmt.Errorf("store: update usenet server %d: %w", srv.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: update usenet server %d: %w", srv.ID, err)
		}
		if n == 0 {
			return fmt.Errorf("store: update usenet server %d: %w", srv.ID, ErrNotFound)
		}
		return nil
	}

	model.CreatedAt = ts
	if _, err := s.db.NewInsert().Model(model).Exec(ctx); err != nil {
		return fmt.Errorf("store: insert usenet server %q: %w", srv.Name, err)
	}
	srv.ID = model.ID
	return nil
}

// GetUsenetServer returns the server with the given id, or ErrNotFound.
func (s *Store) GetUsenetServer(ctx context.Context, id int64) (*core.UsenetServerConfig, error) {
	var model usenetServerModel
	err := s.db.NewSelect().Model(&model).
		Column(usenetServerReadColumns...).
		Where("id = ?", id).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: usenet server %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get usenet server %d: %w", id, err)
	}
	server := model.coreConfig()
	return &server, nil
}

// ListUsenetServers returns every configured server ordered by name, which is
// the order the settings screen renders.
func (s *Store) ListUsenetServers(ctx context.Context) ([]core.UsenetServerConfig, error) {
	return s.listUsenetServers(ctx, false)
}

// ListEnabledUsenetServers returns only the servers the engine may fetch from,
// best candidate first: lowest priority wins, name breaks the tie so the order
// is stable. This is the order nntp.NewMultiPool fails over in, so it is part
// of the contract rather than a display detail.
func (s *Store) ListEnabledUsenetServers(ctx context.Context) ([]core.UsenetServerConfig, error) {
	return s.listUsenetServers(ctx, true)
}

func (s *Store) listUsenetServers(ctx context.Context, enabledOnly bool) ([]core.UsenetServerConfig, error) {
	models := make([]usenetServerModel, 0)
	query := s.db.NewSelect().Model(&models).Column(usenetServerReadColumns...)
	if enabledOnly {
		query = query.Where("enabled = ?", true).Order("priority ASC", "name ASC")
	} else {
		query = query.Order("name ASC")
	}
	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list usenet servers: %w", err)
	}

	out := make([]core.UsenetServerConfig, 0, len(models))
	for i := range models {
		out = append(out, models[i].coreConfig())
	}
	return out, nil
}

// DeleteUsenetServer removes the configuration row. Downloads that already
// pulled articles from it keep their history, for the same reason a deleted
// indexer never erases where a release came from.
func (s *Store) DeleteUsenetServer(ctx context.Context, id int64) error {
	if _, err := s.db.NewDelete().Model((*usenetServerModel)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("store: delete usenet server %d: %w", id, err)
	}
	return nil
}

func usenetServerModelFromCore(srv *core.UsenetServerConfig) *usenetServerModel {
	return &usenetServerModel{
		ID:             srv.ID,
		Name:           srv.Name,
		Host:           srv.Host,
		Port:           srv.Port,
		TLS:            srv.TLS,
		Username:       srv.Username,
		Password:       srv.Password,
		MaxConnections: srv.MaxConnections,
		Priority:       srv.Priority,
		Enabled:        srv.Enabled,
	}
}

func (m *usenetServerModel) coreConfig() core.UsenetServerConfig {
	return core.UsenetServerConfig{
		ID:             m.ID,
		Name:           m.Name,
		Host:           m.Host,
		Port:           m.Port,
		TLS:            m.TLS,
		Username:       m.Username,
		Password:       m.Password,
		MaxConnections: m.MaxConnections,
		Priority:       m.Priority,
		Enabled:        m.Enabled,
	}
}
