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

// EpisodeIDsByStashID reports which of the given scenes the library already
// holds an episode row for, keyed by stash id. It is GetEpisodeByStashID's bulk
// form and exists for the same reason MovieIDsByTMDBID does: a discover page is
// twenty scenes, and twenty round trips to answer "is this one ours" would make
// the screen cost twenty queries.
//
// Absent ids are simply missing from the result, so the caller reads it as a
// set. A blank id is dropped rather than queried: episodes.stash_id is the
// empty string for every television episode there is, so a blank in the list
// would match all of them and report the whole TV library as scenes.
func (s *Store) EpisodeIDsByStashID(ctx context.Context, stashIDs []string) (map[string]int64, error) {
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

	rows, err := s.db.QueryContext(ctx,
		"SELECT stash_id, id FROM episodes WHERE stash_id IN ("+placeholders(len(wanted))+")",
		wanted...)
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
