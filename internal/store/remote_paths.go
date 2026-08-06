package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const remotePathMappingColumns = `id, remote_path, local_path, match_count, last_matched_at, created_at, updated_at`

// CreateRemotePathMapping inserts m and writes back its assigned ID and times.
func (s *Store) CreateRemotePathMapping(ctx context.Context, m *core.RemotePathMapping) error {
	ts := formatTime(now())
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO remote_path_mappings (remote_path, local_path, created_at, updated_at)
		VALUES (?, ?, ?, ?)`, m.RemotePath, m.LocalPath, ts, ts)
	if err != nil {
		return fmt.Errorf("store: create remote path mapping %q: %w", m.RemotePath, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: create remote path mapping %q: %w", m.RemotePath, err)
	}
	m.ID = id
	m.CreatedAt = parseTime(ts)
	m.UpdatedAt = m.CreatedAt
	return nil
}

// UpdateRemotePathMapping replaces the paths for an existing mapping.
func (s *Store) UpdateRemotePathMapping(ctx context.Context, m *core.RemotePathMapping) error {
	ts := formatTime(now())
	res, err := s.db.ExecContext(ctx, `
		UPDATE remote_path_mappings SET remote_path = ?, local_path = ?, updated_at = ?
		WHERE id = ?`, m.RemotePath, m.LocalPath, ts, m.ID)
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
	m, err := scanRemotePathMapping(s.db.QueryRowContext(ctx,
		"SELECT "+remotePathMappingColumns+" FROM remote_path_mappings WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: remote path mapping %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get remote path mapping %d: %w", id, err)
	}
	return m, nil
}

// ListRemotePathMappings returns every mapping in stable remote-path order.
func (s *Store) ListRemotePathMappings(ctx context.Context) ([]core.RemotePathMapping, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+remotePathMappingColumns+" FROM remote_path_mappings ORDER BY remote_path COLLATE NOCASE, id")
	if err != nil {
		return nil, fmt.Errorf("store: list remote path mappings: %w", err)
	}
	defer rows.Close()

	out := []core.RemotePathMapping{}
	for rows.Next() {
		m, err := scanRemotePathMapping(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan remote path mapping: %w", err)
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list remote path mappings: %w", err)
	}
	return out, nil
}

// RecordRemotePathMappingMatch records one successful translation. It runs
// only when an import actually selects the mapping, so the settings screen can
// distinguish configured roots from roots that Caravan has observed in use.
func (s *Store) RecordRemotePathMappingMatch(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE remote_path_mappings
		SET match_count = match_count + 1, last_matched_at = ?
		WHERE id = ?`, formatTime(now()), id)
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
	if _, err := s.db.ExecContext(ctx, "DELETE FROM remote_path_mappings WHERE id = ?", id); err != nil {
		return fmt.Errorf("store: delete remote path mapping %d: %w", id, err)
	}
	return nil
}

func scanRemotePathMapping(sc scanner) (*core.RemotePathMapping, error) {
	var m core.RemotePathMapping
	var lastMatchedAt sql.NullString
	var createdAt, updatedAt string
	if err := sc.Scan(
		&m.ID, &m.RemotePath, &m.LocalPath, &m.MatchCount, &lastMatchedAt, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	if lastMatchedAt.Valid {
		m.LastMatchedAt = parseTime(lastMatchedAt.String)
	}
	m.CreatedAt = parseTime(createdAt)
	m.UpdatedAt = parseTime(updatedAt)
	return &m, nil
}
