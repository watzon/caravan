package store

import (
	"context"
	"errors"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestMediaFileLibraryKind(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	// A second television library, so the answer has to name a library and not
	// just a kind: the DLNA tree gates a media URL on the owning library's own
	// dlna_visible flag.
	anime := &core.Library{
		Kind: core.LibraryKindTV, Name: "Anime", RootPath: "library/Anime",
		Provider: core.ProviderTMDB,
	}
	if err := st.CreateLibrary(ctx, anime); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}

	movie := &core.Movie{TMDBID: 1, Title: "Movie", SortTitle: "movie"}
	if err := st.UpsertMovie(ctx, movie); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	tv := &core.Series{TMDBID: 2, Title: "Show", SortTitle: "show"}
	if err := st.UpsertSeries(ctx, tv); err != nil {
		t.Fatalf("UpsertSeries(tv): %v", err)
	}
	animeSeries := &core.Series{TMDBID: 3, Title: "Frieren", SortTitle: "frieren", LibraryID: anime.ID}
	if err := st.UpsertSeries(ctx, animeSeries); err != nil {
		t.Fatalf("UpsertSeries(anime): %v", err)
	}
	adult := &core.Series{
		Kind: core.SeriesKindAdult, StashID: "site-1", Title: "Site", SortTitle: "site",
	}
	if err := st.UpsertSeries(ctx, adult); err != nil {
		t.Fatalf("UpsertSeries(adult): %v", err)
	}

	tvEpisode := &core.Episode{SeriesID: tv.ID, SeasonNumber: 1, EpisodeNumber: 1}
	animeEpisode := &core.Episode{SeriesID: animeSeries.ID, SeasonNumber: 1, EpisodeNumber: 1}
	adultEpisodes := []*core.Episode{
		{SeriesID: adult.ID, SeasonNumber: 2025, EpisodeNumber: 1},
		{SeriesID: adult.ID, SeasonNumber: 2025, EpisodeNumber: 2},
	}
	for _, episode := range []*core.Episode{tvEpisode, animeEpisode} {
		if err := st.UpsertEpisode(ctx, episode); err != nil {
			t.Fatalf("UpsertEpisode(tv): %v", err)
		}
	}
	for _, episode := range adultEpisodes {
		if err := st.UpsertEpisode(ctx, episode); err != nil {
			t.Fatalf("UpsertEpisode(adult): %v", err)
		}
	}

	movieFile := &core.MediaFile{Path: "Movies/movie.mkv", MovieID: movie.ID}
	tvFile := &core.MediaFile{Path: "TV/show.mkv"}
	animeFile := &core.MediaFile{Path: "Anime/frieren.mkv"}
	adultFile := &core.MediaFile{Path: "Adult/scene.mkv"}
	multiAdultFile := &core.MediaFile{Path: "Adult/double-scene.mkv"}
	unownedFile := &core.MediaFile{Path: "unowned.mkv"}
	mixedKindFile := &core.MediaFile{Path: "mixed-kind.mkv"}
	mixedLibraryFile := &core.MediaFile{Path: "mixed-library.mkv"}
	movieEpisodeFile := &core.MediaFile{Path: "movie-episode.mkv", MovieID: movie.ID}
	for _, file := range []*core.MediaFile{
		movieFile, tvFile, animeFile, adultFile, multiAdultFile, unownedFile,
		mixedKindFile, mixedLibraryFile, movieEpisodeFile,
	} {
		if err := st.UpsertMediaFile(ctx, file); err != nil {
			t.Fatalf("UpsertMediaFile(%q): %v", file.Path, err)
		}
	}
	for _, link := range []struct {
		episodeID int64
		fileID    int64
	}{
		{tvEpisode.ID, tvFile.ID},
		{animeEpisode.ID, animeFile.ID},
		{adultEpisodes[0].ID, adultFile.ID},
		{adultEpisodes[0].ID, multiAdultFile.ID},
		{adultEpisodes[1].ID, multiAdultFile.ID},
		{tvEpisode.ID, mixedKindFile.ID},
		{adultEpisodes[0].ID, mixedKindFile.ID},
		{tvEpisode.ID, mixedLibraryFile.ID},
		{animeEpisode.ID, mixedLibraryFile.ID},
		{tvEpisode.ID, movieEpisodeFile.ID},
	} {
		if err := st.LinkEpisodeFile(ctx, link.episodeID, link.fileID); err != nil {
			t.Fatalf("LinkEpisodeFile: %v", err)
		}
	}

	tests := []struct {
		name    string
		id      int64
		want    string
		wantLib int64
	}{
		{name: "movie", id: movieFile.ID, want: core.LibraryKindMovie},
		{name: "television", id: tvFile.ID, want: core.LibraryKindTV},
		{name: "second television library", id: animeFile.ID, want: core.LibraryKindTV, wantLib: anime.ID},
		{name: "adult", id: adultFile.ID, want: core.LibraryKindAdult},
		{name: "multi-episode adult", id: multiAdultFile.ID, want: core.LibraryKindAdult},
		{name: "unowned", id: unownedFile.ID},
		{name: "mixed television and adult", id: mixedKindFile.ID},
		{name: "two television libraries", id: mixedLibraryFile.ID},
		{name: "movie and episode", id: movieEpisodeFile.ID},
		{name: "missing", id: 9999},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotLib, got, err := st.GetMediaFileLibrary(ctx, tt.id)
			if tt.want == "" {
				if !errors.Is(err, ErrNotFound) {
					t.Fatalf("GetMediaFileLibrary(%d) error = %v, want ErrNotFound", tt.id, err)
				}
				if got != "" || gotLib != 0 {
					t.Fatalf("GetMediaFileLibrary(%d) = %d, %q, want zero", tt.id, gotLib, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("GetMediaFileLibrary(%d): %v", tt.id, err)
			}
			if got != tt.want || gotLib != tt.wantLib {
				t.Fatalf("GetMediaFileLibrary(%d) = %d, %q, want %d, %q",
					tt.id, gotLib, got, tt.wantLib, tt.want)
			}
		})
	}

	// Conversion candidates use the same fail-closed ownership rule in one
	// batched query. A multi-episode file appears once.
	assertCandidates := func(want []struct{ path, kind string }) {
		t.Helper()
		got, err := st.ListConversionCandidates(ctx)
		if err != nil {
			t.Fatalf("ListConversionCandidates: %v", err)
		}
		if len(got) != len(want) {
			t.Fatalf("candidates = %+v, want %+v", got, want)
		}
		for i, candidate := range got {
			if candidate.File.Path != want[i].path || candidate.LibraryKind != want[i].kind {
				t.Fatalf("candidate %d = %+v, want path %q kind %q",
					i, candidate, want[i].path, want[i].kind)
			}
		}
	}
	// Conversion is not an exposure surface, so its ownership rule stays by
	// kind: a file shared by two television libraries is still one television
	// file to convert, even though GetMediaFileLibrary refuses to name an owner
	// for it.
	allCandidates := []struct{ path, kind string }{
		{"Adult/double-scene.mkv", core.LibraryKindAdult},
		{"Adult/scene.mkv", core.LibraryKindAdult},
		{"Anime/frieren.mkv", core.LibraryKindTV},
		{"Movies/movie.mkv", core.LibraryKindMovie},
		{"TV/show.mkv", core.LibraryKindTV},
		{"mixed-library.mkv", core.LibraryKindTV},
	}
	assertCandidates(allCandidates)

	conversion := &core.Conversion{
		MediaFileID: tvFile.ID, SourcePath: tvFile.Path, Status: core.ConversionQueued,
	}
	if err := st.CreateConversion(ctx, conversion); err != nil {
		t.Fatalf("CreateConversion: %v", err)
	}
	withoutShow := append(append([]struct{ path, kind string }{}, allCandidates[:4]...), allCandidates[5:]...)
	assertCandidates(withoutShow)

	// Terminal history does not hide a file that still needs work.
	conversion.Status = core.ConversionCancelled
	if err := st.UpdateConversion(ctx, conversion); err != nil {
		t.Fatalf("UpdateConversion(cancelled): %v", err)
	}
	assertCandidates(allCandidates)
}

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
