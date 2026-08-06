package tmdb

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

func TestSearchSeries(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		"/search/tv": {okJSON(t, "search_tv.json")},
	})

	got, err := c.SearchSeries(context.Background(), "breaking bad")
	if err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}

	want := []core.SeriesMeta{
		{
			TMDBID:        1396,
			Title:         "Breaking Bad",
			OriginalTitle: "Breaking Bad",
			Year:          2008,
			Overview:      "Walter White, a New Mexico chemistry teacher, is diagnosed with Stage III cancer and given a prognosis of only two years left to live.",
			VoteAverage:   8.9,
			FirstAirDate:  time.Date(2008, 1, 20, 0, 0, 0, 0, time.UTC),
			PosterURL:     "https://image.tmdb.org/t/p/w500/ggFHVNu6YYI5L9pCfOacjizRGt.jpg",
		},
		{
			// Unaired show with no poster: zero year, blank poster URL.
			TMDBID:        90228,
			Title:         "The Broken and the Bad",
			OriginalTitle: "The Broken and the Bad",
			Overview:      "A companion series exploring the world of Breaking Bad.",
			VoteAverage:   6.8,
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

func TestGetSeries(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/tv/1396":          {okJSON(t, "tv_detail.json")},
		"/tv/1396/season/0": {okJSON(t, "tv_season_0.json")},
		"/tv/1396/season/1": {okJSON(t, "tv_season_1.json")},
	})

	got, err := c.GetSeries(context.Background(), 1396)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	want := core.SeriesMeta{
		TMDBID:        1396,
		TVDBID:        81189,
		IMDBID:        "tt0903747",
		Title:         "Breaking Bad",
		OriginalTitle: "Breaking Bad",
		Year:          2008,
		Overview:      "Walter White, a New Mexico chemistry teacher, is diagnosed with Stage III cancer and given a prognosis of only two years left to live.",
		VoteAverage:   8.9,
		Status:        "Ended",
		FirstAirDate:  time.Date(2008, 1, 20, 0, 0, 0, 0, time.UTC),
		PosterURL:     "https://image.tmdb.org/t/p/w500/ggFHVNu6YYI5L9pCfOacjizRGt.jpg",
		Seasons: []core.SeasonMeta{
			{
				// Specials are season 0, per SPEC §7.
				Number:    0,
				Title:     "Specials",
				AirDate:   time.Date(2009, 2, 17, 0, 0, 0, 0, time.UTC),
				PosterURL: "https://image.tmdb.org/t/p/w500/40dT82MCkGZOOxCwAdRjnBTNVoZ.jpg",
				Episodes: []core.EpisodeMeta{
					{
						TMDBID:   62165,
						Season:   0,
						Number:   1,
						Title:    "Good Cop / Bad Cop",
						Overview: "Hank and Marie discuss the finer points of good cop, bad cop.",
						AirDate:  time.Date(2009, 2, 17, 0, 0, 0, 0, time.UTC),
					},
					{
						// Unaired: zero AirDate, not an error.
						TMDBID: 62166,
						Season: 0,
						Number: 2,
						Title:  "Wedding Day",
					},
				},
			},
			{
				Number:    1,
				Title:     "Season 1",
				Overview:  "High school chemistry teacher Walter White's life is suddenly transformed by a dire medical diagnosis.",
				AirDate:   time.Date(2008, 1, 20, 0, 0, 0, 0, time.UTC),
				PosterURL: "https://image.tmdb.org/t/p/w500/1BP4xYv9ZG4ZVHkL7ocOziBbSYH.jpg",
				Episodes: []core.EpisodeMeta{
					{
						TMDBID:   62085,
						Season:   1,
						Number:   1,
						Title:    "Pilot",
						Overview: "When an unassuming high school chemistry teacher discovers he has a rare form of lung cancer, he decides to team up with a former student and create a top of the line crystal meth in a used RV.",
						AirDate:  time.Date(2008, 1, 20, 0, 0, 0, 0, time.UTC),
					},
					{
						TMDBID:   62086,
						Season:   1,
						Number:   2,
						Title:    "Cat's in the Bag...",
						Overview: "Walt and Jesse attempt to tie up loose ends.",
						AirDate:  time.Date(2008, 1, 27, 0, 0, 0, 0, time.UTC),
					},
				},
			},
		},
	}

	if !reflect.DeepEqual(*got, want) {
		t.Errorf("GetSeries:\n got %+v\nwant %+v", *got, want)
	}

	seen := s.seen()
	wantPaths := []string{"/tv/1396", "/tv/1396/season/0", "/tv/1396/season/1"}
	if len(seen) != len(wantPaths) {
		t.Fatalf("requests = %d, want %d", len(seen), len(wantPaths))
	}
	for i, p := range wantPaths {
		if seen[i].path != p {
			t.Errorf("request %d path = %q, want %q", i, seen[i].path, p)
		}
	}
	if a := seen[0].query.Get("append_to_response"); a != "external_ids" {
		t.Errorf("append_to_response = %q, want external_ids", a)
	}
}

func TestSeriesVoteAverageDefaultsToZero(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		"/search/tv": {{status: http.StatusOK, body: []byte(`{"results":[{"id":1}]}`)}},
		"/tv/1":      {{status: http.StatusOK, body: []byte(`{"id":1}`)}},
	})

	results, err := c.SearchSeries(context.Background(), "unrated")
	if err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	if got := results[0].VoteAverage; got != 0 {
		t.Errorf("search VoteAverage = %v, want 0", got)
	}

	detail, err := c.GetSeries(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got := detail.VoteAverage; got != 0 {
		t.Errorf("detail VoteAverage = %v, want 0", got)
	}
}

func TestGetSeriesPropagatesSeasonFailure(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		"/tv/1396":          {okJSON(t, "tv_detail.json")},
		"/tv/1396/season/0": {errJSON(t, http.StatusNotFound, "error_404.json")},
		"/tv/1396/season/1": {okJSON(t, "tv_season_1.json")},
	})

	_, err := c.GetSeries(context.Background(), 1396)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound from the season fetch", err)
	}
}
