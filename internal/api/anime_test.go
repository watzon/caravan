package api

import (
	"net/http"
	"slices"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/library"
	"github.com/watzon/caravan/internal/store"
)

// activeAnimeLibrary is the seeded Anime library switched on, which is the one
// act that turns the shelf into something the content routes answer for.
func activeAnimeLibrary(t *testing.T, st *store.Store) core.Library {
	t.Helper()
	lib, err := st.GetLibraryByKind(t.Context(), core.LibraryKindAnime)
	if err != nil {
		t.Fatalf("GetLibraryByKind(anime): %v", err)
	}
	if err := st.SetLibraryActive(t.Context(), lib.ID, true); err != nil {
		t.Fatalf("SetLibraryActive: %v", err)
	}
	lib.Active = true
	return *lib
}

type seriesListBody struct {
	Series []seriesJSON `json:"series"`
}

func listSeries(t *testing.T, h http.Handler, query string) []seriesJSON {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/api/v1/library/series"+query, "")
	wantStatus(t, rec, http.StatusOK)
	var body seriesListBody
	decodeBody(t, rec, &body)
	return body.Series
}

// The two series screens are one endpoint asking for one kind at a time. An
// anime row belongs to /anime and to nothing else: a television list that also
// carried it would show every anime twice across the two screens, and the
// Series screen would claim rows whose shelf it is not.
func TestListSeriesFiltersByKind(t *testing.T) {
	h, st, _ := newTestServer(t)
	anime := activeAnimeLibrary(t, st)

	if err := st.UpsertSeries(t.Context(), &core.Series{
		Kind: core.SeriesKindTV, TMDBID: 1, Title: "Planet Earth II",
	}); err != nil {
		t.Fatalf("UpsertSeries(tv): %v", err)
	}
	if err := st.UpsertSeries(t.Context(), &core.Series{
		Kind: core.SeriesKindAnime, TMDBID: 2, Title: "Frieren", LibraryID: anime.ID,
	}); err != nil {
		t.Fatalf("UpsertSeries(anime): %v", err)
	}

	// No kind is television, which is what every client written before /anime
	// existed asks for.
	for query, want := range map[string]string{
		"":            "Planet Earth II",
		"?kind=tv":    "Planet Earth II",
		"?kind=anime": "Frieren",
	} {
		got := listSeries(t, h, query)
		if len(got) != 1 || got[0].Title != want {
			t.Errorf("GET /library/series%s = %+v, want only %q", query, got, want)
		}
	}
	// The anime row carries its kind on the wire, so the screen that asked for
	// it can tell what it got.
	if got := listSeries(t, h, "?kind=anime"); got[0].Kind != core.SeriesKindAnime {
		t.Errorf("anime row kind = %q, want %q", got[0].Kind, core.SeriesKindAnime)
	}
}

// `adult` is refused rather than gated. This route is not an adult surface, and
// a kind it silently answered with an empty list would be a second door worth
// knocking on; an unknown kind is a client mistake and reads like one.
func TestListSeriesRefusesKindsThatAreNotItsOwn(t *testing.T) {
	h, _, _ := newTestServer(t)

	for _, kind := range []string{"adult", "movie", "Anime", "wat"} {
		rec := do(t, h, http.MethodGet, "/api/v1/library/series?kind="+kind, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET /library/series?kind=%s = %d, want 400 (body %q)",
				kind, rec.Code, rec.Body.String())
		}
	}
}

// An anime library accepts BOTH an add of a movie and an add of a series: it is
// the one shelf that speaks two vocabularies, and the acceptance rule is what
// says so (core.LibraryKindAccepts). A television library keeps refusing films,
// so the widening is the anime kind's and not everybody's.
func TestAnimeLibraryAcceptsBothAddScopes(t *testing.T) {
	h, st, _ := newTestServer(t)
	anime := activeAnimeLibrary(t, st)
	tv, err := st.GetLibraryByKind(t.Context(), core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetLibraryByKind(tv): %v", err)
	}

	for _, path := range []string{"/api/v1/library/movies", "/api/v1/library/series"} {
		rec := do(t, h, http.MethodPost, path,
			`{"tmdb_id":1,"library_id":`+itoa(anime.ID)+`}`)
		wantStatus(t, rec, http.StatusCreated)
	}
	rec := do(t, h, http.MethodPost, "/api/v1/library/movies",
		`{"tmdb_id":1,"library_id":`+itoa(tv.ID)+`}`)
	wantStatus(t, rec, http.StatusBadRequest)
}

