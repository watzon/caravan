package usenet

import (
	"context"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/usenet/extract"
	"github.com/watzon/caravan/internal/usenet/nntp"
	"github.com/watzon/caravan/internal/usenet/nzb"
	"github.com/watzon/caravan/internal/usenet/par2"
	"github.com/watzon/caravan/internal/usenet/pipeline"
	"github.com/watzon/caravan/internal/usenet/yenc"
)

// start launches the worker for one download. It must be called with e.mu
// held.
//
// It is a no-op when the download should not be running — paused, finished, or
// on a closing engine — and also when a worker is still unwinding from the
// last cancellation. That last case is not a refusal: run re-checks the same
// condition as it exits and relaunches, which is what makes Resume immediately
// after Pause work rather than silently do nothing.
func (e *Engine) start(it *item) {
	if !e.shouldRunLocked(it) || it.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(e.ctx)
	stopped := make(chan struct{})
	it.cancel, it.stopped = cancel, stopped
	it.phase = core.PhaseDownloading
	it.track = pipeline.NewTracker()
	it.sampledAt, it.lastBytes = time.Time{}, 0

	e.workers.Add(1)
	go e.run(ctx, it, cancel, stopped)
}

// run drives one download through every stage and records how it ended.
//
// A cancelled context is not a failure. It means one of two things — the user
// paused, or the engine is shutting down — and both must leave the download
// resumable: the pipeline has already flushed its sidecar, so the articles on
// disk stay on disk and the next start continues from them.
func (e *Engine) run(ctx context.Context, it *item, cancel context.CancelFunc, stopped chan struct{}) {
	defer e.workers.Done()
	defer close(stopped)
	defer cancel()

	err := e.stages(ctx, it)

	e.mu.Lock()
	it.cancel, it.stopped = nil, nil
	it.downRate = 0
	// Whatever the tracker last saw is the download's byte count from here on:
	// the stage that owned it is over, and Status must not read a tracker
	// nobody is updating.
	if it.track != nil {
		p := it.track.Snapshot()
		it.bytesDone, it.size = p.Bytes, p.TotalBytes
		it.track = nil
	}
	switch {
	case err == nil:
		it.finished = true
		it.phase = ""
		it.bytesDone = it.size
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		// Paused or shutting down. Neither is a failure and neither is
		// terminal; the phase is cleared because nothing is happening now.
		it.phase = ""
	default:
		it.failure = err.Error()
		it.phase = ""
		e.logger.Error("usenet download failed",
			"download", it.rec.EngineID, "title", it.rec.Title, "err", err)
	}
	// Pause and Resume can both land while this worker was on its way out, in
	// which case start() saw a live cancel and deferred to here. Re-reading the
	// item now — under the lock, with the worker slot free — is what closes
	// that window: a download the user has asked for goes back to running
	// instead of stalling until something else pokes it.
	e.start(it)
	snapshot := e.refreshLocked(it)
	e.mu.Unlock()

	// Its own context: ctx is cancelled by now on the pause and shutdown
	// paths, and the final state is exactly the one worth keeping.
	if err := e.save(context.WithoutCancel(ctx), snapshot); err != nil {
		e.logger.Warn("persisting download", "download", snapshot.EngineID, "err", err)
	}
}

// shouldRunLocked reports whether this download wants a worker right now. It
// must be called with e.mu held.
//
// The registration check matters: Remove drops the item and then waits for the
// worker to stop, and without it that worker's exit could relaunch itself onto
// a download that no longer exists — and into a directory Remove is about to
// delete.
func (e *Engine) shouldRunLocked(it *item) bool {
	if e.closed || it.paused || it.finished || it.failure != "" {
		return false
	}
	return e.items[it.rec.EngineID] == it
}

