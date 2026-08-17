package library

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// fakeEngine is a core.Engine that reports whatever the test puts in it. The
// watcher's job is what it does with an engine's answers, not how a torrent
// client produces them.
type fakeEngine struct {
	mu        sync.Mutex
	statuses  []core.DownloadStatus
	listErr   error
	statusErr error
	listCalls int
}

func (e *fakeEngine) List(context.Context) ([]core.DownloadStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listCalls++
	if e.listErr != nil {
		return nil, e.listErr
	}
	return append([]core.DownloadStatus(nil), e.statuses...), nil
}

func (e *fakeEngine) Status(_ context.Context, id core.DownloadID) (*core.DownloadStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.statusErr != nil {
		return nil, e.statusErr
	}
	for i := range e.statuses {
		if e.statuses[i].ID == id {
			s := e.statuses[i]
			return &s, nil
		}
	}
	return nil, nil
}

func (e *fakeEngine) calls() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.listCalls
}

func (e *fakeEngine) Add(context.Context, core.Release, core.AddOpts) (core.DownloadID, error) {
	return "", errors.New("fakeEngine: Add is not part of the watcher's contract")
}
func (e *fakeEngine) Pause(context.Context, core.DownloadID) error  { return nil }
func (e *fakeEngine) Resume(context.Context, core.DownloadID) error { return nil }
func (e *fakeEngine) Remove(context.Context, core.DownloadID, bool) error {
	return nil
}
func (e *fakeEngine) Close() error { return nil }

var _ core.Engine = (*fakeEngine)(nil)

// linkDownloadToGrab writes the downloads row that ties an engine handle to a
// grab, which is what the grab endpoint does when it starts a download.
func linkDownloadToGrab(h *harness, id core.DownloadID, grab core.GrabInfo) {
	h.t.Helper()
	d := &core.Download{
		GrabID:   grab.GrabID,
		Engine:   "fake",
		EngineID: id,
		Title:    grab.ReleaseTitle,
		State:    core.DownloadDownloading,
	}
	if err := h.st.UpsertDownload(context.Background(), d); err != nil {
		h.t.Fatalf("UpsertDownload: %v", err)
	}
}

func newWatcher(h *harness, engine core.Engine) *watcher {
	return &watcher{mgr: h.mgr, engine: engine, queued: map[core.DownloadID]bool{}}
}

func (h *harness) jobs() []core.Job {
	h.t.Helper()
	var out []core.Job
	for id := int64(1); ; id++ {
		j, err := h.st.GetJob(context.Background(), id)
		if errors.Is(err, store.ErrNotFound) {
			return out
		}
		if err != nil {
			h.t.Fatalf("GetJob(%d): %v", id, err)
		}
		out = append(out, *j)
	}
}

// TestWatcherImportsACompletedDownloadExactlyOnce is the watcher's whole job:
// notice the download finished, queue the import, run it, and never do it
// twice however often it polls.
func TestWatcherImportsACompletedDownloadExactlyOnce(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	dl := movieDownload(h, "movie bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny.2008"})
	linkDownloadToGrab(h, dl.ID, grab)

	engine := &fakeEngine{statuses: []core.DownloadStatus{dl}}
	w := newWatcher(h, engine)

	ctx := context.Background()
	if err := w.tick(ctx); err != nil {
		t.Fatalf("first tick: %v", err)
	}

	if got := h.read(organizedRel); got != "movie bytes" {
		t.Fatalf("imported content = %q, want the download's bytes", got)
	}
	if !h.sameFile(movieDownloadFile, organizedRel) {
		t.Errorf("the watcher's import did not hardlink")
	}
	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusImported {
		t.Errorf("grab status = %q, want %q", got, core.GrabStatusImported)
	}

	// The live status made it into the durable half of the queue.
	stored, err := h.st.GetDownloadByEngineID(ctx, dl.ID)
	if err != nil {
		t.Fatalf("GetDownloadByEngineID: %v", err)
	}
	if stored.State != core.DownloadSeeding || stored.SavePath != movieDownloadDir {
		t.Errorf("persisted download = %+v, want the engine's live state", stored)
	}
	if stored.GrabID != grab.GrabID || stored.Engine != "fake" {
		t.Errorf("persisted download lost its grab or engine: %+v", stored)
	}

	jobs := h.jobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v, want exactly one import job", jobs)
	}
	if jobs[0].Kind != JobKindImport || jobs[0].State != core.JobStateDone {
		t.Fatalf("job = %+v, want a finished %q job", jobs[0], JobKindImport)
	}

	// A download that keeps seeding must not be re-queued on every poll.
	if err := w.tick(ctx); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if jobs := h.jobs(); len(jobs) != 1 {
		t.Fatalf("jobs after a second tick = %+v, want no new job", jobs)
	}
	collision := "library/Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008) (1).mkv"
	if h.exists(collision) {
		t.Errorf("the second tick imported the download again, at %s", collision)
	}
}

