package library

import (
	"context"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// animeLibrary is the seeded Anime shelf switched on. It is the same row a real
// install flips in Libraries settings, so these tests exercise the shelf people
// will actually have rather than one built for the test.
func animeLibrary(t *testing.T, h *harness) core.Library {
	t.Helper()
	ctx := context.Background()
	lib, err := h.st.GetLibraryByKind(ctx, core.LibraryKindAnime)
	if err != nil {
		t.Fatalf("GetLibraryByKind(anime): %v", err)
	}
	lib.Active = true
	// The seed chains the shelf to AniList; the harness wires one provider and
	// it answers to the TMDB id, so the chain is repointed at it. Which provider
	// identifies an anime is the owner's per-library choice either way — what
	// these tests are about is the shelf, not the catalogue behind it.
	lib.Providers = []string{core.ProviderTMDB}
	if err := h.st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	return *lib
}

// A series added to an anime library is filed as anime. The provider metadata
// cannot say which it is — the same TMDB record is television everywhere else —
// so the LIBRARY decides, and the row has to carry the answer or the two series
// screens could not tell their rows apart.
func TestAddSeriesToAnAnimeLibraryFilesItAsAnime(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedSeries(h)
	anime := animeLibrary(t, h)

	sr, err := h.mgr.AddSeries(ctx, core.TMDBRef(42), nil, anime.ID)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if sr.Kind != core.SeriesKindAnime || sr.LibraryID != anime.ID {
		t.Errorf("added series = {kind %q, library %d}, want anime in %d",
			sr.Kind, sr.LibraryID, anime.ID)
	}
	if sr.Path != "library/Anime/Planet Earth II (2016)" {
		t.Errorf("series path = %q, want it under the anime root", sr.Path)
	}

	// It is on the anime shelf and on no other.
	byKind, err := h.st.ListSeriesByKind(ctx, core.SeriesKindAnime)
	if err != nil {
		t.Fatalf("ListSeriesByKind(anime): %v", err)
	}
	if len(byKind) != 1 || byKind[0].ID != sr.ID {
		t.Errorf("anime series = %+v, want just the added row", byKind)
	}
	tv, err := h.st.ListSeriesByKind(ctx, core.SeriesKindTV)
	if err != nil {
		t.Fatalf("ListSeriesByKind(tv): %v", err)
	}
	if len(tv) != 0 {
		t.Errorf("television series = %+v, want none", tv)
	}
}

// A movie added to an anime library lands there too: it is the one shelf that
// holds both vocabularies. Movies carry no kind of their own, so `library_id`
// is the whole answer.
func TestAddMovieToAnAnimeLibrary(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedMovie(h)
	anime := animeLibrary(t, h)

	mv, err := h.mgr.AddMovie(ctx, core.TMDBRef(10378), "", nil, anime.ID)
	if err != nil {
		t.Fatalf("AddMovie: %v", err)
	}
	if mv.LibraryID != anime.ID {
		t.Errorf("added movie library = %d, want the anime library %d", mv.LibraryID, anime.ID)
	}
	if mv.Path != "library/Anime/Big Buck Bunny (2008)" {
		t.Errorf("movie path = %q, want it under the anime root", mv.Path)
	}
}

// Moving a series between the television and anime shelves rewrites its kind to
// match the destination. Leaving the kind behind would leave a row the store
// refuses to write (store.UpsertSeries insists the two line up) and, if it did
// write, one that both series screens would claim or both drop.
func TestMoveSeriesBetweenTVAndAnimeRewritesTheKind(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedSeries(h)
	anime := animeLibrary(t, h)

	sr, err := h.mgr.AddSeries(ctx, core.TMDBRef(42), nil, 0)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if sr.Kind != core.SeriesKindTV {
		t.Fatalf("added series kind = %q, want television", sr.Kind)
	}
	tvLibrary := sr.LibraryID

	if err := h.mgr.MoveSeries(ctx, sr.ID, anime.ID); err != nil {
		t.Fatalf("MoveSeries(tv -> anime): %v", err)
	}
	moved, err := h.st.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if moved.Kind != core.SeriesKindAnime || moved.LibraryID != anime.ID {
		t.Errorf("after the move = {kind %q, library %d}, want anime in %d",
			moved.Kind, moved.LibraryID, anime.ID)
	}
	if moved.Path != "library/Anime/Planet Earth II (2016)" {
		t.Errorf("moved series path = %q, want it under the anime root", moved.Path)
	}

	// And back, because a shelf you cannot move off is a trapdoor.
	if err := h.mgr.MoveSeries(ctx, sr.ID, tvLibrary); err != nil {
		t.Fatalf("MoveSeries(anime -> tv): %v", err)
	}
	back, err := h.st.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if back.Kind != core.SeriesKindTV || back.LibraryID != tvLibrary {
		t.Errorf("after moving back = {kind %q, library %d}, want television in %d",
			back.Kind, back.LibraryID, tvLibrary)
	}
}

// The adult shelf is not part of the widening in either direction: a site's
// identity model is stash-box's, and a shelf whose promise is absence is not a
// place an ordinary series may drift into.
func TestMoveSeriesStillRefusesTheAdultShelf(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedSeries(h)
	adult, err := h.st.GetLibraryByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind(adult): %v", err)
	}

	sr, err := h.mgr.AddSeries(ctx, core.TMDBRef(42), nil, 0)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}
	if err := h.mgr.MoveSeries(ctx, sr.ID, adult.ID); err != ErrCrossKindMove {
		t.Errorf("MoveSeries(tv -> adult) = %v, want ErrCrossKindMove", err)
	}
}

