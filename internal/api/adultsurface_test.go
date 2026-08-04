package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// adultRoutes is every route registered on the adult mux in api.go, as
// (method, path) pairs. The list is duplicated from the registration on
// purpose: a test that discovered the routes from the mux would pass whatever
// the mux happened to hold, and what these tests are for is that the SET of
// adult routes is closed and every member of it is gated.
var adultRoutes = []struct{ method, path string }{
	{http.MethodGet, "/api/v1/adult/sites"},
	{http.MethodPost, "/api/v1/adult/sites"},
	{http.MethodGet, "/api/v1/adult/sites/1"},
	{http.MethodGet, "/api/v1/adult/search?q=brazzers"},
	{http.MethodGet, "/api/v1/adult/discover"},
	{http.MethodGet, "/api/v1/adult/users"},
	{http.MethodPut, "/api/v1/adult/users/1/access"},
}

// fakeAdultProvider is a canned core.AdultMetadataProvider that records every
// call. The recording is what the "no adult surface reaches the provider"
// assertions are made against — a nil provider would prove only that a nil
// provider is quiet.
type fakeAdultProvider struct {
	sites  []core.SiteMeta
	scenes []core.SceneMeta
	err    error

	mu    sync.Mutex
	calls []string
}

func (p *fakeAdultProvider) record(call string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls = append(p.calls, call)
}

func (p *fakeAdultProvider) callLog() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.calls...)
}

func (p *fakeAdultProvider) SearchSites(ctx context.Context, q string) ([]core.SiteMeta, error) {
	p.record("SearchSites " + q)
	return p.sites, p.err
}

func (p *fakeAdultProvider) GetSite(ctx context.Context, stashID string) (*core.SiteMeta, error) {
	p.record("GetSite " + stashID)
	for i := range p.sites {
		if p.sites[i].StashID == stashID {
			return &p.sites[i], p.err
		}
	}
	return nil, store.ErrNotFound
}

func (p *fakeAdultProvider) SearchScenes(ctx context.Context, q core.SceneQuery) (*core.ScenePage, error) {
	p.record("SearchScenes " + q.Text)
	if p.err != nil {
		return nil, p.err
	}
	return &core.ScenePage{Page: 1, PerPage: 25, Total: len(p.scenes), Scenes: p.scenes}, nil
}

func (p *fakeAdultProvider) GetScene(ctx context.Context, stashID string) (*core.SceneMeta, error) {
	p.record("GetScene " + stashID)
	for i := range p.scenes {
		if p.scenes[i].StashID == stashID {
			return &p.scenes[i], p.err
		}
	}
	return nil, store.ErrNotFound
}

// fakeScenes is the canned catalogue the provider answers with.
func fakeScenes() []core.SceneMeta {
	return []core.SceneMeta{{
		StashID:     "scene-1",
		SiteStashID: "site-1",
		SiteName:    "Brazzers",
		Title:       "Deep Impact",
		Date:        time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC),
		Performers:  []core.ScenePerformer{{StashID: "p-1", Name: "Jane Doe", As: "Janie"}},
	}}
}

// enableAdult turns the module on the way POST /settings/adult does.
func enableAdult(t *testing.T, st *store.Store) {
	t.Helper()
	if err := st.SetAdultEnabled(context.Background(), true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
}

// seedSite puts one site with one scene in the library, the way library.AddSite
// does.
func seedSite(t *testing.T, st *store.Store) *core.Series {
	t.Helper()
	ctx := context.Background()

	sr := &core.Series{
		StashID: "site-1", Title: "Brazzers", SortTitle: "brazzers",
		Kind: core.SeriesKindAdult, Monitored: true,
		Path: store.AdultLibraryRoot + "/Brazzers",
	}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := st.UpsertSeason(ctx, &core.Season{
		SeriesID: sr.ID, Number: 2022, Title: "2022", Monitored: true,
	}); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	if err := st.UpsertEpisode(ctx, &core.Episode{
		SeriesID: sr.ID, SeasonNumber: 2022, EpisodeNumber: 1, StashID: "scene-1",
		Title: "Deep Impact", AirDate: time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC),
		Monitored: true,
		Scene:     &core.SceneInfo{Studio: "Brazzers", Performers: []string{"Janie"}, URL: "https://example.test/s/1"},
	}); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	return sr
}

// ---------------------------------------------------------------------------
// Disabled: every adult route is 404, for everyone, including an admin.
// ---------------------------------------------------------------------------

// This is the test Track 2 left as a placeholder: it could not tell a gated
// route from an unregistered one while the adult mux was empty. It can now —
// every path below IS registered, so a 404 can only be the gate.
func TestEveryAdultRouteIs404WhenTheModuleIsDisabled(t *testing.T) {
	h, st, mgr := newTestServer(t)
	provider := &fakeAdultProvider{sites: []core.SiteMeta{{StashID: "site-1", Name: "Brazzers"}}, scenes: fakeScenes()}
	// Deliberately wired: the refusal has to come from the gate, not from an
	// absent provider that would have 503'd anyway.
	mgr.adult = provider
	seedSite(t, st)

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	cookie := login(t, h, testAdmin, testPassword)

	for _, route := range adultRoutes {
		rec := doAuth(t, h, route.method, route.path, `{"granted":true,"stash_id":"site-1"}`, withCookie(cookie))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404 (body %q)", route.method, route.path, rec.Code, rec.Body.String())
		}
	}
	if calls := provider.callLog(); len(calls) != 0 {
		t.Errorf("the disabled module reached the provider: %v", calls)
	}
}

// The refusal must be indistinguishable from a path nobody ever registered, or
// the 404 becomes a directory of what to come back for once the module is on.
func TestDisabledAdultRoutesLookLikeUnroutedPaths(t *testing.T) {
	h, st, _ := newTestServer(t)
	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	cookie := login(t, h, testAdmin, testPassword)

	control := doAuth(t, h, http.MethodGet, "/api/v1/adult/not-a-route-at-all", "", withCookie(cookie))
	wantStatus(t, control, http.StatusNotFound)

	for _, route := range adultRoutes {
		if route.method != http.MethodGet {
			continue
		}
		rec := doAuth(t, h, route.method, route.path, "", withCookie(cookie))
		if rec.Body.String() != control.Body.String() {
			t.Errorf("%s answered %q, want the unrouted body %q",
				route.path, rec.Body.String(), control.Body.String())
		}
	}
}

// ---------------------------------------------------------------------------
// Enabled: an ungranted member sees nothing adult, anywhere.
// ---------------------------------------------------------------------------

