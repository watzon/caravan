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
	// SettingAPIKey is Caravan's own API credential, used by endpoints an
	// external app subscribes to (the iCal feed, PLAN phase 3 task 9). It is
	// generated from the settings screen, never hand-written.
	SettingAPIKey = "api_key"
	// SettingRSSSyncIntervalMinutes is how often enabled indexers are polled
	// for new releases (PLAN phase 3, task 4).
	SettingRSSSyncIntervalMinutes = "rss_sync_interval_minutes"
	// SettingBacklogIntervalMinutes is how often the wanted list is swept for
	// items that need a backlog search (PLAN phase 3, task 4).
	SettingBacklogIntervalMinutes = "backlog_interval_minutes"
	// SettingEngineListenPort is the port the embedded engine binds. Zero lets
	// the torrent library choose its default.
	SettingEngineListenPort = "engine_listen_port"
	// SettingEngineMaxConnections is the per-torrent connection cap. Zero uses
	// the torrent library default.
	SettingEngineMaxConnections = "engine_max_connections"
	// SettingEngineMaxDownKBps and SettingEngineMaxUpKBps are global rate
	// limits in KB/s. Zero means unlimited.
	SettingEngineMaxDownKBps = "engine_max_down_kbps"
	SettingEngineMaxUpKBps   = "engine_max_up_kbps"
	// SettingEngineSeedRatio and SettingEngineSeedDays stop seeding once either
	// configured target is reached. Zero disables that target.
	SettingEngineSeedRatio = "engine_seed_ratio"
	SettingEngineSeedDays  = "engine_seed_days"
	// SettingTVProfile is the id of the active core.TVProfile — the target set
	// releases and imported files are described against (SPEC §8, PLAN phase 4
	// task 3). Unset resolves to the safe default, so this key is a preference
	// and never a required row.
	SettingTVProfile = "tv_profile"
	// SettingJellyfinURL, SettingJellyfinAPIKey and SettingJellyfinEnabled
	// configure the playback handoff (SPEC §5.2, PLAN phase 4 task 1): where
	// the user's Jellyfin lives, the API key created in its dashboard, and
	// whether an import is allowed to tell it to rescan. Enabled is stored as
	// "true"/"false"; anything else reads as off.
	SettingJellyfinURL     = "jellyfin_url"
	SettingJellyfinAPIKey  = "jellyfin_api_key"
	SettingJellyfinEnabled = "jellyfin_enabled"
	// SettingDLNAEnabled, SettingDLNAFriendlyName and SettingDLNAUUID configure
	// the built-in DLNA media server (SPEC §5.1, PLAN phase 4 task 2). Enabled
	// is stored as "true"/"false" and defaults to ON when the key has never been
	// written, because SPEC's promise is that the library is advertised whenever
	// the server runs. The UUID is generated on first advertisement and kept so
	// clients see the same device across restarts; losing it costs a
	// re-discovery, never media, which is why it is allowed to live in the
	// disposable database.
	SettingDLNAEnabled      = "dlna_enabled"
	SettingDLNAFriendlyName = "dlna_friendly_name"
	SettingDLNAUUID         = "dlna_uuid"
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
