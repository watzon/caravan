package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

// UpsertRelease caches a search result and writes back the assigned ID.
func (s *Store) UpsertRelease(ctx context.Context, r *core.Release) error {
	parsed, err := json.Marshal(r.Parsed)
	if err != nil {
		return fmt.Errorf("store: encode parsed release for %q: %w", r.Title, err)
	}
	categories := ""
	if len(r.Categories) > 0 {
		encoded, err := json.Marshal(r.Categories)
		if err != nil {
			return fmt.Errorf("store: encode categories for %q: %w", r.Title, err)
		}
		categories = string(encoded)
	}

	model := releaseModelFromCore(r, string(parsed), categories)
	_, err = s.db.NewInsert().Model(&model).
		On("CONFLICT (indexer_id, guid) DO UPDATE").
		Set("indexer_name = EXCLUDED.indexer_name").
		Set("title = EXCLUDED.title").
		Set("download_url = EXCLUDED.download_url").
		Set("info_hash = EXCLUDED.info_hash").
		Set("protocol = EXCLUDED.protocol").
		Set("size = EXCLUDED.size").
		Set("seeders = EXCLUDED.seeders").
		Set("leechers = EXCLUDED.leechers").
		Set("published_at = EXCLUDED.published_at").
		Set("parsed = EXCLUDED.parsed").
		Set("categories = EXCLUDED.categories").
		Set("seen_at = EXCLUDED.seen_at").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: upsert release %q: %w", r.Title, err)
	}
	if r.ID == 0 {
		if err := s.db.NewSelect().Model(&model).Column("id").
			Where("indexer_id = ?", r.IndexerID).Where("guid = ?", r.GUID).Scan(ctx); err != nil {
			return fmt.Errorf("store: upsert release %q: %w", r.Title, err)
		}
		r.ID = model.ID
	}
	return nil
}

// GetRelease returns the cached release with the given id, or ErrNotFound.
func (s *Store) GetRelease(ctx context.Context, id int64) (*core.Release, error) {
	return s.release(ctx, s.db.NewSelect().Model((*releaseModel)(nil)).Where("id = ?", id),
		fmt.Sprintf("release %d", id))
}

// GetReleaseByGUID returns the cached result an indexer previously published
// under guid, or ErrNotFound.
func (s *Store) GetReleaseByGUID(ctx context.Context, indexerID int64, guid string) (*core.Release, error) {
	query := s.db.NewSelect().Model((*releaseModel)(nil)).
		Where("indexer_id = ?", indexerID).Where("guid = ?", guid)
	return s.release(ctx, query, fmt.Sprintf("release %q on indexer %d", guid, indexerID))
}

func (s *Store) release(ctx context.Context, query *bun.SelectQuery, what string) (*core.Release, error) {
	var model releaseModel
	if err := query.Model(&model).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("store: %s: %w", what, ErrNotFound)
		}
		return nil, fmt.Errorf("store: get %s: %w", what, err)
	}
	out := model.core()
	return &out, nil
}
