package download

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// The health model in one test: a client is not condemned for a single missed
// poll, it is condemned on the Nth, the condemnation is announced exactly
// once, and answering again clears it exactly once.
func TestHealthMarksAClientDownAfterConsecutiveFailures(t *testing.T) {
	h := NewHealth(3)
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	h.now = func() time.Time { return at }
	boom := errors.New("connection refused")

	for i := 1; i < 3; i++ {
		if got := h.Observe(7, "Seedbox", "qbittorrent", boom); got != "" {
			t.Fatalf("failure %d reported %q, want no transition before the threshold", i, got)
		}
		if h.Reason(7) != "" {
			t.Fatalf("client marked unreachable after %d of 3 failures", i)
		}
	}

	if got := h.Observe(7, "Seedbox", "qbittorrent", boom); got != HealthDown {
		t.Fatalf("third failure reported %q, want %q", got, HealthDown)
	}
	if got := h.Reason(7); got != "connection refused" {
		t.Fatalf("reason = %q, want the poll's own message", got)
	}
	// Still down, but the feed hears about it once.
	if got := h.Observe(7, "Seedbox", "qbittorrent", boom); got != "" {
		t.Fatalf("fourth failure reported %q, want no repeat transition", got)
	}

	unhealthy := h.Unhealthy()
	if len(unhealthy) != 1 {
		t.Fatalf("unhealthy = %+v, want one client", unhealthy)
	}
	want := core.DownloadClientHealth{
		ID: 7, Name: "Seedbox", Type: "qbittorrent", Error: "connection refused", Since: at,
	}
	if unhealthy[0] != want {
		t.Fatalf("unhealthy[0] = %+v, want %+v", unhealthy[0], want)
	}

	if got := h.Observe(7, "Seedbox", "qbittorrent", nil); got != HealthUp {
		t.Fatalf("recovery reported %q, want %q", got, HealthUp)
	}
	if got := h.Observe(7, "Seedbox", "qbittorrent", nil); got != "" {
		t.Fatalf("second good poll reported %q, want no repeat transition", got)
	}
	if h.Reason(7) != "" || len(h.Unhealthy()) != 0 {
		t.Fatalf("client still reported unreachable after recovering: %+v", h.Unhealthy())
	}
}

// A run of failures interrupted by a success never crosses the threshold: the
// count is consecutive failures, not failures ever seen.
func TestHealthResetsTheFailureCountOnASuccessfulPoll(t *testing.T) {
	h := NewHealth(3)
	boom := errors.New("i/o timeout")

	h.Observe(1, "Box", "sabnzbd", boom)
	h.Observe(1, "Box", "sabnzbd", boom)
	h.Observe(1, "Box", "sabnzbd", nil)
	h.Observe(1, "Box", "sabnzbd", boom)
	h.Observe(1, "Box", "sabnzbd", boom)

	if got := h.Reason(1); got != "" {
		t.Fatalf("reason = %q, want the client still considered healthy", got)
	}
}

// One dead client must not affect another. This is the "pauses only that
// queue" half of the acceptance criterion, at the level that decides it.
func TestHealthIsPerClient(t *testing.T) {
	h := NewHealth(2)
	boom := errors.New("connection refused")

	h.Observe(1, "Dead", "qbittorrent", boom)
	h.Observe(1, "Dead", "qbittorrent", boom)
	h.Observe(2, "Alive", "sabnzbd", nil)
	h.Observe(2, "Alive", "sabnzbd", nil)

	if h.Reason(1) == "" {
		t.Fatal("the failing client is not marked unreachable")
	}
	if got := h.Reason(2); got != "" {
		t.Fatalf("the healthy client was marked unreachable: %q", got)
	}
	if got := h.Unhealthy(); len(got) != 1 || got[0].ID != 1 {
		t.Fatalf("unhealthy = %+v, want only the failing client", got)
	}
}

// A banner about a client the user just deleted is a banner they cannot act
// on, so a client that leaves the configuration leaves the tracker with it.
func TestHealthForgetsClientsThatAreNoLongerConfigured(t *testing.T) {
	h := NewHealth(1)
	h.Observe(1, "Gone", "nzbget", errors.New("connection refused"))
	if len(h.Unhealthy()) != 1 {
		t.Fatal("client was not marked unreachable")
	}

	h.Retain(map[int64]bool{2: true})
	if got := h.Unhealthy(); len(got) != 0 {
		t.Fatalf("unhealthy = %+v, want the removed client forgotten", got)
	}
}