// Automatic grabs used to persist the engine row and never write grab_id.
// The watcher must still import that finished download, because the grab
// named the same release and the file is sitting in incomplete.
func TestWatcherImportsOrphanedDownloadByReleaseTitle(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	dl := movieDownload(h, "movie bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: dl.Name})

	ctx := context.Background()
	if err := h.st.UpsertDownload(ctx, &core.Download{
		Engine: "embedded-usenet", EngineID: dl.ID, Title: dl.Name, State: core.DownloadDownloading,
	}); err != nil {
		t.Fatalf("UpsertDownload orphan: %v", err)
	}

	w := newWatcher(h, &fakeEngine{statuses: []core.DownloadStatus{dl}})
	if err := w.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if got := h.read(organizedRel); got != "movie bytes" {
		t.Fatalf("imported content = %q, want the download's bytes", got)
	}
	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusImported {
		t.Errorf("grab status = %q, want %q", got, core.GrabStatusImported)
	}
	stored, err := h.st.GetDownloadByEngineID(ctx, dl.ID)
	if err != nil {
		t.Fatalf("GetDownloadByEngineID: %v", err)
	}
	if stored.GrabID != grab.GrabID {
		t.Errorf("download grab_id = %d, want %d", stored.GrabID, grab.GrabID)
	}
}

func TestWatcherSkipsUnfinishedDownloads(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	dl := movieDownload(h, "movie bytes")
	dl.State = core.DownloadDownloading
	dl.Progress = 0.4
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID})
	linkDownloadToGrab(h, dl.ID, grab)

	w := newWatcher(h, &fakeEngine{statuses: []core.DownloadStatus{dl}})
	ctx := context.Background()
	if err := w.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if jobs := h.jobs(); len(jobs) != 0 {
		t.Fatalf("jobs = %+v, want none for an unfinished download", jobs)
	}
	if h.exists(organizedRel) {
		t.Errorf("%s was imported before the download finished", organizedRel)
	}
	stored, err := h.st.GetDownloadByEngineID(ctx, dl.ID)
	if err != nil {
		t.Fatalf("GetDownloadByEngineID: %v", err)
	}
	if stored.Progress != 0.4 || stored.State != core.DownloadDownloading {
		t.Errorf("persisted download = %+v, want the in-progress state", stored)
	}
}

// TestWatcherIgnoresDownloadsWithoutAGrab: a download nobody asked for has no
// library item to import into, and inventing one is exactly the guess the
// import pipeline refuses to make.
func TestWatcherIgnoresDownloadsWithoutAGrab(t *testing.T) {
	h := newHarness(t)
	addMovieItem(h)
	dl := movieDownload(h, "movie bytes")

	w := newWatcher(h, &fakeEngine{statuses: []core.DownloadStatus{dl}})
	ctx := context.Background()
	if err := w.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	if jobs := h.jobs(); len(jobs) != 0 {
		t.Fatalf("jobs = %+v, want none for a download with no grab", jobs)
	}
	if _, err := h.st.GetDownloadByEngineID(ctx, dl.ID); err != nil {
		t.Errorf("the download was not persisted: %v", err)
	}
}

