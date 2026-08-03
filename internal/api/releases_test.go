package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// addMovie stores a movie to search or grab for.
func addMovie(t *testing.T, st *store.Store, title string, year int) core.Movie {
	t.Helper()
	m := core.Movie{TMDBID: 1234, Title: title, SortTitle: title, Year: year, Monitored: true}
	if err := st.UpsertMovie(context.Background(), &m); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	return m
}

// addSeries stores a series with three episodes in season 1.
func addSeries(t *testing.T, st *store.Store, title string) (core.Series, []core.Episode) {
	t.Helper()
	ctx := context.Background()

	sr := core.Series{TMDBID: 99, Title: title, SortTitle: title, Year: 2016, Monitored: true}
	if err := st.UpsertSeries(ctx, &sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	episodes := make([]core.Episode, 0, 3)
	for number := 1; number <= 3; number++ {
		e := core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: number, Monitored: true}
		if err := st.UpsertEpisode(ctx, &e); err != nil {
			t.Fatalf("UpsertEpisode: %v", err)
		}
		episodes = append(episodes, e)
	}
	return sr, episodes
}

func torrentRelease(title, guid string, seeders int, parsed core.ParsedRelease) core.Release {
	return core.Release{
		Title:       title,
		GUID:        guid,
		DownloadURL: "magnet:?xt=urn:btih:" + guid,
		Protocol:    core.ProtocolTorrent,
		Size:        4 << 30,
		Seeders:     seeders,
		PublishedAt: time.Now().Add(-48 * time.Hour),
		Parsed:      parsed,
	}
}

func TestMovieReleasesFanOutMergesSortsAndCaches(t *testing.T) {
	h, st, _, fake := newAcquisitionServer(t)
	m := addMovie(t, st, "Big Buck Bunny", 2008)

	addIndexer(t, st, fake, "alpha")
	addIndexer(t, st, fake, "beta")
	addIndexer(t, st, fake, "broken")
	fake.breaks("broken")

	fake.serve("alpha",
		torrentRelease("BBB.2008.720p.WEB-DL", "a1", 900, core.ParsedRelease{Title: "Big Buck Bunny", Year: 2008, Quality: core.Quality720p}),
		torrentRelease("BBB.2008.1080p.WEB-DL", "a2", 10, core.ParsedRelease{Title: "Big Buck Bunny", Year: 2008, Quality: core.Quality1080p}),
	)
	fake.serve("beta",
		// Parsed is left empty: the client did not parse, so the API must.
		torrentRelease("Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP", "b1", 50, core.ParsedRelease{}),
		// Same GUID twice from one indexer must collapse to one row.
		torrentRelease("Dead.Release.2009.480p.HDTV", "b2", 0, core.ParsedRelease{Title: "Dead Release", Year: 2009, Quality: core.Quality480p}),
		torrentRelease("Dead.Release.2009.480p.HDTV", "b2", 0, core.ParsedRelease{Title: "Dead Release", Year: 2009, Quality: core.Quality480p}),
	)

	rec := do(t, h, http.MethodGet, "/api/v1/library/movies/"+itoa(m.ID)+"/releases", "")
	wantStatus(t, rec, http.StatusOK)
	var body releasesResponse
	decodeBody(t, rec, &body)

	if body.Query != "Big Buck Bunny 2008" {
		t.Fatalf("query = %q, want the title and year", body.Query)
	}
	if len(body.Releases) != 4 {
		t.Fatalf("releases = %d, want 4 (the duplicate GUID collapsed)", len(body.Releases))
	}

	// Best quality first, then the healthiest swarm.
	wantOrder := []string{
		"Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP", // 1080p, 50 seeders
		"BBB.2008.1080p.WEB-DL",                     // 1080p, 10 seeders
		"BBB.2008.720p.WEB-DL",
		"Dead.Release.2009.480p.HDTV",
	}
	for i, want := range wantOrder {
		if body.Releases[i].Title != want {
			t.Fatalf("release %d = %q, want %q (order %+v)", i, body.Releases[i].Title, want, titlesOf(body.Releases))
		}
	}

	// The API parsed the release the client did not.
	if got := body.Releases[0].Parsed.Quality; got != core.Quality1080p {
		t.Fatalf("parsed quality = %q, want the API to have parsed the title", got)
	}
	if body.Releases[0].Indexer != "beta" || body.Releases[0].IndexerID == 0 {
		t.Fatalf("release = %+v, want it attributed to the indexer that returned it", body.Releases[0])
	}
	if body.Releases[0].AgeDays != 2 {
		t.Fatalf("age_days = %d, want 2", body.Releases[0].AgeDays)
	}

	// The mismatched year and the dead swarm are flagged, the good ones are not.
	dead := body.Releases[3]
	if !slices.Contains(dead.Flags, flagWrongYear) || !slices.Contains(dead.Flags, flagNoSeeders) {
		t.Fatalf("flags = %v, want wrong-year and no-seeders", dead.Flags)
	}
	if len(body.Releases[0].Flags) != 0 {
		t.Fatalf("flags = %v, want none on a matching release", body.Releases[0].Flags)
	}

	// The failing indexer is reported rather than silently dropped.
	if len(body.Errors) != 1 || body.Errors[0].Indexer != "broken" || body.Errors[0].Error == "" {
		t.Fatalf("errors = %+v, want the broken indexer reported", body.Errors)
	}

	// Every row is cached, and its id is what the grab endpoint takes.
	for _, rel := range body.Releases {
		if rel.ID == 0 {
			t.Fatalf("release %q has no cached id", rel.Title)
		}
		cached, err := st.GetRelease(context.Background(), rel.ID)
		if err != nil {
			t.Fatalf("GetRelease(%d): %v", rel.ID, err)
		}
		if cached.Title != rel.Title || cached.Indexer != rel.Indexer {
			t.Fatalf("cached release = %+v, want it to match the response row %+v", cached, rel)
		}
	}

	// Every enabled indexer was asked. These carry no category configuration,
	// so the search goes out unfiltered — never a guessed default, which
	// silently returns nothing from indexers that do not expand parent
	// categories.
	searches := fake.recorded()
	if len(searches) != 3 {
		t.Fatalf("searches = %+v, want one per enabled indexer", searches)
	}
	for _, s := range searches {
		if s.query != "Big Buck Bunny 2008" || s.cats != "" {
			t.Fatalf("search = %+v, want the movie query and no category filter", s)
		}
	}
}

