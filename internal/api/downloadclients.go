package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
)

// downloadClientJSON is a configured external download client on the wire
// (SPEC §5.1, §11 `/download-clients`).
//
// Unlike the indexer and Jellyfin API keys, the password and API key are NOT
// echoed back. Those two predate the phase-5 password: with the API open, a
// credential the server withheld was a credential the user could not re-read,
// so echoing lost nothing. This surface is new, so it follows the rule
// hiddenSettings already states — a credential the API hands back is a
// credential that ends up in a browser cache, a screenshot or a bug report
// (SPEC §12) — and reports only whether one is stored.
//
// The consequence is that the edit form cannot pre-fill the field, which is
// why downloadClientRequest treats an omitted credential as "keep the stored
// one" rather than "clear it".
type downloadClientJSON struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	Username string `json:"username"`
	// HasPassword and HasAPIKey stand in for the values themselves, so the
	// form can render "stored" instead of an empty box that looks unset.
	HasPassword bool   `json:"has_password"`
	HasAPIKey   bool   `json:"has_api_key"`
	Category    string `json:"category"`
	Priority    int    `json:"priority"`
	Enabled     bool   `json:"enabled"`
}

func downloadClientDTO(c core.DownloadClientConfig) downloadClientJSON {
	return downloadClientJSON{
		ID:          c.ID,
		Type:        c.Type,
		Name:        c.Name,
		URL:         c.URL,
		Username:    c.Username,
		HasPassword: c.Password != "",
		HasAPIKey:   c.APIKey != "",
		Category:    c.Category,
		Priority:    c.Priority,
		Enabled:     c.Enabled,
	}
}

// downloadClientRequest is the body of POST /download-clients, PUT
// /download-clients/{id} and POST /download-clients/test. Like the indexer
// endpoints, PUT replaces the whole configuration rather than patching it.
//
// Password and APIKey are pointers because the GET never returns them: a form
// that reposts what it was given has no value to repost, and treating that as
// "clear the credential" would wipe a working client on every unrelated edit.
// Omitted (or null) keeps what is stored; "" clears it deliberately.
//
// Enabled is a pointer for the reason it is on indexers: an omitted flag means
// enabled, because a user who just added a client wants it used. Priority is a
// pointer so an omitted one keeps the same default the column has.
type downloadClientRequest struct {
	// ID is read only by POST /download-clients/test, where it names the
	// stored row a blank credential falls back to. It is ignored elsewhere:
	// the path segment is the identity for PUT.
	ID       int64   `json:"id"`
	Type     string  `json:"type"`
	Name     string  `json:"name"`
	URL      string  `json:"url"`
	Username string  `json:"username"`
	Password *string `json:"password"`
	APIKey   *string `json:"api_key"`
	Category string  `json:"category"`
	Priority *int    `json:"priority"`
	Enabled  *bool   `json:"enabled"`
}

// defaultDownloadClientPriority matches the column default, so a client added
// without an opinion sorts alongside one added before the field existed.
const defaultDownloadClientPriority = 25

