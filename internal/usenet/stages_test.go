package usenet

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/usenet/nntp"
	"github.com/watzon/caravan/internal/usenet/nntptest"
	"github.com/watzon/caravan/internal/usenet/par2"
	"github.com/watzon/caravan/internal/usenet/pipeline"
	"github.com/watzon/caravan/internal/usenet/yenc"
)

// breakFileCRC rewrites the whole-file "crc32=" a final part declares, leaving
// every per-part pcrc32 intact.
//
// This is the poster who posted from an already-corrupt source, or who omitted
// pcrc32 on the parts that rotted: every article arrives, decodes and verifies,
// and only the checksum over the finished file says the release is not what it
// claims to be.
func breakFileCRC(t *testing.T, article []byte) []byte {
	t.Helper()
	// " crc32=" with the leading space matches only the file checksum;
	// "pcrc32=" is preceded by a 'p'.
	i := bytes.Index(article, []byte(" crc32="))
	if i < 0 {
		t.Fatalf("article carries no whole-file crc32:\n%s", article)
	}
	out := bytes.Clone(article)
	digit := i + len(" crc32=")
	if out[digit] == '0' {
		out[digit] = '1'
	} else {
		out[digit] = '0'
	}
	return out
}

// A download where every article arrived is not a download that is right. The
// whole-file crc32 the poster declared is the only check a release posted as
// plain files ever gets, and before this it was recorded and never read.
func TestEngineCatchesAFileThatFailsThePostersChecksum(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	body := []byte(strings.Repeat("caravan checksum payload\n", 200))
	rel := stage(t, nntpSrv, map[string][]byte{"movie.mkv": body}, 500)

	// Re-post the final article with a wrong whole-file checksum. Everything
	// else about the posting is untouched, so the pipeline sees no failures.
	articles, err := yenc.EncodeFile("movie.mkv", body, 500)
	if err != nil {
		t.Fatalf("EncodeFile: %v", err)
	}
	last := len(articles)
	nntpSrv.Add(yenc.MessageID("movie.mkv", last), breakFileCRC(t, articles[last-1]))

	e, root := newTestEngine(t, nntpSrv, newMemStore())
	id := addRelease(t, e, rel, "Bad.Checksum.Release")
	st := waitForState(t, e, id, core.DownloadFailed)

	if !strings.Contains(st.Error, "checksum") {
		t.Errorf("failure = %q, want it to say the file failed its checksum", st.Error)
	}
	if !strings.Contains(st.Error, "movie.mkv") {
		t.Errorf("failure = %q, want it to name the file", st.Error)
	}
	if !strings.Contains(st.Error, "no par2") {
		t.Errorf("failure = %q, want it to say there is nothing to repair it with", st.Error)
	}
	// And a download that failed verification must not be marked assembled:
	// nothing about it is finished.
	if _, err := os.Stat(filepath.Join(root, download.IncompleteDir, metaDir, string(id)+".done")); !os.IsNotExist(err) {
		t.Errorf("a download that failed its checksum was marked assembled: %v", err)
	}
}

// The same release without the tampering has to complete, or the check above
// is just a decoder that rejects everything.
func TestEngineAcceptsAFileThatMatchesThePostersChecksum(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	body := []byte(strings.Repeat("caravan checksum payload\n", 200))
	rel := stage(t, nntpSrv, map[string][]byte{"movie.mkv": body}, 500)

	e, _ := newTestEngine(t, nntpSrv, newMemStore())
	id := addRelease(t, e, rel, "Good.Checksum.Release")
	waitForState(t, e, id, core.DownloadCompleted)
}

