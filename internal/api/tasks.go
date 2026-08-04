package api

import (
	"net/http"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// The Tasks screen: the recurring background jobs, when each last ran, when the
// next one is due, and a way to bring that forward.
//
// It reads the same interval settings and the same job rows the scheduler in
// internal/automation writes (store.RecurringIntervalFor), so what the screen
// says and what the queue does cannot drift.

// The result strings GET /system/tasks reports for the last run. The empty
// string is the third case: nothing has finished yet.
const (
	taskResultOK     = "ok"
	taskResultFailed = "failed"
)

// taskCopy is the human name and one-line explanation of each recurring kind.
// It is server-side because the kinds are: the SPA renders whatever the API
// lists, so a job kind added to the queue reaches the screen without a matching
// SPA release.
var taskCopy = map[string]struct{ name, description string }{
	core.JobRSSSync: {
		"RSS sync",
		"Checks indexer feeds for newly posted releases.",
	},
	core.JobBacklogSweep: {
		"Backlog search",
		"Searches indexers for everything on the wanted list.",
	},
	core.JobRefreshMetadata: {
		"Metadata refresh",
		"Updates titles, statuses, new seasons and scenes.",
	},
}

type taskJSON struct {
	Kind            string `json:"kind"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	IntervalMinutes int    `json:"interval_minutes"`
	// LastRun is when the most recent run finished, empty when none has.
	LastRun string `json:"last_run"`
	// LastResult is "ok", "failed", or empty when the task has never finished.
	LastResult string `json:"last_result"`
	// LastError is why the last run failed. Empty unless LastResult is failed.
	LastError string `json:"last_error"`
	// NextRun is when the pending successor comes due. Empty means either it is
	// due now or there is nothing queued — Queued tells those apart.
	NextRun string `json:"next_run"`
	// Running is true while the task is being worked on right now.
	Running bool `json:"running"`
	// Queued is true when a pending or running row exists. False means the
	// self-scheduling chain is broken, which a run-now repairs.
	Queued bool `json:"queued"`
}

// handleListTasks answers GET /system/tasks.
func (s *server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	statuses, err := s.st.RecurringJobStatus(ctx, store.RecurringKinds())
	if err != nil {
		s.writeStoreError(w, "list tasks", err)
		return
	}

	out := make([]taskJSON, 0, len(statuses))
	for _, status := range statuses {
		text := taskCopy[status.Kind]
		interval, _ := store.RecurringIntervalFor(status.Kind)
		task := taskJSON{
			Kind:            status.Kind,
			Name:            text.name,
			Description:     text.description,
			IntervalMinutes: s.st.IntervalMinutes(ctx, interval.Key, interval.DefaultMinutes),
		}
		if job := status.LastFinished; job != nil {
			task.LastRun = jsonTime(job.UpdatedAt)
			task.LastResult = taskResultOK
			if job.State == core.JobStateFailed {
				task.LastResult = taskResultFailed
				task.LastError = job.LastError
			}
		}
		if job := status.Open; job != nil {
			task.Queued = true
			task.Running = job.State == core.JobStateRunning
			// A running job's run_after is in the past by definition; reporting
			// it as the next run would read as a schedule, not as "now".
			if !task.Running {
				task.NextRun = jsonTime(job.RunAfter)
			}
		}
		out = append(out, task)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

// runTaskResponse is what POST /system/tasks/{kind}/run answers. A task that
// was already running is not an error — there is nothing to bring forward
// because the work is happening — so it is a 200 that says so.
type runTaskResponse struct {
	Kind           string `json:"kind"`
	AlreadyRunning bool   `json:"already_running"`
}

// handleRunTask answers POST /system/tasks/{kind}/run.
func (s *server) handleRunTask(w http.ResponseWriter, r *http.Request) {
	kind := r.PathValue("kind")
	if _, ok := store.RecurringIntervalFor(kind); !ok {
		writeError(w, http.StatusNotFound, "unknown task")
		return
	}

	result, err := s.st.RunJobNow(r.Context(), kind)
	if err != nil {
		s.writeStoreError(w, "run task", err)
		return
	}
	// No open row at all means the self-scheduling chain was broken — a runner
	// that was stopped mid-cycle, or a database that has never had one. The
	// button still has to work, and enqueueing here restarts the chain rather
	// than making the user wait for the next process start to bootstrap it.
	// RunJobNow established there is no open row inside a transaction, so this
	// needs no second HasOpenJob guard.
	if result == store.RunNowNoOpenJob {
		if err := s.st.EnqueueJob(r.Context(), &core.Job{Kind: kind, Payload: "{}"}); err != nil {
			s.writeStoreError(w, "run task", err)
			return
		}
	}
	writeJSON(w, http.StatusOK, runTaskResponse{
		Kind:           kind,
		AlreadyRunning: result == store.RunNowRunning,
	})
}
