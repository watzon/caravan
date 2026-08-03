package core

import (
	"context"
	"errors"
	"time"
)

// ErrNoMetadataProvider reports that an operation needed a metadata provider
// and none is configured — in practice, no TMDB API key has been entered yet.
//
// It is a sentinel in core rather than an error string in each package because
// every layer treats it the same way: SPEC §13 makes a missing provider a
// visible, recoverable condition (files park in the unmatched queue, the UI
// says so) rather than a failure.
var ErrNoMetadataProvider = errors.New("core: no metadata provider configured")

// MetadataProvider is the metadata source Caravan matches library items
// against (TMDB in v1, SPEC §4). It is the seam that keeps the library and
// scanner packages testable without network access.
type MetadataProvider interface {
	// SearchMovies returns movie candidates for a free-text query, best match
	// first.
	SearchMovies(ctx context.Context, q string) ([]MovieMeta, error)
	// SearchSeries returns series candidates for a free-text query, best match
	// first.
	SearchSeries(ctx context.Context, q string) ([]SeriesMeta, error)
	// GetMovie returns full details for one movie by provider id.
	GetMovie(ctx context.Context, tmdbID int64) (*MovieMeta, error)
	// GetSeries returns full details for one series by provider id, including
	// its seasons and episodes.
	GetSeries(ctx context.Context, tmdbID int64) (*SeriesMeta, error)
}

// MovieMeta is provider-side movie metadata, before it becomes a library
// Movie.
type MovieMeta struct {
	TMDBID        int64
	IMDBID        string
	Title         string
	OriginalTitle string
	Year          int
	Overview      string
	// ReleaseDate is the theatrical release date, zero when the provider did
	// not supply one.
	ReleaseDate time.Time
	// DigitalRelease and PhysicalRelease are the home-release dates, zero when
	// unknown. Search results never carry them; GetMovie does.
	DigitalRelease  time.Time
	PhysicalRelease time.Time
	// PosterURL is an absolute provider URL, not a storage-root-relative path:
	// it is what the organizer downloads from, not what it writes.
	PosterURL string
}

// SeriesMeta is provider-side series metadata, before it becomes a library
// Series. Seasons is populated by GetSeries and typically empty on search
// results.
type SeriesMeta struct {
	TMDBID        int64
	TVDBID        int64
	IMDBID        string
	Title         string
	OriginalTitle string
	Year          int
	Overview      string
	// Status is the provider's series status ("Continuing", "Ended", …).
	Status string
	// FirstAirDate is zero when the provider did not supply one.
	FirstAirDate time.Time
	// PosterURL is an absolute provider URL (see MovieMeta.PosterURL).
	PosterURL string
	Seasons   []SeasonMeta
}

// SeasonMeta is provider-side season metadata. Number 0 is the specials
// season. Episodes is populated by GetSeries.
type SeasonMeta struct {
	Number   int
	Title    string
	Overview string
	// AirDate is zero when the provider did not supply one.
	AirDate time.Time
	// PosterURL is an absolute provider URL (see MovieMeta.PosterURL).
	PosterURL string
	Episodes  []EpisodeMeta
}

// EpisodeMeta is provider-side episode metadata.
type EpisodeMeta struct {
	TMDBID   int64
	Season   int
	Number   int
	Title    string
	Overview string
	// AirDate is zero when the episode is unaired or the provider had no date.
	AirDate time.Time
}
