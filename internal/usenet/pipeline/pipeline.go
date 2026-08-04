// Package pipeline downloads an NZB: the scheduler that turns a parsed NZB,
// a set of news servers and a directory into files on disk (SPEC §5.1, PLAN
// phase 7 task 3).
//
// It is the piece between the index (internal/usenet/nzb), the transport
// (internal/usenet/nntp) and the codec (internal/usenet/yenc). Segments are
// fetched by a bounded pool of workers, decoded, and written straight to their
// offset in the target file — parts arrive in whatever order the servers
// answer, and yEnc carries where each one goes precisely so assembly never has
// to buffer a whole file in memory.
//
// # Holes are not failures
//
// A segment that cannot be had is written off and the download carries on.
// Usenet articles rot, and par2 exists to fill exactly these holes (PLAN phase
// 7 task 4); aborting a fifteen-gigabyte download over one dead article would
// throw away the repairable case that Usenet is built around. Every write-off
// is reported in Result.Failures with the reason it happened, so the stage
// above can tell "par2 can fix this" from "the provider is down".
//
// One damaged article is worth one more ask, though, and only of a server that
// has not already answered: a copy that failed its own yEnc CRC on the primary
// is often clean on the backup, and finding it there is far cheaper than
// spending recovery blocks.
//
// # Resume
//
// Completed segments are recorded in a sidecar inside the download's own
// directory (StateFile), not in the database. The database is a disposable
// cache; a half-finished download is not, and refetching it because a cache
// was deleted is a bill the user pays.
//
// # Boundaries
//
// Nothing here imports internal/store or touches the database, matching
// internal/download: it is an NZB, a fetcher and a directory in, files and a
// summary out, which is what makes the whole thing testable against
// internal/usenet/nntptest with no network anywhere.
package pipeline

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/watzon/caravan/internal/usenet/nntp"
	"github.com/watzon/caravan/internal/usenet/nzb"
	"github.com/watzon/caravan/internal/usenet/yenc"
)

// Defaults for Options.
const (
	// DefaultConcurrency is how many segments are in flight at once. The
	// per-server connection caps live in nntp.MultiPool and do the real
	// throttling; this only bounds how much decoded article the pipeline
	// holds in memory at a time.
	DefaultConcurrency = 8
	// DefaultHeadroom is the free space the preflight demands beyond the
	// download's own size. It covers the sidecar, filesystem overhead and a
	// little slack, and nothing else — in particular it does not budget for
	// extraction, which needs roughly a second copy of the payload. That is a
	// separate preflight, run by the engine once it knows what the release was
	// packed in (internal/usenet.Engine.checkExtractionSpace), because until
	// the archives are on disk there is nothing to measure.
	DefaultHeadroom = 64 << 20
)

// Fetcher is the pipeline's view of a set of news servers: one message-id in,
// one article body out, safe to call from every worker at once.
//
// *nntp.MultiPool is the implementation; the interface exists so this package
// can be tested without one and so the engine can wrap it.
type Fetcher interface {
	// FetchBody returns one article body as the server sent it —
	// dot-stuffing removed, CRLF endings intact. An error unwrapping to
	// nntp.ErrArticleNotFound means every server agreed the article is gone;
	// anything else means "unknown".
	FetchBody(ctx context.Context, messageID string) ([]byte, error)
}

// FailoverFetcher is a Fetcher that can be told to skip the servers that have
// already answered, and which reports the one that did.
//
// It is what makes a CRC failure survivable without spending recovery blocks:
// asking the same server for the same damaged article returns the same damaged
// bytes, so the second try has to start below it. A plain Fetcher works
// without this — the segment simply becomes a hole for par2 instead.
type FailoverFetcher interface {
	Fetcher
	// FetchBodyFrom fetches considering only the servers from index from
	// downwards in priority order, and reports which one answered.
	FetchBodyFrom(ctx context.Context, messageID string, from int) (body []byte, server int, err error)
}

// The transport this package was written against satisfies the richer
// contract; if that ever stops being true, the build says so here rather than
// silently degrading every corrupt segment into a par2 repair.
var _ FailoverFetcher = (*nntp.MultiPool)(nil)

// Options configures a download. The zero value is the intended one.
type Options struct {
	// Concurrency is how many segments are fetched at once. Zero uses
	// DefaultConcurrency.
	Concurrency int
	// IncludePar2 downloads the release's par2 volumes along with its
	// content. It is off by default because par2 is a repair budget, not
	// payload (SPEC §5.1): fetching every recovery volume of a release that
	// needed no repair is the easiest way to waste a paid account. The
	// repair stage asks for the volumes it needs by calling DownloadFiles.
	IncludePar2 bool
	// Headroom is the free space demanded beyond the download's own size.
	// Zero uses DefaultHeadroom; negative demands none.
	Headroom int64
	// SkipSpaceCheck turns the disk preflight off entirely.
	SkipSpaceCheck bool
	// FreeSpace measures the bytes available on the filesystem holding a
	// directory. Nil uses FreeSpace, the platform implementation; a test or
	// an engine that already knows the answer supplies its own.
	FreeSpace func(path string) (int64, error)
	// Progress receives live counters. Nil makes one internally, which is
	// still reported in Result.Progress; supply one to poll while the
	// download runs.
	Progress *Tracker
}

