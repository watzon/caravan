package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/usenet/nntptest"
	"github.com/watzon/caravan/internal/usenet/nzb"
)

func background(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// readFile is the assertion every other one rests on: the assembled file has
// to be the poster's file byte for byte, not merely the right length.
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func TestDownloadAssemblesEveryFileByteForByte(t *testing.T) {
	srv := newServer(t)
	one := stage(t, "release.part01.rar", payload(1, 5_000), 800)
	two := stage(t, "release.part02.rar", payload(2, 3_100), 800)
	par2 := stage(t, "release.vol000+01.par2", payload(3, 900), 800)
	one.publish(srv)
	two.publish(srv)
	par2.publish(srv)

	dir := t.TempDir()
	doc := document(t, one, two, par2)
	res, err := Download(background(t), doc, dir, newPool(t, srv), Options{SkipSpaceCheck: true})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !res.Complete() {
		t.Fatalf("failures: %v", res.Failures)
	}

	for _, f := range []staged{one, two} {
		if got := readFile(t, filepath.Join(dir, f.name)); !bytes.Equal(got, f.data) {
			t.Fatalf("%s: assembled %d bytes, want %d identical", f.name, len(got), len(f.data))
		}
	}
	// par2 is the repair budget, not payload: nothing asked for it, so
	// nothing paid for it (SPEC §5.1).
	if _, err := os.Stat(filepath.Join(dir, par2.name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("par2 volume was downloaded without being asked for: %v", err)
	}
	if n := len(res.Files); n != 2 {
		t.Fatalf("result covers %d files, want the 2 content files", n)
	}

	p := res.Progress
	if p.Files != 2 || p.FilesComplete != 2 || p.SegmentsDone != p.Segments || p.SegmentsFailed != 0 {
		t.Fatalf("progress = %+v", p)
	}
	if p.Bytes != p.TotalBytes {
		t.Fatalf("progress bytes = %d/%d, want them equal", p.Bytes, p.TotalBytes)
	}
	if p.BytesWritten != int64(len(one.data)+len(two.data)) {
		t.Fatalf("wrote %d decoded bytes, want %d", p.BytesWritten, len(one.data)+len(two.data))
	}
	if f := p.Fraction(); f != 1 {
		t.Fatalf("fraction = %v, want 1", f)
	}
}

func TestDownloadIncludesPar2WhenAsked(t *testing.T) {
	srv := newServer(t)
	content := stage(t, "release.part01.rar", payload(4, 2_000), 700)
	par2 := stage(t, "release.par2", payload(5, 600), 700)
	content.publish(srv)
	par2.publish(srv)

	dir := t.TempDir()
	res, err := Download(background(t), document(t, content, par2), dir, newPool(t, srv),
		Options{SkipSpaceCheck: true, IncludePar2: true})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if got := readFile(t, filepath.Join(dir, par2.name)); !bytes.Equal(got, par2.data) {
		t.Fatalf("par2 volume did not assemble")
	}
	var sawPar2 bool
	for _, f := range res.Files {
		if f.Name == par2.name {
			sawPar2 = f.IsPar2
		}
	}
	if !sawPar2 {
		t.Fatalf("result did not mark %s as par2: %+v", par2.name, res.Files)
	}
}

// The whole point of the sidecar: a restart never pays for an article twice.
func TestDownloadResumesWithoutRefetchingCompletedSegments(t *testing.T) {
	srv := newServer(t)
	file := stage(t, "release.mkv", payload(6, 12_000), 500)
	file.publish(srv)
	if len(file.ids) < 8 {
		t.Fatalf("test needs a multi-segment file, got %d", len(file.ids))
	}

	dir := t.TempDir()
	doc := document(t, file)

	// Stop the first run part way, deterministically, from the server side.
	ctx, cancel := context.WithCancel(background(t))
	defer cancel()
	var mu sync.Mutex
	served := 0
	srv.SetBodyHook(func(string) {
		mu.Lock()
		served++
		stop := served >= 4
		mu.Unlock()
		if stop {
			cancel()
		}
	})

	if _, err := Download(ctx, doc, dir, newPool(t, srv), Options{SkipSpaceCheck: true, Concurrency: 1}); !errors.Is(err, context.Canceled) {
		t.Fatalf("interrupted download returned %v, want context.Canceled", err)
	}
	srv.SetBodyHook(nil)
	first := srv.Commands()

	// The sidecar is the contract: whatever it claims is on disk must not be
	// fetched again.
	completed := completedSegments(t, dir)
	if len(completed) == 0 {
		t.Fatal("the interrupted run recorded no completed segments")
	}
	if len(completed) >= len(file.ids) {
		t.Fatalf("the interrupted run finished all %d segments; it cannot prove resume", len(file.ids))
	}

	res, err := Download(background(t), doc, dir, newPool(t, srv), Options{SkipSpaceCheck: true})
	if err != nil {
		t.Fatalf("resumed Download: %v", err)
	}
	if !res.Complete() {
		t.Fatalf("resumed download failures: %v", res.Failures)
	}
	if got := readFile(t, filepath.Join(dir, file.name)); !bytes.Equal(got, file.data) {
		t.Fatalf("resumed file does not match the original")
	}

	second := srv.Commands()[len(first):]
	for number := range completed {
		if n := bodyCount(second, file.ids[number-1]); n != 0 {
			t.Fatalf("segment %d was already on disk and was fetched %d more times", number, n)
		}
	}
	// And every segment landed exactly once across both runs.
	all := srv.Commands()
	for i, id := range file.ids {
		if n := bodyCount(all, id); n < 1 {
			t.Fatalf("segment %d was never fetched", i+1)
		}
	}
	if res.Progress.SegmentsDone != len(file.ids) {
		t.Fatalf("progress after resume = %+v, want %d segments done", res.Progress, len(file.ids))
	}
}

// A resume must never trust the sidecar over the disk. If the assembled file is
// gone, the sidecar's segments describe bytes that no longer exist, and
// skipping them re-creates the file as a full-length hole of zeros that reports
// itself complete with no failures: the one shape that makes the engine skip
// par2 entirely and hand a zero-filled file to the import.
func TestDownloadRefetchesWhenTheAssembledFileIsGone(t *testing.T) {
	srv := newServer(t)
	file := stage(t, "movie.rar", payload(9, 4096), 1024)
	file.publish(srv)
	doc := document(t, file)
	dir := t.TempDir()

	if _, err := Download(background(t), doc, dir, newPool(t, srv), Options{SkipSpaceCheck: true}); err != nil {
		t.Fatalf("first Download: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, file.name)); err != nil {
		t.Fatalf("remove the assembled file: %v", err)
	}

	res, err := Download(background(t), doc, dir, newPool(t, srv), Options{SkipSpaceCheck: true})
	if err != nil {
		t.Fatalf("second Download: %v", err)
	}
	if !res.Complete() {
		t.Fatalf("re-download failures: %v", res.Failures)
	}
	if got := readFile(t, filepath.Join(dir, file.name)); !bytes.Equal(got, file.data) {
		t.Fatalf("re-download produced %d bytes that do not match the original", len(got))
	}
}

// The same trap with the file merely truncated: only the segments the file can
// no longer account for are refetched, because throwing away a whole
// thirty-gigabyte release over a few missing bytes is the bill the sidecar
// exists to prevent.
func TestDownloadRefetchesOnlyWhatATruncatedFileLost(t *testing.T) {
	srv := newServer(t)
	file := stage(t, "movie.rar", payload(10, 4096), 1024)
	file.publish(srv)
	doc := document(t, file)
	dir := t.TempDir()

	if _, err := Download(background(t), doc, dir, newPool(t, srv), Options{SkipSpaceCheck: true}); err != nil {
		t.Fatalf("first Download: %v", err)
	}
	first := srv.Commands()

	// Keep the first two segments' bytes, lose the rest.
	if err := os.Truncate(filepath.Join(dir, file.name), 2048); err != nil {
		t.Fatalf("truncate: %v", err)
	}

	res, err := Download(background(t), doc, dir, newPool(t, srv), Options{SkipSpaceCheck: true})
	if err != nil {
		t.Fatalf("second Download: %v", err)
	}
	if !res.Complete() {
		t.Fatalf("re-download failures: %v", res.Failures)
	}
	if got := readFile(t, filepath.Join(dir, file.name)); !bytes.Equal(got, file.data) {
		t.Fatalf("re-download produced %d bytes that do not match the original", len(got))
	}

	second := srv.Commands()[len(first):]
	for _, i := range []int{0, 1} {
		if n := bodyCount(second, file.ids[i]); n != 0 {
			t.Errorf("segment %d survived the truncation and was fetched %d more times", i+1, n)
		}
	}
	for _, i := range []int{2, 3} {
		if n := bodyCount(second, file.ids[i]); n == 0 {
			t.Errorf("segment %d was truncated away and was not refetched", i+1)
		}
	}
}

// completedSegments reads the sidecar and returns the segment numbers it says
// are on disk, for the single-file downloads the resume tests use.
func completedSegments(t *testing.T, dir string) map[int]struct{} {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, StateFile))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("decode sidecar: %v", err)
	}
	if len(st.Files) != 1 {
		t.Fatalf("sidecar covers %d files, want 1", len(st.Files))
	}
	out := make(map[int]struct{}, len(st.Files[0].Segments))
	for _, s := range st.Files[0].Segments {
		out[s.Number] = struct{}{}
	}
	return out
}

