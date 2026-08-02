package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const usenetServerColumns = `id, name, host, port, tls, username, password,
	max_connections, priority, enabled`

// UpsertUsenetServer inserts or updates s and writes back the assigned ID.
// Identity is s.ID when set; otherwise a new server is inserted.
func (s *Store) UpsertUsenetServer(ctx context.Context, srv *core.UsenetServerConfig) error {
	ts := formatTime(now())

	if srv.ID != 0 {
		res, err := s.db.ExecContext(ctx, `
			UPDATE usenet_servers SET name = ?, host = ?, port = ?, tls = ?,
				username = ?, password = ?, max_connections = ?, priority = ?,
				enabled = ?, updated_at = ?
			WHERE id = ?`,
			srv.Name, srv.Host, srv.Port, srv.TLS, srv.Username, srv.Password,
			srv.MaxConnections, srv.Priority, srv.Enabled, ts, srv.ID)
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

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO usenet_servers (name, host, port, tls, username, password,
			max_connections, priority, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		srv.Name, srv.Host, srv.Port, srv.TLS, srv.Username, srv.Password,
		srv.MaxConnections, srv.Priority, srv.Enabled, ts, ts)
	if err != nil {
		return fmt.Errorf("store: insert usenet server %q: %w", srv.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: insert usenet server %q: %w", srv.Name, err)
	}
	srv.ID = id
	return nil
}

// GetUsenetServer returns the server with the given id, or ErrNotFound.
func (s *Store) GetUsenetServer(ctx context.Context, id int64) (*core.UsenetServerConfig, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+usenetServerColumns+" FROM usenet_servers WHERE id = ?", id)
	srv, err := scanUsenetServer(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: usenet server %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get usenet server %d: %w", id, err)
	}
	return srv, nil
}

// ListUsenetServers returns every configured server ordered by name, which is
// the order the settings screen renders.
func (s *Store) ListUsenetServers(ctx context.Context) ([]core.UsenetServerConfig, error) {
	return s.listUsenetServers(ctx,
		"SELECT "+usenetServerColumns+" FROM usenet_servers ORDER BY name")
}

// ListEnabledUsenetServers returns only the servers the engine may fetch from,
// best candidate first: lowest priority wins, name breaks the tie so the order
// is stable. This is the order nntp.NewMultiPool fails over in, so it is part
// of the contract rather than a display detail.
func (s *Store) ListEnabledUsenetServers(ctx context.Context) ([]core.UsenetServerConfig, error) {
	return s.listUsenetServers(ctx,
		"SELECT "+usenetServerColumns+" FROM usenet_servers WHERE enabled = 1 ORDER BY priority, name")
}

func (s *Store) listUsenetServers(ctx context.Context, query string) ([]core.UsenetServerConfig, error) {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list usenet servers: %w", err)
	}
	defer rows.Close()

	out := []core.UsenetServerConfig{}
	for rows.Next() {
		srv, err := scanUsenetServer(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan usenet server: %w", err)
		}
		out = append(out, *srv)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list usenet servers: %w", err)
	}
	return out, nil
}

// DeleteUsenetServer removes the configuration row. Downloads that already
// pulled articles from it keep their history, for the same reason a deleted
// indexer never erases where a release came from.
func (s *Store) DeleteUsenetServer(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM usenet_servers WHERE id = ?", id); err != nil {
		return fmt.Errorf("store: delete usenet server %d: %w", id, err)
	}
	return nil
}

func scanUsenetServer(sc scanner) (*core.UsenetServerConfig, error) {
	var srv core.UsenetServerConfig
	if err := sc.Scan(&srv.ID, &srv.Name, &srv.Host, &srv.Port, &srv.TLS,
		&srv.Username, &srv.Password, &srv.MaxConnections, &srv.Priority,
		&srv.Enabled); err != nil {
		return nil, err
	}
	return &srv, nil
}
