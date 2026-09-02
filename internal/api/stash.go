package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/stash"
	"github.com/watzon/caravan/internal/store"
)

// StashService is the slice of internal/stash the HTTP layer needs: whether the
// last handoff attempt reached the server, and the ability to forget that
// verdict.
//
// It is an interface for the same reason DLNAService is: a server built without
// one still serves the whole API, and the status endpoint simply reports no
// Stash trouble rather than the endpoint failing.
type StashService interface {
	Health() stash.Health
	// ResetHealth forgets the last verdict. The handoff remembers rather than
	// probes, so the settings screen is the only place that can tell it the
	// question has changed. A new URL, a handoff switched off, a test that just
	// succeeded. Without it a banner survives the fix that made it wrong.
	ResetHealth()
}

// forgetStashHealth clears a stale outage banner, if there is a handoff at all.
func (s *server) forgetStashHealth() {
	if s.stash != nil {
		s.stash.ResetHealth()
	}
}

// WithStash supplies the adult library's handoff.
func WithStash(s StashService) Option {
	return func(srv *server) { srv.stash = s }
}

// stashJSON is the handoff configuration on the wire (GET/POST /adult/stash).
// It mirrors jellyfinJSON field for field, because the settings card mirrors
// the Jellyfin one. See jellyfinJSON for why the API key is echoed back.
type stashJSON struct {
	URL     string `json:"url"`
	APIKey  string `json:"api_key"`
	Enabled bool   `json:"enabled"`
}

// stashTestJSON is what a successful test reports: proof the server answered
// and identified itself.
type stashTestJSON struct {
	Version string `json:"version"`
	Hash    string `json:"hash"`
}

// stashHealthJSON is the unreachable-Stash banner's input, carried on
// GET /system/status exactly as unhealthy_download_clients is.
type stashHealthJSON struct {
	Error string `json:"error"`
	// Since is when the outage started, RFC3339, so a banner can say how long
	// it has been going on.
	Since string `json:"since"`
}

// stashRequest is the body of POST /adult/stash and, with every field optional,
// of POST /adult/stash/test. Blank fields on a test fall back to what is
// stored, so "test the saved configuration" is an empty object.
type stashRequest struct {
	URL     string `json:"url"`
	APIKey  string `json:"api_key"`
	Enabled bool   `json:"enabled"`
}

// config validates the body into a storable configuration. The returned message
// is empty when the body is valid.
func (b stashRequest) config() (stash.Config, string) {
	raw := strings.TrimRight(strings.TrimSpace(b.URL), "/")
	if raw != "" {
		if problem := checkStashURL(raw); problem != "" {
			return stash.Config{}, problem
		}
	}
	if b.Enabled && raw == "" {
		return stash.Config{}, "url is required to enable the Stash handoff"
	}
	return stash.Config{
		URL:     raw,
		APIKey:  strings.TrimSpace(b.APIKey),
		Enabled: b.Enabled,
	}, ""
}

// checkStashURL validates a server address, returning the user-facing problem
// or "".
//
// Parsed rather than pattern-matched: the client builds request URLs from this
// string, so a value it cannot use should fail here, where the user can fix it,
// rather than inside a background job.
//
// Userinfo is refused rather than ignored. Go's HTTP client turns
// http://user:pass@stash.lan:9999 into an Authorization header, so a URL that
// carries it *is* a credential. One that would then be stored beside the API
// key, echoed back by GET /adult/stash, and written wherever the handoff logs
// the address it is talking to. Stash's own credential is the API key field
// next to this one, and keeping the secret in exactly one field is what makes
// "never log a credential" (SPEC §12) something the code can actually honour.
func checkStashURL(raw string) string {
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "url must be an http or https URL"
	}
	if parsed.User != nil {
		return "url must not carry a username or password; use the API key field"
	}
	return ""
}

// handleGetStash returns the stored handoff configuration.
//
// It lives on the adult mux, so the gate is structural: with the module off, or
// for a caller who was never granted it, this is 404 before the handler runs.
// That is the whole reason the Stash card is not a pair of keys in PUT
// /settings. An adult-module feature has to be absent, not merely disabled.
func (s *server) handleGetStash(w http.ResponseWriter, r *http.Request) {
	cfg, err := s.stashConfig(r.Context())
	if err != nil {
		s.writeStoreError(w, "read stash settings", err)
		return
	}
	writeJSON(w, http.StatusOK, stashJSON(cfg))
}

