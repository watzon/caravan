package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/parse"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/wanted"
)

func pastDate() time.Time   { return time.Now().UTC().AddDate(0, 0, -7) }
func futureDate() time.Time { return time.Now().UTC().AddDate(0, 0, 7) }

func TestQualityProfileCRUD(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	// The seeded default is always there.
	rec := do(t, h, http.MethodGet, "/api/v1/quality-profiles", "")
	wantStatus(t, rec, http.StatusOK)
	var list struct {
		Profiles []qualityProfileJSON `json:"profiles"`
	}
	decodeBody(t, rec, &list)
	if len(list.Profiles) != 1 || list.Profiles[0].Name != "Standard" || !list.Profiles[0].IsDefault {
		t.Fatalf("profiles = %+v, want the seeded Standard default profile", list.Profiles)
	}
	if list.Profiles[0].Assignments != (profileAssignmentsJSON{}) {
		t.Fatalf("seeded assignments = %+v, want none", list.Profiles[0].Assignments)
	}

	rec = do(t, h, http.MethodPost, "/api/v1/quality-profiles",
		`{"name":"HD only","cutoff":"1080p","items":["2160p","1080p"],"upgrade_allowed":true}`)
	wantStatus(t, rec, http.StatusCreated)
	var created qualityProfileJSON
	decodeBody(t, rec, &created)
	if created.ID == 0 {
		t.Fatal("created profile has no id")
	}
	if created.Assignments != (profileAssignmentsJSON{}) {
		t.Fatalf("created assignments = %+v, want none", created.Assignments)
	}

	// A duplicate name is a conflict, not a 500.
	rec = do(t, h, http.MethodPost, "/api/v1/quality-profiles",
		`{"name":"HD only","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true}`)
	wantStatus(t, rec, http.StatusConflict)

	// The cutoff must be reachable from the items.
	rec = do(t, h, http.MethodPost, "/api/v1/quality-profiles",
		`{"name":"broken","cutoff":"720p","items":["1080p"],"upgrade_allowed":true}`)
	wantStatus(t, rec, http.StatusBadRequest)

	rec = do(t, h, http.MethodPut, "/api/v1/quality-profiles/"+itoa(created.ID),
		`{"name":"HD only","cutoff":"1080p","items":["1080p","720p"],"upgrade_allowed":false}`)
	wantStatus(t, rec, http.StatusOK)
	var updated qualityProfileJSON
	decodeBody(t, rec, &updated)
	if updated.UpgradeAllowed || len(updated.Items) != 2 {
		t.Fatalf("updated profile = %+v", updated)
	}
	if updated.Assignments != (profileAssignmentsJSON{}) {
		t.Fatalf("updated assignments = %+v, want none", updated.Assignments)
	}

	// The default profile is protected; any other deletable.
	defaults, err := st.ListQualityProfiles(ctx)
	if err != nil {
		t.Fatalf("ListQualityProfiles: %v", err)
	}
	rec = do(t, h, http.MethodDelete, "/api/v1/quality-profiles/"+itoa(defaults[0].ID), "")
	wantStatus(t, rec, http.StatusConflict)
	rec = do(t, h, http.MethodDelete, "/api/v1/quality-profiles/"+itoa(created.ID), "")
	wantStatus(t, rec, http.StatusNoContent)
}

