package store

import (
	"context"
	"errors"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func seedMediaFile(t *testing.T, st *Store, path string) *core.MediaFile {
	t.Helper()
	f := core.MediaFile{Path: path, Size: 100, Quality: core.Quality2160p, Codec: "x265", Audio: "DTS"}
	if err := st.UpsertMediaFile(context.Background(), &f); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	return &f
}

func TestConversionLifecycle(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)
	file := seedMediaFile(t, st, "library/Movies/A (2001)/A (2001).mkv")

	c := core.Conversion{MediaFileID: file.ID, SourcePath: file.Path, ProfileID: core.TVProfileSafe}
	if err := st.CreateConversion(ctx, &c); err != nil {
		t.Fatalf("CreateConversion: %v", err)
	}
	if c.ID == 0 {
		t.Fatal("CreateConversion did not write back an ID")
	}
	if c.Status != core.ConversionQueued {
		t.Fatalf("Status = %q, want %q", c.Status, core.ConversionQueued)
	}

	open, err := st.OpenConversionForFile(ctx, file.ID)
	if err != nil {
		t.Fatalf("OpenConversionForFile: %v", err)
	}
	if open.ID != c.ID {
		t.Fatalf("OpenConversionForFile = %d, want %d", open.ID, c.ID)
	}

	c.Status = core.ConversionDone
	c.Strategy = core.ConvertStrategyRemux
	c.OutputPath = "library/Movies/A (2001)/A (2001).mp4"
	if err := st.UpdateConversion(ctx, &c); err != nil {
		t.Fatalf("UpdateConversion: %v", err)
	}

	got, err := st.GetConversion(ctx, c.ID)
	if err != nil {
		t.Fatalf("GetConversion: %v", err)
	}
	if got.Status != core.ConversionDone || got.Strategy != core.ConvertStrategyRemux ||
		got.OutputPath != c.OutputPath || got.ProfileID != core.TVProfileSafe {
		t.Fatalf("GetConversion = %+v", *got)
	}

	// A finished conversion is no longer the queue's business, so the file is
	// free to be queued again.
	if _, err := st.OpenConversionForFile(ctx, file.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("OpenConversionForFile after done = %v, want ErrNotFound", err)
	}
}

// TestTransitionConversionIsConditional is what keeps the cancel button honest.
//
// Cancel runs on an HTTP goroutine and the conversion runs on a worker, and both
// write the same row. With unconditional writes the user can be told
// "cancelled", 200, while the worker goes on to replace the file: each side has
// to claim the row it read, so exactly one of them wins.
func TestTransitionConversionIsConditional(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)
	file := seedMediaFile(t, st, "library/Movies/C (2003)/C (2003).mkv")

	c := core.Conversion{MediaFileID: file.ID, SourcePath: file.Path}
	if err := st.CreateConversion(ctx, &c); err != nil {
		t.Fatalf("CreateConversion: %v", err)
	}

	// The worker claims it first.
	claimed, err := st.TransitionConversion(ctx, c.ID, core.ConversionRunning,
		core.ConversionQueued, core.ConversionRunning)
	if err != nil {
		t.Fatalf("TransitionConversion: %v", err)
	}
	if !claimed {
		t.Fatal("a queued conversion must be claimable")
	}
	// So the cancel that was already in flight loses, instead of reporting a
	// cancellation that never happens.
	cancelled, err := st.TransitionConversion(ctx, c.ID, core.ConversionCancelled, core.ConversionQueued)
	if err != nil {
		t.Fatalf("TransitionConversion: %v", err)
	}
	if cancelled {
		t.Fatal("a running conversion was cancelled out from under the worker")
	}
	if got, err := st.GetConversion(ctx, c.ID); err != nil || got.Status != core.ConversionRunning {
		t.Fatalf("status = %+v, %v; want running", got, err)
	}

	// The other order: a cancel that lands first must not be claimable, so the
	// worker cannot resurrect it and rewrite the file.
	other := core.Conversion{MediaFileID: file.ID, SourcePath: file.Path}
	c.Status = core.ConversionDone
	if err := st.UpdateConversion(ctx, &c); err != nil {
		t.Fatalf("UpdateConversion: %v", err)
	}
	if err := st.CreateConversion(ctx, &other); err != nil {
		t.Fatalf("CreateConversion: %v", err)
	}
	if ok, err := st.TransitionConversion(ctx, other.ID, core.ConversionCancelled, core.ConversionQueued); err != nil || !ok {
		t.Fatalf("cancel of a queued conversion = %v, %v; want true", ok, err)
	}
	ok, err := st.TransitionConversion(ctx, other.ID, core.ConversionRunning,
		core.ConversionQueued, core.ConversionRunning)
	if err != nil {
		t.Fatalf("TransitionConversion: %v", err)
	}
	if ok {
		t.Fatal("a cancelled conversion was claimed by the worker")
	}
}

