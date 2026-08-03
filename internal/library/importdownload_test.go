package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// The download fixtures live outside library/, exactly where a download engine
// writes them (SPEC §13: incomplete data is never inside the library).
const (
	movieDownloadDir  = "incomplete/Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP"
	movieDownloadFile = movieDownloadDir + "/Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP.mkv"
)

// sameFile reports whether two storage-root-relative paths are the same inode,
// which is what proves an import hardlinked rather than copied.
func (h *harness) sameFile(a, b string) bool {
	h.t.Helper()
	ai, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(a)))
	if err != nil {
		h.t.Fatalf("stat %s: %v", a, err)
	}
	bi, err := os.Stat(filepath.Join(h.root, filepath.FromSlash(b)))
	if err != nil {
		h.t.Fatalf("stat %s: %v", b, err)
	}
	return os.SameFile(ai, bi)
}

// grabFor inserts a grab and returns the GrabInfo the import pipeline is
// handed for it.
func (h *harness) grabFor(info core.GrabInfo) core.GrabInfo {
	h.t.Helper()
	g := &core.Grab{GrabInfo: info}
	if err := h.st.InsertGrab(context.Background(), g); err != nil {
		h.t.Fatalf("InsertGrab: %v", err)
	}
	return g.GrabInfo
}

func (h *harness) grabStatus(grabID int64) string {
	h.t.Helper()
	g, err := h.st.GetGrab(context.Background(), grabID)
	if err != nil {
		h.t.Fatalf("GetGrab: %v", err)
	}
	return g.Status
}

func (h *harness) unmatched() []core.UnmatchedFile {
	h.t.Helper()
	parked, err := h.st.ListUnmatchedFiles(context.Background())
	if err != nil {
		h.t.Fatalf("ListUnmatchedFiles: %v", err)
	}
	return parked
}

func (h *harness) events() []core.Event {
	h.t.Helper()
	events, err := h.st.ListEvents(context.Background(), 0)
	if err != nil {
		h.t.Fatalf("ListEvents: %v", err)
	}
	return events
}

// addMovieItem seeds the canonical movie fixture and adds it to the library,
// which is the state a grab is made from (SPEC §9 step 2).
func addMovieItem(h *harness) *core.Movie {
	h.t.Helper()
	seedMovie(h)
	mv, err := h.mgr.AddMovie(context.Background(), 10378, "")
	if err != nil {
		h.t.Fatalf("AddMovie: %v", err)
	}
	return mv
}

// addSeriesItem is addMovieItem's series twin.
func addSeriesItem(h *harness) *core.Series {
	h.t.Helper()
	seedSeries(h)
	sr, err := h.mgr.AddSeries(context.Background(), 42)
	if err != nil {
		h.t.Fatalf("AddSeries: %v", err)
	}
	return sr
}

// episodeID resolves a seeded episode's row id, which is what a grab records.
func episodeID(h *harness, seriesID int64, season, number int) int64 {
	h.t.Helper()
	e, err := h.st.GetEpisodeByNumber(context.Background(), seriesID, season, number)
	if err != nil {
		t := h.t
		t.Fatalf("GetEpisodeByNumber(%d, %d): %v", season, number, err)
	}
	return e.ID
}

// movieDownload writes the canonical completed movie download and returns the
// status the engine would report for it.
func movieDownload(h *harness, content string) core.DownloadStatus {
	h.t.Helper()
	h.writeVideo(movieDownloadFile, content)
	return core.DownloadStatus{
		ID:       "infohash-bbb",
		State:    core.DownloadSeeding,
		Name:     filepath.Base(movieDownloadDir),
		Progress: 1,
		SavePath: movieDownloadDir,
	}
}

