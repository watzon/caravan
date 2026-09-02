package tmdb

import (
	"context"
	"errors"
	"maps"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// wantQuery asserts the exact parameter set the client sent, api_key aside.
// An equality check rather than a spot check: a filter that leaks an extra
// parameter, or spells one TMDB ignores, is the failure these tests exist to
// catch.
func wantQuery(t *testing.T, got url.Values, want map[string]string) {
	t.Helper()

	flat := map[string]string{}
	for k, vs := range got {
		if k == "api_key" {
			continue
		}
		if len(vs) != 1 {
			t.Errorf("%s sent %d values, want exactly one", k, len(vs))
			continue
		}
		flat[k] = vs[0]
	}
	if !reflect.DeepEqual(flat, want) {
		for _, k := range slices.Sorted(maps.Keys(flat)) {
			if want[k] != flat[k] {
				t.Errorf("%s = %q, want %q", k, flat[k], want[k])
			}
		}
		for _, k := range slices.Sorted(maps.Keys(want)) {
			if _, ok := flat[k]; !ok {
				t.Errorf("%s missing, want %q", k, want[k])
			}
		}
		t.Errorf("query:\n got %v\nwant %v", flat, want)
	}
}

func date(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// TestDiscoverMoviesSendsEveryFilter is the movie half of the param-mapping
// proof: one filter with every field set, one exact query.
func TestDiscoverMoviesSendsEveryFilter(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/discover/movie": {okJSON(t, "discover_movie.json")},
	})

	_, err := c.DiscoverMovies(context.Background(), core.MovieFilter{
		DiscoverFilter: core.DiscoverFilter{
			Genres:         []int64{28, 878},
			Companies:      []int64{41077},
			Keywords:       []int64{9715, 4565},
			ReleasedFrom:   date(1980, time.January, 1),
			ReleasedTo:     date(1989, time.December, 31),
			RuntimeMin:     90,
			RuntimeMax:     150,
			VoteAverageMin: 7.5,
			VoteCountMin:   200,
			Language:       "ja",
			Sort:           core.SortRating,
			Order:          core.OrderAsc,
			Page:           3,
		},
		Cast:   []int64{3},
		Crew:   []int64{525, 488},
		People: []int64{1245},
	})
	if err != nil {
		t.Fatalf("DiscoverMovies: %v", err)
	}

	seen := s.seen()
	if len(seen) != 1 || seen[0].path != "/discover/movie" {
		t.Fatalf("requests = %+v, want one /discover/movie", seen)
	}
	wantQuery(t, seen[0].query, map[string]string{
		"with_genres":              "28,878",
		"with_companies":           "41077",
		"with_keywords":            "9715,4565",
		"with_cast":                "3",
		"with_crew":                "525,488",
		"with_people":              "1245",
		"primary_release_date.gte": "1980-01-01",
		"primary_release_date.lte": "1989-12-31",
		"with_runtime.gte":         "90",
		"with_runtime.lte":         "150",
		"vote_average.gte":         "7.5",
		"vote_count.gte":           "200",
		"with_original_language":   "ja",
		"sort_by":                  "vote_average.asc",
		"page":                     "3",
	})
}

// TestDiscoverSeriesSendsEveryFilter is the series half. The date parameter
// changes name, networks appear, and there is no person parameter to send:
// SeriesFilter has nowhere to put one.
func TestDiscoverSeriesSendsEveryFilter(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/discover/tv": {okJSON(t, "discover_tv.json")},
	})

	_, err := c.DiscoverSeries(context.Background(), core.SeriesFilter{
		DiscoverFilter: core.DiscoverFilter{
			Genres:         []int64{10765},
			Companies:      []int64{2},
			Keywords:       []int64{4565},
			ReleasedFrom:   date(2015, time.June, 1),
			ReleasedTo:     date(2020, time.June, 1),
			RuntimeMin:     20,
			RuntimeMax:     45,
			VoteAverageMin: 8,
			VoteCountMin:   50,
			Language:       "en",
			Sort:           core.SortReleaseDate,
			Order:          core.OrderDesc,
			Page:           2,
		},
		Networks: []int64{213, 49},
	})
	if err != nil {
		t.Fatalf("DiscoverSeries: %v", err)
	}

	seen := s.seen()
	if len(seen) != 1 || seen[0].path != "/discover/tv" {
		t.Fatalf("requests = %+v, want one /discover/tv", seen)
	}
	wantQuery(t, seen[0].query, map[string]string{
		"with_genres":            "10765",
		"with_companies":         "2",
		"with_keywords":          "4565",
		"with_networks":          "213,49",
		"first_air_date.gte":     "2015-06-01",
		"first_air_date.lte":     "2020-06-01",
		"with_runtime.gte":       "20",
		"with_runtime.lte":       "45",
		"vote_average.gte":       "8",
		"vote_count.gte":         "50",
		"with_original_language": "en",
		"sort_by":                "first_air_date.desc",
		"page":                   "2",
	})
	// The seam, asserted rather than only documented: nothing person-shaped
	// may reach /discover/tv, because TMDB ignores it there.
	for _, param := range []string{"with_cast", "with_crew", "with_people"} {
		if seen[0].query.Has(param) {
			t.Errorf("%s reached /discover/tv; TMDB ignores it there", param)
		}
	}
}

