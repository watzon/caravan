package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/wanted"
)

// SettingMode records the deployment mode (SPEC §2) so GET /system/status can
// report it. The serving process writes it at startup from the bootstrap
// config; an unset value reports ModeServer.
const SettingMode = "mode"

// Deployment modes reported by GET /system/status.
const (
	ModeServer   = "server"
	ModePortable = "portable"
)

// writableSettings is the allowlist PUT /settings accepts. Settings are a
// key-value table, so without an allowlist a buggy client could quietly fill
// it with keys nothing reads.
//
// store.SettingStorageRoot is deliberately absent. It is the one setting with
// rules attached — it must be absolute, it must name a folder that exists, and
// it must not change while a migration owns both roots — and a generic
// key-value PUT enforces none of them. POST /system/storage-root/repoint is the
// only way in (SPEC §10); see internal/api/storage.go.
var writableSettings = map[string]bool{
	store.SettingTMDBAPIKey:             true,
	store.SettingRSSSyncIntervalMinutes: true,
	store.SettingBacklogIntervalMinutes: true,
	store.SettingEngineListenPort:       true,
	store.SettingEngineMaxConnections:   true,
	store.SettingEngineMaxDownKBps:      true,
	store.SettingEngineMaxUpKBps:        true,
	store.SettingEngineSeedRatio:        true,
	store.SettingEngineSeedDays:         true,
	store.SettingRouteTorrent:           true,
	store.SettingRouteUsenet:            true,
	store.SettingTVProfile:              true,
	store.SettingDLNAEnabled:            true,
	store.SettingDLNAFriendlyName:       true,
	SettingMode:                         true,
}

// engineSettingsApplier is implemented by providers that can apply the live
// subset of engine settings after the settings row has been committed.
type engineSettingsApplier interface {
	ApplyEngineSettings(context.Context, map[string]string) error
}

// hiddenSettings never leave the server. The password hash is not a value the
// UI has any use for, and a credential the API hands back is a credential that
// ends up in a browser cache, a screenshot or a bug report (SPEC §12).
//
// The other secrets in this table (the TMDB and Jellyfin keys) are values the
// user typed into a form and has to be able to see and correct, so they stay.
var hiddenSettings = map[string]bool{
	store.SettingPasswordHash: true,
}

// visibleSettings is every setting a client is allowed to read. It is the only
// path from the settings table to a response body, so a key added to
// hiddenSettings is hidden everywhere at once.
func (s *server) visibleSettings(ctx context.Context) (map[string]string, error) {
	settings, err := s.st.AllSettings(ctx)
	if err != nil {
		return nil, err
	}
	for key := range hiddenSettings {
		delete(settings, key)
	}
	return settings, nil
}