func (o Options) normalized() Options {
	if o.Concurrency <= 0 {
		o.Concurrency = DefaultConcurrency
	}
	if o.Headroom == 0 {
		o.Headroom = DefaultHeadroom
	}
	if o.Headroom < 0 {
		o.Headroom = 0
	}
	if o.FreeSpace == nil {
		o.FreeSpace = FreeSpace
	}
	if o.Progress == nil {
		o.Progress = NewTracker()
	}
	return o
}

// Reason is why a segment did not make it to disk.
type Reason string

const (
	// ReasonMissing means every configured server said the article does not
	// exist. This is the clean par2 case: a hole of known size in a known
	// place.
	ReasonMissing Reason = "missing"
	// ReasonCorrupt means an article arrived but could not be trusted — a
	// CRC mismatch, a short payload, or bytes that are not yEnc at all — on
	// every server that was asked. Also a hole for par2.
	ReasonCorrupt Reason = "corrupt"
	// ReasonUnavailable means no server could be reached or none gave a
	// conclusive answer. The article may well still exist; this is a
	// transport problem to surface, and a download full of them is a
	// download to retry rather than to repair.
	ReasonUnavailable Reason = "unavailable"
)

// Failure is one segment that did not make it to disk.
type Failure struct {
	// File is the on-disk name of the file the segment belonged to.
	File string
	// Segment is the segment's 1-based number within that file.
	Segment int
	// MessageID is the article that was wanted. A message-id is not a
	// credential, so it is safe to log.
	MessageID string
	// Reason is what kind of failure it was.
	Reason Reason
	// Err is the underlying error, with the detail the transport or the
	// codec attached.
	Err error
}

func (f Failure) String() string {
	return fmt.Sprintf("%s segment %d (%s): %s: %v", f.File, f.Segment, f.MessageID, f.Reason, f.Err)
}

// FileResult is one file after the download.
type FileResult struct {
	// Name is the file's name inside the download directory.
	Name string
	// Path is its absolute path.
	Path string
	// Subject is the NZB subject it came from, which is the only identity an
	// obfuscated release has.
	Subject string
	// IsPar2 reports whether it is a recovery volume rather than payload.
	IsPar2 bool
	// Size is its length on disk, holes included.
	Size int64
	// Segments is how many segments it was posted in.
	Segments int
	// SegmentsDone is how many of them are on disk.
	SegmentsDone int
	// FileCRC32 is the whole-file CRC32 a yEnc trailer declared, valid only
	// when HasFileCRC is set. Checking it belongs to the verify stage, which
	// is already reading these files; recording it here means that stage
	// does not have to decode an article again to find it.
	FileCRC32 uint32
	// HasFileCRC reports whether a poster declared one.
	HasFileCRC bool
}

// Complete reports whether every segment of this file is on disk.
func (f FileResult) Complete() bool { return f.SegmentsDone == f.Segments }

// Result is what a download achieved.
type Result struct {
	// Dir is the directory the files were assembled in.
	Dir string
	// Files is one entry per NZB file that was downloaded, in NZB order.
	Files []FileResult
	// Failures is every segment that did not make it, in the order they were
	// given up on.
	Failures []Failure
	// Progress is the final snapshot of the counters.
	Progress Progress
}

// Complete reports whether every segment of every file is on disk, which is
// the only case where the verify and repair stage has nothing to do.
func (r *Result) Complete() bool { return len(r.Failures) == 0 }

// Count is how many failures had the given reason. The split is what decides
// the next move: missing and corrupt are par2's problem, unavailable is the
// provider's.
func (r *Result) Count(reason Reason) int {
	n := 0
	for _, f := range r.Failures {
		if f.Reason == reason {
			n++
		}
	}
	return n
}

// Download fetches an NZB's content files into dir.
//
// par2 volumes are skipped unless Options.IncludePar2 says otherwise, because
// they are a repair budget that is only worth paying for once verification
// says so (SPEC §5.1).
func Download(ctx context.Context, doc *nzb.NZB, dir string, fetch Fetcher, opts Options) (*Result, error) {
	if doc == nil {
		return nil, errors.New("pipeline: nil nzb")
	}
	files := doc.ContentFiles()
	if opts.IncludePar2 {
		files = doc.Files
	}
	return DownloadFiles(ctx, files, dir, fetch, opts)
}

