package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// sqliteIDQueryBatchSize leaves ample room below SQLite's conservative
// bind-variable ceiling for any fixed query arguments.
const sqliteIDQueryBatchSize = 500

const grabColumns = `id, release_id, movie_id, series_id, season_number, episode_ids,
	release_title, reason, status, created_at, library_id`

// InsertGrab appends a grab to the history and writes back the assigned
// GrabID. Grabs are append-only: a grab that later succeeds or fails is
// updated through SetGrabStatus, never replaced.
func (s *Store) InsertGrab(ctx context.Context, g *core.Grab) error {
	episodeIDs, err := json.Marshal(g.EpisodeIDs)
	if err != nil {
		return fmt.Errorf("store: encode episode ids for grab of %q: %w", g.ReleaseTitle, err)
	}
	if g.Status == "" {
		g.Status = core.GrabStatusGrabbed
	}
	if g.CreatedAt.IsZero() {
		g.CreatedAt = now()
	}

	model := grabModelFromCore(g, string(episodeIDs))
	if err := s.db.NewInsert().Model(&model).Returning("id").Scan(ctx); err != nil {
		return fmt.Errorf("store: insert grab of %q: %w", g.ReleaseTitle, err)
	}
	g.GrabID = model.ID
	return nil
}

// GetGrab returns the grab with the given id, or ErrNotFound.
func (s *Store) GetGrab(ctx context.Context, id int64) (*core.Grab, error) {
	var model grabModel
	err := s.db.NewSelect().Model(&model).Where("id = ?", id).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: grab %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get grab %d: %w", id, err)
	}
	out, err := model.core()
	if err != nil {
		return nil, fmt.Errorf("store: get grab %d: %w", id, err)
	}
	return &out, nil
}

// GetUnlinkedGrabbedByReleaseTitle returns the open grab that named this
// release, or ErrNotFound. It is the recovery path for a download the engine
// persisted before the grab handler could write grab_id: the titles match
// because both sides recorded the same release name.
//
// Only an unlinked grabbed row is returned. A grab already tied to a download
// is somebody else's payload, and a finished grab is a decision that must not
// be replayed.
func (s *Store) GetUnlinkedGrabbedByReleaseTitle(ctx context.Context, title string) (*core.Grab, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		return nil, fmt.Errorf("store: grab for release %q: %w", title, ErrNotFound)
	}
	row := s.db.QueryRowContext(ctx, "SELECT "+grabColumns+`
		FROM grabs
		WHERE status = ?
			AND release_title = ?
			AND NOT EXISTS (
				SELECT 1 FROM downloads WHERE downloads.grab_id = grabs.id
			)
		ORDER BY id DESC
		LIMIT 1`, core.GrabStatusGrabbed, title)
	g, err := scanGrab(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: grab for release %q: %w", title, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get grab for release %q: %w", title, err)
	}
	return g, nil
}

// GetGrabByDownloadID returns the grab a download was started for, or
// ErrNotFound when the download has no grab behind it (a download added out of
// band, or one whose grab row is gone). This is what the import pipeline calls
// to learn what a finished download was supposed to be.
func (s *Store) GetGrabByDownloadID(ctx context.Context, engineID core.DownloadID) (*core.Grab, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+grabColumns+`
		FROM grabs
		WHERE id = (SELECT grab_id FROM downloads WHERE engine_id = ?)`, string(engineID))
	g, err := scanGrab(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: grab for download %q: %w", engineID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get grab for download %q: %w", engineID, err)
	}
	return g, nil
}

// ActiveGrabForMovie returns the in-flight automatic or interactive grab for
// movieID. A failed download does not block a retry, but a grab whose engine
// has not persisted its first status snapshot yet still does: delivery is
// at-least-once, so treating that short gap as idle would duplicate a download.
func (s *Store) ActiveGrabForMovie(ctx context.Context, movieID int64) (*core.Grab, bool, error) {
	return s.activeGrab(ctx, "movie_id = ?", movieID)
}

// ActiveGrabForEpisode returns the in-flight automatic or interactive grab
// containing episodeID. Episode ids are JSON in the history table, so SQLite's
// json_each keeps this query precise without a lossy string match.
func (s *Store) ActiveGrabForEpisode(ctx context.Context, episodeID int64) (*core.Grab, bool, error) {
	return s.activeGrab(ctx, "EXISTS (SELECT 1 FROM json_each(grabs.episode_ids) WHERE value = ?)", episodeID)
}

// ActiveTargets is the set of library items covered by in-flight grabs.
type ActiveTargets struct {
	Movies   map[int64]bool
	Episodes map[int64]bool
	Series   map[int64]bool
}

