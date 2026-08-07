package anilist

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/htmltext"
)

// GraphQL operation names. They are the Operation on every APIError and the
// routing key a test stub matches on, so they are constants rather than literals
// repeated in two places.
const (
	opSearchSeries   = "SearchSeries"
	opGetSeries      = "GetSeries"
	opSeriesSchedule = "SeriesSchedule"
)

// maxAiringPages bounds the airing-schedule walk at four pages of 50 — 200
// episodes. Long-runners (One Piece, Detective Conan) have thousands of schedule
// nodes, and paging all of them would spend a whole rate-limit window on one
// series. Past the bound the episodes still exist — their numbers come from
// `episodes` — they simply keep a zero AirDate, which is the same thing an
// unaired episode carries and which the NFO writer and the calendar already
// handle.
//
// The 50 is written into the query documents below because a GraphQL document
// is a constant string and 50 is AniList's own maximum for that connection.
const maxAiringPages = 4

// mediaFields is the selection set shared by search and lookup, so the two can
// never drift into decoding different shapes into the same struct. The fields
// only a lookup can afford — synonyms, the schedule, the streaming list — are
// appended by the detail query rather than paid for on every keystroke.
const mediaFields = `
    id
    title { romaji english native }
    startDate { year month day }
    endDate { year month day }
    description(asHtml: false)
    averageScore
    status
    episodes
    coverImage { extraLarge large }
    stats { scoreDistribution { amount } }
`

const searchSeriesQuery = `query ` + opSearchSeries + `($q: String!, $perPage: Int!) {
  Page(page: 1, perPage: $perPage) {
    media(type: ANIME, search: $q, sort: [SEARCH_MATCH]) {` + mediaFields + `}
  }
}`

const getSeriesQuery = `query ` + opGetSeries + `($id: Int!, $page: Int!) {
  Media(id: $id, type: ANIME) {` + mediaFields + `
    synonyms
    format
    nextAiringEpisode { episode airingAt }
    streamingEpisodes { title url site }
    airingSchedule(page: $page, perPage: 50) {
      pageInfo { hasNextPage }
      nodes { episode airingAt }
    }
  }
}`

// seriesScheduleQuery fetches one further page of the airing schedule and
// nothing else. Re-asking for the whole Media document per page would re-send
// the description and the streaming list four times over for the one connection
// that actually pages.
const seriesScheduleQuery = `query ` + opSeriesSchedule + `($id: Int!, $page: Int!) {
  Media(id: $id, type: ANIME) {
    airingSchedule(page: $page, perPage: 50) {
      pageInfo { hasNextPage }
      nodes { episode airingAt }
    }
  }
}`

// fuzzyDateResult is AniList's FuzzyDate: any of the three parts may be null on
// a title whose exact premiere is not known yet.
type fuzzyDateResult struct {
	Year  int `json:"year"`
	Month int `json:"month"`
	Day   int `json:"day"`
}

// airingNode is one entry of the airing schedule: which episode, and when it
// aired or will air, as unix seconds.
type airingNode struct {
	Episode  int   `json:"episode"`
	AiringAt int64 `json:"airingAt"`
}

type airingScheduleResult struct {
	PageInfo struct {
		HasNextPage bool `json:"hasNextPage"`
	} `json:"pageInfo"`
	Nodes []airingNode `json:"nodes"`
}