// The enumeration this phase's acceptance asks for: every surface an ungranted
// member can reach at all, checked for adult content in one place so a new
// surface has an obvious home.
func TestUngrantedMemberSeesNothingAdultOnAnySharedSurface(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{sites: []core.SiteMeta{{StashID: "site-1", Name: "Brazzers"}}, scenes: fakeScenes()}
	enableAdult(t, st)
	site := seedSite(t, st)

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	adminCookie := login(t, h, testAdmin, testPassword)
	cookie := login(t, h, testMember, testPassword)

	// The member's OWN scene request, made while they were granted and then
	// left behind when the grant was taken away. It has to be theirs: a member
	// sees only their own rows anyway, so an admin's row would make the
	// requests-screen assertion below true for a reason that has nothing to do
	// with the adult filter.
	if err := st.SetUserAdultAccess(context.Background(), member.ID, true); err != nil {
		t.Fatalf("SetUserAdultAccess: %v", err)
	}
	rec := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"scene","stash_id":"scene-2","title":"Another Scene"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusCreated)
	var mine requestJSON
	decodeBody(t, rec, &mine)
	if err := st.SetUserAdultAccess(context.Background(), member.ID, false); err != nil {
		t.Fatalf("SetUserAdultAccess: %v", err)
	}

	// And an admin's scene row, which the member must not reach by id either.
	rec = doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"scene","stash_id":"scene-3","title":"A Third Scene"}`, withCookie(adminCookie))
	wantStatus(t, rec, http.StatusCreated)
	var adminRequest requestJSON
	decodeBody(t, rec, &adminRequest)

	t.Run("the nav item is off", func(t *testing.T) {
		rec := doAuth(t, h, http.MethodGet, "/api/v1/auth/me", "", withCookie(cookie))
		wantStatus(t, rec, http.StatusOK)
		var me meResponse
		decodeBody(t, rec, &me)
		if me.Adult {
			t.Error("auth/me tells an ungranted member the adult module is theirs")
		}
	})

	t.Run("every adult route refuses", func(t *testing.T) {
		for _, route := range adultRoutes {
			rec := doAuth(t, h, route.method, route.path, `{"granted":true}`, withCookie(cookie))
			// 404 from the gate for the routes memberAllowed names, 403 from
			// requireAuth for the rest — which is the same 403 every
			// non-allowlisted route in the API gives a member, so it says
			// nothing about this module in particular.
			if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
				t.Errorf("%s %s = %d, want 404 or 403", route.method, route.path, rec.Code)
			}
		}
	})

	t.Run("the requests screen holds no scene", func(t *testing.T) {
		rec := doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(cookie))
		wantStatus(t, rec, http.StatusOK)
		if strings.Contains(rec.Body.String(), MediaTypeScene) {
			t.Errorf("a scene row reached an ungranted member: %s", rec.Body.String())
		}
	})

	t.Run("a scene cannot be requested, and the refusal names no scene", func(t *testing.T) {
		// A scene the library does NOT hold, so the refusal has to come from
		// the media-type rule and cannot come from the already-in-the-library
		// conflict check standing in front of it.
		rec := doAuth(t, h, http.MethodPost, "/api/v1/requests",
			`{"media_type":"scene","stash_id":"scene-not-held","title":"Deep Impact"}`, withCookie(cookie))
		wantStatus(t, rec, http.StatusBadRequest)

		// Byte-identical to the answer an unrecognised media type gets, so the
		// endpoint is not an oracle for "does this server have the module".
		control := doAuth(t, h, http.MethodPost, "/api/v1/requests",
			`{"media_type":"banana","tmdb_id":1,"title":"x"}`, withCookie(cookie))
		wantStatus(t, control, http.StatusBadRequest)
		if rec.Body.String() != control.Body.String() {
			t.Errorf("scene refusal %q differs from the unknown-type refusal %q",
				rec.Body.String(), control.Body.String())
		}
		// And the message an ungranted caller is given does not name the third
		// media type at all. Indistinguishability alone would still hold if
		// both messages mentioned scenes, which would tell a member the API
		// knows about a kind of thing they were never shown.
		if strings.Contains(rec.Body.String(), MediaTypeScene) {
			t.Errorf("the refusal names the scene media type: %s", rec.Body.String())
		}
	})

	t.Run("no scene request can be reached by id, not even their own", func(t *testing.T) {
		for _, id := range []int64{mine.ID, adminRequest.ID} {
			rec := doAuth(t, h, http.MethodDelete, "/api/v1/requests/"+itoa(id), "", withCookie(cookie))
			wantStatus(t, rec, http.StatusNotFound)
		}
	})

	t.Run("discover is the one it saw before the module existed", func(t *testing.T) {
		// /discover needs a TMDB provider; without one it is a 503 either way.
		// What matters is that it never mentions a scene.
		rec := doAuth(t, h, http.MethodGet, "/api/v1/discover", "", withCookie(cookie))
		if strings.Contains(rec.Body.String(), "scene") {
			t.Errorf("discover mentioned a scene: %s", rec.Body.String())
		}
	})

	t.Run("the calendar and the library are refused outright", func(t *testing.T) {
		for _, path := range []string{
			"/api/v1/calendar",
			"/api/v1/library/series",
			"/api/v1/library/series/" + itoa(site.ID),
			"/api/v1/search?q=brazzers",
		} {
			rec := doAuth(t, h, http.MethodGet, path, "", withCookie(cookie))
			if rec.Code != http.StatusForbidden {
				t.Errorf("GET %s = %d, want 403", path, rec.Code)
			}
		}
	})
}

// An ADMIN on a server with the module switched off is the other half of the
// rule: their own scene rows go quiet rather than reappearing as evidence of a
// module they turned off.
func TestDisablingTheModuleHidesSceneRequestsFromTheAdminToo(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{scenes: fakeScenes()}
	enableAdult(t, st)
	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"scene","stash_id":"scene-1","title":"Deep Impact"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusCreated)
	var created requestJSON
	decodeBody(t, rec, &created)

	rec = doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(cookie))
	if !strings.Contains(rec.Body.String(), "Deep Impact") {
		t.Fatalf("the admin cannot see their own scene request: %s", rec.Body.String())
	}

	wantStatus(t, doAuth(t, h, http.MethodPost, "/api/v1/settings/adult",
		`{"enabled":false}`, withCookie(cookie)), http.StatusOK)

	rec = doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "Deep Impact") {
		t.Errorf("a scene row survived the module being switched off: %s", rec.Body.String())
	}
	// And the row itself is unreachable by id, with the 404 an unissued id gets.
	wantStatus(t, doAuth(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve",
		"{}", withCookie(cookie)), http.StatusNotFound)

	// Nothing was deleted: turning it back on finds the row as it was.
	wantStatus(t, doAuth(t, h, http.MethodPost, "/api/v1/settings/adult",
		`{"enabled":true}`, withCookie(cookie)), http.StatusOK)
	rec = doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(cookie))
	if !strings.Contains(rec.Body.String(), "Deep Impact") {
		t.Errorf("disabling deleted the scene request: %s", rec.Body.String())
	}
}

// ---------------------------------------------------------------------------
// Enabled and granted: the member's whole journey.
// ---------------------------------------------------------------------------

func TestGrantedMemberCanDiscoverAndRequestAScene(t *testing.T) {
	h, st, mgr := newTestServer(t)
	provider := &fakeAdultProvider{
		sites:  []core.SiteMeta{{StashID: "site-1", Name: "Brazzers"}},
		scenes: fakeScenes(),
	}
	mgr.adult = provider
	enableAdult(t, st)

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	adminCookie := login(t, h, testAdmin, testPassword)

	// Ungranted first: the nav is off and discover is a 404.
	cookie := login(t, h, testMember, testPassword)
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/adult/discover", "", withCookie(cookie)),
		http.StatusNotFound)

	// The admin grants access through the member-access card.
	rec := doAuth(t, h, http.MethodPut, "/api/v1/adult/users/"+itoa(member.ID)+"/access",
		`{"granted":true}`, withCookie(adminCookie))
	wantStatus(t, rec, http.StatusOK)

	// No re-login: requireAuth reads the grant per request.
	rec = doAuth(t, h, http.MethodGet, "/api/v1/auth/me", "", withCookie(cookie))
	var me meResponse
	decodeBody(t, rec, &me)
	if !me.Adult {
		t.Fatal("a granted member is not told the module is theirs")
	}

	rec = doAuth(t, h, http.MethodGet, "/api/v1/adult/discover?q=deep", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var discover struct {
		Scenes []sceneMetaJSON `json:"scenes"`
	}
	decodeBody(t, rec, &discover)
	if len(discover.Scenes) != 1 || discover.Scenes[0].StashID != "scene-1" {
		t.Fatalf("discover = %+v, want the one canned scene", discover.Scenes)
	}
	if discover.Scenes[0].Requested || discover.Scenes[0].InLibrary {
		t.Errorf("a scene nobody asked for reads as requested/in-library: %+v", discover.Scenes[0])
	}
	if got := discover.Scenes[0].Performers; len(got) != 1 || got[0] != "Janie" {
		// The credited alias, not the canonical name: that is what the scene's
		// own page shows and what a release filename carries.
		t.Errorf("performers = %v, want the credited alias", got)
	}

	rec = doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"scene","stash_id":"scene-1","title":"Deep Impact"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusCreated)
	var created requestJSON
	decodeBody(t, rec, &created)
	if created.StashID != "scene-1" || created.TMDBID != 0 {
		t.Fatalf("request = %+v, want a stash-identified scene", created)
	}

	// The decoration comes back on the next discover load, which is the whole
	// reason the state is read per response.
	rec = doAuth(t, h, http.MethodGet, "/api/v1/adult/discover?q=deep", "", withCookie(cookie))
	decodeBody(t, rec, &discover)
	if !discover.Scenes[0].Requested {
		t.Error("a requested scene does not read as requested")
	}

	// Approving adds the SITE the scene belongs to, and closes the request.
	rec = doAuth(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve",
		"{}", withCookie(adminCookie))
	wantStatus(t, rec, http.StatusOK)
	if calls := mgr.siteCalls(); len(calls) != 1 || calls[0] != "site-1" {
		t.Fatalf("AddSite calls = %v, want exactly the scene's site", calls)
	}
	stored, err := st.GetRequest(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if stored.Status != core.RequestApproved {
		t.Errorf("status = %q, want approved", stored.Status)
	}
	// The provider was asked for the scene, which is how the site was found.
	if !containsCall(provider.callLog(), "GetScene scene-1") {
		t.Errorf("approval did not resolve the scene's site: %v", provider.callLog())
	}
}

// A granted member reads the Adult screens; writing to them is still an admin's
// job, and the refusal is the ordinary member 403 rather than anything that
// names the module.
func TestGrantedMemberReadsTheAdultScreensButCannotWrite(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{sites: []core.SiteMeta{{StashID: "site-1", Name: "Brazzers"}}}
	enableAdult(t, st)
	site := seedSite(t, st)
	// A television series in the same table, so the grid's kind filter has
	// something to leave out. Without it the assertion below would hold for a
	// listing that did not filter at all.
	if err := st.UpsertSeries(context.Background(), &core.Series{
		TMDBID: 68507, Title: "Planet Earth II", SortTitle: "planet earth ii", Monitored: true,
	}); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	if err := st.SetUserAdultAccess(context.Background(), member.ID, true); err != nil {
		t.Fatalf("SetUserAdultAccess: %v", err)
	}
	cookie := login(t, h, testMember, testPassword)

	rec := doAuth(t, h, http.MethodGet, "/api/v1/adult/sites", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var list struct {
		Sites []siteJSON `json:"sites"`
	}
	decodeBody(t, rec, &list)
	if len(list.Sites) != 1 || list.Sites[0].Title != "Brazzers" {
		t.Fatalf("sites = %+v, want the seeded site", list.Sites)
	}
	if list.Sites[0].SceneCount != 1 || list.Sites[0].SceneFileCount != 0 {
		t.Errorf("badge = %d/%d, want 0/1", list.Sites[0].SceneFileCount, list.Sites[0].SceneCount)
	}

	rec = doAuth(t, h, http.MethodGet, "/api/v1/adult/sites/"+itoa(site.ID), "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var detail siteDetailJSON
	decodeBody(t, rec, &detail)
	if len(detail.Years) != 1 || detail.Years[0].Year != 2022 {
		t.Fatalf("years = %+v, want one release year", detail.Years)
	}
	scene := detail.Years[0].Scenes[0]
	if scene.Number != 1 || scene.ReleaseDate != "2022-03-14" || scene.Title != "Deep Impact" {
		t.Errorf("scene row = %+v", scene)
	}
	if len(scene.Performers) != 1 || scene.Performers[0] != "Janie" {
		t.Errorf("performers = %v", scene.Performers)
	}

	// Writes and the admin-only screens are refused, with the generic member
	// answer rather than one that mentions the module.
	for _, route := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/adult/sites", `{"stash_id":"site-1"}`},
		{http.MethodGet, "/api/v1/adult/search?q=brazzers", ""},
		{http.MethodGet, "/api/v1/adult/users", ""},
		{http.MethodPut, "/api/v1/adult/users/" + itoa(member.ID) + "/access", `{"granted":true}`},
		{http.MethodPost, "/api/v1/settings/adult", `{"enabled":false}`},
	} {
		rec := doAuth(t, h, route.method, route.path, route.body, withCookie(cookie))
		wantStatus(t, rec, http.StatusForbidden)
	}
}

// A television series id handed to the adult site endpoint must not become a
// second way to read the television library.
func TestSiteEndpointRefusesATelevisionSeries(t *testing.T) {
	h, st, _ := newTestServer(t)
	enableAdult(t, st)
	tv := &core.Series{TMDBID: 42, Title: "Some Show", SortTitle: "some show", Monitored: true}
	if err := st.UpsertSeries(context.Background(), tv); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/adult/sites/"+itoa(tv.ID), "")
	wantStatus(t, rec, http.StatusNotFound)

	// And the Series screen still holds no site, which is the mirror rule.
	site := seedSite(t, st)
	rec = do(t, h, http.MethodGet, "/api/v1/library/series", "")
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "Brazzers") {
		t.Errorf("the Series screen holds a site: %s", rec.Body.String())
	}
	rec = do(t, h, http.MethodGet, "/api/v1/adult/sites/"+itoa(site.ID), "")
	wantStatus(t, rec, http.StatusOK)
}

// ---------------------------------------------------------------------------
// The master switch.
// ---------------------------------------------------------------------------

func TestSettingsAdultSwitchCreatesTheLibraryAndIsAdminOnly(t *testing.T) {
	h, st, _ := newTestServer(t)
	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	if err := st.SetUserAdultAccess(context.Background(), member.ID, true); err != nil {
		t.Fatalf("SetUserAdultAccess: %v", err)
	}
	adminCookie := login(t, h, testAdmin, testPassword)
	memberCookie := login(t, h, testMember, testPassword)

	// Even a GRANTED member may not flip the server-wide switch: the grant is
	// permission to see the module, not to install it.
	wantStatus(t, doAuth(t, h, http.MethodPost, "/api/v1/settings/adult",
		`{"enabled":true}`, withCookie(memberCookie)), http.StatusForbidden)

	// An absent field is a client bug, not a silent switch-off.
	rec := doAuth(t, h, http.MethodPost, "/api/v1/settings/adult", `{}`, withCookie(adminCookie))
	wantStatus(t, rec, http.StatusBadRequest)
	if enabled, err := st.AdultEnabled(context.Background()); err != nil || enabled {
		t.Fatalf("AdultEnabled = %v (err %v), want off after a malformed save", enabled, err)
	}

	wantStatus(t, doAuth(t, h, http.MethodPost, "/api/v1/settings/adult",
		`{"enabled":true}`, withCookie(adminCookie)), http.StatusOK)

	lib, err := st.GetLibraryByKind(context.Background(), core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	if lib.RootPath != store.AdultLibraryRoot {
		t.Errorf("adult root = %q, want %q", lib.RootPath, store.AdultLibraryRoot)
	}
	if lib.DLNAVisible {
		t.Error("the Adult library was born advertised on the LAN")
	}
}

// PUT /settings must still refuse the switch: it cannot create the library row,
// so accepting the key would leave the module half on.
func TestPutSettingsStillRefusesTheAdultKey(t *testing.T) {
	h, _, _ := newTestServer(t)
	rec := do(t, h, http.MethodPut, "/api/v1/settings", `{"adult_enabled":"true"}`)
	wantStatus(t, rec, http.StatusBadRequest)
}

// ---------------------------------------------------------------------------
// The member-access card.
// ---------------------------------------------------------------------------

// GET /users is reachable on every install, so it must carry nothing adult:
// an "adult_access" key on an install that never enabled the module is exactly
// the trace this phase promises not to leave.
func TestTheAccountsAPICarriesNoAdultField(t *testing.T) {
	h, st, _ := newTestServer(t)
	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	createUser(t, st, testMember, testPassword, core.RoleMember)
	cookie := login(t, h, testAdmin, testPassword)

	for _, enabled := range []bool{false, true} {
		if enabled {
			enableAdult(t, st)
		}
		rec := doAuth(t, h, http.MethodGet, "/api/v1/users", "", withCookie(cookie))
		wantStatus(t, rec, http.StatusOK)
		if strings.Contains(strings.ToLower(rec.Body.String()), "adult") {
			t.Errorf("GET /users mentions the module (adult enabled=%v): %s", enabled, rec.Body.String())
		}
	}
}

func TestAdultUsersCardReportsGrantsAndAdminsAreAlwaysGranted(t *testing.T) {
	h, st, _ := newTestServer(t)
	enableAdult(t, st)
	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	cookie := login(t, h, testAdmin, testPassword)

	read := func() map[string]adultUserJSON {
		t.Helper()
		rec := doAuth(t, h, http.MethodGet, "/api/v1/adult/users", "", withCookie(cookie))
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Users []adultUserJSON `json:"users"`
		}
		decodeBody(t, rec, &body)
		out := map[string]adultUserJSON{}
		for _, u := range body.Users {
			out[u.Username] = u
		}
		return out
	}

	rows := read()
	if !rows[testAdmin].AlwaysGranted {
		t.Error("the admin row does not say it always has access")
	}
	if rows[testMember].AlwaysGranted || rows[testMember].Granted {
		t.Errorf("a fresh member row = %+v, want no grant", rows[testMember])
	}

	wantStatus(t, doAuth(t, h, http.MethodPut, "/api/v1/adult/users/"+itoa(member.ID)+"/access",
		`{"granted":true}`, withCookie(cookie)), http.StatusOK)
	if !read()[testMember].Granted {
		t.Error("the grant did not stick")
	}

	// Revoking takes effect on the very next request, with no logout.
	memberCookie := login(t, h, testMember, testPassword)
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/adult/sites", "", withCookie(memberCookie)),
		http.StatusOK)
	wantStatus(t, doAuth(t, h, http.MethodPut, "/api/v1/adult/users/"+itoa(member.ID)+"/access",
		`{"granted":false}`, withCookie(cookie)), http.StatusOK)
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/adult/sites", "", withCookie(memberCookie)),
		http.StatusNotFound)
}

// ---------------------------------------------------------------------------
// GET /search is the television search and stays that way.
// ---------------------------------------------------------------------------

// The provider search endpoint reaches TMDB and nothing else. A `type=scene`
// that quietly worked would put adult results on the add-to-library picker,
// which is an admin screen with no adult gate on it at all.
func TestTelevisionSearchNeverReachesTheAdultProvider(t *testing.T) {
	h, st, mgr := newTestServer(t)
	provider := &fakeAdultProvider{sites: []core.SiteMeta{{StashID: "site-1", Name: "Brazzers"}}, scenes: fakeScenes()}
	mgr.adult = provider
	mgr.provider = &stubProvider{}
	enableAdult(t, st)
	seedSite(t, st)

	for _, target := range []string{
		"/api/v1/search?q=brazzers",
		"/api/v1/search?q=brazzers&type=scene",
		"/api/v1/search?q=brazzers&type=series",
	} {
		rec := do(t, h, http.MethodGet, target, "")
		if rec.Code != http.StatusOK && rec.Code != http.StatusBadRequest {
			t.Errorf("GET %s = %d", target, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "Brazzers") {
			t.Errorf("GET %s answered with a site: %s", target, rec.Body.String())
		}
	}
	if calls := provider.callLog(); len(calls) != 0 {
		t.Errorf("GET /search reached the adult provider: %v", calls)
	}
}

// ---------------------------------------------------------------------------
// Helpers.
// ---------------------------------------------------------------------------

func containsCall(calls []string, want string) bool {
	for _, c := range calls {
		if c == want {
			return true
		}
	}
	return false
}

// memberAllowed is the allowlist every adult route's reachability rests on, so
// it is asserted directly as well as through the handlers: a route silently
// dropped from it would leave a granted member with a nav item that 403s, and
// one silently added would open a write endpoint to the household.
func TestMemberAllowlistNamesExactlyTheAdultReadRoutes(t *testing.T) {
	allowed := []string{
		http.MethodGet + " /adult/sites",
		http.MethodGet + " /adult/sites/7",
		http.MethodGet + " /adult/discover",
	}
	refused := []string{
		http.MethodPost + " /adult/sites",
		http.MethodDelete + " /adult/sites/7",
		http.MethodGet + " /adult/search",
		http.MethodGet + " /adult/users",
		http.MethodPut + " /adult/users/7/access",
		http.MethodPost + " /settings/adult",
	}
	for _, entry := range allowed {
		method, path, _ := strings.Cut(entry, " ")
		if !memberAllowed(method, path) {
			t.Errorf("memberAllowed(%q) = false, want true", entry)
		}
	}
	for _, entry := range refused {
		method, path, _ := strings.Cut(entry, " ")
		if memberAllowed(method, path) {
			t.Errorf("memberAllowed(%q) = true, want false", entry)
		}
	}
}

// The routes a member may reach must all actually exist on the adult mux, or
// the allowlist is naming something that will answer 404 forever.
func TestEveryMemberAllowedAdultRouteIsRegistered(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{}
	enableAdult(t, st)
	site := seedSite(t, st)

	for _, target := range []string{
		"/api/v1/adult/sites",
		"/api/v1/adult/sites/" + itoa(site.ID),
		"/api/v1/adult/discover",
	} {
		rec := do(t, h, http.MethodGet, target, "")
		if rec.Code == http.StatusNotFound {
			t.Errorf("GET %s is allowlisted but unrouted", target)
		}
	}
}

// requestJSON must carry the stash id, or a scene row on the requests screen is
// a row the client cannot identify.
func TestSceneRequestRoundTripsItsStashID(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{scenes: fakeScenes()}
	enableAdult(t, st)

	rec := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"scene","stash_id":"scene-1","title":"Deep Impact","year":2022}`)
	wantStatus(t, rec, http.StatusCreated)

	rec = do(t, h, http.MethodGet, "/api/v1/requests", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Requests []requestJSON `json:"requests"`
	}
	decodeBody(t, rec, &body)
	if len(body.Requests) != 1 || body.Requests[0].StashID != "scene-1" {
		t.Fatalf("requests = %+v", body.Requests)
	}
	_ = st
}

