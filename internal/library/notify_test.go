package library

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// stubNotifier stands in for the playback handoff (internal/jellyfin). It
// counts notifications rather than making a call, because what the import
// pipeline owes is exactly one notification per successful batch — whether that
// turns into a Jellyfin scan is the handoff's business.
type stubNotifier struct {
	calls int
	err   error
}

func (n *stubNotifier) LibraryChanged(context.Context) error {
	n.calls++
	return n.err
}

// notifyingHarness is newHarness with the manager wired to a stub notifier.
func notifyingHarness(t *testing.T, notify Notifier) *harness {
	t.Helper()
	h := newHarness(t)
	h.mgr.notify = notify
	return h
}

// TestImportDownloadNotifiesOnSuccess is PLAN phase 4 task 1's acceptance
// criterion at the pipeline seam: an import that landed files tells the
// playback handoff about it.
func TestImportDownloadNotifiesOnSuccess(t *testing.T) {
	notify := &stubNotifier{}
	h := notifyingHarness(t, notify)
	mv := addMovieItem(h)
	dl := movieDownload(h, "movie bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP"})

	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	if notify.calls != 1 {
		t.Fatalf("notifications = %d, want 1 after a successful import", notify.calls)
	}
}

// A season pack is one download and therefore one notification, not one per
// episode: the handoff's job is "the library changed", not "this file changed".
func TestImportDownloadNotifiesOncePerBatch(t *testing.T) {
	notify := &stubNotifier{}
	h := notifyingHarness(t, notify)
	sr := addSeriesItem(h)

	const dir = "incomplete/Planet.Earth.II.S01.1080p.WEB-DL.x265"
	for name, parsed := range map[string]core.ParsedRelease{
		"Planet.Earth.II.S01E01.1080p.WEB-DL.x265.mkv": episodeParse("Planet Earth II", 1, 1),
		"Planet.Earth.II.S01E02.1080p.WEB-DL.x265.mkv": episodeParse("Planet Earth II", 1, 2),
	} {
		h.parser[name] = parsed
		h.writeVideo(dir+"/"+name, "bytes of "+name)
	}
	dl := core.DownloadStatus{ID: "infohash-pack", State: core.DownloadSeeding, SavePath: dir}
	grab := h.grabFor(core.GrabInfo{
		SeriesID:   sr.ID,
		SeasonNum:  1,
		EpisodeIDs: []int64{episodeID(h, sr.ID, 1, 1), episodeID(h, sr.ID, 1, 2)},
	})

	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	if notify.calls != 1 {
		t.Fatalf("notifications = %d, want 1 for a multi-file download", notify.calls)
	}
}

// TestImportDownloadDoesNotNotifyOnFailure is the other half of the criterion:
// a download whose files all parked changed nothing on disk, so there is
// nothing for a media server to rescan.
func TestImportDownloadDoesNotNotifyOnFailure(t *testing.T) {
	notify := &stubNotifier{}
	h := notifyingHarness(t, notify)
	mv := addMovieItem(h)

	const (
		dir  = "incomplete/Some.Other.Movie.2019.1080p"
		file = dir + "/Some.Other.Movie.2019.1080p.mkv"
	)
	h.parser[filepath.Base(file)] = movieParse("Some Other Movie", 2019)
	h.writeVideo(file, "not the movie we asked for")
	dl := core.DownloadStatus{ID: "infohash-other", State: core.DownloadCompleted, SavePath: dir}
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny.2008"})

	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusFailed {
		t.Fatalf("grab status = %q, want %q — the fixture is not exercising a failed import", got, core.GrabStatusFailed)
	}
	if notify.calls != 0 {
		t.Fatalf("notifications = %d, want 0 when nothing was imported", notify.calls)
	}
}

// A redelivered job short-circuits on the already-imported grab, so it must not
// notify again either (SPEC §7: at-least-once delivery, idempotent handlers).
func TestImportDownloadDoesNotNotifyOnRedelivery(t *testing.T) {
	notify := &stubNotifier{}
	h := notifyingHarness(t, notify)
	mv := addMovieItem(h)
	dl := movieDownload(h, "movie bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny"})

	ctx := context.Background()
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("first ImportDownload: %v", err)
	}
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("second ImportDownload: %v", err)
	}
	if notify.calls != 1 {
		t.Fatalf("notifications = %d, want 1 across a redelivered job", notify.calls)
	}
}

// A manual match puts a file in the library exactly as a download import does,
// so it owes the same notification.
func TestImportUnmatchedNotifies(t *testing.T) {
	notify := &stubNotifier{}
	h := notifyingHarness(t, notify)
	seedMovie(h)
	h.parser["Big.Buck.Bunny.2008.mkv"] = movieParse("Big Buck Bunny", 2008)
	h.writeVideo("inbox/Big.Buck.Bunny.2008.mkv", "movie bytes")
	u := &core.UnmatchedFile{Path: "inbox/Big.Buck.Bunny.2008.mkv", Size: 11, Reason: "no match"}
	if err := h.st.UpsertUnmatchedFile(context.Background(), u); err != nil {
		t.Fatalf("UpsertUnmatchedFile: %v", err)
	}

	if _, err := h.mgr.ImportUnmatched(context.Background(), u.ID, 10378, MediaTypeMovie); err != nil {
		t.Fatalf("ImportUnmatched: %v", err)
	}
	if notify.calls != 1 {
		t.Fatalf("notifications = %d, want 1 after a manual match", notify.calls)
	}
}

// The handoff is best-effort: an import must not fail because the notification
// could not be recorded, but the user must still be told it did not happen.
func TestNotifierFailureDoesNotFailTheImport(t *testing.T) {
	notify := &stubNotifier{err: errors.New("queue is on fire")}
	h := notifyingHarness(t, notify)
	mv := addMovieItem(h)
	dl := movieDownload(h, "movie bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny"})

	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusImported {
		t.Errorf("grab status = %q, want %q", got, core.GrabStatusImported)
	}
	if !h.exists(organizedRel) {
		t.Errorf("%s was not imported", organizedRel)
	}

	var warned bool
	for _, ev := range h.events() {
		if ev.Level == core.EventLevelWarn && ev.Detail == "queue is on fire" {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("events = %+v, want a warning naming the handoff failure", h.events())
	}
}

// Without a handoff configured the pipeline simply does not notify — the nil
// check is load-bearing, not defensive decoration.
func TestImportWithoutANotifier(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	dl := movieDownload(h, "movie bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny"})

	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
}
