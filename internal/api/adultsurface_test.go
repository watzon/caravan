package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	{http.MethodGet, "/api/v1/adult/scenes/scene-1"},
	{http.MethodGet, "/api/v1/adult/performers?q=mia"},
	{http.MethodGet, "/api/v1/adult/tags?q=anal"},
	{http.MethodGet, "/api/v1/adult/stash"},
	{http.MethodPost, "/api/v1/adult/stash"},
	{http.MethodPost, "/api/v1/adult/stash/test"},
}

// fakeAdultProvider is a canned core.AdultMetadataProvider that records every
// call. The recording is what the "no adult surface reaches the provider"
// assertions are made against — a nil provider would prove only that a nil
// provider is quiet.
type fakeAdultProvider struct {
	sites      []core.SiteMeta
	scenes     []core.SceneMeta
	performers []core.ScenePerformerMeta
	tags       []core.SceneFilterRef
	err        error
	// sceneErr, when set, is what SearchScenes fails with instead of err. It is
	// how the "this endpoint cannot serve that filter" path is exercised
	// without failing every other call too.
	sceneErr error

	mu sync.Mutex
	// lastQuery is the SceneQuery the last SearchScenes was handed. The filter
	// surface is asserted against it: what reaches the provider is the only
	// proof a parameter was not dropped.
	lastQuery core.SceneQuery
	calls     []string
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
	p.mu.Lock()
	p.lastQuery = q
	p.mu.Unlock()
	if p.sceneErr != nil {
		return nil, p.sceneErr
	}
	if p.err != nil {
		return nil, p.err
	}
	return &core.ScenePage{Page: 1, PerPage: 25, Total: len(p.scenes), Scenes: p.scenes}, nil
}

func (p *fakeAdultProvider) SearchPerformers(ctx context.Context, q string) ([]core.ScenePerformerMeta, error) {
	p.record("SearchPerformers " + q)
	return p.performers, p.err
}

func (p *fakeAdultProvider) SearchTags(ctx context.Context, q string) ([]core.SceneFilterRef, error) {
	p.record("SearchTags " + q)
	return p.tags, p.err
}

// sceneQuery is the last SceneQuery the provider was handed.
func (p *fakeAdultProvider) sceneQuery() core.SceneQuery {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.lastQuery
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

// enableAdult gives the install an adult library, switched on, which is what
// makes the module reachable at all. It returns the row because every test that
// then switches the module off, or names who may see it, does so through that
// library's own routes.
func enableAdult(t *testing.T, st *store.Store) core.Library {
	t.Helper()
	return enableAdultLibrary(t, st)
}

// seedSite puts one site with one scene in the library, the way library.AddSite
// does. It is pinned to the legacy instance, which is what a single-box install
// and every row written before 0026 carry; seedSiteOn pins it elsewhere.
func seedSite(t *testing.T, st *store.Store) *core.Series {
	t.Helper()
	return seedSiteOn(t, st, core.ProviderStashbox, "site-1", "scene-1", "Brazzers")
}

func seedSiteOn(t *testing.T, st *store.Store, providerID, siteID, sceneID, title string) *core.Series {
	t.Helper()
	ctx := context.Background()

	sr := &core.Series{
		Provider: providerID, ProviderRef: siteID,
		StashID: siteID, Title: title, SortTitle: strings.ToLower(title),
		Kind: core.SeriesKindAdult, Monitored: true,
		LibraryID: defaultLibraryID(t, st, core.LibraryKindAdult),
		Path:      store.AdultLibraryRoot + "/" + title,
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
		SeriesID: sr.ID, SeasonNumber: 2022, EpisodeNumber: 1, StashID: sceneID,
		Title: "Deep Impact", AirDate: time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC),
		Monitored: true,
		Scene:     &core.SceneInfo{Studio: title, Performers: []string{"Janie"}, URL: "https://example.test/s/1"},
	}); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}

	return sr
}
func linkSceneFile(t *testing.T, st *store.Store, stashID string) int64 {
	t.Helper()
	ctx := context.Background()
	scene, err := st.GetEpisodeByStashID(ctx, stashID)
	if err != nil {
		t.Fatalf("GetEpisodeByStashID: %v", err)
	}
	file := &core.MediaFile{
		Path: store.AdultLibraryRoot + "/Brazzers/" + stashID + ".mkv",
		Size: 42,
	}
	if err := st.UpsertMediaFile(ctx, file); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	if err := st.LinkEpisodeFile(ctx, scene.ID, file.ID); err != nil {
		t.Fatalf("LinkEpisodeFile: %v", err)
	}
	return scene.ID
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
	grantAdultAccess(t, st, member.ID, true)
	rec := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"scene","stash_id":"scene-2","title":"Another Scene"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusCreated)
	var mine requestJSON
	decodeBody(t, rec, &mine)
	grantAdultAccess(t, st, member.ID, false)

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
	lib := enableAdult(t, st)
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

	wantStatus(t, doAuth(t, h, http.MethodPatch, "/api/v1/libraries/"+itoa(lib.ID),
		`{"active":false}`, withCookie(cookie)), http.StatusOK)

	rec = doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "Deep Impact") {
		t.Errorf("a scene row survived the module being switched off: %s", rec.Body.String())
	}
	// And the row itself is unreachable by id, with the 404 an unissued id gets.
	wantStatus(t, doAuth(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve",
		"{}", withCookie(cookie)), http.StatusNotFound)

	// Nothing was deleted: switching it back on finds the row as it was. The
	// library route is reachable throughout, which is the whole difference
	// between a dormant shelf and a hidden one — see manageableLibrary.
	wantStatus(t, doAuth(t, h, http.MethodPatch, "/api/v1/libraries/"+itoa(lib.ID),
		`{"active":true}`, withCookie(cookie)), http.StatusOK)
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
	// The walk files the scene the request asks for, so the approval has an
	// episode row to monitor.
	mgr.addSiteSceneStashID = "scene-1"
	lib := enableAdult(t, st)

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	adminCookie := login(t, h, testAdmin, testPassword)

	// Ungranted first: the nav is off and discover is a 404.
	cookie := login(t, h, testMember, testPassword)
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/adult/discover", "", withCookie(cookie)),
		http.StatusNotFound)

	// The admin grants access through the library's own access card.
	rec := doAuth(t, h, http.MethodPut, "/api/v1/libraries/"+itoa(lib.ID)+"/access",
		`{"restricted":true,"user_ids":[`+itoa(member.ID)+`]}`, withCookie(adminCookie))
	wantStatus(t, rec, http.StatusOK)

	// No re-login: the gate reads the grant per request.
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
	lib := enableAdult(t, st)
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
	grantAdultAccess(t, st, member.ID, true)
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

	// The Explore rail's Site pill (PLAN phase 12 task 5) drives this one, and
	// the widening ladder only appears once a site is picked — so a member who
	// cannot search sites cannot ask for "this whole network's scenes" at all.
	rec = doAuth(t, h, http.MethodGet, "/api/v1/adult/search?q=brazzers", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var found struct {
		Sites []siteMetaJSON `json:"sites"`
	}
	decodeBody(t, rec, &found)
	if len(found.Sites) != 1 || found.Sites[0].StashID != "site-1" {
		t.Fatalf("site search = %+v, want the provider's hit", found.Sites)
	}

	// Writes and the admin-only screens are refused, with the generic member
	// answer rather than one that mentions the module.
	for _, route := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/adult/sites", `{"stash_id":"site-1"}`},
		{http.MethodGet, "/api/v1/libraries/" + itoa(lib.ID) + "/access", ""},
		{http.MethodPut, "/api/v1/libraries/" + itoa(lib.ID) + "/access",
			`{"restricted":true,"user_ids":[` + itoa(member.ID) + `]}`},
		{http.MethodPatch, "/api/v1/libraries/" + itoa(lib.ID), `{"active":false}`},
	} {
		rec := doAuth(t, h, route.method, route.path, route.body, withCookie(cookie))
		wantStatus(t, rec, http.StatusForbidden)
	}
}