// TestImportDownloadHardlinksIntoTheLibrary is the phase-2 magic moment: a
// finished download appears renamed in the library while the engine keeps its
// copy to seed from.
func TestImportDownloadHardlinksIntoTheLibrary(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	dl := movieDownload(h, "movie bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP"})

	ctx := context.Background()
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	if got := h.read(organizedRel); got != "movie bytes" {
		t.Errorf("imported content = %q, want %q", got, "movie bytes")
	}
	if !h.exists(movieDownloadFile) {
		t.Fatalf("download data at %s was consumed; the engine can no longer seed it", movieDownloadFile)
	}
	if !h.sameFile(movieDownloadFile, organizedRel) {
		t.Errorf("%s and %s are not the same inode, so the import copied instead of hardlinking",
			movieDownloadFile, organizedRel)
	}
	if !h.exists(movieDirRel + "/" + MovieNFOName) {
		t.Errorf("%s not written", MovieNFOName)
	}

	files, err := h.st.ListMediaFilesForMovie(ctx, mv.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForMovie: %v", err)
	}
	if len(files) != 1 || files[0].Path != organizedRel {
		t.Fatalf("movie files = %+v, want one row at %s", files, organizedRel)
	}
	if files[0].Quality != core.Quality1080p || files[0].ReleaseGroup != "GRP" {
		t.Errorf("release tags were not recorded: %+v", files[0])
	}

	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusImported {
		t.Errorf("grab status = %q, want %q", got, core.GrabStatusImported)
	}
	if parked := h.unmatched(); len(parked) != 0 {
		t.Errorf("unmatched queue = %+v, want empty after a clean import", parked)
	}

	events := h.events()
	if len(events) != 1 || events[0].Category != EventCategoryImport || events[0].MovieID != mv.ID {
		t.Fatalf("events = %+v, want one import event for the movie", events)
	}
	if !strings.Contains(events[0].Detail, "Big.Buck.Bunny") {
		t.Errorf("import event detail = %q, want it to name the grabbed release", events[0].Detail)
	}
}

// TestImportDownloadIsIdempotent covers the at-least-once job queue (SPEC §7):
// the same import delivered twice must not duplicate the file or its rows.
func TestImportDownloadIsIdempotent(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	dl := movieDownload(h, "movie bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny"})

	ctx := context.Background()
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("first ImportDownload: %v", err)
	}
	original, err := h.st.GetMediaFileByPath(ctx, organizedRel)
	if err != nil {
		t.Fatalf("GetMediaFileByPath before re-import: %v", err)
	}
	// Re-run past the grab-status short circuit as well, so this proves the
	// file-level idempotency and not only the cheap guard in front of it.
	if err := h.st.SetGrabStatus(ctx, grab.GrabID, core.GrabStatusGrabbed, ""); err != nil {
		t.Fatalf("SetGrabStatus: %v", err)
	}
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("second ImportDownload: %v", err)
	}

	collision := "library/Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008) (1).mkv"
	if h.exists(collision) {
		t.Errorf("re-import created a duplicate at %s", collision)
	}
	files, err := h.st.ListMediaFilesForMovie(ctx, mv.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForMovie: %v", err)
	}
	if len(files) != 1 || files[0].ID != original.ID || files[0].Path != organizedRel {
		t.Fatalf("movie files = %+v, want the original row at %s", files, organizedRel)
	}
	if !h.exists(organizedRel) {
		t.Errorf("re-import removed the existing library file at %s", organizedRel)
	}
	if !h.exists(movieDownloadFile) {
		t.Errorf("download data disappeared across the re-import")
	}
}

func TestImportDownloadReplacesMovieAfterVerification(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	ctx := context.Background()

	oldParse := movieParse("Big Buck Bunny", 2008)
	oldParse.Quality = core.Quality720p
	h.parser[filepath.Base(movieDownloadFile)] = oldParse
	oldGrab := h.grabFor(core.GrabInfo{MovieID: mv.ID})
	if err := h.mgr.ImportDownload(ctx, movieDownload(h, "old movie bytes"), oldGrab); err != nil {
		t.Fatalf("old ImportDownload: %v", err)
	}

	const upgradeFile = "incomplete/Big.Buck.Bunny.2008.1080p/Big.Buck.Bunny.2008.1080p.mkv"
	h.parser[filepath.Base(upgradeFile)] = movieParse("Big Buck Bunny", 2008)
	h.writeVideo(upgradeFile, "new movie bytes")
	upgradeGrab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big Buck Bunny 1080p"})
	if err := h.mgr.ImportDownload(ctx, core.DownloadStatus{ID: "upgrade", State: core.DownloadSeeding, SavePath: upgradeFile}, upgradeGrab); err != nil {
		t.Fatalf("upgrade ImportDownload: %v", err)
	}

	const replacement = "library/Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008) (1).mkv"
	if h.exists(organizedRel) {
		t.Errorf("superseded movie remains at %s", organizedRel)
	}
	if got := h.read(replacement); got != "new movie bytes" {
		t.Errorf("replacement content = %q, want new movie bytes", got)
	}
	files, err := h.st.ListMediaFilesForMovie(ctx, mv.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForMovie: %v", err)
	}
	if len(files) != 1 || files[0].Path != replacement || files[0].Quality != core.Quality1080p {
		t.Fatalf("movie files = %+v, want one 1080p replacement at %s", files, replacement)
	}
	if !eventMessageContains(h.events(), "Upgraded Big Buck Bunny (2008) to 1080p (was 720p)") {
		t.Errorf("events = %+v, want an upgrade record", h.events())
	}
}

