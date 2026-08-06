package store

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// enqueued writes a job and hands back its id.
func enqueued(t *testing.T, st *Store, kind string, runAfter time.Time) int64 {
	t.Helper()
	j := core.Job{Kind: kind, Payload: "{}", RunAfter: runAfter}
	if err := st.EnqueueJob(context.Background(), &j); err != nil {
		t.Fatalf("EnqueueJob(%s): %v", kind, err)
	}
	return j.ID
}

// statusOf picks one kind out of a RecurringJobStatus result.
func statusOf(t *testing.T, statuses []JobStatus, kind string) JobStatus {
	t.Helper()
	for _, s := range statuses {
		if s.Kind == kind {
			return s
		}
	}
	t.Fatalf("no status for kind %q in %+v", kind, statuses)
	return JobStatus{}
}

// The Tasks screen reads exactly two rows per kind out of a queue that holds
// every run ever made: the newest finished one, and the one still open. Mixing
// kinds, states and generations is the normal shape of that table, so the
// query has to pick correctly out of all three at once.
func TestRecurringJobStatusPicksTheLatestFinishedAndTheOpenRow(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	// Two completed RSS generations, oldest first, then the pending successor.
	first := enqueued(t, st, core.JobRSSSync, time.Time{})
	if _, err := st.ClaimJob(ctx, []string{core.JobRSSSync}, time.Minute); err != nil {
		t.Fatalf("claim first rss job: %v", err)
	}
	if err := st.CompleteJob(ctx, first); err != nil {
		t.Fatalf("complete first rss job: %v", err)
	}
	second := enqueued(t, st, core.JobRSSSync, time.Time{})
	if _, err := st.ClaimJob(ctx, []string{core.JobRSSSync}, time.Minute); err != nil {
		t.Fatalf("claim second rss job: %v", err)
	}
	if err := st.CompleteJob(ctx, second); err != nil {
		t.Fatalf("complete second rss job: %v", err)
	}
	next := time.Now().Add(15 * time.Minute)
	successor := enqueued(t, st, core.JobRSSSync, next)

	// A different kind, still pending, must not leak into the RSS answer.
	backlog := enqueued(t, st, core.JobBacklogSweep, time.Now().Add(6*time.Hour))

	statuses, err := st.RecurringJobStatus(ctx, RecurringKinds())
	if err != nil {
		t.Fatalf("RecurringJobStatus: %v", err)
	}
	if len(statuses) != len(RecurringKinds()) {
		t.Fatalf("statuses = %d, want one per recurring kind", len(statuses))
	}

	rss := statusOf(t, statuses, core.JobRSSSync)
	if rss.LastFinished == nil || rss.LastFinished.ID != second {
		t.Errorf("LastFinished = %+v, want the newer completed job %d", rss.LastFinished, second)
	}
	if rss.Open == nil || rss.Open.ID != successor {
		t.Errorf("Open = %+v, want the pending successor %d", rss.Open, successor)
	}
	if rss.Open != nil && rss.Open.RunAfter.IsZero() {
		t.Error("Open.RunAfter is zero, want the scheduled next run")
	}

	sweep := statusOf(t, statuses, core.JobBacklogSweep)
	if sweep.LastFinished != nil {
		t.Errorf("backlog LastFinished = %+v, want nil: it has never finished", sweep.LastFinished)
	}
	if sweep.Open == nil || sweep.Open.ID != backlog {
		t.Errorf("backlog Open = %+v, want job %d", sweep.Open, backlog)
	}

	// A kind with no rows at all answers with a status, not an omission.
	refresh := statusOf(t, statuses, core.JobRefreshMetadata)
	if refresh.LastFinished != nil || refresh.Open != nil {
		t.Errorf("refresh status = %+v, want both halves nil", refresh)
	}
}

