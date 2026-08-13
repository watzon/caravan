package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

// notificationWebhookModel is the database representation of a notification
// webhook. Time values deliberately remain strings so malformed cache values
// retain the store's existing zero-time fallback when converted to core types.
type notificationWebhookModel struct {
	bun.BaseModel `bun:"table:notification_webhooks,alias:notification_webhook"`

	ID          int64 `bun:",pk,autoincrement"`
	Name        string
	URL         string
	OnGrab      bool
	OnImport    bool
	OnHealth    bool
	Enabled     bool
	LastEventID int64
	CreatedAt   string
	UpdatedAt   string
}

func (m notificationWebhookModel) coreValue() core.NotificationWebhook {
	return core.NotificationWebhook{
		ID:          m.ID,
		Name:        m.Name,
		URL:         m.URL,
		OnGrab:      m.OnGrab,
		OnImport:    m.OnImport,
		OnHealth:    m.OnHealth,
		Enabled:     m.Enabled,
		LastEventID: m.LastEventID,
		CreatedAt:   parseTime(m.CreatedAt),
		UpdatedAt:   parseTime(m.UpdatedAt),
	}
}

// CreateNotificationWebhook inserts w at the current event cursor. Events that
// existed before the webhook was configured are not delivered retroactively.
func (s *Store) CreateNotificationWebhook(ctx context.Context, w *core.NotificationWebhook) error {
	ts := formatTime(now())
	model := &notificationWebhookModel{
		Name:      w.Name,
		URL:       w.URL,
		OnGrab:    w.OnGrab,
		OnImport:  w.OnImport,
		OnHealth:  w.OnHealth,
		Enabled:   w.Enabled,
		CreatedAt: ts,
		UpdatedAt: ts,
	}
	if _, err := s.db.NewInsert().Model(model).
		Value("last_event_id", "(SELECT COALESCE(MAX(id), 0) FROM events)").
		Exec(ctx); err != nil {
		return fmt.Errorf("store: create notification webhook %q: %w", w.Name, err)
	}
	created, err := s.GetNotificationWebhook(ctx, model.ID)
	if err != nil {
		return fmt.Errorf("store: read created notification webhook: %w", err)
	}
	*w = *created
	return nil
}

// UpdateNotificationWebhook replaces a webhook's configurable fields. Its
// identity, delivery cursor, and creation time remain unchanged.
func (s *Store) UpdateNotificationWebhook(ctx context.Context, w *core.NotificationWebhook) error {
	model := &notificationWebhookModel{
		ID:        w.ID,
		Name:      w.Name,
		URL:       w.URL,
		OnGrab:    w.OnGrab,
		OnImport:  w.OnImport,
		OnHealth:  w.OnHealth,
		Enabled:   w.Enabled,
		UpdatedAt: formatTime(now()),
	}
	res, err := s.db.NewUpdate().Model(model).
		Column("name", "url", "on_grab", "on_import", "on_health", "enabled", "updated_at").
		WherePK().Exec(ctx)
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
	var model notificationWebhookModel
	err := s.db.NewSelect().Model(&model).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("store: get notification webhook %d: %w", id, err)
	}
	out := model.coreValue()
	return &out, nil
}

// ListNotificationWebhooks returns every webhook in stable name and ID order.
func (s *Store) ListNotificationWebhooks(ctx context.Context) ([]core.NotificationWebhook, error) {
	models := make([]notificationWebhookModel, 0)
	if err := s.db.NewSelect().Model(&models).
		Order("name ASC", "id ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list notification webhooks: %w", err)
	}

	webhooks := make([]core.NotificationWebhook, 0, len(models))
	for _, model := range models {
		webhooks = append(webhooks, model.coreValue())
	}
	return webhooks, nil
}

// DeleteNotificationWebhook removes a webhook. A missing row is already gone.
func (s *Store) DeleteNotificationWebhook(ctx context.Context, id int64) error {
	if _, err := s.db.NewDelete().Model((*notificationWebhookModel)(nil)).
		Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("store: delete notification webhook %d: %w", id, err)
	}
	return nil
}

// CurrentEventID returns the newest activity event ID, or zero when there are
// no events. New webhooks use this cursor to avoid historical replay.
func (s *Store) CurrentEventID(ctx context.Context) (int64, error) {
	var id int64
	if err := s.db.NewSelect().Model((*eventModel)(nil)).
		ColumnExpr("COALESCE(MAX(id), 0)").
		Scan(ctx, &id); err != nil {
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
	models := make([]eventModel, 0)
	if err := s.db.NewSelect().Model(&models).
		Where("id > ?", id).
		Order("id ASC").
		Limit(limit).
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list events after %d: %w", id, err)
	}

	events := make([]core.Event, 0, len(models))
	for _, model := range models {
		events = append(events, model.core())
	}
	return events, nil
}

// AdvanceNotificationWebhookCursor records that every event through eventID
// has either been delivered or did not match this webhook. The cursor never
// moves backwards.
func (s *Store) AdvanceNotificationWebhookCursor(ctx context.Context, id, eventID int64) error {
	res, err := s.db.NewUpdate().Model((*notificationWebhookModel)(nil)).
		Set("last_event_id = CASE WHEN last_event_id < ? THEN ? ELSE last_event_id END", eventID, eventID).
		Set("updated_at = ?", formatTime(now())).
		Where("id = ?", id).
		Exec(ctx)
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
