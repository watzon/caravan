package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// seedScene puts one dated scene on the calendar: a series of kind adult with
// one episode whose air date is the scene's release date.
func seedScene(t *testing.T, st *store.Store, title string, when time.Time) {
	t.Helper()
	ctx := context.Background()
	series := &core.Series{
		StashID: "site-" + title, Title: title, SortTitle: strings.ToLower(title),
		Kind: core.SeriesKindAdult, Monitored: true,
	}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	episode := &core.Episode{
		SeriesID: series.ID, SeasonNumber: when.Year(), EpisodeNumber: 1,
		StashID: "scene-" + title, Title: "A Scene", AirDate: when, Monitored: true,
	}
	if err := st.UpsertEpisode(ctx, episode); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
}

func hasTitle(entries []calendarEntry, want string) bool {
	for _, e := range entries {
		if e.Title == want {
			return true
		}
	}
	return false
}

// The calendar is the shared surface the phase names explicitly, and the
// exposure rule is per-caller: an ungranted member — and everybody, once the
// module is off — must see nothing adult in it.
//
// It is driven through calendarEntries with a synthetic identity rather than
// over HTTP because members cannot reach GET /calendar at all today
// (memberAllowed, phase 8). That is a second, independent wall, and it must not
// be the only one: the moment the calendar is opened to members this filter is
// what stands between an ungranted housemate and a grid full of scene titles.
func TestCalendarHidesScenesFromEveryoneWithoutTheGrant(t *testing.T) {
	ctx := context.Background()
	_, st, _ := newTestServer(t)
	s := &server{st: st}
	today := calendarDate(time.Now())
	yesterday := today.AddDate(0, 0, -1)

	series := &core.Series{TMDBID: 100, Title: "Calendar Show", SortTitle: "calendar show", Monitored: true}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	storeCalendarEpisode(t, st, series.ID, 1, 1, "Television", yesterday, true)

	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	seedScene(t, st, "Brazzers", yesterday)

	entriesFor := func(u requestUser) []calendarEntry {
		t.Helper()
		r := withRequestUser(httptest.NewRequest(http.MethodGet, "/calendar", nil), u)
		got, err := s.calendarEntriesFor(r, today.AddDate(0, 0, -7), today, today)
		if err != nil {
			t.Fatalf("calendarEntriesFor: %v", err)
		}
		return got
	}

	admin := requestUser{ID: 1, Role: core.RoleAdmin}
	ungranted := requestUser{ID: 2, Role: core.RoleMember}
	granted := requestUser{ID: 3, Role: core.RoleMember, AdultAccess: true}

	// An admin is implicitly granted, so the scene is on their calendar.
	if got := entriesFor(admin); !hasTitle(got, "Brazzers") || !hasTitle(got, "Calendar Show") {
		t.Fatalf("admin calendar = %+v, want both the scene and the episode", got)
	}
	if got := entriesFor(granted); !hasTitle(got, "Brazzers") {
		t.Errorf("a granted member's calendar has no scene: %+v", got)
	}

	// An ungranted member gets the television row and nothing else.
	got := entriesFor(ungranted)
	if hasTitle(got, "Brazzers") {
		t.Errorf("an ungranted member's calendar carries a scene: %+v", got)
	}
	if !hasTitle(got, "Calendar Show") {
		t.Errorf("the member's calendar lost the television row: %+v", got)
	}

	// Switch the module off and it is gone for everybody, the admin included.
	if err := st.SetAdultEnabled(ctx, false); err != nil {
		t.Fatalf("SetAdultEnabled(false): %v", err)
	}
	for _, u := range []requestUser{admin, granted, ungranted} {
		if got := entriesFor(u); hasTitle(got, "Brazzers") {
			t.Errorf("%s's calendar carries a scene with the module off: %+v", u.Role, got)
		}
	}
}

