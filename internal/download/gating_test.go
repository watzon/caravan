package download

import (
	"context"
	"errors"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// clientEngine is a fake external download client: it records how each release
// was handed over and whether it was ever told to start.
//
// The distinction is the whole point of this file. A client that is handed a
// release paused and never resumed has done no work — no connections, no
// bytes, nothing on its disk — which is what "held by the cap" has to mean when
// Caravan cannot keep the release to itself.
type clientEngine struct {
	name string
	// addedPaused records, per download, whether it arrived paused.
	addedPaused map[core.DownloadID]bool
	// resumed counts start commands per download, and started is the order
	// they arrived in.
	resumed map[core.DownloadID]int
	started []core.DownloadID
	paused  []core.DownloadID
	// state is what the client reports for each download.
	state map[core.DownloadID]core.DownloadState
	// addErr makes the handoff fail, standing in for a client that refuses.
	addErr error
	// resumeErr makes a start command fail.
	resumeErr error
}

func newClientEngine(name string) *clientEngine {
	return &clientEngine{
		name:        name,
		addedPaused: map[core.DownloadID]bool{},
		resumed:     map[core.DownloadID]int{},
		state:       map[core.DownloadID]core.DownloadState{},
	}
}

func (e *clientEngine) Add(_ context.Context, r core.Release, opts core.AddOpts) (core.DownloadID, error) {
	if e.addErr != nil {
		return "", e.addErr
	}
	id := core.DownloadID(r.Title)
	e.addedPaused[id] = opts.Paused
	if opts.Paused {
		e.state[id] = core.DownloadPaused
	} else {
		e.state[id] = core.DownloadDownloading
	}
	return id, nil
}

func (e *clientEngine) Status(_ context.Context, id core.DownloadID) (*core.DownloadStatus, error) {
	st, ok := e.state[id]
	if !ok {
		return nil, ErrNotFound
	}
	return &core.DownloadStatus{ID: id, State: st, Name: string(id)}, nil
}

func (e *clientEngine) List(context.Context) ([]core.DownloadStatus, error) {
	out := make([]core.DownloadStatus, 0, len(e.state))
	for id, st := range e.state {
		out = append(out, core.DownloadStatus{ID: id, State: st, Name: string(id)})
	}
	return out, nil
}

func (e *clientEngine) Pause(_ context.Context, id core.DownloadID) error {
	e.paused = append(e.paused, id)
	e.state[id] = core.DownloadPaused
	return nil
}

func (e *clientEngine) Resume(_ context.Context, id core.DownloadID) error {
	if e.resumeErr != nil {
		return e.resumeErr
	}
	e.resumed[id]++
	e.started = append(e.started, id)
	e.state[id] = core.DownloadDownloading
	return nil
}

func (e *clientEngine) Remove(_ context.Context, id core.DownloadID, _ bool) error {
	delete(e.state, id)
	return nil
}

func (e *clientEngine) Close() error { return nil }

// gatedRouter wires one fake client behind a router that rations it.
func gatedRouter(t *testing.T, caps Caps) (*Router, *clientEngine, *Admission, string) {
	t.Helper()
	engine := newClientEngine("sabnzbd")
	adm := NewAdmission(caps)
	method := "client:1"
	table := func(context.Context) ([]Route, error) {
		return []Route{{
			Name:      "sabnzbd",
			Protocol:  core.ProtocolUsenet,
			Engine:    engine,
			IDPrefix:  "c1.",
			Method:    method,
			Admission: adm,
		}}, nil
	}
	return NewRouter(table), engine, adm, method
}

func usenetRelease(title string) core.Release {
	return core.Release{Title: title, Protocol: core.ProtocolUsenet, DownloadURL: "https://indexer.example/" + title}
}

// The core claim of handoff gating: with room for one, the second release is
// still handed to the client — there is no way to hold it back and give it the
// same identity later — but it is handed over PAUSED, and the client is never
// told to start it. No connections, no articles, no work.
func TestRouterHandsTheSecondReleaseOverPaused(t *testing.T) {
	ctx := context.Background()
	r, client, adm, method := gatedRouter(t, Caps{Method: map[string]int{"client:1": 1}})

	first, err := r.Add(ctx, usenetRelease("first"), core.AddOpts{})
	if err != nil {
		t.Fatalf("Add first: %v", err)
	}
	second, err := r.Add(ctx, usenetRelease("second"), core.AddOpts{})
	if err != nil {
		t.Fatalf("Add second: %v", err)
	}

	if client.addedPaused["first"] {
		t.Error("the first release was handed over paused despite a free slot")
	}
	if !client.addedPaused["second"] {
		t.Fatal("the second release was handed over running, past a cap of 1")
	}
	if n := client.resumed["second"]; n != 0 {
		t.Fatalf("the held download was started %d times, want never", n)
	}
	if len(client.started) != 0 {
		t.Fatalf("the client was told to start %v, want nothing", client.started)
	}

	// And it reads as queued, not as something a person paused.
	statuses, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	byID := map[core.DownloadID]core.DownloadState{}
	for _, st := range statuses {
		byID[st.ID] = st.State
	}
	if byID[second] != core.DownloadQueued {
		t.Errorf("held download reads as %s, want queued", byID[second])
	}
	if byID[first] != core.DownloadDownloading {
		t.Errorf("running download reads as %s, want downloading", byID[first])
	}

	// A freed slot completes the handoff: the client is finally told to start.
	adm.Release(first)
	granted := adm.TakeWaiting(method, nil)
	if len(granted) != 1 || granted[0] != second {
		t.Fatalf("granted = %v, want the held download %s", granted, second)
	}
	if err := client.Resume(ctx, "second"); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if client.resumed["second"] != 1 {
		t.Errorf("the held download was started %d times, want once", client.resumed["second"])
	}
}

// A user's pause and Caravan's hold look identical at the client and mean
// opposite things here, so the ledger has to tell them apart: a person's pause
// reads as paused and frees a slot; Caravan's hold reads as queued.
func TestRouterDistinguishesAUserPauseFromAHold(t *testing.T) {
	ctx := context.Background()
	r, client, adm, _ := gatedRouter(t, Caps{Method: map[string]int{"client:1": 2}})

	running, err := r.Add(ctx, usenetRelease("running"), core.AddOpts{})
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := r.Pause(ctx, running); err != nil {
		t.Fatalf("Pause: %v", err)
	}
	if adm.Waiting(running) {
		t.Error("a user pause left the download reading as Caravan's hold")
	}
	if adm.Held() != 0 {
		t.Errorf("held slots = %d after a user pause, want the slot given back", adm.Held())
	}

	st, err := r.Status(ctx, running)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.State != core.DownloadPaused {
		t.Errorf("a user-paused download reads as %s, want paused", st.State)
	}
	if len(client.paused) != 1 {
		t.Errorf("client pauses = %v, want the one the user asked for", client.paused)
	}

	// And resuming into a full queue queues rather than jumping it: the client
	// is not told to start anything.
	adm.SetCaps(Caps{Method: map[string]int{"client:1": 0}, Global: 1})
	if _, err := r.Add(ctx, usenetRelease("other"), core.AddOpts{}); err != nil {
		t.Fatalf("Add other: %v", err)
	}
	before := len(client.started)
	if err := r.Resume(ctx, running); err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if len(client.started) != before {
		t.Errorf("resuming into a full queue started %v at the client", client.started[before:])
	}
	if !adm.Waiting(running) {
		t.Error("a download resumed into a full queue is not being held, so nothing will start it")
	}
}

// A client that refuses the handoff must not leave a reservation behind, or the
// cap leaks a slot on every failed grab until a restart.
func TestRouterGivesBackTheSlotWhenAHandoffFails(t *testing.T) {
	ctx := context.Background()
	r, client, adm, _ := gatedRouter(t, Caps{Method: map[string]int{"client:1": 1}})
	client.addErr = errors.New("SABnzbd would not take the NZB link")

	if _, err := r.Add(ctx, usenetRelease("doomed"), core.AddOpts{}); err == nil {
		t.Fatal("Add reported success for a client that refused the release")
	}
	if adm.Held() != 0 {
		t.Fatalf("held slots = %d after a failed handoff, want 0", adm.Held())
	}

	// The next grab still gets the slot, which is the thing a leak would break.
	client.addErr = nil
	if _, err := r.Add(ctx, usenetRelease("next"), core.AddOpts{}); err != nil {
		t.Fatalf("Add after a failed handoff: %v", err)
	}
	if client.addedPaused["next"] {
		t.Error("the slot leaked: the next release was held despite nothing running")
	}
}

// Somebody unpausing a held download in the client's own web UI is a clear
// request, and Caravan does not fight it: the download stops being held and is
// counted if there is room, rather than being paused again under their hands.
func TestRouterYieldsToAnUnpauseMadeAtTheClient(t *testing.T) {
	ctx := context.Background()
	r, client, adm, _ := gatedRouter(t, Caps{Method: map[string]int{"client:1": 1}})

	if _, err := r.Add(ctx, usenetRelease("first"), core.AddOpts{}); err != nil {
		t.Fatalf("Add first: %v", err)
	}
	held, err := r.Add(ctx, usenetRelease("second"), core.AddOpts{})
	if err != nil {
		t.Fatalf("Add second: %v", err)
	}
	if !adm.Waiting(held) {
		t.Fatal("the second download is not being held")
	}

	// A person starts it in SABnzbd directly. The next poll sees it running.
	client.state["second"] = core.DownloadDownloading
	statuses, err := r.List(ctx)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	for _, st := range statuses {
		if st.ID == held && st.State != core.DownloadDownloading {
			t.Errorf("a download started by hand reads as %s, want downloading", st.State)
		}
	}
	if adm.Waiting(held) {
		t.Error("Caravan is still holding a download somebody started by hand")
	}
	if len(client.paused) != 0 {
		t.Errorf("Caravan paused %v back, want it to leave a hand-started download alone", client.paused)
	}
}

// With no caps configured the router hands everything over running, which is
// what it did before gating existed.
func TestRouterWithoutCapsHandsEverythingOverRunning(t *testing.T) {
	ctx := context.Background()
	r, client, _, _ := gatedRouter(t, Caps{})
	for _, title := range []string{"a", "b", "c"} {
		if _, err := r.Add(ctx, usenetRelease(title), core.AddOpts{}); err != nil {
			t.Fatalf("Add %s: %v", title, err)
		}
		if client.addedPaused[core.DownloadID(title)] {
			t.Errorf("%s was handed over paused with no caps configured", title)
		}
	}
}

// A granted slot the client will not take must not strand the download.
//
// The wake lives in the composition root, but the guarantee it depends on is
// here: giving the slot back and putting the download back in line leaves the
// row readable as queued AND reachable by the next freed slot. A wake that
// dropped it instead would produce a row that says "queued" forever with
// nothing left that would ever start it.
func TestRouterHoldSurvivesAClientThatWillNotStartIt(t *testing.T) {
	ctx := context.Background()
	r, client, adm, method := gatedRouter(t, Caps{Method: map[string]int{"client:1": 1}})

	first, err := r.Add(ctx, usenetRelease("first"), core.AddOpts{})
	if err != nil {
		t.Fatalf("Add first: %v", err)
	}
	held, err := r.Add(ctx, usenetRelease("second"), core.AddOpts{})
	if err != nil {
		t.Fatalf("Add second: %v", err)
	}

	// A slot frees and the client refuses to start the download that gets it.
	adm.Release(first)
	client.resumeErr = errors.New("nzo_id not found")
	granted := adm.TakeWaiting(method, nil)
	if len(granted) != 1 {
		t.Fatalf("granted = %v, want the held download", granted)
	}
	if err := client.Resume(ctx, "second"); err == nil {
		t.Fatal("the fake client accepted a start it was told to refuse")
	}
	// What the wake does with that failure, and what this pins:
	adm.Release(held)
	adm.Wait(method, held)

	if !adm.Waiting(held) {
		t.Fatal("a download the client refused to start is no longer held, so nothing will retry it")
	}
	if adm.Held() != 0 {
		t.Errorf("held slots = %d, want the ungranted slot back", adm.Held())
	}

	// The next attempt reaches it, which is what "will try again" means.
	client.resumeErr = nil
	if again := adm.TakeWaiting(method, nil); len(again) != 1 || again[0] != held {
		t.Fatalf("second attempt granted %v, want %s", again, held)
	}
}
