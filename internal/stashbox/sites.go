package stashbox

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"unicode"

	"github.com/watzon/caravan/internal/core"
)

// GraphQL operation names. They are the routing key for the fake endpoint in
// stashboxtest and the Operation on every APIError, so they are constants
// rather than literals repeated in two places.
const (
	opSearchSites = "SearchSites"
	opFindSite    = "FindSite"
	// The two scene-derived searches SearchSites falls back to on an endpoint
	// with no queryStudios; see searchSitesByScenes.
	opSearchSitesByScene = "SearchSitesByScene"
	opRecentSitesByScene = "RecentSitesByScene"
)

// maxSceneDerivedSites caps how many candidates the fallback looks up. Each one
// costs a findStudio round trip, and a search box wants a shortlist rather than
// every studio that ever appeared on a page of scenes.
const maxSceneDerivedSites = 10

// siteFields is the selection set shared by site search and site lookup, so the
// two can never drift into decoding different shapes into the same struct.
//
// stash-box calls a site a Studio. `parent` is one level deep on purpose: the
// network above a site is worth showing, the network above that is not, and
// recursive selections are where a GraphQL query starts costing an endpoint
// real work.
const siteFields = `
    id
    name
    aliases
    urls { url }
    images { url width height }
    parent { id name }
`

const searchSitesQuery = `query ` + opSearchSites + `($input: StudioQueryInput!) {
  queryStudios(input: $input) {
    count
    studios {` + siteFields + `}
  }
}`

const findSiteQuery = `query ` + opFindSite + `($id: ID!) {
  findStudio(id: $id) {` + siteFields + `}
}`

// sceneStudioFields is all the fallback needs off a scene: the site it belongs
// to and the network above that site. It is deliberately not sceneFields: the
// fallback is already paying for an extra round trip per candidate, and asking
// for scene metadata nobody reads would widen the compatibility surface of the
// one path that exists because an endpoint is narrow.
const sceneStudioFields = `
    id
    studio { id name parent { id name } }
`

const searchSitesBySceneQuery = `query ` + opSearchSitesByScene + `($term: String!) {
  searchScene(term: $term) {` + sceneStudioFields + `}
}`

const recentSitesBySceneQuery = `query ` + opRecentSitesByScene + `($input: SceneQueryInput!) {
  queryScenes(input: $input) {
    scenes {` + sceneStudioFields + `}
  }
}`

// siteResult is the stash-box Studio shape shared by search results and
// lookups.
type siteResult struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	Aliases []string      `json:"aliases"`
	URLs    []urlResult   `json:"urls"`
	Images  []imageResult `json:"images"`
	Parent  *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"parent"`
}

// sceneStudioResult is the slice of a stash-box Scene the scene-derived site
// search reads: which studio the scene belongs to, and the network above it.
type sceneStudioResult struct {
	Studio *struct {
		ID     string `json:"id"`
		Name   string `json:"name"`
		Parent *struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"parent"`
	} `json:"studio"`
}

// SearchSites returns site candidates for q, in the endpoint's own relevance
// order. A blank q returns the endpoint's first page rather than an error,
// which is what an "add a site" screen wants before anything is typed.
//
// queryStudios is the right query and StashDB serves it, so it is always tried
// first. TPDB, the default endpoint, does not implement it at all and answers
// with a bare HTTP 500, which is why there is a second path at all: see
// searchSitesByScenes. Any other failure (auth, throttling, a network error) is
// the caller's, because falling back on those would turn a fixable error into a
// worse set of results.
//
// The full order, best answer first: queryStudios (every stash-box but TPDB) →
// TPDB's REST site index for a typed query (tpdb.go) → studios derived from
// scene search, which is what a blank query uses on TPDB and what any future
// endpoint without queryStudios falls back to.
func (c *Client) SearchSites(ctx context.Context, q string) ([]core.SiteMeta, error) {
	if !c.noQueryStudios.Load() {
		out, err := c.searchSitesByStudios(ctx, q)
		if err == nil {
			return out, nil
		}
		if !isQueryStudiosUnsupported(err) {
			return nil, err
		}
		c.noQueryStudios.Store(true)
	}

	// TPDB's REST site index searches names, which is the question the user is
	// asking; the scene-derived path searches scene text, which is a different
	// question that happens to return studios. A failure here is not shown: the
	// scene-derived path below is a worse answer but still an answer, and if it
	// fails too its error is the one the user sees.
	if needle := strings.TrimSpace(q); needle != "" && c.restSites != "" {
		if out, err := c.searchSitesByREST(ctx, needle); err == nil {
			return out, nil
		}
	}
	return c.searchSitesByScenes(ctx, q)
}

