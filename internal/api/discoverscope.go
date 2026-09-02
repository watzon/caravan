package api

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// The filter rail's query surface. These are Caravan's names, not TMDB's: the
// provider spells "when it came out" two different ways depending on the media
// type, and a shareable URL should not have to know that.
const (
	paramGenres     = "genres"
	paramCompanies  = "companies"
	paramKeywords   = "keywords"
	paramNetworks   = "networks"
	paramCast       = "cast"
	paramCrew       = "crew"
	paramPeople     = "people"
	paramFrom       = "from"
	paramTo         = "to"
	paramRuntimeMin = "runtime_min"
	paramRuntimeMax = "runtime_max"
	paramRatingMin  = "rating_min"
	paramVotesMin   = "votes_min"
	paramLanguage   = "language"
	paramSort       = "sort"
	paramOrder      = "order"
	paramPage       = "page"

	// filterDateLayout is how a date range is spelled in the URL. It is the
	// provider's own layout, which is also the one a human types.
	filterDateLayout = "2006-01-02"
)

// sharedFilterParams is what both scopes accept; the two scope lists add what
// only one of them can serve.
//
// The lists are an allowlist rather than documentation: a parameter that is
// not on the scope's list is a 400, so "movies with this actor" and "series
// with this actor" are visibly different questions rather than one that works
// and one that quietly returns the unfiltered catalogue. TMDB's /discover/tv
// has no with_cast, with_crew or with_people and ignores them if sent, which is
// the whole reason this is enforced here and not left to the provider.
var (
	sharedFilterParams = []string{
		paramGenres, paramCompanies, paramKeywords,
		paramFrom, paramTo, paramRuntimeMin, paramRuntimeMax,
		paramRatingMin, paramVotesMin, paramLanguage,
		paramSort, paramOrder, paramPage,
	}
	movieFilterParams  = append([]string{paramCast, paramCrew, paramPeople}, sharedFilterParams...)
	seriesFilterParams = append([]string{paramNetworks}, sharedFilterParams...)
)

// discoverScopeResponse is one page of a filtered scope. It carries no source
// block, unlike /discover/browse there is no curated shelf behind it, the
// filter itself is the description, and the client already holds it.
type discoverScopeResponse struct {
	MediaType  string             `json:"media_type"`
	Page       int                `json:"page"`
	TotalPages int                `json:"total_pages"`
	Items      []discoverItemJSON `json:"items"`
}

type discoverPersonJSON struct {
	TMDBID int64  `json:"tmdb_id"`
	Name   string `json:"name"`
	// Department is what the provider says this person is best known for
	// ("Acting", "Directing"), empty when it does not say. It is the line under
	// the name that tells two people apart.
	Department string `json:"department"`
	ProfileURL string `json:"profile_url"`
}

type discoverCompanyJSON struct {
	TMDBID  int64  `json:"tmdb_id"`
	Name    string `json:"name"`
	Country string `json:"country"`
	LogoURL string `json:"logo_url"`
}

// discoverNamedJSON is an id and a name: what a keyword and a genre both are.
type discoverNamedJSON struct {
	TMDBID int64  `json:"tmdb_id"`
	Name   string `json:"name"`
}

type discoverPeopleResponse struct {
	People []discoverPersonJSON `json:"people"`
}

type discoverCompaniesResponse struct {
	Companies []discoverCompanyJSON `json:"companies"`
}

type discoverKeywordsResponse struct {
	Keywords []discoverNamedJSON `json:"keywords"`
}

type discoverGenresResponse struct {
	MediaType string              `json:"media_type"`
	Genres    []discoverNamedJSON `json:"genres"`
}

