package sabnzbd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
)

// The fixtures are SABnzbd's real payloads, including the fields Caravan does
// not read and the numbers it sends as strings. Decoding them is the contract
// this package exists to keep.
func TestQueueDecodesTheRealPayload(t *testing.T) {
	srv := fixtureServer(t, "queue.json")
	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	queue, err := c.Queue(context.Background(), Query{})
	if err != nil {
		t.Fatalf("Queue: %v", err)
	}
	if queue.Paused {
		t.Fatalf("queue reported paused")
	}
	// kbpersec arrives as the string "5120.00".
	if queue.KBPerSec.Float() != 5120 {
		t.Fatalf("kbpersec = %v, want 5120", queue.KBPerSec.Float())
	}
	if len(queue.Slots) != 4 {
		t.Fatalf("slots = %d, want 4", len(queue.Slots))
	}

	got := queue.Slots[0]
	want := QueueSlot{
		NZOID:    "SABnzbd_nzo_a1b2c3",
		Filename: "Arrival.2016.1080p.BluRay.x264-GROUP",
		Status:   statusDownloading,
		Category: "caravan-movies",
		MB:       8192,
		MBLeft:   4096,
		TimeLeft: "0:13:39",
		Priority: "Normal",
	}
	if got != want {
		t.Fatalf("slot =\n%+v\nwant\n%+v", got, want)
	}
}

func TestHistoryDecodesTheRealPayload(t *testing.T) {
	srv := fixtureServer(t, "history.json")
	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	slots, err := c.History(context.Background(), Query{})
	if err != nil {
		t.Fatalf("History: %v", err)
	}
	if len(slots) != 4 {
		t.Fatalf("slots = %d, want 4", len(slots))
	}

	done := slots[0]
	if done.NZOID != "SABnzbd_nzo_done01" || done.Status != statusCompleted {
		t.Fatalf("slot = %+v", done)
	}
	if done.Storage != "/downloads/complete/caravan-movies/Sicario.2015.1080p.BluRay.x264-GROUP" {
		t.Fatalf("storage = %q: the completed payload path is the whole point of reading history", done.Storage)
	}
	if done.Bytes.Int() != 9663676416 || done.Downloaded.Int() != 9931964416 {
		t.Fatalf("bytes/downloaded = %d/%d", done.Bytes.Int(), done.Downloaded.Int())
	}

	failed := slots[1]
	if failed.Status != statusFailed || !strings.Contains(failed.FailMessage, "repair blocks") {
		t.Fatalf("failed slot = %+v", failed)
	}
}

// SABnzbd sends the same field as a number in one version and a quoted string
// in another, so both have to decode or an upgrade breaks a working install.
func TestNumberAcceptsBothWireForms(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want float64
		bad  bool
	}{
		{"integer", `12`, 12, false},
		{"float", `12.34`, 12.34, false},
		{"quoted integer", `"12"`, 12, false},
		{"quoted float", `"8192.00"`, 8192, false},
		{"quoted empty", `""`, 0, false},
		{"null", `null`, 0, false},
		{"quoted junk", `"n/a"`, 0, true},
		{"object", `{}`, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var n Number
			err := n.UnmarshalJSON([]byte(tt.in))
			if tt.bad {
				if err == nil {
					t.Fatalf("UnmarshalJSON(%s) accepted junk", tt.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("UnmarshalJSON(%s): %v", tt.in, err)
			}
			if n.Float() != tt.want {
				t.Fatalf("= %v, want %v", n.Float(), tt.want)
			}
		})
	}

	if got := Number(-5).Int(); got != 0 {
		t.Fatalf("Int of a negative = %d, want 0", got)
	}
}

func TestVersion(t *testing.T) {
	c, _ := newClient(t)

	version, err := c.Version(context.Background())
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if version != "4.3.3" {
		t.Fatalf("version = %q", version)
	}
}

func TestEveryCallCarriesTheKeyAndAsksForJSON(t *testing.T) {
	c, f := newClient(t)

	if _, err := c.Queue(context.Background(), Query{}); err != nil {
		t.Fatalf("Queue: %v", err)
	}
	params := f.seen("queue")[0].Params
	if params.Get("apikey") != testKey {
		t.Fatalf("apikey = %q", params.Get("apikey"))
	}
	if params.Get("output") != "json" {
		t.Fatalf("output = %q, want json", params.Get("output"))
	}
}

