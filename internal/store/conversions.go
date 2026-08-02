package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const conversionColumns = `id, media_file_id, source_path, output_path, strategy,
	profile_id, status, error, created_at, updated_at`

// ErrConversionOpen is returned by CreateConversion when the file already has
// a queued or running conversion. It is a distinct error rather than a generic
// constraint failure because the HTTP layer answers it with 409, not 500.
var ErrConversionOpen = errors.New("store: conversion already open for this file")

// CreateConversion inserts a queued conversion and writes back its ID.
//
// The partial unique index on (media_file_id) over open statuses is what makes
// this safe against a double submit: the second insert loses, and the caller
// sees ErrConversionOpen rather than a second ffmpeg run.
func (s *Store) CreateConversion(ctx context.Context, c *core.Conversion) error {
	ts := now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = ts
	}
	c.UpdatedAt = ts
	if c.Status == "" {
		c.Status = core.ConversionQueued
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO conversions (media_file_id, source_path, output_path, strategy,
			profile_id, status, error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		c.MediaFileID, c.SourcePath, c.OutputPath, c.Strategy, c.ProfileID,
		c.Status, c.Error, formatTime(c.CreatedAt), formatTime(c.UpdatedAt))
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("store: create conversion for media file %d: %w", c.MediaFileID, ErrConversionOpen)
		}
		return fmt.Errorf("store: create conversion for media file %d: %w", c.MediaFileID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: create conversion for media file %d: %w", c.MediaFileID, err)
	}
	c.ID = id
	return nil
}

// UpdateConversion writes the mutable half of a conversion back. Updating an
// absent conversion is ErrNotFound.
func (s *Store) UpdateConversion(ctx context.Context, c *core.Conversion) error {
	c.UpdatedAt = now()
	res, err := s.db.ExecContext(ctx, `
		UPDATE conversions
		SET output_path = ?, strategy = ?, profile_id = ?, status = ?, error = ?, updated_at = ?
		WHERE id = ?`,
		c.OutputPath, c.Strategy, c.ProfileID, c.Status, c.Error, formatTime(c.UpdatedAt), c.ID)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("store: update conversion %d: %w", c.ID, ErrConversionOpen)
		}
		return fmt.Errorf("store: update conversion %d: %w", c.ID, err)
	}
	return affectedOne(res, "update conversion", c.ID)
}

// TransitionConversion moves a conversion to `to`, but only while it is still
// in one of `from`. It reports whether it moved.
//
// The condition is the point. Cancel runs on an HTTP goroutine and the job
// handler runs on a worker, and both write the same row: a plain UpdateConversion
// from each lets the user be told "cancelled" while the handler goes on to
// replace the file. Making each side claim the row it thought it read means the
// loser finds out.
func (s *Store) TransitionConversion(ctx context.Context, id int64, to string, from ...string) (bool, error) {
	if len(from) == 0 {
		return false, fmt.Errorf("store: transition conversion %d: no source status given", id)
	}
	args := []any{to, formatTime(now()), id}
	for _, status := range from {
		args = append(args, status)
	}
	res, err := s.db.ExecContext(ctx, `
		UPDATE conversions
		SET status = ?, error = '', updated_at = ?
		WHERE id = ? AND status IN (`+placeholders(len(from))+`)`, args...)
	if err != nil {
		return false, fmt.Errorf("store: transition conversion %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: transition conversion %d: %w", id, err)
	}
	return n > 0, nil
}

// GetConversion returns one conversion, or ErrNotFound.
func (s *Store) GetConversion(ctx context.Context, id int64) (*core.Conversion, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+conversionColumns+" FROM conversions WHERE id = ?", id)
	c, err := scanConversion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: conversion %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get conversion %d: %w", id, err)
	}
	return c, nil
}

// OpenConversionForFile returns the file's queued or running conversion, or
// ErrNotFound when it has none.
func (s *Store) OpenConversionForFile(ctx context.Context, mediaFileID int64) (*core.Conversion, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+conversionColumns+" FROM conversions WHERE media_file_id = ? AND status IN (?, ?)",
		mediaFileID, core.ConversionQueued, core.ConversionRunning)
	c, err := scanConversion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: open conversion for media file %d: %w", mediaFileID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: open conversion for media file %d: %w", mediaFileID, err)
	}
	return c, nil
}

// ListConversions returns the most recent conversions, newest first. A limit
// of zero or less returns every row.
func (s *Store) ListConversions(ctx context.Context, limit int) ([]core.Conversion, error) {
	query := "SELECT " + conversionColumns + " FROM conversions ORDER BY id DESC"
	if limit > 0 {
		query += fmt.Sprintf(" LIMIT %d", limit)
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("store: list conversions: %w", err)
	}
	defer rows.Close()

	out := []core.Conversion{}
	for rows.Next() {
		c, err := scanConversion(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan conversion: %w", err)
		}
		out = append(out, *c)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list conversions: %w", err)
	}
	return out, nil
}

func scanConversion(sc scanner) (*core.Conversion, error) {
	var (
		c         core.Conversion
		createdAt string
		updatedAt string
	)
	err := sc.Scan(&c.ID, &c.MediaFileID, &c.SourcePath, &c.OutputPath, &c.Strategy,
		&c.ProfileID, &c.Status, &c.Error, &createdAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	c.CreatedAt = parseTime(createdAt)
	c.UpdatedAt = parseTime(updatedAt)
	return &c, nil
}
