package usenet

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/usenet/yenc"
)

// insightOf reads one download's insight or fails the test.
func insightOf(t *testing.T, e *Engine, id core.DownloadID) *core.DownloadInsight {
	t.Helper()
	ins, err := e.Insight(context.Background(), id)
	if err != nil {
		t.Fatalf("Insight: %v", err)
	}
	return ins
}

func fileInsight(ins *core.DownloadInsight, name string) (core.UsenetFileInsight, bool) {
	for _, f := range ins.Files {
		if f.Name == name {
			return f, true
		}
	}
	return core.UsenetFileInsight{}, false
}

// The queue drawer's whole reason for existing on a Usenet download: which
// files the NZB indexes and how much of each one is on disk *while it is still
// being fetched*. The pipeline's Tracker only aggregated before this, so a
// half-downloaded release could say "47%" and nothing about which of its files
// that was.
func TestEngineInsightReportsPerFileSegmentsMidDownload(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	rel := stage(t, nntpSrv, map[string][]byte{
		"movie.mkv": []byte(strings.Repeat("caravan usenet payload\n", 400)),
		"extra.nfo": []byte(strings.Repeat("notes\n", 200)),
	}, 500)

	// The provider stops handing out articles after the first two, and stays
	// stopped until this test has looked. That is the only way to observe a
	// download mid-flight without racing it.
	gate := make(chan struct{})
	var served atomic.Int64
	nntpSrv.SetBodyHook(func(string) {
		if served.Add(1) > 2 {
			<-gate
		}
	})
	t.Cleanup(func() { close(gate) })

	e, _ := newTestEngine(t, nntpSrv, newMemStore())
	id := addRelease(t, e, rel, "Mid.Flight.Release")

	var ins *core.DownloadInsight
	waitFor(t, "the first segments to land", func() bool {
		ins = insightOf(t, e, id)
		return ins.SegmentsDone > 0
	})

	if len(ins.Files) != 2 {
		t.Fatalf("files = %#v, want one entry per NZB file", ins.Files)
	}
	if ins.Segments <= ins.SegmentsDone {
		t.Fatalf("segments = %d done of %d, want a download still in flight",
			ins.SegmentsDone, ins.Segments)
	}
	// Per file, not just in aggregate: the counts have to add up to the totals
	// the file list itself reports.
	var segments, done int
	for _, f := range ins.Files {
		if f.Segments == 0 {
			t.Errorf("file %q reports no segments at all", f.Name)
		}
		segments += f.Segments
		done += f.SegmentsDone
	}
	if segments != ins.Segments || done != ins.SegmentsDone {
		t.Errorf("per-file totals = %d/%d, want the aggregate %d/%d",
			done, segments, ins.SegmentsDone, ins.Segments)
	}
	if _, ok := fileInsight(ins, "movie.mkv"); !ok {
		t.Errorf("files = %#v, want the on-disk name movie.mkv", ins.Files)
	}
	// A Usenet download has no peers and no trackers, and says so with empty
	// lists rather than nulls the drawer would have to defend against.
	if len(ins.Peers) != 0 || len(ins.Trackers) != 0 {
		t.Errorf("insight = %#v, want no peers or trackers", ins)
	}
}

// What the repairing phase is working on. par2 reports no live progress, so
// "N segments to reconstruct, in these files" is the whole of the honest answer
// — and the drawer shows an indeterminate stage rather than a fabricated
// percentage.
func TestEngineInsightReportsWhatTheRepairStageIsWorkingOn(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	rel := stagePar2Corpus(t, nntpSrv, nil)
	nntpSrv.Remove(yenc.MessageID("beta.bin", 2))

	// Hold the recovery volumes at the provider, so the repairing phase is
	// observable instead of being over before the test can look at it.
	gate := make(chan struct{})
	nntpSrv.SetBodyHook(func(messageID string) {
		if strings.Contains(messageID, "par2") {
			<-gate
		}
	})
	t.Cleanup(func() { close(gate) })

	e, _ := newTestEngine(t, nntpSrv, newMemStore())
	id := addRelease(t, e, rel, "Repairing.Release")

	var ins *core.DownloadInsight
	waitFor(t, "the download to reach the repairing phase", func() bool {
		st, err := e.Status(context.Background(), id)
		if err != nil || st.Phase != core.PhaseRepairing {
			return false
		}
		ins = insightOf(t, e, id)
		return true
	})

	if ins.DamagedSegments != 1 {
		t.Errorf("damaged segments = %d, want the one article that rotted away", ins.DamagedSegments)
	}
	if len(ins.DamagedFiles) != 1 || ins.DamagedFiles[0] != "beta.bin" {
		t.Errorf("damaged files = %v, want [beta.bin]", ins.DamagedFiles)
	}
	// The file list survives the download stage: the tracker is gone by now,
	// and a drawer that emptied itself the moment par2 started would look like
	// the download had been thrown away.
	if len(ins.Files) == 0 {
		t.Fatal("files = none, want the download stage's frozen breakdown")
	}
	beta, ok := fileInsight(ins, "beta.bin")
	if !ok {
		t.Fatalf("files = %#v, want beta.bin", ins.Files)
	}
	if beta.Complete || beta.SegmentsFailed != 1 {
		t.Errorf("beta.bin = %#v, want one failed segment and not complete", beta)
	}
}

// Once every stage is done the drawer still has a file list — from the frozen
// results while the process lives, and from the NZB after a restart — and every
// file reads as whole.
func TestEngineInsightReportsEveryFileCompleteOnceTheDownloadFinishes(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	rel := stage(t, nntpSrv, map[string][]byte{
		"movie.mkv": []byte(strings.Repeat("payload\n", 300)),
		"extra.nfo": []byte("notes\n"),
	}, 500)

	store := newMemStore()
	e, root := newTestEngine(t, nntpSrv, store)
	id := addRelease(t, e, rel, "Finished.Release")
	waitForState(t, e, id, core.DownloadCompleted)

	ins := insightOf(t, e, id)
	if len(ins.Files) != 2 || ins.FilesComplete != 2 {
		t.Fatalf("insight = %#v, want two complete files", ins)
	}
	for _, f := range ins.Files {
		if !f.Complete || f.SegmentsDone != f.Segments || f.SegmentsFailed != 0 {
			t.Errorf("file %#v, want every segment on disk", f)
		}
	}
	if ins.SegmentsDone != ins.Segments || ins.Segments == 0 {
		t.Errorf("segments = %d of %d, want all of them", ins.SegmentsDone, ins.Segments)
	}
	if ins.DamagedSegments != 0 || len(ins.DamagedFiles) != 0 {
		t.Errorf("insight = %#v, want no repair detail on a clean download", ins)
	}

	// After a restart there are no counters left anywhere, only the NZB. A
	// completed download still has to answer with its files rather than with
	// nothing.
	e.Close()
	restarted := newTestEngineAt(t, root, nntpSrv, store)
	after := insightOf(t, restarted, id)
	if len(after.Files) != 2 || after.FilesComplete != 2 {
		t.Fatalf("insight after restart = %#v, want two complete files", after)
	}
}

func TestEngineInsightIsNotFoundForAnUnknownDownload(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	e, _ := newTestEngine(t, nntpSrv, newMemStore())
	if _, err := e.Insight(context.Background(), "u-nope"); err == nil ||
		!strings.Contains(err.Error(), download.ErrNotFound.Error()) {
		t.Fatalf("Insight of an unknown download = %v, want not found", err)
	}
}
