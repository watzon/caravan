package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// Retry policy for failed jobs. The delay doubles per attempt from
// JobRetryBaseDelay up to JobRetryMaxDelay; after JobMaxAttempts the job stops
// retrying and parks in JobStateFailed, visible in the activity feed rather
// than silently spinning.
const (
	JobRetryBaseDelay = 30 * time.Second
	JobRetryMaxDelay  = 30 * time.Minute
	JobMaxAttempts    = 5
)

const jobColumns = `id, kind, payload, state, attempts, run_after, lease_expires_at,
	last_error, created_at, updated_at`

// EnqueueJob appends a job to the queue and writes back the assigned ID.
// A zero RunAfter means "eligible immediately".
func (s *Store) EnqueueJob(ctx context.Context, j *core.Job) error {
	ts := now()
	if j.CreatedAt.IsZero() {
		j.CreatedAt = ts
	}
	j.UpdatedAt = ts
	j.State = core.JobStatePending

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO jobs (kind, payload, state, attempts, run_after, lease_expires_at,
			last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, '', '', ?, ?)`,
		j.Kind, j.Payload, j.State, j.Attempts, formatTime(j.RunAfter),
		formatTime(j.CreatedAt), formatTime(j.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: enqueue %s job: %w", j.Kind, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: enqueue %s job: %w", j.Kind, err)
	}
	j.ID = id
	return nil
}

// ClaimJob takes the oldest eligible job of one of the given kinds and leases
// it for the given duration. An empty kinds slice claims any kind.
//
// It returns (nil, nil) when nothing is eligible: an idle queue is the normal
// case for a poller, not an error. The claim is a single transaction, so two
// workers can never hold the same job — and because a lease can expire and be
// reclaimed (SPEC §7), delivery is at-least-once and every handler must be
// idempotent.
func (s *Store) ClaimJob(ctx context.Context, kinds []string, lease time.Duration) (*core.Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("store: claim job: %w", err)
	}
	defer tx.Rollback()

	ts := now()
	stamp := formatTime(ts)
	args := []any{core.JobStatePending, stamp}
	where := "state = ? AND (run_after = '' OR run_after <= ?)"
	if len(kinds) > 0 {
		where += " AND kind IN (" + placeholders(len(kinds)) + ")"
		for _, k := range kinds {
			args = append(args, k)
		}
	}

	row := tx.QueryRowContext(ctx,
		"SELECT "+jobColumns+" FROM jobs WHERE "+where+" ORDER BY run_after, id LIMIT 1", args...)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: claim job: %w", err)
	}

	j.State = core.JobStateRunning
	j.LeaseExpiresAt = ts.Add(lease)
	j.UpdatedAt = ts
	if _, err := tx.ExecContext(ctx,
		"UPDATE jobs SET state = ?, lease_expires_at = ?, updated_at = ? WHERE id = ?",
		j.State, formatTime(j.LeaseExpiresAt), stamp, j.ID); err != nil {
		return nil, fmt.Errorf("store: claim job %d: %w", j.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("store: claim job %d: %w", j.ID, err)
	}
	return j, nil
}

// CompleteJob marks a claimed job done and drops its lease. Completing an
// absent job is ErrNotFound.
func (s *Store) CompleteJob(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET state = ?, lease_expires_at = '', last_error = '', updated_at = ?
		WHERE id = ?`, core.JobStateDone, formatTime(now()), id)
	if err != nil {
		return fmt.Errorf("store: complete job %d: %w", id, err)
	}
	return affectedOne(res, "complete job", id)
}