// The two id namespaces must not be mixable from the wire: the table's CHECK
// enforces it, and a 500 from a constraint violation is a worse answer than a
// 400 from the handler.
func TestSceneAndTitleRequestsCannotMixTheirIDs(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{}
	enableAdult(t, st)

	for _, tc := range []struct{ name, body string }{
		{"a scene with a tmdb id", `{"media_type":"scene","stash_id":"scene-1","tmdb_id":9,"title":"x"}`},
		{"a scene with no stash id", `{"media_type":"scene","title":"x"}`},
		{"a series with a stash id", `{"media_type":"series","tmdb_id":9,"stash_id":"scene-1","title":"x"}`},
		{"a scene with seasons", `{"media_type":"scene","stash_id":"scene-1","title":"x","seasons":[1]}`},
		{"a scene with min availability", `{"media_type":"scene","stash_id":"scene-1","title":"x","min_availability":"released"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/v1/requests", tc.body)
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
		})
	}

	rows, err := st.ListRequests(context.Background(), "")
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(rows) != 0 {
		t.Fatalf("a refused request was written anyway: %+v", rows)
	}
}

// A scene already in the library cannot be requested again, the same way a
// title cannot: nothing would ever absorb the row.
func TestRequestingASceneAlreadyInTheLibraryIsRefused(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{scenes: fakeScenes()}
	enableAdult(t, st)
	seedSite(t, st)

	rec := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"scene","stash_id":"scene-1","title":"Deep Impact"}`)
	wantStatus(t, rec, http.StatusConflict)
}

// The discover decoration is read from the library, so a scene already held
// reads as held.
func TestAdultDiscoverMarksScenesAlreadyInTheLibrary(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{scenes: fakeScenes()}
	enableAdult(t, st)
	seedSite(t, st)

	rec := do(t, h, http.MethodGet, "/api/v1/adult/discover", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Scenes []sceneMetaJSON `json:"scenes"`
	}
	decodeBody(t, rec, &body)
	if len(body.Scenes) != 1 || !body.Scenes[0].InLibrary || body.Scenes[0].LibraryID == 0 {
		t.Fatalf("scenes = %+v, want the held scene marked", body.Scenes)
	}
}

// Without a stash-box credential the adult screens say so rather than pretending
// there is nothing to find — the same 503 GET /search gives for a missing TMDB
// key.
func TestAdultProviderScreensReportAMissingCredential(t *testing.T) {
	h, st, _ := newTestServer(t)
	enableAdult(t, st)

	for _, target := range []string{"/api/v1/adult/search?q=x", "/api/v1/adult/discover"} {
		rec := do(t, h, http.MethodGet, target, "")
		wantStatus(t, rec, http.StatusServiceUnavailable)
	}
	// The library screens still work: they read rows, not the provider.
	wantStatus(t, do(t, h, http.MethodGet, "/api/v1/adult/sites", ""), http.StatusOK)
}

// The site picker says which hits are already tracked, so an admin does not add
// the same site twice.
func TestSiteSearchMarksSitesAlreadyInTheLibrary(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{sites: []core.SiteMeta{
		{StashID: "site-1", Name: "Brazzers", Aliases: []string{"BRZ"}},
		{StashID: "site-2", Name: "Elsewhere"},
	}}
	enableAdult(t, st)
	site := seedSite(t, st)

	rec := do(t, h, http.MethodGet, "/api/v1/adult/search?q=b", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Sites []siteMetaJSON `json:"sites"`
	}
	decodeBody(t, rec, &body)
	sort.Slice(body.Sites, func(i, j int) bool { return body.Sites[i].StashID < body.Sites[j].StashID })
	if len(body.Sites) != 2 {
		t.Fatalf("sites = %+v", body.Sites)
	}
	if !body.Sites[0].InLibrary || body.Sites[0].LibraryID != site.ID {
		t.Errorf("the held site is not marked: %+v", body.Sites[0])
	}
	if body.Sites[1].InLibrary {
		t.Errorf("an unheld site is marked: %+v", body.Sites[1])
	}
	if body.Sites[1].Aliases == nil {
		t.Error("aliases decoded as null, want an empty array")
	}
}

// The add-a-site dialog opens before anything is typed, so a blank query is a
// search of its own: the provider answers it with its own default list. This is
// where it differs from GET /search, which has nothing to ask TMDB for.
func TestSiteSearchWithNoQueryAsksTheProviderForItsDefaultList(t *testing.T) {
	h, st, mgr := newTestServer(t)
	provider := &fakeAdultProvider{sites: []core.SiteMeta{{StashID: "site-9", Name: "Brazzers"}}}
	mgr.adult = provider
	enableAdult(t, st)

	rec := do(t, h, http.MethodGet, "/api/v1/adult/search", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Sites []siteMetaJSON `json:"sites"`
	}
	decodeBody(t, rec, &body)
	if len(body.Sites) != 1 || body.Sites[0].StashID != "site-9" {
		t.Fatalf("sites = %+v, want the provider's default list", body.Sites)
	}
	// The blank query reaches the provider as a blank query rather than being
	// turned into some stand-in term the endpoint would match against.
	if calls := provider.callLog(); len(calls) != 1 || calls[0] != "SearchSites " {
		t.Errorf("provider calls = %v, want one blank SearchSites", calls)
	}
}

// The provider id on a site's page is a link out, and where it points depends on
// which endpoint is configured — which is why the server derives it: the setting
// is admin-only, and this page is one a granted member reads.
func TestSiteDetailCarriesTheProviderLink(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{
			// TPDB files a site under /sites; it is the default endpoint.
			name: "the default endpoint",
			want: "https://theporndb.net/sites/site-1",
		},
		{
			name:     "a stash-box keeps the /studios convention",
			endpoint: "https://stashdb.org/graphql",
			want:     "https://stashdb.org/studios/site-1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, st, _ := newTestServer(t)
			enableAdult(t, st)
			if tt.endpoint != "" {
				if err := st.SetSetting(context.Background(), store.SettingStashboxEndpoint, tt.endpoint); err != nil {
					t.Fatalf("set endpoint: %v", err)
				}
			}
			site := seedSite(t, st)

			rec := do(t, h, http.MethodGet, "/api/v1/adult/sites/"+itoa(site.ID), "")
			wantStatus(t, rec, http.StatusOK)
			var body siteDetailJSON
			decodeBody(t, rec, &body)
			if body.ProviderURL != tt.want {
				t.Errorf("provider_url = %q, want %q", body.ProviderURL, tt.want)
			}
		})
	}
}

