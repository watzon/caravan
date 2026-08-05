package usenet

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/usenet/nntp"
	"github.com/watzon/caravan/internal/usenet/nntptest"
)

// gate holds every article until it is opened, so a test can have downloads
// that are genuinely in flight rather than already finished.
type gate struct {
	ch   chan struct{}
	once sync.Once
}

func newGate(t *testing.T, srv *nntptest.Server) *gate {
	t.Helper()
	g := &gate{ch: make(chan struct{})}
	srv.SetBodyHook(func(string) { <-g.ch })
	t.Cleanup(g.open)
	return g
}

func (g *gate) open() { g.once.Do(func() { close(g.ch) }) }

// newCappedEngine starts an engine whose downloads are rationed by adm.
func newCappedEngine(t *testing.T, srv *nntptest.Server, adm core.Admitter) *Engine {
	t.Helper()
	e, err := NewEngine(t.TempDir(), EngineOpts{
		Servers:        []nntp.ServerConfig{testServer(srv)},
		NNTP:           fastRetry(),
		Store:          newMemStore(),
		PollInterval:   10 * 1000000, // 10ms
		SkipSpaceCheck: true,
		Admitter:       adm,
	})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	t.Cleanup(func() { e.Close() })
	return e
}

func stateOf(t *testing.T, e *Engine, id core.DownloadID) core.DownloadState {
	t.Helper()
	st, err := e.Status(context.Background(), id)
	if err != nil {
		t.Fatalf("Status %s: %v", id, err)
	}
	return st.State
}

// The complaint this feature answers: every added download used to start at
// once and they starved each other. With a cap of two, the third add is a
// queue entry — visible, ordered, and not touching the news server.
func TestUsenetEngineHoldsAddsOverTheCap(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	newGate(t, nntpSrv)

	adm := download.NewAdmission(download.Caps{Method: map[string]int{EngineName: 2}})
	e := newCappedEngine(t, nntpSrv, adm)

	ids := make([]core.DownloadID, 0, 3)
	for i := 0; i < 3; i++ {
		rel := stage(t, nntpSrv, map[string][]byte{
			fmt.Sprintf("movie%d.mkv", i): []byte(strings.Repeat("payload\n", 300)),
		}, 500)
		ids = append(ids, addRelease(t, e, rel, fmt.Sprintf("Release.%d", i)))
	}

	waitFor(t, "two downloads to be running", func() bool {
		return adm.Active(EngineName) == 2
	})
	if got := stateOf(t, e, ids[2]); got != core.DownloadQueued {
		t.Fatalf("third download is %s, want queued behind the cap", got)
	}
	// Not merely reported as queued — it has no worker and has asked the news
	// server for nothing. A "queue" that still downloads is the bug.
	e.mu.Lock()
	third := e.items[ids[2]]
	running, admitted := third.cancel != nil, third.admitted
	e.mu.Unlock()
	if running || admitted {
		t.Errorf("third download has worker=%v admitted=%v, want neither", running, admitted)
	}
	if adm.Held() != 2 {
		t.Errorf("held slots = %d, want 2", adm.Held())
	}
}