// stages is the download's state machine: fetch the articles, repair the holes
// par2 can fill, unpack whatever the release was packed in.
//
// Every error it returns is written verbatim onto the queue item, so each one
// is phrased for the person reading it rather than for a log.
func (e *Engine) stages(ctx context.Context, it *item) error {
	// A download that already made it through every stage is done, whatever
	// the database says. The window this closes is the one between extraction
	// finishing and the completed row being written: without the marker, a
	// crash in there re-fetches every article of the release from the provider
	// and then fails on the files extraction already put in place.
	if e.assembled(it) {
		// Drop the tracker this stage never fed: run() reads whatever is left
		// in it as the download's final byte count, and a fresh one would
		// rewrite the totals restored with the record as zeros.
		e.mu.Lock()
		it.track = nil
		e.mu.Unlock()
		return nil
	}

	res, err := pipeline.Download(ctx, it.doc, it.dir, e.fetch, e.pipelineOpts(it.track))
	if err != nil {
		return e.downloadError(err)
	}

	// Freeze the counters before anything else runs: repair and extraction
	// have no byte total of their own, and progress that rewinds to zero
	// halfway through reads as a download that restarted.
	e.mu.Lock()
	it.bytesDone, it.size = res.Progress.Bytes, res.Progress.TotalBytes
	it.track = nil
	e.mu.Unlock()

	// A provider that could not be reached is a different answer from an
	// article that is gone: par2 cannot fill a hole that only exists because
	// the server was down, and spending recovery blocks on one would waste the
	// budget the release actually needs.
	if n := res.Count(pipeline.ReasonUnavailable); n > 0 {
		return fmt.Errorf("%d of %d articles could not be fetched from any news server — check the servers under Settings → Usenet servers, then resume to retry",
			n, res.Progress.Segments)
	}

	if err := e.verify(ctx, it, res); err != nil {
		return err
	}
	return e.unpack(ctx, it)
}

// verify decides whether the assembled files can be trusted, and spends the
// release's recovery blocks when they cannot.
//
// "Every article arrived" is not the same as "every file is right". A poster
// who omits pcrc32 on some parts, or who posted from an already-corrupt
// source, produces a download with no failures at all — and for a release
// posted as plain files there is no archive CRC downstream to catch it either,
// so the whole-file crc32 the yEnc trailer carries is the last check there is.
// The pipeline recorded it precisely so this stage could read it without
// decoding an article again.
func (e *Engine) verify(ctx context.Context, it *item, res *pipeline.Result) error {
	if holes := res.Count(pipeline.ReasonMissing) + res.Count(pipeline.ReasonCorrupt); holes > 0 {
		damage := fmt.Sprintf("%d article(s) are missing or damaged", holes)
		return e.repair(ctx, it, damage, damagedFiles(res))
	}

	bad, err := checksumFailures(ctx, res)
	if err != nil {
		return err
	}
	if len(bad) == 0 {
		return nil
	}
	damage := fmt.Sprintf("%s arrived whole but does not match the checksum its poster declared",
		strings.Join(bad, ", "))
	return e.repair(ctx, it, damage, bad)
}

// checksumFailures re-reads every fully assembled file and names the ones that
// do not match the whole-file CRC32 their poster declared.
func checksumFailures(ctx context.Context, res *pipeline.Result) ([]string, error) {
	var bad []string
	for _, f := range res.Files {
		if !f.HasFileCRC || !f.Complete() {
			continue
		}
		sum, err := crcFile(ctx, f.Path)
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}
			return nil, fmt.Errorf("checking %s against the checksum the poster declared: %w", f.Name, err)
		}
		if yenc.CheckFileCRC(f.Name, f.FileCRC32, sum) != nil {
			bad = append(bad, f.Name)
		}
	}
	return bad, nil
}

// crcFile is the CRC32 of a whole file on disk.
func crcFile(ctx context.Context, file string) (uint32, error) {
	f, err := os.Open(file)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	h := crc32.NewIEEE()
	buf := make([]byte, 1<<20)
	for {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		n, err := f.Read(buf)
		h.Write(buf[:n])
		if errors.Is(err, io.EOF) {
			return h.Sum32(), nil
		}
		if err != nil {
			return 0, err
		}
	}
}

// damagedFiles names every file the pipeline wrote off a segment of, once
// each, in the order they were given up on.
func damagedFiles(res *pipeline.Result) []string {
	seen := make(map[string]struct{}, len(res.Failures))
	var out []string
	for _, f := range res.Failures {
		if _, dup := seen[f.File]; dup {
			continue
		}
		seen[f.File] = struct{}{}
		out = append(out, f.File)
	}
	return out
}

// downloadError turns a stopped download into words the queue can show.
func (e *Engine) downloadError(err error) error {
	var space *pipeline.SpaceError
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return err
	case errors.As(err, &space):
		return fmt.Errorf("not enough free disk space: %d bytes needed, %d free on %s",
			space.Need, space.Free, space.Path)
	case errors.Is(err, nntp.ErrNoServers):
		return errors.New("no news server is configured — add one under Settings → Usenet servers")
	default:
		return err
	}
}

