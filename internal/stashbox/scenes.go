package stashbox

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// GraphQL operation names for the scene half of the protocol.
const (
	opSearchScenes = "SearchScenes"
	opFindScene    = "FindScene"
)

// sceneFields is the selection set shared by scene search and scene lookup.
//
// The scene's own `urls`/`images` are selected the same narrow way sites are
// (see siteFields), and the studio is one level deep: a scene needs the site it
// belongs to, not that site's whole record — GetSite is how a site is fetched.
const sceneFields = `
    id
    title
    details
    date
    code
    duration
    urls { url }
    images { url width height }
    studio { id name }
    performers { as performer { id name } }
`

const searchScenesQuery = `query ` + opSearchScenes + `($input: SceneQueryInput!) {
  queryScenes(input: $input) {
    count
    scenes {` + sceneFields + `}
  }
}`

const findSceneQuery = `query ` + opFindScene + `($id: ID!) {
  findScene(id: $id) {` + sceneFields + `}
}`

// sceneResult is the stash-box Scene shape shared by search results and
// lookups. Every nullable field decodes to its zero value, which is exactly
// what SceneMeta documents an absent field as.
type sceneResult struct {
	ID       string        `json:"id"`
	Title    string        `json:"title"`
	Details  string        `json:"details"`
	Date     string        `json:"date"`
	Code     string        `json:"code"`
	Duration int           `json:"duration"`
	URLs     []urlResult   `json:"urls"`
	Images   []imageResult `json:"images"`
	Studio   *struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"studio"`
	Performers []struct {
		As        string `json:"as"`
		Performer struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"performer"`
	} `json:"performers"`
}

// SearchScenes returns one page of scenes matching q.
//
// Results are sorted by release date, newest first: a site's scenes become the
// episodes of a series whose seasons are release years, so date order is the
// order the library wants and relevance order would only have to be re-sorted.
func (c *Client) SearchScenes(ctx context.Context, q core.SceneQuery) (*core.ScenePage, error) {
	// On TPDB the GraphQL road does not exist: queryScenes is a stub whose
	// scenes are always null, so listing goes through the REST dialect
	// unconditionally rather than probe-and-fall-back. See tpdb.go.
	if c.restScenes != "" {
		return c.searchScenesByREST(ctx, q)
	}

	page, perPage := clampPaging(q.Page, q.PerPage)

	input, err := sceneQueryInput(q, page, perPage)
	if err != nil {
		return nil, err
	}

	var resp struct {
		QueryScenes struct {
			Count  int           `json:"count"`
			Scenes []sceneResult `json:"scenes"`
		} `json:"queryScenes"`
	}
	if err := c.query(ctx, opSearchScenes, searchScenesQuery, map[string]any{"input": input}, &resp); err != nil {
		return nil, err
	}

	out := &core.ScenePage{
		Page:    page,
		PerPage: perPage,
		Total:   resp.QueryScenes.Count,
		Scenes:  make([]core.SceneMeta, 0, len(resp.QueryScenes.Scenes)),
	}
	for _, s := range resp.QueryScenes.Scenes {
		out.Scenes = append(out.Scenes, sceneMeta(s))
	}
	return out, nil
}

// SceneFilterSupport reports which scene filters this endpoint can express, so
// the filter rail can leave out a control the answer would only refuse (PLAN
// phase 12, acceptance criterion 1). It is core.SceneFilterReporter.
//
// The dialect decides it and nothing else, the same single fact SearchScenes
// branches on: the TPDB REST index serves every one of these, and the generic
// stash-box GraphQL scene query serves none of them (see sceneQueryInput for
// each refusal, and TestSceneFilterSupportAgreesWithWhatTheQueryRefuses for the
// proof that these two lists cannot drift apart).
func (c *Client) SceneFilterSupport() core.SceneFilterSupport {
	if c.restScenes != "" {
		return core.EverySceneFilter()
	}
	// Everything false. Spelled as the zero value rather than field by field
	// because the honest summary of this dialect is "none of them", and a list
	// of `false`s invites somebody to flip one without changing the query.
	return core.SceneFilterSupport{}
}

