package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const grabColumns = `id, release_id, movie_id, series_id, season_number, episode_ids,
	release_title, reason, status, created_at`

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

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO grabs (release_id, movie_id, series_id, season_number, episode_ids,
			release_title, reason, status, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		g.ReleaseID, g.MovieID, g.SeriesID, g.SeasonNum, string(episodeIDs),
		g.ReleaseTitle, g.Reason, g.Status, formatTime(g.CreatedAt))
	if err != nil {
		return fmt.Errorf("store: insert grab of %q: %w", g.ReleaseTitle, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: insert grab of %q: %w", g.ReleaseTitle, err)
	}
	g.GrabID = id
	return nil
}

// GetGrab returns the grab with the given id, or ErrNotFound.
func (s *Store) GetGrab(ctx context.Context, id int64) (*core.Grab, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+grabColumns+" FROM grabs WHERE id = ?", id)
	g, err := scanGrab(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: grab %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get grab %d: %w", id, err)
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

// ListGrabs returns the most recent grabs, newest first. A limit of zero or
// less returns every grab. Ordering is by id for the same reason as events:
// ids are monotonic where a timestamp can tie.
func (s *Store) ListGrabs(ctx context.Context, limit int) ([]core.Grab, error) {
	query := "SELECT " + grabColumns + " FROM grabs ORDER BY id DESC"
	args := []any{}
	if limit > 0 {
		query += " LIMIT ?"
		args = append(args, limit)
	}

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list grabs: %w", err)
	}
	defer rows.Close()

	out := []core.Grab{}
	for rows.Next() {
		g, err := scanGrab(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan grab: %w", err)
		}
		out = append(out, *g)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list grabs: %w", err)
	}
	return out, nil
}

// SetGrabStatus records the outcome of a grab. Updating an absent grab is
// ErrNotFound.
func (s *Store) SetGrabStatus(ctx context.Context, id int64, status, reason string) error {
	res, err := s.db.ExecContext(ctx,
		"UPDATE grabs SET status = ?, reason = ? WHERE id = ?", status, reason, id)
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
	)
	err := sc.Scan(&g.GrabID, &g.ReleaseID, &g.MovieID, &g.SeriesID, &g.SeasonNum,
		&episodeIDs, &g.ReleaseTitle, &g.Reason, &g.Status, &createdAt)
	if err != nil {
		return nil, err
	}
	if episodeIDs != "" {
		if err := json.Unmarshal([]byte(episodeIDs), &g.EpisodeIDs); err != nil {
			return nil, fmt.Errorf("decode episode ids of grab %d: %w", g.GrabID, err)
		}
	}
	g.CreatedAt = parseTime(createdAt)
	return &g, nil
}
