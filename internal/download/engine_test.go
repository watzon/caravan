package download

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/watzon/caravan/internal/core"
)

// memStore is a Persistence that keeps everything in memory, and remembers
// every state it was ever asked to save so a test can assert on transitions
// rather than only on the final answer.
type memStore struct {
	mu      sync.Mutex
	recs    map[core.DownloadID]core.Download
	states  []core.DownloadState
	saveErr error
}

func newMemStore() *memStore {
	return &memStore{recs: make(map[core.DownloadID]core.Download)}
}

func (s *memStore) Save(_ context.Context, d core.Download) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.saveErr != nil {
		return s.saveErr
	}
	s.recs[d.EngineID] = d
	s.states = append(s.states, d.State)
	return nil
}

func (s *memStore) Load(_ context.Context) ([]core.Download, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]core.Download, 0, len(s.recs))
	for _, d := range s.recs {
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].EngineID < out[j].EngineID })
	return out, nil
}

func (s *memStore) Delete(_ context.Context, id core.DownloadID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.recs, id)
	return nil
}

func (s *memStore) get(id core.DownloadID) (core.Download, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.recs[id]
	return d, ok
}

func (s *memStore) sawState(want core.DownloadState) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, st := range s.states {
		if st == want {
			return true
		}
	}
	return false
}

// testOpts is the hermetic configuration every test engine runs with: no DHT,
// no PEX, no trackers, an ephemeral port, and a poll interval short enough that
// a test does not spend its life waiting for a tick.
func testOpts(store Persistence) EmbeddedOpts {
	return EmbeddedOpts{
		ListenPort:      0,
		DisableDHT:      true,
		DisablePEX:      true,
		DisableTrackers: true,
		Store:           store,
		PollInterval:    50 * time.Millisecond,
	}
}

func newTestEngine(t *testing.T, dataDir string, opts EmbeddedOpts) *Embedded {
	t.Helper()
	e, err := NewEmbedded(dataDir, opts)
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

// buildTorrent writes size bytes of deterministic data into its own directory
// and returns the metainfo describing it, plus the data and the directory it
// lives in.
func buildTorrent(t *testing.T, name string, size int) (*metainfo.MetaInfo, []byte, string) {
	t.Helper()
	dir := t.TempDir()
	data := make([]byte, size)
	if _, err := rand.New(rand.NewSource(1)).Read(data); err != nil {
		t.Fatalf("generate payload: %v", err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}

	info := metainfo.Info{PieceLength: 32 << 10}
	if err := info.BuildFromFilePath(path); err != nil {
		t.Fatalf("build info: %v", err)
	}
	var mi metainfo.MetaInfo
	b, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("marshal info: %v", err)
	}
	mi.InfoBytes = b
	return &mi, data, dir
}

// startSeeder brings up a second in-process torrent client that already holds
// the data, and returns the magnet link pointing at it. The seeder is named as
// an explicit peer (BEP 9's "x.pe"), so no discovery mechanism is involved:
// the whole transfer is one loopback TCP connection.
func startSeeder(t *testing.T, mi *metainfo.MetaInfo, dir string) string {
	t.Helper()

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dir
	cfg.ListenPort = 0
	cfg.NoDHT = true
	cfg.DisablePEX = true
	cfg.DisableTrackers = true
	cfg.NoDefaultPortForwarding = true
	cfg.Seed = true

	cl, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatalf("start seeder: %v", err)
	}
	t.Cleanup(func() { cl.Close() })

	tor, err := cl.AddTorrent(mi)
	if err != nil {
		t.Fatalf("seed torrent: %v", err)
	}
	select {
	case <-tor.Complete().On():
	case <-time.After(30 * time.Second):
		t.Fatalf("seeder never completed its own data")
	}

	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	m := mi.Magnet(nil, &info)
	m.Params.Set("x.pe", fmt.Sprintf("127.0.0.1:%d", cl.LocalPort()))
	return m.String()
}

