// Package automation runs Caravan's durable background jobs.
package automation

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

const (
	jobRSSSync       = "rss_sync"
	jobBacklogSweep  = "backlog_sweep"
	jobSearchMovie   = "search_movie"
	jobSearchEpisode = "search_episode"

	jobLease        = 2 * time.Minute
	jobPollInterval = time.Second
)

// Handler performs one job's durable work. It receives only decoded job data
// and shared collaborators, which keeps handlers independently testable and
// makes at-least-once delivery an explicit concern at each boundary.
type Handler func(ctx context.Context, st *store.Store, payload json.RawMessage) error

// EngineGetter waits for, or returns, the currently configured download engine.
// It may return nil when no storage root has been configured yet.
type EngineGetter func(ctx context.Context) core.Engine

// Runner claims durable jobs and dispatches them to idempotent handlers.
type Runner struct {
	st       *store.Store
	indexers api.IndexerFactory
	engine   EngineGetter
	handlers map[string]Handler
}

// NewRunner creates the standard phase-3 automation runner.
func NewRunner(st *store.Store, indexers api.IndexerFactory, engine EngineGetter) *Runner {
	r := &Runner{
		st:       st,
		indexers: indexers,
		engine:   engine,
		handlers: make(map[string]Handler),
	}
	r.handlers[jobRSSSync] = r.handleRSSSync
	r.handlers[jobBacklogSweep] = r.handleBacklogSweep
	r.handlers[jobSearchMovie] = r.handleSearchMovie
	r.handlers[jobSearchEpisode] = r.handleSearchEpisode
	return r
}

// Bootstrap enqueues the two recurring roots when they are not already pending
// or running. Repeating this at every process start is safe and prevents a
// restart from depending on an old timer surviving shutdown.
func Bootstrap(ctx context.Context, st *store.Store) error {
	for _, kind := range []string{jobRSSSync, jobBacklogSweep} {
		open, err := st.HasOpenJob(ctx, kind, "{}")
		if err != nil {
			return fmt.Errorf("store: check %s bootstrap job: %w", kind, err)
		}
		if open {
			continue
		}
		if err := st.EnqueueJob(ctx, &core.Job{Kind: kind, Payload: "{}"}); err != nil {
			return fmt.Errorf("store: enqueue %s bootstrap job: %w", kind, err)
		}
	}
	return nil
}

// Run serves jobs until ctx is cancelled. Reclaiming at startup and once a
// minute returns leases abandoned by a crash without making job attempts look
// like semantic failures.
func (r *Runner) Run(ctx context.Context) {
	_, _ = r.st.ReclaimExpired(ctx)

	poll := time.NewTicker(jobPollInterval)
	defer poll.Stop()
	reclaim := time.NewTicker(time.Minute)
	defer reclaim.Stop()

	for {
		worked, _ := r.ProcessOne(ctx)
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-reclaim.C:
			_, _ = r.st.ReclaimExpired(ctx)
		case <-poll.C:
		}
	}
}

// ProcessOne claims and processes one eligible job. It is exported to make the
// lease-to-completion transition testable without a timing-dependent runner.
func (r *Runner) ProcessOne(ctx context.Context) (bool, error) {
	job, err := r.st.ClaimJob(ctx, nil, jobLease)
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
	// is complete, schedule the successor under the normal singleton check.
	if job.Kind == jobRSSSync || job.Kind == jobBacklogSweep {
		if err := r.scheduleRecurring(ctx, job.Kind); err != nil {
			return true, err
		}
	}
	return true, nil
}