// A wrong key is an HTTP 200 with an error field, not a 401, so the status code
// alone cannot be trusted.
func TestWrongKeyIsUnauthorizedAndNeverQuoted(t *testing.T) {
	f, srv := newFake(t)
	f.rejectKey = true
	cfg := config(srv)
	cfg.APIKey = "wrong-key-sentinel"
	c, err := New(cfg, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = c.Queue(context.Background(), Query{})
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if !strings.Contains(err.Error(), "API Key Incorrect") {
		t.Fatalf("error = %q, want SABnzbd's own complaint", err.Error())
	}
	if strings.Contains(err.Error(), cfg.APIKey) {
		t.Fatalf("error quoted the credential: %q", err.Error())
	}
}

// SABnzbd refuses everything but `version` when the key is missing entirely.
func TestMissingKeyIsUnauthorized(t *testing.T) {
	_, srv := newFake(t)
	cfg := config(srv)
	cfg.APIKey = ""
	// Type.Validate refuses an empty key, so this exercises the wire path via a
	// client built around it.
	if _, err := New(cfg, srv.Client()); err == nil {
		t.Fatalf("New accepted a configuration with no API key")
	}

	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	c.key = ""
	if _, err := c.Queue(context.Background(), Query{}); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
}

// The API key travels in the query string, and net/http quotes the whole URL
// back in a transport error. Nothing that reaches a user may carry it.
func TestTransportErrorsNeverQuoteTheURL(t *testing.T) {
	_, srv := newFake(t)
	cfg := config(srv)
	c, err := New(cfg, srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.Close()

	_, err = c.Queue(context.Background(), Query{})
	if err == nil {
		t.Fatalf("Queue against a closed server succeeded")
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("transport error quoted the API key: %q", err.Error())
	}
	if strings.Contains(err.Error(), "apikey") {
		t.Fatalf("transport error quoted the request URL: %q", err.Error())
	}
}

func TestNonJSONAnswerIsReportedAsSuch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("<html><body>Sign in</body></html>"))
	}))
	t.Cleanup(srv.Close)

	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.Version(context.Background()); err == nil {
		t.Fatalf("Version accepted an HTML page")
	}
}

func TestHTTPFailureIsAnAPIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Bad Gateway", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.Queue(context.Background(), Query{})
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway {
		t.Fatalf("err = %v, want an APIError with status 502", err)
	}
	if apiErr.Mode != "queue" {
		t.Fatalf("mode = %q, want the failing call", apiErr.Mode)
	}
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("error quoted the credential: %q", err.Error())
	}
}

func TestAddURLSendsTheLinkNameAndCategory(t *testing.T) {
	c, f := newClient(t)

	id, err := c.AddURL(context.Background(), AddRequest{
		URL:      "https://indexer.example/getnzb/abc?apikey=indexer-key",
		Name:     "Sicario.2015.1080p.BluRay-GROUP",
		Category: "caravan-movies",
	})
	if err != nil {
		t.Fatalf("AddURL: %v", err)
	}
	if id == "" {
		t.Fatalf("AddURL returned no nzo_id")
	}

	params := f.seen("addurl")[0].Params
	if params.Get("name") != "https://indexer.example/getnzb/abc?apikey=indexer-key" {
		t.Fatalf("name = %q, want the NZB link", params.Get("name"))
	}
	if params.Get("nzbname") != "Sicario.2015.1080p.BluRay-GROUP" {
		t.Fatalf("nzbname = %q", params.Get("nzbname"))
	}
	if params.Get("cat") != "caravan-movies" {
		t.Fatalf("cat = %q", params.Get("cat"))
	}
}

// A refused add is a bare `status: false` with no message at all, so the
// client has to notice the missing id rather than wait for an error field.
func TestAddURLReportsARefusedLinkWithoutQuotingIt(t *testing.T) {
	c, f := newClient(t)
	f.addFails = true

	link := "https://indexer.example/getnzb/abc?apikey=indexer-key-sentinel"
	_, err := c.AddURL(context.Background(), AddRequest{URL: link})
	if err == nil {
		t.Fatalf("AddURL accepted a link SABnzbd refused")
	}
	if strings.Contains(err.Error(), "indexer-key-sentinel") {
		t.Fatalf("error quoted the indexer's credential: %q", err.Error())
	}
}

