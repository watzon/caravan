package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const seriesColumns = `id, tmdb_id, tvdb_id, imdb_id, title, sort_title, year, overview,
	status, path, poster_path, poster_url, monitored, quality_profile_id, first_aired, added_at, updated_at`

// UpsertSeries inserts or updates sr and writes back the assigned ID.
// Identity is sr.ID when set, otherwise sr.TMDBID.
func (s *Store) UpsertSeries(ctx context.Context, sr *core.Series) error {
	if sr.ID == 0 && sr.TMDBID != 0 {
		existing, err := s.GetSeriesByTMDBID(ctx, sr.TMDBID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil {
			sr.ID = existing.ID
			if sr.AddedAt.IsZero() {
				sr.AddedAt = existing.AddedAt
			}
		}
	}

	ts := now()
	if sr.AddedAt.IsZero() {
		sr.AddedAt = ts
	}
	sr.UpdatedAt = ts

	if sr.ID != 0 {
		res, err := s.db.ExecContext(ctx, `
			UPDATE series SET tmdb_id = ?, tvdb_id = ?, imdb_id = ?, title = ?, sort_title = ?,
				year = ?, overview = ?, status = ?, path = ?, poster_path = ?, poster_url = ?,
				monitored = ?, quality_profile_id = ?, first_aired = ?, added_at = ?, updated_at = ?
			WHERE id = ?`,
			sr.TMDBID, sr.TVDBID, sr.IMDBID, sr.Title, sr.SortTitle, sr.Year, sr.Overview,
			sr.Status, sr.Path, sr.PosterPath, sr.PosterURL, sr.Monitored, sr.QualityProfileID,
			formatTime(sr.FirstAired), formatTime(sr.AddedAt), formatTime(sr.UpdatedAt), sr.ID)
		if err != nil {
			return fmt.Errorf("store: update series %d: %w", sr.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: update series %d: %w", sr.ID, err)
		}
		if n == 0 {
			return fmt.Errorf("store: update series %d: %w", sr.ID, ErrNotFound)
		}
		return nil
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO series (tmdb_id, tvdb_id, imdb_id, title, sort_title, year, overview,
			status, path, poster_path, poster_url, monitored, quality_profile_id, first_aired,
			added_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sr.TMDBID, sr.TVDBID, sr.IMDBID, sr.Title, sr.SortTitle, sr.Year, sr.Overview,
		sr.Status, sr.Path, sr.PosterPath, sr.PosterURL, sr.Monitored, sr.QualityProfileID,
		formatTime(sr.FirstAired), formatTime(sr.AddedAt), formatTime(sr.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: insert series %q: %w", sr.Title, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: insert series %q: %w", sr.Title, err)
	}
	sr.ID = id
	return nil
}

// GetSeries returns the series with the given id, or ErrNotFound.
func (s *Store) GetSeries(ctx context.Context, id int64) (*core.Series, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+seriesColumns+" FROM series WHERE id = ?", id)
	sr, err := scanSeries(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: series %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get series %d: %w", id, err)
	}
	return sr, nil
}

// GetSeriesByTMDBID returns the series with the given TMDB id, or ErrNotFound.
func (s *Store) GetSeriesByTMDBID(ctx context.Context, tmdbID int64) (*core.Series, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+seriesColumns+" FROM series WHERE tmdb_id = ?", tmdbID)
	sr, err := scanSeries(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: series tmdb %d: %w", tmdbID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get series tmdb %d: %w", tmdbID, err)
	}
	return sr, nil
}

// ListSeries returns every series ordered by sort title then title.
func (s *Store) ListSeries(ctx context.Context) ([]core.Series, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+seriesColumns+" FROM series ORDER BY sort_title, title, year")
	if err != nil {
		return nil, fmt.Errorf("store: list series: %w", err)
	}
	defer rows.Close()

	out := []core.Series{}
	for rows.Next() {
		sr, err := scanSeries(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan series: %w", err)
		}
		out = append(out, *sr)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list series: %w", err)
	}
	return out, nil
}

// DeleteSeries removes the series and, by foreign-key cascade, its seasons,
// episodes, and episode-file links. Files on disk are untouched.
func (s *Store) DeleteSeries(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM series WHERE id = ?", id); err != nil {
		return fmt.Errorf("store: delete series %d: %w", id, err)
	}
	return nil
}

func scanSeries(sc scanner) (*core.Series, error) {
	var (
		sr         core.Series
		firstAired string
		addedAt    string
		updatedAt  string
	)
	err := sc.Scan(&sr.ID, &sr.TMDBID, &sr.TVDBID, &sr.IMDBID, &sr.Title, &sr.SortTitle, &sr.Year,
		&sr.Overview, &sr.Status, &sr.Path, &sr.PosterPath, &sr.PosterURL, &sr.Monitored,
		&sr.QualityProfileID, &firstAired, &addedAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	sr.FirstAired = parseTime(firstAired)
	sr.AddedAt = parseTime(addedAt)
	sr.UpdatedAt = parseTime(updatedAt)
	return &sr, nil
}
