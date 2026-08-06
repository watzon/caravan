package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

// The `protocol` column holds core.IndexerConfig.Type: 0001 named it for the
// wire dialect (torznab/newznab), which is exactly what Type selects.
const indexerColumns = `id, name, url, api_key, protocol, categories, priority, enabled`

// UpsertIndexer inserts or updates c and writes back the assigned ID.
// Identity is c.ID when set; otherwise a new indexer is inserted.
func (s *Store) UpsertIndexer(ctx context.Context, c *core.IndexerConfig) error {
	categories, err := json.Marshal(c.Categories)
	if err != nil {
		return fmt.Errorf("store: encode categories of indexer %q: %w", c.Name, err)
	}
	ts := formatTime(now())

	if c.ID != 0 {
		res, err := s.db.ExecContext(ctx, `
			UPDATE indexers SET name = ?, url = ?, api_key = ?, protocol = ?,
				categories = ?, priority = ?, enabled = ?, updated_at = ?
			WHERE id = ?`,
			c.Name, c.URL, c.APIKey, c.Type, string(categories), c.Priority, c.Enabled, ts, c.ID)
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

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO indexers (name, url, api_key, protocol, categories, priority, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.Name, c.URL, c.APIKey, c.Type, string(categories), c.Priority, c.Enabled, ts, ts)
	if err != nil {
		return fmt.Errorf("store: insert indexer %q: %w", c.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: insert indexer %q: %w", c.Name, err)
	}
	c.ID = id
	return nil
}

// GetIndexer returns the indexer with the given id, or ErrNotFound.
func (s *Store) GetIndexer(ctx context.Context, id int64) (*core.IndexerConfig, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+indexerColumns+" FROM indexers WHERE id = ?", id)
	c, err := scanIndexer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: indexer %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get indexer %d: %w", id, err)
	}
	return c, nil
}

// ListIndexers returns every configured indexer in search order.
func (s *Store) ListIndexers(ctx context.Context) ([]core.IndexerConfig, error) {
	return s.listIndexers(ctx, "SELECT "+indexerColumns+" FROM indexers ORDER BY priority, name")
}

// ListEnabledIndexers returns only the indexers search fans out to, in search
// order. A disabled indexer keeps its configuration but is skipped.
func (s *Store) ListEnabledIndexers(ctx context.Context) ([]core.IndexerConfig, error) {
	return s.listIndexers(ctx, "SELECT "+indexerColumns+" FROM indexers WHERE enabled = 1 ORDER BY priority, name")
}

func (s *Store) listIndexers(ctx context.Context, query string) ([]core.IndexerConfig, error) {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list indexers: %w", err)
	}
	defer rows.Close()

	out := []core.IndexerConfig{}
	for rows.Next() {
		c, err := scanIndexer(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan indexer: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list indexers: %w", err)
	}
	return out, nil
}

// DeleteIndexer removes the indexer row. Cached releases keep their
// indexer_id and denormalized name: the reference is soft, so a deleted
// indexer never invalidates history.
func (s *Store) DeleteIndexer(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM indexers WHERE id = ?", id); err != nil {
		return fmt.Errorf("store: delete indexer %d: %w", id, err)
	}
	return nil
}

func scanIndexer(sc scanner) (*core.IndexerConfig, error) {
	var (
		c          core.IndexerConfig
		categories string
	)
	if err := sc.Scan(&c.ID, &c.Name, &c.URL, &c.APIKey, &c.Type, &categories, &c.Priority, &c.Enabled); err != nil {
		return nil, err
	}
	if categories != "" {
		if err := json.Unmarshal([]byte(categories), &c.Categories); err != nil {
			return nil, fmt.Errorf("decode categories of indexer %q: %w", c.Name, err)
		}
	}
	return &c, nil
}