// stagePar2Corpus posts the par2 package's par2cmdline-generated set — its
// three source files and its recovery volumes — plus whatever extra files the
// caller wants, which the set deliberately says nothing about.
func stagePar2Corpus(t *testing.T, srv *nntptest.Server, extra map[string][]byte) release {
	t.Helper()

	files := map[string][]byte{}
	for _, name := range []string{"alpha.bin", "beta.bin", "gamma.bin"} {
		files[name] = readCorpusFile(t, filepath.Join("par2", "testdata", "sets", "basic", "original", name))
	}
	for _, name := range []string{"basic.par2", "basic.vol00+1.par2", "basic.vol01+2.par2", "basic.vol03+4.par2", "basic.vol07+3.par2"} {
		files[name] = readCorpusFile(t, filepath.Join("par2", "testdata", "sets", "basic", "par2", name))
	}
	for name, data := range extra {
		files[name] = data
	}
	return stage(t, srv, files, 1024)
}

func readCorpusFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

// A par2 set only vouches for the files it describes. A hole in a file it does
// not cover used to end as "completed": verification found nothing wrong,
// because it never looked, and the import took a file with a hole in it.
func TestEngineFailsWhenTheHoleIsInAFilePar2DoesNotCover(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	uncovered := []byte(strings.Repeat("this file is not in the par2 set\n", 200))
	rel := stagePar2Corpus(t, nntpSrv, map[string][]byte{"extra.nfo": uncovered})

	// A hole in the file nothing covers. Every covered file is intact, so par2
	// verification is going to come back clean.
	nntpSrv.Remove(yenc.MessageID("extra.nfo", 2))

	e, _ := newTestEngine(t, nntpSrv, newMemStore())
	id := addRelease(t, e, rel, "Uncovered.Hole.Release")
	st := waitForState(t, e, id, core.DownloadFailed)

	if !strings.Contains(st.Error, "does not cover") {
		t.Errorf("failure = %q, want it to say the par2 set does not cover the damage", st.Error)
	}
	if !strings.Contains(st.Error, "extra.nfo") {
		t.Errorf("failure = %q, want it to name the uncovered file", st.Error)
	}
}

// The same release with the hole in a file the set *does* cover has to be
// repaired and completed, or the check above is a blanket refusal.
func TestEngineRepairsAHolePar2Covers(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	rel := stagePar2Corpus(t, nntpSrv, nil)
	nntpSrv.Remove(yenc.MessageID("beta.bin", 2))

	e, root := newTestEngine(t, nntpSrv, newMemStore())
	id := addRelease(t, e, rel, "Covered.Hole.Release")
	st := waitForState(t, e, id, core.DownloadCompleted)

	got := readCorpusFile(t, filepath.Join(root, filepath.FromSlash(st.SavePath), "beta.bin"))
	if want := rel.files["beta.bin"]; !bytes.Equal(got, want) {
		t.Errorf("repaired beta.bin is %d bytes, want %d", len(got), len(want))
	}
}

// notCovered is the cross-check the message above is built on, and the base
// name case is what keeps a set that records "sub/dir/movie.mkv" from looking
// like a set that covers nothing.
func TestNotCoveredMatchesOnTheNameOrItsBase(t *testing.T) {
	rep := &par2.Report{Files: []par2.FileStatus{
		{Name: "movie.part01.rar"},
		{Name: "sub/dir/movie.mkv"},
	}}
	got := notCovered(rep, []string{"movie.part01.rar", "movie.mkv", "extra.nfo"})
	if len(got) != 1 || got[0] != "extra.nfo" {
		t.Fatalf("notCovered = %v, want [extra.nfo]", got)
	}
	if notCovered(nil, []string{"anything"}) != nil {
		t.Error("a nil report reported files as uncovered")
	}
}

