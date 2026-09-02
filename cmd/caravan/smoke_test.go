package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/library"
	"github.com/watzon/caravan/internal/store"
)

// The end-to-end acquisition test: the phase-2 path from "a movie is wanted"
// to "the file is in the library", driven through the real HTTP API against
// the real wiring in runServe.
//
// Everything outside the process is faked, and nothing touches the network:
//
//   - The indexer is an httptest server speaking Torznab, whose one result is
//     a magnet link for a torrent seeded by a second anacrolix client on
//     localhost. The magnet carries that seeder as an explicit peer (BEP 9
//     "x.pe"), so the transfer needs neither DHT nor a tracker.
//   - TMDB is an httptest server, reached by rewriting http.DefaultTransport.
//     That is the only seam the test needs that production code does not
//     already provide: runServe builds its metadata client with a nil
//     Transport, which is DefaultTransport.
//
// It is skipped under -short: it moves two megabytes through a real
// BitTorrent handshake, which takes seconds rather than milliseconds.
const (
	smokeMovieTitle = "Big Buck Bunny"
	smokeMovieYear  = 2008
	smokeTMDBID     = 10378
	smokeIMDBID     = "tt1254207"

	// The release as the indexer publishes it, and the file inside the
	// torrent. They differ in punctuation the way real ones do, so the
	// parser is doing real work.
	smokeReleaseTitle = "Big Buck Bunny 2008 1080p BluRay x264-CARAVAN"
	smokeContentName  = "Big.Buck.Bunny.2008.1080p.BluRay.x264-CARAVAN.mkv"
	smokeContentSize  = 2 << 20

	// Where the import must land, relative to the storage root.
	smokeWantPath = library.LibraryDir + "/" + library.MoviesDir +
		"/Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv"
)

