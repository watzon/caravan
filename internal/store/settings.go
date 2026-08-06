package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
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
	// SettingRecycleRetentionDays keeps deleted Caravan-owned library files in
	// recycle batches for this many days. Zero preserves permanent deletion.
	SettingRecycleRetentionDays = "recycle_retention_days"
	// SettingRecycleCleanupIntervalMinutes controls the recurring cleanup job.
	SettingRecycleCleanupIntervalMinutes = "recycle_cleanup_interval_minutes"
	// SettingNotificationIntervalMinutes controls webhook delivery cadence.
	SettingNotificationIntervalMinutes = "notification_interval_minutes"
	// Naming settings describe the generated Jellyfin-compatible paths. Unset
	// values use the built-in formats so existing installations keep their
	// exact paths.
	SettingMovieFolderFormat  = "movie_folder_format"
	SettingMovieFileFormat    = "movie_file_format"
	SettingSeriesFolderFormat = "series_folder_format"
	SettingSeasonFolderFormat = "season_folder_format"
	SettingEpisodeFileFormat  = "episode_file_format"
	// SettingTMDBAPIKey is the metadata provider credential.
	SettingTMDBAPIKey = "tmdb_api_key"
	// SettingStashboxEndpoint and SettingStashboxAPIKey configure the adult
	// library's metadata provider (PLAN phase 9 task 1). "stash-box" is a
	// protocol rather than a service, so the endpoint is a value: TPDB is the
	// preset (stashbox.DefaultEndpoint), and StashDB, FansDB or a self-hosted
	// box are the same code with a different URL. An unset endpoint means the
	// preset, which is why "just paste a key" is the whole configuration.
	//
	// Neither key does anything on its own. The adult module is gated by its
	// own enable flag and a per-user grant, and nothing reads these until both
	// are satisfied — a stored endpoint is not a reason to talk to it.
	SettingStashboxEndpoint = "stashbox_endpoint"
	SettingStashboxAPIKey   = "stashbox_api_key"
	// SettingAdultEnabled is the server-wide switch for the adult module (PLAN
	// phase 9 task 5). Stored as "true"/"false"; absent and unparseable both
	// read as OFF, which is the opposite default to SettingDLNAEnabled and for
	// the opposite reason — a typo must never be what turns this on.
	//
	// It is deliberately NOT in the PUT /settings allowlist. Flipping it has
	// consequences a key-value write cannot carry out (the Adult library row is
	// created on first enable), so it has its own endpoint, exactly as
	// SettingStorageRoot does. Read it through AdultEnabled, never by hand.
	SettingAdultEnabled = "adult_enabled"
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
	// SettingDefaultQualityProfileID is the id of the profile used when neither
	// an item nor its library selects one. It is persisted rather than inferred
	// from profile order so changing the system default is explicit and stable.
	SettingDefaultQualityProfileID = "default_quality_profile_id"
	// SettingRefreshIntervalMinutes is how often provider metadata is
	// re-fetched for every monitored title (core.JobRefreshMetadata).
	SettingRefreshIntervalMinutes = "refresh_interval_minutes"
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
	// SettingMaxConcurrentDownloads is the ceiling on downloads transferring at
	// once across every engine and client together. Zero is unlimited, which is
	// what Caravan did before there were caps at all: ten grabs all started at
	// once and starved each other.
	//
	// A download over the ceiling is not refused and not paused — it waits in
	// the queue's existing `queued` state until a slot frees, oldest first.
	SettingMaxConcurrentDownloads = "max_concurrent_downloads"
	// SettingEmbeddedTorrentMaxConcurrent and SettingEmbeddedUsenetMaxConcurrent
	// are the per-method ceilings for the two built-in engines. Zero is
	// unlimited for that method; the global ceiling still applies.
	//
	// The Usenet one deserves a small number. Parallel NZBs share one pool of
	// connections to the same news servers, so a second simultaneous download
	// does not make articles arrive faster — it splits the same bandwidth and
	// doubles how long it takes either release to become importable.
	SettingEmbeddedTorrentMaxConcurrent = "embedded_torrent_max_concurrent"
	SettingEmbeddedUsenetMaxConcurrent  = "embedded_usenet_max_concurrent"
	// SettingRouteTorrent and SettingRouteUsenet name the default download
	// engine per release protocol (SPEC §5.1, PLAN phase 6 task 3). A grab is
	// routed on the release's protocol, never on the user's last choice, so
	// these are the whole routing configuration.
	//
	// The value is a `download_clients.id` in decimal, or RouteEmbedded for
	// the built-in torrent engine. Unset means RouteEmbedded for torrents —
	// a stock Caravan downloads torrents with nothing configured — and means
	// "nothing configured" for usenet, where there is no built-in engine and
	// a grab is therefore a recorded rejection rather than a misroute.
	SettingRouteTorrent = "route_torrent"
	SettingRouteUsenet  = "route_usenet"
	// SettingTVProfile is the id of the active core.TVProfile — the target set
	// releases and imported files are described against (SPEC §8, PLAN phase 4
	// task 3). Unset resolves to the safe default, so this key is a preference
	// and never a required row.
	SettingTVProfile = "tv_profile"
	// SettingConvertVideoPreset, SettingConvertVideoCRF and
	// SettingConvertAudioBitrateKbps control how ffmpeg produces output when
	// the active TV profile requires a re-encode. They do not change the
	// profile's compatibility target, and they have code defaults so a fresh
	// install keeps the original veryfast/CRF 20/192 kbps behaviour.
	SettingConvertVideoPreset      = "convert_video_preset"
	SettingConvertVideoCRF         = "convert_video_crf"
	SettingConvertAudioBitrateKbps = "convert_audio_bitrate_kbps"
	// SettingJellyfinURL, SettingJellyfinAPIKey and SettingJellyfinEnabled
	// configure the playback handoff (SPEC §5.2, PLAN phase 4 task 1): where
	// the user's Jellyfin lives, the API key created in its dashboard, and
	// whether an import is allowed to tell it to rescan. Enabled is stored as
	// "true"/"false"; anything else reads as off.
	SettingJellyfinURL     = "jellyfin_url"
	SettingJellyfinAPIKey  = "jellyfin_api_key"
	SettingJellyfinEnabled = "jellyfin_enabled"
	// SettingStashURL, SettingStashAPIKey and SettingStashEnabled configure the
	// adult library's handoff (PLAN phase 11): where the user's Stash lives, the
	// API key from its Security screen, and whether an adult import is allowed
	// to tell it to rescan. They are the Jellyfin keys' adult twin, stored the
	// same way — Enabled is "true"/"false" and anything else reads as off.
	//
	// Like the stash-box credential, none of them does anything on its own: the
	// handoff also requires SettingAdultEnabled, so a stored Stash address is
	// not a reason to talk to one. They are deliberately absent from the PUT
	// /settings allowlist and from GET /settings for a caller the adult module
	// is not visible to; POST /adult/stash is the only door.
	SettingStashURL     = "stash_url"
	SettingStashAPIKey  = "stash_api_key"
	SettingStashEnabled = "stash_enabled"
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
	// SettingDLNAUpdateID is the ContentDirectory's SystemUpdateID: the version
	// of the content tree, which clients cache against. It is written only by
	// bumpDLNAUpdateID and is deliberately not in the PUT /settings allowlist —
	// a counter a client trusts to mean "something changed" is not a preference.
	// Absent means 1, so an install nobody has reconfigured reports the value it
	// always did.
	SettingDLNAUpdateID = "dlna_update_id"
	// SettingPasswordHash was the optional single-user password, an argon2id
	// PHC string (SPEC §11, PLAN phase 5 task 5). Migration 0011 folded it into
	// an 'admin' row in the users table and deleted the setting, so nothing
	// writes it any more.
	//
	// The name survives because the API still refuses to read or write it: it
	// stays out of the PUT /settings allowlist and inside hiddenSettings, so a
	// database that somehow still carries the row can neither serve the hash
	// nor have one planted (SPEC §12 — credentials never leave the server).
	SettingPasswordHash = "password_hash"
)

// RouteEmbedded is the SettingRouteTorrent value that selects Caravan's
// built-in torrent engine instead of an external client. It is the string
// those downloads record in `downloads.engine` (download.EngineName), so a
// routing setting and a download row name the same engine the same way.
const RouteEmbedded = "embedded"

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

// SetSettings writes every pair or none of them.
//
// It exists for the settings that are only meaningful together — the stash-box
// endpoint and its API key are one credential, and committing half of a new
// pair leaves a combination nothing ever validated behind a module that is
// already on (SPEC §10.2). Callers writing independent keys should keep using
// SetSetting; this is for the ones where a partial write is a wrong answer
// rather than an incomplete one.
func (s *Store) SetSettings(ctx context.Context, values map[string]string) error {
	if len(values) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: set settings: %w", err)
	}
	defer tx.Rollback()

	// Sorted so two concurrent writers touching the same keys take them in the
	// same order, and so a failure is reproducible.
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	updated := formatTime(now())
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
			key, values[key], updated); err != nil {
			return fmt.Errorf("store: set setting %q: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: set settings: %w", err)
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
