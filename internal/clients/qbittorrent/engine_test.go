package qbittorrent

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/watzon/caravan/internal/core"
)

const (
	movieHash    = "0123456789abcdef0123456789abcdef01234567"
	seedingHash  = "89abcdef0123456789abcdef0123456789abcdef"
	stalledHash  = "fedcba9876543210fedcba9876543210fedcba98"
	untaggedHash = "1111111111111111111111111111111111111111"
)

// The state table is the contract between qBittorrent's vocabulary and
// Caravan's, and the import watcher fires on two of the six values, so every
// state qBittorrent can serialize is pinned here.
func TestStateMapping(t *testing.T) {
	tests := []struct {
		qbit string
		want core.DownloadState
	}{
		{stateError, core.DownloadFailed},
		{stateMissingFiles, core.DownloadFailed},

		{stateUploading, core.DownloadSeeding},
		{stateQueuedUP, core.DownloadSeeding},
		{stateStalledUP, core.DownloadSeeding},
		{stateForcedUP, core.DownloadSeeding},
		{stateCheckingUP, core.DownloadSeeding},

		// Finished but stopped: importable, not paused.
		{stateStoppedUP, core.DownloadCompleted},
		{statePausedUP, core.DownloadCompleted},

		{stateStoppedDL, core.DownloadPaused},
		{statePausedDL, core.DownloadPaused},

		{stateQueuedDL, core.DownloadQueued},

		{stateAllocating, core.DownloadDownloading},
		{stateDownloading, core.DownloadDownloading},
		{stateMetaDL, core.DownloadDownloading},
		{stateForcedMetaDL, core.DownloadDownloading},
		{stateStalledDL, core.DownloadDownloading},
		{stateForcedDL, core.DownloadDownloading},
		{stateCheckingDL, core.DownloadDownloading},
		// Still touching the files: importing now would copy a moving target.
		{stateCheckingResumeData, core.DownloadDownloading},
		{stateMoving, core.DownloadDownloading},

		// Nothing is claimed for a state we do not recognise.
		{stateUnknown, core.DownloadQueued},
		{"someFutureState", core.DownloadQueued},
	}
	for _, tt := range tests {
		t.Run(tt.qbit, func(t *testing.T) {
			if got := mapState(tt.qbit); got != tt.want {
				t.Fatalf("mapState(%q) = %q, want %q", tt.qbit, got, tt.want)
			}
		})
	}
}

func TestStatusConversion(t *testing.T) {
	torrents := loadTorrents(t, "torrents_info.json")

	downloading := status(torrents[0])
	want := core.DownloadStatus{
		ID:         movieHash,
		State:      core.DownloadDownloading,
		Name:       "Arrival.2016.1080p.BluRay.x264-GROUP",
		Progress:   0.5,
		BytesDone:  4294967296,
		Size:       8589934592,
		DownRate:   5242880,
		ETASeconds: 819,
		SavePath:   "/downloads/Arrival.2016.1080p.BluRay.x264-GROUP",
	}
	if downloading != want {
		t.Fatalf("status =\n%+v\nwant\n%+v", downloading, want)
	}

	// A seeding torrent reports qBittorrent's 100-day sentinel for its ETA,
	// which core.DownloadStatus spells -1.
	seeding := status(torrents[1])
	if seeding.State != core.DownloadSeeding {
		t.Fatalf("state = %q, want seeding", seeding.State)
	}
	if seeding.ETASeconds != -1 {
		t.Fatalf("eta = %d, want -1 for qBittorrent's infinity sentinel", seeding.ETASeconds)
	}
	if seeding.Ratio != 1.75 || seeding.UpRate != 1048576 {
		t.Fatalf("ratio/up = %v/%d", seeding.Ratio, seeding.UpRate)
	}
}

