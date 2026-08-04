package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

func TestAddMovieCreatesTheRowWithoutTouchingDisk(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.provider.movieByID[10378] = core.MovieMeta{
		TMDBID:    10378,
		Title:     "Big Buck Bunny",
		Year:      2008,
		Overview:  "A rabbit.",
		PosterURL: h.posterURL,
	}

	mv, err := h.mgr.AddMovie(ctx, 10378, "", nil)
	if err != nil {
		t.Fatalf("AddMovie: %v", err)
	}
	if mv.ID == 0 || mv.TMDBID != 10378 || mv.Title != "Big Buck Bunny" {
		t.Fatalf("movie = %+v, want the provider's metadata with an id", mv)
	}
	if !mv.Monitored {
		t.Fatal("a newly added movie is not monitored, want monitored")
	}
	// The folder is recorded so a later import knows where the file goes...
	if want := "library/Movies/Big Buck Bunny (2008)"; mv.Path != want {
		t.Fatalf("path = %q, want %q", mv.Path, want)
	}
	// ...but nothing is written yet: an empty folder would be a library item
	// with no media in it.
	if _, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(mv.Path))); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stat %q = %v, want the folder not to exist", mv.Path, err)
	}
	if h.posterHits != 0 {
		t.Fatalf("poster fetches = %d, want 0 — there is no folder to write into", h.posterHits)
	}

	stored, err := h.st.GetMovieByTMDBID(ctx, 10378)
	if err != nil {
		t.Fatalf("GetMovieByTMDBID: %v", err)
	}
	if stored.ID != mv.ID {
		t.Fatalf("stored id = %d, want %d", stored.ID, mv.ID)
	}
	// No local poster can exist yet, so the provider URL is the only artwork
	// the UI has — losing it is a poster-less library entry until import.
	if stored.PosterPath != "" {
		t.Fatalf("poster path = %q, want empty before anything is on disk", stored.PosterPath)
	}
	if stored.PosterURL != h.posterURL {
		t.Fatalf("poster url = %q, want the provider's %q", stored.PosterURL, h.posterURL)
	}
}