// A slot goes to the download that has been waiting longest, not to whichever
// goroutine happens to ask first.
//
// The gate is what makes this deterministic: with every article held, the one
// admitted download cannot finish and hand its slot on before the test has
// looked at who got it.
func TestUsenetEngineAdmitsTheOldestWaiterWhenASlotFrees(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	g := newGate(t, nntpSrv)

	adm := download.NewAdmission(download.Caps{Method: map[string]int{EngineName: 1}})
	e := newCappedEngine(t, nntpSrv, adm)

	ids := make([]core.DownloadID, 0, 3)
	for i := 0; i < 3; i++ {
		rel := stage(t, nntpSrv, map[string][]byte{
			fmt.Sprintf("movie%d.mkv", i): []byte(strings.Repeat("payload\n", 300)),
		}, 500)
		ids = append(ids, addRelease(t, e, rel, fmt.Sprintf("Release.%d", i)))
	}

	// The first one added is the one running; the other two are in line.
	waitFor(t, "the first download to take the only slot", func() bool {
		return stateOf(t, e, ids[0]) == core.DownloadDownloading
	})
	for _, later := range ids[1:] {
		if got := stateOf(t, e, later); got != core.DownloadQueued {
			t.Fatalf("download %s is %s while the queue is full, want queued", later, got)
		}
	}

	// Free the slot without finishing anything, so the only question the test
	// asks is which waiter gets it.
	if err := e.Pause(context.Background(), ids[0]); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	waitFor(t, "the freed slot to reach the next in line", func() bool {
		return stateOf(t, e, ids[1]) == core.DownloadDownloading
	})
	if got := stateOf(t, e, ids[2]); got != core.DownloadQueued {
		t.Errorf("the last download is %s, want it still queued behind the one added before it", got)
	}

	// And the whole line drains once the articles flow.
	g.open()
	if err := e.Resume(context.Background(), ids[0]); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	for _, id := range ids {
		waitForState(t, e, id, core.DownloadCompleted)
	}
	if adm.Held() != 0 {
		t.Errorf("held slots = %d after everything completed, want 0", adm.Held())
	}
}

// Pausing frees the slot, which is what makes pausing one download start the
// next; and resuming re-enters the queue rather than jumping it.
func TestUsenetEnginePauseFreesASlotAndResumeRequeues(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	g := newGate(t, nntpSrv)

	adm := download.NewAdmission(download.Caps{Method: map[string]int{EngineName: 1}})
	e := newCappedEngine(t, nntpSrv, adm)

	ids := make([]core.DownloadID, 0, 2)
	for i := 0; i < 2; i++ {
		rel := stage(t, nntpSrv, map[string][]byte{
			fmt.Sprintf("movie%d.mkv", i): []byte(strings.Repeat("payload\n", 300)),
		}, 500)
		ids = append(ids, addRelease(t, e, rel, fmt.Sprintf("Release.%d", i)))
	}
	waitFor(t, "the first download to take the only slot", func() bool {
		return adm.Held() == 1 && stateOf(t, e, ids[0]) == core.DownloadDownloading
	})
	if got := stateOf(t, e, ids[1]); got != core.DownloadQueued {
		t.Fatalf("second download is %s, want queued", got)
	}

	if err := e.Pause(context.Background(), ids[0]); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	waitFor(t, "the paused download's slot to reach the one behind it", func() bool {
		return stateOf(t, e, ids[1]) == core.DownloadDownloading
	})
	if got := stateOf(t, e, ids[0]); got != core.DownloadPaused {
		t.Errorf("paused download is %s, want paused", got)
	}

	// Resuming into a full queue is queued, not running: the slot is taken.
	if err := e.Resume(context.Background(), ids[0]); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if got := stateOf(t, e, ids[0]); got != core.DownloadQueued {
		t.Errorf("resumed download is %s, want queued behind the one that took its slot", got)
	}
	g.open()
}

// With no caps configured nothing is asked and nothing waits, which is what
// every Caravan did before this existed.
func TestUsenetEngineWithoutCapsStartsEverything(t *testing.T) {
	nntpSrv := startFakeNNTP(t)
	g := newGate(t, nntpSrv)
	adm := download.NewAdmission(download.Caps{})
	e := newCappedEngine(t, nntpSrv, adm)

	ids := make([]core.DownloadID, 0, 3)
	for i := 0; i < 3; i++ {
		rel := stage(t, nntpSrv, map[string][]byte{
			fmt.Sprintf("movie%d.mkv", i): []byte(strings.Repeat("payload\n", 300)),
		}, 500)
		ids = append(ids, addRelease(t, e, rel, fmt.Sprintf("Release.%d", i)))
	}
	waitFor(t, "every download to be running at once", func() bool {
		for _, id := range ids {
			if stateOf(t, e, id) != core.DownloadDownloading {
				return false
			}
		}
		return true
	})
	g.open()
}
