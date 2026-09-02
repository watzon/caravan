package stashbox

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/stashbox/stashboxtest"
)

// The scene filter surface on TPDB's REST dialect.
//
// These assert the EXACT parameter set, not merely that the expected ones are
// present: a filter this client misspells is one TPDB ignores, which answers a
// wider question than the caller asked and puts scenes on screen the filter
// existed to keep off it. An extra parameter fails here for the same reason.

// wantSceneParams asserts a recorded query string is exactly want.
func wantSceneParams(t *testing.T, rawQuery string, want map[string]string) {
	t.Helper()

	got, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("parse query %q: %v", rawQuery, err)
	}
	for key, value := range want {
		if !got.Has(key) {
			t.Errorf("%s is missing (query %q)", key, rawQuery)
			continue
		}
		if g := got.Get(key); g != value {
			t.Errorf("%s = %q, want %q", key, g, value)
		}
	}
	for key := range got {
		if _, ok := want[key]; !ok {
			t.Errorf("unexpected parameter %s=%q (query %q)", key, got.Get(key), rawQuery)
		}
	}
}

// sceneIndexQuery runs one query against a TPDB-shaped client and returns the
// scene index request it produced.
func sceneIndexQuery(t *testing.T, c *Client, srv *stashboxtest.Server, q core.SceneQuery) stashboxtest.Request {
	t.Helper()

	if _, err := c.SearchScenes(context.Background(), q); err != nil {
		t.Fatalf("SearchScenes: %v", err)
	}
	reqs := pathRequests(srv.Requests(), "/scenes")
	if len(reqs) == 0 {
		t.Fatal("no scene index request was made")
	}
	return reqs[len(reqs)-1]
}

func TestSearchScenesOnTPDBSendsEveryFilter(t *testing.T) {
	c, srv := tpdbSceneStub(t, stashboxtest.Options{
		SiteLookup: []stashboxtest.Response{siteLookupResponse()},
		SceneIndex: []stashboxtest.Response{sceneIndexResponse()},
	})

	req := sceneIndexQuery(t, c, srv, core.SceneQuery{
		Text:        " golden hour ",
		SiteStashID: exxtraUUID,
		SiteScope:   core.SceneSiteNetwork,
		Performers: []core.SceneFilterRef{
			{ID: 84060, Name: "Mia Malkova"},
			{ID: 90211, Name: "Riley Reid"},
		},
		PerformersAll: true,
		Tags:          []core.SceneFilterRef{{ID: 70, Name: "Anal"}},
		TagsAll:       true,
		Year:          2024,
		Date:          time.Date(2024, time.March, 2, 0, 0, 0, 0, time.UTC),
		DateOp:        core.SceneDateOnOrAfter,
		Duration:      1800,
		Sort:          core.SceneSortDuration,
		Order:         core.OrderAsc,
		Page:          3,
		PerPage:       40,
	})

	wantSceneParams(t, req.RawQuery, map[string]string{
		"page":              "3",
		"per_page":          "40",
		"orderBy":           "duration_asc",
		"q":                 "golden hour",
		"site_id":           "94",
		"site_operation":    "Site/Parent/Network",
		"performers[84060]": "Mia Malkova",
		"performers[90211]": "Riley Reid",
		"performer_and":     "true",
		"tags[70]":          "Anal",
		"tag_and":           "true",
		"year":              "2024",
		"date":              "2024-03-02",
		"date_operation":    ">=",
		"duration":          "1800",
	})

	// And the map encoding itself, on the wire rather than after decoding:
	// the scalar spelling (performers=84060) is answered with a 422, so the
	// bracketed key is load-bearing.
	if !strings.Contains(req.RawQuery, "performers%5B84060%5D=Mia+Malkova") {
		t.Errorf("raw query %q does not carry the performers[84060]=Mia Malkova map form", req.RawQuery)
	}
	if !strings.Contains(req.RawQuery, "tags%5B70%5D=Anal") {
		t.Errorf("raw query %q does not carry the tags[70]=Anal map form", req.RawQuery)
	}
}

