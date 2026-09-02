package api

import (
	"context"
	"log/slog"
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

// A freshly migrated install answers with the four seeded libraries, no
// overrides anywhere, and every configured indexer searched with its own
// categories. The JSON shape of "nothing has changed yet".
//
// This is the management surface, so the two dormant rows are here: an admin
// has to be able to see Anime and Adult to switch either on. Every
// content route still answers 404 for them, and /auth/me still lists neither.
func TestListLibrariesReportsSeededDefaults(t *testing.T) {
	h, st, _ := newTestServer(t)
	seedIndexer(t, st, "jackett", []int{2000, 5000}, true)

	rec := do(t, h, http.MethodGet, "/api/v1/libraries", "")
	wantStatus(t, rec, http.StatusOK)
	var body libraryListBody
	decodeBody(t, rec, &body)

	if len(body.Libraries) != 4 {
		t.Fatalf("libraries = %+v, want the four seeded rows", body.Libraries)
	}
	for _, kind := range []string{core.LibraryKindAnime, core.LibraryKindAdult} {
		lib := libraryOfKind(t, body, kind)
		if lib.Active || !lib.IsDefault {
			t.Errorf("seeded %s library = %+v, want a dormant default", kind, lib)
		}
		if lib.Icon != "" {
			t.Errorf("seeded %s library icon = %q, want the kind default", kind, lib.Icon)
		}
	}
	movies := libraryOfKind(t, body, core.LibraryKindMovie)
	if movies.Name != "Movies" || movies.RootPath == "" {
		t.Errorf("movie library = %+v, want the seeded name and root path", movies)
	}
	if movies.Slug != "movies" {
		t.Errorf("movie library slug = %q, want %q", movies.Slug, "movies")
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

	// And the resolver (what search actually reads) agrees with the JSON.
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

// A dormant library is still the admin's to work, and nobody else's to see.
//
// The two halves are the whole point of the manageable/visible split. Every
// content route answers 404 for an inactive library, an admin included: that is
// what "dormant for everyone" means. The management surface keeps answering,
// because the toggle that undoes dormancy lives on it: a library switched off
// and then dropped from the only list it appears in would be a one-way door.
//
// GET /libraries is admin-only (routePolicies; memberAllowed names none of it),
// so the row a member was never meant to see does not reach one here. The
// filter that hides it from a member is the same `manages` predicate, with the
// role rule inside core.LibraryVisible doing the work.
func TestDormantLibrariesStayManageableAndUnreachable(t *testing.T) {
	h, st, _ := newTestServer(t)
	seedIndexer(t, st, "jackett", []int{2000, 5000}, true)

	enableAdultLibrary(t, st)

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

	// Switched off through the door that does it, which is the toggle the
	// screen renders and not a store call reaching around it.
	wantStatus(t, do(t, h, http.MethodPatch, "/api/v1/libraries/"+itoa(adult.ID),
		`{"active":false}`), http.StatusOK)

	dormant := libraryOfKind(t, list(), core.LibraryKindAdult)
	if dormant.Active {
		t.Fatalf("the switched-off library reads as active: %+v", dormant)
	}
	// Its DLNA state is remembered rather than cleared: dormancy is not
	// un-sharing, and switching it back on must not need the owner to redo a
	// decision they never revoked.
	if !dormant.DLNAVisible {
		t.Errorf("switching the library off forgot that it was shared: %+v", dormant)
	}

	// Content routes are shut for the admin too. 404, not 403: "there is
	// nothing here" is the same answer every gated route gives.
	for _, target := range []string{
		"/api/v1/adult/sites",
		"/api/v1/adult/discover",
	} {
		if rec := do(t, h, http.MethodGet, target, ""); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s = %d with the library off, want 404", target, rec.Code)
		}
	}

	// The management routes are not, or there would be no way back.
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodPatch, "/api/v1/libraries/" + itoa(adult.ID), `{"dlna_visible":true}`},
		{http.MethodPut, "/api/v1/libraries/" + itoa(adult.ID) + "/indexers/1", `{"enabled":false}`},
		{http.MethodGet, "/api/v1/libraries/" + itoa(adult.ID) + "/access", ""},
		{http.MethodPut, "/api/v1/libraries/" + itoa(adult.ID) + "/access", `{"restricted":true}`},
	} {
		rec := do(t, h, tc.method, tc.target, tc.body)
		if rec.Code != http.StatusOK {
			t.Errorf("%s %s = %d with the library off, want 200 (body %q)",
				tc.method, tc.target, rec.Code, rec.Body.String())
		}
	}

	// And back on, through the same toggle, with the state it was left in. The
	// access write above restricted it, which clears dlna_visible, so the PATCH
	// that re-shared it is what this reads back.
	wantStatus(t, do(t, h, http.MethodPatch, "/api/v1/libraries/"+itoa(adult.ID),
		`{"active":true,"dlna_visible":true}`), http.StatusOK)
	back := libraryOfKind(t, list(), core.LibraryKindAdult)
	if !back.Active || !back.DLNAVisible {
		t.Errorf("switching the library back on forgot its state: %+v", back)
	}
}

