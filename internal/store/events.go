package store

import (
	"context"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const eventColumns = `id, level, category, message, detail, movie_id, series_id, created_at`

// InsertEvent appends an event to the activity feed and writes back the
// assigned ID. An empty Level defaults to info.
func (s *Store) InsertEvent(ctx context.Context, e *core.Event) error {
	if e.Level == "" {
		e.Level = core.EventLevelInfo
	}
	if e.CreatedAt.IsZero() {
		e.CreatedAt = now()
	}

	model := eventModelFromCore(e)
	if err := s.db.NewInsert().Model(&model).Returning("id").Scan(ctx); err != nil {
		return fmt.Errorf("store: insert event: %w", err)
	}
	e.ID = model.ID
	return nil
}

// ListEvents returns the most recent events, newest first. A limit of zero or
// less returns every event.
//
// Ordering is by id rather than created_at: ids are monotonic, so two events
// written in the same clock tick still come back in the order they happened.
func (s *Store) ListEvents(ctx context.Context, limit int) ([]core.Event, error) {
	query := s.db.NewSelect().Model((*eventModel)(nil)).OrderExpr("id DESC")
	if limit > 0 {
		query.Limit(limit)
	}
	models := []eventModel{}
	if err := query.Scan(ctx, &models); err != nil {
		return nil, fmt.Errorf("store: list events: %w", err)
	}
	out := make([]core.Event, len(models))
	for i := range models {
		out[i] = models[i].core()
	}
	return out, nil
}

// ListEventsPage returns one keyset page of events, newest first. beforeID
// excludes that event and every newer row. nextID is non-zero only when more
// rows remain.
func (s *Store) ListEventsPage(ctx context.Context, limit, beforeID int64) ([]core.Event, int64, error) {
	if limit <= 0 {
		return []core.Event{}, 0, nil
	}
	models := []eventModel{}
	query := s.db.NewSelect().Model(&models).OrderExpr("id DESC").Limit(int(limit + 1))
	if beforeID > 0 {
		query.Where("id < ?", beforeID)
	}
	if err := query.Scan(ctx); err != nil {
		return nil, 0, fmt.Errorf("store: list event page: %w", err)
	}

	more := len(models) > int(limit)
	if more {
		models = models[:limit]
	}
	out := make([]core.Event, len(models))
	for i := range models {
		out[i] = models[i].core()
	}
	if more {
		return out, out[len(out)-1].ID, nil
	}
	return out, 0, nil
}
