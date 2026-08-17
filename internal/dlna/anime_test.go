package dlna

import (
	"context"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// shareAnimeLibrary switches the seeded Anime shelf on and shares it, which is
// the two decisions the DLNA tree reads: `active AND dlna_visible`.
func shareAnimeLibrary(t *testing.T, st *store.Store) core.Library {
	t.Helper()
	ctx := context.Background()
	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindAnime)
	if err != nil {
		t.Fatalf("GetLibraryByKind(anime): %v", err)
	}
	lib.Active = true
	lib.DLNAVisible = true
	if err := st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	return *lib
}

// seedAnime puts one series with one playable episode and one film with one
// file on the anime shelf — the two halves the container has to hold together.
func seedAnime(t *testing.T, st *store.Store, lib core.Library) {
	t.Helper()
	ctx := context.Background()

	series := &core.Series{
		Kind: core.SeriesKindAnime, TMDBID: 209867, Title: "Frieren", SortTitle: "frieren",
		Year: 2023, Path: "library/Anime/Frieren (2023)", LibraryID: lib.ID,
	}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := st.UpsertSeason(ctx, &core.Season{SeriesID: series.ID, Number: 1}); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	episode := &core.Episode{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 1,
		Title: "The Journey's End"}
	if err := st.UpsertEpisode(ctx, episode); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	episodeFile := &core.MediaFile{Path: "library/Anime/Frieren (2023)/Season 01/E01.mkv", Size: 10}
	if err := st.UpsertMediaFile(ctx, episodeFile); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	if err := st.LinkEpisodeFile(ctx, episode.ID, episodeFile.ID); err != nil {
		t.Fatalf("LinkEpisodeFile: %v", err)
	}

	film := &core.Movie{TMDBID: 21519, Title: "Your Name.", SortTitle: "your name", Year: 2016,
		Path: "library/Anime/Your Name. (2016)", LibraryID: lib.ID}
	if err := st.UpsertMovie(ctx, film); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	filmFile := &core.MediaFile{Path: "library/Anime/Your Name. (2016)/Your Name. (2016).mkv",
		Size: 10, MovieID: film.ID}
	if err := st.UpsertMediaFile(ctx, filmFile); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
}

// The anime shelf is the one container that holds two kinds of child at once,
// because the library it stands for does. It is otherwise ordinary: it keeps
// the well-known "anime" id as its kind's default, and the root's count is the
// container's own listing.
func TestAnimeShelfHoldsSeriesAndFilms(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	lib := shareAnimeLibrary(t, st)
	seedAnime(t, st, lib)
	ctx := context.Background()

	rootDoc, err := svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	ids := containerIDs(rootDoc)
	if len(ids) != 3 || ids[2] != animeID {
		t.Fatalf("root children = %v, want the anime container last", ids)
	}
	if titles := containerTitles(rootDoc); titles[2] != "Anime" {
		t.Fatalf("root titles = %v, want the anime shelf named Anime", titles)
	}

	kids, err := svc.children(ctx, testURLs, animeID)
	if err != nil {
		t.Fatalf("children(anime): %v", err)
	}
	if got := containerTitles(kids); len(got) != 1 || got[0] != "Frieren (2023)" {
		t.Errorf("anime containers = %v, want the one series", got)
	}
	if got := itemTitles(kids); len(got) != 1 || got[0] != "Your Name. (2016)" {
		t.Errorf("anime items = %v, want the one film", got)
	}
	// The root advertises what the container actually answers with, and BOTH
	// halves of the shelf are in that number: a remote control that saw 1 here
	// would draw a folder claiming to hold the series alone. The literal is the
	// assertion — comparing the root's count against the same call that produced
	// it would hold however wrong both were.
	if rootDoc.Containers[2].ChildCount != 2 {
		t.Errorf("anime child count = %d, want 2 (one series container and one film)",
			rootDoc.Containers[2].ChildCount)
	}
	if kids.count() != 2 {
		t.Errorf("anime container holds %d children, want 2", kids.count())
	}
}

// The anime series id space is its own, for the reason the adult one is: an
// anime series is a `series` row, so "s:12" would otherwise be ambiguous between
// two shelves whose sharing flags are separate.
func TestAnimeSeriesCarriesItsOwnIDSpace(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	lib := shareAnimeLibrary(t, st)
	seedAnime(t, st, lib)
	ctx := context.Background()

	kids, err := svc.children(ctx, testURLs, animeID)
	if err != nil {
		t.Fatalf("children(anime): %v", err)
	}
	objectID := kids.Containers[0].ID
	if space, ok := shelfSpaceOf(objectID); !ok || space.seriesKind != core.SeriesKindAnime {
		t.Fatalf("anime series object id %q does not resolve to the anime id space", objectID)
	}

	// The television shelf holds its own show and not this one.
	tvDoc, err := svc.children(ctx, testURLs, tvID)
	if err != nil {
		t.Fatalf("children(tv): %v", err)
	}
	for _, c := range tvDoc.Containers {
		if c.Title == "Frieren (2023)" {
			t.Errorf("the anime series appeared on the television shelf")
		}
	}
}

// A dormant shelf is absent from the tree and every id beneath it answers "no
// such object" — which is what a client holding a cached id must be told.
func TestDormantAnimeShelfIsAbsent(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	lib := shareAnimeLibrary(t, st)
	seedAnime(t, st, lib)
	ctx := context.Background()

	kids, err := svc.children(ctx, testURLs, animeID)
	if err != nil {
		t.Fatalf("children(anime): %v", err)
	}
	seriesObjectID := kids.Containers[0].ID

	lib.Active = false
	if err := st.UpdateLibrary(ctx, &lib); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}

	rootDoc, err := svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	for _, id := range containerIDs(rootDoc) {
		if id == animeID {
			t.Errorf("the dormant anime shelf is still advertised")
		}
	}
	for _, objectID := range []string{animeID, seriesObjectID} {
		if _, err := svc.children(ctx, testURLs, objectID); err != errNoObject {
			t.Errorf("children(%q) on a dormant shelf = %v, want errNoObject", objectID, err)
		}
	}
}