func TestImportDownloadKeepsMovieWhenReplacementCannotBeVerified(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	ctx := context.Background()

	oldParse := movieParse("Big Buck Bunny", 2008)
	oldParse.Quality = core.Quality720p
	h.parser[filepath.Base(movieDownloadFile)] = oldParse
	if err := h.mgr.ImportDownload(ctx, movieDownload(h, "old movie bytes"), h.grabFor(core.GrabInfo{MovieID: mv.ID})); err != nil {
		t.Fatalf("old ImportDownload: %v", err)
	}

	const upgradeFile = "incomplete/Big.Buck.Bunny.2008.1080p/Big.Buck.Bunny.2008.1080p.mkv"
	h.parser[filepath.Base(upgradeFile)] = movieParse("Big Buck Bunny", 2008)
	h.writeVideo(upgradeFile, "new movie bytes")
	h.mgr.link = func(_ string, dst string) error {
		return os.WriteFile(dst, []byte("short"), 0o644)
	}
	err := h.mgr.ImportDownload(ctx, core.DownloadStatus{ID: "broken-upgrade", State: core.DownloadSeeding, SavePath: upgradeFile}, h.grabFor(core.GrabInfo{MovieID: mv.ID}))
	if err == nil || !strings.Contains(err.Error(), "verify imported file") {
		t.Fatalf("broken upgrade error = %v, want verification failure", err)
	}

	if got := h.read(organizedRel); got != "old movie bytes" {
		t.Errorf("old movie content = %q, want it retained after verification failure", got)
	}
	files, err := h.st.ListMediaFilesForMovie(ctx, mv.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForMovie: %v", err)
	}
	if len(files) != 2 || !mediaFilePathExists(files, organizedRel) {
		t.Fatalf("movie files = %+v, want the old row retained until verification succeeds", files)
	}
}

func TestImportDownloadReplacesMovieWithDowngrade(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	ctx := context.Background()

	if err := h.mgr.ImportDownload(ctx, movieDownload(h, "old movie bytes"), h.grabFor(core.GrabInfo{MovieID: mv.ID})); err != nil {
		t.Fatalf("old ImportDownload: %v", err)
	}
	const downgradeFile = "incomplete/Big.Buck.Bunny.2008.720p/Big.Buck.Bunny.2008.720p.mkv"
	downgradeParse := movieParse("Big Buck Bunny", 2008)
	downgradeParse.Quality = core.Quality720p
	h.parser[filepath.Base(downgradeFile)] = downgradeParse
	h.writeVideo(downgradeFile, "downgraded movie bytes")
	if err := h.mgr.ImportDownload(ctx, core.DownloadStatus{ID: "downgrade", State: core.DownloadSeeding, SavePath: downgradeFile}, h.grabFor(core.GrabInfo{MovieID: mv.ID})); err != nil {
		t.Fatalf("downgrade ImportDownload: %v", err)
	}

	if !eventMessageContains(h.events(), "Replaced Big Buck Bunny (2008) with 720p (was 1080p; same or lower quality)") {
		t.Errorf("events = %+v, want an honest downgrade record", h.events())
	}
	files, err := h.st.ListMediaFilesForMovie(ctx, mv.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForMovie: %v", err)
	}
	if len(files) != 1 || files[0].Quality != core.Quality720p {
		t.Fatalf("movie files = %+v, want one downgraded replacement", files)
	}
}

func eventMessageContains(events []core.Event, want string) bool {
	for _, event := range events {
		if strings.Contains(event.Message, want) {
			return true
		}
	}
	return false
}

func mediaFilePathExists(files []core.MediaFile, want string) bool {
	for _, file := range files {
		if file.Path == want {
			return true
		}
	}
	return false
}

