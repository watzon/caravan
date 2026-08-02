package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// fakeClientProbe records what the download-client test endpoints handed to the
// registry, and answers with whatever the test asked for. No network: the wire
// protocols belong to later tracks.
type fakeClientProbe struct {
	mu   sync.Mutex
	seen []core.DownloadClientConfig
	err  error
}

func (f *fakeClientProbe) test(_ context.Context, cfg core.DownloadClientConfig) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seen = append(f.seen, cfg)
	return f.err
}

func (f *fakeClientProbe) calls() []core.DownloadClientConfig {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]core.DownloadClientConfig(nil), f.seen...)
}

// newDownloadClientServer builds a server whose registry carries a probe for
// qBittorrent only, so the same test file covers both the implemented and the
// not-yet-implemented halves.
func newDownloadClientServer(t *testing.T) (http.Handler, *store.Store, *fakeClientProbe) {
	t.Helper()
	probe := &fakeClientProbe{}
	reg := clients.NewRegistry()
	if err := reg.Register(core.DownloadClientQBittorrent, probe.test); err != nil {
		t.Fatalf("Register: %v", err)
	}
	h, st, _ := newTestServer(t, WithDownloadClients(reg))
	return h, st, probe
}

func TestDownloadClientCRUD(t *testing.T) {
	h, st, _ := newDownloadClientServer(t)
	ctx := context.Background()

	rec := do(t, h, http.MethodGet, "/api/v1/download-clients", "")
	wantStatus(t, rec, http.StatusOK)
	var list struct {
		Clients []downloadClientJSON `json:"download_clients"`
	}
	decodeBody(t, rec, &list)
	if len(list.Clients) != 0 {
		t.Fatalf("download clients = %v, want none on a fresh database", list.Clients)
	}

	rec = do(t, h, http.MethodPost, "/api/v1/download-clients",
		`{"type":"qbittorrent","name":"qBit","url":"http://127.0.0.1:8080/","username":"admin","password":"adminadmin","category":"caravan"}`)
	wantStatus(t, rec, http.StatusCreated)
	var created downloadClientJSON
	decodeBody(t, rec, &created)
	if created.ID == 0 {
		t.Fatalf("created client = %+v, want an assigned id", created)
	}
	if created.URL != "http://127.0.0.1:8080" {
		t.Fatalf("url = %q, want the trailing slash trimmed", created.URL)
	}
	if !created.Enabled {
		t.Fatalf("enabled = false, want an omitted flag to mean enabled")
	}
	if created.Priority != defaultDownloadClientPriority {
		t.Fatalf("priority = %d, want the default %d", created.Priority, defaultDownloadClientPriority)
	}
	if created.Username != "admin" || created.Category != "caravan" {
		t.Fatalf("created client = %+v, want the submitted configuration", created)
	}
	if !created.HasPassword {
		t.Fatalf("has_password = false, want the stored credential reported")
	}

	// The credential itself really is in the database, even though the wire
	// never showed it.
	stored, err := st.GetDownloadClient(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetDownloadClient: %v", err)
	}
	if stored.Password != "adminadmin" {
		t.Fatalf("stored password = %q, want the submitted one", stored.Password)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/download-clients", "")
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &list)
	if len(list.Clients) != 1 || list.Clients[0].Name != "qBit" {
		t.Fatalf("download clients = %+v, want the one just created", list.Clients)
	}

	rec = do(t, h, http.MethodPut, "/api/v1/download-clients/"+itoa(created.ID),
		`{"type":"qbittorrent","name":"qBit","url":"http://nas.local:8080","username":"caravan","priority":5,"enabled":false}`)
	wantStatus(t, rec, http.StatusOK)
	var updated downloadClientJSON
	decodeBody(t, rec, &updated)
	if updated.ID != created.ID || updated.Username != "caravan" || updated.Priority != 5 || updated.Enabled {
		t.Fatalf("updated client = %+v, want the replacement configuration", updated)
	}

	// A disabled client keeps its configuration but drops out of routing.
	enabled, err := st.ListEnabledDownloadClients(ctx)
	if err != nil {
		t.Fatalf("ListEnabledDownloadClients: %v", err)
	}
	if len(enabled) != 0 {
		t.Fatalf("enabled clients = %+v, want none", enabled)
	}

	rec = do(t, h, http.MethodDelete, "/api/v1/download-clients/"+itoa(created.ID), "")
	wantStatus(t, rec, http.StatusNoContent)

	all, err := st.ListDownloadClients(ctx)
	if err != nil {
		t.Fatalf("ListDownloadClients: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("download clients = %+v, want the row deleted", all)
	}
}

// The credential rule: it goes in, it never comes back out, and it is not lost
// by an edit that could not have re-sent it.
func TestDownloadClientCredentialsAreNeverReturned(t *testing.T) {
	h, st, _ := newDownloadClientServer(t)
	ctx := context.Background()

	rec := do(t, h, http.MethodPost, "/api/v1/download-clients",
		`{"type":"sabnzbd","name":"SAB","url":"http://127.0.0.1:8085","api_key":"sab-secret"}`)
	wantStatus(t, rec, http.StatusCreated)
	if strings.Contains(rec.Body.String(), "sab-secret") {
		t.Fatalf("create response %q leaked the API key", rec.Body.String())
	}
	var created downloadClientJSON
	decodeBody(t, rec, &created)
	if !created.HasAPIKey || created.HasPassword {
		t.Fatalf("created = %+v, want has_api_key only", created)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/download-clients", "")
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "sab-secret") {
		t.Fatalf("list response %q leaked the API key", rec.Body.String())
	}
	// Not just absent from the value: the field itself must not exist, or a
	// client would render an empty box over a stored credential.
	if strings.Contains(rec.Body.String(), `"api_key"`) || strings.Contains(rec.Body.String(), `"password"`) {
		t.Fatalf("list response %q carries a credential field", rec.Body.String())
	}

	// An edit that does not re-send the credential keeps it: the form was
	// never given one to re-send.
	rec = do(t, h, http.MethodPut, "/api/v1/download-clients/"+itoa(created.ID),
		`{"type":"sabnzbd","name":"SAB renamed","url":"http://127.0.0.1:8085"}`)
	wantStatus(t, rec, http.StatusOK)
	var updated downloadClientJSON
	decodeBody(t, rec, &updated)
	if !updated.HasAPIKey {
		t.Fatalf("updated = %+v, want the stored API key kept", updated)
	}
	stored, err := st.GetDownloadClient(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetDownloadClient: %v", err)
	}
	if stored.APIKey != "sab-secret" {
		t.Fatalf("stored api key = %q, want the original kept across an edit that omitted it", stored.APIKey)
	}

	// An explicit empty string is the deliberate way to clear one, which then
	// fails validation for a backend that needs it.
	rec = do(t, h, http.MethodPut, "/api/v1/download-clients/"+itoa(created.ID),
		`{"type":"sabnzbd","name":"SAB renamed","url":"http://127.0.0.1:8085","api_key":""}`)
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)

	// Rejected, so the stored credential is untouched.
	stored, err = st.GetDownloadClient(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetDownloadClient: %v", err)
	}
	if stored.APIKey != "sab-secret" {
		t.Fatalf("stored api key = %q, want the rejected request to have changed nothing", stored.APIKey)
	}
}

