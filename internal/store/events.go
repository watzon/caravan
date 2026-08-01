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

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO events (level, category, message, detail, movie_id, series_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		e.Level, e.Category, e.Message, e.Detail, e.MovieID, e.SeriesID, formatTime(e.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: insert event: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: insert event: %w", err)
	}
	e.ID = id
	return nil
}

// ListEvents returns the most recent events, newest first. A limit of zero or
// less returns every event.
//
// Ordering is by id rather than created_at: ids are monotonic, so two events
// written in the same clock tick still come back in the order they happened.
func (s *Store) ListEvents(ctx context.Context, limit int) ([]core.Event, error) {
	query := "SELECT " + eventColumns + " FROM events ORDER BY id DESC"
	args := []any{}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list events: %w", err)
	}
	defer rows.Close()

	out := []core.Event{}
	for rows.Next() {
		var (
			e         core.Event
			createdAt string
		)
		if err := rows.Scan(&e.ID, &e.Level, &e.Category, &e.Message, &e.Detail,
			&e.MovieID, &e.SeriesID, &createdAt); err != nil {
			return nil, fmt.Errorf("store: scan event: %w", err)
		}
		e.CreatedAt = parseTime(createdAt)
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list events: %w", err)
	}
	return out, nil
}
