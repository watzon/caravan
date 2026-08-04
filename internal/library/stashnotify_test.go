package library

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// recordingAdultNotifier stands in for the Stash handoff (internal/stash).
type recordingAdultNotifier struct {
	calls   int
	scenes  []int64
	failing bool
}

func (n *recordingAdultNotifier) AdultLibraryChanged(_ context.Context, ids []int64) error {
	n.calls++
	n.scenes = append(n.scenes, ids...)
	if n.failing {
		return errors.New("stash is unreachable")
	}
	return nil
}

// TestSceneImportNotifiesTheAdultHandoffOnce is PLAN phase 11 acceptance
// criterion 1's first half: an adult import fires exactly one scoped scan.
//
// "Once" is asserted at the notification rather than at the HTTP call because
// this is where the count is decided — the handoff coalesces a burst, but a
// pipeline that notified per file would still be wrong, and would only look
// right for as long as the debounce window held.
func TestSceneImportNotifiesTheAdultHandoffOnce(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	notify := &recordingAdultNotifier{}
	h.mgr.notifyAdult = notify
	sr := h.addSite("site-1")

	// Two scenes in one download: a pack owes one notification, not one per
	// file.
	first := h.sceneNamed(sr.ID, "Deep Impact")
	second := h.sceneNamed(sr.ID, "Second")

	const dir = "incomplete/Brazzers.pack"
	h.writeVideo(dir+"/Brazzers.22.03.14.Deep.Impact.XXX.1080p.MP4-KTR.mp4", "scene one payload")
	h.writeVideo(dir+"/Brazzers.22.06.09.Second.XXX.1080p.MP4-KTR.mp4", "scene two payload")

	grab := h.grabFor(core.GrabInfo{
		SeriesID:   sr.ID,
		EpisodeIDs: []int64{first.ID, second.ID},
	})
	dl := core.DownloadStatus{ID: "scene-pack", State: core.DownloadCompleted, SavePath: dir}
	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	if notify.calls != 1 {
		t.Fatalf("adult handoff notifications = %d, want exactly 1 for one download", notify.calls)
	}
	if len(notify.scenes) != 2 {
		t.Fatalf("scenes handed to the handoff = %v, want both imported episodes", notify.scenes)
	}
	got := map[int64]bool{}
	for _, id := range notify.scenes {
		got[id] = true
	}
	if !got[first.ID] || !got[second.ID] {
		t.Errorf("scenes = %v, want %d and %d", notify.scenes, first.ID, second.ID)
	}
}

// A download that lands nothing changed no files, so there is nothing for Stash
// to scan or identify.
func TestParkedSceneDoesNotNotifyTheAdultHandoff(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	notify := &recordingAdultNotifier{}
	h.mgr.notifyAdult = notify
	sr := h.addSite("site-1")
	target := h.sceneNamed(sr.ID, "Deep Impact")

	// A file with no date in its name and no release title to rescue it.
	const dir = "incomplete/mystery"
	h.writeVideo(dir+"/qQ8xNoiseNoDate.mp4", "payload")

	grab := h.grabFor(core.GrabInfo{SeriesID: sr.ID, EpisodeIDs: []int64{target.ID}})
	dl := core.DownloadStatus{ID: "parked", State: core.DownloadCompleted, SavePath: dir}
	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	if notify.calls != 0 {
		t.Fatalf("adult handoff notifications = %d, want 0 when nothing imported", notify.calls)
	}
}

