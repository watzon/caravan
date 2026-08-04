package usenet

import (
	"context"
	"errors"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/usenet/nntp"
	"github.com/watzon/caravan/internal/usenet/nntptest"
	"github.com/watzon/caravan/internal/usenet/pipeline"
	"github.com/watzon/caravan/internal/usenet/yenc"
)

// ---------------------------------------------------------------------------
// scaffolding
// ---------------------------------------------------------------------------

// release is one staged posting: the files, the fake news server holding their
// articles, and the NZB that indexes them.
type release struct {
	nzb   []byte
	files map[string][]byte
}

// stage posts every file to srv as yEnc articles and builds the NZB that
// indexes them, exactly as an indexer would hand one out.
func stage(t *testing.T, srv *nntptest.Server, files map[string][]byte, partSize int) release {
	t.Helper()

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	b.WriteString(`<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">` + "\n")

	// Sorted, so the document is byte-identical across runs and the handle a
	// test computes for it is stable.
	for _, name := range sortedKeys(files) {
		data := files[name]
		ids, err := yenc.Publish(srv.Add, name, data, partSize)
		if err != nil {
			t.Fatalf("publish %s: %v", name, err)
		}
		subject := fmt.Sprintf(`Caravan Test [1/1] - "%s" yEnc (1/%d)`, name, len(ids))
		fmt.Fprintf(&b, "  <file poster=\"tester@caravan.invalid\" date=\"1700000000\" subject=\"%s\">\n",
			html.EscapeString(subject))
		b.WriteString("    <groups><group>alt.binaries.test</group></groups>\n")
		b.WriteString("    <segments>\n")
		for i, id := range ids {
			// The encoded size the NZB advertises. Real ones are approximate;
			// the pipeline only uses it for scheduling and the preflight.
			size := len(data)/len(ids) + 128
			fmt.Fprintf(&b, "      <segment bytes=\"%d\" number=\"%d\">%s</segment>\n", size, i+1, id)
		}
		b.WriteString("    </segments>\n  </file>\n")
	}
	b.WriteString("</nzb>\n")
	return release{nzb: []byte(b.String()), files: files}
}

func sortedKeys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// serveNZB publishes the document over HTTP, which is where a real engine
// fetches it from.
func serveNZB(t *testing.T, doc []byte) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-nzb")
		w.Write(doc)
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/release.nzb"
}

// memStore is download.Persistence in a map, so the engine's restart behaviour
// is testable without a database.
type memStore struct {
	mu   sync.Mutex
	rows map[core.DownloadID]core.Download
}

func newMemStore() *memStore { return &memStore{rows: map[core.DownloadID]core.Download{}} }

func (m *memStore) Save(ctx context.Context, d core.Download) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rows[d.EngineID] = d
	return nil
}

func (m *memStore) Load(ctx context.Context) ([]core.Download, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]core.Download, 0, len(m.rows))
	for _, d := range m.rows {
		out = append(out, d)
	}
	return out, nil
}

func (m *memStore) Delete(ctx context.Context, id core.DownloadID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.rows, id)
	return nil
}

func (m *memStore) get(id core.DownloadID) (core.Download, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.rows[id]
	return d, ok
}

// newTestEngine starts an engine pointed at srv, under a fresh storage root.
func newTestEngine(t *testing.T, srv *nntptest.Server, store download.Persistence) (*Engine, string) {
	t.Helper()
	root := t.TempDir()
	return newTestEngineAt(t, root, srv, store), root
}

