package anilist

import (
	"context"
	"fmt"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// day is a UTC date, the only kind AniList's fuzzy dates and airing timestamps
// resolve to here.
func day(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

// ep is one episode of the single synthesized season.
// Absolute is n and not 0: on AniList the episode number IS the absolute
// number, because each cour is its own Media record and nothing this record
// describes precedes its own episode 1. Every expectation below carries it, so
// a mapping that stopped emitting it could not pass quietly.
func ep(n int, title string, air time.Time) core.EpisodeMeta {
	return core.EpisodeMeta{Season: 1, Number: n, Absolute: n, Title: title, AirDate: air}
}

func TestSearchSeries(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		opSearchSeries: {okJSON(t, "search_anime.json")},
	})

	got, err := c.SearchSeries(context.Background(), "attack on titan")
	if err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}

	want := []core.SeriesMeta{
		{
			Provider:    ProviderID,
			ProviderRef: "16498",
			Title:       "Attack on Titan",
			// The romaji is what a release is named, and the library scores it
			// against OriginalTitle.
			OriginalTitle: "Shingeki no Kyojin",
			Year:          2013,
			Overview:      "Several hundred years ago, humans were nearly exterminated by titans.\n\n(Source: MangaHelpers)",
			// AniList rates out of 100; core is out of 10.
			VoteAverage: 8.4,
			// AniList serves no vote total, so the score histogram is summed.
			VoteCount:    11000,
			Status:       "Ended",
			FirstAirDate: day(2013, 4, 7),
			PosterURL:    "https://s4.anilist.co/file/anilistcdn/media/anime/cover/large/bx16498-C6FPmWm59CyP.jpg",
		},
		{
			Provider:    ProviderID,
			ProviderRef: "21519",
			// No English title: the romaji becomes Title, so the native script
			// is what is left to be the original.
			Title:         "Kimi no Na wa.",
			OriginalTitle: "君の名は。",
			// Year-only start date, widened to the 1st of January.
			Year:         2016,
			FirstAirDate: day(2016, 1, 1),
			Status:       "Ended",
			// Unscored: no average and no votes, not a fabricated 0-of-0.
			VoteAverage: 0,
			VoteCount:   0,
			// No extraLarge cover; the smaller one still beats no artwork.
			PosterURL: "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/bx21519-XIr3PeqA4Pgb.png",
		},
	}

	if len(got) != len(want) {
		t.Fatalf("got %d results, want %d", len(got), len(want))
	}
	for i := range want {
		if !reflect.DeepEqual(got[i], want[i]) {
			t.Errorf("result %d:\n got %+v\nwant %+v", i, got[i], want[i])
		}
		// Search results carry no season: nothing on this page can populate one.
		if got[i].Seasons != nil {
			t.Errorf("result %d carries seasons %+v, want none", i, got[i].Seasons)
		}
	}
}

func TestGetSeriesFinished(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		opGetSeries:      {okJSON(t, "media_finished.json")},
		opSeriesSchedule: {okJSON(t, "media_finished_schedule_2.json")},
	})

	got, err := c.GetSeries(context.Background(), "98202")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	want := core.SeriesMeta{
		Provider:      ProviderID,
		ProviderRef:   "98202",
		Title:         "Laid-Back Camp",
		OriginalTitle: "Yuru Camp△",
		Year:          2018,
		Overview:      "Rin camps alone at the foot of Mount Fuji.\n\n(Source: Crunchyroll)",
		VoteAverage:   8.2,
		VoteCount:     2542,
		Status:        "Ended",
		FirstAirDate:  day(2018, 1, 4),
		PosterURL:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/large/bx98202-cfSDlnyLOAcT.jpg",
		Seasons: []core.SeasonMeta{{
			// One season, always: AniList files a sequel as its own Media, and
			// a scanned "S01E05" has to land somewhere.
			Number:  1,
			Title:   "Season 1",
			AirDate: day(2018, 1, 4),
			Episodes: []core.EpisodeMeta{
				ep(1, "Mount Fuji and Curry Noodles", day(2018, 1, 4)),
				ep(2, "Welcome to the Outdoor Activities Circle!", day(2018, 1, 11)),
				// Listed with no name beyond its number: numbered, untitled.
				ep(3, "", day(2018, 1, 18)),
				ep(4, "", day(2018, 1, 25)),
				ep(5, "", day(2018, 2, 1)),
				ep(6, "", day(2018, 2, 8)),
				ep(7, "", day(2018, 2, 15)),
				ep(8, "", day(2018, 2, 22)),
				// Episodes 9-12 come from the second schedule page.
				ep(9, "", day(2018, 3, 1)),
				ep(10, "", day(2018, 3, 8)),
				ep(11, "", day(2018, 3, 15)),
				ep(12, "Mount Fuji and the Solo Camper Girls", day(2018, 3, 22)),
			},
		}},
	}

	if !reflect.DeepEqual(*got, want) {
		t.Errorf("GetSeries:\n got %+v\nwant %+v", *got, want)
	}

	seen := s.seen()
	if len(seen) != 2 {
		t.Fatalf("requests = %d, want 2 (the series and one further schedule page)", len(seen))
	}
	if seen[1].operationName != opSeriesSchedule {
		t.Errorf("second request = %q, want the lean schedule query", seen[1].operationName)
	}
	if p := seen[1].variables["page"]; p != float64(2) {
		t.Errorf("schedule page = %v, want 2", p)
	}
}

