package automation

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// TestWithHandlerDispatchesForeignKinds covers the extension point the
// convert-for-TV queue rides on (PLAN phase 4, task 4): a job kind owned by
// another package gets the same lease, retry and completion semantics as the
// automation brain's own.
func TestWithHandlerDispatchesForeignKinds(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	var seen []string
	runner := NewRunner(st, nil, nil, WithHandler("frobnicate", func(_ context.Context, _ *store.Store, payload json.RawMessage) error {
		seen = append(seen, string(payload))
		return nil
	}))

	if err := st.EnqueueJob(ctx, &core.Job{Kind: "frobnicate", Payload: `{"n":1}`}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	worked, err := runner.ProcessOne(ctx)
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if !worked {
		t.Fatal("ProcessOne claimed nothing")
	}
	if len(seen) != 1 || seen[0] != `{"n":1}` {
		t.Fatalf("handler saw %v, want one payload", seen)
	}

	jobs, err := st.ListJobs(ctx, 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 1 || jobs[0].State != core.JobStateDone {
		t.Fatalf("job = %+v, want done", jobs[0])
	}
}

// TestWithHandlerFailuresRetry proves the borrowed kind also inherits the
// backoff: a failing handler parks the job as pending, not lost.
func TestWithHandlerFailuresRetry(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	runner := NewRunner(st, nil, nil, WithHandler("frobnicate", func(context.Context, *store.Store, json.RawMessage) error {
		return errors.New("boom")
	}))
	if err := st.EnqueueJob(ctx, &core.Job{Kind: "frobnicate", Payload: "{}"}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if _, err := runner.ProcessOne(ctx); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}

	jobs, err := st.ListJobs(ctx, 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if jobs[0].State != core.JobStatePending || jobs[0].Attempts != 1 || jobs[0].LastError != "boom" {
		t.Fatalf("job = %+v, want a pending retry recording the failure", jobs[0])
	}
}

// TestWithHandlerDoesNotDisplaceTheBuiltIns is the guard on the option: it
// registers, it does not replace the phase-3 handler table.
func TestWithHandlerDoesNotDisplaceTheBuiltIns(t *testing.T) {
	runner := NewRunner(openStore(t), nil, nil, WithHandler("frobnicate", func(context.Context, *store.Store, json.RawMessage) error {
		return nil
	}))
	for _, kind := range []string{core.JobRSSSync, core.JobBacklogSweep, core.JobSearchMovie, core.JobSearchEpisode, "frobnicate"} {
		if _, ok := runner.handlers[kind]; !ok {
			t.Errorf("handler for %q is missing", kind)
		}
	}
}

// TestDedicatedWorkerDoesNotStarveTheGeneralQueue is PLAN phase 4's
// convert-for-TV queue on the shared runner: a transcode blocks its handler for
// hours, and on one goroutine that is hours in which the Jellyfin handoff, the
// RSS sync and every monitored search do not run.
func TestDedicatedWorkerDoesNotStarveTheGeneralQueue(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	st := openStore(t)

	release := make(chan struct{})
	started := make(chan struct{})
	fast := make(chan struct{}, 1)
	runner := NewRunner(st, nil, nil,
		WithDedicatedWorker("slow", func(context.Context, *store.Store, json.RawMessage) error {
			close(started)
			<-release
			return nil
		}),
		WithHandler("fast", func(context.Context, *store.Store, json.RawMessage) error {
			fast <- struct{}{}
			return nil
		}))

	// The long job is queued first, so a single worker would reach it first.
	for _, kind := range []string{"slow", "fast"} {
		if err := st.EnqueueJob(ctx, &core.Job{Kind: kind, Payload: "{}"}); err != nil {
			t.Fatalf("EnqueueJob(%s): %v", kind, err)
		}
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.Run(ctx)
	}()

	select {
	case <-started:
	case <-time.After(10 * time.Second):
		t.Fatal("the dedicated worker never claimed its job")
	}
	select {
	case <-fast:
	case <-time.After(10 * time.Second):
		t.Fatal("the general queue is starved while a dedicated job runs")
	}

	close(release)
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
}

// The other half of the split: the general worker must leave a dedicated kind
// alone, or it claims the long job first and the dedicated worker is decoration.
func TestGeneralWorkerLeavesDedicatedKindsAlone(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	runner := NewRunner(st, nil, nil,
		WithDedicatedWorker("slow", func(context.Context, *store.Store, json.RawMessage) error {
			t.Error("the general worker ran a dedicated kind")
			return nil
		}))
	if err := st.EnqueueJob(ctx, &core.Job{Kind: "slow", Payload: "{}"}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	worked, err := runner.ProcessOne(ctx)
	if err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if worked {
		t.Fatal("the general worker claimed a job belonging to a dedicated worker")
	}
}

// Bootstrap starts the metadata-refresh chain, but a full interval out: the
// searches must run the moment a restarted process comes up, while the
// refresh is a provider round trip per title and must not fire on every dev
// restart.
func TestBootstrapDefersTheFirstMetadataRefresh(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	if err := Bootstrap(ctx, st); err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}

	jobs, err := st.ListJobs(ctx, 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	byKind := map[string]core.Job{}
	for _, j := range jobs {
		byKind[j.Kind] = j
	}

	refresh, ok := byKind[core.JobRefreshMetadata]
	if !ok {
		t.Fatal("Bootstrap did not enqueue refresh_metadata")
	}
	if !refresh.RunAfter.After(time.Now().Add(11 * time.Hour)) {
		t.Errorf("refresh RunAfter = %v, want roughly the 12h default out", refresh.RunAfter)
	}
	for _, kind := range []string{core.JobRSSSync, core.JobBacklogSweep} {
		j, ok := byKind[kind]
		if !ok {
			t.Fatalf("Bootstrap did not enqueue %s", kind)
		}
		if !j.RunAfter.IsZero() {
			t.Errorf("%s RunAfter = %v, want immediate", kind, j.RunAfter)
		}
	}

	// A second Bootstrap is a restart: everything is already open, nothing
	// doubles up.
	if err := Bootstrap(ctx, st); err != nil {
		t.Fatalf("Bootstrap again: %v", err)
	}
	jobs, err = st.ListJobs(ctx, 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 3 {
		t.Fatalf("jobs after a second Bootstrap = %d, want still 3", len(jobs))
	}
}

// The refresh handler lives in cmd/caravan, so the runner's completion hook is
// the only thing that keeps the chain recurring — a completed sweep must leave
// a successor on the queue.
func TestMetadataRefreshReschedulesItself(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)

	ran := 0
	runner := NewRunner(st, nil, nil, WithHandler(core.JobRefreshMetadata,
		func(context.Context, *store.Store, json.RawMessage) error {
			ran++
			return nil
		}))

	if err := st.EnqueueJob(ctx, &core.Job{Kind: core.JobRefreshMetadata, Payload: "{}"}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if _, err := runner.ProcessOne(ctx); err != nil {
		t.Fatalf("ProcessOne: %v", err)
	}
	if ran != 1 {
		t.Fatalf("handler ran %d times, want 1", ran)
	}

	jobs, err := st.ListJobs(ctx, 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	var successor *core.Job
	for i := range jobs {
		if jobs[i].Kind == core.JobRefreshMetadata && jobs[i].State == core.JobStatePending {
			successor = &jobs[i]
		}
	}
	if successor == nil {
		t.Fatal("no pending successor: the refresh chain died after one run")
	}
	if !successor.RunAfter.After(time.Now()) {
		t.Errorf("successor RunAfter = %v, want in the future", successor.RunAfter)
	}
}
