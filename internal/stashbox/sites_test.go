package stashbox

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/stashbox/stashboxtest"
)

func TestSearchSites(t *testing.T) {
	c, s := newStub(t, map[string][]stashboxtest.Response{
		opSearchSites: {okFixture(t, "query_studios.json")},
	})

	got, err := c.SearchSites(context.Background(), "  tushy  ")
	if err != nil {
		t.Fatalf("SearchSites: %v", err)
	}

	want := []core.SiteMeta{
		{
			StashID:       "f1f1f1f1-1111-4111-8111-111111111111",
			Name:          "Tushy",
			Aliases:       []string{"TUSHY", "Tushy.com"},
			ParentStashID: "aaaaaaaa-2222-4222-8222-222222222222",
			ParentName:    "Vixen Media Group",
			URL:           "https://www.tushy.com",
			// The 1000px logo, not the 200px thumbnail beside it.
			ImageURL: "https://cdn.example.test/tushy-logo.jpg",
		},
		{
			// Present but empty everywhere: a bare site is not an error.
			StashID: "f2f2f2f2-3333-4333-8333-333333333333",
			Name:    "Tushy Raw",
			Aliases: []string{},
		},
		{
			// Nulls decode the same way absent lists do.
			StashID: "f3f3f3f3-4444-4444-8444-444444444444",
			Name:    "Deeper",
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("SearchSites:\n got %+v\nwant %+v", got, want)
	}

	req := s.Requests()[0]
	input, ok := req.Variables["input"].(map[string]any)
	if !ok {
		t.Fatalf("variables.input = %v, want an object", req.Variables["input"])
	}
	// `names` rather than `name`: it matches aliases too, which is what a user
	// typing the site string off a release name needs.
	if input["names"] != "tushy" {
		t.Errorf("input.names = %v, want the trimmed query", input["names"])
	}
	if input["page"] != float64(1) {
		t.Errorf("input.page = %v, want 1", input["page"])
	}
	if input["per_page"] != float64(defaultPerPage) {
		t.Errorf("input.per_page = %v, want %d", input["per_page"], defaultPerPage)
	}
}

func TestSearchSitesBlankQueryOmitsTheNameFilter(t *testing.T) {
	// The add-a-site screen renders before anything is typed; a blank query is
	// "the first page", not a filter on the empty string, which some endpoints
	// match against nothing.
	c, s := newStub(t, map[string][]stashboxtest.Response{
		opSearchSites: {okFixture(t, "query_studios.json")},
	})

	if _, err := c.SearchSites(context.Background(), "   "); err != nil {
		t.Fatalf("SearchSites: %v", err)
	}

	input := s.Requests()[0].Variables["input"].(map[string]any)
	if _, present := input["names"]; present {
		t.Errorf("input.names = %v, want it omitted for a blank query", input["names"])
	}
}

func TestSearchSitesSelectsTheNarrowFieldSet(t *testing.T) {
	// The selection set is the compatibility surface: every field asked for is
	// a field a thinner dialect can reject. `type`/`site` on a URL is the known
	// split — older boxes have one, newer ones the other — so neither is asked
	// for.
	c, s := newStub(t, map[string][]stashboxtest.Response{
		opSearchSites: {okFixture(t, "query_studios.json")},
	})

	if _, err := c.SearchSites(context.Background(), "tushy"); err != nil {
		t.Fatalf("SearchSites: %v", err)
	}

	query := s.Requests()[0].Query
	if !strings.Contains(query, "urls { url }") {
		t.Errorf("query does not select urls narrowly: %s", query)
	}
	for _, banned := range []string{"child_studios", "is_favorite", "site {"} {
		if strings.Contains(query, banned) {
			t.Errorf("query selects %q, which not every stash-box dialect serves: %s", banned, query)
		}
	}
}

func TestGetSite(t *testing.T) {
	c, s := newStub(t, map[string][]stashboxtest.Response{
		opFindSite: {okFixture(t, "find_studio.json")},
	})

	got, err := c.GetSite(context.Background(), "f1f1f1f1-1111-4111-8111-111111111111")
	if err != nil {
		t.Fatalf("GetSite: %v", err)
	}

	want := core.SiteMeta{
		StashID:       "f1f1f1f1-1111-4111-8111-111111111111",
		Name:          "Tushy",
		Aliases:       []string{"TUSHY", "Tushy.com"},
		ParentStashID: "aaaaaaaa-2222-4222-8222-222222222222",
		ParentName:    "Vixen Media Group",
		URL:           "https://www.tushy.com",
		ImageURL:      "https://cdn.example.test/tushy-logo.jpg",
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("GetSite:\n got %+v\nwant %+v", *got, want)
	}

	if id := s.Requests()[0].Variables["id"]; id != "f1f1f1f1-1111-4111-8111-111111111111" {
		t.Errorf("variables.id = %v, want the requested site id", id)
	}
}

func TestGetSiteNotFound(t *testing.T) {
	// stash-box answers an unknown id with a null field and no errors array.
	// Callers must see the same ErrNotFound a 404 would produce.
	c, _ := newStub(t, map[string][]stashboxtest.Response{
		opFindSite: {okFixture(t, "find_studio_null.json")},
	})

	got, err := c.GetSite(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got != nil {
		t.Errorf("site = %+v, want nil alongside the error", got)
	}
}

func TestGetSiteBlankIDIsNotFoundWithoutTraffic(t *testing.T) {
	// A blank id cannot match anything, and the adult module's whole promise is
	// that it makes no request it does not have to.
	c, s := newStub(t, map[string][]stashboxtest.Response{})

	if _, err := c.GetSite(context.Background(), "  "); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if n := s.Count(); n != 0 {
		t.Errorf("requests = %d, want 0: a blank id must not reach the endpoint", n)
	}
}

// Ids from testdata/search_scene_studios.json. Named here because the whole
// point of the scene-derived search is which studio ends up where.
const (
	idBrazzers       = "bbbbbbbb-1111-4111-8111-111111111111"
	idBrazzersExxtra = "eeeeeeee-2222-4222-8222-222222222222"
	idBrazzersLive   = "11111111-3333-4333-8333-333333333333"
	idRealityKings   = "44444444-4444-4444-8444-444444444444"
)

// Ids from the ranking fixtures.
const (
	idDeepNine  = "99999999-9999-4999-8999-999999999999"
	idKinkVault = "cccccccc-1111-4111-8111-111111111111"
	idDeeper    = "dddddddd-2222-4222-8222-222222222222"
)

// newTPDBStub returns a client pointed at a fake endpoint with no queryStudios,
// which is what TPDB — the default endpoint — actually is.
func newTPDBStub(t *testing.T, ops map[string][]stashboxtest.Response) (*Client, *stashboxtest.Server) {
	t.Helper()

	srv := stashboxtest.New(stashboxtest.Options{Operations: ops, WithoutQueryStudios: true})
	t.Cleanup(srv.Close)

	c := New(testAPIKey, srv.URL(), srv.Client())
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c, srv
}

// siteReply is a findStudio answer for one site. The fallback looks a candidate
// up per id, so the interesting part of each reply is only which site it is —
// and its aliases, which are the names only the full record carries.
func siteReply(id, name string, aliases ...string) stashboxtest.Response {
	encoded := []byte("[]")
	if len(aliases) > 0 {
		var err error
		if encoded, err = json.Marshal(aliases); err != nil {
			panic(err)
		}
	}
	return stashboxtest.Data([]byte(fmt.Sprintf(
		`{"findStudio":{"id":%q,"name":%q,"aliases":%s,"urls":[],"images":[],"parent":null}}`,
		id, name, encoded)))
}

// siteNames is the readable form of a search result for assertions.
func siteNames(sites []core.SiteMeta) []string {
	out := make([]string, 0, len(sites))
	for _, s := range sites {
		out = append(out, s.Name)
	}
	return out
}

// requestedIDs is the id variable of every request for op, in order.
func requestedIDs(reqs []stashboxtest.Request, op string) []string {
	out := []string{}
	for _, r := range reqs {
		if r.OperationName != op {
			continue
		}
		id, _ := r.Variables["id"].(string)
		out = append(out, id)
	}
	return out
}

func TestSearchSitesFallsBackToScenesWhenQueryStudiosIsMissing(t *testing.T) {
	// TPDB implements no queryStudios and answers it with a bare 500, which is
	// the 502 the "add a site" screen was showing. The sites are still findable
	// through the scenes that name them.
	c, s := newTPDBStub(t, map[string][]stashboxtest.Response{
		opSearchSitesByScene: {okFixture(t, "search_scene_studios.json")},
		opFindSite: {
			siteReply(idBrazzers, "Brazzers"),
			siteReply(idBrazzersExxtra, "Brazzers Exxtra"),
			// Brazzers Live is unreadable. One bad candidate must not empty a
			// search that found three good ones.
			okFixture(t, "find_studio_null.json"),
			siteReply(idRealityKings, "Reality Kings"),
		},
	})

	got, err := c.SearchSites(context.Background(), "  brazzers  ")
	if err != nil {
		t.Fatalf("SearchSites: %v", err)
	}

	want := []string{"Brazzers", "Brazzers Exxtra", "Reality Kings"}
	if !reflect.DeepEqual(siteNames(got), want) {
		t.Errorf("sites = %v, want %v", siteNames(got), want)
	}

	// The network the sub-studios hang off is a candidate in its own right, and
	// outranks them: it is what a user typing "brazzers" means. The two studios
	// on one scene each are deduped and ordered by how often they appeared.
	wantIDs := []string{idBrazzers, idBrazzersExxtra, idBrazzersLive, idRealityKings}
	if gotIDs := requestedIDs(s.Requests(), opFindSite); !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("looked up %v, want %v", gotIDs, wantIDs)
	}

	var search stashboxtest.Request
	for _, r := range s.Requests() {
		if r.OperationName == opSearchSitesByScene {
			search = r
		}
	}
	if search.Variables["term"] != "brazzers" {
		t.Errorf("variables.term = %v, want the trimmed query", search.Variables["term"])
	}
	// Without the parent selection the network above a sub-studio is invisible,
	// and "brazzers" can never offer Brazzers.
	if !strings.Contains(search.Query, "parent { id name }") {
		t.Errorf("query does not select the parent network: %s", search.Query)
	}
}

// The relevance bug: a studio with five scenes but a name nobody typed used to
// bury the one whose name is exactly what was typed. Frequency is a tiebreak,
// not the question — the queryStudios path this stands in for ranks by name.
func TestSearchSitesFallbackRanksNameMatchesAboveBusierStudios(t *testing.T) {
	c, s := newTPDBStub(t, map[string][]stashboxtest.Response{
		opSearchSitesByScene: {okFixture(t, "search_scene_studios_ranking.json")},
		opFindSite: {
			siteReply(idBrazzers, "Brazzers"),
			siteReply(idDeepNine, "Deep Nine"),
		},
	})

	got, err := c.SearchSites(context.Background(), "br")
	if err != nil {
		t.Fatalf("SearchSites: %v", err)
	}

	// The request order is the load-bearing assertion: the fake answers a queue
	// rather than routing on id, so only the log can tell which site the ranking
	// actually put first.
	wantIDs := []string{idBrazzers, idDeepNine}
	if gotIDs := requestedIDs(s.Requests(), opFindSite); !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Errorf("looked up %v, want the name match first: %v", gotIDs, wantIDs)
	}
	if want := []string{"Brazzers", "Deep Nine"}; !reflect.DeepEqual(siteNames(got), want) {
		t.Errorf("sites = %v, want %v", siteNames(got), want)
	}
}