// An indexer that carries its own categories is searched with exactly those,
// on every search type.
func TestReleaseSearchUsesConfiguredCategories(t *testing.T) {
	h, st, _, fake := newAcquisitionServer(t)
	m := addMovie(t, st, "Big Buck Bunny", 2008)
	addIndexer(t, st, fake, "alpha", 2040, 2045)
	fake.serve("alpha")

	rec := do(t, h, http.MethodGet, "/api/v1/library/movies/"+itoa(m.ID)+"/releases", "")
	wantStatus(t, rec, http.StatusOK)

	searches := fake.recorded()
	if len(searches) != 1 || searches[0].cats != "2040,2045" {
		t.Fatalf("searches = %+v, want the configured categories", searches)
	}
}

// overrideLibraryIndexer writes one per-library indexer override, addressing
// the library the way items do: by kind.
func overrideLibraryIndexer(t *testing.T, st *store.Store, kind string, indexerID int64, enabled bool, cats []int) {
	t.Helper()
	ctx := context.Background()
	library, err := st.GetLibraryByKind(ctx, kind)
	if err != nil {
		t.Fatalf("GetLibraryByKind(%q): %v", kind, err)
	}
	if err := st.SetLibraryIndexer(ctx, &core.LibraryIndexer{
		LibraryID: library.ID, IndexerID: indexerID, Enabled: enabled, Categories: cats,
	}); err != nil {
		t.Fatalf("SetLibraryIndexer: %v", err)
	}
}

