package api

import (
	"context"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/library"
)

// Values accepted by GET /search?type=. The default is TypeAll, which queries
// both media types in one round trip — the UI's add-to-library picker lets the
// user flip between them without re-typing.
const (
	TypeAll = "all"
)

// movieMetaJSON and seriesMetaJSON are provider search hits: not library items
// yet, so they carry a provider identity and no library id. PosterURL is an
// absolute provider URL rather than a storage-root-relative path, because
// nothing has been downloaded at this point.
//
// Provider and ProviderRef are the row's identity in the vocabulary of the
// provider that offered it, and they are what an add sends back. TMDBID stays
// beside them and stays authoritative for a TMDB hit: a client written before
// chains existed keeps reading exactly what it always did, and a hit from any
// other provider simply carries a zero there — which is honest, because it has
// no TMDB id.
type movieMetaJSON struct {
	TMDBID int64 `json:"tmdb_id"`
	// Provider is the id that answered ("tmdb", "anilist"), and ProviderRef is
	// this title's id in that provider's own numbering. The pair is the only
	// thing that identifies a hit from a chain of more than one provider: two
	// providers' ids are different numbers for different things.
	Provider      string  `json:"provider"`
	ProviderRef   string  `json:"provider_ref"`
	IMDBID        string  `json:"imdb_id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Year          int     `json:"year"`
	Overview      string  `json:"overview"`
	ReleaseDate   string  `json:"release_date"`
	VoteAverage   float64 `json:"vote_average"`
	VoteCount     int     `json:"vote_count"`
	PosterURL     string  `json:"poster_url"`
}

type seriesMetaJSON struct {
	TMDBID int64 `json:"tmdb_id"`
	// Provider and ProviderRef read exactly as movieMetaJSON's do.
	Provider      string  `json:"provider"`
	ProviderRef   string  `json:"provider_ref"`
	TVDBID        int64   `json:"tvdb_id"`
	IMDBID        string  `json:"imdb_id"`
	Title         string  `json:"title"`
	OriginalTitle string  `json:"original_title"`
	Year          int     `json:"year"`
	Overview      string  `json:"overview"`
	Status        string  `json:"status"`
	FirstAirDate  string  `json:"first_air_date"`
	VoteAverage   float64 `json:"vote_average"`
	VoteCount     int     `json:"vote_count"`
	PosterURL     string  `json:"poster_url"`
}

// searchResponse keeps the two media types in separate lists rather than one
// tagged list: they have genuinely different fields, and the client renders
// them in separate tabs anyway.
type searchResponse struct {
	Movies []movieMetaJSON  `json:"movies"`
	Series []seriesMetaJSON `json:"series"`
	// Providers are the chain ids that actually ran, in the order they ran.
	// The client uses the LENGTH as much as the contents: a per-row provider
	// badge is noise on the overwhelmingly common single-provider install, and
	// is the only way to tell two hits apart once a chain is longer than one.
	Providers []string `json:"providers"`
	// LibraryID is the library the chain belongs to, echoed so the add the user
	// makes next lands in the library they searched. Zero means the request
	// named none and the kind's default answered.
	LibraryID int64 `json:"library_id"`
	// Errors are the providers that ran and failed while others succeeded.
	//
	// They are part of a 200, deliberately: one provider being down must not
	// hide the hits the others returned, and a chain that silently came back
	// short is indistinguishable from a chain that had nothing to say. A chain
	// where EVERY provider failed is not this — that is the 502/503 below.
	Errors []searchErrorJSON `json:"errors"`
}

// searchErrorJSON is one provider's refusal, named so the add dialog can say
// which one and why.
type searchErrorJSON struct {
	Provider string `json:"provider"`
	Message  string `json:"message"`
}

// handleSearch identifies a title through a library's provider chain (SPEC §9
// step 1).
//
// ?library_id= names the shelf the add will land on, and therefore the chain
// that answers. Absent, the kind's default library answers — which is the shelf
// the add would land on anyway, so a search made before the user picked one is
// still a search of somewhere real.
//
// type=movie or type=series restricts the query to one half; anything else runs
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

	lib, libraryID, ok := s.resolveSearchLibrary(w, r)
	if !ok {
		return
	}

	// A named library answers about ONE kind of thing, so only the half its
	// kind speaks can run: a television library's chain has no vocabulary for a
	// movie query, and asking it anyway would return whatever the chain's
	// providers happen to say about films that are not going on that shelf.
	// With no library named, both halves run against their own defaults, which
	// is what type=all has always meant.
	runMovies := kind == TypeAll || kind == MediaTypeMovie
	runSeries := kind == TypeAll || kind == MediaTypeSeries
	if lib != nil {
		runMovies = runMovies && lib.Kind == core.LibraryKindMovie
		runSeries = runSeries && lib.Kind == core.LibraryKindTV
	}

	ctx := r.Context()

	// The credential pre-check, before any provider is asked.
	//
	// A cached verdict is about ONE provider's key, so it can only speak for a
	// chain that contains that provider. A library chained to AniList alone
	// needs no key at all, and refusing its search because a key it never uses
	// was rejected an hour ago would make the provider that works unreachable
	// through the provider that does not. The question is asked of every
	// credentialed provider on the chain rather than of TMDB, so a rejected
	// TheTVDB key refuses the libraries chained to TheTVDB and no others. A
	// MIXED chain is still refused whole: this runs before anything does, so
	// there is no partial answer to keep.
	var willRun []string
	if runMovies {
		willRun = appendUniqueProviders(willRun, s.searchChain(ctx, lib, core.LibraryKindMovie))
	}
	if runSeries {
		willRun = appendUniqueProviders(willRun, s.searchChain(ctx, lib, core.LibraryKindTV))
	}
	if s.credentialRejected(w, r, willRun) {
		return
	}

	out := searchResponse{
		Movies: []movieMetaJSON{}, Series: []seriesMetaJSON{},
		Providers: []string{}, Errors: []searchErrorJSON{},
		LibraryID: libraryID,
	}

	if runMovies {
		hits, err := s.mgr.SearchLibrary(ctx, libraryID, MediaTypeMovie, query)
		if err != nil {
			s.writeMetadataError(w, willRun, "metadata movie search failed", err)
			return
		}
		for _, m := range hits.Movies {
			out.Movies = append(out.Movies, movieMetaDTO(m))
		}
		out.absorb(hits)
	}

	if runSeries {
		hits, err := s.mgr.SearchLibrary(ctx, libraryID, MediaTypeSeries, query)
		if err != nil {
			s.writeMetadataError(w, willRun, "metadata series search failed", err)
			return
		}
		for _, sr := range hits.Series {
			out.Series = append(out.Series, seriesMetaDTO(sr))
		}
		out.absorb(hits)
	}

	writeJSON(w, http.StatusOK, out)
}

// absorb folds one half's chain report into the response.
func (out *searchResponse) absorb(hits *library.SearchHits) {
	out.Providers = appendUniqueProviders(out.Providers, hits.Providers)
	for _, f := range hits.Failures {
		out.Errors = append(out.Errors, searchErrorJSON{Provider: f.Provider, Message: f.Message})
	}
}

// appendUniqueProviders adds one chain's ids to a running list, keeping each
// id's first position.
//
// type=all with no library named searches TWO libraries, and on a stock install
// both are chained to TMDB. A list that named it twice would make the client's
// "more than one provider ran, so badge the rows" rule fire on an install with
// exactly one provider.
func appendUniqueProviders(into, ids []string) []string {
	for _, id := range ids {
		if !slices.Contains(into, id) {
			into = append(into, id)
		}
	}
	return into
}

// resolveSearchLibrary resolves ?library_id=, writing the refusal itself. A nil
// library with a true second return means none was named.
//
// An adult library is refused rather than searched. Without this, a caller who
// may see the adult module could route a stash-box search through the
// television endpoint by naming an adult library — and /search sits in front of
// requireAdult, not behind it, so the gate the adult surfaces are built on
// would simply not be in the path.
func (s *server) resolveSearchLibrary(w http.ResponseWriter, r *http.Request) (*core.Library, int64, bool) {
	raw := r.URL.Query().Get("library_id")
	if raw == "" {
		return nil, 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id < 0 {
		writeError(w, http.StatusBadRequest, "invalid library_id")
		return nil, 0, false
	}
	// Zero is the same "nobody chose" every other endpoint spells that way.
	if id == 0 {
		return nil, 0, true
	}
	lib, ok := s.getVisibleLibrary(w, r, id)
	if !ok {
		return nil, 0, false
	}
	if lib.Kind == core.LibraryKindAdult {
		writeError(w, http.StatusBadRequest, "adult libraries are searched through /adult/search")
		return nil, 0, false
	}
	return lib, lib.ID, true
}

// searchChain is the provider chain the search is about to walk: the named
// library's, or the kind's default library's when none was named.
//
// It exists only for the credential pre-check, which has to know whether TMDB
// is on the chain BEFORE anything is asked. SearchLibrary resolves the same
// library for itself, so a failure to resolve it here is not worth reporting —
// the search that follows reports it properly.
func (s *server) searchChain(ctx context.Context, lib *core.Library, kind string) []string {
	if lib != nil {
		return lib.ProviderChain()
	}
	def, err := s.st.GetDefaultLibrary(ctx, kind)
	if err != nil {
		return nil
	}
	return def.ProviderChain()
}

func movieMetaDTO(m core.MovieMeta) movieMetaJSON {
	return movieMetaJSON{
		TMDBID:        m.TMDBID,
		Provider:      m.Provider,
		ProviderRef:   m.ProviderRef,
		IMDBID:        m.IMDBID,
		Title:         m.Title,
		OriginalTitle: m.OriginalTitle,
		Year:          m.Year,
		Overview:      m.Overview,
		ReleaseDate:   jsonTime(m.ReleaseDate),
		VoteAverage:   m.VoteAverage,
		VoteCount:     m.VoteCount,
		PosterURL:     m.PosterURL,
	}
}

func seriesMetaDTO(sr core.SeriesMeta) seriesMetaJSON {
	return seriesMetaJSON{
		TMDBID:        sr.TMDBID,
		Provider:      sr.Provider,
		ProviderRef:   sr.ProviderRef,
		TVDBID:        sr.TVDBID,
		IMDBID:        sr.IMDBID,
		Title:         sr.Title,
		OriginalTitle: sr.OriginalTitle,
		Year:          sr.Year,
		Overview:      sr.Overview,
		Status:        sr.Status,
		FirstAirDate:  jsonTime(sr.FirstAirDate),
		VoteAverage:   sr.VoteAverage,
		VoteCount:     sr.VoteCount,
		PosterURL:     sr.PosterURL,
	}
}
