package stashbox

import (
	"context"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/stashbox/stashboxtest"
)

// Ids from testdata/tpdb_sites.json. The Brazzers uuid is the same value the
// GraphQL side calls a studio id, which is the fact the whole dialect rests on.
const (
	idTPDBBrazzers      = "e3b61b3e-1111-4111-8111-111111111111"
	idTPDBBrazzersVault = "11111111-aaaa-4aaa-8aaa-111111111111"
	idTPDBNebraska      = "22222222-bbbb-4bbb-8bbb-222222222222"
)

// newTPDBRESTStub is a client that believes it is talking to TPDB — no
// queryStudios, and a REST site index — pointed at one fake serving both.
//
// The REST base is set directly because New derives it from the endpoint host,
// and a fake cannot be both 127.0.0.1 and theporndb.net. The derivation itself
// is covered by TestTPDBSitesURLForEndpoint, so the two halves are tested where
// each one lives.
func newTPDBRESTStub(t *testing.T, ops map[string][]stashboxtest.Response, index ...stashboxtest.Response) (*Client, *stashboxtest.Server) {
	t.Helper()

	srv := stashboxtest.New(stashboxtest.Options{
		Operations:          ops,
		WithoutQueryStudios: true,
		SiteIndex:           index,
	})
	t.Cleanup(srv.Close)

	c := New(testAPIKey, srv.URL(), srv.Client())
	c.sleep = func(context.Context, time.Duration) error { return nil }
	c.restSites = srv.URL() + "/sites"
	return c, srv
}

// restRequests are the requests that reached the REST site index.
func restRequests(reqs []stashboxtest.Request) []stashboxtest.Request {
	out := []stashboxtest.Request{}
	for _, r := range reqs {
		if r.Path == "/sites" {
			out = append(out, r)
		}
	}
	return out
}

func operationNames(reqs []stashboxtest.Request) []string {
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, r.OperationName)
	}
	return out
}

