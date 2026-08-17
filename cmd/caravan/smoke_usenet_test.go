package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/library"
	"github.com/watzon/caravan/internal/usenet"
	"github.com/watzon/caravan/internal/usenet/nntptest"
	"github.com/watzon/caravan/internal/usenet/yenc"
)

// The end-to-end Usenet path, driven through the real HTTP API against the
// real wiring in runServe with zero external download clients configured:
//
//	grab → download → repair → extract → import
//
// Everything outside the process is faked, and nothing touches the network:
//
//   - The indexer is an httptest server speaking Newznab, whose one result
//     links to an .nzb the same server serves.
//   - The news server is internal/usenet/nntptest, holding the release's yEnc
//     articles. One article of the archive is served with a wrong per-part
//     CRC, which is the corruption par2 exists for.
//   - The release is a committed fixture: an archive holding the movie, and a
//     par2 set over it created by par2cmdline (see testdata/usenet/gen). The
//     recovery data is a foreign implementation's, so "the repair worked"
//     means the bytes really came back rather than that two halves of Caravan
//     agree with each other.
//   - TMDB is an httptest server, reached the same way the torrent smoke test
//     reaches it.
//
// It is skipped under -short: it moves a hundred kilobytes through a real NNTP
// handshake, a real Reed-Solomon repair and a real unpack.

// usenetPartSize is the yEnc part size the release is posted with. At 8 KiB
// against the fixture's 4 KiB par2 blocks, one lost article costs exactly two
// recovery blocks — comfortably inside the fixture's budget of six, and five
// lost articles are comfortably outside it.
const usenetPartSize = 8 << 10

// usenetFixture is testdata/usenet/MANIFEST.json.
type usenetFixture struct {
	ReleaseTitle   string   `json:"release_title"`
	ArchiveName    string   `json:"archive_name"`
	ContentName    string   `json:"content_name"`
	ContentSize    int      `json:"content_size"`
	ContentSHA256  string   `json:"content_sha256"`
	SetName        string   `json:"set_name"`
	BlockSize      int      `json:"block_size"`
	RecoveryBlocks int      `json:"recovery_blocks"`
	Par2Files      []string `json:"par2_files"`

	// files is every fixture file that gets posted, keyed by name.
	files map[string][]byte
	// articleIDs are the message-ids each file was posted under, recorded by
	// postRelease so a test can ask exactly which articles were fetched.
	articleIDs map[string][]string
}

// loadUsenetFixture reads the committed release and checks it still describes
// the movie the smoke library is seeded with. A regenerated fixture that
// renamed the release would otherwise import nothing and fail somewhere far
// less obvious.
func loadUsenetFixture(t *testing.T) *usenetFixture {
	t.Helper()
	dir := filepath.Join("testdata", "usenet")

	raw, err := os.ReadFile(filepath.Join(dir, "MANIFEST.json"))
	if err != nil {
		t.Fatalf("read the usenet fixture manifest (regenerate with `go run ./cmd/caravan/testdata/usenet/gen`): %v", err)
	}
	fx := &usenetFixture{files: map[string][]byte{}, articleIDs: map[string][]string{}}
	if err := json.Unmarshal(raw, fx); err != nil {
		t.Fatalf("decode the usenet fixture manifest: %v", err)
	}
	if fx.ReleaseTitle != smokeReleaseTitle || fx.ContentName != smokeContentName {
		t.Fatalf("fixture describes %q/%q but the smoke library is seeded for %q/%q",
			fx.ReleaseTitle, fx.ContentName, smokeReleaseTitle, smokeContentName)
	}

	for _, name := range append([]string{fx.ArchiveName}, fx.Par2Files...) {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		fx.files[name] = data
	}
	return fx
}

