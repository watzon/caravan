package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// The wanted list is what the interactive picker is opened from. A scene that
// arrives without series_kind is labelled and linked as television, and the
// picker then writes title/season/episode into the box until the real search
// lands. The field is the one fact that keeps those two spellings apart.
func TestWantedListCarriesSeriesKind(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	enableAdultLibrary(t, st)

	show := &core.Series{TMDBID: 9, Title: "Andor", Monitored: true}
	if err := st.UpsertSeries(ctx, show); err != nil {
		t.Fatalf("UpsertSeries(show): %v", err)
	}
	episode := &core.Episode{
		SeriesID: show.ID, SeasonNumber: 1, EpisodeNumber: 1,
		Title: "Kassa", Monitored: true, AirDate: pastDate(),
	}
	if err := st.UpsertEpisode(ctx, episode); err != nil {
		t.Fatalf("UpsertEpisode(show): %v", err)
	}

	site := &core.Series{
		StashID: "site-transfixed", Title: "Transfixed", SortTitle: "transfixed",
		Kind: core.SeriesKindAdult, Monitored: true,
	}
	if err := st.UpsertSeries(ctx, site); err != nil {
		t.Fatalf("UpsertSeries(site): %v", err)
	}
	scene := &core.Episode{
		SeriesID: site.ID, SeasonNumber: 2026, EpisodeNumber: 24,
		Title: "A Lesson", Monitored: true, AirDate: pastDate(),
	}
	if err := st.UpsertEpisode(ctx, scene); err != nil {
		t.Fatalf("UpsertEpisode(site): %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/wanted", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Episodes []wantedEpisodeJSON `json:"episodes"`
	}
	decodeBody(t, rec, &body)

	got := map[string]string{}
	for _, row := range body.Episodes {
		got[row.Title] = row.SeriesKind
	}
	if got["Kassa"] != core.SeriesKindTV {
		t.Fatalf("Kassa series_kind = %q, want %q", got["Kassa"], core.SeriesKindTV)
	}
	if got["A Lesson"] != core.SeriesKindAdult {
		t.Fatalf("A Lesson series_kind = %q, want %q", got["A Lesson"], core.SeriesKindAdult)
	}
}
