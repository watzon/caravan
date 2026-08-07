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

// A disabled module leaves no library behind either.
//
// The Adult row is never deleted — that is deliberate, so turning the module
// back on finds the sites, the scenes and the files as they were — but a row
// that outlives the switch would keep GET /libraries answering with an "Adult"
// pill, its root path and its DLNA state on an install whose owner turned the
// whole thing off. The Libraries screen renders one pill per returned row, so
// the response IS the UI trace.
func TestLibrariesHideTheAdultRowWhenTheModuleIsOff(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newTestServer(t)
	seedIndexer(t, st, "jackett", []int{2000, 5000}, true)

	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}

	list := func() libraryListBody {
		t.Helper()
		rec := do(t, h, http.MethodGet, "/api/v1/libraries", "")
		wantStatus(t, rec, http.StatusOK)
		var body libraryListBody
		decodeBody(t, rec, &body)
		return body
	}

	// Enabled: the pill is there, which is what the phase asks for.
	adult := libraryOfKind(t, list(), core.LibraryKindAdult)
	if adult.Name != store.AdultLibraryName || adult.RootPath != store.AdultLibraryRoot {
		t.Fatalf("adult library = %+v, want the seeded name and root", adult)
	}
	// The owner shares it on the LAN, so the row carries state worth hiding.
	rec := do(t, h, http.MethodPatch, "/api/v1/libraries/"+itoa(adult.ID), `{"dlna_visible":true}`)
	wantStatus(t, rec, http.StatusOK)

	if err := st.SetAdultEnabled(ctx, false); err != nil {
		t.Fatalf("SetAdultEnabled(false): %v", err)
	}

	body := list()
	for _, l := range body.Libraries {
		if l.Kind == core.LibraryKindAdult {
			t.Fatalf("GET /libraries still names the adult library with the module off: %+v", l)
		}
	}
	if len(body.Libraries) != 2 {
		t.Errorf("libraries = %+v, want only Movies and Series", body.Libraries)
	}

	// And the row is not reachable by id either, or the pill would be gone
	// while the card behind it stayed fully editable. 404, not 403: "there is
	// nothing here" is the same answer every adult route gives.
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodPatch, "/api/v1/libraries/" + itoa(adult.ID), `{"dlna_visible":false}`},
		{http.MethodPut, "/api/v1/libraries/" + itoa(adult.ID) + "/indexers/1", `{"enabled":false}`},
	} {
		rec := do(t, h, tc.method, tc.target, tc.body)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404", tc.method, tc.target, rec.Code)
		}
	}

	// Turning it back on finds the row exactly as it was left.
	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled(true): %v", err)
	}
	if back := libraryOfKind(t, list(), core.LibraryKindAdult); !back.DLNAVisible {
		t.Errorf("re-enabling the module forgot the library's state: %+v", back)
	}
}

// The Libraries screen renders the per-library matrix from the RAW override
// table, where "no row" is a hole it fills with a default. That default has to
// be the one a search would actually resolve, or the card describes a fan-out
// that never happens: the Adult library falls back to the 6000 block rather
// than to the indexer's own movie and television categories.
func TestAdultLibraryCardShowsTheAdultCategoriesAsItsDefault(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newTestServer(t)
	cfg := seedIndexer(t, st, "jackett", []int{2000, 5000}, true)
	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/libraries", "")
	wantStatus(t, rec, http.StatusOK)
	var body libraryListBody
	decodeBody(t, rec, &body)

	adult := libraryOfKind(t, body, core.LibraryKindAdult)
	if len(adult.Indexers) != 1 {
		t.Fatalf("adult indexer rows = %+v, want one", adult.Indexers)
	}
	row := adult.Indexers[0]
	if !reflect.DeepEqual(row.DefaultCategories, []int{core.AdultCategoryBase}) {
		t.Errorf("adult default categories = %v, want the 6000 block", row.DefaultCategories)
	}
	if !reflect.DeepEqual(row.Categories, []int{core.AdultCategoryBase}) {
		t.Errorf("adult effective categories = %v, want the 6000 block", row.Categories)
	}
	if row.CategoriesOverridden {
		t.Error("the fallback was reported as an override the owner wrote")
	}

	// The television card is untouched: it still inherits the indexer's own.
	tv := libraryOfKind(t, body, core.LibraryKindTV)
	if len(tv.Indexers) != 1 || !reflect.DeepEqual(tv.Indexers[0].DefaultCategories, []int{2000, 5000}) {
		t.Errorf("television default categories = %+v, want the indexer's own", tv.Indexers)
	}

	// And what the card claims is what the search resolves.
	settings, err := st.ResolveLibrarySettingsByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("ResolveLibrarySettingsByKind: %v", err)
	}
	if len(settings.Indexers) != 1 || settings.Indexers[0].ID != cfg.ID {
		t.Fatalf("resolved indexers = %+v, want the one configured", settings.Indexers)
	}
	if !reflect.DeepEqual(settings.Indexers[0].Categories, row.Categories) {
		t.Errorf("the card shows %v but a search sends %v",
			row.Categories, settings.Indexers[0].Categories)
	}
}