// An interactive search belongs to a library too: the picker must ask the
// indexers that library searches, with the categories that library asked for
// (PLAN phase 8 task 4).
func TestReleaseSearchUsesTheLibrarysIndexersAndCategories(t *testing.T) {
	h, st, _, fake := newAcquisitionServer(t)
	m := addMovie(t, st, "Big Buck Bunny", 2008)
	sr, _ := addSeries(t, st, "Planet Earth II")
	shared := addIndexer(t, st, fake, "shared", 2000, 5000)
	tvOnly := addIndexer(t, st, fake, "tv-only", 5000)
	fake.serve("shared")
	fake.serve("tv-only")

	overrideLibraryIndexer(t, st, core.LibraryKindMovie, shared.ID, true, []int{2000})
	overrideLibraryIndexer(t, st, core.LibraryKindMovie, tvOnly.ID, false, nil)
	overrideLibraryIndexer(t, st, core.LibraryKindTV, shared.ID, true, []int{5000})

	rec := do(t, h, http.MethodGet, "/api/v1/library/movies/"+itoa(m.ID)+"/releases", "")
	wantStatus(t, rec, http.StatusOK)
	searches := fake.recorded()
	if len(searches) != 1 || searches[0].name != "shared" || searches[0].cats != "2000" {
		t.Fatalf("movie searches = %+v, want only the movie library's indexer and categories", searches)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/library/series/"+itoa(sr.ID)+"/releases", "")
	wantStatus(t, rec, http.StatusOK)
	tvSearches := fake.recorded()[1:]
	byIndexer := map[string]string{}
	for _, s := range tvSearches {
		byIndexer[s.name] = s.cats
	}
	if len(tvSearches) != 2 || byIndexer["shared"] != "5000" || byIndexer["tv-only"] != "5000" {
		t.Fatalf("series searches = %+v, want both tv indexers with the tv categories", tvSearches)
	}
}

func TestSeriesReleasesNarrowsQueryAndFlags(t *testing.T) {
	h, st, _, fake := newAcquisitionServer(t)
	sr, _ := addSeries(t, st, "Planet Earth II")
	addIndexer(t, st, fake, "alpha")

	fake.serve("alpha",
		torrentRelease("Planet.Earth.II.S01E02.1080p", "e2", 100, core.ParsedRelease{Title: "Planet Earth II", Season: 1, Episodes: []int{2}, Quality: core.Quality1080p}),
		torrentRelease("Planet.Earth.II.S01E03.1080p", "e3", 90, core.ParsedRelease{Title: "Planet Earth II", Season: 1, Episodes: []int{3}, Quality: core.Quality1080p}),
		torrentRelease("Planet.Earth.II.S02.1080p", "s2", 80, core.ParsedRelease{Title: "Planet Earth II", Season: 2, Quality: core.Quality1080p}),
	)

	rec := do(t, h, http.MethodGet, "/api/v1/library/series/"+itoa(sr.ID)+"/releases?season=1&episode=2", "")
	wantStatus(t, rec, http.StatusOK)
	var body releasesResponse
	decodeBody(t, rec, &body)

	if body.Query != "Planet Earth II S01E02" {
		t.Fatalf("query = %q, want the SxxEyy form", body.Query)
	}
	if searches := fake.recorded(); len(searches) != 1 || searches[0].cats != "" {
		t.Fatalf("searches = %+v, want no category filter on an unconfigured indexer", searches)
	}

	flags := map[string][]string{}
	for _, rel := range body.Releases {
		flags[rel.GUID] = rel.Flags
	}
	if len(flags["e2"]) != 0 {
		t.Fatalf("flags = %v, want none on the requested episode", flags["e2"])
	}
	if !slices.Contains(flags["e3"], flagWrongEpisode) {
		t.Fatalf("flags = %v, want wrong-episode", flags["e3"])
	}
	if !slices.Contains(flags["s2"], flagWrongSeason) || !slices.Contains(flags["s2"], flagSeasonPack) {
		t.Fatalf("flags = %v, want wrong-season and season-pack", flags["s2"])
	}

	// Narrowing to a season alone asks for the pack.
	rec = do(t, h, http.MethodGet, "/api/v1/library/series/"+itoa(sr.ID)+"/releases?season=1", "")
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &body)
	if body.Query != "Planet Earth II S01" {
		t.Fatalf("query = %q, want the season form", body.Query)
	}

	// With neither, the whole series is searched.
	rec = do(t, h, http.MethodGet, "/api/v1/library/series/"+itoa(sr.ID)+"/releases", "")
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &body)
	if body.Query != "Planet Earth II" {
		t.Fatalf("query = %q, want the bare series title", body.Query)
	}
	for _, rel := range body.Releases {
		if slices.Contains(rel.Flags, flagWrongSeason) {
			t.Fatalf("flags = %v, want no season flag when no season was asked for", rel.Flags)
		}
	}
}

