package sabnzbd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// testKey is a recognisable string so a test can assert it never reaches an
// error message (SPEC §12).
const testKey = "sab-api-key-do-not-log"

// call is one request the fake saw, for asserting on wire format.
type call struct {
	Mode   string
	Name   string
	Params url.Values
}

// fakeSAB is a SABnzbd API good enough to exercise every call Caravan makes.
//
// It reproduces the two behaviours that shape the client: the API key is
// checked per request rather than exchanged for a session, and `mode=version`
// is answered *without* checking it — which is why a connection probe that
// only asks for the version cannot tell a good key from a bad one.
type fakeSAB struct {
	t *testing.T

	mu      sync.Mutex
	version string
	// kbpersec is the queue-wide rate, as SABnzbd formats it: a string.
	kbpersec string
	queue    []QueueSlot
	history  []HistorySlot
	calls    []call
	nextID   int

	// rejectKey makes every key-checked call answer the way SABnzbd refuses a
	// wrong key: HTTP 200 with an error field.
	rejectKey bool
	// addFails makes addurl answer with SABnzbd's bare `status: false`.
	addFails bool
	// onAdd runs after a successful addurl, so a test can decide what the
	// server produces.
	onAdd func(f *fakeSAB, params url.Values, id string)
}

func newFake(t *testing.T) (*fakeSAB, *httptest.Server) {
	t.Helper()
	f := &fakeSAB{
		t:        t,
		version:  "4.3.3",
		kbpersec: "5120.00",
		queue:    loadQueue(t, "queue.json").Slots,
		history:  loadHistory(t, "history.json"),
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

// config returns a client configuration pointed at srv.
func config(srv *httptest.Server) core.DownloadClientConfig {
	return core.DownloadClientConfig{
		ID:      1,
		Type:    core.DownloadClientSABnzbd,
		Name:    "sab",
		URL:     srv.URL,
		APIKey:  testKey,
		Enabled: true,
	}
}

func newClient(t *testing.T) (*Client, *fakeSAB) {
	t.Helper()
	f, srv := newFake(t)
	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, f
}

func newEngine(t *testing.T) (*Engine, *fakeSAB) {
	t.Helper()
	f, srv := newFake(t)
	e, err := NewEngine(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e, f
}

func (f *fakeSAB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !strings.HasSuffix(r.URL.Path, apiPath) {
		http.NotFound(w, r)
		return
	}
	q := r.URL.Query()
	mode, name := q.Get("mode"), q.Get("name")

	f.mu.Lock()
	f.calls = append(f.calls, call{Mode: mode, Name: name, Params: q})
	reject := f.rejectKey
	f.mu.Unlock()

	// `version` is answered without checking the key, exactly as SABnzbd does.
	if mode == "version" {
		f.json(w, map[string]string{"version": f.version})
		return
	}
	switch {
	case q.Get("apikey") == "":
		f.fail(w, "API Key Required")
		return
	case reject || q.Get("apikey") != testKey:
		f.fail(w, "API Key Incorrect")
		return
	}

	switch mode {
	case "queue":
		f.serveQueue(w, name, q)
	case "history":
		f.serveHistory(w, name, q)
	case "addurl":
		f.serveAddURL(w, q)
	default:
		f.fail(w, "not implemented")
	}
}

func (f *fakeSAB) serveQueue(w http.ResponseWriter, name string, q url.Values) {
	f.mu.Lock()
	defer f.mu.Unlock()

	switch name {
	case "pause", "resume":
		id := q.Get("value")
		var hit []string
		for i := range f.queue {
			if f.queue[i].NZOID != id {
				continue
			}
			if name == "pause" {
				f.queue[i].Status = statusPaused
			} else {
				f.queue[i].Status = statusDownloading
			}
			hit = append(hit, id)
		}
		f.json(w, map[string]any{"status": len(hit) > 0, "nzo_ids": hit})
		return
	case "delete":
		id := q.Get("value")
		kept := make([]QueueSlot, 0, len(f.queue))
		var removed []string
		for _, s := range f.queue {
			if s.NZOID == id {
				removed = append(removed, id)
				continue
			}
			kept = append(kept, s)
		}
		f.queue = kept
		f.json(w, map[string]any{"status": len(removed) > 0, "nzo_ids": removed})
		return
	}

	slots := []QueueSlot{}
	for _, s := range f.queue {
		if !matches(q, s.NZOID, s.Category) {
			continue
		}
		if limit := intParam(q.Get("limit")); limit > 0 && len(slots) >= limit {
			break
		}
		slots = append(slots, s)
	}
	f.json(w, map[string]any{"queue": map[string]any{
		"paused":   false,
		"kbpersec": f.kbpersec,
		"status":   "Downloading",
		"slots":    slots,
	}})
}

func (f *fakeSAB) serveHistory(w http.ResponseWriter, name string, q url.Values) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if name == "delete" {
		id := q.Get("value")
		kept := make([]HistorySlot, 0, len(f.history))
		for _, s := range f.history {
			if s.NZOID != id {
				kept = append(kept, s)
			}
		}
		f.history = kept
		f.json(w, map[string]any{"status": true})
		return
	}

	slots := []HistorySlot{}
	for _, s := range f.history {
		if !matches(q, s.NZOID, s.Category) {
			continue
		}
		if limit := intParam(q.Get("limit")); limit > 0 && len(slots) >= limit {
			break
		}
		slots = append(slots, s)
	}
	f.json(w, map[string]any{"history": map[string]any{
		"noofslots": len(slots),
		"slots":     slots,
	}})
}

func (f *fakeSAB) serveAddURL(w http.ResponseWriter, q url.Values) {
	f.mu.Lock()
	if f.addFails || strings.TrimSpace(q.Get("name")) == "" {
		f.mu.Unlock()
		// A refused add is a bare false with no message.
		f.json(w, map[string]any{"status": false, "nzo_ids": []string{}})
		return
	}
	f.nextID++
	id := "SABnzbd_nzo_added" + strconv.Itoa(f.nextID)
	name := q.Get("nzbname")
	if name == "" {
		name = "unnamed"
	}
	// SABnzbd files the job immediately, as Grabbing, under the id it keeps.
	f.queue = append(f.queue, QueueSlot{
		NZOID:    id,
		Filename: name,
		Status:   statusGrabbing,
		Category: q.Get("cat"),
		TimeLeft: "0:00:00",
	})
	onAdd := f.onAdd
	f.mu.Unlock()

	if onAdd != nil {
		onAdd(f, q, id)
	}
	f.json(w, map[string]any{"status": true, "nzo_ids": []string{id}})
}

// matches applies the nzo_ids and cat filters the way SABnzbd does: an absent
// filter does not narrow.
func matches(q url.Values, id, category string) bool {
	if raw := q.Get("nzo_ids"); raw != "" {
		found := false
		for _, want := range strings.Split(raw, ",") {
			if want == id {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	if cat := q.Get("cat"); cat != "" && cat != category {
		return false
	}
	return true
}

func intParam(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return v
}

// fail answers the way SABnzbd reports a refusal: HTTP 200 with an error.
func (f *fakeSAB) fail(w http.ResponseWriter, msg string) {
	f.json(w, map[string]any{"status": false, "error": msg})
}

func (f *fakeSAB) json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json;charset=UTF-8")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		f.t.Errorf("encode fake response: %v", err)
	}
}

// seen returns every recorded call to a mode.
func (f *fakeSAB) seen(mode string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.Mode == mode {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeSAB) queued() []QueueSlot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]QueueSlot(nil), f.queue...)
}

func (f *fakeSAB) historyRows() []HistorySlot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]HistorySlot(nil), f.history...)
}

func loadQueue(t *testing.T, name string) Queue {
	t.Helper()
	var out struct {
		Queue Queue `json:"queue"`
	}
	if err := json.Unmarshal(readFixture(t, name), &out); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return out.Queue
}

func loadHistory(t *testing.T, name string) []HistorySlot {
	t.Helper()
	var out struct {
		History struct {
			Slots []HistorySlot `json:"slots"`
		} `json:"history"`
	}
	if err := json.Unmarshal(readFixture(t, name), &out); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return out.History.Slots
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// fixtureServer answers every request with one fixture, verbatim. It is how a
// test proves the *real* payload decodes — unknown fields, string-typed
// numbers and all — rather than the subset the fake re-encodes.
func fixtureServer(t *testing.T, name string) *httptest.Server {
	t.Helper()
	body := readFixture(t, name)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json;charset=UTF-8")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}
