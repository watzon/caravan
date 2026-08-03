package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"strconv"

	"github.com/watzon/caravan/internal/core"
)

// Compile-time proof that the client also satisfies the browse seam the
// discover endpoints depend on.
var _ core.DiscoverProvider = (*Client)(nil)

const (
	// trendingWindow is the window Caravan asks TMDB for. "day" churns too
	// fast to be a homepage a user recognises between visits; "week" is the
	// list people mean when they say what is trending.
	trendingWindow = "week"

	// tvMediaType is TMDB's name for what Caravan calls a series. It appears
	// in trending results, which are the one place the provider tags the type
	// itself rather than the endpoint implying it.
	tvMediaType = "tv"

	// maxPage is TMDB's hard page ceiling. Asking past it is an error
	// response, so the client clamps rather than forwarding a doomed request.
	maxPage = 500

	// homePages is how many pages each home shelf merges. One TMDB page is 20
	// rows and trending drops its people, so one page cannot promise the 20–30
	// cards a carousel wants; two always can.
	homePages = 2

	// homeShelfLimit caps a merged home shelf. Past 30 cards a shelf stops
	// being a sample and starts being the browse screen without its paging.
	homeShelfLimit = 30

	// detailAppend pulls the three sub-resources the detail screen needs in
	// the same round trip. TMDB allows up to 20 appended namespaces; three
	// keeps one screen to one request.
	detailAppend = "credits,recommendations,external_ids"
)