// config validates the body against its type and turns it into a store-ready
// configuration. stored is the row being replaced (nil when there is none):
// credentials the body omits are taken from it. The returned message is empty
// when the body is valid.
func (b downloadClientRequest) config(stored *core.DownloadClientConfig) (core.DownloadClientConfig, string) {
	t, known := clients.Lookup(b.Type)
	if !known {
		return core.DownloadClientConfig{}, "type must be one of " + strings.Join(clients.TypeNames(), ", ")
	}

	cfg := core.DownloadClientConfig{
		Type:     t.Name,
		Name:     strings.TrimSpace(b.Name),
		URL:      strings.TrimRight(strings.TrimSpace(b.URL), "/"),
		Username: strings.TrimSpace(b.Username),
		Category: strings.TrimSpace(b.Category),
		Priority: defaultDownloadClientPriority,
		Enabled:  true,
	}
	if b.Priority != nil {
		cfg.Priority = *b.Priority
	}
	if b.Enabled != nil {
		cfg.Enabled = *b.Enabled
	}

	switch {
	case b.Password != nil:
		cfg.Password = *b.Password
	case stored != nil:
		cfg.Password = stored.Password
	}
	switch {
	case b.APIKey != nil:
		cfg.APIKey = strings.TrimSpace(*b.APIKey)
	case stored != nil:
		cfg.APIKey = stored.APIKey
	}
	// A backend that does not use a credential must not keep one: leaving a
	// password behind after the type changed would store a secret nothing can
	// use and nothing shows.
	if !t.UsesLogin {
		cfg.Username = ""
		cfg.Password = ""
	}
	if !t.UsesAPIKey {
		cfg.APIKey = ""
	}

	if err := t.Validate(cfg); err != nil {
		return core.DownloadClientConfig{}, err.Error()
	}
	return cfg, ""
}

// sameDownloadClientTarget reports whether a test body still names the machine
// stored's credential belongs to, comparing the two fields that decide where
// the probe's request — and with it the credential — is sent.
//
// URL and type are normalised exactly as config normalises them, so a body that
// round-trips what the GET returned still matches.
func sameDownloadClientTarget(body downloadClientRequest, stored core.DownloadClientConfig) bool {
	t, known := clients.Lookup(body.Type)
	if !known || t.Name != stored.Type {
		return false
	}
	return strings.TrimRight(strings.TrimSpace(body.URL), "/") == stored.URL
}

