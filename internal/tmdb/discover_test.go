package tmdb

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

func TestTrendingWeekDropsNonTitles(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/trending/all/week": {okJSON(t, "trending_all_week.json")},
	})

	got, err := c.TrendingWeek(context.Background())
	if err != nil {
		t.Fatalf("TrendingWeek: %v", err)
	}

	want := []core.DiscoverItem{
		{
			MediaType:   core.MediaTypeMovie,
			TMDBID:      78,
			Title:       "Blade Runner",
			Year:        1982,
			Overview:    "In the smog-choked dystopian Los Angeles of 2019, blade runner Rick Deckard is called out of retirement.",
			PosterPath:  "/63N9uy8nd9j7Eog2axPQ8lbr3Wj.jpg",
			PosterURL:   "https://image.tmdb.org/t/p/w500/63N9uy8nd9j7Eog2axPQ8lbr3Wj.jpg",
			BackdropURL: "https://image.tmdb.org/t/p/w780/hZkgoQYus5vegHoetLkCJzb17zJ.jpg",
			VoteAverage: 7.9,
			VoteCount:   12894,
			Date:        time.Date(1982, 6, 25, 0, 0, 0, 0, time.UTC),
		},
		{
			// A "tv" result becomes a series, and its title comes from `name`
			// rather than `title`.
			MediaType:   core.MediaTypeSeries,
			TMDBID:      1396,
			Title:       "Breaking Bad",
			Year:        2008,
			Overview:    "Walter White, a New Mexico chemistry teacher, is diagnosed with Stage III cancer.",
			PosterPath:  "/ggFHVNu6YYI5L9pCfOacjizRGt.jpg",
			PosterURL:   "https://image.tmdb.org/t/p/w500/ggFHVNu6YYI5L9pCfOacjizRGt.jpg",
			BackdropURL: "https://image.tmdb.org/t/p/w780/tsRy63Mu5cu8etL1X7ZLyf7UP1M.jpg",
			VoteAverage: 8.9,
			VoteCount:   12442,
			Date:        time.Date(2008, 1, 20, 0, 0, 0, 0, time.UTC),
		},
		{
			// No date and no artwork: both degrade to zero values.
			MediaType:  core.MediaTypeMovie,
			TMDBID:     999999,
			Title:      "Untitled Workprint",
			PosterPath: "",
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("TrendingWeek:\n got %+v\nwant %+v", got, want)
	}
	// The fixture says total_pages 1, so the shelf must not ask for a page
	// that does not exist.
	if seen := s.seen(); len(seen) != 1 || seen[0].path != "/trending/all/week" {
		t.Errorf("requests = %+v, want one /trending/all/week", seen)
	}
}

func TestPopularMoviesAndSeries(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/movie/popular": {okJSON(t, "movie_popular.json")},
		"/tv/popular":    {okJSON(t, "tv_popular.json")},
	})
	ctx := context.Background()

	movies, err := c.PopularMovies(ctx)
	if err != nil {
		t.Fatalf("PopularMovies: %v", err)
	}
	// The fixture reports many pages, so the shelf fetches two — and the stub
	// answers page 2 with the same body, which is exactly what a popularity
	// list that reordered under the client does. The rows must not double up.
	if len(movies) != 2 {
		t.Fatalf("PopularMovies returned %d items, want 2 (pages merged and deduped)", len(movies))
	}
	pages := []string{}
	for _, req := range s.seen() {
		pages = append(pages, req.query.Get("page"))
	}
	if !reflect.DeepEqual(pages, []string{"1", "2"}) {
		t.Errorf("pages requested = %v, want [1 2]", pages)
	}
	// The endpoint implies the type; the results carry no media_type at all.
	for _, m := range movies {
		if m.MediaType != core.MediaTypeMovie {
			t.Errorf("MediaType = %q, want %q", m.MediaType, core.MediaTypeMovie)
		}
	}
	if movies[0].Title != "Blade Runner" || movies[0].Year != 1982 {
		t.Errorf("movies[0] = %+v, want Blade Runner (1982)", movies[0])
	}

	series, err := c.PopularSeries(ctx)
	if err != nil {
		t.Fatalf("PopularSeries: %v", err)
	}
	if len(series) != 2 {
		t.Fatalf("PopularSeries returned %d items, want 2", len(series))
	}
	for _, sr := range series {
		if sr.MediaType != core.MediaTypeSeries {
			t.Errorf("MediaType = %q, want %q", sr.MediaType, core.MediaTypeSeries)
		}
	}
	if series[0].Title != "Breaking Bad" || series[0].Year != 2008 {
		t.Errorf("series[0] = %+v, want Breaking Bad (2008)", series[0])
	}
	if series[1].Year != 0 || series[1].PosterURL != "" {
		t.Errorf("series[1] = %+v, want a zero year and no poster", series[1])
	}
}

