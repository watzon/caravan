package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Setting keys owned by the application. Everything the UI manages lives in
// this table (SPEC §10); the bootstrap YAML only covers what is needed before
// the database exists.
const (
	// SettingStorageRoot is the absolute path every stored path is relative
	// to. This is the one absolute path Caravan persists, and it lives here
	// rather than on a row so re-pointing the root is a single update
	// (SPEC §10).
	SettingStorageRoot = "storage_root"
	// SettingTMDBAPIKey is the metadata provider credential.
	SettingTMDBAPIKey = "tmdb_api_key"
)

// GetSetting returns the value for key, or ErrNotFound when the key has never
// been set.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("store: setting %q: %w", key, ErrNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("store: get setting %q: %w", key, err)
	}
	return value, nil
}

// SetSetting writes key, overwriting any previous value.
func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		key, value, formatTime(now()))
	if err != nil {
		return fmt.Errorf("store: set setting %q: %w", key, err)
	}
	return nil
}

// DeleteSetting removes key. Deleting an absent key is not an error.
func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM settings WHERE key = ?", key); err != nil {
		return fmt.Errorf("store: delete setting %q: %w", key, err)
	}
	return nil
}

// AllSettings returns every setting. The map is empty, never nil, on a fresh
// database.
func (s *Store) AllSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM settings ORDER BY key")
	if err != nil {
		return nil, fmt.Errorf("store: list settings: %w", err)
	}
	defer rows.Close()

	out := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, fmt.Errorf("store: scan setting: %w", err)
		}
		out[key] = value
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list settings: %w", err)
	}
	return out, nil
}
