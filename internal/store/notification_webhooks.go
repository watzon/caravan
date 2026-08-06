package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const notificationWebhookColumns = `id, name, url, on_grab, on_import, on_health, enabled, last_event_id, created_at, updated_at`

// CreateNotificationWebhook inserts w at the current event cursor. Events that
// existed before the webhook was configured are not delivered retroactively.
func (s *Store) CreateNotificationWebhook(ctx context.Context, w *core.NotificationWebhook) error {
	ts := formatTime(now())
	res, err := s.db.ExecContext(ctx, `
		INSERT INTO notification_webhooks (
			name, url, on_grab, on_import, on_health, enabled, last_event_id, created_at, updated_at
		)
		SELECT ?, ?, ?, ?, ?, ?, COALESCE(MAX(id), 0), ?, ? FROM events`,
		w.Name, w.URL, w.OnGrab, w.OnImport, w.OnHealth, w.Enabled, ts, ts)
	if err != nil {
		return fmt.Errorf("store: create notification webhook %q: %w", w.Name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: create notification webhook %q: %w", w.Name, err)
	}
	created, err := s.GetNotificationWebhook(ctx, id)
	if err != nil {
		return fmt.Errorf("store: read created notification webhook: %w", err)
	}
	*w = *created
	return nil
}

// UpdateNotificationWebhook replaces a webhook's configurable fields. Its
// identity, delivery cursor, and creation time remain unchanged.
func (s *Store) UpdateNotificationWebhook(ctx context.Context, w *core.NotificationWebhook) error {
	ts := formatTime(now())
	res, err := s.db.ExecContext(ctx, `
		UPDATE notification_webhooks
		SET name = ?, url = ?, on_grab = ?, on_import = ?, on_health = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		w.Name, w.URL, w.OnGrab, w.OnImport, w.OnHealth, w.Enabled, ts, w.ID)
	if err != nil {
		return fmt.Errorf("store: update notification webhook %d: %w", w.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: update notification webhook %d: %w", w.ID, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	updated, err := s.GetNotificationWebhook(ctx, w.ID)
	if err != nil {
		return fmt.Errorf("store: read updated notification webhook: %w", err)
	}
	*w = *updated
	return nil
}

// GetNotificationWebhook returns one webhook, or ErrNotFound.
func (s *Store) GetNotificationWebhook(ctx context.Context, id int64) (*core.NotificationWebhook, error) {
	w, err := scanNotificationWebhook(s.db.QueryRowContext(ctx,
		"SELECT "+notificationWebhookColumns+" FROM notification_webhooks WHERE id = ?", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get notification webhook %d: %w", id, err)
	}
	return w, nil
}

// ListNotificationWebhooks returns every webhook in stable name and ID order.
func (s *Store) ListNotificationWebhooks(ctx context.Context) ([]core.NotificationWebhook, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+notificationWebhookColumns+" FROM notification_webhooks ORDER BY name, id")
	if err != nil {
		return nil, fmt.Errorf("store: list notification webhooks: %w", err)
	}
	defer rows.Close()

	webhooks := []core.NotificationWebhook{}
	for rows.Next() {
		w, err := scanNotificationWebhook(rows)
		if err != nil {
			return nil, fmt.Errorf("store: list notification webhooks: %w", err)
		}
		webhooks = append(webhooks, *w)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list notification webhooks: %w", err)
	}
	return webhooks, nil
}

// DeleteNotificationWebhook removes a webhook. A missing row is already gone.
func (s *Store) DeleteNotificationWebhook(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM notification_webhooks WHERE id = ?", id); err != nil {
		return fmt.Errorf("store: delete notification webhook %d: %w", id, err)
	}
	return nil
}

// CurrentEventID returns the newest activity event ID, or zero when there are
// no events. New webhooks use this cursor to avoid historical replay.
func (s *Store) CurrentEventID(ctx context.Context) (int64, error) {
	var id int64
	if err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM events").Scan(&id); err != nil {
		return 0, fmt.Errorf("store: get current event ID: %w", err)
	}
	return id, nil
}

// ListEventsAfter returns at most limit activity events after id in delivery
// order. Callers choose a bounded batch so an old cursor cannot materialize the
// whole event history in memory.
func (s *Store) ListEventsAfter(ctx context.Context, id int64, limit int) ([]core.Event, error) {
	if limit <= 0 {
		return nil, fmt.Errorf("store: event batch limit must be positive")
	}
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+eventColumns+" FROM events WHERE id > ? ORDER BY id LIMIT ?", id, limit)
	if err != nil {
		return nil, fmt.Errorf("store: list events after %d: %w", id, err)
	}
	defer rows.Close()

	events := []core.Event{}
	for rows.Next() {
		var (
			e         core.Event
			createdAt string
		)
		if err := rows.Scan(&e.ID, &e.Level, &e.Category, &e.Message, &e.Detail,
			&e.MovieID, &e.SeriesID, &createdAt); err != nil {
			return nil, fmt.Errorf("store: scan event after %d: %w", id, err)
		}
		e.CreatedAt = parseTime(createdAt)
		events = append(events, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list events after %d: %w", id, err)
	}
	return events, nil
}

// AdvanceNotificationWebhookCursor records that every event through eventID
// has either been delivered or did not match this webhook. The cursor never
// moves backwards.
func (s *Store) AdvanceNotificationWebhookCursor(ctx context.Context, id, eventID int64) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE notification_webhooks
		SET last_event_id = CASE WHEN last_event_id < ? THEN ? ELSE last_event_id END,
			updated_at = ?
		WHERE id = ?`, eventID, eventID, formatTime(now()), id)
	if err != nil {
		return fmt.Errorf("store: advance notification webhook cursor %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: advance notification webhook cursor %d: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

func scanNotificationWebhook(sc scanner) (*core.NotificationWebhook, error) {
	var w core.NotificationWebhook
	var createdAt, updatedAt string
	if err := sc.Scan(
		&w.ID, &w.Name, &w.URL, &w.OnGrab, &w.OnImport, &w.OnHealth, &w.Enabled,
		&w.LastEventID, &createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}
	w.CreatedAt = parseTime(createdAt)
	w.UpdatedAt = parseTime(updatedAt)
	return &w, nil
}