func TestAddMovieAgainKeepsUserIntent(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.provider.movieByID[10378] = core.MovieMeta{TMDBID: 10378, Title: "Big Buck Bunny", Year: 2008}

	first, err := h.mgr.AddMovie(ctx, 10378, "", nil)
	if err != nil {
		t.Fatalf("AddMovie: %v", err)
	}
	first.Monitored = false
	first.QualityProfileID = 3
	if err := h.st.UpsertMovie(ctx, first); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	// Re-adding refreshes provider metadata; it must not undo the user's
	// choices, exactly as a rescan does not.
	h.provider.movieByID[10378] = core.MovieMeta{TMDBID: 10378, Title: "Big Buck Bunny", Year: 2008, Overview: "Updated."}
	second, err := h.mgr.AddMovie(ctx, 10378, "", nil)
	if err != nil {
		t.Fatalf("AddMovie again: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("id = %d, want the existing row %d", second.ID, first.ID)
	}
	if second.Overview != "Updated." {
		t.Fatalf("overview = %q, want the refreshed metadata", second.Overview)
	}
	if second.Monitored || second.QualityProfileID != 3 {
		t.Fatalf("movie = %+v, want the user's monitored flag and profile preserved", second)
	}
}

func TestAddSeriesWritesTheWholeTree(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.provider.seriesByID[66732] = core.SeriesMeta{
		TMDBID: 66732,
		Title:  "Stranger Things",
		Year:   2016,
		Seasons: []core.SeasonMeta{{
			Number: 1,
			Title:  "Season 1",
			Episodes: []core.EpisodeMeta{
				{Season: 1, Number: 1, Title: "Chapter One"},
				{Season: 1, Number: 2, Title: "Chapter Two"},
			},
		}},
	}

	sr, err := h.mgr.AddSeries(ctx, 66732, nil)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if want := "library/TV/Stranger Things (2016)"; sr.Path != want {
		t.Fatalf("path = %q, want %q", sr.Path, want)
	}

	// The episodes that are *not* on disk are the point: a wanted list needs
	// to know what is missing.
	episodes, err := h.st.ListEpisodes(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(episodes) != 2 {
		t.Fatalf("episodes = %+v, want 2", episodes)
	}
	seasons, err := h.st.ListSeasons(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListSeasons: %v", err)
	}
	if len(seasons) != 1 || seasons[0].Number != 1 {
		t.Fatalf("seasons = %+v, want season 1", seasons)
	}

	files, err := h.st.ListMediaFiles(ctx)
	if err != nil {
		t.Fatalf("ListMediaFiles: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("media files = %+v, want none — nothing was imported", files)
	}
}

func TestAddSeriesLeavesSpecialsUnmonitored(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	// Anime commonly carries a TMDB "Specials" season full of promo shorts;
	// automation must not hunt for those unless the user opts in.
	meta := core.SeriesMeta{
		TMDBID: 312949,
		Title:  "Chainsmoker Cat",
		Year:   2026,
		Seasons: []core.SeasonMeta{
			{
				Number: 0,
				Title:  "Specials",
				Episodes: []core.EpisodeMeta{
					{Season: 0, Number: 1, Title: "Episode 1"},
					{Season: 0, Number: 2, Title: "Episode 2"},
				},
			},
			{
				Number: 1,
				Title:  "Season 1",
				Episodes: []core.EpisodeMeta{
					{Season: 1, Number: 1, Title: "I'm Yani Neko, Nya"},
				},
			},
		},
	}
	h.provider.seriesByID[meta.TMDBID] = meta

	sr, err := h.mgr.AddSeries(ctx, meta.TMDBID, nil)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	seasons, err := h.st.ListSeasons(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListSeasons: %v", err)
	}
	if len(seasons) != 2 || seasons[0].Number != 0 || seasons[1].Number != 1 {
		t.Fatalf("seasons = %+v, want specials then season 1", seasons)
	}
	if seasons[0].Monitored {
		t.Fatal("specials season is monitored on add, want unmonitored by default")
	}
	if !seasons[1].Monitored {
		t.Fatal("season 1 is unmonitored on add, want monitored")
	}

	episodes, err := h.st.ListEpisodes(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	for _, e := range episodes {
		if want := e.SeasonNumber != 0; e.Monitored != want {
			t.Fatalf("S%02dE%02d monitored = %v, want %v", e.SeasonNumber, e.EpisodeNumber, e.Monitored, want)
		}
	}

	// Opting in is user intent, and a metadata refresh must not undo it.
	specials := seasons[0]
	specials.Monitored = true
	if err := h.st.UpsertSeason(ctx, &specials); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	if _, err := h.mgr.AddSeries(ctx, meta.TMDBID, nil); err != nil {
		t.Fatalf("AddSeries again: %v", err)
	}
	seasons, err = h.st.ListSeasons(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListSeasons after refresh: %v", err)
	}
	if !seasons[0].Monitored {
		t.Fatal("refresh reset the specials season to unmonitored, want the user's opt-in preserved")
	}
}

func TestAddWithoutProviderIsRecognizable(t *testing.T) {
	h := newHarness(t)
	// SPEC §13: no TMDB key degrades visibly. The API turns this sentinel into
	// a 503 that tells the user to add a key, so it must survive the call.
	mgr := h.newManager(h.st, nil)

	if _, err := mgr.AddMovie(context.Background(), 1, "", nil); !errors.Is(err, core.ErrNoMetadataProvider) {
		t.Fatalf("AddMovie error = %v, want core.ErrNoMetadataProvider", err)
	}
	if _, err := mgr.AddSeries(context.Background(), 1, nil); !errors.Is(err, core.ErrNoMetadataProvider) {
		t.Fatalf("AddSeries error = %v, want core.ErrNoMetadataProvider", err)
	}
}

func TestAddRejectsInvalidProviderID(t *testing.T) {
	h := newHarness(t)

	if _, err := h.mgr.AddMovie(context.Background(), 0, "", nil); err == nil {
		t.Fatal("AddMovie(0) succeeded, want an error")
	}
	if _, err := h.mgr.AddSeries(context.Background(), -1, nil); err == nil {
		t.Fatal("AddSeries(-1) succeeded, want an error")
	}
}

// The availability choice is user intent like the monitored flag: an add that
// names one sets it, an add that does not leaves the existing choice alone,
// and the provider's home-release dates always refresh alongside.
func TestAddMovieMinAvailability(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	digital := time.Date(2026, 11, 3, 0, 0, 0, 0, time.UTC)
	h.provider.movieByID[10378] = core.MovieMeta{TMDBID: 10378, Title: "Big Buck Bunny", Year: 2008,
		DigitalRelease: digital}

	mv, err := h.mgr.AddMovie(ctx, 10378, core.AvailabilityAnnounced, nil)
	if err != nil {
		t.Fatalf("AddMovie: %v", err)
	}
	if mv.MinAvailability != core.AvailabilityAnnounced {
		t.Fatalf("MinAvailability = %q, want %q", mv.MinAvailability, core.AvailabilityAnnounced)
	}
	if !mv.DigitalRelease.Equal(digital) {
		t.Fatalf("DigitalRelease = %v, want the provider's %v", mv.DigitalRelease, digital)
	}

	// Re-adding with no opinion keeps the choice.
	kept, err := h.mgr.AddMovie(ctx, 10378, "", nil)
	if err != nil {
		t.Fatalf("AddMovie again: %v", err)
	}
	if kept.MinAvailability != core.AvailabilityAnnounced {
		t.Fatalf("MinAvailability after silent re-add = %q, want %q kept", kept.MinAvailability, core.AvailabilityAnnounced)
	}

	// Re-adding with a new choice is fresh user intent.
	changed, err := h.mgr.AddMovie(ctx, 10378, core.AvailabilityInCinemas, nil)
	if err != nil {
		t.Fatalf("AddMovie with a new choice: %v", err)
	}
	if changed.MinAvailability != core.AvailabilityInCinemas {
		t.Fatalf("MinAvailability = %q, want the new choice %q", changed.MinAvailability, core.AvailabilityInCinemas)
	}
}

// A movie that arrives with no stated availability gets the released default —
// the store's job, but the add path is where it matters.
func TestAddMovieDefaultsToReleased(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.provider.movieByID[10378] = core.MovieMeta{TMDBID: 10378, Title: "Big Buck Bunny", Year: 2008}

	mv, err := h.mgr.AddMovie(ctx, 10378, "", nil)
	if err != nil {
		t.Fatalf("AddMovie: %v", err)
	}
	if mv.MinAvailability != core.AvailabilityReleased {
		t.Fatalf("MinAvailability = %q, want the %q default", mv.MinAvailability, core.AvailabilityReleased)
	}
}

// ---- "Add and monitor", off by default in the dialog ------------------------

// ptr is the shortest honest way to write an explicit monitored choice: the
// parameter is a pointer precisely so that absent and false differ.
func ptr[T any](v T) *T { return &v }

// An explicit false lands the row unmonitored; absent still means monitored,
// which is what every caller that predates the checkbox — and request approval
// — relies on.
func TestAddMovieHonoursTheMonitoredChoice(t *testing.T) {
	ctx := context.Background()

	for _, tt := range []struct {
		name      string
		monitored *bool
		want      bool
	}{
		{name: "no opinion is monitored", monitored: nil, want: true},
		{name: "an explicit yes", monitored: ptr(true), want: true},
		{name: "an explicit no", monitored: ptr(false), want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.provider.movieByID[10378] = core.MovieMeta{TMDBID: 10378, Title: "Big Buck Bunny", Year: 2008}
			mv, err := h.mgr.AddMovie(ctx, 10378, "", tt.monitored)
			if err != nil {
				t.Fatalf("AddMovie: %v", err)
			}
			if mv.Monitored != tt.want {
				t.Errorf("Monitored = %v, want %v", mv.Monitored, tt.want)
			}
		})
	}
}

// A re-add is a metadata refresh, so the flag stays the owner's however
// emphatically the request restates its own opinion.
func TestAddMovieLeavesAnExistingRowsMonitoredFlagAlone(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.provider.movieByID[10378] = core.MovieMeta{TMDBID: 10378, Title: "Big Buck Bunny", Year: 2008}

	if _, err := h.mgr.AddMovie(ctx, 10378, "", ptr(true)); err != nil {
		t.Fatalf("AddMovie: %v", err)
	}
	again, err := h.mgr.AddMovie(ctx, 10378, "", ptr(false))
	if err != nil {
		t.Fatalf("AddMovie again: %v", err)
	}
	if !again.Monitored {
		t.Error("a re-add overruled the owner's monitored flag")
	}
}

// The series flag is not enough on its own: the wanted list is computed from
// episodes.monitored, so an unmonitored add has to reach the tree — otherwise
// "add without monitoring" downloads the whole series anyway.
func TestAddSeriesUnmonitoredLeavesTheWholeTreeUnmonitored(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.provider.seriesByID[66732] = core.SeriesMeta{
		TMDBID: 66732, Title: "Stranger Things", Year: 2016,
		Seasons: []core.SeasonMeta{{
			Number: 1, Title: "Season 1",
			Episodes: []core.EpisodeMeta{
				{Season: 1, Number: 1, Title: "Chapter One"},
				{Season: 1, Number: 2, Title: "Chapter Two"},
			},
		}},
	}

	sr, err := h.mgr.AddSeries(ctx, 66732, ptr(false))
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if sr.Monitored {
		t.Fatal("the series row is monitored after an unmonitored add")
	}
	seasons, err := h.st.ListSeasons(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListSeasons: %v", err)
	}
	if len(seasons) == 0 {
		t.Fatal("no season rows: the tree must still land, only unmonitored")
	}
	for _, se := range seasons {
		if se.Monitored {
			t.Errorf("season %d is monitored under an unmonitored series", se.Number)
		}
	}
	episodes, err := h.st.ListEpisodes(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(episodes) != 2 {
		t.Fatalf("episodes = %+v, want the whole tree", episodes)
	}
	for _, e := range episodes {
		if e.Monitored {
			t.Errorf("S%02dE%02d is monitored under an unmonitored series", e.SeasonNumber, e.EpisodeNumber)
		}
	}
}

// The default add is unchanged: monitored series, monitored tree, specials
// still opted out of.
func TestAddSeriesWithNoOpinionStillMonitorsTheTree(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.provider.seriesByID[66732] = core.SeriesMeta{
		TMDBID: 66732, Title: "Stranger Things", Year: 2016,
		Seasons: []core.SeasonMeta{
			{Number: 0, Title: "Specials", Episodes: []core.EpisodeMeta{{Season: 0, Number: 1, Title: "Recap"}}},
			{Number: 1, Title: "Season 1", Episodes: []core.EpisodeMeta{{Season: 1, Number: 1, Title: "Chapter One"}}},
		},
	}

	sr, err := h.mgr.AddSeries(ctx, 66732, nil)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if !sr.Monitored {
		t.Fatal("an add with no opinion left the series unmonitored")
	}
	episodes, err := h.st.ListEpisodes(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	for _, e := range episodes {
		want := e.SeasonNumber != 0
		if e.Monitored != want {
			t.Errorf("S%02dE%02d monitored = %v, want %v", e.SeasonNumber, e.EpisodeNumber, e.Monitored, want)
		}
	}
}
