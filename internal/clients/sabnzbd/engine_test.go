package sabnzbd

import (
	"context"
	"errors"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// The nzo_ids in testdata/queue.json and testdata/history.json.
const (
	downloadingID = "SABnzbd_nzo_a1b2c3"
	strangerID    = "SABnzbd_nzo_stranger"
	completedID   = "SABnzbd_nzo_done01"
)

// The state table is the contract between SABnzbd's vocabulary and Caravan's,
// and the import watcher fires on one of the six values, so every status
// SABnzbd can report is pinned here — twice, because the same word means
// different things in the queue and in the history.
func TestQueueStateMapping(t *testing.T) {
	tests := []struct {
		sab  string
		want core.DownloadState
	}{
		{statusIdle, core.DownloadQueued},
		{statusQueued, core.DownloadQueued},
		// Deliberately held back until the articles have propagated: nothing
		// is moving yet.
		{statusPropagating, core.DownloadQueued},

		{statusPaused, core.DownloadPaused},

		// Fetching the NZB from the link Caravan handed over is a transfer
		// that is already under way.
		{statusGrabbing, core.DownloadDownloading},
		{statusFetching, core.DownloadDownloading},
		{statusDownloading, core.DownloadDownloading},
		// Still touching the files: importing now would copy a moving target.
		{statusChecking, core.DownloadDownloading},
		{statusQuickCheck, core.DownloadDownloading},
		{statusVerifying, core.DownloadDownloading},
		{statusRepairing, core.DownloadDownloading},
		{statusExtracting, core.DownloadDownloading},
		{statusMoving, core.DownloadDownloading},
		{statusRunning, core.DownloadDownloading},

		{statusCompleted, core.DownloadCompleted},
		{statusFailed, core.DownloadFailed},
		{statusDeleted, core.DownloadFailed},

		// Nothing is claimed for a status we do not recognise.
		{"SomeFutureStatus", core.DownloadQueued},
		{"", core.DownloadQueued},
	}
	for _, tt := range tests {
		t.Run(tt.sab, func(t *testing.T) {
			if got := mapQueueState(tt.sab); got != tt.want {
				t.Fatalf("mapQueueState(%q) = %q, want %q", tt.sab, got, tt.want)
			}
		})
	}
}

func TestHistoryStateMapping(t *testing.T) {
	tests := []struct {
		sab  string
		want core.DownloadState
	}{
		{statusCompleted, core.DownloadCompleted},
		{statusFailed, core.DownloadFailed},
		{statusDeleted, core.DownloadFailed},

		// In the history, Queued means "waiting for post-processing", not
		// "waiting to download". This is the entry the two maps exist for.
		{statusQueued, core.DownloadDownloading},
		{statusChecking, core.DownloadDownloading},
		{statusQuickCheck, core.DownloadDownloading},
		{statusVerifying, core.DownloadDownloading},
		{statusRepairing, core.DownloadDownloading},
		{statusExtracting, core.DownloadDownloading},
		{statusMoving, core.DownloadDownloading},
		{statusRunning, core.DownloadDownloading},
		{statusFetching, core.DownloadDownloading},

		{"SomeFutureStatus", core.DownloadQueued},
	}
	for _, tt := range tests {
		t.Run(tt.sab, func(t *testing.T) {
			if got := mapHistoryState(tt.sab); got != tt.want {
				t.Fatalf("mapHistoryState(%q) = %q, want %q", tt.sab, got, tt.want)
			}
		})
	}

	// The one word whose meaning depends on which list it came from.
	if mapQueueState(statusQueued) == mapHistoryState(statusQueued) {
		t.Fatalf("Queued must not mean the same thing in the queue and the history")
	}
}

func TestQueueStatusConversion(t *testing.T) {
	slots := loadQueue(t, "queue.json").Slots

	got := queueStatus(slots[0], 5120*1024)
	want := core.DownloadStatus{
		ID:         downloadingID,
		State:      core.DownloadDownloading,
		Name:       "Arrival.2016.1080p.BluRay.x264-GROUP",
		Progress:   0.5,
		BytesDone:  4 << 30,
		Size:       8 << 30,
		DownRate:   5120 * 1024,
		ETASeconds: 13*60 + 39,
	}
	if got != want {
		t.Fatalf("status =\n%+v\nwant\n%+v", got, want)
	}

	// A queued job is not the one transferring, so the queue-wide rate is not
	// its rate — otherwise a queue of ten all appear to move at full speed.
	queued := queueStatus(slots[1], 5120*1024)
	if queued.DownRate != 0 {
		t.Fatalf("down rate = %d, want 0 for a job that is not transferring", queued.DownRate)
	}
	// "0:00:00" is not an estimate of "now".
	if queued.ETASeconds != -1 {
		t.Fatalf("eta = %d, want -1", queued.ETASeconds)
	}

	paused := queueStatus(slots[2], 5120*1024)
	if paused.State != core.DownloadPaused || paused.Progress != 0.5 {
		t.Fatalf("paused = %+v", paused)
	}
	// SABnzbd does not say where a queued job will land, and guessing would
	// point the importer at a temporary directory.
	if paused.SavePath != "" {
		t.Fatalf("save path = %q, want empty until the job reaches history", paused.SavePath)
	}
}

func TestHistoryStatusConversion(t *testing.T) {
	slots := loadHistory(t, "history.json")

	done := historyStatus(slots[0])
	want := core.DownloadStatus{
		ID:         completedID,
		State:      core.DownloadCompleted,
		Name:       "Sicario.2015.1080p.BluRay.x264-GROUP",
		Progress:   1,
		BytesDone:  9663676416,
		Size:       9663676416,
		ETASeconds: -1,
		SavePath:   "/downloads/complete/caravan-movies/Sicario.2015.1080p.BluRay.x264-GROUP",
	}
	if done != want {
		t.Fatalf("status =\n%+v\nwant\n%+v", done, want)
	}

	failed := historyStatus(slots[1])
	if failed.State != core.DownloadFailed {
		t.Fatalf("state = %q, want failed", failed.State)
	}
	if failed.Error == "" {
		t.Fatalf("a failure carried no message")
	}
	// A failure has only what it actually fetched, not the whole job.
	if failed.BytesDone != 2147483648 || failed.Progress >= 1 {
		t.Fatalf("failure = %+v, want its real partial progress", failed)
	}
	// No storage yet: the working directory is where the files are.
	if failed.SavePath != "/downloads/incomplete/Broken.Release.2019.1080p.BluRay-GROUP" {
		t.Fatalf("save path = %q, want the working directory fallback", failed.SavePath)
	}

	pp := historyStatus(slots[2])
	if pp.State != core.DownloadDownloading {
		t.Fatalf("state = %q: a job still unpacking must not be importable", pp.State)
	}
	// Downloaded exceeds the job size because par2 blocks count too.
	if pp.BytesDone != pp.Size {
		t.Fatalf("bytes done = %d, want it capped at the size %d", pp.BytesDone, pp.Size)
	}
}

func TestHistoryStatusNamesAFailureWithNoMessage(t *testing.T) {
	got := historyStatus(HistorySlot{NZOID: "x", Status: statusFailed, Bytes: 10})
	if got.Error == "" {
		t.Fatalf("a failure with no fail_message carried no message")
	}
}

func TestParseTimeLeft(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"0:13:39", 13*60 + 39},
		{"1:00:00", 3600},
		{"2:03:04:05", 2*86400 + 3*3600 + 4*60 + 5},
		// SABnzbd prints this for a paused job and for one it has no rate
		// for — neither is an estimate.
		{"0:00:00", -1},
		{"", -1},
		{"soon", -1},
		{"12:34", -1},
		{"-1:00:00", -1},
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			if got := parseTimeLeft(tt.in); got != tt.want {
				t.Fatalf("parseTimeLeft(%q) = %d, want %d", tt.in, got, tt.want)
			}
		})
	}
}

