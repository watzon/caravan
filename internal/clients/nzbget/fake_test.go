package nzbget

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// Credentials every test configures. The password is a recognisable string so
// a test can assert it never reaches an error message (SPEC §12).
const (
	testUser = "nzbget"
	testPass = "tegbzn6789-do-not-log"
)

// call is one RPC the fake saw, for asserting on wire format.
type call struct {
	Method string
	Params []any
	// Auth is the basic-auth username the request carried, empty when it
	// carried none.
	Auth string
}

// fakeNZBGet is an NZBGet JSON-RPC endpoint good enough to exercise every call
// Caravan makes: the basic-auth gate, the positional parameter convention, the
// queue/history split, and the plain `false` NZBGet answers an edit aimed at
// the list an id is not in.
type fakeNZBGet struct {
	t *testing.T

	mu      sync.Mutex
	version string
	rate    int64
	groups  []Group
	history []HistoryItem
	calls   []call
	nextID  int64

	// rejectAuth makes every call answer 401, the way NZBGet refuses a wrong
	// control login.
	rejectAuth bool
	// appendFails makes append answer with NZBGet's bare zero id.
	appendFails bool
	// onAppend runs after a successful append, so a test can decide what the
	// server produces.
	onAppend func(f *fakeNZBGet, params []any, nzbID int64)
}

func newFake(t *testing.T) (*fakeNZBGet, *httptest.Server) {
	t.Helper()
	f := &fakeNZBGet{
		t:       t,
		version: "24.3",
		rate:    5242880,
		groups:  loadGroups(t, "listgroups.json"),
		history: loadHistory(t, "history.json"),
		nextID:  100,
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

// config returns a client configuration pointed at srv.
func config(srv *httptest.Server) core.DownloadClientConfig {
	return core.DownloadClientConfig{
		ID:       1,
		Type:     core.DownloadClientNZBGet,
		Name:     "nzbget",
		URL:      srv.URL,
		Username: testUser,
		Password: testPass,
		Enabled:  true,
	}
}

func newClient(t *testing.T) (*Client, *fakeNZBGet) {
	t.Helper()
	f, srv := newFake(t)
	c, err := New(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, f
}

func newEngine(t *testing.T) (*Engine, *fakeNZBGet) {
	t.Helper()
	f, srv := newFake(t)
	e, err := NewEngine(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	return e, f
}

func (f *fakeNZBGet) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != rpcPath {
		http.NotFound(w, r)
		return
	}
	var req struct {
		Version string `json:"version"`
		Method  string `json:"method"`
		Params  []any  `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	user, pass, _ := r.BasicAuth()

	f.mu.Lock()
	f.calls = append(f.calls, call{Method: req.Method, Params: req.Params, Auth: user})
	reject := f.rejectAuth
	f.mu.Unlock()

	if reject || user != testUser || pass != testPass {
		w.Header().Set("WWW-Authenticate", `Basic realm="NZBGet"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	switch req.Method {
	case "version":
		f.result(w, f.version)
	case "status":
		f.mu.Lock()
		rate := f.rate
		f.mu.Unlock()
		f.result(w, map[string]any{"DownloadRate": rate, "DownloadPaused": false})
	case "listgroups":
		f.mu.Lock()
		groups := append([]Group(nil), f.groups...)
		f.mu.Unlock()
		f.result(w, groups)
	case "history":
		f.mu.Lock()
		history := append([]HistoryItem(nil), f.history...)
		f.mu.Unlock()
		f.result(w, history)
	case "append":
		f.serveAppend(w, req.Params)
	case "editqueue":
		f.serveEditQueue(w, req.Params)
	default:
		f.fault(w, 1, "Invalid method")
	}
}

func (f *fakeNZBGet) serveAppend(w http.ResponseWriter, params []any) {
	// NZBGet reads parameters positionally and by type: nothing here trusts a
	// name, because NZBGet does not send one either.
	if len(params) < 9 {
		f.fault(w, 2, "Invalid parameter")
		return
	}
	filename, _ := params[0].(string)
	content, _ := params[1].(string)
	category, _ := params[2].(string)
	if filename == "" || content == "" {
		f.fault(w, 2, "Invalid parameter (Filename)")
		return
	}
	if _, err := base64.StdEncoding.DecodeString(content); err != nil {
		f.fault(w, 2, "Invalid parameter (Content)")
		return
	}
	if mode, _ := params[8].(string); mode != "score" && mode != "all" && mode != "force" {
		f.fault(w, 2, "Invalid parameter (DupeMode)")
		return
	}

	f.mu.Lock()
	if f.appendFails {
		f.mu.Unlock()
		// A refused append is a zero id with no message.
		f.result(w, 0)
		return
	}
	f.nextID++
	nzbID := f.nextID
	f.groups = append(f.groups, Group{
		NZBID:    nzbID,
		NZBName:  filename,
		Kind:     "NZB",
		Status:   statusQueued,
		Category: category,
		DestDir:  "/downloads/intermediate/" + filename,
	})
	onAppend := f.onAppend
	f.mu.Unlock()

	if onAppend != nil {
		onAppend(f, params, nzbID)
	}
	f.result(w, nzbID)
}

func (f *fakeNZBGet) serveEditQueue(w http.ResponseWriter, params []any) {
	if len(params) < 4 {
		f.fault(w, 2, "Invalid parameter")
		return
	}
	command, _ := params[0].(string)
	ids := map[int64]bool{}
	for _, raw := range params[3:] {
		if n, ok := raw.(float64); ok {
			ids[int64(n)] = true
		}
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	matched := false
	switch command {
	case EditGroupPause, EditGroupResume:
		for i := range f.groups {
			if !ids[f.groups[i].NZBID] {
				continue
			}
			if command == EditGroupPause {
				f.groups[i].Status = statusPaused
			} else {
				f.groups[i].Status = statusDownload
			}
			matched = true
		}
	case EditGroupFinalDelete:
		kept := make([]Group, 0, len(f.groups))
		for _, g := range f.groups {
			if ids[g.NZBID] {
				matched = true
				continue
			}
			kept = append(kept, g)
		}
		f.groups = kept
	case EditHistoryFinalDelete:
		kept := make([]HistoryItem, 0, len(f.history))
		for _, h := range f.history {
			if ids[h.NZBID] {
				matched = true
				continue
			}
			kept = append(kept, h)
		}
		f.history = kept
	default:
		f.fault(w, 3, "Invalid action")
		return
	}
	// NZBGet answers a command that matched nothing with a plain false, which
	// is how a caller learns an id is in the other list.
	f.result(w, matched)
}

func (f *fakeNZBGet) result(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"version": rpcVersion, "result": v}); err != nil {
		f.t.Errorf("encode fake response: %v", err)
	}
}

func (f *fakeNZBGet) fault(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{
		"version": rpcVersion,
		"error":   map[string]any{"name": "JSONRPCError", "code": code, "message": message},
	}); err != nil {
		f.t.Errorf("encode fake fault: %v", err)
	}
}

// seen returns every recorded call to a method.
func (f *fakeNZBGet) seen(method string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeNZBGet) queued() []Group {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Group(nil), f.groups...)
}

func (f *fakeNZBGet) historyRows() []HistoryItem {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]HistoryItem(nil), f.history...)
}