// The create endpoint owns every guard the schema does not: kind and provider
// agreement, root placement under library/, and no nesting among roots. A
// created library starts with DLNA sharing off (the per-library tree is a
// later phase) and is never the default.
func TestCreateLibraryValidatesAndCreates(t *testing.T) {
	h, st, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/libraries",
		`{"kind":"tv","name":"Anime","root_path":"library/Anime"}`)
	wantStatus(t, rec, http.StatusCreated)
	var created libraryJSON
	decodeBody(t, rec, &created)
	if created.Provider != core.ProviderTMDB || created.IsDefault || created.DLNAVisible {
		t.Errorf("created = %+v, want tmdb default provider, not default, dlna off", created)
	}

	for name, body := range map[string]string{
		"root outside library/": `{"kind":"tv","name":"X","root_path":"media/X"}`,
		"root is library/":      `{"kind":"tv","name":"X","root_path":"library"}`,
		"nested root":           `{"kind":"tv","name":"X","root_path":"library/Anime/Sub"}`,
		"duplicate root":        `{"kind":"movie","name":"X","root_path":"library/Anime"}`,
		"dotdot root":           `{"kind":"tv","name":"X","root_path":"library/../etc"}`,
		"wrong provider":        `{"kind":"movie","name":"X","root_path":"library/X","provider":"stashbox"}`,
		"unknown kind":          `{"kind":"music","name":"X","root_path":"library/X"}`,
		"adult without module":  `{"kind":"adult","name":"X","root_path":"library/X"}`,
		"empty name":            `{"kind":"tv","name":"  ","root_path":"library/X"}`,
	} {
		rec := do(t, h, http.MethodPost, "/api/v1/libraries", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}

	// Deleting: the fresh, empty, non-default library goes; the defaults stay.
	rec = do(t, h, http.MethodDelete, "/api/v1/libraries/"+itoa(created.ID), "")
	wantStatus(t, rec, http.StatusNoContent)

	def, err := st.GetDefaultLibrary(context.Background(), core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetDefaultLibrary: %v", err)
	}
	rec = do(t, h, http.MethodDelete, "/api/v1/libraries/"+itoa(def.ID), "")
	wantStatus(t, rec, http.StatusConflict)
}

// Deleting a library that still owns items is refused with the count-bearing
// message; promoting another library moves the default flag transactionally;
// and a non-default library may be shared over DLNA, which is what the tree's
// per-library containers made possible.
func TestLibraryDefaultHandoffAndGuards(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/libraries",
		`{"kind":"tv","name":"Anime","root_path":"library/Anime"}`)
	wantStatus(t, rec, http.StatusCreated)
	var anime libraryJSON
	decodeBody(t, rec, &anime)

	sr := &core.Series{TMDBID: 7, Title: "Frieren", Kind: core.SeriesKindTV, LibraryID: anime.ID}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	rec = do(t, h, http.MethodDelete, "/api/v1/libraries/"+itoa(anime.ID), "")
	wantStatus(t, rec, http.StatusConflict)

	rec = do(t, h, http.MethodPatch, "/api/v1/libraries/"+itoa(anime.ID), `{"dlna_visible":true}`)
	wantStatus(t, rec, http.StatusOK)
	var shared libraryJSON
	decodeBody(t, rec, &shared)
	if !shared.DLNAVisible {
		t.Errorf("library = %+v, want dlna_visible saved on a non-default library", shared)
	}
	saved, err := st.GetLibrary(ctx, anime.ID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	if !saved.DLNAVisible {
		t.Error("the flag came back on the response but never reached the row")
	}

	rec = do(t, h, http.MethodPatch, "/api/v1/libraries/"+itoa(anime.ID), `{"is_default":true}`)
	wantStatus(t, rec, http.StatusOK)
	def, err := st.GetDefaultLibrary(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetDefaultLibrary: %v", err)
	}
	if def.ID != anime.ID {
		t.Errorf("default tv library = %+v, want Anime promoted", def)
	}
	rec = do(t, h, http.MethodPatch, "/api/v1/libraries/"+itoa(anime.ID), `{"is_default":false}`)
	wantStatus(t, rec, http.StatusBadRequest)
}

