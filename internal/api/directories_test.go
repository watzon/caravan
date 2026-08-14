package api

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
)

func TestListDirectoriesReturnsChildFolders(t *testing.T) {
	h, _, _ := newTestServer(t)
	root := t.TempDir()
	movies := filepath.Join(root, "Movies")
	shows := filepath.Join(root, "Shows")
	if err := os.MkdirAll(movies, 0o755); err != nil {
		t.Fatalf("mkdir movies: %v", err)
	}
	if err := os.MkdirAll(shows, 0o755); err != nil {
		t.Fatalf("mkdir shows: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/system/directories?path="+url.QueryEscape(root), "")
	wantStatus(t, rec, http.StatusOK)

	var body directoryListing
	decodeBody(t, rec, &body)
	if body.Path != root {
		t.Fatalf("path = %q, want %q", body.Path, root)
	}
	if body.Parent != filepath.Dir(root) {
		t.Fatalf("parent = %q, want %q", body.Parent, filepath.Dir(root))
	}
	got := make([]string, 0, len(body.Directories))
	for _, dir := range body.Directories {
		got = append(got, dir.Name)
		if dir.Path != filepath.Join(root, dir.Name) {
			t.Fatalf("%s path = %q, want %q", dir.Name, dir.Path, filepath.Join(root, dir.Name))
		}
	}
	if !slices.Equal(got, []string{"Movies", "Shows"}) {
		t.Fatalf("directories = %v, want [Movies Shows] — files must not appear", got)
	}
}

func TestListDirectoriesRefusesARelativePath(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/system/directories?path=relative/media", "")
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}

func TestListDirectoriesRefusesAMissingPath(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/system/directories?path="+url.QueryEscape(filepath.Join(t.TempDir(), "nope")), "")
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}

func TestListDirectoriesRefusesAFile(t *testing.T) {
	h, _, _ := newTestServer(t)
	file := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/system/directories?path="+url.QueryEscape(file), "")
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}

func TestListDirectoriesDefaultsToTheFilesystemRoot(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/system/directories", "")
	wantStatus(t, rec, http.StatusOK)

	var body directoryListing
	decodeBody(t, rec, &body)
	if runtime.GOOS == "windows" {
		if body.Path != "" {
			t.Fatalf("windows root path = %q, want empty so the picker lists volumes", body.Path)
		}
		if body.Parent != "" {
			t.Fatalf("windows root parent = %q, want empty", body.Parent)
		}
		if len(body.Directories) == 0 {
			t.Fatal("windows root listing is empty, want at least one volume")
		}
		return
	}
	if body.Path != "/" {
		t.Fatalf("path = %q, want /", body.Path)
	}
	if body.Parent != "" {
		t.Fatalf("parent of / = %q, want empty so the picker cannot walk above the root", body.Parent)
	}
	if len(body.Directories) == 0 {
		t.Fatal("filesystem root listing is empty")
	}
}