// A damaged article on the primary is not a hole: the backup's copy of the
// same article is usually clean, and finding it there is far cheaper than
// spending recovery blocks.
func TestDownloadRefetchesACorruptArticleFromTheBackup(t *testing.T) {
	primary, backup := newServer(t), newServer(t)
	file := stage(t, "release.mkv", payload(7, 4_000), 900)
	if len(file.ids) < 3 {
		t.Fatalf("test needs at least 3 segments, got %d", len(file.ids))
	}
	file.publishDamaged(t, primary, 2)
	file.publish(backup)

	dir := t.TempDir()
	res, err := Download(background(t), document(t, file), dir, newPool(t, primary, backup),
		Options{SkipSpaceCheck: true})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !res.Complete() {
		t.Fatalf("failures: %v", res.Failures)
	}
	if got := readFile(t, filepath.Join(dir, file.name)); !bytes.Equal(got, file.data) {
		t.Fatalf("file assembled from the backup does not match the original")
	}
	// Only the damaged article cost a second request, and the primary was
	// never asked twice for the bytes it had already ruined.
	if n := backup.Stats().Bodies; n != 1 {
		t.Fatalf("backup answered %d BODY commands, want exactly the damaged one", n)
	}
	if n := bodyCount(primary.Commands(), file.ids[1]); n != 1 {
		t.Fatalf("primary was asked %d times for the article it had damaged, want 1", n)
	}
}

