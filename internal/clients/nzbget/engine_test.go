package nzbget

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// The NZBIDs in testdata/listgroups.json and testdata/history.json.
const (
	downloadingID core.DownloadID = "41"
	queuedID      core.DownloadID = "42"
	strangerID    core.DownloadID = "44"
	completedID   core.DownloadID = "39"
	stranger2ID   core.DownloadID = "36"
)

// The state table is the contract between NZBGet's vocabulary and Caravan's,
// and the import watcher fires on one of the six values, so every status
// listgroups can report is pinned here.
func TestGroupStateMapping(t *testing.T) {
	tests := []struct {
		nzbget string
		want   core.DownloadState
	}{
		{statusQueued, core.DownloadQueued},
		{statusPaused, core.DownloadPaused},
		{statusDownload, core.DownloadDownloading},
		{statusFetching, core.DownloadDownloading},

		// Post-processing: NZBGet is still touching the files, so importing
		// now would copy a moving target. A par repair that *fails* never
		// appears here — it lands in the history as FAILURE/PAR.
		{statusPPQueued, core.DownloadDownloading},
		{statusLoadingPars, core.DownloadDownloading},
		{statusVerifyingSources, core.DownloadDownloading},
		{statusRepairing, core.DownloadDownloading},
		{statusVerifyingRepaired, core.DownloadDownloading},
		{statusRenaming, core.DownloadDownloading},
		{statusUnpacking, core.DownloadDownloading},
		{statusMoving, core.DownloadDownloading},
		{statusPostUnpackRenaming, core.DownloadDownloading},
		{statusExecutingScript, core.DownloadDownloading},
		{statusPPFinished, core.DownloadDownloading},
		{statusQSQueued, core.DownloadDownloading},
		{statusQSExecuting, core.DownloadDownloading},

		// Nothing is claimed for a stage we do not recognise.
		{"SOME_FUTURE_STAGE", core.DownloadQueued},
		{"", core.DownloadQueued},
	}
	for _, tt := range tests {
		t.Run(tt.nzbget, func(t *testing.T) {
			if got := mapGroupState(tt.nzbget); got != tt.want {
				t.Fatalf("mapGroupState(%q) = %q, want %q", tt.nzbget, got, tt.want)
			}
		})
	}

	// A queued group is never completed: NZBGet moves it to the history first.
	for status := range groupStateMap {
		if mapGroupState(status) == core.DownloadCompleted {
			t.Fatalf("%q maps to completed, but the queue never holds a finished download", status)
		}
	}
}

// History statuses are "CATEGORY/DETAIL" pairs and NZBGet adds details in most
// releases, so only the category is read.
func TestHistoryStateMapping(t *testing.T) {
	tests := []struct {
		nzbget string
		want   core.DownloadState
	}{
		{"SUCCESS/ALL", core.DownloadCompleted},
		{"SUCCESS/PAR", core.DownloadCompleted},
		{"SUCCESS/UNPACK", core.DownloadCompleted},
		{"SUCCESS/HEALTH", core.DownloadCompleted},

		// A warning is a post-processing complaint over data NZBGet did
		// finish fetching. Failing it would block the import of a download
		// that is sitting right there.
		{"WARNING/SCRIPT", core.DownloadCompleted},
		{"WARNING/SPACE", core.DownloadCompleted},
		{"WARNING/DAMAGED", core.DownloadCompleted},

		{"FAILURE/PAR", core.DownloadFailed},
		{"FAILURE/UNPACK", core.DownloadFailed},
		{"FAILURE/HEALTH", core.DownloadFailed},
		{"FAILURE/BAD", core.DownloadFailed},
		{"FAILURE/MOVE", core.DownloadFailed},
		{"FAILURE/SOMETHING_NEW", core.DownloadFailed},

		// Removed in NZBGet: it did not deliver, and failed is the state that
		// lets the grab be retried.
		{"DELETED/MANUAL", core.DownloadFailed},
		{"DELETED/DUPE", core.DownloadFailed},

		// Nothing is claimed for a category we do not recognise.
		{"SOMETHING/ELSE", core.DownloadQueued},
		{"", core.DownloadQueued},
	}
	for _, tt := range tests {
		t.Run(tt.nzbget, func(t *testing.T) {
			if got := mapHistoryState(tt.nzbget); got != tt.want {
				t.Fatalf("mapHistoryState(%q) = %q, want %q", tt.nzbget, got, tt.want)
			}
		})
	}
}

