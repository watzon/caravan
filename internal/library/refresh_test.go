package library

import (
	"context"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// A refresh rewrites what the provider owns and preserves what the user and
// the disk own: release dates and titles update, while the monitored flag,
// the profile, the minimum availability and the on-disk path survive.
func TestRefreshLibraryUpdatesMovieAndPreservesIntent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.provider.movieByID[10378] = core.MovieMeta{TMDBID: 10378, Title: "Big Buck Bunny", Year: 2008}

	mv, err := h.mgr.AddMovie(ctx, 10378, core.AvailabilityAnnounced, nil)
	if err != nil {
		t.Fatalf("AddMovie: %v", err)
	}
	mv.QualityProfileID = 3
	if err := h.st.UpsertMovie(ctx, mv); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	// TMDB publishes the digital date and retitles the movie.
	digital := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	h.provider.movieByID[10378] = core.MovieMeta{
		TMDBID: 10378, Title: "Big Buck Bunny: Extended", Year: 2008, DigitalRelease: digital,
	}

	res, err := h.mgr.RefreshLibrary(ctx)
	if err != nil {
		t.Fatalf("RefreshLibrary: %v", err)
	}
	if res.Movies != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want one movie and no errors", res)
	}

	got, err := h.st.GetMovie(ctx, mv.ID)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if !got.DigitalRelease.Equal(digital) {
		t.Errorf("DigitalRelease = %v, want the provider's %v", got.DigitalRelease, digital)
	}
	if got.Title != "Big Buck Bunny: Extended" {
		t.Errorf("Title = %q, want the refreshed title", got.Title)
	}
	// The retitle must not repoint the row at a folder that does not exist.
	if got.Path != mv.Path {
		t.Errorf("Path = %q, want the original %q kept", got.Path, mv.Path)
	}
	if got.MinAvailability != core.AvailabilityAnnounced || got.QualityProfileID != 3 || !got.Monitored {
		t.Errorf("movie = %+v, want availability, profile and monitored preserved", got)
	}
}

// Unmonitored and unmatched titles cost no provider round trip. Neither stub
// entry exists, so a fetch for either would land in Errors.
func TestRefreshLibrarySkipsUnmonitoredAndUnmatched(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.provider.movieByID[10378] = core.MovieMeta{TMDBID: 10378, Title: "Big Buck Bunny", Year: 2008}

	if _, err := h.mgr.AddMovie(ctx, 10378, "", nil); err != nil {
		t.Fatalf("AddMovie: %v", err)
	}
	unmonitored := &core.Movie{TMDBID: 99, Title: "Sleeping", Monitored: false}
	if err := h.st.UpsertMovie(ctx, unmonitored); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	unmatched := &core.Movie{Title: "Mystery", Monitored: true}
	if err := h.st.UpsertMovie(ctx, unmatched); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	res, err := h.mgr.RefreshLibrary(ctx)
	if err != nil {
		t.Fatalf("RefreshLibrary: %v", err)
	}
	if res.Movies != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want exactly the one monitored, matched movie", res)
	}
}

// One title the provider cannot answer for is a warning, not the end of the
// sweep: the titles after it still refresh.
func TestRefreshLibraryContinuesPastProviderFailures(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.provider.movieByID[1] = core.MovieMeta{TMDBID: 1, Title: "Answered", Year: 2008}
	h.provider.movieByID[2] = core.MovieMeta{TMDBID: 2, Title: "Missing", Year: 2008}

	if _, err := h.mgr.AddMovie(ctx, 2, "", nil); err != nil {
		t.Fatalf("AddMovie: %v", err)
	}
	answered, err := h.mgr.AddMovie(ctx, 1, "", nil)
	if err != nil {
		t.Fatalf("AddMovie: %v", err)
	}

	// The provider forgets movie 2; movie 1 gains an overview.
	delete(h.provider.movieByID, 2)
	h.provider.movieByID[1] = core.MovieMeta{TMDBID: 1, Title: "Answered", Year: 2008, Overview: "Updated."}

	res, err := h.mgr.RefreshLibrary(ctx)
	if err != nil {
		t.Fatalf("RefreshLibrary: %v", err)
	}
	if res.Movies != 1 || len(res.Errors) != 1 {
		t.Fatalf("result = %+v, want one refreshed and one recorded failure", res)
	}

	got, err := h.st.GetMovie(ctx, answered.ID)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if got.Overview != "Updated." {
		t.Errorf("Overview = %q, want the refresh applied past the failure", got.Overview)
	}
}

// A refresh is how a new season reaches the library before any file does, and
// it must arrive without undoing the user's season selections.
func TestRefreshLibraryImportsNewSeasonsAndKeepsSelections(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.provider.seriesByID[1396] = core.SeriesMeta{
		TMDBID: 1396, Title: "Breaking Bad", Year: 2008, Status: "Continuing",
		Seasons: []core.SeasonMeta{{
			Number: 1, Title: "Season 1",
			Episodes: []core.EpisodeMeta{{Season: 1, Number: 1, Title: "Pilot"}},
		}},
	}

	sr, err := h.mgr.AddSeries(ctx, 1396, nil)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	// The user turns season 1 off.
	seasons, err := h.st.ListSeasons(ctx, sr.ID)
	if err != nil || len(seasons) != 1 {
		t.Fatalf("ListSeasons = %v, %v; want one season", seasons, err)
	}
	seasons[0].Monitored = false
	if err := h.st.UpsertSeason(ctx, &seasons[0]); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}

	// The provider announces season 2 and ends the show.
	h.provider.seriesByID[1396] = core.SeriesMeta{
		TMDBID: 1396, Title: "Breaking Bad", Year: 2008, Status: "Ended",
		Seasons: []core.SeasonMeta{
			{Number: 1, Title: "Season 1",
				Episodes: []core.EpisodeMeta{{Season: 1, Number: 1, Title: "Pilot"}}},
			{Number: 2, Title: "Season 2",
				Episodes: []core.EpisodeMeta{{Season: 2, Number: 1, Title: "Seven Thirty-Seven"}}},
		},
	}

	res, err := h.mgr.RefreshLibrary(ctx)
	if err != nil {
		t.Fatalf("RefreshLibrary: %v", err)
	}
	if res.Series != 1 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v, want the one series refreshed", res)
	}

	got, err := h.st.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.Status != "Ended" {
		t.Errorf("Status = %q, want the refreshed status", got.Status)
	}
	seasons, err = h.st.ListSeasons(ctx, sr.ID)
	if err != nil || len(seasons) != 2 {
		t.Fatalf("ListSeasons = %v, %v; want seasons 1 and 2", seasons, err)
	}
	for _, se := range seasons {
		switch se.Number {
		case 1:
			if se.Monitored {
				t.Error("season 1 became monitored again; the user turned it off")
			}
		case 2:
			if !se.Monitored {
				t.Error("season 2 arrived unmonitored; a new season is wanted by default")
			}
		}
	}
	episodes, err := h.st.ListEpisodes(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(episodes) != 2 {
		t.Fatalf("episodes = %d, want season 2's episode imported alongside season 1's", len(episodes))
	}
}