func TestResolveQualityProfileUsesExplicitDefaultAndRepairsLegacySettings(t *testing.T) {
	_, st, _ := newTestServer(t)
	ctx := context.Background()

	standard, err := st.ResolveQualityProfile(ctx, 0)
	if err != nil {
		t.Fatalf("ResolveQualityProfile(0): %v", err)
	}
	if standard.Name != "Standard" {
		t.Fatalf("default = %q, want Standard", standard.Name)
	}

	// A database upgraded from before migration 0015 has no persisted key.
	// Resolving it preserves the historical oldest-profile fallback and writes
	// the explicit replacement exactly once.
	if err := st.DeleteSetting(ctx, store.SettingDefaultQualityProfileID); err != nil {
		t.Fatalf("DeleteSetting(default): %v", err)
	}
	repaired, err := st.ResolveQualityProfile(ctx, 0)
	if err != nil {
		t.Fatalf("ResolveQualityProfile after missing setting: %v", err)
	}
	if repaired.ID != standard.ID {
		t.Fatalf("repaired default = %d, want oldest profile %d", repaired.ID, standard.ID)
	}
	if value, err := st.GetSetting(ctx, store.SettingDefaultQualityProfileID); err != nil || value != itoa(standard.ID) {
		t.Fatalf("persisted default = %q, %v; want %d", value, err, standard.ID)
	}

	// A stale id is repaired to the same valid oldest profile instead of
	// leaving every later read to retry the fallback.
	if err := st.SetSetting(ctx, store.SettingDefaultQualityProfileID, "999"); err != nil {
		t.Fatalf("SetSetting(stale default): %v", err)
	}
	repaired, err = st.ResolveQualityProfile(ctx, 0)
	if err != nil {
		t.Fatalf("ResolveQualityProfile after stale setting: %v", err)
	}
	if repaired.ID != standard.ID {
		t.Fatalf("stale default resolved to %d, want %d", repaired.ID, standard.ID)
	}
	if value, err := st.GetSetting(ctx, store.SettingDefaultQualityProfileID); err != nil || value != itoa(standard.ID) {
		t.Fatalf("repaired stale default = %q, %v; want %d", value, err, standard.ID)
	}

	libraryProfile := &core.QualityProfile{Name: "Library", Cutoff: "720p", Items: []string{"720p"}, UpgradeAllowed: true}
	if err := st.CreateQualityProfile(ctx, libraryProfile); err != nil {
		t.Fatalf("CreateQualityProfile(library): %v", err)
	}
	itemProfile := &core.QualityProfile{Name: "Item", Cutoff: "2160p", Items: []string{"2160p"}, UpgradeAllowed: true}
	if err := st.CreateQualityProfile(ctx, itemProfile); err != nil {
		t.Fatalf("CreateQualityProfile(item): %v", err)
	}
	movieLibrary, err := st.GetLibraryByKind(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("GetLibraryByKind(movie): %v", err)
	}
	movieLibrary.QualityProfileID = libraryProfile.ID
	if err := st.UpdateLibrary(ctx, movieLibrary); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}

	item, err := st.ResolveItemQualityProfile(ctx, core.LibraryKindMovie, itemProfile.ID)
	if err != nil {
		t.Fatalf("ResolveItemQualityProfile(item): %v", err)
	}
	if item.ID != itemProfile.ID {
		t.Fatalf("item override resolved to %d, want %d", item.ID, itemProfile.ID)
	}
	library, err := st.ResolveItemQualityProfile(ctx, core.LibraryKindMovie, 0)
	if err != nil {
		t.Fatalf("ResolveItemQualityProfile(library): %v", err)
	}
	if library.ID != libraryProfile.ID {
		t.Fatalf("library default resolved to %d, want %d", library.ID, libraryProfile.ID)
	}
	movieLibrary.QualityProfileID = 0
	if err := st.UpdateLibrary(ctx, movieLibrary); err != nil {
		t.Fatalf("clear library default: %v", err)
	}
	system, err := st.ResolveItemQualityProfile(ctx, core.LibraryKindMovie, 0)
	if err != nil {
		t.Fatalf("ResolveItemQualityProfile(system): %v", err)
	}
	if system.ID != standard.ID {
		t.Fatalf("system default resolved to %d, want %d", system.ID, standard.ID)
	}
}

