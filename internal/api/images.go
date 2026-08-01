package api

import (
	"errors"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/watzon/caravan/internal/store"
)

// imageExtensions is what GET /images will serve. The endpoint exists to show
// the posters the organizer wrote next to the media (SPEC §6); restricting it
// by extension keeps it from doubling as an unauthenticated reader of the
// whole storage root, which is a much bigger promise than the UI needs.
var imageExtensions = map[string]bool{
	".jpg":  true,
	".jpeg": true,
	".png":  true,
	".webp": true,
}

// handleImage serves an artwork file addressed by its storage-root-relative
// path — the same path the library rows carry, so the client can render
// `poster_path` without knowing where the root is.
//
// The storage root is read per request rather than captured at startup because
// re-pointing it is an instant, supported operation (SPEC §10) and the handler
// must follow it without a restart.
func (s *server) handleImage(w http.ResponseWriter, r *http.Request) {
	rel := r.PathValue("path")
	if rel == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if !imageExtensions[strings.ToLower(path.Ext(rel))] {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	root, err := s.st.GetSetting(r.Context(), store.SettingStorageRoot)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeStoreError(w, "read storage root", err)
		return
	}
	if root == "" {
		writeError(w, http.StatusNotFound, "no storage root configured")
		return
	}

	// os.Root confines every lookup below it, so a "../" segment or a symlink
	// pointing outside the storage root fails here rather than serving a file
	// the user never put in their library.
	dir, err := os.OpenRoot(root)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	defer dir.Close()

	f, err := dir.Open(rel)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	http.ServeContent(w, r, path.Base(rel), info.ModTime(), f)
}