// A show still airing has no confirmed episode count. What has already aired is
// everything before the episode AniList is counting down to.
func TestGetSeriesAiring(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		opGetSeries: {okJSON(t, "media_airing.json")},
	})

	got, err := c.GetSeries(context.Background(), "116742")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	want := core.SeriesMeta{
		Provider:      ProviderID,
		ProviderRef:   "116742",
		Title:         "Tsuki ga Michibiku Isekai Douchuu",
		OriginalTitle: "月が導く異世界道中",
		Year:          2021,
		Overview:      "Makoto is summoned to another world — and promptly dumped in the wastelands.\n\n(Source: Crunchyroll)",
		VoteAverage:   7.5,
		VoteCount:     1000,
		Status:        "Continuing",
		FirstAirDate:  day(2021, 7, 7),
		PosterURL:     "https://s4.anilist.co/file/anilistcdn/media/anime/cover/large/bx116742-jTfNRt0nHOhu.jpg",
		Seasons: []core.SeasonMeta{{
			Number:  1,
			Title:   "Season 1",
			AirDate: day(2021, 7, 7),
			// Seven, not eight: episode 8 is scheduled, not aired, and an
			// episode that does not exist yet must not be advertised as missing.
			Episodes: []core.EpisodeMeta{
				ep(1, "The Goddess's Rejection", day(2021, 7, 7)),
				ep(2, "The Demon Lord's Daughter", day(2021, 7, 14)),
				ep(3, "", day(2021, 7, 21)),
				ep(4, "", day(2021, 7, 28)),
				ep(5, "", day(2021, 8, 4)),
				ep(6, "", day(2021, 8, 11)),
				ep(7, "", day(2021, 8, 18)),
			},
		}},
	}

	if !reflect.DeepEqual(*got, want) {
		t.Errorf("GetSeries:\n got %+v\nwant %+v", *got, want)
	}
	if n := len(s.seen()); n != 1 {
		t.Errorf("requests = %d, want 1: the schedule fits on one page", n)
	}
}

// A long-runner's schedule is thousands of nodes. The walk stops rather than
// spend a whole rate-limit window on one series; the episodes past the bound
// still exist, they just carry no air date.
func TestAiringScheduleWalkIsBounded(t *testing.T) {
	head := []byte(`{"data":{"Media":{
		"id": 21,
		"title": {"romaji":"One Piece","english":"One Piece","native":"ワンピース"},
		"startDate": {"year":1999,"month":10,"day":20},
		"description": "",
		"status": "RELEASING",
		"episodes": 220,
		"coverImage": {"large":"https://example.test/op.jpg"},
		"stats": {"scoreDistribution":[]},
		"airingSchedule": {"pageInfo":{"hasNextPage":true},"nodes":[{"episode":1,"airingAt":940377600}]}
	}}}`)
	// Every further page claims another after it, so only the bound stops this.
	more := []byte(`{"data":{"Media":{"airingSchedule":{"pageInfo":{"hasNextPage":true},"nodes":[]}}}}`)

	c, s := newStub(t, map[string][]response{
		opGetSeries:      {{status: http.StatusOK, body: head}},
		opSeriesSchedule: {{status: http.StatusOK, body: more}},
	})

	got, err := c.GetSeries(context.Background(), "21")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	if n := len(s.seen()); n != maxAiringPages {
		t.Errorf("requests = %d, want %d (the series plus %d further pages)", n, maxAiringPages, maxAiringPages-1)
	}
	if len(got.Seasons) != 1 {
		t.Fatalf("seasons = %d, want 1", len(got.Seasons))
	}
	eps := got.Seasons[0].Episodes
	if len(eps) != 220 {
		t.Fatalf("episodes = %d, want the confirmed count of 220", len(eps))
	}
	if !eps[0].AirDate.Equal(time.Unix(940377600, 0).UTC()) {
		t.Errorf("episode 1 AirDate = %v, want the scheduled one", eps[0].AirDate)
	}
	if !eps[219].AirDate.IsZero() {
		t.Errorf("episode 220 AirDate = %v, want zero: its schedule page was never fetched", eps[219].AirDate)
	}
}

