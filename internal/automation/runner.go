// Package automation runs Caravan's durable background jobs.
package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

const (
	jobLease        = 2 * time.Minute
	jobPollInterval = time.Second

	// dedicatedJobLease is the lease a dedicated worker takes. It is hours
	// rather than minutes because the kind that gets its own worker is the kind
	// that runs for hours (a 4K transcode), and a lease that expires under a
	// running handler is one the reclaim sweep hands back to the pending pool.
	dedicatedJobLease = 12 * time.Hour
)

// Handler performs one job's durable work. It receives only decoded job data
// and shared collaborators, which keeps handlers independently testable and
// makes at-least-once delivery an explicit concern at each boundary.
type Handler func(ctx context.Context, st *store.Store, payload json.RawMessage) error

// EngineGetter waits for, or returns, the currently configured download engine
// for grabs made on behalf of the library of one core.LibraryKind* — "" for an
// operation belonging to no library. The kind is what honours a library's own
// download routing (PLAN phase 8 task 2). It may return nil when no storage
// root has been configured yet.
type EngineGetter func(ctx context.Context, kind string) core.Engine

// Runner claims durable jobs and dispatches them to idempotent handlers.
type Runner struct {
	st       *store.Store
	indexers api.IndexerFactory
	engine   EngineGetter
	handlers map[string]Handler
	// dedicated are the kinds served by a worker of their own. The general
	// worker never claims them, which is what keeps a job that runs for hours
	// from being the only job that runs.
	dedicated []string
}

// Option configures a Runner at construction.
type Option func(*Runner)

// WithHandler registers a job kind owned by another package.
//
// The phase-3 handlers live here because they are the automation brain. The
// convert-for-TV queue (PLAN phase 4, task 4) is not: it is ffmpeg work that
// needs the storage root, which this package deliberately does not know about.
// Registering it from outside keeps the durable-queue semantics — leases,
// backoff, at-least-once — in exactly one place without dragging the
// filesystem in with them.
func WithHandler(kind string, h Handler) Option {
	return func(r *Runner) { r.handlers[kind] = h }
}

// WithDedicatedWorker registers a kind and gives it a worker of its own.
//
// The general worker is a single goroutine that blocks for as long as a handler
// runs, and a convert job is an ffmpeg process that can run for hours. Left on
// the shared worker it starves everything behind it: the Jellyfin handoff an
// import just queued, the RSS sync, every monitored search — a release that
// appears and expires inside a long transcode is simply missed. The dedicated
// worker is also single-goroutine, so the one-conversion-at-a-time assumption
// internal/convert is written against still holds.
func WithDedicatedWorker(kind string, h Handler) Option {
	return func(r *Runner) {
		r.handlers[kind] = h
		r.dedicated = append(r.dedicated, kind)
	}
}

