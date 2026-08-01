package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

func TestListMoviesEmpty(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/library/movies", "")
	wantStatus(t, rec, http.StatusOK)
	// An empty library must be an empty array, not null: the SPA iterates it.
	if got := rec.Body.String(); got != "{\"movies\":[]}\n" {
		t.Fatalf("body = %q, want an empty movies array", got)
	}
}

func TestAddAndGetMovie(t *testing.T) {
	h, st, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/library/movies", `{"tmdb_id":10378}`)
	wantStatus(t, rec, http.StatusCreated)
	var created movieJSON
	decodeBody(t, rec, &created)
	if created.ID == 0 || created.TMDBID != 10378 {
		t.Fatalf("created = %+v, want an id and the requested tmdb id", created)
	}
	if created.AddedAt == "" {
		t.Fatalf("created.AddedAt is empty, want an RFC3339 timestamp")
	}

	// A file on the movie shows up on the detail response.
	f := &core.MediaFile{
		Path:    "Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv",
		Size:    42,
		MovieID: created.ID,
		Quality: core.Quality1080p,
	}
	if err := st.UpsertMediaFile(context.Background(), f); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/library/movies/"+itoa(created.ID), "")
	wantStatus(t, rec, http.StatusOK)
	var detail movieJSON
	decodeBody(t, rec, &detail)
	if detail.ID != created.ID {
		t.Fatalf("detail.ID = %d, want %d", detail.ID, created.ID)
	}
	if detail.File == nil || detail.File.Path != f.Path {
		t.Fatalf("detail.File = %+v, want the linked file", detail.File)
	}
	if detail.File.Quality != core.Quality1080p {
		t.Fatalf("file quality = %q, want %q", detail.File.Quality, core.Quality1080p)
	}

	// The listing sees it too, with the file attached: the poster grid decides
	// downloaded-vs-wanted from it without fetching each movie.
	rec = do(t, h, http.MethodGet, "/api/v1/library/movies", "")
	wantStatus(t, rec, http.StatusOK)
	var list struct {
		Movies []movieJSON `json:"movies"`
	}
	decodeBody(t, rec, &list)
	if len(list.Movies) != 1 || list.Movies[0].ID != created.ID {
		t.Fatalf("movies = %+v, want the created movie", list.Movies)
	}
	if list.Movies[0].File == nil || list.Movies[0].File.Path != f.Path {
		t.Fatalf("listed movie file = %+v, want the linked file", list.Movies[0].File)
	}
}

func TestPatchMovieMonitored(t *testing.T) {
	h, st, _ := newTestServer(t)

	m := &core.Movie{TMDBID: 11, Title: "Arrival", Monitored: true}
	if err := st.UpsertMovie(context.Background(), m); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	rec := do(t, h, http.MethodPatch, "/api/v1/library/movies/"+itoa(m.ID), `{"monitored":false}`)
	wantStatus(t, rec, http.StatusOK)
	var updated movieJSON
	decodeBody(t, rec, &updated)
	if updated.Monitored {
		t.Fatal("response still reports monitored, want false")
	}

	stored, err := st.GetMovie(context.Background(), m.ID)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if stored.Monitored {
		t.Fatal("stored movie is still monitored, want false")
	}

	// A body that names no field is a client bug, not a silent no-op.
	rec = do(t, h, http.MethodPatch, "/api/v1/library/movies/"+itoa(m.ID), `{}`)
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)

	rec = do(t, h, http.MethodPatch, "/api/v1/library/movies/9999", `{"monitored":true}`)
	wantStatus(t, rec, http.StatusNotFound)
	wantErrorBody(t, rec)
}

func TestAddMovieRejectsBadRequests(t *testing.T) {
	h, _, _ := newTestServer(t)

	tests := []struct {
		name string
		body string
	}{
		{"missing tmdb id", `{}`},
		{"zero tmdb id", `{"tmdb_id":0}`},
		{"negative tmdb id", `{"tmdb_id":-3}`},
		{"malformed json", `{"tmdb_id":`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/v1/library/movies", tt.body)
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
		})
	}
}

func TestAddMovieReportsManagerFailure(t *testing.T) {
	h, _, mgr := newTestServer(t)

	t.Run("unknown provider id is 404", func(t *testing.T) {
		mgr.addErr = store.ErrNotFound
		rec := do(t, h, http.MethodPost, "/api/v1/library/movies", `{"tmdb_id":1}`)
		wantStatus(t, rec, http.StatusNotFound)
		wantErrorBody(t, rec)
	})

	t.Run("upstream failure is 502", func(t *testing.T) {
		mgr.addErr = errors.New("tmdb unreachable")
		rec := do(t, h, http.MethodPost, "/api/v1/library/movies", `{"tmdb_id":1}`)
		wantStatus(t, rec, http.StatusBadGateway)
		wantErrorBody(t, rec)
	})
}