func TestSetDefaultQualityProfile(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	rec := do(t, h, http.MethodPost, "/api/v1/quality-profiles",
		`{"name":"Premium","cutoff":"2160p","items":["2160p"],"upgrade_allowed":true}`)
	wantStatus(t, rec, http.StatusCreated)
	var created qualityProfileJSON
	decodeBody(t, rec, &created)
	if created.IsDefault {
		t.Fatal("new profile unexpectedly marked default")
	}

	rec = do(t, h, http.MethodPut, "/api/v1/quality-profiles/"+itoa(created.ID)+"/default", "")
	wantStatus(t, rec, http.StatusOK)
	var set qualityProfileJSON
	decodeBody(t, rec, &set)
	if !set.IsDefault || set.ID != created.ID {
		t.Fatalf("set default response = %+v, want profile %d marked default", set, created.ID)
	}
	resolved, err := st.ResolveQualityProfile(ctx, 0)
	if err != nil {
		t.Fatalf("ResolveQualityProfile: %v", err)
	}
	if resolved.ID != created.ID {
		t.Fatalf("resolved default = %d, want %d", resolved.ID, created.ID)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/quality-profiles", "")
	wantStatus(t, rec, http.StatusOK)
	var list struct {
		Profiles []qualityProfileJSON `json:"profiles"`
	}
	decodeBody(t, rec, &list)
	for _, profile := range list.Profiles {
		if profile.IsDefault != (profile.ID == created.ID) {
			t.Fatalf("profile %d default = %t, want %t", profile.ID, profile.IsDefault, profile.ID == created.ID)
		}
	}
}

func TestQualityProfileTestParsesAndScoresTitles(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	profile := &core.QualityProfile{
		Name:           "HD test",
		Cutoff:         core.Quality1080p,
		Items:          []string{core.Quality2160p, core.Quality1080p},
		UpgradeAllowed: true,
	}
	if err := st.CreateQualityProfile(ctx, profile); err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/quality-profiles/"+itoa(profile.ID)+"/test",
		`{"titles":["Big.Buck.Bunny.2008.1080p.WEB-DL.PROPER.REPACK","Big.Buck.Bunny.2008.720p.WEB-DL"]}`)
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Results []profileTestResultJSON `json:"results"`
	}
	decodeBody(t, rec, &body)
	if len(body.Results) != 2 {
		t.Fatalf("results = %+v, want two rows", body.Results)
	}

	accepted := body.Results[0]
	if accepted.Title != "Big.Buck.Bunny.2008.1080p.WEB-DL.PROPER.REPACK" || accepted.Parsed.Quality != core.Quality1080p {
		t.Fatalf("accepted result = %+v, want raw title and production parser tags", accepted)
	}
	if !accepted.Decision.Accepted || accepted.Decision.ProfileID != profile.ID || accepted.Decision.ProfileName != profile.Name {
		t.Fatalf("accepted decision = %+v", accepted.Decision)
	}
	if accepted.Decision.Score != accepted.Decision.Contributions.Quality+
		accepted.Decision.Contributions.Source+
		accepted.Decision.Contributions.Proper+
		accepted.Decision.Contributions.Repack+
		accepted.Decision.Contributions.Seeders+
		accepted.Decision.Contributions.CustomFormats+
		accepted.Decision.Contributions.TVCompatibility {
		t.Fatalf("accepted decision score = %+v, want its contribution total", accepted.Decision)
	}
	if want := `accepted by profile "HD test"`; accepted.Decision.Reason != want {
		t.Fatalf("accepted reason = %q, want %q", accepted.Decision.Reason, want)
	}

	rejected := body.Results[1]
	if rejected.Decision.Accepted || rejected.Decision.Score != 0 {
		t.Fatalf("rejected decision = %+v, want a zero-score rejection", rejected.Decision)
	}
	if rejected.Decision.Contributions != (profileContributionsJSON{}) {
		t.Fatalf("rejected contributions = %+v, want none", rejected.Decision.Contributions)
	}
	if want := `quality 720p is not in profile "HD test"`; rejected.Decision.Reason != want {
		t.Fatalf("rejected reason = %q, want %q", rejected.Decision.Reason, want)
	}
}

