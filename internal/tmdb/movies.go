package tmdb

import (
	"context"
	"fmt"
	"net/url"

	"github.com/watzon/caravan/internal/core"
)

// movieResult is the movie shape shared by search results and details.
type movieResult struct {
	ID            int64  `json:"id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	Overview      string `json:"overview"`
	PosterPath    string `json:"poster_path"`
	ReleaseDate   string `json:"release_date"`
}

// movieDetail is /movie/{id}. IMDB ids only appear on the details endpoint,
// never on search results.
type movieDetail struct {
	movieResult
	IMDBID string `json:"imdb_id"`
}

type movieSearchResponse struct {
	Results []movieResult `json:"results"`
}

// SearchMovies returns movie candidates for q, in TMDB's relevance order.
// Results carry no IMDB id; call GetMovie for the full record.
func (c *Client) SearchMovies(ctx context.Context, q string) ([]core.MovieMeta, error) {
	var resp movieSearchResponse
	if err := c.get(ctx, "/search/movie", url.Values{"query": {q}}, &resp); err != nil {
		return nil, err
	}

	out := make([]core.MovieMeta, 0, len(resp.Results))
	for _, r := range resp.Results {
		out = append(out, c.movieMeta(r, ""))
	}
	return out, nil
}

// GetMovie returns full details for one movie.
func (c *Client) GetMovie(ctx context.Context, tmdbID int64) (*core.MovieMeta, error) {
	var d movieDetail
	if err := c.get(ctx, fmt.Sprintf("/movie/%d", tmdbID), nil, &d); err != nil {
		return nil, err
	}
	m := c.movieMeta(d.movieResult, d.IMDBID)
	return &m, nil
}

// movieMeta converts a TMDB movie into the provider-side domain type.
func (c *Client) movieMeta(r movieResult, imdbID string) core.MovieMeta {
	released := parseDate(r.ReleaseDate)
	return core.MovieMeta{
		TMDBID:        r.ID,
		IMDBID:        imdbID,
		Title:         r.Title,
		OriginalTitle: r.OriginalTitle,
		Year:          yearOf(released),
		Overview:      r.Overview,
		ReleaseDate:   released,
		PosterURL:     c.posterURL(r.PosterPath),
	}
}