func TestStatusFallsBackToSavePathAndNamesFailures(t *testing.T) {
	got := status(Torrent{Hash: "h", State: stateMissingFiles, SavePath: "/downloads", ContentPath: ""})
	if got.SavePath != "/downloads" {
		t.Fatalf("save path = %q, want the fallback", got.SavePath)
	}
	if got.Error == "" {
		t.Fatalf("missingFiles carried no error message")
	}
	if got.State != core.DownloadFailed {
		t.Fatalf("state = %q, want failed", got.State)
	}
	if healthy := status(Torrent{Hash: "h", State: stateDownloading}); healthy.Error != "" {
		t.Fatalf("healthy torrent carried an error: %q", healthy.Error)
	}
}

func TestStatusClampsNonsenseValues(t *testing.T) {
	got := status(Torrent{Hash: "h", State: stateDownloading, Progress: 1.5, Ratio: -1, DlSpeed: -1})
	if got.Progress != 1 || got.Ratio != 0 || got.DownRate != 0 {
		t.Fatalf("status = %+v, want clamped values", got)
	}
}

func TestListReturnsOnlyCaravansTorrents(t *testing.T) {
	e, _ := newEngine(t)

	list, err := e.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("list = %d, want the 3 tagged torrents", len(list))
	}
	for _, s := range list {
		if s.ID == untaggedHash {
			t.Fatalf("an untagged torrent leaked into the queue")
		}
	}
}

// A qBittorrent older than WebAPI 2.8.3 ignores the tag filter and answers with
// the whole queue. Without the second pass, the user's own torrents would
// appear in Caravan's queue: and be removable from it.
func TestListReFiltersWhenTheServerIgnoresTheTagParameter(t *testing.T) {
	e, f := newEngine(t)
	f.ignoresTagFilter = true

	list, err := e.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("list = %d, want the 3 tagged torrents", len(list))
	}
	for _, s := range list {
		if s.ID == untaggedHash {
			t.Fatalf("an untagged torrent leaked into the queue")
		}
	}
}

func TestStatusByID(t *testing.T) {
	e, _ := newEngine(t)

	got, err := e.Status(context.Background(), seedingHash)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.ID != seedingHash || got.State != core.DownloadSeeding {
		t.Fatalf("status = %+v", got)
	}
}

