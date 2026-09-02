package api

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/core"
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
// The escape target sits in the storage root's *parent*, which is exactly where
// "library/../../secret.jpg" resolves once the root is prepended, so this fails
// if the handler ever goes back to joining the request path onto the root
// instead of confining it with os.Root.
//
// The dot segments are percent-encoded because http.ServeMux cleans and
// redirects a literal "/../" before any handler sees it; encoding them is how a
// real attacker gets ".." all the way to the handler.
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

// Adult artwork is the one thing GET /images will not hand to just anybody.
//
// The endpoint is auth-exempt so a television can fetch album art, and the
// paths under it are guessable: importScene writes a site's poster to <adult
// root>/<Site>/poster.jpg, derived from the site's public name. Left open, a
// 200 versus a 404 on that URL is a yes/no oracle for "is this site in this
// library", answerable with no credential at all, by any device on the LAN, and
// even with the module switched off.
func TestImagesRefuseAdultArtworkWithoutAReasonToServeIt(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newTestServer(t)
	root := t.TempDir()
	if err := st.SetSetting(ctx, store.SettingStorageRoot, root); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	writeFile(t, root, "library/Movies/Big Buck Bunny (2008)/poster.jpg", "movieposter")
	writeFile(t, root, store.AdultLibraryRoot+"/Brazzers/poster.jpg", "siteposter")

	const moviePath = "/api/v1/images/library/Movies/Big%20Buck%20Bunny%20(2008)/poster.jpg"
	const sitePath = "/api/v1/images/library/Adult/Brazzers/poster.jpg"

	get := func(target string, decorate func(*http.Request)) int {
		t.Helper()
		return doAuth(t, h, http.MethodGet, target, "", decorate).Code
	}

	createUser(t, st, testAdmin, "correct-horse", core.RoleAdmin)
	member := createUser(t, st, testMember, "correct-horse", core.RoleMember)
	adminCookie := withCookie(login(t, h, testAdmin, "correct-horse"))
	memberCookie := withCookie(login(t, h, testMember, "correct-horse"))

	// Module never enabled: there is no adult library row, so nothing changes
	// for anyone, and the site path is a 404 for the ordinary reason.
	if got := get(sitePath, nil); got != http.StatusNotFound {
		t.Errorf("anonymous adult poster with the module never enabled = %d, want 404", got)
	}

	enableAdultLibrary(t, st)

	// Televisions and the login screen still get ordinary library artwork with
	// no credential: the hole this endpoint deliberately has must stay open.
	if got := get(moviePath, nil); got != http.StatusOK {
		t.Fatalf("anonymous movie poster = %d, want 200 — the TV hole was closed", got)
	}

	for _, tc := range []struct {
		name     string
		decorate func(*http.Request)
		want     int
	}{
		{"anonymous", nil, http.StatusNotFound},
		{"ungranted member", memberCookie, http.StatusNotFound},
		{"admin", adminCookie, http.StatusOK},
	} {
		if got := get(sitePath, tc.decorate); got != tc.want {
			t.Errorf("%s adult poster = %d, want %d", tc.name, got, tc.want)
		}
	}

	// A grant opens it, which is what the SPA's Adult screens need.
	grantAdultAccess(t, st, member.ID, true)
	if got := get(sitePath, memberCookie); got != http.StatusOK {
		t.Errorf("granted member adult poster = %d, want 200", got)
	}
	grantAdultAccess(t, st, member.ID, false)

	// Case is not a way around it: APFS and NTFS would serve the same bytes
	// for a path the check spelled differently.
	if got := get("/api/v1/images/LIBRARY/adult/Brazzers/poster.jpg", nil); got != http.StatusNotFound {
		t.Errorf("anonymous adult poster by a case variant = %d, want 404", got)
	}

	// Sharing the shelf on DLNA is the owner deciding every device on the
	// network may browse it, so the television's album art works again.
	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	lib.DLNAVisible = true
	if err := st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	if got := get(sitePath, nil); got != http.StatusOK {
		t.Errorf("anonymous adult poster with the shelf shared on DLNA = %d, want 200", got)
	}

	// And switching the module off closes it for everyone, the admin and the
	// LAN included. The dlna_visible the owner left behind does not apply.
	setAdultLibrariesActive(t, st, false)
	for _, tc := range []struct {
		name     string
		decorate func(*http.Request)
	}{{"anonymous", nil}, {"member", memberCookie}, {"admin", adminCookie}} {
		if got := get(sitePath, tc.decorate); got != http.StatusNotFound {
			t.Errorf("%s adult poster with the module off = %d, want 404", tc.name, got)
		}
	}
	if got := get(moviePath, nil); got != http.StatusOK {
		t.Errorf("movie poster with the module off = %d, want 200", got)
	}
}

// Dot segments are the other way into the adult root: os.Root confines the
// lookup to the storage root, and the adult library is inside it, so a path
// that walks there through a sibling library resolves to exactly the same file.
func TestImagesRefuseAdultArtworkReachedThroughDotSegments(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newTestServer(t)
	root := t.TempDir()
	if err := st.SetSetting(ctx, store.SettingStorageRoot, root); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	writeFile(t, root, store.AdultLibraryRoot+"/Brazzers/poster.jpg", "siteposter")
	enableAdultLibrary(t, st)

	// Percent-encoded, because http.ServeMux cleans and redirects a literal
	// "/../" before any handler sees it.
	rec := do(t, h, http.MethodGet,
		"/api/v1/images/library/Movies/%2e%2e/Adult/Brazzers/poster.jpg", "")
	if rec.Code != http.StatusNotFound {
		t.Errorf("adult poster through a dot segment = %d, want 404", rec.Code)
	}
}
