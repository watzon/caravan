package tmdb

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// movieResult is the movie shape shared by search results and details.
type movieResult struct {
	ID            int64   `json:"id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Overview      string  `json:"overview"`
	PosterPath    string  `json:"poster_path"`
	ReleaseDate   string  `json:"release_date"`
	VoteAverage   float64 `json:"vote_average"`
}

// TMDB release types, from /movie/{id}/release_dates. Only the two home-release
// kinds matter to Caravan; theatrical is already the top-level release_date.
const (
	releaseTypeDigital  = 4
	releaseTypePhysical = 5
)

// movieDetail is /movie/{id}. IMDB ids only appear on the details endpoint,
// never on search results. ReleaseDates is the appended per-region release
// list; its dates are full timestamps ("2017-01-03T00:00:00.000Z"), unlike
// every other date TMDB serves.
type movieDetail struct {
	movieResult
	IMDBID       string `json:"imdb_id"`
	ReleaseDates struct {
		Results []struct {
			ReleaseDates []struct {
				ReleaseDate string `json:"release_date"`
				Type        int    `json:"type"`
			} `json:"release_dates"`
		} `json:"results"`
	} `json:"release_dates"`
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

// GetMovie returns full details for one movie, including its home-release
// dates: they are what a minimum availability of "released" waits for.
func (c *Client) GetMovie(ctx context.Context, tmdbID int64) (*core.MovieMeta, error) {
	var d movieDetail
	q := url.Values{"append_to_response": {"release_dates"}}
	if err := c.get(ctx, fmt.Sprintf("/movie/%d", tmdbID), q, &d); err != nil {
		return nil, err
	}
	m := c.movieMeta(d.movieResult, d.IMDBID)
	m.DigitalRelease = earliestRelease(d, releaseTypeDigital)
	m.PhysicalRelease = earliestRelease(d, releaseTypePhysical)
	return &m, nil
}

// earliestRelease finds the earliest release of one type across every region.
// Earliest-anywhere rather than one home region: "released" asks whether a
// copy can exist at all, and the first region's release is when that starts
// being true.
func earliestRelease(d movieDetail, releaseType int) time.Time {
	var earliest time.Time
	for _, region := range d.ReleaseDates.Results {
		for _, r := range region.ReleaseDates {
			if r.Type != releaseType {
				continue
			}
			// The timestamp's date half is dateLayout; the clock is noise.
			raw := r.ReleaseDate
			if len(raw) > len(dateLayout) {
				raw = raw[:len(dateLayout)]
			}
			when := parseDate(raw)
			if when.IsZero() {
				continue
			}
			if earliest.IsZero() || when.Before(earliest) {
				earliest = when
			}
		}
	}
	return earliest
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
		VoteAverage:   r.VoteAverage,
		ReleaseDate:   released,
		PosterURL:     c.posterURL(r.PosterPath),
	}
}
