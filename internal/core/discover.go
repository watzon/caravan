package core

import (
	"context"
	"time"
)

// Media types shared by the discover surface, the requests table and the HTTP
// API. They are the values stored in requests.media_type, so changing one is a
// migration, not a rename.
const (
	MediaTypeMovie  = "movie"
	MediaTypeSeries = "series"
)

// DiscoverItem is one title in a browse list: a movie or a series that may or
// may not be in the library yet. It is deliberately thinner than MovieMeta and
// SeriesMeta — a discover row renders a poster, a title and a year, and the
// two media types share every field it needs.
type DiscoverItem struct {
	// MediaType is MediaTypeMovie or MediaTypeSeries.
	MediaType string
	TMDBID    int64
	Title     string
	Year      int
	Overview  string
	// PosterPath is the provider's own path ("/abc.jpg"). It is kept
	// alongside PosterURL because a request row stores the path, not the
	// rendered URL: the CDN prefix is a client concern that may change.
	PosterPath string
	// PosterURL and BackdropURL are absolute provider URLs, empty when the
	// provider has no artwork.
	PosterURL   string
	BackdropURL string
	VoteAverage float64
	// Date is the release date for a movie and the first air date for a
	// series, zero when the provider has none.
	Date time.Time
}

// DiscoverPage is one page of a paged browse query. TotalPages is the
// provider's own count, which is what the UI pages against.
type DiscoverPage struct {
	Page       int
	TotalPages int
	Items      []DiscoverItem
}

// CastMember is one billed performer on a title's detail screen.
type CastMember struct {
	TMDBID     int64
	Name       string
	Character  string
	ProfileURL string
}

// DiscoverSeason is a season as the provider describes it, before anything is
// known about what the library holds. EpisodeCount is the provider's count,
// which is what "3 of 10 episodes" is measured against.
type DiscoverSeason struct {
	Number       int
	Title        string
	Overview     string
	PosterURL    string
	AirDate      time.Time
	EpisodeCount int
}

// TitleDetail is everything the discover detail screen shows for one title:
// the item itself plus its cast, its recommendations and its external ids.
// Seasons is populated for series only.
type TitleDetail struct {
	DiscoverItem
	// Status is the provider's series status ("Ended", "Returning Series");
	// empty for movies, which have no equivalent.
	Status string
	// Runtime is minutes: the feature length for a movie, the typical episode
	// length for a series.
	Runtime int
	// Network is who made it under the name each media type uses for that: the
	// originating network for a series, the lead production company for a
	// movie. One field rather than two because exactly one applies per title,
	// the same way Date is a release date or a first air date.
	Network string
	// LastAired is a series' most recent air date, zero for movies and for a
	// series that has not aired yet. DiscoverItem.Date holds the other end.
	LastAired time.Time
	// Language is the provider's ISO 639-1 original-language code ("en"),
	// empty when it has none. It is the code rather than a display name
	// because naming a language is the client's job and depends on its locale.
	Language        string
	Genres          []string
	IMDBID          string
	TVDBID          int64
	Cast            []CastMember
	Recommendations []DiscoverItem
	Seasons         []DiscoverSeason
}

// DiscoverProvider is the browse-and-explore half of a metadata provider: the
// curated lists and the rich detail the discover screens render.
//
// It is separate from MetadataProvider because it is optional. Matching a
// scanned file needs search and lookup and nothing else, so the scanner and
// the organizer keep depending on the smaller interface; the discover
// endpoints type-assert their way up to this one and report a provider that
// cannot browse the same way they report no provider at all.
type DiscoverProvider interface {
	// TrendingWeek returns this week's trending titles, movies and series
	// mixed, in the provider's own order.
	TrendingWeek(ctx context.Context) ([]DiscoverItem, error)
	// PopularMovies and PopularSeries return the provider's popularity lists.
	PopularMovies(ctx context.Context) ([]DiscoverItem, error)
	PopularSeries(ctx context.Context) ([]DiscoverItem, error)
	// MoviesByCompany browses one production company's movies. page is
	// 1-based; anything lower is clamped.
	MoviesByCompany(ctx context.Context, companyID int64, page int) (*DiscoverPage, error)
	// SeriesByNetwork browses one network's series.
	SeriesByNetwork(ctx context.Context, networkID int64, page int) (*DiscoverPage, error)
	// MovieDetail and SeriesDetail return one title with its cast,
	// recommendations and external ids.
	MovieDetail(ctx context.Context, tmdbID int64) (*TitleDetail, error)
	SeriesDetail(ctx context.Context, tmdbID int64) (*TitleDetail, error)
	// PosterURL renders a stored poster path as an absolute URL. Request rows
	// keep the path, so the API needs the provider to turn it back into
	// something a browser can load.
	PosterURL(path string) string
}
