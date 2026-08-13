package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

// UpsertUnmatchedFile parks a file in the scan-review queue, or refreshes the
// entry if it is already parked.
func (s *Store) UpsertUnmatchedFile(ctx context.Context, u *core.UnmatchedFile) error {
	parsed, err := json.Marshal(u.Parsed)
	if err != nil {
		return fmt.Errorf("store: encode parsed release for %q: %w", u.Path, err)
	}
	if u.SeenAt.IsZero() {
		u.SeenAt = now()
	}
	model := unmatchedFileModelFromCore(u, string(parsed))
	_, err = s.db.NewInsert().Model(&model).
		On("CONFLICT (path) DO UPDATE").
		Set("size = EXCLUDED.size").
		Set("parsed = EXCLUDED.parsed").
		Set("reason = EXCLUDED.reason").
		Set("seen_at = EXCLUDED.seen_at").
		Set("library_id = EXCLUDED.library_id").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: upsert unmatched file %q: %w", u.Path, err)
	}
	if u.ID == 0 {
		if err := s.db.NewSelect().Model(&model).Column("id").Where("path = ?", u.Path).Scan(ctx); err != nil {
			return fmt.Errorf("store: upsert unmatched file %q: %w", u.Path, err)
		}
		u.ID = model.ID
	}
	return nil
}

// GetUnmatchedFile returns the parked file with the given id, or ErrNotFound.
func (s *Store) GetUnmatchedFile(ctx context.Context, id int64) (*core.UnmatchedFile, error) {
	var model unmatchedFileModel
	err := s.db.NewSelect().Model(&model).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: unmatched file %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get unmatched file %d: %w", id, err)
	}
	out := model.core()
	return &out, nil
}

// ListUnmatchedFiles returns the scan-review queue ordered by id DESC.
func (s *Store) ListUnmatchedFiles(ctx context.Context) ([]core.UnmatchedFile, error) {
	models := []unmatchedFileModel{}
	if err := s.db.NewSelect().Model(&models).OrderExpr("id DESC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list unmatched files: %w", err)
	}
	out := make([]core.UnmatchedFile, len(models))
	for i := range models {
		out[i] = models[i].core()
	}
	return out, nil
}

// DeleteUnmatchedFileByPath clears a file from the review queue.
func (s *Store) DeleteUnmatchedFileByPath(ctx context.Context, path string) error {
	if _, err := s.db.NewDelete().Model((*unmatchedFileModel)(nil)).Where("path = ?", path).Exec(ctx); err != nil {
		return fmt.Errorf("store: delete unmatched file %q: %w", path, err)
	}
	return nil
}
