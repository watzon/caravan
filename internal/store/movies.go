package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const movieColumns = `id, provider, provider_ref, tmdb_id, imdb_id, title, sort_title, year, overview,
	path, poster_path, poster_url, monitored, quality_profile_id, release_date,
	digital_release, physical_release, min_availability, added_at, updated_at,
	library_id`

// UpsertMovie inserts or updates m and writes back the assigned ID.
//
// Identity is m.ID when set, otherwise the provider ref, otherwise m.TMDBID. A
// movie with none of the three is always inserted: an unmatched movie has no
// stable identity to collapse on.
//
// The ref rung comes FIRST and the tmdb_id rung is the compatibility alias
// behind it. Reversed, a re-fetched TMDB movie would match on tmdb_id, and any
// movie identified by a provider that writes no tmdb_id would match on nothing
// and insert a duplicate on every refresh.
func (s *Store) UpsertMovie(ctx context.Context, m *core.Movie) error {
	normalizeMovieProvider(m)

	if m.ID == 0 && m.ProviderRef != "" {
		existing, err := s.GetMovieByProviderRef(ctx, m.Provider, m.ProviderRef)
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

	if m.MinAvailability == "" {
		m.MinAvailability = core.AvailabilityReleased
	}

	if m.ID != 0 {
		// library_id 0 keeps the stored value: a caller that rebuilt the
		// struct from provider metadata has not decided the movie moves, and a
		// rescan must never move an item between libraries. A move names its
		// target explicitly.
		res, err := s.db.ExecContext(ctx, `
			UPDATE movies SET provider = ?, provider_ref = ?, tmdb_id = ?, imdb_id = ?,
				title = ?, sort_title = ?, year = ?,
				overview = ?, path = ?, poster_path = ?, poster_url = ?, monitored = ?,
				quality_profile_id = ?, release_date = ?, digital_release = ?,
				physical_release = ?, min_availability = ?, added_at = ?, updated_at = ?,
				library_id = COALESCE(NULLIF(?, 0), library_id)
			WHERE id = ?`,
			m.Provider, m.ProviderRef, m.TMDBID, m.IMDBID, m.Title, m.SortTitle, m.Year,
			m.Overview, m.Path, m.PosterPath,
			m.PosterURL, m.Monitored, m.QualityProfileID, formatTime(m.ReleaseDate),
			formatTime(m.DigitalRelease), formatTime(m.PhysicalRelease), m.MinAvailability,
			formatTime(m.AddedAt), formatTime(m.UpdatedAt), m.LibraryID, m.ID)
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
		INSERT INTO movies (provider, provider_ref, tmdb_id, imdb_id, title, sort_title,
			year, overview, path,
			poster_path, poster_url, monitored, quality_profile_id, release_date,
			digital_release, physical_release, min_availability, added_at, updated_at,
			library_id)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		m.Provider, m.ProviderRef, m.TMDBID, m.IMDBID, m.Title, m.SortTitle, m.Year,
		m.Overview, m.Path, m.PosterPath,
		m.PosterURL, m.Monitored, m.QualityProfileID, formatTime(m.ReleaseDate),
		formatTime(m.DigitalRelease), formatTime(m.PhysicalRelease), m.MinAvailability,
		formatTime(m.AddedAt), formatTime(m.UpdatedAt), m.LibraryID)
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

// normalizeMovieProvider derives the provider identity from the legacy TMDB id
// when the caller supplied none — the same move UpsertSeries makes for Kind,
// and for the same reason.
//
// It is what makes "every matched row carries a ref" a property of the table
// rather than a habit of its callers: a caller written before 0024 still lands
// a row the ref lookups can find.
func normalizeMovieProvider(m *core.Movie) {
	if m.Provider == "" && m.TMDBID != 0 {
		ref := core.TMDBRef(m.TMDBID)
		m.Provider, m.ProviderRef = ref.Provider, ref.Ref
	}
}

// GetMovieByProviderRef returns the movie one provider identified by ref, or
// ErrNotFound. A blank ref matches nothing rather than matching every
// unidentified row — "" is precisely the value the partial unique index
// excludes (GetSeriesByStashID's rule, generalized).
func (s *Store) GetMovieByProviderRef(ctx context.Context, provider, ref string) (*core.Movie, error) {
	if provider == "" || ref == "" {
		return nil, fmt.Errorf("store: movie %s/%s: %w", provider, ref, ErrNotFound)
	}
	row := s.db.QueryRowContext(ctx, "SELECT "+movieColumns+
		" FROM movies WHERE provider = ? AND provider_ref = ?", provider, ref)
	m, err := scanMovie(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: movie %s/%s: %w", provider, ref, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get movie %s/%s: %w", provider, ref, err)
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

// MovieIDsByTMDBID maps the given provider ids onto library ids, omitting the
// ones that are not in the library. It exists for the discover screens, which
// decorate a page of provider results with "already yours" and would otherwise
// either load the whole library or issue a query per row.
func (s *Store) MovieIDsByTMDBID(ctx context.Context, tmdbIDs []int64) (map[int64]int64, error) {
	return s.idsByTMDBID(ctx, "movies", tmdbIDs)
}

// idsByTMDBID is the shared body of MovieIDsByTMDBID and SeriesIDsByTMDBID.
// table is a package-internal literal, never caller input.
func (s *Store) idsByTMDBID(ctx context.Context, table string, tmdbIDs []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	if len(tmdbIDs) == 0 {
		return out, nil
	}
	args := make([]any, 0, len(tmdbIDs))
	for _, id := range tmdbIDs {
		args = append(args, id)
	}

	rows, err := s.db.QueryContext(ctx,
		"SELECT tmdb_id, id FROM "+table+" WHERE tmdb_id IN ("+placeholders(len(tmdbIDs))+")", args...)
	if err != nil {
		return nil, fmt.Errorf("store: %s ids by tmdb id: %w", table, err)
	}
	defer rows.Close()

	for rows.Next() {
		var tmdbID, id int64
		if err := rows.Scan(&tmdbID, &id); err != nil {
			return nil, fmt.Errorf("store: scan %s id: %w", table, err)
		}
		out[tmdbID] = id
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: %s ids by tmdb id: %w", table, err)
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
	return scanMovieWith(sc)
}
