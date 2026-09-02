package api

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// The scene scope's filter surface: the query parameters GET /adult/discover
// accepts, and the two typeaheads that fill them in.
//
// every route in this file is registered on the adult mux in api.go and nowhere
// else, for the reason adultsites.go gives at length: the mux is the access
// control, so a handler here does not repeat the check and must never be moved
// off it.
const (
	paramQuery         = "q"
	paramSite          = "site"
	paramScope         = "scope"
	paramPerformers    = "performers"
	paramPerformersAll = "performers_all"
	paramTags          = "tags"
	paramTagsAll       = "tags_all"
	paramYear          = "year"
	paramDate          = "date"
	paramDateOp        = "date_op"
	paramDuration      = "duration"
	// paramProvider names which configured stash-box instance answers. It is
	// not a filter (it chooses the catalogue the filters are applied to) but it
	// rides in the same query string, so the allowlist has to know it or
	// rejectUnknown would refuse it as an unserved filter.
	paramProvider = "provider"
)

// sceneFilterParams is the allowlist for GET /adult/discover. As on the movie
// and series scopes, a parameter that is not on it is a 400 rather than a
// silent drop: a caller must never believe it asked a narrower question than
// it did.
var sceneFilterParams = []string{
	paramQuery, paramPage, paramSite, paramScope,
	paramPerformers, paramPerformersAll, paramTags, paramTagsAll,
	paramYear, paramDate, paramDateOp, paramDuration,
	paramSort, paramOrder, paramProvider,
}

// sceneFilterRefJSON is one performer or tag, in a typeahead answer and in the
// filter that follows it.
//
// ID is a string and is opaque: it is TPDB's numeric id on a TPDB install and a
// stash-box uuid elsewhere, and a client that echoes back what it was handed
// never has to know which. That is the same rule the rest of the adult surface
// keeps. Nothing downstream learns that the REST dialect exists.
type sceneFilterRefJSON struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type scenePerformerJSON struct {
	sceneFilterRefJSON
	ImageURL string `json:"image_url"`
}

type scenePerformersResponse struct {
	Performers []scenePerformerJSON `json:"performers"`
}

type sceneTagsResponse struct {
	Tags []sceneFilterRefJSON `json:"tags"`
}

