package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/usenet/nntptest"
)

// newUsenetServerFixture starts a fake news server that demands the given
// password, and an API server to point at it. The fake listens on 127.0.0.1
// with a kernel-chosen port, so the test endpoint really dials a socket rather
// than a stub: the probe's whole job is the handshake.
func newUsenetServerFixture(t *testing.T, password string) (http.Handler, *store.Store, *nntptest.Server) {
	t.Helper()
	news, err := nntptest.New(nntptest.Options{
		Username:    "user",
		Password:    password,
		RequireAuth: true,
	})
	if err != nil {
		t.Fatalf("nntptest.New: %v", err)
	}
	t.Cleanup(func() { news.Close() })

	h, st, _ := newTestServer(t)
	return h, st, news
}

func TestUsenetServerCRUD(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	rec := do(t, h, http.MethodGet, "/api/v1/usenet-servers", "")
	wantStatus(t, rec, http.StatusOK)
	var list struct {
		Servers []usenetServerJSON `json:"usenet_servers"`
	}
	decodeBody(t, rec, &list)
	if len(list.Servers) != 0 {
		t.Fatalf("usenet servers = %v, want none on a fresh database", list.Servers)
	}

	rec = do(t, h, http.MethodPost, "/api/v1/usenet-servers",
		`{"name":"  Eweka  ","host":"  news.eweka.nl  ","username":" user ","password":"secret"}`)
	wantStatus(t, rec, http.StatusCreated)
	var created usenetServerJSON
	decodeBody(t, rec, &created)
	if created.ID == 0 {
		t.Fatalf("created server = %+v, want an assigned id", created)
	}
	if created.Name != "Eweka" || created.Host != "news.eweka.nl" || created.Username != "user" {
		t.Fatalf("created server = %+v, want the submitted values trimmed", created)
	}
	// An omitted TLS flag means on, and an omitted port then means 563: the
	// form shows the number that will be dialled, not a blank box.
	if !created.TLS || created.Port != core.UsenetDefaultTLSPort {
		t.Fatalf("created server = %+v, want implicit TLS on the default port", created)
	}
	if created.MaxConnections != core.UsenetDefaultMaxConnections {
		t.Fatalf("max_connections = %d, want the default %d",
			created.MaxConnections, core.UsenetDefaultMaxConnections)
	}
	if created.Priority != core.UsenetDefaultPriority {
		t.Fatalf("priority = %d, want the default %d", created.Priority, core.UsenetDefaultPriority)
	}
	if !created.Enabled {
		t.Fatalf("enabled = false, want an omitted flag to mean enabled")
	}
	if !created.HasPassword {
		t.Fatalf("has_password = false, want the stored credential reported")
	}

	// The credential really is in the database, even though the wire never
	// showed it.
	stored, err := st.GetUsenetServer(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetUsenetServer: %v", err)
	}
	if stored.Password != "secret" {
		t.Fatalf("stored password = %q, want the submitted one", stored.Password)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/usenet-servers/"+itoa(created.ID), "")
	wantStatus(t, rec, http.StatusOK)
	var fetched usenetServerJSON
	decodeBody(t, rec, &fetched)
	if fetched != created {
		t.Fatalf("GET one = %+v, want %+v", fetched, created)
	}

	// Turning TLS off without naming a port moves to the plaintext default.
	rec = do(t, h, http.MethodPut, "/api/v1/usenet-servers/"+itoa(created.ID),
		`{"name":"Eweka","host":"news.eweka.nl","tls":false,"username":"user","max_connections":20,"priority":5,"enabled":false}`)
	wantStatus(t, rec, http.StatusOK)
	var updated usenetServerJSON
	decodeBody(t, rec, &updated)
	if updated.ID != created.ID || updated.TLS || updated.Port != core.UsenetDefaultPort {
		t.Fatalf("updated server = %+v, want plaintext on port %d", updated, core.UsenetDefaultPort)
	}
	if updated.MaxConnections != 20 || updated.Priority != 5 || updated.Enabled {
		t.Fatalf("updated server = %+v, want the replacement configuration", updated)
	}

	// A disabled server keeps its configuration but drops out of the pool.
	enabled, err := st.ListEnabledUsenetServers(ctx)
	if err != nil {
		t.Fatalf("ListEnabledUsenetServers: %v", err)
	}
	if len(enabled) != 0 {
		t.Fatalf("enabled servers = %+v, want none", enabled)
	}

	rec = do(t, h, http.MethodDelete, "/api/v1/usenet-servers/"+itoa(created.ID), "")
	wantStatus(t, rec, http.StatusNoContent)

	all, err := st.ListUsenetServers(ctx)
	if err != nil {
		t.Fatalf("ListUsenetServers: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("usenet servers = %+v, want the row deleted", all)
	}
}

// The credential rule: it goes in, it never comes back out, and it is not lost
// by an edit that could not have re-sent it.
func TestUsenetServerPasswordIsNeverReturned(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	const secret = "hunter2-news-secret"
	rec := do(t, h, http.MethodPost, "/api/v1/usenet-servers",
		`{"name":"Eweka","host":"news.eweka.nl","username":"user","password":"`+secret+`"}`)
	wantStatus(t, rec, http.StatusCreated)
	if strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("create response %q leaked the password", rec.Body.String())
	}
	var created usenetServerJSON
	decodeBody(t, rec, &created)
	if !created.HasPassword {
		t.Fatalf("created = %+v, want has_password", created)
	}

	for _, target := range []string{
		"/api/v1/usenet-servers",
		"/api/v1/usenet-servers/" + itoa(created.ID),
	} {
		rec = do(t, h, http.MethodGet, target, "")
		wantStatus(t, rec, http.StatusOK)
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("GET %s response %q leaked the password", target, rec.Body.String())
		}
		// Not just absent from the value: the field itself must not exist, or a
		// client would render an empty box over a stored credential.
		if strings.Contains(rec.Body.String(), `"password"`) {
			t.Fatalf("GET %s response %q carries a password field", target, rec.Body.String())
		}
	}

	// An edit that does not re-send the password keeps it: the form was never
	// given one to re-send.
	rec = do(t, h, http.MethodPut, "/api/v1/usenet-servers/"+itoa(created.ID),
		`{"name":"Eweka renamed","host":"news.eweka.nl","username":"user"}`)
	wantStatus(t, rec, http.StatusOK)
	var updated usenetServerJSON
	decodeBody(t, rec, &updated)
	if !updated.HasPassword {
		t.Fatalf("updated = %+v, want the stored password kept", updated)
	}
	stored, err := st.GetUsenetServer(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetUsenetServer: %v", err)
	}
	if stored.Password != secret {
		t.Fatalf("stored password = %q, want the original kept across an edit that omitted it", stored.Password)
	}

	// An explicit empty string is the deliberate way to clear one. The server
	// is then anonymous, which is a valid configuration.
	rec = do(t, h, http.MethodPut, "/api/v1/usenet-servers/"+itoa(created.ID),
		`{"name":"Eweka renamed","host":"news.eweka.nl","username":"","password":""}`)
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &updated)
	if updated.HasPassword {
		t.Fatalf("updated = %+v, want an explicit empty password to clear it", updated)
	}
	stored, err = st.GetUsenetServer(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetUsenetServer: %v", err)
	}
	if stored.Password != "" {
		t.Fatalf("stored password = %q, want it cleared", stored.Password)
	}
}

