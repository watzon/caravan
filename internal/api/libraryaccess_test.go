package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// The per-library access rule generalizes the adult module's promise of
// absence, so it has to hold on an ORDINARY shelf: a television library
// narrowed to one housemate must be as absent to the other as the adult
// library ever was, on every surface that can carry one of its rows.
//
// These are driven through the handlers with a synthetic identity rather than
// over HTTP because a member cannot reach /library, /calendar, /events or
// /import at all today (memberAllowed). That is a second, independent wall, and
// it must not be the only one: the moment any of those opens to members, this
// filter is what stands between an ungranted housemate and somebody else's
// shelf.

// seedRef is a per-title provider id, so two fixtures never collide on one row.
func seedRef(title string) int64 {
	var sum int64
	for _, b := range []byte(title) {
		sum = sum*31 + int64(b)
	}
	return sum
}

// seriesIn and movieIn seed one item into a library and return its id.
func seriesIn(t *testing.T, st *store.Store, lib core.Library, title string) int64 {
	t.Helper()
	sr := &core.Series{
		TMDBID: seedRef(title), Title: title + " Show",
		SortTitle: strings.ToLower(title) + " show", Kind: core.SeriesKindTV, Monitored: true,
		LibraryID: lib.ID, Path: lib.RootPath + "/" + title + " Show",
	}
	if err := st.UpsertSeries(context.Background(), sr); err != nil {
		t.Fatalf("UpsertSeries(%s): %v", title, err)
	}
	return sr.ID
}

func movieIn(t *testing.T, st *store.Store, lib core.Library, title string) int64 {
	t.Helper()
	m := &core.Movie{
		TMDBID: seedRef(title), Title: title + " Film",
		SortTitle: strings.ToLower(title) + " film", Monitored: true,
		LibraryID: lib.ID, Path: lib.RootPath + "/" + title + " Film",
	}
	if err := st.UpsertMovie(context.Background(), m); err != nil {
		t.Fatalf("UpsertMovie(%s): %v", title, err)
	}
	return m.ID
}