// TestWatcherRetriesAFailedImport: an import that blows up goes back on the
// queue with backoff and says so in the activity feed (SPEC §7, §13).
func TestWatcherRetriesAFailedImport(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	dl := movieDownload(h, "movie bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID})
	linkDownloadToGrab(h, dl.ID, grab)

	engine := &fakeEngine{statuses: []core.DownloadStatus{dl}}
	w := newWatcher(h, engine)
	ctx := context.Background()

	// The engine loses track of the download between queueing and running.
	engine.statusErr = errors.New("engine is confused")
	if err := w.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	jobs := h.jobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs = %+v, want the import job", jobs)
	}
	if jobs[0].State != core.JobStatePending || jobs[0].Attempts != 1 {
		t.Fatalf("job = %+v, want one failed attempt back in the pending pool", jobs[0])
	}
	if jobs[0].RunAfter.IsZero() {
		t.Error("retry was not backed off")
	}
	if h.exists(organizedRel) {
		t.Errorf("%s was imported despite the failure", organizedRel)
	}

	events := h.events()
	if len(events) != 1 || events[0].Level != core.EventLevelError {
		t.Fatalf("events = %+v, want the failure reported", events)
	}

	// The retry is the queue's business. A download that is still finished and
	// still not imported must not pile up a second job on the next poll.
	if err := w.tick(ctx); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if jobs := h.jobs(); len(jobs) != 1 {
		t.Fatalf("jobs after a second tick = %+v, want the failed job to be retried, not duplicated", jobs)
	}
}

// TestWatcherReportsAnUnreachableEngineOnce: an engine that is down is down
// every tick, and the activity feed is for the user, not for a log file.
func TestWatcherReportsAnUnreachableEngineOnce(t *testing.T) {
	h := newHarness(t)
	engine := &fakeEngine{listErr: errors.New("connection refused")}
	w := newWatcher(h, engine)

	ctx := context.Background()
	for range 3 {
		err := w.tick(ctx)
		if err == nil {
			t.Fatal("tick succeeded with an unreachable engine")
		}
		w.report(ctx, err)
	}

	events := h.events()
	if len(events) != 1 {
		t.Fatalf("events = %+v, want a single report for a persistent failure", events)
	}
	if events[0].Level != core.EventLevelError || events[0].Category != EventCategoryImport {
		t.Errorf("event = %+v, want an import-category error", events[0])
	}
}