func TestUsenetServerRequestsAreValidated(t *testing.T) {
	h, st, _ := newTestServer(t)

	tests := []struct {
		name string
		body string
	}{
		{"no name", `{"host":"news.example"}`},
		{"blank name", `{"name":"  ","host":"news.example"}`},
		{"no host", `{"name":"a"}`},
		{"blank host", `{"name":"a","host":"   "}`},
		{"negative port", `{"name":"a","host":"news.example","port":-1}`},
		{"port out of range", `{"name":"a","host":"news.example","port":70000}`},
		{"negative max connections", `{"name":"a","host":"news.example","max_connections":-1}`},
		{"negative priority", `{"name":"a","host":"news.example","priority":-1}`},
		{"password without username", `{"name":"a","host":"news.example","password":"p"}`},
		// A credential is interpolated into a command line, so a line break in
		// one would let a stored value inject an NNTP command.
		{"line break in host", `{"name":"a","host":"news.example\r\nQUIT"}`},
		{"line break in username", `{"name":"a","host":"news.example","username":"u\nQUIT","password":"p"}`},
		{"line break in password", `{"name":"a","host":"news.example","username":"u","password":"p\r\nQUIT"}`},
		{"malformed json", `{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/v1/usenet-servers", tt.body)
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
		})
	}

	// Nothing was written by any of the rejected requests.
	all, err := st.ListUsenetServers(context.Background())
	if err != nil {
		t.Fatalf("ListUsenetServers: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("usenet servers = %+v, want no writes from rejected requests", all)
	}
}

// A rejected request must not leak the credential it carried back in the error
// envelope: a validation failure is the easiest place for one to escape.
func TestUsenetServerValidationErrorsHideThePassword(t *testing.T) {
	h, _, _ := newTestServer(t)

	const secret = "leaky-password-value"
	for _, body := range []string{
		`{"name":"a","host":"","password":"` + secret + `"}`,
		`{"name":"a","host":"news.example","password":"` + secret + `"}`,
		`{"name":"a","host":"news.example","username":"u","password":"` + secret + `\r\nQUIT"}`,
	} {
		rec := do(t, h, http.MethodPost, "/api/v1/usenet-servers", body)
		wantStatus(t, rec, http.StatusBadRequest)
		if strings.Contains(rec.Body.String(), secret) {
			t.Fatalf("error response %q leaked the password", rec.Body.String())
		}
	}
}

// A duplicate name is a user mistake, not a server failure: usenet_servers
// .name is unique, and without the pre-check the collision would be a 500.
func TestUsenetServerRejectsDuplicateName(t *testing.T) {
	h, _, _ := newTestServer(t)

	body := `{"name":"dup","host":"news.example","username":"u","password":"p"}`
	rec := do(t, h, http.MethodPost, "/api/v1/usenet-servers", body)
	wantStatus(t, rec, http.StatusCreated)
	var created usenetServerJSON
	decodeBody(t, rec, &created)

	rec = do(t, h, http.MethodPost, "/api/v1/usenet-servers", body)
	wantStatus(t, rec, http.StatusConflict)
	wantErrorBody(t, rec)

	// Renaming a server to its own name is not a conflict with itself.
	rec = do(t, h, http.MethodPut, "/api/v1/usenet-servers/"+itoa(created.ID), body)
	wantStatus(t, rec, http.StatusOK)
}

func TestUsenetServerMissingRowIs404(t *testing.T) {
	h, _, _ := newTestServer(t)

	body := `{"name":"a","host":"news.example","username":"u","password":"p"}`
	for _, tt := range []struct {
		name, method, target, body string
	}{
		{"get", http.MethodGet, "/api/v1/usenet-servers/404", ""},
		{"update", http.MethodPut, "/api/v1/usenet-servers/404", body},
		{"delete", http.MethodDelete, "/api/v1/usenet-servers/404", ""},
		{"test", http.MethodPost, "/api/v1/usenet-servers/404/test", ""},
		{"test unsaved with a stored id", http.MethodPost, "/api/v1/usenet-servers/test",
			`{"id":404,"name":"a","host":"news.example","username":"u","password":"p"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, tt.method, tt.target, tt.body)
			wantStatus(t, rec, http.StatusNotFound)
			wantErrorBody(t, rec)
		})
	}
}