// Adding a site goes through the manager, which is the one thing that can walk
// a catalogue.
func TestAddSiteUsesTheManager(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{}
	enableAdult(t, st)

	wantStatus(t, do(t, h, http.MethodPost, "/api/v1/adult/sites", `{"stash_id":"  "}`),
		http.StatusBadRequest)

	rec := do(t, h, http.MethodPost, "/api/v1/adult/sites", `{"stash_id":"site-9"}`)
	wantStatus(t, rec, http.StatusCreated)
	var site siteJSON
	decodeBody(t, rec, &site)
	if site.StashID != "site-9" {
		t.Fatalf("site = %+v", site)
	}
	if calls := mgr.siteCalls(); len(calls) != 1 || calls[0] != "site-9" {
		t.Fatalf("AddSite calls = %v", calls)
	}
}

// The site DTO must not offer television fields a site can never fill in.
func TestSiteDTOOffersNoTelevisionFields(t *testing.T) {
	raw, err := json.Marshal(siteDTO(core.Series{Title: "Brazzers", Kind: core.SeriesKindAdult}))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, field := range []string{"tmdb_id", "tvdb_id", "imdb_id", "first_aired", "status", "year"} {
		if strings.Contains(string(raw), `"`+field+`"`) {
			t.Errorf("siteJSON carries %q, which a site never has: %s", field, raw)
		}
	}
}

