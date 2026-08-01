package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

const (
	rawMovieRel  = "library/Movies/Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP.mkv"
	organizedRel = "library/Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv"
	movieDirRel  = "library/Movies/Big Buck Bunny (2008)"
)

// seedMovie registers the parse and the provider result for the canonical
// movie fixture.
func seedMovie(h *harness) {
	h.parser[filepath.Base(rawMovieRel)] = movieParse("Big Buck Bunny", 2008)
	h.provider.movies = []core.MovieMeta{{
		TMDBID:      10378,
		IMDBID:      "tt1254207",
		Title:       "Big Buck Bunny",
		Year:        2008,
		Overview:    "A big rabbit.",
		ReleaseDate: time.Date(2008, 5, 20, 0, 0, 0, 0, time.UTC),
		PosterURL:   h.posterURL,
	}}
	h.provider.movieByID[10378] = h.provider.movies[0]
}

func TestScanOrganizesAndRecordsAMovie(t *testing.T) {
	h := newHarness(t)
	seedMovie(h)
	h.writeVideo(rawMovieRel, "movie bytes")

	res := h.scan()

	if res.Scanned != 1 || res.Added != 1 || res.Unmatched != 0 || res.Removed != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected scan errors: %v", res.Errors)
	}
	if h.exists(rawMovieRel) {
		t.Errorf("source file %s still present after organize", rawMovieRel)
	}
	if got := h.read(organizedRel); got != "movie bytes" {
		t.Errorf("organized file content = %q, want %q", got, "movie bytes")
	}
	if !h.exists(movieDirRel + "/" + MovieNFOName) {
		t.Errorf("%s not written", MovieNFOName)
	}
	if got := h.read(movieDirRel + "/" + PosterName); got != string(posterBytes) {
		t.Errorf("poster content = %q, want %q", got, posterBytes)
	}

	ctx := context.Background()
	movies, err := h.st.ListMovies(ctx)
	if err != nil {
		t.Fatalf("ListMovies: %v", err)
	}
	if len(movies) != 1 {
		t.Fatalf("got %d movies, want 1", len(movies))
	}
	mv := movies[0]
	if mv.TMDBID != 10378 || mv.Title != "Big Buck Bunny" || mv.Year != 2008 {
		t.Errorf("movie row = %+v", mv)
	}
	if mv.Path != movieDirRel {
		t.Errorf("movie path = %q, want %q", mv.Path, movieDirRel)
	}
	if want := movieDirRel + "/" + PosterName; mv.PosterPath != want {
		t.Errorf("movie poster path = %q, want %q", mv.PosterPath, want)
	}

	files, err := h.st.ListMediaFiles(ctx)
	if err != nil {
		t.Fatalf("ListMediaFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d media files, want 1", len(files))
	}
	f := files[0]
	if f.Path != organizedRel {
		t.Errorf("media file path = %q, want %q", f.Path, organizedRel)
	}
	if f.MovieID != mv.ID {
		t.Errorf("media file movie id = %d, want %d", f.MovieID, mv.ID)
	}
	if f.Quality != core.Quality1080p || f.Source != core.SourceBluray || f.Codec != "x264" || f.ReleaseGroup != "GRP" {
		t.Errorf("media file tags = %+v", f)
	}
	if f.Size != int64(len("movie bytes")) {
		t.Errorf("media file size = %d, want %d", f.Size, len("movie bytes"))
	}
}