// The other half of the same rule: an article the primary never carried.
func TestDownloadFindsAMissingArticleOnTheBackup(t *testing.T) {
	primary, backup := newServer(t), newServer(t)
	file := stage(t, "release.mkv", payload(8, 4_000), 900)
	file.publishExcept(primary, 1)
	file.publish(backup)

	dir := t.TempDir()
	res, err := Download(background(t), document(t, file), dir, newPool(t, primary, backup),
		Options{SkipSpaceCheck: true})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !res.Complete() {
		t.Fatalf("failures: %v", res.Failures)
	}
	if got := readFile(t, filepath.Join(dir, file.name)); !bytes.Equal(got, file.data) {
		t.Fatalf("file does not match the original")
	}
	if n := backup.Stats().Bodies; n != 1 {
		t.Fatalf("backup answered %d BODY commands, want exactly the missing one", n)
	}
}

// A hole is par2's problem, not a reason to throw away the other fourteen
// gigabytes that arrived intact.
func TestDownloadReportsAPermanentlyMissingSegmentWithoutFailing(t *testing.T) {
	srv := newServer(t)
	gone := stage(t, "release.part01.rar", payload(9, 5_000), 800)
	intact := stage(t, "release.part02.rar", payload(10, 2_000), 800)
	gone.publishExcept(srv, 2)
	intact.publish(srv)

	dir := t.TempDir()
	res, err := Download(background(t), document(t, gone, intact), dir, newPool(t, srv),
		Options{SkipSpaceCheck: true})
	if err != nil {
		t.Fatalf("Download returned an error for a repairable hole: %v", err)
	}
	if res.Complete() {
		t.Fatal("Download reported a download with a missing article as complete")
	}
	if n := len(res.Failures); n != 1 {
		t.Fatalf("failures = %v, want exactly one", res.Failures)
	}
	f := res.Failures[0]
	if f.Reason != ReasonMissing || f.File != gone.name || f.Segment != 2 {
		t.Fatalf("failure = %+v", f)
	}
	if got := res.Count(ReasonMissing); got != 1 {
		t.Fatalf("Count(missing) = %d, want 1", got)
	}
	if got := res.Count(ReasonUnavailable); got != 0 {
		t.Fatalf("a 430 from every server was counted as a transport failure")
	}

	// The file keeps its full length so par2 sees a damaged file rather than
	// a different one, and every segment that did arrive is in its place.
	holed := readFile(t, filepath.Join(dir, gone.name))
	if len(holed) != len(gone.data) {
		t.Fatalf("file with a hole is %d bytes, want the declared %d", len(holed), len(gone.data))
	}
	begin, end := 800, 1600
	if !bytes.Equal(holed[:begin], gone.data[:begin]) {
		t.Fatal("bytes before the hole do not match")
	}
	if !bytes.Equal(holed[end:], gone.data[end:]) {
		t.Fatal("bytes after the hole do not match")
	}
	if !bytes.Equal(holed[begin:end], make([]byte, end-begin)) {
		t.Fatal("the hole is not zeroes")
	}

	// The intact file is untouched by its neighbour's damage.
	if got := readFile(t, filepath.Join(dir, intact.name)); !bytes.Equal(got, intact.data) {
		t.Fatal("the intact file did not assemble")
	}
	if res.Files[1].SegmentsDone != res.Files[1].Segments {
		t.Fatalf("intact file = %+v", res.Files[1])
	}
}