// A failed run is the one the screen most needs to show, and FailJob parks it
// with the reason. The newest finished row wins whether it succeeded or not.
func TestRecurringJobStatusReportsAFailedLatestRun(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	done := enqueued(t, st, core.JobRefreshMetadata, time.Time{})
	if _, err := st.ClaimJob(ctx, []string{core.JobRefreshMetadata}, time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := st.CompleteJob(ctx, done); err != nil {
		t.Fatalf("complete: %v", err)
	}

	failed := enqueued(t, st, core.JobRefreshMetadata, time.Time{})
	// FailJob only parks in the failed state once the attempts are spent.
	for range JobMaxAttempts {
		if err := st.FailJob(ctx, failed, "tmdb unreachable"); err != nil {
			t.Fatalf("FailJob: %v", err)
		}
	}

	statuses, err := st.RecurringJobStatus(ctx, []string{core.JobRefreshMetadata})
	if err != nil {
		t.Fatalf("RecurringJobStatus: %v", err)
	}
	latest := statuses[0].LastFinished
	if latest == nil || latest.ID != failed {
		t.Fatalf("LastFinished = %+v, want the failed job %d", latest, failed)
	}
	if latest.State != core.JobStateFailed || latest.LastError != "tmdb unreachable" {
		t.Errorf("LastFinished = %q/%q, want failed with its reason", latest.State, latest.LastError)
	}
	// A parked failure is not open work: nothing is queued behind it.
	if statuses[0].Open != nil {
		t.Errorf("Open = %+v, want nil: the failed row is not open", statuses[0].Open)
	}
}

// A running job outranks a pending one. Both exist during a cycle whose
// successor was enqueued early, and reporting the pending row would show a
// future "next run" for work that is happening right now.
func TestRecurringJobStatusPrefersTheRunningRow(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	running := enqueued(t, st, core.JobRSSSync, time.Time{})
	if _, err := st.ClaimJob(ctx, []string{core.JobRSSSync}, time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}
	enqueued(t, st, core.JobRSSSync, time.Now().Add(15*time.Minute))

	statuses, err := st.RecurringJobStatus(ctx, []string{core.JobRSSSync})
	if err != nil {
		t.Fatalf("RecurringJobStatus: %v", err)
	}
	open := statuses[0].Open
	if open == nil || open.ID != running || open.State != core.JobStateRunning {
		t.Fatalf("Open = %+v, want the running job %d", open, running)
	}
}

// Run-now moves the row that already exists rather than adding one: the
// recurring chain keeps exactly one open row per kind, and the proof it worked
// is that ClaimJob — which was finding nothing due — now hands the job over.
func TestRunJobNowMakesThePendingRowClaimable(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	id := enqueued(t, st, core.JobRSSSync, time.Now().Add(15*time.Minute))
	// Another kind's pending row must not be dragged forward with it.
	other := enqueued(t, st, core.JobBacklogSweep, time.Now().Add(6*time.Hour))

	claimed, err := st.ClaimJob(ctx, []string{core.JobRSSSync}, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob before: %v", err)
	}
	if claimed != nil {
		t.Fatalf("ClaimJob before = job %d, want nil: it is not due yet", claimed.ID)
	}

	result, err := st.RunJobNow(ctx, core.JobRSSSync)
	if err != nil {
		t.Fatalf("RunJobNow: %v", err)
	}
	if result != RunNowAdvanced {
		t.Fatalf("RunJobNow = %q, want %q", result, RunNowAdvanced)
	}

	claimed, err = st.ClaimJob(ctx, []string{core.JobRSSSync}, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob after: %v", err)
	}
	if claimed == nil || claimed.ID != id {
		t.Fatalf("ClaimJob after = %+v, want the advanced job %d", claimed, id)
	}

	// No second row was created, and the other kind is still waiting.
	jobs, err := st.ListJobs(ctx, 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 2 {
		t.Fatalf("jobs = %d, want the two originals and no duplicate", len(jobs))
	}
	untouched, err := st.GetJob(ctx, other)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if untouched.RunAfter.IsZero() || untouched.State != core.JobStatePending {
		t.Errorf("other kind = %q at %v, want it left pending in the future",
			untouched.State, untouched.RunAfter)
	}
}

func TestRunJobNowConcurrentMissingChainCreatesOneOpenJob(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	type outcome struct {
		result RunNowResult
		err    error
	}
	start := make(chan struct{})
	outcomes := make(chan outcome, 2)
	var callers sync.WaitGroup
	for range 2 {
		callers.Add(1)
		go func() {
			defer callers.Done()
			<-start
			result, err := st.RunJobNow(ctx, core.JobRSSSync)
			outcomes <- outcome{result: result, err: err}
		}()
	}
	close(start)
	callers.Wait()
	close(outcomes)

	var enqueued, advanced int
	for outcome := range outcomes {
		if outcome.err != nil {
			t.Fatalf("RunJobNow: %v", outcome.err)
		}
		switch outcome.result {
		case RunNowEnqueued:
			enqueued++
		case RunNowAdvanced:
			advanced++
		default:
			t.Fatalf("RunJobNow result = %q, want enqueued or advanced", outcome.result)
		}
	}
	if enqueued != 1 || advanced != 1 {
		t.Fatalf("results = %d enqueued, %d advanced, want one of each", enqueued, advanced)
	}

	open, err := st.OpenJobsByKind(ctx, core.JobRSSSync)
	if err != nil {
		t.Fatalf("OpenJobsByKind: %v", err)
	}
	if len(open) != 1 {
		t.Fatalf("open jobs = %d, want one after concurrent Run now calls", len(open))
	}
	if open[0].State != core.JobStatePending || open[0].Payload != "{}" || !open[0].RunAfter.IsZero() {
		t.Fatalf("open job = %+v, want one immediate recurring row", open[0])
	}
}

// A running task cannot be pulled forward, even if a successor is already
// pending behind it.
func TestRunJobNowReportsRunning(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	id := enqueued(t, st, core.JobRSSSync, time.Time{})
	if _, err := st.ClaimJob(ctx, []string{core.JobRSSSync}, time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}
	// The successor the handler enqueues before finishing must remain scheduled
	// while the current generation is still running.
	successor := enqueued(t, st, core.JobRSSSync, time.Now().Add(15*time.Minute))

	result, err := st.RunJobNow(ctx, core.JobRSSSync)
	if err != nil {
		t.Fatalf("RunJobNow: %v", err)
	}
	if result != RunNowRunning {
		t.Fatalf("RunJobNow = %q, want %q", result, RunNowRunning)
	}
	waiting, err := st.GetJob(ctx, successor)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if waiting.RunAfter.IsZero() {
		t.Error("the successor was advanced while the job was running")
	}
	if _, err := st.GetJob(ctx, id); err != nil {
		t.Fatalf("GetJob running: %v", err)
	}
}

// The interval a task reports and the interval the scheduler sleeps for are the
// same read, so a nonsense settings row must land on the default rather than
// scheduling a job zero minutes — or a century — from now.
func TestIntervalMinutesFallsBackOnUnusableValues(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	if got := st.IntervalMinutes(ctx, SettingRSSSyncIntervalMinutes, DefaultRSSSyncIntervalMinutes); got != DefaultRSSSyncIntervalMinutes {
		t.Errorf("unset key = %d, want the default %d", got, DefaultRSSSyncIntervalMinutes)
	}
	for _, value := range []string{"", "soon", "0", "-5"} {
		if err := st.SetSetting(ctx, SettingRSSSyncIntervalMinutes, value); err != nil {
			t.Fatalf("SetSetting(%q): %v", value, err)
		}
		if got := st.IntervalMinutes(ctx, SettingRSSSyncIntervalMinutes, DefaultRSSSyncIntervalMinutes); got != DefaultRSSSyncIntervalMinutes {
			t.Errorf("value %q = %d, want the default %d", value, got, DefaultRSSSyncIntervalMinutes)
		}
	}
	if err := st.SetSetting(ctx, SettingRSSSyncIntervalMinutes, "45"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if got := st.IntervalMinutes(ctx, SettingRSSSyncIntervalMinutes, DefaultRSSSyncIntervalMinutes); got != 45 {
		t.Errorf("configured value = %d, want 45", got)
	}
}

func TestSetRecurringIntervalReschedulesPendingRun(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)
	job := core.Job{
		Kind:     core.JobRSSSync,
		Payload:  "{}",
		RunAfter: time.Now().Add(24 * time.Hour),
	}
	if err := st.EnqueueJob(ctx, &job); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}

	before := time.Now().UTC()
	if err := st.SetRecurringInterval(ctx, core.JobRSSSync, 45); err != nil {
		t.Fatalf("SetRecurringInterval: %v", err)
	}
	after := time.Now().UTC()

	if got := st.IntervalMinutes(ctx, SettingRSSSyncIntervalMinutes, DefaultRSSSyncIntervalMinutes); got != 45 {
		t.Fatalf("stored interval = %d, want 45", got)
	}
	rescheduled, err := st.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	earliest := before.Add(45 * time.Minute).Add(-time.Second)
	latest := after.Add(45 * time.Minute).Add(time.Second)
	if rescheduled.RunAfter.Before(earliest) || rescheduled.RunAfter.After(latest) {
		t.Fatalf("run_after = %s, want between %s and %s", rescheduled.RunAfter, earliest, latest)
	}
}

func TestSetRecurringIntervalRejectsUnknownKind(t *testing.T) {
	st, _ := openTemp(t)
	if err := st.SetRecurringInterval(context.Background(), "not-recurring", 30); err == nil {
		t.Fatal("unknown recurring kind was accepted")
	}
}