// discoverResult is the list-item shape every browse endpoint returns. Movie
// and TV results differ only in which of title/name and
// release_date/first_air_date is populated, so one struct covers both;
// media_type is set on /trending, where the list is mixed, and absent
// elsewhere.
type discoverResult struct {
	ID           int64   `json:"id"`
	MediaType    string  `json:"media_type"`
	Title        string  `json:"title"`
	Name         string  `json:"name"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	BackdropPath string  `json:"backdrop_path"`
	VoteAverage  float64 `json:"vote_average"`
	ReleaseDate  string  `json:"release_date"`
	FirstAirDate string  `json:"first_air_date"`
}

type discoverResponse struct {
	Page       int              `json:"page"`
	TotalPages int              `json:"total_pages"`
	Results    []discoverResult `json:"results"`
}

// detailResponse is /movie/{id} and /tv/{id} with detailAppend. The two
// endpoints disagree on where a few fields live — imdb_id is top level on a
// movie and inside external_ids on a series, runtime is a number on a movie
// and a list on a series — so both spellings are decoded and the method that
// knows which endpoint it called picks.
type detailResponse struct {
	discoverResult
	Status         string `json:"status"`
	Runtime        int    `json:"runtime"`
	EpisodeRunTime []int  `json:"episode_run_time"`
	IMDBID         string `json:"imdb_id"`
	// LastAirDate and Networks are series-only; ProductionCompanies is the
	// movie's answer to the same question. OriginalLanguage is on both.
	LastAirDate      string `json:"last_air_date"`
	OriginalLanguage string `json:"original_language"`
	Networks         []struct {
		Name string `json:"name"`
	} `json:"networks"`
	ProductionCompanies []struct {
		Name string `json:"name"`
	} `json:"production_companies"`
	Genres []struct {
		Name string `json:"name"`
	} `json:"genres"`
	Credits struct {
		Cast []struct {
			ID          int64  `json:"id"`
			Name        string `json:"name"`
			Character   string `json:"character"`
			ProfilePath string `json:"profile_path"`
		} `json:"cast"`
	} `json:"credits"`
	Recommendations discoverResponse `json:"recommendations"`
	ExternalIDs     externalIDs      `json:"external_ids"`
	Seasons         []struct {
		SeasonNumber int    `json:"season_number"`
		Name         string `json:"name"`
		Overview     string `json:"overview"`
		PosterPath   string `json:"poster_path"`
		AirDate      string `json:"air_date"`
		EpisodeCount int    `json:"episode_count"`
	} `json:"seasons"`
}

// TrendingWeek returns this week's trending titles. TMDB's /trending/all list
// includes people; they are dropped rather than rendered as a title with no
// year.
func (c *Client) TrendingWeek(ctx context.Context) ([]core.DiscoverItem, error) {
	return c.list(ctx, "/trending/all/"+trendingWindow, nil, "")
}

// PopularMovies returns TMDB's popular-movies list.
func (c *Client) PopularMovies(ctx context.Context) ([]core.DiscoverItem, error) {
	return c.list(ctx, "/movie/popular", nil, core.MediaTypeMovie)
}

// PopularSeries returns TMDB's popular-TV list.
func (c *Client) PopularSeries(ctx context.Context) ([]core.DiscoverItem, error) {
	return c.list(ctx, "/tv/popular", nil, core.MediaTypeSeries)
}

// MoviesByCompany browses one production company's catalogue, most popular
// first. companyID is a TMDB company id (A24 is 41077).
func (c *Client) MoviesByCompany(ctx context.Context, companyID int64, page int) (*core.DiscoverPage, error) {
	q := url.Values{
		"with_companies": {strconv.FormatInt(companyID, 10)},
		"sort_by":        {"popularity.desc"},
	}
	return c.page(ctx, "/discover/movie", q, core.MediaTypeMovie, page)
}

// SeriesByNetwork browses one network's catalogue, most popular first.
// networkID is a TMDB network id (Netflix is 213).
func (c *Client) SeriesByNetwork(ctx context.Context, networkID int64, page int) (*core.DiscoverPage, error) {
	q := url.Values{
		"with_networks": {strconv.FormatInt(networkID, 10)},
		"sort_by":       {"popularity.desc"},
	}
	return c.page(ctx, "/discover/tv", q, core.MediaTypeSeries, page)
}

// MovieDetail returns one movie with its cast, its recommendations and its
// external ids, in a single request.
func (c *Client) MovieDetail(ctx context.Context, tmdbID int64) (*core.TitleDetail, error) {
	d, err := c.detail(ctx, fmt.Sprintf("/movie/%d", tmdbID))
	if err != nil {
		return nil, err
	}

	td := c.titleDetail(d, core.MediaTypeMovie)
	td.Runtime = d.Runtime
	// The studio is the first billed production company: TMDB lists financiers
	// and service companies alongside it, and the first is the one a poster
	// credits.
	td.Network = firstName(d.ProductionCompanies)
	// A movie carries its IMDB id at the top level; external_ids repeats it,
	// and is the fallback rather than the source.
	td.IMDBID = d.IMDBID
	if td.IMDBID == "" {
		td.IMDBID = d.ExternalIDs.IMDBID
	}
	return &td, nil
}

// SeriesDetail is MovieDetail's series twin. The season list comes from the
// details response — TMDB describes every season there, so no per-season
// request is needed to render the detail screen. (GetSeries still fetches each
// season: it needs the episodes, which only the season endpoint has.)
func (c *Client) SeriesDetail(ctx context.Context, tmdbID int64) (*core.TitleDetail, error) {
	d, err := c.detail(ctx, fmt.Sprintf("/tv/%d", tmdbID))
	if err != nil {
		return nil, err
	}

	td := c.titleDetail(d, core.MediaTypeSeries)
	td.Status = d.Status
	td.Network = firstName(d.Networks)
	td.LastAired = parseDate(d.LastAirDate)
	td.IMDBID = d.ExternalIDs.IMDBID
	td.TVDBID = d.ExternalIDs.TVDBID
	if len(d.EpisodeRunTime) > 0 {
		td.Runtime = d.EpisodeRunTime[0]
	}
	for _, se := range d.Seasons {
		td.Seasons = append(td.Seasons, core.DiscoverSeason{
			Number:       se.SeasonNumber,
			Title:        se.Name,
			Overview:     se.Overview,
			PosterURL:    c.posterURL(se.PosterPath),
			AirDate:      parseDate(se.AirDate),
			EpisodeCount: se.EpisodeCount,
		})
	}
	return &td, nil
}

func (c *Client) detail(ctx context.Context, path string) (detailResponse, error) {
	var d detailResponse
	q := url.Values{"append_to_response": {detailAppend}}
	if err := c.get(ctx, path, q, &d); err != nil {
		return detailResponse{}, err
	}
	return d, nil
}

// titleDetail converts the parts of a detail response both media types share.
func (c *Client) titleDetail(d detailResponse, mediaType string) core.TitleDetail {
	td := core.TitleDetail{DiscoverItem: c.discoverItem(d.discoverResult, mediaType)}
	td.Language = d.OriginalLanguage
	for _, g := range d.Genres {
		td.Genres = append(td.Genres, g.Name)
	}
	for _, m := range d.Credits.Cast {
		td.Cast = append(td.Cast, core.CastMember{
			TMDBID:    m.ID,
			Name:      m.Name,
			Character: m.Character,
			// Headshots come off the same CDN prefix as posters; TMDB serves
			// every size for every image kind.
			ProfileURL: c.posterURL(m.ProfilePath),
		})
	}
	td.Recommendations = c.items(d.Recommendations.Results, mediaType)
	return td
}

// firstName reads the lead entry of one of TMDB's name lists (networks,
// production companies), empty when the list is.
func firstName(list []struct {
	Name string `json:"name"`
}) string {
	if len(list) == 0 {
		return ""
	}
	return list[0].Name
}

// titleKey identifies a title on a mixed shelf: a movie and a series can share
// a TMDB id, so neither half is a key on its own.
type titleKey struct {
	mediaType string
	tmdbID    int64
}

// list fetches an unpaged home shelf: up to homePages pages merged into one
// list, capped at homeShelfLimit. fallbackType is the media type the endpoint
// implies, empty for a mixed list that tags its own results.
//
// The merge dedupes by title: popularity lists reorder between requests, so
// page 2 can hand back a row page 1 already did, and a duplicate key crashes
// the client's keyed render.
func (c *Client) list(ctx context.Context, path string, q url.Values, fallbackType string) ([]core.DiscoverItem, error) {
	if q == nil {
		q = url.Values{}
	}

	seen := map[titleKey]bool{}
	out := []core.DiscoverItem{}
	for page := 1; page <= homePages; page++ {
		q.Set("page", strconv.Itoa(page))
		var resp discoverResponse
		if err := c.get(ctx, path, q, &resp); err != nil {
			return nil, err
		}
		for _, item := range c.items(resp.Results, fallbackType) {
			key := titleKey{item.MediaType, item.TMDBID}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, item)
		}
		if resp.TotalPages <= page {
			break
		}
	}
	if len(out) > homeShelfLimit {
		out = out[:homeShelfLimit]
	}
	return out, nil
}

// page fetches one page of a paged browse list.
func (c *Client) page(ctx context.Context, path string, q url.Values, fallbackType string, page int) (*core.DiscoverPage, error) {
	if page < 1 {
		page = 1
	}
	if page > maxPage {
		page = maxPage
	}
	q.Set("page", strconv.Itoa(page))

	var resp discoverResponse
	if err := c.get(ctx, path, q, &resp); err != nil {
		return nil, err
	}
	// TMDB reports the catalogue's true page count but refuses to serve past
	// maxPage, so the clamp has to be reported too: a caller paging against the
	// raw total would ask for page 501 and be handed page 500 a second time.
	total := resp.TotalPages
	if total > maxPage {
		total = maxPage
	}
	return &core.DiscoverPage{
		Page:       resp.Page,
		TotalPages: total,
		Items:      c.items(resp.Results, fallbackType),
	}, nil
}

// items converts a result list, dropping anything that is not a title.
func (c *Client) items(results []discoverResult, fallbackType string) []core.DiscoverItem {
	out := make([]core.DiscoverItem, 0, len(results))
	for _, r := range results {
		mediaType, ok := mediaTypeOf(r.MediaType, fallbackType)
		if !ok {
			continue
		}
		out = append(out, c.discoverItem(r, mediaType))
	}
	return out
}

// mediaTypeOf resolves a result's media type, preferring the provider's own
// tag over what the endpoint implies. It reports false for anything that is
// not a movie or a series — /trending/all also returns people.
func mediaTypeOf(tag, fallback string) (string, bool) {
	if tag == "" {
		tag = fallback
	}
	switch tag {
	case core.MediaTypeMovie:
		return core.MediaTypeMovie, true
	case tvMediaType, core.MediaTypeSeries:
		return core.MediaTypeSeries, true
	}
	return "", false
}

// discoverItem converts one TMDB result. mediaType decides which of the two
// title and date spellings is read.
func (c *Client) discoverItem(r discoverResult, mediaType string) core.DiscoverItem {
	title, date := r.Title, r.ReleaseDate
	if mediaType == core.MediaTypeSeries {
		title, date = r.Name, r.FirstAirDate
	}
	when := parseDate(date)
	return core.DiscoverItem{
		MediaType:   mediaType,
		TMDBID:      r.ID,
		Title:       title,
		Year:        yearOf(when),
		Overview:    r.Overview,
		PosterPath:  r.PosterPath,
		PosterURL:   c.posterURL(r.PosterPath),
		BackdropURL: c.backdropURL(r.BackdropPath),
		VoteAverage: r.VoteAverage,
		Date:        when,
	}
}
