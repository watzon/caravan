package download

import (
	"sort"
	"sync"

	"github.com/watzon/caravan/internal/core"
)

// Caps is the configured concurrency ceiling. Zero anywhere means unlimited,
// which is the state a Caravan that has never been told a number is in.
type Caps struct {
	// Global is the ceiling across every engine and client together.
	Global int
	// Method is the ceiling per download method, keyed by the engine's own
	// name — the `downloads.engine` value ("embedded", "embedded-usenet",
	// "sabnzbd", …). A method with no entry is unlimited by method and still
	// bounded by Global.
	Method map[string]int
}

// limit reports the ceiling for one method, and whether there is one.
func (c Caps) limit(method string) (int, bool) {
	n, ok := c.Method[method]
	return n, ok && n > 0
}

// Admission is the single authority on which downloads may transfer.
//
// It is a ledger of granted slots rather than a copy of the queue: it knows
// which downloads hold a slot and which method each came from, and nothing
// else. What is *waiting* stays with the engines, which already track their own
// downloads and already have a state for one that is not running. That split is
// what keeps this from becoming a second, disagreeing copy of the queue.
//
// # Waking
//
// A refused download has to be reconsidered when a slot frees, and only this
// type knows when that happens. Engines register a wake function; releasing a
// slot calls every registered wake, and each engine re-asks for the downloads
// it is holding back. Wakes run on their own goroutine and never under this
// type's lock — the engine calls Request while holding its own lock, so waking
// it from inside ours would close the cycle.
type Admission struct {
	mu sync.Mutex
	// caps is the live configuration; a settings save replaces it.
	caps Caps
	// held maps a download onto the method whose budget it is spending.
	held map[core.DownloadID]string
	// reserved counts slots taken for downloads that do not have an id yet.
	//
	// An external client hands out its id only once it has accepted the
	// release, so the decision to admit has to be made before the thing being
	// admitted can be named. A reservation is that slot, and it counts against
	// every ceiling exactly as a held one does until it is committed or
	// cancelled.
	reserved map[string]int
	// waiting is what an external client is holding paused because Caravan has
	// no slot for it. The engines track their own; a client cannot, so the
	// coordinator does it on their behalf.
	waiting map[core.DownloadID]string
	// wakes are the engines to nudge when a slot frees, keyed by method so a
	// re-registered engine replaces rather than duplicates its entry.
	wakes map[string]func()
	// dispatching serialises wake fan-out, so a burst of releases produces one
	// round of re-checks rather than a goroutine each.
	dispatching bool
	pending     bool
}

// Admission is the Admitter every engine is handed.
var _ core.Admitter = (*Admission)(nil)

// NewAdmission returns a coordinator with the given caps. The zero Caps is
// unlimited.
func NewAdmission(caps Caps) *Admission {
	return &Admission{
		caps:     caps,
		held:     map[core.DownloadID]string{},
		reserved: map[string]int{},
		waiting:  map[core.DownloadID]string{},
		wakes:    map[string]func(){},
	}
}

// Reserve takes a slot for a download that cannot be named yet.
//
// It is the external-client half of Request: those clients only hand out an id
// once they have accepted the release, so the decision has to come first. Every
// reservation must be answered by exactly one Commit or Cancel.
func (a *Admission) Reserve(method string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.roomLocked(method) {
		return false
	}
	a.reserved[method]++
	return true
}

// Commit attaches a reservation to the id the client gave back.
func (a *Admission) Commit(method string, id core.DownloadID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.dropReservationLocked(method)
	a.held[id] = method
}

// Cancel gives back a reservation whose add never completed.
func (a *Admission) Cancel(method string) {
	a.mu.Lock()
	held := a.dropReservationLocked(method)
	a.mu.Unlock()
	if held {
		a.wake()
	}
}

func (a *Admission) dropReservationLocked(method string) bool {
	if a.reserved[method] <= 0 {
		return false
	}
	a.reserved[method]--
	if a.reserved[method] == 0 {
		delete(a.reserved, method)
	}
	return true
}

// Wait records that a client is holding this download paused for want of a
// slot, so the queue can report it as queued and the next free slot can find
// it. It is the coordinator's copy of what the engines keep for themselves.
func (a *Admission) Wait(method string, id core.DownloadID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.waiting[id] = method
}

// Waiting reports whether this download is one Caravan is holding paused, as
// opposed to one a person paused. The two look identical at the client and
// mean opposite things to the user.
func (a *Admission) Waiting(id core.DownloadID) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	_, ok := a.waiting[id]
	return ok
}

// Unwait forgets a held download without granting it anything: the user paused
// it, removed it, or the client lost it.
func (a *Admission) Unwait(id core.DownloadID) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.waiting, id)
}