// TestImportDownloadSkipsAnAlreadyImportedGrab is the cheap half of the same
// guarantee: a redelivered job for a grab already marked imported does nothing
// at all.
func TestImportDownloadSkipsAnAlreadyImportedGrab(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	dl := movieDownload(h, "movie bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID})

	ctx := context.Background()
	if err := h.st.SetGrabStatus(ctx, grab.GrabID, core.GrabStatusImported, "already done"); err != nil {
		t.Fatalf("SetGrabStatus: %v", err)
	}
	h.mgr.link = func(string, string) error {
		t.Fatal("ImportDownload touched the filesystem for an already-imported grab")
		return nil
	}
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	if h.exists(organizedRel) {
		t.Errorf("%s was written for an already-imported grab", organizedRel)
	}
}

// TestImportDownloadParksAMismatch is PLAN phase 2's "a deliberately
// mismatched download parks in the stuck-import queue" criterion.
func TestImportDownloadParksAMismatch(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)

	const (
		dir  = "incomplete/Some.Other.Movie.2019.1080p"
		file = dir + "/Some.Other.Movie.2019.1080p.mkv"
	)
	h.parser[filepath.Base(file)] = movieParse("Some Other Movie", 2019)
	h.writeVideo(file, "not the movie we asked for")
	dl := core.DownloadStatus{ID: "infohash-other", State: core.DownloadCompleted, SavePath: dir}
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: "Big.Buck.Bunny.2008"})

	ctx := context.Background()
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload returned an error; parking is a successful outcome: %v", err)
	}

	parked := h.unmatched()
	if len(parked) != 1 {
		t.Fatalf("unmatched queue = %+v, want the mismatched file parked", parked)
	}
	if parked[0].Path != file {
		t.Errorf("parked path = %q, want %q", parked[0].Path, file)
	}
	if parked[0].Reason != ReasonImport {
		t.Errorf("park reason = %q, want %q", parked[0].Reason, ReasonImport)
	}
	if parked[0].Parsed.Title != "Some Other Movie" {
		t.Errorf("parked parse = %+v, want the parser's guess preserved", parked[0].Parsed)
	}

	// Nothing in the library, and the download's data untouched.
	if h.exists(movieDirRel) {
		t.Errorf("%s was created for a mismatched import", movieDirRel)
	}
	if !h.exists(file) {
		t.Errorf("mismatched download data at %s was moved", file)
	}
	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusFailed {
		t.Errorf("grab status = %q, want %q", got, core.GrabStatusFailed)
	}

	events := h.events()
	if len(events) != 1 || events[0].Level != core.EventLevelWarn {
		t.Fatalf("events = %+v, want one warning", events)
	}
	if !strings.Contains(events[0].Detail, "Some Other Movie") {
		t.Errorf("event detail = %q, want it to explain the mismatch", events[0].Detail)
	}
}

// TestImportDownloadParksAnEpisodeUnderAMovieGrab covers the other direction
// of the sanity check: the file is not even the right *kind* of thing.
func TestImportDownloadParksAnEpisodeUnderAMovieGrab(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)

	const (
		dir  = "incomplete/Planet.Earth.II.S01E01"
		file = dir + "/Planet.Earth.II.S01E01.mkv"
	)
	h.parser[filepath.Base(file)] = episodeParse("Planet Earth II", 1, 1)
	h.writeVideo(file, "wrong kind")
	dl := core.DownloadStatus{ID: "infohash-ep", State: core.DownloadCompleted, SavePath: dir}
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID})

	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	parked := h.unmatched()
	if len(parked) != 1 || parked[0].Reason != ReasonImport {
		t.Fatalf("unmatched queue = %+v, want the episode parked with reason %q", parked, ReasonImport)
	}
}

// TestImportDownloadPicksTheFeature: samples and extras ride along with real
// releases, and the biggest video file is the one that was wanted.
func TestImportDownloadPicksTheFeature(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	dl := movieDownload(h, strings.Repeat("feature ", 64))
	sample := movieDownloadDir + "/sample.mkv"
	h.writeVideo(sample, "sample")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID})

	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	if got := h.read(organizedRel); got != strings.Repeat("feature ", 64) {
		t.Errorf("imported the wrong file: content = %q", got)
	}
	if parked := h.unmatched(); len(parked) != 0 {
		t.Errorf("unmatched queue = %+v, want the sample ignored rather than parked", parked)
	}
}

