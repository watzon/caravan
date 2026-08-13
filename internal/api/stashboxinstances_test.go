package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// newTestStore opens a bare store, for the handful of tests that exercise a
// server method directly rather than through the router.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// seedStashboxInstance writes one instance row the way the create handler does.
func seedStashboxInstance(t *testing.T, st *store.Store, providerID, name, endpoint string) *core.StashboxInstance {
	t.Helper()
	in := &core.StashboxInstance{
		ProviderID: providerID, Name: name, Endpoint: endpoint, APIKey: "seed-key",
	}
	if err := st.UpsertStashboxInstance(context.Background(), in); err != nil {
		t.Fatalf("UpsertStashboxInstance: %v", err)
	}
	return in
}

// adultAdmin is a server with the module on and an admin logged in — the state
// every instance route is reachable from.
func adultAdmin(t *testing.T) (http.Handler, *store.Store, *stubManager, *http.Cookie) {
	t.Helper()
	h, st, mgr := newTestServer(t)
	enableAdult(t, st)
	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	return h, st, mgr, login(t, h, testAdmin, testPassword)
}

func createInstance(t *testing.T, h http.Handler, cookie *http.Cookie, body string) stashboxInstanceJSON {
	t.Helper()
	rec := doAuth(t, h, http.MethodPost, "/api/v1/adult/stashbox-instances", body, withCookie(cookie))
	wantStatus(t, rec, http.StatusCreated)
	var out stashboxInstanceJSON
	decodeBody(t, rec, &out)
	return out
}

// The id of the first instance on an install is the bare `stashbox`, which is
// what every adult row written before instances existed already carries. A fresh
// install therefore uses the compatibility id too, and neither state ends up
// with a row nothing can resolve.
func TestCreateStashboxInstanceMintsTheLegacyIDFirst(t *testing.T) {
	h, _, _, cookie := adultAdmin(t)

	first := createInstance(t, h, cookie,
		`{"name":"StashDB","endpoint":"https://stashdb.org/graphql","api_key":"k"}`)
	if first.ProviderID != core.ProviderStashbox {
		t.Fatalf("first provider_id = %q, want %q", first.ProviderID, core.ProviderStashbox)
	}

	second := createInstance(t, h, cookie,
		`{"name":"PMV Stash","endpoint":"https://pmvstash.org/graphql","api_key":"k2"}`)
	if second.ProviderID != core.ProviderStashbox+":pmv-stash" {
		t.Fatalf("second provider_id = %q, want %q", second.ProviderID, core.ProviderStashbox+":pmv-stash")
	}
	if !core.ValidProviderInstanceID(second.ProviderID) {
		t.Fatalf("minted %q, which no chain validator would accept", second.ProviderID)
	}
}

// The credential is write-only (SPEC §12): no response on this surface may carry
// it, and has_api_key is what the editor renders instead.
func TestStashboxInstanceResponsesNeverCarryTheKey(t *testing.T) {
	h, _, _, cookie := adultAdmin(t)

	created := createInstance(t, h, cookie,
		`{"name":"StashDB","endpoint":"https://stashdb.org/graphql","api_key":"sk-secret"}`)
	if !created.HasAPIKey {
		t.Error("has_api_key = false after a create that carried one")
	}

	for _, probe := range []struct {
		method, path, body string
	}{
		{http.MethodPost, "/api/v1/adult/stashbox-instances", `{"name":"FansDB","endpoint":"https://fansdb.cc/graphql","api_key":"sk-secret"}`},
		{http.MethodGet, "/api/v1/adult/stashbox-instances", ""},
		{http.MethodPut, "/api/v1/adult/stashbox-instances/" + itoa(created.ID), `{"name":"StashDB"}`},
		{http.MethodPost, "/api/v1/adult/stashbox-instances/" + itoa(created.ID) + "/test", ""},
	} {
		rec := doAuth(t, h, probe.method, probe.path, probe.body, withCookie(cookie))
		if strings.Contains(rec.Body.String(), "sk-secret") || strings.Contains(rec.Body.String(), `"api_key"`) {
			t.Errorf("%s %s leaked the credential: %s", probe.method, probe.path, rec.Body.String())
		}
	}
}