// The aliases only arrive with the full record, which is why the shortlist is
// ranked twice. A site whose alias is what was typed has to climb once that is
// known — a release names a site by whichever alias its packager saw.
func TestSearchSitesFallbackPromotesAnAliasMatchOnceResolved(t *testing.T) {
	c, s := newTPDBStub(t, map[string][]stashboxtest.Response{
		opSearchSitesByScene: {okFixture(t, "search_scene_studios_alias.json")},
		opFindSite: {
			// Looked up first, on scene frequency, and matching nothing.
			siteReply(idKinkVault, "Kink Vault"),
			// Looked up second; only its alias answers the query.
			siteReply(idDeeper, "Deeper", "VX Prime"),
		},
	})

	got, err := c.SearchSites(context.Background(), "vx")
	if err != nil {
		t.Fatalf("SearchSites: %v", err)
	}

	// Fetched in frequency order...
	wantIDs := []string{idKinkVault, idDeeper}
	if gotIDs := requestedIDs(s.Requests(), opFindSite); !reflect.DeepEqual(gotIDs, wantIDs) {
		t.Fatalf("looked up %v, want %v", gotIDs, wantIDs)
	}
	// ...and returned in name order, which is the whole point of the second pass.
	if want := []string{"Deeper", "Kink Vault"}; !reflect.DeepEqual(siteNames(got), want) {
		t.Errorf("sites = %v, want the alias match first: %v", siteNames(got), want)
	}
}

