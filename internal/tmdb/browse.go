package tmdb

import (
	"context"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// ErrUnsupportedMediaType means Genres was asked for something TMDB keeps no
// genre vocabulary for. Only movies and series have one.
var ErrUnsupportedMediaType = errors.New("tmdb: unsupported media type")

// TMDB's own names for the filter surface. They are gathered here rather than
// spelled inline so the two endpoint builders can be read side by side and the
// differences between them are the only thing that stands out.
const (
	paramGenres    = "with_genres"
	paramCompanies = "with_companies"
	paramKeywords  = "with_keywords"
	paramNetworks  = "with_networks"
	paramCast      = "with_cast"
	paramCrew      = "with_crew"
	paramPeople    = "with_people"
	paramLanguage  = "with_original_language"
	paramSort      = "sort_by"

	// The two date parameters TMDB spells differently per endpoint: a movie is
	// filtered on its primary release date and a series on its first air date.
	paramMovieDate  = "primary_release_date"
	paramSeriesDate = "first_air_date"

	// Range parameters take a .gte/.lte suffix.
	suffixGTE = ".gte"
	suffixLTE = ".lte"

	paramRuntime     = "with_runtime"
	paramVoteAverage = "vote_average"
	paramVoteCount   = "vote_count"
)

// movieSortBy and seriesSortBy map Caravan's orderings onto each endpoint's
// own field names. The two lists differ in exactly two rows, which is the
// whole reason the mapping exists rather than the caller sending sort_by
// itself.
var (
	movieSortBy = map[core.DiscoverSort]string{
		core.SortPopularity:  "popularity",
		core.SortReleaseDate: "primary_release_date",
		core.SortRating:      "vote_average",
		core.SortVotes:       "vote_count",
		core.SortTitle:       "original_title",
	}
	seriesSortBy = map[core.DiscoverSort]string{
		core.SortPopularity:  "popularity",
		core.SortReleaseDate: "first_air_date",
		core.SortRating:      "vote_average",
		core.SortVotes:       "vote_count",
		core.SortTitle:       "name",
	}
)

// DiscoverMovies browses /discover/movie under a filter. Person filters are
// part of MovieFilter because this is the only endpoint that answers them.
func (c *Client) DiscoverMovies(ctx context.Context, f core.MovieFilter) (*core.DiscoverPage, error) {
	q := commonQuery(f.DiscoverFilter, paramMovieDate, movieSortBy)
	setIDs(q, paramCast, f.Cast)
	setIDs(q, paramCrew, f.Crew)
	setIDs(q, paramPeople, f.People)
	return c.page(ctx, "/discover/movie", q, core.MediaTypeMovie, f.Page)
}

// DiscoverSeries browses /discover/tv under a filter.
//
// There is no person parameter here and no field on SeriesFilter to hold one:
// TMDB's TV discover endpoint has no with_cast, with_crew or with_people, and
// sending one is ignored rather than refused, so a "series with this actor"
// filter would silently return the unfiltered catalogue.
func (c *Client) DiscoverSeries(ctx context.Context, f core.SeriesFilter) (*core.DiscoverPage, error) {
	q := commonQuery(f.DiscoverFilter, paramSeriesDate, seriesSortBy)
	setIDs(q, paramNetworks, f.Networks)
	return c.page(ctx, "/discover/tv", q, core.MediaTypeSeries, f.Page)
}

// MoviesByCompany browses one production company's catalogue, most popular
// first. companyID is a TMDB company id (A24 is 41077).
func (c *Client) MoviesByCompany(ctx context.Context, companyID int64, page int) (*core.DiscoverPage, error) {
	return c.DiscoverMovies(ctx, core.MovieFilter{DiscoverFilter: core.DiscoverFilter{
		Companies: []int64{companyID},
		Page:      page,
	}})
}

// SeriesByNetwork browses one network's catalogue, most popular first.
// networkID is a TMDB network id (Netflix is 213).
func (c *Client) SeriesByNetwork(ctx context.Context, networkID int64, page int) (*core.DiscoverPage, error) {
	return c.DiscoverSeries(ctx, core.SeriesFilter{
		DiscoverFilter: core.DiscoverFilter{Page: page},
		Networks:       []int64{networkID},
	})
}

// commonQuery renders the half of a filter both endpoints share. dateParam is
// the endpoint's own name for "when it came out", and sortBy is its mapping
// from Caravan's orderings onto its own.
func commonQuery(f core.DiscoverFilter, dateParam string, sortBy map[core.DiscoverSort]string) url.Values {
	q := url.Values{}
	setIDs(q, paramGenres, f.Genres)
	setIDs(q, paramCompanies, f.Companies)
	setIDs(q, paramKeywords, f.Keywords)

	setDate(q, dateParam+suffixGTE, f.ReleasedFrom)
	setDate(q, dateParam+suffixLTE, f.ReleasedTo)

	setInt(q, paramRuntime+suffixGTE, f.RuntimeMin)
	setInt(q, paramRuntime+suffixLTE, f.RuntimeMax)
	setInt(q, paramVoteCount+suffixGTE, f.VoteCountMin)
	if f.VoteAverageMin > 0 {
		q.Set(paramVoteAverage+suffixGTE, strconv.FormatFloat(f.VoteAverageMin, 'f', -1, 64))
	}
	if f.Language != "" {
		q.Set(paramLanguage, f.Language)
	}

	// sort_by is always sent. TMDB's own default is popularity.desc, so this
	// changes nothing about the results; it makes the request self-describing
	// and keeps the curated shelves' query identical to a filtered one's.
	sort := f.Sort
	if sort == "" {
		sort = core.SortPopularity
	}
	order := f.Order
	if order == "" {
		order = core.OrderDesc
	}
	field, ok := sortBy[sort]
	if !ok {
		field = sortBy[core.SortPopularity]
		order = core.OrderDesc
	}
	q.Set(paramSort, field+"."+string(order))
	return q
}

// setIDs writes a comma-joined id list, which is TMDB's AND spelling. (Its OR
// spelling is a pipe; Caravan does not offer it.) An empty list writes
// nothing: a present-but-empty parameter is a filter matching nothing.
func setIDs(q url.Values, key string, ids []int64) {
	if len(ids) == 0 {
		return
	}
	parts := make([]string, 0, len(ids))
	for _, id := range ids {
		parts = append(parts, strconv.FormatInt(id, 10))
	}
	q.Set(key, strings.Join(parts, ","))
}

func setDate(q url.Values, key string, t time.Time) {
	if t.IsZero() {
		return
	}
	q.Set(key, t.Format(dateLayout))
}

// setInt writes a positive bound only: zero and negative both mean "no bound",
// and there is no filter any of these parameters expresses at zero.
func setInt(q url.Values, key string, v int) {
	if v <= 0 {
		return
	}
	q.Set(key, strconv.Itoa(v))
}

// The typeahead response shapes. Only the fields a filter chip renders are
// decoded; TMDB sends a good deal more.
type searchPeopleResponse struct {
	Results []struct {
		ID          int64  `json:"id"`
		Name        string `json:"name"`
		Department  string `json:"known_for_department"`
		ProfilePath string `json:"profile_path"`
	} `json:"results"`
}

type searchCompanyResponse struct {
	Results []struct {
		ID            int64  `json:"id"`
		Name          string `json:"name"`
		OriginCountry string `json:"origin_country"`
		LogoPath      string `json:"logo_path"`
	} `json:"results"`
}

type namedListResponse struct {
	Results []namedItem `json:"results"`
	Genres  []namedItem `json:"genres"`
}

type namedItem struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// SearchPeople backs the cast/crew typeahead.
func (c *Client) SearchPeople(ctx context.Context, query string) ([]core.DiscoverPerson, error) {
	var resp searchPeopleResponse
	if err := c.get(ctx, "/search/person", url.Values{"query": {query}}, &resp); err != nil {
		return nil, err
	}
	out := make([]core.DiscoverPerson, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, core.DiscoverPerson{
			TMDBID:     r.ID,
			Name:       r.Name,
			Department: r.Department,
			ProfileURL: c.posterURL(r.ProfilePath),
		})
	}
	return out, nil
}

