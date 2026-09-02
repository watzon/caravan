package api

import (
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// The scene scope's filter surface.
//
// What is under test is the round trip: every control the rail offers reaches
// the provider as the field it means, and anything the parser cannot read is a
// 400 rather than an unfiltered page. The provider records the SceneQuery it
// was handed, because what reaches the provider is the only proof a parameter
// was not dropped on the way.

// adultDiscoverServer wires a granted-by-default server with a recording
// provider and the module on.
func adultDiscoverServer(t *testing.T) (http.Handler, *fakeAdultProvider) {
	t.Helper()

	h, st, mgr := newTestServer(t)
	provider := &fakeAdultProvider{scenes: fakeScenes()}
	mgr.adult = provider
	enableAdult(t, st)
	return h, provider
}

func TestAdultDiscoverMapsEveryFilterParam(t *testing.T) {
	h, provider := adultDiscoverServer(t)

	query := url.Values{}
	query.Set("q", "  golden hour  ")
	query.Set("page", "3")
	query.Set("site", "site-1")
	query.Set("scope", "network")
	query.Add("performers", "84060:Mia Malkova")
	query.Add("performers", "90211:Riley Reid")
	query.Set("performers_all", "true")
	query.Add("tags", "70:Anal")
	query.Set("tags_all", "true")
	query.Set("year", "2024")
	query.Set("date", "2024-03-02")
	query.Set("date_op", "on_or_after")
	query.Set("duration", "1800")
	query.Set("sort", "duration")
	query.Set("order", "asc")

	rec := do(t, h, http.MethodGet, "/api/v1/adult/discover?"+query.Encode(), "")
	wantStatus(t, rec, http.StatusOK)

	want := core.SceneQuery{
		Text:        "golden hour",
		SiteStashID: "site-1",
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
	}
	if got := provider.sceneQuery(); !reflect.DeepEqual(got, want) {
		t.Errorf("SceneQuery:\n got %+v\nwant %+v", got, want)
	}
}

// A filter id is opaque on this surface: TPDB serves numeric ids and every
// other stash-box serves uuids, and a client echoes back whichever it was
// handed. Both spellings have to survive the trip.
func TestAdultDiscoverAcceptsBothIDDialects(t *testing.T) {
	h, provider := adultDiscoverServer(t)

	rec := do(t, h, http.MethodGet,
		"/api/v1/adult/discover?performers=b1b1b1b1-1111-4111-8111-bbbbbbbbbbbb%3AAva+Rivers&tags=70%3AAnal", "")
	wantStatus(t, rec, http.StatusOK)

	got := provider.sceneQuery()
	wantPerformers := []core.SceneFilterRef{{StashID: "b1b1b1b1-1111-4111-8111-bbbbbbbbbbbb", Name: "Ava Rivers"}}
	if !reflect.DeepEqual(got.Performers, wantPerformers) {
		t.Errorf("performers = %+v, want the uuid kept as a stash id", got.Performers)
	}
	if !reflect.DeepEqual(got.Tags, []core.SceneFilterRef{{ID: 70, Name: "Anal"}}) {
		t.Errorf("tags = %+v, want the numeric id", got.Tags)
	}
}

// A name is optional: an id alone is a legal filter, it simply renders a chip
// with nothing on it.
func TestAdultDiscoverAcceptsARefWithNoName(t *testing.T) {
	h, provider := adultDiscoverServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/adult/discover?performers=84060", "")
	wantStatus(t, rec, http.StatusOK)
	if got := provider.sceneQuery().Performers; !reflect.DeepEqual(got, []core.SceneFilterRef{{ID: 84060}}) {
		t.Errorf("performers = %+v, want the bare id", got)
	}
}

// The unfiltered scope is the whole index: no filter set, and still a 200.
func TestAdultDiscoverAcceptsAnEmptyFilter(t *testing.T) {
	h, provider := adultDiscoverServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/adult/discover", "")
	wantStatus(t, rec, http.StatusOK)
	if got := provider.sceneQuery(); !reflect.DeepEqual(got, core.SceneQuery{}) {
		t.Errorf("SceneQuery = %+v, want the zero query", got)
	}
}

// Every one of these would otherwise reach the provider as "no filter", which
// is a wider question than the caller asked. The provider must not be called
// at all.
func TestAdultDiscoverRejectsMalformedFilters(t *testing.T) {
	tests := []struct {
		name  string
		query string
	}{
		{name: "an unknown parameter", query: "?performer=84060"},
		{name: "a filter from the movie scope", query: "?genres=28"},
		{name: "an empty performer id", query: "?performers=%3AMia+Malkova"},
		{name: "a zero performer id", query: "?performers=0%3AMia+Malkova"},
		{name: "a negative tag id", query: "?tags=-3%3AAnal"},
		{name: "a non-boolean any/all switch", query: "?performers_all=yes"},
		{name: "an unknown scope", query: "?site=site-1&scope=galaxy"},
		{name: "a scope with no site", query: "?scope=network"},
		{name: "a malformed date", query: "?date=March+2"},
		{name: "an unknown date comparison", query: "?date=2024-03-02&date_op=roughly"},
		{name: "a comparison with no date", query: "?date_op=after"},
		{name: "a negative year", query: "?year=-1"},
		{name: "a non-numeric duration", query: "?duration=half+an+hour"},
		{name: "an unknown sort", query: "?sort=revenue"},
		{name: "an unknown order", query: "?order=sideways"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, provider := adultDiscoverServer(t)

			rec := do(t, h, http.MethodGet, "/api/v1/adult/discover"+tc.query, "")
			wantStatus(t, rec, http.StatusBadRequest)
			if calls := provider.callLog(); len(calls) != 0 {
				t.Errorf("a malformed filter reached the provider: %v", calls)
			}
		})
	}
}

