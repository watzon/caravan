package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// This file owns the recurring half of the job queue: which kinds run on a
// timer, how often, where they are in their cycle, and how to pull the next
// run forward. The scheduler in internal/automation and the Tasks screen in
// the API both read the cadence from here, so there is exactly one answer to
// "how often does the RSS sync run" and no chance of the settings screen and
// the scheduler disagreeing.

// The cadences a fresh install runs on, in minutes.
const (
	// DefaultRSSSyncIntervalMinutes polls enabled indexers every quarter hour.
	DefaultRSSSyncIntervalMinutes = 15
	// DefaultBacklogIntervalMinutes sweeps the wanted list every six hours.
	DefaultBacklogIntervalMinutes = 360
	// DefaultRefreshIntervalMinutes is twelve hours, Radarr and Sonarr's
	// cadence: a release date or a series status changes on the scale of days,
	// and every sweep is one provider round trip per movie plus one per season.
	DefaultRefreshIntervalMinutes = 720
)

// RecurringInterval says where one recurring kind's cadence is configured: the
// settings key that holds it, and what it means when that key is unset.
type RecurringInterval struct {
	Key            string
	DefaultMinutes int
}

// recurringKinds is the order recurring jobs are bootstrapped and listed in —
// most frequent first, which is also how a user reads them.
var recurringKinds = []string{core.JobRSSSync, core.JobBacklogSweep, core.JobRefreshMetadata}

var recurringIntervals = map[string]RecurringInterval{
	core.JobRSSSync:         {SettingRSSSyncIntervalMinutes, DefaultRSSSyncIntervalMinutes},
	core.JobBacklogSweep:    {SettingBacklogIntervalMinutes, DefaultBacklogIntervalMinutes},
	core.JobRefreshMetadata: {SettingRefreshIntervalMinutes, DefaultRefreshIntervalMinutes},
}

// RecurringKinds returns every job kind that reschedules itself, in display
// order. The slice is a copy, so a caller may sort or filter it.
func RecurringKinds() []string {
	return append([]string(nil), recurringKinds...)
}

// RecurringIntervalFor reports how often a kind runs. The second result is
// false for a kind that does not recur, which is what makes this the one place
// that decides whether a job kind belongs on the Tasks screen at all.
func RecurringIntervalFor(kind string) (RecurringInterval, bool) {
	interval, ok := recurringIntervals[kind]
	return interval, ok
}

// IntervalMinutes reads a positive minute count from the settings table,
// falling back when the key is unset, unreadable, or not a sane duration. An
// interval is scheduling policy, not library data: a hand-edited row must slow
// nothing down and stop nothing, so every failure lands on the default.
func (s *Store) IntervalMinutes(ctx context.Context, key string, fallback int) int {
	value, err := s.GetSetting(ctx, key)
	if err != nil {
		return fallback
	}
	minutes, err := strconv.Atoi(value)
	if err != nil || minutes <= 0 || int64(minutes) > int64(^uint64(0)>>1)/int64(time.Minute) {
		return fallback
	}
	return minutes
}

// JobStatus is one kind's place in the queue: the run that finished most
// recently, and the row that is still open. Either may be nil — a kind that
// has never completed a cycle has no finished row, and a kind whose chain was
// broken (a stopped runner, a fresh database) has no open one.
type JobStatus struct {
	Kind string
	// LastFinished is the newest done or failed row. Its UpdatedAt is when the
	// run ended and its LastError is why, when it failed.
	LastFinished *core.Job
	// Open is the pending successor waiting on its RunAfter, or the row that is
	// running right now — the running one wins when both exist.
	Open *core.Job
}

