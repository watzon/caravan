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
//
// minAvailability is the release stage the movie's automatic search waits for
// (a core.Availability* constant). Empty means "no opinion": a new row gets
// the released default, an existing row keeps whatever it has.
//
// monitored is the "Add and monitor" choice and follows monitoredOrDefault:
// nil means unmonitored, and it decides a new row only.
//
// libraryID is the add's target library; 0 means the default movie library.
// It decides a NEW row only — re-adding a movie refreshes its metadata in the
// library it already lives in, because a move is an explicit operation, never
// a side effect of an add (see libraries.go).
//
// ref names the provider that identified the title AND that provider's own id
// for it. The provider comes from the ref rather than from the library,
// because the ref is the only thing that knows which vocabulary its id is
// written in (see providerByID).
func (m *Manager) AddMovie(ctx context.Context, ref core.ItemRef, minAvailability string, monitored *bool, libraryID int64) (*core.Movie, error) {
	if !ref.Valid() {
		return nil, fmt.Errorf("library: invalid provider ref %q/%q", ref.Provider, ref.Ref)
	}
	// The library first, so the destination path is built from it.
	lib, err := m.movieLibrary(ctx, ref, "", libraryID)
	if err != nil {
		return nil, err
	}
	provider := m.providerByID(ctx, ref.Provider)
	if provider == nil {
		return nil, core.ErrNoMetadataProvider
	}

	meta, err := provider.GetMovie(ctx, ref.Ref)
	if err != nil {
		return nil, fmt.Errorf("library: get movie %s/%s: %w", ref.Provider, ref.Ref, err)
	}
	if meta == nil {
		return nil, fmt.Errorf("library: movie %s/%s not found", ref.Provider, ref.Ref)
	}

	mv, _, err := m.upsertMovieRow(ctx, meta, m.movieDir(lib, meta.Title, meta.Year), "", minAvailability, monitored, lib.ID)
	return mv, err
}

// AddSeries adds a series and its full season/episode tree by provider id.
//
// The whole tree lands even though no episode is on disk: that is what makes
// the series view able to show what is missing (see upsertSeriesTree). Like
// AddMovie it touches no files.
//
// monitored is the series-level "Add and monitor" choice (monitoredOrDefault).
// It is the flag that gates the wanted list, and it is the only one this
// touches: the season and episode rows the tree writes keep the monitored
// semantics they have always had, because unmonitoring a whole tree row by row
// would be a different decision from not following the series.
//
// ref reads exactly as AddMovie's does.
func (m *Manager) AddSeries(ctx context.Context, ref core.ItemRef, monitored *bool, libraryID int64) (*core.Series, error) {
	if !ref.Valid() {
		return nil, fmt.Errorf("library: invalid provider ref %q/%q", ref.Provider, ref.Ref)
	}
	lib, err := m.seriesLibrary(ctx, ref, "", libraryID)
	if err != nil {
		return nil, err
	}
	provider := m.providerByID(ctx, ref.Provider)
	if provider == nil {
		return nil, core.ErrNoMetadataProvider
	}

	meta, err := provider.GetSeries(ctx, ref.Ref)
	if err != nil {
		return nil, fmt.Errorf("library: get series %s/%s: %w", ref.Provider, ref.Ref, err)
	}
	if meta == nil {
		return nil, fmt.Errorf("library: series %s/%s not found", ref.Provider, ref.Ref)
	}

	sr, _, err := m.upsertSeriesRow(ctx, meta, core.SeriesKindForLibrary(lib.Kind),
		m.seriesDir(lib, meta.Title, meta.Year), "", monitored, lib.ID)
	if err != nil {
		return nil, err
	}
	if err := m.upsertSeriesTree(ctx, sr, meta); err != nil {
		return nil, err
	}
	return sr, nil
}