// A provider that cannot express a filter says so, and that is the caller's
// problem: a 400 naming the filter, not the 502 an upstream failure gets. The
// value asked for is never echoed.
func TestAdultDiscoverReportsAFilterTheEndpointCannotServe(t *testing.T) {
	h, st, mgr := newTestServer(t)
	provider := &fakeAdultProvider{
		scenes:   fakeScenes(),
		sceneErr: &core.SceneFilterUnsupportedError{Filter: "release year"},
	}
	mgr.adult = provider
	enableAdult(t, st)

	rec := do(t, h, http.MethodGet, "/api/v1/adult/discover?year=2024&q=golden+hour", "")
	wantStatus(t, rec, http.StatusBadRequest)
	if !strings.Contains(rec.Body.String(), "release year") {
		t.Errorf("body = %q, want the refused filter named", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "golden hour") {
		t.Errorf("body = %q, echoes the query", rec.Body.String())
	}
}

// An ordinary upstream failure still reads as one.
func TestAdultDiscoverProviderFailureIsBadGateway(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{sceneErr: errors.New("stashbox: endpoint is down")}
	enableAdult(t, st)

	rec := do(t, h, http.MethodGet, "/api/v1/adult/discover", "")
	wantStatus(t, rec, http.StatusBadGateway)
}

func TestAdultTypeaheadsPassThroughTheProvider(t *testing.T) {
	h, st, mgr := newTestServer(t)
	provider := &fakeAdultProvider{
		performers: []core.ScenePerformerMeta{
			{
				SceneFilterRef: core.SceneFilterRef{ID: 84060, StashID: "60416988-eb25-4517-9914-4aa1915a3e43", Name: "Mia Malkova"},
				ImageURL:       "https://cdn.example.test/mia.jpg",
			},
			// A stash-box row, which carries no numeric id at all.
			{SceneFilterRef: core.SceneFilterRef{StashID: "b1b1b1b1-1111-4111-8111-bbbbbbbbbbbb", Name: "Ava Rivers"}},
		},
		tags: []core.SceneFilterRef{{ID: 70, StashID: "3b5c4a11-9d21-4a0e-8f31-1c2d3e4f5061", Name: "Anal"}},
	}
	mgr.adult = provider
	enableAdult(t, st)

	rec := do(t, h, http.MethodGet, "/api/v1/adult/performers?q=+mia+", "")
	wantStatus(t, rec, http.StatusOK)
	var performers scenePerformersResponse
	decodeBody(t, rec, &performers)
	want := []scenePerformerJSON{
		{sceneFilterRefJSON: sceneFilterRefJSON{ID: "84060", Name: "Mia Malkova"}, ImageURL: "https://cdn.example.test/mia.jpg"},
		{sceneFilterRefJSON: sceneFilterRefJSON{ID: "b1b1b1b1-1111-4111-8111-bbbbbbbbbbbb", Name: "Ava Rivers"}},
	}
	if !reflect.DeepEqual(performers.Performers, want) {
		t.Errorf("performers = %+v, want %+v", performers.Performers, want)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/adult/tags?q=anal", "")
	wantStatus(t, rec, http.StatusOK)
	var tags sceneTagsResponse
	decodeBody(t, rec, &tags)
	if !reflect.DeepEqual(tags.Tags, []sceneFilterRefJSON{{ID: "70", Name: "Anal"}}) {
		t.Errorf("tags = %+v", tags.Tags)
	}

	// The query reaches the provider trimmed, once per typeahead.
	if got := provider.callLog(); !reflect.DeepEqual(got, []string{"SearchPerformers mia", "SearchTags anal"}) {
		t.Errorf("calls = %v", got)
	}
}

// The id a typeahead hands out is the id the filter takes back, whichever
// dialect is behind it. This is the contract the rail's chips ride on.
func TestAdultTypeaheadIDsRoundTripIntoTheFilter(t *testing.T) {
	h, st, mgr := newTestServer(t)
	provider := &fakeAdultProvider{
		scenes: fakeScenes(),
		tags:   []core.SceneFilterRef{{ID: 70, StashID: "3b5c4a11-9d21-4a0e-8f31-1c2d3e4f5061", Name: "Anal"}},
	}
	mgr.adult = provider
	enableAdult(t, st)

	rec := do(t, h, http.MethodGet, "/api/v1/adult/tags?q=anal", "")
	wantStatus(t, rec, http.StatusOK)
	var tags sceneTagsResponse
	decodeBody(t, rec, &tags)
	if len(tags.Tags) != 1 {
		t.Fatalf("tags = %+v", tags.Tags)
	}

	filter := url.Values{}
	filter.Set("tags", tags.Tags[0].ID+":"+tags.Tags[0].Name)
	rec = do(t, h, http.MethodGet, "/api/v1/adult/discover?"+filter.Encode(), "")
	wantStatus(t, rec, http.StatusOK)
	if got := provider.sceneQuery().Tags; !reflect.DeepEqual(got, []core.SceneFilterRef{{ID: 70, Name: "Anal"}}) {
		t.Errorf("tags = %+v, want the id the typeahead handed out", got)
	}
}

func TestAdultTypeaheadsRequireAQuery(t *testing.T) {
	h, st, mgr := newTestServer(t)
	provider := &fakeAdultProvider{}
	mgr.adult = provider
	enableAdult(t, st)

	for _, target := range []string{
		"/api/v1/adult/performers",
		"/api/v1/adult/performers?q=+++",
		"/api/v1/adult/tags",
		"/api/v1/adult/tags?q=",
	} {
		rec := do(t, h, http.MethodGet, target, "")
		wantStatus(t, rec, http.StatusBadRequest)
	}
	if calls := provider.callLog(); len(calls) != 0 {
		t.Errorf("a blank typeahead reached the provider: %v", calls)
	}
}

// A list is always a list: a null would make the rail render nothing without
// saying why.
func TestAdultTypeaheadsReturnArrays(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{}
	enableAdult(t, st)

	for _, tc := range []struct{ target, key string }{
		{target: "/api/v1/adult/performers?q=nobody", key: `"performers":[]`},
		{target: "/api/v1/adult/tags?q=nothing", key: `"tags":[]`},
	} {
		rec := do(t, h, http.MethodGet, tc.target, "")
		wantStatus(t, rec, http.StatusOK)
		if !strings.Contains(rec.Body.String(), tc.key) {
			t.Errorf("%s = %s, want %s", tc.target, rec.Body.String(), tc.key)
		}
	}
}

// What the rail is allowed to draw.

// thinAdultProvider is a dialect that serves the filters every stash-box has
// and refuses the ones only TPDB does, StashDB and FansDB, in other words.
type thinAdultProvider struct {
	fakeAdultProvider
}

func (p *thinAdultProvider) SceneFilterSupport() core.SceneFilterSupport {
	return core.SceneFilterSupport{}
}

// The rail cannot hide a control it does not know is unserved, and the scene
// answer is too late to tell it: a URL naming an unsupported filter fails, so
// the block has to arrive before the first scene request. /auth/me is where the
// SPA already learns what it may draw.
func TestAuthMeReportsWhichSceneFiltersTheEndpointServes(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &thinAdultProvider{}
	enableAdult(t, st)

	rec := do(t, h, http.MethodGet, "/api/v1/auth/me", "")
	wantStatus(t, rec, http.StatusOK)
	var thin meResponse
	decodeBody(t, rec, &thin)
	if thin.SceneFilters == nil {
		t.Fatalf("me = %s, want a scene_filters block", rec.Body.String())
	}
	if *thin.SceneFilters != (sceneFiltersJSON{}) {
		t.Errorf("scene_filters = %+v, want every control withheld", *thin.SceneFilters)
	}

	// A provider that does not report is assumed able: the refusal still
	// explains, which is exactly the behaviour that predates the block.
	mgr.adult = &fakeAdultProvider{}
	rec = do(t, h, http.MethodGet, "/api/v1/auth/me", "")
	wantStatus(t, rec, http.StatusOK)
	var full meResponse
	decodeBody(t, rec, &full)
	if full.SceneFilters == nil || !full.SceneFilters.Year || !full.SceneFilters.SiteScope ||
		!full.SceneFilters.AnyOf || !full.SceneFilters.SortDuration {
		t.Errorf("scene_filters = %+v, want every control offered", full.SceneFilters)
	}
}

// The block is a fact about the adult module, so it is absent for a caller the
// module is invisible to. The same rule the `adult` flag itself follows, and
// the reason it is a pointer.
func TestSceneFiltersAreAbsentWithoutTheModule(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &thinAdultProvider{}

	// The module switched off server-wide.
	rec := do(t, h, http.MethodGet, "/api/v1/auth/me", "")
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "scene_filters") {
		t.Errorf("me = %s, want no scene_filters with the module off", rec.Body.String())
	}

	// On, but with no credential: the screen's own 503 is the better answer
	// than a block of falses that would hide half the rail for a setup problem.
	enableAdult(t, st)
	mgr.adult = nil
	rec = do(t, h, http.MethodGet, "/api/v1/auth/me", "")
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "scene_filters") {
		t.Errorf("me = %s, want no scene_filters without a credential", rec.Body.String())
	}
}
