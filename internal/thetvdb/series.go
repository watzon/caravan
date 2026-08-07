package thetvdb

import (
	"context"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/htmltext"
)

// dateLayout is the shape TheTVDB serves every date in: a plain calendar day,
// no zone. It is what `firstAired`, `aired` and `first_air_time` all carry.
const dateLayout = "2006-01-02"

// searchPath is the free-text search endpoint. seriesExtendedPath and
// seriesEpisodesPath are the two documents one GetSeries reads. They are built
// here rather than inline so the strings a test stub routes on and the strings
// this client sends are the same expression.
const searchPath = "/search"

func seriesExtendedPath(id int64) string {
	return "/series/" + strconv.FormatInt(id, 10) + "/extended"
}

func seriesEpisodesPath(id int64) string {
	return "/series/" + strconv.FormatInt(id, 10) + "/episodes/default"
}

// searchLimit bounds a search reply. TheTVDB's default page is larger than any
// picker shows, and the rows past the first screen are never the answer to a
// title someone typed.
const searchLimit = 25

// maxEpisodePages bounds the episode walk. TheTVDB pages this endpoint, and a
// long-runner (One Piece, Detective Conan) has thousands of episodes; paging all
// of them would spend a whole lookup on one series. Twenty pages is more
// episodes than any catalogued series has, so the bound is a runaway guard
// rather than a truncation anyone will meet — the same role anilist.maxAiringPages
// plays for the airing schedule.
const maxEpisodePages = 20

// searchRecord is one entry of TheTVDB's search reply.
//
// The identity trap is `id`: on this endpoint it is a PREFIXED string
// ("series-71663"), not the numeric id every other endpoint takes, and
// `tvdb_id` is the plain one. A ref built from `id` looks right in a search
// response and 404s on every lookup afterwards.
type searchRecord struct {
	TVDBID string `json:"tvdb_id"`
	Name   string `json:"name"`
	// Year is a string here, unlike the extended record's integer.
	Year         string     `json:"year"`
	Overview     string     `json:"overview"`
	ImageURL     string     `json:"image_url"`
	FirstAirTime string     `json:"first_air_time"`
	RemoteIDs    []remoteID `json:"remote_ids"`
}

// remoteID is one cross-provider id. TheTVDB carries several kinds — IMDB,
// TMDB, EIDR, official sites — distinguished only by sourceName, so the list is
// searched rather than indexed.
type remoteID struct {
	ID         string `json:"id"`
	SourceName string `json:"sourceName"`
}

// imdbSourceName is remoteID.SourceName for an IMDB title id.
const imdbSourceName = "IMDB"

// seriesExtended is /series/{id}/extended.
type seriesExtended struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Overview   string `json:"overview"`
	Image      string `json:"image"`
	FirstAired string `json:"firstAired"`
	Year       string `json:"year"`
	Status     struct {
		Name string `json:"name"`
	} `json:"status"`
	RemoteIDs []remoteID `json:"remoteIds"`
}

// episodeRecord is one entry of an episodes page.
type episodeRecord struct {
	SeasonNumber int    `json:"seasonNumber"`
	Number       int    `json:"number"`
	Name         string `json:"name"`
	Overview     string `json:"overview"`
	Aired        string `json:"aired"`
	// AbsoluteNumber is TheTVDB's anime-style running count across seasons. It
	// is decoded but not mapped: core.EpisodeMeta has no field for it until the
	// absolute-numbering phase, which is what consumes it.
	AbsoluteNumber int `json:"absoluteNumber"`
}

// episodesPage is one page of /series/{id}/episodes/default. `links.next` is
// TheTVDB's cursor; see episodes for why it is read as a flag rather than
// followed.
type episodesPage struct {
	Data struct {
		Episodes []episodeRecord `json:"episodes"`
	} `json:"data"`
	Links struct {
		Next string `json:"next"`
	} `json:"links"`
}

// statuses maps TheTVDB's series status onto the vocabulary the rest of Caravan
// uses — the same strings TMDB serves, because the library, the NFO writer and
// the UI all branch on those and a second spelling of "Ended" would have to be
// taught to every one of them.
//
// An unknown status maps to nothing rather than to a guess: "" is how every
// other provider says it does not know, and the UI already renders it.
var statuses = map[string]string{
	"Continuing": "Continuing",
	"Ended":      "Ended",
	"Upcoming":   "Planned",
}

