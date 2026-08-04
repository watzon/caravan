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

// exxtraUUID/exxtraNumericID mirror the live pair this dialect was verified
// against: the stash-box studio id and TPDB's own integer for the same site.
const (
	exxtraUUID      = "6dd2bb96-8388-479a-93f0-bb3b22eac1d9"
	exxtraNumericID = 94
)

// tpdbSceneStub builds a TPDB-shaped client whose REST scene index, site
// lookup and GraphQL side all point at one fake.
func tpdbSceneStub(t *testing.T, opts stashboxtest.Options) (*Client, *stashboxtest.Server) {
	t.Helper()

	opts.WithoutQueryStudios = true
	srv := stashboxtest.New(opts)
	t.Cleanup(srv.Close)

	c := New(testAPIKey, srv.URL(), srv.Client())
	c.sleep = func(context.Context, time.Duration) error { return nil }
	c.restSites = srv.URL() + "/sites"
	c.restScenes = srv.URL() + "/scenes"
	return c, srv
}

func siteLookupResponse() stashboxtest.Response {
	return stashboxtest.Raw([]byte(`{"data":{"id":94,"uuid":"` + exxtraUUID + `","name":"Brazzers Exxtra"}}`))
}

func sceneIndexResponse() stashboxtest.Response {
	return stashboxtest.Raw([]byte(`{
  "data": [
    {
      "id": "2499acef-1e9d-44f4-b552-3aa8ded1986c",
      "title": "Taking It All In",
      "description": "A scene.",
      "date": "2026-08-03",
      "duration": 2518,
      "url": "https://example.test/scene",
      "poster": "https://thumb.example.test/poster.jpg",
      "image": "https://media.example.test/image.jpg",
      "site": {"id": 94, "uuid": "` + exxtraUUID + `", "name": "Brazzers Exxtra"},
      "performers": [
        {"id": "60416988-eb25-4517-9914-4aa1915a3e43", "name": "Cassie Lenoir"},
        {"id": "", "name": ""}
      ]
    },
    {
      "id": "7d17f11f-ac9d-45ee-b8ea-2d71a5828c3d",
      "title": "Second Scene",
      "date": "2026-07-20",
      "poster": null,
      "image": "https://media.example.test/second.jpg",
      "site": {"id": 94, "uuid": "` + exxtraUUID + `", "name": "Brazzers Exxtra"},
      "performers": []
    }
  ],
  "meta": {"current_page": 1, "per_page": 25, "total": 3950}
}`))
}

// pathRequests returns the requests whose path begins with prefix.
func pathRequests(reqs []stashboxtest.Request, prefix string) []stashboxtest.Request {
	out := []stashboxtest.Request{}
	for _, r := range reqs {
		if strings.HasPrefix(r.Path, prefix) {
			out = append(out, r)
		}
	}
	return out
}

// The regression for the reported bug: a site's catalogue walk on TPDB used to
// POST queryScenes — which failed on the input type, and would have silently
// returned zero scenes once that was "fixed", because TPDB's queryScenes never
// returns scenes at all. On TPDB the walk must be REST, and GraphQL must not
// be consulted for it.
func TestSearchScenesOnTPDBWalksTheRESTSceneIndex(t *testing.T) {
	c, srv := tpdbSceneStub(t, stashboxtest.Options{
		SiteLookup: []stashboxtest.Response{siteLookupResponse()},
		SceneIndex: []stashboxtest.Response{sceneIndexResponse()},
	})

	page, err := c.SearchScenes(context.Background(), core.SceneQuery{SiteStashID: exxtraUUID, Page: 1})
	if err != nil {
		t.Fatalf("SearchScenes: %v", err)
	}

	reqs := srv.Requests()
	if posts := pathRequests(reqs, "/graphql"); len(posts) != 0 {
		t.Fatalf("GraphQL requests = %d, want 0: queryScenes is a stub on TPDB", len(posts))
	}
	lookups := pathRequests(reqs, "/sites/")
	if len(lookups) != 1 || !strings.HasSuffix(lookups[0].Path, exxtraUUID) {
		t.Fatalf("site lookups = %+v, want exactly one for %s", lookups, exxtraUUID)
	}
	scenes := pathRequests(reqs, "/scenes")
	if len(scenes) != 1 {
		t.Fatalf("scene index requests = %d, want 1", len(scenes))
	}
	q := scenes[0]
	for param, want := range map[string]string{
		"site_id": "94", "page": "1", "per_page": "25", "sort": "date", "order": "desc",
	} {
		if got := valueOf(t, q.RawQuery, param); got != want {
			t.Errorf("%s = %q, want %q (query %q)", param, got, want, q.RawQuery)
		}
	}
	if q.Authorization != "Bearer "+testAPIKey || q.APIKey != "" {
		t.Errorf("credentials = (%q, ApiKey %q), want Bearer only", q.Authorization, q.APIKey)
	}

	if page.Total != 3950 || page.Page != 1 || page.PerPage != 25 {
		t.Fatalf("page = %+v, want total 3950 page 1 per_page 25", page)
	}
	if len(page.Scenes) != 2 {
		t.Fatalf("scenes = %d, want 2", len(page.Scenes))
	}
	first := page.Scenes[0]
	if first.StashID != "2499acef-1e9d-44f4-b552-3aa8ded1986c" ||
		first.Title != "Taking It All In" ||
		first.Overview != "A scene." ||
		first.Date.Format("2006-01-02") != "2026-08-03" ||
		first.Duration != 2518 ||
		first.URL != "https://example.test/scene" ||
		first.ImageURL != "https://thumb.example.test/poster.jpg" ||
		first.SiteStashID != exxtraUUID || first.SiteName != "Brazzers Exxtra" {
		t.Errorf("first scene mapped wrong: %+v", first)
	}
	if len(first.Performers) != 1 || first.Performers[0].StashID != "60416988-eb25-4517-9914-4aa1915a3e43" {
		t.Errorf("performers = %+v, want the one real credit, empty one skipped", first.Performers)
	}
	if second := page.Scenes[1]; second.ImageURL != "https://media.example.test/second.jpg" {
		t.Errorf("second.ImageURL = %q, want the image fallback for a null poster", second.ImageURL)
	}
}

