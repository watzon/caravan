package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// addMovieWithFile writes a movie folder holding one media file, the poster and
// the NFO Caravan would have written beside it, and returns the movie.
func (h *harness) addMovieWithFile(title string, year int) (*core.Movie, string) {
	h.t.Helper()
	dir := movieDir(title, year)
	rel := dir + "/" + movieFileName(title, year, "", ".mkv")
	h.writeVideo(rel, "movie")
	h.writeVideo(dir+"/"+PosterName, "poster")
	h.writeVideo(dir+"/"+MovieNFOName, "<movie/>")

	mv := &core.Movie{TMDBID: int64(year), Title: title, Year: year, Path: dir}
	if err := h.st.UpsertMovie(context.Background(), mv); err != nil {
		h.t.Fatalf("UpsertMovie: %v", err)
	}
	h.addMediaFile(rel, mv.ID)
	return mv, rel
}

// addMediaFile records a media_files row at rel. movieID 0 is an episode file.
func (h *harness) addMediaFile(rel string, movieID int64) *core.MediaFile {
	h.t.Helper()
	f := &core.MediaFile{Path: rel, Size: 1, MovieID: movieID}
	if err := h.st.UpsertMediaFile(context.Background(), f); err != nil {
		h.t.Fatalf("UpsertMediaFile %s: %v", rel, err)
	}
	return f
}

// addSeriesWithEpisodes writes a series folder with one file per episode and
// returns the series and the files' storage-root-relative paths.
func (h *harness) addSeriesWithEpisodes(title string, year, season int, episodes ...int) (*core.Series, []string) {
	h.t.Helper()
	ctx := context.Background()
	dir := seriesDir(title, year)
	h.writeVideo(dir+"/"+PosterName, "poster")
	h.writeVideo(dir+"/"+TVShowNFOName, "<tvshow/>")

	sr := &core.Series{TMDBID: int64(year), Title: title, Year: year, Path: dir}
	if err := h.st.UpsertSeries(ctx, sr); err != nil {
		h.t.Fatalf("UpsertSeries: %v", err)
	}

	rels := make([]string, 0, len(episodes))
	for _, number := range episodes {
		rel := dir + "/" + seasonFolderName(season) + "/" +
			episodeFileName(title, year, season, []int{number}, "", ".mkv")
		h.writeVideo(rel, "episode")

		e := &core.Episode{SeriesID: sr.ID, SeasonNumber: season, EpisodeNumber: number, Monitored: true}
		if err := h.st.UpsertEpisode(ctx, e); err != nil {
			h.t.Fatalf("UpsertEpisode: %v", err)
		}
		f := h.addMediaFile(rel, 0)
		if err := h.st.LinkEpisodeFile(ctx, e.ID, f.ID); err != nil {
			h.t.Fatalf("LinkEpisodeFile: %v", err)
		}
		rels = append(rels, rel)
	}
	return sr, rels
}

func (h *harness) movieGone(id int64) bool {
	h.t.Helper()
	_, err := h.st.GetMovie(context.Background(), id)
	return errors.Is(err, store.ErrNotFound)
}

// warnings returns the detail lines of the refusal events a removal recorded.
func (h *harness) warnings() []string {
	h.t.Helper()
	events, err := h.st.ListEvents(context.Background(), 0)
	if err != nil {
		h.t.Fatalf("ListEvents: %v", err)
	}
	out := []string{}
	for _, e := range events {
		if e.Category == EventCategoryLibrary {
			out = append(out, e.Detail)
		}
	}
	return out
}

// Untracking is what DELETE has always meant (SPEC §1.2): the rows go, the
// filesystem does not move.
func TestRemoveMovieUntrackOnlyLeavesFiles(t *testing.T) {
	h := newHarness(t)
	mv, rel := h.addMovieWithFile("Big Buck Bunny", 2008)

	if err := h.mgr.RemoveMovie(context.Background(), mv.ID, false); err != nil {
		t.Fatalf("RemoveMovie: %v", err)
	}

	if !h.movieGone(mv.ID) {
		t.Fatal("movie is still tracked after RemoveMovie")
	}
	if !h.exists(rel) {
		t.Fatalf("%s was deleted by an untrack-only removal", rel)
	}
	if !h.exists(movieDir("Big Buck Bunny", 2008) + "/" + PosterName) {
		t.Fatal("the poster was deleted by an untrack-only removal")
	}
}