func TestStatusOfAnUnknownHashIsNotFound(t *testing.T) {
	e, _ := newEngine(t)

	_, err := e.Status(context.Background(), "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestAddMagnetReturnsTheInfoHashWithoutPolling(t *testing.T) {
	e, f := newEngine(t)
	e.cfg.Category = "caravan-movies"

	id, err := e.Add(context.Background(), core.Release{
		Title:       "Sicario.2015.1080p.BluRay-GROUP",
		Protocol:    core.ProtocolTorrent,
		DownloadURL: "magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01&dn=Sicario",
	}, core.AddOpts{Category: "movies"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("id = %q, want the lowercased magnet info hash", id)
	}
	// The hash was knowable, so the queue was never listed.
	if got := len(f.seen("/torrents/info")); got != 0 {
		t.Fatalf("info calls = %d, want 0 when the info hash is known", got)
	}
	form := f.seen("/torrents/add")[0].Form
	if form.Get("category") != "caravan-movies" {
		t.Fatalf("category = %q, want the client's own configured category", form.Get("category"))
	}
	if form.Get("tags") != Tag {
		t.Fatalf("tags = %q, want %q", form.Get("tags"), Tag)
	}
}

func TestAddPrefersTheIndexersInfoHash(t *testing.T) {
	e, _ := newEngine(t)

	id, err := e.Add(context.Background(), core.Release{
		Title:       "Sicario.2015.1080p.BluRay-GROUP",
		Protocol:    core.ProtocolTorrent,
		DownloadURL: "https://indexer.example/download/1234.torrent",
		InfoHash:    "ABCDEF0123456789ABCDEF0123456789ABCDEF01",
	}, core.AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id != "abcdef0123456789abcdef0123456789abcdef01" {
		t.Fatalf("id = %q", id)
	}
}

// A .torrent URL from an indexer that reports no info hash: qBittorrent has to
// fetch and parse it, and its add endpoint says nothing about what it produced.
func TestAddDiscoversTheHashOfATorrentFile(t *testing.T) {
	e, f := newEngine(t)
	added := "2222222222222222222222222222222222222222"
	f.onAdd = func(f *fakeQB, form url.Values) {
		f.add(Torrent{
			Hash:     added,
			Name:     "Sicario.2015.1080p.BluRay-GROUP",
			State:    stateMetaDL,
			Tags:     form.Get("tags"),
			Category: form.Get("category"),
		})
	}

	id, err := e.Add(context.Background(), core.Release{
		Title:       "Sicario.2015.1080p.BluRay-GROUP",
		Protocol:    core.ProtocolTorrent,
		DownloadURL: "https://indexer.example/download/1234.torrent",
	}, core.AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id != core.DownloadID(added) {
		t.Fatalf("id = %q, want the newly tagged torrent %q", id, added)
	}
}

// qBittorrent 5.0 and older answer "added nothing" with HTTP 200 and the body
// "Fails.", so the status alone cannot be believed.
//
// The magnet path is where this bites: the info hash comes from the magnet, so
// Add returns immediately without ever confirming the torrent exists. Trusting
// the 200 records the grab as succeeded and writes a `downloads` row for a
// handle qBittorrent rejected: a queue row that never progresses, never
// imports, and is never retried, because a grab that is not failed is not
// retried.
func TestAddFailsWhenQBittorrentRejectsItWithHTTP200(t *testing.T) {
	e, f := newEngine(t)
	f.rejectsAdd = true

	id, err := e.Add(context.Background(), core.Release{
		Title:       "Sicario.2015.1080p.BluRay-GROUP",
		Protocol:    core.ProtocolTorrent,
		DownloadURL: "magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01",
	}, core.AddOpts{})
	if err == nil {
		t.Fatalf("Add reported success for a torrent qBittorrent refused, handing back id %q", id)
	}
	if !strings.Contains(err.Error(), "Fails.") {
		t.Errorf("error = %v, want it to carry qBittorrent's own refusal", err)
	}
}

func TestAddFailsWhenTheTorrentNeverAppears(t *testing.T) {
	e, _ := newEngine(t)

	_, err := e.Add(context.Background(), core.Release{
		Title:       "Never.Arrives",
		Protocol:    core.ProtocolTorrent,
		DownloadURL: "https://indexer.example/download/gone.torrent",
	}, core.AddOpts{})
	if err == nil {
		t.Fatalf("Add succeeded for a torrent qBittorrent never produced")
	}
}

func TestAddUploadsResolvedTorrentPayloadInsteadOfRefetchingIndexerURL(t *testing.T) {
	engine, fake := newEngine(t)
	engine.cfg.Category = "movies"
	payload, payloadHash := testTorrentPayload(t)
	id, err := engine.Add(context.Background(), core.Release{
		Title:          "Payload Torrent",
		Protocol:       core.ProtocolTorrent,
		DownloadURL:    "https://tracker.example/private.torrent",
		TorrentPayload: payload,
		InfoHash:       payloadHash,
	}, core.AddOpts{Paused: true})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id != core.DownloadID(payloadHash) {
		t.Fatalf("id = %q", id)
	}
	payloads := fake.payloads()
	if len(payloads) != 1 || !bytes.Equal(payloads[0], payload) {
		t.Fatalf("uploaded payloads = %q", payloads)
	}
	addCalls := fake.seen("/torrents/add")
	if len(addCalls) != 1 {
		t.Fatalf("qBittorrent add calls = %d, want 1", len(addCalls))
	}
	addCall := addCalls[0]
	if got := addCall.Form.Get("urls"); got != "" {
		t.Fatalf("qBittorrent URL = %q, want uploaded bytes", got)
	}
	if got := addCall.Form.Get("category"); got != "movies" {
		t.Fatalf("qBittorrent category = %q, want movies", got)
	}
	if got := addCall.Form.Get("paused"); got != "true" {
		t.Fatalf("qBittorrent paused = %q, want true", got)
	}
	if got := addCall.Form.Get("stopped"); got != "true" {
		t.Fatalf("qBittorrent stopped = %q, want true", got)
	}
	if len(addCall.TorrentFiles) != 1 || addCall.TorrentFiles[0] != "caravan.torrent" {
		t.Fatalf("qBittorrent torrent parts = %q, want one caravan.torrent", addCall.TorrentFiles)
	}
}

func testTorrentPayload(t *testing.T) ([]byte, string) {
	t.Helper()
	infoBytes, err := bencode.Marshal(metainfo.Info{
		Name: "payload.bin", Length: 1, PieceLength: 1, Pieces: make([]byte, 20),
	})
	if err != nil {
		t.Fatalf("marshal torrent info: %v", err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}
	var payload bytes.Buffer
	if err := mi.Write(&payload); err != nil {
		t.Fatalf("write torrent payload: %v", err)
	}
	return payload.Bytes(), fmt.Sprintf("%x", mi.HashInfoBytes())
}

func TestAddRejectsMalformedTorrentPayloadBeforeCallingQBittorrent(t *testing.T) {
	engine, fake := newEngine(t)
	_, err := engine.Add(context.Background(), core.Release{
		Title:          "Malformed Payload",
		Protocol:       core.ProtocolTorrent,
		TorrentPayload: []byte("not bencoded metainfo"),
	}, core.AddOpts{})
	if err == nil || !strings.Contains(err.Error(), "torrent payload") {
		t.Fatalf("Add error = %v, want malformed-payload rejection", err)
	}
	if calls := fake.seen("/torrents/add"); len(calls) != 0 {
		t.Fatalf("qBittorrent add calls = %d, want 0", len(calls))
	}
}

func TestAddRejectsEmptyTorrentInfoDictionaryBeforeCallingQBittorrent(t *testing.T) {
	engine, fake := newEngine(t)
	_, err := engine.Add(context.Background(), core.Release{
		Title:          "Empty Info Payload",
		Protocol:       core.ProtocolTorrent,
		TorrentPayload: []byte("d4:infodee"),
	}, core.AddOpts{})
	if err == nil || !strings.Contains(err.Error(), "torrent payload") {
		t.Fatalf("Add error = %v, want invalid-info rejection", err)
	}
	if calls := fake.seen("/torrents/add"); len(calls) != 0 {
		t.Fatalf("qBittorrent add calls = %d, want 0", len(calls))
	}
}

func TestAddRejectsUsenetAndEmptyURLs(t *testing.T) {
	e, _ := newEngine(t)

	if _, err := e.Add(context.Background(), core.Release{
		Title:       "Some.Release",
		Protocol:    core.ProtocolUsenet,
		DownloadURL: "https://indexer.example/getnzb/1.nzb",
	}, core.AddOpts{}); err == nil {
		t.Fatalf("Add accepted a usenet release")
	}

	if _, err := e.Add(context.Background(), core.Release{
		Title:    "Some.Release",
		Protocol: core.ProtocolTorrent,
	}, core.AddOpts{}); err == nil {
		t.Fatalf("Add accepted a release with no download url")
	}
}

func TestPauseAndResume(t *testing.T) {
	e, f := newEngine(t)
	ctx := context.Background()

	if err := e.Pause(ctx, movieHash); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	got, err := e.Status(ctx, movieHash)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.State != core.DownloadPaused {
		t.Fatalf("state = %q, want paused", got.State)
	}

	if err := e.Resume(ctx, movieHash); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got, err = e.Status(ctx, movieHash); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.State != core.DownloadDownloading {
		t.Fatalf("state = %q, want downloading", got.State)
	}
	if n := len(f.seen("/torrents/stop")) + len(f.seen("/torrents/start")); n != 2 {
		t.Fatalf("stop/start calls = %d, want 2", n)
	}
}

// A finished torrent the user stopped is completed, not paused: the import
// watcher must still pick it up.
func TestStoppingAFinishedTorrentLeavesItImportable(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()

	if err := e.Pause(ctx, seedingHash); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	got, err := e.Status(ctx, seedingHash)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.State != core.DownloadCompleted {
		t.Fatalf("state = %q, want completed", got.State)
	}
}

func TestRemove(t *testing.T) {
	e, f := newEngine(t)
	ctx := context.Background()

	if err := e.Remove(ctx, stalledHash, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if got := f.seen("/torrents/delete")[0].Form.Get("deleteFiles"); got != "true" {
		t.Fatalf("deleteFiles = %q, want true", got)
	}
	if _, err := e.Status(ctx, stalledHash); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound after removal", err)
	}
}

func TestEngineFiles(t *testing.T) {
	e, f := newEngine(t)
	f.setFiles(movieHash, loadFiles(t, "torrents_files.json"))

	files, err := e.Files(context.Background(), movieHash)
	if err != nil {
		t.Fatalf("Files: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("files = %d, want 2", len(files))
	}

	if _, err := e.Files(context.Background(), "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestCloseForgetsTheSession(t *testing.T) {
	e, f := newEngine(t)
	ctx := context.Background()

	if _, err := e.List(ctx); err != nil {
		t.Fatalf("List: %v", err)
	}
	if err := e.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if _, err := e.List(ctx); err != nil {
		t.Fatalf("List after Close: %v", err)
	}
	if got := f.loginCount(); got != 2 {
		t.Fatalf("logins = %d, want 2 (Close drops the session)", got)
	}
}

func TestMagnetHash(t *testing.T) {
	tests := []struct {
		name string
		link string
		want string
	}{
		{"hex", "magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01&dn=x", "abcdef0123456789abcdef0123456789abcdef01"},
		{"extra xt first", "magnet:?xt=urn:ed2k:deadbeef&xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01", "abcdef0123456789abcdef0123456789abcdef01"},
		{"base32 is not usable as an id", "magnet:?xt=urn:btih:MFRGGZDFMZTWQ2LKNNWG23TPOA", ""},
		{"not a magnet", "https://indexer.example/1.torrent", ""},
		{"no xt", "magnet:?dn=x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := magnetHash(tt.link); got != tt.want {
				t.Fatalf("magnetHash = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNormalizeHash(t *testing.T) {
	tests := []struct{ in, want string }{
		{"ABCDEF0123456789ABCDEF0123456789ABCDEF01", "abcdef0123456789abcdef0123456789abcdef01"},
		{"  abcdef0123456789abcdef0123456789abcdef01  ", "abcdef0123456789abcdef0123456789abcdef01"},
		{"not-a-hash", ""},
		{"zzcdef0123456789abcdef0123456789abcdef01", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := normalizeHash(tt.in); got != tt.want {
			t.Fatalf("normalizeHash(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestHasTag(t *testing.T) {
	tests := []struct {
		tags string
		want bool
	}{
		{"caravan", true},
		{"manual, caravan", true},
		{"manual,caravan,other", true},
		{"", false},
		{"caravanish", false},
		{"not-caravan", false},
	}
	for _, tt := range tests {
		if got := hasTag(tt.tags, Tag); got != tt.want {
			t.Fatalf("hasTag(%q) = %v, want %v", tt.tags, got, tt.want)
		}
	}
}

func TestEngineName(t *testing.T) {
	if EngineName != "qbittorrent" {
		t.Fatalf("EngineName = %q", EngineName)
	}
}

// qBittorrent reads an empty `hashes` parameter as "no filter" and answers
// with the whole queue. Trusting the first row would report a stranger's
// torrent as this download.
func TestStatusOfAnEmptyIDIsNotFound(t *testing.T) {
	e, _ := newEngine(t)

	if _, err := e.Status(context.Background(), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