// searchSitesByStudios is the primary search: one queryStudios call.
func (c *Client) searchSitesByStudios(ctx context.Context, q string) ([]core.SiteMeta, error) {
	var resp struct {
		QueryStudios struct {
			Count   int          `json:"count"`
			Studios []siteResult `json:"studios"`
		} `json:"queryStudios"`
	}

	input := map[string]any{
		"page":     1,
		"per_page": defaultPerPage,
	}
	if name := strings.TrimSpace(q); name != "" {
		// `names` matches aliases as well as the canonical name, which is what
		// a user typing a release-name site string needs.
		input["names"] = name
	}

	if err := c.query(ctx, opSearchSites, searchSitesQuery, map[string]any{"input": input}, &resp); err != nil {
		return nil, err
	}

	out := make([]core.SiteMeta, 0, len(resp.QueryStudios.Studios))
	for _, s := range resp.QueryStudios.Studios {
		out = append(out, siteMeta(s))
	}
	return out, nil
}

// searchSitesByScenes finds sites on an endpoint that cannot search studios, by
// searching the thing it can search, scenes, and keeping the studios they name.
//
// It is strictly worse than queryStudios and only runs when that query is
// missing: it sees the sites that have scenes matching q rather than the sites
// whose *name* matches q, and it costs one findStudio round trip per candidate.
// What it buys is that "add a site" works at all on TPDB, which is the default
// endpoint.
//
// Candidates are looked up one at a time and a failed lookup is skipped rather
// than fatal: one unreadable studio must not empty a search that found nine
// good ones.
//
// Ranking happens twice, and deliberately. The shortlist is chosen on the names
// the scene payload already carries, because that is the only ordering that can
// exist before anything is fetched: and the cap has to be applied to *ranked*
// candidates or it is the frequency-only bug again with an extra step. The
// shortlist is then re-ranked once the full records arrive, because those carry
// the aliases, and an alias is how a release names a site. The consequence is
// worth stating plainly: an alias can promote a candidate within the shortlist,
// but it cannot rescue one that never made it in. Fetching everything first
// would fix that and cost an unbounded number of round trips per keystroke,
// which is the trade this endpoint cannot afford.
func (c *Client) searchSitesByScenes(ctx context.Context, q string) ([]core.SiteMeta, error) {
	scenes, err := c.sceneStudios(ctx, q)
	if err != nil {
		return nil, err
	}

	needle := strings.ToLower(strings.TrimSpace(q))
	shortlist := rankSceneStudios(scenes, needle, maxSceneDerivedSites)

	resolved := make([]siteCandidate, 0, len(shortlist))
	sites := make(map[string]core.SiteMeta, len(shortlist))
	for _, cand := range shortlist {
		// A cancelled context turns every remaining lookup into a doomed
		// request, so stop rather than skip.
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		site, err := c.GetSite(ctx, cand.id)
		if err != nil {
			continue
		}
		if a := nameAffinity(needle, append([]string{site.Name}, site.Aliases...)...); a > cand.affinity {
			cand.affinity = a
		}
		sites[cand.id] = *site
		resolved = append(resolved, cand)
	}

	sortCandidates(resolved)
	out := make([]core.SiteMeta, 0, len(resolved))
	for _, cand := range resolved {
		out = append(out, sites[cand.id])
	}
	return out, nil
}

// sceneStudios fetches the page of scenes the candidates are derived from.
//
// searchScene needs a term, so a blank q, the add-a-site screen before anything
// is typed, asks for the newest scenes instead, the same page-1 DATE/DESC shape
// SearchScenes uses. That gives the screen the sites the endpoint is currently
// busy with, which is a better empty state than nothing.
func (c *Client) sceneStudios(ctx context.Context, q string) ([]sceneStudioResult, error) {
	if term := strings.TrimSpace(q); term != "" {
		var resp struct {
			SearchScene []sceneStudioResult `json:"searchScene"`
		}
		if err := c.query(ctx, opSearchSitesByScene, searchSitesBySceneQuery, map[string]any{"term": term}, &resp); err != nil {
			return nil, err
		}
		return resp.SearchScene, nil
	}

	var resp struct {
		QueryScenes struct {
			Scenes []sceneStudioResult `json:"scenes"`
		} `json:"queryScenes"`
	}
	input := map[string]any{
		"page":      1,
		"per_page":  defaultPerPage,
		"sort":      "DATE",
		"direction": "DESC",
	}
	if err := c.query(ctx, opRecentSitesByScene, recentSitesBySceneQuery, map[string]any{"input": input}, &resp); err != nil {
		return nil, err
	}
	return resp.QueryScenes.Scenes, nil
}