// ---------------------------------------------------------------------------
// The shared by-id series routes (PLAN phase 9 task 3).
// ---------------------------------------------------------------------------

// seriesByIDRoutes is every route that takes a series id — or an id belonging
// to one of its children — and can therefore be handed a SITE's id. The list is
// written out rather than discovered, for the reason adultRoutes gives: what is
// under test is that the SET is closed and every member of it is gated.
func seriesByIDRoutes(seriesID, episodeID int64) []struct{ method, path, body string } {
	id := itoa(seriesID)
	return []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/library/series/" + id, ""},
		{http.MethodPatch, "/api/v1/library/series/" + id, `{"monitored":false}`},
		{http.MethodDelete, "/api/v1/library/series/" + id, ""},
		{http.MethodPatch, "/api/v1/library/series/" + id + "/seasons/2022", `{"monitored":false}`},
		{http.MethodPatch, "/api/v1/library/episodes/" + itoa(episodeID), `{"monitored":false}`},
		{http.MethodGet, "/api/v1/library/series/" + id + "/releases?season=2022&episode=1", ""},
		{http.MethodPost, "/api/v1/library/series/" + id + "/grab?season=2022&episode=1", `{"release_id":1}`},
		{http.MethodPost, "/api/v1/library/series/" + id + "/search", ""},
	}
}