// An empty filter is the unfiltered catalogue: no half-set bounds, no
// present-but-empty id lists (which TMDB reads as "match nothing").
func TestDiscoverEmptyFilterSendsOnlyTheDefaults(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/discover/movie": {okJSON(t, "discover_movie.json")},
		"/discover/tv":    {okJSON(t, "discover_tv.json")},
	})
	ctx := context.Background()

	if _, err := c.DiscoverMovies(ctx, core.MovieFilter{}); err != nil {
		t.Fatalf("DiscoverMovies: %v", err)
	}
	wantQuery(t, s.seen()[0].query, map[string]string{
		"sort_by": "popularity.desc",
		"page":    "1",
	})

	if _, err := c.DiscoverSeries(ctx, core.SeriesFilter{}); err != nil {
		t.Fatalf("DiscoverSeries: %v", err)
	}
	wantQuery(t, s.seen()[1].query, map[string]string{
		"sort_by": "popularity.desc",
		"page":    "1",
	})
}

// The same ordering means different fields on the two endpoints; a sort that
// is not one of the five falls back to the default rather than being forwarded.
func TestDiscoverSortMapsPerEndpoint(t *testing.T) {
	tests := []struct {
		name       string
		sort       core.DiscoverSort
		order      core.DiscoverOrder
		wantMovie  string
		wantSeries string
	}{
		{"popularity", core.SortPopularity, core.OrderDesc, "popularity.desc", "popularity.desc"},
		{"release date", core.SortReleaseDate, core.OrderAsc, "primary_release_date.asc", "first_air_date.asc"},
		{"rating", core.SortRating, core.OrderDesc, "vote_average.desc", "vote_average.desc"},
		{"votes", core.SortVotes, core.OrderDesc, "vote_count.desc", "vote_count.desc"},
		{"title", core.SortTitle, core.OrderAsc, "original_title.asc", "name.asc"},
		{"unknown falls back", core.DiscoverSort("revenue"), core.OrderAsc, "popularity.desc", "popularity.desc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, s := newStub(t, map[string][]response{
				"/discover/movie": {okJSON(t, "discover_movie.json")},
				"/discover/tv":    {okJSON(t, "discover_tv.json")},
			})
			ctx := context.Background()
			f := core.DiscoverFilter{Sort: tt.sort, Order: tt.order}

			if _, err := c.DiscoverMovies(ctx, core.MovieFilter{DiscoverFilter: f}); err != nil {
				t.Fatalf("DiscoverMovies: %v", err)
			}
			if got := s.seen()[0].query.Get("sort_by"); got != tt.wantMovie {
				t.Errorf("movie sort_by = %q, want %q", got, tt.wantMovie)
			}

			if _, err := c.DiscoverSeries(ctx, core.SeriesFilter{DiscoverFilter: f}); err != nil {
				t.Fatalf("DiscoverSeries: %v", err)
			}
			if got := s.seen()[1].query.Get("sort_by"); got != tt.wantSeries {
				t.Errorf("series sort_by = %q, want %q", got, tt.wantSeries)
			}
		})
	}
}

// The curated shelves now route through the filter builders. Their query must
// not have changed: they are the same two TMDB requests they always were.
func TestCuratedShelvesKeepTheirQuery(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/discover/movie": {okJSON(t, "discover_movie.json")},
		"/discover/tv":    {okJSON(t, "discover_tv.json")},
	})
	ctx := context.Background()

	if _, err := c.MoviesByCompany(ctx, 41077, 2); err != nil {
		t.Fatalf("MoviesByCompany: %v", err)
	}
	wantQuery(t, s.seen()[0].query, map[string]string{
		"with_companies": "41077",
		"sort_by":        "popularity.desc",
		"page":           "2",
	})

	if _, err := c.SeriesByNetwork(ctx, 213, 3); err != nil {
		t.Fatalf("SeriesByNetwork: %v", err)
	}
	wantQuery(t, s.seen()[1].query, map[string]string{
		"with_networks": "213",
		"sort_by":       "popularity.desc",
		"page":          "3",
	})
}