// handleGetSettings returns every setting as a flat object, minus the ones that
// are never readable.
func (s *server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.visibleSettings(r.Context())
	if err != nil {
		s.writeStoreError(w, "read settings", err)
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

// handlePutSettings upserts the supplied keys and returns the resulting
// settings. It is a partial update: keys absent from the body keep their
// current value.
func (s *server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var body map[string]string
	if !decodeJSON(w, r, &body) {
		return
	}

	unknown := []string{}
	for key := range body {
		if !writableSettings[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		writeError(w, http.StatusBadRequest, "unknown setting: "+unknown[0])
		return
	}

	if err := validateEngineSettings(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateTVProfileSetting(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := validateDLNASettings(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validateRouteSettings(r.Context(), body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Sorted so a partial failure is at least deterministic.
	keys := make([]string, 0, len(body))
	for key := range body {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		if err := s.st.SetSetting(r.Context(), key, body[key]); err != nil {
			s.writeStoreError(w, "write settings", err)
			return
		}
	}

	settings, err := s.visibleSettings(r.Context())
	if err != nil {
		s.writeStoreError(w, "read settings", err)
		return
	}
	if applier, ok := s.engine.(engineSettingsApplier); ok {
		if err := applier.ApplyEngineSettings(r.Context(), settings); err != nil {
			s.writeEngineError(w, "apply engine settings", err)
			return
		}
	}
	// The media server re-reads its own keys so the toggle takes effect without
	// a restart. It cannot fail the request: a LAN that will not carry SSDP is
	// reported through GET /dlna, not by rejecting a settings save that already
	// landed.
	if s.dlna != nil {
		s.dlna.Reload(r.Context())
	}
	writeJSON(w, http.StatusOK, settings)
}

// validateDLNASettings refuses values the media server would silently reinterpret.
//
// An unparseable dlna_enabled reads as off, and a friendly name that is only
// whitespace falls back to the default — both are quiet surprises, so they are
// rejected here where the user can see them (SPEC §13).
func validateDLNASettings(settings map[string]string) error {
	if raw, ok := settings[store.SettingDLNAEnabled]; ok {
		if _, err := strconv.ParseBool(strings.TrimSpace(raw)); err != nil {
			return fmt.Errorf("invalid %s", store.SettingDLNAEnabled)
		}
	}
	if raw, ok := settings[store.SettingDLNAFriendlyName]; ok {
		name := strings.TrimSpace(raw)
		if name == "" {
			return fmt.Errorf("invalid %s", store.SettingDLNAFriendlyName)
		}
		// The name is carried in the device description and rendered on a TV's
		// device list; anything longer is truncated there and unreadable.
		if len([]rune(name)) > 64 {
			return fmt.Errorf("invalid %s", store.SettingDLNAFriendlyName)
		}
	}
	return nil
}

// validateRouteSettings refuses a per-protocol default that would not route.
//
// The router resolves these ids at grab time and falls back to "nothing
// configured" for one it cannot use, so an id that is gone, disabled, or of
// the wrong protocol would otherwise be accepted here and silently reject
// every grab later. Pointing the torrent default at SABnzbd is the mistake
// worth catching: it looks configured and downloads nothing.
func (s *server) validateRouteSettings(ctx context.Context, settings map[string]string) error {
	for _, route := range []struct {
		key      string
		protocol string
	}{
		{store.SettingRouteTorrent, core.ProtocolTorrent},
		{store.SettingRouteUsenet, core.ProtocolUsenet},
	} {
		raw, ok := settings[route.key]
		if !ok {
			continue
		}
		value := strings.TrimSpace(raw)
		// Empty is "no default": legal everywhere, and the only value usenet
		// has before a client exists.
		if value == "" {
			continue
		}
		if value == store.RouteEmbedded {
			if route.protocol != core.ProtocolTorrent {
				return fmt.Errorf("invalid %s: the embedded engine only handles torrents", route.key)
			}
			continue
		}
		id, err := strconv.ParseInt(value, 10, 64)
		if err != nil || id <= 0 {
			return fmt.Errorf("invalid %s", route.key)
		}
		cfg, err := s.st.GetDownloadClient(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("invalid %s: no download client with id %d", route.key, id)
		}
		if err != nil {
			return fmt.Errorf("invalid %s", route.key)
		}
		t, ok := clients.Lookup(cfg.Type)
		if !ok || t.Protocol != route.protocol {
			return fmt.Errorf("invalid %s: %s does not handle %s releases", route.key, cfg.Name, route.protocol)
		}
	}
	return nil
}

// validateTVProfileSetting refuses a profile id nothing implements. The
// resolver falls back to the safe default at read time, so an unknown id would
// otherwise be stored and silently ignored — the opposite of SPEC §13.
func validateTVProfileSetting(settings map[string]string) error {
	id, ok := settings[store.SettingTVProfile]
	if !ok {
		return nil
	}
	id = strings.TrimSpace(id)
	for _, p := range core.TVProfiles() {
		if p.ID == id {
			return nil
		}
	}
	return fmt.Errorf("invalid %s", store.SettingTVProfile)
}

func validateEngineSettings(settings map[string]string) error {
	for key, value := range settings {
		switch key {
		case store.SettingEngineListenPort:
			port, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || port < 0 || port > 65535 {
				return fmt.Errorf("invalid %s", key)
			}
		case store.SettingEngineMaxConnections, store.SettingEngineSeedDays:
			n, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || n < 0 {
				return fmt.Errorf("invalid %s", key)
			}
		case store.SettingEngineMaxDownKBps, store.SettingEngineMaxUpKBps:
			n, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil || n < 0 {
				return fmt.Errorf("invalid %s", key)
			}
		case store.SettingEngineSeedRatio:
			ratio, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
			if err != nil || ratio < 0 {
				return fmt.Errorf("invalid %s", key)
			}
		}
	}
	return nil
}

// statusResponse is the payload of GET /system/status.
type statusResponse struct {
	Version       string       `json:"version"`
	Mode          string       `json:"mode"`
	StorageRoot   string       `json:"storage_root"`
	SchemaVersion int          `json:"schema_version"`
	Scanning      bool         `json:"scanning"`
	Counts        statusCounts `json:"counts"`
	// DiskFreeBytes and DiskTotalBytes describe the filesystem holding the
	// storage root. Both zero when no root is set or the filesystem cannot be
	// asked — the UI renders that as "unknown", never as "full".
	DiskFreeBytes  int64 `json:"disk_free_bytes"`
	DiskTotalBytes int64 `json:"disk_total_bytes"`
	// EngineHealth is the download engine's state: "ok", "unconfigured" (no
	// storage root yet, so no engine), or "error" (it failed to start).
	EngineHealth string `json:"engine_health"`
	// UnhealthyDownloadClients names the external clients the queue poller
	// cannot reach (PLAN phase 6 task 4). Empty is the normal case; a
	// non-empty list is what raises the "client X unreachable" banner. The
	// embedded engine is never in it — it is not a client, and one dead
	// seedbox must not make Caravan look broken.
	UnhealthyDownloadClients []unhealthyClientJSON `json:"unhealthy_download_clients"`
	// FFmpegAvailable reports whether ffmpeg and ffprobe are both on PATH.
	// False hides the whole convert-for-TV affordance and degrades the
	// TV-incompatible warning to informational (SPEC §8).
	FFmpegAvailable bool `json:"ffmpeg_available"`
	// PasswordSet and ListeningPublicly are the two halves of the nag in
	// SPEC §11: a server reachable from other machines with no password on it.
	// Neither is a credential — the hash itself never leaves the server.
	PasswordSet       bool `json:"password_set"`
	ListeningPublicly bool `json:"listening_publicly"`
	// Dirty says the previous session ended without a clean shutdown — a pulled
	// drive, a power cut, a kill -9 (SPEC §2.3). It stays true until
	// POST /system/verify passes, and while it is true downloads refuse to
	// resume. Only portable mode ever sets it.
	Dirty bool `json:"dirty"`
}

// unhealthyClientJSON is one unreachable download client on GET
// /system/status. It carries no credential — the fields are the ones the
// settings screen already shows, plus the poll's own failure message
// (SPEC §12).
type unhealthyClientJSON struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Type  string `json:"type"`
	Error string `json:"error"`
	Since string `json:"since"`
}

type statusCounts struct {
	Movies     int `json:"movies"`
	Series     int `json:"series"`
	MediaFiles int `json:"media_files"`
	Unmatched  int `json:"unmatched"`
	// Wanted is the monitored-but-missing backlog (movies plus episodes),
	// the same list GET /wanted renders.
	Wanted int `json:"wanted"`
	// Converting is the open convert-for-TV queue: queued plus running.
	Converting int `json:"converting"`
}

// handleSystemStatus reports what the UI needs to render the shell: build
// version, deployment mode, where the library lives, and how much is in it.
//
// The counts come from the list queries rather than COUNT(*) so that SQL stays
// inside the store package. A phase-1 library is small enough that this is a
// non-issue; if it stops being one, the fix is a Count* method in the store,
// not a query here.
func (s *server) handleSystemStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	root, err := s.st.GetSetting(ctx, store.SettingStorageRoot)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeStoreError(w, "read storage root", err)
		return
	}

	mode, err := s.st.GetSetting(ctx, SettingMode)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeStoreError(w, "read mode", err)
		return
	}
	if mode == "" {
		mode = ModeServer
	}

	schemaVersion, err := s.st.SchemaVersion()
	if err != nil {
		s.writeStoreError(w, "read schema version", err)
		return
	}

	movies, err := s.st.ListMovies(ctx)
	if err != nil {
		s.writeStoreError(w, "count movies", err)
		return
	}
	series, err := s.st.ListSeries(ctx)
	if err != nil {
		s.writeStoreError(w, "count series", err)
		return
	}
	files, err := s.st.ListMediaFiles(ctx)
	if err != nil {
		s.writeStoreError(w, "count media files", err)
		return
	}
	unmatched, err := s.st.ListUnmatchedFiles(ctx)
	if err != nil {
		s.writeStoreError(w, "count unmatched files", err)
		return
	}
	wantedLists, err := wanted.Compute(ctx, s.st)
	if err != nil {
		s.writeStoreError(w, "compute wanted list", err)
		return
	}
	conversions, err := s.st.ListConversions(ctx, 0)
	if err != nil {
		s.writeStoreError(w, "count conversions", err)
		return
	}
	converting := 0
	for _, c := range conversions {
		if core.ConversionOpen(c.Status) {
			converting++
		}
	}

	passwordHash, err := s.passwordHash(ctx)
	if err != nil {
		s.writeStoreError(w, "read password", err)
		return
	}

	var diskFree, diskTotal int64
	if root != "" {
		if free, total, err := diskUsage(root); err == nil {
			diskFree, diskTotal = free, total
		}
		// A failure stays zeros: the storage root being unreadable is the
		// scanner's error to raise loudly, not the status endpoint's.
	}

	writeJSON(w, http.StatusOK, statusResponse{
		Version:       Version,
		Mode:          mode,
		StorageRoot:   root,
		SchemaVersion: schemaVersion,
		Scanning:      s.scanning.Load(),
		Counts: statusCounts{
			Movies:     len(movies),
			Series:     len(series),
			MediaFiles: len(files),
			Unmatched:  len(unmatched),
			Wanted:     len(wantedLists.Movies) + len(wantedLists.Episodes),
			Converting: converting,
		},
		DiskFreeBytes:            diskFree,
		DiskTotalBytes:           diskTotal,
		EngineHealth:             s.engineHealth(),
		UnhealthyDownloadClients: s.unhealthyDownloadClients(),
		FFmpegAvailable:          s.ffmpegAvailable(),
		PasswordSet:              passwordHash != "",
		ListeningPublicly:        listeningPublicly(s.listenAddr),
		Dirty:                    s.dirty.Load(),
	})
}

// engineHealth is what the system panel renders next to "Engine". A provider
// that can tell a failed engine from an unbuilt one implements HealthReporter;
// otherwise health is derived from whether an engine exists at all.
func (s *server) engineHealth() string {
	if s.engine == nil {
		return "unconfigured"
	}
	if hr, ok := s.engine.(HealthReporter); ok {
		return hr.Health()
	}
	if s.engine.Engine() == nil {
		return "unconfigured"
	}
	return "ok"
}

// unhealthyDownloadClients is the banner's input: the external clients the
// queue poller cannot reach right now. A provider that does not poll external
// clients — the phase-2 embedded-only wiring, and every test server built
// without one — reports none.
func (s *server) unhealthyDownloadClients() []unhealthyClientJSON {
	out := []unhealthyClientJSON{}
	if s.engine == nil {
		return out
	}
	reporter, ok := s.engine.(DownloadClientHealthReporter)
	if !ok {
		return out
	}
	for _, c := range reporter.UnhealthyDownloadClients() {
		out = append(out, unhealthyClientJSON{
			ID:    c.ID,
			Name:  c.Name,
			Type:  c.Type,
			Error: c.Error,
			Since: jsonTime(c.Since),
		})
	}
	return out
}