// TestRunWatcherStopsWithItsContext: the watcher is a background loop the
// server owns, so shutting the server down must actually stop it.
func TestRunWatcherStopsWithItsContext(t *testing.T) {
	h := newHarness(t)
	engine := &fakeEngine{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.mgr.RunWatcher(ctx, engine, time.Millisecond) }()

	deadline := time.After(5 * time.Second)
	for engine.calls() < 2 {
		select {
		case <-deadline:
			t.Fatal("the watcher never polled the engine")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("RunWatcher returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("RunWatcher did not return after its context was cancelled")
	}
}

// A download an external client holds goes through the same poll loop as an
// embedded one: the state lands in the queue, the owning backend is recorded,
// and the import runs — but the client's own absolute directory does not reach
// the `downloads` table (PLAN phase 6 task 1, SPEC §1.2 pillar 3).
func TestWatcherPersistsExternalDownloadsWithoutTheirForeignPath(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	dl, _ := externalDownload(h, "external bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP"})

	ctx := context.Background()
	// The grab endpoint records the row; the watcher then only refreshes it.
	if err := h.st.UpsertDownload(ctx, &core.Download{
		GrabID: grab.GrabID, Engine: "sabnzbd", EngineID: dl.ID,
		Title: dl.Name, State: core.DownloadDownloading,
	}); err != nil {
		t.Fatalf("UpsertDownload: %v", err)
	}

	w := newWatcher(h, &fakeEngine{statuses: []core.DownloadStatus{dl}})
	if err := w.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	stored, err := h.st.GetDownloadByEngineID(ctx, dl.ID)
	if err != nil {
		t.Fatalf("GetDownloadByEngineID: %v", err)
	}
	if stored.State != core.DownloadCompleted {
		t.Errorf("persisted state = %q, want %q", stored.State, core.DownloadCompleted)
	}
	if stored.Engine != "sabnzbd" {
		t.Errorf("persisted engine = %q, want the client that holds it", stored.Engine)
	}
	if stored.SavePath != "" {
		t.Errorf("persisted save path = %q, want the client's foreign path kept out of the database", stored.SavePath)
	}
	if got := h.read(organizedRel); got != "external bytes" {
		t.Fatalf("imported content = %q, want the download's bytes", got)
	}

	// Idempotency across poll cycles and across a restart: the in-process
	// queued set is what stops the first, the durable grab status the second.
	if err := w.tick(ctx); err != nil {
		t.Fatalf("second tick: %v", err)
	}
	restarted := newWatcher(h, &fakeEngine{statuses: []core.DownloadStatus{dl}})
	if err := restarted.tick(ctx); err != nil {
		t.Fatalf("tick after restart: %v", err)
	}
	if jobs := h.jobs(); len(jobs) != 1 {
		t.Fatalf("jobs = %+v, want exactly one import across every poll and a restart", jobs)
	}
	collision := "library/Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008) (1).mkv"
	if h.exists(collision) {
		t.Errorf("the download was imported twice, at %s", collision)
	}
}

// Parking for a manual match is a finished import decision. A process restart
// empties the in-memory queued set; the durable grab status must still stop
// the watcher from putting the incomplete file back on Scan Review.
func TestWatcherDoesNotReparkAMatchedGrabAfterRestart(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)

	const (
		dir  = "incomplete/Some.Other.Movie.2019.1080p"
		file = dir + "/Some.Other.Movie.2019.1080p.mkv"
	)
	h.parser["Some.Other.Movie.2019.1080p.mkv"] = movieParse("Some Other Movie", 2019)
	h.writeVideo(file, "not the movie we asked for")
	dl := core.DownloadStatus{ID: "infohash-other", State: core.DownloadCompleted, SavePath: dir}
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny.2008"})
	linkDownloadToGrab(h, dl.ID, grab)

	ctx := context.Background()
	first := newWatcher(h, &fakeEngine{statuses: []core.DownloadStatus{dl}})
	if err := first.tick(ctx); err != nil {
		t.Fatalf("first tick: %v", err)
	}
	parked := h.unmatched()
	if len(parked) != 1 {
		t.Fatalf("unmatched after first tick = %+v, want the parked file", parked)
	}
	if _, err := h.mgr.ImportUnmatched(ctx, parked[0].ID, core.TMDBRef(10378), MediaTypeMovie); err != nil {
		t.Fatalf("ImportUnmatched: %v", err)
	}

	restarted := newWatcher(h, &fakeEngine{statuses: []core.DownloadStatus{dl}})
	if err := restarted.tick(ctx); err != nil {
		t.Fatalf("tick after restart: %v", err)
	}
	if got := h.unmatched(); len(got) != 0 {
		t.Fatalf("unmatched after restart = %+v, want empty", got)
	}
}

// A router names the backend that answered, and that name heals a row the
// grab endpoint never got to fill in.
func TestWatcherRecordsTheEngineTheStatusNames(t *testing.T) {
	h := newHarness(t)
	dl, _ := externalDownload(h, "external bytes")

	w := newWatcher(h, &fakeEngine{statuses: []core.DownloadStatus{dl}})
	ctx := context.Background()
	if err := w.tick(ctx); err != nil {
		t.Fatalf("tick: %v", err)
	}

	stored, err := h.st.GetDownloadByEngineID(ctx, dl.ID)
	if err != nil {
		t.Fatalf("GetDownloadByEngineID: %v", err)
	}
	if stored.Engine != "sabnzbd" {
		t.Fatalf("persisted engine = %q, want %q", stored.Engine, "sabnzbd")
	}
}