func TestRemoveMovieDeletesFilesAndPrunesFolder(t *testing.T) {
	h := newHarness(t)
	mv, rel := h.addMovieWithFile("Big Buck Bunny", 2008)
	dir := movieDir("Big Buck Bunny", 2008)

	if err := h.mgr.RemoveMovie(context.Background(), mv.ID, true); err != nil {
		t.Fatalf("RemoveMovie: %v", err)
	}

	if !h.movieGone(mv.ID) {
		t.Fatal("movie is still tracked after RemoveMovie")
	}
	if h.exists(rel) {
		t.Fatalf("%s survived a file-deleting removal", rel)
	}
	if h.exists(dir) {
		t.Fatalf("%s survived, but the removal emptied it", dir)
	}
	// The section directory is layout, not content (SPEC §6): it outlives
	// every item in it.
	if !h.exists(LibraryDir + "/" + MoviesDir) {
		t.Fatalf("%s/%s was pruned, but it is a section root", LibraryDir, MoviesDir)
	}
	// A row describing a file that no longer exists is stale, not history.
	if _, err := h.st.GetMediaFileByPath(context.Background(), rel); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("media_files row for %s: %v, want ErrNotFound", rel, err)
	}
}

// Blast radius: one movie's removal must reach exactly one movie's files.
func TestRemoveMovieLeavesSiblingsAlone(t *testing.T) {
	h := newHarness(t)
	gone, goneRel := h.addMovieWithFile("Big Buck Bunny", 2008)
	_, keptRel := h.addMovieWithFile("Sintel", 2010)

	if err := h.mgr.RemoveMovie(context.Background(), gone.ID, true); err != nil {
		t.Fatalf("RemoveMovie: %v", err)
	}

	if h.exists(goneRel) {
		t.Fatalf("%s survived its own removal", goneRel)
	}
	if !h.exists(keptRel) {
		t.Fatalf("%s was deleted by another movie's removal", keptRel)
	}
	if !h.exists(movieDir("Sintel", 2010) + "/" + PosterName) {
		t.Fatal("a sibling movie's poster was deleted")
	}
}

// A media_files row is data. One naming a path outside the library — an
// absolute path, or one that climbs out with ".." — must not steer os.Remove.
// The item still stops being tracked, and the refusal is visible in the feed.
func TestRemoveMovieRefusesPathsOutsideTheLibrary(t *testing.T) {
	outside := t.TempDir()
	absolute := filepath.Join(outside, "not-caravans.mkv")
	if err := os.WriteFile(absolute, []byte("precious"), 0o644); err != nil {
		t.Fatalf("write %s: %v", absolute, err)
	}

	tests := []struct {
		name string
		path string
		// abs is the on-disk path that must survive, absolute.
		abs string
	}{
		{name: "absolute", path: absolute, abs: absolute},
		{name: "dot-dot", path: "library/Movies/../../escaped.mkv", abs: ""},
		{name: "outside the sections", path: "library/escaped.mkv", abs: ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()

			victim := tc.abs
			if victim == "" {
				victim = filepath.Join(h.root, filepath.FromSlash(strings.TrimPrefix(filepath.ToSlash(filepath.Clean(tc.path)), "./")))
				if err := os.MkdirAll(filepath.Dir(victim), 0o755); err != nil {
					t.Fatalf("mkdir: %v", err)
				}
				if err := os.WriteFile(victim, []byte("precious"), 0o644); err != nil {
					t.Fatalf("write %s: %v", victim, err)
				}
			}

			mv := &core.Movie{TMDBID: 1, Title: "Escapee", Year: 2020}
			if err := h.st.UpsertMovie(ctx, mv); err != nil {
				t.Fatalf("UpsertMovie: %v", err)
			}
			h.addMediaFile(tc.path, mv.ID)

			if err := h.mgr.RemoveMovie(ctx, mv.ID, true); err != nil {
				t.Fatalf("RemoveMovie: %v", err)
			}

			if _, err := os.Stat(victim); err != nil {
				t.Fatalf("%s was deleted: %v", victim, err)
			}
			if !h.movieGone(mv.ID) {
				t.Fatal("the movie is still tracked; a refused path must not block the untrack")
			}
			warnings := h.warnings()
			if len(warnings) != 1 || !strings.Contains(warnings[0], tc.path) {
				t.Fatalf("warnings = %v, want one naming %s", warnings, tc.path)
			}
		})
	}
}