// TestImportDownloadReplacesEpisodeFile proves an episode import removes the
// prior file only after its replacement has a verified media row and link.
func TestImportDownloadReplacesEpisodeFile(t *testing.T) {
	h := newHarness(t)
	sr := addSeriesItem(h)
	ctx := context.Background()
	episode := episodeID(h, sr.ID, 1, 1)

	const oldFile = "incomplete/Planet.Earth.II.S01E01.720p/Planet.Earth.II.S01E01.720p.mkv"
	oldParse := episodeParse("Planet Earth II", 1, 1)
	oldParse.Quality = core.Quality720p
	h.parser[filepath.Base(oldFile)] = oldParse
	h.writeVideo(oldFile, "old episode bytes")
	if err := h.mgr.ImportDownload(ctx, core.DownloadStatus{ID: "old-episode", State: core.DownloadSeeding, SavePath: oldFile}, h.grabFor(core.GrabInfo{SeriesID: sr.ID, SeasonNum: 1, EpisodeIDs: []int64{episode}})); err != nil {
		t.Fatalf("old ImportDownload: %v", err)
	}

	const newFile = "incomplete/Planet.Earth.II.S01E01.1080p/Planet.Earth.II.S01E01.1080p.mkv"
	h.parser[filepath.Base(newFile)] = episodeParse("Planet Earth II", 1, 1)
	h.writeVideo(newFile, "new episode bytes")
	if err := h.mgr.ImportDownload(ctx, core.DownloadStatus{ID: "new-episode", State: core.DownloadSeeding, SavePath: newFile}, h.grabFor(core.GrabInfo{SeriesID: sr.ID, SeasonNum: 1, EpisodeIDs: []int64{episode}})); err != nil {
		t.Fatalf("new ImportDownload: %v", err)
	}

	const (
		oldOrganized = "library/TV/Planet Earth II (2016)/Season 01/Planet Earth II (2016) - S01E01 - Islands.mkv"
		replacement  = "library/TV/Planet Earth II (2016)/Season 01/Planet Earth II (2016) - S01E01 - Islands (1).mkv"
	)
	if h.exists(oldOrganized) {
		t.Errorf("superseded episode remains at %s", oldOrganized)
	}
	linked, err := h.st.ListMediaFilesForEpisode(ctx, episode)
	if err != nil {
		t.Fatalf("ListMediaFilesForEpisode: %v", err)
	}
	if len(linked) != 1 || linked[0].Path != replacement || linked[0].Quality != core.Quality1080p {
		t.Fatalf("episode files = %+v, want the 1080p replacement at %s", linked, replacement)
	}
	if !eventMessageContains(h.events(), "Upgraded file for Planet Earth II S01E01 to 1080p (was 720p)") {
		t.Errorf("events = %+v, want an episode upgrade record", h.events())
	}
}

// TestImportDownloadSeasonPack proves a directory can carry several episodes:
// each matching file imports independently, while unrelated files park.
func TestImportDownloadSeasonPack(t *testing.T) {
	h := newHarness(t)
	sr := addSeriesItem(h)

	const dir = "incomplete/Planet.Earth.II.S01.1080p.WEB-DL.x265"
	files := map[string]core.ParsedRelease{
		"Planet.Earth.II.S01E01.1080p.WEB-DL.x265.mkv": episodeParse("Planet Earth II", 1, 1),
		"Planet.Earth.II.S01E02.1080p.WEB-DL.x265.mkv": episodeParse("Planet Earth II", 1, 2),
		// Not among the episodes the grab was for.
		"Planet.Earth.II.S01E03.1080p.WEB-DL.x265.mkv": episodeParse("Planet Earth II", 1, 3),
		// Wrong season entirely.
		"Planet.Earth.II.S02E01.1080p.WEB-DL.x265.mkv": episodeParse("Planet Earth II", 2, 1),
	}
	for name, parsed := range files {
		h.parser[name] = parsed
		h.writeVideo(dir+"/"+name, "bytes of "+name)
	}
	// Not media: it must be ignored, not parked.
	h.writeVideo(dir+"/release.nfo", "scene notes")

	grab := h.grabFor(core.GrabInfo{
		SeriesID:     sr.ID,
		SeasonNum:    1,
		EpisodeIDs:   []int64{episodeID(h, sr.ID, 1, 1), episodeID(h, sr.ID, 1, 2)},
		ReleaseTitle: "Planet.Earth.II.S01.1080p.WEB-DL.x265",
	})
	dl := core.DownloadStatus{ID: "infohash-pack", State: core.DownloadSeeding, SavePath: dir}

	ctx := context.Background()
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	const seasonDir = "library/TV/Planet Earth II (2016)/Season 01"
	wanted := map[int]string{
		1: seasonDir + "/Planet Earth II (2016) - S01E01 - Islands.mkv",
		2: seasonDir + "/Planet Earth II (2016) - S01E02 - Mountains.mkv",
	}
	for number, rel := range wanted {
		if !h.exists(rel) {
			t.Fatalf("episode %d not imported to %s", number, rel)
		}
		episode, err := h.st.GetEpisodeByNumber(ctx, sr.ID, 1, number)
		if err != nil {
			t.Fatalf("GetEpisodeByNumber(%d): %v", number, err)
		}
		linked, err := h.st.ListMediaFilesForEpisode(ctx, episode.ID)
		if err != nil {
			t.Fatalf("ListMediaFilesForEpisode(%d): %v", number, err)
		}
		if len(linked) != 1 || linked[0].Path != rel {
			t.Fatalf("episode %d files = %+v, want one row at %s", number, linked, rel)
		}
	}
	if h.exists(seasonDir + "/Planet Earth II (2016) - S01E03 - Jungles.mkv") {
		t.Error("an episode the grab did not ask for was imported")
	}

	parked := h.unmatched()
	if len(parked) != 2 {
		t.Fatalf("unmatched queue = %+v, want the two files the grab did not cover", parked)
	}
	for _, u := range parked {
		if u.Reason != ReasonImport {
			t.Errorf("park reason for %s = %q, want %q", u.Path, u.Reason, ReasonImport)
		}
		if !strings.Contains(u.Path, "S01E03") && !strings.Contains(u.Path, "S02E01") {
			t.Errorf("unexpected parked file %s", u.Path)
		}
	}
	// Two files landed, so the grab did its job even though two others parked.
	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusImported {
		t.Errorf("grab status = %q, want %q", got, core.GrabStatusImported)
	}
}