// TakeWaiting returns the downloads this method is holding paused that a slot
// can now be granted to, oldest-registered first is not knowable here, so the
// caller orders them; it grants a slot to each it returns.
func (a *Admission) TakeWaiting(method string, order func([]core.DownloadID)) []core.DownloadID {
	a.mu.Lock()
	candidates := make([]core.DownloadID, 0, len(a.waiting))
	for id, m := range a.waiting {
		if m == method {
			candidates = append(candidates, id)
		}
	}
	a.mu.Unlock()

	if order != nil {
		order(candidates)
	} else {
		sort.Slice(candidates, func(i, j int) bool { return candidates[i] < candidates[j] })
	}

	granted := make([]core.DownloadID, 0, len(candidates))
	for _, id := range candidates {
		if !a.Request(method, id) {
			break
		}
		a.mu.Lock()
		delete(a.waiting, id)
		a.mu.Unlock()
		granted = append(granted, id)
	}
	return granted
}

// SetCaps replaces the configuration, for a settings save that has to reach a
// running engine without a restart.
//
// Raising a cap frees slots immediately, so every engine is woken. Lowering one
// does not evict downloads that are already running: stopping a transfer that
// is half done to honour a number the user has just typed would cost more than
// it saves, and the next completion brings the count back under the new ceiling
// on its own.
func (a *Admission) SetCaps(caps Caps) {
	a.mu.Lock()
	a.caps = caps
	a.mu.Unlock()
	a.wake()
}

// CurrentCaps returns the live configuration.
func (a *Admission) CurrentCaps() Caps {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.caps
}

// Register installs the function to call when a slot frees. An engine calls
// this once, with a closure that re-asks for everything it is holding back.
func (a *Admission) Register(method string, wake func()) {
	a.mu.Lock()
	a.wakes[method] = wake
	a.mu.Unlock()
}

// Request implements core.Admitter.
func (a *Admission) Request(method string, id core.DownloadID) bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Already holding one. Asking twice is normal — an engine re-checks its
	// held-back downloads on every wake, including the ones already running.
	if _, ok := a.held[id]; ok {
		return true
	}
	if !a.roomLocked(method) {
		return false
	}
	a.held[id] = method
	return true
}

// roomLocked reports whether both ceilings have room for one more download of
// this method. Reservations count: a slot promised to an add that has not
// finished is a slot spent.
func (a *Admission) roomLocked(method string) bool {
	if limit, capped := a.caps.limit(method); capped && a.countLocked(method) >= limit {
		return false
	}
	if a.caps.Global > 0 && a.totalLocked() >= a.caps.Global {
		return false
	}
	return true
}

func (a *Admission) totalLocked() int {
	n := len(a.held)
	for _, r := range a.reserved {
		n += r
	}
	return n
}

// Release implements core.Admitter.
func (a *Admission) Release(id core.DownloadID) {
	a.mu.Lock()
	_, held := a.held[id]
	delete(a.held, id)
	a.mu.Unlock()

	// Only a slot that was actually given back is worth a round of re-checks.
	if held {
		a.wake()
	}
}

// Active is how many slots one method is spending, and Held is the total. Both
// are for the settings screen and for tests; nothing decides on them.
func (a *Admission) Active(method string) int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.countLocked(method)
}

func (a *Admission) Held() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.held)
}

func (a *Admission) countLocked(method string) int {
	n := a.reserved[method]
	for _, m := range a.held {
		if m == method {
			n++
		}
	}
	return n
}

// wake nudges every registered engine to re-ask for what it is holding back.
//
// It runs on its own goroutine and takes no lock while calling out, because the
// engines call Request from inside their own locks. A wake that arrives while
// one is already running is collapsed into a single follow-up round rather than
// queued: the engines re-read their whole held-back set each time, so two
// rounds would ask the same questions twice.
func (a *Admission) wake() {
	a.mu.Lock()
	if a.dispatching {
		a.pending = true
		a.mu.Unlock()
		return
	}
	a.dispatching = true
	a.mu.Unlock()

	go a.dispatch()
}

func (a *Admission) dispatch() {
	for {
		a.mu.Lock()
		wakes := make([]func(), 0, len(a.wakes))
		// Sorted by method so the order slots are offered in is deterministic,
		// which is what makes a two-engine cap test mean anything.
		methods := make([]string, 0, len(a.wakes))
		for m := range a.wakes {
			methods = append(methods, m)
		}
		sort.Strings(methods)
		for _, m := range methods {
			wakes = append(wakes, a.wakes[m])
		}
		a.mu.Unlock()

		for _, w := range wakes {
			w()
		}

		a.mu.Lock()
		if !a.pending {
			a.dispatching = false
			a.mu.Unlock()
			return
		}
		a.pending = false
		a.mu.Unlock()
	}
}