// The bug this dialect exists for: searchScene matches scene text, so a search
// for a site by NAME could not find it — "br" answered with the studios behind
// whichever scenes mentioned "br" and never offered Brazzers at all. The REST
// index searches names, which is the question being asked.
func TestSearchSitesUsesTheTPDBSiteIndexForATypedQuery(t *testing.T) {
	// The scene-derived path is stubbed and would answer perfectly well. That is
	// the point: the assertion is that the REST index is *preferred*, not merely
	// that it works when nothing else does.
	c, s := newTPDBRESTStub(t, map[string][]stashboxtest.Response{
		opSearchSitesByScene: {okFixture(t, "search_scene_studios.json")},
		opFindSite:           {siteReply(idBrazzers, "Brazzers")},
	}, okFixture(t, "tpdb_sites.json"))

	got, err := c.SearchSites(context.Background(), "  brazzers  ")
	if err != nil {
		t.Fatalf("SearchSites: %v", err)
	}

	rest := restRequests(s.Requests())
	if len(rest) != 1 {
		t.Fatalf("REST requests = %d, want exactly 1:\n%v", len(rest), operationNames(s.Requests()))
	}
	req := rest[0]
	if req.Method != http.MethodGet {
		t.Errorf("method = %q, want GET", req.Method)
	}
	params, err := url.ParseQuery(req.RawQuery)
	if err != nil {
		t.Fatalf("parse query %q: %v", req.RawQuery, err)
	}
	if params.Get("q") != "brazzers" {
		t.Errorf("q = %q, want the trimmed query", params.Get("q"))
	}
	if params.Get("per_page") != "25" {
		t.Errorf("per_page = %q, want the picker's page", params.Get("per_page"))
	}
	// Bearer only: the REST API does not read stash-box's own ApiKey header, and
	// sending a credential where it means nothing is not free.
	if want := "Bearer " + testAPIKey; req.Authorization != want {
		t.Errorf("Authorization = %q, want %q", req.Authorization, want)
	}
	if req.APIKey != "" {
		t.Errorf("%s header = %q, want it unset on the REST call", APIKeyHeader, req.APIKey)
	}

	// The index listed Brazzers third; an exact name outranks a prefix of it,
	// and a row that only contains the query outranks nothing.
	want := []core.SiteMeta{
		{
			StashID:  idTPDBBrazzers,
			Name:     "Brazzers",
			URL:      "https://www.brazzers.com",
			ImageURL: "https://cdn.example.test/brazzers-logo.jpg",
		},
		{
			StashID:       idTPDBBrazzersVault,
			Name:          "Brazzers Vault",
			Aliases:       []string{"brazzersvault"},
			URL:           "https://www.brazzers.com/vault",
			ImageURL:      "https://cdn.example.test/vault-logo.jpg",
			ParentStashID: idTPDBBrazzers,
			ParentName:    "Brazzers",
		},
		{
			StashID:       idTPDBNebraska,
			Name:          "Nebraska Coeds",
			Aliases:       []string{"nebraskacoeds"},
			URL:           "https://www.nebraskacoeds.com",
			ImageURL:      "https://cdn.example.test/nebraska-poster.jpg",
			ParentStashID: "33333333-cccc-4ccc-8ccc-333333333333",
			ParentName:    "Coeds Network",
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SearchSites:\n got %+v\nwant %+v", got, want)
	}

	// No per-row findStudio: the row is enough for the picker, and AddSite
	// resolves the one site the user actually chooses.
	for _, r := range s.Requests() {
		if r.OperationName == opFindSite {
			t.Errorf("the REST path made a findStudio call, which would be %d round trips per keystroke", len(want))
		}
		if r.OperationName == opSearchSitesByScene {
			t.Error("the REST path fell through to the scene-derived search")
		}
	}
}

// A blank query is the picker opening, and the right answer there is "what this
// endpoint is currently busy with" — the newest scenes' studios. The REST index
// has no such ordering to offer, so the blank case is unchanged.
func TestSearchSitesKeepsTheSceneDerivedListForABlankQueryOnTPDB(t *testing.T) {
	c, s := newTPDBRESTStub(t, map[string][]stashboxtest.Response{
		opRecentSitesByScene: {okFixture(t, "query_scenes_studios.json")},
		opFindSite: {
			siteReply("aaaaaaaa-2222-4222-8222-222222222222", "Vixen Media Group"),
			siteReply("f1f1f1f1-1111-4111-8111-111111111111", "Tushy"),
			siteReply("f3f3f3f3-4444-4444-8444-444444444444", "Deeper"),
		},
	}, okFixture(t, "tpdb_sites.json"))

	got, err := c.SearchSites(context.Background(), "   ")
	if err != nil {
		t.Fatalf("SearchSites: %v", err)
	}
	if want := []string{"Vixen Media Group", "Tushy", "Deeper"}; !reflect.DeepEqual(siteNames(got), want) {
		t.Errorf("sites = %v, want the recent-scenes list %v", siteNames(got), want)
	}
	if rest := restRequests(s.Requests()); len(rest) != 0 {
		t.Errorf("REST requests = %d, want 0: a blank query has nothing to search the index for", len(rest))
	}
}

// The dialect is an improvement, never a dependency: if the index fails, the
// worse answer is still an answer, and the user sees no error at all.
func TestSearchSitesFallsBackToScenesWhenTheSiteIndexFails(t *testing.T) {
	c, s := newTPDBRESTStub(t, map[string][]stashboxtest.Response{
		opSearchSitesByScene: {okFixture(t, "search_scene_studios.json")},
		opFindSite:           {siteReply(idBrazzers, "Brazzers")},
	}, stashboxtest.Status(http.StatusInternalServerError, []byte(`{"message":"Server Error"}`)))

	got, err := c.SearchSites(context.Background(), "brazzers")
	if err != nil {
		t.Fatalf("SearchSites: %v, want the scene-derived answer instead of an error", err)
	}
	if len(got) == 0 {
		t.Error("sites = none, want the scene-derived fallback's results")
	}
	if rest := restRequests(s.Requests()); len(rest) != 1 {
		t.Errorf("REST requests = %d, want 1 (tried, failed, moved on)", len(rest))
	}
	var derived bool
	for _, r := range s.Requests() {
		if r.OperationName == opSearchSitesByScene {
			derived = true
		}
	}
	if !derived {
		t.Error("the failed REST search did not fall through to the scene-derived path")
	}
}

// Every other stash-box has queryStudios and no REST side at all. The dialect
// must be unreachable there — not merely unused.
func TestSearchSitesNeverTouchesTheSiteIndexOnAStashBox(t *testing.T) {
	// The fake offers a REST index; the client, built for a non-TPDB endpoint,
	// must have no way to ask for it.
	srv := stashboxtest.New(stashboxtest.Options{
		Operations: map[string][]stashboxtest.Response{
			opSearchSites: {okFixture(t, "query_studios.json")},
		},
		SiteIndex: []stashboxtest.Response{okFixture(t, "tpdb_sites.json")},
	})
	t.Cleanup(srv.Close)

	c := New(testAPIKey, srv.URL(), srv.Client())
	if c.restSites != "" {
		t.Fatalf("restSites = %q for a non-TPDB endpoint, want none", c.restSites)
	}

	if _, err := c.SearchSites(context.Background(), "tushy"); err != nil {
		t.Fatalf("SearchSites: %v", err)
	}
	if rest := restRequests(srv.Requests()); len(rest) != 0 {
		t.Errorf("REST requests = %d, want 0 on a stash-box endpoint", len(rest))
	}
	if ops := operationNames(srv.Requests()); !reflect.DeepEqual(ops, []string{opSearchSites}) {
		t.Errorf("operations = %v, want only %q", ops, opSearchSites)
	}
}

func TestTPDBSitesURLForEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{name: "the TPDB preset", endpoint: DefaultEndpoint, want: tpdbSitesURL},
		{name: "blank means the preset, which is TPDB", endpoint: "", want: tpdbSitesURL},
		{name: "padded", endpoint: "  https://theporndb.net/graphql  ", want: tpdbSitesURL},
		{name: "a subdomain of it", endpoint: "https://api.theporndb.net/graphql", want: tpdbSitesURL},
		{name: "case is not part of a host", endpoint: "https://ThePornDB.net/graphql", want: tpdbSitesURL},
		{name: "with a port", endpoint: "https://theporndb.net:443/graphql", want: tpdbSitesURL},
		{name: "stashdb", endpoint: "https://stashdb.org/graphql", want: ""},
		{name: "fansdb", endpoint: "https://fansdb.cc/graphql", want: ""},
		{name: "a self-hosted box", endpoint: "http://127.0.0.1:9998/graphql", want: ""},
		{
			// A host that merely ends in the same letters is a different host.
			name:     "a lookalike host",
			endpoint: "https://nottheporndb.net/graphql",
			want:     "",
		},
		{name: "unparseable", endpoint: "://nonsense", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tpdbSitesURLFor(tt.endpoint); got != tt.want {
				t.Errorf("tpdbSitesURLFor(%q) = %q, want %q", tt.endpoint, got, tt.want)
			}
		})
	}

	// The fake serves the path this URL asks for. Without this, a rename here
	// would quietly stop every REST test from exercising the route.
	u, err := url.Parse(tpdbSitesURL)
	if err != nil {
		t.Fatalf("parse %q: %v", tpdbSitesURL, err)
	}
	if u.Path != "/sites" {
		t.Errorf("REST path = %q, want the /sites the fake endpoint answers", u.Path)
	}
	if !strings.HasPrefix(tpdbSitesURL, "https://") {
		t.Errorf("REST base = %q, want https", tpdbSitesURL)
	}
}