// With no confirmed count, no countdown and no schedule, there is nothing to
// count. An invented episode list would have the organizer file real files
// against numbers that do not exist.
func TestSeriesWithNothingToCountHasNoEpisodes(t *testing.T) {
	body := []byte(`{"data":{"Media":{
		"id": 5,
		"title": {"romaji":"Announced Only","english":null,"native":"未定"},
		"startDate": {"year":null,"month":null,"day":null},
		"status": "NOT_YET_RELEASED",
		"episodes": null,
		"coverImage": {},
		"stats": {"scoreDistribution":[]}
	}}}`)

	c, _ := newStub(t, map[string][]response{
		opGetSeries: {{status: http.StatusOK, body: body}},
	})

	got, err := c.GetSeries(context.Background(), "5")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	want := core.SeriesMeta{
		Provider:      ProviderID,
		ProviderRef:   "5",
		Title:         "Announced Only",
		OriginalTitle: "未定",
		Status:        "Planned",
		Seasons:       []core.SeasonMeta{{Number: 1, Title: "Season 1"}},
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("GetSeries:\n got %+v\nwant %+v", *got, want)
	}
}

func TestStatusMapping(t *testing.T) {
	tests := []struct {
		anilist string
		want    string
	}{
		{anilist: "FINISHED", want: "Ended"},
		{anilist: "RELEASING", want: "Continuing"},
		{anilist: "NOT_YET_RELEASED", want: "Planned"},
		{anilist: "CANCELLED", want: "Canceled"},
		{anilist: "HIATUS", want: "Hiatus"},
		// An enum value AniList has not invented yet is better empty than
		// mistranslated: an unknown status must not read as "Ended".
		{anilist: "SOMETHING_NEW", want: ""},
		{anilist: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.anilist, func(t *testing.T) {
			m := mediaResult{Status: tt.anilist}
			if got := seriesMeta(m).Status; got != tt.want {
				t.Errorf("status %q mapped to %q, want %q", tt.anilist, got, tt.want)
			}
		})
	}
}

// The stripping rules themselves are internal/htmltext's, tested there. What
// this package still owes is the wiring: a recorded description carrying the
// markup AniList actually emits has to arrive on Overview as plain text, or
// every tvshow.nfo written from it carries tags.
func TestOverviewIsStripped(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		opGetSeries:      {okJSON(t, "media_finished.json")},
		opSeriesSchedule: {okJSON(t, "media_finished_schedule_2.json")},
	})

	got, err := c.GetSeries(context.Background(), "98202")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	// The fixture's description is "Rin camps alone at the foot of <i>Mount
	// Fuji</i>.<br><br>(Source: Crunchyroll)".
	want := "Rin camps alone at the foot of Mount Fuji.\n\n(Source: Crunchyroll)"
	if got.Overview != want {
		t.Errorf("Overview =\n %q\nwant %q", got.Overview, want)
	}
}

func TestParseStreamingTitle(t *testing.T) {
	tests := []struct {
		in        string
		wantNum   int
		wantTitle string
		wantOK    bool
	}{
		{in: "Episode 1 - To You, in 2000 Years", wantNum: 1, wantTitle: "To You, in 2000 Years", wantOK: true},
		{in: "Episode 12", wantNum: 12, wantTitle: "", wantOK: true},
		{in: "Episode 7 – Em dash separator", wantNum: 7, wantTitle: "Em dash separator", wantOK: true},
		// Nothing ties these to an episode number, so they are dropped rather
		// than guessed at — a special is not episode 1.
		{in: "Special - Room Camp", wantOK: false},
		{in: "OVA", wantOK: false},
		{in: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%q", tt.in), func(t *testing.T) {
			n, title, ok := parseStreamingTitle(tt.in)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if n != tt.wantNum || title != tt.wantTitle {
				t.Errorf("= (%d, %q), want (%d, %q)", n, title, tt.wantNum, tt.wantTitle)
			}
		})
	}
}
