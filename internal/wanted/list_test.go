package wanted

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

func TestComputeSkipsKnownFutureEpisodesAndKeepsUnknownDates(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	series := &core.Series{TMDBID: 101, Title: "Calendar Show", SortTitle: "calendar show", Monitored: true}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("upsert series: %v", err)
	}

	now := time.Now().UTC()
	episodes := []*core.Episode{
		{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Past", AirDate: now.AddDate(0, 0, -1), Monitored: true},
		{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 2, Title: "Unknown", Monitored: true},
		{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 3, Title: "Future", AirDate: now.AddDate(0, 0, 1), Monitored: true},
	}
	for _, episode := range episodes {
		if err := st.UpsertEpisode(ctx, episode); err != nil {
			t.Fatalf("upsert episode %q: %v", episode.Title, err)
		}
	}

	lists, err := Compute(ctx, st)
	if err != nil {
		t.Fatalf("compute wanted: %v", err)
	}
	got := make([]string, 0, len(lists.Episodes))
	for _, episode := range lists.Episodes {
		got = append(got, episode.Title)
	}
	if len(got) != 2 || got[0] != "Past" || got[1] != "Unknown" {
		t.Fatalf("wanted episodes = %v, want [Past Unknown]", got)
	}
}