// The manual-match picker switches between the target library's configured
// instances. Each row therefore carries that exact endpoint's dialect rather
// than repeating the default instance's /auth/me answer.
func TestStashboxInstanceListReportsEachSceneFilterDialect(t *testing.T) {
	h, st, mgr, cookie := adultAdmin(t)
	first := seedStashboxInstance(t, st, core.ProviderStashbox, "StashDB", "https://stashdb.org/graphql")
	second := seedStashboxInstance(t, st, core.ProviderStashbox+":fansdb", "FansDB", "https://fansdb.cc/graphql")
	mgr.adult = &thinAdultProvider{}

	rec := doAuth(t, h, http.MethodGet, "/api/v1/adult/stashbox-instances", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Instances []stashboxInstanceJSON `json:"instances"`
	}
	decodeBody(t, rec, &body)
	if len(body.Instances) != 2 {
		t.Fatalf("instances = %+v, want both configured endpoints", body.Instances)
	}
	for _, instance := range body.Instances {
		if instance.SceneFilters == nil {
			t.Fatalf("%s scene_filters = nil", instance.ProviderID)
		}
		if *instance.SceneFilters != (sceneFiltersJSON{}) {
			t.Fatalf("%s scene_filters = %+v, want the thin endpoint dialect", instance.ProviderID, *instance.SceneFilters)
		}
	}
	if got, want := mgr.adultProviders(), []string{first.ProviderID, second.ProviderID}; !slices.Equal(got, want) {
		t.Fatalf("provider capability lookups = %v, want %v", got, want)
	}

	// A configured row whose client cannot be built is explicitly "no optional
	// filters", not an omitted block the browser could mistake for an old API.
	mgr.adult = nil
	rec = doAuth(t, h, http.MethodGet, "/api/v1/adult/stashbox-instances", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	body.Instances = nil
	decodeBody(t, rec, &body)
	for _, instance := range body.Instances {
		if instance.SceneFilters == nil || *instance.SceneFilters != (sceneFiltersJSON{}) {
			t.Fatalf("%s scene_filters without a client = %+v, want explicit zero capabilities",
				instance.ProviderID, instance.SceneFilters)
		}
	}
}

// Re-pointing an instance at another box would have the next refresh overwrite
// every row pinned to it with whatever the new box holds under the same UUIDs.
// Repeating the stored endpoint — which is what the read-only form field sends —
// is not a change and is accepted.
func TestStashboxInstanceEndpointIsImmutable(t *testing.T) {
	h, st, _, cookie := adultAdmin(t)
	created := createInstance(t, h, cookie,
		`{"name":"StashDB","endpoint":"https://stashdb.org/graphql","api_key":"k"}`)
	path := "/api/v1/adult/stashbox-instances/" + itoa(created.ID)

	rec := doAuth(t, h, http.MethodPut, path,
		`{"name":"StashDB","endpoint":"https://fansdb.cc/graphql"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)

	stored, err := st.GetStashboxInstance(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetStashboxInstance: %v", err)
	}
	if stored.Endpoint != "https://stashdb.org/graphql" {
		t.Fatalf("endpoint = %q, want the refused edit not to have landed", stored.Endpoint)
	}

	wantStatus(t, doAuth(t, h, http.MethodPut, path,
		`{"name":"Stash DB","endpoint":"https://stashdb.org/graphql"}`, withCookie(cookie)),
		http.StatusOK)
}

// The invariant guardAdultCredentialEdit used to hold from the settings side:
// the module runs against a credential that was PROVED, and an edit is as much a
// way to break that as a bad enable. A rejected key writes nothing.
func TestStashboxInstanceUpdateProvesANewKey(t *testing.T) {
	h, st, mgr, cookie := adultAdmin(t)
	created := createInstance(t, h, cookie,
		`{"name":"StashDB","endpoint":"https://stashdb.org/graphql","api_key":"good"}`)
	path := "/api/v1/adult/stashbox-instances/" + itoa(created.ID)

	mgr.adultCredentialErr = errKeyRejected
	rec := doAuth(t, h, http.MethodPut, path, `{"name":"StashDB","api_key":"typo"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusBadGateway)
	wantCode(t, rec, CodeAdultCredentialInvalid)
	if strings.Contains(rec.Body.String(), "typo") {
		t.Errorf("the rejected key was echoed back: %q", rec.Body.String())
	}

	stored, err := st.GetStashboxInstance(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("GetStashboxInstance: %v", err)
	}
	if stored.APIKey != "good" {
		t.Fatalf("api_key = %q, want the working key kept", stored.APIKey)
	}

	// An edit that touches only the name costs no upstream call: the stored key
	// is the key that was already proved.
	before := len(mgr.adultCredentials())
	wantStatus(t, doAuth(t, h, http.MethodPut, path, `{"name":"StashDB (main)"}`, withCookie(cookie)),
		http.StatusOK)
	if after := len(mgr.adultCredentials()); after != before {
		t.Fatalf("a name-only edit made %d validations, want none", after-before)
	}
}

// Nothing cascades, so the counts ARE the guard: a chain naming a gone instance
// walks to a provider nothing can build, and an item pinned to one loses the only
// box that can be asked about its refs.
func TestDeleteStashboxInstanceRefusesWhileItIsInUse(t *testing.T) {
	h, st, _, cookie := adultAdmin(t)
	ctx := context.Background()
	keep := createInstance(t, h, cookie,
		`{"name":"StashDB","endpoint":"https://stashdb.org/graphql","api_key":"k"}`)
	spare := createInstance(t, h, cookie,
		`{"name":"FansDB","endpoint":"https://fansdb.cc/graphql","api_key":"k2"}`)
	_ = spare

	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	lib.Provider = keep.ProviderID
	lib.Providers = []string{keep.ProviderID}
	if err := st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	site := seedSite(t, st)
	site.Provider = keep.ProviderID
	site.ProviderRef = site.StashID
	if err := st.UpsertSeries(ctx, site); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}

	rec := doAuth(t, h, http.MethodDelete,
		"/api/v1/adult/stashbox-instances/"+itoa(keep.ID), "", withCookie(cookie))
	wantStatus(t, rec, http.StatusConflict)
	body := rec.Body.String()
	if !strings.Contains(body, "1 library") || !strings.Contains(body, "1 item") {
		t.Fatalf("refusal = %q, want both counts named", body)
	}
	if _, err := st.GetStashboxInstance(ctx, keep.ID); err != nil {
		t.Fatalf("the refused delete removed the row: %v", err)
	}
}

// Deleting the only instance while the module is on leaves every adult surface
// answering 503 with no screen saying why. Switching the module off is the
// deliberate act that makes it deletable.
func TestDeleteStashboxInstanceRefusesTheLastWhileTheModuleIsOn(t *testing.T) {
	h, st, _, cookie := adultAdmin(t)
	only := createInstance(t, h, cookie,
		`{"name":"StashDB","endpoint":"https://stashdb.org/graphql","api_key":"k"}`)
	path := "/api/v1/adult/stashbox-instances/" + itoa(only.ID)

	rec := doAuth(t, h, http.MethodDelete, path, "", withCookie(cookie))
	wantStatus(t, rec, http.StatusConflict)
	wantErrorBody(t, rec)

	// A second instance the adult library is moved onto makes the first
	// deletable again, module still on.
	second := createInstance(t, h, cookie, `{"name":"FansDB","endpoint":"https://fansdb.cc/graphql"}`)
	ctx := context.Background()
	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	lib.Provider = second.ProviderID
	lib.Providers = []string{second.ProviderID}
	if err := st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	wantStatus(t, doAuth(t, h, http.MethodDelete, path, "", withCookie(cookie)), http.StatusNoContent)

	if _, err := st.GetStashboxInstance(context.Background(), only.ID); err == nil {
		t.Fatal("the instance survived a permitted delete")
	}
}

// Both columns are UNIQUE, and two different names can fold to one slug, so
// either clash is a plain user mistake and answers 409 rather than a 500.
func TestCreateStashboxInstanceRefusesADuplicate(t *testing.T) {
	h, _, _, cookie := adultAdmin(t)
	createInstance(t, h, cookie, `{"name":"StashDB","endpoint":"https://stashdb.org/graphql"}`)
	createInstance(t, h, cookie, `{"name":"Fans DB","endpoint":"https://fansdb.cc/graphql"}`)

	for _, body := range []string{
		`{"name":"Fans DB","endpoint":"https://other.test/graphql"}`,
		`{"name":"fans-db","endpoint":"https://other.test/graphql"}`,
	} {
		rec := doAuth(t, h, http.MethodPost, "/api/v1/adult/stashbox-instances", body, withCookie(cookie))
		wantStatus(t, rec, http.StatusConflict)
		wantErrorBody(t, rec)
	}
}

// The shape check absorbed from the deleted validateStashboxSettings: an
// endpoint nothing could dial is refused where the user can see it, not stored
// happily and failed much later inside a refresh nobody is watching.
func TestStashboxInstanceRefusesAnUndialableEndpoint(t *testing.T) {
	h, _, _, cookie := adultAdmin(t)

	for _, body := range []string{
		`{"name":"X","endpoint":""}`,
		`{"name":"X","endpoint":"theporndb.net/graphql"}`,
		`{"name":"X","endpoint":"ftp://theporndb.net/graphql"}`,
		`{"name":"X","endpoint":"https:///graphql"}`,
	} {
		for _, path := range []string{
			"/api/v1/adult/stashbox-instances",
			"/api/v1/adult/stashbox-instances/test",
		} {
			rec := doAuth(t, h, http.MethodPost, path, body, withCookie(cookie))
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
		}
	}

	// A row needs a label; the test-config route does not, because a test is
	// about the endpoint and the key and the form may have no name yet.
	rec := doAuth(t, h, http.MethodPost, "/api/v1/adult/stashbox-instances",
		`{"name":"  ","endpoint":"https://stashdb.org/graphql"}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
	wantStatus(t, doAuth(t, h, http.MethodPost, "/api/v1/adult/stashbox-instances/test",
		`{"endpoint":"https://stashdb.org/graphql"}`, withCookie(cookie)), http.StatusOK)
}

// Both test routes prove a credential with one live call: the stored one by id,
// the typed one from the body — which is what the add form needs before the
// instance exists to have an id.
func TestStashboxInstanceTestRoutes(t *testing.T) {
	h, _, mgr, cookie := adultAdmin(t)
	created := createInstance(t, h, cookie,
		`{"name":"StashDB","endpoint":"https://stashdb.org/graphql","api_key":"stored-key"}`)

	wantStatus(t, doAuth(t, h, http.MethodPost,
		"/api/v1/adult/stashbox-instances/"+itoa(created.ID)+"/test", "", withCookie(cookie)),
		http.StatusOK)
	want := adultCredential{endpoint: "https://stashdb.org/graphql", key: "stored-key"}
	if got := mgr.adultCredentials(); len(got) == 0 || got[len(got)-1] != want {
		t.Fatalf("stored test validated %v, want %v last", got, want)
	}

	wantStatus(t, doAuth(t, h, http.MethodPost, "/api/v1/adult/stashbox-instances/test",
		`{"name":"Candidate","endpoint":"https://fansdb.cc/graphql","api_key":"typed"}`, withCookie(cookie)),
		http.StatusOK)
	want = adultCredential{endpoint: "https://fansdb.cc/graphql", key: "typed"}
	if got := mgr.adultCredentials(); got[len(got)-1] != want {
		t.Fatalf("body test validated %v, want %v last", got[len(got)-1], want)
	}

	mgr.adultCredentialErr = errKeyRejected
	rec := doAuth(t, h, http.MethodPost,
		"/api/v1/adult/stashbox-instances/"+itoa(created.ID)+"/test", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusBadGateway)
	wantCode(t, rec, CodeAdultCredentialInvalid)
}

// The chain editor's vocabulary: one entry per configured instance, and the
// static "Stash-box" descriptor never among them — a protocol is not something a
// chain can name.
func TestListProvidersMergesConfiguredInstances(t *testing.T) {
	h, st, _, cookie := adultAdmin(t)
	seedStashboxInstance(t, st, core.ProviderStashbox, "StashDB", "https://stashdb.org/graphql")
	seedStashboxInstance(t, st, core.ProviderStashbox+":fansdb", "FansDB", "https://fansdb.cc/graphql")

	rec := doAuth(t, h, http.MethodGet, "/api/v1/libraries/providers", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Providers []struct {
			ID    string   `json:"id"`
			Name  string   `json:"name"`
			Kinds []string `json:"kinds"`
		} `json:"providers"`
	}
	decodeBody(t, rec, &body)

	byID := map[string]string{}
	for _, p := range body.Providers {
		byID[p.ID] = p.Name
		for _, k := range p.Kinds {
			if k == core.LibraryKindAdult && core.ProviderBase(p.ID) != core.ProviderStashbox {
				t.Errorf("provider %s claims the adult kind", p.ID)
			}
		}
	}
	if byID[core.ProviderStashbox] != "StashDB" || byID[core.ProviderStashbox+":fansdb"] != "FansDB" {
		t.Fatalf("providers = %+v, want both instances under their own names", body.Providers)
	}
	// The static descriptor's name is "Stash-box"; the bare id may only appear
	// as the instance that claimed it.
	for _, p := range body.Providers {
		if p.Name == "Stash-box" {
			t.Errorf("the protocol descriptor shipped as a chain element: %+v", p)
		}
	}
}

// An ungranted caller sees no adult provider at all — not the protocol, not one
// instance, not an adult kind on anything else.
func TestListProvidersHidesInstancesFromEveryoneElse(t *testing.T) {
	h, st, _ := newTestServer(t)
	seedStashboxInstance(t, st, core.ProviderStashbox, "StashDB", "https://stashdb.org/graphql")

	rec := do(t, h, http.MethodGet, "/api/v1/libraries/providers", "")
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), core.ProviderStashbox) ||
		strings.Contains(rec.Body.String(), "StashDB") ||
		strings.Contains(rec.Body.String(), core.LibraryKindAdult) {
		t.Fatalf("providers named the adult module with it off: %s", rec.Body.String())
	}
}

// A ref pinned to an instance this install does not hold could never be
// refreshed, so it is refused at the door rather than written to a row and
// discovered on the next refresh.
func TestItemRefRefusesAnUnconfiguredInstance(t *testing.T) {
	st := newTestStore(t)
	seedStashboxInstance(t, st, core.ProviderStashbox, "StashDB", "https://stashdb.org/graphql")
	s := &server{st: st, log: slog.Default()}
	ctx := context.Background()

	if _, ok := s.itemRefFrom(ctx, httptest.NewRecorder(), core.ProviderStashbox, "uuid-1", 0,
		core.LibraryKindAdult); !ok {
		t.Error("a ref naming the configured instance was refused")
	}
	w := httptest.NewRecorder()
	if _, ok := s.itemRefFrom(ctx, w, core.ProviderStashbox+":fansdb", "uuid-1", 0,
		core.LibraryKindAdult); ok {
		t.Error("a ref naming an unconfigured instance was accepted")
	}
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "fansdb") {
		t.Errorf("refusal = %d %q, want a 400 naming the id", w.Code, w.Body.String())
	}
	// The non-instanced providers keep their old behaviour: no store read, no
	// new way to fail.
	if _, ok := s.itemRefFrom(ctx, httptest.NewRecorder(), core.ProviderTMDB, "603", 0,
		core.LibraryKindMovie); !ok {
		t.Error("a TMDB ref was refused")
	}
}

// The settings table is no longer a door to the stash-box credential: both keys
// left writableSettings with the prerelease rows, so PUT /settings
// answers them the way it answers any key nothing reads.
func TestPutSettingsNoLongerAcceptsStashboxCredentials(t *testing.T) {
	h, _, _ := newTestServer(t)

	for _, body := range []string{
		`{"stashbox_endpoint":"https://stashdb.org/graphql"}`,
		`{"stashbox_api_key":"sk-adult"}`,
	} {
		rec := do(t, h, http.MethodPut, "/api/v1/settings", body)
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/settings", "")
	wantStatus(t, rec, http.StatusOK)
	var settings map[string]json.RawMessage
	decodeBody(t, rec, &settings)
	if _, ok := settings[store.SettingStashboxEndpoint]; ok {
		t.Fatalf("settings still project the retired endpoint: %v", settings)
	}
}

// ---------------------------------------------------------------------------
// Instance resolution on the adult surfaces (PLAN Part 2 phase 4).
// ---------------------------------------------------------------------------

// adultAdminWithProvider is adultAdmin with a canned stash-box behind it and the
// two instances the resolution tests choose between.
func adultAdminWithProvider(t *testing.T) (http.Handler, *store.Store, *stubManager, *http.Cookie) {
	t.Helper()
	h, st, mgr, cookie := adultAdmin(t)
	mgr.adult = &fakeAdultProvider{
		sites:  []core.SiteMeta{{StashID: "site-1", Name: "Brazzers"}},
		scenes: fakeScenes(),
	}
	seedStashboxInstance(t, st, core.ProviderStashbox, "StashDB", "https://stashdb.org/graphql")
	seedStashboxInstance(t, st, core.ProviderStashbox+":fansdb", "FansDB", "https://fansdb.cc/graphql")
	return h, st, mgr, cookie
}

// A hit says which box answered, because two boxes can hold the same site under
// the same UUID — a hit without the instance names nothing an add could pin to.
func TestAdultHitsCarryTheAnsweringInstance(t *testing.T) {
	h, st, _, cookie := adultAdminWithProvider(t)

	assertProvider := func(target, want string) {
		t.Helper()
		rec := doAuth(t, h, http.MethodGet, target, "", withCookie(cookie))
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Sites  []siteMetaJSON  `json:"sites"`
			Scenes []sceneMetaJSON `json:"scenes"`
		}
		decodeBody(t, rec, &body)
		for _, site := range body.Sites {
			if site.Provider != want {
				t.Errorf("%s: site provider = %q, want %q", target, site.Provider, want)
			}
		}
		for _, scene := range body.Scenes {
			if scene.Provider != want {
				t.Errorf("%s: scene provider = %q, want %q", target, scene.Provider, want)
			}
		}
		if len(body.Sites)+len(body.Scenes) == 0 {
			t.Fatalf("%s answered nothing to attribute", target)
		}
	}

	// The default: the adult library's chain head, which the enable seeded as
	// the legacy id.
	assertProvider("/api/v1/adult/search?q=brazzers", core.ProviderStashbox)
	assertProvider("/api/v1/adult/discover", core.ProviderStashbox)

	// Moving the library's chain moves the default with it.
	ctx := context.Background()
	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	lib.Provider = core.ProviderStashbox + ":fansdb"
	lib.Providers = []string{core.ProviderStashbox + ":fansdb"}
	if err := st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	assertProvider("/api/v1/adult/search?q=brazzers", core.ProviderStashbox+":fansdb")

	// And an explicit ?provider= overrules the default.
	assertProvider("/api/v1/adult/search?q=brazzers&provider="+core.ProviderStashbox, core.ProviderStashbox)
	assertProvider("/api/v1/adult/discover?provider="+core.ProviderStashbox, core.ProviderStashbox)
}

// An id nothing answers to is the CALLER's mistake and reads as one. Passing it
// through would come back as the same nil an unconfigured module gives, and a
// typo would be reported as a configuration problem.
func TestAdultSurfacesRefuseAnUnknownProvider(t *testing.T) {
	h, _, mgr, cookie := adultAdminWithProvider(t)

	for _, target := range []string{
		"/api/v1/adult/search?q=x&provider=" + core.ProviderStashbox + ":nope",
		"/api/v1/adult/discover?provider=" + core.ProviderTMDB,
		"/api/v1/adult/performers?q=mia&provider=" + core.ProviderStashbox + ":nope",
		"/api/v1/adult/tags?q=anal&provider=not%20an%20id",
	} {
		rec := doAuth(t, h, http.MethodGet, target, "", withCookie(cookie))
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
	}
	if calls := mgr.adult.(*fakeAdultProvider).callLog(); len(calls) != 0 {
		t.Errorf("a refused provider id still reached the endpoint: %v", calls)
	}
}

// The bodies accept the same id the hits carry, validated the same way. AddSite
// cannot yet carry it to the library layer (see handleAddSite) and a request row
// has no column for it, so both drop it — but neither accepts one nobody checked.
func TestAdultBodiesValidateTheInstanceTheyName(t *testing.T) {
	h, _, _, cookie := adultAdminWithProvider(t)

	wantStatus(t, doAuth(t, h, http.MethodPost, "/api/v1/adult/sites",
		`{"stash_id":"site-1","provider":"`+core.ProviderStashbox+`:fansdb"}`, withCookie(cookie)),
		http.StatusCreated)

	for _, probe := range []struct{ path, body string }{
		{"/api/v1/adult/sites", `{"stash_id":"site-2","provider":"` + core.ProviderStashbox + `:nope"}`},
		{"/api/v1/requests", `{"media_type":"scene","stash_id":"scene-9","title":"X","provider":"` + core.ProviderStashbox + `:nope"}`},
		{"/api/v1/requests", `{"media_type":"movie","tmdb_id":603,"title":"X","provider":"` + core.ProviderStashbox + `"}`},
	} {
		rec := doAuth(t, h, http.MethodPost, probe.path, probe.body, withCookie(cookie))
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
	}
}

// /auth/me reports the DEFAULT instance's filter dialect for Adult Explore.
// Provider-switching match screens use the per-instance dialect tested above.
func TestSceneFiltersReportTheDefaultInstance(t *testing.T) {
	h, _, mgr, cookie := adultAdminWithProvider(t)

	rec := doAuth(t, h, http.MethodGet, "/api/v1/auth/me", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	var me meResponse
	decodeBody(t, rec, &me)
	if !me.Adult || me.SceneFilters == nil {
		t.Fatalf("me = %+v, want the adult module and its filter rail", me)
	}

	// With no provider behind the module there is nothing to report, and the
	// screen's own 503 is the better answer.
	mgr.adult = nil
	rec = doAuth(t, h, http.MethodGet, "/api/v1/auth/me", "", withCookie(cookie))
	wantStatus(t, rec, http.StatusOK)
	// A fresh value: scene_filters is omitempty, so decoding into the old one
	// would keep the pointer the first response set.
	me = meResponse{}
	decodeBody(t, rec, &me)
	if me.SceneFilters != nil {
		t.Fatalf("scene_filters = %+v with no provider, want absent", me.SceneFilters)
	}
}
