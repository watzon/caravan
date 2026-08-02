package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
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
var writableSettings = map[string]bool{
	store.SettingStorageRoot:            true,
	store.SettingTMDBAPIKey:             true,
	store.SettingRSSSyncIntervalMinutes: true,
	store.SettingBacklogIntervalMinutes: true,
	store.SettingEngineListenPort:       true,
	store.SettingEngineMaxConnections:   true,
	store.SettingEngineMaxDownKBps:      true,
	store.SettingEngineMaxUpKBps:        true,
	store.SettingEngineSeedRatio:        true,
	store.SettingEngineSeedDays:         true,
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

// handleGetSettings returns every setting as a flat object.
func (s *server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.st.AllSettings(r.Context())
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

	settings, err := s.st.AllSettings(r.Context())
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
	// FFmpegAvailable reports whether ffmpeg and ffprobe are both on PATH.
	// False hides the whole convert-for-TV affordance and degrades the
	// TV-incompatible warning to informational (SPEC §8).
	FFmpegAvailable bool `json:"ffmpeg_available"`
}

type statusCounts struct {
	Movies     int `json:"movies"`
	Series     int `json:"series"`
	MediaFiles int `json:"media_files"`
	Unmatched  int `json:"unmatched"`
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
		},
		DiskFreeBytes:   diskFree,
		DiskTotalBytes:  diskTotal,
		EngineHealth:    s.engineHealth(),
		FFmpegAvailable: s.ffmpegAvailable(),
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
