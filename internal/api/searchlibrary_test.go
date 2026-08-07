package api

import (
	"context"
	"net/http"
	"slices"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/library"
	"github.com/watzon/caravan/internal/store"
)

// createLibrary makes a library through the create endpoint, which is the one
// path that validates the chain, and returns the row.
func createLibrary(t *testing.T, h http.Handler, body string) libraryJSON {
	t.Helper()
	rec := do(t, h, http.MethodPost, "/api/v1/libraries", body)
	wantStatus(t, rec, http.StatusCreated)
	var created libraryJSON
	decodeBody(t, rec, &created)
	return created
}

// A search scoped to a library goes through THAT library's chain, and says so.
//
// The envelope has to carry both halves of the answer: which providers ran, so
// the client knows whether a per-row badge distinguishes anything, and which
// library was searched, so the add that follows lands on the shelf the user was
// looking at rather than the kind's default.
func TestSearchThroughANamedLibrary(t *testing.T) {
	h, _, mgr := newTestServer(t)
	anime := createLibrary(t, h,
		`{"kind":"tv","name":"Anime","root_path":"library/Anime","providers":["tmdb","anilist"]}`)

	mgr.searchHits = &library.SearchHits{
		Series: []core.SeriesMeta{
			{TMDBID: 209867, Provider: core.ProviderTMDB, ProviderRef: "209867", Title: "Frieren"},
			{Provider: core.ProviderAniList, ProviderRef: "154587", Title: "Sousou no Frieren"},
		},
		Providers: []string{core.ProviderTMDB, core.ProviderAniList},
	}

	rec := do(t, h, http.MethodGet, "/api/v1/search?q=frieren&library_id="+itoa(anime.ID), "")
	wantStatus(t, rec, http.StatusOK)
	var body searchResponse
	decodeBody(t, rec, &body)

	if body.LibraryID != anime.ID {
		t.Errorf("library_id = %d, want %d", body.LibraryID, anime.ID)
	}
	if !slices.Equal(body.Providers, []string{core.ProviderTMDB, core.ProviderAniList}) {
		t.Errorf("providers = %v, want the chain that ran", body.Providers)
	}
	if len(body.Series) != 2 {
		t.Fatalf("series = %+v, want both providers' hits", body.Series)
	}
	// The AniList hit has no TMDB id and does not pretend to: the pair beside
	// it is the only thing that identifies it.
	if got := body.Series[1]; got.Provider != core.ProviderAniList || got.ProviderRef != "154587" || got.TMDBID != 0 {
		t.Errorf("anilist hit = %+v, want the anilist ref and no tmdb id", got)
	}
	if got := body.Series[0]; got.Provider != core.ProviderTMDB || got.ProviderRef != "209867" {
		t.Errorf("tmdb hit = %+v, want the tmdb ref", got)
	}

	// A television library speaks no movies, so the movie half of type=all is
	// not run at all rather than run and discarded.
	if len(body.Movies) != 0 {
		t.Errorf("movies = %+v, want none from a tv library", body.Movies)
	}
	calls := mgr.searchCalls()
	if len(calls) != 1 {
		t.Fatalf("search calls = %+v, want only the series half", calls)
	}
	if calls[0].libraryID != anime.ID || calls[0].mediaType != MediaTypeSeries || calls[0].q != "frieren" {
		t.Errorf("search call = %+v, want the named library's series half", calls[0])
	}
}

// With no library named, both halves run against their own kind's default, and
// the chain both defaults share is reported once. A list that named TMDB twice
// would make a client's "more than one provider ran" rule fire on a stock
// install.
func TestSearchWithoutALibraryKeepsBothDefaults(t *testing.T) {
	h, _, mgr := newTestServer(t)
	mgr.provider = &stubProvider{
		movies: []core.MovieMeta{{TMDBID: 1, Title: "Dune"}},
		series: []core.SeriesMeta{{TMDBID: 2, Title: "Dune: Prophecy"}},
	}

	rec := do(t, h, http.MethodGet, "/api/v1/search?q=dune", "")
	wantStatus(t, rec, http.StatusOK)
	var body searchResponse
	decodeBody(t, rec, &body)

	if body.LibraryID != 0 {
		t.Errorf("library_id = %d, want 0 when none was named", body.LibraryID)
	}
	if !slices.Equal(body.Providers, []string{core.ProviderTMDB}) {
		t.Errorf("providers = %v, want tmdb named once", body.Providers)
	}
	calls := mgr.searchCalls()
	if len(calls) != 2 {
		t.Fatalf("search calls = %+v, want both halves", calls)
	}
	for _, c := range calls {
		if c.libraryID != 0 {
			t.Errorf("search call = %+v, want library 0 so the manager picks the default", c)
		}
	}
}

