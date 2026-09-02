package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/usenet"
	"github.com/watzon/caravan/internal/usenet/nntp"
)

// usenetServerJSON is one configured news server on the wire (SPEC §5.1, §11
// `/usenet-servers`).
//
// These are the built-in engine's article sources, not download clients: the
// engine reads article bodies from them itself. Nothing here is optional in the
// way an external client is, with no server configured the engine has nowhere
// to download Usenet releases from at all.
//
// The password follows the download-client rule exactly: it is never echoed
// back, because a credential the API hands out is a credential that ends up in
// a browser cache, a screenshot or a bug report (SPEC §12). Only whether one
// is stored is reported, which is why usenetServerRequest treats an omitted
// password as "keep the stored one" rather than "clear it".
type usenetServerJSON struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	TLS      bool   `json:"tls"`
	Username string `json:"username"`
	// HasPassword stands in for the value itself, so the form can render
	// "stored" instead of an empty box that looks unset.
	HasPassword    bool `json:"has_password"`
	MaxConnections int  `json:"max_connections"`
	Priority       int  `json:"priority"`
	Enabled        bool `json:"enabled"`
}

func usenetServerDTO(c core.UsenetServerConfig) usenetServerJSON {
	return usenetServerJSON{
		ID:             c.ID,
		Name:           c.Name,
		Host:           c.Host,
		Port:           c.Port,
		TLS:            c.TLS,
		Username:       c.Username,
		HasPassword:    c.Password != "",
		MaxConnections: c.MaxConnections,
		Priority:       c.Priority,
		Enabled:        c.Enabled,
	}
}

// usenetServerRequest is the body of POST /usenet-servers, PUT
// /usenet-servers/{id} and POST /usenet-servers/test. Like the download-client
// endpoints, PUT replaces the whole configuration rather than patching it.
//
// Password is a pointer because the GET never returns it: a form that reposts
// what it was given has no value to repost, and treating that as "clear the
// credential" would wipe a working server on every unrelated edit. Omitted (or
// null) keeps what is stored; "" clears it deliberately.
//
// Port, MaxConnections, Priority and Enabled are pointers so an omitted field
// keeps the same default the column has, rather than meaning zero.
type usenetServerRequest struct {
	// ID is read only by POST /usenet-servers/test, where it names the stored
	// row a blank password falls back to. It is ignored elsewhere: the path
	// segment is the identity for PUT.
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	Host           string  `json:"host"`
	Port           *int    `json:"port"`
	TLS            *bool   `json:"tls"`
	Username       string  `json:"username"`
	Password       *string `json:"password"`
	MaxConnections *int    `json:"max_connections"`
	Priority       *int    `json:"priority"`
	Enabled        *bool   `json:"enabled"`
}

