package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

type libraryListBody struct {
	Libraries []libraryJSON `json:"libraries"`
}

// seedIndexer adds one configured indexer, the thing every per-library override
// hangs off.
func seedIndexer(t *testing.T, st *store.Store, name string, categories []int, enabled bool) *core.IndexerConfig {
	t.Helper()
	cfg := &core.IndexerConfig{
		Name: name, URL: "http://" + name + ".test", APIKey: "k",
		Type: core.IndexerTypeTorznab, Categories: categories, Enabled: enabled,
	}
	if err := st.UpsertIndexer(context.Background(), cfg); err != nil {
		t.Fatalf("UpsertIndexer(%q): %v", name, err)
	}
	return cfg
}

func libraryOfKind(t *testing.T, body libraryListBody, kind string) libraryJSON {
	t.Helper()
	for _, l := range body.Libraries {
		if l.Kind == kind {
			return l
		}
	}
	t.Fatalf("no %q library in %+v", kind, body.Libraries)
	return libraryJSON{}
}

// A freshly migrated install answers with the two seeded libraries, no
// overrides anywhere, and every configured indexer searched with its own
// categories — the JSON shape of "nothing has changed yet".
func TestListLibrariesReportsSeededDefaults(t *testing.T) {
	h, st, _ := newTestServer(t)
	seedIndexer(t, st, "jackett", []int{2000, 5000}, true)

	rec := do(t, h, http.MethodGet, "/api/v1/libraries", "")
	wantStatus(t, rec, http.StatusOK)
	var body libraryListBody
	decodeBody(t, rec, &body)

	if len(body.Libraries) != 2 {
		t.Fatalf("libraries = %+v, want the two seeded rows", body.Libraries)
	}
	movies := libraryOfKind(t, body, core.LibraryKindMovie)
	if movies.Name != "Movies" || movies.RootPath == "" {
		t.Errorf("movie library = %+v, want the seeded name and root path", movies)
	}
	if !movies.DLNAVisible {
		t.Error("movie library is not shared over DLNA on a fresh install")
	}
	if movies.RouteTorrent != "" || movies.RouteUsenet != "" || movies.QualityProfileID != 0 {
		t.Errorf("movie library carries overrides on a fresh install: %+v", movies)
	}
	if len(movies.Indexers) != 1 {
		t.Fatalf("indexers = %+v, want the one configured indexer", movies.Indexers)
	}
	want := libraryIndexerJSON{
		IndexerID: 1, Name: "jackett", Type: core.IndexerTypeTorznab,
		IndexerEnabled: true, Enabled: true,
		Categories: []int{2000, 5000}, CategoriesOverridden: false,
		DefaultCategories: []int{2000, 5000},
	}
	if !reflect.DeepEqual(movies.Indexers[0], want) {
		t.Errorf("indexer row = %+v, want %+v", movies.Indexers[0], want)
	}
}

// A globally disabled indexer still appears, because the screen has to explain
// why it is not searched. What it must not do is read as this library's choice.
func TestListLibrariesSeparatesGlobalAndPerLibraryDisable(t *testing.T) {
	h, st, _ := newTestServer(t)
	seedIndexer(t, st, "off-everywhere", []int{2000}, false)

	rec := do(t, h, http.MethodGet, "/api/v1/libraries", "")
	wantStatus(t, rec, http.StatusOK)
	var body libraryListBody
	decodeBody(t, rec, &body)

	row := libraryOfKind(t, body, core.LibraryKindTV).Indexers[0]
	if row.IndexerEnabled {
		t.Error("indexer_enabled = true for a globally disabled indexer")
	}
	if !row.Enabled {
		t.Error("enabled = false, but this library never disabled the indexer")
	}
}