// The per-library item counts the navigation badges each shelf with. They are
// ownership rather than attribution: a row that names no library counts for
// nobody, and a library the caller cannot see contributes no entry at all —
// which is the one thing an item count must not leak.
func TestSystemStatusCountsItemsPerVisibleLibrary(t *testing.T) {
	h, st, _ := newTestServer(t)
	anime := activeAnimeLibrary(t, st)
	movies, err := st.GetLibraryByKind(t.Context(), core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("GetLibraryByKind(movie): %v", err)
	}

	if err := st.UpsertMovie(t.Context(), &core.Movie{
		TMDBID: 1, Title: "A Film", LibraryID: movies.ID,
	}); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	if err := st.UpsertSeries(t.Context(), &core.Series{
		Kind: core.SeriesKindAnime, TMDBID: 2, Title: "Frieren", LibraryID: anime.ID,
	}); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)
	var status statusResponse
	decodeBody(t, rec, &status)

	got := map[int64]int64{}
	for _, entry := range status.Counts.Libraries {
		got[entry.ID] = entry.Items
	}
	if got[movies.ID] != 1 || got[anime.ID] != 1 {
		t.Errorf("per-library counts = %+v, want one item each on %d and %d",
			status.Counts.Libraries, movies.ID, anime.ID)
	}
	// The Adult library is still dormant, so it is not a shelf this caller has.
	adult, err := st.GetLibraryByKind(t.Context(), core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind(adult): %v", err)
	}
	if _, ok := got[adult.ID]; ok {
		t.Errorf("per-library counts = %+v, want no entry for the dormant adult shelf",
			status.Counts.Libraries)
	}
}

// The icon is validated for SHAPE and nothing else, so a client can ship a new
// glyph without a server release — and empty is how a library goes back to its
// kind's default.
func TestPatchLibraryIcon(t *testing.T) {
	h, st, _ := newTestServer(t)
	movies, err := st.GetLibraryByKind(t.Context(), core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("GetLibraryByKind(movie): %v", err)
	}
	path := "/api/v1/libraries/" + itoa(movies.ID)

	rec := do(t, h, http.MethodPatch, path, `{"icon":"sparkles"}`)
	wantStatus(t, rec, http.StatusOK)
	var updated libraryJSON
	decodeBody(t, rec, &updated)
	if updated.Icon != "sparkles" {
		t.Errorf("icon after PATCH = %q, want %q", updated.Icon, "sparkles")
	}

	// A name the server has never heard of is stored anyway: the glyph list
	// lives in the client, and a server-side allow-list would go stale.
	rec = do(t, h, http.MethodPatch, path, `{"icon":"someGlyphTheServerHasNeverSeen"}`)
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &updated)
	if updated.Icon != "someGlyphTheServerHasNeverSeen" {
		t.Errorf("icon after PATCH = %q, want the unknown name stored verbatim", updated.Icon)
	}

	// Empty is a value, not an omission: it resets to the kind default.
	rec = do(t, h, http.MethodPatch, path, `{"icon":""}`)
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &updated)
	if updated.Icon != "" {
		t.Errorf("icon after reset = %q, want empty", updated.Icon)
	}

	// What the shape rule buys: the value stays a bare identifier, with no
	// markup, path or separator a future consumer could read as structure.
	for name, body := range map[string]string{
		"markup":    `{"icon":"<script>"}`,
		"path":      `{"icon":"icons/film"}`,
		"digits":    `{"icon":"film2"}`,
		"too long":  `{"icon":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}`,
		"separator": `{"icon":"film-outline"}`,
	} {
		rec := do(t, h, http.MethodPatch, path, body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: PATCH icon = %d, want 400 (body %q)", name, rec.Code, rec.Body.String())
		}
	}
}

// The sidebar's own source of data carries the icon, because /auth/me IS what
// the navigation is built from — a member cannot read GET /libraries to find
// one.
func TestMeReportsLibraryIcons(t *testing.T) {
	h, st, _ := newTestServer(t)
	movies, err := st.GetLibraryByKind(t.Context(), core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("GetLibraryByKind(movie): %v", err)
	}
	movies.Icon = "sparkles"
	if err := st.UpdateLibrary(t.Context(), movies); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/auth/me", "")
	wantStatus(t, rec, http.StatusOK)
	var me meResponse
	decodeBody(t, rec, &me)

	// Two entries: the seeded Anime and Adult shelves are dormant, so they are
	// absent for everyone, admins included.
	if len(me.Libraries) != 2 {
		t.Fatalf("me.libraries = %+v, want the two active seeded shelves", me.Libraries)
	}
	for _, lib := range me.Libraries {
		if lib.ID == movies.ID && lib.Icon != "sparkles" {
			t.Errorf("movie shelf icon = %q, want %q", lib.Icon, "sparkles")
		}
		if lib.ID != movies.ID && lib.Icon != "" {
			t.Errorf("shelf %d icon = %q, want empty", lib.ID, lib.Icon)
		}
	}
}

