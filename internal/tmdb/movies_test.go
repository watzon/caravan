package tmdb

import (
	"context"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

func TestSearchMovies(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		"/search/movie": {okJSON(t, "search_movie.json")},
	})

	got, err := c.SearchMovies(context.Background(), "blade runner")
	if err != nil {
		t.Fatalf("SearchMovies: %v", err)
	}

	want := []core.MovieMeta{
		{
			TMDBID:        78,
			Title:         "Blade Runner",
			OriginalTitle: "Blade Runner",
			Year:          1982,
			Overview:      "In the smog-choked dystopian Los Angeles of 2019, blade runner Rick Deckard is called out of retirement to terminate a quartet of replicants who have escaped to Earth seeking their creator for a way to extend their short life spans.",
			VoteAverage:   7.9,
			ReleaseDate:   time.Date(1982, 6, 25, 0, 0, 0, 0, time.UTC),
			PosterURL:     "https://image.tmdb.org/t/p/w500/63N9uy8nd9j7Eog2axPQ8lbr3Wj.jpg",
		},
		{
			TMDBID:        335984,
			Title:         "Blade Runner 2049",
			OriginalTitle: "Blade Runner 2049",
			Year:          2017,
			Overview:      "Thirty years after the events of the first film, a new blade runner, LAPD Officer K, unearths a long-buried secret that has the potential to plunge what's left of society into chaos.",
			VoteAverage:   7.6,
			ReleaseDate:   time.Date(2017, 10, 4, 0, 0, 0, 0, time.UTC),
			PosterURL:     "https://image.tmdb.org/t/p/w500/gajva2L0rPYkEWjzgFlBXCAVBE5.jpg",
		},
		{
			// No release date and no poster: both must degrade to zero
			// values rather than being dropped or faked.
			TMDBID:        999999,
			Title:         "Blade Runner: Untitled Workprint",
			OriginalTitle: "Blade Runner: Untitled Workprint",
		},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("result %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
	}
}

func TestSearchMoviesEmptyResults(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		"/search/movie": {{status: http.StatusOK, body: []byte(`{"page":1,"results":[],"total_results":0}`)}},
	})

	got, err := c.SearchMovies(context.Background(), "nothing matches this")
	if err != nil {
		t.Fatalf("SearchMovies: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d results, want 0", len(got))
	}
}

func TestGetMovie(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/movie/78": {okJSON(t, "movie_detail.json")},
	})

	got, err := c.GetMovie(context.Background(), 78)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}

	want := core.MovieMeta{
		TMDBID:        78,
		IMDBID:        "tt0083658",
		Title:         "Blade Runner",
		OriginalTitle: "Blade Runner",
		Year:          1982,
		Overview:      "In the smog-choked dystopian Los Angeles of 2019, blade runner Rick Deckard is called out of retirement to terminate a quartet of replicants who have escaped to Earth seeking their creator for a way to extend their short life spans.",
		VoteAverage:   7.9,
		ReleaseDate:   time.Date(1982, 6, 25, 0, 0, 0, 0, time.UTC),
		// The earliest of each home-release type across every region, not the
		// US one: the fixture's earliest digital is German, its earliest
		// physical American, so both directions of the cross-region pick are
		// proven. The type-3 theatrical entries must not leak into either.
		DigitalRelease:  time.Date(2011, 11, 4, 0, 0, 0, 0, time.UTC),
		PhysicalRelease: time.Date(2007, 12, 18, 0, 0, 0, 0, time.UTC),
		PosterURL:       "https://image.tmdb.org/t/p/w500/63N9uy8nd9j7Eog2axPQ8lbr3Wj.jpg",
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("GetMovie:\n got %+v\nwant %+v", *got, want)
	}
	if appended := s.seen()[0].query.Get("append_to_response"); appended != "release_dates" {
		t.Errorf("append_to_response = %q, want release_dates", appended)
	}
}

func TestMovieVoteAverageDefaultsToZero(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		"/search/movie": {{status: http.StatusOK, body: []byte(`{"results":[{"id":1}]}`)}},
		"/movie/1":      {{status: http.StatusOK, body: []byte(`{"id":1}`)}},
	})

	results, err := c.SearchMovies(context.Background(), "unrated")
	if err != nil {
		t.Fatalf("SearchMovies: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if got := results[0].VoteAverage; got != 0 {
		t.Errorf("search VoteAverage = %v, want 0", got)
	}

	detail, err := c.GetMovie(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if got := detail.VoteAverage; got != 0 {
		t.Errorf("detail VoteAverage = %v, want 0", got)
	}
}
