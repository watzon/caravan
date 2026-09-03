package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// A finished job keeps its name in the feed: the History page shows what a
// done or failed search was about, not just its kind.
func TestHandleListJobsNamesFinishedSubjects(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	movie := &core.Movie{TMDBID: 329865, Title: "Arrival", SortTitle: "arrival"}
	if err := st.UpsertMovie(ctx, movie); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	payload, err := json.Marshal(core.JobSearchMoviePayload{MovieID: movie.ID})
	if err != nil {
		t.Fatalf("encode movie payload: %v", err)
	}
	if err := st.EnqueueJob(ctx, &core.Job{Kind: core.JobSearchMovie, Payload: string(payload)}); err != nil {
		t.Fatalf("EnqueueJob: %v", err)
	}
	claimed, err := st.ClaimJob(ctx, []string{core.JobSearchMovie}, time.Minute)
	if err != nil || claimed == nil {
		t.Fatalf("ClaimJob: %v (job=%v)", err, claimed)
	}
	if err := st.CompleteJob(ctx, claimed.ID); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/jobs?limit=10", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Jobs []jobJSON `json:"jobs"`
	}
	decodeBody(t, rec, &body)
	for _, job := range body.Jobs {
		if job.ID != claimed.ID {
			continue
		}
		if job.State != core.JobStateDone {
			t.Fatalf("state = %q, want %q", job.State, core.JobStateDone)
		}
		if job.Subject != "Arrival" || job.SubjectKind != "movie" || job.SubjectID != movie.ID {
			t.Fatalf("finished job subject = %q/%q/%d, want Arrival/movie/%d", job.Subject, job.SubjectKind, job.SubjectID, movie.ID)
		}
		return
	}
	t.Fatalf("job %d missing from the feed: %+v", claimed.ID, body.Jobs)
}