func TestQualityProfileAcquisitionPolicyAPI(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	body := `{
		"name":"Policy",
		"cutoff":"1080p",
		"items":["1080p"],
		"upgrade_allowed":true,
		"preferred_sources":["webdl","bluray"],
		"proper_repack_preference":"neutral",
		"min_seeders":5,
		"min_size_mb":700,
		"max_size_mb":8000,
		"custom_formats":[
			{"name":"HDR","include_terms":["hdr"],"exclude_terms":["dv"],"score":25}
		],
		"tv_profile":"capable",
		"tv_compatibility_policy":"prefer"
	}`
	rec := do(t, h, http.MethodPost, "/api/v1/quality-profiles", body)
	wantStatus(t, rec, http.StatusCreated)
	var created qualityProfileJSON
	decodeBody(t, rec, &created)
	if created.ProperRepackPreference != core.ProperRepackPreferenceNeutral ||
		len(created.PreferredSources) != 2 ||
		created.MinSeeders != 5 ||
		created.MinSizeMB != 700 ||
		created.MaxSizeMB != 8_000 ||
		len(created.CustomFormats) != 1 ||
		created.TVProfile != core.TVProfileCapable ||
		created.TVCompatibilityPolicy != core.TVCompatibilityPolicyPrefer {
		t.Fatalf("created policy = %+v", created)
	}

	rec = do(t, h, http.MethodPut, "/api/v1/quality-profiles/"+itoa(created.ID), strings.Replace(body, "Policy", "Policy updated", 1))
	wantStatus(t, rec, http.StatusOK)
	var updated qualityProfileJSON
	decodeBody(t, rec, &updated)
	if updated.Name != "Policy updated" || updated.TVCompatibilityPolicy != core.TVCompatibilityPolicyPrefer {
		t.Fatalf("updated policy = %+v", updated)
	}

	rec = do(t, h, http.MethodPut, "/api/v1/quality-profiles/"+itoa(created.ID),
		`{"name":"must not save","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"min_seeders":-1}`)
	wantStatus(t, rec, http.StatusBadRequest)
	stored, err := st.GetQualityProfile(ctx, created.ID)
	if err != nil {
		t.Fatalf("get profile after invalid update: %v", err)
	}
	if stored.Name != "Policy updated" || stored.MinSeeders != 5 {
		t.Fatalf("invalid update wrote policy: %+v", stored)
	}

	before, err := st.ListQualityProfiles(ctx)
	if err != nil {
		t.Fatalf("list profiles: %v", err)
	}
	invalidBodies := []string{
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"proper_repack_preference":"always"}`,
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"preferred_sources":["webdl","webdl"]}`,
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"preferred_sources":["unknown"]}`,
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"min_seeders":-1}`,
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"min_size_mb":-1}`,
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"max_size_mb":-1}`,
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"min_size_mb":4,"max_size_mb":3}`,
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"custom_formats":[{"name":" ","include_terms":["hdr"],"score":1}]}`,
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"custom_formats":[{"name":"x","include_terms":["hdr"],"score":1},{"name":"X","include_terms":["dv"],"score":2}]}`,
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"custom_formats":[{"name":"x","include_terms":[],"score":1}]}`,
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"custom_formats":[{"name":"x","include_terms":[" "],"score":1}]}`,
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"custom_formats":[{"name":"x","include_terms":["hdr","HDR"],"score":1}]}`,
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"custom_formats":[{"name":"x","include_terms":["hdr"],"score":0}]}`,
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"tv_profile":"plasma"}`,
		`{"name":"bad","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"tv_compatibility_policy":"maybe"}`,
	}
	for _, invalid := range invalidBodies {
		rec = do(t, h, http.MethodPost, "/api/v1/quality-profiles", invalid)
		wantStatus(t, rec, http.StatusBadRequest)
	}
	after, err := st.ListQualityProfiles(ctx)
	if err != nil {
		t.Fatalf("list profiles after invalid requests: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("invalid requests wrote profiles: before %d, after %d", len(before), len(after))
	}
}