// TestTelevisionImportNeverNotifiesTheAdultHandoff is acceptance criterion 1's
// second half, and the exposure rule underneath it: Stash is told about the
// adult library and nothing else. A television import that reached the adult
// handoff would hand a scoped-to-Adult scan a reason to run for a file that is
// not there — and, worse, would put a television episode id into an identity
// push aimed at a stash-box.
func TestTelevisionImportNeverNotifiesTheAdultHandoff(t *testing.T) {
	h := newHarness(t)
	notify := &recordingAdultNotifier{}
	h.mgr.notifyAdult = notify
	sr := addSeriesItem(h)
	episode := episodeID(h, sr.ID, 1, 1)

	const file = "incomplete/Planet.Earth.II.S01E01.1080p/Planet.Earth.II.S01E01.1080p.mkv"
	h.parser[filepath.Base(file)] = episodeParse("Planet Earth II", 1, 1)
	h.writeVideo(file, "episode bytes")

	grab := h.grabFor(core.GrabInfo{SeriesID: sr.ID, SeasonNum: 1, EpisodeIDs: []int64{episode}})
	dl := core.DownloadStatus{ID: "tv", State: core.DownloadCompleted, SavePath: file}
	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	files, err := h.st.ListMediaFilesForEpisode(context.Background(), episode)
	if err != nil {
		t.Fatalf("ListMediaFilesForEpisode: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("imported %d files, want 1 — the fixture is not exercising an import", len(files))
	}
	if notify.calls != 0 {
		t.Fatalf("adult handoff notifications = %d, want 0 for a television import", notify.calls)
	}
}

// A movie import is the other non-adult path, and takes a different branch
// through ImportDownload, so it is asserted separately rather than assumed.
func TestMovieImportNeverNotifiesTheAdultHandoff(t *testing.T) {
	h := newHarness(t)
	notify := &recordingAdultNotifier{}
	h.mgr.notifyAdult = notify
	movie := addMovieItem(h)

	const file = "incomplete/Big.Buck.Bunny.2008.1080p/Big.Buck.Bunny.2008.1080p.mkv"
	h.parser[filepath.Base(file)] = movieParse("Big Buck Bunny", 2008)
	h.writeVideo(file, "movie bytes")

	dl := core.DownloadStatus{ID: "movie", State: core.DownloadCompleted, SavePath: file}
	if err := h.mgr.ImportDownload(context.Background(), dl, h.grabFor(core.GrabInfo{MovieID: movie.ID})); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	if notify.calls != 0 {
		t.Fatalf("adult handoff notifications = %d, want 0 for a movie import", notify.calls)
	}
}

// The import is complete whatever the handoff says. A notifier that fails is a
// warning in the feed, never an error out of ImportDownload — failing would make
// the job queue retry an import that already landed.
func TestAdultHandoffFailureDoesNotFailTheImport(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	notify := &recordingAdultNotifier{failing: true}
	h.mgr.notifyAdult = notify
	sr := h.addSite("site-1")
	target := h.sceneNamed(sr.ID, "Deep Impact")

	const dir = "incomplete/Brazzers.22.03.14.Deep.Impact"
	h.writeVideo(dir+"/Brazzers.22.03.14.Deep.Impact.XXX.1080p.MP4-KTR.mp4", "payload")

	grab := h.grabFor(core.GrabInfo{SeriesID: sr.ID, EpisodeIDs: []int64{target.ID}})
	dl := core.DownloadStatus{ID: "scene-down-stash", State: core.DownloadCompleted, SavePath: dir}
	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload with an unreachable handoff: %v", err)
	}

	const organized = "library/Adult/Brazzers/Season 2022/Brazzers - 2022-03-14 - Deep Impact.mp4"
	if !h.exists(organized) {
		t.Fatalf("scene was not imported to %s despite the handoff failing", organized)
	}
	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusImported {
		t.Errorf("grab status = %q, want %q", got, core.GrabStatusImported)
	}
	if !eventMessageContains(h.events(), "Adult library handoff could not be notified") {
		t.Errorf("events = %+v, want the handoff failure recorded as a warning", h.events())
	}
}

// sceneNamed finds one seeded scene by title.
func (a *adultHarness) sceneNamed(seriesID int64, title string) core.Episode {
	a.t.Helper()
	for _, ep := range a.episodes(seriesID) {
		if strings.EqualFold(ep.Title, title) {
			return ep
		}
	}
	a.t.Fatalf("no seeded scene titled %q", title)
	return core.Episode{}
}

// The other direction of the split, pinned because the AdultNotifier doc claims
// a guarantee only in one of them.
//
// An adult import fires the generic Notifier as well: ImportDownload runs
// libraryChanged for any import that landed a file, and a scene import is one.
// That is deliberate — "the library changed" is true, and Caravan does not know
// which of a user's Jellyfin libraries covers which directory — but it is the
// kind of thing a reader assumes away, so it is asserted rather than described.
func TestSceneImportAlsoNotifiesThePlaybackHandoff(t *testing.T) {
	h := newAdultHarness(t, true)
	h.seedBrazzers()
	playback := &stubNotifier{}
	adult := &recordingAdultNotifier{}
	h.mgr.notify = playback
	h.mgr.notifyAdult = adult
	sr := h.addSite("site-1")
	target := h.sceneNamed(sr.ID, "Deep Impact")

	const dir = "incomplete/Brazzers.22.03.14.Deep.Impact"
	h.writeVideo(dir+"/Brazzers.22.03.14.Deep.Impact.XXX.1080p.MP4-KTR.mp4", "payload")

	grab := h.grabFor(core.GrabInfo{SeriesID: sr.ID, EpisodeIDs: []int64{target.ID}})
	dl := core.DownloadStatus{ID: "scene-both", State: core.DownloadCompleted, SavePath: dir}
	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	if adult.calls != 1 {
		t.Errorf("adult handoff notifications = %d, want 1", adult.calls)
	}
	if playback.calls != 1 {
		t.Errorf("playback handoff notifications = %d, want 1 — the guarantee is one-directional", playback.calls)
	}
}
