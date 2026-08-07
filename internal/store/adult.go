package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

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

// AdultEnabled reports whether the adult module is switched on server-wide.
//
// NOTHING WRITES THE SETTING ANY MORE: per-library `active` replaced it, and
// the only writer went with POST /settings/adult. What it reports is therefore
// whatever an install was carrying when it upgraded, frozen — useful to
// migration 0027, which backfills from it, and to nobody else. It survives only
// until 0028 deletes the key; ask AnyActiveLibraryOfKind instead.
//
// Absent means off, and so does anything that will not parse. That is the
// mirror image of SettingDLNAEnabled's rule and the reason is the same one read
// backwards: a default is a decision about what a typo means, and for a setting
// whose job is to keep a whole module absent, the only safe answer is "off".
func (s *Store) AdultEnabled(ctx context.Context) (bool, error) {
	raw, err := s.GetSetting(ctx, SettingAdultEnabled)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	enabled, _ := strconv.ParseBool(strings.TrimSpace(raw))
	return enabled, nil
}

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