// The storage root, the library directory and the two section directories are
// the layout SPEC §6 promises to players. No row may address them.
func TestRemoveMovieNeverDeletesTheLayout(t *testing.T) {
	protected := []string{".", LibraryDir, LibraryDir + "/" + MoviesDir, LibraryDir + "/" + TVDir}

	for _, dir := range protected {
		t.Run(dir, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			// The Movies section holds a file, so a mistaken prune there would
			// have to delete something real. The TV section is left empty on
			// purpose: nothing but the guard stands between it and os.Remove.
			h.writeVideo(LibraryDir+"/"+MoviesDir+"/keep.mkv", "keep")
			if err := os.MkdirAll(filepath.Join(h.root, LibraryDir, TVDir), 0o755); err != nil {
				t.Fatalf("mkdir %s: %v", TVDir, err)
			}

			mv := &core.Movie{TMDBID: 1, Title: "Layout", Year: 2020, Path: dir}
			if err := h.st.UpsertMovie(ctx, mv); err != nil {
				t.Fatalf("UpsertMovie: %v", err)
			}
			h.addMediaFile(dir, mv.ID)

			if err := h.mgr.RemoveMovie(ctx, mv.ID, true); err != nil {
				t.Fatalf("RemoveMovie: %v", err)
			}

			for _, want := range protected {
				if !h.exists(want) {
					t.Fatalf("%s was deleted while removing a movie pointed at %s", want, dir)
				}
			}
			if !h.exists(LibraryDir + "/" + MoviesDir + "/keep.mkv") {
				t.Fatal("a file under the Movies section was deleted")
			}
		})
	}
}

