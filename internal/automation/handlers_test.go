package automation

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

type fakeIndexer struct {
	rss    []core.Release
	movies []core.Release
	tv     []core.Release
}

func (f *fakeIndexer) Search(_ context.Context, _ string, _ []int) ([]core.Release, error) {
	return f.rss, nil
}

func (f *fakeIndexer) SearchMovie(_ context.Context, _ string, _ []int) ([]core.Release, error) {
	return f.movies, nil
}

func (f *fakeIndexer) SearchTV(_ context.Context, _ string, _ int, _ int, _ []int) ([]core.Release, error) {
	return f.tv, nil
}

func (f *fakeIndexer) Test(context.Context) error { return nil }

func (f *fakeIndexer) Categories(context.Context) ([]core.IndexerCategory, error) {
	return nil, nil
}

func (f *fakeIndexer) factory() api.IndexerFactory {
	return func(core.IndexerConfig) api.IndexerClient { return f }
}

type fakeEngine struct {
	adds int
}

func (e *fakeEngine) Add(_ context.Context, _ core.Release, _ core.AddOpts) (core.DownloadID, error) {
	e.adds++
	return core.DownloadID("fake-download"), nil
}

func (e *fakeEngine) Status(context.Context, core.DownloadID) (*core.DownloadStatus, error) {
	return nil, nil
}

func (e *fakeEngine) List(context.Context) ([]core.DownloadStatus, error) { return nil, nil }
func (e *fakeEngine) Pause(context.Context, core.DownloadID) error        { return nil }
func (e *fakeEngine) Resume(context.Context, core.DownloadID) error       { return nil }
func (e *fakeEngine) Remove(context.Context, core.DownloadID, bool) error { return nil }
func (e *fakeEngine) Close() error                                        { return nil }

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

func newRunner(st *store.Store, indexer *fakeIndexer, engine *fakeEngine) *Runner {
	return NewRunner(st, indexer.factory(), func(context.Context) core.Engine { return engine })
}

func addIndexer(t *testing.T, ctx context.Context, st *store.Store) {
	t.Helper()
	if err := st.UpsertIndexer(ctx, &core.IndexerConfig{Name: "fake", URL: "https://fake.invalid", Enabled: true}); err != nil {
		t.Fatalf("upsert indexer: %v", err)
	}
}

func addMovie(t *testing.T, ctx context.Context, st *store.Store, title string, year int, monitored bool) *core.Movie {
	t.Helper()
	movie := &core.Movie{Title: title, SortTitle: title, Year: year, Monitored: monitored}
	if err := st.UpsertMovie(ctx, movie); err != nil {
		t.Fatalf("upsert movie: %v", err)
	}
	return movie
}

func TestRunnerHandleSearchMovieGrabsBestOnce(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	addIndexer(t, ctx, st)
	movie := addMovie(t, ctx, st, "Example Movie", 2024, true)
	indexer := &fakeIndexer{movies: []core.Release{
		{GUID: "low", Title: "Example Movie 2024 720p", Protocol: core.ProtocolTorrent, Seeders: 10, Parsed: core.ParsedRelease{Quality: core.Quality720p, Source: core.SourceWebDL}},
		{GUID: "best", Title: "Example Movie 2024 1080p", Protocol: core.ProtocolTorrent, Seeders: 5, Parsed: core.ParsedRelease{Quality: core.Quality1080p, Source: core.SourceWebDL}},
	}}
	engine := &fakeEngine{}
	runner := newRunner(st, indexer, engine)
	payload, _ := json.Marshal(moviePayload{MovieID: movie.ID})

	if err := runner.handleSearchMovie(ctx, st, payload); err != nil {
		t.Fatalf("handle search movie: %v", err)
	}
	if err := runner.handleSearchMovie(ctx, st, payload); err != nil {
		t.Fatalf("repeat search movie: %v", err)
	}
	if engine.adds != 1 {
		t.Fatalf("engine Add calls = %d, want 1", engine.adds)
	}
	grabs, err := st.ListGrabs(ctx, 0)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 2 {
		t.Fatalf("grab history rows = %d, want winner plus rejected candidate", len(grabs))
	}
	if grabs[0].ReleaseTitle != "Example Movie 2024 1080p" || grabs[0].Reason == "" {
		t.Fatalf("winning grab = %#v, want scored best release with a reason", grabs[0])
	}
}

func TestRunnerHandleSearchMovieRecordsRejectedCandidates(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	addIndexer(t, ctx, st)
	movie := addMovie(t, ctx, st, "Reject Me", 2024, true)
	indexer := &fakeIndexer{movies: []core.Release{{
		GUID: "rejected", Title: "Reject Me 2024 CAM", Protocol: core.ProtocolTorrent,
		Parsed: core.ParsedRelease{Quality: core.QualityUnknown, Source: core.SourceCam},
	}}}
	runner := newRunner(st, indexer, &fakeEngine{})
	payload, _ := json.Marshal(moviePayload{MovieID: movie.ID})

	if err := runner.handleSearchMovie(ctx, st, payload); err != nil {
		t.Fatalf("handle search movie: %v", err)
	}
	grabs, err := st.ListGrabs(ctx, 0)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 1 || grabs[0].Status != core.GrabStatusRejected || grabs[0].Reason == "" || grabs[0].ReleaseID == 0 {
		t.Fatalf("rejection history = %#v, want one rejected release with an id and reason", grabs)
	}
}

func TestRunnerHandleRSSSyncSchedulesSingleton(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	if err := st.EnqueueJob(ctx, &core.Job{Kind: jobRSSSync, Payload: "{}"}); err != nil {
		t.Fatalf("enqueue rss sync: %v", err)
	}
	runner := newRunner(st, &fakeIndexer{}, &fakeEngine{})

	if err := runner.handleRSSSync(ctx, st, json.RawMessage("{}")); err != nil {
		t.Fatalf("handle rss sync: %v", err)
	}
	jobs, err := st.ListJobs(ctx, 0)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	count := 0
	for _, job := range jobs {
		if job.Kind == jobRSSSync {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("rss sync jobs = %d, want one open singleton", count)
	}
}

func TestRunnerHandleBacklogSweepEnqueuesMissingSearchesOnce(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	first := addMovie(t, ctx, st, "First", 2020, true)
	second := addMovie(t, ctx, st, "Second", 2021, true)
	firstPayload, _ := json.Marshal(moviePayload{MovieID: first.ID})
	if err := st.EnqueueJob(ctx, &core.Job{Kind: jobSearchMovie, Payload: string(firstPayload)}); err != nil {
		t.Fatalf("enqueue existing movie job: %v", err)
	}
	runner := newRunner(st, &fakeIndexer{}, &fakeEngine{})

	if err := runner.handleBacklogSweep(ctx, st, json.RawMessage("{}")); err != nil {
		t.Fatalf("handle backlog sweep: %v", err)
	}
	jobs, err := st.ListJobs(ctx, 0)
	if err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	movieJobs := map[int64]int{}
	for _, job := range jobs {
		if job.Kind != jobSearchMovie {
			continue
		}
		var payload moviePayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			t.Fatalf("decode movie job payload: %v", err)
		}
		movieJobs[payload.MovieID]++
	}
	if movieJobs[first.ID] != 1 || movieJobs[second.ID] != 1 {
		t.Fatalf("movie search jobs = %#v, want one per wanted movie", movieJobs)
	}
}