func TestSearchSitesFallbackWithBlankQueryUsesRecentScenes(t *testing.T) {
	// searchScene needs a term, and the add-a-site screen renders before
	// anything is typed. The newest scenes are the fallback's empty state.
	c, s := newTPDBStub(t, map[string][]stashboxtest.Response{
		opRecentSitesByScene: {okFixture(t, "query_scenes_studios.json")},
		opFindSite: {
			siteReply("aaaaaaaa-2222-4222-8222-222222222222", "Vixen Media Group"),
			siteReply("f1f1f1f1-1111-4111-8111-111111111111", "Tushy"),
			siteReply("f3f3f3f3-4444-4444-8444-444444444444", "Deeper"),
		},
	})

	got, err := c.SearchSites(context.Background(), "   ")
	if err != nil {
		t.Fatalf("SearchSites: %v", err)
	}

	want := []string{"Vixen Media Group", "Tushy", "Deeper"}
	if !reflect.DeepEqual(siteNames(got), want) {
		t.Errorf("sites = %v, want %v", siteNames(got), want)
	}

	var recent stashboxtest.Request
	for _, r := range s.Requests() {
		if r.OperationName == opRecentSitesByScene {
			recent = r
		}
	}
	input, ok := recent.Variables["input"].(map[string]any)
	if !ok {
		t.Fatalf("variables.input = %v, want an object", recent.Variables["input"])
	}
	if input["page"] != float64(1) || input["sort"] != "DATE" || input["direction"] != "DESC" {
		t.Errorf("input = %v, want page 1 sorted DATE/DESC", input)
	}
	if _, present := input["text"]; present {
		t.Errorf("input.text = %v, want it omitted for a blank query", input["text"])
	}
}