// postRelease publishes every fixture file to the news server as yEnc articles
// and returns the NZB indexing them.
//
// damage names how many of the archive's articles are served with a wrong
// per-part CRC. Zero posts a clean release; one is the repairable case; five
// exceeds the fixture's recovery budget.
func postRelease(t *testing.T, srv *nntptest.Server, fx *usenetFixture, damage int) []byte {
	t.Helper()

	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n")
	b.WriteString(`<nzb xmlns="http://www.newzbin.com/DTD/2003/nzb">` + "\n")
	fmt.Fprintf(&b, "  <head><meta type=\"name\">%s</meta></head>\n", html.EscapeString(fx.ReleaseTitle))

	// The archive first, then the recovery volumes, which is the order a real
	// posting uses and the order the engine reads them back in.
	for _, name := range append([]string{fx.ArchiveName}, fx.Par2Files...) {
		data := fx.files[name]
		articles, err := yenc.EncodeFile(name, data, usenetPartSize)
		if err != nil {
			t.Fatalf("encode %s: %v", name, err)
		}

		fmt.Fprintf(&b, "  <file poster=\"poster@caravan.invalid\" date=\"%d\" subject=\"%s\">\n",
			time.Now().Add(-time.Hour).Unix(),
			html.EscapeString(fmt.Sprintf(`%s [1/1] - "%s" yEnc (1/%d)`, fx.ReleaseTitle, name, len(articles))))
		b.WriteString("    <groups><group>alt.binaries.caravan</group></groups>\n")
		b.WriteString("    <segments>\n")

		for i, article := range articles {
			id := yenc.MessageID(name, i+1)
			fx.articleIDs[name] = append(fx.articleIDs[name], id)
			body := article
			// Damage the middle articles of the archive, never the first: the
			// first one carries the =ybegin size= the assembler needs to size
			// the file, and a release whose every copy of that is gone is a
			// different (and much less interesting) failure.
			if name == fx.ArchiveName && i >= 1 && i <= damage {
				body = corruptPartCRC(t, article)
			}
			srv.Add(id, body)
			fmt.Fprintf(&b, "      <segment bytes=\"%d\" number=\"%d\">%s</segment>\n",
				len(article), i+1, id)
		}
		b.WriteString("    </segments>\n  </file>\n")
	}
	b.WriteString("</nzb>\n")
	return []byte(b.String())
}

// corruptPartCRC rewrites the =yend trailer's pcrc32 so the article arrives
// complete and fails its own checksum.
//
// This is the realistic corruption: the bytes are there, the length is right,
// and only the checksum says the payload is not what was posted. A decoder
// that trusted the length would write silent garbage into the middle of the
// user's file, which is precisely the failure par2 is downstream of.
func corruptPartCRC(t *testing.T, article []byte) []byte {
	t.Helper()
	const marker = "pcrc32="
	i := strings.LastIndex(string(article), marker)
	if i < 0 {
		t.Fatalf("article has no pcrc32 trailer to corrupt:\n%s", tail(article, 120))
	}
	out := append([]byte(nil), article...)
	copy(out[i+len(marker):], "deadbeef")
	return out
}

func tail(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[len(b)-n:])
}

// startFakeNewznab serves a Newznab caps document, a one-item search feed, and
// the .nzb that item points at.
func startFakeNewznab(t *testing.T, doc []byte, size int) string {
	t.Helper()
	var base string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/getnzb" {
			w.Header().Set("Content-Type", "application/x-nzb")
			w.Write(doc)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		if r.URL.Query().Get("t") == "caps" {
			io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?>`+
				`<caps><searching><search available="yes" supportedParams="q"/>`+
				`<movie-search available="yes" supportedParams="q"/></searching></caps>`)
			return
		}
		nzbURL := base + "/getnzb"
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<rss version="2.0" xmlns:newznab="http://www.newznab.com/DTD/2010/feeds/attributes/">
  <channel>
    <item>
      <title>%s</title>
      <guid>usenet-smoke-1</guid>
      <link>%s</link>
      <pubDate>%s</pubDate>
      <size>%d</size>
      <enclosure url="%s" length="%d" type="application/x-nzb"/>
      <newznab:attr name="category" value="2000"/>
    </item>
  </channel>
</rss>`,
			smokeReleaseTitle,
			escapeXML(nzbURL),
			time.Now().Add(-time.Hour).Format(time.RFC1123Z),
			size,
			escapeXML(nzbURL), size)
	}))
	t.Cleanup(srv.Close)
	base = srv.URL
	return srv.URL
}

// startSmokeNNTP starts the fake news server the release is posted to, and
// records every article it is asked for.
//
// The record is the test's evidence that par2 is a lazy repair budget: the
// recovery volumes are only fetched when the content came back with holes, so
// "an article of the par2 set was requested" is a precise signal that the
// repair stage ran, and one that does not depend on catching a short-lived
// phase badge between two polls.
func startSmokeNNTP(t *testing.T) (*nntptest.Server, func() []string) {
	t.Helper()
	srv, err := nntptest.New(nntptest.Options{
		Username: "caravan", Password: "secret", RequireAuth: true,
	})
	if err != nil {
		t.Fatalf("start fake news server: %v", err)
	}
	t.Cleanup(func() { srv.Close() })

	var mu sync.Mutex
	var asked []string
	srv.SetBodyHook(func(id string) {
		mu.Lock()
		asked = append(asked, id)
		mu.Unlock()
	})
	return srv, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return append([]string(nil), asked...)
	}
}