// The whole point of the screen: one library narrows an indexer and the other
// is untouched.
func TestSetLibraryIndexerOverridesOneLibraryOnly(t *testing.T) {
	h, st, _ := newTestServer(t)
	seedIndexer(t, st, "shared", []int{2000, 5000}, true)
	ctx := context.Background()

	tv, err := st.GetLibraryByKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}

	rec := putLibraryIndexer(t, h, tv.ID, 1, `{"enabled":true,"categories":[5000]}`)
	wantStatus(t, rec, http.StatusOK)
	var updated libraryJSON
	decodeBody(t, rec, &updated)
	if updated.Kind != core.LibraryKindTV {
		t.Fatalf("response describes %q, want the tv library", updated.Kind)
	}
	row := updated.Indexers[0]
	if !reflect.DeepEqual(row.Categories, []int{5000}) || !row.CategoriesOverridden {
		t.Errorf("tv indexer row = %+v, want categories [5000] marked as an override", row)
	}
	if !reflect.DeepEqual(row.DefaultCategories, []int{2000, 5000}) {
		t.Errorf("default_categories = %v, want the indexer's own list", row.DefaultCategories)
	}

	list := do(t, h, http.MethodGet, "/api/v1/libraries", "")
	wantStatus(t, list, http.StatusOK)
	var body libraryListBody
	decodeBody(t, list, &body)
	movies := libraryOfKind(t, body, core.LibraryKindMovie).Indexers[0]
	if !reflect.DeepEqual(movies.Categories, []int{2000, 5000}) || movies.CategoriesOverridden {
		t.Errorf("movie indexer row = %+v, want the indexer's own categories, unoverridden", movies)
	}

	// And the resolver — what search actually reads — agrees with the JSON.
	settings, err := st.ResolveLibrarySettingsByKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("ResolveLibrarySettingsByKind: %v", err)
	}
	if len(settings.Indexers) != 1 || !reflect.DeepEqual(settings.Indexers[0].Categories, []int{5000}) {
		t.Errorf("resolved tv indexers = %+v, want the [5000] override", settings.Indexers)
	}
}

// A null categories list is the revert: it is what an absent row already means,
// and an empty list is a different answer ("search unfiltered") that must
// survive the round trip.
func TestSetLibraryIndexerDistinguishesNullFromEmptyCategories(t *testing.T) {
	h, st, _ := newTestServer(t)
	seedIndexer(t, st, "shared", []int{2000}, true)
	ctx := context.Background()
	movies, err := st.GetLibraryByKind(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}

	empty := putLibraryIndexer(t, h, movies.ID, 1, `{"enabled":true,"categories":[]}`)
	wantStatus(t, empty, http.StatusOK)
	var afterEmpty libraryJSON
	decodeBody(t, empty, &afterEmpty)
	if !afterEmpty.Indexers[0].CategoriesOverridden || len(afterEmpty.Indexers[0].Categories) != 0 {
		t.Errorf("row = %+v, want an override to the empty (unfiltered) list", afterEmpty.Indexers[0])
	}

	revert := putLibraryIndexer(t, h, movies.ID, 1, `{"enabled":true,"categories":null}`)
	wantStatus(t, revert, http.StatusOK)
	var afterRevert libraryJSON
	decodeBody(t, revert, &afterRevert)
	if afterRevert.Indexers[0].CategoriesOverridden {
		t.Errorf("row = %+v, want the override gone", afterRevert.Indexers[0])
	}
	if !reflect.DeepEqual(afterRevert.Indexers[0].Categories, []int{2000}) {
		t.Errorf("categories = %v, want the indexer's own list back", afterRevert.Indexers[0].Categories)
	}
}

func TestSetLibraryIndexerRejectsUnknownRows(t *testing.T) {
	h, st, _ := newTestServer(t)
	seedIndexer(t, st, "shared", []int{2000}, true)

	wantStatus(t, putLibraryIndexer(t, h, 99, 1, `{"enabled":false}`), http.StatusNotFound)
	wantStatus(t, putLibraryIndexer(t, h, 1, 99, `{"enabled":false}`), http.StatusNotFound)
	wantStatus(t, putLibraryIndexer(t, h, 1, 1, `{"enabled":true,"categories":[0]}`), http.StatusBadRequest)
	rec := do(t, h, http.MethodPut, "/api/v1/libraries/1/indexers/abc", `{"enabled":false}`)
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}