func TestReleaseSearchRejectsBadSeasonEpisode(t *testing.T) {
	h, st, _, fake := newAcquisitionServer(t)
	sr, _ := addSeries(t, st, "Planet Earth II")
	addIndexer(t, st, fake, "alpha")

	for _, query := range []string{"?season=x", "?season=-1", "?season=1&episode=0", "?season=1&episode=x", "?episode=2"} {
		t.Run(query, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, "/api/v1/library/series/"+itoa(sr.ID)+"/releases"+query, "")
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
		})
	}
}

func TestReleaseSearchEdgeCases(t *testing.T) {
	t.Run("no indexers configured", func(t *testing.T) {
		h, st, _, _ := newAcquisitionServer(t)
		m := addMovie(t, st, "Big Buck Bunny", 2008)

		rec := do(t, h, http.MethodGet, "/api/v1/library/movies/"+itoa(m.ID)+"/releases", "")
		wantStatus(t, rec, http.StatusOK)
		var body releasesResponse
		decodeBody(t, rec, &body)
		if len(body.Releases) != 0 || len(body.Errors) != 0 {
			t.Fatalf("body = %+v, want empty lists, not null", body)
		}
	})

	t.Run("disabled indexers are skipped", func(t *testing.T) {
		h, st, _, fake := newAcquisitionServer(t)
		m := addMovie(t, st, "Big Buck Bunny", 2008)
		cfg := addIndexer(t, st, fake, "alpha")
		cfg.Enabled = false
		if err := st.UpsertIndexer(context.Background(), &cfg); err != nil {
			t.Fatalf("UpsertIndexer: %v", err)
		}

		rec := do(t, h, http.MethodGet, "/api/v1/library/movies/"+itoa(m.ID)+"/releases", "")
		wantStatus(t, rec, http.StatusOK)
		if searches := fake.recorded(); len(searches) != 0 {
			t.Fatalf("searches = %+v, want a disabled indexer to be skipped", searches)
		}
	})

	t.Run("unknown item", func(t *testing.T) {
		h, _, _, _ := newAcquisitionServer(t)
		rec := do(t, h, http.MethodGet, "/api/v1/library/movies/99/releases", "")
		wantStatus(t, rec, http.StatusNotFound)
		wantErrorBody(t, rec)
	})

	t.Run("no indexer client configured", func(t *testing.T) {
		h, st, _ := newTestServer(t)
		m := addMovie(t, st, "Big Buck Bunny", 2008)

		rec := do(t, h, http.MethodGet, "/api/v1/library/movies/"+itoa(m.ID)+"/releases", "")
		wantStatus(t, rec, http.StatusServiceUnavailable)
		wantErrorBody(t, rec)
	})
}

// cacheRelease stores a release the way a search would, so a grab test does
// not have to run a search first.
func cacheRelease(t *testing.T, st *store.Store, title string) core.Release {
	t.Helper()
	rel := torrentRelease(title, "guid-"+title, 100, core.ParsedRelease{Title: title, Quality: core.Quality1080p})
	rel.IndexerID = 7
	rel.Indexer = "alpha"
	if err := st.UpsertRelease(context.Background(), &rel); err != nil {
		t.Fatalf("UpsertRelease: %v", err)
	}
	return rel
}