// FailJob records a failed attempt. The job goes back to pending with an
// exponentially backed-off run_after until JobMaxAttempts is reached, after
// which it parks in JobStateFailed. Failing an absent job is ErrNotFound.
func (s *Store) FailJob(ctx context.Context, id int64, reason string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: fail job %d: %w", id, err)
	}
	defer tx.Rollback()

	var attempts int
	err = tx.QueryRowContext(ctx, "SELECT attempts FROM jobs WHERE id = ?", id).Scan(&attempts)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: fail job %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return fmt.Errorf("store: fail job %d: %w", id, err)
	}

	attempts++
	state := core.JobStatePending
	runAfter := now().Add(RetryDelay(attempts))
	if attempts >= JobMaxAttempts {
		state = core.JobStateFailed
		runAfter = time.Time{}
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE jobs SET state = ?, attempts = ?, run_after = ?, lease_expires_at = '',
			last_error = ?, updated_at = ?
		WHERE id = ?`,
		state, attempts, formatTime(runAfter), reason, formatTime(now()), id); err != nil {
		return fmt.Errorf("store: fail job %d: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: fail job %d: %w", id, err)
	}
	return nil
}

// ReclaimExpired returns every running job whose lease has expired to the
// pending pool and reports how many it moved. Workers die: a crash mid-job
// (SPEC §7) leaves a lease nobody will ever renew, and this is what unsticks
// it at startup and on a periodic sweep.
//
// Attempts are not incremented here: an expired lease is not evidence the job
// itself is bad, only that the worker went away.
func (s *Store) ReclaimExpired(ctx context.Context) (int, error) {
	res, err := s.db.ExecContext(ctx, `
		UPDATE jobs SET state = ?, lease_expires_at = '', updated_at = ?
		WHERE state = ? AND lease_expires_at <> '' AND lease_expires_at <= ?`,
		core.JobStatePending, formatTime(now()), core.JobStateRunning, formatTime(now()))
	if err != nil {
		return 0, fmt.Errorf("store: reclaim expired jobs: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("store: reclaim expired jobs: %w", err)
	}
	return int(n), nil
}

// ListJobs returns the most recent jobs, newest first, for the activity
// feed (PLAN phase 3, task 8). A limit of zero or less returns every job.
func (s *Store) ListJobs(ctx context.Context, limit int) ([]core.Job, error) {
	query := "SELECT " + jobColumns + " FROM jobs ORDER BY id DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list jobs: %w", err)
	}
	defer rows.Close()

	out := []core.Job{}
	for rows.Next() {
		j, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan job: %w", err)
		}
		out = append(out, *j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list jobs: %w", err)
	}
	return out, nil
}

// HasOpenJob reports whether a job of the given kind and payload is already
// pending or running. Recurring jobs (RSS sync, backlog sweeps) use it to
// stay singletons: a redelivered tick enqueues nothing when the work is
// already queued, which is what keeps a restart from stacking duplicates
// (PLAN phase 3, task 5).
func (s *Store) HasOpenJob(ctx context.Context, kind, payload string) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM jobs
		WHERE kind = ? AND payload = ? AND state IN (?, ?)`,
		kind, payload, core.JobStatePending, core.JobStateRunning).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("store: has open %s job: %w", kind, err)
	}
	return n > 0, nil
}

// GetJob returns the job with the given id, or ErrNotFound.
func (s *Store) GetJob(ctx context.Context, id int64) (*core.Job, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+jobColumns+" FROM jobs WHERE id = ?", id)
	j, err := scanJob(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: job %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get job %d: %w", id, err)
	}
	return j, nil
}

// RetryDelay is the backoff before attempt number n (1-based) is retried:
// JobRetryBaseDelay doubled per previous attempt, capped at JobRetryMaxDelay.
func RetryDelay(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := JobRetryBaseDelay
	for range attempts - 1 {
		delay *= 2
		if delay >= JobRetryMaxDelay {
			return JobRetryMaxDelay
		}
	}
	return delay
}

// placeholders renders `?, ?, ?` for an IN clause of n values.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?, ", n), ", ")
}

// affectedOne turns a zero-row update into ErrNotFound.
func affectedOne(res sql.Result, what string, id int64) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: %s %d: %w", what, id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: %s %d: %w", what, id, ErrNotFound)
	}
	return nil
}

func scanJob(sc scanner) (*core.Job, error) {
	var (
		j              core.Job
		runAfter       string
		leaseExpiresAt string
		createdAt      string
		updatedAt      string
	)
	err := sc.Scan(&j.ID, &j.Kind, &j.Payload, &j.State, &j.Attempts, &runAfter,
		&leaseExpiresAt, &j.LastError, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	j.RunAfter = parseTime(runAfter)
	j.LeaseExpiresAt = parseTime(leaseExpiresAt)
	j.CreatedAt = parseTime(createdAt)
	j.UpdatedAt = parseTime(updatedAt)
	return &j, nil
}