// askedFor reports whether any article of one of the named files was fetched.
//
// It matches on the exact message-ids postRelease published rather than on a
// prefix, because the server records what the wire carried — angle brackets
// included — and a prefix test that silently stops matching would turn this
// assertion into one that can only pass.
func (fx *usenetFixture) askedFor(asked []string, names ...string) bool {
	want := map[string]bool{}
	for _, name := range names {
		for _, id := range fx.articleIDs[name] {
			want[id] = true
		}
	}
	if len(want) == 0 {
		return false
	}
	for _, id := range asked {
		if want[strings.Trim(id, "<>")] {
			return true
		}
	}
	return false
}

// configureUsenet points a running Caravan at the fake indexer and the fake
// news server, through the same endpoints the settings screens use.
func configureUsenet(t *testing.T, api *caravanProcess, news *nntptest.Server, indexerURL string) {
	t.Helper()

	var server struct {
		ID          int64 `json:"id"`
		HasPassword bool  `json:"has_password"`
	}
	api.post(t, "/usenet-servers", map[string]any{
		"name": "Fake News", "host": news.Host(), "port": news.Port(),
		"tls": false, "username": "caravan", "password": "secret",
		"max_connections": 4, "priority": 1, "enabled": true,
	}, http.StatusCreated, &server)
	if server.ID == 0 {
		t.Fatal("POST /usenet-servers returned no id")
	}
	// SPEC §12: the credential never comes back, only the fact of one.
	if !server.HasPassword {
		t.Error("the created news server does not report a stored password")
	}
	api.post(t, fmt.Sprintf("/usenet-servers/%d/test", server.ID), nil, http.StatusOK, nil)
	t.Logf("news server %d configured at %s", server.ID, news.Addr())

	var indexer struct {
		ID int64 `json:"id"`
	}
	api.post(t, "/indexers", map[string]any{
		"name": "Fake Newznab", "url": indexerURL, "api_key": "smoke",
		"type": "newznab", "categories": []int{2000}, "enabled": true,
	}, http.StatusCreated, &indexer)
	if indexer.ID == 0 {
		t.Fatal("POST /indexers returned no id")
	}
	api.post(t, fmt.Sprintf("/indexers/%d/test", indexer.ID), nil, http.StatusOK, nil)
	t.Logf("indexer %d configured at %s", indexer.ID, indexerURL)
}

