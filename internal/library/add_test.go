package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

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

	mv, err := h.mgr.AddMovie(ctx, 10378)
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

	first, err := h.mgr.AddMovie(ctx, 10378)
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
	second, err := h.mgr.AddMovie(ctx, 10378)
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

	sr, err := h.mgr.AddSeries(ctx, 66732)
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

func TestAddWithoutProviderIsRecognizable(t *testing.T) {
	h := newHarness(t)
	// SPEC §13: no TMDB key degrades visibly. The API turns this sentinel into
	// a 503 that tells the user to add a key, so it must survive the call.
	mgr := h.newManager(h.st, nil)

	if _, err := mgr.AddMovie(context.Background(), 1); !errors.Is(err, core.ErrNoMetadataProvider) {
		t.Fatalf("AddMovie error = %v, want core.ErrNoMetadataProvider", err)
	}
	if _, err := mgr.AddSeries(context.Background(), 1); !errors.Is(err, core.ErrNoMetadataProvider) {
		t.Fatalf("AddSeries error = %v, want core.ErrNoMetadataProvider", err)
	}
}

func TestAddRejectsInvalidProviderID(t *testing.T) {
	h := newHarness(t)

	if _, err := h.mgr.AddMovie(context.Background(), 0); err == nil {
		t.Fatal("AddMovie(0) succeeded, want an error")
	}
	if _, err := h.mgr.AddSeries(context.Background(), -1); err == nil {
		t.Fatal("AddSeries(-1) succeeded, want an error")
	}
}
