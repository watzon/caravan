package store

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

func TestEnqueueAndClaimJob(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	// An idle queue is not an error condition.
	claimed, err := st.ClaimJob(ctx, nil, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob on an empty queue: %v", err)
	}
	if claimed != nil {
		t.Fatalf("ClaimJob on an empty queue = %+v, want nil", *claimed)
	}

	j := core.Job{Kind: "scan", Payload: `{"root":"Movies"}`}
	if err := st.EnqueueJob(ctx, &j); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if j.ID == 0 {
		t.Fatal("EnqueueJob did not write back an ID")
	}
	if j.State != core.JobStatePending {
		t.Errorf("State = %q, want %q", j.State, core.JobStatePending)
	}

	claimed, err = st.ClaimJob(ctx, []string{"scan"}, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimJob = nil, want the enqueued job")
	}
	if claimed.ID != j.ID || claimed.Payload != j.Payload {
		t.Errorf("ClaimJob = %+v, want job %d with payload %q", *claimed, j.ID, j.Payload)
	}
	if claimed.State != core.JobStateRunning {
		t.Errorf("claimed State = %q, want %q", claimed.State, core.JobStateRunning)
	}
	if claimed.LeaseExpiresAt.IsZero() {
		t.Error("claimed job has no lease expiry")
	}

	// A claimed job is off the queue: no second worker gets it.
	again, err := st.ClaimJob(ctx, nil, time.Minute)
	if err != nil {
		t.Fatalf("second ClaimJob: %v", err)
	}
	if again != nil {
		t.Errorf("second ClaimJob = job %d, want nil (already leased)", again.ID)
	}

	if err := st.CompleteJob(ctx, j.ID); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	done, err := st.GetJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if done.State != core.JobStateDone {
		t.Errorf("State after CompleteJob = %q, want %q", done.State, core.JobStateDone)
	}
	if !done.LeaseExpiresAt.IsZero() {
		t.Errorf("LeaseExpiresAt after CompleteJob = %v, want zero", done.LeaseExpiresAt)
	}

	if err := st.CompleteJob(ctx, 404); !errors.Is(err, ErrNotFound) {
		t.Errorf("CompleteJob(absent) = %v, want ErrNotFound", err)
	}
	if _, err := st.GetJob(ctx, 404); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetJob(absent) = %v, want ErrNotFound", err)
	}
}