// Switching a client to a backend that authenticates differently must not
// leave the old credential behind: it can never be used and is never shown.
func TestDownloadClientDropsCredentialsTheTypeCannotUse(t *testing.T) {
	h, st, _ := newDownloadClientServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/download-clients",
		`{"type":"qbittorrent","name":"box","url":"http://127.0.0.1:8080","username":"admin","password":"adminadmin"}`)
	wantStatus(t, rec, http.StatusCreated)
	var created downloadClientJSON
	decodeBody(t, rec, &created)

	rec = do(t, h, http.MethodPut, "/api/v1/download-clients/"+itoa(created.ID),
		`{"type":"sabnzbd","name":"box","url":"http://127.0.0.1:8085","api_key":"k"}`)
	wantStatus(t, rec, http.StatusOK)
	var updated downloadClientJSON
	decodeBody(t, rec, &updated)
	if updated.HasPassword || updated.Username != "" {
		t.Fatalf("updated = %+v, want the login credentials dropped with the type change", updated)
	}

	stored, err := st.GetDownloadClient(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetDownloadClient: %v", err)
	}
	if stored.Password != "" || stored.Username != "" {
		t.Fatalf("stored = %+v, want no orphaned login credential", stored)
	}
}

func TestDownloadClientRequestsAreValidated(t *testing.T) {
	h, st, _ := newDownloadClientServer(t)

	tests := []struct {
		name string
		body string
	}{
		{"no type", `{"name":"a","url":"http://a.example","username":"u"}`},
		{"unknown type", `{"type":"transmission","name":"a","url":"http://a.example"}`},
		{"no name", `{"type":"qbittorrent","url":"http://a.example","username":"u"}`},
		{"blank name", `{"type":"qbittorrent","name":"  ","url":"http://a.example","username":"u"}`},
		{"no url", `{"type":"qbittorrent","name":"a","username":"u"}`},
		{"url without scheme", `{"type":"qbittorrent","name":"a","url":"a.example","username":"u"}`},
		{"url with wrong scheme", `{"type":"qbittorrent","name":"a","url":"ftp://a.example","username":"u"}`},
		{"login client without username", `{"type":"qbittorrent","name":"a","url":"http://a.example"}`},
		{"api key client without key", `{"type":"sabnzbd","name":"a","url":"http://a.example"}`},
		{"negative priority", `{"type":"qbittorrent","name":"a","url":"http://a.example","username":"u","priority":-1}`},
		{"malformed json", `{`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/v1/download-clients", tt.body)
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
		})
	}

	// Nothing was written by any of the rejected requests.
	all, err := st.ListDownloadClients(context.Background())
	if err != nil {
		t.Fatalf("ListDownloadClients: %v", err)
	}
	if len(all) != 0 {
		t.Fatalf("download clients = %+v, want no writes from rejected requests", all)
	}
}