func TestQualityProfileCustomFormatScoreBounds(t *testing.T) {
	h, _, _ := newTestServer(t)
	profileJSON := func(name string, score int) string {
		return fmt.Sprintf(
			`{"name":%q,"cutoff":"1080p","items":["1080p"],"upgrade_allowed":true,"custom_formats":[{"name":"match","include_terms":["match"],"score":%d}]}`,
			name, score)
	}

	for _, score := range []int{wanted.MaxCustomFormatScore, -wanted.MaxCustomFormatScore} {
		rec := do(t, h, http.MethodPost, "/api/v1/quality-profiles", profileJSON(fmt.Sprintf("edge %d", score), score))
		wantStatus(t, rec, http.StatusCreated)
	}

	overflow := wanted.MaxCustomFormatScore + 1
	rec := do(t, h, http.MethodPost, "/api/v1/quality-profiles", profileJSON("too high", overflow))
	wantStatus(t, rec, http.StatusBadRequest)
	rec = do(t, h, http.MethodPost, "/api/v1/quality-profiles", profileJSON("too low", -overflow))
	wantStatus(t, rec, http.StatusBadRequest)

	rec = do(t, h, http.MethodPost, "/api/v1/quality-profiles", profileJSON("update target", 1))
	wantStatus(t, rec, http.StatusCreated)
	var created qualityProfileJSON
	decodeBody(t, rec, &created)
	rec = do(t, h, http.MethodPut, "/api/v1/quality-profiles/"+itoa(created.ID), profileJSON("update target", overflow))
	wantStatus(t, rec, http.StatusBadRequest)

	rec = do(t, h, http.MethodPost, "/api/v1/quality-profiles/import",
		`{"version":1,"default_profile":"overflow","profiles":[`+profileJSON("overflow", overflow)+`]}`)
	wantStatus(t, rec, http.StatusBadRequest)
}

func TestQualityProfileTestUsesWantedPolicyScoring(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	profile := &core.QualityProfile{
		Name:           "Test policy",
		Cutoff:         core.Quality1080p,
		Items:          []string{core.Quality1080p},
		UpgradeAllowed: true,
		CustomFormats: []core.CustomFormat{
			{Name: "HDR", IncludeTerms: []string{"hdr"}, Score: 25},
		},
	}
	if err := st.CreateQualityProfile(ctx, profile); err != nil {
		t.Fatalf("create profile: %v", err)
	}
	title := "Example.1080p.WEB-DL.HDR"
	rec := do(t, h, http.MethodPost, "/api/v1/quality-profiles/"+itoa(profile.ID)+"/test",
		`{"titles":["`+title+`"]}`)
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Results []profileTestResultJSON `json:"results"`
	}
	decodeBody(t, rec, &body)
	if len(body.Results) != 1 {
		t.Fatalf("results = %+v", body.Results)
	}
	wantScore, wantReject, wantContributions := wanted.ScoreReleaseWithContributions(
		core.Release{Title: title, Parsed: parse.Parse(title)}, profile)
	got := body.Results[0].Decision
	if got.Score != wantScore || (got.Accepted != (wantReject == "")) ||
		got.Contributions.CustomFormats != wantContributions.CustomFormats {
		t.Fatalf("profile test decision = %+v, wanted score %d reject %q contributions %+v",
			got, wantScore, wantReject, wantContributions)
	}
}

