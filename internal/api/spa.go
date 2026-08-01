package api

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// indexFile is the SPA entry point and the fallback for unknown paths.
const indexFile = "index.html"

// handleSPA serves the embedded Svelte build. A request for a path that is not
// a file in the bundle falls back to index.html so client-side routes survive
// a reload or a deep link; requests under /api are routed elsewhere and never
// reach here.
func (s *server) handleSPA(w http.ResponseWriter, r *http.Request) {
	if s.dist == nil {
		writeError(w, http.StatusNotFound, "no web UI bundled")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
	if name == "" || name == "." {
		name = indexFile
	}
	if !fileExists(s.dist, name) {
		name = indexFile
		if !fileExists(s.dist, name) {
			writeError(w, http.StatusNotFound, "no web UI bundled")
			return
		}
	}
	http.ServeFileFS(w, r, s.dist, name)
}

// fileExists reports whether name is a regular file in fsys. Directories are
// not servable here: the SPA has no directory listings.
func fileExists(fsys fs.FS, name string) bool {
	if !fs.ValidPath(name) {
		return false
	}
	info, err := fs.Stat(fsys, name)
	return err == nil && info.Mode().IsRegular()
}
