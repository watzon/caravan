// Package relocate moves Caravan's storage root (SPEC §10).
//
// Two operations, and the difference between them is the whole design.
//
// Re-pointing changes where Caravan looks. Every path in the database is
// relative to the root, so it is one settings update: instant, reversible, and
// with no file I/O on media at all. It does not live here — it is a handler in
// internal/api, because there is nothing durable about it.
//
// Migrating moves the bytes, and that is what this package is. It runs as a
// durable job on a worker of its own, because copying a library is hours of
// work that must not starve the RSS sync behind it. Three rules shape it:
//
//   - The root setting moves last. Until every file has arrived and been
//     measured, the database still points at the root that has the files.
//     A failure at any point before that costs time and nothing else.
//
//   - Nothing is deleted until its replacement is verified. A file is copied
//     under a temporary name, its size is checked, it is renamed into place, and
//     only then is the original released — so at no observable moment does a
//     file exist at neither root.
//
//   - Failure puts it back. The target's trees are empty before the move
//     starts, so everything under them afterwards belongs to this migration and
//     rollback is "move the target's trees back". That is derivable from the
//     filesystem alone, which is what makes it survive the crash that took the
//     in-memory bookkeeping with it.
package relocate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// JobKind is the durable job kind the migration runs on. It is registered with
// automation.WithDedicatedWorker: a move that runs for hours on the shared
// worker would hold up every search, sync and handoff behind it.
const JobKind = "storage_migrate"

// EventCategory tags the activity-feed entries a storage operation writes.
const EventCategory = "storage"

// Payload is the job payload. The row holds everything else, so a redelivered
// job cannot disagree with the queue about what it is moving.
type Payload struct {
	MigrationID int64 `json:"migration_id"`
}

// progressInterval throttles how often progress reaches the database. Per-file
// writes would put one transaction per file into a job that copies tens of
// thousands of them; half a second is finer than a progress bar can show.
const progressInterval = 500 * time.Millisecond

// EngineFunc returns the download engine in force right now, or nil when there
// is none. It matches the provider's own Engine method, so the wiring hands
// this package the same lazily-built engine everything else uses.
type EngineFunc func() core.Engine

// Service owns the migration job handler.
type Service struct {
	st     *store.Store
	engine EngineFunc
	log    *slog.Logger
	// step is the mover's test seam; see mover.step.
	step func(rel string)
}

// New builds the service. engine may be nil, which is what a deployment with no
// download queue looks like: there is then nothing to pause, and the move runs
// exactly as it otherwise would.
func New(st *store.Store, engine EngineFunc, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: st, engine: engine, log: log}
}

// Handle runs one migration. It matches automation.Handler; the store argument
// is ignored because the service holds its own handle.
//
// Idempotency: the migration row is the state, not the job. A redelivered job
// for a finished migration is a no-op, and a redelivered job for one that was
// mid-flight re-derives where every file is by looking at both roots — which is
// also how it resumes after a crash.
//
// It never returns an error. A failed move has already put the files back and
// said so on the row; letting the queue retry it on a backoff would start
// moving a library again, unattended, minutes after it went wrong. Restarting
// one is an explicit act.
func (s *Service) Handle(ctx context.Context, _ *store.Store, payload json.RawMessage) error {
	var p Payload
	if err := json.Unmarshal(payload, &p); err != nil {
		s.log.Error("storage migration: undecodable job payload", "error", err)
		return nil
	}

	m, err := s.st.GetStorageMigration(ctx, p.MigrationID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		s.log.Error("storage migration: read the migration", "id", p.MigrationID, "error", err)
		return nil
	}
	if !core.StorageMigrationOpen(m.Status) {
		return nil
	}

	m.Status = core.StorageMigrationRunning
	m.Error = ""
	if err := s.st.UpdateStorageMigration(ctx, m); err != nil {
		s.log.Error("storage migration: claim the migration", "id", m.ID, "error", err)
		return nil
	}

	// Paused for the duration: a download writing into the incomplete folder
	// while it is being moved is a file the mover measures, copies and deletes
	// between two writes.
	paused := s.pauseQueue(ctx)

	if err := s.run(ctx, m); err != nil {
		return s.unwind(ctx, m, paused, err)
	}
	return s.finish(ctx, m)
}

// run does the move, leaving every status decision to its caller.
func (s *Service) run(ctx context.Context, m *core.StorageMigration) error {
	// Re-validated rather than trusted from the enqueue: hours can pass in
	// between, and every rule it enforces describes a way to lose a library.
	// The empty-target rule is deliberately not re-checked — a resumed
	// migration's target is full of its own files.
	if err := ValidateMove(m.SourceRoot, m.TargetRoot); err != nil {
		return err
	}

	entries, err := plan(m.SourceRoot, m.TargetRoot)
	if err != nil {
		return err
	}
	m.FilesTotal, m.FilesDone, m.BytesTotal, m.BytesDone = int64(len(entries)), 0, 0, 0
	for _, e := range entries {
		m.BytesTotal += e.size
	}
	if err := s.st.UpdateStorageMigration(ctx, m); err != nil {
		return err
	}

	flushed := time.Now()
	done := func(e entry) {
		m.FilesDone++
		m.BytesDone += e.size
		if time.Since(flushed) < progressInterval {
			return
		}
		flushed = time.Now()
		if err := s.st.UpdateStorageMigration(ctx, m); err != nil {
			// Progress is a nicety; the move is not. A database that will not
			// take the counter is reported and the copying continues.
			s.log.Warn("storage migration: record progress", "id", m.ID, "error", err)
		}
	}

	mv := &mover{from: m.SourceRoot, to: m.TargetRoot, log: s.log, step: s.step}
	if err := mv.move(ctx, entries, done, false); err != nil {
		return err
	}
	if err := verify(m.SourceRoot, m.TargetRoot, entries); err != nil {
		return err
	}

	// Everything has arrived and been measured. Only now does the database stop
	// describing the old root.
	if err := s.st.SetSetting(ctx, store.SettingStorageRoot, m.TargetRoot); err != nil {
		return err
	}
	pruneEmptyDirs(m.SourceRoot)
	return nil
}