// A duplicate name is a user mistake, not a server failure: download_clients
// .name is unique, and without the pre-check the collision would be a 500.
func TestDownloadClientRejectsDuplicateName(t *testing.T) {
	h, _, _ := newDownloadClientServer(t)

	body := `{"type":"qbittorrent","name":"dup","url":"http://a.example","username":"u"}`
	rec := do(t, h, http.MethodPost, "/api/v1/download-clients", body)
	wantStatus(t, rec, http.StatusCreated)
	var created downloadClientJSON
	decodeBody(t, rec, &created)

	rec = do(t, h, http.MethodPost, "/api/v1/download-clients", body)
	wantStatus(t, rec, http.StatusConflict)
	wantErrorBody(t, rec)

	// Renaming a client to its own name is not a conflict with itself.
	rec = do(t, h, http.MethodPut, "/api/v1/download-clients/"+itoa(created.ID), body)
	wantStatus(t, rec, http.StatusOK)
}

func TestDownloadClientMissingRowIs404(t *testing.T) {
	h, _, _ := newDownloadClientServer(t)

	body := `{"type":"qbittorrent","name":"a","url":"http://a.example","username":"u"}`
	for _, tt := range []struct {
		name, method, target, body string
	}{
		{"update", http.MethodPut, "/api/v1/download-clients/404", body},
		{"delete", http.MethodDelete, "/api/v1/download-clients/404", ""},
		{"test", http.MethodPost, "/api/v1/download-clients/404/test", ""},
		{"test unsaved with a stored id", http.MethodPost, "/api/v1/download-clients/test",
			`{"id":404,"type":"qbittorrent","name":"a","url":"http://a.example","username":"u"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, tt.method, tt.target, tt.body)
			wantStatus(t, rec, http.StatusNotFound)
			wantErrorBody(t, rec)
		})
	}
}

// The stored-row probe: the registry gets the whole configuration, including
// the credential the API never handed back.
func TestTestStoredDownloadClient(t *testing.T) {
	h, st, probe := newDownloadClientServer(t)
	ctx := context.Background()

	cfg := core.DownloadClientConfig{
		Type: core.DownloadClientQBittorrent, Name: "qBit", URL: "http://127.0.0.1:8080",
		Username: "admin", Password: "adminadmin", Enabled: true,
	}
	if err := st.UpsertDownloadClient(ctx, &cfg); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/download-clients/"+itoa(cfg.ID)+"/test", "")
	wantStatus(t, rec, http.StatusOK)
	var body map[string]string
	decodeBody(t, rec, &body)
	if body["status"] != "ok" {
		t.Fatalf("body = %v, want a status of ok", body)
	}
	calls := probe.calls()
	if len(calls) != 1 || calls[0].Password != "adminadmin" || calls[0].URL != "http://127.0.0.1:8080" {
		t.Fatalf("probe saw %+v, want the stored configuration", calls)
	}

	// The client's own complaint survives to the user, as a bad gateway: the
	// request was fine, the far end was not.
	probe.err = errors.New("403 Forbidden")
	rec = do(t, h, http.MethodPost, "/api/v1/download-clients/"+itoa(cfg.ID)+"/test", "")
	wantStatus(t, rec, http.StatusBadGateway)
	var failure errorResponse
	decodeBody(t, rec, &failure)
	if !strings.Contains(failure.Error, "403") {
		t.Fatalf("error = %q, want the client's own failure", failure.Error)
	}
}

// The unsaved probe, which is what the add form uses before there is a row.
func TestTestUnsavedDownloadClientConfig(t *testing.T) {
	h, _, probe := newDownloadClientServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/download-clients/test",
		`{"type":"qbittorrent","url":"http://127.0.0.1:8080","username":"admin","password":"typed"}`)
	wantStatus(t, rec, http.StatusOK)

	calls := probe.calls()
	if len(calls) != 1 || calls[0].Password != "typed" {
		t.Fatalf("probe saw %+v, want the body's configuration", calls)
	}
	// No name yet is fine: the form tests before it is finished.
	if calls[0].Name == "" {
		t.Fatalf("probe saw an unnamed configuration, want a placeholder name")
	}

	rec = do(t, h, http.MethodPost, "/api/v1/download-clients/test",
		`{"type":"qbittorrent","url":"nope","username":"admin"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
	if len(probe.calls()) != 1 {
		t.Fatal("an invalid configuration reached the probe")
	}
}

// Testing while editing a saved client: the form has no password to send back,
// so the id in the body names the row the blank one falls back to.
func TestTestUnsavedConfigFallsBackToStoredCredential(t *testing.T) {
	h, st, probe := newDownloadClientServer(t)

	cfg := core.DownloadClientConfig{
		Type: core.DownloadClientQBittorrent, Name: "qBit", URL: "http://127.0.0.1:8080",
		Username: "admin", Password: "stored-secret", Enabled: true,
	}
	if err := st.UpsertDownloadClient(context.Background(), &cfg); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/download-clients/test",
		`{"id":`+itoa(cfg.ID)+`,"type":"qbittorrent","name":"qBit","url":"http://127.0.0.1:8080","username":"operator"}`)
	wantStatus(t, rec, http.StatusOK)

	calls := probe.calls()
	if len(calls) != 1 {
		t.Fatalf("probe calls = %d, want 1", len(calls))
	}
	if calls[0].Password != "stored-secret" {
		t.Fatalf("probe saw password %q, want the stored one", calls[0].Password)
	}
	// Everything else on screen still wins: only the credential falls back.
	if calls[0].Username != "operator" {
		t.Fatalf("probe saw username %q, want the one on screen", calls[0].Username)
	}
	// Nothing was saved by a test.
	stored, err := st.GetDownloadClient(context.Background(), cfg.ID)
	if err != nil {
		t.Fatalf("GetDownloadClient: %v", err)
	}
	if stored.Username != "admin" {
		t.Fatalf("stored username = %q, want a test to have written nothing", stored.Username)
	}
}

// The fallback is scoped to the machine the credential was stored for.
//
// Without that, POST /download-clients/test is a credential exfiltrator: name a
// saved row by id, give any URL you like, omit the credential, and the server
// sends the stored password (or API key) to the host you named — the qBittorrent
// probe POSTs it to /api/v2/auth/login, the SABnzbd one puts the key in the
// query string. That defeats the whole reason downloadClientJSON withholds the
// credential (SPEC §12), and it is reachable by anything that can reach the API.
func TestTestUnsavedConfigWillNotSendAStoredCredentialToAnotherHost(t *testing.T) {
	h, st, probe := newDownloadClientServer(t)
	ctx := context.Background()

	cfg := core.DownloadClientConfig{
		Type: core.DownloadClientQBittorrent, Name: "qBit", URL: "http://127.0.0.1:8080",
		Username: "admin", Password: "stored-secret", Enabled: true,
	}
	if err := st.UpsertDownloadClient(ctx, &cfg); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/download-clients/test",
		`{"id":`+itoa(cfg.ID)+`,"type":"qbittorrent","name":"qBit","url":"http://attacker.example","username":"admin"}`)
	wantStatus(t, rec, http.StatusOK)

	calls := probe.calls()
	if len(calls) != 1 {
		t.Fatalf("probe calls = %d, want 1", len(calls))
	}
	if calls[0].URL != "http://attacker.example" {
		t.Fatalf("probe saw url %q, want the one from the body", calls[0].URL)
	}
	if calls[0].Password != "" {
		t.Fatalf("the stored password was sent to %q; a credential must never follow a URL it was not stored for", calls[0].URL)
	}
}

// The same scoping by type. Both qBittorrent and NZBGet authenticate with a
// username and password, so without the type check a stored qBittorrent
// password falls back into an NZBGet probe — the credential leaves for a
// backend it was never typed into. The row it was saved on is the only thing
// that says which backend a credential belongs to.
func TestTestUnsavedConfigWillNotSendAStoredCredentialToAnotherType(t *testing.T) {
	probe := &fakeClientProbe{}
	reg := clients.NewRegistry()
	if err := reg.Register(core.DownloadClientNZBGet, probe.test); err != nil {
		t.Fatalf("Register: %v", err)
	}
	h, st, _ := newTestServer(t, WithDownloadClients(reg))
	ctx := context.Background()

	cfg := core.DownloadClientConfig{
		Type: core.DownloadClientQBittorrent, Name: "qBit", URL: "http://127.0.0.1:8080",
		Username: "admin", Password: "stored-secret", Enabled: true,
	}
	if err := st.UpsertDownloadClient(ctx, &cfg); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/download-clients/test",
		`{"id":`+itoa(cfg.ID)+`,"type":"nzbget","name":"x","url":"http://127.0.0.1:8080","username":"nzbget"}`)
	wantStatus(t, rec, http.StatusOK)

	calls := probe.calls()
	if len(calls) != 1 {
		t.Fatalf("probe calls = %d, want 1", len(calls))
	}
	if calls[0].Password != "" {
		t.Fatalf("probe saw password %q, want none: the stored password belongs to a qBittorrent row", calls[0].Password)
	}
}

