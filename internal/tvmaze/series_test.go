package tvmaze

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// day is a UTC date, the only kind TVmaze's plain calendar days resolve to.
func day(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

func TestSearchSeries(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		searchShowsPath: {okJSON(t, "search_shows.json")},
	})

	got, err := c.SearchSeries(context.Background(), "breaking bad")
	if err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}

	want := []core.SeriesMeta{
		{
			Provider:    ProviderID,
			ProviderRef: "169",
			// The cross-provider ids are the reason to chain TVmaze behind
			// another provider at all.
			TVDBID: 81189,
			IMDBID: "tt0903747",
			Title:  "Breaking Bad",
			// OriginalTitle stays empty: TVmaze serves one name, and repeating
			// it would tell the matcher it had two pieces of evidence.
			Year:     2008,
			Overview: "Breaking Bad follows Walter White, a chemistry teacher.\n\nDiagnosed with cancer, he turns to a life of crime.",
			// TVmaze rates out of 10 already, which is core's scale.
			VoteAverage: 9.2,
			// TVmaze publishes no vote total. Inventing one would render as a
			// fact in the UI.
			VoteCount:    0,
			Status:       "Ended",
			FirstAirDate: day(2008, 1, 20),
			PosterURL:    "https://static.tvmaze.com/uploads/images/original_untouched/0/2400.jpg",
		},
		{
			Provider:    ProviderID,
			ProviderRef: "44778",
			Title:       "Better Call Saul",
			// Announced but not scheduled: no premiere, no rating, no artwork,
			// no external ids. Every one of those is null in the reply and none
			// of them may become a fabricated zero-of-zero on screen.
			Status: "Planned",
		},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("result %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
		// Search results carry no seasons: nothing on this page can build one.
		if got[i].Seasons != nil {
			t.Errorf("result %d carries seasons %+v, want none", i, got[i].Seasons)
		}
	}
}

// The real seasons are TVmaze's reason to exist in a chain: AniList synthesizes
// one because it has no episode documents, and these are the genuine article.
func TestGetSeriesBuildsRealSeasons(t *testing.T) {
	c, _ := newStub(t, showRoutes(t, 169))

	got, err := c.GetSeries(context.Background(), "169")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	want := []core.SeasonMeta{
		{
			Number: 1,
			Title:  "Season 1",
			// The earliest episode's date; TVmaze serves no season document of
			// its own.
			AirDate: day(2008, 1, 20),
			Episodes: []core.EpisodeMeta{
				{Season: 1, Number: 1, Title: "Pilot", Overview: "Diagnosed with terminal lung cancer, Walter White turns to cooking.", AirDate: day(2008, 1, 20)},
				{Season: 1, Number: 2, Title: "Cat's in the Bag...", Overview: "Walt and Jesse deal with the aftermath.", AirDate: day(2008, 1, 27)},
				{Season: 1, Number: 3, Title: "...And the Bag's in the River", AirDate: day(2008, 2, 10)},
			},
		},
		{
			Number:  2,
			Title:   "Season 2",
			AirDate: day(2009, 3, 8),
			// The fixture lists episode 2 before episode 1. The order on the
			// wire is not the contract; the numbers are.
			Episodes: []core.EpisodeMeta{
				{Season: 2, Number: 1, Title: "Seven Thirty-Seven", AirDate: day(2009, 3, 8)},
				{Season: 2, Number: 2, Title: "Grilled", AirDate: day(2009, 3, 15)},
			},
		},
		{
			Number:  3,
			Title:   "Season 3",
			AirDate: day(2010, 3, 21),
			Episodes: []core.EpisodeMeta{
				{Season: 3, Number: 1, Title: "No Más", AirDate: day(2010, 3, 21)},
				// No announced air date, which is what an unaired episode
				// carries too.
				{Season: 3, Number: 2, Title: "Caballo sin Nombre"},
			},
		},
	}

	if !reflect.DeepEqual(got.Seasons, want) {
		t.Errorf("Seasons:\n got %+v\nwant %+v", got.Seasons, want)
	}
	// The series half of the mapping is the same one search exercises; this is
	// the part only the detail document carries.
	if got.ProviderRef != "169" || got.Title != "Breaking Bad" {
		t.Errorf("GetSeries returned %q/%q, want 169/Breaking Bad", got.ProviderRef, got.Title)
	}
}

// A special carries `number: null`. Numbering it here would invent a fact that
// depends on how many specials have been catalogued so far, so an upstream edit
// would renumber the ones already filed and move real files off the episodes
// they matched. The file parks in review instead, which is visible and correct.
func TestSpecialsAreDropped(t *testing.T) {
	c, _ := newStub(t, showRoutes(t, 169))

	got, err := c.GetSeries(context.Background(), "169")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	for _, s := range got.Seasons {
		for _, e := range s.Episodes {
			if e.Number <= 0 {
				t.Errorf("season %d carries an unnumbered episode %+v", s.Number, e)
			}
			if e.Title == "Good Cop / Bad Cop" {
				t.Errorf("the fixture's unnumbered special was filed as S%02dE%02d", s.Number, e.Number)
			}
		}
	}
	if n := len(got.Seasons[0].Episodes); n != 3 {
		t.Errorf("season 1 has %d episodes, want 3: the special is not one of them", n)
	}
}

// A show with no episodes at all gets no seasons rather than an empty one: an
// invented season 1 would have the organizer file real files into a season the
// provider never claimed exists.
func TestNoEpisodesMeansNoSeasons(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		showPath(169):     {okJSON(t, "show.json")},
		episodesPath(169): {{status: 200, body: []byte(`[]`)}},
	})

	got, err := c.GetSeries(context.Background(), "169")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.Seasons != nil {
		t.Errorf("Seasons = %+v, want none", got.Seasons)
	}
}

func TestStatusMapping(t *testing.T) {
	tests := []struct {
		tvmaze string
		want   string
	}{
		{tvmaze: "Running", want: "Continuing"},
		{tvmaze: "Ended", want: "Ended"},
		// A show between seasons that nobody has cancelled is still running.
		// Reading this as Ended would tell someone their show is over during a
		// hiatus.
		{tvmaze: "To Be Determined", want: "Continuing"},
		{tvmaze: "In Development", want: "Planned"},
		// A value TVmaze has not invented yet is better empty than
		// mistranslated: an unknown status must not read as "Ended".
		{tvmaze: "Something New", want: ""},
		{tvmaze: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.tvmaze, func(t *testing.T) {
			show := showResult{Status: tt.tvmaze}
			if got := seriesMeta(show).Status; got != tt.want {
				t.Errorf("status %q mapped to %q, want %q", tt.tvmaze, got, tt.want)
			}
		})
	}
}