func TestSmokeGrabDownloadImport(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end: runs a real BitTorrent transfer between two local clients")
	}

	dirs := smokeDirs(t)
	movieID := seedSmokeLibrary(t, dirs)
	tmdbURL := startFakeTMDB(t)
	redirectTMDB(t, tmdbURL)

	peer, infoHash := startSeeder(t, filepath.Join(dirs.work, "seed"))
	magnet := smokeMagnet(infoHash, peer)
	indexerURL := startFakeTorznab(t, magnet)

	// boot
	api := startCaravan(t, dirs)

	// configure the indexer (POST /indexers)
	var indexerResp struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	api.post(t, "/indexers", map[string]any{
		"name":       "Smoke",
		"url":        indexerURL,
		"api_key":    "smoke",
		"type":       "torznab",
		"categories": []int{2000},
		"enabled":    true,
	}, http.StatusCreated, &indexerResp)
	if indexerResp.ID == 0 {
		t.Fatalf("POST /indexers returned no id: %+v", indexerResp)
	}
	t.Logf("indexer %d configured at %s", indexerResp.ID, indexerURL)

	api.post(t, fmt.Sprintf("/indexers/%d/test", indexerResp.ID), nil, http.StatusOK, nil)
	t.Log("indexer test passed")

	// interactive search (GET .../releases)
	var releases struct {
		Query    string `json:"query"`
		Releases []struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
			Size  int64  `json:"size"`
		} `json:"releases"`
		Errors []struct {
			Indexer string `json:"indexer"`
			Error   string `json:"error"`
		} `json:"errors"`
	}
	api.get(t, fmt.Sprintf("/library/movies/%d/releases", movieID), http.StatusOK, &releases)
	if len(releases.Errors) > 0 {
		t.Fatalf("release search reported indexer errors: %+v", releases.Errors)
	}
	if len(releases.Releases) != 1 {
		t.Fatalf("release search returned %d releases, want 1: %+v", len(releases.Releases), releases.Releases)
	}
	if got := releases.Releases[0].Title; got != smokeReleaseTitle {
		t.Errorf("release title = %q, want %q", got, smokeReleaseTitle)
	}
	t.Logf("search %q returned release %d %q", releases.Query, releases.Releases[0].ID, releases.Releases[0].Title)

	// grab (POST .../grab)
	var grab struct {
		GrabID       int64  `json:"grab_id"`
		DownloadID   string `json:"download_id"`
		ReleaseTitle string `json:"release_title"`
	}
	api.post(t, fmt.Sprintf("/library/movies/%d/grab", movieID),
		map[string]any{"release_id": releases.Releases[0].ID}, http.StatusCreated, &grab)
	if grab.DownloadID != infoHash {
		t.Fatalf("grab download id = %q, want the info hash %q", grab.DownloadID, infoHash)
	}
	t.Logf("grabbed as grab %d, download %s", grab.GrabID, grab.DownloadID)

	// the transfer (GET /downloads)
	waitFor(t, 90*time.Second, "download to finish", func() string {
		d, ok := api.download(t, grab.DownloadID)
		if !ok {
			return "download not in the queue yet"
		}
		switch d.State {
		case string(core.DownloadSeeding), string(core.DownloadCompleted):
			return ""
		case string(core.DownloadFailed):
			t.Fatalf("download failed: %s", d.Error)
		}
		return fmt.Sprintf("state %s, %d/%d bytes", d.State, d.BytesDone, d.Size)
	})
	done, _ := api.download(t, grab.DownloadID)
	if done.Size != smokeContentSize {
		t.Errorf("download size = %d, want %d", done.Size, smokeContentSize)
	}
	t.Logf("download complete: state=%s size=%d save_path=%s", done.State, done.Size, done.SavePath)

	// the watcher imports it
	libraryFile := filepath.Join(dirs.storage, filepath.FromSlash(smokeWantPath))
	waitFor(t, 60*time.Second, "the import watcher to import the download", func() string {
		if _, err := os.Stat(libraryFile); err != nil {
			return "library file not written yet"
		}
		return ""
	})
	t.Logf("imported to %s", smokeWantPath)

	// The library must agree with the filesystem.
	var movie struct {
		ID   int64  `json:"id"`
		Path string `json:"path"`
		File *struct {
			Path string `json:"path"`
			Size int64  `json:"size"`
		} `json:"file"`
	}
	waitFor(t, 30*time.Second, "the movie to report its file", func() string {
		movie.File = nil
		api.get(t, fmt.Sprintf("/library/movies/%d", movieID), http.StatusOK, &movie)
		if movie.File == nil {
			return "movie still has no file"
		}
		return ""
	})
	if movie.File.Path != smokeWantPath {
		t.Errorf("movie file path = %q, want %q", movie.File.Path, smokeWantPath)
	}
	if movie.File.Size != smokeContentSize {
		t.Errorf("movie file size = %d, want %d", movie.File.Size, smokeContentSize)
	}
	assertSize(t, libraryFile, smokeContentSize)
	t.Logf("library reports file %s (%d bytes)", movie.File.Path, movie.File.Size)

	// restart: the queue survives it
	api.stop(t)
	t.Log("caravan stopped")

	api = startCaravan(t, dirs)
	waitFor(t, 30*time.Second, "the queue to come back after a restart", func() string {
		if _, ok := api.download(t, grab.DownloadID); !ok {
			return "download not restored yet"
		}
		return ""
	})
	resumed, _ := api.download(t, grab.DownloadID)
	if resumed.Size != smokeContentSize {
		t.Errorf("resumed download size = %d, want %d", resumed.Size, smokeContentSize)
	}
	assertSize(t, libraryFile, smokeContentSize)
	t.Logf("resumed after restart: state=%s size=%d", resumed.State, resumed.Size)

	// A restart must not re-import what is already imported.
	if n := countMovieFiles(t, dirs.storage); n != 1 {
		t.Errorf("library holds %d movie files after a restart, want 1", n)
	}

	// removing the download must not cost the library
	api.delete(t, "/downloads/"+grab.DownloadID+"?deleteData=false", http.StatusNoContent)
	if _, ok := api.download(t, grab.DownloadID); ok {
		t.Error("download still in the queue after DELETE")
	}
	assertSize(t, libraryFile, smokeContentSize)
	api.get(t, fmt.Sprintf("/library/movies/%d", movieID), http.StatusOK, &movie)
	if movie.File == nil || movie.File.Path != smokeWantPath {
		t.Errorf("movie lost its file when the download was removed: %+v", movie.File)
	}
	t.Log("download removed without deleteData; library file untouched")

	api.stop(t)
}

