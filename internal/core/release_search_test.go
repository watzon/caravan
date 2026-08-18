package core

import (
	"slices"
	"testing"
	"time"
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

func TestSceneSearchesExactDateThenTitle(t *testing.T) {
	released := time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC)
	got := SceneSearches("Brazzers", released, "Deep Impact")
	want := []SceneSearch{
		{Variant: SceneSearchByDate, Query: "Brazzers 22.03.14"},
		{Variant: SceneSearchByTitle, Query: "Brazzers Deep Impact"},
	}
	if !equalSceneSearches(got, want) {
		t.Fatalf("SceneSearches = %#v, want %#v", got, want)
	}
}

func TestNearbySceneSearchesAsksAdjacentDaysBeforeTitle(t *testing.T) {
	released := time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC)
	got := NearbySceneSearches("Brazzers", released, "Deep Impact")
	want := []SceneSearch{
		{Variant: SceneSearchByDate, Query: "Brazzers 22.03.14"},
		{Variant: SceneSearchByDate, Query: "Brazzers 22.03.13"},
		{Variant: SceneSearchByDate, Query: "Brazzers 22.03.15"},
		{Variant: SceneSearchByTitle, Query: "Brazzers Deep Impact"},
	}
	if !equalSceneSearches(got, want) {
		t.Fatalf("NearbySceneSearches = %#v, want %#v", got, want)
	}

	if got := NearbySceneSearches("Brazzers", time.Time{}, "Deep Impact"); !equalSceneSearches(got, SceneSearches("Brazzers", time.Time{}, "Deep Impact")) {
		t.Fatalf("a scene with no date must not invent adjacent queries: %#v", got)
	}
}

func equalSceneSearches(got, want []SceneSearch) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
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
