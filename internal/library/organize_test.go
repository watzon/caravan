package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestNaming(t *testing.T) {
	tests := []struct {
		name string
		got  string
		want string
	}{
		{"movie folder", movieFolderName("Big Buck Bunny", 2008), "Big Buck Bunny (2008)"},
		{"movie folder without year", movieFolderName("Big Buck Bunny", 0), "Big Buck Bunny"},
		{"movie file", movieFileName("Big Buck Bunny", 2008, "", ".mkv"), "Big Buck Bunny (2008).mkv"},
		{"movie file with edition", movieFileName("Blade Runner", 1982, "Director's Cut", ".mkv"),
			"Blade Runner (1982) - Director's Cut.mkv"},
		{"series folder", seriesFolderName("Planet Earth II", 2016), "Planet Earth II (2016)"},
		{"season folder", seasonFolderName(1), "Season 01"},
		{"specials folder", seasonFolderName(0), "Season 00"},
		{"season folder past nine", seasonFolderName(12), "Season 12"},
		{"episode file", episodeFileName("Planet Earth II", 2016, 1, []int{1}, "Islands", ".mkv"),
			"Planet Earth II (2016) - S01E01 - Islands.mkv"},
		{"multi episode file", episodeFileName("Planet Earth II", 2016, 1, []int{1, 2}, "Islands + Mountains", ".mkv"),
			"Planet Earth II (2016) - S01E01-E02 - Islands + Mountains.mkv"},
		{"episode file without title", episodeFileName("Planet Earth II", 2016, 1, []int{3}, "", ".mkv"),
			"Planet Earth II (2016) - S01E03.mkv"},
		{"illegal characters dropped", movieFolderName(`Mission: Impossible / Fallout?`, 2018),
			"Mission Impossible Fallout (2018)"},
		{"trailing dot trimmed", movieFolderName("Dr. Strangelove.", 1964), "Dr. Strangelove (1964)"},
		{"empty title", movieFolderName("", 0), "Unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %q, want %q", tt.got, tt.want)
			}
		})
	}
}