// The Libraries screen renders the per-library matrix from the raw override
// table, where "no row" is a hole it fills with a default. That default has to
// be the one a search would actually resolve, or the card describes a fan-out
// that never happens: the Adult library falls back to the 6000 block rather
// than to the indexer's own movie and television categories.
func TestAdultLibraryCardShowsTheAdultCategoriesAsItsDefault(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newTestServer(t)
	cfg := seedIndexer(t, st, "jackett", []int{2000, 5000}, true)
	enableAdultLibrary(t, st)

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
		`{"kind":"tv","name":"Kids","root_path":"library/Kids"}`)
	wantStatus(t, rec, http.StatusCreated)
	var created libraryJSON
	decodeBody(t, rec, &created)
	if created.Provider != core.ProviderTMDB || created.IsDefault || created.DLNAVisible {
		t.Errorf("created = %+v, want tmdb default provider, not default, dlna off", created)
	}

	for name, body := range map[string]string{
		"root outside library/": `{"kind":"tv","name":"X","root_path":"media/X"}`,
		"root is library/":      `{"kind":"tv","name":"X","root_path":"library"}`,
		"nested root":           `{"kind":"tv","name":"X","root_path":"library/Kids/Sub"}`,
		"duplicate root":        `{"kind":"movie","name":"X","root_path":"library/Kids"}`,
		"dotdot root":           `{"kind":"tv","name":"X","root_path":"library/../etc"}`,
		"wrong provider":        `{"kind":"movie","name":"X","root_path":"library/X","provider":"stashbox"}`,
		"unknown kind":          `{"kind":"music","name":"X","root_path":"library/X"}`,
		"empty name":            `{"kind":"tv","name":"  ","root_path":"library/X"}`,
		// An adult chain that names a particular box gets no bootstrap benefit:
		// only the bare legacy id is a forward reference to the instance about
		// to be minted, and a qualified one has nothing to resolve into.
		"adult on a named box": `{"kind":"adult","name":"X","root_path":"library/X","providers":["stashbox:fansdb"]}`,
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
		`{"kind":"tv","name":"Kids","root_path":"library/Kids"}`)
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

// AniList is a non-adult provider, so the create form offers it to everybody,
// for the anime kind and for that kind alone. The registry partitions strictly
// by library kind, so the chain editor of a Movies or a Series library never
// names it, whatever AniList can look up (core.ProviderLooksUp).
func TestListProvidersOffersAniListForItsKinds(t *testing.T) {
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
		if p.ID != core.ProviderAniList {
			continue
		}
		want := []string{core.LibraryKindAnime}
		if !reflect.DeepEqual(p.Kinds, want) {
			t.Errorf("anilist kinds = %v, want %v", p.Kinds, want)
		}
		return
	}
	t.Errorf("providers = %+v, want anilist listed", body.Providers)
}

// A library carries an ordered provider chain, and the wire keeps both
// spellings: `providers` is the list, `provider` is its read-only head, so a
// client written before chains keeps reading exactly what it always did.
func TestCreateLibraryAcceptsAProviderChain(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/libraries",
		`{"kind":"tv","name":"Kids","root_path":"library/Kids","providers":["tmdb"]}`)
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