// DownloadFiles fetches an explicit set of an NZB's files into dir.
//
// It is the entry point the repair stage uses to pull the par2 volumes it
// turned out to need, into the same directory and the same resume sidecar as
// the content that came before them.
//
// The returned error is reserved for the things that stop a download: a
// cancelled context, a failed disk preflight, or a filesystem that will not
// accept the writes. Articles that could not be had are not errors — they are
// Result.Failures, and par2's job.
func DownloadFiles(ctx context.Context, files []nzb.File, dir string, fetch Fetcher, opts Options) (*Result, error) {
	opts = opts.normalized()
	if fetch == nil {
		return nil, errors.New("pipeline: nil fetcher")
	}
	if dir == "" {
		return nil, errors.New("pipeline: no download directory")
	}
	if len(files) == 0 {
		return nil, errors.New("pipeline: nothing to download")
	}
	dir = filepath.Clean(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("pipeline: create %s: %w", dir, err)
	}

	targets := plan(files, dir)
	st := loadState(dir)
	resumed := st.attach(targets)

	d := &download{
		dir:      dir,
		targets:  targets,
		fetch:    fetch,
		track:    opts.Progress,
		state:    st,
		failover: failoverOf(fetch),
	}

	// Totals cover the whole plan, resumed segments included: progress is
	// "how much of this download exists", not "how much of it happened
	// today".
	var totalSegments int
	var totalBytes, remaining int64
	filePlan := make([]FileProgress, len(files))
	for i, f := range files {
		totalSegments += len(f.Segments)
		totalBytes += targets[i].wire
		filePlan[i] = FileProgress{
			Name:     targets[i].name,
			Segments: targets[i].total,
			IsPar2:   targets[i].isPar2,
		}
		for _, s := range f.Segments {
			if _, done := resumed[i][s.Number]; !done {
				remaining += s.Bytes
			}
		}
	}
	d.track.reset(filePlan, totalSegments, totalBytes)

	// The preflight measures what is left to fetch, so resuming a download
	// onto a nearly full disk is not refused for space its own parts already
	// occupy. Encoded sizes overstate the decoded payload by a few percent,
	// which is the right direction to be wrong in.
	if !opts.SkipSpaceCheck {
		if err := checkSpace(dir, remaining+opts.Headroom, opts.FreeSpace); err != nil {
			return nil, err
		}
	}

	for i, t := range targets {
		if err := t.open(); err != nil {
			d.closeTargets()
			return nil, err
		}
		done := resumed[i]
		t.done = len(done)
		for _, s := range files[i].Segments {
			if seg, ok := done[s.Number]; ok {
				d.track.segmentDone(t.index, s.Bytes, seg.Bytes)
			}
		}
		if t.done >= t.total {
			d.track.fileComplete()
		}
	}

	err := d.run(ctx, files, resumed, opts.Concurrency)
	return d.finish(err)
}

// download is one run's mutable world.
type download struct {
	dir      string
	targets  []*target
	fetch    Fetcher
	failover FailoverFetcher
	track    *Tracker

	stateMu sync.Mutex
	state   *state

	failMu   sync.Mutex
	failures []Failure

	fatalOnce sync.Once
	fatal     error
}

// failoverOf reports the richer contract when the fetcher offers it.
func failoverOf(f Fetcher) FailoverFetcher {
	if ff, ok := f.(FailoverFetcher); ok {
		return ff
	}
	return nil
}

type job struct {
	target  *target
	segment nzb.Segment
}