// Two full TMDB pages are 40 rows; a home shelf hands the client at most
// homeShelfLimit of them.
func TestHomeShelfIsCapped(t *testing.T) {
	page := func(n int) response {
		var b strings.Builder
		fmt.Fprintf(&b, `{"page":%d,"total_pages":10,"results":[`, n)
		for i := 0; i < 20; i++ {
			if i > 0 {
				b.WriteString(",")
			}
			fmt.Fprintf(&b, `{"id":%d,"title":"Movie %d"}`, n*100+i, n*100+i)
		}
		b.WriteString("]}")
		return response{status: http.StatusOK, body: []byte(b.String())}
	}
	c, _ := newStub(t, map[string][]response{
		"/movie/popular": {page(1), page(2)},
	})

	got, err := c.PopularMovies(context.Background())
	if err != nil {
		t.Fatalf("PopularMovies: %v", err)
	}
	if len(got) != homeShelfLimit {
		t.Errorf("len = %d, want %d", len(got), homeShelfLimit)
	}
	// The cap trims the tail, not the head: the list stays most-popular-first.
	if got[0].TMDBID != 100 {
		t.Errorf("first item = %d, want 100", got[0].TMDBID)
	}
}

func TestMoviesByCompany(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/discover/movie": {okJSON(t, "discover_movie.json")},
	})

	got, err := c.MoviesByCompany(context.Background(), 41077, 2)
	if err != nil {
		t.Fatalf("MoviesByCompany: %v", err)
	}
	if got.Page != 2 || got.TotalPages != 7 {
		t.Errorf("page/total = %d/%d, want 2/7", got.Page, got.TotalPages)
	}
	if len(got.Items) != 1 || got.Items[0].TMDBID != 530385 {
		t.Fatalf("items = %+v, want one Midsommar", got.Items)
	}
	if got.Items[0].MediaType != core.MediaTypeMovie {
		t.Errorf("MediaType = %q, want %q", got.Items[0].MediaType, core.MediaTypeMovie)
	}
	if got.Items[0].VoteCount != 6100 {
		t.Errorf("VoteCount = %d, want 6100", got.Items[0].VoteCount)
	}

	q := s.seen()[0].query
	if q.Get("with_companies") != "41077" {
		t.Errorf("with_companies = %q, want 41077", q.Get("with_companies"))
	}
	if q.Get("page") != "2" {
		t.Errorf("page = %q, want 2", q.Get("page"))
	}
}

func TestSeriesByNetwork(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/discover/tv": {okJSON(t, "discover_tv.json")},
	})

	got, err := c.SeriesByNetwork(context.Background(), 213, 3)
	if err != nil {
		t.Fatalf("SeriesByNetwork: %v", err)
	}
	if got.Page != 3 || got.TotalPages != 42 {
		t.Errorf("page/total = %d/%d, want 3/42", got.Page, got.TotalPages)
	}
	if len(got.Items) != 1 || got.Items[0].Title != "Stranger Things" {
		t.Fatalf("items = %+v, want one Stranger Things", got.Items)
	}
	if got.Items[0].MediaType != core.MediaTypeSeries {
		t.Errorf("MediaType = %q, want %q", got.Items[0].MediaType, core.MediaTypeSeries)
	}
	if got.Items[0].VoteCount != 16000 {
		t.Errorf("VoteCount = %d, want 16000", got.Items[0].VoteCount)
	}

	q := s.seen()[0].query
	if q.Get("with_networks") != "213" {
		t.Errorf("with_networks = %q, want 213", q.Get("with_networks"))
	}
}

func TestBrowsePageIsClamped(t *testing.T) {
	tests := []struct {
		name string
		page int
		want string
	}{
		{name: "zero becomes one", page: 0, want: "1"},
		{name: "negative becomes one", page: -4, want: "1"},
		{name: "past the ceiling is capped", page: 9000, want: "500"},
		{name: "in range is forwarded", page: 12, want: "12"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, s := newStub(t, map[string][]response{
				"/discover/tv": {okJSON(t, "discover_tv.json")},
			})
			if _, err := c.SeriesByNetwork(context.Background(), 213, tt.page); err != nil {
				t.Fatalf("SeriesByNetwork: %v", err)
			}
			if got := s.seen()[0].query.Get("page"); got != tt.want {
				t.Errorf("page = %q, want %q", got, tt.want)
			}
		})
	}
}