// newTestEngineAt is newTestEngine over an existing storage root, which is what
// a restart looks like: the same directory, the same sidecars, a new engine.
func newTestEngineAt(t *testing.T, root string, srv *nntptest.Server, store download.Persistence) *Engine {
	t.Helper()
	opts := EngineOpts{
		Servers:      []nntp.ServerConfig{testServer(srv)},
		NNTP:         fastRetry(),
		Store:        store,
		PollInterval: 10 * time.Millisecond,
		// The preflight measures the real filesystem, and these releases are
		// kilobytes; skipping it keeps the tests off the CI disk's mood.
		SkipSpaceCheck: true,
	}
	e, err := NewEngine(root, opts)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

// fastRetry keeps the transport's failure paths honest but instant: the retry
// policy is exercised, the wall-clock backoff is not.
func fastRetry() nntp.Options {
	return nntp.Options{
		Retry: nntp.Retry{Attempts: 2, Base: -1},
		Sleep: func(context.Context, time.Duration) error { return nil },
	}
}

func testServer(srv *nntptest.Server) nntp.ServerConfig {
	return nntp.ServerConfig{
		ID: 1, Name: "fake", Host: srv.Host(), Port: srv.Port(),
		MaxConnections: 2, Enabled: true,
	}
}

func startFakeNNTP(t *testing.T) *nntptest.Server {
	t.Helper()
	srv, err := nntptest.New(nntptest.Options{})
	if err != nil {
		t.Fatalf("nntptest.New: %v", err)
	}
	t.Cleanup(func() { srv.Close() })
	return srv
}

// waitFor polls until cond holds, which is how a test observes an engine that
// does its work on its own goroutines.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func waitForState(t *testing.T, e *Engine, id core.DownloadID, want core.DownloadState) core.DownloadStatus {
	t.Helper()
	var last core.DownloadStatus
	waitFor(t, fmt.Sprintf("download %s to reach %s (last %s: %q)", id, want, last.State, last.Error), func() bool {
		st, err := e.Status(context.Background(), id)
		if err != nil {
			return false
		}
		last = *st
		return st.State == want
	})
	return last
}

func addRelease(t *testing.T, e *Engine, rel release, title string) core.DownloadID {
	t.Helper()
	id, err := e.Add(context.Background(), core.Release{
		Title:       title,
		Protocol:    core.ProtocolUsenet,
		DownloadURL: serveNZB(t, rel.nzb),
	}, core.AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return id
}

// ---------------------------------------------------------------------------
// tests
// ---------------------------------------------------------------------------

// The whole point of the engine: an NZB URL in, assembled files on disk, a
// completed download the import watcher will pick up.
func TestEngineDownloadsAnNZBToCompletion(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	payload := []byte(strings.Repeat("caravan usenet payload\n", 400))
	rel := stage(t, nntpSrv, map[string][]byte{"movie.mkv": payload}, 500)

	store := newMemStore()
	e, root := newTestEngine(t, nntpSrv, store)

	id := addRelease(t, e, rel, "Some.Release.2024.1080p-GRP")
	st := waitForState(t, e, id, core.DownloadCompleted)

	if st.Progress != 1 {
		t.Errorf("progress = %v, want 1", st.Progress)
	}
	if st.Phase != "" {
		t.Errorf("phase = %q, want none once complete", st.Phase)
	}
	// SavePath is storage-root-relative, which is the whole of SPEC §1.2
	// pillar 3 as far as the import pipeline is concerned.
	if filepath.IsAbs(st.SavePath) {
		t.Errorf("save path %q is absolute; it must be relative to the storage root", st.SavePath)
	}
	wantPath := download.IncompleteDir + "/Some.Release.2024.1080p-GRP"
	if st.SavePath != wantPath {
		t.Errorf("save path = %q, want %q", st.SavePath, wantPath)
	}

	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(st.SavePath), "movie.mkv"))
	if err != nil {
		t.Fatalf("read assembled file: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("assembled file differs from what was posted (%d bytes, want %d)", len(got), len(payload))
	}

	// The resume sidecar has done its job and must not be left in the
	// directory the import is about to read.
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(st.SavePath), pipeline.StateFile)); !os.IsNotExist(err) {
		t.Errorf("resume sidecar survived completion: %v", err)
	}

	// And the durable half agrees, so the queue renders after a restart.
	row, ok := store.get(id)
	if !ok {
		t.Fatal("nothing persisted for the completed download")
	}
	if row.State != core.DownloadCompleted || row.Engine != EngineName {
		t.Errorf("persisted row = %s/%s, want completed/%s", row.State, row.Engine, EngineName)
	}
}

// A missing article with no par2 to fix it is a failure the user can act on,
// and the message has to say what happened rather than name a Go error.
func TestEngineFailsWithAReasonWhenArticlesAreGoneAndThereIsNoPar2(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	payload := []byte(strings.Repeat("x", 4000))
	rel := stage(t, nntpSrv, map[string][]byte{"movie.mkv": payload}, 500)

	// One article rots away, which is the failure Usenet is famous for.
	nntpSrv.Remove(yenc.MessageID("movie.mkv", 3))

	e, _ := newTestEngine(t, nntpSrv, newMemStore())
	id := addRelease(t, e, rel, "No.Par2.Release")
	st := waitForState(t, e, id, core.DownloadFailed)

	if !strings.Contains(st.Error, "no par2") {
		t.Errorf("failure = %q, want it to explain that there are no recovery files", st.Error)
	}
	if !strings.Contains(st.Error, "1 article") {
		t.Errorf("failure = %q, want the number of damaged articles", st.Error)
	}
}