// TestOneOpenConversionPerFile is the constraint that stops a double-click on
// Convert from starting two ffmpeg runs over the same output.
func TestOneOpenConversionPerFile(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)
	file := seedMediaFile(t, st, "library/Movies/B (2002)/B (2002).mkv")

	first := core.Conversion{MediaFileID: file.ID, SourcePath: file.Path}
	if err := st.CreateConversion(ctx, &first); err != nil {
		t.Fatalf("CreateConversion: %v", err)
	}
	second := core.Conversion{MediaFileID: file.ID, SourcePath: file.Path}
	if err := st.CreateConversion(ctx, &second); !errors.Is(err, ErrConversionOpen) {
		t.Fatalf("second CreateConversion = %v, want ErrConversionOpen", err)
	}

	// Running still counts as open.
	first.Status = core.ConversionRunning
	if err := st.UpdateConversion(ctx, &first); err != nil {
		t.Fatalf("UpdateConversion: %v", err)
	}
	third := core.Conversion{MediaFileID: file.ID, SourcePath: file.Path}
	if err := st.CreateConversion(ctx, &third); !errors.Is(err, ErrConversionOpen) {
		t.Fatalf("CreateConversion while running = %v, want ErrConversionOpen", err)
	}

	// Once it fails, a retry may take its place.
	first.Status = core.ConversionFailed
	if err := st.UpdateConversion(ctx, &first); err != nil {
		t.Fatalf("UpdateConversion: %v", err)
	}
	retry := core.Conversion{MediaFileID: file.ID, SourcePath: file.Path}
	if err := st.CreateConversion(ctx, &retry); err != nil {
		t.Fatalf("CreateConversion after failure: %v", err)
	}
}

func TestListConversionsNewestFirst(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	if rows, err := st.ListConversions(ctx, 0); err != nil || len(rows) != 0 {
		t.Fatalf("ListConversions on a fresh database = %v, %v; want an empty slice", rows, err)
	}

	for i := range 3 {
		file := seedMediaFile(t, st, "library/Movies/C/"+string(rune('a'+i))+".mkv")
		c := core.Conversion{MediaFileID: file.ID, SourcePath: file.Path}
		if err := st.CreateConversion(ctx, &c); err != nil {
			t.Fatalf("CreateConversion: %v", err)
		}
	}

	rows, err := st.ListConversions(ctx, 0)
	if err != nil {
		t.Fatalf("ListConversions: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("ListConversions returned %d rows, want 3", len(rows))
	}
	if rows[0].ID < rows[2].ID {
		t.Fatalf("ListConversions is not newest-first: %d then %d", rows[0].ID, rows[2].ID)
	}

	limited, err := st.ListConversions(ctx, 2)
	if err != nil {
		t.Fatalf("ListConversions: %v", err)
	}
	if len(limited) != 2 || limited[0].ID != rows[0].ID {
		t.Fatalf("limited list = %+v", limited)
	}
}

func TestUpdateMediaFileConvertedKeepsTheRowIdentity(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	series := core.Series{TMDBID: 1, Title: "Show"}
	if err := st.UpsertSeries(ctx, &series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	episode := core.Episode{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot"}
	if err := st.UpsertEpisode(ctx, &episode); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	file := seedMediaFile(t, st, "library/TV/Show/Season 01/Show - S01E01.mkv")
	if err := st.LinkEpisodeFile(ctx, episode.ID, file.ID); err != nil {
		t.Fatalf("LinkEpisodeFile: %v", err)
	}

	const newPath = "library/TV/Show/Season 01/Show - S01E01.mp4"
	if err := st.UpdateMediaFileConverted(ctx, file.ID, newPath, 4242, core.Quality1080p, "h264", "AAC"); err != nil {
		t.Fatalf("UpdateMediaFileConverted: %v", err)
	}

	got, err := st.GetMediaFile(ctx, file.ID)
	if err != nil {
		t.Fatalf("GetMediaFile: %v", err)
	}
	if got.Path != newPath || got.Size != 4242 || got.Codec != "h264" || got.Audio != "AAC" {
		t.Fatalf("converted file = %+v", *got)
	}
	// A downscale is part of what a conversion does: a row left claiming the
	// source resolution keeps failing the TV compatibility check on a file that
	// now passes it.
	if got.Quality != core.Quality1080p {
		t.Fatalf("quality = %q, want %q after a downscaling conversion", got.Quality, core.Quality1080p)
	}

	// The episode link is the reason the update is in place rather than
	// delete-and-insert: a new row id would silently detach it.
	linked, err := st.ListMediaFilesForEpisode(ctx, episode.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForEpisode: %v", err)
	}
	if len(linked) != 1 || linked[0].ID != file.ID || linked[0].Path != newPath {
		t.Fatalf("episode link after conversion = %+v", linked)
	}
}

func TestGetMediaFileNotFound(t *testing.T) {
	st, _ := openTemp(t)
	if _, err := st.GetMediaFile(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetMediaFile(999) = %v, want ErrNotFound", err)
	}
	if err := st.UpdateMediaFileConverted(context.Background(), 999, "x.mp4", 1, core.Quality1080p, "h264", "AAC"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateMediaFileConverted(999) = %v, want ErrNotFound", err)
	}
}