// The HTTP surface, for the roles that can reach it: an admin's calendar loses
// its scenes the moment the module is switched off.
func TestCalendarEndpointDropsScenesWhenTheModuleIsOff(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newTestServer(t)
	today := calendarDate(time.Now())

	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	seedScene(t, st, "Brazzers", today.AddDate(0, 0, -1))

	titles := func() []string {
		t.Helper()
		rec := do(t, h, http.MethodGet, "/api/v1/calendar", "")
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Entries []struct {
				Title string `json:"title"`
			} `json:"entries"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode calendar: %v", err)
		}
		out := make([]string, 0, len(body.Entries))
		for _, e := range body.Entries {
			out = append(out, e.Title)
		}
		return out
	}

	got := titles()
	if len(got) != 1 || got[0] != "Brazzers" {
		t.Fatalf("calendar with the module on = %v, want the scene", got)
	}

	if err := st.SetAdultEnabled(ctx, false); err != nil {
		t.Fatalf("SetAdultEnabled(false): %v", err)
	}
	if got := titles(); len(got) != 0 {
		t.Errorf("calendar with the module off = %v, want nothing adult", got)
	}
}

// The iCal feed is a bearer URL, so it carries no scenes for anybody.
//
// It is authenticated by a query parameter that ends up in Google Calendar, a
// wall display, a housemate's phone, browser history and third-party databases
// — apiKeyAuthenticated's own comment says as much. There is no account behind
// such a request, so no grant can be consulted; inheriting currentUser's
// implicit admin would put every site name, scene title and release date on a
// shared calendar the moment the module was switched on.
func TestCalendarICSNeverCarriesAScene(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newTestServer(t)
	today := calendarDate(time.Now())

	// A closed server with a real roster, which is what makes the point: the
	// feed has an admin and an ungranted member behind it and answers to
	// neither.
	createUser(t, st, testAdmin, "correct-horse", core.RoleAdmin)
	createUser(t, st, testMember, "correct-horse", core.RoleMember)
	const apiKey = "feedkey"
	if err := st.SetSetting(ctx, store.SettingAPIKey, apiKey); err != nil {
		t.Fatalf("SetSetting(api key): %v", err)
	}
	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}

	series := &core.Series{TMDBID: 100, Title: "Calendar Show", SortTitle: "calendar show", Monitored: true}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	storeCalendarEpisode(t, st, series.ID, 1, 1, "Television", today.AddDate(0, 0, -1), true)
	seedScene(t, st, "Brazzers", today.AddDate(0, 0, -1))

	rec := do(t, h, http.MethodGet, "/api/v1/calendar.ics?apikey="+apiKey, "")
	wantStatus(t, rec, http.StatusOK)
	body := rec.Body.String()

	// The television row proves the feed is populated at all, so "no scene" is
	// not "no entries".
	if !strings.Contains(body, "Calendar Show") {
		t.Fatalf("the ics feed lost the television row: %s", body)
	}
	for _, leak := range []string{"Brazzers", "A Scene"} {
		if strings.Contains(body, leak) {
			t.Errorf("the ics feed carries %q: %s", leak, body)
		}
	}
}

// The Series screen is the television shelf, and the status card counts it. A
// site is a series row, so both would carry the adult library without a filter
// — for every role, and on an install with the module switched off.
func TestSeriesScreenAndStatusCountHoldNoSites(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newTestServer(t)

	series := &core.Series{TMDBID: 100, Title: "Calendar Show", SortTitle: "calendar show", Monitored: true}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	seedScene(t, st, "Brazzers", time.Now().UTC())

	rec := do(t, h, http.MethodGet, "/api/v1/library/series", "")
	wantStatus(t, rec, http.StatusOK)
	if body := rec.Body.String(); strings.Contains(body, "Brazzers") {
		t.Errorf("the Series screen lists a site: %s", body)
	}

	var listed struct {
		Series []json.RawMessage `json:"series"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode series: %v", err)
	}
	if len(listed.Series) != 1 {
		t.Fatalf("series = %d, want only the television one", len(listed.Series))
	}

	rec = do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)
	var status struct {
		Counts map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if got := status.Counts["series"]; got != 1 {
		t.Errorf("status series count = %d, want 1 (the site must not be counted)", got)
	}
}