// An adult library's chain can only ever name stash-box instances, and not
// because a rule says so: no other compiled-in provider serves the adult kind,
// and the per-element check is what makes that a consequence rather than a
// promise. The instance half is a database question. A well-formed id for a box
// this install does not hold is refused too, because a chain that names one
// walks to a provider nothing can build.
func TestProviderChainForAdultNamesConfiguredInstancesAlone(t *testing.T) {
	st := newTestStore(t)
	seedStashboxInstance(t, st, core.ProviderStashbox, "StashDB", "https://stashdb.org/graphql")
	seedStashboxInstance(t, st, core.ProviderStashbox+":fansdb", "FansDB", "https://fansdb.cc/graphql")
	s := &server{st: st, log: slog.Default()}

	cases := []struct {
		chain []string
		want  bool
	}{
		{[]string{core.ProviderStashbox}, true},
		{[]string{core.ProviderStashbox, core.ProviderStashbox + ":fansdb"}, true},
		{[]string{core.ProviderStashbox, core.ProviderStashbox}, false},
		{[]string{core.ProviderStashbox + ":pmvstash"}, false},
		{[]string{core.ProviderStashbox, core.ProviderTMDB}, false},
		{[]string{core.ProviderTMDB}, false},
		{nil, false},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		if got := s.validProviderChain(context.Background(), w, c.chain, core.LibraryKindAdult); got != c.want {
			t.Errorf("validProviderChain(%v, adult) = %v, want %v (body %q)",
				c.chain, got, c.want, w.Body.String())
		}
	}
}

// The registry partitions strictly by library kind, and the create endpoint is
// where an owner feels it: a shelf may only be chained to the catalogue that
// kind is filed under.
//
// The anime kind is the case the partition exists for. An anime shelf whose
// chain mixed AniList's flat records with TMDB's or TheTVDB's seasons would
// renumber episodes rather than fill a gap (a rung that answers wrong is worse
// than a rung that answers nothing) so the anime kind is AniList's alone and
// AniList reaches no other kind.
func TestProviderChainsPartitionStrictlyByLibraryKind(t *testing.T) {
	cases := []struct {
		name, body string
		want       int
	}{
		{"anime on anilist", `{"kind":"anime","name":"A","root_path":"library/A","providers":["anilist"]}`,
			http.StatusCreated},
		{"anime on tmdb", `{"kind":"anime","name":"B","root_path":"library/B","providers":["tmdb"]}`,
			http.StatusBadRequest},
		{"anime on thetvdb", `{"kind":"anime","name":"C","root_path":"library/C","providers":["thetvdb"]}`,
			http.StatusBadRequest},
		{"anime chaining anilist then tmdb", `{"kind":"anime","name":"D","root_path":"library/D","providers":["anilist","tmdb"]}`,
			http.StatusBadRequest},
		{"tv on anilist", `{"kind":"tv","name":"E","root_path":"library/E","providers":["anilist"]}`,
			http.StatusBadRequest},
		{"movie on anilist", `{"kind":"movie","name":"F","root_path":"library/F","providers":["anilist"]}`,
			http.StatusBadRequest},
		// The television kind keeps its three catalogues: they file the same
		// vocabulary, so a chain of them is a fallback rather than a conflict.
		{"tv on tmdb, tvmaze and thetvdb",
			`{"kind":"tv","name":"G","root_path":"library/G","providers":["tmdb","tvmaze","thetvdb"]}`,
			http.StatusCreated},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _, _ := newTestServer(t)
			rec := do(t, h, http.MethodPost, "/api/v1/libraries", c.body)
			wantStatus(t, rec, c.want)
		})
	}
}

// The other half of the split: what a provider may be chained on says nothing
// about what it may be asked to look up.
//
// An AniList film ref is accepted on POST /library/movies even though AniList
// chains onto no movie library, because a ref pasted off a search hit is the
// user naming the title outright. The chain governs identification, which is
// the question nobody is asking here. Before the split this add could only be
// made to work by widening AniList's chain kinds, which put it in every Movies
// library's chain editor.
func TestAddAcceptsARefFromAProviderThatChainsOnNoSuchLibrary(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/library/movies",
		`{"provider":"anilist","provider_ref":"21519"}`)
	wantStatus(t, rec, http.StatusCreated)
	if core.ProviderServes(core.ProviderAniList, core.LibraryKindMovie) {
		t.Error("AniList chains onto the movie kind again; the two questions have re-merged")
	}

	// A provider whose catalogue genuinely files no films is still refused, and
	// refused at the door rather than on the next refresh: TheTVDB answers
	// GetMovie with ErrProviderKindUnsupported.
	rec = do(t, h, http.MethodPost, "/api/v1/library/movies",
		`{"provider":"thetvdb","provider_ref":"70327"}`)
	wantStatus(t, rec, http.StatusBadRequest)
}