// A backend nothing has registered yet is configurable and storable; only the
// probe is unavailable, and it says so as 501 rather than blaming the client.
func TestTestDownloadClientWithoutAnImplementation(t *testing.T) {
	h, st, _ := newDownloadClientServer(t)

	cfg := core.DownloadClientConfig{
		Type: core.DownloadClientSABnzbd, Name: "SAB", URL: "http://127.0.0.1:8085",
		APIKey: "k", Enabled: true,
	}
	if err := st.UpsertDownloadClient(context.Background(), &cfg); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/download-clients/"+itoa(cfg.ID)+"/test", "")
	wantStatus(t, rec, http.StatusNotImplemented)
	var failure errorResponse
	decodeBody(t, rec, &failure)
	if !strings.Contains(failure.Error, "SABnzbd") || !strings.Contains(failure.Error, "not supported") {
		t.Fatalf("error = %q, want it to name the backend and say it is not supported yet", failure.Error)
	}
}

// The type list is served rather than hard-coded in the SPA, so the form knows
// which credential fields to render and which backends this build can probe.
func TestListDownloadClientTypes(t *testing.T) {
	h, _, _ := newDownloadClientServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/download-clients/types", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Types []struct {
			Type       string `json:"type"`
			Label      string `json:"label"`
			Protocol   string `json:"protocol"`
			UsesLogin  bool   `json:"uses_login"`
			UsesAPIKey bool   `json:"uses_api_key"`
			Supported  bool   `json:"supported"`
		} `json:"types"`
	}
	decodeBody(t, rec, &body)
	if len(body.Types) != len(clients.Types()) {
		t.Fatalf("types = %+v, want every supported backend", body.Types)
	}

	byName := map[string]bool{}
	for _, ty := range body.Types {
		byName[ty.Type] = ty.Supported
		switch ty.Type {
		case core.DownloadClientQBittorrent:
			if ty.Protocol != core.ProtocolTorrent || !ty.UsesLogin || ty.UsesAPIKey {
				t.Errorf("qbittorrent = %+v, want a torrent backend with a login", ty)
			}
		case core.DownloadClientSABnzbd:
			if ty.Protocol != core.ProtocolUsenet || !ty.UsesAPIKey {
				t.Errorf("sabnzbd = %+v, want a usenet backend with an API key", ty)
			}
		}
	}
	if !byName[core.DownloadClientQBittorrent] {
		t.Error("qbittorrent supported = false, want the registered backend reported as supported")
	}
	if byName[core.DownloadClientSABnzbd] {
		t.Error("sabnzbd supported = true, want an unregistered backend reported as unsupported")
	}
}
