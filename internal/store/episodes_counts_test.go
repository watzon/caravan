package store

import (
	"context"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// TestEpisodeCountsBySeries pins the tally the library list renders as
// "owned / total".
//
// The multi-episode case is the reason this is one GROUP BY rather than a
// join: a single S01E01E02 file links to two episode rows, and a naive
// LEFT JOIN COUNT(*) would report three episodes for a two-episode season.
func TestEpisodeCountsBySeries(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	newSeries := func(title string) *core.Series {
		t.Helper()
		sr := &core.Series{Title: title}
		if err := st.UpsertSeries(ctx, sr); err != nil {
			t.Fatalf("UpsertSeries: %v", err)
		}
		return sr
	}
	newEpisode := func(seriesID int64, season, number int) *core.Episode {
		t.Helper()
		e := &core.Episode{SeriesID: seriesID, SeasonNumber: season, EpisodeNumber: number}
		if err := st.UpsertEpisode(ctx, e); err != nil {
			t.Fatalf("UpsertEpisode: %v", err)
		}
		return e
	}
	linkFile := func(path string, episodeIDs ...int64) {
		t.Helper()
		f := &core.MediaFile{Path: path}
		if err := st.UpsertMediaFile(ctx, f); err != nil {
			t.Fatalf("UpsertMediaFile: %v", err)
		}
		for _, id := range episodeIDs {
			if err := st.LinkEpisodeFile(ctx, id, f.ID); err != nil {
				t.Fatalf("LinkEpisodeFile: %v", err)
			}
		}
	}

	// One file covering two episodes.
	multi := newSeries("Multi")
	m1 := newEpisode(multi.ID, 1, 1)
	m2 := newEpisode(multi.ID, 1, 2)
	newEpisode(multi.ID, 1, 3)
	linkFile("TV/Multi/Season 01/S01E01-E02.mkv", m1.ID, m2.ID)

	// One episode with two files (an upgrade mid-flight) counts once.
	dupe := newSeries("Dupe")
	d1 := newEpisode(dupe.ID, 1, 1)
	linkFile("TV/Dupe/Season 01/S01E01.mkv", d1.ID)
	linkFile("TV/Dupe/Season 01/S01E01 (2160p).mkv", d1.ID)

	// A series with episodes but no files at all.
	empty := newSeries("Empty")
	newEpisode(empty.ID, 1, 1)

	// A series with no episode rows is absent from the map.
	bare := newSeries("Bare")

	counts, err := st.EpisodeCountsBySeries(ctx)
	if err != nil {
		t.Fatalf("EpisodeCountsBySeries: %v", err)
	}

	tests := []struct {
		name     string
		seriesID int64
		want     EpisodeCounts
	}{
		{"multi-episode file counts each episode once", multi.ID, EpisodeCounts{Total: 3, WithFile: 2}},
		{"several files on one episode count once", dupe.ID, EpisodeCounts{Total: 1, WithFile: 1}},
		{"episodes with no files", empty.ID, EpisodeCounts{Total: 1, WithFile: 0}},
		{"series with no episodes", bare.ID, EpisodeCounts{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := counts[tt.seriesID]; got != tt.want {
				t.Fatalf("counts = %+v, want %+v", got, tt.want)
			}
		})
	}
}
