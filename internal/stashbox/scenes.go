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

	input := map[string]any{
		"page":      page,
		"per_page":  perPage,
		"sort":      "DATE",
		"direction": "DESC",
	}
	if text := strings.TrimSpace(q.Text); text != "" {
		input["text"] = text
	}
	if site := strings.TrimSpace(q.SiteStashID); site != "" {
		// MultiIDCriterionInput. INCLUDES rather than EQUALS: EQUALS asks for a
		// scene whose studio set is exactly this one, which is not what "scenes
		// from this site" means on an endpoint that models sub-studios.
		input["studios"] = map[string]any{
			"value":    []string{site},
			"modifier": "INCLUDES",
		}
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