// A download nobody can fetch from is a transport problem, not a repair
// problem: saying so is what stops a user spending an afternoon on par2 when
// their provider is simply down.
func TestEngineDistinguishesAnUnreachableProviderFromMissingArticles(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	payload := []byte(strings.Repeat("y", 2000))
	rel := stage(t, nntpSrv, map[string][]byte{"movie.mkv": payload}, 500)

	e, _ := newTestEngine(t, nntpSrv, newMemStore())

	// The provider stops answering the moment the download starts. The count
	// is far above the article count, so every attempt of every segment fails
	// — a provider that is simply down, not one that is flaky.
	nntpSrv.SetFault(nntptest.Fault{Bodies: 10000, Mode: nntptest.FaultDropBeforeStatus})

	id := addRelease(t, e, rel, "Provider.Down.Release")
	st := waitForState(t, e, id, core.DownloadFailed)

	if !strings.Contains(st.Error, "could not be fetched from any news server") {
		t.Errorf("failure = %q, want it to name the provider as the problem", st.Error)
	}
	if !strings.Contains(st.Error, "resume to retry") {
		t.Errorf("failure = %q, want it to say what to do next", st.Error)
	}
}

// Pausing must keep the bytes already on disk. The pipeline's sidecar is what
// makes that true, and the engine's job is to cancel rather than to abandon.
func TestEnginePauseKeepsProgressAndResumeFinishesIt(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	payload := []byte(strings.Repeat("z", 20000))
	rel := stage(t, nntpSrv, map[string][]byte{"movie.mkv": payload}, 500)

	// Hold the download open on its first article so there is something to
	// pause, then release it once the pause has landed. Waiting for the first
	// BODY is what makes this deterministic: it proves the pipeline is past
	// creating the directory rather than merely registered.
	gate := make(chan struct{})
	fetching := make(chan struct{})
	var reached, released sync.Once
	nntpSrv.SetBodyHook(func(string) {
		reached.Do(func() { close(fetching) })
		<-gate
	})

	e, root := newTestEngine(t, nntpSrv, newMemStore())
	id := addRelease(t, e, rel, "Pausable.Release")

	<-fetching
	waitForState(t, e, id, core.DownloadDownloading)
	if err := e.Pause(context.Background(), id); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	released.Do(func() { close(gate) })
	nntpSrv.SetBodyHook(nil)

	st := waitForState(t, e, id, core.DownloadPaused)
	if st.DownRate != 0 {
		t.Errorf("paused download reports rate %d, want 0", st.DownRate)
	}

	// The directory survives the pause: pausing is not discarding.
	dir := filepath.Join(root, filepath.FromSlash(st.SavePath))
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("paused download lost its directory: %v", err)
	}

	if err := e.Resume(context.Background(), id); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waitForState(t, e, id, core.DownloadCompleted)

	got, err := os.ReadFile(filepath.Join(dir, "movie.mkv"))
	if err != nil {
		t.Fatalf("read assembled file: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("resumed download assembled %d bytes, want %d", len(got), len(payload))
	}
}