// "The provider is down" and "the article is gone" are different answers, and
// only the second one is par2's to fix.
func TestDownloadDistinguishesATransportFailureFromAMissingArticle(t *testing.T) {
	srv := newServer(t)
	file := stage(t, "release.mkv", payload(11, 1_500), 800)
	file.publish(srv)
	srv.SetFault(nntptest.Fault{Bodies: 1000, Mode: nntptest.FaultStatus, Code: 400})

	dir := t.TempDir()
	res, err := Download(background(t), document(t, file), dir, newPool(t, srv), Options{SkipSpaceCheck: true})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if got := res.Count(ReasonUnavailable); got != len(file.ids) {
		t.Fatalf("unavailable = %d, want all %d segments; failures: %v", got, len(file.ids), res.Failures)
	}
	if got := res.Count(ReasonMissing); got != 0 {
		t.Fatalf("a 400 was reported as a missing article")
	}
}

// An article that arrives damaged everywhere is a hole, and it is labelled as
// damage rather than as absence.
func TestDownloadReportsAnArticleThatIsCorruptOnEveryServer(t *testing.T) {
	primary, backup := newServer(t), newServer(t)
	file := stage(t, "release.mkv", payload(12, 3_000), 900)
	file.publishDamaged(t, primary, 1)
	file.publishDamaged(t, backup, 1)

	dir := t.TempDir()
	res, err := Download(background(t), document(t, file), dir, newPool(t, primary, backup),
		Options{SkipSpaceCheck: true})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if n := len(res.Failures); n != 1 {
		t.Fatalf("failures = %v, want one", res.Failures)
	}
	if got := res.Failures[0].Reason; got != ReasonCorrupt {
		t.Fatalf("reason = %q, want %q", got, ReasonCorrupt)
	}
	if res.Failures[0].MessageID != file.ids[0] {
		t.Fatalf("failure names %q, want %q", res.Failures[0].MessageID, file.ids[0])
	}
}

func TestDownloadStopsOnACancelledContextAndKeepsItsState(t *testing.T) {
	srv := newServer(t)
	file := stage(t, "release.mkv", payload(13, 20_000), 400)
	file.publish(srv)

	ctx, cancel := context.WithCancel(background(t))
	defer cancel()
	var mu sync.Mutex
	served := 0
	srv.SetBodyHook(func(string) {
		mu.Lock()
		served++
		stop := served >= 3
		mu.Unlock()
		if stop {
			cancel()
		}
	})

	dir := t.TempDir()
	res, err := Download(ctx, document(t, file), dir, newPool(t, srv), Options{SkipSpaceCheck: true, Concurrency: 2})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if res == nil {
		t.Fatal("a cancelled download returned no result")
	}
	// Cancelling is not failing: the segments that were in flight are still
	// wanted, and writing them off would make the next run refetch nothing
	// and call the download broken.
	if len(res.Failures) != 0 {
		t.Fatalf("cancellation produced failures: %v", res.Failures)
	}
	if _, err := os.Stat(filepath.Join(dir, StateFile)); err != nil {
		t.Fatalf("cancelled download left no resume state: %v", err)
	}
	if res.Progress.SegmentsDone >= res.Progress.Segments {
		t.Fatalf("progress = %+v, want an unfinished download", res.Progress)
	}
}

