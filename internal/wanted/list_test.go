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

// The wanted list is what the backlog sweep, the RSS matcher and the wanted
// screen all read, so an item that never enters it cannot leak out of any of
// them. That is where a dormant library's items have to be dropped — and it
// applies to movies as well as episodes, which is new: the switch this
// generalizes could only ever be off for adult series.
func TestComputeDropsAnInactiveLibrarysItems(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	// Every item names its shelf, which is what the library switch is read
	// through: a row that named none would belong nowhere and take part in
	// nothing, so the fixture files both rows the way a real add does.
	tvLib, err := st.GetLibraryByKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetLibraryByKind(tv): %v", err)
	}
	movieLib, err := st.GetLibraryByKind(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("GetLibraryByKind(movie): %v", err)
	}

	series := &core.Series{TMDBID: 42, Title: "Example Series", SortTitle: "example series",
		Monitored: true, LibraryID: tvLib.ID}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("upsert series: %v", err)
	}
	episode := &core.Episode{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Pilot", Monitored: true}
	if err := st.UpsertEpisode(ctx, episode); err != nil {
		t.Fatalf("upsert episode: %v", err)
	}
	movie := &core.Movie{TMDBID: 603, Title: "The Matrix", SortTitle: "matrix", Year: 1999,
		Monitored: true, LibraryID: movieLib.ID}
	if err := st.UpsertMovie(ctx, movie); err != nil {
		t.Fatalf("upsert movie: %v", err)
	}

	lists, err := Compute(ctx, st)
	if err != nil {
		t.Fatalf("compute wanted: %v", err)
	}
	if len(lists.Movies) != 1 || len(lists.Episodes) != 1 {
		t.Fatalf("wanted with both libraries on = %d movies, %d episodes, want one each",
			len(lists.Movies), len(lists.Episodes))
	}

	tv := tvLib
	if err := st.SetLibraryActive(ctx, tv.ID, false); err != nil {
		t.Fatalf("SetLibraryActive(tv, false): %v", err)
	}
	lists, err = Compute(ctx, st)
	if err != nil {
		t.Fatalf("compute wanted: %v", err)
	}
	if len(lists.Episodes) != 0 {
		t.Errorf("wanted still carries %d episodes from an inactive library", len(lists.Episodes))
	}
	if len(lists.Movies) != 1 {
		t.Errorf("switching the tv library off dropped %d movies too", 1-len(lists.Movies))
	}

	movies := movieLib
	if err := st.SetLibraryActive(ctx, movies.ID, false); err != nil {
		t.Fatalf("SetLibraryActive(movie, false): %v", err)
	}
	lists, err = Compute(ctx, st)
	if err != nil {
		t.Fatalf("compute wanted: %v", err)
	}
	if len(lists.Movies) != 0 {
		t.Errorf("wanted still carries %d movies from an inactive library", len(lists.Movies))
	}

	// Nothing was deleted: switching both back on restores the whole list.
	if err := st.SetLibraryActive(ctx, tv.ID, true); err != nil {
		t.Fatalf("SetLibraryActive(tv, true): %v", err)
	}
	if err := st.SetLibraryActive(ctx, movies.ID, true); err != nil {
		t.Fatalf("SetLibraryActive(movie, true): %v", err)
	}
	lists, err = Compute(ctx, st)
	if err != nil {
		t.Fatalf("compute wanted: %v", err)
	}
	if len(lists.Movies) != 1 || len(lists.Episodes) != 1 {
		t.Errorf("reactivating restored %d movies and %d episodes, want one each",
			len(lists.Movies), len(lists.Episodes))
	}
}

// An anime episode reaches the wanted list on the same terms a television one
// does. Nothing here knows the anime kind exists: the rows are gated per row
// through core.LibraryKindForSeries, so the mapping is what makes an anime shelf
// searchable at all — and it is what makes switching that shelf off stop the
// searching, which is the other half of the same promise.
func TestComputeCarriesAnimeEpisodes(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	anime, err := st.GetLibraryByKind(ctx, core.LibraryKindAnime)
	if err != nil {
		t.Fatalf("GetLibraryByKind(anime): %v", err)
	}
	if err := st.SetLibraryActive(ctx, anime.ID, true); err != nil {
		t.Fatalf("SetLibraryActive(anime, true): %v", err)
	}

	series := &core.Series{Kind: core.SeriesKindAnime, TMDBID: 209867, Title: "Frieren",
		SortTitle: "frieren", Monitored: true, LibraryID: anime.ID}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("upsert series: %v", err)
	}
	episode := &core.Episode{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 1,
		Title: "The Journey's End", Monitored: true}
	if err := st.UpsertEpisode(ctx, episode); err != nil {
		t.Fatalf("upsert episode: %v", err)
	}

	lists, err := Compute(ctx, st)
	if err != nil {
		t.Fatalf("compute wanted: %v", err)
	}
	if len(lists.Episodes) != 1 || lists.Episodes[0].Title != "The Journey's End" {
		t.Fatalf("wanted episodes = %+v, want the missing anime episode", lists.Episodes)
	}

	if err := st.SetLibraryActive(ctx, anime.ID, false); err != nil {
		t.Fatalf("SetLibraryActive(anime, false): %v", err)
	}
	lists, err = Compute(ctx, st)
	if err != nil {
		t.Fatalf("compute wanted: %v", err)
	}
	if len(lists.Episodes) != 0 {
		t.Errorf("wanted still carries %d episodes from a dormant anime shelf", len(lists.Episodes))
	}
}
