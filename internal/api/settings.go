package api

import (
	"errors"
	"net/http"
	"sort"

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
	SettingMode:                         true,
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
	writeJSON(w, http.StatusOK, settings)
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
		DiskFreeBytes:  diskFree,
		DiskTotalBytes: diskTotal,
		EngineHealth:   s.engineHealth(),
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