// sceneQueryInput builds stash-box's SceneQueryInput from a SceneQuery, and
// REFUSES — with a *core.SceneFilterUnsupportedError — anything this protocol
// has no field for.
//
// The refusal is the whole point. stash-box's scene query answers text,
// studios, performers, tags and an exact date; it has no release year, no
// runtime and no way to widen a studio to its network. Sending a query that
// silently omitted one of those would answer a wider question than the caller
// asked, which on this surface means putting scenes on screen that the filter
// existed to keep off it. TPDB, which is the default endpoint, serves all of
// them through the REST dialect in tpdb.go; this path is what a StashDB or
// FansDB install gets, and it says what it cannot do.
func sceneQueryInput(q core.SceneQuery, page, perPage int) (map[string]any, error) {
	sort, err := sceneSortEnum(q.Sort)
	if err != nil {
		return nil, err
	}
	direction := "DESC"
	if q.Order == core.OrderAsc {
		direction = "ASC"
	}
	input := map[string]any{
		"page":      page,
		"per_page":  perPage,
		"sort":      sort,
		"direction": direction,
	}

	if text := strings.TrimSpace(q.Text); text != "" {
		input["text"] = text
	}
	if site := strings.TrimSpace(q.SiteStashID); site != "" {
		// The widening operator has no equivalent here: a stash-box studio
		// filter is the set of ids you name, and the parent chain is not one
		// of them.
		if q.SiteScope != "" && q.SiteScope != core.SceneSiteOnly {
			return nil, &core.SceneFilterUnsupportedError{Filter: "a widened site scope"}
		}
		// MultiIDCriterionInput. INCLUDES rather than EQUALS: EQUALS asks for a
		// scene whose studio set is exactly this one, which is not what "scenes
		// from this site" means on an endpoint that models sub-studios.
		input["studios"] = map[string]any{
			"value":    []string{site},
			"modifier": "INCLUDES",
		}
	}

	performers, err := sceneCriterion("performers", q.Performers, q.PerformersAll)
	if err != nil {
		return nil, err
	}
	if performers != nil {
		input["performers"] = performers
	}
	tags, err := sceneCriterion("tags", q.Tags, q.TagsAll)
	if err != nil {
		return nil, err
	}
	if tags != nil {
		input["tags"] = tags
	}

	if q.Year > 0 {
		return nil, &core.SceneFilterUnsupportedError{Filter: "release year"}
	}
	if !q.Date.IsZero() {
		// `date` is an exact day and carries no operator, so the four
		// comparisons have nowhere to go.
		if q.DateOp != "" && q.DateOp != core.SceneDateOn {
			return nil, &core.SceneFilterUnsupportedError{Filter: "a date comparison"}
		}
		input["date"] = q.Date.Format(sceneDateLayout)
	}
	if q.Duration > 0 {
		return nil, &core.SceneFilterUnsupportedError{Filter: "duration"}
	}
	return input, nil
}

// sceneDateLayout is how stash-box spells a date in a query, the same way it
// spells one in an answer (see parseDate).
const sceneDateLayout = "2006-01-02"

// sceneSortEnum maps Caravan's sort vocabulary onto SceneSortEnum. Duration
// and relevance are not orderings this protocol offers, so they are refused
// rather than quietly answered in date order.
func sceneSortEnum(sort core.SceneSort) (string, error) {
	switch sort {
	case "", core.SceneSortReleased:
		return "DATE", nil
	case core.SceneSortCreated:
		return "CREATED_AT", nil
	case core.SceneSortUpdated:
		return "UPDATED_AT", nil
	}
	return "", &core.SceneFilterUnsupportedError{Filter: "that ordering"}
}