// SearchCompanies backs the studio typeahead. TMDB's company index is the same
// one /discover's with_companies reads, so anything this returns is filterable.
func (c *Client) SearchCompanies(ctx context.Context, query string) ([]core.DiscoverCompany, error) {
	var resp searchCompanyResponse
	if err := c.get(ctx, "/search/company", url.Values{"query": {query}}, &resp); err != nil {
		return nil, err
	}
	out := make([]core.DiscoverCompany, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, core.DiscoverCompany{
			TMDBID:  r.ID,
			Name:    r.Name,
			Country: r.OriginCountry,
			// Logos live on the same CDN as posters and take the same sizes.
			LogoURL: c.posterURL(r.LogoPath),
		})
	}
	return out, nil
}

// SearchKeywords backs the keyword typeahead.
func (c *Client) SearchKeywords(ctx context.Context, query string) ([]core.DiscoverKeyword, error) {
	var resp namedListResponse
	if err := c.get(ctx, "/search/keyword", url.Values{"query": {query}}, &resp); err != nil {
		return nil, err
	}
	out := make([]core.DiscoverKeyword, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, core.DiscoverKeyword{TMDBID: r.ID, Name: r.Name})
	}
	return out, nil
}

// genreCache memoises the two genre lists for the life of the client.
//
// TMDB's genre vocabulary is a fixed table, it gains an entry every few years,
// so there is no TTL: an expiry would only add a way for the list to be briefly
// wrong. A client is rebuilt whenever the API key changes, which is the only
// event that could make a cached list unreachable.
type genreCache struct {
	mu          sync.Mutex
	byMediaType map[string][]core.DiscoverGenre
}

func (g *genreCache) get(mediaType string) ([]core.DiscoverGenre, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	list, ok := g.byMediaType[mediaType]
	return list, ok
}

func (g *genreCache) put(mediaType string, list []core.DiscoverGenre) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.byMediaType == nil {
		g.byMediaType = map[string][]core.DiscoverGenre{}
	}
	g.byMediaType[mediaType] = list
}

// Genres returns the genre vocabulary for one media type, fetched once per
// client. mediaType is core.MediaTypeMovie or core.MediaTypeSeries; anything
// else is core.ErrUnsupportedMediaType.
func (c *Client) Genres(ctx context.Context, mediaType string) ([]core.DiscoverGenre, error) {
	var path string
	switch mediaType {
	case core.MediaTypeMovie:
		path = "/genre/movie/list"
	case core.MediaTypeSeries:
		path = "/genre/tv/list"
	default:
		return nil, ErrUnsupportedMediaType
	}

	if list, ok := c.genres.get(mediaType); ok {
		return list, nil
	}

	var resp namedListResponse
	if err := c.get(ctx, path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]core.DiscoverGenre, 0, len(resp.Genres))
	for _, g := range resp.Genres {
		out = append(out, core.DiscoverGenre{TMDBID: g.ID, Name: g.Name})
	}
	c.genres.put(mediaType, out)
	return out, nil
}
