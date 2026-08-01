package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const movieColumns = `id, tmdb_id, imdb_id, title, sort_title, year, overview,
	path, poster_path, poster_url, monitored, quality_profile_id, release_date, added_at, updated_at`

// UpsertMovie inserts or updates m and writes back the assigned ID.
//
// Identity is m.ID when set, otherwise m.TMDBID. A movie with neither is
// always inserted: an unmatched movie has no stable identity to collapse on.
func (s *Store) UpsertMovie(ctx context.Context, m *core.Movie) error {
	if m.ID == 0 && m.TMDBID != 0 {
		existing, err := s.GetMovieByTMDBID(ctx, m.TMDBID)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return err
		}
		if err == nil {
			m.ID = existing.ID
			if m.AddedAt.IsZero() {
				m.AddedAt = existing.AddedAt
			}
		}
	}

	ts := now()
	if m.AddedAt.IsZero() {
		m.AddedAt = ts
	}
	m.UpdatedAt = ts

	if m.ID != 0 {
		res, err := s.db.ExecContext(ctx, `
			UPDATE movies SET tmdb_id = ?, imdb_id = ?, title = ?, sort_title = ?, year = ?,
				overview = ?, path = ?, poster_path = ?, poster_url = ?, monitored = ?,
				quality_profile_id = ?, release_date = ?, added_at = ?, updated_at = ?
			WHERE id = ?`,
			m.TMDBID, m.IMDBID, m.Title, m.SortTitle, m.Year, m.Overview, m.Path, m.PosterPath,
			m.PosterURL, m.Monitored, m.QualityProfileID, formatTime(m.ReleaseDate),
			formatTime(m.AddedAt), formatTime(m.UpdatedAt), m.ID)
		if err != nil {
			return fmt.Errorf("store: update movie %d: %w", m.ID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("store: update movie %d: %w", m.ID, err)
		}
		if n == 0 {
			return fmt.Errorf("store: update movie %d: %w", m.ID, ErrNotFound)
		}
		return nil
	}

	res, err := s.db.ExecContext(ctx, `
		INSERT INTO movies (tmdb_id, imdb_id, title, sort_title, year, overview, path,
			poster_path, poster_url, monitored, quality_profile_id, release_date, added_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.TMDBID, m.IMDBID, m.Title, m.SortTitle, m.Year, m.Overview, m.Path, m.PosterPath,
		m.PosterURL, m.Monitored, m.QualityProfileID, formatTime(m.ReleaseDate), formatTime(m.AddedAt),
		formatTime(m.UpdatedAt))
	if err != nil {
		return fmt.Errorf("store: insert movie %q: %w", m.Title, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("store: insert movie %q: %w", m.Title, err)
	}
	m.ID = id
	return nil
}

// GetMovie returns the movie with the given id, or ErrNotFound.
func (s *Store) GetMovie(ctx context.Context, id int64) (*core.Movie, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+movieColumns+" FROM movies WHERE id = ?", id)
	m, err := scanMovie(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: movie %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get movie %d: %w", id, err)
	}
	return m, nil
}

// GetMovieByTMDBID returns the movie with the given TMDB id, or ErrNotFound.
func (s *Store) GetMovieByTMDBID(ctx context.Context, tmdbID int64) (*core.Movie, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+movieColumns+" FROM movies WHERE tmdb_id = ?", tmdbID)
	m, err := scanMovie(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: movie tmdb %d: %w", tmdbID, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get movie tmdb %d: %w", tmdbID, err)
	}
	return m, nil
}

// ListMovies returns every movie ordered by sort title then title. The slice
// is empty, never nil, on an empty library.
func (s *Store) ListMovies(ctx context.Context) ([]core.Movie, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+movieColumns+" FROM movies ORDER BY sort_title, title, year")
	if err != nil {
		return nil, fmt.Errorf("store: list movies: %w", err)
	}
	defer rows.Close()

	out := []core.Movie{}
	for rows.Next() {
		m, err := scanMovie(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan movie: %w", err)
		}
		out = append(out, *m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list movies: %w", err)
	}
	return out, nil
}

// DeleteMovie removes the movie row. It does not touch files on disk and does
// not remove media_files rows: those are reconciled by a rescan.
func (s *Store) DeleteMovie(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM movies WHERE id = ?", id); err != nil {
		return fmt.Errorf("store: delete movie %d: %w", id, err)
	}
	return nil
}

// scanner is satisfied by both *sql.Row and *sql.Rows.
type scanner interface {
	Scan(dest ...any) error
}

func scanMovie(sc scanner) (*core.Movie, error) {
	var (
		m           core.Movie
		releaseDate string
		addedAt     string
		updatedAt   string
	)
	err := sc.Scan(&m.ID, &m.TMDBID, &m.IMDBID, &m.Title, &m.SortTitle, &m.Year, &m.Overview,
		&m.Path, &m.PosterPath, &m.PosterURL, &m.Monitored, &m.QualityProfileID, &releaseDate,
		&addedAt, &updatedAt)
	if err != nil {
		return nil, err
	}
	m.ReleaseDate = parseTime(releaseDate)
	m.AddedAt = parseTime(addedAt)
	m.UpdatedAt = parseTime(updatedAt)
	return &m, nil
}
