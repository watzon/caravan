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

func TestDeleteSeries(t *testing.T) {
	h, st, _ := newTestServer(t)

	sr := &core.Series{TMDBID: 7, Title: "Gone"}
	if err := st.UpsertSeries(context.Background(), sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}

	rec := do(t, h, http.MethodDelete, "/api/v1/library/series/"+itoa(sr.ID), "")
	wantStatus(t, rec, http.StatusNoContent)
	if rec.Body.Len() != 0 {
		t.Fatalf("body = %q, want empty on 204", rec.Body.String())
	}

	rec = do(t, h, http.MethodGet, "/api/v1/library/series/"+itoa(sr.ID), "")
	wantStatus(t, rec, http.StatusNotFound)

	rec = do(t, h, http.MethodDelete, "/api/v1/library/series/"+itoa(sr.ID), "")
	wantStatus(t, rec, http.StatusNotFound)
	wantErrorBody(t, rec)
}

// The ?files=true switch is the whole difference between untracking an item
// and deleting media, so the HTTP layer has to forward exactly what was asked
// for: nothing but an explicit "true" may reach the manager as a file delete.
func TestDeleteForwardsFilesSwitch(t *testing.T) {
	tests := []struct {
		name  string
		query string
		want  bool
	}{
		{name: "absent", query: ""},
		{name: "false", query: "?files=false"},
		{name: "empty", query: "?files="},
		{name: "not-a-bool", query: "?files=1"},
		{name: "true", query: "?files=true", want: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h, st, mgr := newTestServer(t)
			ctx := context.Background()

			m := &core.Movie{TMDBID: 7, Title: "Gone"}
			if err := st.UpsertMovie(ctx, m); err != nil {
				t.Fatalf("UpsertMovie: %v", err)
			}
			sr := &core.Series{TMDBID: 8, Title: "Also Gone"}
			if err := st.UpsertSeries(ctx, sr); err != nil {
				t.Fatalf("UpsertSeries: %v", err)
			}

			rec := do(t, h, http.MethodDelete, "/api/v1/library/movies/"+itoa(m.ID)+tc.query, "")
			wantStatus(t, rec, http.StatusNoContent)
			rec = do(t, h, http.MethodDelete, "/api/v1/library/series/"+itoa(sr.ID)+tc.query, "")
			wantStatus(t, rec, http.StatusNoContent)

			calls := mgr.removeCalls()
			want := []removeCall{
				{kind: "movie", id: m.ID, deleteFiles: tc.want},
				{kind: "series", id: sr.ID, deleteFiles: tc.want},
			}
			if !slices.Equal(calls, want) {
				t.Fatalf("remove calls = %+v, want %+v", calls, want)
			}
		})
	}
}

// seedActiveGrab writes a grabbed-status grab and its in-flight download row,
// which is what "this item is downloading right now" looks like in the store.
func seedActiveGrab(t *testing.T, st *store.Store, info core.GrabInfo, engineID core.DownloadID) *core.Grab {
	t.Helper()
	ctx := context.Background()
	g := &core.Grab{GrabInfo: info, Status: core.GrabStatusGrabbed}
	g.ReleaseTitle = "Release." + string(engineID)
	if err := st.InsertGrab(ctx, g); err != nil {
		t.Fatalf("InsertGrab: %v", err)
	}
	if err := st.UpsertDownload(ctx, &core.Download{
		GrabID:   g.GrabID,
		Engine:   "embedded",
		EngineID: engineID,
		Title:    g.ReleaseTitle,
		State:    core.DownloadDownloading,
	}); err != nil {
		t.Fatalf("UpsertDownload: %v", err)
	}
	return g
}

