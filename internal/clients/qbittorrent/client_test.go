package qbittorrent

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
)

func newClient(t *testing.T) (*Client, *fakeQB) {
	t.Helper()
	f, srv := newFake(t)
	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, f
}

// The login handshake is the whole reason this client is not three lines of
// http.Get: every call needs a cookie, the cookie expires, and a rejected
// login is a 200.
func TestLoginIssuesOneSessionAndReusesIt(t *testing.T) {
	c, f := newClient(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if _, err := c.WebAPIVersion(ctx); err != nil {
			t.Fatalf("WebAPIVersion: %v", err)
		}
	}
	if got := f.loginCount(); got != 1 {
		t.Fatalf("logins = %d, want 1 (session must be reused)", got)
	}

	calls := f.seen("/app/webapiVersion")
	if len(calls) != 3 {
		t.Fatalf("version calls = %d, want 3", len(calls))
	}
	for i, call := range calls {
		if call.SID == "" {
			t.Fatalf("call %d carried no %s cookie", i, sessionCookie)
		}
	}
}

func TestExpiredSessionIsReplacedAndTheCallReplayed(t *testing.T) {
	c, f := newClient(t)
	ctx := context.Background()

	if _, err := c.WebAPIVersion(ctx); err != nil {
		t.Fatalf("WebAPIVersion: %v", err)
	}
	f.expireSessions()

	version, err := c.WebAPIVersion(ctx)
	if err != nil {
		t.Fatalf("WebAPIVersion after expiry: %v", err)
	}
	if version != "2.11.3" {
		t.Fatalf("version = %q, want %q", version, "2.11.3")
	}
	if got := f.loginCount(); got != 2 {
		t.Fatalf("logins = %d, want 2 (one re-login after the session expired)", got)
	}
}

// A server that hands out cookies it never honours must fail, not spin: the
// re-login retry happens exactly once.
func TestPersistentlyForbiddenRequestFailsAfterOneRetry(t *testing.T) {
	var logins, probes int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/auth/login") {
			logins++
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "stale"})
			_, _ = w.Write([]byte("Ok."))
			return
		}
		probes++
		http.Error(w, "Forbidden", http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = c.WebAPIVersion(context.Background())
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusForbidden {
		t.Fatalf("err = %v, want an APIError with status 403", err)
	}
	if logins != 2 || probes != 2 {
		t.Fatalf("logins = %d, probes = %d, want 2 and 2 (retry exactly once)", logins, probes)
	}
}