// seedSeries registers the parse and provider results for the canonical series
// fixture.
func seedSeries(h *harness) core.SeriesMeta {
	meta := core.SeriesMeta{
		TMDBID:       42,
		TVDBID:       137,
		Title:        "Planet Earth II",
		Year:         2016,
		Overview:     "Nature.",
		Status:       "Ended",
		FirstAirDate: time.Date(2016, 11, 6, 0, 0, 0, 0, time.UTC),
		PosterURL:    h.posterURL,
		Seasons: []core.SeasonMeta{{
			Number:  1,
			Title:   "Season 1",
			AirDate: time.Date(2016, 11, 6, 0, 0, 0, 0, time.UTC),
			Episodes: []core.EpisodeMeta{
				{TMDBID: 1, Season: 1, Number: 1, Title: "Islands"},
				{TMDBID: 2, Season: 1, Number: 2, Title: "Mountains"},
				{TMDBID: 3, Season: 1, Number: 3, Title: "Jungles"},
			},
		}},
	}
	h.provider.series = []core.SeriesMeta{{TMDBID: 42, Title: "Planet Earth II", Year: 2016}}
	h.provider.seriesByID[42] = meta
	return meta
}

func TestScanOrganizesAndRecordsAnEpisode(t *testing.T) {
	h := newHarness(t)
	seedSeries(h)
	raw := "library/TV/Planet.Earth.II.S01E01.1080p.WEB-DL.x265.mkv"
	h.parser[filepath.Base(raw)] = episodeParse("Planet Earth II", 1, 1)
	h.writeVideo(raw, "episode bytes")

	res := h.scan()
	if res.Scanned != 1 || res.Added != 1 || res.Unmatched != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("unexpected scan errors: %v", res.Errors)
	}

	const (
		seriesRel = "library/TV/Planet Earth II (2016)"
		wantFile  = seriesRel + "/Season 01/Planet Earth II (2016) - S01E01 - Islands.mkv"
	)
	if !h.exists(wantFile) {
		t.Fatalf("organized episode %s missing", wantFile)
	}
	if h.exists(raw) {
		t.Errorf("source file %s still present", raw)
	}
	if !h.exists(seriesRel + "/" + TVShowNFOName) {
		t.Errorf("%s not written", TVShowNFOName)
	}
	if !h.exists(seriesRel + "/" + PosterName) {
		t.Errorf("series poster not written")
	}

	ctx := context.Background()
	all, err := h.st.ListSeries(ctx)
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("got %d series, want 1", len(all))
	}
	sr := all[0]
	if sr.TMDBID != 42 || sr.TVDBID != 137 || sr.Status != "Ended" || sr.Path != seriesRel {
		t.Errorf("series row = %+v", sr)
	}

	// The whole provider tree lands, not just the episode on disk: the library
	// view has to be able to show what is missing.
	episodes, err := h.st.ListEpisodes(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	if len(episodes) != 3 {
		t.Fatalf("got %d episodes, want 3", len(episodes))
	}

	linked, err := h.st.ListMediaFilesForEpisode(ctx, episodes[0].ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForEpisode: %v", err)
	}
	if len(linked) != 1 || linked[0].Path != wantFile {
		t.Fatalf("S01E01 links = %+v, want one link to %s", linked, wantFile)
	}
	if linked[0].MovieID != 0 {
		t.Errorf("episode file has movie id %d, want 0", linked[0].MovieID)
	}
	for _, e := range episodes[1:] {
		files, err := h.st.ListMediaFilesForEpisode(ctx, e.ID)
		if err != nil {
			t.Fatalf("ListMediaFilesForEpisode: %v", err)
		}
		if len(files) != 0 {
			t.Errorf("S%02dE%02d unexpectedly has files %+v", e.SeasonNumber, e.EpisodeNumber, files)
		}
	}
}

