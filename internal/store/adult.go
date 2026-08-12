package store

import (
	"context"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

// AdultLibraryName and AdultLibraryRoot are the Adult library's seed values.
// The root is storage-root-relative with forward slashes like every other
// stored path (SPEC §1.2 pillar 3), and sits beside library/Movies and
// library/TV so that "exclude the adult root" is one path prefix rather than a
// scan of the whole tree (PLAN phase 9 task 6).
const (
	AdultLibraryName = "Adult"
	AdultLibraryRoot = "library/Adult"
)

// EpisodeIDsByStashID reports episode rows for the given provider scene ids,
// whether or not a media file has arrived for them. It is used by catalogue
// reconciliation and approval, both of which need the placeholder row too.
func (s *Store) EpisodeIDsByStashID(ctx context.Context, stashIDs []string) (map[string]int64, error) {
	return s.episodeIDsByStashID(ctx, "", stashIDs, false)
}

// EpisodeFileIDsByStashID reports only scenes with a linked media file. Adding
// a site creates episode placeholders for its full catalogue, so those rows do
// not by themselves mean that the individual scene is in the library.
func (s *Store) EpisodeFileIDsByStashID(ctx context.Context, stashIDs []string) (map[string]int64, error) {
	return s.episodeIDsByStashID(ctx, "", stashIDs, true)
}

// EpisodeFileIDsByStashIDForProvider is the provider-pinned form used by Adult
// Explore. Different stash-box instances may carry the same UUID, so a file
// owned under one instance must not decorate another instance's result.
func (s *Store) EpisodeFileIDsByStashIDForProvider(
	ctx context.Context,
	provider string,
	stashIDs []string,
) (map[string]int64, error) {
	return s.episodeIDsByStashID(ctx, provider, stashIDs, true)
}
func (s *Store) episodeIDsByStashID(
	ctx context.Context,
	provider string,
	stashIDs []string,
	requireFile bool,
) (map[string]int64, error) {
	wanted := make([]any, 0, len(stashIDs))
	seen := make(map[string]bool, len(stashIDs))
	for _, id := range stashIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		wanted = append(wanted, id)
	}
	out := map[string]int64{}
	if len(wanted) == 0 {
		return out, nil
	}

	query := "SELECT stash_id, id FROM episodes WHERE stash_id IN (" + placeholders(len(wanted)) + ")"
	if requireFile {
		query += " AND EXISTS (SELECT 1 FROM episode_files ef WHERE ef.episode_id = episodes.id)"
	}
	if provider != "" {
		query += " AND EXISTS (SELECT 1 FROM series s WHERE s.id = episodes.series_id AND s.kind = ? AND s.provider = ?)"
		wanted = append(wanted, core.SeriesKindAdult, provider)
	}
	rows, err := s.db.QueryContext(ctx, query, wanted...)
	if err != nil {
		return nil, fmt.Errorf("store: episode ids by stash id: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			stashID string
			id      int64
		)
		if err := rows.Scan(&stashID, &id); err != nil {
			return nil, fmt.Errorf("store: scan episode id: %w", err)
		}
		out[stashID] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: episode ids by stash id: %w", err)
	}
	return out, nil
}

// ListPendingRequestsForStashIDs is ListPendingRequestsForTMDBIDs' scene twin.
//
// It is a separate method rather than a widened one because the two ids live in
// different columns and different namespaces (see core.Request): a query that
// took both would have to OR across them, and an empty tmdb list would then
// match every scene row. Keeping them apart is also what stops a television
// discover page from ever loading a scene request to decorate itself with.
func (s *Store) ListPendingRequestsForStashIDs(ctx context.Context, stashIDs []string) ([]core.Request, error) {
	args := []any{core.RequestPending}
	seen := map[string]bool{}
	for _, id := range stashIDs {
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		args = append(args, id)
	}
	if len(args) == 1 {
		return []core.Request{}, nil
	}
	return s.listRequests(ctx,
		"SELECT "+requestColumns+" FROM requests WHERE status = ? AND stash_id IN ("+
			placeholders(len(args)-1)+")", args...)
}