// An unfiltered query is the paging and ordering defaults and nothing else: a
// parameter sent on every request is one that cannot be proven to come from a
// filter.
func TestSearchScenesOnTPDBWithNoFiltersSendsOnlyTheDefaults(t *testing.T) {
	c, srv := tpdbSceneStub(t, stashboxtest.Options{
		SceneIndex: []stashboxtest.Response{sceneIndexResponse()},
	})

	req := sceneIndexQuery(t, c, srv, core.SceneQuery{})
	wantSceneParams(t, req.RawQuery, map[string]string{
		"page":     "1",
		"per_page": "25",
		"orderBy":  "recently_released",
	})
}

// The widening ladder. Each step keeps the narrower one, and the narrow scope
// sends nothing at all: Site is the endpoint's own default for a site_id, so a
// catalogue walk's query stays what it was.
func TestSearchScenesOnTPDBWidensTheSiteScope(t *testing.T) {
	tests := []struct {
		name  string
		scope core.SceneSiteScope
		want  string
	}{
		{name: "unset sends nothing", scope: "", want: ""},
		{name: "site sends nothing", scope: core.SceneSiteOnly, want: ""},
		{name: "parent", scope: core.SceneSiteParent, want: "Site/Parent"},
		{name: "network", scope: core.SceneSiteNetwork, want: "Site/Parent/Network"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, srv := tpdbSceneStub(t, stashboxtest.Options{
				SiteLookup: []stashboxtest.Response{siteLookupResponse()},
				SceneIndex: []stashboxtest.Response{sceneIndexResponse()},
			})

			req := sceneIndexQuery(t, c, srv, core.SceneQuery{SiteStashID: exxtraUUID, SiteScope: tc.scope})
			if got := valueOf(t, req.RawQuery, "site_operation"); got != tc.want {
				t.Errorf("site_operation = %q, want %q (query %q)", got, tc.want, req.RawQuery)
			}
			if got := valueOf(t, req.RawQuery, "site_id"); got != "94" {
				t.Errorf("site_id = %q, want 94: widening must not replace the site", got)
			}
		})
	}
}

// TPDB bakes the direction into one orderBy enum, so every Caravan
// sort-and-direction pair has to land on the right member of it.
func TestSearchScenesOnTPDBMapsEveryOrdering(t *testing.T) {
	tests := []struct {
		sort  core.SceneSort
		order core.DiscoverOrder
		want  string
	}{
		{sort: "", order: "", want: "recently_released"},
		{sort: core.SceneSortReleased, order: core.OrderDesc, want: "recently_released"},
		{sort: core.SceneSortReleased, order: core.OrderAsc, want: "former_released"},
		{sort: core.SceneSortCreated, order: core.OrderDesc, want: "recently_created"},
		{sort: core.SceneSortCreated, order: core.OrderAsc, want: "former_created"},
		{sort: core.SceneSortUpdated, order: core.OrderDesc, want: "recently_updated"},
		{sort: core.SceneSortUpdated, order: core.OrderAsc, want: "former_updated"},
		{sort: core.SceneSortDuration, order: core.OrderDesc, want: "duration_desc"},
		{sort: core.SceneSortDuration, order: core.OrderAsc, want: "duration_asc"},
		// Relevance has no direction on this endpoint, so Order is ignored
		// rather than turned into a second enum member that does not exist.
		{sort: core.SceneSortRelevance, order: core.OrderDesc, want: "most_relevant"},
		{sort: core.SceneSortRelevance, order: core.OrderAsc, want: "most_relevant"},
	}
	for _, tc := range tests {
		t.Run(string(tc.sort)+"/"+string(tc.order), func(t *testing.T) {
			c, srv := tpdbSceneStub(t, stashboxtest.Options{
				SceneIndex: []stashboxtest.Response{sceneIndexResponse()},
			})
			req := sceneIndexQuery(t, c, srv, core.SceneQuery{Sort: tc.sort, Order: tc.order})
			if got := valueOf(t, req.RawQuery, "orderBy"); got != tc.want {
				t.Errorf("orderBy = %q, want %q", got, tc.want)
			}
		})
	}
}

