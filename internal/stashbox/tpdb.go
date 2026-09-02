package stashbox

// TPDB's REST API: the one place in this package that speaks something other
// than stash-box GraphQL.
//
// It is a deliberate, fenced breach of the "one protocol, endpoints differ only
// by URL" rule the rest of the package keeps, and it is here rather than spread
// through sites.go and scenes.go so the whole of the exception fits in one
// file.
//
// Why it exists, in two parts.
//
// Sites: TPDB implements no queryStudios (see isQueryStudiosUnsupported), so
// site search there falls back to deriving studios from scene search. That
// fallback answers the wrong question: searchScene matches scene *text*, so
// searching "br" returns whichever scenes mention it and then offers their
// studios, Manyvids, Nebraska Coeds, KinkBomb, while Brazzers, a site whose
// NAME is what was typed, never appears at all. No amount of re-ranking fixes a
// candidate pool that does not contain the answer.
//
// Scenes: TPDB's queryScenes is a stub. Its `scenes` field is always null and
// its `count` merely echoes per_page, a garbage studio filter "matches" as many
// rows as were asked for, and its SceneQueryInput disagrees with stash-box
// anyway (`studios` is a String, not a MultiIDCriterionInput). So a site's
// catalogue walk, the refresh that follows it, and the newest-scenes shelf have
// no GraphQL road on TPDB at all; the REST scene index is the road. All
// verified live, 2026-08-03.
//
// What makes the breach safe: TPDB's REST `uuid` fields ARE the GraphQL ids,
// site uuid == studio id and scene id == stash-box scene id, verified in both
// directions, so anything picked from REST remains addressable by every GraphQL
// call that follows (findStudio, findScene), and nothing downstream learns that
// this dialect exists.
//
// The fence: this file is reached only when the configured endpoint is TPDB's.
// For site search, only for a non-blank query and only after queryStudios has
// been tried, with any failure falling through to the scene-derived path. For
// scene listing there is nothing to fall through to, the GraphQL side is a
// stub, so a REST failure there is a real error and surfaces as one.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// The dialect table: one host and two indexes. The scenes entry is here because
// the alternative was a provider whose site pages are permanently empty, since
// TPDB's queryScenes is a stub (see the file comment). An entry that is not "the
// REST twin of a broken GraphQL query" is the signal to redesign this.
const (
	// tpdbHost is the host whose GraphQL endpoint means "this is TPDB". It
	// matches the host itself and anything under it (api.theporndb.net).
	tpdbHost = "theporndb.net"
	// tpdbSitesURL is TPDB's REST site index, searched with ?q=. It is a
	// different host from the GraphQL endpoint, which is why it is spelled out
	// rather than derived. /sites/{uuid} on the same index resolves one site,
	// numeric id included.
	tpdbSitesURL = "https://api.theporndb.net/sites"
	// tpdbScenesURL is TPDB's REST scene index: ?site_id= for a site's
	// catalogue, ?q= for text, paged with page/per_page and ordered with
	// orderBy, and filtered by the performer and tag maps this file builds.
	tpdbScenesURL = "https://api.theporndb.net/scenes"
	// tpdbPerformersURL and tpdbTagsURL are the REST indexes the scene filter
	// rail's typeaheads read. They are here for the same reason the scene index
	// is: the GraphQL side has no usable twin, and the numeric ids the scene
	// index filters on are served nowhere else.
	tpdbPerformersURL = "https://api.theporndb.net/performers"
	tpdbTagsURL       = "https://api.theporndb.net/tags"

	// tpdbDateLayout is how the REST scene index spells a release date.
	tpdbDateLayout = "2006-01-02"
)

// REST operation names for errors and the fake endpoint's request log, the same
// way the GraphQL operation names work.
const (
	opSearchSitesREST      = "SearchSitesREST"
	opSearchScenesREST     = "SearchScenesREST"
	opResolveSiteREST      = "ResolveSiteREST"
	opSearchPerformersREST = "SearchPerformersREST"
	opSearchTagsREST       = "SearchTagsREST"
)

