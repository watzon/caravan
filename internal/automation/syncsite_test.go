package automation

// The deferred catalogue walk (core.JobSyncSite). What is being defended here
// is the ORDER: the walk creates the episode rows, and only rows that exist can
// be wanted, so a search-on-add that ran before the walk would queue nothing
// and look like it had worked.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// syncSitePayload encodes the job body the way the API does — through the
// struct, because the encoded string is also the queue's dedupe key.
func syncSitePayload(t *testing.T, seriesID int64, searchNow bool) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(core.JobSyncSitePayload{SeriesID: seriesID, SearchNow: searchNow})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

func episodeSearchJobs(t *testing.T, ctx context.Context, st *store.Store) []core.Job {
	t.Helper()
	jobs, err := st.ListJobs(ctx, 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	out := []core.Job{}
	for _, job := range jobs {
		if job.Kind == core.JobSearchEpisode {
			out = append(out, job)
		}
	}
	return out
}

// The site exists with no scenes when the job starts; the walk is what puts
// them there, and the searches are queued for what the walk produced.
func TestSyncSiteJobWalksTheCatalogueThenSearches(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdultLibrary(t, st)

	site := core.Series{
		StashID: "site-1", Title: "Brazzers", SortTitle: "brazzers",
		Kind: core.SeriesKindAdult, Monitored: true,
	}
	if err := st.UpsertSeries(ctx, &site); err != nil {
		t.Fatalf("upsert site: %v", err)
	}

	walked := 0
	handler := SyncSiteHandler(func(ctx context.Context, seriesID int64) error {
		if seriesID != site.ID {
			t.Errorf("walk called for series %d, want %d", seriesID, site.ID)
		}
		walked++
		// What a real walk does: file the site's scenes as episodes.
		return st.UpsertEpisode(ctx, &core.Episode{
			SeriesID: site.ID, SeasonNumber: 2022, EpisodeNumber: 1, StashID: "scene-a",
			Title: "Deep Impact", AirDate: time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC),
			Monitored: true,
		})
	})

	if got := episodeSearchJobs(t, ctx, st); len(got) != 0 {
		t.Fatalf("searches queued before the job ran: %+v", got)
	}
	if err := handler(ctx, st, syncSitePayload(t, site.ID, true)); err != nil {
		t.Fatalf("sync_site handler: %v", err)
	}
	if walked != 1 {
		t.Fatalf("walk ran %d times, want 1", walked)
	}

	episodes, err := st.ListEpisodes(ctx, site.ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(episodes) != 1 || episodes[0].StashID != "scene-a" {
		t.Fatalf("episodes = %+v, want the walked scene", episodes)
	}

	queued := episodeSearchJobs(t, ctx, st)
	if len(queued) != 1 {
		t.Fatalf("queued %d episode searches, want 1: %+v", len(queued), queued)
	}
	var payload core.JobSearchEpisodePayload
	if err := json.Unmarshal([]byte(queued[0].Payload), &payload); err != nil {
		t.Fatalf("decode queued search: %v", err)
	}
	if payload.EpisodeID != episodes[0].ID {
		t.Errorf("queued a search for episode %d, want the walked scene %d",
			payload.EpisodeID, episodes[0].ID)
	}
}

// Without the flag the job is the walk and nothing else: adding a site is not
// on its own a decision to start downloading its whole back catalogue.
func TestSyncSiteJobQueuesNoSearchWithoutTheFlag(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdultLibrary(t, st)

	site, _ := addSite(t, ctx, st, "Brazzers", time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC))
	handler := SyncSiteHandler(func(context.Context, int64) error { return nil })

	if err := handler(ctx, st, syncSitePayload(t, site.ID, false)); err != nil {
		t.Fatalf("sync_site handler: %v", err)
	}
	if got := episodeSearchJobs(t, ctx, st); len(got) != 0 {
		t.Fatalf("queued %d searches without search_now: %+v", len(got), got)
	}
}

// An unmonitored site has no wanted scenes, so search_now on it queues nothing.
// The UI does not offer the combination, but the queue must not treat it as an
// error — a job that failed here would retry forever against a site nobody is
// following.
func TestSyncSiteSearchOnAnUnmonitoredSiteQueuesNothing(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdultLibrary(t, st)

	// What an unmonitored add leaves behind: the site AND the scenes the walk
	// filed under it are unmonitored, because library.writeScenes gives a new
	// scene its site's flag. The scene's own flag is the one that matters —
	// that is what the wanted list reads.
	site, scene := addSite(t, ctx, st, "Brazzers", time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC))
	site.Monitored = false
	if err := st.UpsertSeries(ctx, &site); err != nil {
		t.Fatalf("unmonitor site: %v", err)
	}
	scene.Monitored = false
	if err := st.UpsertEpisode(ctx, &scene); err != nil {
		t.Fatalf("unmonitor scene: %v", err)
	}

	handler := SyncSiteHandler(func(context.Context, int64) error { return nil })
	if err := handler(ctx, st, syncSitePayload(t, site.ID, true)); err != nil {
		t.Fatalf("sync_site handler on an unmonitored site: %v", err)
	}
	if got := episodeSearchJobs(t, ctx, st); len(got) != 0 {
		t.Fatalf("queued %d searches for an unmonitored site: %+v", len(got), got)
	}
}

// A failed walk fails the job, so the queue's backoff retries it. Nothing is
// searched for on the strength of a catalogue that was never filed.
func TestSyncSiteJobFailsWhenTheWalkFails(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdultLibrary(t, st)

	site, _ := addSite(t, ctx, st, "Brazzers", time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC))
	boom := errors.New("provider is down")
	handler := SyncSiteHandler(func(context.Context, int64) error { return boom })

	if err := handler(ctx, st, syncSitePayload(t, site.ID, true)); !errors.Is(err, boom) {
		t.Fatalf("handler error = %v, want the walk's own error", err)
	}
	if got := episodeSearchJobs(t, ctx, st); len(got) != 0 {
		t.Fatalf("queued %d searches after a failed walk: %+v", len(got), got)
	}
}

func TestSyncSiteJobRejectsAnUnusablePayload(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	handler := SyncSiteHandler(func(context.Context, int64) error {
		t.Error("the walk ran for a payload that names no site")
		return nil
	})
	for _, payload := range []string{`{}`, `{"series_id":0}`, `not json`} {
		if err := handler(ctx, st, json.RawMessage(payload)); err == nil {
			t.Errorf("payload %q was accepted, want an error", payload)
		}
	}
}
