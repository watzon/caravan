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
		LibraryID: defaultLibraryID(t, st, core.LibraryKindAdult),
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

// The wanted screen is a combined list. A library filter only works if every
// row names the shelf it belongs to, otherwise checking "Movies" cannot hide a
// television hole, and two movie libraries collapse into one pile.
func TestWantedListCarriesLibraryID(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	enableAdultLibrary(t, st)

	movieLib := defaultLibraryID(t, st, core.LibraryKindMovie)
	tvLib := defaultLibraryID(t, st, core.LibraryKindTV)
	adultLib := defaultLibraryID(t, st, core.LibraryKindAdult)

	movie := &core.Movie{Title: "Arrival", Year: 2016, Monitored: true, LibraryID: movieLib}
	if err := st.UpsertMovie(ctx, movie); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	show := &core.Series{TMDBID: 9, Title: "Andor", Monitored: true, LibraryID: tvLib}
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
		Kind: core.SeriesKindAdult, Monitored: true, LibraryID: adultLib,
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
		Movies   []wantedMovieJSON   `json:"movies"`
		Episodes []wantedEpisodeJSON `json:"episodes"`
	}
	decodeBody(t, rec, &body)

	if len(body.Movies) != 1 || body.Movies[0].Title != "Arrival" {
		t.Fatalf("movies = %+v, want Arrival", body.Movies)
	}
	if body.Movies[0].LibraryID != movieLib {
		t.Fatalf("Arrival library_id = %d, want %d", body.Movies[0].LibraryID, movieLib)
	}

	got := map[string]int64{}
	for _, row := range body.Episodes {
		got[row.Title] = row.LibraryID
	}
	if got["Kassa"] != tvLib {
		t.Fatalf("Kassa library_id = %d, want %d", got["Kassa"], tvLib)
	}
	if got["A Lesson"] != adultLib {
		t.Fatalf("A Lesson library_id = %d, want %d", got["A Lesson"], adultLib)
	}
}