func loadGroups(t *testing.T, name string) []Group {
	t.Helper()
	var out struct {
		Result []Group `json:"result"`
	}
	if err := json.Unmarshal(readFixture(t, name), &out); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return out.Result
}

func loadHistory(t *testing.T, name string) []HistoryItem {
	t.Helper()
	var out struct {
		Result []HistoryItem `json:"result"`
	}
	if err := json.Unmarshal(readFixture(t, name), &out); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return out.Result
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// fixtureServer answers every RPC with one fixture, verbatim. It is how a test
// proves the *real* payload decodes — split 64-bit sizes, unknown fields and
// all — rather than the subset the fake re-encodes.
func fixtureServer(t *testing.T, name string) *httptest.Server {
	t.Helper()
	body := readFixture(t, name)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// nzbBody is a small but well-formed NZB, for the indexer side of Add.
const nzbBody = `<?xml version="1.0" encoding="iso-8859-1" ?>
<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">
  <file poster="poster@example" date="1735600000" subject="Sicario [1/2] - &#34;s.r00&#34; yEnc">
    <groups><group>alt.binaries.example</group></groups>
    <segments><segment bytes="768000" number="1">part1@example</segment></segments>
  </file>
</nzb>`

// indexerServer serves an NZB the way a Newznab indexer does, and records what
// was asked for.
func indexerServer(t *testing.T, body string, status int) (*httptest.Server, *[]string) {
	t.Helper()
	var asked []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		asked = append(asked, r.URL.String())
		w.Header().Set("Content-Type", "application/x-nzb")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, &asked
}
