package library

import (
	"context"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

// RefreshResult summarizes one metadata refresh sweep.
type RefreshResult struct {
	// Movies and Series count the titles whose metadata was re-fetched and
	// written back.
	Movies int
	Series int
	// Sites counts adult series (sites) whose catalogue was re-walked. It is
	// separate from Series because the two used different providers, and a
	// single number would hide a stash-box sweep that did nothing.
	Sites int
	// Errors holds per-title provider failures. The sweep continues past all
	// of them: one title TMDB cannot answer for must not stop the other two
	// hundred from getting their dates.
	Errors []string
}

func (r *RefreshResult) addErr(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

// RefreshLibrary re-fetches provider metadata for every monitored title and
// writes it back through the same upserts a rescan uses, so the
// preserve-user-intent rules hold: monitored flags, profile assignments,
// minimum availability and season selections all survive.
//
// This is the only path that revisits a title with no files: the scan walks
// the filesystem, so a wanted-but-absent movie's release dates and an unaired
// series' season list are otherwise frozen at add time — and the
// minimum-availability gate (internal/wanted) judges against those dates.
//
// Unmonitored titles are skipped: nothing downstream acts on their metadata,
// and the sweep's cost is provider round trips.
//
// Every title is re-fetched through the provider that identified it, by that
// provider's own ref (see providerByID). There is no Manager-level gate in
// front of the sweep: an install whose items are all pinned to some other
// provider has no TMDB client and still has a library to refresh, and a title
// whose provider is not configured is one recorded error rather than a dead
// sweep.
func (m *Manager) RefreshLibrary(ctx context.Context) (*RefreshResult, error) {
	res := &RefreshResult{}

	movies, err := m.store.ListMovies(ctx)
	if err != nil {
		return nil, err
	}
	for _, mv := range movies {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if !mv.Monitored || mv.ProviderRef == "" {
			continue
		}
		provider := m.providerByID(ctx, mv.Provider)
		if provider == nil {
			res.addErr("refresh movie %q: provider %q is not configured", mv.Title, mv.Provider)
			continue
		}
		meta, err := provider.GetMovie(ctx, mv.ProviderRef)
		if err != nil {
			res.addErr("refresh movie %q: %v", mv.Title, err)
			continue
		}
		if meta == nil {
			continue
		}
		// The row's own path, not a recomputed one: the folder on disk is
		// ground truth, and a provider retitle must not point the row at a
		// directory that does not exist.
		if _, _, err := m.upsertMovieRow(ctx, meta, mv.Path, "", "", nil, mv.LibraryID); err != nil {
			return res, err
		}
		res.Movies++
	}

	// Television and anime series: both are refreshed through the ordinary
	// metadata seam, by the provider each row is pinned to, so the two sweep
	// identically and an anime row's episode list stays as current as a
	// television one's. An adult series is refreshed from stash-box instead, and
	// asking a metadata provider about a site would be both wrong and a request
	// the module's switch is supposed to be able to stop — so the adult kind is
	// absent here and swept by adult.go.
	var series []core.Series
	for _, kind := range []string{core.SeriesKindTV, core.SeriesKindAnime} {
		rows, err := m.store.ListSeriesByKind(ctx, kind)
		if err != nil {
			return nil, err
		}
		series = append(series, rows...)
	}
	for _, sr := range series {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if !sr.Monitored || sr.ProviderRef == "" {
			continue
		}
		provider := m.providerByID(ctx, sr.Provider)
		if provider == nil {
			res.addErr("refresh series %q: provider %q is not configured", sr.Title, sr.Provider)
			continue
		}
		meta, err := provider.GetSeries(ctx, sr.ProviderRef)
		if err != nil {
			res.addErr("refresh series %q: %v", sr.Title, err)
			continue
		}
		if meta == nil {
			continue
		}
		row, _, err := m.upsertSeriesRow(ctx, meta, sr.Kind, sr.Path, "", nil, sr.LibraryID)
		if err != nil {
			return res, err
		}
		// The whole tree, so a season the provider just announced lands as
		// rows the wanted list can see once its episodes air.
		if err := m.upsertSeriesTree(ctx, row, meta); err != nil {
			return res, err
		}
		res.Series++
	}

	// The adult sweep runs last and answers for itself: it is a no-op, and
	// makes no request at all, when the module is off or no stash-box
	// credential is configured (see refreshSites).
	if err := m.refreshSites(ctx, res); err != nil {
		return res, err
	}
	return res, nil
}