// mediaResult is AniList's Media shape. Every nullable scalar is decoded into a
// plain Go value: unmarshalling JSON null into one is a no-op, so "AniList does
// not know" and "the zero value" are the same thing here, which is exactly what
// the mapping wants.
type mediaResult struct {
	ID    int `json:"id"`
	Title struct {
		Romaji  string `json:"romaji"`
		English string `json:"english"`
		Native  string `json:"native"`
	} `json:"title"`
	// Synonyms are the alternate titles releases are sometimes named after.
	// core.SeriesMeta has nowhere to put them yet, so nothing below reads this
	// field; it is selected because the detail document is the only place they
	// can be had, and a recorded fixture that already carries them is what the
	// library's alias matching will be built against.
	Synonyms    []string        `json:"synonyms"`
	StartDate   fuzzyDateResult `json:"startDate"`
	EndDate     fuzzyDateResult `json:"endDate"`
	Description string          `json:"description"`
	// AverageScore is AniList's 0-100 mean, 0 when nobody has rated the title.
	AverageScore int    `json:"averageScore"`
	Status       string `json:"status"`
	// Episodes is the confirmed episode count, null while a show is airing.
	Episodes   int    `json:"episodes"`
	Format     string `json:"format"`
	CoverImage struct {
		ExtraLarge string `json:"extraLarge"`
		Large      string `json:"large"`
	} `json:"coverImage"`
	Stats struct {
		ScoreDistribution []struct {
			Amount int `json:"amount"`
		} `json:"scoreDistribution"`
	} `json:"stats"`
	NextAiringEpisode struct {
		Episode  int   `json:"episode"`
		AiringAt int64 `json:"airingAt"`
	} `json:"nextAiringEpisode"`
	StreamingEpisodes []struct {
		Title string `json:"title"`
		URL   string `json:"url"`
		Site  string `json:"site"`
	} `json:"streamingEpisodes"`
	AiringSchedule airingScheduleResult `json:"airingSchedule"`
}

// statuses maps AniList's MediaStatus enum onto the vocabulary the rest of
// Caravan uses — the same strings TMDB serves, because the library, the NFO
// writer and the UI all branch on those and a second spelling of "Ended" would
// have to be taught to every one of them.
var statuses = map[string]string{
	"FINISHED":         "Ended",
	"RELEASING":        "Continuing",
	"NOT_YET_RELEASED": "Planned",
	"CANCELLED":        "Canceled",
	"HIATUS":           "Hiatus",
}

// SearchSeries returns anime candidates for q, in AniList's SEARCH_MATCH order.
// Results carry no seasons; call GetSeries for those.
func (c *Client) SearchSeries(ctx context.Context, q string) ([]core.SeriesMeta, error) {
	var resp struct {
		Page struct {
			Media []mediaResult `json:"media"`
		} `json:"Page"`
	}
	vars := map[string]any{"q": q, "perPage": defaultPerPage}
	if err := c.query(ctx, opSearchSeries, searchSeriesQuery, vars, &resp); err != nil {
		return nil, err
	}

	out := make([]core.SeriesMeta, 0, len(resp.Page.Media))
	for _, m := range resp.Page.Media {
		out = append(out, seriesMeta(m))
	}
	return out, nil
}