// titlesFrom pulls the titles out of a list response under one key.
func titlesFrom(t *testing.T, rec *httptest.ResponseRecorder, key string) map[string]bool {
	t.Helper()
	var body map[string][]struct {
		Title string `json:"title"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %s: %v (%s)", key, err, rec.Body.String())
	}
	out := map[string]bool{}
	for _, row := range body[key] {
		out[row.Title] = true
	}
	return out
}

func TestRestrictedLibraryIsAbsentToAnUngrantedMember(t *testing.T) {
	ctx := context.Background()
	_, st, mgr := newTestServer(t)
	s := &server{st: st, mgr: mgr, log: slog.Default()}
	f := restrictedLibraryFixture(t, st)

	kidsSeries := seriesIn(t, st, f.kids, "Kids")
	kidsMovie := movieIn(t, st, f.kidsFilms, "Kids")
	openSeries := seriesIn(t, st, f.openTV, "Open")
	movieIn(t, st, f.openMovie, "Family")

	today := calendarDate(time.Now())
	storeCalendarEpisode(t, st, kidsSeries, 1, 1, "Kids Episode", today.AddDate(0, 0, -1), true)
	storeCalendarEpisode(t, st, openSeries, 1, 1, "Open Episode", today.AddDate(0, 0, -1), true)

	if err := st.InsertEvent(ctx, &core.Event{
		Category: "grab", Message: "Kids Event", SeriesID: kidsSeries,
	}); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	if err := st.InsertEvent(ctx, &core.Event{
		Category: "grab", Message: "Open Event", SeriesID: openSeries,
	}); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	if err := st.UpsertUnmatchedFile(ctx, &core.UnmatchedFile{
		Path: f.kids.RootPath + "/mystery.mkv", Size: 1, Reason: "unmatched", LibraryID: f.kids.ID,
	}); err != nil {
		t.Fatalf("UpsertUnmatchedFile: %v", err)
	}

	for _, who := range []struct {
		name string
		user requestUser
		sees bool
	}{
		{"admin", f.adminUser(), true},
		{"granted member", f.grantedUser(), true},
		{"ungranted member", f.ungrantedUser(), false},
	} {
		t.Run(who.name, func(t *testing.T) {
			call := func(h http.HandlerFunc, target string) *httptest.ResponseRecorder {
				t.Helper()
				rec := httptest.NewRecorder()
				h(rec, f.as(s, who.user, http.MethodGet, target))
				return rec
			}

			rec := call(s.handleListSeries, "/library/series")
			wantStatus(t, rec, http.StatusOK)
			if got := titlesFrom(t, rec, "series")["Kids Show"]; got != who.sees {
				t.Errorf("series list holds the restricted series = %t, want %t", got, who.sees)
			}
			if !titlesFrom(t, rec, "series")["Open Show"] {
				t.Errorf("series list lost the unrestricted series: %s", rec.Body.String())
			}

			rec = call(s.handleListMovies, "/library/movies")
			wantStatus(t, rec, http.StatusOK)
			if got := titlesFrom(t, rec, "movies")["Kids Film"]; got != who.sees {
				t.Errorf("movie list holds the restricted movie = %t, want %t", got, who.sees)
			}
			if !titlesFrom(t, rec, "movies")["Family Film"] {
				t.Errorf("movie list lost the unrestricted movie: %s", rec.Body.String())
			}

			rec = httptest.NewRecorder()
			r := f.as(s, who.user, http.MethodGet, "/library/series/"+itoa(kidsSeries))
			r.SetPathValue("id", itoa(kidsSeries))
			s.handleGetSeries(rec, r)
			wantSeries := http.StatusNotFound
			if who.sees {
				wantSeries = http.StatusOK
			}
			if rec.Code != wantSeries {
				t.Errorf("GET the restricted series by id = %d, want %d", rec.Code, wantSeries)
			}

			entries, err := s.calendarEntriesFor(
				f.as(s, who.user, http.MethodGet, "/calendar"),
				today.AddDate(0, 0, -7), today, today)
			if err != nil {
				t.Fatalf("calendarEntriesFor: %v", err)
			}
			if got := hasTitle(entries, "Kids Show"); got != who.sees {
				t.Errorf("calendar holds the restricted series = %t, want %t (%+v)", got, who.sees, entries)
			}
			if !hasTitle(entries, "Open Show") {
				t.Errorf("calendar lost the unrestricted series: %+v", entries)
			}

			rec = call(s.handleEvents, "/events")
			wantStatus(t, rec, http.StatusOK)
			var events struct {
				Events []struct {
					Message string `json:"message"`
				} `json:"events"`
			}
			decodeBody(t, rec, &events)
			seenKids, seenOpen := false, false
			for _, e := range events.Events {
				seenKids = seenKids || e.Message == "Kids Event"
				seenOpen = seenOpen || e.Message == "Open Event"
			}
			if seenKids != who.sees {
				t.Errorf("history holds the restricted event = %t, want %t", seenKids, who.sees)
			}
			if !seenOpen {
				t.Errorf("history lost the unrestricted event: %s", rec.Body.String())
			}

			rec = call(s.handleImportQueue, "/import/queue")
			wantStatus(t, rec, http.StatusOK)
			if got := strings.Contains(rec.Body.String(), "mystery.mkv"); got != who.sees {
				t.Errorf("review queue holds the restricted file = %t, want %t", got, who.sees)
			}
		})
	}

	// The movie half of the ownership filter is new, so it gets its own
	// assertion rather than riding on the series one: before this phase a movie
	// id was waved through, because a movie could not be adult.
	gate := s.gateFor(f.ungrantedUser())
	filter := libraryOwnershipFilter{server: s, gate: gate}
	visible, err := filter.ownerVisible(ctx, kidsMovie, 0)
	if err != nil {
		t.Fatalf("ownerVisible: %v", err)
	}
	if visible {
		t.Error("a movie in a restricted library is visible to an ungranted member")
	}
}

// An inactive library binds EVERYONE. The admin is the case worth pinning:
// deactivating is how an owner hides a shelf from themselves, and a switch the
// person holding it cannot feel is not a switch.
func TestInactiveLibraryIsAbsentToTheAdminToo(t *testing.T) {
	h, st, mgr := newTestServer(t)
	s := &server{st: st, mgr: mgr, log: slog.Default()}
	f := restrictedLibraryFixture(t, st)

	dormantSeries := seriesIn(t, st, f.dormantTV, "Dormant")
	movieIn(t, st, f.dormantMovie, "Shelved")

	today := calendarDate(time.Now())
	storeCalendarEpisode(t, st, dormantSeries, 1, 1, "Dormant Episode", today.AddDate(0, 0, -1), true)

	admin := f.adminUser()
	rec := httptest.NewRecorder()
	s.handleListSeries(rec, f.as(s, admin, http.MethodGet, "/library/series"))
	wantStatus(t, rec, http.StatusOK)
	if titlesFrom(t, rec, "series")["Dormant Show"] {
		t.Errorf("the admin's series list holds an inactive library's series: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	s.handleListMovies(rec, f.as(s, admin, http.MethodGet, "/library/movies"))
	wantStatus(t, rec, http.StatusOK)
	if titlesFrom(t, rec, "movies")["Shelved Film"] {
		t.Errorf("the admin's movie list holds an inactive library's movie: %s", rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r := f.as(s, admin, http.MethodGet, "/library/series/"+itoa(dormantSeries))
	r.SetPathValue("id", itoa(dormantSeries))
	s.handleGetSeries(rec, r)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET an inactive library's series by id as an admin = %d, want 404", rec.Code)
	}

	entries, err := s.calendarEntriesFor(
		f.as(s, admin, http.MethodGet, "/calendar"), today.AddDate(0, 0, -7), today, today)
	if err != nil {
		t.Fatalf("calendarEntriesFor: %v", err)
	}
	if hasTitle(entries, "Dormant Show") {
		t.Errorf("the admin's calendar holds an inactive library's series: %+v", entries)
	}

	// The library itself is gone from every by-id content route, over HTTP, as
	// a real signed-in admin.
	adminCookie := withCookie(login(t, h, testAdmin, testPassword))
	for _, tc := range []struct{ method, target, body string }{
		{http.MethodGet, "/api/v1/search?q=x&library_id=" + itoa(f.dormantTV.ID), ""},
		{http.MethodPost, "/api/v1/search/grab",
			`{"release_id":1,"library_id":` + itoa(f.dormantTV.ID) + `}`},
		{http.MethodGet, "/api/v1/search/releases?q=x&library_id=" + itoa(f.dormantTV.ID), ""},
	} {
		rec := doAuth(t, h, tc.method, tc.target, tc.body, adminCookie)
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s against an inactive library = %d, want 404 (body %q)",
				tc.method, tc.target, rec.Code, rec.Body.String())
		}
	}

	// And it is not on the Libraries screen either, so nothing describes a shelf
	// that answers nothing.
	rec = doAuth(t, h, http.MethodGet, "/api/v1/libraries", "", adminCookie)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "Dormant Shows") {
		t.Errorf("GET /libraries names an inactive library: %s", rec.Body.String())
	}
}

// GET /images is auth-exempt for televisions, so it is the one surface an
// ungranted housemate — or anyone on the LAN — can reach with no credential at
// all. Every library root now answers there, not just the adult one.
func TestImagesFollowLibraryAccess(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newTestServer(t)
	f := restrictedLibraryFixture(t, st)

	root := t.TempDir()
	if err := st.SetSetting(ctx, store.SettingStorageRoot, root); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	writeFile(t, root, f.openTV.RootPath+"/Open Show/poster.jpg", "openposter")
	writeFile(t, root, f.kids.RootPath+"/Kids Show/poster.jpg", "kidsposter")
	writeFile(t, root, f.dormantTV.RootPath+"/Dormant Show/poster.jpg", "dormantposter")

	openPath := "/api/v1/images/" + f.openTV.RootPath + "/Open%20Show/poster.jpg"
	kidsPath := "/api/v1/images/" + f.kids.RootPath + "/Kids%20Show/poster.jpg"
	dormantPath := "/api/v1/images/" + f.dormantTV.RootPath + "/Dormant%20Show/poster.jpg"

	grantedCookie := withCookie(login(t, h, "granted", testPassword))
	ungrantedCookie := withCookie(login(t, h, "ungranted", testPassword))
	adminCookie := withCookie(login(t, h, testAdmin, testPassword))

	get := func(target string, decorate func(*http.Request)) int {
		t.Helper()
		return doAuth(t, h, http.MethodGet, target, "", decorate).Code
	}

	// The hole authExempt deliberately leaves open stays open for an ordinary
	// shelf: a television cannot log in.
	if got := get(openPath, nil); got != http.StatusOK {
		t.Fatalf("anonymous poster from an open library = %d, want 200", got)
	}

	for _, tc := range []struct {
		name     string
		decorate func(*http.Request)
		want     int
	}{
		{"anonymous", nil, http.StatusNotFound},
		{"ungranted member", ungrantedCookie, http.StatusNotFound},
		{"granted member", grantedCookie, http.StatusOK},
		{"admin", adminCookie, http.StatusOK},
	} {
		if got := get(kidsPath, tc.decorate); got != tc.want {
			t.Errorf("%s restricted poster = %d, want %d", tc.name, got, tc.want)
		}
	}

	// Sharing the shelf on DLNA is the owner deciding every device on the
	// network may browse it, so the television's album art works again. It is a
	// second, deliberate act: SetLibraryAccess cleared the flag when the library
	// was restricted.
	kids, err := st.GetLibrary(ctx, f.kids.ID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	if kids.DLNAVisible {
		t.Fatal("restricting the library left it advertised on DLNA")
	}
	kids.DLNAVisible = true
	if err := st.UpdateLibrary(ctx, kids); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	if got := get(kidsPath, nil); got != http.StatusOK {
		t.Errorf("anonymous restricted poster with the shelf shared on DLNA = %d, want 200", got)
	}

	// An inactive library is closed to everyone, and dlna_visible does not
	// reopen it: the shelf is off, not merely locked.
	dormant, err := st.GetLibrary(ctx, f.dormantTV.ID)
	if err != nil {
		t.Fatalf("GetLibrary: %v", err)
	}
	dormant.DLNAVisible = true
	if err := st.UpdateLibrary(ctx, dormant); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	for _, tc := range []struct {
		name     string
		decorate func(*http.Request)
	}{
		{"anonymous", nil},
		{"ungranted member", ungrantedCookie},
		{"granted member", grantedCookie},
		{"admin", adminCookie},
	} {
		if got := get(dormantPath, tc.decorate); got != http.StatusNotFound {
			t.Errorf("%s inactive poster = %d, want 404", tc.name, got)
		}
	}
}

// The gate is what bounds the cost of per-library filtering, so its two queries
// have to be exactly that: two, however many surfaces one request asks.
func TestLibraryGateReadsTheStoreOncePerRequest(t *testing.T) {
	ctx := context.Background()
	_, st, _ := newTestServer(t)
	s := &server{st: st, log: slog.Default()}
	f := restrictedLibraryFixture(t, st)

	gate := s.gateFor(f.ungrantedUser())
	for range 3 {
		if _, err := gate.visible(ctx, f.kids.ID); err != nil {
			t.Fatalf("visible: %v", err)
		}
		if _, err := gate.seesAdult(ctx); err != nil {
			t.Fatalf("seesAdult: %v", err)
		}
		if _, err := gate.visibleKind(ctx, 0, core.LibraryKindTV); err != nil {
			t.Fatalf("visibleKind: %v", err)
		}
	}
	if !gate.loaded {
		t.Fatal("the gate never loaded")
	}

	// A grant taken away is taken away: the gate lives for one request, so the
	// next one reads the new roster rather than the map this one built.
	if err := st.SetLibraryAccess(ctx, f.kids.ID, true, nil); err != nil {
		t.Fatalf("SetLibraryAccess: %v", err)
	}
	fresh := s.gateFor(f.grantedUser())
	visible, err := fresh.visible(ctx, f.kids.ID)
	if err != nil {
		t.Fatalf("visible: %v", err)
	}
	if visible {
		t.Error("a revoked grant still opens the library on a new request")
	}
}
