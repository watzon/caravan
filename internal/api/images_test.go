package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/store"
)

// writeFile creates name (with parents) under dir and returns nothing; a
// failure fails the test.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
}

func TestImageServesPosterFromStorageRoot(t *testing.T) {
	h, st, _ := newTestServer(t)
	root := t.TempDir()

	if err := st.SetSetting(context.Background(), store.SettingStorageRoot, root); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	writeFile(t, root, "library/Movies/Big Buck Bunny (2008)/poster.jpg", "jpegbytes")

	rec := do(t, h, http.MethodGet,
		"/api/v1/images/library/Movies/Big%20Buck%20Bunny%20(2008)/poster.jpg", "")
	wantStatus(t, rec, http.StatusOK)
	if got := rec.Body.String(); got != "jpegbytes" {
		t.Fatalf("body = %q, want the poster bytes", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "image/jpeg" {
		t.Fatalf("Content-Type = %q, want image/jpeg", got)
	}
}

// TestImageRefusesEscapingTheStorageRoot is the traversal regression.
//
// The escape target sits in the storage root's *parent*, which is exactly
// where "library/../../secret.jpg" resolves once the root is prepended — so
// this fails if the handler ever goes back to joining the request path onto
// the root instead of confining it with os.Root.
//
// The dot segments are percent-encoded because http.ServeMux cleans and
// redirects a literal "/../" before any handler sees it; encoding them is how
// a real attacker gets ".." all the way to the handler.
func TestImageRefusesEscapingTheStorageRoot(t *testing.T) {
	h, st, _ := newTestServer(t)
	root := t.TempDir()

	if err := st.SetSetting(context.Background(), store.SettingStorageRoot, root); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	writeFile(t, filepath.Dir(root), "secret.jpg", "not yours")

	rec := do(t, h, http.MethodGet, "/api/v1/images/library/%2e%2e/%2e%2e/secret.jpg", "")
	if rec.Code == http.StatusOK {
		t.Fatalf("status = 200, want a refusal (body %q)", rec.Body.String())
	}
	if got := rec.Body.String(); got == "not yours" {
		t.Fatal("served a file outside the storage root")
	}
}

func TestImageRefusesNonFiles(t *testing.T) {
	h, st, _ := newTestServer(t)
	root := t.TempDir()

	if err := st.SetSetting(context.Background(), store.SettingStorageRoot, root); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	writeFile(t, root, "library/Movies/BBB/movie.nfo", "<movie/>")
	// A directory that looks like an image, to exercise the regular-file guard.
	if err := os.MkdirAll(filepath.Join(root, "library", "Movies", "folder.jpg"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	tests := []struct {
		name string
		path string
	}{
		{"non-image extension", "/api/v1/images/library/Movies/BBB/movie.nfo"},
		{"missing file", "/api/v1/images/library/Movies/Nope/poster.jpg"},
		{"directory", "/api/v1/images/library/Movies/folder.jpg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, tt.path, "")
			wantStatus(t, rec, http.StatusNotFound)
			wantErrorBody(t, rec)
		})
	}
}

func TestImageWithoutStorageRootIs404(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/images/library/Movies/x/poster.jpg", "")
	wantStatus(t, rec, http.StatusNotFound)
	wantErrorBody(t, rec)
}