func TestSearchPeople(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/search/person": {okJSON(t, "search_person.json")},
	})

	got, err := c.SearchPeople(context.Background(), "harrison")
	if err != nil {
		t.Fatalf("SearchPeople: %v", err)
	}
	want := []core.DiscoverPerson{
		{TMDBID: 3, Name: "Harrison Ford", Department: "Acting",
			ProfileURL: "https://image.tmdb.org/t/p/w500/zVnHagUvXkR2StdOtquEwsiwSVt.jpg"},
		// No headshot stays empty rather than becoming a broken URL.
		{TMDBID: 525, Name: "Christopher Nolan", Department: "Directing"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SearchPeople:\n got %+v\nwant %+v", got, want)
	}
	wantQuery(t, s.seen()[0].query, map[string]string{"query": "harrison"})
}

func TestSearchCompanies(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/search/company": {okJSON(t, "search_company.json")},
	})

	got, err := c.SearchCompanies(context.Background(), "a24")
	if err != nil {
		t.Fatalf("SearchCompanies: %v", err)
	}
	want := []core.DiscoverCompany{
		{TMDBID: 41077, Name: "A24", Country: "US",
			LogoURL: "https://image.tmdb.org/t/p/w500/1ZXsGaFPgrgS6ZZGS37AqD5uU12.png"},
		{TMDBID: 10342, Name: "Studio Ghibli", Country: "JP"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SearchCompanies:\n got %+v\nwant %+v", got, want)
	}
	wantQuery(t, s.seen()[0].query, map[string]string{"query": "a24"})
}

func TestSearchKeywords(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/search/keyword": {okJSON(t, "search_keyword.json")},
	})

	got, err := c.SearchKeywords(context.Background(), "hero")
	if err != nil {
		t.Fatalf("SearchKeywords: %v", err)
	}
	want := []core.DiscoverKeyword{
		{TMDBID: 9715, Name: "superhero"},
		{TMDBID: 4565, Name: "dystopia"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SearchKeywords:\n got %+v\nwant %+v", got, want)
	}
	wantQuery(t, s.seen()[0].query, map[string]string{"query": "hero"})
}

// The genre vocabularies are per media type and fetched once each: a filter
// rail rendered on every browse must not cost a round trip every time.
func TestGenresAreFetchedOncePerMediaType(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/genre/movie/list": {okJSON(t, "genre_movie_list.json")},
		"/genre/tv/list":    {okJSON(t, "genre_tv_list.json")},
	})
	ctx := context.Background()

	movies, err := c.Genres(ctx, core.MediaTypeMovie)
	if err != nil {
		t.Fatalf("Genres(movie): %v", err)
	}
	want := []core.DiscoverGenre{{TMDBID: 28, Name: "Action"}, {TMDBID: 878, Name: "Science Fiction"}}
	if !reflect.DeepEqual(movies, want) {
		t.Errorf("Genres(movie) = %+v, want %+v", movies, want)
	}

	series, err := c.Genres(ctx, core.MediaTypeSeries)
	if err != nil {
		t.Fatalf("Genres(series): %v", err)
	}
	if len(series) != 2 || series[0].Name != "Action & Adventure" {
		t.Errorf("Genres(series) = %+v, want the TV vocabulary", series)
	}

	// Second time round, neither costs a request.
	for i := 0; i < 3; i++ {
		if _, err := c.Genres(ctx, core.MediaTypeMovie); err != nil {
			t.Fatalf("Genres(movie) repeat: %v", err)
		}
		if _, err := c.Genres(ctx, core.MediaTypeSeries); err != nil {
			t.Fatalf("Genres(series) repeat: %v", err)
		}
	}
	if seen := s.seen(); len(seen) != 2 {
		t.Errorf("requests = %d, want 2: each genre list is fetched once", len(seen))
	}
}

func TestGenresRejectsAnUnknownMediaType(t *testing.T) {
	c, s := newStub(t, map[string][]response{})

	if _, err := c.Genres(context.Background(), "scene"); !errors.Is(err, ErrUnsupportedMediaType) {
		t.Errorf("Genres(scene) error = %v, want ErrUnsupportedMediaType", err)
	}
	if seen := s.seen(); len(seen) != 0 {
		t.Errorf("requests = %+v, want none", seen)
	}
}

// A failed fetch must not be remembered as an empty vocabulary.
func TestGenresDoNotCacheAFailure(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/genre/movie/list": {
			errJSON(t, http.StatusInternalServerError, "error_404.json"),
			okJSON(t, "genre_movie_list.json"),
		},
	})
	ctx := context.Background()

	if _, err := c.Genres(ctx, core.MediaTypeMovie); err == nil {
		t.Fatal("Genres: want an error on the first call")
	}
	got, err := c.Genres(ctx, core.MediaTypeMovie)
	if err != nil {
		t.Fatalf("Genres retry: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("Genres = %+v, want the list the retry fetched", got)
	}
	if seen := s.seen(); len(seen) != 2 {
		t.Errorf("requests = %d, want 2: the failure was retried", len(seen))
	}
}