// ActiveTargetsFromGrabs reduces grabbed history to the items that are still
// downloading. The short gap before the first download row is written counts
// as active. A grab with downloads counts as inactive only when all of them
// have failed.
func (s *Store) ActiveTargetsFromGrabs(ctx context.Context, grabs []core.Grab) (ActiveTargets, error) {
	targets := ActiveTargets{
		Movies:   make(map[int64]bool),
		Episodes: make(map[int64]bool),
		Series:   make(map[int64]bool),
	}
	grabIDs := make([]int64, 0, len(grabs))
	for _, grab := range grabs {
		grabIDs = append(grabIDs, grab.GrabID)
	}
	downloads, err := s.ListDownloadsForGrabs(ctx, grabIDs)
	if err != nil {
		return ActiveTargets{}, err
	}

	type downloadState struct {
		hasAny bool
		active bool
	}
	byGrabID := make(map[int64]downloadState, len(downloads))
	for _, download := range downloads {
		if download.GrabID == 0 {
			continue
		}
		state := byGrabID[download.GrabID]
		state.hasAny = true
		state.active = state.active || download.State != core.DownloadFailed
		byGrabID[download.GrabID] = state
	}

	for _, grab := range grabs {
		if grab.Status != core.GrabStatusGrabbed {
			continue
		}
		state := byGrabID[grab.GrabID]
		if state.hasAny && !state.active {
			continue
		}
		if grab.MovieID != 0 {
			targets.Movies[grab.MovieID] = true
		}
		if grab.SeriesID != 0 {
			targets.Series[grab.SeriesID] = true
		}
		for _, episodeID := range grab.EpisodeIDs {
			targets.Episodes[episodeID] = true
		}
	}
	return targets, nil
}

func (s *Store) activeGrab(ctx context.Context, target string, arg int64) (*core.Grab, bool, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+grabColumns+`
		FROM grabs
		WHERE status = ?
			AND (`+target+`)
			AND NOT EXISTS (
				SELECT 1 FROM downloads
				WHERE downloads.grab_id = grabs.id AND downloads.state = ?
			)
		ORDER BY id DESC
		LIMIT 1`, core.GrabStatusGrabbed, arg, core.DownloadFailed)
	g, err := scanGrab(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("store: find active grab: %w", err)
	}
	return g, true, nil
}

// ListGrabsForReleaseIDs returns every grab that named one of the cached
// releases, newest first. The interactive picker uses this to mark a row as
// already downloading or already imported.
func (s *Store) ListGrabsForReleaseIDs(ctx context.Context, ids []int64) ([]core.Grab, error) {
	if len(ids) == 0 {
		return []core.Grab{}, nil
	}
	byID := make(map[int64]core.Grab)
	for start := 0; start < len(ids); start += sqliteIDQueryBatchSize {
		end := min(start+sqliteIDQueryBatchSize, len(ids))
		batch := ids[start:end]
		args := make([]any, 0, len(batch))
		for _, id := range batch {
			args = append(args, id)
		}
		query := "SELECT " + grabColumns + " FROM grabs WHERE release_id IN (" +
			placeholders(len(batch)) + ") ORDER BY id DESC"
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("store: list grabs for releases: %w", err)
		}
		for rows.Next() {
			g, err := scanGrab(rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: scan grab for release: %w", err)
			}
			byID[g.GrabID] = *g
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("store: list grabs for releases: %w", err)
		}
	}
	out := make([]core.Grab, 0, len(byID))
	for _, g := range byID {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].GrabID > out[j].GrabID
	})
	return out, nil
}

// ListGrabsForSeriesIDs returns grabbed rows targeting any of the series.
// The series shelf uses it to mark a poster as downloading.
func (s *Store) ListGrabsForSeriesIDs(ctx context.Context, ids []int64) ([]core.Grab, error) {
	if len(ids) == 0 {
		return []core.Grab{}, nil
	}
	byID := make(map[int64]core.Grab)
	for start := 0; start < len(ids); start += sqliteIDQueryBatchSize {
		end := min(start+sqliteIDQueryBatchSize, len(ids))
		batch := ids[start:end]
		args := []any{core.GrabStatusGrabbed}
		for _, id := range batch {
			args = append(args, id)
		}
		query := "SELECT " + grabColumns + " FROM grabs WHERE status = ? AND series_id IN (" +
			placeholders(len(batch)) + ") ORDER BY id DESC"
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return nil, fmt.Errorf("store: list grabs for series: %w", err)
		}
		for rows.Next() {
			g, err := scanGrab(rows)
			if err != nil {
				rows.Close()
				return nil, fmt.Errorf("store: scan grab for series: %w", err)
			}
			byID[g.GrabID] = *g
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("store: list grabs for series: %w", err)
		}
	}
	out := make([]core.Grab, 0, len(byID))
	for _, g := range byID {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].GrabID > out[j].GrabID
	})
	return out, nil
}