// A search scoped to an anime library runs BOTH halves, because the shelf holds
// both. The gate used to be kind equality, which matched neither `movie` nor
// `tv` for an anime library and answered every scope — movie, series and all —
// with two empty lists while the chain underneath was perfectly able to answer
// (library.SearchLibrary resolves through the same acceptance rule).
func TestSearchThroughAnAnimeLibraryRunsBothHalves(t *testing.T) {
	h, st, mgr := newTestServer(t)
	anime := activeAnimeLibrary(t, st)
	id := itoa(anime.ID)
	// The chain answers; what is under test is which halves reach it.
	mgr.searchHits = &library.SearchHits{Providers: []string{core.ProviderAniList}}

	for name, tc := range map[string]struct {
		query string
		want  []string
	}{
		"movie scope":  {"&type=movie", []string{MediaTypeMovie}},
		"series scope": {"&type=series", []string{MediaTypeSeries}},
		// type=all on an anime library means both halves of THIS shelf.
		"both scopes": {"", []string{MediaTypeMovie, MediaTypeSeries}},
	} {
		t.Run(name, func(t *testing.T) {
			mgr.searches = nil
			rec := do(t, h, http.MethodGet, "/api/v1/search?q=frieren&library_id="+id+tc.query, "")
			wantStatus(t, rec, http.StatusOK)

			var got []string
			for _, call := range mgr.searchCalls() {
				if call.libraryID != anime.ID {
					t.Errorf("search ran against library %d, want the anime library %d",
						call.libraryID, anime.ID)
				}
				got = append(got, call.mediaType)
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("halves that ran = %v, want %v", got, tc.want)
			}
		})
	}
}

// The widening is the anime kind's and nobody else's: a television library
// still refuses a movie query rather than asking its chain about films that
// cannot go on that shelf.
func TestSearchThroughATVLibraryStillRunsOneHalf(t *testing.T) {
	h, st, mgr := newTestServer(t)
	tv, err := st.GetLibraryByKind(t.Context(), core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetLibraryByKind(tv): %v", err)
	}
	mgr.searchHits = &library.SearchHits{Providers: []string{core.ProviderTMDB}}

	rec := do(t, h, http.MethodGet, "/api/v1/search?q=frieren&library_id="+itoa(tv.ID), "")
	wantStatus(t, rec, http.StatusOK)

	calls := mgr.searchCalls()
	if len(calls) != 1 || calls[0].mediaType != MediaTypeSeries {
		t.Errorf("halves that ran = %+v, want the series half alone", calls)
	}
}

// A dormant shelf accepts nothing, through ANY door that names a target:
// searching it, adding to it, or moving into it. It is the promise
// `active = 0` makes — dormant for EVERYONE, an admin included — and it is what
// a client naming a library id has to be able to rely on: the seeded Anime and
// Adult rows are visible on the admin Libraries screen, so a surface that built
// a target list from THAT list could name a shelf no content route will answer
// for. The refusal is 404 rather than 403, for the reason visibleLibrary gives.
func TestDormantLibraryRefusesEveryTargetDoor(t *testing.T) {
	h, st, mgr := newTestServer(t)
	// The seeded Anime library, left dormant.
	anime, err := st.GetLibraryByKind(t.Context(), core.LibraryKindAnime)
	if err != nil {
		t.Fatalf("GetLibraryByKind(anime): %v", err)
	}
	if anime.Active {
		t.Fatalf("the seeded anime library is active, want this test to start dormant")
	}
	id := itoa(anime.ID)
	mgr.searchHits = &library.SearchHits{Providers: []string{core.ProviderAniList}}

	// A movie and a series on the ACTIVE shelves, so the move cases below fail
	// on the destination rather than on the item.
	if err := st.UpsertMovie(t.Context(), &core.Movie{TMDBID: 9, Title: "A Film"}); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	if err := st.UpsertSeries(t.Context(), &core.Series{TMDBID: 9, Title: "A Show"}); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}

	for name, target := range map[string]struct{ method, path, body string }{
		"search":      {http.MethodGet, "/api/v1/search?q=frieren&library_id=" + id, ""},
		"add movie":   {http.MethodPost, "/api/v1/library/movies", `{"tmdb_id":1,"library_id":` + id + `}`},
		"add series":  {http.MethodPost, "/api/v1/library/series", `{"tmdb_id":1,"library_id":` + id + `}`},
		"move movie":  {http.MethodPost, "/api/v1/library/movies/1/move", `{"library_id":` + id + `}`},
		"move series": {http.MethodPost, "/api/v1/library/series/1/move", `{"library_id":` + id + `}`},
	} {
		t.Run(name, func(t *testing.T) {
			rec := do(t, h, target.method, target.path, target.body)
			wantStatus(t, rec, http.StatusNotFound)
		})
	}
	// Nothing reached the manager: a dormant shelf is refused before any
	// provider is asked and before any row is written.
	if calls := mgr.searchCalls(); len(calls) != 0 {
		t.Errorf("a dormant shelf still ran a search: %+v", calls)
	}
	if calls := mgr.addCalls(); len(calls) != 0 {
		t.Errorf("a dormant shelf still took an add: %+v", calls)
	}
}