// serveTorrent publishes a .torrent over HTTP and returns its URL. A magnet
// link carries only an info hash, so a test that needs the file list without a
// peer to fetch metadata from has to hand the engine the metainfo itself.
func serveTorrent(t *testing.T, mi *metainfo.MetaInfo) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := mi.Write(w); err != nil {
			t.Errorf("serve metainfo: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL + "/release.torrent"
}

// waitState polls until a download reaches want, and fails with the last
// status it saw so a timeout says what the engine was actually doing.
func waitState(t *testing.T, e *Embedded, id core.DownloadID, want core.DownloadState) core.DownloadStatus {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	var last core.DownloadStatus
	for time.Now().Before(deadline) {
		st, err := e.Status(context.Background(), id)
		if err != nil {
			t.Fatalf("Status(%s): %v", id, err)
		}
		last = *st
		if last.State == want {
			return last
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("download %s never reached %q: state=%q progress=%.2f bytes=%d/%d err=%q",
		id, want, last.State, last.Progress, last.BytesDone, last.Size, last.Error)
	return last
}

func TestAddAcceptsEveryReleaseShape(t *testing.T) {
	mi, _, _ := buildTorrent(t, "payload.bin", 128<<10)
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	hash := mi.HashInfoBytes()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok.torrent":
			if err := mi.Write(w); err != nil {
				t.Errorf("serve metainfo: %v", err)
			}
		case "/lying.torrent":
			// A 200 that is not a torrent — an indexer's HTML error page, or
			// an nzb where a .torrent was promised (the AnimeTosho incident).
			w.Header().Set("Content-Type", "text/html")
			_, _ = io.WriteString(w, "<html><body>not found</body></html>")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	magnet := mi.Magnet(nil, &info).String()

	tests := []struct {
		name    string
		release core.Release
		wantID  core.DownloadID
		wantErr bool
	}{
		{
			name:    "magnet link",
			release: core.Release{Title: "Magnet Release", Protocol: core.ProtocolTorrent, DownloadURL: magnet},
			wantID:  core.DownloadID(hash.HexString()),
		},
		{
			name:    "torrent url",
			release: core.Release{Title: "URL Release", Protocol: core.ProtocolTorrent, DownloadURL: srv.URL + "/ok.torrent"},
			wantID:  core.DownloadID(hash.HexString()),
		},
		{
			name:    "bare info hash",
			release: core.Release{Title: "Hash Release", Protocol: core.ProtocolTorrent, InfoHash: hash.HexString()},
			wantID:  core.DownloadID(hash.HexString()),
		},
		{
			name:    "info hash in download url",
			release: core.Release{Title: "Hash URL Release", Protocol: core.ProtocolTorrent, DownloadURL: hash.HexString()},
			wantID:  core.DownloadID(hash.HexString()),
		},
		{
			name: "non-torrent body falls back to the info hash",
			release: core.Release{
				Title:       "Lying URL Release",
				Protocol:    core.ProtocolTorrent,
				DownloadURL: srv.URL + "/lying.torrent",
				InfoHash:    hash.HexString(),
			},
			wantID: core.DownloadID(hash.HexString()),
		},
		{
			name: "dead url falls back to the info hash",
			release: core.Release{
				Title:       "Dead URL Release",
				Protocol:    core.ProtocolTorrent,
				DownloadURL: srv.URL + "/missing.torrent",
				InfoHash:    hash.HexString(),
			},
			wantID: core.DownloadID(hash.HexString()),
		},
		{
			name:    "usenet release",
			release: core.Release{Title: "Usenet Release", Protocol: core.ProtocolUsenet, DownloadURL: srv.URL + "/ok.nzb"},
			wantErr: true,
		},
		{
			name:    "missing torrent file without a hash to fall back on",
			release: core.Release{Title: "Gone", Protocol: core.ProtocolTorrent, DownloadURL: srv.URL + "/missing.torrent"},
			wantErr: true,
		},
		{
			name:    "unusable source",
			release: core.Release{Title: "Nonsense", Protocol: core.ProtocolTorrent, DownloadURL: "ftp://example.invalid/x"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A fresh engine per case: every shape resolves to the same info
			// hash, and sharing one engine would make the second add a no-op.
			e := newTestEngine(t, t.TempDir(), testOpts(newMemStore()))
			id, err := e.Add(context.Background(), tt.release, core.AddOpts{})
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Add(%s) = %q, want error", tt.name, id)
				}
				list, err := e.List(context.Background())
				if err != nil {
					t.Fatalf("List: %v", err)
				}
				if len(list) != 0 {
					t.Fatalf("failed Add left %d downloads behind", len(list))
				}
				return
			}
			if err != nil {
				t.Fatalf("Add(%s): %v", tt.name, err)
			}
			if id != tt.wantID {
				t.Fatalf("Add(%s) = %q, want %q", tt.name, id, tt.wantID)
			}
		})
	}
}

func TestAddRecordsAndIsIdempotent(t *testing.T) {
	mi, _, _ := buildTorrent(t, "payload.bin", 128<<10)
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	store := newMemStore()
	e := newTestEngine(t, t.TempDir(), testOpts(store))
	ctx := context.Background()

	// A .torrent rather than a magnet: this asserts on the file list, which a
	// magnet does not carry.
	release := core.Release{
		Title:       "Some Release 2024 1080p",
		Protocol:    core.ProtocolTorrent,
		DownloadURL: serveTorrent(t, mi),
	}
	id, err := e.Add(ctx, release, core.AddOpts{Category: "movies", MovieID: 7})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	again, err := e.Add(ctx, release, core.AddOpts{})
	if err != nil {
		t.Fatalf("second Add: %v", err)
	}
	if again != id {
		t.Fatalf("re-adding the same torrent produced %q, want %q", again, id)
	}

	list, err := e.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("List returned %d downloads, want 1", len(list))
	}
	if list[0].ID != id {
		t.Fatalf("List reported %q, want %q", list[0].ID, id)
	}

	// The metainfo came with the release, so size and destination are known
	// before a single peer is contacted.
	if list[0].Size != info.TotalLength() {
		t.Fatalf("Size = %d, want %d", list[0].Size, info.TotalLength())
	}
	if want := "incomplete/" + info.BestName(); list[0].SavePath != want {
		t.Fatalf("SavePath = %q, want %q", list[0].SavePath, want)
	}

	rec, ok := store.get(id)
	if !ok {
		t.Fatalf("Add did not persist %q", id)
	}
	if rec.Engine != EngineName {
		t.Fatalf("persisted engine = %q, want %q", rec.Engine, EngineName)
	}
	if rec.Title != info.BestName() && rec.Title != release.Title {
		t.Fatalf("persisted title = %q, want the release or torrent name", rec.Title)
	}
}

