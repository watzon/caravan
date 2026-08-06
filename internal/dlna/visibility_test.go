package dlna

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
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

// setLibraryVisible flips one library's dlna_visible by id, which is what the
// Libraries screen's Reach card does for a library that is not its kind's
// default.
func setLibraryVisible(t *testing.T, st *store.Store, id int64, visible bool) {
	t.Helper()
	ctx := context.Background()
	lib, err := st.GetLibrary(ctx, id)
	if err != nil {
		t.Fatalf("GetLibrary(%d): %v", id, err)
	}
	lib.DLNAVisible = visible
	if err := st.UpdateLibrary(ctx, lib); err != nil {
		t.Fatalf("UpdateLibrary(%d): %v", id, err)
	}
}

// seedSecondTVLibrary adds a second television library, shared over DLNA, with
// one show of its own and one playable file on disk. It is the case a tree
// keyed by KIND could not tell apart from the default library's shows.
func seedSecondTVLibrary(t *testing.T, st *store.Store, root string) (core.Library, *core.Series, *core.MediaFile) {
	t.Helper()
	ctx := context.Background()

	lib := &core.Library{
		Kind: core.LibraryKindTV, Name: "Anime", RootPath: "library/Anime",
		Provider: core.ProviderTMDB, DLNAVisible: true,
	}
	if err := st.CreateLibrary(ctx, lib); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	series := &core.Series{
		TMDBID: 209867, Title: "Frieren", SortTitle: "frieren", Year: 2023,
		Path: "library/Anime/Frieren (2023)", LibraryID: lib.ID,
	}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := st.UpsertSeason(ctx, &core.Season{SeriesID: series.ID, Number: 1}); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	episode := &core.Episode{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "The Journey's End"}
	if err := st.UpsertEpisode(ctx, episode); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	rel := "library/Anime/Frieren (2023)/Season 01/Frieren (2023) - S01E01.mkv"
	writeMedia(t, root, rel, []byte("anime-media-bytes"))
	file := &core.MediaFile{Path: rel, Size: 17}
	if err := st.UpsertMediaFile(ctx, file); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	if err := st.LinkEpisodeFile(ctx, episode.ID, file.ID); err != nil {
		t.Fatalf("LinkEpisodeFile: %v", err)
	}
	return *lib, series, file
}

// The acceptance for PLAN phase A8: a second library of a kind that already
// has one gets a container of its own, holding its own rows and nobody else's.
func TestSecondLibraryGetsItsOwnContainer(t *testing.T) {
	svc, st, root := newTestService(t)
	seedLibrary(t, st)
	anime, series, _ := seedSecondTVLibrary(t, st, root)
	ctx := context.Background()

	wantID := libraryPrefix + strconv.FormatInt(anime.ID, 10)
	rootDoc, err := svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	ids := containerIDs(rootDoc)
	if len(ids) != 3 || ids[0] != moviesID || ids[1] != tvID || ids[2] != wantID {
		t.Fatalf("root children = %v, want [movies tv %s]", ids, wantID)
	}
	// The non-default library is named after the row; the default keeps the
	// inherited title so existing clients see the shelf they already have.
	titles := containerTitles(rootDoc)
	if titles[1] != "TV" || titles[2] != "Anime" {
		t.Fatalf("root titles = %v, want the legacy TV title and the row's name", titles)
	}

	// Its own container holds its show and only its show.
	kids, err := svc.children(ctx, testURLs, wantID)
	if err != nil {
		t.Fatalf("children(%s): %v", wantID, err)
	}
	if len(kids.Containers) != 1 || kids.Containers[0].Title != "Frieren (2023)" {
		t.Fatalf("%s children = %v, want only its own show", wantID, containerTitles(kids))
	}
	// And a series container's parent is its OWN library's container, so a
	// BrowseMetadata agrees with the browse that handed the id out.
	if kids.Containers[0].ParentID != wantID {
		t.Fatalf("series parentID = %q, want %q", kids.Containers[0].ParentID, wantID)
	}

	// The default TV container no longer answers for it.
	tv, err := svc.children(ctx, testURLs, tvID)
	if err != nil {
		t.Fatalf("children(tv): %v", err)
	}
	if len(tv.Containers) != 1 || tv.Containers[0].Title != "Planet Earth II (2016)" {
		t.Fatalf("tv children = %v, want only the default library's show", containerTitles(tv))
	}
	// The advertised child counts say the same thing, so a television that
	// caches them is not told a shelf holds more than it can browse.
	if rootDoc.Containers[1].ChildCount != 1 || rootDoc.Containers[2].ChildCount != 1 {
		t.Fatalf("root child counts = %d, %d, want one show each",
			rootDoc.Containers[1].ChildCount, rootDoc.Containers[2].ChildCount)
	}

	// Search exposure matches browse exposure: the root search reaches the new
	// library, and a search scoped to the default one does not.
	found, err := svc.search(ctx, testURLs, rootID, `dc:title contains "Frieren"`)
	if err != nil {
		t.Fatalf("search(root): %v", err)
	}
	if len(found.Containers) == 0 && len(found.Items) == 0 {
		t.Error("the second library is browsable but not searchable from the root")
	}
	scoped, err := svc.search(ctx, testURLs, tvID, `dc:title contains "Frieren"`)
	if err != nil {
		t.Fatalf("search(tv): %v", err)
	}
	if len(scoped.Containers) != 0 || len(scoped.Items) != 0 {
		t.Errorf("a search under the default TV container reached the second library: %+v", scoped)
	}

	// The series id is still spelled "s:<id>": the id space did not change, only
	// which container the row hangs under.
	if _, err := svc.metadata(ctx, testURLs, tvIDSpace.seriesObjectID(series.ID)); err != nil {
		t.Fatalf("metadata(s:%d): %v", series.ID, err)
	}
}