// handleDiscoverMovies serves one page of the filtered movie scope.
func (s *server) handleDiscoverMovies(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.discovery(w, r)
	if !ok {
		return
	}

	filter, err := parseMovieFilter(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	page, err := provider.DiscoverMovies(r.Context(), filter)
	if err != nil {
		s.writeDiscoverError(w, r, "discover movies", err)
		return
	}
	s.writeDiscoverScope(w, r, MediaTypeMovie, page)
}

// handleDiscoverSeries serves one page of the filtered series scope.
func (s *server) handleDiscoverSeries(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.discovery(w, r)
	if !ok {
		return
	}

	filter, err := parseSeriesFilter(r.URL.Query())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	page, err := provider.DiscoverSeries(r.Context(), filter)
	if err != nil {
		s.writeDiscoverError(w, r, "discover series", err)
		return
	}
	s.writeDiscoverScope(w, r, MediaTypeSeries, page)
}

// writeDiscoverScope decorates a provider page exactly as every other discover
// row is decorated and writes it.
func (s *server) writeDiscoverScope(w http.ResponseWriter, r *http.Request, mediaType string, page *core.DiscoverPage) {
	state, err := s.libraryStateFor(r.Context(), page.Items)
	if err != nil {
		s.writeStoreError(w, "read library state", err)
		return
	}
	writeJSON(w, http.StatusOK, discoverScopeResponse{
		MediaType:  mediaType,
		Page:       page.Page,
		TotalPages: page.TotalPages,
		Items:      state.decorateAll(page.Items),
	})
}

// handleDiscoverPeople, handleDiscoverCompanies and handleDiscoverKeywords are
// the filter rail's typeaheads. They are pure passthroughs: nothing here is in
// the library, so nothing is decorated.
func (s *server) handleDiscoverPeople(w http.ResponseWriter, r *http.Request) {
	provider, query, ok := s.typeahead(w, r)
	if !ok {
		return
	}
	people, err := provider.SearchPeople(r.Context(), query)
	if err != nil {
		s.writeDiscoverError(w, r, "person search", err)
		return
	}
	out := discoverPeopleResponse{People: make([]discoverPersonJSON, 0, len(people))}
	for _, p := range people {
		out.People = append(out.People, discoverPersonJSON{
			TMDBID:     p.TMDBID,
			Name:       p.Name,
			Department: p.Department,
			ProfileURL: p.ProfileURL,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleDiscoverCompanies(w http.ResponseWriter, r *http.Request) {
	provider, query, ok := s.typeahead(w, r)
	if !ok {
		return
	}
	companies, err := provider.SearchCompanies(r.Context(), query)
	if err != nil {
		s.writeDiscoverError(w, r, "company search", err)
		return
	}
	out := discoverCompaniesResponse{Companies: make([]discoverCompanyJSON, 0, len(companies))}
	for _, c := range companies {
		out.Companies = append(out.Companies, discoverCompanyJSON{
			TMDBID:  c.TMDBID,
			Name:    c.Name,
			Country: c.Country,
			LogoURL: c.LogoURL,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleDiscoverKeywords(w http.ResponseWriter, r *http.Request) {
	provider, query, ok := s.typeahead(w, r)
	if !ok {
		return
	}
	keywords, err := provider.SearchKeywords(r.Context(), query)
	if err != nil {
		s.writeDiscoverError(w, r, "keyword search", err)
		return
	}
	out := discoverKeywordsResponse{Keywords: make([]discoverNamedJSON, 0, len(keywords))}
	for _, k := range keywords {
		out.Keywords = append(out.Keywords, discoverNamedJSON{TMDBID: k.TMDBID, Name: k.Name})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleDiscoverGenres serves one media type's genre vocabulary. The two lists
// differ and neither is a subset of the other, so ?type= is required rather
// than defaulted. A rail rendering the movie genres over a series scope would
// offer filters that match nothing.
func (s *server) handleDiscoverGenres(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.discovery(w, r)
	if !ok {
		return
	}

	mediaType := r.URL.Query().Get("type")
	if mediaType != MediaTypeMovie && mediaType != MediaTypeSeries {
		writeError(w, http.StatusBadRequest, "type must be movie or series")
		return
	}

	genres, err := provider.Genres(r.Context(), mediaType)
	if err != nil {
		s.writeDiscoverError(w, r, "genre list", err)
		return
	}
	out := discoverGenresResponse{MediaType: mediaType, Genres: make([]discoverNamedJSON, 0, len(genres))}
	for _, g := range genres {
		out.Genres = append(out.Genres, discoverNamedJSON{TMDBID: g.TMDBID, Name: g.Name})
	}
	writeJSON(w, http.StatusOK, out)
}

// typeahead is the guard every filter typeahead shares: a browse-capable
// provider and a non-empty query.
func (s *server) typeahead(w http.ResponseWriter, r *http.Request) (core.DiscoverProvider, string, bool) {
	provider, ok := s.discovery(w, r)
	if !ok {
		return nil, "", false
	}
	query := strings.TrimSpace(r.URL.Query().Get("q"))
	if query == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return nil, "", false
	}
	return provider, query, true
}

// parseMovieFilter reads the movie scope's query string. Person filters are
// accepted here and refused by parseSeriesFilter.
func parseMovieFilter(q url.Values) (core.MovieFilter, error) {
	p := &filterParser{q: q}
	p.rejectUnknown(movieFilterParams)
	out := core.MovieFilter{
		DiscoverFilter: p.shared(),
		Cast:           p.ids(paramCast),
		Crew:           p.ids(paramCrew),
		People:         p.ids(paramPeople),
	}
	return out, p.err
}

// parseSeriesFilter reads the series scope's query string. cast, crew and
// people are absent from seriesFilterParams, so sending one is a 400.
func parseSeriesFilter(q url.Values) (core.SeriesFilter, error) {
	p := &filterParser{q: q}
	p.rejectUnknown(seriesFilterParams)
	out := core.SeriesFilter{
		DiscoverFilter: p.shared(),
		Networks:       p.ids(paramNetworks),
	}
	return out, p.err
}

// filterParser reads one query string, keeping the first problem it finds.
// Every reader is a no-op once err is set, so a malformed value is reported
// once rather than cascading.
type filterParser struct {
	q   url.Values
	err error
}

func (p *filterParser) fail(format string, args ...any) {
	if p.err == nil {
		p.err = fmt.Errorf(format, args...)
	}
}

// rejectUnknown refuses any parameter this scope cannot serve, naming it. A
// silently dropped filter is the failure mode this whole surface is built to
// avoid: the caller believes it asked a narrower question than it did.
func (p *filterParser) rejectUnknown(allowed []string) {
	for key := range p.q {
		if !slices.Contains(allowed, key) {
			p.fail("%s is not a filter this scope accepts", key)
			return
		}
	}
}

func (p *filterParser) shared() core.DiscoverFilter {
	return core.DiscoverFilter{
		Genres:         p.ids(paramGenres),
		Companies:      p.ids(paramCompanies),
		Keywords:       p.ids(paramKeywords),
		ReleasedFrom:   p.date(paramFrom),
		ReleasedTo:     p.date(paramTo),
		RuntimeMin:     p.count(paramRuntimeMin),
		RuntimeMax:     p.count(paramRuntimeMax),
		VoteAverageMin: p.rating(paramRatingMin),
		VoteCountMin:   p.count(paramVotesMin),
		Language:       strings.TrimSpace(p.q.Get(paramLanguage)),
		Sort:           p.sort(),
		Order:          p.order(),
		Page:           p.count(paramPage),
	}
}

// ids reads a comma-separated id list. Ids are positive by definition, so a
// zero or negative one is a malformed request rather than a filter.
func (p *filterParser) ids(key string) []int64 {
	raw := strings.TrimSpace(p.q.Get(key))
	if p.err != nil || raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]int64, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			p.fail("%s must be a comma-separated list of ids", key)
			return nil
		}
		out = append(out, id)
	}
	return out
}

func (p *filterParser) date(key string) time.Time {
	raw := strings.TrimSpace(p.q.Get(key))
	if p.err != nil || raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(filterDateLayout, raw)
	if err != nil {
		p.fail("%s must be a date like 2006-01-02", key)
		return time.Time{}
	}
	return t
}

// count reads a non-negative whole number: a runtime bound, a vote floor, a
// page. Zero is "unset" for all three, which is why negatives are refused
// rather than clamped. They would be indistinguishable from not asking.
func (p *filterParser) count(key string) int {
	raw := strings.TrimSpace(p.q.Get(key))
	if p.err != nil || raw == "" {
		return 0
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 0 {
		p.fail("%s must be a whole number of 0 or more", key)
		return 0
	}
	return n
}

func (p *filterParser) rating(key string) float64 {
	raw := strings.TrimSpace(p.q.Get(key))
	if p.err != nil || raw == "" {
		return 0
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil || v < 0 || v > 10 {
		p.fail("%s must be a rating between 0 and 10", key)
		return 0
	}
	return v
}

func (p *filterParser) sort() core.DiscoverSort {
	raw := strings.TrimSpace(p.q.Get(paramSort))
	if p.err != nil || raw == "" {
		return ""
	}
	sort, ok := core.ParseDiscoverSort(raw)
	if !ok {
		p.fail("sort must be one of popularity, release_date, rating, votes, title")
		return ""
	}
	return sort
}

func (p *filterParser) order() core.DiscoverOrder {
	raw := strings.TrimSpace(p.q.Get(paramOrder))
	if p.err != nil || raw == "" {
		return ""
	}
	order, ok := core.ParseDiscoverOrder(raw)
	if !ok {
		p.fail("order must be asc or desc")
		return ""
	}
	return order
}