// GetSeries returns full details for one anime, including its one synthesized
// season and that season's episodes.
//
// AniList has no season/episode documents the way TMDB does: an anime's seasons
// are separate Media records ("Sequel" relations), and the only per-episode
// facts it serves are the airing schedule and the streaming-site episode list.
// The mapping below turns those into a single season; the two of them together
// are why one GetSeries can cost several requests, and why this client paces
// itself (see the package comment).
//
// The full mapping onto core.SeriesMeta, which is the contract this function
// implements:
//
//	Provider          ProviderID ("anilist")
//	ProviderRef       Media.id, decimal
//	TMDBID/TVDBID     0, IMDBID "" — AniList knows none of them
//	Title             title.english, else romaji, else native
//	OriginalTitle     romaji when it differs from Title, else native. Release
//	                  filenames carry romaji far more often than English, and the
//	                  library's bestMatch scores Title and OriginalTitle both, so
//	                  the romaji has to be in one of the two fields for a scanned
//	                  file to match at all.
//	Year              startDate.year
//	FirstAirDate      startDate, widened to the 1st of the period when the month
//	                  or day is unknown — Caravan's convention everywhere
//	Overview          description run through htmltext.Strip: AniList emits <br>
//	                  and <i>, and this string is written into tvshow.nfo as XML
//	Status            FINISHED→Ended, RELEASING→Continuing,
//	                  NOT_YET_RELEASED→Planned, CANCELLED→Canceled,
//	                  HIATUS→Hiatus
//	PosterURL         coverImage.extraLarge, else large
//	VoteAverage       averageScore/10 — AniList rates 0-100, core is 0-10
//	VoteCount         sum of stats.scoreDistribution[].amount. AniList serves no
//	                  vote total, and a 0 there renders as "Not yet rated" in the
//	                  UI for a title thousands of people have scored
//	Seasons           exactly one, Number 1, Title "Season 1", AirDate startDate,
//	                  populated by GetSeries only. See below for its episodes
//
// The season's episodes are numbers 1..N where N is `episodes` if AniList has
// confirmed one, else nextAiringEpisode.episode-1 (everything already aired),
// else the highest episode in the airing schedule, else none. Each episode takes
// its Title from the streaming-episode list and its AirDate from the schedule
// node with the same number; either may be absent.
func (c *Client) GetSeries(ctx context.Context, ref string) (*core.SeriesMeta, error) {
	id, err := parseRef(ref)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Media *mediaResult `json:"Media"`
	}
	vars := map[string]any{"id": id, "page": 1}
	if err := c.query(ctx, opGetSeries, getSeriesQuery, vars, &resp); err != nil {
		return nil, err
	}
	// AniList usually answers a missing id with an errors array, but a null
	// Media in a 200 says the same thing and must not decode into an empty
	// series that then overwrites a good library row.
	if resp.Media == nil {
		return nil, ErrNotFound
	}
	m := *resp.Media

	airDates := make(map[int]time.Time, len(m.AiringSchedule.Nodes))
	addAirDates(airDates, m.AiringSchedule.Nodes)
	hasNext := m.AiringSchedule.PageInfo.HasNextPage
	for page := 2; hasNext && page <= maxAiringPages; page++ {
		sched, err := c.schedulePage(ctx, id, page)
		if err != nil {
			return nil, err
		}
		addAirDates(airDates, sched.Nodes)
		hasNext = sched.PageInfo.HasNextPage
	}

	s := seriesMeta(m)
	s.Seasons = []core.SeasonMeta{season(m, airDates)}
	return &s, nil
}

// schedulePage fetches one further page of the airing schedule.
func (c *Client) schedulePage(ctx context.Context, id, page int) (airingScheduleResult, error) {
	var resp struct {
		Media *struct {
			AiringSchedule airingScheduleResult `json:"airingSchedule"`
		} `json:"Media"`
	}
	vars := map[string]any{"id": id, "page": page}
	if err := c.query(ctx, opSeriesSchedule, seriesScheduleQuery, vars, &resp); err != nil {
		return airingScheduleResult{}, err
	}
	if resp.Media == nil {
		return airingScheduleResult{}, ErrNotFound
	}
	return resp.Media.AiringSchedule, nil
}

// seriesMeta converts an AniList Media into the provider-side domain type,
// without its season: search results have nothing to build one from.
func seriesMeta(m mediaResult) core.SeriesMeta {
	start := fuzzyDate(m.StartDate)
	title := firstNonEmpty(m.Title.English, m.Title.Romaji, m.Title.Native)
	return core.SeriesMeta{
		Provider:      ProviderID,
		ProviderRef:   strconv.Itoa(m.ID),
		Title:         title,
		OriginalTitle: originalTitle(m, title),
		Year:          yearOf(start),
		Overview:      htmltext.Strip(m.Description),
		VoteAverage:   float64(m.AverageScore) / 10,
		VoteCount:     voteCount(m),
		Status:        statuses[m.Status],
		FirstAirDate:  start,
		PosterURL:     firstNonEmpty(m.CoverImage.ExtraLarge, m.CoverImage.Large),
	}
}

// season builds the one season every anime gets here, numbered 1 because that is
// what a scanned "S01E05" says and because AniList has no specials season to
// reserve 0 for.
func season(m mediaResult, airDates map[int]time.Time) core.SeasonMeta {
	sm := core.SeasonMeta{
		Number:  1,
		Title:   "Season 1",
		AirDate: fuzzyDate(m.StartDate),
	}
	titles := episodeTitles(m)
	for n := 1; n <= episodeCount(m, airDates); n++ {
		sm.Episodes = append(sm.Episodes, core.EpisodeMeta{
			Season: 1,
			Number: n,
			Title:  titles[n],
			// Zero for an episode past maxAiringPages or one AniList has no
			// schedule node for, which is what an unaired episode carries too.
			AirDate: airDates[n],
		})
	}
	return sm
}