func TestWrongPasswordIsUnauthorizedAndNeverQuoted(t *testing.T) {
	f, srv := newFake(t)
	cfg := config(srv)
	cfg.Password = "wrong-password-sentinel"
	c, err := New(cfg, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = c.WebAPIVersion(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(err.Error(), cfg.Password) {
		t.Fatalf("error quoted the credential: %q", err.Error())
	}
	if got := f.loginCount(); got != 1 {
		t.Fatalf("logins = %d, want 1 (a rejected login must not be retried)", got)
	}
}

func TestBannedAddressIsUnauthorizedAndSaysSo(t *testing.T) {
	f, srv := newFake(t)
	f.banned = true
	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = c.WebAPIVersion(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if !strings.Contains(err.Error(), "banned") {
		t.Fatalf("error = %q, want it to name the ban", err.Error())
	}
	if strings.Contains(err.Error(), testPass) {
		t.Fatalf("error quoted the credential: %q", err.Error())
	}
}

// qBittorrent can be configured to skip authentication for an address; it then
// answers "Ok." and sets no cookie, and every later call must still work.
func TestAuthBypassNeedsNoCookie(t *testing.T) {
	f, srv := newFake(t)
	f.authBypass = true
	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := c.WebAPIVersion(context.Background()); err != nil {
		t.Fatalf("WebAPIVersion: %v", err)
	}
	if got := f.loginCount(); got != 1 {
		t.Fatalf("logins = %d, want 1", got)
	}
}

func TestLoginSendsRefererForTheCSRFCheck(t *testing.T) {
	f, _ := newFake(t)
	var referer string
	guard := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == apiPath+"/auth/login" {
			referer = r.Header.Get("Referer")
		}
		f.ServeHTTP(w, r)
	})
	proxy := httptest.NewServer(guard)
	t.Cleanup(proxy.Close)

	c, err := New(config(proxy), proxy.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.WebAPIVersion(context.Background()); err != nil {
		t.Fatalf("WebAPIVersion: %v", err)
	}
	if referer != proxy.URL {
		t.Fatalf("Referer = %q, want %q", referer, proxy.URL)
	}
}

func TestAddSendsURLCategoryAndTag(t *testing.T) {
	c, f := newClient(t)

	err := c.Add(context.Background(), AddRequest{
		URL:      "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		Category: "caravan-movies",
		Tags:     []string{Tag},
	})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	calls := f.seen("/torrents/add")
	if len(calls) != 1 {
		t.Fatalf("add calls = %d, want 1", len(calls))
	}
	form := calls[0].Form
	if calls[0].Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", calls[0].Method)
	}
	if got := form.Get("urls"); !strings.HasPrefix(got, "magnet:?xt=urn:btih:") {
		t.Fatalf("urls = %q", got)
	}
	if got := form.Get("category"); got != "caravan-movies" {
		t.Fatalf("category = %q, want caravan-movies", got)
	}
	if got := form.Get("tags"); got != Tag {
		t.Fatalf("tags = %q, want %q", got, Tag)
	}
	if _, ok := form["savepath"]; ok {
		t.Fatalf("savepath sent: qBittorrent's own configuration decides where it writes")
	}
}

func TestAddRequiresExactlyOneTorrentSource(t *testing.T) {
	client, fake := newClient(t)
	for name, request := range map[string]AddRequest{
		"neither": {},
		"both":    {URL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567", Payload: []byte("payload")},
	} {
		t.Run(name, func(t *testing.T) {
			if err := client.Add(context.Background(), request); err == nil {
				t.Fatal("Add succeeded")
			}
		})
	}
	if calls := fake.seen("/torrents/add"); len(calls) != 0 {
		t.Fatalf("qBittorrent add calls = %d, want 0", len(calls))
	}
}

func TestAddTorrentPayloadReloginsAndReplaysMultipartBody(t *testing.T) {
	client, fake := newClient(t)
	if _, err := client.WebAPIVersion(context.Background()); err != nil {
		t.Fatalf("WebAPIVersion: %v", err)
	}
	fake.expireSessions()
	payload := []byte("replayable torrent payload")
	if err := client.Add(context.Background(), AddRequest{Payload: payload, Tags: []string{Tag}}); err != nil {
		t.Fatalf("Add: %v", err)
	}
	if got := fake.loginCount(); got != 2 {
		t.Fatalf("login count = %d, want initial login plus one retry", got)
	}
	if calls := fake.seen("/torrents/add"); len(calls) != 2 {
		t.Fatalf("qBittorrent add attempts = %d, want 2", len(calls))
	}
	payloads := fake.payloads()
	if len(payloads) != 1 || !bytes.Equal(payloads[0], payload) {
		t.Fatalf("uploaded payloads = %q", payloads)
	}
}

func TestInfoDecodesTheRealPayload(t *testing.T) {
	c, _ := newClient(t)

	torrents, err := c.Info(context.Background(), InfoQuery{Tag: Tag})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if len(torrents) != 3 {
		t.Fatalf("torrents = %d, want the 3 tagged ones", len(torrents))
	}
	got := torrents[0]
	want := Torrent{
		Hash:        "0123456789abcdef0123456789abcdef01234567",
		Name:        "Arrival.2016.1080p.BluRay.x264-GROUP",
		State:       stateDownloading,
		Progress:    0.5,
		DlSpeed:     5242880,
		ETA:         819,
		Size:        8589934592,
		TotalSize:   8589934592,
		Completed:   4294967296,
		AmountLeft:  4294967296,
		SavePath:    "/downloads",
		ContentPath: "/downloads/Arrival.2016.1080p.BluRay.x264-GROUP",
		Category:    "caravan-movies",
		Tags:        "caravan",
		AddedOn:     1735689600,
	}
	if got != want {
		t.Fatalf("torrent =\n%+v\nwant\n%+v", got, want)
	}
}

func TestInfoFiltersByHashes(t *testing.T) {
	c, f := newClient(t)

	hash := "89abcdef0123456789abcdef0123456789abcdef"
	torrents, err := c.Info(context.Background(), InfoQuery{Hashes: []string{hash}})
	if err != nil {
		t.Fatalf("Info: %v", err)
	}
	if len(torrents) != 1 || torrents[0].Hash != hash {
		t.Fatalf("torrents = %+v, want just %s", torrents, hash)
	}
	if got := f.seen("/torrents/info")[0].Form.Get("hashes"); got != hash {
		t.Fatalf("hashes param = %q, want %q", got, hash)
	}
}

func TestFilesDecodesTheRealPayload(t *testing.T) {
	c, f := newClient(t)
	hash := "0123456789abcdef0123456789abcdef01234567"
	f.setFiles(hash, loadFiles(t, "torrents_files.json"))

	files, err := c.Files(context.Background(), hash)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}
	if files[0].Name != "Arrival.2016.1080p.BluRay.x264-GROUP/Arrival.2016.1080p.BluRay.x264-GROUP.mkv" {
		t.Fatalf("name = %q", files[0].Name)
	}
	if files[0].Size != 8589000000 || files[1].Priority != 0 {
		t.Fatalf("files = %+v", files)
	}
}

func TestDeleteSendsHashesAndDeleteFiles(t *testing.T) {
	c, f := newClient(t)
	hash := "1111111111111111111111111111111111111111"

	if err := c.Delete(context.Background(), true, hash); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	form := f.seen("/torrents/delete")[0].Form
	if form.Get("hashes") != hash {
		t.Fatalf("hashes = %q", form.Get("hashes"))
	}
	if form.Get("deleteFiles") != "true" {
		t.Fatalf("deleteFiles = %q, want true", form.Get("deleteFiles"))
	}

	if err := c.Delete(context.Background(), false, hash); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := f.seen("/torrents/delete")[1].Form.Get("deleteFiles"); got != "false" {
		t.Fatalf("deleteFiles = %q, want false", got)
	}
}

// qBittorrent 5.0 renamed pause/resume to stop/start. A 4.x server answers the
// new names with a 404, and Caravan has to keep working against it.
func TestStopStartFallBackToPauseResumeOnOlderServers(t *testing.T) {
	f, srv := newFake(t)
	f.legacy = true
	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx := context.Background()
	hash := "0123456789abcdef0123456789abcdef01234567"

	if err := c.Stop(ctx, hash); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := len(f.seen("/torrents/pause")); got != 1 {
		t.Fatalf("pause calls = %d, want 1", got)
	}

	// The fallback is remembered: the second action must not probe the modern
	// endpoint again.
	if err := c.Start(ctx, hash); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if got := len(f.seen("/torrents/start")); got != 0 {
		t.Fatalf("start calls = %d, want 0 once the server is known to be legacy", got)
	}
	if got := len(f.seen("/torrents/resume")); got != 1 {
		t.Fatalf("resume calls = %d, want 1", got)
	}
	if got := len(f.seen("/torrents/stop")); got != 1 {
		t.Fatalf("stop calls = %d, want the single probe", got)
	}
}

func TestStopUsesTheModernEndpointOnCurrentServers(t *testing.T) {
	c, f := newClient(t)
	hash := "0123456789abcdef0123456789abcdef01234567"

	if err := c.Stop(context.Background(), hash); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if got := len(f.seen("/torrents/stop")); got != 1 {
		t.Fatalf("stop calls = %d, want 1", got)
	}
	if got := len(f.seen("/torrents/pause")); got != 0 {
		t.Fatalf("pause calls = %d, want 0", got)
	}
}

func TestNewRejectsAnUnusableConfiguration(t *testing.T) {
	tests := []struct {
		name string
		cfg  core.DownloadClientConfig
	}{
		{"no name", core.DownloadClientConfig{URL: "http://localhost:8080", Username: "u"}},
		{"no url", core.DownloadClientConfig{Name: "q", Username: "u"}},
		{"url without scheme", core.DownloadClientConfig{Name: "q", URL: "localhost:8080", Username: "u"}},
		{"no username", core.DownloadClientConfig{Name: "q", URL: "http://localhost:8080"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(tt.cfg, nil); err == nil {
				t.Fatalf("New accepted %+v", tt.cfg)
			}
		})
	}
}

func TestTestConnectionSucceeds(t *testing.T) {
	_, srv := newFake(t)
	if err := TestConnection(context.Background(), config(srv)); err != nil {
		t.Fatalf("TestConnection: %v", err)
	}
}

func TestTestConnectionReportsAuthFailureWithoutTheCredential(t *testing.T) {
	_, srv := newFake(t)
	cfg := config(srv)
	cfg.Password = "wrong-password-sentinel"

	err := TestConnection(context.Background(), cfg)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(err.Error(), cfg.Password) {
		t.Fatalf("error quoted the credential: %q", err.Error())
	}
}

func TestTestConnectionReportsAnUnreachableClient(t *testing.T) {
	_, srv := newFake(t)
	cfg := config(srv)
	srv.Close()

	err := TestConnection(context.Background(), cfg)
	if err == nil {
		t.Fatalf("TestConnection against a closed server succeeded")
	}
	if strings.Contains(err.Error(), testPass) {
		t.Fatalf("error quoted the credential: %q", err.Error())
	}
}

// Something that answers 200 with nothing is not qBittorrent, and saying
// "reachable" would send the user looking for the wrong problem.
func TestTestConnectionRejectsAnEmptyVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/auth/login") {
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "sid"})
			_, _ = w.Write([]byte("Ok."))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	if err := TestConnection(context.Background(), config(srv)); err == nil {
		t.Fatalf("TestConnection accepted a server with no version")
	}
}

func TestRegisterInstallsTheProbe(t *testing.T) {
	reg := clients.NewRegistry()
	if err := Register(reg); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if !reg.Supported(core.DownloadClientQBittorrent) {
		t.Fatalf("qbittorrent not supported after Register")
	}
	if err := Register(reg); err == nil {
		t.Fatalf("registering twice succeeded")
	}

	_, srv := newFake(t)
	if err := reg.TestConnection(context.Background(), config(srv)); err != nil {
		t.Fatalf("registry TestConnection: %v", err)
	}
}