// grabTheRelease searches for the movie and grabs the single result, returning
// the download handle.
func grabTheRelease(t *testing.T, api *caravanProcess, movieID int64) string {
	t.Helper()

	var releases struct {
		Releases []struct {
			ID       int64  `json:"id"`
			Title    string `json:"title"`
			Protocol string `json:"protocol"`
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
	if got := releases.Releases[0].Protocol; got != "usenet" {
		t.Fatalf("release protocol = %q, want usenet", got)
	}

	var grab struct {
		DownloadID string `json:"download_id"`
	}
	api.post(t, fmt.Sprintf("/library/movies/%d/grab", movieID),
		map[string]any{"release_id": releases.Releases[0].ID}, http.StatusCreated, &grab)
	if grab.DownloadID == "" {
		t.Fatal("grab returned no download handle")
	}

	// The acceptance criterion for task 6: with no external client configured,
	// a usenet grab lands on the built-in engine rather than being rejected.
	// The queue row is where the engine that took it is recorded.
	row, ok := queueRowFor(t, api, grab.DownloadID)
	if !ok {
		t.Fatalf("the grabbed download %q is not in the queue", grab.DownloadID)
	}
	if row.Engine != usenet.EngineName {
		t.Fatalf("grab went to engine %q, want the built-in %q", row.Engine, usenet.EngineName)
	}
	t.Logf("grabbed as %s on %s", grab.DownloadID, row.Engine)
	return grab.DownloadID
}

// queueRow is one row of GET /downloads.
type queueRow struct {
	ID       string  `json:"id"`
	Engine   string  `json:"engine"`
	Name     string  `json:"name"`
	State    string  `json:"state"`
	Phase    string  `json:"phase"`
	Progress float64 `json:"progress"`
	Error    string  `json:"error"`
	SavePath string  `json:"save_path"`
}

func queue(t *testing.T, api *caravanProcess) []queueRow {
	t.Helper()
	var body struct {
		Downloads []queueRow `json:"downloads"`
	}
	api.get(t, "/downloads", http.StatusOK, &body)
	return body.Downloads
}

func queueRowFor(t *testing.T, api *caravanProcess, id string) (queueRow, bool) {
	t.Helper()
	for _, row := range queue(t, api) {
		if row.ID == id {
			return row, true
		}
	}
	return queueRow{}, false
}

func allowPrivateReleasePayloadsForSmoke(t *testing.T) {
	t.Helper()
	previous := releasePayloadHTTPClientFactory
	releasePayloadHTTPClientFactory = func() *http.Client {
		return &http.Client{Timeout: 30 * time.Second}
	}
	t.Cleanup(func() { releasePayloadHTTPClientFactory = previous })
}

// The whole phase-7 path in one test.
func TestSmokeUsenetGrabRepairExtractImport(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end: runs a real NNTP transfer, par2 repair and unpack")
	}
	allowPrivateReleasePayloadsForSmoke(t)

	fx := loadUsenetFixture(t)
	dirs := smokeDirs(t)
	movieID := seedSmokeLibrary(t, dirs)
	redirectTMDB(t, startFakeTMDB(t))

	news, articlesAsked := startSmokeNNTP(t)
	// One damaged article: two par2 blocks against a budget of six, so the
	// release is repairable and the repair has real work to do.
	doc := postRelease(t, news, fx, 1)
	indexerURL := startFakeNewznab(t, doc, len(fx.files[fx.ArchiveName]))

	api := startCaravan(t, dirs)
	configureUsenet(t, api, news, indexerURL)

	// No external download client is configured anywhere in this test. That is
	// the point of it.
	var clients struct {
		DownloadClients []struct{} `json:"download_clients"`
	}
	api.get(t, "/download-clients", http.StatusOK, &clients)
	if len(clients.DownloadClients) != 0 {
		t.Fatalf("the test configured %d download clients; it must configure none", len(clients.DownloadClients))
	}

	downloadID := grabTheRelease(t, api, movieID)

	// ---- the queue reports the stages ------------------------------------
	// Phases are live and each one is short, so this records what it saw
	// rather than requiring a particular phase to be caught mid-flight.
	seenPhases := map[string]bool{}
	waitFor(t, 90*time.Second, "the usenet download to finish every stage", func() string {
		row, ok := queueRowFor(t, api, downloadID)
		if !ok {
			return "the download is not in the queue"
		}
		if row.Phase != "" {
			seenPhases[row.Phase] = true
		}
		switch row.State {
		case "completed":
			return ""
		case "failed":
			t.Fatalf("the download failed: %s", row.Error)
			return ""
		default:
			return fmt.Sprintf("state %s phase %q progress %.2f", row.State, row.Phase, row.Progress)
		}
	})
	t.Logf("phases observed: %v", seenPhases)

	// The repair stage really ran. par2 volumes are fetched only when the
	// content came back with holes (SPEC §5.1), so this is both "the repair
	// happened" and "a clean release would not have paid for it".
	if !fx.askedFor(articlesAsked(), fx.Par2Files...) {
		t.Error("no par2 recovery volume was ever fetched, so the damaged article was never repaired")
	}

	row, _ := queueRowFor(t, api, downloadID)
	if row.Engine != usenet.EngineName {
		t.Errorf("queue row engine = %q, want %q", row.Engine, usenet.EngineName)
	}
	// The save path stays storage-root-relative (SPEC §1.2 pillar 3).
	if filepath.IsAbs(row.SavePath) {
		t.Errorf("save path %q is absolute; the built-in engine writes under the storage root", row.SavePath)
	}

	// ---- repair and extraction really happened ---------------------------
	downloadDir := filepath.Join(dirs.storage, filepath.FromSlash(row.SavePath))
	entries, err := os.ReadDir(downloadDir)
	if err != nil {
		t.Fatalf("read the download directory: %v", err)
	}
	var left []string
	for _, e := range entries {
		left = append(left, e.Name())
	}
	// The extract stage removes the archive and the recovery volumes once it
	// has verified what it unpacked, so what is left is the media.
	for _, name := range left {
		if strings.HasSuffix(name, ".zip") || strings.HasSuffix(name, ".par2") {
			t.Errorf("the archive debris %q survived extraction; the directory holds %v", name, left)
		}
	}

	// ---- and the import landed it in the library -------------------------
	want := filepath.Join(dirs.storage, filepath.FromSlash(smokeWantPath))
	waitFor(t, 90*time.Second, "the import to land the file in the library", func() string {
		if _, err := os.Stat(want); err != nil {
			return err.Error()
		}
		return ""
	})

	// The strongest assertion available: the file in the library is byte for
	// byte what was posted. It travelled through a damaged article, a
	// Reed-Solomon repair and an unpack to get there, so anything less than an
	// exact match means one of those three quietly corrupted it.
	got, err := os.ReadFile(want)
	if err != nil {
		t.Fatalf("read the imported file: %v", err)
	}
	sum := sha256.Sum256(got)
	if hex.EncodeToString(sum[:]) != fx.ContentSHA256 {
		t.Fatalf("the imported file is not what was posted:\n got sha256 %s (%d bytes)\nwant sha256 %s (%d bytes)",
			hex.EncodeToString(sum[:]), len(got), fx.ContentSHA256, fx.ContentSize)
	}
	t.Logf("imported %s (%d bytes, sha256 verified)", smokeWantPath, len(got))

	// The movie is no longer wanted, which is what the whole path was for.
	var movie struct {
		File *struct {
			Path string `json:"path"`
		} `json:"file"`
	}
	api.get(t, fmt.Sprintf("/library/movies/%d", movieID), http.StatusOK, &movie)
	if movie.File == nil {
		t.Fatal("the movie still reports no file after the import")
	}
	if movie.File.Path != smokeWantPath {
		t.Errorf("the movie's file is %q, want %q", movie.File.Path, smokeWantPath)
	}
}

// Damage beyond the release's recovery budget is a failure the user can act
// on, and "act on" means the queue says how many blocks short it was rather
// than that something went wrong.
func TestSmokeUsenetUnrepairableReportsTheDeficit(t *testing.T) {
	if testing.Short() {
		t.Skip("end-to-end: runs a real NNTP transfer and par2 verification")
	}
	allowPrivateReleasePayloadsForSmoke(t)

	fx := loadUsenetFixture(t)
	dirs := smokeDirs(t)
	movieID := seedSmokeLibrary(t, dirs)
	redirectTMDB(t, startFakeTMDB(t))

	news, _ := startSmokeNNTP(t)
	// Five damaged articles: ten par2 blocks against a budget of six, so the
	// release is four blocks short of repairable.
	doc := postRelease(t, news, fx, 5)
	indexerURL := startFakeNewznab(t, doc, len(fx.files[fx.ArchiveName]))

	api := startCaravan(t, dirs)
	configureUsenet(t, api, news, indexerURL)
	downloadID := grabTheRelease(t, api, movieID)

	var failure queueRow
	waitFor(t, 90*time.Second, "the unrepairable download to fail", func() string {
		row, ok := queueRowFor(t, api, downloadID)
		if !ok {
			return "the download is not in the queue"
		}
		if row.State == "failed" {
			failure = row
			return ""
		}
		if row.State == "completed" {
			t.Fatalf("a release damaged past its recovery budget completed anyway: %+v", row)
		}
		return fmt.Sprintf("state %s phase %q", row.State, row.Phase)
	})

	t.Logf("failure reason: %s", failure.Error)
	// The specific numbers, not a generic "repair failed": the deficit is what
	// tells a user whether a better-stocked backup provider would have saved
	// this release.
	for _, want := range []string{"unrepairable", "recovery block", "short"} {
		if !strings.Contains(failure.Error, want) {
			t.Errorf("failure %q does not mention %q", failure.Error, want)
		}
	}
	if !strings.Contains(failure.Error, fmt.Sprint(fx.RecoveryBlocks)) {
		t.Errorf("failure %q does not say how many recovery blocks the release carried (%d)",
			failure.Error, fx.RecoveryBlocks)
	}

	// A failed download must not be half-imported: nothing reaches the library.
	if _, err := os.Stat(filepath.Join(dirs.storage, library.LibraryDir, library.MoviesDir)); err == nil {
		entries, _ := os.ReadDir(filepath.Join(dirs.storage, library.LibraryDir, library.MoviesDir))
		if len(entries) != 0 {
			t.Errorf("an unrepairable download put %d entries in the library", len(entries))
		}
	}
}