func TestSearchScenesCachesTheSiteIDAcrossPages(t *testing.T) {
	c, srv := tpdbSceneStub(t, stashboxtest.Options{
		SiteLookup: []stashboxtest.Response{siteLookupResponse()},
		SceneIndex: []stashboxtest.Response{sceneIndexResponse()},
	})

	for pageNo := 1; pageNo <= 3; pageNo++ {
		if _, err := c.SearchScenes(context.Background(), core.SceneQuery{SiteStashID: exxtraUUID, Page: pageNo}); err != nil {
			t.Fatalf("page %d: %v", pageNo, err)
		}
	}
	if lookups := pathRequests(srv.Requests(), "/sites/"); len(lookups) != 1 {
		t.Fatalf("site lookups = %d across three pages, want 1: the id never changes", len(lookups))
	}
}

func TestSearchScenesTextQueryOnTPDBNeedsNoSiteLookup(t *testing.T) {
	c, srv := tpdbSceneStub(t, stashboxtest.Options{
		SceneIndex: []stashboxtest.Response{sceneIndexResponse()},
	})

	if _, err := c.SearchScenes(context.Background(), core.SceneQuery{Text: " golden hour "}); err != nil {
		t.Fatalf("SearchScenes: %v", err)
	}
	reqs := srv.Requests()
	if lookups := pathRequests(reqs, "/sites/"); len(lookups) != 0 {
		t.Fatalf("site lookups = %d for a text search, want 0", len(lookups))
	}
	scenes := pathRequests(reqs, "/scenes")
	if len(scenes) != 1 {
		t.Fatalf("scene index requests = %d, want 1", len(scenes))
	}
	if got := valueOf(t, scenes[0].RawQuery, "q"); got != "golden hour" {
		t.Errorf("q = %q, want the trimmed text", got)
	}
	if got := valueOf(t, scenes[0].RawQuery, "site_id"); got != "" {
		t.Errorf("site_id = %q, want unset", got)
	}
}

func TestSearchScenesSurfacesAnUnknownSiteAsNotFound(t *testing.T) {
	c, _ := tpdbSceneStub(t, stashboxtest.Options{
		// A 200 whose data has no id: TPDB's answer for a site it does not know.
		SiteLookup: []stashboxtest.Response{stashboxtest.Raw([]byte(`{"data":{}}`))},
	})

	_, err := c.SearchScenes(context.Background(), core.SceneQuery{SiteStashID: "not-a-site"})
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// A stash-box that is not TPDB keeps the GraphQL road exactly as it was,
// criterion input included — the dialect must be unreachable, not just unused.
func TestSearchScenesOnAStashBoxStillSpeaksGraphQL(t *testing.T) {
	srv := stashboxtest.New(stashboxtest.Options{
		Operations: map[string][]stashboxtest.Response{
			opSearchScenes: {stashboxtest.Data([]byte(`{"queryScenes":{"count":0,"scenes":[]}}`))},
		},
	})
	t.Cleanup(srv.Close)
	c := New(testAPIKey, srv.URL(), srv.Client())

	if _, err := c.SearchScenes(context.Background(), core.SceneQuery{SiteStashID: exxtraUUID}); err != nil {
		t.Fatalf("SearchScenes: %v", err)
	}
	reqs := srv.Requests()
	if len(reqs) != 1 || reqs[0].OperationName != opSearchScenes {
		t.Fatalf("requests = %+v, want exactly one %s GraphQL call", reqs, opSearchScenes)
	}
	input, _ := reqs[0].Variables["input"].(map[string]any)
	studios, _ := input["studios"].(map[string]any)
	if studios == nil || studios["modifier"] != "INCLUDES" {
		t.Fatalf("studios = %#v, want the stash-box MultiIDCriterionInput unchanged", input["studios"])
	}
}

// valueOf reads one parameter out of a recorded query string.
func valueOf(t *testing.T, rawQuery, param string) string {
	t.Helper()
	values, err := url.ParseQuery(rawQuery)
	if err != nil {
		t.Fatalf("parse query %q: %v", rawQuery, err)
	}
	return values.Get(param)
}