// The restart contract: a download that was mid-flight comes back and finishes,
// driven by the NZB sidecar rather than by anything in the database. This is
// what "delete caravan.db and the queue still works" costs.
func TestEngineResumesAfterARestart(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	payload := []byte(strings.Repeat("q", 20000))
	rel := stage(t, nntpSrv, map[string][]byte{"movie.mkv": payload}, 500)

	gate := make(chan struct{})
	nntpSrv.SetBodyHook(func(string) { <-gate })

	store := newMemStore()
	root := t.TempDir()
	first, err := NewEngine(root, EngineOpts{
		Servers: []nntp.ServerConfig{testServer(nntpSrv)}, Store: store, NNTP: fastRetry(),
		PollInterval: 10 * time.Millisecond, SkipSpaceCheck: true,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	id, err := first.Add(context.Background(), core.Release{
		Title: "Restarted.Release", Protocol: core.ProtocolUsenet,
		DownloadURL: serveNZB(t, rel.nzb),
	}, core.AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	waitForState(t, first, id, core.DownloadDownloading)

	// Shut down mid-flight, exactly as a restart would.
	close(gate)
	nntpSrv.SetBodyHook(nil)
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second, err := NewEngine(root, EngineOpts{
		Servers: []nntp.ServerConfig{testServer(nntpSrv)}, Store: store, NNTP: fastRetry(),
		PollInterval: 10 * time.Millisecond, SkipSpaceCheck: true,
	})
	if err != nil {
		t.Fatalf("NewEngine after restart: %v", err)
	}
	defer second.Close()

	st := waitForState(t, second, id, core.DownloadCompleted)
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(st.SavePath), "movie.mkv"))
	if err != nil {
		t.Fatalf("read assembled file: %v", err)
	}
	if string(got) != string(payload) {
		t.Errorf("restarted download assembled %d bytes, want %d", len(got), len(payload))
	}
}

// Grabbing the same release twice is one download, not two racing over one
// directory. The NZB's digest is what makes that true, and it is the same
// property an info hash gives the torrent engine.
func TestEngineAddIsIdempotentForTheSameNZB(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	rel := stage(t, nntpSrv, map[string][]byte{"movie.mkv": []byte("same bytes")}, 500)

	e, _ := newTestEngine(t, nntpSrv, newMemStore())
	first := addRelease(t, e, rel, "Duplicate.Release")
	second := addRelease(t, e, rel, "Duplicate.Release")

	if first != second {
		t.Fatalf("the same NZB produced two handles, %q and %q", first, second)
	}
	list, err := e.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d downloads for one release, want 1", len(list))
	}
}

// Two live releases that share a title must not share a directory: one
// directory would mix their files and their resume state.
func TestEngineGivesCollidingTitlesTheirOwnDirectories(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	first := stage(t, nntpSrv, map[string][]byte{"a.mkv": []byte("first release")}, 500)
	second := stage(t, nntpSrv, map[string][]byte{"b.mkv": []byte("second release")}, 500)

	e, _ := newTestEngine(t, nntpSrv, newMemStore())
	idA := addRelease(t, e, first, "Same.Title")
	idB := addRelease(t, e, second, "Same.Title")

	a, err := e.Status(context.Background(), idA)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	b, err := e.Status(context.Background(), idB)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if a.SavePath == b.SavePath {
		t.Fatalf("two downloads share the directory %q", a.SavePath)
	}
}

// A release title is a stranger's text off an indexer, and "remove" must never
// be able to reach outside the incomplete directory (SPEC §13).
func TestEngineRefusesToRemoveDataOutsideTheIncompleteDirectory(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	rel := stage(t, nntpSrv, map[string][]byte{"movie.mkv": []byte("payload")}, 500)

	e, root := newTestEngine(t, nntpSrv, newMemStore())
	id := addRelease(t, e, rel, "../../escape")
	waitForState(t, e, id, core.DownloadCompleted)

	st, err := e.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	// The name is neutralised at the point it is chosen, so the download never
	// had a path outside the root to begin with: one element, directly under
	// the incomplete directory, with the separators defanged.
	if dir := path.Dir(st.SavePath); dir != download.IncompleteDir {
		t.Fatalf("save path %q sits under %q, want a single element under %q",
			st.SavePath, dir, download.IncompleteDir)
	}
	dir := filepath.Join(root, filepath.FromSlash(st.SavePath))
	if rel, err := filepath.Rel(filepath.Join(root, download.IncompleteDir), dir); err != nil || strings.HasPrefix(rel, "..") {
		t.Fatalf("download directory %q is not under the incomplete directory", dir)
	}

	if err := e.Remove(context.Background(), id, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("Remove(deleteData) left %s behind: %v", dir, err)
	}
	// Removing a download never costs the library.
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("Remove reached outside the download's own directory: %v", err)
	}
}

