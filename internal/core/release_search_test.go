package core

import (
	"slices"
	"testing"
)

func TestMovieSearchesFanOutYearAndBareTitleForms(t *testing.T) {
	if got := MovieSearches("Big Buck Bunny", 2008); !slices.Equal(got, []string{"Big Buck Bunny 2008", "Big Buck Bunny"}) {
		t.Fatalf("MovieSearches with year = %v", got)
	}
	if got := MovieSearches("Big Buck Bunny", 0); !slices.Equal(got, []string{"Big Buck Bunny"}) {
		t.Fatalf("MovieSearches without year = %v", got)
	}
	if got := MovieSearches("   ", 2008); got != nil {
		t.Fatalf("MovieSearches with blank title = %v, want nil", got)
	}
}

func TestSeriesSearchesFanOutEpisodeAndSeasonPackForms(t *testing.T) {
	if got := SeriesSearches("Some Show", 1, 2); !slices.Equal(got, []string{"Some Show S01E02", "Some Show S01"}) {
		t.Fatalf("SeriesSearches episode = %v", got)
	}
	if got := SeriesSearches("Some Show", 1, 0); !slices.Equal(got, []string{"Some Show S01"}) {
		t.Fatalf("SeriesSearches season = %v", got)
	}
	if got := SeriesSearches("Some Show", -1, 0); !slices.Equal(got, []string{"Some Show"}) {
		t.Fatalf("SeriesSearches whole series = %v", got)
	}
	if got := SeriesSearches("Specials Show", 0, 1); !slices.Equal(got, []string{"Specials Show S00E01", "Specials Show S00"}) {
		t.Fatalf("SeriesSearches specials = %v", got)
	}
}
