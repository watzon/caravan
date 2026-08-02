package store

import (
	"context"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// A multi-episode file has to come back once per episode it covers: the DLNA
// browse counts these rows as "playable things under this season", and
// collapsing them would hide S01E02 from a client entirely (SPEC §7).
func TestListEpisodeMediaFilesForSeries(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	series := &core.Series{TMDBID: 1, Title: "Show", SortTitle: "show"}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	other := &core.Series{TMDBID: 2, Title: "Other", SortTitle: "other"}
	if err := st.UpsertSeries(ctx, other); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}

	episodes := []*core.Episode{
		{SeriesID: series.ID, SeasonNumber: 2, EpisodeNumber: 1},
		{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 1},
		{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 2},
		// An episode with no file at all.
		{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 3},
		{SeriesID: other.ID, SeasonNumber: 1, EpisodeNumber: 1},
	}
	for _, e := range episodes {
		if err := st.UpsertEpisode(ctx, e); err != nil {
			t.Fatalf("UpsertEpisode: %v", err)
		}
	}

	double := &core.MediaFile{Path: "TV/Show/S01E01E02.mkv", Size: 10}
	single := &core.MediaFile{Path: "TV/Show/S02E01.mkv", Size: 20}
	foreign := &core.MediaFile{Path: "TV/Other/S01E01.mkv", Size: 30}
	for _, f := range []*core.MediaFile{double, single, foreign} {
		if err := st.UpsertMediaFile(ctx, f); err != nil {
			t.Fatalf("UpsertMediaFile: %v", err)
		}
	}
	for _, link := range []struct{ episode, file int64 }{
		{episodes[0].ID, single.ID},
		{episodes[1].ID, double.ID},
		{episodes[2].ID, double.ID},
		{episodes[4].ID, foreign.ID},
	} {
		if err := st.LinkEpisodeFile(ctx, link.episode, link.file); err != nil {
			t.Fatalf("LinkEpisodeFile: %v", err)
		}
	}

	got, err := st.ListEpisodeMediaFilesForSeries(ctx, series.ID)
	if err != nil {
		t.Fatalf("ListEpisodeMediaFilesForSeries: %v", err)
	}
	// Ordered by season then episode, so a browse renders in the order a
	// viewer expects without sorting afterwards. The other series' file is
	// absent.
	want := []struct {
		season, episode int
		path            string
	}{
		{1, 1, "TV/Show/S01E01E02.mkv"},
		{1, 2, "TV/Show/S01E01E02.mkv"},
		{2, 1, "TV/Show/S02E01.mkv"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d pairs, want %d: %+v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i].SeasonNumber != w.season || got[i].EpisodeNumber != w.episode {
			t.Fatalf("pair %d = S%02dE%02d, want S%02dE%02d",
				i, got[i].SeasonNumber, got[i].EpisodeNumber, w.season, w.episode)
		}
		if got[i].File.Path != w.path {
			t.Fatalf("pair %d path = %q, want %q", i, got[i].File.Path, w.path)
		}
		if got[i].File.ID == 0 || got[i].File.Size == 0 {
			t.Fatalf("pair %d lost the file row: %+v", i, got[i].File)
		}
		if got[i].EpisodeID == 0 {
			t.Fatalf("pair %d lost the episode id", i)
		}
	}

	// A series with no files at all is an empty slice, never nil.
	empty, err := st.ListEpisodeMediaFilesForSeries(ctx, 9999)
	if err != nil {
		t.Fatalf("ListEpisodeMediaFilesForSeries(absent): %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("absent series = %+v, want an empty slice", empty)
	}
}