// episodeCount decides how many episodes the season has, best evidence first: a
// confirmed count, then "everything before the next one to air", then the
// schedule itself. A show that answers none of the three gets no episodes rather
// than a guess — an invented episode list would have the organizer file real
// files against numbers that do not exist.
func episodeCount(m mediaResult, airDates map[int]time.Time) int {
	if m.Episodes > 0 {
		return m.Episodes
	}
	if aired := m.NextAiringEpisode.Episode - 1; aired > 0 {
		return aired
	}
	highest := 0
	for n := range airDates {
		if n > highest {
			highest = n
		}
	}
	return highest
}

// episodeTitles reads the streaming-episode list into a number→title map.
//
// It is the only per-episode title AniList serves, and it is a list of links
// rather than an episode index: the same episode appears once per streaming
// site, and a bonus entry ("Special - …") may not be an episode at all. First
// entry wins so the result is stable across refreshes, and an entry whose
// number cannot be read is dropped rather than guessed at.
func episodeTitles(m mediaResult) map[int]string {
	out := make(map[int]string, len(m.StreamingEpisodes))
	for _, e := range m.StreamingEpisodes {
		n, title, ok := parseStreamingTitle(e.Title)
		if !ok || title == "" {
			continue
		}
		if _, seen := out[n]; !seen {
			out[n] = title
		}
	}
	return out
}

// episodePrefix matches the "Episode 7" / "Episode 7 - " prefix AniList's
// streaming titles carry, which is the only thing that ties one to an episode
// number.
var episodePrefix = regexp.MustCompile(`^\s*Episode\s+(\d+)\s*(?:[-–—:]\s*)?`)

// parseStreamingTitle splits a streaming-episode title into its episode number
// and the episode's actual name. ok is false when the entry names no episode.
func parseStreamingTitle(s string) (int, string, bool) {
	m := episodePrefix.FindStringSubmatch(s)
	if m == nil {
		return 0, "", false
	}
	n, err := strconv.Atoi(m[1])
	if err != nil || n <= 0 {
		return 0, "", false
	}
	return n, strings.TrimSpace(s[len(m[0]):]), true
}

// addAirDates folds a page of schedule nodes into the number→date map. The first
// node for an episode wins: a rebroadcast is scheduled again under the same
// number, and the premiere is the air date everything downstream means.
func addAirDates(dst map[int]time.Time, nodes []airingNode) {
	for _, n := range nodes {
		if n.Episode <= 0 || n.AiringAt <= 0 {
			continue
		}
		if _, seen := dst[n.Episode]; !seen {
			dst[n.Episode] = time.Unix(n.AiringAt, 0).UTC()
		}
	}
}

// voteCount sums the score histogram, which is the closest thing AniList serves
// to a vote total; see the mapping table on GetSeries.
func voteCount(m mediaResult) int {
	total := 0
	for _, b := range m.Stats.ScoreDistribution {
		total += b.Amount
	}
	return total
}

// originalTitle picks the title a release is most likely to be named after that
// is not already in Title; see the mapping table on GetSeries.
func originalTitle(m mediaResult, title string) string {
	if m.Title.Romaji != "" && m.Title.Romaji != title {
		return m.Title.Romaji
	}
	return m.Title.Native
}

// fuzzyDate widens AniList's FuzzyDate into a time.Time, filling an unknown
// month or day with the 1st. A year alone is a real answer ("premiered in
// 2016"), and the rest of Caravan already reads a widened partial date that way.
// No year at all is no date.
func fuzzyDate(d fuzzyDateResult) time.Time {
	if d.Year <= 0 {
		return time.Time{}
	}
	month, day := d.Month, d.Day
	if month <= 0 {
		month = 1
	}
	if day <= 0 {
		day = 1
	}
	return time.Date(d.Year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
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

