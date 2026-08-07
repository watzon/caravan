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

// SetAdultEnabled flips the server-wide switch, creating the Adult library row
// the first time it is turned on.
//
// The creation lives here rather than in the caller for the reason
// UpdateLibrary bumps the DLNA update id itself: the setting and the row are
// two halves of one fact, and two callers that each did half would eventually
// disagree. It is idempotent — enabling a second time reuses the row that
// already exists, along with whatever the owner has since done to it.
//
// Disabling deletes nothing. Not the library row, not the series, not the
// episodes, and certainly not the files: "off" means the module is not
// reachable, which is a visibility promise, not a retention policy. Turning it
// back on has to find the library exactly as it was left, or the switch would
// be a destructive operation wearing a toggle's clothes.
//
// The new row is created with dlna_visible OFF. That is the one place in
// Caravan where a library is born hidden, and it is deliberate: DLNA has no
// accounts, so a container advertised on the LAN is readable by every device on
// it. Sharing it is a second, separate decision the owner makes on the DLNA
// card (PLAN phase 9 task 6).
func (s *Store) SetAdultEnabled(ctx context.Context, enabled bool) error {
	if err := s.SetSetting(ctx, SettingAdultEnabled, strconv.FormatBool(enabled)); err != nil {
		return err
	}
	if !enabled {
		return nil
	}
	return s.ensureAdultLibrary(ctx)
}

// ensureAdultLibrary creates the Adult library row if it is not already there.
//
// The insert is guarded by root_path's UNIQUE constraint rather than by a
// read-then-write, so two enables racing produce one row and one of them does
// nothing, instead of two rows or a failed request. Before 0022 the guard was
// UNIQUE(kind); with several libraries per kind allowed, the seed root is the
// column that still identifies THIS row, and it is what a re-enable collides
// with — the row is never deleted (see the disable contract above), so the
// collision is the common case, not the edge.
//
// The row becomes the kind's default only when no adult default exists yet:
// on first enable that is this row, and forever after the subquery keeps a
// re-enable from ever contending with idx_libraries_default_per_kind.
func (s *Store) ensureAdultLibrary(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO libraries (kind, name, root_path, dlna_visible, provider, providers, is_default)
		SELECT ?, ?, ?, 0, ?, ?,
			NOT EXISTS (SELECT 1 FROM libraries WHERE kind = ? AND is_default = 1)
		ON CONFLICT (root_path) DO NOTHING`,
		core.LibraryKindAdult, AdultLibraryName, AdultLibraryRoot,
		core.ProviderStashbox, `["`+core.ProviderStashbox+`"]`, core.LibraryKindAdult)
	if err != nil {
		return fmt.Errorf("store: create adult library: %w", err)
	}
	return nil
}

// SetUserAdultAccess grants or revokes one account's access to the adult
// module. Setting it on an absent account is ErrNotFound.
//
// The grant is only half of the answer: core.AdultVisible also requires the
// server-wide switch, so a grant made and then forgotten opens nothing once the
// module is turned off.
func (s *Store) SetUserAdultAccess(ctx context.Context, id int64, granted bool) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE users SET adult_access = ?, updated_at = ? WHERE id = ?",
		granted, formatTime(now()), id)
	if err != nil {
		return fmt.Errorf("store: set user %d adult access: %w", id, err)
	}
	return affectedOne(res, "set user adult access", id)
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
