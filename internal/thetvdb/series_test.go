package thetvdb

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// day is a UTC date, the only kind TheTVDB's plain calendar days resolve to.
func day(y, m, d int) time.Time {
	return time.Date(y, time.Month(m), d, 0, 0, 0, 0, time.UTC)
}

// The ref a search result yields is `tvdb_id`, NOT `id`. TheTVDB's search
// endpoint prefixes `id` ("series-81189") and every lookup endpoint takes the
// bare number, so a ref built from `id` looks correct in a search response and
// then 404s on every add, refresh and rescan afterwards.
func TestSearchSeriesUsesTheTVDBIDNotThePrefixedID(t *testing.T) {
	routes := loginRoutes(t)
	routes[searchPath] = []response{okJSON(t, "search_series.json")}
	c, s := newStub(t, "", routes)

	got, err := c.SearchSeries(context.Background(), "breaking bad")
	if err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}

	want := []core.SeriesMeta{
		{
			Provider:    ProviderID,
			ProviderRef: "81189",
			TVDBID:      81189,
			// TMDBID stays 0 even though the record carries a TheMovieDB remote
			// id: a TMDB id Caravan did not get from TMDB would let the
			// library's TMDB rung fold this row onto a TMDB one.
			IMDBID:       "tt0903747",
			Title:        "Breaking Bad",
			Year:         2008,
			Overview:     "A high school chemistry teacher turns to cooking after a terminal diagnosis.",
			FirstAirDate: day(2008, 1, 20),
			PosterURL:    "https://artworks.thetvdb.com/banners/posters/81189-1.jpg",
			// Status is absent from a search hit, and `score` is a popularity
			// integer rather than a rating, so VoteAverage and VoteCount stay 0.
		},
		{
			Provider:    ProviderID,
			ProviderRef: "273181",
			TVDBID:      273181,
			Title:       "Better Call Saul",
			// A record with no year, no artwork, no premiere and no external
			// ids: every one of those is empty in the reply and none of them may
			// become a fabricated zero on screen.
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

	// The catalogue holds movies, people and companies too, so the search is
	// scoped rather than filtered afterwards.
	var query url.Values
	for _, req := range s.seen() {
		if req.path == searchPath {
			query, _ = url.ParseQuery(req.query)
		}
	}
	if query.Get("query") != "breaking bad" || query.Get("type") != "series" {
		t.Errorf("search query = %v, want the typed text scoped to series", query)
	}
}

func TestGetSeriesMapsTheExtendedRecord(t *testing.T) {
	c, _ := newStub(t, "", seriesRoutes(t, 81189))

	got, err := c.GetSeries(context.Background(), "81189")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	want := core.SeriesMeta{
		Provider:     ProviderID,
		ProviderRef:  "81189",
		TVDBID:       81189,
		IMDBID:       "tt0903747",
		Title:        "Breaking Bad",
		Year:         2008,
		Overview:     "Walter White, a New Mexico chemistry teacher, is diagnosed with terminal lung cancer.",
		Status:       "Ended",
		FirstAirDate: day(2008, 1, 20),
		PosterURL:    "https://artworks.thetvdb.com/banners/posters/81189-1.jpg",
		Seasons:      got.Seasons,
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("series:\n got %+v\nwant %+v", *got, want)
	}
	// The fixture carries score: 328476. It is a popularity count, not a rating,
	// and rescaling it would put a made-up number on the detail page.
	if got.VoteAverage != 0 || got.VoteCount != 0 {
		t.Errorf("votes = %v/%d, want both zero — TheTVDB publishes no rating",
			got.VoteAverage, got.VoteCount)
	}
}

// Season 0 is kept, unlike internal/tvmaze's. TheTVDB numbers its specials
// properly — a special carries seasonNumber 0 and a real number inside it — so
// there is nothing to invent and nothing to drop.
func TestGetSeriesBuildsSeasonsIncludingSpecials(t *testing.T) {
	c, _ := newStub(t, "", seriesRoutes(t, 81189))

	got, err := c.GetSeries(context.Background(), "81189")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	want := []core.SeasonMeta{
		{
			Number: 0,
			Title:  "Specials",
			// The earliest episode's date; TheTVDB serves no season document
			// worth reading.
			AirDate: day(2009, 2, 17),
			Episodes: []core.EpisodeMeta{
				// A special TheTVDB keeps out of the running order: absolute 0,
				// which is what "no absolute number" is spelled as.
				{Season: 0, Number: 1, Title: "Good Cop / Bad Cop",
					Overview: "A promotional short. TheTVDB numbers it inside the specials season.",
					AirDate:  day(2009, 2, 17)},
			},
		},
		{
			Number:  1,
			Title:   "Season 1",
			AirDate: day(2008, 1, 20),
			Episodes: []core.EpisodeMeta{
				{Season: 1, Number: 1, Absolute: 1, Title: "Pilot",
					Overview: "Diagnosed with terminal lung cancer, Walter White turns to cooking.",
					AirDate:  day(2008, 1, 20)},
				{Season: 1, Number: 2, Absolute: 2, Title: "Cat's in the Bag...", AirDate: day(2008, 1, 27)},
			},
		},
		{
			Number:  2,
			Title:   "Season 2",
			AirDate: day(2009, 3, 8),
			Episodes: []core.EpisodeMeta{
				// Page 1 serves these out of order, and the overview is real
				// markup: both are normalised here rather than downstream. The
				// absolute numbers ride along per episode — 9 and 10 across a
				// season boundary, which is the whole point of the order and the
				// one thing counting seasons here could never reproduce.
				{Season: 2, Number: 1, Absolute: 9, Title: "Seven Thirty-Seven",
					Overview: "Walt and Jesse deal with the aftermath.",
					AirDate:  day(2009, 3, 8)},
				{Season: 2, Number: 2, Absolute: 10, Title: "Grilled", AirDate: day(2009, 3, 15)},
			},
		},
	}

	if !reflect.DeepEqual(got.Seasons, want) {
		t.Errorf("seasons:\n got %+v\nwant %+v", got.Seasons, want)
	}
}

// The episode list is paged, and a series whose episodes stop at the first page
// boundary is the most ordinary shape there is — a walk that read one page
// would silently lose every later season.
func TestGetSeriesWalksEveryEpisodePage(t *testing.T) {
	c, s := newStub(t, "", seriesRoutes(t, 81189))

	if _, err := c.GetSeries(context.Background(), "81189"); err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	var pages []string
	for _, req := range s.seen() {
		if req.path != seriesEpisodesPath(81189) {
			continue
		}
		q, _ := url.ParseQuery(req.query)
		pages = append(pages, q.Get("page"))
	}
	// Page numbers are this client's own count rather than the cursor
	// `links.next` carries; see episodes.
	if !reflect.DeepEqual(pages, []string{"0", "1"}) {
		t.Errorf("episode pages = %v, want both pages walked and then a stop", pages)
	}
}

// A server that always says there is another page must not be able to walk this
// client forever.
func TestEpisodeWalkStopsAtThePageCap(t *testing.T) {
	routes := seriesRoutes(t, 81189)
	// page0's links.next is never null, and a single-element queue repeats.
	routes[seriesEpisodesPath(81189)] = []response{okJSON(t, "series_episodes_page0.json")}
	c, s := newStub(t, "", routes)

	if _, err := c.GetSeries(context.Background(), "81189"); err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if n := s.count(seriesEpisodesPath(81189)); n != maxEpisodePages {
		t.Errorf("episode requests = %d, want the cap of %d", n, maxEpisodePages)
	}
}

// An episode TheTVDB has not numbered cannot be filed against anything, so it is
// dropped rather than given a number Caravan invented.
func TestUnnumberedEpisodesAreDropped(t *testing.T) {
	routes := seriesRoutes(t, 81189)
	routes[seriesEpisodesPath(81189)] = []response{{
		status: http.StatusOK,
		body: []byte(`{"data":{"episodes":[
			{"seasonNumber":1,"number":0,"name":"Unnumbered","aired":"2008-01-20"},
			{"seasonNumber":1,"number":1,"name":"Pilot","aired":"2008-01-20"}
		]},"links":{"next":null}}`),
	}}
	c, _ := newStub(t, "", routes)

	got, err := c.GetSeries(context.Background(), "81189")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if len(got.Seasons) != 1 || len(got.Seasons[0].Episodes) != 1 {
		t.Fatalf("seasons = %+v, want the numbered episode alone", got.Seasons)
	}
	if got.Seasons[0].Episodes[0].Title != "Pilot" {
		t.Errorf("episode = %+v, want the numbered one", got.Seasons[0].Episodes[0])
	}
}

// A series with no episodes at all carries no seasons rather than an empty one:
// nil is how every other provider says "nothing here" and the NFO writer and the
// UI already handle it.
func TestGetSeriesWithNoEpisodesCarriesNoSeasons(t *testing.T) {
	routes := seriesRoutes(t, 81189)
	routes[seriesEpisodesPath(81189)] = []response{{
		status: http.StatusOK,
		body:   []byte(`{"data":{"episodes":[]},"links":{"next":null}}`),
	}}
	c, _ := newStub(t, "", routes)

	got, err := c.GetSeries(context.Background(), "81189")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.Seasons != nil {
		t.Errorf("seasons = %+v, want none", got.Seasons)
	}
}

// The status vocabulary is TMDB's, because the library, the NFO writer and the
// UI all branch on those strings. An unknown status maps to nothing rather than
// to a guess.
func TestStatusMapping(t *testing.T) {
	cases := map[string]string{
		"Continuing": "Continuing",
		"Ended":      "Ended",
		"Upcoming":   "Planned",
		"Whatever":   "",
		"":           "",
	}
	for in, want := range cases {
		if got := statuses[in]; got != want {
			t.Errorf("statuses[%q] = %q, want %q", in, got, want)
		}
	}
}