func TestGetMovieNotFound(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/library/movies/999", "")
	wantStatus(t, rec, http.StatusNotFound)
	wantErrorBody(t, rec)

	rec = do(t, h, http.MethodGet, "/api/v1/library/movies/abc", "")
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}

func TestDeleteMovie(t *testing.T) {
	h, st, _ := newTestServer(t)

	m := &core.Movie{TMDBID: 7, Title: "Gone"}
	if err := st.UpsertMovie(context.Background(), m); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	rec := do(t, h, http.MethodDelete, "/api/v1/library/movies/"+itoa(m.ID), "")
	wantStatus(t, rec, http.StatusNoContent)
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty on 204", rec.Body.String())
	}

	rec = do(t, h, http.MethodGet, "/api/v1/library/movies/"+itoa(m.ID), "")
	wantStatus(t, rec, http.StatusNotFound)

	// Deleting again is a 404, not a silent success.
	rec = do(t, h, http.MethodDelete, "/api/v1/library/movies/"+itoa(m.ID), "")
	wantStatus(t, rec, http.StatusNotFound)
	wantErrorBody(t, rec)
}

func TestAddAndListSeries(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/library/series", `{"tmdb_id":66732}`)
	wantStatus(t, rec, http.StatusCreated)
	var created seriesJSON
	decodeBody(t, rec, &created)
	if created.ID == 0 || created.TMDBID != 66732 {
		t.Fatalf("created = %+v, want an id and the requested tmdb id", created)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/library/series", "")
	wantStatus(t, rec, http.StatusOK)
	var list struct {
		Series []seriesJSON `json:"series"`
	}
	decodeBody(t, rec, &list)
	if len(list.Series) != 1 || list.Series[0].ID != created.ID {
		t.Fatalf("series = %+v, want the created series", list.Series)
	}

	rec = do(t, h, http.MethodPost, "/api/v1/library/series", `{}`)
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}

func TestGetSeriesIncludesSeasonsAndEpisodes(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	sr := &core.Series{TMDBID: 3, Title: "Planet Earth II", Year: 2016}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	season := &core.Season{SeriesID: sr.ID, Number: 1, Title: "Season 1", Monitored: true}
	if err := st.UpsertSeason(ctx, season); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	ep := &core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Islands"}
	if err := st.UpsertEpisode(ctx, ep); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	// An episode whose season row is missing must still be visible.
	orphan := &core.Episode{SeriesID: sr.ID, SeasonNumber: 2, EpisodeNumber: 1, Title: "Orphan"}
	if err := st.UpsertEpisode(ctx, orphan); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}

	f := &core.MediaFile{Path: "TV/Planet Earth II (2016)/Season 01/S01E01.mkv", Size: 9}
	if err := st.UpsertMediaFile(ctx, f); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	if err := st.LinkEpisodeFile(ctx, ep.ID, f.ID); err != nil {
		t.Fatalf("LinkEpisodeFile: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/library/series/"+itoa(sr.ID), "")
	wantStatus(t, rec, http.StatusOK)
	var detail seriesDetailJSON
	decodeBody(t, rec, &detail)

	if detail.Title != "Planet Earth II" {
		t.Fatalf("title = %q, want %q", detail.Title, "Planet Earth II")
	}
	if len(detail.Seasons) != 2 {
		t.Fatalf("seasons = %+v, want 2 (one stored, one derived from an orphan episode)", detail.Seasons)
	}
	if detail.Seasons[0].SeasonNumber != 1 || detail.Seasons[1].SeasonNumber != 2 {
		t.Fatalf("season numbers = %d, %d, want 1, 2",
			detail.Seasons[0].SeasonNumber, detail.Seasons[1].SeasonNumber)
	}
	if detail.Seasons[0].ID != season.ID || !detail.Seasons[0].Monitored {
		t.Fatalf("season 1 = %+v, want the stored row", detail.Seasons[0])
	}
	if len(detail.Seasons[0].Episodes) != 1 || detail.Seasons[0].Episodes[0].Title != "Islands" {
		t.Fatalf("season 1 episodes = %+v, want S01E01", detail.Seasons[0].Episodes)
	}
	if got := detail.Seasons[0].Episodes[0].File; got == nil || got.Path != f.Path {
		t.Fatalf("episode file = %+v, want the linked file", got)
	}
	if detail.Seasons[1].ID != 0 || len(detail.Seasons[1].Episodes) != 1 {
		t.Fatalf("derived season = %+v, want no row id and the orphan episode", detail.Seasons[1])
	}
	// Two episodes exist, one of which has a file: the counts the poster grid
	// renders as "1 / 2".
	if detail.EpisodeCount != 2 || detail.EpisodeFileCount != 1 {
		t.Fatalf("counts = %d/%d, want 1/2", detail.EpisodeFileCount, detail.EpisodeCount)
	}
}

func TestListSeriesCarriesEpisodeCounts(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	sr := &core.Series{TMDBID: 5, Title: "Severance"}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	for n := 1; n <= 3; n++ {
		e := &core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: n}
		if err := st.UpsertEpisode(ctx, e); err != nil {
			t.Fatalf("UpsertEpisode: %v", err)
		}
		if n > 1 {
			continue
		}
		f := &core.MediaFile{Path: "TV/Severance/Season 01/S01E01.mkv"}
		if err := st.UpsertMediaFile(ctx, f); err != nil {
			t.Fatalf("UpsertMediaFile: %v", err)
		}
		if err := st.LinkEpisodeFile(ctx, e.ID, f.ID); err != nil {
			t.Fatalf("LinkEpisodeFile: %v", err)
		}
	}

	rec := do(t, h, http.MethodGet, "/api/v1/library/series", "")
	wantStatus(t, rec, http.StatusOK)
	var list struct {
		Series []seriesJSON `json:"series"`
	}
	decodeBody(t, rec, &list)
	if len(list.Series) != 1 {
		t.Fatalf("series = %+v, want one", list.Series)
	}
	if list.Series[0].EpisodeCount != 3 || list.Series[0].EpisodeFileCount != 1 {
		t.Fatalf("counts = %d/%d, want 1/3",
			list.Series[0].EpisodeFileCount, list.Series[0].EpisodeCount)
	}
}

