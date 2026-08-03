package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// openJobs returns the queue's rows of one kind, newest last, so a test can
// assert both the payload and the absence of a duplicate.
func openJobs(t *testing.T, st *store.Store, kind string) []core.Job {
	t.Helper()
	jobs, err := st.ListJobs(context.Background(), 100)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	out := []core.Job{}
	for _, j := range jobs {
		if j.Kind == kind {
			out = append(out, j)
		}
	}
	return out
}

func wantQueued(t *testing.T, h http.Handler, target string, want int) {
	t.Helper()
	rec := do(t, h, http.MethodPost, target, "")
	wantStatus(t, rec, http.StatusAccepted)
	var body searchQueuedResponse
	decodeBody(t, rec, &body)
	if body.Queued != want {
		t.Fatalf("POST %s queued = %d, want %d", target, body.Queued, want)
	}
}

// airedEpisode writes a monitored episode that aired a week ago, which is what
// makes it wanted: the wanted list deliberately skips episodes that have not
// aired yet.
func airedEpisode(t *testing.T, st *store.Store, seriesID int64, season, number int) *core.Episode {
	t.Helper()
	e := &core.Episode{
		SeriesID:      seriesID,
		SeasonNumber:  season,
		EpisodeNumber: number,
		Title:         "Episode",
		AirDate:       time.Now().UTC().AddDate(0, 0, -7),
		Monitored:     true,
	}
	if err := st.UpsertEpisode(context.Background(), e); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	return e
}

func TestSearchMovieNowQueuesOneJobAndDedupes(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	m := &core.Movie{TMDBID: 11, Title: "Arrival", Monitored: true}
	if err := st.UpsertMovie(ctx, m); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	wantQueued(t, h, "/api/v1/library/movies/"+itoa(m.ID)+"/search", 1)

	jobs := openJobs(t, st, core.JobSearchMovie)
	if len(jobs) != 1 {
		t.Fatalf("search_movie jobs = %d, want 1", len(jobs))
	}
	if want := `{"movie_id":` + itoa(m.ID) + `}`; jobs[0].Payload != want {
		t.Fatalf("payload = %q, want %q", jobs[0].Payload, want)
	}

	// A second click while the first search is still queued adds nothing: the
	// dedupe is the payload, so the worker sees one search either way.
	wantQueued(t, h, "/api/v1/library/movies/"+itoa(m.ID)+"/search", 0)
	if jobs := openJobs(t, st, core.JobSearchMovie); len(jobs) != 1 {
		t.Fatalf("search_movie jobs after a repeat = %d, want 1", len(jobs))
	}
}

// A movie whose file already meets the profile cutoff is not wanted, and the
// search_movie handler has no guard against re-grabbing one — so the endpoint
// must refuse to queue it rather than hand the worker something to upgrade.
func TestSearchMovieNowSkipsAnAtCutoffMovie(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	m := &core.Movie{TMDBID: 12, Title: "Dune", Monitored: true}
	if err := st.UpsertMovie(ctx, m); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	if err := st.UpsertMediaFile(ctx, &core.MediaFile{
		Path: "Movies/Dune (2021)/Dune (2021).mkv", Size: 42, MovieID: m.ID, Quality: core.Quality1080p,
	}); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}

	wantQueued(t, h, "/api/v1/library/movies/"+itoa(m.ID)+"/search", 0)
	if jobs := openJobs(t, st, core.JobSearchMovie); len(jobs) != 0 {
		t.Fatalf("search_movie jobs = %d, want none for an at-cutoff movie", len(jobs))
	}
}

func TestSearchMovieNowRejectsUnknownMovie(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/library/movies/9999/search", "")
	wantStatus(t, rec, http.StatusNotFound)
	wantErrorBody(t, rec)
}

func TestSearchSeriesNowQueuesOnlyItsOwnWantedEpisodes(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	mine := &core.Series{TMDBID: 21, Title: "Severance", Monitored: true}
	other := &core.Series{TMDBID: 22, Title: "Andor", Monitored: true}
	for _, sr := range []*core.Series{mine, other} {
		if err := st.UpsertSeries(ctx, sr); err != nil {
			t.Fatalf("UpsertSeries: %v", err)
		}
	}
	first := airedEpisode(t, st, mine.ID, 1, 1)
	second := airedEpisode(t, st, mine.ID, 1, 2)
	airedEpisode(t, st, other.ID, 1, 1)

	wantQueued(t, h, "/api/v1/library/series/"+itoa(mine.ID)+"/search", 2)

	jobs := openJobs(t, st, core.JobSearchEpisode)
	if len(jobs) != 2 {
		t.Fatalf("search_episode jobs = %d, want 2 (the other series must not be swept in)", len(jobs))
	}
	queued := map[string]bool{}
	for _, j := range jobs {
		queued[j.Payload] = true
	}
	for _, e := range []*core.Episode{first, second} {
		if want := `{"episode_id":` + itoa(e.ID) + `}`; !queued[want] {
			t.Fatalf("payloads = %v, want one for %q", queued, want)
		}
	}

	wantQueued(t, h, "/api/v1/library/series/"+itoa(mine.ID)+"/search", 0)
	if jobs := openJobs(t, st, core.JobSearchEpisode); len(jobs) != 2 {
		t.Fatalf("search_episode jobs after a repeat = %d, want 2", len(jobs))
	}
}