// With the module off, GET /library/series/{siteID} used to answer 200 with the
// site's title, its root under library/Adult and its whole season/episode tree
// — scene titles and air dates — which the SPA renders as an ordinary
// television detail page. handleListSeries and handleSystemStatus were narrowed
// to television precisely to remove that trace; these routes are the same trace
// reachable by id.
func TestSeriesByIDRoutesAre404ForASiteWhenTheModuleIsDisabled(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{scenes: fakeScenes()}
	enableAdult(t, st)
	site := seedSite(t, st)
	scene := siteScene(t, st, site.ID)

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	cookie := login(t, h, testAdmin, testPassword)

	// The gate is the SWITCH, not the seeding: the rows stay, as
	// store.SetAdultEnabled promises.
	if err := st.SetAdultEnabled(context.Background(), false); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}

	control := doAuth(t, h, http.MethodGet, "/api/v1/library/series/9999", "", withCookie(cookie))
	wantStatus(t, control, http.StatusNotFound)

	for _, route := range seriesByIDRoutes(site.ID, scene.ID) {
		rec := doAuth(t, h, route.method, route.path, route.body, withCookie(cookie))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404 (body %q)",
				route.method, route.path, rec.Code, rec.Body.String())
		}
		// And the refusal reads like an unknown id, never "this exists and you
		// may not have it".
		if rec.Body.String() != control.Body.String() {
			t.Errorf("%s %s answered %q, want the unknown-id body %q",
				route.method, route.path, rec.Body.String(), control.Body.String())
		}
	}

	// Nothing about the site leaked into any of those bodies, and the rows are
	// still there for when the module comes back on.
	sr, err := st.GetSeries(context.Background(), site.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if !sr.Monitored {
		t.Error("a refused PATCH still un-monitored the site")
	}
}