// config validates the body and turns it into a store-ready configuration.
// stored is the row being replaced (nil when there is none): a password the
// body omits is taken from it. The returned message is empty when the body is
// valid.
//
// Port and MaxConnections are resolved to concrete values rather than stored as
// 0, so the settings screen shows the number that will actually be dialled
// instead of an empty box the user has to know the default for.
func (b usenetServerRequest) config(stored *core.UsenetServerConfig) (core.UsenetServerConfig, string) {
	cfg := core.UsenetServerConfig{
		Name:     strings.TrimSpace(b.Name),
		Host:     strings.TrimSpace(b.Host),
		Username: strings.TrimSpace(b.Username),
		Priority: core.UsenetDefaultPriority,
		// TLS defaults on: the password below crosses this socket, and every
		// provider that matters offers implicit TLS on 563.
		TLS:     true,
		Enabled: true,
	}
	if b.TLS != nil {
		cfg.TLS = *b.TLS
	}
	if b.Port != nil {
		cfg.Port = *b.Port
	}
	if b.MaxConnections != nil {
		cfg.MaxConnections = *b.MaxConnections
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

	if cfg.Name == "" {
		return core.UsenetServerConfig{}, "name is required"
	}
	if cfg.Priority < 0 {
		return core.UsenetServerConfig{}, "priority must not be negative"
	}
	// Rejected before resolution so a submitted 0 still means "use the
	// default" while a submitted -1 is the mistake it looks like.
	if cfg.Port < 0 || cfg.Port > 65535 {
		return core.UsenetServerConfig{}, "port must be between 1 and 65535"
	}
	if cfg.MaxConnections < 0 {
		return core.UsenetServerConfig{}, "max connections must not be negative"
	}
	cfg.Port = cfg.ResolvedPort()
	cfg.MaxConnections = cfg.ResolvedMaxConnections()

	// The transport owns the rest: a blank host, a line break smuggled into a
	// credential, a password with no username to send it for. Validating
	// through it keeps one definition of a dialable server rather than a copy
	// here that drifts.
	if err := usenet.ServerConfig(cfg).Validate(); err != nil {
		return core.UsenetServerConfig{}, usenetServerMessage(err)
	}
	return cfg, ""
}

// usenetServerMessage strips the transport's package prefix off an error, so a
// message written for a Go caller reads as one written for the settings form:
// "news server Eweka: host is required" rather than "nntp: news server ...".
//
// Only the prefix is removed. Cutting further would mean parsing a label the
// user chose out of the middle of the string, and everything after the prefix
// is already safe to show: nntp names a server by Label and never formats a
// credential into an error (SPEC §12).
func usenetServerMessage(err error) string {
	return strings.TrimPrefix(err.Error(), "nntp: ")
}

// sameUsenetServerTarget reports whether a test body still names the machine
// stored's password belongs to, comparing the three fields that decide where
// the credential is sent and how it is protected on the way.
//
// TLS is part of the target, not a detail: a body that kept the host and port
// but turned TLS off would put the stored password on the wire in plaintext.
// That is the download-client guard's scheme comparison in its usenet form.
// There, the scheme lives inside the URL being compared.
//
// Port is compared resolved, so a body that round-trips what the GET returned
// still matches, as does one that leaves the port blank for the default.
func sameUsenetServerTarget(body usenetServerRequest, stored core.UsenetServerConfig) bool {
	target := core.UsenetServerConfig{
		Host: strings.TrimSpace(body.Host),
		TLS:  true,
	}
	if body.TLS != nil {
		target.TLS = *body.TLS
	}
	if body.Port != nil {
		target.Port = *body.Port
	}
	return target.Host == stored.Host &&
		target.TLS == stored.TLS &&
		target.ResolvedPort() == stored.ResolvedPort()
}

func (s *server) handleListUsenetServers(w http.ResponseWriter, r *http.Request) {
	configs, err := s.st.ListUsenetServers(r.Context())
	if err != nil {
		s.writeStoreError(w, "list usenet servers", err)
		return
	}

	out := make([]usenetServerJSON, 0, len(configs))
	for _, c := range configs {
		out = append(out, usenetServerDTO(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"usenet_servers": out})
}

func (s *server) handleGetUsenetServer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cfg, err := s.st.GetUsenetServer(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get usenet server", err)
		return
	}
	writeJSON(w, http.StatusOK, usenetServerDTO(*cfg))
}

func (s *server) handleCreateUsenetServer(w http.ResponseWriter, r *http.Request) {
	var body usenetServerRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	cfg, msg := body.config(nil)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	ctx := r.Context()

	if !s.usenetServerNameFree(ctx, w, cfg.Name, 0) {
		return
	}
	if err := s.st.UpsertUsenetServer(ctx, &cfg); err != nil {
		s.writeStoreError(w, "create usenet server", err)
		return
	}
	writeJSON(w, http.StatusCreated, usenetServerDTO(cfg))
}

func (s *server) handleUpdateUsenetServer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body usenetServerRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()

	// Read first, both so updating a server that never existed is a 404 rather
	// than a silent insert, and so an omitted password has a stored value to
	// fall back to.
	stored, err := s.st.GetUsenetServer(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get usenet server", err)
		return
	}
	cfg, msg := body.config(stored)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if !s.usenetServerNameFree(ctx, w, cfg.Name, id) {
		return
	}
	cfg.ID = id
	if err := s.st.UpsertUsenetServer(ctx, &cfg); err != nil {
		s.writeStoreError(w, "update usenet server", err)
		return
	}
	writeJSON(w, http.StatusOK, usenetServerDTO(cfg))
}