// handleListDownloadClientTypes reports the backends this build can be
// configured with, so the settings form does not have to hard-code the list or
// guess which credentials each one needs.
func (s *server) handleListDownloadClientTypes(w http.ResponseWriter, r *http.Request) {
	types := clients.Types()
	out := make([]map[string]any, 0, len(types))
	for _, t := range types {
		out = append(out, map[string]any{
			"type":         t.Name,
			"label":        t.Label,
			"protocol":     t.Protocol,
			"uses_login":   t.UsesLogin,
			"uses_api_key": t.UsesAPIKey,
			"supported":    s.clients().Supported(t.Name),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"types": out})
}

func (s *server) handleListDownloadClients(w http.ResponseWriter, r *http.Request) {
	configs, err := s.st.ListDownloadClients(r.Context())
	if err != nil {
		s.writeStoreError(w, "list download clients", err)
		return
	}

	out := make([]downloadClientJSON, 0, len(configs))
	for _, c := range configs {
		out = append(out, downloadClientDTO(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"download_clients": out})
}

func (s *server) handleCreateDownloadClient(w http.ResponseWriter, r *http.Request) {
	var body downloadClientRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	cfg, msg := body.config(nil)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ctx := r.Context()

	if !s.downloadClientNameFree(ctx, w, cfg.Name, 0) {
		return
	}
	if err := s.st.UpsertDownloadClient(ctx, &cfg); err != nil {
		s.writeStoreError(w, "create download client", err)
		return
	}
	writeJSON(w, http.StatusCreated, downloadClientDTO(cfg))
}

func (s *server) handleUpdateDownloadClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body downloadClientRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()

	// Read first, both so updating a client that never existed is a 404 rather
	// than a silent insert, and so an omitted credential has a stored value to
	// fall back to.
	stored, err := s.st.GetDownloadClient(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get download client", err)
		return
	}
	cfg, msg := body.config(stored)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if !s.downloadClientNameFree(ctx, w, cfg.Name, id) {
		return
	}
	cfg.ID = id
	if err := s.st.UpsertDownloadClient(ctx, &cfg); err != nil {
		s.writeStoreError(w, "update download client", err)
		return
	}
	writeJSON(w, http.StatusOK, downloadClientDTO(cfg))
}

// handleDeleteDownloadClient removes the configuration. Downloads it already
// started keep their engine name and engine id, so the queue and the library
// survive the delete.
func (s *server) handleDeleteDownloadClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if _, err := s.st.GetDownloadClient(r.Context(), id); err != nil {
		s.writeStoreError(w, "get download client", err)
		return
	}
	if err := s.st.DeleteDownloadClient(r.Context(), id); err != nil {
		s.writeStoreError(w, "delete download client", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestDownloadClient asks the stored client whether it answers with the
// stored credentials, the same probe POST /indexers/{id}/test runs.
func (s *server) handleTestDownloadClient(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cfg, err := s.st.GetDownloadClient(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get download client", err)
		return
	}
	s.writeDownloadClientTest(w, r.Context(), *cfg)
}

// handleTestDownloadClientConfig probes a configuration from the body rather
// than a stored row, for the same reason POST /indexers/categories does: the
// settings form needs an answer while the user is still typing, before the
// client exists to have an id.
//
// An "id" in the body names the row a blank credential falls back to, which is
// what makes Test work while editing a saved client: the GET never returned the
// password, so the form has none to send.
func (s *server) handleTestDownloadClientConfig(w http.ResponseWriter, r *http.Request) {
	var body downloadClientRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	var stored *core.DownloadClientConfig
	if body.ID > 0 {
		found, err := s.st.GetDownloadClient(r.Context(), body.ID)
		if err != nil {
			s.writeStoreError(w, "get download client", err)
			return
		}
		// The fallback only applies to the destination the credential was
		// stored for. Pairing a stored password or API key with a URL from the
		// body would make this endpoint send the credential the GET refuses to
		// return to any host the caller names (SPEC §12) — the exfiltration the
		// withholding exists to prevent. A genuinely new address is a new
		// credential, and the form still has the field to type it into.
		if sameDownloadClientTarget(body, *found) {
			stored = found
		}
	}
	// The name only labels error messages here; the form may not have one yet.
	if strings.TrimSpace(body.Name) == "" {
		body.Name = "download client"
	}
	cfg, msg := body.config(stored)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	s.writeDownloadClientTest(w, r.Context(), cfg)
}

// writeDownloadClientTest runs the probe and reports it.
//
// A failed test carries the client's own message, because "it did not work"
// without a reason is useless for fixing a wrong password or a typo'd port.
// That message is deliberately not logged: it can quote the request URL, and
// SPEC §12 keeps download-client credentials out of the logs.
//
// A backend nothing has registered yet is a 501, not a 502: the client is not
// broken, this build simply cannot talk to it.
func (s *server) writeDownloadClientTest(w http.ResponseWriter, ctx context.Context, cfg core.DownloadClientConfig) {
	err := s.clients().TestConnection(ctx, cfg)
	switch {
	case err == nil:
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	case errors.Is(err, clients.ErrNotSupported):
		writeError(w, http.StatusNotImplemented, err.Error())
	default:
		writeError(w, http.StatusBadGateway, "download client test failed: "+err.Error())
	}
}

// clients returns the registry the test endpoints probe through. A server
// built without WithDownloadClients uses the process-wide registry the serving
// process wires its backends into.
func (s *server) clients() *clients.Registry {
	if s.downloadClients != nil {
		return s.downloadClients
	}
	return clients.Default
}

// downloadClientNameFree reports whether name is available, writing a 409 when
// it is not. download_clients.name is unique, so without this check a duplicate
// name — a plain user mistake — would surface as a 500.
func (s *server) downloadClientNameFree(ctx context.Context, w http.ResponseWriter, name string, exceptID int64) bool {
	configs, err := s.st.ListDownloadClients(ctx)
	if err != nil {
		s.writeStoreError(w, "list download clients", err)
		return false
	}
	for _, c := range configs {
		if c.ID != exceptID && c.Name == name {
			writeError(w, http.StatusConflict, "a download client named "+name+" already exists")
			return false
		}
	}
	return true
}
