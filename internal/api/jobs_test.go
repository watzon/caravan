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
		Jobs []jobJSON `json:"jobs"`
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
}