// A big catalogue reports thousands of pages while TMDB refuses to serve past
// maxPage. Reporting the raw count would have a client ask for page 501, be
// handed page 500 again, and append it twice.
func TestBrowseTotalPagesIsClampedToTheCeiling(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		"/discover/tv": {{
			status: http.StatusOK,
			body:   []byte(`{"page":500,"total_pages":9134,"results":[]}`),
		}},
	})

	got, err := c.SeriesByNetwork(context.Background(), 213, 500)
	if err != nil {
		t.Fatalf("SeriesByNetwork: %v", err)
	}
	if got.TotalPages != maxPage {
		t.Errorf("TotalPages = %d, want it clamped to %d", got.TotalPages, maxPage)
	}
}

func TestMovieDetail(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/movie/78": {okJSON(t, "movie_detail_append.json")},
	})

	got, err := c.MovieDetail(context.Background(), 78)
	if err != nil {
		t.Fatalf("MovieDetail: %v", err)
	}

	if got.MediaType != core.MediaTypeMovie || got.TMDBID != 78 {
		t.Errorf("item = %+v, want movie 78", got.DiscoverItem)
	}
	if got.Title != "Blade Runner" || got.Year != 1982 {
		t.Errorf("title/year = %q/%d, want Blade Runner/1982", got.Title, got.Year)
	}
	if got.VoteCount != 12894 {
		t.Errorf("VoteCount = %d, want 12894", got.VoteCount)
	}
	if got.Runtime != 117 {
		t.Errorf("Runtime = %d, want 117", got.Runtime)
	}
	if got.IMDBID != "tt0083658" {
		t.Errorf("IMDBID = %q, want tt0083658", got.IMDBID)
	}
	// The studio is the first billed production company, not the last.
	if got.Network != "The Ladd Company" {
		t.Errorf("Network = %q, want The Ladd Company", got.Network)
	}
	if got.Language != "en" {
		t.Errorf("Language = %q, want en", got.Language)
	}
	if !got.LastAired.IsZero() {
		t.Errorf("LastAired = %v, want zero on a movie", got.LastAired)
	}
	if want := []string{"Drama", "Science Fiction"}; !reflect.DeepEqual(got.Genres, want) {
		t.Errorf("Genres = %v, want %v", got.Genres, want)
	}
	wantCast := []core.CastMember{
		{TMDBID: 3, Name: "Harrison Ford", Character: "Rick Deckard",
			ProfileURL: "https://image.tmdb.org/t/p/w500/zVnHagUvXkR2StdOtquEwsiwSVt.jpg"},
		// No headshot stays empty rather than becoming a broken URL.
		{TMDBID: 3899, Name: "Rutger Hauer", Character: "Roy Batty"},
	}
	if !reflect.DeepEqual(got.Cast, wantCast) {
		t.Errorf("Cast:\n got %+v\nwant %+v", got.Cast, wantCast)
	}
	if len(got.Recommendations) != 1 || got.Recommendations[0].TMDBID != 335984 {
		t.Fatalf("Recommendations = %+v, want Blade Runner 2049", got.Recommendations)
	}
	// A movie's recommendations are movies: the appended list carries no
	// media_type, so it has to inherit the parent's.
	if got.Recommendations[0].MediaType != core.MediaTypeMovie {
		t.Errorf("recommendation MediaType = %q, want %q",
			got.Recommendations[0].MediaType, core.MediaTypeMovie)
	}
	if got.Recommendations[0].VoteAverage != 7.5 || got.Recommendations[0].VoteCount != 0 {
		t.Errorf("recommendation rating = %v/%d, want 7.5/0 when vote_count is omitted",
			got.Recommendations[0].VoteAverage, got.Recommendations[0].VoteCount)
	}
	if len(got.Seasons) != 0 {
		t.Errorf("Seasons = %+v, want none on a movie", got.Seasons)
	}

	if appended := s.seen()[0].query.Get("append_to_response"); appended != detailAppend {
		t.Errorf("append_to_response = %q, want %q", appended, detailAppend)
	}
}

