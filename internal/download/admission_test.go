package download

import (
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// The two-level cap is the whole point: a per-method ceiling nobody else can
// see, and a global one no single engine could enforce.
func TestAdmissionGlobalCeilingBindsAcrossMethods(t *testing.T) {
	// Either method could run two, but together they may only run three.
	a := NewAdmission(Caps{
		Global: 3,
		Method: map[string]int{"embedded": 2, "embedded-usenet": 2},
	})

	if !a.Request("embedded", "t1") || !a.Request("embedded", "t2") {
		t.Fatal("the torrent engine could not fill its own allowance")
	}
	if !a.Request("embedded-usenet", "u1") {
		t.Fatal("the usenet engine could not take the last global slot")
	}
	// Its method has room; the global ceiling does not.
	if a.Request("embedded-usenet", "u2") {
		t.Fatal("a fourth download was admitted past a global ceiling of 3")
	}
	if got := a.Held(); got != 3 {
		t.Fatalf("held = %d, want 3", got)
	}

	// And freeing one anywhere lets the other engine through, which is what a
	// shared ceiling means.
	a.Release("t1")
	if !a.Request("embedded-usenet", "u2") {
		t.Fatal("a freed slot did not reach the other engine")
	}
	if got := a.Active("embedded"); got != 1 {
		t.Fatalf("torrent slots = %d, want 1", got)
	}
	if got := a.Active("embedded-usenet"); got != 2 {
		t.Fatalf("usenet slots = %d, want 2", got)
	}
}

// A method ceiling binds even when the global one has room.
func TestAdmissionMethodCeilingBindsUnderAnOpenGlobal(t *testing.T) {
	a := NewAdmission(Caps{Method: map[string]int{"embedded-usenet": 1}})

	if !a.Request("embedded-usenet", "u1") {
		t.Fatal("the first download was refused")
	}
	if a.Request("embedded-usenet", "u2") {
		t.Fatal("a second usenet download was admitted past a method cap of 1")
	}
	// An uncapped method is not affected by another's ceiling.
	if !a.Request("embedded", "t1") || !a.Request("embedded", "t2") {
		t.Fatal("an uncapped method was limited by another method's cap")
	}
}

// Unset caps are unlimited, which is what a Caravan that has never been told a
// number has always done.
func TestAdmissionWithoutCapsAdmitsEverything(t *testing.T) {
	a := NewAdmission(Caps{})
	for i := 0; i < 50; i++ {
		if !a.Request("embedded", core.DownloadID(string(rune('a'+i%26))+string(rune('0'+i/26)))) {
			t.Fatalf("download %d was refused with no caps configured", i)
		}
	}
}

// Asking twice for a download that already holds a slot is normal — engines
// re-ask for everything on every wake — and must not spend a second one.
func TestAdmissionRequestIsIdempotent(t *testing.T) {
	a := NewAdmission(Caps{Global: 1})
	if !a.Request("embedded", "t1") {
		t.Fatal("the first request was refused")
	}
	if !a.Request("embedded", "t1") {
		t.Fatal("re-asking for a slot already held was refused")
	}
	if got := a.Held(); got != 1 {
		t.Fatalf("held = %d after asking twice for one download, want 1", got)
	}
}

// Releasing a slot has to reach the engines, or a queued download waits for a
// poll that may be seconds away.
func TestAdmissionReleaseWakesEveryEngine(t *testing.T) {
	a := NewAdmission(Caps{Global: 1})

	var mu sync.Mutex
	woke := map[string]int{}
	wake := func(name string) func() {
		return func() {
			mu.Lock()
			woke[name]++
			mu.Unlock()
		}
	}
	a.Register("embedded", wake("embedded"))
	a.Register("embedded-usenet", wake("embedded-usenet"))

	a.Request("embedded", "t1")
	a.Release("t1")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		done := woke["embedded"] > 0 && woke["embedded-usenet"] > 0
		mu.Unlock()
		if done {
			return
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	t.Fatalf("wakes = %v, want both engines nudged by a freed slot", woke)
}

// Releasing something that never held a slot is a no-op, so a caller never has
// to remember whether it was admitted.
func TestAdmissionReleaseOfAnUnheldDownloadDoesNothing(t *testing.T) {
	a := NewAdmission(Caps{Global: 1})
	a.Register("embedded", func() { t.Error("an unheld release woke the engines") })
	a.Release("never-admitted")
	time.Sleep(20 * time.Millisecond)
	if got := a.Held(); got != 0 {
		t.Fatalf("held = %d, want 0", got)
	}
}

// A settings save reaches a running engine. Raising a cap frees slots at once;
// lowering one does not stop downloads that are already going, because the next
// completion brings the count under the new ceiling anyway and killing a
// half-done transfer to honour a freshly typed number costs more than it saves.
func TestAdmissionSetCapsAppliesLive(t *testing.T) {
	a := NewAdmission(Caps{Global: 1})
	if !a.Request("embedded", "t1") || a.Request("embedded", "t2") {
		t.Fatal("the initial ceiling of 1 was not applied")
	}

	a.SetCaps(Caps{Global: 3})
	if !a.Request("embedded", "t2") {
		t.Fatal("raising the ceiling did not admit a waiting download")
	}

	a.SetCaps(Caps{Global: 1})
	if got := a.Held(); got != 2 {
		t.Fatalf("held = %d after lowering the ceiling, want the 2 already running left alone", got)
	}
	if a.Request("embedded", "t3") {
		t.Fatal("a new download was admitted while over the lowered ceiling")
	}
}

// The coordinator is called from inside engine locks and calls back into them,
// so the one thing it must never do is hold its own lock while waking.
func TestAdmissionDoesNotDeadlockWhenAWakeRequests(t *testing.T) {
	a := NewAdmission(Caps{Global: 2})
	var mu sync.Mutex
	done := make(chan struct{}, 1)

	// A wake that behaves exactly as an engine does: takes its own lock, then
	// asks the coordinator from inside it.
	a.Register("embedded", func() {
		mu.Lock()
		defer mu.Unlock()
		a.Request("embedded", "t2")
		select {
		case done <- struct{}{}:
		default:
		}
	})

	mu.Lock()
	a.Request("embedded", "t1")
	a.Release("t1") // wakes on another goroutine, which will block on mu
	mu.Unlock()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a wake that asks for a slot deadlocked against the coordinator")
	}
}
