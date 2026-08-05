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
	// MediaTypeScene is one adult scene (PLAN phase 9 task 7). A scene request
	// is identified by a stash-box id rather than a TMDB one, which is a rule
	// the requests table enforces itself — see migration 0013.
	MediaTypeScene = "scene"
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

// DiscoverSort names an ordering both discover endpoints answer. The values
// are Caravan's names, not the provider's: TMDB spells the same idea
// "primary_release_date" for a movie and "first_air_date" for a series, and
// which one to send is the client's problem, not the caller's.
//
// Only orderings BOTH endpoints support are listed. Movie-only orderings
// (revenue) are absent for the same reason the series filter has no cast
// field: a control the provider cannot answer for half the scopes is a control
// that should not exist.
type DiscoverSort string

const (
	// SortPopularity is the provider's own popularity score, and the default:
	// it is the order every curated shelf already uses.
	SortPopularity DiscoverSort = "popularity"
	// SortReleaseDate is the release date of a movie and the first air date of
	// a series.
	SortReleaseDate DiscoverSort = "release_date"
	// SortRating is the average vote. Sorting by it without a vote-count floor
	// surfaces titles with three votes, which is why VoteCountMin exists.
	SortRating DiscoverSort = "rating"
	// SortVotes is the number of votes: a rough "how well known is this".
	SortVotes DiscoverSort = "votes"
	// SortTitle is the original title of a movie and the name of a series.
	SortTitle DiscoverSort = "title"
)

// ParseDiscoverSort resolves a caller-supplied sort name. It reports false for
// anything that is not an ordering the provider serves, so an unknown value is
// refused rather than quietly becoming the default.
func ParseDiscoverSort(s string) (DiscoverSort, bool) {
	switch DiscoverSort(s) {
	case SortPopularity, SortReleaseDate, SortRating, SortVotes, SortTitle:
		return DiscoverSort(s), true
	}
	return "", false
}

// DiscoverOrder is the direction of a DiscoverSort.
type DiscoverOrder string

const (
	// OrderDesc is the default: the most popular, the newest, the highest
	// rated. It is what every existing shelf asks for.
	OrderDesc DiscoverOrder = "desc"
	OrderAsc  DiscoverOrder = "asc"
)

// ParseDiscoverOrder resolves a caller-supplied direction, reporting false for
// anything else.
func ParseDiscoverOrder(s string) (DiscoverOrder, bool) {
	switch DiscoverOrder(s) {
	case OrderDesc, OrderAsc:
		return DiscoverOrder(s), true
	}
	return "", false
}

// DiscoverFilter is the faceted-browse surface BOTH /discover endpoints
// answer. Anything either endpoint cannot serve is absent from it; anything
// only one endpoint serves lives on that endpoint's own filter type
// (MovieFilter, SeriesFilter) rather than here.
//
// Every zero value means "do not constrain on this", so a blank filter is the
// provider's unfiltered popularity list.
type DiscoverFilter struct {
	// Genres, Companies and Keywords are provider ids, ANDed: a title must
	// carry all of them. (TMDB can OR them too; Caravan does not offer it,
	// because a rail of chips reads as "and these".)
	Genres    []int64
	Companies []int64
	Keywords  []int64
	// ReleasedFrom and ReleasedTo bound the release date of a movie and the
	// first air date of a series. Either half may be zero for an open end.
	ReleasedFrom time.Time
	ReleasedTo   time.Time
	// RuntimeMin and RuntimeMax bound minutes: the feature length of a movie
	// and the episode length of a series.
	RuntimeMin int
	RuntimeMax int
	// VoteAverageMin is a rating floor out of 10; VoteCountMin is the number
	// of votes a title needs before its rating is believed.
	VoteAverageMin float64
	VoteCountMin   int
	// Language is an ISO 639-1 original-language code ("ja"), empty for any.
	Language string
	// Sort and Order default to SortPopularity and OrderDesc when empty.
	Sort  DiscoverSort
	Order DiscoverOrder
	// Page is 1-based; anything lower is clamped by the provider.
	Page int
}

// MovieFilter is DiscoverFilter plus what only /discover/movie answers.
type MovieFilter struct {
	DiscoverFilter
	// Cast, Crew and People are person ids. Cast means "acted in it", Crew
	// means "worked on it behind the camera", People means either.
	//
	// THE SEAM: TMDB's /discover/tv has no with_cast, with_crew or with_people
	// parameter — a person cannot be a TV filter at all, and sending one is
	// ignored rather than refused. That is why these live here instead of on
	// DiscoverFilter: "movies with this actor" is expressible and "series with
	// this actor" is not, and the type system says so rather than the docs.
	Cast   []int64
	Crew   []int64
	People []int64
}

// SeriesFilter is DiscoverFilter plus what only /discover/tv answers.
type SeriesFilter struct {
	DiscoverFilter
	// Networks are TV network ids (Netflix is 213). Movies have no equivalent
	// — their maker is a production company, which is DiscoverFilter.Companies
	// on both sides.
	Networks []int64
}

// DiscoverPerson is one person in a person typeahead: enough to render a row
// and to filter by afterwards, and nothing else.
type DiscoverPerson struct {
	TMDBID int64
	Name   string
	// Department is what the provider says this person is best known for
	// ("Acting", "Directing"), empty when it does not say.
	Department string
	ProfileURL string
}

// DiscoverCompany is one production company or studio in a typeahead.
type DiscoverCompany struct {
	TMDBID int64
	Name   string
	// Country is the ISO 3166-1 origin code ("US"), empty when unknown. It is
	// the disambiguator between the many companies sharing a name.
	Country string
	LogoURL string
}

// DiscoverKeyword is one TMDB keyword — the free-tagging vocabulary behind
// "based on a true story" or "heist".
type DiscoverKeyword struct {
	TMDBID int64
	Name   string
}

// DiscoverGenre is one genre in the provider's fixed per-media-type list.
type DiscoverGenre struct {
	TMDBID int64
	Name   string
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
	// DiscoverMovies and DiscoverSeries are the faceted browse the filter
	// rail drives. They take separate filter types on purpose: the movie
	// endpoint answers person filters and the series endpoint does not.
	DiscoverMovies(ctx context.Context, f MovieFilter) (*DiscoverPage, error)
	DiscoverSeries(ctx context.Context, f SeriesFilter) (*DiscoverPage, error)
	// SearchPeople, SearchCompanies and SearchKeywords back the filter rail's
	// typeaheads. They return the provider's first page only: a typeahead is
	// a shortlist, and anyone who has to page one has not typed enough.
	SearchPeople(ctx context.Context, query string) ([]DiscoverPerson, error)
	SearchCompanies(ctx context.Context, query string) ([]DiscoverCompany, error)
	SearchKeywords(ctx context.Context, query string) ([]DiscoverKeyword, error)
	// Genres lists the fixed genre vocabulary for one media type
	// (MediaTypeMovie or MediaTypeSeries). The two lists differ and neither is
	// a subset of the other, so the media type is required rather than
	// defaulted.
	Genres(ctx context.Context, mediaType string) ([]DiscoverGenre, error)
	// MovieDetail and SeriesDetail return one title with its cast,
	// recommendations and external ids.
	MovieDetail(ctx context.Context, tmdbID int64) (*TitleDetail, error)
	SeriesDetail(ctx context.Context, tmdbID int64) (*TitleDetail, error)
	// PosterURL renders a stored poster path as an absolute URL. Request rows
	// keep the path, so the API needs the provider to turn it back into
	// something a browser can load.
	PosterURL(path string) string
}
