package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

// ErrConversionOpen is returned by CreateConversion when the file already has
// a queued or running conversion. It is a distinct error rather than a generic
// constraint failure because the HTTP layer answers it with 409, not 500.
var ErrConversionOpen = errors.New("store: conversion already open for this file")

// CreateConversion inserts a queued conversion and writes back its ID.
func (s *Store) CreateConversion(ctx context.Context, c *core.Conversion) error {
	ts := now()
	if c.CreatedAt.IsZero() {
		c.CreatedAt = ts
	}
	c.UpdatedAt = ts
	if c.Status == "" {
		c.Status = core.ConversionQueued
	}

	model := conversionModelFromCore(c)
	err := s.db.NewInsert().Model(&model).Returning("id").Scan(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("store: create conversion for media file %d: %w", c.MediaFileID, ErrConversionOpen)
		}
		return fmt.Errorf("store: create conversion for media file %d: %w", c.MediaFileID, err)
	}
	c.ID = model.ID
	s.note("library")
	return nil
}

// UpdateConversion writes the mutable half of a conversion back. Updating an
// absent conversion is ErrNotFound.
func (s *Store) UpdateConversion(ctx context.Context, c *core.Conversion) error {
	c.UpdatedAt = now()
	model := conversionModelFromCore(c)
	res, err := s.db.NewUpdate().Model(&model).
		Column("output_path", "strategy", "profile_id", "status", "error", "updated_at").
		WherePK().Exec(ctx)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("store: update conversion %d: %w", c.ID, ErrConversionOpen)
		}
		return fmt.Errorf("store: update conversion %d: %w", c.ID, err)
	}
	if err := affectedOne(res, "update conversion", c.ID); err != nil {
		return err
	}
	s.note("library")
	return nil
}

// TransitionConversion moves a conversion to `to`, but only while it is still
// in one of `from`. It reports whether it moved.
func (s *Store) TransitionConversion(ctx context.Context, id int64, to string, from ...string) (bool, error) {
	if len(from) == 0 {
		return false, fmt.Errorf("store: transition conversion %d: no source status given", id)
	}
	res, err := s.db.NewUpdate().Model((*conversionModel)(nil)).
		Set("status = ?", to).
		Set("error = ''").
		Set("updated_at = ?", formatTime(now())).
		Where("id = ?", id).
		Where("status IN (?)", bun.In(from)).
		Exec(ctx)
	if err != nil {
		return false, fmt.Errorf("store: transition conversion %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("store: transition conversion %d: %w", id, err)
	}
	if n > 0 {
		s.note("library")
	}
	return n > 0, nil
}

// GetConversion returns one conversion, or ErrNotFound.
func (s *Store) GetConversion(ctx context.Context, id int64) (*core.Conversion, error) {
	var model conversionModel
	err := s.db.NewSelect().Model(&model).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: conversion %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get conversion %d: %w", id, err)
	}
	out := model.core()
	return &out, nil
}

// OpenConversionForFile returns the file's queued or running conversion, or
// ErrNotFound when it has none.
func (s *Store) OpenConversionForFile(ctx context.Context, mediaFileID int64) (*core.Conversion, error) {
	var model conversionModel
	err := s.db.NewSelect().Model(&model).
		Where("media_file_id = ?", mediaFileID).
		Where("status IN (?)", bun.In([]string{core.ConversionQueued, core.ConversionRunning})).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: open conversion for media file %d: %w", mediaFileID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: open conversion for media file %d: %w", mediaFileID, err)
	}
	out := model.core()
	return &out, nil
}

// ListConversions returns the most recent conversions, newest first. A limit
// of zero or less returns every row.
func (s *Store) ListConversions(ctx context.Context, limit int) ([]core.Conversion, error) {
	models := []conversionModel{}
	query := s.db.NewSelect().Model(&models).OrderExpr("id DESC")
	if limit > 0 {
		query.Limit(limit)
	}
	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list conversions: %w", err)
	}
	out := make([]core.Conversion, len(models))
	for i := range models {
		out[i] = models[i].core()
	}
	return out, nil
}