// ?library_id=<adult> is refused rather than searched.
//
// /search sits in FRONT of requireAdult, so without this refusal naming an
// adult library would route a stash-box chain through the television endpoint
// and the gate the adult surfaces are built on would simply not be in the path.
// The refusal a caller the library is invisible to gets is the 404 every other
// adult-adjacent route gives, for the same reason: "this exists and you may not
// have it" is the worse leak on a module whose promise is absence.
func TestSearchRefusesAnAdultLibrary(t *testing.T) {
	t.Run("granted caller is refused with a 400", func(t *testing.T) {
		h, st, mgr := newTestServer(t)
		mgr.provider = &stubProvider{}
		enableAdult(t, st)
		lib, err := st.GetDefaultLibrary(context.Background(), core.LibraryKindAdult)
		if err != nil {
			t.Fatalf("GetDefaultLibrary(adult): %v", err)
		}

		rec := do(t, h, http.MethodGet, "/api/v1/search?q=brazzers&library_id="+itoa(lib.ID), "")
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
		if calls := mgr.searchCalls(); len(calls) != 0 {
			t.Errorf("an adult library still reached a chain: %+v", calls)
		}
	})

	t.Run("invisible caller is refused with a 404", func(t *testing.T) {
		// The module was on once — that is what created the library — and has
		// been switched off, so the row is still there and the caller is
		// somebody it must not exist for.
		h, st, mgr := newTestServer(t)
		mgr.provider = &stubProvider{}
		enableAdult(t, st)
		lib, err := st.GetDefaultLibrary(context.Background(), core.LibraryKindAdult)
		if err != nil {
			t.Fatalf("GetDefaultLibrary(adult): %v", err)
		}
		setAdultLibrariesActive(t, st, false)

		rec := do(t, h, http.MethodGet, "/api/v1/search?q=brazzers&library_id="+itoa(lib.ID), "")
		wantStatus(t, rec, http.StatusNotFound)
		if calls := mgr.searchCalls(); len(calls) != 0 {
			t.Errorf("an adult library still reached a chain: %+v", calls)
		}
	})
}

// A rejected TMDB key must not make an AniList library unsearchable.
//
// The cached verdict is about ONE credential, and AniList needs none. Before
// the chain-aware check, a stale rejection on a key the library never uses
// refused every search the install could make.
func TestARejectedTMDBKeyLeavesAnAniListLibrarySearchable(t *testing.T) {
	h, st, mgr := newTestServer(t)
	setSetting(t, st, store.SettingTMDBAPIKey, "revoked")
	mgr.provider = &stubProvider{err: errKeyRejected}

	// Prove the key wrong the way a live call does, so the verdict is cached.
	rec := do(t, h, http.MethodGet, "/api/v1/search?q=dune", "")
	wantStatus(t, rec, http.StatusServiceUnavailable)
	wantCode(t, rec, CodeMetadataCredentialInvalid)

	anime := createLibrary(t, h,
		`{"kind":"tv","name":"Anime","root_path":"library/Anime","providers":["anilist"]}`)
	mgr.searchHits = &library.SearchHits{
		Series:    []core.SeriesMeta{{Provider: core.ProviderAniList, ProviderRef: "154587", Title: "Frieren"}},
		Providers: []string{core.ProviderAniList},
	}

	rec = do(t, h, http.MethodGet, "/api/v1/search?q=frieren&library_id="+itoa(anime.ID), "")
	wantStatus(t, rec, http.StatusOK)
	var body searchResponse
	decodeBody(t, rec, &body)
	if len(body.Series) != 1 || body.Series[0].Provider != core.ProviderAniList {
		t.Fatalf("series = %+v, want the anilist hit", body.Series)
	}

	// The verdict still stands for a chain that does contain TMDB.
	rec = do(t, h, http.MethodGet, "/api/v1/search?q=dune", "")
	wantStatus(t, rec, http.StatusServiceUnavailable)
	wantCode(t, rec, CodeMetadataCredentialInvalid)
}