// A row that outlived its file is not a failure: the file is already in the
// state the caller asked for.
func TestRemoveMovieToleratesAMissingFile(t *testing.T) {
	h := newHarness(t)
	mv, rel := h.addMovieWithFile("Big Buck Bunny", 2008)
	if err := os.Remove(filepath.Join(h.root, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("remove %s: %v", rel, err)
	}

	if err := h.mgr.RemoveMovie(context.Background(), mv.ID, true); err != nil {
		t.Fatalf("RemoveMovie: %v", err)
	}
	if !h.movieGone(mv.ID) {
		t.Fatal("movie is still tracked after RemoveMovie")
	}
	if h.exists(movieDir("Big Buck Bunny", 2008)) {
		t.Fatal("the movie folder survived, but nothing was left in it")
	}
}

// Anything Caravan did not write into an item folder is the user's. It is not
// deleted, and it keeps the folder alive.
func TestRemoveMovieKeepsFilesCaravanDidNotWrite(t *testing.T) {
	h := newHarness(t)
	mv, rel := h.addMovieWithFile("Big Buck Bunny", 2008)
	dir := movieDir("Big Buck Bunny", 2008)
	h.writeVideo(dir+"/subtitles.srt", "1\n")

	if err := h.mgr.RemoveMovie(context.Background(), mv.ID, true); err != nil {
		t.Fatalf("RemoveMovie: %v", err)
	}

	if h.exists(rel) {
		t.Fatalf("%s survived a file-deleting removal", rel)
	}
	if !h.exists(dir + "/subtitles.srt") {
		t.Fatal("a file Caravan did not write was deleted")
	}
	if !h.exists(dir) {
		t.Fatal("the folder was pruned while it still held a user's file")
	}
}

func TestRemoveSeriesUntrackOnlyLeavesFiles(t *testing.T) {
	h := newHarness(t)
	sr, rels := h.addSeriesWithEpisodes("Planet Earth II", 2016, 1, 1, 2)

	if err := h.mgr.RemoveSeries(context.Background(), sr.ID, false); err != nil {
		t.Fatalf("RemoveSeries: %v", err)
	}

	if _, err := h.st.GetSeries(context.Background(), sr.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("GetSeries: %v, want ErrNotFound", err)
	}
	for _, rel := range rels {
		if !h.exists(rel) {
			t.Fatalf("%s was deleted by an untrack-only removal", rel)
		}
	}
}

func TestRemoveSeriesDeletesEveryEpisodeFileAndTheFolderTree(t *testing.T) {
	h := newHarness(t)
	sr, rels := h.addSeriesWithEpisodes("Planet Earth II", 2016, 1, 1, 2)
	dir := seriesDir("Planet Earth II", 2016)

	if err := h.mgr.RemoveSeries(context.Background(), sr.ID, true); err != nil {
		t.Fatalf("RemoveSeries: %v", err)
	}

	for _, rel := range rels {
		if h.exists(rel) {
			t.Fatalf("%s survived a file-deleting removal", rel)
		}
	}
	if h.exists(dir + "/" + seasonFolderName(1)) {
		t.Fatal("the season folder survived, but the removal emptied it")
	}
	if h.exists(dir) {
		t.Fatal("the series folder survived, but the removal emptied it")
	}
	if !h.exists(LibraryDir + "/" + TVDir) {
		t.Fatalf("%s/%s was pruned, but it is a section root", LibraryDir, TVDir)
	}
}

// A file covering S01E01E02 is listed once per episode. Deleting it twice would
// read the second pass as an already-gone file and hide a real failure.
func TestRemoveSeriesDeletesAMultiEpisodeFileOnce(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	dir := seriesDir("Planet Earth II", 2016)
	rel := dir + "/" + seasonFolderName(1) + "/" +
		episodeFileName("Planet Earth II", 2016, 1, []int{1, 2}, "", ".mkv")
	h.writeVideo(rel, "double")

	sr := &core.Series{TMDBID: 2016, Title: "Planet Earth II", Year: 2016, Path: dir}
	if err := h.st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	f := h.addMediaFile(rel, 0)
	for _, number := range []int{1, 2} {
		e := &core.Episode{SeriesID: sr.ID, SeasonNumber: 1, EpisodeNumber: number, Monitored: true}
		if err := h.st.UpsertEpisode(ctx, e); err != nil {
			t.Fatalf("UpsertEpisode: %v", err)
		}
		if err := h.st.LinkEpisodeFile(ctx, e.ID, f.ID); err != nil {
			t.Fatalf("LinkEpisodeFile: %v", err)
		}
	}

	if err := h.mgr.RemoveSeries(ctx, sr.ID, true); err != nil {
		t.Fatalf("RemoveSeries: %v", err)
	}
	if h.exists(rel) {
		t.Fatalf("%s survived a file-deleting removal", rel)
	}
	if got := h.warnings(); len(got) != 0 {
		t.Fatalf("warnings = %v, want none", got)
	}
}

func TestRemoveSeriesLeavesSiblingsAlone(t *testing.T) {
	h := newHarness(t)
	gone, goneRels := h.addSeriesWithEpisodes("Planet Earth II", 2016, 1, 1)
	_, keptRels := h.addSeriesWithEpisodes("Blue Planet II", 2017, 1, 1)

	if err := h.mgr.RemoveSeries(context.Background(), gone.ID, true); err != nil {
		t.Fatalf("RemoveSeries: %v", err)
	}

	if h.exists(goneRels[0]) {
		t.Fatalf("%s survived its own removal", goneRels[0])
	}
	if !h.exists(keptRels[0]) {
		t.Fatalf("%s was deleted by another series' removal", keptRels[0])
	}
}
