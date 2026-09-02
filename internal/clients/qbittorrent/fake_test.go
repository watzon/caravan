package qbittorrent

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// Credentials every test configures. The password is a recognisable string so
// a test can assert it never reaches an error message (SPEC §12).
const (
	testUser = "caravan"
	testPass = "hunter2-do-not-log"
)

// call is one request the fake saw, for asserting on wire format.
type call struct {
	Method       string
	Path         string
	Form         url.Values
	SID          string
	TorrentFiles []string
}

// fakeQB is a qBittorrent WebUI API v2 good enough to exercise every call
// Caravan makes: the session-cookie flow, the endpoints, and the two
// compatibility shapes older servers have.
type fakeQB struct {
	t *testing.T

	mu       sync.Mutex
	sessions map[string]bool
	nextSID  int
	torrents []Torrent
	files    map[string][]File

	// banned makes login answer 403, the way qBittorrent refuses an address
	// after repeated failures.
	banned bool
	// authBypass makes login succeed without issuing a cookie, the way
	// qBittorrent does for a whitelisted address.
	authBypass bool
	// legacy is a pre-5.0 server: torrents/stop and torrents/start do not
	// exist, torrents/pause and torrents/resume do.
	legacy bool
	// ignoresTagFilter is a pre-2.8.3 server: the `tag` parameter is ignored
	// and torrents/info answers with the whole queue.
	ignoresTagFilter bool

	logins      int
	calls       []call
	addPayloads [][]byte
	// onAdd runs after a successful torrents/add, so a test can decide what
	// the server produces.
	onAdd func(f *fakeQB, form url.Values)
	// rejectsAdd makes torrents/add add nothing and say so the way qBittorrent
	// 5.0 and older do: HTTP 200 with the body "Fails.". That is what a
	// malformed magnet, an unreachable .torrent URL or a duplicate really looks
	// like: not the 400 a wholly missing `urls` parameter gets.
	rejectsAdd bool
}

func newFake(t *testing.T) (*fakeQB, *httptest.Server) {
	t.Helper()
	f := &fakeQB{
		t:        t,
		sessions: map[string]bool{},
		torrents: loadTorrents(t, "torrents_info.json"),
		files:    map[string][]File{},
	}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	return f, srv
}

// config returns a client configuration pointed at srv.
func config(srv *httptest.Server) core.DownloadClientConfig {
	return core.DownloadClientConfig{
		ID:       1,
		Type:     core.DownloadClientQBittorrent,
		Name:     "qbit",
		URL:      srv.URL,
		Username: testUser,
		Password: testPass,
		Enabled:  true,
	}
}

// newEngine returns an engine pointed at a fresh fake, with the add poll tuned
// down so a test that exercises it does not sleep.
func newEngine(t *testing.T) (*Engine, *fakeQB) {
	t.Helper()
	f, srv := newFake(t)
	e, err := NewEngine(config(srv), srv.Client())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	e.addPollInterval = time.Millisecond
	e.addPollAttempts = 5
	return e, f
}

func (f *fakeQB) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	path, ok := strings.CutPrefix(r.URL.Path, apiPath)
	if !ok {
		http.NotFound(w, r)
		return
	}

	if path == "/auth/login" {
		f.serveLogin(w, r)
		return
	}

	sid := ""
	if ck, err := r.Cookie(sessionCookie); err == nil {
		sid = ck.Value
	}

	f.mu.Lock()
	f.calls = append(f.calls, call{Method: r.Method, Path: path, Form: r.Form, SID: sid})
	valid := f.authBypass || f.sessions[sid]
	f.mu.Unlock()

	if !valid {
		// qBittorrent answers every unauthenticated API call with 403.
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	switch path {
	case "/app/webapiVersion":
		f.text(w, "2.11.3")
	case "/torrents/info":
		f.serveInfo(w, r)
	case "/torrents/files":
		f.serveFiles(w, r)
	case "/torrents/add":
		f.serveAdd(w, r)
	case "/torrents/stop", "/torrents/start":
		if f.legacy {
			http.NotFound(w, r)
			return
		}
		f.setStopped(w, r, path == "/torrents/stop")
	case "/torrents/pause", "/torrents/resume":
		if !f.legacy {
			http.NotFound(w, r)
			return
		}
		f.setStopped(w, r, path == "/torrents/pause")
	case "/torrents/delete":
		f.serveDelete(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeQB) serveLogin(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins++
	f.calls = append(f.calls, call{Method: r.Method, Path: "/auth/login", Form: r.Form})

	if f.banned {
		http.Error(w, "Your IP address has been banned after too many failed authentication attempts.", http.StatusForbidden)
		return
	}
	if r.Form.Get("username") != testUser || r.Form.Get("password") != testPass {
		// A rejected login is a 200 with a "Fails." body, not a 4xx.
		f.text(w, "Fails.")
		return
	}
	if f.authBypass {
		f.text(w, "Ok.")
		return
	}
	f.nextSID++
	sid := "sid-" + strconv.Itoa(f.nextSID)
	f.sessions[sid] = true
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: sid, Path: "/"})
	f.text(w, "Ok.")
}