func TestSizeCombinesNZBGetsSplitHalves(t *testing.T) {
	tests := []struct {
		hi, lo uint32
		want   int64
	}{
		{0, 0, 0},
		{0, 4294967295, 4294967295},
		{1, 0, 4294967296},
		{2, 0, 8 << 30},
		{3, 3221225472, 16106127360},
	}
	for _, tt := range tests {
		if got := size(tt.hi, tt.lo); got != tt.want {
			t.Fatalf("size(%d, %d) = %d, want %d", tt.hi, tt.lo, got, tt.want)
		}
	}
}

func TestGroupStatusConversion(t *testing.T) {
	groups := loadGroups(t, "listgroups.json")

	got := groupStatus(groups[0], 5242880)
	want := core.DownloadStatus{
		ID:        downloadingID,
		State:     core.DownloadDownloading,
		Name:      "Arrival.2016.1080p.BluRay.x264-GROUP",
		Progress:  0.5,
		BytesDone: 4 << 30,
		Size:      8 << 30,
		DownRate:  5242880,
		// NZBGet publishes no estimate; remaining over rate is the only one
		// there is.
		ETASeconds: (4 << 30) / 5242880,
		SavePath:   "/downloads/intermediate/Arrival.2016.1080p.BluRay.x264-GROUP",
	}
	if got != want {
		t.Fatalf("status =\n%+v\nwant\n%+v", got, want)
	}

	// A queued group is not the one transferring, so the server-wide rate is
	// not its rate — otherwise a queue of ten all appear to move at once.
	queued := groupStatus(groups[1], 5242880)
	if queued.DownRate != 0 || queued.ETASeconds != -1 {
		t.Fatalf("queued = %+v, want no rate and no estimate", queued)
	}

	unpacking := groupStatus(groups[2], 5242880)
	if unpacking.State != core.DownloadDownloading {
		t.Fatalf("state = %q: a download still unpacking must not be importable", unpacking.State)
	}
	if unpacking.Progress != 1 {
		t.Fatalf("progress = %v, want 1 once nothing is remaining", unpacking.Progress)
	}

	paused := groupStatus(groups[3], 5242880)
	if paused.State != core.DownloadPaused || paused.DownRate != 0 {
		t.Fatalf("paused = %+v", paused)
	}
}

func TestHistoryStatusConversion(t *testing.T) {
	items := loadHistory(t, "history.json")

	done := historyStatus(items[0])
	want := core.DownloadStatus{
		ID:         completedID,
		State:      core.DownloadCompleted,
		Name:       "Sicario.2015.1080p.BluRay.x264-GROUP",
		Progress:   1,
		BytesDone:  9663676416,
		Size:       9663676416,
		ETASeconds: -1,
		// FinalDir wins over DestDir: post-processing moved the payload there.
		SavePath: "/downloads/complete/caravan-movies/Sicario.2015.1080p.BluRay.x264-GROUP",
	}
	if done != want {
		t.Fatalf("status =\n%+v\nwant\n%+v", done, want)
	}

	failed := historyStatus(items[1])
	if failed.State != core.DownloadFailed {
		t.Fatalf("state = %q, want failed", failed.State)
	}
	if !strings.Contains(failed.Error, "FAILURE/PAR") || !strings.Contains(failed.Error, "par repair failed") {
		t.Fatalf("error = %q, want NZBGet's own status and what it means", failed.Error)
	}
	// A failure has only what it actually fetched, not the whole download.
	if failed.BytesDone != 2147483648 || failed.Progress >= 1 {
		t.Fatalf("failure = %+v, want its real partial progress", failed)
	}
	// No FinalDir: the intermediate directory is where the files are.
	if failed.SavePath != "/downloads/intermediate/Broken.Release.2019.1080p.BluRay-GROUP" {
		t.Fatalf("save path = %q, want the DestDir fallback", failed.SavePath)
	}

	warned := historyStatus(items[2])
	if warned.State != core.DownloadCompleted {
		t.Fatalf("state = %q: a post-processing warning must not hide a finished download", warned.State)
	}
	if warned.Error != "" {
		t.Fatalf("a completed download carried an error: %q", warned.Error)
	}
	// Downloaded exceeds the size because par2 blocks count too.
	if warned.BytesDone != warned.Size {
		t.Fatalf("bytes done = %d, want it capped at the size %d", warned.BytesDone, warned.Size)
	}
}