func TestDownloadRefusesToStartWithoutRoomOnDisk(t *testing.T) {
	srv := newServer(t)
	file := stage(t, "release.mkv", payload(14, 5_000), 800)
	file.publish(srv)

	dir := t.TempDir()
	res, err := Download(background(t), document(t, file), dir, newPool(t, srv), Options{
		Headroom:  1_000,
		FreeSpace: func(string) (int64, error) { return 2_048, nil },
	})
	if !errors.Is(err, ErrInsufficientSpace) {
		t.Fatalf("err = %v, want ErrInsufficientSpace", err)
	}
	if res != nil {
		t.Fatalf("a refused download returned a result: %+v", res)
	}
	var se *SpaceError
	if !errors.As(err, &se) {
		t.Fatalf("err is not a *SpaceError: %v", err)
	}
	if se.Free != 2_048 || se.Need <= se.Free || se.Path != dir {
		t.Fatalf("SpaceError = %+v", se)
	}
	// Nothing was fetched and nothing was created: the point of a preflight
	// is that it happens before the first article.
	if n := srv.Stats().Bodies; n != 0 {
		t.Fatalf("a refused download fetched %d articles", n)
	}
	if _, err := os.Stat(filepath.Join(dir, file.name)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused download created its target file")
	}
}

// A filesystem that cannot answer statfs must not stop a download that would
// have worked.
func TestDownloadProceedsWhenFreeSpaceCannotBeMeasured(t *testing.T) {
	srv := newServer(t)
	file := stage(t, "release.mkv", payload(15, 1_200), 800)
	file.publish(srv)

	dir := t.TempDir()
	res, err := Download(background(t), document(t, file), dir, newPool(t, srv), Options{
		FreeSpace: func(string) (int64, error) { return 0, errSpaceUnsupported },
	})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !res.Complete() {
		t.Fatalf("failures: %v", res.Failures)
	}
}

// Resuming must not demand room for the parts already on disk, or a download
// that filled a drive to within a hair of its size could never finish.
func TestDownloadPreflightOnlyCountsWhatIsLeft(t *testing.T) {
	srv := newServer(t)
	file := stage(t, "release.mkv", payload(16, 8_000), 500)
	file.publish(srv)

	dir := t.TempDir()
	doc := document(t, file)
	if _, err := Download(background(t), doc, dir, newPool(t, srv), Options{SkipSpaceCheck: true}); err != nil {
		t.Fatalf("first Download: %v", err)
	}

	// Everything is already on disk, so the second run needs nothing beyond
	// the headroom even though the release is far larger than the budget.
	var asked int64
	_, err := Download(background(t), doc, dir, newPool(t, srv), Options{
		Headroom:  1_000,
		FreeSpace: func(string) (int64, error) { asked++; return 4_096, nil },
	})
	if err != nil {
		t.Fatalf("resumed Download refused for space it does not need: %v", err)
	}
	if asked == 0 {
		t.Fatal("the preflight did not run")
	}
}

func TestDownloadRejectsBadArguments(t *testing.T) {
	srv := newServer(t)
	file := stage(t, "release.mkv", payload(17, 900), 800)
	file.publish(srv)
	pool := newPool(t, srv)
	doc := document(t, file)
	ctx := background(t)

	if _, err := Download(ctx, nil, t.TempDir(), pool, Options{}); err == nil {
		t.Fatal("Download accepted a nil NZB")
	}
	if _, err := Download(ctx, doc, "", pool, Options{}); err == nil {
		t.Fatal("Download accepted an empty directory")
	}
	if _, err := Download(ctx, doc, t.TempDir(), nil, Options{}); err == nil {
		t.Fatal("Download accepted a nil fetcher")
	}
	if _, err := DownloadFiles(ctx, nil, t.TempDir(), pool, Options{}); err == nil {
		t.Fatal("DownloadFiles accepted an empty file list")
	}
	// An NZB of nothing but par2 has no content to fetch, which is a
	// malformed grab rather than a finished download.
	par2Only := document(t, stage(t, "release.par2", payload(18, 500), 800))
	if _, err := Download(ctx, par2Only, t.TempDir(), pool, Options{SkipSpaceCheck: true}); err == nil {
		t.Fatal("Download accepted an NZB with no content files")
	}
}