func TestSearchSitesRemembersTheEndpointHasNoQueryStudios(t *testing.T) {
	// A search box searches per keystroke. One doomed queryStudios per keystroke
	// is a request that can never succeed, sent forever.
	c, s := newTPDBStub(t, map[string][]stashboxtest.Response{
		opSearchSitesByScene: {okFixture(t, "search_scene_studios.json")},
		opFindSite:           {siteReply(idBrazzers, "Brazzers")},
	})

	if _, err := c.SearchSites(context.Background(), "brazzers"); err != nil {
		t.Fatalf("first SearchSites: %v", err)
	}
	s.Reset()

	got, err := c.SearchSites(context.Background(), "brazzers")
	if err != nil {
		t.Fatalf("second SearchSites: %v", err)
	}
	if len(got) == 0 {
		t.Error("second search returned nothing; the fallback must still run")
	}
	for _, r := range s.Requests() {
		if strings.Contains(r.Query, "queryStudios") {
			t.Errorf("second search asked for queryStudios again: %s", r.Query)
		}
	}
}

func TestSearchSitesFallsBackOnAValidationErrorNamingTheField(t *testing.T) {
	// A stricter endpoint rejects the document instead of failing at runtime.
	// Same missing field, same fallback.
	c, _ := newStub(t, map[string][]stashboxtest.Response{
		opSearchSites: {stashboxtest.GraphQLError(
			`Cannot query field "queryStudios" on type "Query".`, "GRAPHQL_VALIDATION_FAILED")},
		opSearchSitesByScene: {okFixture(t, "search_scene_studios.json")},
		opFindSite:           {siteReply(idBrazzers, "Brazzers")},
	})

	got, err := c.SearchSites(context.Background(), "brazzers")
	if err != nil {
		t.Fatalf("SearchSites: %v", err)
	}
	if len(got) == 0 {
		t.Error("sites = none, want the scene-derived results")
	}
}