func TestQualityProfileTestIsAdminOnly(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	profiles, err := st.ListQualityProfiles(ctx)
	if err != nil {
		t.Fatalf("ListQualityProfiles: %v", err)
	}
	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	createUser(t, st, testMember, testPassword, core.RoleMember)

	member := login(t, h, testMember, testPassword)

	rec := doAuth(t, h, http.MethodPost,
		"/api/v1/quality-profiles/"+itoa(profiles[0].ID)+"/test",
		`{"titles":["Example.1080p"]}`, withCookie(member))
	wantStatus(t, rec, http.StatusForbidden)

	admin := login(t, h, testAdmin, testPassword)
	rec = doAuth(t, h, http.MethodPost,
		"/api/v1/quality-profiles/"+itoa(profiles[0].ID)+"/test",
		`{"titles":["Example.1080p"]}`, withCookie(admin))
	wantStatus(t, rec, http.StatusOK)
}
func TestQualityProfileExportImport(t *testing.T) {
	source, sourceStore, _ := newTestServer(t)
	ctx := context.Background()
	profile := &core.QualityProfile{
		Name:                   "Portable",
		Cutoff:                 core.Quality1080p,
		Items:                  []string{core.Quality1080p},
		UpgradeAllowed:         true,
		PreferredSources:       []string{core.SourceWebDL, core.SourceBluray},
		ProperRepackPreference: core.ProperRepackPreferenceNeutral,
		MinSeeders:             6,
		MinSizeMB:              700,
		MaxSizeMB:              8_000,
		CustomFormats: []core.CustomFormat{
			{Name: "HDR", IncludeTerms: []string{"hdr"}, Score: 25},
		},
		TVProfile:             core.TVProfileCapable,
		TVCompatibilityPolicy: core.TVCompatibilityPolicyPrefer,
	}
	if err := sourceStore.CreateQualityProfile(ctx, profile); err != nil {
		t.Fatalf("create source profile: %v", err)
	}
	if err := sourceStore.SetDefaultQualityProfile(ctx, profile.ID); err != nil {
		t.Fatalf("set source default: %v", err)
	}
	rec := do(t, source, http.MethodGet, "/api/v1/quality-profiles/export", "")
	wantStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="quality-profiles.json"` {
		t.Fatalf("Content-Disposition = %q", got)
	}
	var exported qualityProfileExportJSON
	decodeBody(t, rec, &exported)
	if exported.Version != 1 || exported.DefaultProfile != "Portable" || len(exported.Profiles) != 2 {
		t.Fatalf("export = %+v", exported)
	}

	target, targetStore, _ := newTestServer(t)
	existing := &core.QualityProfile{
		Name: "Portable", Cutoff: core.Quality720p, Items: []string{core.Quality720p}, UpgradeAllowed: true,
	}
	unlisted := &core.QualityProfile{
		Name: "Keep", Cutoff: core.Quality720p, Items: []string{core.Quality720p}, UpgradeAllowed: true,
	}
	for _, profile := range []*core.QualityProfile{existing, unlisted} {
		if err := targetStore.CreateQualityProfile(ctx, profile); err != nil {
			t.Fatalf("create target profile %q: %v", profile.Name, err)
		}
	}
	library, err := targetStore.GetLibraryByKind(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("get target library: %v", err)
	}
	library.QualityProfileID = existing.ID
	if err := targetStore.UpdateLibrary(ctx, library); err != nil {
		t.Fatalf("assign target profile: %v", err)
	}
	rec = do(t, target, http.MethodPost, "/api/v1/quality-profiles/import", rec.Body.String())
	wantStatus(t, rec, http.StatusOK)
	imported, err := targetStore.GetQualityProfile(ctx, existing.ID)
	if err != nil {
		t.Fatalf("get imported profile: %v", err)
	}
	if imported.MinSeeders != 6 || imported.TVCompatibilityPolicy != core.TVCompatibilityPolicyPrefer {
		t.Fatalf("imported profile = %+v", imported)
	}
	if library, err = targetStore.GetLibraryByKind(ctx, core.LibraryKindMovie); err != nil ||
		library.QualityProfileID != existing.ID {
		t.Fatalf("profile assignment = %+v, %v; want %d", library, err, existing.ID)
	}
	if _, err := targetStore.GetQualityProfile(ctx, unlisted.ID); err != nil {
		t.Fatalf("unlisted profile was removed: %v", err)
	}
	defaultProfile, err := targetStore.GetDefaultQualityProfile(ctx)
	if err != nil || defaultProfile.Name != "Portable" {
		t.Fatalf("default profile = %+v, %v; want Portable", defaultProfile, err)
	}
}

func TestQualityProfileImportRejectsInvalidDocumentWithoutWrites(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	before, err := st.ListQualityProfiles(ctx)
	if err != nil {
		t.Fatalf("list profiles: %v", err)
	}
	for _, body := range []string{
		`{"version":2,"default_profile":"Standard","profiles":[]}`,
		`{"version":1,"default_profile":"","profiles":[]}`,
		`{"version":1,"default_profile":"Missing","profiles":[]}`,
		`{"version":1,"default_profile":"HD","profiles":[{"name":"HD","cutoff":"1080p","items":["1080p"],"upgrade_allowed":true},{"name":"HD","cutoff":"720p","items":["720p"],"upgrade_allowed":true}]}`,
	} {
		rec := do(t, h, http.MethodPost, "/api/v1/quality-profiles/import", body)
		wantStatus(t, rec, http.StatusBadRequest)
	}
	after, err := st.ListQualityProfiles(ctx)
	if err != nil {
		t.Fatalf("list profiles after import errors: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("invalid import wrote profiles: before %d, after %d", len(before), len(after))
	}
}

func TestDeleteQualityProfileRejectsReferences(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	p := &core.QualityProfile{Name: "Referenced", Cutoff: "1080p", Items: []string{"1080p"}, UpgradeAllowed: true}
	if err := st.CreateQualityProfile(ctx, p); err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}
	movieLibrary, err := st.GetLibraryByKind(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("GetLibraryByKind(movie): %v", err)
	}
	movieLibrary.QualityProfileID = p.ID
	if err := st.UpdateLibrary(ctx, movieLibrary); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	if err := st.UpsertMovie(ctx, &core.Movie{TMDBID: 7001, Title: "Referenced Movie", Monitored: true, QualityProfileID: p.ID}); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	if err := st.UpsertSeries(ctx, &core.Series{TMDBID: 7002, Title: "Referenced Series", Monitored: true, QualityProfileID: p.ID}); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/quality-profiles", "")
	wantStatus(t, rec, http.StatusOK)
	var list struct {
		Profiles []qualityProfileJSON `json:"profiles"`
	}
	decodeBody(t, rec, &list)
	found := false
	for _, profile := range list.Profiles {
		if profile.ID != p.ID {
			continue
		}
		found = true
		if profile.Assignments != (profileAssignmentsJSON{Libraries: 1, Movies: 1, Series: 1}) {
			t.Fatalf("profile assignments = %+v, want one of each", profile.Assignments)
		}
	}
	if !found {
		t.Fatalf("referenced profile %d missing from profile list", p.ID)
	}

	rec = do(t, h, http.MethodDelete, "/api/v1/quality-profiles/"+itoa(p.ID), "")
	wantStatus(t, rec, http.StatusConflict)
	var response errorResponse
	decodeBody(t, rec, &response)
	want := "quality profile is still referenced by 1 library, 1 movie, and 1 series"
	if response.Error != want {
		t.Fatalf("delete error = %q, want %q", response.Error, want)
	}
	if _, err := st.GetQualityProfile(ctx, p.ID); err != nil {
		t.Fatalf("referenced profile was deleted: %v", err)
	}
}

func TestPatchItemAssignsQualityProfile(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	m := &core.Movie{TMDBID: 5, Title: "Profile Me", Monitored: true}
	if err := st.UpsertMovie(ctx, m); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	p := core.QualityProfile{Name: "UHD", Cutoff: "2160p", Items: []string{"2160p"}, UpgradeAllowed: true}
	if err := st.CreateQualityProfile(ctx, &p); err != nil {
		t.Fatalf("CreateQualityProfile: %v", err)
	}

	// Assignment by id.
	rec := do(t, h, http.MethodPatch, "/api/v1/library/movies/"+itoa(m.ID),
		`{"quality_profile_id": `+itoa(p.ID)+`}`)
	wantStatus(t, rec, http.StatusOK)
	got, err := st.GetMovie(ctx, m.ID)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if got.QualityProfileID != p.ID {
		t.Fatalf("profile = %d, want %d", got.QualityProfileID, p.ID)
	}
	// Monitored must be untouched by a profile-only patch.
	if !got.Monitored {
		t.Fatal("profile-only patch flipped monitored")
	}

	// 0 re-assigns the default.
	rec = do(t, h, http.MethodPatch, "/api/v1/library/movies/"+itoa(m.ID), `{"quality_profile_id": 0}`)
	wantStatus(t, rec, http.StatusOK)
	got, _ = st.GetMovie(ctx, m.ID)
	if got.QualityProfileID != 0 {
		t.Fatalf("profile = %d, want 0 (default)", got.QualityProfileID)
	}

	// An unknown profile is a client error, not a 404.
	rec = do(t, h, http.MethodPatch, "/api/v1/library/movies/"+itoa(m.ID), `{"quality_profile_id": 999}`)
	wantStatus(t, rec, http.StatusBadRequest)

	// An empty patch is still rejected.
	rec = do(t, h, http.MethodPatch, "/api/v1/library/movies/"+itoa(m.ID), `{}`)
	wantStatus(t, rec, http.StatusBadRequest)
}

func TestWantedList(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	missing := &core.Movie{TMDBID: 1, Title: "Missing Movie", Monitored: true}
	if err := st.UpsertMovie(ctx, missing); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	owned := &core.Movie{TMDBID: 2, Title: "Owned Movie", Monitored: true}
	if err := st.UpsertMovie(ctx, owned); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	// The default profile's cutoff is 1080p, so a 720p file keeps the movie
	// wanted as below-cutoff; a 1080p file settles it.
	if err := st.UpsertMediaFile(ctx, &core.MediaFile{
		Path: "Movies/Owned Movie (2020)/Owned Movie (2020).mkv", MovieID: owned.ID,
		Quality: core.Quality720p, Source: core.SourceWebDL,
	}); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	unmonitored := &core.Movie{TMDBID: 3, Title: "Skipped Movie", Monitored: false}
	if err := st.UpsertMovie(ctx, unmonitored); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	sr := &core.Series{TMDBID: 9, Title: "Andor", Monitored: true,
		PosterPath: "TV/Andor/poster.jpg", PosterURL: "https://img.example/andor.jpg"}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	aired := &core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: 1,
		Title: "Kassa", Monitored: true, AirDate: pastDate()}
	if err := st.UpsertEpisode(ctx, aired); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	unaired := &core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: 2,
		Title: "Future", Monitored: true, AirDate: futureDate()}
	if err := st.UpsertEpisode(ctx, unaired); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/wanted", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Movies   []wantedMovieJSON   `json:"movies"`
		Episodes []wantedEpisodeJSON `json:"episodes"`
	}
	decodeBody(t, rec, &body)

	if len(body.Movies) != 2 {
		t.Fatalf("wanted movies = %+v, want 2 (missing + below cutoff)", body.Movies)
	}
	reasons := map[string]string{}
	for _, m := range body.Movies {
		reasons[m.Title] = m.Reason
	}
	if reasons["Missing Movie"] != "missing" {
		t.Fatalf("Missing Movie reason = %q", reasons["Missing Movie"])
	}
	if reasons["Owned Movie"] != "below_cutoff" {
		t.Fatalf("Owned Movie reason = %q", reasons["Owned Movie"])
	}

	if len(body.Episodes) != 1 || body.Episodes[0].Title != "Kassa" {
		t.Fatalf("wanted episodes = %+v, want only the aired one", body.Episodes)
	}
	// The row carries the series' artwork: episodes have none of their own,
	// and the wanted list renders the series poster beside them.
	if got := body.Episodes[0]; got.PosterPath != sr.PosterPath || got.PosterURL != sr.PosterURL {
		t.Fatalf("episode poster = %q/%q, want the series' %q/%q",
			got.PosterPath, got.PosterURL, sr.PosterPath, sr.PosterURL)
	}
}