// fixture: directories, config, seeded database

type smokeEnv struct {
	work    string
	config  string
	storage string
	cfgPath string
}

func smokeDirs(t *testing.T) smokeEnv {
	t.Helper()
	work := t.TempDir()
	env := smokeEnv{
		work:    work,
		config:  filepath.Join(work, "config"),
		storage: filepath.Join(work, "storage"),
		cfgPath: filepath.Join(work, "config", "caravan.yaml"),
	}
	for _, dir := range []string{env.config, env.storage, filepath.Join(work, "seed")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}

	cfg := fmt.Sprintf("data_dir: %q\nstorage_root: %q\nlog_level: warn\n", env.config, env.storage)
	if err := os.WriteFile(env.cfgPath, []byte(cfg), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return env
}

// seedSmokeLibrary puts one wanted movie and a TMDB key in the database, then
// closes it so the server owns it. This is the state the test starts from: a
// monitored movie with no file.
func seedSmokeLibrary(t *testing.T, dirs smokeEnv) int64 {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(filepath.Join(dirs.config, "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	movie := core.Movie{
		TMDBID:    smokeTMDBID,
		IMDBID:    smokeIMDBID,
		Title:     smokeMovieTitle,
		SortTitle: strings.ToLower(smokeMovieTitle),
		Year:      smokeMovieYear,
		Monitored: true,
	}
	if err := st.UpsertMovie(ctx, &movie); err != nil {
		t.Fatalf("seed movie: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingTMDBAPIKey, "smoke-key"); err != nil {
		t.Fatalf("seed tmdb key: %v", err)
	}
	t.Logf("seeded movie %d %q (%d), monitored, no file", movie.ID, movie.Title, movie.Year)
	return movie.ID
}

// fixture: the fake outside world

// startFakeTMDB serves the one movie the library needs to resolve a grab.
func startFakeTMDB(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		want := fmt.Sprintf("/3/movie/%d", smokeTMDBID)
		if r.URL.Path != want {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":             smokeTMDBID,
			"title":          smokeMovieTitle,
			"original_title": smokeMovieTitle,
			"overview":       "A large rabbit takes revenge.",
			// Deliberately empty: a poster would be a second fake server for
			// no extra coverage of the acquisition path.
			"poster_path":  "",
			"release_date": "2008-05-20",
			"imdb_id":      smokeIMDBID,
		})
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// redirectTMDB points every request for api.themoviedb.org at the fake.
//
// runServe builds its metadata client with a nil Transport, so replacing
// http.DefaultTransport reaches it without production code needing a knob it
// would otherwise have no use for.
func redirectTMDB(t *testing.T, target string) {
	t.Helper()
	u, err := url.Parse(target)
	if err != nil {
		t.Fatalf("parse fake tmdb url: %v", err)
	}
	original := http.DefaultTransport
	http.DefaultTransport = &hostRewriter{host: u.Host, base: original}
	t.Cleanup(func() { http.DefaultTransport = original })
}

type hostRewriter struct {
	host string
	base http.RoundTripper
}

func (h *hostRewriter) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host == "api.themoviedb.org" {
		r = r.Clone(r.Context())
		r.URL.Scheme = "http"
		r.URL.Host = h.host
		r.Host = ""
	}
	return h.base.RoundTrip(r)
}

// startFakeTorznab serves a Torznab caps document and a one-item search feed
// pointing at magnet.
func startFakeTorznab(t *testing.T, magnet string) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("t") == "caps" {
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>
<caps><searching><search available="yes" supportedParams="q"/>`+
				`<movie-search available="yes" supportedParams="q"/></searching></caps>`)
			return
		}
		_, _ = fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed">
  <channel>
    <item>
      <title>%s</title>
      <guid>smoke-release-1</guid>
      <link>%s</link>
      <pubDate>%s</pubDate>
      <size>%d</size>
      <enclosure url="%s" length="%d" type="application/x-bittorrent"/>
      <torznab:attr name="seeders" value="1"/>
      <torznab:attr name="peers" value="1"/>
      <torznab:attr name="magneturl" value="%s"/>
    </item>
  </channel>
</rss>`,
			smokeReleaseTitle,
			escapeXML(magnet),
			time.Now().Add(-time.Hour).Format(time.RFC1123Z),
			smokeContentSize,
			escapeXML(magnet), smokeContentSize,
			escapeXML(magnet))
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

func escapeXML(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;").Replace(s)
}

// startSeeder writes the content file, makes a torrent of it, and seeds it
// from a second anacrolix client bound to loopback with every form of peer
// discovery turned off. The only way to find it is the explicit peer address
// the magnet carries.
func startSeeder(t *testing.T, dir string) (peer, infoHash string) {
	t.Helper()

	content := filepath.Join(dir, smokeContentName)
	payload := make([]byte, smokeContentSize)
	// Deterministic, incompressible-enough content: the point is that the
	// bytes on both sides can be compared, not what they are.
	rng := rand.New(rand.NewSource(20080520))
	if _, err := rng.Read(payload); err != nil {
		t.Fatalf("generate content: %v", err)
	}
	if err := os.WriteFile(content, payload, 0o644); err != nil {
		t.Fatalf("write content: %v", err)
	}

	info := metainfo.Info{PieceLength: 256 << 10}
	if err := info.BuildFromFilePath(content); err != nil {
		t.Fatalf("build torrent info: %v", err)
	}
	infoBytes, err := bencode.Marshal(info)
	if err != nil {
		t.Fatalf("encode torrent info: %v", err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}
	hash := mi.HashInfoBytes()

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dir
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.DisablePEX = true
	cfg.NoDefaultPortForwarding = true
	cfg.Seed = true
	// Loopback only, and IPv4 only: binding "127.0.0.1" for the IPv6 listener
	// too is an error, and the peer address in the magnet is v4 regardless.
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.DisableIPv6 = true

	client, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatalf("start seeder: %v", err)
	}
	t.Cleanup(func() { client.Close() })

	tor, _, err := client.AddTorrentSpec(&torrent.TorrentSpec{
		DisplayName:    smokeContentName,
		AddTorrentOpts: torrent.AddTorrentOpts{InfoHash: hash, InfoBytes: infoBytes},
	})
	if err != nil {
		t.Fatalf("seed torrent: %v", err)
	}
	<-tor.GotInfo()
	// Hash-check what is already on disk, so the seeder knows it is complete
	// and will answer requests instead of asking for the data itself.
	if err := tor.VerifyData(); err != nil {
		t.Fatalf("verify seeded data: %v", err)
	}

	port := client.LocalPort()
	if port == 0 {
		t.Fatal("seeder is not listening on a port")
	}
	t.Logf("seeding %s (%d bytes, hash %s) from 127.0.0.1:%d", smokeContentName, smokeContentSize, hash.HexString(), port)
	return fmt.Sprintf("127.0.0.1:%d", port), hash.HexString()
}

// smokeMagnet builds the magnet the indexer publishes: the info hash plus the
// seeder as an explicit peer (BEP 9 x.pe), which is what lets the transfer
// work with no DHT and no tracker.
func smokeMagnet(infoHash, peer string) string {
	q := url.Values{
		"dn":   {smokeContentName},
		"x.pe": {peer},
	}
	return "magnet:?xt=urn:btih:" + infoHash + "&" + q.Encode()
}

// fixture: the server under test

// caravanProcess is one run of runServe, in-process.
//
// In-process rather than a spawned binary so the test drives exactly the wiring
// runServe builds (engine, watcher, indexer factory and shutdown) and so the
// fake TMDB transport is reachable. Shutdown goes through a real SIGTERM, which
// is the signal path runServe actually installs.
type caravanProcess struct {
	baseURL string
	errCh   chan error
	stopped bool
}

func startCaravan(t *testing.T, dirs smokeEnv) *caravanProcess {
	t.Helper()
	addr := freeAddr(t)
	p := &caravanProcess{
		baseURL: "http://" + addr + "/api/v1",
		errCh:   make(chan error, 1),
	}
	go func() { p.errCh <- runServe([]string{"--config", dirs.cfgPath, "--listen", addr}) }()
	t.Cleanup(func() { p.stop(t) })

	waitFor(t, 30*time.Second, "caravan to start listening", func() string {
		resp, err := http.Get(p.baseURL + "/system/status")
		if err != nil {
			return err.Error()
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusOK {
			return "status " + resp.Status
		}
		return ""
	})
	t.Logf("caravan listening on %s", addr)
	return p
}

// stop sends the real shutdown signal and waits for runServe to return.
//
// It signals at most once: after runServe returns it has unregistered its
// SIGTERM handler, and a second signal would take the default action and kill
// the test binary.
func (p *caravanProcess) stop(t *testing.T) {
	t.Helper()
	if p.stopped {
		return
	}
	p.stopped = true

	signalSelfTerm(t)
	select {
	case err := <-p.errCh:
		if err != nil {
			t.Fatalf("caravan exited with an error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("caravan did not shut down within 30s")
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve a port: %v", err)
	}
	defer l.Close()
	return l.Addr().String()
}

// fixture: API client

type downloadRow struct {
	ID        string  `json:"id"`
	GrabID    int64   `json:"grab_id"`
	State     string  `json:"state"`
	Progress  float64 `json:"progress"`
	BytesDone int64   `json:"bytes_done"`
	Size      int64   `json:"size"`
	SavePath  string  `json:"save_path"`
	Error     string  `json:"error"`
}

func (p *caravanProcess) get(t *testing.T, path string, wantStatus int, out any) {
	t.Helper()
	p.do(t, http.MethodGet, path, nil, wantStatus, out)
}

func (p *caravanProcess) post(t *testing.T, path string, body any, wantStatus int, out any) {
	t.Helper()
	p.do(t, http.MethodPost, path, body, wantStatus, out)
}

func (p *caravanProcess) delete(t *testing.T, path string, wantStatus int) {
	t.Helper()
	p.do(t, http.MethodDelete, path, nil, wantStatus, nil)
}

func (p *caravanProcess) do(t *testing.T, method, path string, body any, wantStatus int, out any) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode %s %s: %v", method, path, err)
		}
		reader = strings.NewReader(string(encoded))
	}
	req, err := http.NewRequest(method, p.baseURL+path, reader)
	if err != nil {
		t.Fatalf("build %s %s: %v", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read %s %s: %v", method, path, err)
	}
	if resp.StatusCode != wantStatus {
		t.Fatalf("%s %s = %s, want %d: %s", method, path, resp.Status, wantStatus, raw)
	}
	if out == nil || len(raw) == 0 {
		return
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("decode %s %s: %v: %s", method, path, err, raw)
	}
}

// download finds one row in the queue.
func (p *caravanProcess) download(t *testing.T, id string) (downloadRow, bool) {
	t.Helper()
	var body struct {
		Downloads []downloadRow `json:"downloads"`
	}
	p.get(t, "/downloads", http.StatusOK, &body)
	for _, row := range body.Downloads {
		if row.ID == id {
			return row, true
		}
	}
	return downloadRow{}, false
}

// assertions

// waitFor polls until check returns "", failing with the last reason it gave.
func waitFor(t *testing.T, timeout time.Duration, what string, check func() string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	reason := "never checked"
	for time.Now().Before(deadline) {
		if reason = check(); reason == "" {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("timed out after %s waiting for %s: %s", timeout, what, reason)
}

func assertSize(t *testing.T, path string, want int64) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if info.Size() != want {
		t.Errorf("%s is %d bytes, want %d", path, info.Size(), want)
	}
}

// countMovieFiles counts the video files under the library's Movies section,
// which is how a duplicate import would show up.
func countMovieFiles(t *testing.T, storageRoot string) int {
	t.Helper()
	root := filepath.Join(storageRoot, library.LibraryDir, library.MoviesDir)
	var n int
	err := filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.EqualFold(filepath.Ext(d.Name()), ".mkv") {
			n++
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return n
}

// the shipped binary

// TestBinaryBootsAndResumesDownloads runs the compiled binary rather than
// runServe, and checks the one thing a process boundary can break that an
// in-process test cannot see: that a real `caravan serve` start brings the
// download engine up and re-adds the queue the database remembers.
//
// The persisted download is a bare info hash with no peers, which is exactly
// what a resume looks like before anyone answers: the engine must re-add it
// and report it, not refuse to start over it.
func TestBinaryBootsAndResumesDownloads(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs the caravan binary")
	}

	dirs := smokeDirs(t)
	const resumeHash = "0123456789abcdef0123456789abcdef01234567"

	ctx := context.Background()
	st, err := store.Open(filepath.Join(dirs.config, "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	download := core.Download{
		Engine:   "embedded",
		EngineID: core.DownloadID(resumeHash),
		Title:    "Something.Grabbed.Before.The.Restart",
		State:    core.DownloadDownloading,
	}
	if err := st.UpsertDownload(ctx, &download); err != nil {
		t.Fatalf("seed download: %v", err)
	}
	st.Close()
	t.Logf("seeded a persisted download %s", resumeHash)

	bin := filepath.Join(t.TempDir(), "caravan")
	build := exec.Command("go", "build", "-o", bin, "./cmd/caravan")
	build.Dir = "../.."
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	t.Logf("built %s", bin)

	addr := freeAddr(t)
	var logs strings.Builder
	cmd := exec.Command(bin, "serve", "--config", dirs.cfgPath, "--listen", addr)
	cmd.Env = append(os.Environ(), "CARAVAN_DEV_UI=")
	cmd.Stdout = &logs
	cmd.Stderr = &logs
	if err := cmd.Start(); err != nil {
		t.Fatalf("start caravan: %v", err)
	}
	killed := false
	t.Cleanup(func() {
		if !killed {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
		t.Logf("caravan output:\n%s", logs.String())
	})

	origin := "http://" + addr
	base := origin + "/api/v1"
	waitFor(t, 60*time.Second, "the binary to start listening", func() string {
		resp, err := http.Get(base + "/system/status")
		if err != nil {
			return err.Error()
		}
		defer resp.Body.Close()
		io.Copy(io.Discard, resp.Body)
		if resp.StatusCode != http.StatusOK {
			return "status " + resp.Status
		}
		return ""
	})
	t.Logf("binary listening on %s", addr)

	resp, err := http.Get(origin + "/")
	if err != nil {
		t.Fatalf("GET / from binary: %v", err)
	}
	index, readErr := io.ReadAll(resp.Body)
	closeErr := resp.Body.Close()
	if readErr != nil {
		t.Fatalf("read GET / response: %v", readErr)
	}
	if closeErr != nil {
		t.Fatalf("close GET / response: %v", closeErr)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /: %s: %s", resp.Status, index)
	}
	indexHTML := string(index)
	for _, marker := range []string{"<title>Caravan</title>", `<div id="app"></div>`, "/assets/"} {
		if !strings.Contains(indexHTML, marker) {
			t.Errorf("GET / response does not contain %q: %q", marker, indexHTML)
		}
	}
	t.Log("binary served the embedded SPA at /")

	// The engine is up (a missing one answers 503 here) and it re-added the
	// remembered download.
	var body struct {
		Downloads []downloadRow `json:"downloads"`
	}
	waitFor(t, 30*time.Second, "the resumed download to appear", func() string {
		resp, err := http.Get(base + "/downloads")
		if err != nil {
			return err.Error()
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != http.StatusOK {
			return fmt.Sprintf("%s: %s", resp.Status, raw)
		}
		body.Downloads = nil
		if err := json.Unmarshal(raw, &body); err != nil {
			return err.Error()
		}
		for _, d := range body.Downloads {
			if d.ID == resumeHash {
				return ""
			}
		}
		return fmt.Sprintf("queue has %d downloads, none of them %s", len(body.Downloads), resumeHash)
	})
	t.Logf("binary resumed the persisted download %s", resumeHash)

	// A clean SIGTERM must exit zero: the engine and the watcher both stop on
	// the same signal the server does.
	signalProcessTerm(t, cmd.Process)
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	select {
	case err := <-waited:
		killed = true
		if err != nil {
			t.Fatalf("caravan exited with %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("caravan did not exit within 30s of SIGTERM")
	}
	t.Log("binary shut down cleanly on SIGTERM")
}