func TestHandleListJobsReturnsNewestFeed(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	if err := st.EnqueueJob(ctx, &core.Job{Kind: "rss_sync", Payload: "{}"}); err != nil {
		t.Fatalf("enqueue first job: %v", err)
	}
	if err := st.EnqueueJob(ctx, &core.Job{Kind: "search_movie", Payload: `{"movie_id":42}`}); err != nil {
		t.Fatalf("enqueue second job: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/jobs?limit=1", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Jobs       []jobJSON `json:"jobs"`
		NextCursor string    `json:"next_cursor"`
	}
	decodeBody(t, rec, &body)
	if len(body.Jobs) != 1 {
		t.Fatalf("jobs = %d, want limit of one", len(body.Jobs))
	}
	job := body.Jobs[0]
	if job.Kind != "search_movie" || job.State != core.JobStatePending || job.CreatedAt == "" || job.UpdatedAt == "" {
		t.Fatalf("job = %#v, want newest persisted job fields", job)
	}
	var payload struct {
		MovieID int64 `json:"movie_id"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.MovieID != 42 {
		t.Fatalf("payload = %s, want movie id 42: %v", job.Payload, err)
	}

	if body.NextCursor == "" {
		t.Fatal("paged job response has empty continuation cursor")
	}
	cursor := body.NextCursor
	rec = do(t, h, http.MethodGet, "/api/v1/jobs?limit=1&cursor="+cursor, "")
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &body)
	if len(body.Jobs) != 1 || body.Jobs[0].Kind != "rss_sync" || body.NextCursor != "" {
		t.Fatalf("final job page = %+v cursor %q, want rss_sync and no cursor", body.Jobs, body.NextCursor)
	}

	for _, bad := range []string{"", "0", "-1", "many"} {
		if bad == "" {
			continue
		}
		rec = do(t, h, http.MethodGet, "/api/v1/jobs?cursor="+bad, "")
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
	}
}

func TestHandleCancelJobsStopsOpenSearches(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	movie := &core.Movie{TMDBID: 1, Title: "Arrival", SortTitle: "arrival"}
	if err := st.UpsertMovie(ctx, movie); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	moviePayload, err := json.Marshal(core.JobSearchMoviePayload{MovieID: movie.ID})
	if err != nil {
		t.Fatalf("marshal movie payload: %v", err)
	}
	if err := st.EnqueueJob(ctx, &core.Job{Kind: core.JobSearchMovie, Payload: string(moviePayload)}); err != nil {
		t.Fatalf("enqueue movie search: %v", err)
	}
	if err := st.EnqueueJob(ctx, &core.Job{Kind: core.JobRSSSync, Payload: "{}"}); err != nil {
		t.Fatalf("enqueue rss: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/jobs/cancel", `{}`)
	wantStatus(t, rec, http.StatusOK)
	var body cancelJobsResponse
	decodeBody(t, rec, &body)
	if body.Cancelled != 1 {
		t.Fatalf("cancelled = %d, want 1 search", body.Cancelled)
	}

	jobs, err := st.ListJobs(ctx, 10)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	var searchState, rssState string
	for _, job := range jobs {
		switch job.Kind {
		case core.JobSearchMovie:
			searchState = job.State
		case core.JobRSSSync:
			rssState = job.State
		}
	}
	if searchState != core.JobStateCancelled {
		t.Errorf("search state = %q, want cancelled", searchState)
	}
	if rssState != core.JobStatePending {
		t.Errorf("rss state = %q, want pending", rssState)
	}

	rec = do(t, h, http.MethodPost, "/api/v1/jobs/cancel", `{"kinds":["rss_sync"]}`)
	wantStatus(t, rec, http.StatusBadRequest)
}

func TestHandleCancelJobsCanLimitToOneTitle(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	keep := &core.Movie{TMDBID: 2, Title: "Dune", SortTitle: "dune"}
	if err := st.UpsertMovie(ctx, keep); err != nil {
		t.Fatalf("UpsertMovie keep: %v", err)
	}
	drop := &core.Movie{TMDBID: 3, Title: "Heat", SortTitle: "heat"}
	if err := st.UpsertMovie(ctx, drop); err != nil {
		t.Fatalf("UpsertMovie drop: %v", err)
	}
	for _, movie := range []*core.Movie{keep, drop} {
		payload, err := json.Marshal(core.JobSearchMoviePayload{MovieID: movie.ID})
		if err != nil {
			t.Fatalf("marshal payload: %v", err)
		}
		if err := st.EnqueueJob(ctx, &core.Job{Kind: core.JobSearchMovie, Payload: string(payload)}); err != nil {
			t.Fatalf("enqueue search: %v", err)
		}
	}

	rec := do(t, h, http.MethodPost, "/api/v1/jobs/cancel",
		fmt.Sprintf(`{"subject_kind":"movie","subject_id":%d}`, drop.ID))
	wantStatus(t, rec, http.StatusOK)
	var body cancelJobsResponse
	decodeBody(t, rec, &body)
	if body.Cancelled != 1 {
		t.Fatalf("cancelled = %d, want 1", body.Cancelled)
	}

	keepOpen, err := st.HasOpenJob(ctx, core.JobSearchMovie, string(mustJSON(t, core.JobSearchMoviePayload{MovieID: keep.ID})))
	if err != nil {
		t.Fatalf("HasOpenJob keep: %v", err)
	}
	if !keepOpen {
		t.Fatal("cancelled the movie that was not named")
	}
}

func mustJSON(t *testing.T, payload any) []byte {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	return raw
}

func TestHandleListJobsNamesLiveSearchSubjects(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	movie := &core.Movie{TMDBID: 329865, Title: "Arrival", SortTitle: "arrival"}
	if err := st.UpsertMovie(ctx, movie); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	show := &core.Series{Kind: core.SeriesKindTV, TMDBID: 95396, Title: "Severance", SortTitle: "severance"}
	if err := st.UpsertSeries(ctx, show); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	site := &core.Series{Kind: core.SeriesKindAdult, StashID: "site-1", Title: "Transfixed", SortTitle: "transfixed"}
	if err := st.UpsertSeries(ctx, site); err != nil {
		t.Fatalf("UpsertSeries(adult): %v", err)
	}
	episode := &core.Episode{SeriesID: show.ID, SeasonNumber: 1, EpisodeNumber: 2, Title: "Half Loop"}
	if err := st.UpsertEpisode(ctx, episode); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	scene := &core.Episode{SeriesID: site.ID, SeasonNumber: 2026, EpisodeNumber: 20, Title: "An Unexpected Craving"}
	if err := st.UpsertEpisode(ctx, scene); err != nil {
		t.Fatalf("UpsertEpisode(scene): %v", err)
	}

	moviePayload, err := json.Marshal(core.JobSearchMoviePayload{MovieID: movie.ID})
	if err != nil {
		t.Fatalf("encode movie payload: %v", err)
	}
	episodePayload, err := json.Marshal(core.JobSearchEpisodePayload{EpisodeID: episode.ID})
	if err != nil {
		t.Fatalf("encode episode payload: %v", err)
	}
	scenePayload, err := json.Marshal(core.JobSearchEpisodePayload{EpisodeID: scene.ID})
	if err != nil {
		t.Fatalf("encode scene payload: %v", err)
	}
	for _, job := range []*core.Job{
		{Kind: core.JobSearchMovie, Payload: string(moviePayload)},
		{Kind: core.JobSearchEpisode, Payload: string(episodePayload)},
		{Kind: core.JobSearchEpisode, Payload: string(scenePayload)},
	} {
		if err := st.EnqueueJob(ctx, job); err != nil {
			t.Fatalf("EnqueueJob(%s): %v", job.Kind, err)
		}
	}

	rec := do(t, h, http.MethodGet, "/api/v1/jobs?limit=10", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Jobs []jobJSON `json:"jobs"`
	}
	decodeBody(t, rec, &body)
	type got struct {
		kind string
		id   int64
	}
	found := map[string]got{}
	for _, job := range body.Jobs {
		if job.Subject == "" {
			continue
		}
		found[job.Subject] = got{kind: job.SubjectKind, id: job.SubjectID}
	}
	want := map[string]got{
		"Arrival":    {kind: "movie", id: movie.ID},
		"Severance":  {kind: "series", id: show.ID},
		"Transfixed": {kind: "site", id: site.ID},
	}
	for name, wantRow := range want {
		if found[name] != wantRow {
			t.Fatalf("subject %q = %+v, want %+v (jobs=%+v)", name, found[name], wantRow, body.Jobs)
		}
	}
}