// A job crosses from the queue to the history the moment its transfer ends, so
// "everything Caravan added" is always both lists.
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
		t.Fatalf("queued job missing from the list")
	}
	if seen[completedID] != core.DownloadCompleted {
		t.Fatalf("completed job missing: without the history half nothing is ever importable")
	}
}

func TestListFiltersByTheConfiguredCategory(t *testing.T) {
	e, f := newEngine(t)
	e.cfg.Category = "caravan-movies"

	list, err := e.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, s := range list {
		if s.ID == strangerID || s.ID == "SABnzbd_nzo_stranger2" {
			t.Fatalf("a job from another category leaked into the queue: %s", s.ID)
		}
	}
	if len(list) != 5 {
		t.Fatalf("list = %d, want the 2 queued and 3 history rows in the category", len(list))
	}

	// The filter is asked of SABnzbd rather than applied afterwards.
	if got := f.seen("queue")[0].Params.Get("cat"); got != "caravan-movies" {
		t.Fatalf("cat = %q", got)
	}
	if got := f.seen("history")[0].Params.Get("cat"); got != "caravan-movies" {
		t.Fatalf("cat = %q", got)
	}
}

func TestStatusFindsAJobInEitherList(t *testing.T) {
	e, _ := newEngine(t)
	ctx := context.Background()

	live, err := e.Status(ctx, downloadingID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if live.State != core.DownloadDownloading {
		t.Fatalf("state = %q", live.State)
	}

	done, err := e.Status(ctx, completedID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if done.State != core.DownloadCompleted || done.SavePath == "" {
		t.Fatalf("completed status = %+v, want a payload path", done)
	}
}

func TestStatusOfAnUnknownJobIsNotFound(t *testing.T) {
	e, _ := newEngine(t)

	if _, err := e.Status(context.Background(), "SABnzbd_nzo_nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// SABnzbd reads an empty nzo_ids filter as "no filter" and answers with the
// whole queue. Trusting the first row would report a stranger's job as this
// download.
func TestStatusOfAnEmptyIDIsNotFound(t *testing.T) {
	e, f := newEngine(t)

	if _, err := e.Status(context.Background(), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if len(f.seen("queue")) != 0 {
		t.Fatalf("an empty id must not be asked about at all")
	}
}

func TestAddHandsOverTheLinkAndReturnsTheNZOID(t *testing.T) {
	e, f := newEngine(t)
	e.cfg.Category = "caravan-movies"

	id, err := e.Add(context.Background(), core.Release{
		Title:       "Sicario.2015.1080p.BluRay-GROUP",
		Protocol:    core.ProtocolUsenet,
		DownloadURL: "https://indexer.example/getnzb/abc123",
	}, core.AddOpts{Category: "movies"})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if id == "" {
		t.Fatalf("Add returned no download id")
	}

	params := f.seen("addurl")[0].Params
	if params.Get("name") != "https://indexer.example/getnzb/abc123" {
		t.Fatalf("name = %q, want the NZB link", params.Get("name"))
	}
	// The release title, not Caravan's internal routing label.
	if params.Get("nzbname") != "Sicario.2015.1080p.BluRay-GROUP" {
		t.Fatalf("nzbname = %q", params.Get("nzbname"))
	}
	if params.Get("cat") != "caravan-movies" {
		t.Fatalf("cat = %q, want the client's own configured category", params.Get("cat"))
	}

	// The id is usable straight away: the job is already in the queue.
	got, err := e.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status of the new download: %v", err)
	}
	if got.State != core.DownloadDownloading {
		t.Fatalf("state = %q, want downloading while SABnzbd fetches the NZB", got.State)
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

// A download is in one list or the other and the caller cannot know which, so
// removal clears both.
func TestRemoveClearsBothLists(t *testing.T) {
	e, f := newEngine(t)
	ctx := context.Background()

	if err := e.Remove(ctx, downloadingID, true); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := e.Status(ctx, downloadingID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound after removal", err)
	}
	if err := e.Remove(ctx, completedID, false); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := e.Status(ctx, completedID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound after removal", err)
	}

	for _, s := range f.queued() {
		if s.NZOID == downloadingID {
			t.Fatalf("the queued job survived removal")
		}
	}
	for _, s := range f.historyRows() {
		if s.NZOID == completedID {
			t.Fatalf("the history row survived removal")
		}
	}

	// del_files travels to both lists, not just to whichever one answered.
	if got := f.seen("queue")[0].Params.Get("del_files"); got != "1" {
		t.Fatalf("queue del_files = %q", got)
	}
	if got := f.seen("history")[0].Params.Get("del_files"); got != "1" {
		t.Fatalf("history del_files = %q", got)
	}
}

func TestEngineName(t *testing.T) {
	if EngineName != "sabnzbd" {
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