func TestScanLinksMultiEpisodeFileToEveryEpisode(t *testing.T) {
	h := newHarness(t)
	seedSeries(h)
	raw := "library/TV/Planet.Earth.II.S01E01E02.1080p.mkv"
	h.parser[filepath.Base(raw)] = episodeParse("Planet Earth II", 1, 1, 2)
	h.writeVideo(raw, "double episode")

	h.scan()

	const wantFile = "library/TV/Planet Earth II (2016)/Season 01/Planet Earth II (2016) - S01E01-E02 - Islands + Mountains.mkv"
	if !h.exists(wantFile) {
		t.Fatalf("organized multi-episode file %s missing", wantFile)
	}

	ctx := context.Background()
	series, err := h.st.ListSeries(ctx)
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	episodes, err := h.st.ListEpisodes(ctx, series[0].ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	for _, e := range episodes[:2] {
		files, err := h.st.ListMediaFilesForEpisode(ctx, e.ID)
		if err != nil {
			t.Fatalf("ListMediaFilesForEpisode: %v", err)
		}
		if len(files) != 1 || files[0].Path != wantFile {
			t.Errorf("S01E%02d links = %+v, want one link to the shared file", e.EpisodeNumber, files)
		}
	}
}

func TestScanParksFilesItCannotMatch(t *testing.T) {
	tests := []struct {
		name       string
		rel        string
		parsed     *core.ParsedRelease
		noProvider bool
		searchErr  error
		movies     []core.MovieMeta
		wantReason string
	}{
		{
			name:       "low parser confidence",
			rel:        "library/Movies/whatever.mkv",
			parsed:     &core.ParsedRelease{Title: "whatever", Confidence: 0.2},
			wantReason: reasonLowParse,
		},
		{
			name:       "no title",
			rel:        "library/Movies/blank.mkv",
			parsed:     &core.ParsedRelease{Title: "   ", Confidence: 0.9},
			wantReason: reasonNoTitle,
		},
		{
			name:       "no provider configured",
			rel:        rawMovieRel,
			noProvider: true,
			wantReason: reasonNoProvider,
		},
		{
			name:       "provider error",
			rel:        rawMovieRel,
			searchErr:  errors.New("tmdb down"),
			wantReason: reasonProviderErr,
		},
		{
			name:       "no metadata match",
			rel:        rawMovieRel,
			movies:     []core.MovieMeta{{TMDBID: 1, Title: "Something Else", Year: 1999}},
			wantReason: reasonNoMatch,
		},
		{
			name:       "tv file without an episode number",
			rel:        "library/TV/Some.Show.Complete.Pack.mkv",
			parsed:     &core.ParsedRelease{Title: "Some Show", Confidence: 0.9},
			wantReason: reasonNoEpisodeNum,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			seedMovie(h)
			if tt.parsed != nil {
				h.parser[filepath.Base(tt.rel)] = *tt.parsed
			}
			if tt.movies != nil {
				h.provider.movies = tt.movies
			}
			h.provider.searchErr = tt.searchErr
			if tt.noProvider {
				h.mgr = h.newManager(h.st, nil)
			}
			h.writeVideo(tt.rel, "bytes")

			res := h.scan()
			if res.Unmatched != 1 || res.Added != 0 {
				t.Fatalf("unexpected result: %+v", res)
			}
			if !h.exists(tt.rel) {
				t.Errorf("parked file %s was moved; parking must not touch the file", tt.rel)
			}

			parked, err := h.st.ListUnmatchedFiles(context.Background())
			if err != nil {
				t.Fatalf("ListUnmatchedFiles: %v", err)
			}
			if len(parked) != 1 {
				t.Fatalf("got %d parked files, want 1", len(parked))
			}
			if parked[0].Path != tt.rel {
				t.Errorf("parked path = %q, want %q", parked[0].Path, tt.rel)
			}
			if parked[0].Reason != tt.wantReason {
				t.Errorf("parked reason = %q, want %q", parked[0].Reason, tt.wantReason)
			}
			if parked[0].Size != int64(len("bytes")) {
				t.Errorf("parked size = %d, want %d", parked[0].Size, len("bytes"))
			}
			// The parser's guess must survive the round trip: it is what the
			// scan-review screen shows the user.
			if tt.parsed != nil && parked[0].Parsed.Title != tt.parsed.Title {
				t.Errorf("parked parse title = %q, want %q", parked[0].Parsed.Title, tt.parsed.Title)
			}
		})
	}
}