func TestMovieGrab(t *testing.T) {
	h, st, engine, _ := newAcquisitionServer(t)
	ctx := context.Background()
	m := addMovie(t, st, "Big Buck Bunny", 2008)
	rel := cacheRelease(t, st, "BBB.2008.1080p")

	rec := do(t, h, http.MethodPost, "/api/v1/library/movies/"+itoa(m.ID)+"/grab", `{"release_id":`+itoa(rel.ID)+`}`)
	wantStatus(t, rec, http.StatusCreated)
	var body grabResponse
	decodeBody(t, rec, &body)
	if body.GrabID == 0 || body.DownloadID == "" || body.ReleaseTitle != rel.Title {
		t.Fatalf("grab response = %+v, want a grab id, a download id and the release title", body)
	}

	adds := engine.addCalls()
	if len(adds) != 1 {
		t.Fatalf("engine adds = %d, want 1", len(adds))
	}
	if adds[0].release.Title != rel.Title || adds[0].release.DownloadURL != rel.DownloadURL {
		t.Fatalf("engine got release %+v, want the cached one", adds[0].release)
	}
	want := core.AddOpts{Category: engineCategoryMovies, MovieID: m.ID}
	if adds[0].opts.Category != want.Category || adds[0].opts.MovieID != want.MovieID || adds[0].opts.SeriesID != 0 {
		t.Fatalf("engine got opts %+v, want %+v", adds[0].opts, want)
	}

	grabs, err := st.ListGrabs(ctx, 0)
	if err != nil {
		t.Fatalf("ListGrabs: %v", err)
	}
	if len(grabs) != 1 {
		t.Fatalf("grabs = %d, want 1", len(grabs))
	}
	g := grabs[0]
	if g.MovieID != m.ID || g.ReleaseID != rel.ID || g.ReleaseTitle != rel.Title || g.Status != core.GrabStatusGrabbed {
		t.Fatalf("grab = %+v, want it recorded against the movie and release", g)
	}

	dl, err := st.GetDownloadByEngineID(ctx, core.DownloadID(body.DownloadID))
	if err != nil {
		t.Fatalf("GetDownloadByEngineID: %v", err)
	}
	if dl.GrabID != g.GrabID || dl.Engine != "stub" || dl.State != core.DownloadQueued || dl.Title != rel.Title || dl.Size != rel.Size {
		t.Fatalf("download = %+v, want it linked to the grab", dl)
	}

	events, err := st.ListEvents(ctx, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Category != "grab" || events[0].MovieID != m.ID {
		t.Fatalf("events = %+v, want one grab event for the movie", events)
	}
}

// A grab the engine refuses is still history: the attempt and its reason have
// to survive, and no download row may be invented.
func TestMovieGrabRecordsEngineFailure(t *testing.T) {
	h, st, engine, _ := newAcquisitionServer(t)
	ctx := context.Background()
	m := addMovie(t, st, "Big Buck Bunny", 2008)
	rel := cacheRelease(t, st, "BBB.2008.1080p")
	engine.addErr = errors.New("engine is not running")

	rec := do(t, h, http.MethodPost, "/api/v1/library/movies/"+itoa(m.ID)+"/grab", `{"release_id":`+itoa(rel.ID)+`}`)
	wantStatus(t, rec, http.StatusBadGateway)
	wantErrorBody(t, rec)

	grabs, err := st.ListGrabs(ctx, 0)
	if err != nil {
		t.Fatalf("ListGrabs: %v", err)
	}
	if len(grabs) != 1 || grabs[0].Status != core.GrabStatusFailed {
		t.Fatalf("grabs = %+v, want one failed grab", grabs)
	}
	if grabs[0].Reason != "engine is not running" {
		t.Fatalf("reason = %q, want the engine's own message", grabs[0].Reason)
	}

	downloads, err := st.ListDownloads(ctx)
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if len(downloads) != 0 {
		t.Fatalf("downloads = %+v, want none for a refused grab", downloads)
	}

	events, err := st.ListEvents(ctx, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Level != core.EventLevelError {
		t.Fatalf("events = %+v, want one error event", events)
	}
}

func TestGrabRejectsBadRequests(t *testing.T) {
	h, st, _, _ := newAcquisitionServer(t)
	m := addMovie(t, st, "Big Buck Bunny", 2008)
	path := "/api/v1/library/movies/" + itoa(m.ID) + "/grab"

	tests := []struct {
		name string
		path string
		body string
		want int
	}{
		{"no body", path, "", http.StatusBadRequest},
		{"no release id", path, `{}`, http.StatusBadRequest},
		{"negative release id", path, `{"release_id":-1}`, http.StatusBadRequest},
		{"unknown release", path, `{"release_id":404}`, http.StatusNotFound},
		{"unknown movie", "/api/v1/library/movies/99/grab", `{"release_id":1}`, http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, tt.path, tt.body)
			wantStatus(t, rec, tt.want)
			wantErrorBody(t, rec)
		})
	}

	grabs, err := st.ListGrabs(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListGrabs: %v", err)
	}
	if len(grabs) != 0 {
		t.Fatalf("grabs = %+v, want no writes from rejected requests", grabs)
	}
}