// The other half: an ungranted member is refused by these routes too. They are
// admin-only anyway (memberAllowed names none of them), so this is belt and
// braces — but the 403 must not become a 200 if the allowlist ever widens.
func TestSeriesByIDRoutesRefuseAnUngrantedMember(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{scenes: fakeScenes()}
	enableAdult(t, st)
	site := seedSite(t, st)
	scene := siteScene(t, st, site.ID)

	createUser(t, st, testMember, testPassword, core.RoleMember)
	cookie := login(t, h, testMember, testPassword)

	for _, route := range seriesByIDRoutes(site.ID, scene.ID) {
		rec := doAuth(t, h, route.method, route.path, route.body, withCookie(cookie))
		if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 404 or 403 (body %q)",
				route.method, route.path, rec.Code, rec.Body.String())
		}
	}
}

// A television series is untouched by the gate: it is the same handler, and a
// regression that closed these routes for everything would be invisible to the
// assertions above.
func TestSeriesByIDRoutesStillAnswerForTelevision(t *testing.T) {
	h, st, _ := newTestServer(t)
	enableAdult(t, st)
	seedSite(t, st)
	sr, _ := addSeries(t, st, "Planet Earth II")

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodGet, "/api/v1/library/series/"+itoa(sr.ID), "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), "Planet Earth II") {
		t.Errorf("television series detail = %s, want the series", rec.Body.String())
	}
}

// siteScene adds a second scene to a seeded site and returns it. seedSite's own
// scene is fine for reading; this one exists so a PATCH test has a row it can
// be handed by episode id.
func siteScene(t *testing.T, st *store.Store, seriesID int64) core.Episode {
	t.Helper()
	e := core.Episode{
		SeriesID: seriesID, SeasonNumber: 2022, EpisodeNumber: 2, StashID: "scene-2",
		Title: "Shallow Impact", AirDate: time.Date(2022, time.April, 18, 0, 0, 0, 0, time.UTC),
		Monitored: true,
	}
	if err := st.UpsertEpisode(context.Background(), &e); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	return e
}

// ---------------------------------------------------------------------------
// The interactive picker for a scene (PLAN phase 9 task 3).
// ---------------------------------------------------------------------------

// routingEngineProvider hands out a different engine per library kind and
// records which kind was asked for. The recording is the assertion: with a
// plain single-engine provider a misrouted grab is invisible, because every
// kind resolves to the same engine.
type routingEngineProvider struct {
	byKind map[string]*stubEngine

	mu    sync.Mutex
	asked []string
}

func (p *routingEngineProvider) Engine() core.Engine { return p.byKind[core.LibraryKindTV] }

func (p *routingEngineProvider) Name() string { return "routing-stub" }

func (p *routingEngineProvider) EngineFor(kind string) core.Engine {
	p.mu.Lock()
	p.asked = append(p.asked, kind)
	p.mu.Unlock()
	return p.byKind[kind]
}

func (p *routingEngineProvider) kindsAsked() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.asked...)
}

// A site is a series row, so GET /library/series/{id}/releases can be handed
// one. It must be searched as a SCENE: the adult library's indexers and
// categories, and a query built from the release date rather than an SxxEyy no
// indexer has ever published for a scene.
func TestInteractiveReleaseSearchForASiteIsAnAdultSearch(t *testing.T) {
	fake := newFakeIndexer(t)
	h, st, _ := newTestServer(t, WithIndexerClients(fake.factory()))
	enableAdult(t, st)
	site := seedSite(t, st)
	// The indexer's own categories, television and movies, with no per-library
	// override: the default an install that just enabled the module has.
	addIndexer(t, st, fake, "alpha", 5000, 2000)

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodGet,
		"/api/v1/library/series/"+itoa(site.ID)+"/releases?season=2022&episode=1", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)

	searches := fake.recorded()
	if len(searches) != 1 {
		t.Fatalf("searches = %+v, want one", searches)
	}
	// "Brazzers 22.03.14" — the site and the scene's release date, the way
	// scene releases are named and the same string searchScene builds.
	if searches[0].query != "Brazzers 22.03.14" {
		t.Errorf("query = %q, want the site and the scene's release date", searches[0].query)
	}
	if searches[0].cats != "6000" {
		t.Errorf("cats = %q, want the adult library's categories", searches[0].cats)
	}
	if strings.Contains(searches[0].cats, "5000") || strings.Contains(searches[0].cats, "2000") {
		t.Errorf("a scene search sent the television library's categories: %q", searches[0].cats)
	}
}