// sceneCriterion builds a MultiIDCriterionInput for a performer or tag filter,
// or nil when there is nothing to filter on.
//
// INCLUDES is "carries all of these" — the same reading the studio filter
// documents — so the ALL half of the any/all switch is exactly this modifier
// and the ANY half has no spelling at all. With a single id the two questions
// are identical, which is why one chip works either way and two do not.
func sceneCriterion(what string, refs []core.SceneFilterRef, all bool) (map[string]any, error) {
	if len(refs) == 0 {
		return nil, nil
	}
	if !all && len(refs) > 1 {
		return nil, &core.SceneFilterUnsupportedError{Filter: "any-of " + what}
	}
	value := make([]string, 0, len(refs))
	for _, ref := range refs {
		// A ref carrying only a numeric id came from TPDB's typeahead — a
		// filter URL copied between installs. See tpdbRefFilter for the
		// mirror image, for why the id is not in the message, and for why a
		// caller-side filter problem is unsupported-filter rather than an
		// upstream failure.
		if strings.TrimSpace(ref.StashID) == "" {
			return nil, &core.SceneFilterUnsupportedError{Filter: what + " by numeric id"}
		}
		value = append(value, ref.StashID)
	}
	return map[string]any{"value": value, "modifier": "INCLUDES"}, nil
}

// GetScene returns full details for one scene by provider id.
func (c *Client) GetScene(ctx context.Context, stashID string) (*core.SceneMeta, error) {
	id := strings.TrimSpace(stashID)
	if id == "" {
		return nil, fmt.Errorf("stashbox: get scene: blank id: %w", ErrNotFound)
	}

	var resp struct {
		FindScene *sceneResult `json:"findScene"`
	}
	if err := c.query(ctx, opFindScene, findSceneQuery, map[string]any{"id": id}, &resp); err != nil {
		return nil, err
	}
	// See GetSite: an unknown id is a null field, not an error.
	if resp.FindScene == nil {
		return nil, fmt.Errorf("stashbox: scene %s: %w", id, ErrNotFound)
	}

	s := sceneMeta(*resp.FindScene)
	return &s, nil
}

// clampPaging turns a caller's paging request into values the endpoint accepts.
// It is forgiving rather than strict — a zero Page is "the first page", not a
// bad request — and returns what it used so ScenePage can report it.
func clampPaging(page, perPage int) (int, int) {
	if page < 1 {
		page = 1
	}
	switch {
	case perPage < 1:
		perPage = defaultPerPage
	case perPage > maxPerPage:
		perPage = maxPerPage
	}
	return page, perPage
}

// sceneMeta converts a stash-box scene into the provider-side domain type.
func sceneMeta(r sceneResult) core.SceneMeta {
	m := core.SceneMeta{
		StashID:  r.ID,
		Title:    r.Title,
		Overview: r.Details,
		Date:     parseDate(r.Date),
		Code:     r.Code,
		Duration: r.Duration,
		URL:      firstURL(r.URLs),
		ImageURL: coverURL(r.Images),
	}
	if r.Studio != nil {
		m.SiteStashID = r.Studio.ID
		m.SiteName = r.Studio.Name
	}
	for _, p := range r.Performers {
		// A credit with no performer behind it is a broken record, not a
		// nameless performer: skipping it keeps a bad row out of the episode's
		// metadata instead of writing an empty name into it.
		if p.Performer.ID == "" && p.Performer.Name == "" {
			continue
		}
		m.Performers = append(m.Performers, core.ScenePerformer{
			StashID: p.Performer.ID,
			Name:    p.Performer.Name,
			As:      p.As,
		})
	}
	return m
}

// SceneWebURL is where a human can read about this scene on the endpoint's own
// website — the destination behind a scene's title on a site's page. It is
// derived rather than stored for the reason SiteWebURL is: the page moves when
// the endpoint setting does.
//
// Every stash-box files a scene under /scenes/{id}, and TPDB happens to agree —
// but its configured endpoint is the api. host, which serves JSON, so the web
// host still has to be special-cased.
func SceneWebURL(endpoint, stashID string) string {
	stashID = strings.TrimSpace(stashID)
	if stashID == "" {
		return ""
	}
	if isTPDBEndpoint(endpoint) {
		return "https://" + tpdbHost + "/scenes/" + stashID
	}

	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		return ""
	}
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/scenes/" + stashID}).String()
}
