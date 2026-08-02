package library

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// externalDownload writes a finished payload into a directory *outside* the
// storage root and returns the status an external client reports for it: an
// absolute path on the client's own filesystem, which is the one place a
// foreign path is allowed to exist (PLAN phase 6 task 2).
func externalDownload(h *harness, content string) (core.DownloadStatus, string) {
	h.t.Helper()
	dir := filepath.Join(h.t.TempDir(), "completed", "Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		h.t.Fatalf("mkdir %s: %v", dir, err)
	}
	file := filepath.Join(dir, "Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP.mkv")
	if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
		h.t.Fatalf("write %s: %v", file, err)
	}
	return core.DownloadStatus{
		ID:       "nzo_external",
		State:    core.DownloadCompleted,
		Name:     filepath.Base(dir),
		Progress: 1,
		SavePath: dir,
		Engine:   "sabnzbd",
	}, file
}

// A download an external client wrote outside the storage root still imports:
// the payload is read from the client's own absolute path, and the copy the
// client keeps is left alone so a torrent can go on seeding.
func TestImportDownloadFromAnExternalClientPath(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	dl, payload := externalDownload(h, "external bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP"})

	ctx := context.Background()
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	if got := h.read(organizedRel); got != "external bytes" {
		t.Fatalf("imported content = %q, want %q", got, "external bytes")
	}
	if _, err := os.Stat(payload); err != nil {
		t.Fatalf("the client's own copy at %s was consumed: %v", payload, err)
	}
	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusImported {
		t.Fatalf("grab status = %q, want %q", got, core.GrabStatusImported)
	}
}

// The one rule the library never bends: nothing it stores may name a path
// outside the storage root, however the file got there (SPEC §1.2 pillar 3).
func TestImportFromExternalClientStoresNoForeignPath(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	dl, payload := externalDownload(h, "external bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP"})

	ctx := context.Background()
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	files, err := h.st.ListMediaFiles(ctx)
	if err != nil {
		t.Fatalf("ListMediaFiles: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("media files = %d, want 1", len(files))
	}
	if filepath.IsAbs(files[0].Path) || strings.HasPrefix(files[0].Path, "..") {
		t.Fatalf("media_files.path = %q, want a storage-root-relative path", files[0].Path)
	}
	if _, err := os.Stat(payload); err != nil {
		t.Fatalf("the client's own copy is gone: %v", err)
	}
}

// An external client's directory is usually on another filesystem, so the
// hardlink the embedded engine gets is not available. The organize engine's
// existing copy fallback has to carry it — and, critically, still leave the
// source where the client put it.
func TestImportFromExternalClientCopiesWhenHardlinksAreUnavailable(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	dl, payload := externalDownload(h, "cross device bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP"})

	// Stand in for a cross-device link: os.Link is the only thing a second
	// filesystem would change about this import.
	h.mgr.link = func(string, string) error { return os.ErrInvalid }

	ctx := context.Background()
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	if got := h.read(organizedRel); got != "cross device bytes" {
		t.Fatalf("imported content = %q, want %q", got, "cross device bytes")
	}
	source, err := os.Stat(payload)
	if err != nil {
		t.Fatalf("the client's own copy at %s was consumed: %v", payload, err)
	}
	imported, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(organizedRel)))
	if err != nil {
		t.Fatalf("stat imported file: %v", err)
	}
	if os.SameFile(source, imported) {
		t.Fatal("the import hardlinked; this test is meant to exercise the copy fallback")
	}
}

// The v1 constraint, made visible. A client that reports a directory Caravan
// cannot open gets one explanatory failure, not a job that retries forever
// against a path that will never appear.
func TestImportFromAnUnreachableExternalPathFailsVisibly(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP"})
	missing := filepath.Join(t.TempDir(), "not-mounted", "Big.Buck.Bunny.2008")
	dl := core.DownloadStatus{
		ID:       "nzo_missing",
		State:    core.DownloadCompleted,
		Name:     "Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP",
		Progress: 1,
		SavePath: missing,
		Engine:   "sabnzbd",
	}

	ctx := context.Background()
	// Not an error: an error is what makes the import job retry, and the whole
	// point is that this one never will succeed on its own.
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v, want the failure to be recorded rather than returned", err)
	}

	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusFailed {
		t.Fatalf("grab status = %q, want %q", got, core.GrabStatusFailed)
	}
	events := h.events()
	if len(events) == 0 {
		t.Fatal("no activity-feed entry for an unreadable download")
	}
	got := events[0]
	if got.Level != core.EventLevelWarn {
		t.Fatalf("event level = %q, want %q", got.Level, core.EventLevelWarn)
	}
	if !strings.Contains(got.Detail, ImportPathConstraint) {
		t.Fatalf("event detail = %q, want it to state %q", got.Detail, ImportPathConstraint)
	}
	if !strings.Contains(got.Detail, missing) {
		t.Fatalf("event detail = %q, want it to name %q", got.Detail, missing)
	}
	if got.MovieID != mv.ID {
		t.Fatalf("event movie = %d, want %d", got.MovieID, mv.ID)
	}
}