// tpdbSitesURLFor returns the REST site index to use for a GraphQL endpoint, or
// "" for an endpoint that is not TPDB's: which is every other stash-box, and
// the reason this is a lookup rather than a flag.
//
// A blank endpoint is TPDB: DefaultEndpoint is TPDB, so "a key and nothing
// else" gets the dialect it is actually talking to.
func tpdbSitesURLFor(endpoint string) string {
	if !isTPDBEndpoint(endpoint) {
		return ""
	}
	return tpdbSitesURL
}

// isTPDBEndpoint reports whether a GraphQL endpoint is TPDB's. It is the only
// place the host is matched, so everything dialect-shaped agrees on what "this
// is TPDB" means.
func isTPDBEndpoint(endpoint string) bool {
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return false
	}
	host := strings.ToLower(u.Hostname())
	return host == tpdbHost || strings.HasSuffix(host, "."+tpdbHost)
}

// tpdbSiteWebURL is a site's page on TPDB's own website. TPDB files sites under
// /sites/{uuid}; the stash-box convention is /studios/{uuid}, and TPDB does not
// serve that path.
func tpdbSiteWebURL(stashID string) string {
	return "https://" + tpdbHost + "/sites/" + stashID
}

// tpdbSiteRef is a site's parent or network as the REST index embeds it. Both
// are null on a site that has neither.
type tpdbSiteRef struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// tpdbSiteRow is one row of the REST site index.
//
// Only the fields the picker shows are read. `id` is deliberately absent: it is
// a TPDB-local integer, and letting it anywhere near a StashID would produce a
// site nothing else in Caravan can look up.
type tpdbSiteRow struct {
	UUID      string       `json:"uuid"`
	Name      string       `json:"name"`
	ShortName string       `json:"short_name"`
	URL       string       `json:"url"`
	Logo      string       `json:"logo"`
	Poster    string       `json:"poster"`
	Parent    *tpdbSiteRef `json:"parent"`
	Network   *tpdbSiteRef `json:"network"`
}

// searchSitesByREST searches TPDB's site index by name.
//
// It makes exactly one request. There is deliberately no findStudio per row:
// the row carries everything the picker draws, and the one site the user
// actually chooses is resolved in full by AddSite. A search box must not cost
// twenty-five round trips.
func (c *Client) searchSitesByREST(ctx context.Context, q string) ([]core.SiteMeta, error) {
	params := url.Values{}
	params.Set("q", q)
	params.Set("per_page", strconv.Itoa(defaultPerPage))

	var body struct {
		Data []tpdbSiteRow `json:"data"`
	}
	if err := c.restGet(ctx, opSearchSitesREST, c.restSites, params, &body); err != nil {
		return nil, err
	}
	return rankTPDBSites(body.Data, strings.ToLower(strings.TrimSpace(q))), nil
}

// restGet performs one GET against a TPDB REST index and decodes its JSON
// answer, with the same credential, size-limit and error taxonomy rules the
// GraphQL half keeps, so callers branch with errors.Is either way.
func (c *Client) restGet(ctx context.Context, op, base string, params url.Values, into any) error {
	u, err := url.Parse(base)
	if err != nil {
		return fmt.Errorf("stashbox: %s: %w", op, err)
	}
	if params != nil {
		merged := u.Query()
		for k, vs := range params {
			merged[k] = vs
		}
		u.RawQuery = merged.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("stashbox: request %s: %w", op, err)
	}
	req.Header.Set("Accept", "application/json")
	// Bearer only: the REST API reads the same key the GraphQL side does, but
	// the ApiKey header means nothing to it.
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		// See do(): unwrap so the message is about the failure rather than the
		// address.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return fmt.Errorf("stashbox: get %s: %w", op, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("stashbox: read %s: %w", op, err)
	}
	// A non-2xx is the endpoint failing whatever its body says, and it goes
	// through the same APIError the GraphQL half uses so callers keep branching
	// with errors.Is.
	if resp.StatusCode/100 != 2 {
		return newAPIError(op, resp.StatusCode, nil)
	}

	if err := json.Unmarshal(raw, into); err != nil {
		return fmt.Errorf("stashbox: decode %s: %w", op, err)
	}
	return nil
}