// The any/all switch is the endpoint's own performer_and / tag_and, sent only
// for the ALL reading: its absence is "any".
func TestSearchScenesOnTPDBSendsTheAnyAllSwitchOnlyForAll(t *testing.T) {
	c, srv := tpdbSceneStub(t, stashboxtest.Options{
		SceneIndex: []stashboxtest.Response{sceneIndexResponse()},
	})

	req := sceneIndexQuery(t, c, srv, core.SceneQuery{
		Performers: []core.SceneFilterRef{{ID: 84060, Name: "Mia Malkova"}, {ID: 90211, Name: "Riley Reid"}},
		Tags:       []core.SceneFilterRef{{ID: 70, Name: "Anal"}, {ID: 112, Name: "Outdoor"}},
	})
	if got := valueOf(t, req.RawQuery, "performer_and"); got != "" {
		t.Errorf("performer_and = %q for an any-of query, want unset", got)
	}
	if got := valueOf(t, req.RawQuery, "tag_and"); got != "" {
		t.Errorf("tag_and = %q for an any-of query, want unset", got)
	}
	// Both ids still travel: "any of these two" is still a two-id filter.
	if got := valueOf(t, req.RawQuery, "performers[90211]"); got != "Riley Reid" {
		t.Errorf("performers[90211] = %q, want the second performer", got)
	}
}