func (s *server) handleDeleteUsenetServer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if _, err := s.st.GetUsenetServer(r.Context(), id); err != nil {
		s.writeStoreError(w, "get usenet server", err)
		return
	}
	if err := s.st.DeleteUsenetServer(r.Context(), id); err != nil {
		s.writeStoreError(w, "delete usenet server", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestUsenetServer dials the stored server with the stored credentials,
// the same probe POST /download-clients/{id}/test runs for an external client.
func (s *server) handleTestUsenetServer(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	cfg, err := s.st.GetUsenetServer(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get usenet server", err)
		return
	}
	s.writeUsenetServerTest(w, r.Context(), *cfg)
}

// handleTestUsenetServerConfig probes a configuration from the body rather than
// a stored row: the settings form needs an answer while the user is still
// typing, before the server exists to have an id.
//
// An "id" in the body names the row a blank password falls back to, which is
// what makes Test work while editing a saved server: the GET never returned the
// password, so the form has none to send.
func (s *server) handleTestUsenetServerConfig(w http.ResponseWriter, r *http.Request) {
	var body usenetServerRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	var stored *core.UsenetServerConfig
	if body.ID > 0 {
		found, err := s.st.GetUsenetServer(r.Context(), body.ID)
		if err != nil {
			s.writeStoreError(w, "get usenet server", err)
			return
		}
		// The fallback only applies to the destination the credential was
		// stored for. Pairing a stored password with a host from the body would
		// make this endpoint send the credential the GET refuses to return to
		// any machine the caller names (SPEC §12). The exfiltration the
		// withholding exists to prevent. A genuinely new address is a new
		// credential, and the form still has the field to type it into.
		if sameUsenetServerTarget(body, *found) {
			stored = found
		}
	}
	// The name only labels error messages here; the form may not have one yet.
	if strings.TrimSpace(body.Name) == "" {
		body.Name = "news server"
	}
	cfg, msg := body.config(stored)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	s.writeUsenetServerTest(w, r.Context(), cfg)
}

// usenetTestDialTimeout bounds the whole probe: TCP, TLS, greeting and
// AUTHINFO. It is shorter than the transport's own default because a person is
// watching this one, and a news server that has not said hello in ten seconds
// is not going to.
const usenetTestDialTimeout = 10 * time.Second

// writeUsenetServerTest opens one connection, authenticates if the
// configuration carries credentials, and hangs up.
//
// nntp.Dial does exactly that and no more: it is the same handshake every
// pooled connection performs, so a green test means article fetches will get
// past the front door. Reading an article is deliberately not part of it. There
// is no message id that every provider is guaranteed to still carry, and a
// missing article would report a working server as broken.
//
// The failure carries the server's own message, because "it did not work"
// without a reason is useless for fixing a wrong password or a typo'd port.
// That message is deliberately not logged: SPEC §12 keeps news-server
// credentials out of the logs, and nntp errors name the server by label
// precisely so they can be shown without carrying one.
func (s *server) writeUsenetServerTest(w http.ResponseWriter, ctx context.Context, cfg core.UsenetServerConfig) {
	conn, err := nntp.Dial(ctx, usenet.ServerConfig(cfg), nntp.Options{
		DialTimeout: usenetTestDialTimeout,
	})
	if err != nil {
		writeError(w, http.StatusBadGateway, "usenet server test failed: "+usenetServerMessage(err))
		return
	}
	// Quit closes the connection either way; a server that mishandles the
	// goodbye still answered everything that matters.
	_ = conn.Quit(ctx)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// usenetServerNameFree reports whether name is available, writing a 409 when it
// is not. usenet_servers.name is unique, so without this check a duplicate name
// (a plain user mistake) would surface as a 500.
func (s *server) usenetServerNameFree(ctx context.Context, w http.ResponseWriter, name string, exceptID int64) bool {
	configs, err := s.st.ListUsenetServers(ctx)
	if err != nil {
		s.writeStoreError(w, "list usenet servers", err)
		return false
	}
	for _, c := range configs {
		if c.ID != exceptID && c.Name == name {
			writeError(w, http.StatusConflict, "a usenet server named "+name+" already exists")
			return false
		}
	}
	return true
}