// Deleting a movie with a download in flight withdraws the download first:
// the engine is told to drop it with its data, the queue row goes, and the
// grab is closed as cancelled rather than left claiming to be active.
func TestDeleteMovieCancelsItsActiveDownload(t *testing.T) {
	engine := &stubEngine{}
	h, st, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: engine}))
	ctx := context.Background()

	m := &core.Movie{TMDBID: 7, Title: "Gone", Monitored: true}
	if err := st.UpsertMovie(ctx, m); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	g := seedActiveGrab(t, st, core.GrabInfo{MovieID: m.ID}, "hash-cancel")

	rec := do(t, h, http.MethodDelete, "/api/v1/library/movies/"+itoa(m.ID), "")
	wantStatus(t, rec, http.StatusNoContent)

	if want := []engineRemove{{id: "hash-cancel", deleteData: true}}; !slices.Equal(engine.removed, want) {
		t.Fatalf("engine removals = %+v, want %+v", engine.removed, want)
	}
	if _, err := st.GetDownloadByEngineID(ctx, "hash-cancel"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("download row: err = %v, want ErrNotFound", err)
	}
	got, err := st.GetGrab(ctx, g.GrabID)
	if err != nil {
		t.Fatalf("GetGrab: %v", err)
	}
	if got.Status != core.GrabStatusCancelled {
		t.Fatalf("grab status = %q, want %q", got.Status, core.GrabStatusCancelled)
	}
}

// A season pack's one grab covers several episodes; deleting the series must
// withdraw that one download once, not once per episode.
func TestDeleteSeriesCancelsASeasonPackOnce(t *testing.T) {
	engine := &stubEngine{}
	h, st, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: engine}))
	ctx := context.Background()

	sr := &core.Series{TMDBID: 8, Title: "Also Gone", Monitored: true}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	e1 := airedEpisode(t, st, sr.ID, 1, 1)
	e2 := airedEpisode(t, st, sr.ID, 1, 2)
	g := seedActiveGrab(t, st,
		core.GrabInfo{SeriesID: sr.ID, SeasonNum: 1, EpisodeIDs: []int64{e1.ID, e2.ID}}, "hash-pack")

	rec := do(t, h, http.MethodDelete, "/api/v1/library/series/"+itoa(sr.ID), "")
	wantStatus(t, rec, http.StatusNoContent)

	if want := []engineRemove{{id: "hash-pack", deleteData: true}}; !slices.Equal(engine.removed, want) {
		t.Fatalf("engine removals = %+v, want %+v", engine.removed, want)
	}
	got, err := st.GetGrab(ctx, g.GrabID)
	if err != nil {
		t.Fatalf("GetGrab: %v", err)
	}
	if got.Status != core.GrabStatusCancelled {
		t.Fatalf("grab status = %q, want %q", got.Status, core.GrabStatusCancelled)
	}
}

// Cancel-first is the contract: when the engine cannot withdraw the download,
// the delete fails with the library untouched, because deleting the item while
// its download kept running is exactly what removal must not do.
func TestDeleteMovieFailsWhenCancelFails(t *testing.T) {
	engine := &stubEngine{controlErr: errors.New("engine unreachable")}
	h, st, mgr := newTestServer(t, WithEngine(&stubEngineProvider{engine: engine}))
	ctx := context.Background()

	m := &core.Movie{TMDBID: 7, Title: "Stays", Monitored: true}
	if err := st.UpsertMovie(ctx, m); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	g := seedActiveGrab(t, st, core.GrabInfo{MovieID: m.ID}, "hash-stuck")

	rec := do(t, h, http.MethodDelete, "/api/v1/library/movies/"+itoa(m.ID)+"?files=true", "")
	wantStatus(t, rec, http.StatusBadGateway)
	wantErrorBody(t, rec)

	if calls := mgr.removeCalls(); len(calls) != 0 {
		t.Fatalf("remove calls = %+v, want none", calls)
	}
	if _, err := st.GetDownloadByEngineID(ctx, "hash-stuck"); err != nil {
		t.Fatalf("download row should survive a failed cancel: %v", err)
	}
	got, err := st.GetGrab(ctx, g.GrabID)
	if err != nil {
		t.Fatalf("GetGrab: %v", err)
	}
	if got.Status != core.GrabStatusGrabbed {
		t.Fatalf("grab status = %q, want %q", got.Status, core.GrabStatusGrabbed)
	}
}