func TestImportDownloadSeasonPackLinksMultiEpisodeFileToEveryEpisode(t *testing.T) {
	h := newHarness(t)
	meta := seedSeries(h)
	meta.Seasons[0].Episodes = meta.Seasons[0].Episodes[:2]
	h.provider.seriesByID[meta.TMDBID] = meta
	sr, err := h.mgr.AddSeries(context.Background(), meta.TMDBID)
	if err != nil {
		t.Fatalf("AddSeries: %v", err)
	}

	const file = "incomplete/Planet.Earth.II.S01.1080p/Planet.Earth.II.S01E01E02.1080p.mkv"
	h.parser[filepath.Base(file)] = episodeParse("Planet Earth II", 1, 1, 2)
	h.writeVideo(file, "combined episode bytes")
	grab := h.grabFor(core.GrabInfo{
		SeriesID:     sr.ID,
		SeasonNum:    1,
		EpisodeIDs:   []int64{episodeID(h, sr.ID, 1, 1), episodeID(h, sr.ID, 1, 2)},
		ReleaseTitle: "Planet Earth II season 1",
	})
	if err := h.mgr.ImportDownload(context.Background(), core.DownloadStatus{ID: "multi-episode", State: core.DownloadSeeding, SavePath: file}, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	const organized = "library/TV/Planet Earth II (2016)/Season 01/Planet Earth II (2016) - S01E01-E02 - Islands + Mountains.mkv"
	files, err := h.st.ListMediaFiles(context.Background())
	if err != nil {
		t.Fatalf("ListMediaFiles: %v", err)
	}
	if len(files) != 1 || files[0].Path != organized {
		t.Fatalf("media files = %+v, want one shared row at %s", files, organized)
	}
	for _, number := range []int{1, 2} {
		episode, err := h.st.GetEpisodeByNumber(context.Background(), sr.ID, 1, number)
		if err != nil {
			t.Fatalf("GetEpisodeByNumber(%d): %v", number, err)
		}
		linked, err := h.st.ListMediaFilesForEpisode(context.Background(), episode.ID)
		if err != nil {
			t.Fatalf("ListMediaFilesForEpisode(%d): %v", number, err)
		}
		if len(linked) != 1 || linked[0].ID != files[0].ID {
			t.Errorf("S01E%02d links = %+v, want the shared media row", number, linked)
		}
	}
}

// TestImportDownloadImportsObfuscatedEpisodeByReleaseTitle covers what usenet
// posts routinely do: the payload's file name is random noise. The grab's
// release title is the evidence of what was fetched, and it vouches for the
// feature-sized file — an obfuscated extra still parks rather than riding in.
func TestImportDownloadImportsObfuscatedEpisodeByReleaseTitle(t *testing.T) {
	h := newHarness(t)
	sr := addSeriesItem(h)

	const release = "Planet.Earth.II.S01E01.1080p.WEB-DL.x265"
	const dir = "incomplete/" + release
	const obfuscated = dir + "/pA9qxTu5Ykpd8Z3UELhIIiADBugdp9zg.mkv"
	h.writeVideo(obfuscated, "the actual episode payload")
	// A smaller obfuscated extra: the release title must not vouch for it.
	h.writeVideo(dir+"/x1.mkv", "sample")
	h.parser[release] = episodeParse("Planet Earth II", 1, 1)

	grab := h.grabFor(core.GrabInfo{
		SeriesID:     sr.ID,
		SeasonNum:    1,
		EpisodeIDs:   []int64{episodeID(h, sr.ID, 1, 1)},
		ReleaseTitle: release,
	})
	dl := core.DownloadStatus{ID: "u-obfuscated", State: core.DownloadCompleted, SavePath: dir}

	ctx := context.Background()
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	const organized = "library/TV/Planet Earth II (2016)/Season 01/Planet Earth II (2016) - S01E01 - Islands.mkv"
	if !h.exists(organized) {
		t.Fatalf("obfuscated episode was not imported to %s", organized)
	}
	if !h.sameFile(obfuscated, organized) {
		t.Errorf("import did not use the feature-sized file")
	}
	file, err := h.st.GetMediaFileByPath(ctx, organized)
	if err != nil {
		t.Fatalf("GetMediaFileByPath: %v", err)
	}
	if file.Quality != core.Quality1080p {
		t.Errorf("quality = %q, want the release title's %q", file.Quality, core.Quality1080p)
	}
	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusImported {
		t.Errorf("grab status = %q, want %q", got, core.GrabStatusImported)
	}
	parked := h.unmatched()
	if len(parked) != 1 || !strings.Contains(parked[0].Path, "x1.mkv") {
		t.Errorf("unmatched queue = %+v, want only the obfuscated extra", parked)
	}
}

// TestImportDownloadImportsObfuscatedMovieByReleaseTitle is the movie twin: a
// noise file name borrows the release title, so the import carries the
// quality tags instead of landing as "unknown" — or parking outright.
func TestImportDownloadImportsObfuscatedMovieByReleaseTitle(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)

	const release = "Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP"
	const obfuscated = "incomplete/" + release + "/jK2mNpQ8vTzWxY4bC.mkv"
	h.writeVideo(obfuscated, "movie bytes")
	h.parser[release] = movieParse("Big Buck Bunny", 2008)

	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID, ReleaseTitle: release})
	dl := core.DownloadStatus{ID: "u-movie", State: core.DownloadCompleted, SavePath: "incomplete/" + release}

	ctx := context.Background()
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	if !h.exists(organizedRel) {
		t.Fatalf("obfuscated movie was not imported to %s", organizedRel)
	}
	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusImported {
		t.Errorf("grab status = %q, want %q", got, core.GrabStatusImported)
	}
	if parked := h.unmatched(); len(parked) != 0 {
		t.Errorf("unmatched queue = %+v, want empty", parked)
	}
}