// A site page reads as a publication feed: the newest year sits on top, and
// within a year the newest scene does too.
func TestSitePageListsYearsAndScenesNewestFirst(t *testing.T) {
	h, st, _ := newTestServer(t)
	enableAdult(t, st)
	site := seedSite(t, st)
	ctx := context.Background()
	scenes := make([]*core.Episode, 0, 2)
	for _, e := range []struct {
		number int
		month  time.Month
		day    int
	}{{2, time.June, 2}, {3, time.November, 20}} {
		scene := &core.Episode{
			SeriesID: site.ID, SeasonNumber: 2022, EpisodeNumber: e.number,
			StashID: "scene-" + itoa(int64(e.number)), Title: "Scene " + itoa(int64(e.number)),
			AirDate: time.Date(2022, e.month, e.day, 0, 0, 0, 0, time.UTC), Monitored: true,
		}
		if err := st.UpsertEpisode(ctx, scene); err != nil {
			t.Fatalf("UpsertEpisode: %v", err)
		}
		scenes = append(scenes, scene)
	}
	sharedFile := &core.MediaFile{
		Path: store.AdultLibraryRoot + "/Brazzers/Season 2022/Brazzers - 2022-06-02-11-20.mkv",
		Size: 42,
	}
	if err := st.UpsertMediaFile(ctx, sharedFile); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	for _, scene := range scenes {
		if err := st.LinkEpisodeFile(ctx, scene.ID, sharedFile.ID); err != nil {
			t.Fatalf("LinkEpisodeFile: %v", err)
		}
	}
	if err := st.UpsertSeason(ctx, &core.Season{
		SeriesID: site.ID, Number: 2021, Title: "2021", Monitored: true,
	}); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	if err := st.UpsertEpisode(ctx, &core.Episode{
		SeriesID: site.ID, SeasonNumber: 2021, EpisodeNumber: 1, StashID: "scene-old",
		Title: "Old Scene", AirDate: time.Date(2021, time.May, 5, 0, 0, 0, 0, time.UTC),
		Monitored: true,
	}); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	cookie := login(t, h, testAdmin, testPassword)
	rec := doAuth(t, h, http.MethodGet, "/api/v1/adult/sites/"+itoa(site.ID), "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var detail siteDetailJSON
	decodeBody(t, rec, &detail)

	if len(detail.Years) != 2 || detail.Years[0].Year != 2022 || detail.Years[1].Year != 2021 {
		t.Fatalf("years = %+v, want 2022 before 2021", detail.Years)
	}
	got := make([]int, 0, len(detail.Years[0].Scenes))
	for _, sc := range detail.Years[0].Scenes {
		got = append(got, sc.Number)
	}
	if len(got) != 3 || got[0] != 3 || got[1] != 2 || got[2] != 1 {
		t.Errorf("2022 scene order = %v, want [3 2 1]", got)
	}

	for _, scene := range detail.Years[0].Scenes {
		if scene.Number != 2 && scene.Number != 3 {
			continue
		}
		if got := scene.File; got == nil || got.Path != sharedFile.Path {
			t.Errorf("scene %d file = %+v, want shared file %q", scene.Number, got, sharedFile.Path)
		}
	}
	if detail.SceneFileCount != 2 {
		t.Errorf("scene file count = %d, want 2 covered scenes", detail.SceneFileCount)
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
// Bringing the module into existence.
// ---------------------------------------------------------------------------

// The whole bootstrap, in the order the door now admits.
//
// There is no module switch: an adult library IS the module, so POST /libraries
// is what turns the /adult browse surface on. Stash-box instance CRUD is
// admin metadata, reachable before that library exists, so the Add-library
// warning can be satisfied first.
//
// The row is born restricted and DLNA-dark, which is what the module's own
// switch used to guarantee: the household does not acquire a shelf because
// somebody made one.
func TestCreatingAnAdultLibraryOpensTheModuleAndThenItsInstanceRoutes(t *testing.T) {
	h, st, _ := newTestServer(t)
	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	createUser(t, st, testMember, testPassword, core.RoleMember)
	adminCookie := login(t, h, testAdmin, testPassword)
	memberCookie := login(t, h, testMember, testPassword)

	// Instance routes are admin metadata: they answer before any adult library
	// exists, so Settings → Metadata can collect an endpoint first.
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/adult/stashbox-instances", "",
		withCookie(adminCookie)), http.StatusOK)
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/adult/stashbox-instances", "",
		withCookie(memberCookie)), http.StatusForbidden)

	// A member may not make one. POST /libraries is admin-only by the ordinary
	// rule, and a household member acquiring a shelf for everybody is precisely
	// what that rule is for.
	wantStatus(t, doAuth(t, h, http.MethodPost, "/api/v1/libraries",
		`{"kind":"adult","name":"Scenes","root_path":"library/Scenes"}`,
		withCookie(memberCookie)), http.StatusForbidden)

	// A SECOND adult library, beside the dormant one migration 0011 seeds: the
	// seeded row's own on-switch is `active`, and this covers the other door —
	// creating one, which still opens the module because CreateLibrary forces a
	// new library active.
	//
	// No stash-box instance is configured, and the create is not refused for it:
	// the chain defaults to the bare legacy id, which is the id the first
	// instance ever created is minted with, so it resolves the moment one is.
	rec := doAuth(t, h, http.MethodPost, "/api/v1/libraries",
		`{"kind":"adult","name":"Scenes","root_path":"library/Scenes"}`, withCookie(adminCookie))
	wantStatus(t, rec, http.StatusCreated)
	var created libraryJSON
	decodeBody(t, rec, &created)
	if !created.Active || !created.Restricted || created.DLNAVisible {
		t.Fatalf("new adult library = %+v, want active, restricted and DLNA-dark", created)
	}
	if len(created.Providers) != 1 || created.Providers[0] != core.ProviderStashbox {
		t.Errorf("chain = %v, want the legacy stash-box id alone", created.Providers)
	}

	// And the member still sees nothing of the browse surface: created
	// restricted means the admins alone until somebody is named.
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/adult/discover", "",
		withCookie(memberCookie)), http.StatusNotFound)
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