func TestAddRollsBackWhenPersistenceFails(t *testing.T) {
	mi, _, _ := buildTorrent(t, "payload.bin", 64<<10)
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}

	store := newMemStore()
	store.saveErr = errors.New("disk on fire")
	e := newTestEngine(t, t.TempDir(), testOpts(store))
	ctx := context.Background()

	_, err = e.Add(ctx, core.Release{
		Title:       "Unsaveable",
		Protocol:    core.ProtocolTorrent,
		DownloadURL: mi.Magnet(nil, &info).String(),
	}, core.AddOpts{})
	if err == nil {
		t.Fatal("Add succeeded despite a failing store")
	}

	list, err := e.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("List returned %d downloads after a failed Add, want 0", len(list))
	}
}

func TestPauseResumeAndRemoveBookkeeping(t *testing.T) {
	mi, _, _ := buildTorrent(t, "payload.bin", 128<<10)
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}

	store := newMemStore()
	e := newTestEngine(t, t.TempDir(), testOpts(store))
	ctx := context.Background()

	id, err := e.Add(ctx, core.Release{
		Title:       "Pausable",
		Protocol:    core.ProtocolTorrent,
		DownloadURL: mi.Magnet(nil, &info).String(),
	}, core.AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	if err := e.Pause(ctx, id); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	st, err := e.Status(ctx, id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.State != core.DownloadPaused {
		t.Fatalf("state after Pause = %q, want %q", st.State, core.DownloadPaused)
	}
	if rec, _ := store.get(id); rec.State != core.DownloadPaused {
		t.Fatalf("persisted state after Pause = %q, want %q", rec.State, core.DownloadPaused)
	}

	if err := e.Resume(ctx, id); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	st, err = e.Status(ctx, id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.State == core.DownloadPaused {
		t.Fatal("state is still paused after Resume")
	}

	if err := e.Remove(ctx, id, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := e.Status(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Status after Remove = %v, want ErrNotFound", err)
	}
	if _, ok := store.get(id); ok {
		t.Fatal("Remove left the download in the store")
	}

	// Unknown ids are a not-found, not a panic and not a silent success.
	for _, call := range []struct {
		name string
		err  error
	}{
		{"Status", func() error { _, err := e.Status(ctx, "deadbeef"); return err }()},
		{"Pause", e.Pause(ctx, "deadbeef")},
		{"Resume", e.Resume(ctx, "deadbeef")},
		{"Remove", e.Remove(ctx, "deadbeef", false)},
	} {
		if !errors.Is(call.err, ErrNotFound) {
			t.Errorf("%s(unknown) = %v, want ErrNotFound", call.name, call.err)
		}
	}
}

func TestRemoveDeletesOnlyItsOwnData(t *testing.T) {
	mi, _, _ := buildTorrent(t, "payload.bin", 64<<10)
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}

	root := t.TempDir()
	store := newMemStore()
	e := newTestEngine(t, root, testOpts(store))
	ctx := context.Background()

	id, err := e.Add(ctx, core.Release{
		Title:       "Removable",
		Protocol:    core.ProtocolTorrent,
		DownloadURL: serveTorrent(t, mi),
	}, core.AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Stand in for this download's data — both the finished name and the
	// ".part" file the storage layer writes while a single-file torrent is
	// still incomplete — plus two files that removing a download must never
	// touch: another download's data, and the library (SPEC §13).
	own := filepath.Join(root, IncompleteDir, info.BestName())
	ownPart := own + partFileSuffix
	neighbour := filepath.Join(root, IncompleteDir, "other-download.bin")
	library := filepath.Join(root, "library", "Movies", "Keep Me (2001)", "Keep Me (2001).mkv")
	if err := os.MkdirAll(filepath.Dir(library), 0o755); err != nil {
		t.Fatalf("create library: %v", err)
	}
	for _, p := range []string{own, ownPart, neighbour, library} {
		if err := os.WriteFile(p, []byte("data"), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}

	if err := e.Remove(ctx, id, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, p := range []string{own, ownPart} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("Remove(deleteData) left %s: %v", p, err)
		}
	}
	for _, p := range []string{neighbour, library} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("Remove(deleteData) touched %s: %v", p, err)
		}
	}
}

func TestRemoveKeepsDataWhenNotAskedToDeleteIt(t *testing.T) {
	mi, _, _ := buildTorrent(t, "payload.bin", 64<<10)
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}

	root := t.TempDir()
	e := newTestEngine(t, root, testOpts(newMemStore()))
	ctx := context.Background()

	id, err := e.Add(ctx, core.Release{
		Title:       "Keeper",
		Protocol:    core.ProtocolTorrent,
		DownloadURL: mi.Magnet(nil, &info).String(),
	}, core.AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	data := filepath.Join(root, IncompleteDir, info.BestName())
	if err := os.WriteFile(data, []byte("data"), 0o644); err != nil {
		t.Fatalf("write data: %v", err)
	}
	if err := e.Remove(ctx, id, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(data); err != nil {
		t.Fatalf("Remove(deleteData=false) removed %s: %v", data, err)
	}
}

func TestDataPathRefusesToEscapeIncomplete(t *testing.T) {
	root := t.TempDir()
	e := newTestEngine(t, root, testOpts(nil))

	if got, err := e.dataPath("Some Movie (2001)"); err != nil {
		t.Fatalf("dataPath(legit): %v", err)
	} else if want := filepath.Join(root, IncompleteDir, "Some Movie (2001)"); got != want {
		t.Fatalf("dataPath(legit) = %q, want %q", got, want)
	}

	// A torrent name is a stranger's bytes. None of these may resolve to
	// anything outside the incomplete directory.
	for _, name := range []string{"..", "../evil", "../../library", "sub/../../evil", ".", ""} {
		if got, err := e.dataPath(name); err == nil {
			t.Errorf("dataPath(%q) = %q, want an error", name, got)
		}
	}

	// An absolute name is contained rather than honoured.
	got, err := e.dataPath("/etc/passwd")
	if err != nil {
		t.Fatalf("dataPath(absolute): %v", err)
	}
	if want := filepath.Join(root, IncompleteDir, "etc", "passwd"); got != want {
		t.Fatalf("dataPath(absolute) = %q, want %q", got, want)
	}
}

func TestRestoreReAddsPersistedDownloads(t *testing.T) {
	mi, _, _ := buildTorrent(t, "payload.bin", 128<<10)
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}

	root := t.TempDir()
	store := newMemStore()
	ctx := context.Background()

	first, err := NewEmbedded(root, testOpts(store))
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	id, err := first.Add(ctx, core.Release{
		Title:       "Survivor",
		Protocol:    core.ProtocolTorrent,
		DownloadURL: serveTorrent(t, mi),
	}, core.AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	// The sidecar is what makes a restart cheap; wait for it rather than
	// racing the goroutine that writes it.
	waitForFile(t, first.metainfoPath(id))
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	second := newTestEngine(t, root, testOpts(store))
	list, err := second.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("restored %d downloads, want 1", len(list))
	}
	if list[0].ID != id {
		t.Fatalf("restored %q, want %q", list[0].ID, id)
	}
	// Restored from the sidecar, so the info dict came back with it: a restart
	// does not have to re-fetch metadata from peers.
	if list[0].Size != info.TotalLength() {
		t.Fatalf("restored Size = %d, want %d", list[0].Size, info.TotalLength())
	}
}

func TestRestoreHonoursPausedOption(t *testing.T) {
	mi, _, _ := buildTorrent(t, "payload.bin", 64<<10)
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}

	root := t.TempDir()
	store := newMemStore()
	ctx := context.Background()

	first, err := NewEmbedded(root, testOpts(store))
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}
	id, err := first.Add(ctx, core.Release{
		Title:       "Portable",
		Protocol:    core.ProtocolTorrent,
		DownloadURL: mi.Magnet(nil, &info).String(),
	}, core.AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	opts := testOpts(store)
	opts.Paused = true
	second := newTestEngine(t, root, opts)

	st, err := second.Status(ctx, id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.State != core.DownloadPaused {
		t.Fatalf("restored state = %q, want %q", st.State, core.DownloadPaused)
	}
}

// TestTransferFromLocalSeeder is the end-to-end proof: two clients in one
// process, one loopback connection, no discovery. It asserts the bytes arrive
// where the import pipeline expects them, that the state becomes seeding, and
// that the download comes back after a restart (PLAN phase 2).
func TestTransferFromLocalSeeder(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping loopback torrent transfer in -short mode")
	}

	const payloadSize = 1 << 20
	mi, payload, seedDir := buildTorrent(t, "Some Movie (2001) 1080p.mkv", payloadSize)
	magnet := startSeeder(t, mi, seedDir)

	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}

	root := t.TempDir()
	store := newMemStore()
	ctx := context.Background()
	e, err := NewEmbedded(root, testOpts(store))
	if err != nil {
		t.Fatalf("NewEmbedded: %v", err)
	}

	id, err := e.Add(ctx, core.Release{
		Title:       "Some Movie 2001 1080p BluRay x264-GRP",
		Protocol:    core.ProtocolTorrent,
		DownloadURL: magnet,
		Size:        payloadSize,
	}, core.AddOpts{MovieID: 42})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	st := waitState(t, e, id, core.DownloadSeeding)
	if st.BytesDone != int64(payloadSize) {
		t.Fatalf("BytesDone = %d, want %d", st.BytesDone, payloadSize)
	}
	if st.Progress != 1 {
		t.Fatalf("Progress = %v, want 1", st.Progress)
	}
	if st.Size != int64(payloadSize) {
		t.Fatalf("Size = %d, want %d", st.Size, payloadSize)
	}

	// The bytes are on disk, under the incomplete directory, at the path the
	// status reported relative to the storage root.
	onDisk := filepath.Join(root, filepath.FromSlash(st.SavePath))
	got, err := os.ReadFile(onDisk)
	if err != nil {
		t.Fatalf("read downloaded data: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("downloaded %d bytes, want the %d-byte payload", len(got), len(payload))
	}

	if !store.sawState(core.DownloadSeeding) {
		t.Fatal("completion was never persisted")
	}
	rec, ok := store.get(id)
	if !ok {
		t.Fatal("download missing from the store")
	}
	if rec.BytesDone != int64(payloadSize) || rec.Progress != 1 {
		t.Fatalf("persisted progress = %v (%d bytes), want 1 (%d bytes)", rec.Progress, rec.BytesDone, payloadSize)
	}

	// Pausing a finished torrent stops the seeding, which is the one way a
	// download reaches "completed" rather than "seeding".
	if err := e.Pause(ctx, id); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if paused := waitState(t, e, id, core.DownloadCompleted); paused.BytesDone != int64(payloadSize) {
		t.Fatalf("BytesDone after Pause = %d, want %d", paused.BytesDone, payloadSize)
	}
	if err := e.Resume(ctx, id); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	waitState(t, e, id, core.DownloadSeeding)

	// Restart: the data and the sidecar are still there, so the download comes
	// straight back as a complete torrent.
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	restarted := newTestEngine(t, root, testOpts(store))
	after := waitState(t, restarted, id, core.DownloadSeeding)
	if after.BytesDone != int64(payloadSize) {
		t.Fatalf("BytesDone after restart = %d, want %d", after.BytesDone, payloadSize)
	}
	if after.Name != info.BestName() {
		t.Fatalf("Name after restart = %q, want %q", after.Name, info.BestName())
	}

	// And removing it takes its data with it, on request.
	if err := restarted.Remove(ctx, id, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Stat(onDisk); !os.IsNotExist(err) {
		t.Fatalf("Remove(deleteData) left %s: %v", onDisk, err)
	}
}

func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s never appeared", path)
}