// TestImportDownloadDoesNotRelabelAPositiveClaim: the fallback must never
// override a file that names a different episode than the grab asked for — a
// mislabeled post imports as the wrong episode forever, a parked one is fixed
// in a minute.
func TestImportDownloadDoesNotRelabelAPositiveClaim(t *testing.T) {
	h := newHarness(t)
	sr := addSeriesItem(h)

	const release = "Planet.Earth.II.S01E01.1080p.WEB-DL.x265"
	const dir = "incomplete/" + release
	const file = dir + "/Planet.Earth.II.S01E03.1080p.WEB-DL.x265.mkv"
	h.writeVideo(file, "actually episode three")
	h.parser[filepath.Base(file)] = episodeParse("Planet Earth II", 1, 3)
	h.parser[release] = episodeParse("Planet Earth II", 1, 1)

	grab := h.grabFor(core.GrabInfo{
		SeriesID:     sr.ID,
		SeasonNum:    1,
		EpisodeIDs:   []int64{episodeID(h, sr.ID, 1, 1)},
		ReleaseTitle: release,
	})
	dl := core.DownloadStatus{ID: "u-mislabeled", State: core.DownloadCompleted, SavePath: dir}

	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	parked := h.unmatched()
	if len(parked) != 1 {
		t.Fatalf("unmatched queue = %+v, want the mislabeled file parked", parked)
	}
	if got := h.grabStatus(grab.GrabID); got != core.GrabStatusFailed {
		t.Errorf("grab status = %q, want %q", got, core.GrabStatusFailed)
	}
}

// TestImportDownloadAcceptsASingleFileDownload covers the save path pointing
// at a file rather than a directory, which is what a one-file torrent gives.
func TestImportDownloadAcceptsASingleFileDownload(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	rel := "incomplete/Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP.mkv"
	h.writeVideo(rel, "movie bytes")
	dl := core.DownloadStatus{ID: "infohash-single", State: core.DownloadCompleted, SavePath: rel}
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID})

	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	if !h.sameFile(rel, organizedRel) {
		t.Errorf("single-file download was not hardlinked into the library")
	}
}

