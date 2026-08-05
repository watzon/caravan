package web

import (
	"io/fs"
	"strings"
	"testing"
)

// The API server serves the SPA from DistFS, so index.html must be reachable
// without the "dist/" prefix and must look like a real Vite build. CI builds
// the ignored dist tree before running this test.
func TestDistFSServesIndex(t *testing.T) {
	data, err := fs.ReadFile(DistFS(), "index.html")
	if err != nil {
		t.Fatalf("read index.html from DistFS: %v", err)
	}
	index := string(data)
	for _, marker := range []string{"<html", `<div id="app"></div>`, "/assets/"} {
		if !strings.Contains(index, marker) {
			t.Errorf("index.html does not contain %q: %q", marker, index)
		}
	}
}

func TestDistEmbedsUnderDistPrefix(t *testing.T) {
	if _, err := fs.ReadFile(Dist, "dist/index.html"); err != nil {
		t.Fatalf("read dist/index.html from Dist: %v", err)
	}
}