// siteAffinity is how well a site's names answer the query. It is the primary
// sort key, ahead of how often the site appeared: a user typing "br" is naming
// a site, not asking which site the endpoint has the most scenes from, and the
// queryStudios path they would get on StashDB answers by name too.
type siteAffinity int

const (
	// affinityNone is no textual match at all. Such a candidate is kept, not
	// dropped: a search for a scene title legitimately surfaces the studio that
	// made it, and that studio is a real answer, just a worse one than anything
	// whose name matches.
	affinityNone siteAffinity = iota
	// affinitySubstring is the query appearing inside a word.
	affinitySubstring
	// affinityWord is a later word starting with the query: "ki" in "Reality
	// Kings". Typing the start of a word is aiming at that word.
	affinityWord
	// affinityPrefix is the whole name starting with the query.
	affinityPrefix
	// affinityExact is the name or an alias being exactly the query.
	affinityExact
)

// siteCandidate is one studio seen across a page of scenes.
type siteCandidate struct {
	id string
	// name is the studio's name as the scene payload gave it. It is what the
	// shortlist is ranked on, since it is the only name available before the
	// full record is fetched.
	name string
	// affinity is how well this candidate's names answer the query. Blank
	// queries leave it at affinityNone for everyone, which sorts on frequency
	// alone: the recent-scenes list the picker opens on.
	affinity siteAffinity
	// count is how many scenes named this studio. A parent counts once per
	// scene naming any of its children, so a network outranks each of its sites
	// without needing a special case.
	count int
	// parent records that the studio was seen as a network above another. It
	// only ever breaks a tie: "Brazzers" and its single sub-studio score the
	// same, and the network is the one a user searching "brazzers" means.
	parent bool
	// first is the order the studio was first seen in, so equal candidates keep
	// the endpoint's own relevance order instead of shuffling between calls.
	first int
}