func TestSearchSitesDoesNotFallBackOnARealFailure(t *testing.T) {
	// A bad API key is fixable and must be reported. Answering it with worse
	// results would hide it, and would search a second time to do so.
	c, s := newStub(t, map[string][]stashboxtest.Response{
		opSearchSites: {errFixture(t, 401, "error_unauthorized.json")},
	})

	if _, err := c.SearchSites(context.Background(), "tushy"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("err = %v, want ErrUnauthorized", err)
	}
	if n := s.Count(); n != 1 {
		t.Errorf("requests = %d, want 1: a fixable failure must not trigger the fallback", n)
	}
}

func TestSearchSitesKeepsUsingQueryStudiosWhereItWorks(t *testing.T) {
	// StashDB implements queryStudios and it is the better query. The fallback
	// must never displace it.
	c, s := newStub(t, map[string][]stashboxtest.Response{
		opSearchSites: {okFixture(t, "query_studios.json")},
	})

	for i := 0; i < 2; i++ {
		if _, err := c.SearchSites(context.Background(), "tushy"); err != nil {
			t.Fatalf("SearchSites: %v", err)
		}
	}

	reqs := s.Requests()
	if len(reqs) != 2 {
		t.Fatalf("requests = %d, want 2 (one per search, no scene-derived traffic)", len(reqs))
	}
	for _, r := range reqs {
		if r.OperationName != opSearchSites {
			t.Errorf("operation = %q, want only %q", r.OperationName, opSearchSites)
		}
	}
}