// Removing without deleteData keeps the bytes: "remove" and "delete my media"
// are never the same click (SPEC §13).
func TestEngineRemoveKeepsDataUnlessAsked(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	rel := stage(t, nntpSrv, map[string][]byte{"movie.mkv": []byte("keep me")}, 500)

	store := newMemStore()
	e, root := newTestEngine(t, nntpSrv, store)
	id := addRelease(t, e, rel, "Kept.Release")
	st := waitForState(t, e, id, core.DownloadCompleted)
	dir := filepath.Join(root, filepath.FromSlash(st.SavePath))

	if err := e.Remove(context.Background(), id, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "movie.mkv")); err != nil {
		t.Errorf("Remove without deleteData destroyed the data: %v", err)
	}
	if _, ok := store.get(id); ok {
		t.Error("Remove left the persisted row behind")
	}
	if _, err := e.Status(context.Background(), id); !errors.Is(err, download.ErrNotFound) {
		t.Errorf("Status after Remove = %v, want download.ErrNotFound", err)
	}
}

// With no news server there is nothing to fetch from, and the answer must name
// the screen that fixes it rather than read as a breakage.
func TestEngineWithoutServersRefusesGrabsAndSaysWhy(t *testing.T) {
	e, err := NewEngine(t.TempDir(), EngineOpts{PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatalf("NewEngine with no servers must still build: %v", err)
	}
	defer e.Close()

	_, err = e.Add(context.Background(), core.Release{
		Title: "Nowhere.To.Fetch.From", Protocol: core.ProtocolUsenet,
		DownloadURL: "http://127.0.0.1:1/release.nzb",
	}, core.AddOpts{})
	if !errors.Is(err, nntp.ErrNoServers) {
		t.Fatalf("Add without servers = %v, want nntp.ErrNoServers", err)
	}
	if !strings.Contains(err.Error(), "Usenet servers") {
		t.Errorf("error = %q, want it to name the settings screen", err)
	}
	// And listing still works, because an engine with no servers still holds
	// whatever it downloaded when it had one.
	if _, err := e.List(context.Background()); err != nil {
		t.Errorf("List with no servers = %v, want it to keep working", err)
	}
}

// A settings save that changed nothing must not churn connections, and one
// that changed a credential must.
func TestEngineSetServersOnlyRebuildsOnRealChanges(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	e, _ := newTestEngine(t, nntpSrv, newMemStore())

	before := e.servers
	if err := e.SetServers([]nntp.ServerConfig{testServer(nntpSrv)}); err != nil {
		t.Fatalf("SetServers: %v", err)
	}
	if e.servers != before {
		t.Error("an unchanged configuration rebuilt the pool")
	}

	changed := testServer(nntpSrv)
	changed.Username, changed.Password = "reader", "rotated"
	if err := e.SetServers([]nntp.ServerConfig{changed}); err != nil {
		t.Fatalf("SetServers: %v", err)
	}
	if e.servers == before {
		t.Error("a changed password did not rebuild the pool")
	}

	// Dropping every server leaves the engine configured-less rather than
	// broken, which is what the settings screen allows.
	if err := e.SetServers(nil); err != nil {
		t.Fatalf("SetServers(nil): %v", err)
	}
	if e.fetch.configured() {
		t.Error("clearing every server left a pool behind")
	}
}

// The engine takes NZBs. A torrent arriving here is a routing bug, and it must
// say so rather than fetch a .torrent and fail on the XML.
func TestEngineRefusesTorrents(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	e, _ := newTestEngine(t, nntpSrv, newMemStore())

	_, err := e.Add(context.Background(), core.Release{
		Title: "A.Torrent", Protocol: core.ProtocolTorrent, DownloadURL: "magnet:?xt=urn:btih:abc",
	}, core.AddOpts{})
	if err == nil || !strings.Contains(err.Error(), "only handles NZBs") {
		t.Fatalf("Add(torrent) = %v, want a refusal naming the protocol mismatch", err)
	}
}

// Handles must never be confusable with the torrent engine's, because the
// router probes every un-namespaced engine with a bare handle.
func TestEngineHandlesCannotBeMistakenForInfoHashes(t *testing.T) {
	id := handle([]byte("some nzb document"))
	if !strings.HasPrefix(string(id), handlePrefix) {
		t.Fatalf("handle %q does not carry the engine's prefix", id)
	}
	// An info hash is 40 hex characters; this must not parse as one.
	rest := strings.TrimPrefix(string(id), handlePrefix)
	if len(rest) != 40 {
		t.Fatalf("handle body = %d characters, want 40", len(rest))
	}
	for _, r := range string(id)[:1] {
		if strings.ContainsRune("0123456789abcdef", r) {
			t.Fatalf("handle %q starts with a hex character and could be read as an info hash", id)
		}
	}
}
