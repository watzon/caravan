package stashbox

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/stashbox/stashboxtest"
)

// tpdbFacetStub builds a TPDB-shaped client whose REST typeahead indexes point
// at one fake. It is tpdbSceneStub's twin, and it wires the scene indexes too
// so a test can prove a typeahead did NOT walk them.
func tpdbFacetStub(t *testing.T, opts stashboxtest.Options) (*Client, *stashboxtest.Server) {
	t.Helper()

	opts.WithoutQueryStudios = true
	srv := stashboxtest.New(opts)
	t.Cleanup(srv.Close)

	c := New(testAPIKey, srv.URL(), srv.Client())
	c.restSites = srv.URL() + "/sites"
	c.restScenes = srv.URL() + "/scenes"
	c.restPerformers = srv.URL() + "/performers"
	c.restTags = srv.URL() + "/tags"
	return c, srv
}

// The wire format the scene filter rides on: TPDB's performer index answers
// `id` as the UUID and `_id` as the numeric id — the opposite way round from
// its site and tag rows — and the numeric one is what the scene index filters
// by. Getting these two the wrong way round produces a typeahead that looks
// perfect and a filter that matches nothing.
func TestSearchPerformersOnTPDBReadsTheRESTIndex(t *testing.T) {
	c, srv := tpdbFacetStub(t, stashboxtest.Options{
		PerformerIndex: []stashboxtest.Response{okFixture(t, "tpdb_performers.json")},
	})

	got, err := c.SearchPerformers(context.Background(), "  mia  ")
	if err != nil {
		t.Fatalf("SearchPerformers: %v", err)
	}

	want := []core.ScenePerformerMeta{
		{
			SceneFilterRef: core.SceneFilterRef{
				ID:      84060,
				StashID: "60416988-eb25-4517-9914-4aa1915a3e43",
				Name:    "Mia Malkova",
			},
			// The thumbnail wins over the full image: a typeahead row draws a
			// small square.
			ImageURL: "https://cdn.example.test/performers/84060-thumb.jpg",
		},
		{
			SceneFilterRef: core.SceneFilterRef{
				ID:      90211,
				StashID: "8bd0a0e1-2b1c-4f27-9f4a-1f1d9f8f2a11",
				Name:    "Riley Reid",
			},
			ImageURL: "https://cdn.example.test/performers/90211.jpg",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SearchPerformers:\n got %+v\nwant %+v", got, want)
	}

	reqs := pathRequests(srv.Requests(), "/performers")
	if len(reqs) != 1 {
		t.Fatalf("performer index requests = %d, want 1", len(reqs))
	}
	if q := valueOf(t, reqs[0].RawQuery, "q"); q != "mia" {
		t.Errorf("q = %q, want the trimmed query", q)
	}
	if posts := pathRequests(srv.Requests(), "/graphql"); len(posts) != 0 {
		t.Errorf("GraphQL requests = %d, want 0 on TPDB", len(posts))
	}
}

func TestSearchTagsOnTPDBReadsTheRESTIndex(t *testing.T) {
	c, srv := tpdbFacetStub(t, stashboxtest.Options{
		TagIndex: []stashboxtest.Response{okFixture(t, "tpdb_tags.json")},
	})

	got, err := c.SearchTags(context.Background(), "anal")
	if err != nil {
		t.Fatalf("SearchTags: %v", err)
	}

	// A tag row spells its ids the other way round from a performer row: `id`
	// is the numeric one here and the uuid has its own field.
	want := []core.SceneFilterRef{
		{ID: 70, StashID: "3b5c4a11-9d21-4a0e-8f31-1c2d3e4f5061", Name: "Anal"},
		{ID: 112, StashID: "9a1b2c3d-4e5f-4061-8172-2b3c4d5e6f71", Name: "Outdoor"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SearchTags:\n got %+v\nwant %+v", got, want)
	}
	if reqs := pathRequests(srv.Requests(), "/tags"); len(reqs) != 1 {
		t.Fatalf("tag index requests = %d, want 1", len(reqs))
	}
}

// A row the scene index cannot be filtered by is not a candidate: offering it
// would produce a chip that silently matches nothing.
func TestTPDBTypeaheadsDropRowsWithNoNumericID(t *testing.T) {
	c, _ := tpdbFacetStub(t, stashboxtest.Options{
		PerformerIndex: []stashboxtest.Response{okFixture(t, "tpdb_performers.json")},
		TagIndex:       []stashboxtest.Response{okFixture(t, "tpdb_tags.json")},
	})

	performers, err := c.SearchPerformers(context.Background(), "x")
	if err != nil {
		t.Fatalf("SearchPerformers: %v", err)
	}
	for _, p := range performers {
		if p.ID <= 0 {
			t.Errorf("performer %q reached the caller with no numeric id", p.Name)
		}
	}
	tags, err := c.SearchTags(context.Background(), "x")
	if err != nil {
		t.Fatalf("SearchTags: %v", err)
	}
	for _, tag := range tags {
		if tag.ID <= 0 {
			t.Errorf("tag %q reached the caller with no numeric id", tag.Name)
		}
	}
}

// Off TPDB there is no REST side at all, and the typeaheads are GraphQL like
// everything else — the dialect must be unreachable, not merely unused.
func TestSearchPerformersSpeaksGraphQLOffTPDB(t *testing.T) {
	c, srv := newStub(t, map[string][]stashboxtest.Response{
		opSearchPerformers: {okFixture(t, "query_performers.json")},
	})

	got, err := c.SearchPerformers(context.Background(), "ava")
	if err != nil {
		t.Fatalf("SearchPerformers: %v", err)
	}

	want := []core.ScenePerformerMeta{
		{
			SceneFilterRef: core.SceneFilterRef{StashID: "b1b1b1b1-1111-4111-8111-bbbbbbbbbbbb", Name: "Ava Rivers"},
			// coverURL picks the widest image, as it does for a scene.
			ImageURL: "https://cdn.example.test/ava-full.jpg",
		},
		{SceneFilterRef: core.SceneFilterRef{StashID: "b2b2b2b2-2222-4222-8222-cccccccccccc", Name: "Mick Stone"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SearchPerformers:\n got %+v\nwant %+v", got, want)
	}
	// A stash-box performer has no numeric id, so the ref carries only the
	// uuid — the seam the scene filter's two dialects sit either side of.
	for _, p := range got {
		if p.ID != 0 {
			t.Errorf("performer %q carries a numeric id stash-box never served", p.Name)
		}
	}

	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].OperationName != opSearchPerformers {
		t.Fatalf("requests = %+v, want one %s call", reqs, opSearchPerformers)
	}
	input, _ := reqs[0].Variables["input"].(map[string]any)
	if input["names"] != "ava" {
		t.Errorf("input = %#v, want the query under `names` (which matches aliases too)", input)
	}
}

func TestSearchTagsSpeaksGraphQLOffTPDB(t *testing.T) {
	c, srv := newStub(t, map[string][]stashboxtest.Response{
		opSearchTags: {okFixture(t, "query_tags.json")},
	})

	got, err := c.SearchTags(context.Background(), "anal")
	if err != nil {
		t.Fatalf("SearchTags: %v", err)
	}
	want := []core.SceneFilterRef{
		{StashID: "3b5c4a11-9d21-4a0e-8f31-1c2d3e4f5061", Name: "Anal"},
		{StashID: "9a1b2c3d-4e5f-4061-8172-2b3c4d5e6f71", Name: "Outdoor"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SearchTags:\n got %+v\nwant %+v", got, want)
	}

	reqs := srv.Requests()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	input, _ := reqs[0].Variables["input"].(map[string]any)
	if input["name"] != "anal" {
		t.Errorf("input = %#v, want the query under `name`", input)
	}
	if !strings.Contains(reqs[0].Query, "queryTags") {
		t.Errorf("query = %q, want the queryTags document", reqs[0].Query)
	}
}

// Typeaheads never return nil: a client renders a list, and a nil one is an
// empty list that decodes as null.
func TestTypeaheadsReturnEmptySlicesRatherThanNil(t *testing.T) {
	c, _ := newStub(t, map[string][]stashboxtest.Response{
		opSearchPerformers: {stashboxtest.Data([]byte(`{"queryPerformers":{"performers":[]}}`))},
		opSearchTags:       {stashboxtest.Data([]byte(`{"queryTags":{"tags":[]}}`))},
	})

	performers, err := c.SearchPerformers(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("SearchPerformers: %v", err)
	}
	if performers == nil {
		t.Error("SearchPerformers returned nil, want an empty slice")
	}
	tags, err := c.SearchTags(context.Background(), "nothing")
	if err != nil {
		t.Fatalf("SearchTags: %v", err)
	}
	if tags == nil {
		t.Error("SearchTags returned nil, want an empty slice")
	}
}
