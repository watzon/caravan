package dlna

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// hideLibrary turns off dlna_visible for one library kind, the way the
// Libraries settings screen does.
func hideLibrary(t *testing.T, st *store.Store, kind string) {
	t.Helper()
	ctx := context.Background()
	lib, err := st.GetLibraryByKind(ctx, kind)
	if err != nil {
		t.Fatalf("GetLibraryByKind(%q): %v", kind, err)
	}
	lib.DLNAVisible = false
	if err := st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary(%q): %v", kind, err)
	}
}

// The acceptance for PLAN phase 8 task 6: a library the owner stopped sharing
// leaves the content tree, and every other library is untouched.
func TestBrowseRootDropsHiddenLibrary(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	ctx := context.Background()

	hideLibrary(t, st, core.LibraryKindTV)

	got, err := svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	if ids := containerIDs(got); len(ids) != 1 || ids[0] != moviesID {
		t.Fatalf("root children = %v, want [movies] only", ids)
	}

	// Sharing it again puts the container back: the flag is a switch, not a
	// one-way door.
	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	lib.DLNAVisible = true
	if err := st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	got, err = svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root) after re-enable: %v", err)
	}
	if ids := containerIDs(got); len(ids) != 2 || ids[0] != moviesID || ids[1] != tvID {
		t.Fatalf("root children = %v, want [movies tv] again", ids)
	}
}

// Dropping the container from the root is not enough on its own: a client that
// cached an object id keeps browsing straight past it. The whole subtree has to
// answer "no such object".
func TestBrowseHiddenLibrarySubtreeIsNoSuchObject(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	ctx := context.Background()

	// s:1 is the seeded series, s:1:1 its first season, e:1:2 an episode item.
	subtree := []string{tvID, "s:1", "s:1:1", "e:1:2"}
	// Asserted while the library is still shared, so the 701s below are the
	// toggle's doing and not four ids that never resolved.
	for _, objectID := range subtree {
		if _, err := svc.metadata(ctx, testURLs, objectID); err != nil {
			t.Fatalf("metadata(%q) while visible: %v", objectID, err)
		}
	}

	hideLibrary(t, st, core.LibraryKindTV)

	for _, objectID := range subtree {
		if _, err := svc.children(ctx, testURLs, objectID); !errors.Is(err, errNoObject) {
			t.Errorf("children(%q) err = %v, want errNoObject", objectID, err)
		}
		if _, err := svc.metadata(ctx, testURLs, objectID); !errors.Is(err, errNoObject) {
			t.Errorf("metadata(%q) err = %v, want errNoObject", objectID, err)
		}
		if _, err := svc.search(ctx, testURLs, objectID, "*"); !errors.Is(err, errNoObject) {
			t.Errorf("search(%q) err = %v, want errNoObject", objectID, err)
		}
	}

	// The movie library is provably unaffected.
	movies, err := svc.children(ctx, testURLs, moviesID)
	if err != nil {
		t.Fatalf("children(movies): %v", err)
	}
	if len(movies.Items) != 1 {
		t.Fatalf("movies = %+v, want the one seeded movie", movies.Items)
	}
}

// Search is how library-style clients enumerate a server, so a hidden library
// must not come back through it either.
func TestSearchRootSkipsHiddenLibrary(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	ctx := context.Background()

	hideLibrary(t, st, core.LibraryKindMovie)

	got, err := svc.search(ctx, testURLs, rootID, "*")
	if err != nil {
		t.Fatalf("search(root): %v", err)
	}
	for _, it := range got.Items {
		if strings.HasPrefix(it.ID, movieItemPrefix) {
			t.Errorf("search returned movie item %q from a hidden library", it.ID)
		}
	}
	for _, c := range got.Containers {
		if c.ID == moviesID {
			t.Error("search returned the Movies container from a hidden library")
		}
	}
	// The TV library is still fully enumerable.
	if len(got.Items) == 0 {
		t.Fatal("search returned nothing at all, want the tv library's episodes")
	}
}

// A television caches the tree against SystemUpdateID. If the counter stands
// still while a library leaves the tree, the TV keeps showing a shelf the
// server no longer serves.
func TestSystemUpdateIDChangesWhenVisibilityToggles(t *testing.T) {
	svc, st, _ := newTestService(t)
	ctx := context.Background()

	before, err := svc.systemUpdateID(ctx)
	if err != nil {
		t.Fatalf("systemUpdateID: %v", err)
	}
	if before != defaultSystemUpdateID {
		t.Fatalf("systemUpdateID = %q on a fresh install, want %q", before, defaultSystemUpdateID)
	}

	hideLibrary(t, st, core.LibraryKindMovie)

	after, err := svc.systemUpdateID(ctx)
	if err != nil {
		t.Fatalf("systemUpdateID: %v", err)
	}
	if after == before {
		t.Fatalf("systemUpdateID stayed %q across a visibility toggle", after)
	}

	// And it moves again on the way back, so a client that re-cached in between
	// is not left holding the value it already has.
	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	lib.DLNAVisible = true
	if err := st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}
	third, err := svc.systemUpdateID(ctx)
	if err != nil {
		t.Fatalf("systemUpdateID: %v", err)
	}
	if third == after || third == before {
		t.Fatalf("systemUpdateID = %q after the second toggle, want a new value (was %q, %q)", third, before, after)
	}
}

// Writing a library without touching dlna_visible must not move the counter:
// a counter that changes for unrelated edits makes every client re-browse the
// whole library for nothing.
func TestSystemUpdateIDHoldsWhenVisibilityIsUnchanged(t *testing.T) {
	svc, st, _ := newTestService(t)
	ctx := context.Background()

	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	lib.RouteTorrent = store.RouteEmbedded
	if err := st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary: %v", err)
	}

	got, err := svc.systemUpdateID(ctx)
	if err != nil {
		t.Fatalf("systemUpdateID: %v", err)
	}
	if got != defaultSystemUpdateID {
		t.Fatalf("systemUpdateID = %q after a routing edit, want it unmoved at %q", got, defaultSystemUpdateID)
	}
}

// The value Browse reports is the one the counter holds, not a constant that
// happens to match it on a fresh install.
func TestBrowseReportsCurrentSystemUpdateID(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	h := svc.Handler()

	hideLibrary(t, st, core.LibraryKindTV)
	want, err := svc.systemUpdateID(context.Background())
	if err != nil {
		t.Fatalf("systemUpdateID: %v", err)
	}
	if want == defaultSystemUpdateID {
		t.Fatalf("the toggle did not move the counter off %q", defaultSystemUpdateID)
	}

	for _, tc := range []struct{ action, body string }{
		{"Browse", browseBody(rootID, browseDirectChildren, 0, 0)},
		{"Search", searchBody(rootID, "*", 0, 0)},
	} {
		rec := soapPost(t, h, MountPath+"/control/cds", contentDirectoryType+"#"+tc.action, tc.body)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, body = %s", tc.action, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "<UpdateID>"+want+"</UpdateID>") {
			t.Errorf("%s UpdateID is not %q:\n%s", tc.action, want, rec.Body.String())
		}
	}

	rec := soapPost(t, h, MountPath+"/control/cds", contentDirectoryType+"#GetSystemUpdateID", "")
	if !strings.Contains(rec.Body.String(), "<Id>"+want+"</Id>") {
		t.Errorf("GetSystemUpdateID is not %q:\n%s", want, rec.Body.String())
	}
}