// SearchSeries returns series candidates for q, in TheTVDB's own relevance
// order. Results carry no seasons; call GetSeries for those.
func (c *Client) SearchSeries(ctx context.Context, q string) ([]core.SeriesMeta, error) {
	var resp struct {
		Data []searchRecord `json:"data"`
	}
	query := url.Values{
		"query": {q},
		// The catalogue holds movies, people and companies too, and a picker
		// offering those as series would file a title against a record that has
		// no episodes.
		"type":  {"series"},
		"limit": {strconv.Itoa(searchLimit)},
	}
	if err := c.get(ctx, searchPath, query, &resp); err != nil {
		return nil, err
	}

	out := make([]core.SeriesMeta, 0, len(resp.Data))
	for _, r := range resp.Data {
		out = append(out, searchMeta(r))
	}
	return out, nil
}

// GetSeries returns full details for one series, including its seasons and
// their episodes.
//
// It costs at least two requests: the extended series document, then the
// episode pages. The episodes are a separate walk because TheTVDB serves no
// season documents worth having — the season records carry an id and a number
// and nothing an NFO wants — so the seasons below are grouped from the episodes
// themselves, exactly as internal/tvmaze does it.
//
// The full mapping onto core.SeriesMeta, which is the contract this function
// implements:
//
//	Provider          ProviderID ("thetvdb")
//	ProviderRef       the TheTVDB series id, decimal
//	TVDBID            the same id as an int64
//	IMDBID            remoteIds, the entry whose sourceName is "IMDB"
//	TMDBID            0 — TheTVDB serves a TMDB remote id for some records, but
//	                  a TMDB id Caravan did not get from TMDB would let the
//	                  library's TMDB rung collapse a TheTVDB row onto a TMDB one
//	Title             name
//	OriginalTitle     "" — TheTVDB's alternate names are a translation list, not
//	                  an original title, and picking one would tell the matcher
//	                  it had two pieces of evidence when it has one
//	Year              year
//	FirstAirDate      firstAired
//	Overview          overview through htmltext.Strip. TheTVDB's overviews are
//	                  usually plain text, but they are user-edited and this
//	                  string is written into tvshow.nfo as XML, so it is stripped
//	                  defensively rather than trusted
//	Status            Continuing→Continuing, Ended→Ended, Upcoming→Planned
//	PosterURL         image
//	VoteAverage       0
//	VoteCount         0. TheTVDB publishes `score`, which is a POPULARITY integer
//	                  in the thousands, not a 0-10 rating. Rescaling it onto
//	                  core's scale would put a number the detail page renders as
//	                  a rating next to a title nobody rated
//	Seasons           grouped from the episode walk; see seasons
func (c *Client) GetSeries(ctx context.Context, ref string) (*core.SeriesMeta, error) {
	id, err := parseRef(ref)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Data seriesExtended `json:"data"`
	}
	if err := c.get(ctx, seriesExtendedPath(id), nil, &resp); err != nil {
		return nil, err
	}

	records, err := c.episodes(ctx, id)
	if err != nil {
		return nil, err
	}

	s := seriesMeta(resp.Data)
	s.Seasons = seasons(records)
	return &s, nil
}

// episodes walks the paged default-order episode list.
//
// `links.next` is read as "there is more" rather than followed as a URL. The
// page number is this client's own, so a cursor pointing at another host — or
// at a query this client never composed — cannot redirect the walk, and the
// page cap stays a cap rather than a suggestion the server can extend.
func (c *Client) episodes(ctx context.Context, id int64) ([]episodeRecord, error) {
	var out []episodeRecord
	path := seriesEpisodesPath(id)

	for page := 0; page < maxEpisodePages; page++ {
		var resp episodesPage
		if err := c.get(ctx, path, url.Values{"page": {strconv.Itoa(page)}}, &resp); err != nil {
			return nil, err
		}
		out = append(out, resp.Data.Episodes...)
		if resp.Links.Next == "" {
			break
		}
	}
	return out, nil
}