// rankTPDBSites converts and re-orders the index's answer.
//
// The index's own order is weak on short queries, "br" returns a page of
// name-matches with no particular preference among them, so the same affinity
// ranking the scene-derived path uses is applied on top. Rows carry no scene
// counts, so within a tier the index's order is kept.
func rankTPDBSites(rows []tpdbSiteRow, needle string) []core.SiteMeta {
	cands := make([]siteCandidate, 0, len(rows))
	metas := make(map[string]core.SiteMeta, len(rows))

	for i, row := range rows {
		// A row with no uuid is not addressable by anything downstream, so it
		// is not a candidate: offering it would produce an add that fails.
		if row.UUID == "" {
			continue
		}
		if _, seen := metas[row.UUID]; seen {
			continue
		}
		meta := tpdbSiteMeta(row)
		metas[row.UUID] = meta
		cands = append(cands, siteCandidate{
			id:       meta.StashID,
			name:     meta.Name,
			affinity: nameAffinity(needle, append([]string{meta.Name}, meta.Aliases...)...),
			first:    i,
		})
	}

	sortCandidates(cands)
	if len(cands) > defaultPerPage {
		cands = cands[:defaultPerPage]
	}
	out := make([]core.SiteMeta, 0, len(cands))
	for _, cand := range cands {
		out = append(out, metas[cand.id])
	}
	return out
}