// repair runs par2 over a download that cannot be trusted as it stands.
//
// damage is the phrase every message here is built from, so the queue says the
// same thing about a hole and about a file that failed its own checksum;
// files are the on-disk names the damage was found in, which is what decides
// whether par2 could have done anything about it at all.
func (e *Engine) repair(ctx context.Context, it *item, damage string, files []string) error {
	if len(it.doc.Par2Files()) == 0 {
		return fmt.Errorf("%s and the release posted no par2 recovery files, so there is nothing to repair it with", damage)
	}
	rep, err := e.runPar2(ctx, it)
	if err == nil {
		// A clean par2 pass only vouches for the files the set describes. A
		// poster who par2'd only the rars, or an NZB carrying two sets (of
		// which par2.OpenFiles keeps one), leaves the rest uncovered — and
		// "verification found nothing wrong" over a file it never looked at is
		// exactly how a hole reaches the library unreported.
		if uncovered := notCovered(rep, files); len(uncovered) > 0 {
			return fmt.Errorf("%s in %s, which the release's par2 set does not cover, so there is nothing to repair it with",
				damage, strings.Join(uncovered, ", "))
		}
		return nil
	}

	var short *par2.InsufficientError
	if errors.As(err, &short) {
		blocks := "blocks"
		if short.Deficit() == 1 {
			blocks = "block"
		}
		return fmt.Errorf("unrepairable: %s, which costs %d recovery %s, and the release carries only %d — %d short",
			damage, short.Needed, blocks, short.Available, short.Deficit())
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	return fmt.Errorf("par2 repair failed: %w", err)
}

// notCovered names the damaged files the par2 set says nothing about.
//
// A set records names with '/' separators and may carry directory components,
// while the pipeline writes flat into the download directory, so the base name
// counts as a match too.
func notCovered(rep *par2.Report, files []string) []string {
	if rep == nil {
		return nil
	}
	covered := make(map[string]struct{}, len(rep.Files)*2)
	for _, f := range rep.Files {
		covered[f.Name] = struct{}{}
		covered[path.Base(f.Name)] = struct{}{}
	}
	var out []string
	for _, name := range files {
		if _, ok := covered[name]; !ok {
			out = append(out, name)
		}
	}
	return out
}

// runPar2 fetches the release's recovery volumes and repairs the directory
// with them.
//
// The volumes are downloaded here rather than with the content because par2 is
// a repair budget, not payload (SPEC §5.1): a release that arrived intact never
// pays for them. It reports the pre-repair damage alongside the outcome, so the
// queue can say how bad it was whether or not the repair worked.
func (e *Engine) runPar2(ctx context.Context, it *item) (*par2.Report, error) {
	e.setPhase(it, core.PhaseRepairing)

	// A fresh tracker: this pass has its own totals, and feeding them to the
	// item's would rewrite the content download's progress with the recovery
	// volumes' much smaller numbers.
	vols, err := pipeline.DownloadFiles(ctx, it.doc.Par2Files(), it.dir, e.fetch, e.pipelineOpts(nil))
	if err != nil {
		return nil, fmt.Errorf("fetching the par2 recovery volumes: %w", e.downloadError(err))
	}

	var paths []string
	for _, f := range vols.Files {
		// A half-fetched volume is worse than an absent one: par2.OpenFiles
		// checks each recovery slice's own MD5, but handing it a file with a
		// hole only wastes the read.
		if f.Complete() {
			paths = append(paths, f.Path)
		}
	}
	if len(paths) == 0 {
		return nil, errors.New("none of the release's par2 recovery volumes could be downloaded")
	}

	set, err := par2.OpenFiles(paths...)
	if err != nil {
		return nil, fmt.Errorf("reading the par2 set: %w", err)
	}
	it.repaired = true
	rep, err := set.VerifyAndRepair(ctx, it.dir)
	return rep, err
}

// unpack extracts the release's archives, if it has any.
//
// A release posted as plain files is the common obfuscated case and is not an
// error: there is simply nothing to unpack, and the files are already where the
// import expects them.
func (e *Engine) unpack(ctx context.Context, it *item) error {
	sets, err := extract.Detect(it.dir)
	if err != nil {
		return fmt.Errorf("reading the download directory: %w", err)
	}
	if len(sets) == 0 {
		return e.tidy(it)
	}
	if err := e.checkExtractionSpace(it, sets); err != nil {
		return err
	}

	for {
		e.setPhase(it, core.PhaseExtracting)
		res, err := extract.Extract(ctx, it.dir)
		if err == nil {
			return e.tidy(it)
		}
		if res != nil {
			// The files are extracted and in place; only removing the debris
			// failed. That is worth a log and not worth failing a download
			// whose media is sitting right there.
			e.logger.Warn("cleaning up after extraction",
				"download", it.rec.EngineID, "err", err)
			return e.tidy(it)
		}
		if !e.canRepairExtraction(ctx, it, err) {
			return e.extractError(err)
		}
		// The archives are damaged in a way yEnc's per-article CRC did not
		// catch — a poster's own bad bytes, or a truncated final part. That is
		// exactly what the recovery volumes are for, so spend them now and try
		// once more. Extract left the directory untouched, so the retry starts
		// from the same place this attempt did.
		if _, rerr := e.runPar2(ctx, it); rerr != nil {
			return e.extractError(err)
		}
	}
}

// checkExtractionSpace refuses an unpack that the filesystem cannot hold.
//
// The download preflight budgets the articles and nothing else, but extraction
// needs roughly a second copy of the payload: extract.Extract writes every
// entry into a staging directory beside the archives and only deletes the
// volumes once the whole set is in place. Discovering that with ENOSPC after
// twenty gigabytes have come over a metered account wastes the entire
// transfer, and Resume repeats it.
//
// The archives' own size is the budget, which is the only figure available
// without opening them and is exact for the stored (uncompressed) archives
// Usenet posters overwhelmingly use. A genuinely compressed set extracts to
// more than this asks for, so the check catches the obvious refusal rather
// than every one — the same direction the download preflight errs in.
func (e *Engine) checkExtractionSpace(it *item, sets []extract.Set) error {
	if e.opts.SkipSpaceCheck {
		return nil
	}
	measure := e.opts.FreeSpace
	if measure == nil {
		measure = pipeline.FreeSpace
	}

	var need int64
	for _, s := range sets {
		for _, v := range s.Volumes {
			info, err := os.Stat(filepath.Join(it.dir, v))
			if err != nil {
				// Gone or unreadable: extraction is about to say so properly,
				// and guessing a size here would only obscure that.
				continue
			}
			need += info.Size()
		}
	}
	need += pipeline.DefaultHeadroom
	if need <= 0 {
		return nil
	}

	// A filesystem that will not answer statfs is not a filesystem that cannot
	// hold the extraction, exactly as the download preflight treats it.
	free, err := measure(it.dir)
	if err != nil || free >= need {
		return nil
	}
	return e.downloadError(&pipeline.SpaceError{Path: it.dir, Need: need, Free: free})
}

// canRepairExtraction reports whether a failed extraction is worth spending
// recovery blocks on.
//
// Only once, only when the release actually carries par2, and never for an
// encrypted archive: there is no password to try, so repair would rebuild
// bytes nobody can read (the extract package calls this terminal, and it is).
func (e *Engine) canRepairExtraction(ctx context.Context, it *item, err error) bool {
	return ctx.Err() == nil &&
		!it.repaired &&
		len(it.doc.Par2Files()) > 0 &&
		!errors.Is(err, extract.ErrEncrypted)
}

// extractError phrases an extraction failure for the queue.
func (e *Engine) extractError(err error) error {
	if errors.Is(err, extract.ErrEncrypted) {
		return fmt.Errorf("the release's archive is password protected, so it cannot be unpacked: %w", err)
	}
	return fmt.Errorf("unpacking the release failed: %w", err)
}

// tidy marks a download as having been through every stage and removes the
// resume sidecar, which has done its job: leaving it behind puts a stray JSON
// file in the directory the import is about to read.
//
// The marker goes down first and outside the download's own directory, beside
// the NZB. Order matters — between removing the sidecar and persisting the
// completed row there is a window where a crash would otherwise leave a
// download that looks half-finished with nothing on disk to say the archives
// were already unpacked and deleted.
func (e *Engine) tidy(it *item) error {
	if err := os.WriteFile(e.donePath(it.rec.EngineID), nil, 0o644); err != nil {
		// Not fatal: the cost is a re-run of the stages after a crash, which
		// is what happened before the marker existed.
		e.logger.Warn("marking the download assembled", "download", it.rec.EngineID, "err", err)
	}
	if err := os.Remove(filepath.Join(it.dir, pipeline.StateFile)); err != nil && !os.IsNotExist(err) {
		e.logger.Warn("removing the resume sidecar", "download", it.rec.EngineID, "err", err)
	}
	return nil
}

// assembled reports whether tidy has already run for this download.
//
// The download's directory has to still be there. The marker says "these
// stages are done", not "this data exists", and honouring it over an empty
// directory would report a download complete with nothing in it.
func (e *Engine) assembled(it *item) bool {
	if _, err := os.Stat(e.donePath(it.rec.EngineID)); err != nil {
		return false
	}
	info, err := os.Stat(it.dir)
	return err == nil && info.IsDir()
}

// donePath is where the assembled marker for one download lives. It sits with
// the NZB rather than in the download's directory so the import never sees it.
func (e *Engine) donePath(id core.DownloadID) string {
	return filepath.Join(e.incomplete, metaDir, string(id)+".done")
}

// setPhase records which stage is running, for the queue's phase badge.
func (e *Engine) setPhase(it *item, phase core.DownloadPhase) {
	e.mu.Lock()
	it.phase = phase
	e.mu.Unlock()
}

// pipelineOpts is the engine's configuration in the pipeline's terms. A nil
// tracker makes the pipeline keep its own, which is what the par2 pass wants.
func (e *Engine) pipelineOpts(track *pipeline.Tracker) pipeline.Options {
	return pipeline.Options{
		Concurrency:    e.opts.Concurrency,
		Progress:       track,
		FreeSpace:      e.opts.FreeSpace,
		SkipSpaceCheck: e.opts.SkipSpaceCheck,
	}
}

// ---------------------------------------------------------------------------
// The NZB sidecar and the download directory
// ---------------------------------------------------------------------------

// nzbPath is where one download's plan lives. The id is a hex handle, so it is
// always a safe filename.
func (e *Engine) nzbPath(id core.DownloadID) string {
	return filepath.Join(e.incomplete, metaDir, string(id)+".nzb")
}

// writeNZB saves the document beside the data, through a temporary file: a
// half-written sidecar read after a crash would be worse than a missing one,
// which merely costs a re-grab.
func (e *Engine) writeNZB(id core.DownloadID, raw []byte) error {
	final := e.nzbPath(id)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return fmt.Errorf("usenet: create %s: %w", filepath.Dir(final), err)
	}
	f, err := os.CreateTemp(filepath.Dir(final), "."+string(id)+".*")
	if err != nil {
		return fmt.Errorf("usenet: write nzb for %q: %w", id, err)
	}
	defer os.Remove(f.Name())

	if _, err := f.Write(raw); err != nil {
		f.Close()
		return fmt.Errorf("usenet: write nzb for %q: %w", id, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("usenet: write nzb for %q: %w", id, err)
	}
	if err := os.Rename(f.Name(), final); err != nil {
		return fmt.Errorf("usenet: write nzb for %q: %w", id, err)
	}
	return nil
}

// readNZB parses the sidecar written when the download was added.
func (e *Engine) readNZB(id core.DownloadID) (*nzb.NZB, error) {
	f, err := os.Open(e.nzbPath(id))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return nzb.Parse(f)
}

// dirFor resolves a persisted record's own directory.
//
// Only the final path element of the stored SavePath is used. The column is
// Caravan's, but it is also the one piece of a download's identity that a
// hand-edited database could point anywhere, and every Usenet download lives
// directly under the incomplete directory by construction.
func (e *Engine) dirFor(rec core.Download) string {
	name := path.Base(path.Clean(strings.TrimSpace(rec.SavePath)))
	if name == "" || name == "." || name == "/" || name == ".." {
		name = string(rec.EngineID)
	}
	return filepath.Join(e.incomplete, name)
}

// dirNameLocked picks the directory a new download assembles into. It must be
// called with e.mu held.
//
// The release title is used because a user looking in `incomplete/` should be
// able to tell what is in there. Two live downloads may genuinely share a
// title — the same release from two indexers, a re-grab of something already
// running — and one directory for both would mix their files and their resume
// state, so a colliding name gains the handle's first bytes. The result is
// persisted in SavePath and read back from there, so it never changes under a
// download once chosen.
func (e *Engine) dirNameLocked(title string, id core.DownloadID) string {
	base := safeDirName(title)
	if base == "" {
		base = string(id)
	}
	taken := false
	for _, it := range e.items {
		if path.Base(it.rec.SavePath) == base {
			taken = true
			break
		}
	}
	if !taken {
		return base
	}
	return base + "-" + string(id)[len(handlePrefix):len(handlePrefix)+8]
}

// safeDirName reduces a release title to one path element.
//
// A title is a stranger's text off an indexer. Separators, traversal and
// control characters are removed rather than escaped, and the result is capped:
// the point is a name that is obviously derived from the release, not one that
// round-trips.
func safeDirName(title string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case r < 0x20 || r == 0x7f:
			return -1
		case r == '/' || r == '\\' || r == ':' || r == '*' || r == '?' ||
			r == '"' || r == '<' || r == '>' || r == '|':
			return '_'
		}
		return r
	}, title)
	cleaned = strings.Trim(cleaned, " .")
	if len(cleaned) > 120 {
		cleaned = strings.TrimRight(cleaned[:120], " .")
	}
	return cleaned
}
