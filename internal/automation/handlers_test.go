package automation

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
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

type failingIndexer struct{ fakeIndexer }

func (f *failingIndexer) Test(context.Context) error { return errors.New("dial timeout") }

func (f *failingIndexer) factory() api.IndexerFactory {
	return func(core.IndexerConfig) api.IndexerClient { return f }
}

type delayedMovieIndexer struct {
	fakeIndexer
	started chan<- struct{}
	release <-chan struct{}
}

func (f *delayedMovieIndexer) SearchMovie(ctx context.Context, _ string, _ []int) ([]core.Release, error) {
	select {
	case f.started <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	select {
	case <-f.release:
		return f.movies, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type completedMovieIndexer struct {
	fakeIndexer
	completed chan<- struct{}
}

func (f *completedMovieIndexer) SearchMovie(_ context.Context, _ string, _ []int) ([]core.Release, error) {
	f.completed <- struct{}{}
	return f.movies, nil
}

type fakeEngine struct {
	adds int
	// added is every release this engine took, so a routing test can see a
	// misroute as a release in the wrong engine rather than only as a
	// missing one.
	added []core.Release
}

func (e *fakeEngine) Add(_ context.Context, r core.Release, _ core.AddOpts) (core.DownloadID, error) {
	e.adds++
	e.added = append(e.added, r)
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
	return NewRunner(st, indexer.factory(), func(context.Context, int64, string) core.Engine { return engine })
}

func addIndexer(t *testing.T, ctx context.Context, st *store.Store) {
	t.Helper()
	if err := st.UpsertIndexer(ctx, &core.IndexerConfig{Name: "fake", URL: "https://fake.invalid", Enabled: true}); err != nil {
		t.Fatalf("upsert indexer: %v", err)
	}
}

func addMovie(t *testing.T, ctx context.Context, st *store.Store, title string, year int, monitored bool) *core.Movie {
	t.Helper()
	movie := &core.Movie{Title: title, SortTitle: title, Year: year, Monitored: monitored,
		LibraryID: defaultLibraryID(t, ctx, st, core.LibraryKindMovie)}
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
	payload, _ := json.Marshal(core.JobSearchMoviePayload{MovieID: movie.ID})

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
	linked, err := st.GetGrabByDownloadID(ctx, "fake-download")
	if err != nil {
		t.Fatalf("GetGrabByDownloadID: %v", err)
	}
	if linked.GrabID != grabs[0].GrabID {
		t.Fatalf("linked grab = %d, want winning grab %d", linked.GrabID, grabs[0].GrabID)
	}
}

func TestRunnerHandleSearchMovieKeepsIndexerPriorityWhenResultsFinishOutOfOrder(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	highConfig := &core.IndexerConfig{
		Name: "high-priority", URL: "https://high.invalid", Enabled: true, Priority: 1,
	}
	if err := st.UpsertIndexer(ctx, highConfig); err != nil {
		t.Fatalf("upsert high-priority indexer: %v", err)
	}
	if err := st.UpsertIndexer(ctx, &core.IndexerConfig{
		Name: "low-priority", URL: "https://low.invalid", Enabled: true, Priority: 2,
	}); err != nil {
		t.Fatalf("upsert low-priority indexer: %v", err)
	}
	movie := addMovie(t, ctx, st, "Example Movie", 2024, true)
	highStarted := make(chan struct{}, 1)
	lowCompleted := make(chan struct{}, 1)
	releaseHigh := make(chan struct{})
	defer func() {
		select {
		case <-releaseHigh:
		default:
			close(releaseHigh)
		}
	}()
	high := &delayedMovieIndexer{
		fakeIndexer: fakeIndexer{movies: []core.Release{{
			GUID: "high", Title: "Example Movie 2024 1080p high", Protocol: core.ProtocolTorrent,
			Parsed: core.ParsedRelease{Quality: core.Quality1080p, Source: core.SourceWebDL},
		}}},
		started: highStarted,
		release: releaseHigh,
	}
	low := &completedMovieIndexer{
		fakeIndexer: fakeIndexer{movies: []core.Release{{
			GUID: "low", Title: "Example Movie 2024 1080p low", Protocol: core.ProtocolTorrent,
			Parsed: core.ParsedRelease{Quality: core.Quality1080p, Source: core.SourceWebDL},
		}}},
		completed: lowCompleted,
	}
	engine := &fakeEngine{}
	runner := NewRunner(st, func(cfg core.IndexerConfig) api.IndexerClient {
		if cfg.ID == highConfig.ID {
			return high
		}
		return low
	}, func(context.Context, int64, string) core.Engine { return engine })
	payload, _ := json.Marshal(core.JobSearchMoviePayload{MovieID: movie.ID})
	done := make(chan error, 1)
	go func() { done <- runner.handleSearchMovie(ctx, st, payload) }()

	select {
	case <-highStarted:
	case <-time.After(time.Second):
		t.Fatal("higher-priority indexer did not start")
	}
	select {
	case <-lowCompleted:
	case <-time.After(time.Second):
		t.Fatal("lower-priority indexer did not complete first")
	}
	close(releaseHigh)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("handle search movie: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("search did not finish")
	}
	if len(engine.added) != 1 || engine.added[0].GUID != "high" {
		t.Fatalf("selected releases = %+v, want the higher-priority equal-score release", engine.added)
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
	payload, _ := json.Marshal(core.JobSearchMoviePayload{MovieID: movie.ID})

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
	if err := st.EnqueueJob(ctx, &core.Job{Kind: core.JobRSSSync, Payload: "{}"}); err != nil {
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
		if job.Kind == core.JobRSSSync {
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
	firstPayload, _ := json.Marshal(core.JobSearchMoviePayload{MovieID: first.ID})
	if err := st.EnqueueJob(ctx, &core.Job{Kind: core.JobSearchMovie, Payload: string(firstPayload)}); err != nil {
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
		if job.Kind != core.JobSearchMovie {
			continue
		}
		var payload core.JobSearchMoviePayload
		if err := json.Unmarshal([]byte(job.Payload), &payload); err != nil {
			t.Fatalf("decode movie job payload: %v", err)
		}
		movieJobs[payload.MovieID]++
	}
	if movieJobs[first.ID] != 1 || movieJobs[second.ID] != 1 {
		t.Fatalf("movie search jobs = %#v, want one per wanted movie", movieJobs)
	}
}

// routedRunner builds a runner whose engine is the real protocol router
// (PLAN phase 6 task 3), so the automatic path is exercised through exactly
// the dispatch the interactive one uses. A nil usenet engine is the
// configuration a stock Caravan has.
func routedRunner(st *store.Store, indexer *fakeIndexer, torrent, usenet *fakeEngine) *Runner {
	routes := []download.Route{
		{Name: download.EngineName, Protocol: core.ProtocolTorrent, Engine: torrent},
	}
	if usenet != nil {
		routes = append(routes, download.Route{
			Name: core.DownloadClientSABnzbd, Protocol: core.ProtocolUsenet, Engine: usenet,
		})
	}
	router := download.NewRouter(func(context.Context) ([]download.Route, error) { return routes, nil })
	return NewRunner(st, indexer.factory(), func(context.Context, int64, string) core.Engine { return router })
}

// The automatic path routes by protocol exactly like the interactive one: an
// automatic search that picks a Newznab result must reach the usenet client,
// not the torrent engine.
func TestRunnerAutomaticGrabRoutesByProtocol(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	addIndexer(t, ctx, st)
	torrentMovie := addMovie(t, ctx, st, "Torrent Movie", 2024, true)
	usenetMovie := addMovie(t, ctx, st, "Usenet Movie", 2024, true)
	torrent := &fakeEngine{}
	usenet := &fakeEngine{}

	indexer := &fakeIndexer{}
	runner := routedRunner(st, indexer, torrent, usenet)

	indexer.movies = []core.Release{{
		GUID: "t", Title: "Torrent Movie 2024 1080p", Protocol: core.ProtocolTorrent, Seeders: 5,
		Parsed: core.ParsedRelease{Quality: core.Quality1080p, Source: core.SourceWebDL},
	}}
	payload, _ := json.Marshal(core.JobSearchMoviePayload{MovieID: torrentMovie.ID})
	if err := runner.handleSearchMovie(ctx, st, payload); err != nil {
		t.Fatalf("handle search movie (torrent): %v", err)
	}

	indexer.movies = []core.Release{{
		GUID: "u", Title: "Usenet Movie 2024 1080p", Protocol: core.ProtocolUsenet,
		Parsed: core.ParsedRelease{Quality: core.Quality1080p, Source: core.SourceWebDL},
	}}
	payload, _ = json.Marshal(core.JobSearchMoviePayload{MovieID: usenetMovie.ID})
	if err := runner.handleSearchMovie(ctx, st, payload); err != nil {
		t.Fatalf("handle search movie (usenet): %v", err)
	}

	if len(torrent.added) != 1 || torrent.added[0].Protocol != core.ProtocolTorrent {
		t.Fatalf("torrent engine got %+v, want only the torrent release", torrent.added)
	}
	if len(usenet.added) != 1 || usenet.added[0].Protocol != core.ProtocolUsenet {
		t.Fatalf("usenet engine got %+v, want only the usenet release", usenet.added)
	}
}

// A usenet release picked by an automatic search with no usenet client
// configured is a recorded rejection, not a job failure: retrying it every
// sweep would never succeed, and a silent drop leaves "why is nothing
// downloading" unanswerable.
func TestRunnerAutomaticGrabRecordsUnroutableProtocol(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	addIndexer(t, ctx, st)
	movie := addMovie(t, ctx, st, "Usenet Movie", 2024, true)
	torrent := &fakeEngine{}
	indexer := &fakeIndexer{movies: []core.Release{{
		GUID: "u", Title: "Usenet Movie 2024 1080p", Protocol: core.ProtocolUsenet,
		Parsed: core.ParsedRelease{Quality: core.Quality1080p, Source: core.SourceWebDL},
	}}}
	runner := routedRunner(st, indexer, torrent, nil)
	payload, _ := json.Marshal(core.JobSearchMoviePayload{MovieID: movie.ID})

	// The job completes: there is nothing to retry until the user configures
	// a client.
	if err := runner.handleSearchMovie(ctx, st, payload); err != nil {
		t.Fatalf("handle search movie: %v", err)
	}
	if len(torrent.added) != 0 {
		t.Fatalf("the usenet release reached the torrent engine: %+v", torrent.added)
	}

	grabs, err := st.ListGrabs(ctx, 0)
	if err != nil {
		t.Fatalf("list grabs: %v", err)
	}
	if len(grabs) != 1 || grabs[0].Status != core.GrabStatusRejected {
		t.Fatalf("grabs = %#v, want one rejected grab", grabs)
	}
	if !strings.Contains(grabs[0].Reason, "Usenet servers") {
		t.Fatalf("recorded reason = %q, want the reason the user can act on", grabs[0].Reason)
	}

	events, err := st.ListEvents(ctx, 0)
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 || events[0].Level != core.EventLevelWarn || events[0].Category != "grab" {
		t.Fatalf("events = %#v, want one warning grab event", events)
	}
}

func TestIndexerHealthDisablesAfterRepeatedFailures(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	addIndexer(t, ctx, st)
	runner := NewRunner(st, (&failingIndexer{}).factory(), nil)

	for i := 0; i < core.IndexerHealthDisableAfter; i++ {
		if err := runner.handleIndexerHealth(ctx, st, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("health run %d: %v", i+1, err)
		}
	}
	list, err := st.ListIndexers(ctx)
	if err != nil {
		t.Fatalf("ListIndexers: %v", err)
	}
	if len(list) != 1 || list[0].Enabled || list[0].HealthError == "" {
		t.Fatalf("after %d failures = %+v, want disabled with an error", core.IndexerHealthDisableAfter, list)
	}
}