// The download-client label follows the SHELF the payload lands on, so an anime
// library's films and its episodes sort into ONE folder — the one its owner
// gave the shelf — rather than being split back across "movies" and "tv".
//
// Four doors reach the engine and all four are checked here, because a label
// that depended on which button was pressed would scatter one shelf's downloads
// across three folders: the per-item movie picker, the per-item series picker,
// the universal search's untied grab, and its tied grab.
func TestAnimeGrabsCarryTheAnimeCategory(t *testing.T) {
	ctx := t.Context()
	h, st, engine, _ := newAcquisitionServer(t)
	anime := activeAnimeLibrary(t, st)

	film := core.Movie{TMDBID: 12477, Title: "Grave of the Fireflies", SortTitle: "grave",
		Year: 1988, Monitored: true, LibraryID: anime.ID}
	if err := st.UpsertMovie(ctx, &film); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	sr := core.Series{Kind: core.SeriesKindAnime, TMDBID: 209867, Title: "Frieren",
		SortTitle: "frieren", Monitored: true, LibraryID: anime.ID}
	if err := st.UpsertSeries(ctx, &sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	episode := core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: 1, Monitored: true}
	if err := st.UpsertEpisode(ctx, &episode); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}

	lastCategory := func(t *testing.T) string {
		t.Helper()
		adds := engine.addCalls()
		if len(adds) == 0 {
			t.Fatal("nothing reached the engine")
		}
		return adds[len(adds)-1].opts.Category
	}

	cases := []struct {
		name, path, body string
	}{
		{
			// A film has no kind column of its own, so this is the case that
			// only its library can answer.
			name: "movie picker",
			path: "/api/v1/library/movies/" + itoa(film.ID) + "/grab",
		},
		{
			name: "series picker",
			path: "/api/v1/library/series/" + itoa(sr.ID) + "/grab",
		},
		{
			name: "untied universal search grab",
			path: "/api/v1/search/grab",
			body: `,"library_id":` + itoa(anime.ID),
		},
		{
			name: "tied universal search grab",
			path: "/api/v1/search/grab",
			body: `,"library_id":` + itoa(anime.ID) +
				`,"tie":{"media_type":"movie","media_id":` + itoa(film.ID) + `}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rel := cacheRelease(t, st, "Frieren."+c.name)
			rec := do(t, h, http.MethodPost, c.path,
				`{"release_id":`+itoa(rel.ID)+c.body+`}`)
			if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
				t.Fatalf("grab = %d, body %q", rec.Code, rec.Body.String())
			}
			if got := lastCategory(t); got != engineCategoryAnime {
				t.Errorf("category = %q, want %q: the label names the shelf the payload lands on",
					got, engineCategoryAnime)
			}
		})
	}

	// And the ordinary shelves are untouched: the anime label is a new one, not
	// a rename of the two every download client already sorts. addMovie files
	// its row on the movie shelf, so this is the derivation answering rather
	// than a fallback.
	ordinary := addMovie(t, st, "Big Buck Bunny", 2008)
	rel := cacheRelease(t, st, "BBB.2008.1080p")
	rec := do(t, h, http.MethodPost, "/api/v1/library/movies/"+itoa(ordinary.ID)+"/grab",
		`{"release_id":`+itoa(rel.ID)+`}`)
	wantStatus(t, rec, http.StatusCreated)
	if got := lastCategory(t); got != engineCategoryMovies {
		t.Errorf("ordinary movie category = %q, want %q", got, engineCategoryMovies)
	}
}
