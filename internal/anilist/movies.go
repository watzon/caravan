package anilist

import (
	"context"
	"strconv"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/htmltext"
)

// The film half of the catalogue.
//
// AniList has no separate film document: a film is a MEDIA record of format
// MOVIE sitting beside the series records, described by the same fields. So
// this file is a second READING of one document rather than a second client —
// the selection set is mediaFields, the mapping reuses fuzzyDate, voteCount and
// originalTitle, and only the format filter and the target type differ.
//
// The format filter is the whole safety argument. A ref is just a number, and
// asking GetMovie for a series id must not answer with a series wearing a
// film's shape: a row pinned that way would be refreshed forever against a
// record whose episodes nothing reads. Search filters server-side (AniList
// takes `format` on the media connection); the lookup filters here, on the
// format the document reports, because that is the reading that also refuses a
// TV id somebody pasted by hand.

const (
	opSearchMovies = "SearchMovies"
	opGetMovie     = "GetMovie"

	// formatMovie is AniList's MediaFormat value for a film. Every other value
	// of that enum — TV, TV_SHORT, OVA, ONA, SPECIAL, MUSIC — is a series in
	// Caravan's vocabulary and belongs to GetSeries.
	formatMovie = "MOVIE"
)

const searchMoviesQuery = `query ` + opSearchMovies + `($q: String!, $perPage: Int!) {
  Page(page: 1, perPage: $perPage) {
    media(type: ANIME, format: MOVIE, search: $q, sort: [SEARCH_MATCH]) {` + mediaFields + `
      format
    }
  }
}`

const getMovieQuery = `query ` + opGetMovie + `($id: Int!) {
  Media(id: $id, type: ANIME) {` + mediaFields + `
    format
  }
}`

// SearchMovies returns anime film candidates for q, in AniList's SEARCH_MATCH
// order. AniList applies the format filter, so nothing television-shaped
// reaches the picker.
func (c *Client) SearchMovies(ctx context.Context, q string) ([]core.MovieMeta, error) {
	var resp struct {
		Page struct {
			Media []mediaResult `json:"media"`
		} `json:"Page"`
	}
	vars := map[string]any{"q": q, "perPage": defaultPerPage}
	if err := c.query(ctx, opSearchMovies, searchMoviesQuery, vars, &resp); err != nil {
		return nil, err
	}

	out := make([]core.MovieMeta, 0, len(resp.Page.Media))
	for _, m := range resp.Page.Media {
		out = append(out, movieMeta(m))
	}
	return out, nil
}

// GetMovie returns full details for one anime film.
//
// The mapping onto core.MovieMeta, which is the contract this implements:
//
//	Provider          ProviderID ("anilist")
//	ProviderRef       Media.id, decimal
//	TMDBID/IMDBID     0 and "" — AniList knows neither
//	Title             title.english, else romaji, else native
//	OriginalTitle     romaji when it differs from Title, else native — the same
//	                  rule GetSeries uses, and for the same reason: a release
//	                  filename carries romaji far more often than English
//	Year              startDate.year
//	ReleaseDate       startDate, widened to the 1st of the period when the month
//	                  or day is unknown. It is the PREMIERE, which for a film is
//	                  the theatrical release
//	DigitalRelease/PhysicalRelease
//	                  zero — AniList catalogues no home release. That is not the
//	                  gap it would be for a provider with no premiere either:
//	                  wanted.homeRelease falls back to ReleaseDate plus the
//	                  cinema window, so minimum availability still waits for a
//	                  film that is currently in cinemas
//	Overview          description run through htmltext.Strip, as GetSeries does
//	VoteAverage       averageScore/10 — AniList rates 0-100, core is 0-10
//	VoteCount         sum of stats.scoreDistribution[].amount
//	PosterURL         coverImage.extraLarge, else large
//
// A ref that names a record of any other format is ErrNotFound: it exists on
// AniList, but not as a film, and answering with it would pin a movie row to a
// television record.
func (c *Client) GetMovie(ctx context.Context, ref string) (*core.MovieMeta, error) {
	id, err := parseRef(ref)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Media *mediaResult `json:"Media"`
	}
	vars := map[string]any{"id": id}
	if err := c.query(ctx, opGetMovie, getMovieQuery, vars, &resp); err != nil {
		return nil, err
	}
	// See GetSeries: a null Media in a 200 says "no such id" and must not decode
	// into an empty movie that then overwrites a good library row.
	if resp.Media == nil || resp.Media.Format != formatMovie {
		return nil, ErrNotFound
	}

	m := movieMeta(*resp.Media)
	return &m, nil
}

// movieMeta converts an AniList Media into the provider-side movie type; see
// the mapping table on GetMovie.
func movieMeta(m mediaResult) core.MovieMeta {
	start := fuzzyDate(m.StartDate)
	title := firstNonEmpty(m.Title.English, m.Title.Romaji, m.Title.Native)
	return core.MovieMeta{
		Provider:      ProviderID,
		ProviderRef:   strconv.Itoa(m.ID),
		Title:         title,
		OriginalTitle: originalTitle(m, title),
		Year:          yearOf(start),
		Overview:      htmltext.Strip(m.Description),
		VoteAverage:   float64(m.AverageScore) / 10,
		VoteCount:     voteCount(m),
		ReleaseDate:   start,
		PosterURL:     firstNonEmpty(m.CoverImage.ExtraLarge, m.CoverImage.Large),
	}
}