// RecurringJobStatus reports the queue state of each kind, in the order given.
// It is what the Tasks screen is: last run, its result, and when the next one
// is due, for every job that runs on a timer.
func (s *Store) RecurringJobStatus(ctx context.Context, kinds []string) ([]JobStatus, error) {
	out := make([]JobStatus, 0, len(kinds))
	for _, kind := range kinds {
		finished, err := s.queryJob(ctx, "SELECT "+jobColumns+` FROM jobs
			WHERE kind = ? AND state IN (?, ?)
			ORDER BY updated_at DESC, id DESC LIMIT 1`,
			kind, core.JobStateDone, core.JobStateFailed)
		if err != nil {
			return nil, fmt.Errorf("store: latest finished %s job: %w", kind, err)
		}
		// A running row outranks a pending one: it is what is happening now,
		// and the pending successor does not exist until it completes. Among
		// pending rows the earliest run_after is next, and the empty string
		// sorts first for the same reason ClaimJob treats it as "eligible now".
		open, err := s.queryJob(ctx, "SELECT "+jobColumns+` FROM jobs
			WHERE kind = ? AND state IN (?, ?)
			ORDER BY (state = ?) DESC, run_after, id LIMIT 1`,
			kind, core.JobStatePending, core.JobStateRunning, core.JobStateRunning)
		if err != nil {
			return nil, fmt.Errorf("store: open %s job: %w", kind, err)
		}
		out = append(out, JobStatus{Kind: kind, LastFinished: finished, Open: open})
	}
	return out, nil
}

// RunNowResult says what RunJobNow found to do.
type RunNowResult string

const (
	// RunNowAdvanced means a pending row is now eligible immediately.
	RunNowAdvanced RunNowResult = "advanced"
	// RunNowRunning means the kind is already running, so there was nothing to
	// pull forward — a polite no-op, not a failure.
	RunNowRunning RunNowResult = "running"
	// RunNowNoOpenJob means the kind has no pending or running row at all. The
	// self-scheduling chain keeps one open row per kind at all times, so this
	// only happens with a stopped runner or a database that has never had one.
	RunNowNoOpenJob RunNowResult = "no_open_job"
)

// RunJobNow makes a recurring kind's next run due now by clearing the run_after
// of its earliest pending row, so the next poll claims it.
//
// It deliberately does not enqueue: the recurring chain keeps exactly one open
// row per kind, each successor scheduled a full interval ahead, and adding a
// second row would either be rejected by the HasOpenJob guard or double every
// future cycle. Moving the row that already exists is what "run it now" means.
func (s *Store) RunJobNow(ctx context.Context, kind string) (RunNowResult, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("store: run %s now: %w", kind, err)
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRowContext(ctx,
		"SELECT id FROM jobs WHERE kind = ? AND state = ? LIMIT 1",
		kind, core.JobStateRunning).Scan(&id)
	if err == nil {
		return RunNowRunning, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("store: run %s now: %w", kind, err)
	}

	err = tx.QueryRowContext(ctx,
		"SELECT id FROM jobs WHERE kind = ? AND state = ? ORDER BY run_after, id LIMIT 1",
		kind, core.JobStatePending).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return RunNowNoOpenJob, nil
	}
	if err != nil {
		return "", fmt.Errorf("store: run %s now: %w", kind, err)
	}

	// The empty string is the schema's "eligible immediately" (0001_init), and
	// is what ClaimJob's `run_after = '' OR run_after <= ?` reads as due.
	if _, err := tx.ExecContext(ctx,
		"UPDATE jobs SET run_after = '', updated_at = ? WHERE id = ?",
		formatTime(now()), id); err != nil {
		return "", fmt.Errorf("store: run %s now: %w", kind, err)
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("store: run %s now: %w", kind, err)
	}
	return RunNowAdvanced, nil
}

// queryJob runs a single-row job query. No matching row is (nil, nil): "there
// has never been one" is an answer, not a failure.
func (s *Store) queryJob(ctx context.Context, query string, args ...any) (*core.Job, error) {
	j, err := scanJob(s.db.QueryRowContext(ctx, query, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return j, nil
}