func TestRankTPDBSites(t *testing.T) {
	row := func(uuid, name string) tpdbSiteRow {
		return tpdbSiteRow{UUID: uuid, Name: name}
	}

	tests := []struct {
		name   string
		needle string
		rows   []tpdbSiteRow
		want   []string
	}{
		{
			name:   "an exact name climbs over the index's own order",
			needle: "brazzers",
			rows:   []tpdbSiteRow{row("vault", "Brazzers Vault"), row("brz", "Brazzers")},
			want:   []string{"brz", "vault"},
		},
		{
			name:   "within a tier the index's order is kept",
			needle: "br",
			rows:   []tpdbSiteRow{row("a", "Bratty Sis"), row("b", "Brazzers Vault")},
			want:   []string{"a", "b"},
		},
		{
			name:   "a name match outranks one that only contains the query",
			needle: "br",
			rows:   []tpdbSiteRow{row("neb", "Nebraska Coeds"), row("brz", "Brazzers")},
			want:   []string{"brz", "neb"},
		},
		{
			// A row nothing can address would produce an add that fails.
			name:   "a row with no uuid is dropped",
			needle: "br",
			rows:   []tpdbSiteRow{row("", "Brazzers"), row("ok", "Bratty Sis")},
			want:   []string{"ok"},
		},
		{
			name:   "a repeated uuid appears once",
			needle: "br",
			rows:   []tpdbSiteRow{row("brz", "Brazzers"), row("brz", "Brazzers")},
			want:   []string{"brz"},
		},
		{
			name:   "nothing to rank",
			needle: "br",
			rows:   nil,
			want:   []string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := make([]string, 0, len(tt.rows))
			for _, meta := range rankTPDBSites(tt.rows, tt.needle) {
				got = append(got, meta.StashID)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("rankTPDBSites = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTPDBSiteMeta(t *testing.T) {
	t.Run("the uuid is the id, never the numeric one", func(t *testing.T) {
		got := tpdbSiteMeta(tpdbSiteRow{UUID: idTPDBBrazzers, Name: "Brazzers"})
		if got.StashID != idTPDBBrazzers {
			t.Errorf("StashID = %q, want the uuid", got.StashID)
		}
	})

	t.Run("the logo wins over the poster", func(t *testing.T) {
		got := tpdbSiteMeta(tpdbSiteRow{UUID: "x", Logo: "logo.jpg", Poster: "poster.jpg"})
		if got.ImageURL != "logo.jpg" {
			t.Errorf("ImageURL = %q, want the logo", got.ImageURL)
		}
	})

	t.Run("the poster stands in for a missing logo", func(t *testing.T) {
		got := tpdbSiteMeta(tpdbSiteRow{UUID: "x", Poster: "poster.jpg"})
		if got.ImageURL != "poster.jpg" {
			t.Errorf("ImageURL = %q, want the poster", got.ImageURL)
		}
	})

	t.Run("a short name that says something new is an alias", func(t *testing.T) {
		got := tpdbSiteMeta(tpdbSiteRow{UUID: "x", Name: "Brazzers Vault", ShortName: "brazzersvault"})
		if want := []string{"brazzersvault"}; !reflect.DeepEqual(got.Aliases, want) {
			t.Errorf("Aliases = %v, want %v", got.Aliases, want)
		}
	})

	t.Run("a short name that only differs in case says nothing", func(t *testing.T) {
		got := tpdbSiteMeta(tpdbSiteRow{UUID: "x", Name: "Brazzers", ShortName: "brazzers"})
		if got.Aliases != nil {
			t.Errorf("Aliases = %v, want none", got.Aliases)
		}
	})

	t.Run("the parent is preferred over the network above it", func(t *testing.T) {
		got := tpdbSiteMeta(tpdbSiteRow{
			UUID:    "x",
			Parent:  &tpdbSiteRef{UUID: "p", Name: "Parent"},
			Network: &tpdbSiteRef{UUID: "n", Name: "Network"},
		})
		if got.ParentStashID != "p" || got.ParentName != "Parent" {
			t.Errorf("parent = (%q, %q), want the nearer one", got.ParentStashID, got.ParentName)
		}
	})

	t.Run("the network stands in when there is no parent", func(t *testing.T) {
		got := tpdbSiteMeta(tpdbSiteRow{UUID: "x", Network: &tpdbSiteRef{UUID: "n", Name: "Network"}})
		if got.ParentStashID != "n" || got.ParentName != "Network" {
			t.Errorf("parent = (%q, %q), want the network", got.ParentStashID, got.ParentName)
		}
	})

	t.Run("a site that stands alone has no parent", func(t *testing.T) {
		got := tpdbSiteMeta(tpdbSiteRow{UUID: "x", Name: "Alone"})
		if got.ParentStashID != "" || got.ParentName != "" {
			t.Errorf("parent = (%q, %q), want none", got.ParentStashID, got.ParentName)
		}
	})
}