// A file under an anime root parses as an episode first and as a film second.
// Absolute numbering is how anime releases are named, so a bare number is
// episode 105 and not a film called "Show 105"; a name with no number at all is
// a film, where a television root would have parked it as "no episode number".
func TestScanUnderAnAnimeRootTakesEpisodesFirstAndFilmsSecond(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedMovie(h)
	absoluteShowTree(h, true)
	anime := animeLibrary(t, h)

	film := "library/Anime/Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP.mkv"
	h.parser["Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP.mkv"] = movieParse("Big Buck Bunny", 2008)
	h.writeVideo(film, "film bytes")
	episode := "library/Anime/[Group] Show - 105.mkv"
	h.parser["[Group] Show - 105.mkv"] = absoluteParse("Show", 105)
	h.writeVideo(episode, "episode bytes")

	res := h.scan()
	if res.Added != 2 || res.Unmatched != 0 || len(res.Errors) != 0 {
		t.Fatalf("unexpected scan result: %+v", res)
	}

	mv, err := h.st.GetMovieByTMDBID(ctx, 10378)
	if err != nil {
		t.Fatalf("GetMovieByTMDBID: %v", err)
	}
	if mv.LibraryID != anime.ID {
		t.Errorf("film library = %d, want the anime library %d", mv.LibraryID, anime.ID)
	}
	sr, err := h.st.GetSeriesByTMDBID(ctx, 77)
	if err != nil {
		t.Fatalf("GetSeriesByTMDBID: %v", err)
	}
	if sr.Kind != core.SeriesKindAnime || sr.LibraryID != anime.ID {
		t.Errorf("series = {kind %q, library %d}, want anime in %d",
			sr.Kind, sr.LibraryID, anime.ID)
	}
	// 105 is S05E03 only because seasons 1-4 ran 25, 25, 25 and 27 episodes:
	// the number was looked up, not counted (see absoluteShowTree).
	if !h.exists("library/Anime/Show (2023)/Season 05/Show (2023) - S05E03 - Episode 105.mkv") {
		t.Errorf("the absolute-numbered episode did not organize under season 5")
	}
}

// The refresh sweep covers anime as well as television. Both are refreshed
// through the ordinary metadata seam by the provider each row is pinned to, so
// an anime row's episode list stays as current as a television one's — without
// this the shelf would freeze at add time and its calendar would go stale.
func TestRefreshSweepsAnimeSeries(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedSeries(h)
	anime := animeLibrary(t, h)

	monitored := true
	sr, err := h.mgr.AddSeries(ctx, core.TMDBRef(42), &monitored, anime.ID)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	// The provider announces a fourth episode after the add.
	meta := h.provider.seriesByID[42]
	meta.Seasons[0].Episodes = append(meta.Seasons[0].Episodes,
		core.EpisodeMeta{TMDBID: 4, Season: 1, Number: 4, Title: "Deserts"})
	h.provider.seriesByID[42] = meta

	res, err := h.mgr.RefreshLibrary(ctx)
	if err != nil {
		t.Fatalf("RefreshLibrary: %v", err)
	}
	if res.Series != 1 || len(res.Errors) != 0 {
		t.Fatalf("refresh result = %+v, want the one anime series refreshed", res)
	}
	episodes, err := h.st.ListEpisodes(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(episodes) != 4 {
		t.Errorf("episodes after refresh = %d, want the announced fourth to have landed", len(episodes))
	}
	// The refresh must not re-file the row: which shelf it is on is the move
	// endpoint's decision, never a sweep's.
	after, err := h.st.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if after.Kind != core.SeriesKindAnime || after.LibraryID != anime.ID {
		t.Errorf("after the refresh = {kind %q, library %d}, want anime in %d",
			after.Kind, after.LibraryID, anime.ID)
	}
}