func TestSearchSeriesNowRejectsUnknownSeries(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/library/series/9999/search", "")
	wantStatus(t, rec, http.StatusNotFound)
	wantErrorBody(t, rec)
}

func TestSearchWantedQueuesTheWholeList(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	m := &core.Movie{TMDBID: 31, Title: "Arrival", Monitored: true}
	if err := st.UpsertMovie(ctx, m); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	sr := &core.Series{TMDBID: 32, Title: "Severance", Monitored: true}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	airedEpisode(t, st, sr.ID, 1, 1)
	airedEpisode(t, st, sr.ID, 1, 2)

	// An unmonitored movie is not wanted, so it must not be part of the count.
	if err := st.UpsertMovie(ctx, &core.Movie{TMDBID: 33, Title: "Ignored", Monitored: false}); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	wantQueued(t, h, "/api/v1/wanted/search", 3)
	if jobs := openJobs(t, st, core.JobSearchMovie); len(jobs) != 1 {
		t.Fatalf("search_movie jobs = %d, want 1", len(jobs))
	}
	if jobs := openJobs(t, st, core.JobSearchEpisode); len(jobs) != 2 {
		t.Fatalf("search_episode jobs = %d, want 2", len(jobs))
	}

	// Everything is already queued, so a second sweep queues nothing.
	wantQueued(t, h, "/api/v1/wanted/search", 0)
}

func TestAddMovieSearchesOnlyWhenAsked(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"without the flag", `{"tmdb_id":10378}`, 0},
		{"with search_now false", `{"tmdb_id":10378,"search_now":false}`, 0},
		{"with search_now true", `{"tmdb_id":10378,"search_now":true}`, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, st, _ := newTestServer(t)

			rec := do(t, h, http.MethodPost, "/api/v1/library/movies", tt.body)
			wantStatus(t, rec, http.StatusCreated)
			var created movieJSON
			decodeBody(t, rec, &created)

			jobs := openJobs(t, st, core.JobSearchMovie)
			if len(jobs) != tt.want {
				t.Fatalf("search_movie jobs = %d, want %d", len(jobs), tt.want)
			}
			if tt.want == 1 {
				if want := `{"movie_id":` + itoa(created.ID) + `}`; jobs[0].Payload != want {
					t.Fatalf("payload = %q, want %q", jobs[0].Payload, want)
				}
			}
		})
	}
}

func TestAddSeriesSearchesMissingOnlyWhenAsked(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"without the flag", `{"tmdb_id":1396}`, 0},
		{"with search_missing false", `{"tmdb_id":1396,"search_missing":false}`, 0},
		{"with search_missing true", `{"tmdb_id":1396,"search_missing":true}`, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, st, mgr := newTestServer(t)
			mgr.addSeriesEpisodes = 3

			rec := do(t, h, http.MethodPost, "/api/v1/library/series", tt.body)
			wantStatus(t, rec, http.StatusCreated)

			if jobs := openJobs(t, st, core.JobSearchEpisode); len(jobs) != tt.want {
				t.Fatalf("search_episode jobs = %d, want %d", len(jobs), tt.want)
			}
		})
	}
}

// A monitored movie that has not reached its minimum availability must not be
// searched: before the gate, adding an unreleased movie queued a search that
// could only find junk. A file on disk overrides the calendar — whatever
// exists is graded against the profile regardless of dates.
func TestSearchWantedSkipsUnavailableMovies(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	future := time.Now().UTC().AddDate(1, 0, 0)
	unreleased := &core.Movie{TMDBID: 41, Title: "Dune Part Three", Monitored: true,
		ReleaseDate: future}
	if err := st.UpsertMovie(ctx, unreleased); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	// Same dates, but the user said announced: searchable immediately.
	eager := &core.Movie{TMDBID: 42, Title: "Impatience", Monitored: true,
		ReleaseDate: future, MinAvailability: core.AvailabilityAnnounced}
	if err := st.UpsertMovie(ctx, eager); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	wantQueued(t, h, "/api/v1/wanted/search", 1)
	jobs := openJobs(t, st, core.JobSearchMovie)
	if len(jobs) != 1 {
		t.Fatalf("search_movie jobs = %d, want 1", len(jobs))
	}
	if want := `{"movie_id":` + itoa(eager.ID) + `}`; jobs[0].Payload != want {
		t.Fatalf("payload = %q, want %q (the announced movie, not the gated one)", jobs[0].Payload, want)
	}

	// Search-now on the gated movie answers "nothing to search", exactly like
	// an unaired episode.
	wantQueued(t, h, "/api/v1/library/movies/"+itoa(unreleased.ID)+"/search", 0)
}