func TestPatchSeasonAndEpisodeMonitored(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	sr := &core.Series{TMDBID: 9, Title: "Andor"}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	season := &core.Season{SeriesID: sr.ID, Number: 1, Monitored: true}
	if err := st.UpsertSeason(ctx, season); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	ep := &core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: 1, Monitored: true}
	if err := st.UpsertEpisode(ctx, ep); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}

	rec := do(t, h, http.MethodPatch,
		"/api/v1/library/series/"+itoa(sr.ID)+"/seasons/1", `{"monitored":false}`)
	wantStatus(t, rec, http.StatusNoContent)

	seasons, err := st.ListSeasons(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListSeasons: %v", err)
	}
	if seasons[0].Monitored {
		t.Fatal("season is still monitored, want false")
	}
	// SPEC §7 makes the cascade a bulk update rather than a lock: the season
	// toggle pushes its flag down to the episode, and the episode can still be
	// toggled back on its own afterwards.
	child, err := st.GetEpisode(ctx, ep.ID)
	if err != nil {
		t.Fatalf("GetEpisode: %v", err)
	}
	if child.Monitored {
		t.Fatal("episode did not inherit the season's unmonitored cascade")
	}

	rec = do(t, h, http.MethodPatch, "/api/v1/library/episodes/"+itoa(ep.ID), `{"monitored":false}`)
	wantStatus(t, rec, http.StatusNoContent)
	child, err = st.GetEpisode(ctx, ep.ID)
	if err != nil {
		t.Fatalf("GetEpisode: %v", err)
	}
	if child.Monitored {
		t.Fatal("episode is still monitored, want false")
	}

	// A season the series does not have is a 404, not a silent success.
	rec = do(t, h, http.MethodPatch,
		"/api/v1/library/series/"+itoa(sr.ID)+"/seasons/7", `{"monitored":false}`)
	wantStatus(t, rec, http.StatusNotFound)
	wantErrorBody(t, rec)
}

func TestGetSeriesNotFound(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/library/series/404", "")
	wantStatus(t, rec, http.StatusNotFound)
	wantErrorBody(t, rec)
}