// searchMeta converts a search hit into the provider-side domain type. Search
// results carry no status and no seasons: nothing on that page can build them.
func searchMeta(r searchRecord) core.SeriesMeta {
	year, _ := strconv.Atoi(r.Year)
	return core.SeriesMeta{
		Provider: ProviderID,
		// tvdb_id, never id: see searchRecord.
		ProviderRef:  r.TVDBID,
		TVDBID:       refID(r.TVDBID),
		IMDBID:       remoteIDOf(r.RemoteIDs, imdbSourceName),
		Title:        r.Name,
		Year:         year,
		Overview:     htmltext.Strip(r.Overview),
		FirstAirDate: parseDate(r.FirstAirTime),
		PosterURL:    r.ImageURL,
	}
}

// seriesMeta converts an extended series document into the provider-side domain
// type, without its seasons; see GetSeries for the whole mapping.
func seriesMeta(d seriesExtended) core.SeriesMeta {
	year, _ := strconv.Atoi(d.Year)
	return core.SeriesMeta{
		Provider:     ProviderID,
		ProviderRef:  strconv.FormatInt(d.ID, 10),
		TVDBID:       d.ID,
		IMDBID:       remoteIDOf(d.RemoteIDs, imdbSourceName),
		Title:        d.Name,
		Year:         year,
		Overview:     htmltext.Strip(d.Overview),
		Status:       statuses[d.Status.Name],
		FirstAirDate: parseDate(d.FirstAired),
		PosterURL:    d.Image,
	}
}

// seasons groups an episode list into the seasons TheTVDB numbers, ascending,
// each with its episodes in order.
//
// Season 0 is kept. TheTVDB numbers its specials properly — a special carries
// seasonNumber 0 and a real episode number within it — so a specials file lands
// on the episode it belongs to. That is the difference from internal/tvmaze,
// which drops its specials because TVmaze leaves them unnumbered and any number
// Caravan chose would be renumbered by the next upstream edit. There is nothing
// to invent here, so nothing is dropped.
//
// A season's AirDate is its earliest episode's, because the episode walk is all
// this provider gives and the day the first episode aired is what a season air
// date means everywhere else in Caravan.
func seasons(records []episodeRecord) []core.SeasonMeta {
	byNumber := make(map[int][]core.EpisodeMeta)
	airDates := make(map[int]time.Time)

	for _, e := range records {
		if e.Number <= 0 {
			continue
		}
		air := parseDate(e.Aired)
		byNumber[e.SeasonNumber] = append(byNumber[e.SeasonNumber], core.EpisodeMeta{
			Season:   e.SeasonNumber,
			Number:   e.Number,
			Title:    e.Name,
			Overview: htmltext.Strip(e.Overview),
			AirDate:  air,
			// TheTVDB's own running count, passed through as it arrives: 0 for
			// an episode it keeps no absolute order for, which is what "not
			// known" is spelled as everywhere downstream.
			Absolute: e.AbsoluteNumber,
		})
		if !air.IsZero() {
			if first, ok := airDates[e.SeasonNumber]; !ok || air.Before(first) {
				airDates[e.SeasonNumber] = air
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
			Title:    seasonTitle(n),
			AirDate:  airDates[n],
			Episodes: eps,
		})
	}
	return out
}

// seasonTitle names a season TheTVDB serves no name for. "Specials" is what
// TMDB calls season 0 and what the media servers expect on the folder, so the
// two providers do not put different labels on the same shelf.
func seasonTitle(number int) string {
	if number == 0 {
		return "Specials"
	}
	return "Season " + strconv.Itoa(number)
}

// remoteIDOf returns the id whose sourceName is source, or "" when the record
// carries none of that kind.
func remoteIDOf(ids []remoteID, source string) string {
	for _, id := range ids {
		if id.SourceName == source {
			return id.ID
		}
	}
	return ""
}

// refID reads a decimal ref as an int64, or 0 when it is not one. It is used
// for the TVDBID field beside the ref, where "not a number" is a record worth
// showing without a cross-provider id rather than a lookup to fail.
func refID(ref string) int64 {
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// parseDate reads one of TheTVDB's plain calendar days. An empty or malformed
// value is no date rather than an error: a series with no announced premiere
// and an episode with no announced air date are both ordinary, and the zero
// time is what the NFO writer and the calendar already handle.
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