// The Access card, on the library whose promise of absence it decides.
//
// Admin rows report always_granted rather than a checkbox: an admin bypasses
// restriction (core.LibraryVisible), so a toggle beside their name would
// describe a permission that does nothing — and the person reading the card
// would believe they had removed access they had not.
func TestLibraryAccessCardReportsGrantsAndAdminsAreAlwaysGranted(t *testing.T) {
	h, st, _ := newTestServer(t)
	lib := enableAdult(t, st)
	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	cookie := login(t, h, testAdmin, testPassword)

	read := func() (libraryAccessJSON, map[string]libraryAccessUserJSON) {
		t.Helper()
		rec := doAuth(t, h, http.MethodGet, "/api/v1/libraries/"+itoa(lib.ID)+"/access", "",
			withCookie(cookie))
		wantStatus(t, rec, http.StatusOK)
		var body libraryAccessJSON
		decodeBody(t, rec, &body)
		out := map[string]libraryAccessUserJSON{}
		for _, u := range body.Users {
			out[u.Username] = u
		}
		return body, out
	}

	body, rows := read()
	if !body.Restricted {
		t.Error("the adult library is not restricted")
	}
	if !rows[testAdmin].AlwaysGranted {
		t.Error("the admin row does not say it always has access")
	}
	if rows[testMember].AlwaysGranted || rows[testMember].Granted {
		t.Errorf("a fresh member row = %+v, want no grant", rows[testMember])
	}

	// The flag and the roster travel together, and the answer is the same body
	// a read gives — the screen re-renders the card from the write.
	rec := doAuth(t, h, http.MethodPut, "/api/v1/libraries/"+itoa(lib.ID)+"/access",
		`{"restricted":true,"user_ids":[`+itoa(member.ID)+`]}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var wrote libraryAccessJSON
	decodeBody(t, rec, &wrote)
	if _, rows := read(); !cmpAccess(wrote, rows) {
		t.Errorf("the write answered %+v, which a read does not agree with", wrote)
	}
	if _, rows := read(); !rows[testMember].Granted {
		t.Error("the grant did not stick")
	}

	// Revoking takes effect on the very next request, with no logout: the roster
	// is read per request, so there is no window in which a removed name still
	// opens the shelf.
	memberCookie := login(t, h, testMember, testPassword)
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/adult/sites", "", withCookie(memberCookie)),
		http.StatusOK)
	wantStatus(t, doAuth(t, h, http.MethodPut, "/api/v1/libraries/"+itoa(lib.ID)+"/access",
		`{"restricted":true,"user_ids":[]}`, withCookie(cookie)), http.StatusOK)
	wantStatus(t, doAuth(t, h, http.MethodGet, "/api/v1/adult/sites", "", withCookie(memberCookie)),
		http.StatusNotFound)
}

// cmpAccess reports whether a write's answer says the same thing a read does.
// The two must agree row for row: a screen that re-rendered from the write and
// then refreshed would otherwise show the card changing under it.
func cmpAccess(wrote libraryAccessJSON, read map[string]libraryAccessUserJSON) bool {
	if !wrote.Restricted || len(wrote.Users) != len(read) {
		return false
	}
	for _, u := range wrote.Users {
		if read[u.Username] != u {
			return false
		}
	}
	return true
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
		http.MethodGet + " /adult/scenes/scene-1",
		// The filter rail's three typeaheads. Site search is a provider read
		// like the other two: without it the Site pill 403s for exactly the
		// granted members the rail exists for.
		http.MethodGet + " /adult/search",
		http.MethodGet + " /adult/performers",
		http.MethodGet + " /adult/tags",
	}
	refused := []string{
		http.MethodPost + " /adult/sites",
		http.MethodDelete + " /adult/sites/7",
		http.MethodGet + " /adult/stashbox-instances",
		http.MethodPost + " /adult/stashbox-instances",
		http.MethodPut + " /adult/stashbox-instances/7",
		http.MethodDelete + " /adult/stashbox-instances/7",
		http.MethodPost + " /adult/stashbox-instances/7/test",
		http.MethodGet + " /libraries/7/access",
		http.MethodPut + " /libraries/7/access",
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
	mgr.adult = &fakeAdultProvider{scenes: fakeScenes()}
	enableAdult(t, st)
	site := seedSite(t, st)

	for _, target := range []string{
		"/api/v1/adult/sites",
		"/api/v1/adult/sites/" + itoa(site.ID),
		"/api/v1/adult/discover",
		"/api/v1/adult/scenes/scene-1",
		"/api/v1/adult/search?q=brazzers",
		"/api/v1/adult/performers?q=mia",
		"/api/v1/adult/tags?q=anal",
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

func TestRequestingACataloguedSceneWithoutAFileIsAllowed(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{scenes: fakeScenes()}
	enableAdult(t, st)
	seedSite(t, st)

	rec := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"scene","stash_id":"scene-1","title":"Deep Impact"}`)
	wantStatus(t, rec, http.StatusCreated)
}