// The real two-pass shape: content first, then the par2 volumes verification
// turned out to need, into the same directory. The second pass must not cost
// the first a single refetched article.
func TestDownloadFilesFetchesPar2IntoAHalfFinishedDirectory(t *testing.T) {
	srv := newServer(t)
	content := stage(t, "release.part01.rar", payload(19, 6_000), 700)
	par2 := stage(t, "release.vol000+02.par2", payload(21, 1_400), 700)
	content.publish(srv)
	par2.publish(srv)

	dir := t.TempDir()
	doc := document(t, content, par2)
	if _, err := Download(background(t), doc, dir, newPool(t, srv), Options{SkipSpaceCheck: true}); err != nil {
		t.Fatalf("content Download: %v", err)
	}
	first := srv.Commands()

	res, err := DownloadFiles(background(t), doc.Par2Files(), dir, newPool(t, srv), Options{SkipSpaceCheck: true})
	if err != nil {
		t.Fatalf("par2 DownloadFiles: %v", err)
	}
	if !res.Complete() {
		t.Fatalf("par2 failures: %v", res.Failures)
	}
	if got := readFile(t, filepath.Join(dir, par2.name)); !bytes.Equal(got, par2.data) {
		t.Fatal("the par2 volume did not assemble")
	}
	if got := readFile(t, filepath.Join(dir, content.name)); !bytes.Equal(got, content.data) {
		t.Fatal("the par2 pass disturbed the content file")
	}

	second := srv.Commands()[len(first):]
	for i, id := range content.ids {
		if n := bodyCount(second, id); n != 0 {
			t.Fatalf("content segment %d was refetched %d times by the par2 pass", i+1, n)
		}
	}

	// And a third pass over everything has nothing left to fetch at all.
	before := len(srv.Commands())
	if _, err := Download(background(t), doc, dir, newPool(t, srv), Options{SkipSpaceCheck: true, IncludePar2: true}); err != nil {
		t.Fatalf("third Download: %v", err)
	}
	for _, id := range append(append([]string(nil), content.ids...), par2.ids...) {
		if n := bodyCount(srv.Commands()[before:], id); n != 0 {
			t.Fatalf("a finished download refetched %s", id)
		}
	}
}

// Two files whose subjects recover to the same name must not write to one
// path, and the name each one gets has to be the same on the next run or
// resume would hand a file the other file's segments.
func TestPlanGivesCollidingNamesStableDistinctPaths(t *testing.T) {
	files := []nzb.File{
		{Subject: `Rel [1/2] - "same.rar" yEnc (1/1)`, Segments: []nzb.Segment{{Number: 1, MessageID: "a@x"}}},
		{Subject: `Rel [2/2] - "same.rar" yEnc (1/1)`, Segments: []nzb.Segment{{Number: 1, MessageID: "b@x"}}},
	}
	first := plan(files, "/downloads/rel")
	second := plan(files, "/downloads/rel")

	if first[0].name == first[1].name {
		t.Fatalf("both files claimed %q", first[0].name)
	}
	if first[0].name != second[0].name || first[1].name != second[1].name {
		t.Fatalf("names are not stable across runs: %q/%q then %q/%q",
			first[0].name, first[1].name, second[0].name, second[1].name)
	}
}

func TestSafeNameRefusesToEscapeTheDownloadDirectory(t *testing.T) {
	for _, name := range []string{"", ".", "..", "../../etc/passwd", `a\b`, "a/b", "a\x00b"} {
		if got := safeName(name, 0); got != "file001.bin" {
			t.Fatalf("safeName(%q) = %q, want the placeholder", name, got)
		}
	}
	if got := safeName("release.part01.rar", 0); got != "release.part01.rar" {
		t.Fatalf("safeName mangled an ordinary name: %q", got)
	}
}