// The window between extraction finishing and the completed row being written
// used to cost the whole release: the next start saw state=downloading with no
// sidecar, refetched every article from the provider, and then failed on the
// files extraction had already put in place.
func TestEngineDoesNotRefetchAfterACrashPastExtraction(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	payload := []byte(strings.Repeat("assembled once\n", 400))
	rel := stage(t, nntpSrv, map[string][]byte{"movie.mkv": payload}, 500)

	store := newMemStore()
	root := t.TempDir()
	opts := func() EngineOpts {
		return EngineOpts{
			Servers:      []nntp.ServerConfig{testServer(nntpSrv)},
			NNTP:         fastRetry(),
			Store:        store,
			PollInterval: 10 * time.Millisecond,
		}
	}

	first, err := NewEngine(root, opts())
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	id, err := first.Add(context.Background(), core.Release{
		Title: "Crashed.After.Extract", Protocol: core.ProtocolUsenet,
		DownloadURL: serveNZB(t, rel.nzb),
	}, core.AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	st := waitForState(t, first, id, core.DownloadCompleted)
	if err := first.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Exactly the crash window: the stages all finished, the row never made it
	// to "completed". And the provider has nothing left to give, so any attempt
	// to fetch a single article is a failure rather than a silent re-download.
	row, ok := store.get(id)
	if !ok {
		t.Fatal("nothing persisted for the completed download")
	}
	row.State = core.DownloadDownloading
	if err := store.Save(context.Background(), row); err != nil {
		t.Fatalf("Save: %v", err)
	}
	for i := 1; ; i++ {
		msgID := yenc.MessageID("movie.mkv", i)
		if i > 100 {
			break
		}
		nntpSrv.Remove(msgID)
	}
	before := len(nntpSrv.Commands())

	second, err := NewEngine(root, opts())
	if err != nil {
		t.Fatalf("NewEngine after restart: %v", err)
	}
	defer second.Close()

	waitForState(t, second, id, core.DownloadCompleted)
	for _, cmd := range nntpSrv.Commands()[before:] {
		if strings.HasPrefix(strings.ToUpper(cmd), "BODY ") {
			t.Fatalf("the restarted engine refetched an article: %q", cmd)
		}
	}
	// And the data is still the data.
	got := readCorpusFile(t, filepath.Join(root, filepath.FromSlash(st.SavePath), "movie.mkv"))
	if !bytes.Equal(got, payload) {
		t.Errorf("the restarted download reports complete over %d bytes, want %d", len(got), len(payload))
	}
	// The byte totals restored with the record survive too: a stage that
	// counted nothing must not rewrite them with zeros.
	after, err := second.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if after.Size != st.Size || after.Size == 0 {
		t.Errorf("size after the marker-driven completion = %d, want %d", after.Size, st.Size)
	}
}

// Extraction needs roughly a second copy of the payload, and the download
// preflight budgets none of it. Finding that out with ENOSPC halfway through
// staging wastes the whole transfer and Resume repeats it, so the check has to
// come before a single entry is written.
func TestEngineRefusesToUnpackWithoutRoomForIt(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	archive := readCorpusFile(t, filepath.Join("extract", "testdata", "single.rar"))
	rel := stage(t, nntpSrv, map[string][]byte{"release.rar": archive}, 512)

	// The first measurement is the download preflight and has to pass; every
	// one after it is the extraction preflight, which must not.
	var calls atomic.Int64
	root := t.TempDir()
	e, err := NewEngine(root, EngineOpts{
		Servers:      []nntp.ServerConfig{testServer(nntpSrv)},
		NNTP:         fastRetry(),
		Store:        newMemStore(),
		PollInterval: 10 * time.Millisecond,
		FreeSpace: func(string) (int64, error) {
			if calls.Add(1) == 1 {
				return 1 << 40, nil
			}
			return 1024, nil
		},
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer e.Close()

	id := addRelease(t, e, rel, "No.Room.To.Unpack")
	st := waitForState(t, e, id, core.DownloadFailed)

	if !strings.Contains(st.Error, "not enough free disk space") {
		t.Errorf("failure = %q, want it to name the disk as the problem", st.Error)
	}
	// The articles are still on disk: a refused unpack is not a lost download,
	// and Resume must not have to pay for them again.
	dir := filepath.Join(root, filepath.FromSlash(st.SavePath))
	if _, err := os.Stat(filepath.Join(dir, "release.rar")); err != nil {
		t.Errorf("a refused unpack threw the downloaded archive away: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, pipeline.StateFile)); err != nil {
		t.Errorf("a refused unpack removed the resume sidecar: %v", err)
	}
	if calls.Load() < 2 {
		t.Errorf("free space was measured %d times, want the extraction preflight to have run", calls.Load())
	}
}