// A file that contradicts its grab is still reported, but a foreign absolute
// path must not become an `unmatched_files` row: that table is addressed
// relative to the storage root.
func TestExternalMismatchIsReportedWithoutStoringAForeignPath(t *testing.T) {
	h := newHarness(t)
	sr := addSeriesItem(h)
	dl, _ := externalDownload(h, "wrong thing")
	// The grab is for an episode; the file parses as a movie.
	grab := h.grabFor(core.GrabInfo{
		SeriesID:     sr.ID,
		SeasonNum:    1,
		EpisodeIDs:   []int64{episodeID(h, sr.ID, 1, 1)},
		ReleaseTitle: "Stranger.Things.S01E01",
	})

	ctx := context.Background()
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	if parked := h.unmatched(); len(parked) != 0 {
		t.Fatalf("unmatched_files = %+v, want none: %q is outside the storage root", parked, dl.SavePath)
	}
	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusFailed {
		t.Fatalf("grab status = %q, want %q", got, core.GrabStatusFailed)
	}
	events := h.events()
	if len(events) == 0 || !strings.Contains(events[0].Message, "needs a manual match") {
		t.Fatalf("events = %+v, want the mismatch reported in the feed", events)
	}
}

// A client pointed inside the storage root — what the docs recommend, because
// it is also what makes imports hardlink — behaves exactly like the embedded
// engine, stuck-import queue included.
func TestExternalMismatchInsideTheRootStillParks(t *testing.T) {
	h := newHarness(t)
	sr := addSeriesItem(h)
	h.writeVideo("downloads/Big.Buck.Bunny.2008/Big.Buck.Bunny.2008.mkv", "wrong thing")
	dl := core.DownloadStatus{
		ID:       "hash-inside",
		State:    core.DownloadSeeding,
		Name:     "Big.Buck.Bunny.2008",
		Progress: 1,
		SavePath: filepath.Join(h.root, "downloads", "Big.Buck.Bunny.2008"),
		Engine:   "qbittorrent",
	}
	grab := h.grabFor(core.GrabInfo{
		SeriesID:     sr.ID,
		SeasonNum:    1,
		EpisodeIDs:   []int64{episodeID(h, sr.ID, 1, 1)},
		ReleaseTitle: "Stranger.Things.S01E01",
	})

	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	parked := h.unmatched()
	if len(parked) != 1 {
		t.Fatalf("unmatched_files = %d, want 1", len(parked))
	}
	if got := parked[0].Path; got != "downloads/Big.Buck.Bunny.2008/Big.Buck.Bunny.2008.mkv" {
		t.Fatalf("parked path = %q, want the storage-root-relative path", got)
	}
}

// The same, for the shape qBittorrent reports most often: a single-file torrent
// whose content_path is the file itself rather than a directory. That path is
// always absolute, so the single-file branch has to be normalised exactly like
// the walked one — otherwise a mismatched single-file download inside the root
// reads as foreign, and silently loses its stuck-import queue row.
func TestExternalSingleFileMismatchInsideTheRootStillParks(t *testing.T) {
	h := newHarness(t)
	sr := addSeriesItem(h)
	h.writeVideo("downloads/Big.Buck.Bunny.2008.mkv", "wrong thing")
	dl := core.DownloadStatus{
		ID:       "hash-single",
		State:    core.DownloadSeeding,
		Name:     "Big.Buck.Bunny.2008",
		Progress: 1,
		// content_path for a one-file torrent: the .mkv, not its parent.
		SavePath: filepath.Join(h.root, "downloads", "Big.Buck.Bunny.2008.mkv"),
		Engine:   "qbittorrent",
	}
	grab := h.grabFor(core.GrabInfo{
		SeriesID:     sr.ID,
		SeasonNum:    1,
		EpisodeIDs:   []int64{episodeID(h, sr.ID, 1, 1)},
		ReleaseTitle: "Stranger.Things.S01E01",
	})

	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	parked := h.unmatched()
	if len(parked) != 1 {
		t.Fatalf("unmatched_files = %d, want 1: a single file inside the root is not a foreign path", len(parked))
	}
	if got := parked[0].Path; got != "downloads/Big.Buck.Bunny.2008.mkv" {
		t.Fatalf("parked path = %q, want the storage-root-relative path", got)
	}
}