// A television series through the same handler is unchanged: same route, same
// code path up to the kind check.
func TestInteractiveReleaseSearchForATelevisionSeriesIsUnchanged(t *testing.T) {
	fake := newFakeIndexer(t)
	h, st, _ := newTestServer(t, WithIndexerClients(fake.factory()))
	enableAdult(t, st)
	seedSite(t, st)
	sr, _ := addSeries(t, st, "Planet Earth II")
	addIndexer(t, st, fake, "alpha", 5000)

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodGet,
		"/api/v1/library/series/"+itoa(sr.ID)+"/releases?season=1&episode=2", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)

	searches := fake.recorded()
	if len(searches) != 1 || searches[0].query != "Planet Earth II S01E02" {
		t.Fatalf("searches = %+v, want the television query", searches)
	}
	if searches[0].cats != "5000" {
		t.Errorf("cats = %q, want the television library's categories", searches[0].cats)
	}
}

// And the grab: a scene picked by hand goes through the ADULT library's engine
// under the adult label, exactly where automation.grabEpisode sends one the
// backlog sweep found. Landing it in the television library's download folder
// under category "tv" is silent — importDownloadedEpisodes branches on kind and
// still imports it — which is why this is asserted rather than noticed.
func TestInteractiveGrabForASiteRoutesThroughTheAdultLibrary(t *testing.T) {
	adultEngine, tvEngine := &stubEngine{}, &stubEngine{}
	provider := &routingEngineProvider{byKind: map[string]*stubEngine{
		core.LibraryKindAdult: adultEngine,
		core.LibraryKindTV:    tvEngine,
	}}
	h, st, _ := newTestServer(t, WithEngine(provider))
	enableAdult(t, st)
	site := seedSite(t, st)
	rel := cacheRelease(t, st, "Brazzers.22.03.14.1080p")

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodPost,
		"/api/v1/library/series/"+itoa(site.ID)+"/grab?season=2022&episode=1",
		`{"release_id":`+itoa(rel.ID)+`}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusCreated)

	if got := provider.kindsAsked(); len(got) != 1 || got[0] != core.LibraryKindAdult {
		t.Fatalf("engine resolved for kinds %v, want only the adult library", got)
	}
	if adds := tvEngine.addCalls(); len(adds) != 0 {
		t.Fatalf("a scene grab reached the television engine: %+v", adds)
	}
	adds := adultEngine.addCalls()
	if len(adds) != 1 {
		t.Fatalf("adult engine adds = %d, want 1", len(adds))
	}
	if adds[0].opts.Category != engineCategoryAdult {
		t.Errorf("engine category = %q, want %q", adds[0].opts.Category, engineCategoryAdult)
	}
}

// A scene request's poster_path is not a TMDB-relative path: stashbox.coverURL
// hands out an ABSOLUTE url and AdultScenes.svelte sends it verbatim. Rendering
// it through the TMDB image base concatenates the two into
// "https://image.tmdb.org/t/p/w500/https://cdn…/scene.jpg" — a dead image the
// browser still fetches from TMDB's CDN with the adult path in the request line.
func TestSceneRequestPosterURLIsTheProvidersOwnURL(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{scenes: fakeScenes()}
	// A configured TMDB provider, so the failure is the concatenation rather
	// than a missing provider.
	mgr.provider = &stubDiscoverProvider{}
	enableAdult(t, st)

	const cover = "https://cdn.example.test/scene/scene-1.jpg"
	rec := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"scene","stash_id":"scene-1","title":"Deep Impact","year":2022,`+
			`"poster_path":"`+cover+`"}`)
	wantStatus(t, rec, http.StatusCreated)
	var created requestJSON
	decodeBody(t, rec, &created)
	if created.PosterURL != cover {
		t.Errorf("poster_url = %q, want the stash-box cover url unchanged", created.PosterURL)
	}

	// The list is the screen the finding is about, and it renders through the
	// same helper — assert it too rather than trusting they stay together.
	rec = do(t, h, http.MethodGet, "/api/v1/requests", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Requests []requestJSON `json:"requests"`
	}
	decodeBody(t, rec, &body)
	if len(body.Requests) != 1 {
		t.Fatalf("requests = %+v, want one", body.Requests)
	}
	if body.Requests[0].PosterURL != cover {
		t.Errorf("listed poster_url = %q, want the stash-box cover url", body.Requests[0].PosterURL)
	}
	if strings.Contains(body.Requests[0].PosterURL, "images.test") {
		t.Errorf("a scene poster went through the TMDB image base: %q", body.Requests[0].PosterURL)
	}
}

// The second half of the same line: with no metadata provider a scene row keeps
// its artwork, because the url it stores never needed one. A movie row still
// loses it, which is the existing contract.
func TestSceneRequestKeepsItsPosterWithoutAMetadataProvider(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{scenes: fakeScenes()}
	mgr.provider = nil
	enableAdult(t, st)

	const cover = "https://cdn.example.test/scene/scene-1.jpg"
	rec := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"scene","stash_id":"scene-1","title":"Deep Impact","poster_path":"`+cover+`"}`)
	wantStatus(t, rec, http.StatusCreated)
	var created requestJSON
	decodeBody(t, rec, &created)
	if created.PosterURL != cover {
		t.Errorf("poster_url = %q, want the cover url even with no TMDB key", created.PosterURL)
	}
}

// poster_path is whatever the requesting account's client sent, and a scene row
// is the one kind that renders it unchanged. Only http and https survive, so a
// member cannot use the field to point an admin's browser somewhere else.
func TestSceneRequestPosterURLRefusesANonHTTPScheme(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{scenes: fakeScenes()}
	mgr.provider = &stubDiscoverProvider{}
	enableAdult(t, st)

	for _, bad := range []string{"javascript:alert(1)", "data:image/png;base64,AAAA", "/relative.jpg"} {
		t.Run(bad, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/v1/requests",
				`{"media_type":"scene","stash_id":"scene-`+bad[:4]+`","title":"x","poster_path":`+
					jsonString(bad)+`}`)
			wantStatus(t, rec, http.StatusCreated)
			var created requestJSON
			decodeBody(t, rec, &created)
			if created.PosterURL != "" {
				t.Errorf("poster_url = %q, want no url rendered for %q", created.PosterURL, bad)
			}
		})
	}
}

func jsonString(s string) string {
	b, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(b)
}
