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
	// DefaultRecycleCleanupIntervalMinutes removes expired recycle batches daily.
	DefaultRecycleCleanupIntervalMinutes = 1440
	// DefaultNotificationIntervalMinutes sends selected events within five minutes.
	DefaultNotificationIntervalMinutes = 5
)

// RecurringInterval says where one recurring kind's cadence is configured: the
// settings key that holds it, and what it means when that key is unset.
type RecurringInterval struct {
	Key            string
	DefaultMinutes int
}

// recurringKinds is the order recurring jobs are bootstrapped and listed in —
// most frequent first, which is also how a user reads them.
var recurringKinds = []string{core.JobRSSSync, core.JobNotificationDispatch, core.JobBacklogSweep, core.JobRefreshMetadata, core.JobRecycleCleanup}

var recurringIntervals = map[string]RecurringInterval{
	core.JobRSSSync:              {SettingRSSSyncIntervalMinutes, DefaultRSSSyncIntervalMinutes},
	core.JobBacklogSweep:         {SettingBacklogIntervalMinutes, DefaultBacklogIntervalMinutes},
	core.JobRefreshMetadata:      {SettingRefreshIntervalMinutes, DefaultRefreshIntervalMinutes},
	core.JobRecycleCleanup:       {SettingRecycleCleanupIntervalMinutes, DefaultRecycleCleanupIntervalMinutes},
	core.JobNotificationDispatch: {SettingNotificationIntervalMinutes, DefaultNotificationIntervalMinutes},
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

// recurringIntervalMinutesTx reads one recurring cadence within a write
// transaction. Failure handling matches IntervalMinutes: an absent, malformed,
// or unsafe setting falls back to the built-in cadence.
func (s *Store) recurringIntervalMinutesTx(ctx context.Context, tx *sql.Tx, kind string) (int, bool, error) {
	interval, recurring := RecurringIntervalFor(kind)
	if !recurring {
		return 0, false, nil
	}

	var value string
	err := tx.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = ?", interval.Key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return interval.DefaultMinutes, true, nil
	}
	if err != nil {
		return 0, true, err
	}
	minutes, err := strconv.Atoi(value)
	if err != nil || minutes <= 0 || int64(minutes) > int64(^uint64(0)>>1)/int64(time.Minute) {
		return interval.DefaultMinutes, true, nil
	}
	return minutes, true, nil
}

// SetRecurringInterval changes a recurring kind's cadence and reschedules any
// pending run from now. A running job is not interrupted; its successor will
// use the stored interval when the handler schedules it.
func (s *Store) SetRecurringInterval(ctx context.Context, kind string, minutes int) error {
	interval, ok := RecurringIntervalFor(kind)
	if !ok {
		return fmt.Errorf("store: %q is not a recurring job kind", kind)
	}
	if minutes <= 0 {
		return fmt.Errorf("store: recurring interval must be positive")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: set %s interval: %w", kind, err)
	}
	defer tx.Rollback()

	updated := now()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at`,
		interval.Key, strconv.Itoa(minutes), formatTime(updated)); err != nil {
		return fmt.Errorf("store: set %s interval: %w", kind, err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE jobs SET run_after = ?, updated_at = ?
		WHERE kind = ? AND state = ?`,
		formatTime(updated.Add(time.Duration(minutes)*time.Minute)), formatTime(updated),
		kind, core.JobStatePending); err != nil {
		return fmt.Errorf("store: reschedule %s: %w", kind, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: set %s interval: %w", kind, err)
	}
	return nil
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

// RunNowResult says what RunJobNow did.
type RunNowResult string

const (
	// RunNowAdvanced means a pending row is now eligible immediately.
	RunNowAdvanced RunNowResult = "advanced"
	// RunNowEnqueued means a broken recurring chain was restored with a new row
	// that is eligible immediately.
	RunNowEnqueued RunNowResult = "enqueued"
	// RunNowRunning means the kind is already running, so there was nothing to
	// pull forward, a polite no-op, not a failure.
	RunNowRunning RunNowResult = "running"
)

// RunJobNow makes a recurring kind's next run due now. It moves the earliest
// pending row forward, leaves a running row alone, or atomically creates an
// immediate row when the chain is missing.
//
// The missing-chain insert belongs in this transaction: two Run now requests
// cannot both observe no open row and enqueue separate successors.
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
		ts := now()
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO jobs (kind, payload, state, attempts, run_after, lease_expires_at,
				last_error, created_at, updated_at)
			VALUES (?, '{}', ?, 0, '', '', '', ?, ?)`,
			kind, core.JobStatePending, formatTime(ts), formatTime(ts)); err != nil {
			return "", fmt.Errorf("store: run %s now: %w", kind, err)
		}
		if err := tx.Commit(); err != nil {
			return "", fmt.Errorf("store: run %s now: %w", kind, err)
		}
		return RunNowEnqueued, nil
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
