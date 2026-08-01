package web

import (
	"io/fs"
	"strings"
	"testing"
)

// The API server serves the SPA from DistFS, so index.html must be reachable
// without the "dist/" prefix. A placeholder is committed precisely so this
// holds on a checkout where the SPA was never built.
func TestDistFSServesIndex(t *testing.T) {
	data, err := fs.ReadFile(DistFS(), "index.html")
	if err != nil {
		t.Fatalf("read index.html from DistFS: %v", err)
	}
	if !strings.Contains(string(data), "<html") {
		t.Errorf("index.html does not look like HTML: %q", string(data))
	}
}

func TestDistEmbedsUnderDistPrefix(t *testing.T) {
	if _, err := fs.ReadFile(Dist, "dist/index.html"); err != nil {
		t.Fatalf("read dist/index.html from Dist: %v", err)
	}
}
