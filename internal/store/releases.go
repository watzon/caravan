package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const releaseColumns = `id, indexer_id, indexer_name, title, guid, download_url, info_hash,
	protocol, size, seeders, leechers, published_at, parsed`

// UpsertRelease caches a search result and writes back the assigned ID.
//
// Identity is (IndexerID, GUID): the same result seen by a later search
// refreshes the row rather than duplicating it, which is what makes this table
// usable as a "have I seen this already" cache. Losing the cache costs a
// re-search and nothing else.
func (s *Store) UpsertRelease(ctx context.Context, r *core.Release) error {
	parsed, err := json.Marshal(r.Parsed)
	if err != nil {
		return fmt.Errorf("store: encode parsed release for %q: %w", r.Title, err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO releases (indexer_id, indexer_name, title, guid, download_url, info_hash,
			protocol, size, seeders, leechers, published_at, parsed, seen_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (indexer_id, guid) DO UPDATE SET
			indexer_name = excluded.indexer_name, title = excluded.title,
			download_url = excluded.download_url, info_hash = excluded.info_hash,
			protocol = excluded.protocol, size = excluded.size,
			seeders = excluded.seeders, leechers = excluded.leechers,
			published_at = excluded.published_at, parsed = excluded.parsed,
			seen_at = excluded.seen_at`,
		r.IndexerID, r.Indexer, r.Title, r.GUID, r.DownloadURL, r.InfoHash, r.Protocol,
		r.Size, r.Seeders, r.Leechers, formatTime(r.PublishedAt), string(parsed), formatTime(now()))
	if err != nil {
		return fmt.Errorf("store: upsert release %q: %w", r.Title, err)
	}
	if r.ID != 0 {
		return nil
	}
	err = s.db.QueryRowContext(ctx,
		"SELECT id FROM releases WHERE indexer_id = ? AND guid = ?", r.IndexerID, r.GUID).Scan(&r.ID)
	if err != nil {
		return fmt.Errorf("store: upsert release %q: %w", r.Title, err)
	}
	return nil
}

// GetRelease returns the cached release with the given id, or ErrNotFound.
func (s *Store) GetRelease(ctx context.Context, id int64) (*core.Release, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+releaseColumns+" FROM releases WHERE id = ?", id)
	r, err := scanRelease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: release %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get release %d: %w", id, err)
	}
	return r, nil
}

// GetReleaseByGUID returns the cached result an indexer previously published
// under guid, or ErrNotFound. This is the seen-cache lookup that keeps an RSS
// sync from re-grabbing what it already handled.
func (s *Store) GetReleaseByGUID(ctx context.Context, indexerID int64, guid string) (*core.Release, error) {
	row := s.db.QueryRowContext(ctx,
		"SELECT "+releaseColumns+" FROM releases WHERE indexer_id = ? AND guid = ?", indexerID, guid)
	r, err := scanRelease(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: release %q on indexer %d: %w", guid, indexerID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get release %q on indexer %d: %w", guid, indexerID, err)
	}
	return r, nil
}

func scanRelease(sc scanner) (*core.Release, error) {
	var (
		r           core.Release
		publishedAt string
		parsed      string
	)
	err := sc.Scan(&r.ID, &r.IndexerID, &r.Indexer, &r.Title, &r.GUID, &r.DownloadURL,
		&r.InfoHash, &r.Protocol, &r.Size, &r.Seeders, &r.Leechers, &publishedAt, &parsed)
	if err != nil {
		return nil, err
	}
	if parsed != "" {
		// As with unmatched files: a row whose JSON no longer decodes must
		// still be readable, because the title is re-parseable at any time.
		_ = json.Unmarshal([]byte(parsed), &r.Parsed)
	}
	r.PublishedAt = parseTime(publishedAt)
	return &r, nil
}