func TestSeriesDetail(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/tv/1396": {okJSON(t, "tv_detail_append.json")},
	})

	got, err := c.SeriesDetail(context.Background(), 1396)
	if err != nil {
		t.Fatalf("SeriesDetail: %v", err)
	}

	if got.MediaType != core.MediaTypeSeries || got.TMDBID != 1396 {
		t.Errorf("item = %+v, want series 1396", got.DiscoverItem)
	}
	if got.Title != "Breaking Bad" || got.Year != 2008 {
		t.Errorf("title/year = %q/%d, want Breaking Bad/2008", got.Title, got.Year)
	}
	if got.VoteCount != 12442 {
		t.Errorf("VoteCount = %d, want 12442", got.VoteCount)
	}
	if got.Status != "Ended" {
		t.Errorf("Status = %q, want Ended", got.Status)
	}
	// A series has a list of episode runtimes; the first is the typical one.
	if got.Runtime != 49 {
		t.Errorf("Runtime = %d, want 49", got.Runtime)
	}
	if got.IMDBID != "tt0903747" || got.TVDBID != 81189 {
		t.Errorf("external ids = %q/%d, want tt0903747/81189", got.IMDBID, got.TVDBID)
	}
	// A series that later streamed elsewhere still lists its originating
	// network first, which is the one the detail screen credits.
	if got.Network != "AMC" {
		t.Errorf("Network = %q, want AMC", got.Network)
	}
	if want := time.Date(2013, 9, 29, 0, 0, 0, 0, time.UTC); !got.LastAired.Equal(want) {
		t.Errorf("LastAired = %v, want %v", got.LastAired, want)
	}
	if got.Language != "en" {
		t.Errorf("Language = %q, want en", got.Language)
	}
	wantSeasons := []core.DiscoverSeason{
		{
			Number:       0,
			Title:        "Specials",
			PosterURL:    "https://image.tmdb.org/t/p/w500/40dT82MCkGZOOxCwAdRjnBTNVoZ.jpg",
			AirDate:      time.Date(2009, 2, 17, 0, 0, 0, 0, time.UTC),
			EpisodeCount: 5,
		},
		{
			Number:       1,
			Title:        "Season 1",
			Overview:     "High school chemistry teacher Walter White's life is suddenly transformed.",
			PosterURL:    "https://image.tmdb.org/t/p/w500/1BP4xYv9ZG4ZVHkL7ocOziBbSYH.jpg",
			AirDate:      time.Date(2008, 1, 20, 0, 0, 0, 0, time.UTC),
			EpisodeCount: 7,
		},
	}
	if !reflect.DeepEqual(got.Seasons, wantSeasons) {
		t.Errorf("Seasons:\n got %+v\nwant %+v", got.Seasons, wantSeasons)
	}
	if len(got.Cast) != 1 || got.Cast[0].Name != "Bryan Cranston" {
		t.Errorf("Cast = %+v, want Bryan Cranston", got.Cast)
	}
	if len(got.Recommendations) != 1 || got.Recommendations[0].MediaType != core.MediaTypeSeries {
		t.Fatalf("Recommendations = %+v, want one series", got.Recommendations)
	}
	if got.Recommendations[0].Title != "Stranger Things" {
		t.Errorf("recommendation title = %q, want Stranger Things", got.Recommendations[0].Title)
	}
	if got.Recommendations[0].VoteAverage != 8.6 || got.Recommendations[0].VoteCount != 0 {
		t.Errorf("recommendation rating = %v/%d, want 8.6/0 when vote_count is omitted",
			got.Recommendations[0].VoteAverage, got.Recommendations[0].VoteCount)
	}

	if appended := s.seen()[0].query.Get("append_to_response"); appended != detailAppend {
		t.Errorf("append_to_response = %q, want %q", appended, detailAppend)
	}
}

func TestDiscoverErrorsPropagate(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		"/tv/1396": {errJSON(t, 404, "error_404.json")},
	})

	if _, err := c.SeriesDetail(context.Background(), 1396); err == nil {
		t.Fatal("SeriesDetail: want error, got nil")
	}
}

func TestBackdropURL(t *testing.T) {
	c := New("k", nil)
	if got := c.backdropURL("/a.jpg"); got != "https://image.tmdb.org/t/p/w780/a.jpg" {
		t.Errorf("backdropURL = %q, want the w780 prefix", got)
	}
	if got := c.backdropURL(""); got != "" {
		t.Errorf("backdropURL(\"\") = %q, want empty", got)
	}
}