// NewRunner creates the standard phase-3 automation runner.
func NewRunner(st *store.Store, indexers api.IndexerFactory, engine EngineGetter, opts ...Option) *Runner {
	r := &Runner{
		st:       st,
		indexers: indexers,
		engine:   engine,
		handlers: make(map[string]Handler),
	}
	r.handlers[core.JobRSSSync] = r.handleRSSSync
	r.handlers[core.JobBacklogSweep] = r.handleBacklogSweep
	r.handlers[core.JobSearchMovie] = r.handleSearchMovie
	r.handlers[core.JobSearchEpisode] = r.handleSearchEpisode
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Bootstrap enqueues the two recurring roots when they are not already pending
// or running. Repeating this at every process start is safe and prevents a
// restart from depending on an old timer surviving shutdown.
//
// It also hands back every lease the previous process died holding. That has to
// happen here, once, at startup: the periodic sweep can only take leases that
// have expired, and a dedicated worker's lease runs for twelve hours
// (dedicatedJobLease), so a storage migration killed five minutes in would
// otherwise be unclaimable for the rest of the day — with the library's files
// half-moved and no way to fix it from the UI.
func Bootstrap(ctx context.Context, st *store.Store) error {
	if n, err := st.ReclaimRunning(ctx); err != nil {
		return err
	} else if n > 0 {
		slog.Warn("jobs left running by a previous process were returned to the queue", "jobs", n)
	}
	for _, kind := range []string{core.JobRSSSync, core.JobBacklogSweep, core.JobRefreshMetadata} {
		open, err := st.HasOpenJob(ctx, kind, "{}")
		if err != nil {
			return fmt.Errorf("store: check %s bootstrap job: %w", kind, err)
		}
		if open {
			continue
		}
		job := &core.Job{Kind: kind, Payload: "{}"}
		// The searches run right away — a restart must not delay a release
		// that appeared while the process was down. The metadata refresh
		// waits a full interval instead: it is one provider round trip per
		// movie plus one per season, dates change on the scale of days, and a
		// dev loop restarting the process must not hammer TMDB every time.
		if kind == core.JobRefreshMetadata {
			job.RunAfter = time.Now().Add(
				time.Duration(settingMinutes(ctx, st, store.SettingRefreshIntervalMinutes, defaultRefreshInterval)) * time.Minute)
		}
		if err := st.EnqueueJob(ctx, job); err != nil {
			return fmt.Errorf("store: enqueue %s bootstrap job: %w", kind, err)
		}
	}
	return nil
}

// Run serves jobs until ctx is cancelled. Reclaiming at startup and once a
// minute returns leases abandoned by a crash without making job attempts look
// like semantic failures.
//
// Every kind registered with WithDedicatedWorker gets a goroutine of its own;
// the general worker runs on this one and claims everything else.
func (r *Runner) Run(ctx context.Context) {
	_, _ = r.st.ReclaimExpired(ctx)

	var workers sync.WaitGroup
	for _, kind := range r.dedicated {
		workers.Add(1)
		go func() {
			defer workers.Done()
			r.serve(ctx, func(ctx context.Context) (*core.Job, error) {
				return r.st.ClaimJob(ctx, []string{kind}, dedicatedJobLease)
			}, false)
		}()
	}

	r.serve(ctx, r.claimGeneral, true)
	workers.Wait()
}

// serve is one worker's loop. Only the general worker sweeps expired leases:
// one sweeper is enough, and a dedicated worker is the one most likely to be
// deep inside a handler when the tick comes.
func (r *Runner) serve(ctx context.Context, claim claimFunc, sweep bool) {
	poll := time.NewTicker(jobPollInterval)
	defer poll.Stop()
	reclaim := time.NewTicker(time.Minute)
	defer reclaim.Stop()

	for {
		worked, _ := r.process(ctx, claim)
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-reclaim.C:
			if sweep {
				_, _ = r.st.ReclaimExpired(ctx)
			}
		case <-poll.C:
		}
	}
}

// claimFunc takes the next job a worker may run, or nil when there is none.
type claimFunc func(ctx context.Context) (*core.Job, error)

// claimGeneral claims anything not owned by a dedicated worker.
func (r *Runner) claimGeneral(ctx context.Context) (*core.Job, error) {
	return r.st.ClaimJobExcept(ctx, r.dedicated, jobLease)
}

// ProcessOne claims and processes one eligible job on the general worker. It is
// exported to make the lease-to-completion transition testable without a
// timing-dependent runner.
func (r *Runner) ProcessOne(ctx context.Context) (bool, error) {
	return r.process(ctx, r.claimGeneral)
}

func (r *Runner) process(ctx context.Context, claim claimFunc) (bool, error) {
	job, err := claim(ctx)
	if err != nil {
		return false, fmt.Errorf("store: claim job: %w", err)
	}
	if job == nil {
		return false, nil
	}

	handler, ok := r.handlers[job.Kind]
	if !ok {
		err = fmt.Errorf("unsupported job kind %q", job.Kind)
	} else {
		err = handler(ctx, r.st, json.RawMessage(job.Payload))
	}
	if err != nil {
		if failErr := r.st.FailJob(ctx, job.ID, err.Error()); failErr != nil {
			return true, fmt.Errorf("store: fail job %d: %w", job.ID, failErr)
		}
		return true, nil
	}
	if err := r.st.CompleteJob(ctx, job.ID); err != nil {
		return true, fmt.Errorf("store: complete job %d: %w", job.ID, err)
	}

	// A running recurring job counts as open while its handler executes. Once it
	// is complete, schedule the successor under the normal singleton check. The
	// refresh handler is registered from outside the package (cmd/caravan), so
	// this hook is the only thing that keeps its chain going.
	if job.Kind == core.JobRSSSync || job.Kind == core.JobBacklogSweep || job.Kind == core.JobRefreshMetadata {
		if err := r.scheduleRecurring(ctx, job.Kind); err != nil {
			return true, err
		}
	}
	return true, nil
}