func TestGrabWithoutEngine(t *testing.T) {
	tests := []struct {
		name string
		opts []Option
	}{
		{"no engine configured", nil},
		{"engine not started", []Option{WithEngine(&stubEngineProvider{})}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, st, _ := newTestServer(t, tt.opts...)
			m := addMovie(t, st, "Big Buck Bunny", 2008)
			rel := cacheRelease(t, st, "BBB.2008.1080p")

			rec := do(t, h, http.MethodPost, "/api/v1/library/movies/"+itoa(m.ID)+"/grab",
				`{"release_id":`+itoa(rel.ID)+`}`)
			wantStatus(t, rec, http.StatusServiceUnavailable)
			wantErrorBody(t, rec)

			// The grab must not be recorded when it was never attempted.
			grabs, err := st.ListGrabs(context.Background(), 0)
			if err != nil {
				t.Fatalf("ListGrabs: %v", err)
			}
			if len(grabs) != 0 {
				t.Fatalf("grabs = %+v, want none", grabs)
			}
		})
	}
}

func TestSeriesGrabResolvesEpisodes(t *testing.T) {
	h, st, engine, _ := newAcquisitionServer(t)
	sr, episodes := addSeries(t, st, "Planet Earth II")
	rel := cacheRelease(t, st, "Planet.Earth.II.S01E02.1080p")
	body := `{"release_id":` + itoa(rel.ID) + `}`
	base := "/api/v1/library/series/" + itoa(sr.ID) + "/grab"

	rec := do(t, h, http.MethodPost, base+"?season=1&episode=2", body)
	wantStatus(t, rec, http.StatusCreated)

	adds := engine.addCalls()
	if len(adds) != 1 {
		t.Fatalf("engine adds = %d, want 1", len(adds))
	}
	opts := adds[0].opts
	if opts.Category != engineCategoryTV || opts.SeriesID != sr.ID || opts.SeasonNum != 1 {
		t.Fatalf("opts = %+v, want the series and season", opts)
	}
	if !slices.Equal(opts.EpisodeIDs, []int64{episodes[1].ID}) {
		t.Fatalf("episode ids = %v, want just the requested episode (%d)", opts.EpisodeIDs, episodes[1].ID)
	}

	// A season grab covers every episode of that season, which is what lets the
	// import pipeline fan a pack out across episodes.
	rec = do(t, h, http.MethodPost, base+"?season=1", body)
	wantStatus(t, rec, http.StatusCreated)
	adds = engine.addCalls()
	wantIDs := []int64{episodes[0].ID, episodes[1].ID, episodes[2].ID}
	if !slices.Equal(adds[1].opts.EpisodeIDs, wantIDs) {
		t.Fatalf("episode ids = %v, want the whole season %v", adds[1].opts.EpisodeIDs, wantIDs)
	}

	grabs, err := st.ListGrabs(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListGrabs: %v", err)
	}
	if len(grabs) != 2 || grabs[0].SeriesID != sr.ID || len(grabs[0].EpisodeIDs) != 3 {
		t.Fatalf("grabs = %+v, want the season grab to carry its episode ids", grabs)
	}
}

func TestSeriesGrabRejectsUnknownSeasonOrEpisode(t *testing.T) {
	h, st, engine, _ := newAcquisitionServer(t)
	sr, _ := addSeries(t, st, "Planet Earth II")
	rel := cacheRelease(t, st, "Planet.Earth.II.S01E02.1080p")
	body := `{"release_id":` + itoa(rel.ID) + `}`
	base := "/api/v1/library/series/" + itoa(sr.ID) + "/grab"

	for _, query := range []string{"?season=9", "?season=1&episode=9"} {
		t.Run(query, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, base+query, body)
			wantStatus(t, rec, http.StatusNotFound)
			wantErrorBody(t, rec)
		})
	}
	if adds := engine.addCalls(); len(adds) != 0 {
		t.Fatalf("engine adds = %+v, want none", adds)
	}
}

func titlesOf(releases []releaseJSON) []string {
	out := make([]string, 0, len(releases))
	for _, rel := range releases {
		out = append(out, rel.Title)
	}
	return out
}
