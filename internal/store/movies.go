package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

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

	model := catalogMovieModelFromCore(m)
	if m.ID != 0 {
		// library_id 0 keeps the stored value: a caller that rebuilt the
		// struct from provider metadata has not decided the movie moves, and a
		// rescan must never move an item between libraries. A move names its
		// target explicitly.
		res, err := s.db.NewUpdate().Model(&model).
			Column("provider", "provider_ref", "tmdb_id", "imdb_id", "title", "sort_title", "year",
				"overview", "path", "poster_path", "poster_url", "monitored", "quality_profile_id",
				"release_date", "digital_release", "physical_release", "min_availability", "added_at", "updated_at",
				"library_id").
			Value("library_id", "COALESCE(NULLIF(?, 0), library_id)", m.LibraryID).
			WherePK().Exec(ctx)
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
		s.note("library")
		return nil
	}

	if err := s.db.NewInsert().Model(&model).Returning("id").Scan(ctx); err != nil {
		return fmt.Errorf("store: insert movie %q: %w", m.Title, err)
	}
	m.ID = model.ID
	s.note("library")
	return nil
}

// GetMovie returns the movie with the given id, or ErrNotFound.
func (s *Store) GetMovie(ctx context.Context, id int64) (*core.Movie, error) {
	return s.movie(ctx, s.db.NewSelect().Model((*catalogMovieModel)(nil)).Where("id = ?", id),
		fmt.Sprintf("movie %d", id))
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
	what := fmt.Sprintf("movie %s/%s", provider, ref)
	if provider == "" || ref == "" {
		return nil, fmt.Errorf("store: %s: %w", what, ErrNotFound)
	}
	query := s.db.NewSelect().Model((*catalogMovieModel)(nil)).
		Where("provider = ?", provider).Where("provider_ref = ?", ref)
	return s.movie(ctx, query, what)
}

// GetMovieByTMDBID returns the movie with the given TMDB id, or ErrNotFound.
func (s *Store) GetMovieByTMDBID(ctx context.Context, tmdbID int64) (*core.Movie, error) {
	return s.movie(ctx, s.db.NewSelect().Model((*catalogMovieModel)(nil)).Where("tmdb_id = ?", tmdbID),
		fmt.Sprintf("movie tmdb %d", tmdbID))
}

func (s *Store) movie(ctx context.Context, query *bun.SelectQuery, what string) (*core.Movie, error) {
	var model catalogMovieModel
	if err := query.Model(&model).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("store: %s: %w", what, ErrNotFound)
		}
		return nil, fmt.Errorf("store: get %s: %w", what, err)
	}
	out := model.core()
	return &out, nil
}

// ListMovies returns every movie ordered by sort title then title. The slice
// is empty, never nil, on an empty library.
func (s *Store) ListMovies(ctx context.Context) ([]core.Movie, error) {
	models := make([]catalogMovieModel, 0)
	if err := s.db.NewSelect().Model(&models).
		Order("sort_title ASC", "title ASC", "year ASC").
		Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list movies: %w", err)
	}

	out := make([]core.Movie, 0, len(models))
	for _, model := range models {
		out = append(out, model.core())
	}
	return out, nil
}

// MovieIDsByTMDBID maps the given provider ids onto library ids, omitting the
// ones that are not in the library. It exists for the discover screens, which
// decorate a page of provider results with "already yours" and would otherwise
// either load the whole library or issue a query per row.
func (s *Store) MovieIDsByTMDBID(ctx context.Context, tmdbIDs []int64) (map[int64]int64, error) {
	out := map[int64]int64{}
	if len(tmdbIDs) == 0 {
		return out, nil
	}

	models := make([]catalogMovieModel, 0)
	if err := s.db.NewSelect().Model(&models).Column("tmdb_id", "id").
		Where("tmdb_id IN (?)", bun.In(tmdbIDs)).Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: movies ids by tmdb id: %w", err)
	}
	for _, model := range models {
		out[model.TMDBID] = model.ID
	}
	return out, nil
}

// DeleteMovie removes the movie row. It does not touch files on disk and does
// not remove media_files rows: those are reconciled by a rescan.
func (s *Store) DeleteMovie(ctx context.Context, id int64) error {
	if _, err := s.db.NewDelete().Model((*catalogMovieModel)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("store: delete movie %d: %w", id, err)
	}
	s.note("library")
	return nil
}

// scanner is satisfied by both *sql.Row and *sql.Rows. Joined/aggregate raw
// queries use it for their custom projections.
type scanner interface {
	Scan(dest ...any) error
}