// A filter ref carrying only a stash-box uuid is a URL copied from another
// install. The REST index has no uuid filter, so the honest answer is a failure
// rather than a page of everything: and the request is never made.
func TestSearchScenesOnTPDBRefusesARefWithNoNumericID(t *testing.T) {
	for _, tc := range []struct {
		name  string
		query core.SceneQuery
	}{
		{
			name:  "performer",
			query: core.SceneQuery{Performers: []core.SceneFilterRef{{StashID: "b1b1b1b1-1111-4111-8111-bbbbbbbbbbbb", Name: "Ava Rivers"}}},
		},
		{
			name:  "tag",
			query: core.SceneQuery{Tags: []core.SceneFilterRef{{StashID: "3b5c4a11-9d21-4a0e-8f31-1c2d3e4f5061", Name: "Anal"}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, srv := tpdbSceneStub(t, stashboxtest.Options{
				SceneIndex: []stashboxtest.Response{sceneIndexResponse()},
			})

			_, err := c.SearchScenes(context.Background(), tc.query)
			if err == nil {
				t.Fatal("SearchScenes = nil error, want a refusal")
			}
			// A caller-side filter problem, so it takes the same 400 path as
			// every other refusal rather than the 502 that logs an upstream
			// failure and offers a Retry that can never succeed.
			if !errors.Is(err, core.ErrSceneFilterUnsupported) {
				t.Fatalf("err = %v, want ErrSceneFilterUnsupported", err)
			}
			var unsupported *core.SceneFilterUnsupportedError
			if !errors.As(err, &unsupported) || unsupported.Filter == "" {
				t.Errorf("err = %v, want a named filter", err)
			}
			if reqs := pathRequests(srv.Requests(), "/scenes"); len(reqs) != 0 {
				t.Errorf("scene index requests = %d, want 0: the query was never valid", len(reqs))
			}
			// The refusal names the filter, never the value asked for.
			if strings.Contains(err.Error(), "Ava Rivers") || strings.Contains(err.Error(), "Anal") {
				t.Errorf("the error quotes the filter value: %v", err)
			}
		})
	}
}

// Every date comparison has a member of TPDB's date_operation enum, and an
// unset one is the exact day.
func TestSearchScenesOnTPDBMapsEveryDateComparison(t *testing.T) {
	day := time.Date(2024, time.March, 2, 0, 0, 0, 0, time.UTC)
	tests := []struct {
		op   core.SceneDateOp
		want string
	}{
		{op: "", want: "="},
		{op: core.SceneDateOn, want: "="},
		{op: core.SceneDateBefore, want: "<"},
		{op: core.SceneDateOnOrBefore, want: "<="},
		{op: core.SceneDateAfter, want: ">"},
		{op: core.SceneDateOnOrAfter, want: ">="},
	}
	for _, tc := range tests {
		t.Run(string(tc.op), func(t *testing.T) {
			c, srv := tpdbSceneStub(t, stashboxtest.Options{
				SceneIndex: []stashboxtest.Response{sceneIndexResponse()},
			})
			req := sceneIndexQuery(t, c, srv, core.SceneQuery{Date: day, DateOp: tc.op})
			if got := valueOf(t, req.RawQuery, "date"); got != "2024-03-02" {
				t.Errorf("date = %q, want 2024-03-02", got)
			}
			if got := valueOf(t, req.RawQuery, "date_operation"); got != tc.want {
				t.Errorf("date_operation = %q, want %q", got, tc.want)
			}
		})
	}
}

// The GraphQL dialect: what it can express, and what it refuses.

func TestSearchScenesGraphQLSendsThePerformerAndTagCriteria(t *testing.T) {
	c, srv := newStub(t, map[string][]stashboxtest.Response{
		opSearchScenes: {stashboxtest.Data([]byte(`{"queryScenes":{"count":0,"scenes":[]}}`))},
	})

	_, err := c.SearchScenes(context.Background(), core.SceneQuery{
		Performers:    []core.SceneFilterRef{{StashID: "b1b1b1b1-1111-4111-8111-bbbbbbbbbbbb", Name: "Ava Rivers"}},
		PerformersAll: true,
		Tags:          []core.SceneFilterRef{{StashID: "3b5c4a11-9d21-4a0e-8f31-1c2d3e4f5061", Name: "Anal"}},
		TagsAll:       true,
		Date:          time.Date(2023, time.November, 4, 0, 0, 0, 0, time.UTC),
		Sort:          core.SceneSortUpdated,
		Order:         core.OrderAsc,
	})
	if err != nil {
		t.Fatalf("SearchScenes: %v", err)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	input, _ := reqs[0].Variables["input"].(map[string]any)
	performers, _ := input["performers"].(map[string]any)
	if performers == nil || performers["modifier"] != "INCLUDES" {
		t.Fatalf("performers = %#v, want a MultiIDCriterionInput with INCLUDES", input["performers"])
	}
	if values, _ := performers["value"].([]any); len(values) != 1 || values[0] != "b1b1b1b1-1111-4111-8111-bbbbbbbbbbbb" {
		t.Errorf("performers.value = %#v, want the stash-box id", performers["value"])
	}
	tags, _ := input["tags"].(map[string]any)
	if tags == nil || tags["modifier"] != "INCLUDES" {
		t.Fatalf("tags = %#v, want a MultiIDCriterionInput with INCLUDES", input["tags"])
	}
	if input["date"] != "2023-11-04" {
		t.Errorf("date = %#v, want the exact day", input["date"])
	}
	if input["sort"] != "UPDATED_AT" || input["direction"] != "ASC" {
		t.Errorf("sort/direction = %#v/%#v, want UPDATED_AT/ASC", input["sort"], input["direction"])
	}
}

// The seam this dialect sits on the wrong side of. Every one of these is a
// control TPDB serves and stash-box has no field for, and each is refused by
// name rather than dropped: a dropped filter answers the wider question.
func TestSearchScenesGraphQLRefusesFiltersItCannotExpress(t *testing.T) {
	site := "f1f1f1f1-1111-4111-8111-111111111111"
	tests := []struct {
		name  string
		query core.SceneQuery
	}{
		{name: "a widened site scope", query: core.SceneQuery{SiteStashID: site, SiteScope: core.SceneSiteNetwork}},
		{name: "release year", query: core.SceneQuery{Year: 2024}},
		{name: "duration", query: core.SceneQuery{Duration: 1800}},
		{
			name:  "a date comparison",
			query: core.SceneQuery{Date: time.Date(2024, time.March, 2, 0, 0, 0, 0, time.UTC), DateOp: core.SceneDateAfter},
		},
		{name: "sorting by duration", query: core.SceneQuery{Sort: core.SceneSortDuration}},
		{name: "sorting by relevance", query: core.SceneQuery{Sort: core.SceneSortRelevance}},
		{
			name: "any-of performers",
			query: core.SceneQuery{Performers: []core.SceneFilterRef{
				{StashID: "b1b1b1b1-1111-4111-8111-bbbbbbbbbbbb"},
				{StashID: "b2b2b2b2-2222-4222-8222-cccccccccccc"},
			}},
		},
		{
			name: "any-of tags",
			query: core.SceneQuery{Tags: []core.SceneFilterRef{
				{StashID: "3b5c4a11-9d21-4a0e-8f31-1c2d3e4f5061"},
				{StashID: "9a1b2c3d-4e5f-4061-8172-2b3c4d5e6f71"},
			}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, srv := newStub(t, map[string][]stashboxtest.Response{
				opSearchScenes: {stashboxtest.Data([]byte(`{"queryScenes":{"count":0,"scenes":[]}}`))},
			})

			_, err := c.SearchScenes(context.Background(), tc.query)
			if !errors.Is(err, core.ErrSceneFilterUnsupported) {
				t.Fatalf("err = %v, want ErrSceneFilterUnsupported", err)
			}
			var unsupported *core.SceneFilterUnsupportedError
			if !errors.As(err, &unsupported) || unsupported.Filter == "" {
				t.Errorf("err = %v, want a named filter", err)
			}
			if n := srv.Count(); n != 0 {
				t.Errorf("requests = %d, want 0: a query that cannot be expressed is never sent", n)
			}
		})
	}
}

// The rail asks SceneFilterSupport which pills to draw, and the answer is only
// worth having if it agrees with what SearchScenes would actually do. So this
// asks BOTH questions of the same client and insists they match: a filter
// advertised as available must not be refused, and one advertised as absent
// must be. Adding a refusal to sceneQueryInput without withholding the
// capability, or the reverse, fails here rather than on somebody's screen.
func TestSceneFilterSupportAgreesWithWhatTheQueryRefuses(t *testing.T) {
	site := "f1f1f1f1-1111-4111-8111-111111111111"
	// Every capability, paired with a query that exercises exactly it.
	probes := []struct {
		name      string
		supported func(core.SceneFilterSupport) bool
		query     core.SceneQuery
	}{
		{
			name:      "year",
			supported: func(s core.SceneFilterSupport) bool { return s.Year },
			query:     core.SceneQuery{Year: 2024},
		},
		{
			name:      "duration",
			supported: func(s core.SceneFilterSupport) bool { return s.Duration },
			query:     core.SceneQuery{Duration: 1800},
		},
		{
			name:      "site scope",
			supported: func(s core.SceneFilterSupport) bool { return s.SiteScope },
			query:     core.SceneQuery{SiteStashID: site, SiteScope: core.SceneSiteNetwork},
		},
		{
			name:      "date comparison",
			supported: func(s core.SceneFilterSupport) bool { return s.DateOp },
			query: core.SceneQuery{
				Date:   time.Date(2024, time.March, 2, 0, 0, 0, 0, time.UTC),
				DateOp: core.SceneDateAfter,
			},
		},
		{
			name:      "sort by duration",
			supported: func(s core.SceneFilterSupport) bool { return s.SortDuration },
			query:     core.SceneQuery{Sort: core.SceneSortDuration},
		},
		{
			name:      "sort by relevance",
			supported: func(s core.SceneFilterSupport) bool { return s.SortRelevance },
			query:     core.SceneQuery{Sort: core.SceneSortRelevance},
		},
		{
			name:      "any-of",
			supported: func(s core.SceneFilterSupport) bool { return s.AnyOf },
			query: core.SceneQuery{Tags: []core.SceneFilterRef{
				{ID: 70, StashID: "3b5c4a11-9d21-4a0e-8f31-1c2d3e4f5061"},
				{ID: 71, StashID: "9a1b2c3d-4e5f-4061-8172-2b3c4d5e6f71"},
			}},
		},
	}

	dialects := []struct {
		name string
		open func(t *testing.T) *Client
	}{
		{
			name: "stash-box GraphQL",
			open: func(t *testing.T) *Client {
				c, _ := newStub(t, map[string][]stashboxtest.Response{
					opSearchScenes: {stashboxtest.Data([]byte(`{"queryScenes":{"count":0,"scenes":[]}}`))},
				})
				return c
			},
		},
		{
			name: "TPDB REST",
			open: func(t *testing.T) *Client {
				c, _ := tpdbSceneStub(t, stashboxtest.Options{
					SiteLookup: []stashboxtest.Response{siteLookupResponse()},
					SceneIndex: []stashboxtest.Response{
						sceneIndexResponse(), sceneIndexResponse(), sceneIndexResponse(),
					},
				})
				return c
			},
		},
	}

	for _, dialect := range dialects {
		t.Run(dialect.name, func(t *testing.T) {
			for _, probe := range probes {
				t.Run(probe.name, func(t *testing.T) {
					c := dialect.open(t)
					want := probe.supported(c.SceneFilterSupport())

					_, err := c.SearchScenes(context.Background(), probe.query)
					refused := errors.Is(err, core.ErrSceneFilterUnsupported)
					if want && refused {
						t.Fatalf("advertised as available but refused: %v", err)
					}
					if !want && !refused {
						t.Fatalf("advertised as absent but not refused: err = %v", err)
					}
				})
			}
		})
	}
}

// One id is the same question either way, so a single chip works on a dialect
// that only knows the ALL reading.
func TestSearchScenesGraphQLAcceptsASingleAnyOfID(t *testing.T) {
	c, _ := newStub(t, map[string][]stashboxtest.Response{
		opSearchScenes: {stashboxtest.Data([]byte(`{"queryScenes":{"count":0,"scenes":[]}}`))},
	})

	_, err := c.SearchScenes(context.Background(), core.SceneQuery{
		Performers: []core.SceneFilterRef{{StashID: "b1b1b1b1-1111-4111-8111-bbbbbbbbbbbb"}},
	})
	if err != nil {
		t.Fatalf("SearchScenes: %v", err)
	}
}

// The mirror of the TPDB refusal: a numeric id means nothing here.
func TestSearchScenesGraphQLRefusesARefWithNoStashID(t *testing.T) {
	c, srv := newStub(t, map[string][]stashboxtest.Response{
		opSearchScenes: {stashboxtest.Data([]byte(`{"queryScenes":{"count":0,"scenes":[]}}`))},
	})

	_, err := c.SearchScenes(context.Background(), core.SceneQuery{
		Performers: []core.SceneFilterRef{{ID: 84060, Name: "Mia Malkova"}},
	})
	if err == nil {
		t.Fatal("SearchScenes = nil error, want a refusal")
	}
	// The same 400 the TPDB mirror gives, for the same reason.
	if !errors.Is(err, core.ErrSceneFilterUnsupported) {
		t.Fatalf("err = %v, want ErrSceneFilterUnsupported", err)
	}
	var unsupported *core.SceneFilterUnsupportedError
	if !errors.As(err, &unsupported) || unsupported.Filter == "" {
		t.Errorf("err = %v, want a named filter", err)
	}
	if strings.Contains(err.Error(), "Mia Malkova") {
		t.Errorf("the error quotes the filter value: %v", err)
	}
	if n := srv.Count(); n != 0 {
		t.Errorf("requests = %d, want 0", n)
	}
}
