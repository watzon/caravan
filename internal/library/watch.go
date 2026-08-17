package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// JobKindImport is the durable job kind the watcher queues for a finished
// download (SPEC §7). Handlers of this kind must be idempotent, which is what
// ImportDownload guarantees.
const JobKindImport = "import"

// DefaultWatchInterval is how often RunWatcher polls the engine when the
// caller does not say. Download progress is a UI number, not a control loop,
// so a few seconds is plenty.
const DefaultWatchInterval = 5 * time.Second

// importJobLease is how long a claimed import job is held before another
// worker may reclaim it (SPEC §7). One import is a hardlink and a handful of
// database writes, so it does not need to be generous — it only has to outlast
// a slow poster fetch.
const importJobLease = 5 * time.Minute

// importPayload is the JSON body of an import job: the engine's handle for the
// finished download, and nothing else. What the download was grabbed for and
// where its data landed are re-read when the job runs, because a job can run
// long after it was queued and a payload that can go stale is a bug waiting
// for a slow queue.
type importPayload struct {
	EngineID string `json:"engine_id"`
}

// RunWatcher polls engine every interval, persists what it sees, and imports
// downloads as they finish (SPEC §5.1, PLAN phase 2 task 5). It runs until ctx
// is done and then returns ctx's error.
//
// It lives in internal/library rather than a package of its own because every
// step it takes is a store call or ImportDownload: it is the import pipeline's
// clock, not a subsystem. It talks to the engine only through core.Engine, so
// the embedded torrent client and the phase-6 external clients drive it
// identically.
//
// Completion becomes a durable job rather than a direct call, so an import
// that is interrupted mid-flight is retried instead of lost. Delivery is
// therefore at-least-once, and "exactly once per completed download" is a
// property of ImportDownload's idempotency plus the in-process queued set
// below — not of the queue.
func (m *Manager) RunWatcher(ctx context.Context, engine core.Engine, interval time.Duration) error {
	if interval <= 0 {
		interval = DefaultWatchInterval
	}
	w := &watcher{mgr: m, engine: engine, queued: map[core.DownloadID]bool{}}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := w.tick(ctx); err != nil && ctx.Err() == nil {
			w.report(ctx, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// watcher is one RunWatcher run's state.
type watcher struct {
	mgr    *Manager
	engine core.Engine
	// queued remembers the downloads this process has already handed to the
	// job queue, so a download that stays "seeding" for a week is enqueued
	// once rather than once per tick. It is deliberately in-process only: after
	// a restart the durable grab status takes over the same job (see
	// queueImport), and a redundant job is harmless.
	queued map[core.DownloadID]bool
	// lastProblem is the last error reported to the activity feed. An engine
	// that is down is down every tick, and the feed is for the user, not for a
	// log file.
	lastProblem string
}

// tick is one polling cycle: refresh the queue's persisted state, queue an
// import for anything that finished, then run whatever imports are due.
func (w *watcher) tick(ctx context.Context) error {
	statuses, err := w.engine.List(ctx)
	if err != nil {
		return fmt.Errorf("library: list downloads: %w", err)
	}

	for _, s := range statuses {
		if err := w.mgr.persistDownload(ctx, s); err != nil {
			return err
		}
		if err := w.queueImport(ctx, s); err != nil {
			return err
		}
	}
	return w.runImportJobs(ctx)
}

// report puts a watcher problem in the activity feed, skipping the repeat of
// one already reported. Failing to record the failure is not worth escalating:
// if the database is the thing that is broken, there is nowhere left to say so.
func (w *watcher) report(ctx context.Context, problem error) {
	msg := problem.Error()
	if msg == w.lastProblem {
		return
	}
	w.lastProblem = msg
	_ = w.mgr.store.InsertEvent(ctx, &core.Event{
		Level:    core.EventLevelError,
		Category: EventCategoryImport,
		Message:  "Download watcher problem",
		Detail:   msg,
	})
}

// persistDownload writes the durable half of a live status (SPEC §7): enough
// for the queue screen to render after a restart, before the engine has
// reported in.
//
// The grab behind the download is written by whoever added it and is preserved
// here — a watcher knows what the engine is doing right now, not who asked for
// it. The engine name is preserved the same way, unless the status names one:
// a router says which of several backends answered, and that is better
// evidence than a row an out-of-band download never got to fill in.
//
// A save path outside the storage root is deliberately not persisted. An
// external client's download directory is its own configuration, an absolute
// path on its own machine, and the `downloads` table is Caravan's — the import
// pipeline re-reads the live status for it (see runImportJob) rather than
// resolving a foreign path out of the database (SPEC §1.2 pillar 3).
func (m *Manager) persistDownload(ctx context.Context, s core.DownloadStatus) error {
	d := core.Download{
		EngineID:  s.ID,
		Engine:    s.Engine,
		Title:     s.Name,
		State:     s.State,
		Progress:  s.Progress,
		BytesDone: s.BytesDone,
		Size:      s.Size,
		Error:     s.Error,
	}
	if !foreignPath(s.SavePath) {
		d.SavePath = s.SavePath
	}

	existing, err := m.store.GetDownloadByEngineID(ctx, s.ID)
	switch {
	case err == nil:
		d.ID = existing.ID
		d.GrabID = existing.GrabID
		if d.Engine == "" {
			d.Engine = existing.Engine
		}
		d.CreatedAt = existing.CreatedAt
	case !errors.Is(err, store.ErrNotFound):
		return err
	}
	return m.store.UpsertDownload(ctx, &d)
}

// queueImport enqueues an import job the first time a download is seen with
// its data complete.
func (w *watcher) queueImport(ctx context.Context, s core.DownloadStatus) error {
	if !importable(s.State) || w.queued[s.ID] {
		return nil
	}

	grab, err := w.mgr.store.GetGrabByDownloadID(ctx, s.ID)
	if errors.Is(err, store.ErrNotFound) {
		// Automatic grabs used to persist the engine row and never write
		// grab_id. The titles still match, so a finished download can be
		// claimed by the grab that fetched it instead of sitting in
		// incomplete forever.
		grab, err = w.attachGrabByTitle(ctx, s)
	}
	if errors.Is(err, store.ErrNotFound) {
		// A download nobody grabbed — added out of band, or its grab row is
		// gone — has no library item to import into. Leaving the data alone is
		// the honest answer; the library scan is how such files get in.
		w.queued[s.ID] = true
		return nil
	}
	if err != nil {
		return err
	}
	if grabImportSettled(grab.Status) {
		// The durable half of "exactly once": after a restart the queued set is
		// empty, and this is what stops an already-handled seeding torrent from
		// being handed to the queue again on every start. Failed is included
		// because parking for Scan Review is a finished decision, not a job
		// that should run again.
		w.queued[s.ID] = true
		return nil
	}

	payload, err := json.Marshal(importPayload{EngineID: string(s.ID)})
	if err != nil {
		return fmt.Errorf("library: encode import job for %q: %w", s.ID, err)
	}
	if err := w.mgr.store.EnqueueJob(ctx, &core.Job{Kind: JobKindImport, Payload: string(payload)}); err != nil {
		return err
	}
	w.queued[s.ID] = true
	return nil
}

// attachGrabByTitle links a grab-less download to the open grab that named
// the same release. persistDownload has already written the row with grab_id
// 0; this writes the missing link so runImportJob can find the grab.
func (w *watcher) attachGrabByTitle(ctx context.Context, s core.DownloadStatus) (*core.Grab, error) {
	grab, err := w.mgr.store.GetUnlinkedGrabbedByReleaseTitle(ctx, strings.TrimSpace(s.Name))
	if err != nil {
		return nil, err
	}
	existing, err := w.mgr.store.GetDownloadByEngineID(ctx, s.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	d := core.Download{
		EngineID:  s.ID,
		Engine:    s.Engine,
		Title:     s.Name,
		State:     s.State,
		Progress:  s.Progress,
		BytesDone: s.BytesDone,
		Size:      s.Size,
		GrabID:    grab.GrabID,
	}
	if existing != nil {
		d.ID = existing.ID
		d.CreatedAt = existing.CreatedAt
		if d.Engine == "" {
			d.Engine = existing.Engine
		}
		if !foreignPath(s.SavePath) {
			d.SavePath = s.SavePath
		} else {
			d.SavePath = existing.SavePath
		}
	} else if !foreignPath(s.SavePath) {
		d.SavePath = s.SavePath
	}
	if err := w.mgr.store.UpsertDownload(ctx, &d); err != nil {
		return nil, err
	}
	return grab, nil
}

// importable reports whether a download's data is complete enough to import.
//
// A finished torrent reports "seeding", not "completed": the end of the
// transfer and the end of the seeding lifecycle are different events, and an
// import must not wait for the second one. Hardlinking is exactly what lets a
// file be in the library and still be seeded (SPEC §5.1).
func importable(state core.DownloadState) bool {
	return state == core.DownloadCompleted || state == core.DownloadSeeding
}

// runImportJobs drains the import queue. Claiming under a lease is what keeps
// two watchers — or a watcher and the one that was restarted out from under a
// crashed import — from importing the same download at the same time.
func (w *watcher) runImportJobs(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		job, err := w.mgr.store.ClaimJob(ctx, []string{JobKindImport}, importJobLease)
		if err != nil {
			return err
		}
		if job == nil {
			return nil
		}

		if runErr := w.runImportJob(ctx, job); runErr != nil {
			if err := w.mgr.store.FailJob(ctx, job.ID, runErr.Error()); err != nil {
				return err
			}
			w.report(ctx, runErr)
			continue
		}
		if err := w.mgr.store.CompleteJob(ctx, job.ID); err != nil {
			return err
		}
	}
}

// runImportJob executes one import job: re-read the download from the engine,
// re-read what it was grabbed for, import it.
func (w *watcher) runImportJob(ctx context.Context, job *core.Job) error {
	var payload importPayload
	if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
		return fmt.Errorf("library: decode import job %d: %w", job.ID, err)
	}
	id := core.DownloadID(payload.EngineID)

	status, err := w.engine.Status(ctx, id)
	if err != nil {
		return fmt.Errorf("library: status of download %q: %w", id, err)
	}
	if status == nil {
		return fmt.Errorf("library: download %q is no longer known to the engine", id)
	}

	grab, err := w.mgr.store.GetGrabByDownloadID(ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		// The grab vanished between queueing and running. There is nothing to
		// import *into*, and retrying will not bring it back, so the job is
		// done and the feed says why.
		_ = w.mgr.store.InsertEvent(ctx, &core.Event{
			Level:    core.EventLevelWarn,
			Category: EventCategoryImport,
			Message:  fmt.Sprintf("Nothing to import for download %s", id),
			Detail:   "the grab this download belonged to is gone",
		})
		return nil
	}
	if err != nil {
		return err
	}
	return w.mgr.ImportDownload(ctx, *status, grab.GrabInfo)
}