// finish records a completed migration.
//
// The download queue stays paused. The engine captured the old root when it was
// built and cannot be re-pointed underneath a running process (see
// cmd/caravan/acquisition.go), so resuming here would write the next block of
// every download into the root the library just left. Saying "restart to resume
// downloads" is the honest option; quietly resuming into the wrong folder is not.
func (s *Service) finish(ctx context.Context, m *core.StorageMigration) error {
	m.Status = core.StorageMigrationDone
	m.Error = ""
	if err := s.st.UpdateStorageMigration(ctx, m); err != nil {
		s.log.Error("storage migration: record completion", "id", m.ID, "error", err)
	}
	s.event(ctx, core.EventLevelInfo,
		fmt.Sprintf("Storage root moved to %s", m.TargetRoot),
		fmt.Sprintf("%d files moved from %s. Restart Caravan to resume downloads under the new root.",
			m.FilesDone, m.SourceRoot))
	s.log.Info("storage migration finished",
		"id", m.ID, "from", m.SourceRoot, "to", m.TargetRoot, "files", m.FilesDone)
	return nil
}

// unwind puts the files back and records why.
//
// It runs on a context that cannot be cancelled: the process shutting down
// mid-move is exactly when the library most needs to end up back under the root
// the settings table still points at.
func (s *Service) unwind(ctx context.Context, m *core.StorageMigration, paused []core.DownloadID, cause error) error {
	ctx = context.WithoutCancel(ctx)
	s.log.Error("storage migration failed, putting the files back",
		"id", m.ID, "from", m.SourceRoot, "to", m.TargetRoot, "error", cause)

	// The plan is rebuilt from the filesystem rather than reused: after a crash
	// there is no in-memory plan to reuse, and this path has to behave the same
	// either way.
	entries, err := plan(m.TargetRoot, m.SourceRoot)
	if err != nil {
		s.log.Error("storage migration: survey the rollback", "id", m.ID, "error", err)
	} else {
		back := &mover{from: m.TargetRoot, to: m.SourceRoot, log: s.log, step: s.step}
		_ = back.move(ctx, entries, nil, true)
	}
	// The setting is only ever written at the very end of a successful move, but
	// a failure after that write must not leave the root pointing at a tree the
	// files have just been taken out of.
	if err := s.st.SetSetting(ctx, store.SettingStorageRoot, m.SourceRoot); err != nil {
		s.log.Error("storage migration: restore the storage root setting", "id", m.ID, "error", err)
	}
	pruneEmptyDirs(m.TargetRoot)

	m.Status = core.StorageMigrationRolledBack
	if stranded := leftAt(m.TargetRoot); stranded > 0 {
		// The one outcome that needs a human: part of the library is at each
		// root and Caravan could not reunite them.
		m.Status = core.StorageMigrationFailed
		cause = fmt.Errorf("%w (%d files could not be moved back and are still under %s)",
			cause, stranded, m.TargetRoot)
	}
	m.Error = cause.Error()
	if err := s.st.UpdateStorageMigration(ctx, m); err != nil {
		s.log.Error("storage migration: record the failure", "id", m.ID, "error", err)
	}

	// Safe now: the files are back where this engine already expects them.
	s.resumeQueue(ctx, paused)

	level, message := core.EventLevelWarn, "Storage migration rolled back"
	if m.Status == core.StorageMigrationFailed {
		level, message = core.EventLevelError, "Storage migration failed and could not be undone"
	}
	s.event(ctx, level, message, m.Error)
	return nil
}

// pauseQueue pauses every transferring download and reports which ones it
// paused, so a rollback can put the queue back the way it found it.
//
// Downloads that were already paused are left alone and not reported: resuming
// them afterwards would start transfers the user had deliberately stopped.
func (s *Service) pauseQueue(ctx context.Context) []core.DownloadID {
	engine := s.currentEngine()
	if engine == nil {
		return nil
	}
	list, err := engine.List(ctx)
	if err != nil {
		s.log.Warn("storage migration: read the download queue", "error", err)
		return nil
	}
	var paused []core.DownloadID
	for _, d := range list {
		switch d.State {
		case core.DownloadPaused, core.DownloadCompleted, core.DownloadFailed:
			continue
		}
		if err := engine.Pause(ctx, d.ID); err != nil {
			s.log.Warn("storage migration: pause a download", "download", d.ID, "error", err)
			continue
		}
		paused = append(paused, d.ID)
	}
	if len(paused) > 0 {
		s.log.Info("storage migration: download queue paused", "downloads", len(paused))
	}
	return paused
}

func (s *Service) resumeQueue(ctx context.Context, ids []core.DownloadID) {
	if len(ids) == 0 {
		return
	}
	engine := s.currentEngine()
	if engine == nil {
		return
	}
	for _, id := range ids {
		if err := engine.Resume(ctx, id); err != nil {
			s.log.Warn("storage migration: resume a download", "download", id, "error", err)
		}
	}
}

func (s *Service) currentEngine() core.Engine {
	if s.engine == nil {
		return nil
	}
	return s.engine()
}

func (s *Service) event(ctx context.Context, level, message, detail string) {
	err := s.st.InsertEvent(ctx, &core.Event{
		Level:    level,
		Category: EventCategory,
		Message:  message,
		Detail:   detail,
	})
	if err != nil {
		s.log.Error("storage migration: record an event", "error", err)
	}
}