// run drives the worker pool until every outstanding segment has an outcome,
// the caller gives up, or the filesystem does.
func (d *download) run(parent context.Context, files []nzb.File, resumed []map[int]segmentState, workers int) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	jobs := make(chan job)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := d.segment(ctx, j); err != nil {
					d.setFatal(err)
					cancel()
					return
				}
			}
		}()
	}

	// Feeding is a goroutine so a cancelled context stops the queue without
	// waiting for the workers to drain it.
	go func() {
		defer close(jobs)
		for i, f := range files {
			for _, s := range f.Segments {
				if _, done := resumed[i][s.Number]; done {
					continue
				}
				select {
				case jobs <- job{target: d.targets[i], segment: s}:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	wg.Wait()

	if d.fatal != nil {
		return d.fatal
	}
	return parent.Err()
}

// segment fetches, decodes and writes one segment. The error it returns is
// fatal to the whole download; an article that could not be had is recorded as
// a Failure and is not one.
func (d *download) segment(ctx context.Context, j job) error {
	part, reason, err := d.article(ctx, j.segment.MessageID)
	if err != nil {
		if ctx.Err() != nil {
			// A cancelled download did not fail its segments; it stopped.
			return nil
		}
		d.record(j, reason, err)
		return nil
	}
	if err := j.target.write(part); err != nil {
		return err
	}
	d.done(j, part)
	return nil
}

// article fetches one message-id and decodes it, giving a damaged copy exactly
// one second chance on a server that has not already produced it.
func (d *download) article(ctx context.Context, messageID string) (*yenc.Part, Reason, error) {
	body, server, err := d.body(ctx, messageID, 0)
	if err != nil {
		return nil, classify(err), err
	}
	part, decodeErr := yenc.DecodeBytes(body)
	if decodeErr == nil {
		return part, "", nil
	}
	if d.failover != nil {
		// Not a retry — a different source. The bytes that failed came from
		// server, and asking it again produces the same bytes.
		if retry, _, err := d.failover.FetchBodyFrom(ctx, messageID, server+1); err == nil {
			if part, err := yenc.DecodeBytes(retry); err == nil {
				return part, "", nil
			}
		}
	}
	return nil, ReasonCorrupt, decodeErr
}

// body fetches from the given server downwards, reporting which one answered.
// A plain Fetcher has one tier and no way to skip it.
func (d *download) body(ctx context.Context, messageID string, from int) ([]byte, int, error) {
	if d.failover != nil {
		return d.failover.FetchBodyFrom(ctx, messageID, from)
	}
	body, err := d.fetch.FetchBody(ctx, messageID)
	return body, 0, err
}

// classify turns a transport error into the distinction that decides what
// happens next. Only nntp's own sentinel means "gone everywhere"; everything
// else is unknown, and treating unknown as missing is how a provider outage
// turns into a release that par2 is asked to rebuild from nothing.
func classify(err error) Reason {
	if errors.Is(err, nntp.ErrArticleNotFound) {
		return ReasonMissing
	}
	return ReasonUnavailable
}

// done records a segment that landed, and flushes the sidecar when enough has
// changed to be worth the write.
func (d *download) done(j job, part *yenc.Part) {
	t := j.target
	d.track.segmentDone(t.index, j.segment.Bytes, int64(len(part.Body)))

	t.mu.Lock()
	t.done++
	complete := t.done+t.failed == t.total && t.failed == 0
	t.mu.Unlock()
	if complete {
		d.track.fileComplete()
	}

	d.stateMu.Lock()
	d.state.mark(t, j.segment.Number, part.Begin, int64(len(part.Body)))
	if d.state.due(time.Now()) {
		// A sidecar that will not write is not worth failing a download over
		// while the data itself is still landing; the final save reports it.
		_ = d.state.save(d.dir)
	}
	d.stateMu.Unlock()
}

// record writes off one segment.
func (d *download) record(j job, reason Reason, err error) {
	d.track.segmentFailed(j.target.index)

	j.target.mu.Lock()
	j.target.failed++
	j.target.mu.Unlock()

	d.failMu.Lock()
	d.failures = append(d.failures, Failure{
		File:      j.target.name,
		Segment:   j.segment.Number,
		MessageID: j.segment.MessageID,
		Reason:    reason,
		Err:       err,
	})
	d.failMu.Unlock()
}

func (d *download) setFatal(err error) {
	d.fatalOnce.Do(func() { d.fatal = err })
}

func (d *download) closeTargets() {
	for _, t := range d.targets {
		if t.f != nil {
			t.f.Close()
			t.f = nil
		}
	}
}

// finish sizes and closes every file, writes the sidecar one last time, and
// assembles the summary. runErr wins over anything that goes wrong here: a
// cancelled download reports the cancellation, not the tidying.
func (d *download) finish(runErr error) (*Result, error) {
	var closeErr error
	for _, t := range d.targets {
		if err := t.finish(); err != nil && closeErr == nil {
			closeErr = err
		}
	}

	d.stateMu.Lock()
	saveErr := d.state.save(d.dir)
	d.stateMu.Unlock()

	res := &Result{Dir: d.dir, Files: make([]FileResult, 0, len(d.targets))}
	for _, t := range d.targets {
		info, err := os.Stat(t.path)
		var size int64
		if err == nil {
			size = info.Size()
		}
		res.Files = append(res.Files, FileResult{
			Name:         t.name,
			Path:         t.path,
			Subject:      t.subject,
			IsPar2:       t.isPar2,
			Size:         size,
			Segments:     t.total,
			SegmentsDone: t.done,
			FileCRC32:    t.crc,
			HasFileCRC:   t.hasCRC,
		})
	}
	d.failMu.Lock()
	res.Failures = append([]Failure(nil), d.failures...)
	d.failMu.Unlock()
	res.Progress = d.track.Snapshot()

	switch {
	case runErr != nil:
		return res, runErr
	case closeErr != nil:
		return res, closeErr
	case saveErr != nil:
		return res, saveErr
	}
	return res, nil
}