// The stored-row probe: a real socket, a real greeting, and a real AUTHINFO
// with the credential the API never handed back.
func TestTestStoredUsenetServer(t *testing.T) {
	const password = "stored-news-secret"
	h, st, news := newUsenetServerFixture(t, password)
	ctx := context.Background()

	cfg := core.UsenetServerConfig{
		Name: "fake", Host: news.Host(), Port: news.Port(),
		Username: "user", Password: password, Enabled: true,
	}
	if err := st.UpsertUsenetServer(ctx, &cfg); err != nil {
		t.Fatalf("UpsertUsenetServer: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/usenet-servers/"+itoa(cfg.ID)+"/test", "")
	wantStatus(t, rec, http.StatusOK)
	var body map[string]string
	decodeBody(t, rec, &body)
	if body["status"] != "ok" {
		t.Fatalf("body = %v, want a status of ok", body)
	}
	if got := news.Stats().Auths; got != 1 {
		t.Fatalf("server saw %d successful auths, want 1", got)
	}
	// The probe hangs up rather than holding a connection from the account's
	// cap open.
	if cmds := news.Commands(); len(cmds) == 0 || cmds[len(cmds)-1] != "QUIT" {
		t.Fatalf("commands = %v, want the probe to end with QUIT", cmds)
	}
}

// A wrong password has to be reported as a wrong password, and the failure
// must not quote the value that was rejected.
func TestTestStoredUsenetServerRejectsBadCredentials(t *testing.T) {
	h, st, news := newUsenetServerFixture(t, "the-right-password")
	const wrong = "the-wrong-password"

	cfg := core.UsenetServerConfig{
		Name: "fake", Host: news.Host(), Port: news.Port(),
		Username: "user", Password: wrong, Enabled: true,
	}
	if err := st.UpsertUsenetServer(context.Background(), &cfg); err != nil {
		t.Fatalf("UpsertUsenetServer: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/usenet-servers/"+itoa(cfg.ID)+"/test", "")
	wantStatus(t, rec, http.StatusBadGateway)
	var failure errorResponse
	decodeBody(t, rec, &failure)
	if !strings.Contains(failure.Error, "481") {
		t.Fatalf("error = %q, want the server's own refusal", failure.Error)
	}
	if strings.Contains(rec.Body.String(), wrong) {
		t.Fatalf("failure %q leaked the password that was rejected", rec.Body.String())
	}
}

// An unreachable server is a bad gateway with a reason, not a 500.
func TestTestUsenetServerUnreachable(t *testing.T) {
	h, st, news := newUsenetServerFixture(t, "p")

	port := news.Port()
	// Closing the fake frees the port, so the dial is refused rather than
	// hanging until the timeout.
	if err := news.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	cfg := core.UsenetServerConfig{
		Name: "gone", Host: "127.0.0.1", Port: port, Enabled: true,
	}
	if err := st.UpsertUsenetServer(context.Background(), &cfg); err != nil {
		t.Fatalf("UpsertUsenetServer: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/usenet-servers/"+itoa(cfg.ID)+"/test", "")
	wantStatus(t, rec, http.StatusBadGateway)
	wantErrorBody(t, rec)
}

// The unsaved probe, which is what the add form uses before there is a row.
func TestTestUnsavedUsenetServerConfig(t *testing.T) {
	const password = "typed-into-the-form"
	h, st, news := newUsenetServerFixture(t, password)

	rec := do(t, h, http.MethodPost, "/api/v1/usenet-servers/test",
		`{"host":"`+news.Host()+`","port":`+itoa(int64(news.Port()))+`,"tls":false,"username":"user","password":"`+password+`"}`)
	wantStatus(t, rec, http.StatusOK)
	if news.Stats().Auths != 1 {
		t.Fatalf("server saw %d auths, want the typed credential used", news.Stats().Auths)
	}

	// An invalid configuration never reaches the socket.
	rec = do(t, h, http.MethodPost, "/api/v1/usenet-servers/test",
		`{"host":"","username":"user","password":"`+password+`"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)

	// A test writes nothing.
	all, err := st.ListUsenetServers(context.Background())
	if err != nil {
		t.Fatalf("ListUsenetServers: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("usenet servers = %+v, want a test to store nothing", all)
	}
}

// Testing while editing a saved server: the form has no password to send back,
// so the id in the body names the row the blank one falls back to.
func TestTestUnsavedUsenetConfigFallsBackToStoredCredential(t *testing.T) {
	const password = "stored-news-secret"
	h, st, news := newUsenetServerFixture(t, password)

	cfg := core.UsenetServerConfig{
		Name: "fake", Host: news.Host(), Port: news.Port(),
		Username: "user", Password: password, Enabled: true,
	}
	if err := st.UpsertUsenetServer(context.Background(), &cfg); err != nil {
		t.Fatalf("UpsertUsenetServer: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/usenet-servers/test",
		`{"id":`+itoa(cfg.ID)+`,"name":"fake","host":"`+news.Host()+`","port":`+itoa(int64(news.Port()))+`,"tls":false,"username":"user"}`)
	wantStatus(t, rec, http.StatusOK)
	if news.Stats().Auths != 1 {
		t.Fatalf("server saw %d auths, want the stored credential used for the blank field", news.Stats().Auths)
	}
}

// The guard that makes the fallback safe. A stored password may only be paired
// with the machine it was stored for: without this, POST /usenet-servers/test
// would hand the credential the GET refuses to return to any host the caller
// names (SPEC §12).
func TestTestUnsavedUsenetConfigDoesNotSendStoredCredentialElsewhere(t *testing.T) {
	const password = "stored-news-secret"
	h, st, victim := newUsenetServerFixture(t, password)

	// The row whose password must not travel.
	cfg := core.UsenetServerConfig{
		Name: "mine", Host: victim.Host(), Port: victim.Port(),
		Username: "user", Password: password, Enabled: true,
	}
	if err := st.UpsertUsenetServer(context.Background(), &cfg); err != nil {
		t.Fatalf("UpsertUsenetServer: %v", err)
	}

	// The attacker's server: it accepts any password, so if the stored one
	// arrives the probe succeeds and the credential is out.
	attacker, err := nntptest.New(nntptest.Options{Username: "user", Password: password, RequireAuth: true})
	if err != nil {
		t.Fatalf("nntptest.New: %v", err)
	}
	defer attacker.Close()

	tests := []struct {
		name string
		// probed is the server the redirected request actually lands on, whose
		// auth counter must not move.
		probed *nntptest.Server
		body   string
	}{
		{
			// Same host, another listener: the port alone is enough to make it
			// a different destination.
			name:   "different port",
			probed: attacker,
			body: `{"id":` + itoa(cfg.ID) + `,"name":"mine","host":"` + victim.Host() + `","port":` +
				itoa(int64(attacker.Port())) + `,"tls":false,"username":"user"}`,
		},
		{
			// Same listener, a different host string. The stored credential
			// belongs to the name that was stored, so a rewritten host is a new
			// destination even when it happens to resolve to the same machine —
			// the comparison cannot depend on resolving anything.
			name:   "different host",
			probed: victim,
			body: `{"id":` + itoa(cfg.ID) + `,"name":"mine","host":"localhost","port":` +
				itoa(int64(victim.Port())) + `,"tls":false,"username":"user"}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := tt.probed.Stats().Auths
			rec := do(t, h, http.MethodPost, "/api/v1/usenet-servers/test", tt.body)
			// No password to send, so the far end refuses: a 502, never a 200.
			wantStatus(t, rec, http.StatusBadGateway)
			if got := tt.probed.Stats().Auths; got != before {
				t.Fatalf("the redirected server authenticated %d more times, want the stored credential withheld", got-before)
			}
			if strings.Contains(rec.Body.String(), password) {
				t.Fatalf("response %q leaked the stored password", rec.Body.String())
			}
		})
	}
}

// Turning TLS off is a change of target, not a detail: keeping the host and
// port but dropping TLS would put the stored password on the wire in
// plaintext, which is the same exfiltration in a different shape.
func TestTestUnsavedUsenetConfigTreatsTLSAsPartOfTheTarget(t *testing.T) {
	const password = "stored-news-secret"
	h, st, news := newUsenetServerFixture(t, password)

	// Stored as a TLS server on the fake's port. The fake speaks plaintext, so
	// a probe that honoured the stored TLS flag could never succeed anyway —
	// what is under test is whether the password travels at all.
	cfg := core.UsenetServerConfig{
		Name: "mine", Host: news.Host(), Port: news.Port(), TLS: true,
		Username: "user", Password: password, Enabled: true,
	}
	if err := st.UpsertUsenetServer(context.Background(), &cfg); err != nil {
		t.Fatalf("UpsertUsenetServer: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/usenet-servers/test",
		`{"id":`+itoa(cfg.ID)+`,"name":"mine","host":"`+news.Host()+`","port":`+
			itoa(int64(news.Port()))+`,"tls":false,"username":"user"}`)
	wantStatus(t, rec, http.StatusBadGateway)
	if got := news.Stats().Auths; got != 0 {
		t.Fatalf("server authenticated %d times, want the stored credential withheld from a downgraded target", got)
	}
	if strings.Contains(rec.Body.String(), password) {
		t.Fatalf("response %q leaked the stored password", rec.Body.String())
	}
}