// handleAdultPerformers and handleAdultTags are the scene filter rail's
// typeaheads. They are pure passthroughs: a performer is not something the
// library holds, so there is nothing to decorate.
func (s *server) handleAdultPerformers(w http.ResponseWriter, r *http.Request) {
	provider, query, ok := s.adultTypeahead(w, r)
	if !ok {
		return
	}
	performers, err := provider.SearchPerformers(r.Context(), query)
	if err != nil {
		s.writeAdultProviderError(w, r, "performer search", err)
		return
	}
	out := scenePerformersResponse{Performers: make([]scenePerformerJSON, 0, len(performers))}
	for _, p := range performers {
		out.Performers = append(out.Performers, scenePerformerJSON{
			sceneFilterRefJSON: sceneFilterRefDTO(p.SceneFilterRef),
			ImageURL:           p.ImageURL,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) handleAdultTags(w http.ResponseWriter, r *http.Request) {
	provider, query, ok := s.adultTypeahead(w, r)
	if !ok {
		return
	}
	tags, err := provider.SearchTags(r.Context(), query)
	if err != nil {
		s.writeAdultProviderError(w, r, "tag search", err)
		return
	}
	out := sceneTagsResponse{Tags: make([]sceneFilterRefJSON, 0, len(tags))}
	for _, t := range tags {
		out.Tags = append(out.Tags, sceneFilterRefDTO(t))
	}
	writeJSON(w, http.StatusOK, out)
}

// adultTypeahead is the guard both scene typeaheads share: a configured
// provider and a non-empty query. It is the adult twin of typeahead(), kept
// separate because the two read different credentials and answer different
// coded 503s.
func (s *server) adultTypeahead(w http.ResponseWriter, r *http.Request) (core.AdultMetadataProvider, string, bool) {
	provider, _, ok := s.adultProvider(w, r)
	if !ok {
		return nil, "", false
	}
	query := strings.TrimSpace(r.URL.Query().Get(paramQuery))
	if query == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return nil, "", false
	}
	return provider, query, true
}

// sceneFilterRefDTO renders a ref for the wire, picking whichever id the
// configured endpoint filters on. parseSceneRef reads it back.
func sceneFilterRefDTO(ref core.SceneFilterRef) sceneFilterRefJSON {
	id := ref.StashID
	if ref.ID > 0 {
		id = strconv.FormatInt(ref.ID, 10)
	}
	return sceneFilterRefJSON{ID: id, Name: ref.Name}
}

// parseSceneQuery reads GET /adult/discover's query string.
func parseSceneQuery(q url.Values) (core.SceneQuery, error) {
	p := &filterParser{q: q}
	p.rejectUnknown(sceneFilterParams)

	out := core.SceneQuery{
		Text:          strings.TrimSpace(q.Get(paramQuery)),
		SiteStashID:   strings.TrimSpace(q.Get(paramSite)),
		SiteScope:     p.siteScope(),
		Performers:    p.refs(paramPerformers),
		PerformersAll: p.flag(paramPerformersAll),
		Tags:          p.refs(paramTags),
		TagsAll:       p.flag(paramTagsAll),
		Year:          p.count(paramYear),
		Date:          p.date(paramDate),
		DateOp:        p.dateOp(),
		Duration:      p.count(paramDuration),
		Sort:          p.sceneSort(),
		Order:         p.order(),
		// An unparseable page is page 1, as it is on /discover/browse: it is
		// how a client that has not paged yet spells "the beginning". Every
		// filter above is strict, because a filter read wrongly changes which
		// scenes come back and a page read wrongly does not.
		Page: sceneQueryPage(q),
	}

	// A scope with nothing to widen is a client bug worth naming: the caller
	// asked for a whole network and would have got the unfiltered index.
	if out.SiteScope != "" && out.SiteScope != core.SceneSiteOnly && out.SiteStashID == "" {
		p.fail("%s needs a %s to widen", paramScope, paramSite)
	}
	// Likewise a comparison with no date to compare.
	if out.DateOp != "" && out.Date.IsZero() {
		p.fail("%s needs a %s", paramDateOp, paramDate)
	}
	return out, p.err
}

func sceneQueryPage(q url.Values) int {
	page, _ := strconv.Atoi(strings.TrimSpace(q.Get(paramPage)))
	return page
}

// refs reads a repeated performer or tag parameter. Each value is one ref,
// spelled `id` or `id:name`.
//
// It is repeated rather than one comma-separated list (the shape the movie and
// series scopes use for their id lists) because a value here carries a name,
// and a performer's name may contain a comma. The id is opaque: numeric on
// TPDB, a uuid elsewhere (see sceneFilterRefJSON), so it is classified rather
// than validated as one or the other.
func (p *filterParser) refs(key string) []core.SceneFilterRef {
	values := p.q[key]
	if p.err != nil || len(values) == 0 {
		return nil
	}
	out := make([]core.SceneFilterRef, 0, len(values))
	for _, raw := range values {
		id, name, _ := strings.Cut(raw, ":")
		id, name = strings.TrimSpace(id), strings.TrimSpace(name)
		if id == "" {
			p.fail("%s must each be an id, optionally followed by :name", key)
			return nil
		}
		ref := core.SceneFilterRef{StashID: id, Name: name}
		if n, err := strconv.ParseInt(id, 10, 64); err == nil {
			if n <= 0 {
				p.fail("%s must each be an id, optionally followed by :name", key)
				return nil
			}
			ref = core.SceneFilterRef{ID: n, Name: name}
		}
		out = append(out, ref)
	}
	return out
}

// flag reads a boolean switch. Only the two spellings are accepted: a filter
// that read "yes" as false would quietly answer the other question.
func (p *filterParser) flag(key string) bool {
	raw := strings.TrimSpace(p.q.Get(key))
	if p.err != nil || raw == "" {
		return false
	}
	switch raw {
	case "true":
		return true
	case "false":
		return false
	}
	p.fail("%s must be true or false", key)
	return false
}

func (p *filterParser) siteScope() core.SceneSiteScope {
	raw := strings.TrimSpace(p.q.Get(paramScope))
	if p.err != nil || raw == "" {
		return ""
	}
	scope, ok := core.ParseSceneSiteScope(raw)
	if !ok {
		p.fail("%s must be one of site, parent, network", paramScope)
		return ""
	}
	return scope
}

func (p *filterParser) dateOp() core.SceneDateOp {
	raw := strings.TrimSpace(p.q.Get(paramDateOp))
	if p.err != nil || raw == "" {
		return ""
	}
	op, ok := core.ParseSceneDateOp(raw)
	if !ok {
		p.fail("%s must be one of on, before, on_or_before, after, on_or_after", paramDateOp)
		return ""
	}
	return op
}

func (p *filterParser) sceneSort() core.SceneSort {
	raw := strings.TrimSpace(p.q.Get(paramSort))
	if p.err != nil || raw == "" {
		return ""
	}
	sort, ok := core.ParseSceneSort(raw)
	if !ok {
		p.fail("%s must be one of released, created, updated, duration, relevance", paramSort)
		return ""
	}
	return sort
}
