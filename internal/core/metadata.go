package core

import (
	"context"
	"errors"
	"strconv"
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

// ErrMetadataUnauthorized reports that a metadata provider rejected the
// credential it was given — a wrong, revoked or suspended TMDB API key.
//
// It is a sentinel in core, and every provider-side unauthorized error wraps
// it, so the layers above can tell "the key is wrong" from "the upstream is
// having a bad day" without importing the provider package. That distinction
// is the whole of the credential-health model (PLAN phase 10 task 2): only a
// rejected credential marks the cached state invalid; a timeout does not.
var ErrMetadataUnauthorized = errors.New("core: metadata credential rejected")

// ErrProviderKindUnsupported reports that a provider was asked for a media
// kind it does not serve — TMDB for an adult site, stash-box for a film.
//
// A chain walker SKIPS such a provider rather than failing on it: this is the
// registry's Kinds list restated at the call site, not a failure. Nothing went
// wrong, this rung of the chain simply has nothing to say.
var ErrProviderKindUnsupported = errors.New("core: provider does not serve this media kind")

// ItemRef is the identity of a provider-side title: which provider answered,
// and that provider's own id for it.
type ItemRef struct {
	Provider string
	Ref      string
}

// TMDBRef is the ItemRef for a TMDB numeric id. A zero or negative id is not
// an identity at all — it is the absence of one — so it yields the zero
// ItemRef rather than a ref pointing at "0".
func TMDBRef(id int64) ItemRef {
	if id <= 0 {
		return ItemRef{}
	}
	return ItemRef{Provider: ProviderTMDB, Ref: strconv.FormatInt(id, 10)}
}

// Valid reports whether the ref names both a provider and an id. Either half
// alone identifies nothing.
func (r ItemRef) Valid() bool {
	return r.Provider != "" && r.Ref != ""
}

// TMDBID returns the ref's numeric TMDB id, or 0 when it belongs to another
// provider or does not parse. It exists for the callers that still key on the
// int64 — NFO uniqueids, discover decoration, the requests table.
func (r ItemRef) TMDBID() int64 {
	if r.Provider != ProviderTMDB {
		return 0
	}
	id, err := strconv.ParseInt(r.Ref, 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// MetadataProvider is the metadata source Caravan matches library items
// against (TMDB and stash-box, SPEC §4). It is the seam that keeps the library
// and scanner packages testable without network access.
//
// The Get* methods take a provider-native ref, not a shared id space: the
// whole point of the seam is that a provider's vocabulary stops here. TMDB's
// refs are its numeric ids written as strings; stash-box's are UUIDs.
type MetadataProvider interface {
	// SearchMovies returns movie candidates for a free-text query, best match
	// first.
	SearchMovies(ctx context.Context, q string) ([]MovieMeta, error)
	// SearchSeries returns series candidates for a free-text query, best match
	// first.
	SearchSeries(ctx context.Context, q string) ([]SeriesMeta, error)
	// GetMovie returns full details for one movie by this provider's own ref.
	GetMovie(ctx context.Context, ref string) (*MovieMeta, error)
	// GetSeries returns full details for one series by this provider's own
	// ref, including its seasons and episodes.
	GetSeries(ctx context.Context, ref string) (*SeriesMeta, error)
}

// MovieMeta is provider-side movie metadata, before it becomes a library
// Movie.
type MovieMeta struct {
	// Provider and ProviderRef are the identity of the record: which provider
	// answered, and its own id for it. TMDBID stays alongside them because the
	// NFO uniqueids, discover decoration and the requests table all key on it.
	Provider      string
	ProviderRef   string
	TMDBID        int64
	IMDBID        string
	Title         string
	OriginalTitle string
	Year          int
	Overview      string
	VoteAverage   float64
	VoteCount     int
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

// Ref is the movie's provider-side identity.
func (m MovieMeta) Ref() ItemRef {
	return ItemRef{Provider: m.Provider, Ref: m.ProviderRef}
}

// SeriesMeta is provider-side series metadata, before it becomes a library
// Series. Seasons is populated by GetSeries and typically empty on search
// results.
type SeriesMeta struct {
	// Provider and ProviderRef are the identity of the record; see
	// MovieMeta for why the legacy id fields below survive alongside them.
	Provider      string
	ProviderRef   string
	TMDBID        int64
	TVDBID        int64
	IMDBID        string
	Title         string
	OriginalTitle string
	Year          int
	Overview      string
	VoteAverage   float64
	VoteCount     int
	// Status is the provider's series status ("Continuing", "Ended", …).
	Status string
	// FirstAirDate is zero when the provider did not supply one.
	FirstAirDate time.Time
	// PosterURL is an absolute provider URL (see MovieMeta.PosterURL).
	PosterURL string
	Seasons   []SeasonMeta
}

// Ref is the series' provider-side identity.
func (s SeriesMeta) Ref() ItemRef {
	return ItemRef{Provider: s.Provider, Ref: s.ProviderRef}
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
