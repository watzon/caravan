package tmdb

import (
	"context"
	"fmt"
	"net/url"

	"github.com/watzon/caravan/internal/core"
)

// tvResult is the series shape shared by search results and details.
type tvResult struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	OriginalName string  `json:"original_name"`
	Overview     string  `json:"overview"`
	PosterPath   string  `json:"poster_path"`
	FirstAirDate string  `json:"first_air_date"`
	VoteAverage  float64 `json:"vote_average"`
	VoteCount    int     `json:"vote_count"`
}

// tvDetail is /tv/{id}. Its seasons list carries no episodes, only the season
// numbers that exist — hence the per-season fetch in GetSeries.
type tvDetail struct {
	tvResult
	Status  string `json:"status"`
	Seasons []struct {
		SeasonNumber int `json:"season_number"`
	} `json:"seasons"`
	ExternalIDs externalIDs `json:"external_ids"`
}

// externalIDs is the append_to_response=external_ids block. TVDB ids matter
// because some indexers and NFO consumers key on them rather than TMDB.
type externalIDs struct {
	IMDBID string `json:"imdb_id"`
	TVDBID int64  `json:"tvdb_id"`
}

// seasonDetail is /tv/{id}/season/{n}.
type seasonDetail struct {
	Name       string          `json:"name"`
	Overview   string          `json:"overview"`
	AirDate    string          `json:"air_date"`
	PosterPath string          `json:"poster_path"`
	Episodes   []episodeResult `json:"episodes"`
}

type episodeResult struct {
	ID            int64  `json:"id"`
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
	Name          string `json:"name"`
	Overview      string `json:"overview"`
	AirDate       string `json:"air_date"`
}

type tvSearchResponse struct {
	Results []tvResult `json:"results"`
}

// SearchSeries returns series candidates for q, in TMDB's relevance order.
// Results carry no seasons and no external ids; call GetSeries for those.
func (c *Client) SearchSeries(ctx context.Context, q string) ([]core.SeriesMeta, error) {
	var resp tvSearchResponse
	if err := c.get(ctx, "/search/tv", url.Values{"query": {q}}, &resp); err != nil {
		return nil, err
	}

	out := make([]core.SeriesMeta, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, c.seriesMeta(r))
	}
	return out, nil
}

// GetSeries returns full details for one series, including every season and
// its episodes. Episodes are not available on the series endpoint at any
// append_to_response depth, so each season is fetched individually — one
// request per season, sequentially, to stay well inside TMDB's rate limit.
func (c *Client) GetSeries(ctx context.Context, tmdbID int64) (*core.SeriesMeta, error) {
	var d tvDetail
	q := url.Values{"append_to_response": {"external_ids"}}
	if err := c.get(ctx, fmt.Sprintf("/tv/%d", tmdbID), q, &d); err != nil {
		return nil, err
	}

	s := c.seriesMeta(d.tvResult)
	s.Status = d.Status
	s.IMDBID = d.ExternalIDs.IMDBID
	s.TVDBID = d.ExternalIDs.TVDBID

	for _, season := range d.Seasons {
		sm, err := c.season(ctx, tmdbID, season.SeasonNumber)
		if err != nil {
			return nil, err
		}
		s.Seasons = append(s.Seasons, sm)
	}
	return &s, nil
}

// season fetches one season with its episodes.
func (c *Client) season(ctx context.Context, tmdbID int64, number int) (core.SeasonMeta, error) {
	var d seasonDetail
	if err := c.get(ctx, fmt.Sprintf("/tv/%d/season/%d", tmdbID, number), nil, &d); err != nil {
		return core.SeasonMeta{}, err
	}

	sm := core.SeasonMeta{
		// The requested number is authoritative: it is the key the rest of
		// the library indexes seasons by, including season 0 for specials.
		Number:    number,
		Title:     d.Name,
		Overview:  d.Overview,
		AirDate:   parseDate(d.AirDate),
		PosterURL: c.posterURL(d.PosterPath),
	}
	for _, e := range d.Episodes {
		sm.Episodes = append(sm.Episodes, core.EpisodeMeta{
			TMDBID:   e.ID,
			Season:   e.SeasonNumber,
			Number:   e.EpisodeNumber,
			Title:    e.Name,
			Overview: e.Overview,
			AirDate:  parseDate(e.AirDate),
		})
	}
	return sm, nil
}

// seriesMeta converts a TMDB series into the provider-side domain type.
// Fields that only exist on the details endpoint are filled by the caller.
func (c *Client) seriesMeta(r tvResult) core.SeriesMeta {
	firstAir := parseDate(r.FirstAirDate)
	return core.SeriesMeta{
		TMDBID:        r.ID,
		Title:         r.Name,
		OriginalTitle: r.OriginalName,
		Year:          yearOf(firstAir),
		Overview:      r.Overview,
		VoteAverage:   r.VoteAverage,
		VoteCount:     r.VoteCount,
		FirstAirDate:  firstAir,
		PosterURL:     c.posterURL(r.PosterPath),
	}
}