// Without any engine configured nothing can actually be downloading, so the
// delete proceeds and still cleans up the rows the grab left behind.
func TestDeleteMovieWithoutEngineStillClosesTheGrab(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	m := &core.Movie{TMDBID: 7, Title: "Gone", Monitored: true}
	if err := st.UpsertMovie(ctx, m); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	g := seedActiveGrab(t, st, core.GrabInfo{MovieID: m.ID}, "hash-noengine")

	rec := do(t, h, http.MethodDelete, "/api/v1/library/movies/"+itoa(m.ID), "")
	wantStatus(t, rec, http.StatusNoContent)

	if _, err := st.GetDownloadByEngineID(ctx, "hash-noengine"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("download row: err = %v, want ErrNotFound", err)
	}
	got, err := st.GetGrab(ctx, g.GrabID)
	if err != nil {
		t.Fatalf("GetGrab: %v", err)
	}
	if got.Status != core.GrabStatusCancelled {
		t.Fatalf("grab status = %q, want %q", got.Status, core.GrabStatusCancelled)
	}
}

// A 404 is decided before the manager is asked, so a delete of something that
// never existed cannot delete files by accident.
func TestDeleteUnknownItemNeverReachesTheManager(t *testing.T) {
	h, _, mgr := newTestServer(t)

	for _, target := range []string{"/api/v1/library/movies/404?files=true", "/api/v1/library/series/404?files=true"} {
		rec := do(t, h, http.MethodDelete, target, "")
		wantStatus(t, rec, http.StatusNotFound)
		wantErrorBody(t, rec)
	}
	if calls := mgr.removeCalls(); len(calls) != 0 {
		t.Fatalf("remove calls = %+v, want none", calls)
	}
}

// A removal that fails on the filesystem must not answer 204: the item is
// still in the library and the UI has to say so.
func TestDeleteReportsManagerFailure(t *testing.T) {
	h, st, mgr := newTestServer(t)
	mgr.removeErr = errors.New("permission denied")

	m := &core.Movie{TMDBID: 7, Title: "Stays"}
	if err := st.UpsertMovie(context.Background(), m); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	rec := do(t, h, http.MethodDelete, "/api/v1/library/movies/"+itoa(m.ID)+"?files=true", "")
	wantStatus(t, rec, http.StatusBadGateway)
	wantErrorBody(t, rec)

	rec = do(t, h, http.MethodGet, "/api/v1/library/movies/"+itoa(m.ID), "")
	wantStatus(t, rec, http.StatusOK)
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

// POST /library/movies carries the availability choice onto the row, and both
// halves of the movie-only rule hold.
func TestAddMovieMinAvailabilityContract(t *testing.T) {
	h, st, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/library/movies",
		`{"tmdb_id":78,"min_availability":"announced"}`)
	wantStatus(t, rec, http.StatusCreated)
	var created movieJSON
	decodeBody(t, rec, &created)
	if created.MinAvailability != core.AvailabilityAnnounced {
		t.Errorf("min_availability = %q, want announced", created.MinAvailability)
	}
	m, err := st.GetMovieByTMDBID(context.Background(), 78)
	if err != nil {
		t.Fatalf("GetMovieByTMDBID: %v", err)
	}
	if m.MinAvailability != core.AvailabilityAnnounced {
		t.Errorf("stored min_availability = %q, want announced", m.MinAvailability)
	}

	rec = do(t, h, http.MethodPost, "/api/v1/library/movies",
		`{"tmdb_id":79,"min_availability":"day_one"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)

	rec = do(t, h, http.MethodPost, "/api/v1/library/series",
		`{"tmdb_id":1396,"min_availability":"released"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}