// tpdbSiteMeta converts one REST row into the provider-side domain type.
func tpdbSiteMeta(row tpdbSiteRow) core.SiteMeta {
	m := core.SiteMeta{
		StashID: row.UUID,
		Name:    row.Name,
		URL:     row.URL,
		// The logo is the site's mark and the poster is artwork behind it; the
		// picker draws a small square, so the logo wins where there is one.
		ImageURL: firstNonEmpty(row.Logo, row.Poster),
	}
	// short_name is the provider's own other name for the site ("brazzers" for
	// "Brazzers Network"), so it is an alias: which also lets the ranking match
	// what a user types. It is dropped when it says nothing new.
	if short := strings.TrimSpace(row.ShortName); short != "" && !strings.EqualFold(short, row.Name) {
		m.Aliases = []string{short}
	}
	// TPDB models both a parent site and a network above it. SiteMeta shows one
	// line, and the parent is the nearer of the two.
	if ref := firstRef(row.Parent, row.Network); ref != nil {
		m.ParentStashID = ref.UUID
		m.ParentName = ref.Name
	}
	return m
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

// firstRef returns the first reference that names something. A null parent with
// a null network is a site that stands alone.
func firstRef(refs ...*tpdbSiteRef) *tpdbSiteRef {
	for _, ref := range refs {
		if ref != nil && (ref.UUID != "" || ref.Name != "") {
			return ref
		}
	}
	return nil
}

// tpdbSceneRow is one row of the REST scene index. Only what SceneMeta carries
// is read; the REST `id` here IS the stash-box scene id (unlike a site row,
// whose stash-box id is its `uuid`): verified live via findScene.
type tpdbSceneRow struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Date        string `json:"date"`
	Duration    int    `json:"duration"`
	URL         string `json:"url"`
	Poster      string `json:"poster"`
	Image       string `json:"image"`
	Site        *struct {
		UUID string `json:"uuid"`
		Name string `json:"name"`
	} `json:"site"`
	Performers []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"performers"`
}

// searchScenesByREST lists scenes from TPDB's REST index. It answers the same
// contract SearchScenes documents: date order, newest first, honest paging.
//
// A network's site_id matches only scenes filed directly under the network, not
// its child sites' (verified live: Brazzers itself carries 272, its children
// thousands; TPDB's network_id parameter ignores its value and returns
// everything, so it is no help). Adding the specific site is how a child's
// catalogue arrives: the same one-site-one-series shape the library models
// everywhere else.
func (c *Client) searchScenesByREST(ctx context.Context, q core.SceneQuery) (*core.ScenePage, error) {
	page, perPage := clampPaging(q.Page, q.PerPage)

	params := url.Values{}
	params.Set("page", strconv.Itoa(page))
	params.Set("per_page", strconv.Itoa(perPage))
	params.Set("orderBy", tpdbOrderBy(q.Sort, q.Order))
	if text := strings.TrimSpace(q.Text); text != "" {
		params.Set("q", text)
	}
	if site := strings.TrimSpace(q.SiteStashID); site != "" {
		id, err := c.tpdbSiteID(ctx, site)
		if err != nil {
			return nil, err
		}
		params.Set("site_id", strconv.Itoa(id))
		// The narrow scope is TPDB's own default for a site_id, so it is left
		// unsent: a catalogue walk's query is then exactly what it always was,
		// and only a caller who asked to widen pays for the parameter.
		if op := tpdbSiteOperation(q.SiteScope); op != "" {
			params.Set("site_operation", op)
		}
	}
	// A scope without a site widens nothing, and there is no parameter to send
	// for it: the API refuses that combination before it gets here.
	if err := tpdbRefFilter(params, "performers", "performer_and", q.Performers, q.PerformersAll); err != nil {
		return nil, err
	}
	if err := tpdbRefFilter(params, "tags", "tag_and", q.Tags, q.TagsAll); err != nil {
		return nil, err
	}
	if q.Year > 0 {
		params.Set("year", strconv.Itoa(q.Year))
	}
	if !q.Date.IsZero() {
		params.Set("date", q.Date.Format(tpdbDateLayout))
		params.Set("date_operation", tpdbDateOperation(q.DateOp))
	}
	if q.Duration > 0 {
		params.Set("duration", strconv.Itoa(q.Duration))
	}

	var body struct {
		Data []tpdbSceneRow `json:"data"`
		Meta struct {
			Total int `json:"total"`
		} `json:"meta"`
	}
	if err := c.restGet(ctx, opSearchScenesREST, c.restScenes, params, &body); err != nil {
		return nil, err
	}

	out := &core.ScenePage{
		Page:    page,
		PerPage: perPage,
		Total:   body.Meta.Total,
		Scenes:  make([]core.SceneMeta, 0, len(body.Data)),
	}
	for _, row := range body.Data {
		out.Scenes = append(out.Scenes, tpdbSceneMeta(row))
	}
	return out, nil
}

// tpdbOrderBy maps Caravan's sort-and-direction pair onto TPDB's single orderBy
// enum, which bakes the direction into the value.
//
// The verified vocabulary is
// ["duration_asc","duration_desc","former_created","former_released",
// "former_updated","most_relevant","recently_created","recently_released",
// "recently_updated"]: "former_" is oldest-first and "recently_" is
// newest-first. most_relevant has no direction, so Order is not consulted for
// it; core.SceneSortRelevance documents that.
//
// An unknown sort falls back to the default rather than failing: the API
// validates the vocabulary before a query is built, so reaching the fallback
// means a new SceneSort was added without a mapping, and a default order is a
// better answer than a 502 on every scene query.
func tpdbOrderBy(sort core.SceneSort, order core.DiscoverOrder) string {
	asc := order == core.OrderAsc
	switch sort {
	case core.SceneSortCreated:
		if asc {
			return "former_created"
		}
		return "recently_created"
	case core.SceneSortUpdated:
		if asc {
			return "former_updated"
		}
		return "recently_updated"
	case core.SceneSortDuration:
		if asc {
			return "duration_asc"
		}
		return "duration_desc"
	case core.SceneSortRelevance:
		return "most_relevant"
	}
	if asc {
		return "former_released"
	}
	return "recently_released"
}

// tpdbSiteOperation maps the widening ladder onto TPDB's site_operation enum,
// whose verified values are ["Network","Parent","Site","Site/Network",
// "Site/Parent","Site/Parent/Network"].
//
// Each widening step KEEPS the narrower one, "Site/Parent" is the site's own
// scenes as well as its parent's, because a filter that swapped one for the
// other would make "widen" drop rows.
//
// The narrow scope returns "", meaning "send nothing": Site is the endpoint's
// own default for a site_id.
func tpdbSiteOperation(scope core.SceneSiteScope) string {
	switch scope {
	case core.SceneSiteParent:
		return "Site/Parent"
	case core.SceneSiteNetwork:
		return "Site/Parent/Network"
	}
	return ""
}

// tpdbDateOperation maps the date comparison onto TPDB's date_operation enum,
// whose verified values are ["=",">",">=","<","<="]. An unset comparison is
// the exact day, which is what core.SceneDateOn documents.
func tpdbDateOperation(op core.SceneDateOp) string {
	switch op {
	case core.SceneDateBefore:
		return "<"
	case core.SceneDateOnOrBefore:
		return "<="
	case core.SceneDateAfter:
		return ">"
	case core.SceneDateOnOrAfter:
		return ">="
	}
	return "="
}

// tpdbRefFilter writes a performer or tag filter in the wire form TPDB insists
// on: a MAP of numeric id to name, `performers[84060]=Mia Malkova`. The scalar
// spelling (`performers=84060`) is answered with a 422 "must be an array", so
// this shape is load-bearing rather than stylistic.
//
// The name is passed through because the map is what the endpoint reads; only
// the key selects, so an id that names nothing simply matches nothing.
//
// allKey is the endpoint's any/all switch (performer_and, tag_and), sent only
// for the ALL reading: its absence is "any", which is the endpoint's default.
func tpdbRefFilter(params url.Values, key, allKey string, refs []core.SceneFilterRef, all bool) error {
	if len(refs) == 0 {
		return nil
	}
	for _, ref := range refs {
		// A ref carrying only a uuid came from another dialect's typeahead: a
		// filter URL copied from a stash-box install onto a TPDB one. The scene
		// index has no uuid filter, so the honest answer is a failure rather
		// than a page of everything. The id is not named in the message: a
		// filter's value is not for a log.
		//
		// It is a SceneFilterUnsupportedError like every other refusal here:
		// the caller's filter is the problem, so this is the 400 that offers
		// Clear filters, not the 502 that offers a Retry which cannot work.
		if ref.ID <= 0 {
			return &core.SceneFilterUnsupportedError{Filter: key + " by stash-box id"}
		}
		params.Set(key+"["+strconv.FormatInt(ref.ID, 10)+"]", ref.Name)
	}
	if all {
		params.Set(allKey, "true")
	}
	return nil
}

// tpdbPerformerRow is one row of the REST performer index.
//
// The id fields are the opposite way round from a site row and from a tag row,
// which is exactly why they are pinned by a fixture test: `id` is the uuid, the
// same id stash-box GraphQL uses, and `_id` is the numeric id the scene index
// filters on.
type tpdbPerformerRow struct {
	ID        string `json:"id"`
	NumericID int64  `json:"_id"`
	Name      string `json:"name"`
	Image     string `json:"image"`
	Thumbnail string `json:"thumbnail"`
}

// tpdbTagRow is one row of the REST tag index. Here `id` IS the numeric id and
// the uuid has its own field (see tpdbPerformerRow).
type tpdbTagRow struct {
	ID   int64  `json:"id"`
	UUID string `json:"uuid"`
	Name string `json:"name"`
}

// searchPerformersByREST reads TPDB's performer index for a typeahead.
func (c *Client) searchPerformersByREST(ctx context.Context, query string) ([]core.ScenePerformerMeta, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("per_page", strconv.Itoa(defaultPerPage))

	var body struct {
		Data []tpdbPerformerRow `json:"data"`
	}
	if err := c.restGet(ctx, opSearchPerformersREST, c.restPerformers, params, &body); err != nil {
		return nil, err
	}

	out := make([]core.ScenePerformerMeta, 0, len(body.Data))
	for _, row := range body.Data {
		// A row with no numeric id cannot be filtered on here, so offering it
		// would produce a chip that selects nothing.
		if row.NumericID <= 0 {
			continue
		}
		out = append(out, core.ScenePerformerMeta{
			SceneFilterRef: core.SceneFilterRef{ID: row.NumericID, StashID: row.ID, Name: row.Name},
			// The thumbnail is the small square a typeahead row draws; the
			// full image is the fallback.
			ImageURL: firstNonEmpty(row.Thumbnail, row.Image),
		})
	}
	return out, nil
}

// searchTagsByREST reads TPDB's tag index for a typeahead.
func (c *Client) searchTagsByREST(ctx context.Context, query string) ([]core.SceneFilterRef, error) {
	params := url.Values{}
	params.Set("q", query)
	params.Set("per_page", strconv.Itoa(defaultPerPage))

	var body struct {
		Data []tpdbTagRow `json:"data"`
	}
	if err := c.restGet(ctx, opSearchTagsREST, c.restTags, params, &body); err != nil {
		return nil, err
	}

	out := make([]core.SceneFilterRef, 0, len(body.Data))
	for _, row := range body.Data {
		if row.ID <= 0 {
			continue
		}
		out = append(out, core.SceneFilterRef{ID: row.ID, StashID: row.UUID, Name: row.Name})
	}
	return out, nil
}

// tpdbSiteID resolves a site's stash-box uuid to TPDB's numeric site id, which
// is what the REST scene index filters by. The answer is cached for the life of
// the client: a catalogue walk pages one site dozens of times and the mapping
// never changes.
func (c *Client) tpdbSiteID(ctx context.Context, uuid string) (int, error) {
	c.siteIDMu.Lock()
	if id, ok := c.siteIDs[uuid]; ok {
		c.siteIDMu.Unlock()
		return id, nil
	}
	c.siteIDMu.Unlock()

	var body struct {
		Data struct {
			ID int `json:"id"`
		} `json:"data"`
	}
	if err := c.restGet(ctx, opResolveSiteREST, c.restSites+"/"+url.PathEscape(uuid), nil, &body); err != nil {
		return 0, err
	}
	// A 200 with no id is an answer about a site TPDB does not have; calling it
	// ErrNotFound keeps the caller's branching identical to GetSite's.
	if body.Data.ID == 0 {
		return 0, fmt.Errorf("stashbox: %s %s: %w", opResolveSiteREST, uuid, ErrNotFound)
	}

	c.siteIDMu.Lock()
	c.siteIDs[uuid] = body.Data.ID
	c.siteIDMu.Unlock()
	return body.Data.ID, nil
}

// tpdbSceneMeta converts one REST scene row into the provider-side domain type.
func tpdbSceneMeta(row tpdbSceneRow) core.SceneMeta {
	m := core.SceneMeta{
		StashID:  row.ID,
		Title:    row.Title,
		Overview: row.Description,
		Date:     parseDate(row.Date),
		Duration: row.Duration,
		URL:      row.URL,
		// The poster is the scene's own artwork; the raw image is the fallback.
		ImageURL: firstNonEmpty(row.Poster, row.Image),
	}
	if row.Site != nil {
		m.SiteStashID = row.Site.UUID
		m.SiteName = row.Site.Name
	}
	for _, p := range row.Performers {
		// See sceneMeta: a credit with neither id nor name is a broken record,
		// not a nameless performer.
		if p.ID == "" && p.Name == "" {
			continue
		}
		m.Performers = append(m.Performers, core.ScenePerformer{
			StashID: p.ID,
			Name:    p.Name,
		})
	}
	return m
}