// PATCH is partial: a field the body does not mention keeps its value, and the
// zero value of a field it does mention is how an override is cleared.
func TestPatchLibraryOverridesAndClears(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	profile := &core.QualityProfile{Name: "1080p", Cutoff: "1080p", Items: []string{"1080p"}}
	if err := st.CreateQualityProfile(ctx, profile); err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}

	set := do(t, h, http.MethodPatch, "/api/v1/libraries/1",
		`{"route_torrent":"embedded","quality_profile_id":`+itoa(profile.ID)+`}`)
	wantStatus(t, set, http.StatusOK)
	var updated libraryJSON
	decodeBody(t, set, &updated)
	if updated.RouteTorrent != store.RouteEmbedded || updated.QualityProfileID != profile.ID {
		t.Fatalf("library = %+v, want the routing and profile overrides", updated)
	}
	if !updated.DLNAVisible {
		t.Error("dlna_visible changed, but the body never mentioned it")
	}

	clear := do(t, h, http.MethodPatch, "/api/v1/libraries/1",
		`{"route_torrent":"","quality_profile_id":0}`)
	wantStatus(t, clear, http.StatusOK)
	decodeBody(t, clear, &updated)
	if updated.RouteTorrent != "" || updated.QualityProfileID != 0 {
		t.Fatalf("library = %+v, want both overrides cleared", updated)
	}

	// Cleared means the global answers again, which is what the resolver reads.
	if err := st.SetSetting(ctx, store.SettingRouteTorrent, store.RouteEmbedded); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	settings, err := st.ResolveLibrarySettings(ctx, 1)
	if err != nil {
		t.Fatalf("ResolveLibrarySettings: %v", err)
	}
	if settings.RouteTorrent != store.RouteEmbedded {
		t.Errorf("resolved route_torrent = %q, want the global default", settings.RouteTorrent)
	}
}

// The routing values are the same values the global settings hold, so they are
// held to the same rule: a default that would silently reject every grab is
// refused where the user can see it, not at grab time.
func TestPatchLibraryRejectsUnroutableOverrides(t *testing.T) {
	h, _, _ := newTestServer(t)

	for _, body := range []string{
		`{"route_torrent":"404"}`,
		`{"route_usenet":"embedded"}`,
	} {
		rec := do(t, h, http.MethodPatch, "/api/v1/libraries/1", body)
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
	}

	rec := do(t, h, http.MethodPatch, "/api/v1/libraries/1", `{"quality_profile_id":404}`)
	wantStatus(t, rec, http.StatusNotFound)
	wantStatus(t, do(t, h, http.MethodPatch, "/api/v1/libraries/99", `{"dlna_visible":false}`), http.StatusNotFound)
}

// The library screen is an admin's: a member gets 403 from the gate itself,
// never a partial view.
func TestLibraryEndpointsAreAdminOnly(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	createUser(t, st, testMember, testPassword, core.RoleMember)
	member := login(t, h, testMember, testPassword)

	for _, tc := range []struct{ method, target, body string }{
		{http.MethodGet, "/api/v1/libraries", ""},
		{http.MethodPatch, "/api/v1/libraries/1", `{"dlna_visible":false}`},
		{http.MethodPut, "/api/v1/libraries/1/indexers/1", `{"enabled":false}`},
	} {
		rec := doAuth(t, h, tc.method, tc.target, tc.body, withCookie(member))
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403 for a member", tc.method, tc.target, rec.Code)
		}
	}

	admin := login(t, h, testAdmin, testPassword)
	rec := doAuth(t, h, http.MethodGet, "/api/v1/libraries", "", withCookie(admin))
	wantStatus(t, rec, http.StatusOK)
}

// putLibraryIndexer writes one (library, indexer) override through the API.
func putLibraryIndexer(t *testing.T, h http.Handler, libraryID, indexerID int64, body string) *httptest.ResponseRecorder {
	t.Helper()
	return do(t, h, http.MethodPut,
		"/api/v1/libraries/"+itoa(libraryID)+"/indexers/"+itoa(indexerID), body)
}