// The router is where a poll outcome is observed, and it must observe every
// engine it polls — including the ones that answered, or a client would never
// recover.
func TestRouterReportsEveryPollOutcome(t *testing.T) {
	good := newFakeEngine("sabnzbd", "nzo_1")
	bad := newFakeEngine("qbittorrent", "hash_1")
	bad.listErr = errors.New("connection refused")

	seen := map[string]error{}
	report := func(name string) func(error) {
		return func(err error) { seen[name] = err }
	}
	router := NewRouter(staticTable(
		Route{Name: EngineName, Protocol: core.ProtocolTorrent, Engine: newFakeEngine(EngineName, "hash_embedded")},
		Route{Name: "sabnzbd", Protocol: core.ProtocolUsenet, Engine: good, Report: report("sabnzbd")},
		Route{Name: "qbittorrent", Engine: bad, Report: report("qbittorrent")},
	))

	statuses, err := router.List(context.Background())
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if seen["sabnzbd"] != nil {
		t.Fatalf("healthy client reported %v, want nil", seen["sabnzbd"])
	}
	if seen["qbittorrent"] == nil {
		t.Fatal("unreachable client's failure was not reported")
	}

	// A dead client contributes nothing; everyone else's downloads are still
	// listed, and each one is stamped with the backend that holds it.
	engines := map[core.DownloadID]string{}
	for _, s := range statuses {
		engines[s.ID] = s.Engine
	}
	if engines["hash_embedded"] != EngineName {
		t.Fatalf("embedded download engine = %q, want %q", engines["hash_embedded"], EngineName)
	}
	if engines["nzo_1"] != "sabnzbd" {
		t.Fatalf("usenet download engine = %q, want %q", engines["nzo_1"], "sabnzbd")
	}
	if _, ok := engines["hash_1"]; ok {
		t.Fatal("an unreachable client's downloads were listed as live")
	}
}

// A grab routed to a client that has stopped answering fails immediately with
// the poll's own reason, instead of producing a download nobody can see.
func TestRouterRefusesToAddToAnUnreachableClient(t *testing.T) {
	client := newFakeEngine("qbittorrent")
	embedded := newFakeEngine(EngineName)
	router := NewRouter(staticTable(
		Route{
			Name: "qbittorrent", Protocol: core.ProtocolTorrent, Engine: client,
			Unhealthy: "connection refused",
		},
		Route{Name: EngineName, Engine: embedded},
	))

	_, err := router.Add(context.Background(),
		core.Release{Title: "Movie.2020.1080p", Protocol: core.ProtocolTorrent}, core.AddOpts{})
	if !errors.Is(err, ErrClientUnreachable) {
		t.Fatalf("Add error = %v, want %v", err, ErrClientUnreachable)
	}
	if err != nil && !strings.Contains(err.Error(), "connection refused") {
		t.Fatalf("Add error = %q, want it to carry the poll's reason", err)
	}
	if len(client.added) != 0 {
		t.Fatalf("the release was handed to the unreachable client anyway: %+v", client.added)
	}
	if len(embedded.added) != 0 {
		t.Fatalf("the release was silently misrouted to %s: %+v", EngineName, embedded.added)
	}
}

// The embedded engine is not a client and cannot be unreachable: an
// unreachable external client must leave it taking grabs exactly as before.
func TestUnreachableClientLeavesTheEmbeddedEngineAlone(t *testing.T) {
	embedded := newFakeEngine(EngineName, "hash_embedded")
	dead := newFakeEngine("sabnzbd")
	dead.listErr = errors.New("connection refused")
	router := NewRouter(staticTable(
		Route{Name: EngineName, Protocol: core.ProtocolTorrent, Engine: embedded},
		Route{
			Name: "sabnzbd", Protocol: core.ProtocolUsenet, Engine: dead,
			Unhealthy: "connection refused", Report: func(error) {},
		},
	))

	ctx := context.Background()
	if _, err := router.Add(ctx,
		core.Release{Title: "Movie.2020.1080p", Protocol: core.ProtocolTorrent}, core.AddOpts{}); err != nil {
		t.Fatalf("torrent grab failed while a usenet client was down: %v", err)
	}
	statuses, err := router.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(statuses) == 0 {
		t.Fatal("the embedded engine's downloads vanished while a client was down")
	}
	if err := router.Pause(ctx, "hash_embedded"); err != nil {
		t.Fatalf("Pause on the embedded engine failed while a client was down: %v", err)
	}
}