// A scene whose file is already in the library cannot be requested again:
// nothing would ever absorb the row.
func TestRequestingASceneAlreadyInTheLibraryIsRefused(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{scenes: fakeScenes()}
	enableAdult(t, st)
	seedSite(t, st)
	linkSceneFile(t, st, "scene-1")

	rec := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"scene","stash_id":"scene-1","title":"Deep Impact"}`)
	wantStatus(t, rec, http.StatusConflict)
}

// Adding a site catalogues every scene as an episode placeholder. Only the
// scene with an attached media file is owned; its neighbours stay requestable.
func TestAdultDiscoverMarksOnlyScenesWithFilesInTheLibrary(t *testing.T) {
	h, st, mgr := newTestServer(t)
	scenes := append(fakeScenes(), core.SceneMeta{
		StashID: "scene-2", SiteStashID: "site-1", SiteName: "Brazzers",
		Title: "Shallow Impact", Date: time.Date(2022, time.April, 18, 0, 0, 0, 0, time.UTC),
	})
	mgr.adult = &fakeAdultProvider{scenes: scenes}
	enableAdult(t, st)
	site := seedSite(t, st)
	siteScene(t, st, site.ID)

	read := func() map[string]sceneMetaJSON {
		t.Helper()
		rec := do(t, h, http.MethodGet, "/api/v1/adult/discover", "")
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Scenes []sceneMetaJSON `json:"scenes"`
		}
		decodeBody(t, rec, &body)
		out := make(map[string]sceneMetaJSON, len(body.Scenes))
		for _, scene := range body.Scenes {
			out[scene.StashID] = scene
		}
		return out
	}

	before := read()
	if before["scene-1"].InLibrary || before["scene-2"].InLibrary {
		t.Fatalf("catalogue placeholders read as owned: %+v", before)
	}

	episodeID := linkSceneFile(t, st, "scene-1")
	after := read()
	if !after["scene-1"].InLibrary || after["scene-1"].LibraryID != episodeID {
		t.Fatalf("scene with file = %+v, want owned episode %d", after["scene-1"], episodeID)
	}
	if after["scene-2"].InLibrary || after["scene-2"].LibraryID != 0 {
		t.Fatalf("neighbouring placeholder scene = %+v, want unowned", after["scene-2"])
	}
}

func TestAdultSceneDetailReadsProviderMetadataAndState(t *testing.T) {
	h, st, mgr := newTestServer(t)
	scenes := fakeScenes()
	scenes[0].Code = "BRZ-220314"
	provider := &fakeAdultProvider{scenes: scenes}
	mgr.adult = provider
	enableAdult(t, st)
	seedStashboxInstance(t, st, core.ProviderStashbox, "StashDB", "https://stashdb.org/graphql")
	seedSite(t, st)

	rec := do(t, h, http.MethodGet,
		"/api/v1/adult/scenes/scene-1?provider="+core.ProviderStashbox, "")
	wantStatus(t, rec, http.StatusOK)
	var scene sceneMetaJSON
	decodeBody(t, rec, &scene)
	if scene.Provider != core.ProviderStashbox || scene.StashID != "scene-1" ||
		scene.Code != "BRZ-220314" || scene.InLibrary || scene.LibraryID != 0 {
		t.Fatalf("scene detail = %+v, want provider metadata and an unowned placeholder", scene)
	}
	if !containsCall(provider.callLog(), "GetScene scene-1") {
		t.Fatalf("provider calls = %v, want exact scene lookup", provider.callLog())
	}

	linkSceneFile(t, st, "scene-1")
	rec = do(t, h, http.MethodGet,
		"/api/v1/adult/scenes/scene-1?provider="+core.ProviderStashbox, "")
	decodeBody(t, rec, &scene)
	if !scene.InLibrary || scene.LibraryID == 0 {
		t.Fatalf("scene detail after file = %+v, want owned", scene)
	}
}

func TestAdultSceneDetailReturnsNotFoundForUnknownScene(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{scenes: fakeScenes()}
	enableAdult(t, st)

	rec := do(t, h, http.MethodGet, "/api/v1/adult/scenes/missing", "")
	wantStatus(t, rec, http.StatusNotFound)
}

// Without a stash-box credential the adult screens say so rather than pretending
// there is nothing to find — the same 503 GET /search gives for a missing TMDB
// key.
func TestAdultProviderScreensReportAMissingCredential(t *testing.T) {
	h, st, _ := newTestServer(t)
	enableAdult(t, st)

	for _, target := range []string{
		"/api/v1/adult/search?q=x",
		"/api/v1/adult/discover",
		"/api/v1/adult/performers?q=x",
		"/api/v1/adult/tags?q=x",
	} {
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
// which endpoint the SITE's own instance answers on — which is why the server
// derives it: the instance list is admin-only, and this page is one a granted
// member reads.
//
// Two instances in one install means two websites, so the link is a per-ROW
// fact and not a server-wide one. A link built from the wrong endpoint lands on
// a page about a different site, or on a 404, while looking exactly like a
// working link.
func TestSiteDetailLinksToTheSitesOwnInstance(t *testing.T) {
	h, st, _ := newTestServer(t)
	enableAdult(t, st)
	seedStashboxInstance(t, st, core.ProviderStashbox, "ThePornDB", "https://theporndb.net/graphql")
	seedStashboxInstance(t, st, core.ProviderStashbox+":stashdb", "StashDB", "https://stashdb.org/graphql")

	tpdb := seedSiteOn(t, st, core.ProviderStashbox, "site-1", "scene-1", "Brazzers")
	stashdb := seedSiteOn(t, st, core.ProviderStashbox+":stashdb", "site-2", "scene-2", "Vixen")

	for _, tt := range []struct {
		name      string
		site      *core.Series
		want      string
		wantScene string
	}{
		{
			// TPDB files a site under /sites.
			name: "the legacy instance", site: tpdb,
			want:      "https://theporndb.net/sites/site-1",
			wantScene: "https://theporndb.net/scenes/scene-1",
		},
		{
			name: "a stash-box keeps the /studios convention", site: stashdb,
			want:      "https://stashdb.org/studios/site-2",
			wantScene: "https://stashdb.org/scenes/scene-2",
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, "/api/v1/adult/sites/"+itoa(tt.site.ID), "")
			wantStatus(t, rec, http.StatusOK)
			var body siteDetailJSON
			decodeBody(t, rec, &body)
			if body.ProviderURL != tt.want {
				t.Errorf("provider_url = %q, want %q", body.ProviderURL, tt.want)
			}
			// The scene rows carry their own page on the same endpoint — the
			// destination behind a scene's title.
			if len(body.Years) != 1 || len(body.Years[0].Scenes) != 1 {
				t.Fatalf("years = %+v, want the one seeded scene", body.Years)
			}
			if got := body.Years[0].Scenes[0].ProviderURL; got != tt.wantScene {
				t.Errorf("scene provider_url = %q, want %q", got, tt.wantScene)
			}
		})
	}
}

// A site whose instance is gone renders with no link rather than with a guessed
// one: a link into a box that never held this UUID is worse than no link, and a
// site with none renders exactly as one whose provider has no page.
func TestSiteDetailHasNoLinkWhenTheInstanceIsGone(t *testing.T) {
	h, st, _ := newTestServer(t)
	enableAdult(t, st)
	site := seedSiteOn(t, st, core.ProviderStashbox+":gone", "site-1", "scene-1", "Brazzers")

	rec := do(t, h, http.MethodGet, "/api/v1/adult/sites/"+itoa(site.ID), "")
	wantStatus(t, rec, http.StatusOK)
	var body siteDetailJSON
	decodeBody(t, rec, &body)
	if body.ProviderURL != "" {
		t.Errorf("provider_url = %q, want none", body.ProviderURL)
	}
	if got := body.Years[0].Scenes[0].ProviderURL; got != "" {
		t.Errorf("scene provider_url = %q, want none", got)
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

	// The gate is the SWITCH, not the seeding: switching a library off deletes
	// nothing, so every row this test is about is still there to be refused.
	setAdultLibrariesActive(t, st, false)

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

func (p *routingEngineProvider) EngineFor(_ int64, kind string) core.Engine {
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
	// Two searches, the same pair the automatic path runs: the date form scene
	// releases are named by, then the title form for the ones that are not.
	if len(searches) != 2 {
		t.Fatalf("searches = %+v, want the date and title variants", searches)
	}
	if searches[0].query != "Brazzers 22.03.14" {
		t.Errorf("query = %q, want the site and the scene's release date first", searches[0].query)
	}
	if searches[1].query != "Brazzers Deep Impact" {
		t.Errorf("query = %q, want the site and the scene's title second", searches[1].query)
	}
	for _, search := range searches {
		if search.cats != "6000" {
			t.Errorf("cats = %q, want the adult library's categories", search.cats)
		}
		if strings.Contains(search.cats, "5000") || strings.Contains(search.cats, "2000") {
			t.Errorf("a scene search sent the television library's categories: %q", search.cats)
		}
	}
}

// The picker runs the same two searches the automatic path does, and shows one
// table: a release both queries return is one row, not two.
func TestInteractiveSceneSearchMergesBothVariants(t *testing.T) {
	fake := newFakeIndexer(t)
	h, st, _ := newTestServer(t, WithIndexerClients(fake.factory()))
	enableAdult(t, st)
	site := seedSite(t, st)
	addIndexer(t, st, fake, "alpha", 6000)

	// The date query finds the standard name and one release that both queries
	// return; the title query finds a title-named release and that same shared
	// one again.
	// The fake answers with core.Release values directly, so the parse the real
	// indexer client does for a 6000-category result is spelled out here — the
	// site as the title and the release date, which is what parse.Scene reads
	// off a date-named release.
	dated := core.ParsedRelease{
		Title:     "Brazzers",
		SceneDate: time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC),
	}
	shared := core.Release{
		GUID: "shared", Title: "Brazzers.22.03.14.Deep.Impact.XXX.1080p", Indexer: "alpha",
		Parsed: dated,
	}
	fake.servesQuery("Brazzers 22.03.14", core.Release{
		GUID: "by-date", Title: "Brazzers.22.03.14.XXX.1080p", Indexer: "alpha", Parsed: dated,
	}, shared)
	// A title-named release carries no date at all, which is the whole reason
	// the automatic path will not take it on the date test alone.
	fake.servesQuery("Brazzers Deep Impact", shared, core.Release{
		GUID: "by-title", Title: "Brazzers.Deep.Impact.XXX.2160p", Indexer: "alpha",
		Parsed: core.ParsedRelease{Title: "Brazzers Deep Impact"},
	})

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodGet,
		"/api/v1/library/series/"+itoa(site.ID)+"/releases?season=2022&episode=1", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)

	var body struct {
		Query    string   `json:"query"`
		Queries  []string `json:"queries"`
		Releases []struct {
			GUID  string   `json:"guid"`
			Flags []string `json:"flags"`
		} `json:"releases"`
	}
	decodeBody(t, rec, &body)

	want := []string{"Brazzers 22.03.14", "Brazzers Deep Impact"}
	if len(body.Queries) != len(want) || body.Queries[0] != want[0] || body.Queries[1] != want[1] {
		t.Errorf("queries = %v, want %v", body.Queries, want)
	}
	if body.Query != "Brazzers 22.03.14" {
		t.Errorf("query = %q, want the first of them", body.Query)
	}

	seen := map[string]int{}
	for _, rel := range body.Releases {
		seen[rel.GUID]++
	}
	if len(body.Releases) != 3 {
		t.Fatalf("releases = %+v, want three distinct rows", body.Releases)
	}
	if seen["shared"] != 1 {
		t.Errorf("the release both queries returned appears %d times, want once", seen["shared"])
	}

	// The wrong-date flagging is unchanged, so the user can still see which
	// candidates the automatic path would distrust: the title-named release
	// carries no date at all, which is exactly that case.
	byGUID := map[string][]string{}
	for _, rel := range body.Releases {
		byGUID[rel.GUID] = rel.Flags
	}
	if contains := func(flags []string) bool {
		for _, f := range flags {
			if f == flagWrongDate {
				return true
			}
		}
		return false
	}; !contains(byGUID["by-title"]) {
		t.Errorf("the title-named release is not flagged %s: %v", flagWrongDate, byGUID["by-title"])
	} else if contains(byGUID["by-date"]) {
		t.Errorf("the date-named release is flagged %s: %v", flagWrongDate, byGUID["by-date"])
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
	// The episode and season-pack forms both use television numbering,
	// never a scene-date query.
	if len(searches) != 2 || searches[0].query != "Planet Earth II S01E02" || searches[1].query != "Planet Earth II S01" {
		t.Fatalf("searches = %+v, want the television query forms", searches)
	}
	for _, search := range searches {
		if search.cats != "5000" {
			t.Errorf("cats = %q, want the television library's categories", search.cats)
		}
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

// TestSystemStatusCountsSitesOnlyWhenTheModuleIsVisible: the sidebar's Adult
// badge reads counts.sites, and the field must not exist at all — not even as
// a zero — for a caller the module is invisible to, so a module-off response
// stays byte-identical to one from an install that never enabled it.
func TestSystemStatusCountsSitesOnlyWhenTheModuleIsVisible(t *testing.T) {
	h, st, _ := newTestServer(t)

	// Module off: no "sites" key anywhere in the body.
	rec := do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), `"sites"`) {
		t.Fatalf("module-off status body carries a sites key: %s", rec.Body.String())
	}

	enableAdult(t, st)
	seedSite(t, st)

	rec = do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)
	var got statusResponse
	decodeBody(t, rec, &got)
	if got.Counts.Sites != 1 {
		t.Fatalf("counts.sites = %d, want 1", got.Counts.Sites)
	}
	if got.Counts.Series != 0 {
		t.Fatalf("counts.series = %d, want 0: a site must not count as television", got.Counts.Series)
	}
}

// A canceled request is the typeahead working — every keystroke aborts the one
// before it — so the provider error it drags along must not become an ERROR
// log and a 502. The same failure with the caller still on the line is a real
// upstream error and stays one.
func TestAbandonedSiteSearchIsNotAnUpstreamFailure(t *testing.T) {
	h, st, mgr := newTestServer(t)
	enableAdult(t, st)

	// The abort happens MID provider call, the way a typeahead abort does: a
	// context canceled before the request even authenticates never reaches the
	// provider at all.
	ctx, cancel := context.WithCancel(context.Background())
	mgr.adult = &hangupProvider{cancel: cancel}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/adult/search?q=braz", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != statusClientClosedRequest {
		t.Fatalf("status = %d, want %d for a caller that hung up", rec.Code, statusClientClosedRequest)
	}
	mgr.adult = &fakeAdultProvider{err: context.Canceled}

	// The caller still waiting gets the honest 502: the guard reads the
	// request's state, not the error's text.
	rec = do(t, h, http.MethodGet, "/api/v1/adult/search?q=braz", "")
	wantStatus(t, rec, http.StatusBadGateway)
}

// hangupProvider cancels the caller's context mid-call — a typeahead abort —
// and returns the error that cancellation produces.
type hangupProvider struct {
	fakeAdultProvider
	cancel context.CancelFunc
}

func (p *hangupProvider) SearchSites(ctx context.Context, q string) ([]core.SiteMeta, error) {
	p.cancel()
	return nil, context.Canceled
}

// ---- the deferred catalogue walk (POST /adult/sites) -----------------------

// jobsOfKind is every queued job of one kind, newest first.
func jobsOfKind(t *testing.T, st *store.Store, kind string) []core.Job {
	t.Helper()
	jobs, err := st.ListJobs(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	out := []core.Job{}
	for _, job := range jobs {
		if job.Kind == kind {
			out = append(out, job)
		}
	}
	return out
}

// The request answers with the site row and NOTHING filed under it: the
// catalogue walk is a job. A large site is hundreds of provider round trips,
// and the whole reason this is a job is that people gave up waiting and clicked
// Add again.
func TestAddSiteAnswersBeforeTheCatalogueIsWalked(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{}
	enableAdult(t, st)
	ctx := context.Background()

	rec := do(t, h, http.MethodPost, "/api/v1/adult/sites", `{"stash_id":"site-9"}`)
	wantStatus(t, rec, http.StatusCreated)
	var site siteJSON
	decodeBody(t, rec, &site)
	if site.ID == 0 || site.StashID != "site-9" {
		t.Fatalf("site = %+v", site)
	}

	episodes, err := st.ListEpisodes(ctx, site.ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(episodes) != 0 {
		t.Fatalf("the add filed %d scenes, want the walk deferred: %+v", len(episodes), episodes)
	}

	queued := jobsOfKind(t, st, core.JobSyncSite)
	if len(queued) != 1 {
		t.Fatalf("queued %d %s jobs, want 1", len(queued), core.JobSyncSite)
	}
	var payload core.JobSyncSitePayload
	if err := json.Unmarshal([]byte(queued[0].Payload), &payload); err != nil {
		t.Fatalf("decode %s payload %q: %v", core.JobSyncSite, queued[0].Payload, err)
	}
	if payload.SeriesID != site.ID {
		t.Errorf("job names series %d, want the added site %d", payload.SeriesID, site.ID)
	}
	if payload.SearchNow {
		t.Error("search_now is set on an add that did not ask for it")
	}
}

// The double click. Two POSTs while the first walk is still pending are one
// site and one job — the row upserts on the stash id, the job dedupes on its
// payload — and neither is an error.
func TestAddSiteQueuesTheCatalogueWalkOnce(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{}
	enableAdult(t, st)

	first := do(t, h, http.MethodPost, "/api/v1/adult/sites", `{"stash_id":"site-9"}`)
	wantStatus(t, first, http.StatusCreated)
	second := do(t, h, http.MethodPost, "/api/v1/adult/sites", `{"stash_id":"site-9"}`)
	wantStatus(t, second, http.StatusCreated)

	if queued := jobsOfKind(t, st, core.JobSyncSite); len(queued) != 1 {
		t.Fatalf("queued %d %s jobs for two adds, want 1: %+v", len(queued), core.JobSyncSite, queued)
	}
	sites, err := st.ListSeriesByKind(context.Background(), core.SeriesKindAdult)
	if err != nil {
		t.Fatalf("ListSeriesByKind: %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("two adds made %d sites, want 1", len(sites))
	}
}

// search_now rides on the sync job rather than being queued here. Before the
// walk the site has no episode rows, so a search queued now would queue
// nothing — the flag has to survive until there is something to search for.
func TestAddSiteCarriesSearchNowOnTheSyncJob(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{}
	enableAdult(t, st)

	wantStatus(t, do(t, h, http.MethodPost, "/api/v1/adult/sites",
		`{"stash_id":"site-9","monitored":true,"search_now":true}`), http.StatusCreated)

	queued := jobsOfKind(t, st, core.JobSyncSite)
	if len(queued) != 1 {
		t.Fatalf("queued %d %s jobs, want 1", len(queued), core.JobSyncSite)
	}
	var payload core.JobSyncSitePayload
	if err := json.Unmarshal([]byte(queued[0].Payload), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !payload.SearchNow {
		t.Error("search_now did not reach the sync job")
	}
	// And nothing was searched for yet: there is nothing to search for.
	if searches := jobsOfKind(t, st, core.JobSearchEpisode); len(searches) != 0 {
		t.Fatalf("queued %d searches before the walk: %+v", len(searches), searches)
	}
}

// "Add and monitor" is opt-in. Omission and explicit false both leave the site
// unmonitored.
func TestAddSiteHonoursTheMonitoredChoice(t *testing.T) {
	for _, tt := range []struct {
		name, body string
		want       bool
	}{
		{name: "omitted is unmonitored", body: `{"stash_id":"site-9"}`, want: false},
		{name: "explicit true", body: `{"stash_id":"site-9","monitored":true}`, want: true},
		{name: "explicit false", body: `{"stash_id":"site-9","monitored":false}`, want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h, st, mgr := newTestServer(t)
			mgr.adult = &fakeAdultProvider{}
			enableAdult(t, st)

			rec := do(t, h, http.MethodPost, "/api/v1/adult/sites", tt.body)
			wantStatus(t, rec, http.StatusCreated)
			var site siteJSON
			decodeBody(t, rec, &site)
			if site.Monitored != tt.want {
				t.Errorf("monitored = %v, want %v", site.Monitored, tt.want)
			}
		})
	}
}

// The regression that guards the AddSite/AddSiteAndWait split.
//
// Approving a scene request grants ONE scene, and a scene is an episode row.
// The ordinary add defers the walk that creates those rows to a job, so an
// approval that used it would answer "approved" against a site with nothing
// filed under it — the granted scene would not be wanted, and no search would
// ever be made for it. This asserts the episode exists the moment the approval
// returns, which is only true of the waiting variant.
func TestApproveSceneLandsTheEpisodeSynchronously(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{
		sites:  []core.SiteMeta{{StashID: "site-1", Name: "Brazzers"}},
		scenes: fakeScenes(),
	}
	mgr.addSiteSceneStashID = "scene-1"
	enableAdult(t, st)
	ctx := context.Background()

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"scene","stash_id":"scene-1","title":"Deep Impact"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusCreated)
	var created requestJSON
	decodeBody(t, rec, &created)

	rec = doAuth(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve",
		"{}", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)

	// The scene the approval granted is a row, right now — not once some job
	// gets around to running.
	filed, err := st.EpisodeIDsByStashID(ctx, []string{"scene-1"})
	if err != nil {
		t.Fatalf("EpisodeIDsByStashID: %v", err)
	}
	if filed["scene-1"] == 0 {
		t.Fatal("approving a scene request left no episode row: the approval used the deferred add")
	}
	// And it did not lean on the queue to get there.
	if queued := jobsOfKind(t, st, core.JobSyncSite); len(queued) != 0 {
		t.Errorf("the approval queued %d catalogue walks, want the walk done inline", len(queued))
	}
}

// The monitoring contract of a scene approval.
//
// Granting one scene is not a standing order for everything the studio
// releases, so the site lands UNMONITORED and only the asked-for scene's
// episode is monitored — the wanted list reads the EPISODE flag, and that one
// flip is the whole grant. monitored:true is how an approver says the studio
// itself is wanted, and search_now queues the hunt for the scene instead of
// leaving it to the next sweep.
func TestApproveSceneMonitorsOnlyTheAskedForScene(t *testing.T) {
	newSceneRequest := func(t *testing.T) (http.Handler, *store.Store, *http.Cookie, int64) {
		t.Helper()
		h, st, mgr := newTestServer(t)
		mgr.adult = &fakeAdultProvider{
			sites:  []core.SiteMeta{{StashID: "site-1", Name: "Brazzers"}},
			scenes: fakeScenes(),
		}
		mgr.addSiteSceneStashID = "scene-1"
		enableAdult(t, st)

		createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
		cookie := login(t, h, testAdmin, testPassword)

		rec := doAuth(t, h, http.MethodPost, "/api/v1/requests",
			`{"media_type":"scene","stash_id":"scene-1","title":"Deep Impact"}`, withCookie(cookie))
		wantStatus(t, rec, http.StatusCreated)
		var created requestJSON
		decodeBody(t, rec, &created)
		return h, st, cookie, created.ID
	}
	siteAndScene := func(t *testing.T, st *store.Store) (*core.Series, *core.Episode) {
		t.Helper()
		ctx := context.Background()
		site, err := st.GetSeriesByStashID(ctx, "site-1")
		if err != nil {
			t.Fatalf("GetSeriesByStashID: %v", err)
		}
		filed, err := st.EpisodeIDsByStashID(ctx, []string{"scene-1"})
		if err != nil {
			t.Fatalf("EpisodeIDsByStashID: %v", err)
		}
		episode, err := st.GetEpisode(ctx, filed["scene-1"])
		if err != nil {
			t.Fatalf("GetEpisode: %v", err)
		}
		return site, episode
	}

	t.Run("the site lands unmonitored and only the scene is monitored", func(t *testing.T) {
		h, st, cookie, id := newSceneRequest(t)
		rec := doAuth(t, h, http.MethodPost, "/api/v1/requests/"+itoa(id)+"/approve", `{}`, withCookie(cookie))
		wantStatus(t, rec, http.StatusOK)

		site, episode := siteAndScene(t, st)
		if site.Monitored {
			t.Error("approving one scene monitored the whole site")
		}
		if !episode.Monitored {
			t.Error("the granted scene is not monitored: nothing would ever be searched for it")
		}
		if queued := jobsOfKind(t, st, core.JobSearchEpisode); len(queued) != 0 {
			t.Errorf("the approval queued %d searches nobody asked for", len(queued))
		}
	})

	t.Run("an explicit monitored true keeps the whole site monitored", func(t *testing.T) {
		h, st, cookie, id := newSceneRequest(t)
		rec := doAuth(t, h, http.MethodPost, "/api/v1/requests/"+itoa(id)+"/approve",
			`{"monitored":true}`, withCookie(cookie))
		wantStatus(t, rec, http.StatusOK)

		site, episode := siteAndScene(t, st)
		if !site.Monitored || !episode.Monitored {
			t.Errorf("monitored:true landed site=%v episode=%v, want both monitored",
				site.Monitored, episode.Monitored)
		}
	})

	t.Run("search now queues the hunt for the granted scene", func(t *testing.T) {
		h, st, cookie, id := newSceneRequest(t)
		rec := doAuth(t, h, http.MethodPost, "/api/v1/requests/"+itoa(id)+"/approve",
			`{"search_now":true}`, withCookie(cookie))
		wantStatus(t, rec, http.StatusOK)

		_, episode := siteAndScene(t, st)
		queued := jobsOfKind(t, st, core.JobSearchEpisode)
		if len(queued) != 1 || !strings.Contains(queued[0].Payload, itoa(episode.ID)) {
			t.Errorf("search jobs = %v, want exactly one, for episode %d", queued, episode.ID)
		}
		// The answer says the search was queued, so the approver is not left
		// guessing whether anything kicked off.
		if !strings.Contains(rec.Body.String(), `"search_queued":true`) {
			t.Errorf("response = %s, want search_queued true", rec.Body.String())
		}
	})
}

// Request-and-approve, in the scene shape: the one call records the wish and
// grants it, and the grant is the same one the approve endpoint would make —
// site added unmonitored, the asked-for scene monitored.
func TestCreateSceneRequestWithApproveGrantsItImmediately(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.adult = &fakeAdultProvider{
		sites:  []core.SiteMeta{{StashID: "site-1", Name: "Brazzers"}},
		scenes: fakeScenes(),
	}
	mgr.addSiteSceneStashID = "scene-1"
	enableAdult(t, st)
	ctx := context.Background()

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	cookie := login(t, h, testAdmin, testPassword)

	rec := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"scene","stash_id":"scene-1","title":"Deep Impact","approve":true}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusCreated)

	rows, err := st.ListRequests(ctx, "")
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(rows) != 1 || rows[0].Status != core.RequestApproved {
		t.Fatalf("requests = %+v, want the one row approved", rows)
	}
	site, err := st.GetSeriesByStashID(ctx, "site-1")
	if err != nil {
		t.Fatalf("GetSeriesByStashID: %v", err)
	}
	if site.Monitored {
		t.Error("request-and-approve monitored the whole site")
	}
	filed, err := st.EpisodeIDsByStashID(ctx, []string{"scene-1"})
	if err != nil {
		t.Fatalf("EpisodeIDsByStashID: %v", err)
	}
	episode, err := st.GetEpisode(ctx, filed["scene-1"])
	if err != nil {
		t.Fatalf("GetEpisode: %v", err)
	}
	if !episode.Monitored {
		t.Error("the granted scene is not monitored")
	}
}

// ---- cataloguing: the site page can see the walk running -------------------

// queueSiteSync puts an open sync_site job in the queue by hand, the way
// POST /adult/sites does. searchNow varies the payload deliberately: the flag
// is part of the encoding, which is exactly why the detail handler cannot ask
// HasOpenJob for an exact payload match.
func queueSiteSync(t *testing.T, st *store.Store, seriesID int64, searchNow bool) *core.Job {
	t.Helper()
	payload, err := json.Marshal(core.JobSyncSitePayload{SeriesID: seriesID, SearchNow: searchNow})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	job := &core.Job{Kind: core.JobSyncSite, Payload: string(payload)}
	if err := st.EnqueueJob(context.Background(), job); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	return job
}

func siteCataloguingFlag(t *testing.T, h http.Handler, id int64) bool {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/api/v1/adult/sites/"+itoa(id), "")
	wantStatus(t, rec, http.StatusOK)
	var body siteDetailJSON
	decodeBody(t, rec, &body)
	return body.Cataloguing
}

// The flag has to be true for BOTH payload spellings: the site is the subject,
// and search_now is only a passenger. This fails against a HasOpenJob-style
// exact-payload match, which would answer true for one of the two and false for
// the other.
func TestSiteDetailReportsCataloguingForEitherPayload(t *testing.T) {
	for _, searchNow := range []bool{false, true} {
		name := "search_now off"
		if searchNow {
			name = "search_now on"
		}
		t.Run(name, func(t *testing.T) {
			h, st, _ := newTestServer(t)
			enableAdult(t, st)
			site := seedSite(t, st)

			if siteCataloguingFlag(t, h, site.ID) {
				t.Fatal("cataloguing is true with no job queued")
			}
			queueSiteSync(t, st, site.ID, searchNow)
			if !siteCataloguingFlag(t, h, site.ID) {
				t.Error("cataloguing is false while a walk for this site is queued")
			}
		})
	}
}

// A running job counts as much as a pending one — the walk is publishing years
// while it runs, which is precisely when the page most needs to keep polling.
func TestSiteDetailReportsCataloguingWhileTheWalkRuns(t *testing.T) {
	h, st, _ := newTestServer(t)
	enableAdult(t, st)
	site := seedSite(t, st)
	ctx := context.Background()

	queueSiteSync(t, st, site.ID, false)
	claimed, err := st.ClaimJob(ctx, []string{core.JobSyncSite}, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if claimed == nil {
		t.Fatal("no job to claim")
	}
	if !siteCataloguingFlag(t, h, site.ID) {
		t.Error("cataloguing is false while the walk is running")
	}

	if err := st.CompleteJob(ctx, claimed.ID); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	if siteCataloguingFlag(t, h, site.ID) {
		t.Error("cataloguing is still true after the walk finished")
	}
}

// A walk of ANOTHER site says nothing about this one. Without the series-id
// match, every site page would poll itself for as long as any site anywhere was
// being indexed.
func TestSiteDetailIgnoresAnotherSitesWalk(t *testing.T) {
	h, st, _ := newTestServer(t)
	enableAdult(t, st)
	ctx := context.Background()
	site := seedSite(t, st)
	other := &core.Series{
		StashID: "site-other", Title: "Other", SortTitle: "other",
		Kind: core.SeriesKindAdult, Monitored: true,
		LibraryID: defaultLibraryID(t, st, core.LibraryKindAdult),
	}
	if err := st.UpsertSeries(ctx, other); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}

	queueSiteSync(t, st, other.ID, false)
	if siteCataloguingFlag(t, h, site.ID) {
		t.Error("a walk of another site marked this one as cataloguing")
	}
	if !siteCataloguingFlag(t, h, other.ID) {
		t.Error("the site actually being walked does not read as cataloguing")
	}
}

// The universal search is a new door to cached adult rows, so it carries the
// same promise of absence every other surface does: with the module off, an
// adult category search answers empty without asking any indexer, a cached
// adult release id is not grabbable, and a file parked into an adult library
// is absent from the review queue.
func TestUniversalSearchKeepsAdultAbsentWithTheModuleOff(t *testing.T) {
	ctx := context.Background()
	fake := newFakeIndexer(t)
	h, st, _ := newTestServer(t, WithIndexerClients(fake.factory()))
	enableAdult(t, st)
	addIndexer(t, st, fake, "alpha", 6000)

	// Cached while the module was on: an adult release and a file parked into
	// the adult library by an untied grab.
	adultRel := torrentRelease("Site.22.03.14.Scene", "guid-adult", 5,
		core.ParsedRelease{Title: "Site"})
	adultRel.IndexerID = 1
	adultRel.Categories = []int{6010}
	if err := st.UpsertRelease(ctx, &adultRel); err != nil {
		t.Fatalf("UpsertRelease: %v", err)
	}
	adultLib, err := st.GetDefaultLibrary(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetDefaultLibrary(adult): %v", err)
	}
	if err := st.UpsertUnmatchedFile(ctx, &core.UnmatchedFile{
		Path: "downloads/scene.mkv", Size: 1, Reason: "manual-grab", LibraryID: adultLib.ID,
	}); err != nil {
		t.Fatalf("UpsertUnmatchedFile: %v", err)
	}

	setAdultLibrariesActive(t, st, false)
	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	cookie := login(t, h, testAdmin, testPassword)

	// An all-adult category request short-circuits: empty answer, zero
	// outbound searches — indistinguishable from a search that matched nothing.
	rec := doAuth(t, h, http.MethodGet, "/api/v1/search/releases?q=scene&cats=6000,6010", "",
		withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var body releasesResponse
	decodeBody(t, rec, &body)
	if len(body.Releases) != 0 || len(fake.recorded()) != 0 {
		t.Fatalf("releases = %+v, searches = %+v; want silence", body.Releases, fake.recorded())
	}

	// The cached adult release id is not a grabbable handle.
	movies, err := st.GetDefaultLibrary(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("GetDefaultLibrary(movie): %v", err)
	}
	rec = doAuth(t, h, http.MethodPost, "/api/v1/search/grab",
		`{"release_id":`+itoa(adultRel.ID)+`,"library_id":`+itoa(movies.ID)+`}`,
		withCookie(cookie))
	wantStatus(t, rec, http.StatusNotFound)

	// The parked file is absent from the review queue, and unreachable by id.
	rec = doAuth(t, h, http.MethodGet, "/api/v1/import/queue", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var queue struct {
		Items []unmatchedJSON `json:"items"`
	}
	decodeBody(t, rec, &queue)
	if len(queue.Items) != 0 {
		t.Fatalf("import queue = %+v, want the adult-parked file hidden", queue.Items)
	}
}