func TestRescanIsSingleFlight(t *testing.T) {
	h, _, mgr := newTestServer(t)
	mgr.scanStarted = make(chan struct{}, 2)
	mgr.scanRelease = make(chan struct{})

	rec := do(t, h, http.MethodPost, "/api/v1/library/rescan", "")
	wantStatus(t, rec, http.StatusAccepted)

	select {
	case <-mgr.scanStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("scan did not start")
	}

	// The second request is refused while the first scan is in flight.
	rec = do(t, h, http.MethodPost, "/api/v1/library/rescan", "")
	wantStatus(t, rec, http.StatusConflict)
	wantErrorBody(t, rec)

	rec = do(t, h, http.MethodGet, "/api/v1/system/status", "")
	var status statusResponse
	decodeBody(t, rec, &status)
	if !status.Scanning {
		t.Fatal("status.scanning = false during a scan, want true")
	}

	close(mgr.scanRelease)

	// Once the scan finishes the guard clears and a new scan is accepted.
	deadline := time.Now().Add(2 * time.Second)
	for {
		rec = do(t, h, http.MethodPost, "/api/v1/library/rescan", "")
		if rec.Code == http.StatusAccepted {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("rescan still refused after the scan finished: %d %s", rec.Code, rec.Body.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	// The accepted response only promises the scan was queued, so wait for it
	// to actually enter the manager before counting.
	select {
	case <-mgr.scanStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("second scan did not start")
	}
	if got := mgr.scanCount.Load(); got != 2 {
		t.Fatalf("scans = %d, want 2", got)
	}
}

func TestSearchReturnsBothMediaTypes(t *testing.T) {
	h, _, mgr := newTestServer(t)
	mgr.provider = &stubProvider{
		movies: []core.MovieMeta{{TMDBID: 1, Title: "Dune", Year: 2021, PosterURL: "https://img/dune.jpg"}},
		series: []core.SeriesMeta{{TMDBID: 2, Title: "Dune: Prophecy", Year: 2024}},
	}

	rec := do(t, h, http.MethodGet, "/api/v1/search?q=dune", "")
	wantStatus(t, rec, http.StatusOK)
	var body searchResponse
	decodeBody(t, rec, &body)

	wantMovies := []movieMetaJSON{
		{TMDBID: 1, Title: "Dune", Year: 2021, PosterURL: "https://img/dune.jpg"},
	}
	wantSeries := []seriesMetaJSON{{TMDBID: 2, Title: "Dune: Prophecy", Year: 2024}}
	if !slices.Equal(body.Movies, wantMovies) {
		t.Fatalf("movies = %+v, want %+v", body.Movies, wantMovies)
	}
	if !slices.Equal(body.Series, wantSeries) {
		t.Fatalf("series = %+v, want %+v", body.Series, wantSeries)
	}
}

func TestSearchRestrictsByType(t *testing.T) {
	h, _, mgr := newTestServer(t)
	mgr.provider = &stubProvider{
		movies: []core.MovieMeta{{TMDBID: 1, Title: "Dune"}},
		series: []core.SeriesMeta{{TMDBID: 2, Title: "Dune: Prophecy"}},
	}

	tests := []struct {
		kind              string
		wantMovies, wantS int
	}{
		{"movie", 1, 0},
		{"series", 0, 1},
		{"all", 1, 1},
	}
	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, "/api/v1/search?q=dune&type="+tt.kind, "")
			wantStatus(t, rec, http.StatusOK)
			var body searchResponse
			decodeBody(t, rec, &body)
			if len(body.Movies) != tt.wantMovies || len(body.Series) != tt.wantS {
				t.Fatalf("got %d movies and %d series, want %d and %d",
					len(body.Movies), len(body.Series), tt.wantMovies, tt.wantS)
			}
		})
	}

	rec := do(t, h, http.MethodGet, "/api/v1/search?q=dune&type=music", "")
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}

func TestSearchFailureModes(t *testing.T) {
	h, _, mgr := newTestServer(t)

	t.Run("missing query", func(t *testing.T) {
		mgr.provider = &stubProvider{}
		rec := do(t, h, http.MethodGet, "/api/v1/search?q=%20", "")
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
	})

	t.Run("no provider configured", func(t *testing.T) {
		mgr.provider = nil
		rec := do(t, h, http.MethodGet, "/api/v1/search?q=dune", "")
		wantStatus(t, rec, http.StatusServiceUnavailable)
		wantErrorBody(t, rec)
	})

	t.Run("provider failure", func(t *testing.T) {
		mgr.provider = &stubProvider{err: errors.New("tmdb down")}
		rec := do(t, h, http.MethodGet, "/api/v1/search?q=dune", "")
		wantStatus(t, rec, http.StatusBadGateway)
		wantErrorBody(t, rec)
	})
}

// itoa keeps the URL building in the tests readable.
func itoa(id int64) string {
	return strconv.FormatInt(id, 10)
}
