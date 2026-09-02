package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// listTasks reads GET /system/tasks into a map keyed by kind.
func listTasks(t *testing.T, h http.Handler) map[string]taskJSON {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/api/v1/system/tasks", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Tasks []taskJSON `json:"tasks"`
	}
	decodeBody(t, rec, &body)
	if len(body.Tasks) != len(store.RecurringKinds()) {
		t.Fatalf("tasks = %d, want one per recurring kind", len(body.Tasks))
	}
	out := map[string]taskJSON{}
	for _, task := range body.Tasks {
		out[task.Kind] = task
	}
	return out
}

// The screen's whole job: a finished run with its result, and the open
// successor that says when the next one is due, read off the same rows the
// scheduler wrote.
func TestSystemTasksReportTheLastRunAndTheNextOne(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	// One completed RSS cycle, then the successor a handler would enqueue.
	finished := core.Job{Kind: core.JobRSSSync, Payload: "{}"}
	if err := st.EnqueueJob(ctx, &finished); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := st.ClaimJob(ctx, []string{core.JobRSSSync}, time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := st.CompleteJob(ctx, finished.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}
	next := time.Now().Add(15 * time.Minute).UTC().Truncate(time.Second)
	if err := st.EnqueueJob(ctx, &core.Job{Kind: core.JobRSSSync, Payload: "{}", RunAfter: next}); err != nil {
		t.Fatalf("enqueue successor: %v", err)
	}
	// A configured interval is reported, not the default.
	if err := st.SetSetting(ctx, store.SettingRSSSyncIntervalMinutes, "30"); err != nil {
		t.Fatalf("set interval: %v", err)
	}

	tasks := listTasks(t, h)

	rss := tasks[core.JobRSSSync]
	if rss.Name == "" || rss.Description == "" {
		t.Errorf("rss_sync copy = %q/%q, want server-authored name and description",
			rss.Name, rss.Description)
	}
	if rss.IntervalMinutes != 30 {
		t.Errorf("interval_minutes = %d, want the configured 30", rss.IntervalMinutes)
	}
	if rss.LastResult != taskResultOK || rss.LastRun == "" || rss.LastError != "" {
		t.Errorf("last run = %q at %q (%q), want a clean ok", rss.LastResult, rss.LastRun, rss.LastError)
	}
	if !rss.Queued || rss.Running {
		t.Errorf("queued/running = %v/%v, want a queued task that is not running", rss.Queued, rss.Running)
	}
	if got, want := rss.NextRun, next.Format(time.RFC3339); got != want {
		t.Errorf("next_run = %q, want %q", got, want)
	}

	// A kind that has never run says so rather than inventing a last run.
	sweep := tasks[core.JobBacklogSweep]
	if sweep.LastRun != "" || sweep.LastResult != "" {
		t.Errorf("backlog last run = %q/%q, want empty: it has never run", sweep.LastRun, sweep.LastResult)
	}
	if sweep.Queued {
		t.Errorf("backlog queued = true, want false: nothing is enqueued for it")
	}
	if sweep.IntervalMinutes != store.DefaultBacklogIntervalMinutes {
		t.Errorf("backlog interval = %d, want the default %d",
			sweep.IntervalMinutes, store.DefaultBacklogIntervalMinutes)
	}
}

// A failure has to reach the screen with its reason: "last run failed" with no
// "why" sends the user to the logs, which is what this screen exists to avoid.
func TestSystemTasksReportAFailedRunWithItsReason(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	job := core.Job{Kind: core.JobRefreshMetadata, Payload: "{}"}
	if err := st.EnqueueJob(ctx, &job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	for range store.JobMaxAttempts {
		if err := st.FailJob(ctx, job.ID, "tmdb unreachable"); err != nil {
			t.Fatalf("fail: %v", err)
		}
	}

	task := listTasks(t, h)[core.JobRefreshMetadata]
	if task.LastResult != taskResultFailed || task.LastError != "tmdb unreachable" {
		t.Errorf("last result = %q (%q), want failed with its reason", task.LastResult, task.LastError)
	}
	if task.LastRun == "" {
		t.Error("last_run is empty for a run that finished, badly")
	}
}

// The button's contract: after it is pressed, the queue hands the job over.
func TestRunTaskMakesThePendingJobDue(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	job := core.Job{Kind: core.JobRSSSync, Payload: "{}", RunAfter: time.Now().Add(time.Hour)}
	if err := st.EnqueueJob(ctx, &job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/system/tasks/rss_sync/run", "")
	wantStatus(t, rec, http.StatusOK)
	var body runTaskResponse
	decodeBody(t, rec, &body)
	if body.Kind != core.JobRSSSync || body.AlreadyRunning {
		t.Errorf("response = %+v, want the kind and already_running false", body)
	}

	claimed, err := st.ClaimJob(ctx, []string{core.JobRSSSync}, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if claimed == nil || claimed.ID != job.ID {
		t.Fatalf("ClaimJob = %+v, want the advanced job %d", claimed, job.ID)
	}
}

// With no open row (a stopped runner, or a database that never bootstrapped)
// the button still has to start the task rather than answer "nothing to do".
func TestRunTaskRestartsAChainWithNoOpenRow(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	rec := do(t, h, http.MethodPost, "/api/v1/system/tasks/backlog_sweep/run", "")
	wantStatus(t, rec, http.StatusOK)

	claimed, err := st.ClaimJob(ctx, []string{core.JobBacklogSweep}, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if claimed == nil {
		t.Fatal("ClaimJob = nil, want the job the run button enqueued")
	}
	if claimed.Payload != "{}" {
		t.Errorf("payload = %q, want the recurring payload {}", claimed.Payload)
	}
}

// Already running is not a failure and not a lie: nothing is brought forward,
// and the answer says why so the UI can tell the user rather than claim success.
func TestRunTaskReportsAnAlreadyRunningTask(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	job := core.Job{Kind: core.JobRSSSync, Payload: "{}"}
	if err := st.EnqueueJob(ctx, &job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if _, err := st.ClaimJob(ctx, []string{core.JobRSSSync}, time.Minute); err != nil {
		t.Fatalf("claim: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/system/tasks/rss_sync/run", "")
	wantStatus(t, rec, http.StatusOK)
	var body runTaskResponse
	decodeBody(t, rec, &body)
	if !body.AlreadyRunning {
		t.Errorf("already_running = false, want true while the job is leased")
	}

	// And the screen agrees with the answer.
	if task := listTasks(t, h)[core.JobRSSSync]; !task.Running || task.NextRun != "" {
		t.Errorf("task = %+v, want running with no next run", task)
	}

	jobs, err := st.ListJobs(ctx, 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if len(jobs) != 1 {
		t.Errorf("jobs = %d, want no duplicate enqueued behind the running one", len(jobs))
	}
}

// A kind that is not on a timer has no task to run, however real a job kind it
// is elsewhere in the queue.
func TestRunTaskRejectsKindsThatDoNotRecur(t *testing.T) {
	h, _, _ := newTestServer(t)
	for _, kind := range []string{"search_movie", "not_a_kind"} {
		rec := do(t, h, http.MethodPost, "/api/v1/system/tasks/"+kind+"/run", "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("POST run %s = %d, want 404", kind, rec.Code)
		}
	}
}

func TestUpdateTaskIntervalPersistsAndReschedules(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	job := core.Job{Kind: core.JobRSSSync, Payload: "{}", RunAfter: time.Now().Add(24 * time.Hour)}
	if err := st.EnqueueJob(ctx, &job); err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	rec := do(t, h, http.MethodPut, "/api/v1/system/tasks/rss_sync", `{"interval_minutes":45}`)
	wantStatus(t, rec, http.StatusOK)
	var body updateTaskIntervalResponse
	decodeBody(t, rec, &body)
	if body.Kind != core.JobRSSSync || body.IntervalMinutes != 45 {
		t.Fatalf("response = %+v, want rss_sync at 45 minutes", body)
	}
	if task := listTasks(t, h)[core.JobRSSSync]; task.IntervalMinutes != 45 {
		t.Fatalf("task interval = %d, want 45", task.IntervalMinutes)
	}
	updated, err := st.GetJob(ctx, job.ID)
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if updated.RunAfter.After(time.Now().Add(46 * time.Minute)) {
		t.Fatalf("next run = %s, want it rescheduled from now", updated.RunAfter)
	}
}

func TestUpdateTaskIntervalValidatesKindAndBounds(t *testing.T) {
	h, _, _ := newTestServer(t)
	for _, tc := range []struct {
		target string
		body   string
		status int
	}{
		{"/api/v1/system/tasks/search_movie", `{"interval_minutes":30}`, http.StatusNotFound},
		{"/api/v1/system/tasks/rss_sync", `{"interval_minutes":4}`, http.StatusBadRequest},
		{"/api/v1/system/tasks/rss_sync", `{"interval_minutes":43201}`, http.StatusBadRequest},
	} {
		rec := do(t, h, http.MethodPut, tc.target, tc.body)
		wantStatus(t, rec, tc.status)
	}
}

// The Tasks screen is an admin's, like the rest of Settings: a member is turned
// away by the gate, never shown the queue or handed the button.
func TestTaskEndpointsAreAdminOnly(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	createUser(t, st, testMember, testPassword, core.RoleMember)
	member := login(t, h, testMember, testPassword)

	for _, tc := range []struct{ method, target string }{
		{http.MethodGet, "/api/v1/system/tasks"},
		{http.MethodPost, "/api/v1/system/tasks/rss_sync/run"},
		{http.MethodPut, "/api/v1/system/tasks/rss_sync"},
	} {
		rec := doAuth(t, h, tc.method, tc.target, "", withCookie(member))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403 for a member", tc.method, tc.target, rec.Code)
		}
	}

	admin := login(t, h, testAdmin, testPassword)
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/system/tasks", "", withCookie(admin)), http.StatusOK)
}
