package api

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

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
