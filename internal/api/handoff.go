package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/jellyfin"
	"github.com/watzon/caravan/internal/store"
)

// jellyfinJSON is the playback-handoff configuration on the wire (SPEC §11:
// GET/POST /handoff/jellyfin). Stored credentials are write-only; HasAPIKey
// tells the settings screen whether one is already present.
type jellyfinJSON struct {
	URL       string `json:"url"`
	HasAPIKey bool   `json:"has_api_key"`
	Enabled   bool   `json:"enabled"`
}

// jellyfinTestJSON is what a successful test reports: proof the server answered
// and identified itself, which is more convincing than a bare "ok".
type jellyfinTestJSON struct {
	ServerName string `json:"server_name"`
	Version    string `json:"version"`
}

// jellyfinRequest is the body of POST /handoff/jellyfin and, with every field
// optional, of POST /handoff/jellyfin/test.
//
// The test body exists so the settings form can verify what is on screen before
// it is saved. Blank fields fall back to what is stored, so "test the saved
// configuration" is an empty object.
type jellyfinRequest struct {
	URL     string  `json:"url"`
	APIKey  *string `json:"api_key"`
	Enabled bool    `json:"enabled"`
}

func (b jellyfinRequest) config(apiKey string) (jellyfin.Config, string) {
	raw := strings.TrimRight(strings.TrimSpace(b.URL), "/")
	if raw != "" {
		// Parsed rather than pattern-matched: the client builds request URLs
		// from this string, so a value it cannot use should fail here, where
		// the user can fix it, rather than inside a background job.
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			return jellyfin.Config{}, "url must be an http or https URL"
		}
	}
	if b.Enabled && raw == "" {
		return jellyfin.Config{}, "url is required to enable the Jellyfin handoff"
	}
	return jellyfin.Config{
		URL:     raw,
		APIKey:  strings.TrimSpace(apiKey),
		Enabled: b.Enabled,
	}, ""
}

// handleGetJellyfin returns the stored playback-handoff configuration.
func (s *server) handleGetJellyfin(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.jellyfinConfig(r.Context())
	if err != nil {
		s.writeStoreError(w, "read jellyfin settings", err)
		return
	}
	writeJSON(w, http.StatusOK, jellyfinJSON{
		URL:       cfg.URL,
		HasAPIKey: cfg.APIKey != "",
		Enabled:   cfg.Enabled,
	})
}

// handleSetJellyfin replaces the stored configuration. It is a replace rather
// than a patch, which is why every field is read: the settings form owns all
// three values at once and a half-applied form is worse than a rejected one.
func (s *server) handleSetJellyfin(w http.ResponseWriter, r *http.Request) {
	var body jellyfinRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	stored, err := s.jellyfinConfig(r.Context())
	if err != nil {
		s.writeStoreError(w, "read jellyfin settings", err)
		return
	}
	apiKey := stored.APIKey
	if body.APIKey != nil {
		apiKey = *body.APIKey
	}
	cfg, problem := body.config(apiKey)
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}

	values := map[string]string{
		store.SettingJellyfinURL:     cfg.URL,
		store.SettingJellyfinAPIKey:  cfg.APIKey,
		store.SettingJellyfinEnabled: strconv.FormatBool(cfg.Enabled),
	}
	// Sorted so a partial failure is at least deterministic.
	for _, key := range []string{store.SettingJellyfinAPIKey, store.SettingJellyfinEnabled, store.SettingJellyfinURL} {
		if err := s.st.SetSetting(r.Context(), key, values[key]); err != nil {
			s.writeStoreError(w, "write jellyfin settings", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, jellyfinJSON{
		URL:       cfg.URL,
		HasAPIKey: cfg.APIKey != "",
		Enabled:   cfg.Enabled,
	})
}

// handleTestJellyfin asks the server who it is with the supplied credentials,
// falling back to the stored ones for whatever the body leaves blank.
//
// A failed test is reported with the server's own message: "it did not work"
// without a reason is useless for fixing a wrong API key or a typo'd port.
func (s *server) handleTestJellyfin(w http.ResponseWriter, r *http.Request) {
	var body jellyfinRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	stored, err := s.jellyfinConfig(r.Context())
	if err != nil {
		s.writeStoreError(w, "read jellyfin settings", err)
		return
	}
	target := strings.TrimRight(strings.TrimSpace(body.URL), "/")
	if target == "" {
		target = stored.URL
	}
	key := stored.APIKey
	if body.APIKey != nil && strings.TrimSpace(*body.APIKey) != "" {
		key = *body.APIKey
	}
	if target == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if parsed, perr := url.Parse(target); perr != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		writeError(w, http.StatusBadRequest, "url must be an http or https URL")
		return
	}

	info, err := jellyfin.NewClient(target, key, nil).SystemInfo(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "jellyfin test failed: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, jellyfinTestJSON{ServerName: info.Name, Version: info.Version})
}

// jellyfinConfig reads the three settings keys, treating "never set" as empty.
// AllSettings rather than three GetSettings: one query, and a key that has
// never been written is simply absent from the map instead of an ErrNotFound to
// special-case three times.
func (s *server) jellyfinConfig(ctx context.Context) (jellyfin.Config, error) {
	values, err := s.st.AllSettings(ctx)
	if err != nil {
		return jellyfin.Config{}, err
	}
	enabled, _ := strconv.ParseBool(values[store.SettingJellyfinEnabled])
	return jellyfin.Config{
		URL:     values[store.SettingJellyfinURL],
		APIKey:  values[store.SettingJellyfinAPIKey],
		Enabled: enabled,
	}, nil
}