// The provider list is the create form's vocabulary. Without the adult module
// it must not name a provider that serves only adult libraries: a picker
// entry is a trace of a module whose promise is absence.
func TestListProvidersOmitsAdultWithoutTheModule(t *testing.T) {
	h, _, _ := newTestServer(t)
	rec := do(t, h, http.MethodGet, "/api/v1/libraries/providers", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Providers []struct {
			ID    string   `json:"id"`
			Kinds []string `json:"kinds"`
		} `json:"providers"`
	}
	decodeBody(t, rec, &body)
	for _, p := range body.Providers {
		if p.ID == core.ProviderStashbox {
			t.Errorf("providers = %+v, want stashbox absent with the module off", body.Providers)
		}
		for _, k := range p.Kinds {
			if k == core.LibraryKindAdult {
				t.Errorf("provider %s lists the adult kind with the module off", p.ID)
			}
		}
	}
}

// A library carries an ordered provider CHAIN, and the wire keeps both
// spellings: `providers` is the list, `provider` is its read-only head, so a
// client written before chains keeps reading exactly what it always did.
func TestCreateLibraryAcceptsAProviderChain(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/libraries",
		`{"kind":"tv","name":"Anime","root_path":"library/Anime","providers":["tmdb"]}`)
	wantStatus(t, rec, http.StatusCreated)
	var created libraryJSON
	decodeBody(t, rec, &created)
	if created.Provider != core.ProviderTMDB ||
		!reflect.DeepEqual(created.Providers, []string{core.ProviderTMDB}) {
		t.Errorf("created = %+v, want the chain and its head", created)
	}

	// The singular spelling is still a chain of one.
	rec = do(t, h, http.MethodPost, "/api/v1/libraries",
		`{"kind":"movie","name":"Docs","root_path":"library/Docs","provider":"tmdb"}`)
	wantStatus(t, rec, http.StatusCreated)
	decodeBody(t, rec, &created)
	if !reflect.DeepEqual(created.Providers, []string{core.ProviderTMDB}) {
		t.Errorf("created = %+v, want the singular provider read as a chain", created)
	}

	for name, body := range map[string]string{
		"repeated provider": `{"kind":"tv","name":"X","root_path":"library/X","providers":["tmdb","tmdb"]}`,
		"wrong kind":        `{"kind":"movie","name":"X","root_path":"library/X","providers":["stashbox"]}`,
		"unknown provider":  `{"kind":"tv","name":"X","root_path":"library/X","providers":["nope"]}`,
	} {
		rec := do(t, h, http.MethodPost, "/api/v1/libraries", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
}

// PATCH takes the same chain under the same rules. An explicit empty list is a
// request to make the library identify nothing, which is refused rather than
// quietly read as "leave it alone".
func TestPatchLibraryValidatesTheProviderChain(t *testing.T) {
	h, st, _ := newTestServer(t)
	def, err := st.GetDefaultLibrary(context.Background(), core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetDefaultLibrary: %v", err)
	}
	path := "/api/v1/libraries/" + itoa(def.ID)

	rec := do(t, h, http.MethodPatch, path, `{"providers":["tmdb"]}`)
	wantStatus(t, rec, http.StatusOK)
	var patched libraryJSON
	decodeBody(t, rec, &patched)
	if patched.Provider != core.ProviderTMDB ||
		!reflect.DeepEqual(patched.Providers, []string{core.ProviderTMDB}) {
		t.Errorf("patched = %+v, want the chain and its head", patched)
	}

	for name, body := range map[string]string{
		"empty chain":       `{"providers":[]}`,
		"repeated provider": `{"providers":["tmdb","tmdb"]}`,
		"wrong kind":        `{"providers":["stashbox"]}`,
		"singular wrong":    `{"provider":"stashbox"}`,
	} {
		rec := do(t, h, http.MethodPatch, path, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", name, rec.Code)
		}
	}
}

// An adult library's chain can only ever be ["stashbox"], and not because a
// rule says so: no other compiled-in provider serves the adult kind, and the
// per-element check is what makes that a consequence rather than a promise.
func TestProviderChainForAdultIsStashboxAlone(t *testing.T) {
	cases := []struct {
		chain []string
		want  bool
	}{
		{[]string{core.ProviderStashbox}, true},
		{[]string{core.ProviderStashbox, core.ProviderTMDB}, false},
		{[]string{core.ProviderTMDB}, false},
		{nil, false},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		if got := validProviderChain(w, c.chain, core.LibraryKindAdult); got != c.want {
			t.Errorf("validProviderChain(%v, adult) = %v, want %v", c.chain, got, c.want)
		}
	}
}