func TestClaimJobFiltersByKind(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	scan := core.Job{Kind: "scan"}
	if err := st.EnqueueJob(ctx, &scan); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	imp := core.Job{Kind: "import"}
	if err := st.EnqueueJob(ctx, &imp); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	// An import worker must not pick up the older scan job.
	claimed, err := st.ClaimJob(ctx, []string{"import"}, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if claimed == nil || claimed.ID != imp.ID {
		t.Fatalf("ClaimJob([import]) = %v, want job %d", claimed, imp.ID)
	}

	// A kind that is not queued yields nothing rather than the wrong job.
	other, err := st.ClaimJob(ctx, []string{"convert"}, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob(convert): %v", err)
	}
	if other != nil {
		t.Errorf("ClaimJob([convert]) = job %d of kind %q, want nil", other.ID, other.Kind)
	}
}

func TestClaimJobRespectsRunAfter(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	future := core.Job{Kind: "search", RunAfter: time.Now().UTC().Add(time.Hour)}
	if err := st.EnqueueJob(ctx, &future); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	claimed, err := st.ClaimJob(ctx, nil, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if claimed != nil {
		t.Fatalf("ClaimJob = job %d scheduled for %v, want nil", claimed.ID, claimed.RunAfter)
	}

	// An eligible job queued later still gets picked up.
	ready := core.Job{Kind: "search"}
	if err := st.EnqueueJob(ctx, &ready); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimed, err = st.ClaimJob(ctx, nil, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if claimed == nil || claimed.ID != ready.ID {
		t.Fatalf("ClaimJob = %v, want job %d", claimed, ready.ID)
	}
}

func TestFailJobBacksOffThenGivesUp(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	j := core.Job{Kind: "search"}
	if err := st.EnqueueJob(ctx, &j); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	for attempt := 1; attempt < JobMaxAttempts; attempt++ {
		if err := st.FailJob(ctx, j.ID, "indexer unreachable"); err != nil {
			t.Fatalf("FailJob attempt %d: %v", attempt, err)
		}
		got, err := st.GetJob(ctx, j.ID)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		if got.State != core.JobStatePending {
			t.Fatalf("State after attempt %d = %q, want %q", attempt, got.State, core.JobStatePending)
		}
		if got.Attempts != attempt {
			t.Errorf("Attempts = %d, want %d", got.Attempts, attempt)
		}
		if got.LastError != "indexer unreachable" {
			t.Errorf("LastError = %q, want the failure reason", got.LastError)
		}
		// Backoff must actually delay the retry, and grow.
		delay := time.Until(got.RunAfter)
		want := RetryDelay(attempt)
		if delay <= 0 || delay > want {
			t.Errorf("attempt %d retry delay = %v, want (0, %v]", attempt, delay, want)
		}
		// A backed-off job is not claimable yet.
		claimed, err := st.ClaimJob(ctx, nil, time.Minute)
		if err != nil {
			t.Fatalf("ClaimJob: %v", err)
		}
		if claimed != nil {
			t.Fatalf("ClaimJob returned backed-off job %d", claimed.ID)
		}
	}

	if err := st.FailJob(ctx, j.ID, "indexer unreachable"); err != nil {
		t.Fatalf("final FailJob: %v", err)
	}
	got, err := st.GetJob(ctx, j.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.State != core.JobStateFailed {
		t.Errorf("State after %d attempts = %q, want %q", JobMaxAttempts, got.State, core.JobStateFailed)
	}
	if got.Attempts != JobMaxAttempts {
		t.Errorf("Attempts = %d, want %d", got.Attempts, JobMaxAttempts)
	}

	if err := st.FailJob(ctx, 404, "gone"); !errors.Is(err, ErrNotFound) {
		t.Errorf("FailJob(absent) = %v, want ErrNotFound", err)
	}
}

func TestRetryDelayGrowsAndCaps(t *testing.T) {
	if got := RetryDelay(1); got != JobRetryBaseDelay {
		t.Errorf("RetryDelay(1) = %v, want %v", got, JobRetryBaseDelay)
	}
	if got := RetryDelay(2); got != 2*JobRetryBaseDelay {
		t.Errorf("RetryDelay(2) = %v, want %v", got, 2*JobRetryBaseDelay)
	}
	if got := RetryDelay(99); got != JobRetryMaxDelay {
		t.Errorf("RetryDelay(99) = %v, want the cap %v", got, JobRetryMaxDelay)
	}
	// Defensive: a nonsense attempt count must not produce a zero delay.
	if got := RetryDelay(0); got != JobRetryBaseDelay {
		t.Errorf("RetryDelay(0) = %v, want %v", got, JobRetryBaseDelay)
	}
}

func TestReclaimExpiredLeases(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	j := core.Job{Kind: "import"}
	if err := st.EnqueueJob(ctx, &j); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	// A worker claims the job with a lease that has already expired — the
	// state a crash mid-job leaves behind (SPEC §7).
	if _, err := st.ClaimJob(ctx, nil, -time.Second); err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}

	// Before the sweep, the job is nobody's: leased, but the worker is gone.
	stuck, err := st.ClaimJob(ctx, nil, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if stuck != nil {
		t.Fatalf("ClaimJob = job %d, want nil before the lease is reclaimed", stuck.ID)
	}

	n, err := st.ReclaimExpired(ctx)
	if err != nil {
		t.Fatalf("ReclaimExpired: %v", err)
	}
	if n != 1 {
		t.Fatalf("ReclaimExpired = %d, want 1", n)
	}

	reclaimed, err := st.ClaimJob(ctx, nil, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob after reclaim: %v", err)
	}
	if reclaimed == nil || reclaimed.ID != j.ID {
		t.Fatalf("ClaimJob after reclaim = %v, want job %d", reclaimed, j.ID)
	}
	// An expired lease is not the job's fault, so the attempt count is
	// untouched and the retry budget is not burned.
	if reclaimed.Attempts != 0 {
		t.Errorf("Attempts after reclaim = %d, want 0", reclaimed.Attempts)
	}

	// A live lease is left alone.
	n, err = st.ReclaimExpired(ctx)
	if err != nil {
		t.Fatalf("ReclaimExpired with a live lease: %v", err)
	}
	if n != 0 {
		t.Errorf("ReclaimExpired = %d, want 0 (lease still valid)", n)
	}
}

// A dedicated worker takes a twelve-hour lease (automation.dedicatedJobLease),
// because the kinds that get their own worker are the ones that run for hours.
// A crash five minutes into one therefore leaves a row in `running` with a
// lease nothing will reclaim until tomorrow, and the dedicated worker only ever
// claims `pending` — so a storage migration killed mid-move stayed stuck with
// the library's files split across two roots and no way to fix it from the UI.
//
// Startup is the one moment where "running" can only mean "left by a process
// that died": exactly one Caravan owns a storage root at a time.
func TestReclaimRunningUnsticksALongLeaseLeftByACrash(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	j := core.Job{Kind: "storage_migrate"}
	if err := st.EnqueueJob(ctx, &j); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	if _, err := st.ClaimJob(ctx, []string{"storage_migrate"}, 12*time.Hour); err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}

	// The periodic sweep cannot help: the lease is good for another 12 hours.
	if n, err := st.ReclaimExpired(ctx); err != nil {
		t.Fatalf("ReclaimExpired: %v", err)
	} else if n != 0 {
		t.Fatalf("ReclaimExpired = %d, want 0 — the lease has not expired", n)
	}
	if stuck, err := st.ClaimJob(ctx, []string{"storage_migrate"}, time.Minute); err != nil {
		t.Fatalf("ClaimJob: %v", err)
	} else if stuck != nil {
		t.Fatalf("ClaimJob = job %d before the startup sweep; the fixture is wrong", stuck.ID)
	}

	n, err := st.ReclaimRunning(ctx)
	if err != nil {
		t.Fatalf("ReclaimRunning: %v", err)
	}
	if n != 1 {
		t.Fatalf("ReclaimRunning = %d, want 1", n)
	}

	reclaimed, err := st.ClaimJob(ctx, []string{"storage_migrate"}, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob after the startup sweep: %v", err)
	}
	if reclaimed == nil || reclaimed.ID != j.ID {
		t.Fatalf("ClaimJob after the startup sweep = %v, want job %d", reclaimed, j.ID)
	}
	// The worker went away; that says nothing about the job, so the retry
	// budget is untouched.
	if reclaimed.Attempts != 0 {
		t.Errorf("Attempts after reclaim = %d, want 0", reclaimed.Attempts)
	}
}

func TestListJobsPageUsesStrictIDBoundaries(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)
	for i := range 5 {
		job := core.Job{Kind: "test", Payload: "{}"}
		if err := st.EnqueueJob(ctx, &job); err != nil {
			t.Fatalf("EnqueueJob %d: %v", i, err)
		}
	}

	first, next, err := st.ListJobsPage(ctx, 2, 0)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first) != 2 || next != first[1].ID {
		t.Fatalf("first page = %d rows, next %d, want 2 rows and cursor %d", len(first), next, first[1].ID)
	}
	second, final, err := st.ListJobsPage(ctx, 2, next)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second) != 2 || final != second[1].ID {
		t.Fatalf("second page = %d rows, final %d, want 2 rows and cursor %d", len(second), final, second[1].ID)
	}
	third, done, err := st.ListJobsPage(ctx, 2, final)
	if err != nil {
		t.Fatalf("final page: %v", err)
	}
	if len(third) != 1 || done != 0 {
		t.Fatalf("final page = %d rows, cursor %d, want one row and empty cursor", len(third), done)
	}
	if first[1].ID <= second[0].ID || second[1].ID <= third[0].ID {
		t.Fatalf("page boundary IDs are not strictly descending: %d, %d, %d", first[1].ID, second[0].ID, third[0].ID)
	}
}