// handleSetStash replaces the stored configuration. It is a replace rather than
// a patch, which is why every field is read: the settings form owns all three
// values at once and a half-applied form is worse than a rejected one.
func (s *server) handleSetStash(w http.ResponseWriter, r *http.Request) {
	var body stashRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	cfg, problem := body.config()
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}
	stored, err := s.stashConfig(r.Context())
	if err != nil {
		s.writeStoreError(w, "read stash settings", err)
		return
	}

	// One transaction: the three keys are one card, and committing a new URL
	// beside a stale key would leave a combination nothing ever tested live
	// against a handoff that is already on.
	if err := s.st.SetSettings(r.Context(), map[string]string{
		store.SettingStashURL:     cfg.URL,
		store.SettingStashAPIKey:  cfg.APIKey,
		store.SettingStashEnabled: strconv.FormatBool(cfg.Enabled),
	}); err != nil {
		s.writeStoreError(w, "write stash settings", err)
		return
	}
	// The remembered verdict was about the old card. A new address, a new key or
	// a handoff switched off all make it a statement about a server this Caravan
	// is no longer trying to reach, and leaving the banner up would tell the user
	// their fix did not work.
	if cfg != stored {
		s.forgetStashHealth()
	}
	writeJSON(w, http.StatusOK, stashJSON(cfg))
}

// handleTestStash asks the server which version it is with the supplied
// credentials, falling back to the stored ones for whatever the body leaves
// blank.
//
// A failed test is reported with the server's own message: "it did not work"
// without a reason is useless for fixing a wrong API key or a typo'd port.
func (s *server) handleTestStash(w http.ResponseWriter, r *http.Request) {
	var body stashRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	stored, err := s.stashConfig(r.Context())
	if err != nil {
		s.writeStoreError(w, "read stash settings", err)
		return
	}
	target := strings.TrimRight(strings.TrimSpace(body.URL), "/")
	if target == "" {
		target = stored.URL
	}
	key := strings.TrimSpace(body.APIKey)
	if key == "" {
		key = stored.APIKey
	}
	if target == "" {
		writeError(w, http.StatusBadRequest, "url is required")
		return
	}
	if problem := checkStashURL(target); problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}

	version, err := stash.NewClient(target, key, nil).Version(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "stash test failed: "+err.Error())
		return
	}
	// A server that just answered is not unreachable, whatever the last handoff
	// concluded. This is the only probe in the system, so it is the one place a
	// stale banner can be cleared the moment the user proves it wrong.
	s.forgetStashHealth()
	writeJSON(w, http.StatusOK, stashTestJSON{Version: version.Version, Hash: version.Hash})
}

// stashConfig reads the three settings keys, treating "never set" as empty.
func (s *server) stashConfig(ctx context.Context) (stash.Config, error) {
	values, err := s.st.AllSettings(ctx)
	if err != nil {
		return stash.Config{}, err
	}
	enabled, _ := strconv.ParseBool(strings.TrimSpace(values[store.SettingStashEnabled]))
	return stash.Config{
		URL:     strings.TrimSpace(values[store.SettingStashURL]),
		APIKey:  strings.TrimSpace(values[store.SettingStashAPIKey]),
		Enabled: enabled,
	}, nil
}

// stashHealth is the status endpoint's banner input: the Stash server the
// handoff cannot reach right now, or nil.
//
// visible is passed in rather than resolved here for the reason the scene count
// is: the caller already asked, and asking twice is a second chance to ask
// differently. An ungranted caller, or one on a server with the module off,
// gets nothing at all. A banner about a service that does not exist for you is
// the same leak requireAdult refuses.
func (s *server) stashHealth(visible bool) *stashHealthJSON {
	if !visible || s.stash == nil {
		return nil
	}
	health := s.stash.Health()
	if !health.Unreachable() {
		return nil
	}
	return &stashHealthJSON{Error: health.Error, Since: jsonTime(health.Since)}
}