func TestFailureTextNamesTheStage(t *testing.T) {
	tests := []struct {
		name string
		item HistoryItem
		want string
	}{
		{"par", HistoryItem{Status: "FAILURE/PAR", ParStatus: "FAILURE"}, "par repair failed"},
		{"unpack", HistoryItem{Status: "FAILURE/UNPACK", UnpackStatus: "FAILURE"}, "unpack failed"},
		{"password", HistoryItem{Status: "FAILURE/UNPACK", UnpackStatus: "PASSWORD"}, "password protected"},
		{"space", HistoryItem{Status: "FAILURE/UNPACK", UnpackStatus: "SPACE"}, "disk space"},
		{"deleted", HistoryItem{Status: "DELETED/MANUAL"}, "DELETED/MANUAL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := failureText(tt.item); !strings.Contains(got, tt.want) {
				t.Fatalf("failureText = %q, want it to mention %q", got, tt.want)
			}
		})
	}
}

func TestParseID(t *testing.T) {
	tests := []struct {
		in   core.DownloadID
		want int64
		ok   bool
	}{
		{"41", 41, true},
		{"  41  ", 41, true},
		{"0", 0, false},
		{"-1", 0, false},
		{"", 0, false},
		{"abc", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseID(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Fatalf("parseID(%q) = %d, %v, want %d, %v", tt.in, got, ok, tt.want, tt.ok)
		}
	}
}

// A download crosses from the queue to the history the moment NZBGet is done
// with it, so "everything Caravan added" is always both lists.
func TestListMergesQueueAndHistory(t *testing.T) {
	e, _ := newEngine(t)

	list, err := e.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 8 {
		t.Fatalf("list = %d, want the 4 queued and 4 history rows", len(list))
	}

	seen := map[core.DownloadID]core.DownloadState{}
	for _, s := range list {
		seen[s.ID] = s.State
	}
	if seen[downloadingID] != core.DownloadDownloading {
		t.Fatalf("queued download missing from the list")
	}
	if seen[completedID] != core.DownloadCompleted {
		t.Fatalf("completed download missing: without the history half nothing is ever importable")
	}
}

func TestListFiltersByTheConfiguredCategory(t *testing.T) {
	e, _ := newEngine(t)
	e.cfg.Category = "caravan-movies"

	list, err := e.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, s := range list {
		if s.ID == strangerID || s.ID == stranger2ID || s.ID == queuedID {
			t.Fatalf("a download from another category leaked into the queue: %s", s.ID)
		}
	}
	if len(list) != 5 {
		t.Fatalf("list = %d, want the 2 queued and 3 history rows in the category", len(list))
	}
}

func TestStatusFindsADownloadInEitherList(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()

	live, err := e.Status(ctx, downloadingID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if live.State != core.DownloadDownloading || live.DownRate == 0 {
		t.Fatalf("status = %+v, want a live rate", live)
	}

	done, err := e.Status(ctx, completedID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if done.State != core.DownloadCompleted || done.SavePath == "" {
		t.Fatalf("completed status = %+v, want a payload path", done)
	}
}

func TestStatusOfAnUnknownDownloadIsNotFound(t *testing.T) {
	e, _ := newEngine(t)

	if _, err := e.Status(context.Background(), "999999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestStatusOfAnUnparseableIDIsNotFoundWithoutACall(t *testing.T) {
	e, f := newEngine(t)

	for _, id := range []core.DownloadID{"", "abc", "0"} {
		if _, err := e.Status(context.Background(), id); !errors.Is(err, ErrNotFound) {
			t.Fatalf("Status(%q) err = %v, want ErrNotFound", id, err)
		}
	}
	if len(f.seen("listgroups")) != 0 {
		t.Fatalf("an id that is not an NZBID must not be asked about at all")
	}
}

// NZBGet mints a *different* id once it has fetched a URL, so the NZB is
// uploaded and the id NZBGet answers with is the real queue entry's.
func TestAddUploadsTheNZBAndReturnsTheNZBID(t *testing.T) {
	e, f := newEngine(t)
	e.cfg.Category = "caravan-movies"
	indexer, asked := indexerServer(t, nzbBody, http.StatusOK)

	link := indexer.URL + "/getnzb/abc?apikey=indexer-key"
	id, err := e.Add(context.Background(), core.Release{
		Title:       "Sicario.2015.1080p.BluRay-GROUP",
		Protocol:    core.ProtocolUsenet,
		DownloadURL: link,
	}, core.AddOpts{Category: "movies"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id == "" {
		t.Fatalf("Add returned no download id")
	}
	if len(*asked) != 1 {
		t.Fatalf("indexer requests = %d, want 1", len(*asked))
	}

	params := f.seen("append")[0].Params
	// The NZB bytes, not the link: the link carries the indexer's API key and
	// would end up in NZBGet's queue, UI and logs.
	if content, _ := params[1].(string); strings.Contains(content, "indexer-key") {
		t.Fatalf("the indexer's link was sent to NZBGet instead of the NZB")
	}
	if params[0] != "Sicario.2015.1080p.BluRay-GROUP.nzb" {
		t.Fatalf("filename = %v, want the release title with an .nzb suffix", params[0])
	}
	if params[2] != "caravan-movies" {
		t.Fatalf("category = %v, want the client's own configured category", params[2])
	}

	// The id is usable straight away: the download is already in the queue.
	if _, err := e.Status(context.Background(), id); err != nil {
		t.Fatalf("Status of the new download: %v", err)
	}
}

// An indexer answers a rate limit, an expired key or a missing release with a
// 200 and an HTML page far more often than with a status code. Base64-ing that
// into NZBGet produces a download that fails minutes later for no stated
// reason.
func TestAddRejectsAnIndexerAnswerThatIsNotAnNZB(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"html error page", "<!DOCTYPE html><html><body>Rate limit reached</body></html>", http.StatusOK},
		{"plain text", "Invalid API key", http.StatusOK},
		{"empty", "", http.StatusOK},
		{"http failure", "not found", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, f := newEngine(t)
			indexer, _ := indexerServer(t, tt.body, tt.status)

			_, err := e.Add(context.Background(), core.Release{
				Title:       "Some.Release",
				Protocol:    core.ProtocolUsenet,
				DownloadURL: indexer.URL + "/getnzb/1?apikey=indexer-key-sentinel",
			}, core.AddOpts{})
			if err == nil {
				t.Fatalf("Add accepted an answer that is not an NZB")
			}
			if strings.Contains(err.Error(), "indexer-key-sentinel") {
				t.Fatalf("error quoted the indexer's credential: %q", err.Error())
			}
			if len(f.seen("append")) != 0 {
				t.Fatalf("a body that is not an NZB reached NZBGet")
			}
		})
	}

	// A gzip body is named rather than lumped in, because "not an NZB" would
	// be unactionable for a file that is one.
	if err := looksLikeNZB([]byte{0x1f, 0x8b, 0x08, 0x00}); err == nil || !strings.Contains(err.Error(), "gzip") {
		t.Fatalf("err = %v, want the gzip case named", err)
	}

	// What a real NZB looks like, in the shapes indexers actually serve it.
	accepted := []string{
		nzbBody,
		"  \n<?xml version=\"1.0\"?><nzb/>",
		"\uFEFF<?xml version=\"1.0\" encoding=\"utf-8\"?>\n<!DOCTYPE nzb PUBLIC \"-//newzBin//DTD NZB 1.1//EN\" \"http://www.newzbin.com/DTD/nzb/nzb-1.1.dtd\">\n<nzb xmlns=\"http://www.newzbin.com/DTD/2003/nzb\"></nzb>",
		"<NZB></NZB>",
	}
	for _, body := range accepted {
		if err := looksLikeNZB([]byte(body)); err != nil {
			t.Fatalf("a real NZB was rejected (%q): %v", body[:min(len(body), 40)], err)
		}
	}
}

func TestAddRejectsTorrentsAndEmptyURLs(t *testing.T) {
	e, _ := newEngine(t)

	if _, err := e.Add(context.Background(), core.Release{
		Title:       "Some.Release",
		Protocol:    core.ProtocolTorrent,
		DownloadURL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
	}, core.AddOpts{}); err == nil {
		t.Fatalf("Add accepted a torrent release")
	}

	if _, err := e.Add(context.Background(), core.Release{
		Title:    "Some.Release",
		Protocol: core.ProtocolUsenet,
	}, core.AddOpts{}); err == nil {
		t.Fatalf("Add accepted a release with no download url")
	}
}

func TestNZBFilename(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Sicario.2015-GROUP", "Sicario.2015-GROUP.nzb"},
		{"Sicario.2015-GROUP.nzb", "Sicario.2015-GROUP.nzb"},
		{"Sicario.2015-GROUP.NZB", "Sicario.2015-GROUP.NZB"},
		{"  spaced  ", "spaced.nzb"},
		{"", "caravan.nzb"},
	}
	for _, tt := range tests {
		if got := nzbFilename(tt.in); got != tt.want {
			t.Fatalf("nzbFilename(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestPauseAndResume(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()

	if err := e.Pause(ctx, downloadingID); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	got, err := e.Status(ctx, downloadingID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.State != core.DownloadPaused {
		t.Fatalf("state = %q, want paused", got.State)
	}

	if err := e.Resume(ctx, downloadingID); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got, err = e.Status(ctx, downloadingID); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if got.State != core.DownloadDownloading {
		t.Fatalf("state = %q, want downloading", got.State)
	}
}

// A download that has just finished into the history cannot be paused, and
// saying so would turn a harmless race into an error the user sees.
func TestPausingADownloadThatIsAlreadyInHistoryIsNotAnError(t *testing.T) {
	e, _ := newEngine(t)

	if err := e.Pause(context.Background(), completedID); err != nil {
		t.Fatalf("Pause of a finished download: %v", err)
	}
}

// NZBGet answers an edit aimed at the wrong list with a plain false, which is
// what picks the second call.
func TestRemoveFindsTheDownloadInEitherList(t *testing.T) {
	e, f := newEngine(t)
	ctx := context.Background()

	if err := e.Remove(ctx, downloadingID, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := e.Status(ctx, downloadingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound after removal", err)
	}
	// A queued download needs one call; the history is not touched.
	if got := len(f.seen("editqueue")); got != 1 {
		t.Fatalf("editqueue calls = %d, want 1 for a queued download", got)
	}

	if err := e.Remove(ctx, completedID, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := e.Status(ctx, completedID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound after removal", err)
	}
	calls := f.seen("editqueue")
	if len(calls) != 3 {
		t.Fatalf("editqueue calls = %d, want the queue probe and the history delete", len(calls))
	}
	if calls[1].Params[0] != EditGroupFinalDelete || calls[2].Params[0] != EditHistoryFinalDelete {
		t.Fatalf("commands = %v, %v", calls[1].Params[0], calls[2].Params[0])
	}

	for _, g := range f.queued() {
		if id(g.NZBID) == downloadingID {
			t.Fatalf("the queued download survived removal")
		}
	}
	for _, h := range f.historyRows() {
		if id(h.NZBID) == completedID {
			t.Fatalf("the history row survived removal")
		}
	}
}

// Removal is idempotent: a download neither list knows is already in the state
// the caller asked for.
func TestRemovingAnUnknownDownloadSucceeds(t *testing.T) {
	e, _ := newEngine(t)

	if err := e.Remove(context.Background(), "999999", true); err != nil {
		t.Fatalf("Remove of an unknown download: %v", err)
	}
	if err := e.Remove(context.Background(), "not-an-id", true); err != nil {
		t.Fatalf("Remove of an unparseable id: %v", err)
	}
}

func TestEngineName(t *testing.T) {
	if EngineName != "nzbget" {
		t.Fatalf("EngineName = %q", EngineName)
	}
}

// Usenet has no swarm, so nothing here may ever claim a seeding state or a
// share ratio the queue would render.
func TestUsenetNeverSeeds(t *testing.T) {
	e, _ := newEngine(t)

	list, err := e.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, s := range list {
		if s.State == core.DownloadSeeding {
			t.Fatalf("%s reported seeding", s.ID)
		}
		if s.Ratio != 0 || s.UpRate != 0 {
			t.Fatalf("%s reported a share ratio or upload rate: %+v", s.ID, s)
		}
	}
}
