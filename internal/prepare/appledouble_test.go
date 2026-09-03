package prepare

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
)

func TestRemoveAppleDoubleSidecarsRemovesOnlySidecarsWithSiblings(t *testing.T) {
	rootPath := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootPath, "library", "Movies"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{
		"library/Movies/Movie.mkv",
		"library/Movies/._Movie.mkv",
		"._README.txt",
		"Notes.txt",
		"._Notes.txt",
		"._orphan.mkv",
		"._",
	} {
		if err := os.WriteFile(filepath.Join(rootPath, filepath.FromSlash(file)), []byte(file), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(rootPath, "README.txt"), []byte("readme"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, file := range []string{"._README.txt", "library/Movies/._Movie.mkv"} {
		contents := append([]byte(appleDoubleMagic), []byte("metadata")...)
		if err := os.WriteFile(filepath.Join(rootPath, filepath.FromSlash(file)), contents, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	removed, err := removeAppleDoubleSidecars(root)
	if err != nil {
		t.Fatalf("removeAppleDoubleSidecars: %v", err)
	}

	want := []string{"._README.txt", "library/Movies/._Movie.mkv"}
	if !slices.Equal(removed, want) {
		t.Fatalf("removed = %v, want %v", removed, want)
	}
	for _, file := range []string{"._Notes.txt", "._orphan.mkv", "._"} {
		if _, err := os.Stat(filepath.Join(rootPath, filepath.FromSlash(file))); err != nil {
			t.Errorf("orphan sidecar %s was removed: %v", file, err)
		}
	}
}

func TestRemoveAppleDoubleSidecarsDoesNotFollowOutsideSymlink(t *testing.T) {
	rootPath := t.TempDir()
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "Movie.mkv"), []byte("movie"), 0o644); err != nil {
		t.Fatal(err)
	}
	contents := append([]byte(appleDoubleMagic), []byte("metadata")...)
	if err := os.WriteFile(filepath.Join(outside, "._Movie.mkv"), contents, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(rootPath, "linked")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if removed, err := removeAppleDoubleSidecars(root); err != nil {
		t.Fatalf("removeAppleDoubleSidecars: %v", err)
	} else if len(removed) != 0 {
		t.Fatalf("removed = %v, want no files from the linked directory", removed)
	}
	if _, err := os.Stat(filepath.Join(outside, "._Movie.mkv")); err != nil {
		t.Fatalf("outside sidecar was touched: %v", err)
	}
}

func TestRemoveAppleDoubleSidecarsReturnsWalkError(t *testing.T) {
	rootPath := t.TempDir()
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := removeAppleDoubleSidecars(root); err == nil {
		t.Fatal("removeAppleDoubleSidecars succeeded with a closed root")
	}
}
