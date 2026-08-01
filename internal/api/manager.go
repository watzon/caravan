package api

import (
	"context"

	"github.com/watzon/caravan/internal/core"
)

// Manager is the slice of the library manager (internal/library) the HTTP
// layer needs: everything that touches the filesystem or a metadata provider.
// Read-only endpoints go straight to the store instead.
//
// It is declared here, as an interface, rather than taking *library.Manager
// directly so this package compiles and tests without the filesystem half of
// the application. *library.Manager is expected to satisfy it; where a
// signature differs, the wiring in cmd/caravan adapts.
type Manager interface {
	// Scan reconciles the database with the storage root. The API triggers it
	// in the background and reports progress through events, so the scan's
	// own result value is deliberately not part of this interface.
	Scan(ctx context.Context) error

	// AddMovie adds a movie to the library by provider id, fetching its
	// metadata. It returns the stored movie.
	AddMovie(ctx context.Context, tmdbID int64) (*core.Movie, error)

	// AddSeries adds a series (with its seasons and episodes) by provider id.
	AddSeries(ctx context.Context, tmdbID int64) (*core.Series, error)

	// MatchUnmatched resolves a file parked in the scan-review queue against a
	// provider id and imports it. mediaType is MediaTypeMovie or
	// MediaTypeSeries; for a series, the season and episode numbers come from
	// the parked file's parsed guess.
	MatchUnmatched(ctx context.Context, unmatchedID int64, mediaType string, tmdbID int64) error

	// Metadata returns the configured metadata provider, or nil when none is
	// configured (no TMDB API key yet). The search endpoint reports that as a
	// 503 rather than pretending there are no results.
	Metadata() core.MetadataProvider
}

// Media types accepted by POST /import/queue/{id}/match and reported by
// GET /search.
const (
	MediaTypeMovie  = "movie"
	MediaTypeSeries = "series"
)
