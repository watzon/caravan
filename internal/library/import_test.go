package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// parkOne scans a library holding exactly one file the scanner cannot match
// and returns the resulting unmatched row.
func parkOne(h *harness, rel string, parsed core.ParsedRelease) core.UnmatchedFile {
	h.t.Helper()
	h.parser[filepath.Base(rel)] = parsed
	h.writeVideo(rel, "bytes")

	if res := h.scan(); res.Unmatched != 1 {
		h.t.Fatalf("setup scan = %+v, want one parked file", res)
	}
	parked, err := h.st.ListUnmatchedFiles(context.Background())
	if err != nil {
		h.t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	if len(parked) != 1 {
		h.t.Fatalf("got %d parked files, want 1", len(parked))
	}
	return parked[0]
}

func TestImportUnmatchedMovie(t *testing.T) {
	h := newHarness(t)
	seedMovie(h)
	// The scanner cannot match this name; the user says what it is.
	h.provider.movies = nil
	rel := "library/Movies/bbb.final.cut.mkv"
	u := parkOne(h, rel, core.ParsedRelease{Title: "bbb final cut", Confidence: 0.9})

	ctx := context.Background()
	res, err := h.mgr.ImportUnmatched(ctx, u.ID, 10378, MediaTypeMovie)
	if err != nil {
		t.Fatalf("ImportUnmatched: %v", err)
	}
	if res.Path != organizedRel {
		t.Fatalf("imported path = %q, want %q", res.Path, organizedRel)
	}
	if res.MovieID == 0 {
		t.Error("imported movie id is zero")
	}
	if len(res.Warnings) != 0 {
		t.Errorf("unexpected warnings: %v", res.Warnings)
	}

	if h.exists(rel) {
		t.Errorf("source %s survived the import", rel)
	}
	if got := h.read(organizedRel); got != "bytes" {
		t.Errorf("imported content = %q", got)
	}
	if !h.exists(movieDirRel + "/" + MovieNFOName) {
		t.Errorf("%s not written", MovieNFOName)
	}

	parked, err := h.st.ListUnmatchedFiles(ctx)
	if err != nil {
		t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	if len(parked) != 0 {
		t.Fatalf("unmatched queue = %+v, want empty after a successful match", parked)
	}

	files, err := h.st.ListMediaFilesForMovie(ctx, res.MovieID)
	if err != nil {
		t.Fatalf("ListMediaFilesForMovie: %v", err)
	}
	if len(files) != 1 || files[0].Path != organizedRel {
		t.Fatalf("movie files = %+v, want one row at %s", files, organizedRel)
	}

	// The next scan must accept the manual result rather than re-parking it.
	h.parser[filepath.Base(organizedRel)] = movieParse("Big Buck Bunny", 2008)
	h.provider.movies = []core.MovieMeta{h.provider.movieByID[10378]}
	if res := h.scan(); res.Unmatched != 0 || res.Updated != 1 || res.Removed != 0 {
		t.Fatalf("rescan after manual match = %+v", res)
	}
}

func TestImportUnmatchedEpisode(t *testing.T) {
	h := newHarness(t)
	seedSeries(h)
	h.provider.series = nil
	rel := "library/TV/pe2.s01e03.mkv"
	u := parkOne(h, rel, core.ParsedRelease{Title: "pe2", Season: 1, Episodes: []int{3}, Confidence: 0.9})

	ctx := context.Background()
	res, err := h.mgr.ImportUnmatched(ctx, u.ID, 42, MediaTypeSeries)
	if err != nil {
		t.Fatalf("ImportUnmatched: %v", err)
	}

	want := "library/TV/Planet Earth II (2016)/Season 01/Planet Earth II (2016) - S01E03 - Jungles.mkv"
	if res.Path != want {
		t.Fatalf("imported path = %q, want %q", res.Path, want)
	}
	if res.SeriesID == 0 {
		t.Error("imported series id is zero")
	}
	if !h.exists(want) {
		t.Errorf("imported file missing")
	}

	episode, err := h.st.GetEpisodeByNumber(ctx, res.SeriesID, 1, 3)
	if err != nil {
		t.Fatalf("GetEpisodeByNumber: %v", err)
	}
	files, err := h.st.ListMediaFilesForEpisode(ctx, episode.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForEpisode: %v", err)
	}
	if len(files) != 1 || files[0].Path != want {
		t.Fatalf("episode files = %+v, want one row at %s", files, want)
	}

	parked, err := h.st.ListUnmatchedFiles(ctx)
	if err != nil {
		t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	if len(parked) != 0 {
		t.Fatalf("unmatched queue = %+v, want empty", parked)
	}
}

// TestImportUnmatchedCreatesEpisodesTheProviderDoesNotKnow: a file on disk is
// evidence the episode exists, whatever the provider lists.
func TestImportUnmatchedCreatesUnknownEpisode(t *testing.T) {
	h := newHarness(t)
	seedSeries(h)
	h.provider.series = nil
	rel := "library/TV/pe2.s01e09.mkv"
	u := parkOne(h, rel, core.ParsedRelease{Title: "pe2", Season: 1, Episodes: []int{9}, Confidence: 0.9})

	ctx := context.Background()
	res, err := h.mgr.ImportUnmatched(ctx, u.ID, 42, MediaTypeSeries)
	if err != nil {
		t.Fatalf("ImportUnmatched: %v", err)
	}
	want := "library/TV/Planet Earth II (2016)/Season 01/Planet Earth II (2016) - S01E09.mkv"
	if res.Path != want {
		t.Fatalf("imported path = %q, want %q", res.Path, want)
	}
	if _, err := h.st.GetEpisodeByNumber(ctx, res.SeriesID, 1, 9); err != nil {
		t.Fatalf("GetEpisodeByNumber for the placeholder episode: %v", err)
	}
}

func TestImportUnmatchedRejectsBadRequests(t *testing.T) {
	h := newHarness(t)
	seedMovie(h)
	h.provider.movies = nil
	rel := "library/Movies/bbb.final.cut.mkv"
	u := parkOne(h, rel, core.ParsedRelease{Title: "bbb final cut", Confidence: 0.9})

	ctx := context.Background()
	tests := []struct {
		name        string
		id          int64
		tmdbID      int64
		mediaType   string
		wantErrPart string
	}{
		{"unknown media type", u.ID, 10378, "album", "unknown media type"},
		{"unknown unmatched id", u.ID + 999, 10378, MediaTypeMovie, "not found"},
		{"invalid tmdb id", u.ID, 0, MediaTypeMovie, "invalid tmdb id"},
		{"movie id used as series", u.ID, 10378, MediaTypeSeries, "no season/episode number"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.mgr.ImportUnmatched(ctx, tt.id, tt.tmdbID, tt.mediaType)
			if err == nil {
				t.Fatalf("ImportUnmatched succeeded, want an error")
			}
			if !strings.Contains(err.Error(), tt.wantErrPart) {
				t.Errorf("error = %v, want it to mention %q", err, tt.wantErrPart)
			}
		})
	}

	// A rejected import must leave the queue and the file untouched.
	parked, err := h.st.ListUnmatchedFiles(ctx)
	if err != nil {
		t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	if len(parked) != 1 {
		t.Errorf("unmatched queue = %+v, want the entry to survive", parked)
	}
	if !h.exists(rel) {
		t.Errorf("file %s was moved by a rejected import", rel)
	}
}

func TestImportUnmatchedRequiresTheFileOnDisk(t *testing.T) {
	h := newHarness(t)
	seedMovie(h)
	h.provider.movies = nil
	rel := "library/Movies/bbb.final.cut.mkv"
	u := parkOne(h, rel, core.ParsedRelease{Title: "bbb final cut", Confidence: 0.9})

	if err := os.Remove(filepath.Join(h.root, filepath.FromSlash(rel))); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if _, err := h.mgr.ImportUnmatched(context.Background(), u.ID, 10378, MediaTypeMovie); err == nil {
		t.Fatal("ImportUnmatched succeeded for a file that is gone")
	}
}

func TestImportUnmatchedNeedsAProvider(t *testing.T) {
	h := newHarness(t)
	rel := "library/Movies/bbb.mkv"
	u := parkOne(h, rel, core.ParsedRelease{Title: "bbb", Confidence: 0.2})

	h.mgr = h.newManager(h.st, nil)
	if _, err := h.mgr.ImportUnmatched(context.Background(), u.ID, 10378, MediaTypeMovie); err == nil {
		t.Fatal("ImportUnmatched succeeded without a metadata provider")
	}
}