// rankSceneStudios reduces a page of scenes to at most limit candidates, most
// relevant first. needle is the query, already trimmed and lowercased; blank
// means "no textual opinion", which leaves the old frequency ordering.
//
// Sub-studios and the networks above them are both candidates: a search for
// "brazzers" on an endpoint that files scenes under "Brazzers Exxtra" has to be
// able to offer "Brazzers" itself.
func rankSceneStudios(scenes []sceneStudioResult, needle string, limit int) []siteCandidate {
	byID := make(map[string]*siteCandidate, len(scenes))
	order := make([]*siteCandidate, 0, len(scenes))

	saw := func(id, name string, isParent bool) {
		if id == "" {
			return
		}
		cand := byID[id]
		if cand == nil {
			cand = &siteCandidate{
				id:       id,
				name:     name,
				affinity: nameAffinity(needle, name),
				first:    len(order),
			}
			byID[id] = cand
			order = append(order, cand)
		}
		cand.count++
		cand.parent = cand.parent || isParent
	}

	for _, s := range scenes {
		if s.Studio == nil {
			continue
		}
		saw(s.Studio.ID, s.Studio.Name, false)
		if s.Studio.Parent != nil {
			saw(s.Studio.Parent.ID, s.Studio.Parent.Name, true)
		}
	}

	out := make([]siteCandidate, 0, len(order))
	for _, cand := range order {
		out = append(out, *cand)
	}
	sortCandidates(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// sortCandidates orders candidates best first: name affinity, then how often
// the site appeared, then the network over its sub-studio, then the order the
// endpoint returned them in. Every tie is broken, so the order is the same on
// every call for the same answer.
func sortCandidates(cands []siteCandidate) {
	sort.Slice(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		if a.affinity != b.affinity {
			return a.affinity > b.affinity
		}
		if a.count != b.count {
			return a.count > b.count
		}
		if a.parent != b.parent {
			return a.parent
		}
		return a.first < b.first
	})
}

// nameAffinity scores the best match between needle and any of names. needle is
// already trimmed and lowercased; a blank one has no opinion about anything.
func nameAffinity(needle string, names ...string) siteAffinity {
	if needle == "" {
		return affinityNone
	}
	best := affinityNone
	for _, name := range names {
		n := strings.ToLower(strings.TrimSpace(name))
		switch {
		case n == "":
			continue
		case n == needle:
			// Nothing outranks an exact name, so stop looking.
			return affinityExact
		case strings.HasPrefix(n, needle):
			if best < affinityPrefix {
				best = affinityPrefix
			}
		case hasWordPrefix(n, needle):
			if best < affinityWord {
				best = affinityWord
			}
		case strings.Contains(n, needle):
			if best < affinitySubstring {
				best = affinitySubstring
			}
		}
	}
	return best
}

// hasWordPrefix reports whether any word of name starts with needle. Words are
// split on anything that is not a letter or a digit, so "Brazzers-Live" and
// "Brazzers Live" answer the same: a site's punctuation is its own business.
func hasWordPrefix(name, needle string) bool {
	for _, word := range strings.FieldsFunc(name, isNameSeparator) {
		if strings.HasPrefix(word, needle) {
			return true
		}
	}
	return false
}

func isNameSeparator(r rune) bool {
	return !unicode.IsLetter(r) && !unicode.IsDigit(r)
}

// isQueryStudiosUnsupported reports whether err is the endpoint saying it has
// no usable queryStudios, as opposed to a failure worth surfacing.
//
// Two shapes count. TPDB answers the query with an HTTP 500 whose body is the
// plain text "Server Error": not a GraphQL error, which is why any 5xx counts
// rather than a specific code. A stricter endpoint would instead reject the
// document at validation time and name the field it does not have.
//
// A 5xx is not proof the query is missing forever, a box having a bad minute
// reads the same, so the memo this drives is per-process and re-checked on
// restart. Degrading to a working search for the life of a process beats a
// search box that 502s on every keystroke.
func isQueryStudiosUnsupported(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode/100 == 5 {
		return true
	}
	return strings.Contains(apiErr.Message, "queryStudios") ||
		strings.Contains(apiErr.Message, "StudioQueryInput")
}

// GetSite returns one site by provider id.
func (c *Client) GetSite(ctx context.Context, stashID string) (*core.SiteMeta, error) {
	id := strings.TrimSpace(stashID)
	if id == "" {
		return nil, fmt.Errorf("stashbox: get site: blank id: %w", ErrNotFound)
	}

	var resp struct {
		FindStudio *siteResult `json:"findStudio"`
	}
	if err := c.query(ctx, opFindSite, findSiteQuery, map[string]any{"id": id}, &resp); err != nil {
		return nil, err
	}
	// stash-box answers an unknown id with a null field and no errors array, so
	// "not found" has to be recognised here rather than in query. Callers get
	// the same ErrNotFound they would from a 404.
	if resp.FindStudio == nil {
		return nil, fmt.Errorf("stashbox: site %s: %w", id, ErrNotFound)
	}

	s := siteMeta(*resp.FindStudio)
	return &s, nil
}

// SiteWebURL is where a human can read about this site on the endpoint's own
// website: the destination behind the provider id on a site's page.
//
// It is derived rather than stored because it is a fact about the endpoint, not
// about the site: re-pointing Caravan at a different box has to move the links
// with it, and a URL saved next to each site would not move.
//
// Every stash-box files a studio under /studios/{id}. TPDB, which is not a
// stash-box behind the protocol, files the same record under /sites/{id}: the
// same exception the REST index is, and it lives beside it in tpdb.go.
//
// An unknown endpoint or a blank id has no page, and says so with "" rather
// than by guessing.
func SiteWebURL(endpoint, stashID string) string {
	stashID = strings.TrimSpace(stashID)
	if stashID == "" {
		return ""
	}
	if isTPDBEndpoint(endpoint) {
		return tpdbSiteWebURL(stashID)
	}

	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/studios/" + stashID}).String()
}

// siteMeta converts a stash-box studio into the provider-side domain type.
func siteMeta(r siteResult) core.SiteMeta {
	m := core.SiteMeta{
		StashID:  r.ID,
		Name:     r.Name,
		Aliases:  r.Aliases,
		URL:      firstURL(r.URLs),
		ImageURL: coverURL(r.Images),
	}
	if r.Parent != nil {
		m.ParentStashID = r.Parent.ID
		m.ParentName = r.Parent.Name
	}
	return m
}
