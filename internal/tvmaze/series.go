package tvmaze

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/htmltext"
)

// dateLayout is the only date shape TVmaze serves: a plain calendar day, no
// zone. `airstamp` carries the zoned instant, but core.EpisodeMeta.AirDate is a
// date everywhere else in Caravan and re-deriving one from a timezone would put
// a different day on screen for the same episode depending on the network.
const dateLayout = "2006-01-02"

// showPath and episodesPath are TVmaze's two per-show documents. They are built
// here rather than inline so the strings a test stub routes on and the strings
// this client sends are the same expression.
func showPath(id int) string     { return "/shows/" + strconv.Itoa(id) }
func episodesPath(id int) string { return showPath(id) + "/episodes" }

// searchShowsPath is the free-text search endpoint.
const searchShowsPath = "/search/shows"

// showResult is TVmaze's Show shape. Every nullable scalar is decoded into a
// plain Go value: unmarshalling JSON null into one is a no-op, so "TVmaze does
// not know" and "the zero value" are the same thing here, which is what the
// mapping below wants.
type showResult struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
	// Status is TVmaze's own vocabulary; see statuses.
	Status string `json:"status"`
	// Premiered is the first air date as a plain day, empty for a show that has
	// not been scheduled yet.
	Premiered string `json:"premiered"`
	// Summary is genuine HTML, TVmaze documents it as such, so it goes through
	// htmltext.Strip before it reaches an NFO.
	Summary string `json:"summary"`
	Rating  struct {
		// Average is already a 0-10 mean, which is core's scale. It is null
		// until enough people have rated the show.
		Average float64 `json:"average"`
	} `json:"rating"`
	// Externals are the cross-provider ids TVmaze knows. They are the reason
	// this provider is worth chaining behind another one.
	Externals struct {
		TheTVDB int64  `json:"thetvdb"`
		IMDB    string `json:"imdb"`
	} `json:"externals"`
	Image struct {
		Medium   string `json:"medium"`
		Original string `json:"original"`
	} `json:"image"`
}

// searchResult is one entry of TVmaze's search reply: a relevance score and the
// whole show document. The score is not carried onto core.SeriesMeta, the order
// is the answer, and a second provider's score would not be comparable with it,
// but the field is decoded so the shape is stated rather than guessed.
type searchResult struct {
	Score float64    `json:"score"`
	Show  showResult `json:"show"`
}

// episodeResult is one entry of a show's episode list.
type episodeResult struct {
	Name   string `json:"name"`
	Season int    `json:"season"`
	// Number is null on a special. See seasons for why that means "dropped"
	// rather than "numbered zero".
	Number  int    `json:"number"`
	AirDate string `json:"airdate"`
	Summary string `json:"summary"`
}

// statuses maps TVmaze's show status onto the vocabulary the rest of Caravan
// uses: the same strings TMDB serves, because the library, the NFO writer and
// the UI all branch on those and a second spelling of "Ended" would have to be
// taught to every one of them.
//
// "To Be Determined" is a show between seasons that has not been cancelled, so
// it is Continuing: the alternative reads as Ended and would have the UI tell
// someone their show is over during a hiatus. An unknown status maps to nothing
// rather than to a guess, for the same reason.
var statuses = map[string]string{
	"Running":          "Continuing",
	"Ended":            "Ended",
	"To Be Determined": "Continuing",
	"In Development":   "Planned",
}

// SearchSeries returns series candidates for q, in TVmaze's own relevance
// order. Results carry no seasons; call GetSeries for those.
func (c *Client) SearchSeries(ctx context.Context, q string) ([]core.SeriesMeta, error) {
	var results []searchResult
	if err := c.get(ctx, searchShowsPath, url.Values{"q": {q}}, &results); err != nil {
		return nil, err
	}

	out := make([]core.SeriesMeta, 0, len(results))
	for _, r := range results {
		out = append(out, seriesMeta(r.Show))
	}
	return out, nil
}