// Hiding one library of a kind must take its whole subtree off the LAN while
// its sibling stays browsable — the thing a tree keyed by kind could not do.
func TestHidingOneLibraryLeavesItsSiblingBrowsable(t *testing.T) {
	svc, st, root := newTestService(t)
	seedLibrary(t, st)
	anime, series, file := seedSecondTVLibrary(t, st, root)
	ctx := context.Background()

	containerID := libraryPrefix + strconv.FormatInt(anime.ID, 10)
	seriesID := tvIDSpace.seriesObjectID(series.ID)
	// Asserted while it is still shared, so the refusals below are the toggle's
	// doing and not ids that never resolved.
	for _, objectID := range []string{containerID, seriesID, tvIDSpace.seasonObjectID(series.ID, 1)} {
		if _, err := svc.metadata(ctx, testURLs, objectID); err != nil {
			t.Fatalf("metadata(%q) while visible: %v", objectID, err)
		}
	}
	if rec := requestDirectMedia(t, svc, file.ID); rec.Code != http.StatusOK {
		t.Fatalf("media while visible: status = %d", rec.Code)
	}

	setLibraryVisible(t, st, anime.ID, false)

	rootDoc, err := svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	if ids := containerIDs(rootDoc); len(ids) != 2 || ids[0] != moviesID || ids[1] != tvID {
		t.Fatalf("root children = %v, want the hidden library gone", ids)
	}
	// Every cached id under it, container and row alike, answers 701.
	for _, objectID := range []string{containerID, seriesID, tvIDSpace.seasonObjectID(series.ID, 1)} {
		if _, err := svc.children(ctx, testURLs, objectID); !errors.Is(err, errNoObject) {
			t.Errorf("children(%q) = %v, want errNoObject", objectID, err)
		}
		if _, err := svc.metadata(ctx, testURLs, objectID); !errors.Is(err, errNoObject) {
			t.Errorf("metadata(%q) = %v, want errNoObject", objectID, err)
		}
		if _, err := svc.search(ctx, testURLs, objectID, "*"); !errors.Is(err, errNoObject) {
			t.Errorf("search(%q) = %v, want errNoObject", objectID, err)
		}
	}
	// A cached media URL stops playing too: the gate is the owning library, not
	// the owning kind.
	if rec := requestDirectMedia(t, svc, file.ID); rec.Code != http.StatusNotFound {
		t.Errorf("media from a hidden library: status = %d, want 404", rec.Code)
	}

	// The default television library is provably untouched.
	tv, err := svc.children(ctx, testURLs, tvID)
	if err != nil {
		t.Fatalf("children(tv): %v", err)
	}
	if len(tv.Containers) != 1 || tv.Containers[0].Title != "Planet Earth II (2016)" {
		t.Fatalf("tv children = %v, want the default library's show", containerTitles(tv))
	}
	if _, err := svc.metadata(ctx, testURLs, "s:1"); err != nil {
		t.Fatalf("metadata(s:1) in the still-shared library: %v", err)
	}
}

// The adult AND-rule is per library, not per kind: with the module off EVERY
// adult library is absent, however many there are and whatever each one's own
// dlna_visible remembers.
func TestModuleOffHidesEveryAdultLibrary(t *testing.T) {
	svc, st, _ := newTestService(t)
	seedLibrary(t, st)
	seedSite(t, st)
	showAdultOnDLNA(t, st, true)
	ctx := context.Background()

	second := &core.Library{
		Kind: core.LibraryKindAdult, Name: "Studios", RootPath: "library/Studios",
		Provider: core.ProviderStashbox, DLNAVisible: true,
	}
	if err := st.CreateLibrary(ctx, second); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	secondID := libraryPrefix + strconv.FormatInt(second.ID, 10)

	rootDoc, err := svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	ids := containerIDs(rootDoc)
	if !slices.Contains(ids, adultID) || !slices.Contains(ids, secondID) {
		t.Fatalf("root children = %v, want both adult containers while the module is on", ids)
	}

	if err := st.SetAdultEnabled(ctx, false); err != nil {
		t.Fatalf("SetAdultEnabled(false): %v", err)
	}

	rootDoc, err = svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	for _, id := range containerIDs(rootDoc) {
		if id == adultID || id == secondID {
			t.Errorf("the root still advertises %q with the module off", id)
		}
	}
	for _, objectID := range []string{adultID, secondID} {
		if _, err := svc.children(ctx, testURLs, objectID); !errors.Is(err, errNoObject) {
			t.Errorf("children(%q) with the module off = %v, want errNoObject", objectID, err)
		}
	}

	// And both come back on, because disabling remembers rather than unshares.
	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled(true): %v", err)
	}
	rootDoc, err = svc.children(ctx, testURLs, rootID)
	if err != nil {
		t.Fatalf("children(root): %v", err)
	}
	ids = containerIDs(rootDoc)
	if !slices.Contains(ids, adultID) || !slices.Contains(ids, secondID) {
		t.Fatalf("root children = %v, want both adult containers back", ids)
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