// TestScanParksWhenSeriesDetailsFail covers the second provider round trip: the
// search matched, but fetching the season/episode tree failed. The file parks
// rather than being organized against half-known metadata.
func TestScanParksWhenSeriesDetailsFail(t *testing.T) {
	h := newHarness(t)
	seedSeries(h)
	h.provider.getErr = errors.New("tmdb down")
	raw := "library/TV/Planet.Earth.II.S01E01.1080p.mkv"
	h.parser[filepath.Base(raw)] = episodeParse("Planet Earth II", 1, 1)
	h.writeVideo(raw, "bytes")

	res := h.scan()
	if res.Unmatched != 1 || res.Added != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if len(res.Errors) != 1 {
		t.Errorf("errors = %v, want the provider failure reported once", res.Errors)
	}
	if !h.exists(raw) {
		t.Errorf("file was moved despite the provider failure")
	}

	parked, err := h.st.ListUnmatchedFiles(context.Background())
	if err != nil {
		t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	if len(parked) != 1 || parked[0].Reason != reasonProviderErr {
		t.Fatalf("parked = %+v, want one entry with reason %q", parked, reasonProviderErr)
	}
}

func TestScanIgnoresNonVideoAndHiddenFiles(t *testing.T) {
	h := newHarness(t)
	h.writeVideo("library/Movies/notes.txt", "text")
	h.writeVideo("library/Movies/._Sidecar.mkv", "apple double")
	h.writeVideo("library/.Trashes/Deleted.mkv", "trash")
	h.writeVideo("library/Movies/poster.jpg", "image")

	res := h.scan()
	if res.Scanned != 0 || res.Unmatched != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestScanIsIdempotent(t *testing.T) {
	h := newHarness(t)
	seedMovie(h)
	h.writeVideo(rawMovieRel, "movie bytes")

	if first := h.scan(); first.Added != 1 {
		t.Fatalf("first scan = %+v, want one added file", first)
	}
	hitsAfterFirst := h.posterHits

	second := h.scan()
	if second.Scanned != 1 || second.Added != 0 || second.Updated != 1 || second.Removed != 0 || second.Unmatched != 0 {
		t.Fatalf("second scan = %+v, want one updated file and nothing else", second)
	}
	if len(second.Errors) != 0 {
		t.Fatalf("unexpected scan errors: %v", second.Errors)
	}
	if h.posterHits != hitsAfterFirst {
		t.Errorf("poster refetched on rescan: %d hits, want %d", h.posterHits, hitsAfterFirst)
	}

	ctx := context.Background()
	files, err := h.st.ListMediaFiles(ctx)
	if err != nil {
		t.Fatalf("ListMediaFiles: %v", err)
	}
	if len(files) != 1 || files[0].Path != organizedRel {
		t.Fatalf("after rescan media files = %+v, want exactly %s", files, organizedRel)
	}
	movies, err := h.st.ListMovies(ctx)
	if err != nil {
		t.Fatalf("ListMovies: %v", err)
	}
	if len(movies) != 1 {
		t.Fatalf("after rescan got %d movies, want 1", len(movies))
	}
}

// TestRescanKeepsReleaseTags: the organized Jellyfin name carries no quality,
// source, codec or group, so a rescan re-parses less than the original release
// name said. The tags must survive anyway — phase-3 upgrade decisions read
// them.
func TestRescanKeepsReleaseTags(t *testing.T) {
	h := newHarness(t)
	seedMovie(h)
	h.writeVideo(rawMovieRel, "movie bytes")
	h.scan()
	h.scan()

	files, err := h.st.ListMediaFiles(context.Background())
	if err != nil {
		t.Fatalf("ListMediaFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("got %d media files, want 1", len(files))
	}
	f := files[0]
	if f.Quality != core.Quality1080p {
		t.Errorf("quality after rescan = %q, want %q", f.Quality, core.Quality1080p)
	}
	if f.Source != core.SourceBluray {
		t.Errorf("source after rescan = %q, want %q", f.Source, core.SourceBluray)
	}
	if f.Codec != "x264" {
		t.Errorf("codec after rescan = %q, want %q", f.Codec, "x264")
	}
	if f.ReleaseGroup != "GRP" {
		t.Errorf("release group after rescan = %q, want %q", f.ReleaseGroup, "GRP")
	}
}

func TestScanPreservesUserIntentOnRescan(t *testing.T) {
	h := newHarness(t)
	seedMovie(h)
	h.writeVideo(rawMovieRel, "movie bytes")
	h.scan()

	ctx := context.Background()
	movies, err := h.st.ListMovies(ctx)
	if err != nil {
		t.Fatalf("ListMovies: %v", err)
	}
	mv := movies[0]
	mv.Monitored = false
	mv.QualityProfileID = 7
	if err := h.st.UpsertMovie(ctx, &mv); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	h.scan()

	after, err := h.st.GetMovie(ctx, mv.ID)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if after.Monitored {
		t.Error("rescan re-monitored a movie the user unmonitored")
	}
	if after.QualityProfileID != 7 {
		t.Errorf("rescan reset quality profile to %d, want 7", after.QualityProfileID)
	}
}

func TestScanRemovesRowsForDeletedFiles(t *testing.T) {
	h := newHarness(t)
	seedMovie(h)
	h.writeVideo(rawMovieRel, "movie bytes")
	h.scan()

	if err := os.Remove(filepath.Join(h.root, filepath.FromSlash(organizedRel))); err != nil {
		t.Fatalf("remove organized file: %v", err)
	}

	res := h.scan()
	if res.Scanned != 0 || res.Removed != 1 {
		t.Fatalf("unexpected result: %+v", res)
	}

	ctx := context.Background()
	files, err := h.st.ListMediaFiles(ctx)
	if err != nil {
		t.Fatalf("ListMediaFiles: %v", err)
	}
	if len(files) != 0 {
		t.Fatalf("media files = %+v, want none", files)
	}
	// The movie row survives: an item with no file is a legitimate wanted item.
	movies, err := h.st.ListMovies(ctx)
	if err != nil {
		t.Fatalf("ListMovies: %v", err)
	}
	if len(movies) != 1 {
		t.Fatalf("got %d movies, want the row to survive its file", len(movies))
	}
}

func TestScanClearsStaleUnmatchedEntries(t *testing.T) {
	h := newHarness(t)
	rel := "library/Movies/mystery.mkv"
	h.writeVideo(rel, "bytes")
	h.scan()

	ctx := context.Background()
	parked, err := h.st.ListUnmatchedFiles(ctx)
	if err != nil {
		t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	if len(parked) != 1 {
		t.Fatalf("got %d parked files, want 1", len(parked))
	}

	if err := os.Remove(filepath.Join(h.root, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("remove: %v", err)
	}
	h.scan()

	parked, err = h.st.ListUnmatchedFiles(ctx)
	if err != nil {
		t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	if len(parked) != 0 {
		t.Fatalf("parked files = %+v, want none after the file was deleted", parked)
	}
}

// TestScanRebuildsAfterDatabaseLoss is the DB-disposability acceptance
// criterion from PLAN phase 1: delete the database, rescan, get the same
// library back with zero file modifications.
func TestScanRebuildsAfterDatabaseLoss(t *testing.T) {
	h := newHarness(t)
	seedMovie(h)
	seedSeries(h)
	h.writeVideo(rawMovieRel, "movie bytes")
	raw := "library/TV/Planet.Earth.II.S01E02.1080p.WEB-DL.x265.mkv"
	h.parser[filepath.Base(raw)] = episodeParse("Planet Earth II", 1, 2)
	h.writeVideo(raw, "episode bytes")
	h.writeVideo("library/Movies/mystery.mkv", "unknown")

	h.scan()
	before := h.snapshot()
	filesBefore := h.walkLibrary()

	// A fresh database is exactly what "deleted caravan.db" leaves behind.
	fresh := h.openStore(filepath.Join(t.TempDir(), "rebuilt.db"))
	h.mgr = h.newManager(fresh, h.provider)

	res, err := h.mgr.Scan(context.Background())
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if len(res.Errors) != 0 {
		t.Fatalf("rescan errors: %v", res.Errors)
	}
	h.st = fresh

	if after := h.snapshot(); after != before {
		t.Errorf("rebuilt library differs\n before: %s\n  after: %s", before, after)
	}
	if after := h.walkLibrary(); !equalStrings(after, filesBefore) {
		t.Errorf("rescan modified files on disk\n before: %v\n  after: %v", filesBefore, after)
	}
}

// snapshot renders the library state that a rescan must reproduce.
//
// Row ids and timestamps are excluded: they are cache identity, not library
// identity. Release tags (quality, source, codec, group) are excluded too, and
// honestly so — the Jellyfin filename SPEC §6 mandates does not carry them, so
// a rebuild from a bare filesystem cannot recover what the original release
// name said. What must survive is identity and location.
func (h *harness) snapshot() string {
	h.t.Helper()
	ctx := context.Background()
	var b strings.Builder

	movies, err := h.st.ListMovies(ctx)
	if err != nil {
		h.t.Fatalf("ListMovies: %v", err)
	}
	for _, m := range movies {
		fmt.Fprintf(&b, "movie tmdb=%d title=%q year=%d path=%q poster=%q\n",
			m.TMDBID, m.Title, m.Year, m.Path, m.PosterPath)
	}

	series, err := h.st.ListSeries(ctx)
	if err != nil {
		h.t.Fatalf("ListSeries: %v", err)
	}
	for _, s := range series {
		fmt.Fprintf(&b, "series tmdb=%d title=%q year=%d path=%q\n", s.TMDBID, s.Title, s.Year, s.Path)
		episodes, err := h.st.ListEpisodes(ctx, s.ID)
		if err != nil {
			h.t.Fatalf("ListEpisodes: %v", err)
		}
		for _, e := range episodes {
			files, err := h.st.ListMediaFilesForEpisode(ctx, e.ID)
			if err != nil {
				h.t.Fatalf("ListMediaFilesForEpisode: %v", err)
			}
			paths := make([]string, len(files))
			for i, f := range files {
				paths[i] = f.Path
			}
			fmt.Fprintf(&b, "  episode S%02dE%02d %q files=%v\n", e.SeasonNumber, e.EpisodeNumber, e.Title, paths)
		}
	}

	files, err := h.st.ListMediaFiles(ctx)
	if err != nil {
		h.t.Fatalf("ListMediaFiles: %v", err)
	}
	for _, f := range files {
		fmt.Fprintf(&b, "file %q size=%d movie=%v\n", f.Path, f.Size, f.MovieID != 0)
	}

	parked, err := h.st.ListUnmatchedFiles(ctx)
	if err != nil {
		h.t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	for _, u := range parked {
		fmt.Fprintf(&b, "unmatched %q reason=%q\n", u.Path, u.Reason)
	}
	return b.String()
}

// walkLibrary lists every file under the library, so a test can assert a
// rescan changed nothing on disk.
func (h *harness) walkLibrary() []string {
	h.t.Helper()
	var out []string
	root := filepath.Join(h.root, LibraryDir)
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		out = append(out, fmt.Sprintf("%s:%d", filepath.ToSlash(rel), info.Size()))
		return nil
	})
	if err != nil {
		h.t.Fatalf("walk library: %v", err)
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestScanMissingLibraryDirectoryIsNotAnError(t *testing.T) {
	h := newHarness(t)
	res := h.scan()
	if res.Scanned != 0 || len(res.Errors) != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
}