// GetSeries returns full details for one show, including its real seasons and
// their episodes.
//
// It costs two requests: the show document and the episode list. They are two
// plain GETs rather than one `?embed=episodes` call because the embedded shape
// nests the list under a different key and takes its own parameters, so the
// saving would be one round trip in exchange for a second response shape to
// decode and stub.
//
// The full mapping onto core.SeriesMeta, which is the contract this function
// implements:
//
//	Provider          ProviderID ("tvmaze")
//	ProviderRef       show.id, decimal
//	TVDBID            externals.thetvdb
//	IMDBID            externals.imdb
//	TMDBID            0: TVmaze does not carry it
//	Title             name
//	OriginalTitle     "": TVmaze serves one name per show, and repeating it
//	                  into OriginalTitle would tell the library's matcher it
//	                  had two pieces of evidence when it has one
//	Year              the year of premiered
//	FirstAirDate      premiered
//	Overview          summary through htmltext.Strip: TVmaze's summary is real
//	                  HTML, and this string is written into tvshow.nfo as XML
//	Status            Running→Continuing, Ended→Ended,
//	                  "To Be Determined"→Continuing, "In Development"→Planned
//	PosterURL         image.original, else image.medium
//	VoteAverage       rating.average, already TVmaze's 0-10 scale
//	VoteCount         0. TVmaze publishes no vote total at all, and a number
//	                  invented from the rating would be a lie the UI renders as
//	                  a fact
//	Seasons           grouped from the episode list; see seasons
func (c *Client) GetSeries(ctx context.Context, ref string) (*core.SeriesMeta, error) {
	id, err := parseRef(ref)
	if err != nil {
		return nil, err
	}

	var show showResult
	if err := c.get(ctx, showPath(id), nil, &show); err != nil {
		return nil, err
	}

	var episodes []episodeResult
	if err := c.get(ctx, episodesPath(id), nil, &episodes); err != nil {
		return nil, err
	}

	s := seriesMeta(show)
	s.Seasons = seasons(episodes)
	return &s, nil
}

// seriesMeta converts a TVmaze show into the provider-side domain type, without
// its seasons: a search result has nothing to build them from.
func seriesMeta(show showResult) core.SeriesMeta {
	premiered := parseDate(show.Premiered)
	return core.SeriesMeta{
		Provider:     ProviderID,
		ProviderRef:  strconv.Itoa(show.ID),
		TVDBID:       show.Externals.TheTVDB,
		IMDBID:       show.Externals.IMDB,
		Title:        show.Name,
		Year:         yearOf(premiered),
		Overview:     htmltext.Strip(show.Summary),
		VoteAverage:  show.Rating.Average,
		Status:       statuses[show.Status],
		FirstAirDate: premiered,
		PosterURL:    firstNonEmpty(show.Image.Original, show.Image.Medium),
	}
}

// seasons groups an episode list into the real seasons TVmaze knows about,
// ascending, each with its episodes in order. This is TVmaze's reason to exist
// in a chain: AniList synthesizes a single season because it has no episode
// documents, and these are the genuine article.
//
// A season's AirDate is its earliest episode's, because TVmaze serves no season
// document of its own and the day the first episode aired is what a season air
// date means everywhere else in Caravan. An episode takes Season and Number
// from TVmaze's own pair, Title from name, Overview from summary through
// htmltext.Strip, and AirDate from airdate.
//
// Episodes TVmaze numbers as null, specials, are dropped. Numbering them
// ourselves would invent facts: the number chosen depends on how many specials
// have been catalogued so far, so an upstream edit renumbers the ones already
// filed and moves real files off the episodes they were matched to. This is the
// same refusal anilist.episodeCount makes about episode counts. A specials file
// therefore parks in the review queue, which is visible and correct, rather
// than landing under a number nobody assigned it.
func seasons(episodes []episodeResult) []core.SeasonMeta {
	byNumber := make(map[int][]core.EpisodeMeta)
	airDates := make(map[int]time.Time)

	for _, e := range episodes {
		if e.Number <= 0 {
			continue
		}
		air := parseDate(e.AirDate)
		byNumber[e.Season] = append(byNumber[e.Season], core.EpisodeMeta{
			Season:   e.Season,
			Number:   e.Number,
			Title:    e.Name,
			Overview: htmltext.Strip(e.Summary),
			AirDate:  air,
		})
		if !air.IsZero() {
			if first, ok := airDates[e.Season]; !ok || air.Before(first) {
				airDates[e.Season] = air
			}
		}
	}
	if len(byNumber) == 0 {
		return nil
	}

	numbers := make([]int, 0, len(byNumber))
	for n := range byNumber {
		numbers = append(numbers, n)
	}
	sort.Ints(numbers)

	out := make([]core.SeasonMeta, 0, len(numbers))
	for _, n := range numbers {
		eps := byNumber[n]
		sort.Slice(eps, func(i, j int) bool { return eps[i].Number < eps[j].Number })
		out = append(out, core.SeasonMeta{
			Number:   n,
			Title:    "Season " + strconv.Itoa(n),
			AirDate:  airDates[n],
			Episodes: eps,
		})
	}
	return out
}

// parseDate reads one of TVmaze's plain calendar days. An empty or malformed
// value is no date rather than an error: a show with no announced premiere and
// an episode with no announced air date are both ordinary, and the zero time is
// what the NFO writer and the calendar already handle.
func parseDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

// yearOf returns the year of t, or 0 when t is unset. time.Time's zero value
// reports year 1, which is not a year anybody meant.
func yearOf(t time.Time) int {
	if t.IsZero() {
		return 0
	}
	return t.Year()
}

// firstNonEmpty returns the first non-empty string, or "" when there is none.
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
