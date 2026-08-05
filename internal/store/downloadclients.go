package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

// The `kind` column holds core.DownloadClientConfig.Type: 0001 named it, and
// it is exactly what Type selects.
const downloadClientColumns = `id, kind, name, url, username, password, api_key,
	category, priority, enabled, max_concurrent`

// UpsertDownloadClient inserts or updates c and writes back the assigned ID.
// Identity is c.ID when set; otherwise a new client is inserted.
func (s *Store) UpsertDownloadClient(ctx context.Context, c *core.DownloadClientConfig) error {
	ts := formatTime(now())

	if c.ID != 0 {
		res, err := s.db.ExecContext(ctx, `
			UPDATE download_clients SET kind = ?, name = ?, url = ?, username = ?,
				password = ?, api_key = ?, category = ?, priority = ?, enabled = ?,
				max_concurrent = ?, updated_at = ?
			WHERE id = ?`,
			c.Type, c.Name, c.URL, c.Username, c.Password, c.APIKey,
			c.Category, c.Priority, c.Enabled, c.MaxConcurrent, ts, c.ID)
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

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO download_clients (kind, name, url, username, password, api_key,
			category, priority, enabled, max_concurrent, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Type, c.Name, c.URL, c.Username, c.Password, c.APIKey,
		c.Category, c.Priority, c.Enabled, c.MaxConcurrent, ts, ts)
	if err != nil {
		return fmt.Errorf("store: insert download client %q: %w", c.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: insert download client %q: %w", c.Name, err)
	}
	c.ID = id
	return nil
}

// GetDownloadClient returns the client with the given id, or ErrNotFound.
func (s *Store) GetDownloadClient(ctx context.Context, id int64) (*core.DownloadClientConfig, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+downloadClientColumns+" FROM download_clients WHERE id = ?", id)
	c, err := scanDownloadClient(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: download client %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get download client %d: %w", id, err)
	}
	return c, nil
}

// ListDownloadClients returns every configured client ordered by name, which
// is the order the settings screen renders.
func (s *Store) ListDownloadClients(ctx context.Context) ([]core.DownloadClientConfig, error) {
	return s.listDownloadClients(ctx,
		"SELECT "+downloadClientColumns+" FROM download_clients ORDER BY name")
}

// ListEnabledDownloadClients returns only the clients a grab may be routed to,
// best candidate first: lowest priority wins, name breaks the tie so the order
// is stable. A disabled client keeps its configuration but is skipped.
func (s *Store) ListEnabledDownloadClients(ctx context.Context) ([]core.DownloadClientConfig, error) {
	return s.listDownloadClients(ctx,
		"SELECT "+downloadClientColumns+" FROM download_clients WHERE enabled = 1 ORDER BY priority, name")
}

func (s *Store) listDownloadClients(ctx context.Context, query string) ([]core.DownloadClientConfig, error) {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list download clients: %w", err)
	}
	defer rows.Close()

	out := []core.DownloadClientConfig{}
	for rows.Next() {
		c, err := scanDownloadClient(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan download client: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list download clients: %w", err)
	}
	return out, nil
}

// DeleteDownloadClient removes the configuration row. Downloads it started
// keep their engine name and engine id, so history survives the delete for the
// same reason a deleted indexer never erases where a release came from.
func (s *Store) DeleteDownloadClient(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM download_clients WHERE id = ?", id); err != nil {
		return fmt.Errorf("store: delete download client %d: %w", id, err)
	}
	return nil
}

func scanDownloadClient(sc scanner) (*core.DownloadClientConfig, error) {
	var c core.DownloadClientConfig
	if err := sc.Scan(&c.ID, &c.Type, &c.Name, &c.URL, &c.Username, &c.Password,
		&c.APIKey, &c.Category, &c.Priority, &c.Enabled, &c.MaxConcurrent); err != nil {
		return nil, err
	}
	return &c, nil
}