func TestQueryStudiosServerErrorIsAnAPIErrorNotADecodeFailure(t *testing.T) {
	// TPDB's 500 carries the plain text "Server Error", not JSON. Reported as a
	// decode failure it names nothing useful; reported as an APIError it both
	// names the operation and is what the fallback recognises.
	c, _ := newTPDBStub(t, nil)

	_, err := c.searchSitesByStudios(context.Background(), "tushy")
	if err == nil {
		t.Fatal("searchSitesByStudios: want error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As(*APIError) = false; err = %v", err)
	}
	if apiErr.Operation != opSearchSites {
		t.Errorf("Operation = %q, want %q", apiErr.Operation, opSearchSites)
	}
	if apiErr.StatusCode != 500 {
		t.Errorf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if strings.Contains(err.Error(), "decode") {
		t.Errorf("err = %v, want the endpoint's failure rather than a decode failure", err)
	}
	if !isQueryStudiosUnsupported(err) {
		t.Error("isQueryStudiosUnsupported = false, want true: this is the shape TPDB sends")
	}
}

func TestRankSceneStudios(t *testing.T) {
	scene := func(id, name, parentID, parentName string) sceneStudioResult {
		var r sceneStudioResult
		r.Studio = new(struct {
			ID     string `json:"id"`
			Name   string `json:"name"`
			Parent *struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"parent"`
		})
		r.Studio.ID = id
		r.Studio.Name = name
		if parentID != "" {
			r.Studio.Parent = &struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			}{ID: parentID, Name: parentName}
		}
		return r
	}
	studio := func(id, parentID string) sceneStudioResult { return scene(id, "", parentID, "") }

	ids := func(cands []siteCandidate) []string {
		out := make([]string, 0, len(cands))
		for _, c := range cands {
			out = append(out, c.id)
		}
		return out
	}

	tests := []struct {
		name   string
		needle string
		scenes []sceneStudioResult
		limit  int
		want   []string
	}{
		{
			name:   "frequency wins when nothing was typed",
			scenes: []sceneStudioResult{studio("a", ""), studio("b", ""), studio("b", "")},
			limit:  10,
			want:   []string{"b", "a"},
		},
		{
			name:   "a network outranks its one sub-studio on the tie",
			scenes: []sceneStudioResult{studio("child", "net")},
			limit:  10,
			want:   []string{"net", "child"},
		},
		{
			name:   "equal candidates keep the endpoint's order",
			scenes: []sceneStudioResult{studio("a", ""), studio("b", "")},
			limit:  10,
			want:   []string{"a", "b"},
		},
		{
			name:   "the shortlist is capped",
			scenes: []sceneStudioResult{studio("a", ""), studio("b", ""), studio("c", "")},
			limit:  2,
			want:   []string{"a", "b"},
		},
		{
			name:   "a scene with no studio contributes nothing",
			scenes: []sceneStudioResult{{}, studio("a", "")},
			limit:  10,
			want:   []string{"a"},
		},
		{
			name:   "nothing to rank",
			scenes: nil,
			limit:  10,
			want:   []string{},
		},
		{
			// The reported bug: five scenes from a studio nobody typed the name
			// of used to bury the one whose name is what was typed.
			name:   "a name match outranks a busier studio nobody named",
			needle: "br",
			scenes: []sceneStudioResult{
				scene("busy", "Deep Nine", "", ""),
				scene("busy", "Deep Nine", "", ""),
				scene("busy", "Deep Nine", "", ""),
				scene("busy", "Deep Nine", "", ""),
				scene("busy", "Deep Nine", "", ""),
				scene("brz", "Brazzers", "", ""),
			},
			limit: 10,
			want:  []string{"brz", "busy"},
		},
		{
			name:   "an exact name beats a prefix of it",
			needle: "brazzers",
			scenes: []sceneStudioResult{
				scene("exxtra", "Brazzers Exxtra", "", ""),
				scene("exxtra", "Brazzers Exxtra", "", ""),
				scene("brz", "Brazzers", "", ""),
			},
			limit: 10,
			want:  []string{"brz", "exxtra"},
		},
		{
			// "ki" is aimed at the word "Kings", not at the middle of "Skinny".
			name:   "a word start beats the inside of a word",
			needle: "ki",
			scenes: []sceneStudioResult{
				scene("skinny", "Skinny", "", ""),
				scene("skinny", "Skinny", "", ""),
				scene("rk", "Reality Kings", "", ""),
			},
			limit: 10,
			want:  []string{"rk", "skinny"},
		},
		{
			name:   "punctuation does not hide a word",
			needle: "li",
			scenes: []sceneStudioResult{
				scene("hyphen", "Brazzers-Live", "", ""),
				scene("none", "Deep Nine", "", ""),
			},
			limit: 10,
			want:  []string{"hyphen", "none"},
		},
		{
			name:   "frequency still orders within one tier",
			needle: "br",
			scenes: []sceneStudioResult{
				scene("one", "Brazzers One", "", ""),
				scene("two", "Brazzers Two", "", ""),
				scene("two", "Brazzers Two", "", ""),
			},
			limit: 10,
			want:  []string{"two", "one"},
		},
		{
			// A scene-title search surfaces the studio that made it. That studio
			// is a real answer, just a worse one than any name match.
			name:   "a studio nobody named is kept, only demoted",
			needle: "br",
			scenes: []sceneStudioResult{
				scene("none", "Deep Nine", "", ""),
				scene("brz", "Brazzers", "", ""),
			},
			limit: 10,
			want:  []string{"brz", "none"},
		},
		{
			name:   "a matching network outranks its non-matching child",
			needle: "br",
			scenes: []sceneStudioResult{scene("child", "Exxtra", "net", "Brazzers")},
			limit:  10,
			want:   []string{"net", "child"},
		},
		{
			name:   "case is not part of the question",
			needle: "br",
			scenes: []sceneStudioResult{
				scene("none", "Deep Nine", "", ""),
				scene("none", "Deep Nine", "", ""),
				scene("brz", "BRAZZERS", "", ""),
			},
			limit: 10,
			want:  []string{"brz", "none"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ids(rankSceneStudios(tt.scenes, tt.needle, tt.limit))
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("rankSceneStudios = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNameAffinity(t *testing.T) {
	tests := []struct {
		name   string
		needle string
		names  []string
		want   siteAffinity
	}{
		{name: "exact", needle: "brazzers", names: []string{"Brazzers"}, want: affinityExact},
		{name: "prefix", needle: "br", names: []string{"Brazzers"}, want: affinityPrefix},
		{name: "word start", needle: "ki", names: []string{"Reality Kings"}, want: affinityWord},
		{name: "inside a word", needle: "ki", names: []string{"Skinny"}, want: affinitySubstring},
		{name: "no match", needle: "br", names: []string{"Deep Nine"}, want: affinityNone},
		{name: "nothing typed has no opinion", needle: "", names: []string{"Brazzers"}, want: affinityNone},
		{
			// The aliases are why the second ranking pass exists: a release
			// names a site by whichever alias its packager saw.
			name:   "the best of several names wins",
			needle: "tu",
			names:  []string{"Vixen Angel", "TUSHY"},
			want:   affinityPrefix,
		},
		{name: "blank names are skipped", needle: "br", names: []string{"", "  "}, want: affinityNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nameAffinity(tt.needle, tt.names...); got != tt.want {
				t.Errorf("nameAffinity(%q, %v) = %d, want %d", tt.needle, tt.names, got, tt.want)
			}
		})
	}
}

func TestSiteWebURL(t *testing.T) {
	const id = "e3b61b3e-1111-4111-8111-111111111111"

	tests := []struct {
		name     string
		endpoint string
		stashID  string
		want     string
	}{
		{
			// TPDB files a site under /sites, not the stash-box /studios.
			name:     "the TPDB preset",
			endpoint: DefaultEndpoint,
			stashID:  id,
			want:     "https://theporndb.net/sites/" + id,
		},
		{
			name:     "blank endpoint is the TPDB preset",
			endpoint: "",
			stashID:  id,
			want:     "https://theporndb.net/sites/" + id,
		},
		{
			name:     "a TPDB subdomain",
			endpoint: "https://api.theporndb.net/graphql",
			stashID:  id,
			want:     "https://theporndb.net/sites/" + id,
		},
		{
			name:     "stashdb keeps the stash-box convention",
			endpoint: "https://stashdb.org/graphql",
			stashID:  id,
			want:     "https://stashdb.org/studios/" + id,
		},
		{
			name:     "fansdb",
			endpoint: "https://fansdb.cc/graphql",
			stashID:  id,
			want:     "https://fansdb.cc/studios/" + id,
		},
		{
			// The web UI is the host, not the /graphql path under it.
			name:     "a self-hosted box keeps its scheme and port",
			endpoint: "http://192.168.1.10:9998/graphql",
			stashID:  id,
			want:     "http://192.168.1.10:9998/studios/" + id,
		},
		{
			name:     "no id means no page",
			endpoint: "https://stashdb.org/graphql",
			stashID:  "   ",
			want:     "",
		},
		{
			name:     "an unparseable endpoint has no page rather than a guessed one",
			endpoint: "://nonsense",
			stashID:  id,
			want:     "",
		},
		{
			name:     "an endpoint with no host has no page",
			endpoint: "/graphql",
			stashID:  id,
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SiteWebURL(tt.endpoint, tt.stashID); got != tt.want {
				t.Errorf("SiteWebURL(%q, %q) = %q, want %q", tt.endpoint, tt.stashID, got, tt.want)
			}
		})
	}
}
