package api

import (
	"errors"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/watzon/caravan/internal/core"
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
	allowed, err := s.imageAllowed(r, rel)
	if err != nil {
		s.writeStoreError(w, "read libraries", err)
		return
	}
	if !allowed {
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

// imageAllowed decides whether this caller may be served the artwork at rel.
//
// EVERY library root is checked, not the adult ones alone: any library can now
// be switched off or narrowed to a few accounts, and its posters have to go
// with it. A path under no library root at all is ordinary artwork and stays
// open, which is what keeps the deliberate hole in authExempt open for the
// televisions it was cut for.
//
// The reason this endpoint has a rule at all is the path itself. importScene
// writes a site's poster to <adult root>/<Site>/poster.jpg, derived from the
// site's PUBLIC name — so an unauthenticated request for
// library/Adult/Brazzers/poster.jpg is a yes/no oracle for "is this site in this
// library", answerable by any device on the LAN and by an ungranted housemate.
// That is the one such surface reachable with no credential at all.
func (s *server) imageAllowed(r *http.Request, rel string) (bool, error) {
	libs, err := s.st.ListLibraries(r.Context())
	if err != nil {
		return false, err
	}
	hasAdult := false
	for _, lib := range libs {
		if lib.Kind == core.LibraryKindAdult {
			hasAdult = true
			break
		}
	}
	if !hasAdult {
		// No adult library, so there is no row to read a root from. The seed
		// root is a constant, though, and anything sitting under it on such an
		// install is not artwork this endpoint should hand out either — a
		// leftover from before somebody deleted the row by hand, say. The
		// synthetic row is inactive, so artworkVisible refuses it for
		// everyone: a shelf that is not there shows nobody anything.
		libs = append(libs, core.Library{RootPath: store.AdultLibraryRoot})
	}
	for _, lib := range libs {
		if underRoot(rel, lib.RootPath) {
			return s.artworkVisible(r, lib)
		}
	}
	return true, nil
}

// artworkVisible answers "may this request see this library's artwork" on the
// two honest grounds it can be true:
//
//   - the caller may see the library itself (core.LibraryVisible) — this is
//     every SPA screen, whose posters come through this endpoint;
//   - the library is advertised on DLNA, which is the owner having already
//     decided every device on the network may browse it. That is the same
//     decision that put the hole in authExempt in the first place, so honouring
//     it here keeps a television's album art working without widening anything
//     the owner did not widen.
//
// An INACTIVE library has neither ground: it is dormant for everyone, the admin
// included, and the dlna_visible the owner left behind does not apply to a shelf
// that is switched off.
//
// The identity comes from resolveUser rather than currentUser: this route is
// auth-exempt, so no middleware ran and currentUser's fallback would report an
// implicit admin for an anonymous caller. A caller with no credential is read as
// an account with no grants, which is why an active, unrestricted library's
// artwork stays open to a television and a restricted one's does not.
func (s *server) artworkVisible(r *http.Request, lib core.Library) (bool, error) {
	if !lib.Active {
		return false, nil
	}
	user, ok, err := s.resolveUser(r)
	if err != nil {
		return false, err
	}
	if !ok {
		user = requestUser{Role: core.RoleMember}
	}
	visible, err := s.gateFor(user).allows(r.Context(), lib)
	if err != nil {
		return false, err
	}
	if visible {
		return true, nil
	}
	return lib.DLNAVisible, nil
}

// underRoot reports whether the storage-root-relative path rel names root or
// something inside it.
//
// The comparison is case-insensitive because the filesystems Caravan runs on
// mostly are (APFS and NTFS by default): a check that only refused
// "library/Adult/..." would hand back the same bytes for "library/adult/...".
func underRoot(rel, root string) bool {
	if root == "" {
		return false
	}
	// Clean first, or "library/Movies/../Adult/poster.jpg" would walk straight
	// past the prefix check — os.Root would then happily serve it, because the
	// file really is inside the storage root.
	clean := strings.ToLower(path.Clean("/" + rel))
	base := strings.ToLower(path.Clean("/" + root))
	return clean == base || strings.HasPrefix(clean, base+"/")
}