// TestImportDownloadFallsBackToCopy simulates exFAT and cross-device data
// (SPEC §3): with no hardlinks available the file is copied, and the download
// still keeps its own copy to seed from.
func TestImportDownloadFallsBackToCopy(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	dl := movieDownload(h, "movie bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID})
	h.mgr.link = func(string, string) error { return errors.New("no hardlinks here") }

	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	if got := h.read(organizedRel); got != "movie bytes" {
		t.Errorf("copied content = %q", got)
	}
	if !h.exists(movieDownloadFile) {
		t.Fatalf("download data was consumed by a copying import")
	}
	if h.sameFile(movieDownloadFile, organizedRel) {
		t.Errorf("expected two distinct files after a copy")
	}
}

// TestImportDownloadRejectsAnEmptyDownload: nothing to import is an error, not
// a silent success — the job queue's retry and the activity feed are how a
// half-written save path becomes visible.
func TestImportDownloadRejectsAnEmptyDownload(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)
	const dir = "incomplete/Empty.Release"
	h.writeVideo(dir+"/readme.txt", "no video here")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID})

	err := h.mgr.ImportDownload(context.Background(),
		core.DownloadStatus{ID: "infohash-empty", State: core.DownloadCompleted, SavePath: dir}, grab)
	if err == nil {
		t.Fatal("ImportDownload succeeded for a download with no video file")
	}
	if !strings.Contains(err.Error(), "no video file") {
		t.Errorf("error = %v, want it to say there is no video file", err)
	}
}

// TestImportDownloadNeedsATarget: a grab that names neither a movie nor a
// series is a programming error and says so rather than guessing.
func TestImportDownloadNeedsATarget(t *testing.T) {
	h := newHarness(t)
	addMovieItem(h)
	dl := movieDownload(h, "movie bytes")

	err := h.mgr.ImportDownload(context.Background(), dl, core.GrabInfo{GrabID: 7})
	if err == nil {
		t.Fatal("ImportDownload succeeded for a grab with no target")
	}
	if !strings.Contains(err.Error(), "neither a movie nor a series") {
		t.Errorf("error = %v", err)
	}
}

// TestParkedImportSurvivesARescan: the stuck-import queue holds files outside
// the library, so a library scan must not mistake "not in this walk" for
// "gone" (SPEC §13 — stuck imports are resolved by the user, on their time).
func TestParkedImportSurvivesARescan(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)

	// A real library to walk: without one the scan has nothing to reconcile
	// against and the regression this test guards could not fire.
	good := movieDownload(h, "movie bytes")
	if err := h.mgr.ImportDownload(context.Background(), good,
		h.grabFor(core.GrabInfo{MovieID: mv.ID})); err != nil {
		t.Fatalf("setup import: %v", err)
	}

	const (
		dir  = "incomplete/Some.Other.Movie.2019.1080p"
		file = dir + "/Some.Other.Movie.2019.1080p.mkv"
	)
	h.parser[filepath.Base(file)] = movieParse("Some Other Movie", 2019)
	h.writeVideo(file, "mismatched")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID})
	dl := core.DownloadStatus{ID: "infohash-other", State: core.DownloadCompleted, SavePath: dir}

	if err := h.mgr.ImportDownload(context.Background(), dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	if parked := h.unmatched(); len(parked) != 1 {
		t.Fatalf("setup: unmatched queue = %+v, want one parked file", parked)
	}

	h.scan()

	parked := h.unmatched()
	if len(parked) != 1 || parked[0].Path != file {
		t.Fatalf("after a rescan the queue = %+v, want the parked import to survive", parked)
	}
}

// TestParkedImportIsResolvableByHand closes the loop PLAN phase 2 asks for: the
// user says what the file actually is, and it imports without the download
// engine losing its copy.
func TestParkedImportIsResolvableByHand(t *testing.T) {
	h := newHarness(t)
	mv := addMovieItem(h)

	const (
		dir  = "incomplete/Big.Buck.Bunny.2008.REPACK"
		file = dir + "/mystery.mkv"
	)
	h.parser[filepath.Base(file)] = core.ParsedRelease{Title: "mystery", Confidence: 0.9}
	h.writeVideo(file, "movie bytes")
	grab := h.grabFor(core.GrabInfo{MovieID: mv.ID})
	dl := core.DownloadStatus{ID: "infohash-repack", State: core.DownloadSeeding, SavePath: dir}

	ctx := context.Background()
	if err := h.mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	parked := h.unmatched()
	if len(parked) != 1 {
		t.Fatalf("unmatched queue = %+v, want the unrecognized file parked", parked)
	}

	res, err := h.mgr.ImportUnmatched(ctx, parked[0].ID, 10378, MediaTypeMovie)
	if err != nil {
		t.Fatalf("ImportUnmatched: %v", err)
	}
	if res.Path != organizedRel {
		t.Fatalf("manual import landed at %q, want %q", res.Path, organizedRel)
	}
	if !h.exists(file) {
		t.Errorf("manually resolving a stuck import consumed the download's data at %s", file)
	}
	if !h.sameFile(file, organizedRel) {
		t.Errorf("manual import of a download file did not hardlink")
	}
	if parked := h.unmatched(); len(parked) != 0 {
		t.Errorf("unmatched queue = %+v, want empty after the manual match", parked)
	}
}