func TestPauseResumeAndDeleteSendTheRightParameters(t *testing.T) {
	c, f := newClient(t)
	ctx := context.Background()
	id := "SABnzbd_nzo_a1b2c3"

	if err := c.PauseJob(ctx, id); err != nil {
		t.Fatalf("PauseJob: %v", err)
	}
	if err := c.ResumeJob(ctx, id); err != nil {
		t.Fatalf("ResumeJob: %v", err)
	}
	if err := c.DeleteQueue(ctx, id, true); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}

	calls := f.seen("queue")
	if len(calls) != 3 {
		t.Fatalf("queue calls = %d, want 3", len(calls))
	}
	if calls[0].Name != "pause" || calls[0].Params.Get("value") != id {
		t.Fatalf("pause call = %+v", calls[0])
	}
	if calls[1].Name != "resume" {
		t.Fatalf("resume call = %+v", calls[1])
	}
	if calls[2].Name != "delete" || calls[2].Params.Get("del_files") != "1" {
		t.Fatalf("delete call = %+v", calls[2])
	}

	if err := c.DeleteQueue(ctx, "SABnzbd_nzo_d4e5f6", false); err != nil {
		t.Fatalf("DeleteQueue: %v", err)
	}
	if got := f.seen("queue")[3].Params.Get("del_files"); got != "0" {
		t.Fatalf("del_files = %q, want 0", got)
	}
}

// SABnzbd archives a deleted history row by default, and an archived row still
// answers a lookup — so the download would never leave Caravan's queue.
func TestDeleteHistoryOptsOutOfArchiving(t *testing.T) {
	c, f := newClient(t)

	if err := c.DeleteHistory(context.Background(), "SABnzbd_nzo_done01", true); err != nil {
		t.Fatalf("DeleteHistory: %v", err)
	}
	params := f.seen("history")[0].Params
	if params.Get("archive") != "0" {
		t.Fatalf("archive = %q, want 0", params.Get("archive"))
	}
	if params.Get("del_files") != "1" {
		t.Fatalf("del_files = %q, want 1", params.Get("del_files"))
	}
}

// `mode=history` with no limit falls back to SABnzbd's small display default,
// which would hide a finished download from the import watcher.
func TestHistoryAlwaysSendsALimit(t *testing.T) {
	c, f := newClient(t)

	if _, err := c.History(context.Background(), Query{}); err != nil {
		t.Fatalf("History: %v", err)
	}
	if got := f.seen("history")[0].Params.Get("limit"); got != "100" {
		t.Fatalf("limit = %q, want the package default", got)
	}

	if _, err := c.History(context.Background(), Query{Limit: 5}); err != nil {
		t.Fatalf("History: %v", err)
	}
	if got := f.seen("history")[1].Params.Get("limit"); got != "5" {
		t.Fatalf("limit = %q, want the caller's", got)
	}
}

func TestNewRejectsAnUnusableConfiguration(t *testing.T) {
	tests := []struct {
		name string
		cfg  core.DownloadClientConfig
	}{
		{"no name", core.DownloadClientConfig{URL: "http://localhost:8080", APIKey: "k"}},
		{"no url", core.DownloadClientConfig{Name: "sab", APIKey: "k"}},
		{"url without scheme", core.DownloadClientConfig{Name: "sab", URL: "localhost:8080", APIKey: "k"}},
		{"no api key", core.DownloadClientConfig{Name: "sab", URL: "http://localhost:8080"}},
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

// SABnzbd answers `version` without checking the key, so a probe that stops
// there calls a wrong key reachable. This fails if the queue call is dropped.
func TestTestConnectionCatchesAWrongKey(t *testing.T) {
	f, srv := newFake(t)
	f.rejectKey = true
	cfg := config(srv)
	cfg.APIKey = "wrong-key-sentinel"

	err := TestConnection(context.Background(), cfg)
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if strings.Contains(err.Error(), cfg.APIKey) {
		t.Fatalf("error quoted the credential: %q", err.Error())
	}
	if len(f.seen("version")) == 0 || len(f.seen("queue")) == 0 {
		t.Fatalf("the probe must ask for both the version and the queue")
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
	if strings.Contains(err.Error(), testKey) {
		t.Fatalf("error quoted the credential: %q", err.Error())
	}
}

// Something that answers 200 with no version is not SABnzbd, and saying
// "reachable" would send the user looking for the wrong problem.
func TestTestConnectionRejectsAnEmptyVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":""}`))
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
	if !reg.Supported(core.DownloadClientSABnzbd) {
		t.Fatalf("sabnzbd not supported after Register")
	}
	if err := Register(reg); err == nil {
		t.Fatalf("registering twice succeeded")
	}

	_, srv := newFake(t)
	if err := reg.TestConnection(context.Background(), config(srv)); err != nil {
		t.Fatalf("registry TestConnection: %v", err)
	}
}
