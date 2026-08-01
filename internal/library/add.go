package library

import (
	"context"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

// AddMovie adds a movie to the library by provider id (SPEC §9 step 2: "add to
// library" creates the wanted item).
//
// Nothing is written to disk: the movie has no file yet, and creating an empty
// folder would make the next scan report a library item Caravan cannot see.
// The folder path is recorded so a later import knows where the file belongs,
// and the poster stays a provider URL until there is a folder to write it into.
//
// Adding a movie that is already in the library refreshes its metadata and
// keeps the user's monitored flag and profile assignment, exactly as a rescan
// does.
func (m *Manager) AddMovie(ctx context.Context, tmdbID int64) (*core.Movie, error) {
	if m.provider == nil {
		return nil, core.ErrNoMetadataProvider
	}
	if tmdbID <= 0 {
		return nil, fmt.Errorf("library: invalid tmdb id %d", tmdbID)
	}

	meta, err := m.provider.GetMovie(ctx, tmdbID)
	if err != nil {
		return nil, fmt.Errorf("library: get movie %d: %w", tmdbID, err)
	}
	if meta == nil {
		return nil, fmt.Errorf("library: movie %d not found", tmdbID)
	}

	return m.upsertMovieRow(ctx, meta, movieDir(meta.Title, meta.Year), "")
}

// AddSeries adds a series and its full season/episode tree by provider id.
//
// The whole tree lands even though no episode is on disk: that is what makes
// the series view able to show what is missing (see upsertSeriesTree). Like
// AddMovie it touches no files.
func (m *Manager) AddSeries(ctx context.Context, tmdbID int64) (*core.Series, error) {
	if m.provider == nil {
		return nil, core.ErrNoMetadataProvider
	}
	if tmdbID <= 0 {
		return nil, fmt.Errorf("library: invalid tmdb id %d", tmdbID)
	}

	meta, err := m.provider.GetSeries(ctx, tmdbID)
	if err != nil {
		return nil, fmt.Errorf("library: get series %d: %w", tmdbID, err)
	}
	if meta == nil {
		return nil, fmt.Errorf("library: series %d not found", tmdbID)
	}

	sr, err := m.upsertSeriesRow(ctx, meta, seriesDir(meta.Title, meta.Year), "")
	if err != nil {
		return nil, err
	}
	if err := m.upsertSeriesTree(ctx, sr.ID, meta); err != nil {
		return nil, err
	}
	return sr, nil
}