// ListGrabs returns the most recent grabs, newest first. A limit of zero or
// less returns every grab. Ordering is by id for the same reason as events:
// ids are monotonic where a timestamp can tie.
func (s *Store) ListGrabs(ctx context.Context, limit int) ([]core.Grab, error) {
	models := []grabModel{}
	query := s.db.NewSelect().Model(&models).Order("id DESC")
	if limit > 0 {
		query.Limit(limit)
	}
	if err := query.Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list grabs: %w", err)
	}
	out := make([]core.Grab, 0, len(models))
	for _, model := range models {
		g, err := model.core()
		if err != nil {
			return nil, fmt.Errorf("store: decode grab %d: %w", model.ID, err)
		}
		out = append(out, g)
	}
	return out, nil
}

// ListCalendarGrabs returns grabbed rows targeting the supplied calendar
// movie or episode IDs, newest first.
func (s *Store) ListCalendarGrabs(ctx context.Context, movieIDs, episodeIDs []int64) ([]core.Grab, error) {
	if len(movieIDs) == 0 && len(episodeIDs) == 0 {
		return []core.Grab{}, nil
	}

	byID := make(map[int64]core.Grab)
	list := func(movieBatch, episodeBatch []int64) error {
		targets := make([]string, 0, 2)
		args := []any{core.GrabStatusGrabbed}
		if len(movieBatch) > 0 {
			targets = append(targets, "movie_id IN ("+placeholders(len(movieBatch))+")")
			for _, id := range movieBatch {
				args = append(args, id)
			}
		}
		if len(episodeBatch) > 0 {
			targets = append(targets,
				"EXISTS (SELECT 1 FROM json_each(grabs.episode_ids) WHERE value IN ("+
					placeholders(len(episodeBatch))+"))")
			for _, id := range episodeBatch {
				args = append(args, id)
			}
		}

		query := "SELECT " + grabColumns + " FROM grabs WHERE status = ? AND (" +
			strings.Join(targets, " OR ") + ") ORDER BY id DESC"
		rows, err := s.db.QueryContext(ctx, query, args...)
		if err != nil {
			return fmt.Errorf("store: list calendar grabs: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			g, err := scanGrab(rows)
			if err != nil {
				return fmt.Errorf("store: scan calendar grab: %w", err)
			}
			byID[g.GrabID] = *g
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("store: list calendar grabs: %w", err)
		}
		return nil
	}
	for start := 0; start < len(movieIDs); start += sqliteIDQueryBatchSize {
		end := min(start+sqliteIDQueryBatchSize, len(movieIDs))
		if err := list(movieIDs[start:end], nil); err != nil {
			return nil, err
		}
	}
	for start := 0; start < len(episodeIDs); start += sqliteIDQueryBatchSize {
		end := min(start+sqliteIDQueryBatchSize, len(episodeIDs))
		if err := list(nil, episodeIDs[start:end]); err != nil {
			return nil, err
		}
	}

	out := make([]core.Grab, 0, len(byID))
	for _, g := range byID {
		out = append(out, g)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].GrabID > out[j].GrabID
	})
	return out, nil
}

// SetGrabStatus records the outcome of a grab. Updating an absent grab is
// ErrNotFound.
func (s *Store) SetGrabStatus(ctx context.Context, id int64, status, reason string) error {
	res, err := s.db.NewUpdate().Model((*grabModel)(nil)).
		Set("status = ?", status).Set("reason = ?", reason).Where("id = ?", id).Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: set status of grab %d: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: set status of grab %d: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("store: set status of grab %d: %w", id, ErrNotFound)
	}
	return nil
}

func scanGrab(sc scanner) (*core.Grab, error) {
	var (
		g          core.Grab
		episodeIDs string
		createdAt  string
		libraryID  sql.NullInt64
	)
	err := sc.Scan(&g.GrabID, &g.ReleaseID, &g.MovieID, &g.SeriesID, &g.SeasonNum,
		&episodeIDs, &g.ReleaseTitle, &g.Reason, &g.Status, &createdAt, &libraryID)
	if err != nil {
		return nil, err
	}
	g.LibraryID = libraryID.Int64
	if episodeIDs != "" {
		if err := json.Unmarshal([]byte(episodeIDs), &g.EpisodeIDs); err != nil {
			return nil, fmt.Errorf("decode episode ids of grab %d: %w", g.GrabID, err)
		}
	}
	g.CreatedAt = parseTime(createdAt)
	return &g, nil
}