// The same refusal, now with two credentials in play rather than one and a
// keyless one.
//
// A rejected TheTVDB key must stop a TheTVDB-chained library and nothing else.
// This is the pair TMDB alone could never prove: the per-chain check has to
// find the verdict for the id ON THIS CHAIN, so a TVmaze library goes on
// searching and — see TestARejectedTheTVDBKeyLeavesTMDBHealthy — the TMDB card
// stays green.
func TestARejectedTheTVDBKeyRefusesOnlyItsOwnChain(t *testing.T) {
	h, st, mgr := newTestServer(t)
	setSetting(t, st, store.SettingTMDBAPIKey, "good")
	setSetting(t, st, store.SettingTheTVDBAPIKey, "revoked")
	mgr.validateKeys = map[string]error{core.ProviderTheTVDB + "/revoked": errKeyRejected}

	// Prove the key wrong the way the Test button does, so the verdict is cached.
	rec := do(t, h, http.MethodPost, "/api/v1/settings/metadata/test", `{"provider":"thetvdb"}`)
	wantStatus(t, rec, http.StatusBadGateway)

	tvdbLib := createLibrary(t, h,
		`{"kind":"tv","name":"Anime","root_path":"library/Anime","providers":["thetvdb"]}`)
	mazeLib := createLibrary(t, h,
		`{"kind":"tv","name":"Shows","root_path":"library/Shows","providers":["tvmaze"]}`)

	rec = do(t, h, http.MethodGet, "/api/v1/search?q=frieren&library_id="+itoa(tvdbLib.ID), "")
	wantStatus(t, rec, http.StatusServiceUnavailable)
	wantCode(t, rec, CodeMetadataCredentialInvalid)
	if calls := mgr.searchCalls(); len(calls) != 0 {
		t.Fatalf("a refused chain still ran: %+v", calls)
	}

	mgr.searchHits = &library.SearchHits{
		Series:    []core.SeriesMeta{{Provider: core.ProviderTVmaze, ProviderRef: "169", Title: "Breaking Bad"}},
		Providers: []string{core.ProviderTVmaze},
	}
	rec = do(t, h, http.MethodGet, "/api/v1/search?q=breaking+bad&library_id="+itoa(mazeLib.ID), "")
	wantStatus(t, rec, http.StatusOK)
	var body searchResponse
	decodeBody(t, rec, &body)
	if len(body.Series) != 1 || body.Series[0].Provider != core.ProviderTVmaze {
		t.Fatalf("series = %+v, want the tvmaze hit", body.Series)
	}
}

// One provider failing while another answered is a 200 that says so. A chain
// that came back silently short is indistinguishable from a chain that had
// nothing to say.
func TestSearchReportsAPartialChainFailure(t *testing.T) {
	h, _, mgr := newTestServer(t)
	anime := createLibrary(t, h,
		`{"kind":"tv","name":"Anime","root_path":"library/Anime","providers":["tmdb","anilist"]}`)
	mgr.searchHits = &library.SearchHits{
		Series:    []core.SeriesMeta{{TMDBID: 1, Provider: core.ProviderTMDB, ProviderRef: "1", Title: "Frieren"}},
		Providers: []string{core.ProviderTMDB, core.ProviderAniList},
		Failures:  []library.ProviderFailure{{Provider: core.ProviderAniList, Message: "anilist: http 429"}},
	}

	rec := do(t, h, http.MethodGet, "/api/v1/search?q=frieren&library_id="+itoa(anime.ID), "")
	wantStatus(t, rec, http.StatusOK)
	var body searchResponse
	decodeBody(t, rec, &body)
	if len(body.Series) != 1 {
		t.Fatalf("series = %+v, want the surviving provider's hit", body.Series)
	}
	if len(body.Errors) != 1 || body.Errors[0].Provider != core.ProviderAniList {
		t.Fatalf("errors = %+v, want the failed provider named", body.Errors)
	}
}

// A library_id that is not a number, or is negative, is a client bug worth
// reporting rather than a silent fall back to the default library.
func TestSearchRejectsABadLibraryID(t *testing.T) {
	h, _, mgr := newTestServer(t)
	mgr.provider = &stubProvider{}

	for _, raw := range []string{"nope", "-1"} {
		rec := do(t, h, http.MethodGet, "/api/v1/search?q=dune&library_id="+raw, "")
		if rec.Code != http.StatusBadRequest {
			t.Errorf("library_id=%s: status = %d, want 400", raw, rec.Code)
		}
	}
	// A library that does not exist is the 404 every by-id route gives.
	rec := do(t, h, http.MethodGet, "/api/v1/search?q=dune&library_id=99999", "")
	wantStatus(t, rec, http.StatusNotFound)
}