func TestSortTitle(t *testing.T) {
	tests := []struct{ in, want string }{
		{"The Matrix", "matrix"},
		{"A Quiet Place", "quiet place"},
		{"An Education", "education"},
		{"Theodore Rex", "theodore rex"},
		{"Big Buck Bunny", "big buck bunny"},
	}
	for _, tt := range tests {
		if got := sortTitle(tt.in); got != tt.want {
			t.Errorf("sortTitle(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestPlaceFileAvoidsCollisions covers two different files that both want the
// same Jellyfin name: the second must land beside the first, not over it.
func TestPlaceFileAvoidsCollisions(t *testing.T) {
	h := newHarness(t)
	h.writeVideo("library/Movies/first.mkv", "first")
	h.writeVideo("library/Movies/second.mkv", "second")

	dst := "library/Movies/Movie (2000)/Movie (2000).mkv"
	got, err := h.mgr.placeFile("library/Movies/first.mkv", dst)
	if err != nil {
		t.Fatalf("placeFile: %v", err)
	}
	if got != dst {
		t.Fatalf("first placement = %q, want %q", got, dst)
	}

	got, err = h.mgr.placeFile("library/Movies/second.mkv", dst)
	if err != nil {
		t.Fatalf("placeFile: %v", err)
	}
	want := "library/Movies/Movie (2000)/Movie (2000) (1).mkv"
	if got != want {
		t.Fatalf("second placement = %q, want %q", got, want)
	}
	if h.read(dst) != "first" {
		t.Errorf("first file was overwritten")
	}
	if h.read(want) != "second" {
		t.Errorf("second file content = %q", h.read(want))
	}
}

// TestPlaceFileIsANoOpWhenAlreadyInPlace is the rescan case: the destination
// already is the source file, so nothing may be copied or removed.
func TestPlaceFileIsANoOpWhenAlreadyInPlace(t *testing.T) {
	h := newHarness(t)
	rel := "library/Movies/Movie (2000)/Movie (2000).mkv"
	h.writeVideo(rel, "content")

	h.mgr.link = func(string, string) error {
		t.Fatal("placeFile tried to transfer a file that was already in place")
		return nil
	}
	got, err := h.mgr.placeFile(rel, rel)
	if err != nil {
		t.Fatalf("placeFile: %v", err)
	}
	if got != rel {
		t.Fatalf("placement = %q, want %q", got, rel)
	}
	if h.read(rel) != "content" {
		t.Errorf("file content changed")
	}
}

// TestScanFallsBackWhenHardlinksAreUnavailable simulates exFAT and cross-device
// sources (SPEC §3): os.Link always fails, and the file must still be
// organized with nothing left behind.
func TestScanFallsBackWhenHardlinksAreUnavailable(t *testing.T) {
	h := newHarness(t)
	seedMovie(h)
	h.writeVideo(rawMovieRel, "movie bytes")

	linkCalls := 0
	h.mgr.link = func(string, string) error {
		linkCalls++
		return errors.New("simulated: filesystem has no hardlinks")
	}

	res := h.scan()
	if res.Added != 1 || len(res.Errors) != 0 {
		t.Fatalf("unexpected result: %+v", res)
	}
	if linkCalls != 1 {
		t.Fatalf("os.Link called %d times, want 1 — hardlink must be tried first", linkCalls)
	}
	if h.exists(rawMovieRel) {
		t.Errorf("source %s survived the fallback transfer", rawMovieRel)
	}
	if got := h.read(organizedRel); got != "movie bytes" {
		t.Errorf("organized content = %q, want %q", got, "movie bytes")
	}
}

// TestCopyThenReplace covers the last-resort transfer path directly: it is the
// only one that works across filesystems, and os.Rename succeeding inside a
// single temp dir means a scan test can never reach it.
func TestCopyThenReplace(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mkv")
	dst := filepath.Join(dir, "sub", "dst.mkv")
	if err := os.WriteFile(src, []byte("payload"), 0o644); err != nil {
		t.Fatalf("write src: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if err := copyThenReplace(src, dst); err != nil {
		t.Fatalf("copyThenReplace: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("read dst: %v", err)
	}
	if string(got) != "payload" {
		t.Errorf("dst content = %q, want %q", got, "payload")
	}
	if _, err := os.Stat(src); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("src still exists after copy: %v", err)
	}
	// No temporary file may be left behind.
	entries, err := os.ReadDir(filepath.Dir(dst))
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("destination directory holds %d entries, want only the placed file", len(entries))
	}
}

func TestBestMatch(t *testing.T) {
	cands := []candidate{
		{title: "The Thing", year: 1982},
		{title: "The Thing", year: 2011},
		{title: "Thing Called Love", year: 1993},
	}

	tests := []struct {
		name  string
		title string
		year  int
		want  int
	}{
		{"exact title and year", "The Thing", 1982, 0},
		{"exact title, other year", "The Thing", 2011, 1},
		{"exact title, no year takes the first", "The Thing", 0, 0},
		{"year off by one still matches", "The Thing", 1983, 0},
		{"unknown title", "Solaris", 1972, -1},
		{"empty title never matches", "", 1982, -1},
		{"partial title needs the year", "Thing", 1993, 2},
		{"partial title without a year is not enough", "Called", 0, -1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := bestMatch(cands, tt.title, tt.year); got != tt.want {
				t.Errorf("bestMatch(%q, %d) = %d, want %d", tt.title, tt.year, got, tt.want)
			}
		})
	}
}

func TestNormalizeTitle(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Marvel's Daredevil", "marvels daredevil"},
		{"Spider-Man: No Way Home", "spider man no way home"},
		{"  WALL·E  ", "wall e"},
		{"...", ""},
	}
	for _, tt := range tests {
		if got := normalizeTitle(tt.in); got != tt.want {
			t.Errorf("normalizeTitle(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestMediaFileFromDefaultsUnknownTags(t *testing.T) {
	f := mediaFileFrom("library/Movies/X/X.mkv", 12, 3, core.ParsedRelease{})
	if f.Quality != core.QualityUnknown {
		t.Errorf("quality = %q, want %q", f.Quality, core.QualityUnknown)
	}
	if f.Source != core.SourceUnknown {
		t.Errorf("source = %q, want %q", f.Source, core.SourceUnknown)
	}
	if f.MovieID != 3 || f.Size != 12 {
		t.Errorf("unexpected file: %+v", f)
	}
}