func (f *fakeQB) serveInfo(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	tag := r.Form.Get("tag")
	category := r.Form.Get("category")
	hashes := map[string]bool{}
	if raw := r.Form.Get("hashes"); raw != "" {
		for _, h := range strings.Split(raw, "|") {
			hashes[h] = true
		}
	}

	out := []Torrent{}
	for _, t := range f.torrents {
		if len(hashes) > 0 && !hashes[t.Hash] {
			continue
		}
		if tag != "" && !f.ignoresTagFilter && !hasTag(t.Tags, tag) {
			continue
		}
		if category != "" && t.Category != category {
			continue
		}
		out = append(out, t)
	}
	f.json(w, out)
}

func (f *fakeQB) serveFiles(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	files, ok := f.files[r.Form.Get("hash")]
	if !ok {
		http.NotFound(w, r)
		return
	}
	f.json(w, files)
}

func (f *fakeQB) serveAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	var payload []byte
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		if err := r.ParseMultipartForm(8 << 20); err != nil {
			http.Error(w, "bad multipart form", http.StatusBadRequest)
			return
		}
		files := r.MultipartForm.File["torrents"]
		fileNames := make([]string, 0, len(files))
		for _, file := range files {
			fileNames = append(fileNames, file.Filename)
		}
		form := make(url.Values, len(r.Form))
		for name, values := range r.Form {
			form[name] = append([]string(nil), values...)
		}
		f.mu.Lock()
		for i := len(f.calls) - 1; i >= 0; i-- {
			if f.calls[i].Path == "/torrents/add" {
				f.calls[i].Form = form
				f.calls[i].TorrentFiles = fileNames
				break
			}
		}
		f.mu.Unlock()
		file, _, err := r.FormFile("torrents")
		if err == nil {
			payload, err = io.ReadAll(io.LimitReader(file, core.MaxTorrentPayloadBytes+1))
			_ = file.Close()
		}
		if err != nil {
			http.Error(w, "missing torrent payload", http.StatusBadRequest)
			return
		}
	}
	if strings.TrimSpace(r.Form.Get("urls")) == "" && len(payload) == 0 {
		http.Error(w, "Fails.", http.StatusBadRequest)
		return
	}
	if len(payload) > 0 {
		f.mu.Lock()
		f.addPayloads = append(f.addPayloads, append([]byte(nil), payload...))
		f.mu.Unlock()
	}
	if f.rejectsAdd {
		f.text(w, "Fails.")
		return
	}
	if f.onAdd != nil {
		f.onAdd(f, r.Form)
	}
	f.text(w, "Ok.")
}

func (f *fakeQB) setStopped(w http.ResponseWriter, r *http.Request, stopped bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range strings.Split(r.Form.Get("hashes"), "|") {
		for i := range f.torrents {
			if f.torrents[i].Hash != h {
				continue
			}
			done := f.torrents[i].Progress >= 1
			switch {
			case stopped && done:
				f.torrents[i].State = stateStoppedUP
			case stopped:
				f.torrents[i].State = stateStoppedDL
			case done:
				f.torrents[i].State = stateUploading
			default:
				f.torrents[i].State = stateDownloading
			}
		}
	}
	f.text(w, "")
}

func (f *fakeQB) serveDelete(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	drop := map[string]bool{}
	for _, h := range strings.Split(r.Form.Get("hashes"), "|") {
		drop[h] = true
	}
	kept := f.torrents[:0]
	for _, t := range f.torrents {
		if !drop[t.Hash] {
			kept = append(kept, t)
		}
	}
	f.torrents = kept
	f.text(w, "")
}

func (f *fakeQB) text(w http.ResponseWriter, body string) {
	w.Header().Set("Content-Type", "text/plain; charset=UTF-8")
	_, _ = w.Write([]byte(body))
}

func (f *fakeQB) json(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		f.t.Errorf("encode fake response: %v", err)
	}
}

// expireSessions forgets every issued cookie, the way a qBittorrent restart or
// an idle timeout does.
func (f *fakeQB) expireSessions() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sessions = map[string]bool{}
}

func (f *fakeQB) loginCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins
}

// seen returns every recorded call to path.
func (f *fakeQB) seen(path string) []call {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []call
	for _, c := range f.calls {
		if c.Path == path {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeQB) payloads() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.addPayloads))
	for i := range f.addPayloads {
		out[i] = append([]byte(nil), f.addPayloads[i]...)
	}
	return out
}

// add appends a torrent to the fake's queue.
func (f *fakeQB) add(t Torrent) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.torrents = append(f.torrents, t)
}

func (f *fakeQB) setFiles(hash string, files []File) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.files[hash] = files
}

func loadTorrents(t *testing.T, name string) []Torrent {
	t.Helper()
	var out []Torrent
	if err := json.Unmarshal(readFixture(t, name), &out); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return out
}

func loadFiles(t *testing.T, name string) []File {
	t.Helper()
	var out []File
	if err := json.Unmarshal(readFixture(t, name), &out); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return out
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}
