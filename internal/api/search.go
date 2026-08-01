package api

import (
	"net/http"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// Values accepted by GET /search?type=. The default is TypeAll, which queries
// both media types in one round trip — the UI's add-to-library picker lets the
// user flip between them without re-typing.
const (
	TypeAll = "all"
)

// movieMetaJSON and seriesMetaJSON are provider search hits: not library items
// yet, so they carry a TMDB id and no library id. PosterURL is an absolute
// provider URL rather than a storage-root-relative path, because nothing has
// been downloaded at this point.
type movieMetaJSON struct {
	TMDBID        int64  `json:"tmdb_id"`
	IMDBID        string `json:"imdb_id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	Year          int    `json:"year"`
	Overview      string `json:"overview"`
	ReleaseDate   string `json:"release_date"`
	PosterURL     string `json:"poster_url"`
}

type seriesMetaJSON struct {
	TMDBID        int64  `json:"tmdb_id"`
	TVDBID        int64  `json:"tvdb_id"`
	IMDBID        string `json:"imdb_id"`
	Title         string `json:"title"`
	OriginalTitle string `json:"original_title"`
	Year          int    `json:"year"`
	Overview      string `json:"overview"`
	Status        string `json:"status"`
	FirstAirDate  string `json:"first_air_date"`
	PosterURL     string `json:"poster_url"`
}

// searchResponse keeps the two media types in separate lists rather than one
// tagged list: they have genuinely different fields, and the client renders
// them in separate tabs anyway.
type searchResponse struct {
	Movies []movieMetaJSON  `json:"movies"`
	Series []seriesMetaJSON `json:"series"`
}

// handleSearch queries the metadata provider (SPEC §9 step 1). type=movie or
// type=series restricts the query to one provider call; anything else queries
// both. The unqueried list comes back empty, never null.
func (s *server) handleSearch(w http.ResponseWriter, r *http.Request) {
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}

	kind := r.URL.Query().Get("type")
	if kind == "" {
		kind = TypeAll
	}
	if kind != TypeAll && kind != MediaTypeMovie && kind != MediaTypeSeries {
		writeError(w, http.StatusBadRequest, "type must be movie, series or all")
		return
	}

	provider := s.mgr.Metadata()
	if provider == nil {
		writeError(w, http.StatusServiceUnavailable, "no metadata provider configured")
		return
	}

	ctx := r.Context()
	out := searchResponse{Movies: []movieMetaJSON{}, Series: []seriesMetaJSON{}}

	if kind == TypeAll || kind == MediaTypeMovie {
		movies, err := provider.SearchMovies(ctx, query)
		if err != nil {
			s.log.Error("metadata movie search failed", "error", err)
			writeError(w, http.StatusBadGateway, "metadata search failed")
			return
		}
		for _, m := range movies {
			out.Movies = append(out.Movies, movieMetaDTO(m))
		}
	}

	if kind == TypeAll || kind == MediaTypeSeries {
		series, err := provider.SearchSeries(ctx, query)
		if err != nil {
			s.log.Error("metadata series search failed", "error", err)
			writeError(w, http.StatusBadGateway, "metadata search failed")
			return
		}
		for _, sr := range series {
			out.Series = append(out.Series, seriesMetaDTO(sr))
		}
	}

	writeJSON(w, http.StatusOK, out)
}

func movieMetaDTO(m core.MovieMeta) movieMetaJSON {
	return movieMetaJSON{
		TMDBID:        m.TMDBID,
		IMDBID:        m.IMDBID,
		Title:         m.Title,
		OriginalTitle: m.OriginalTitle,
		Year:          m.Year,
		Overview:      m.Overview,
		ReleaseDate:   jsonTime(m.ReleaseDate),
		PosterURL:     m.PosterURL,
	}
}

func seriesMetaDTO(sr core.SeriesMeta) seriesMetaJSON {
	return seriesMetaJSON{
		TMDBID:        sr.TMDBID,
		TVDBID:        sr.TVDBID,
		IMDBID:        sr.IMDBID,
		Title:         sr.Title,
		OriginalTitle: sr.OriginalTitle,
		Year:          sr.Year,
		Overview:      sr.Overview,
		Status:        sr.Status,
		FirstAirDate:  jsonTime(sr.FirstAirDate),
		PosterURL:     sr.PosterURL,
	}
}
