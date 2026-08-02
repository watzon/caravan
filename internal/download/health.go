package download

import (
	"sort"
	"sync"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// DefaultUnhealthyAfter is how many consecutive failed polls mark an external
// download client unreachable (PLAN phase 6 task 4).
//
// More than one, because a single missed poll is normal: a client restarting,
// a reverse proxy recycling a connection, a laptop that slept for a second.
// Few enough that a user who pulled the plug on their seedbox sees the banner
// within a poll cycle or two rather than a minute later.
const DefaultUnhealthyAfter = 3

// Health transitions reported by Observe. An empty string means nothing
// changed, which is the answer on almost every poll.
const (
	HealthDown = "down"
	HealthUp   = "up"
)

// Health tracks the reachability of external download clients from the
// outcome of the queue poll.
//
// It lives here rather than in the composition root because it is the routing
// layer's state: the router reports poll outcomes into it (see Route.Report)
// and reads the result back out as Route.Unhealthy. It holds no store and no
// engine, so "three failures then a banner" is testable without either.
//
// A client is remembered by `download_clients.id`. Rows that are deleted or
// disabled are dropped by Retain, so a client the user removed cannot leave a
// banner behind.
type Health struct {
	threshold int

	mu      sync.Mutex
	clients map[int64]*clientHealth
	// now is time.Now, overridable so the transition timestamps are testable.
	now func() time.Time
}

// clientHealth is one client's consecutive-failure count and current verdict.
type clientHealth struct {
	name      string
	kind      string
	failures  int
	unhealthy bool
	reason    string
	since     time.Time
}

// NewHealth returns a tracker that declares a client unreachable after
// threshold consecutive failed polls. A threshold below 1 uses
// DefaultUnhealthyAfter.
func NewHealth(threshold int) *Health {
	if threshold < 1 {
		threshold = DefaultUnhealthyAfter
	}
	return &Health{threshold: threshold, clients: map[int64]*clientHealth{}, now: time.Now}
}

// Observe records one poll of a client and reports the transition it caused:
// HealthDown when this poll was the one that crossed the threshold, HealthUp
// when a client that was down answered again, "" otherwise.
//
// Returning only transitions is what throttles the activity feed: an
// unreachable client fails every poll, and the feed is for the user, not for a
// log file.
func (h *Health) Observe(id int64, name, kind string, err error) string {
	h.mu.Lock()
	defer h.mu.Unlock()

	c := h.clients[id]
	if c == nil {
		c = &clientHealth{}
		h.clients[id] = c
	}
	c.name, c.kind = name, kind

	if err == nil {
		c.failures = 0
		if !c.unhealthy {
			return ""
		}
		c.unhealthy = false
		c.reason = ""
		c.since = time.Time{}
		return HealthUp
	}

	c.failures++
	c.reason = err.Error()
	if c.unhealthy || c.failures < h.threshold {
		return ""
	}
	c.unhealthy = true
	c.since = h.now()
	return HealthDown
}

// Reason returns why a client is currently unreachable, or "" when it is fine
// or unknown. It is what fills Route.Unhealthy.
func (h *Health) Reason(id int64) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	if c := h.clients[id]; c != nil && c.unhealthy {
		return c.reason
	}
	return ""
}

// Unhealthy lists the clients currently considered unreachable, ordered by id
// so the status endpoint is stable between polls. The slice is empty, never
// nil, so the JSON is `[]`.
func (h *Health) Unhealthy() []core.DownloadClientHealth {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([]core.DownloadClientHealth, 0, len(h.clients))
	for id, c := range h.clients {
		if !c.unhealthy {
			continue
		}
		out = append(out, core.DownloadClientHealth{
			ID:    id,
			Name:  c.name,
			Type:  c.kind,
			Error: c.reason,
			Since: c.since,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

// Retain forgets every client whose id is not in keep — one that was deleted,
// or disabled. A banner for a client that is no longer configured has nothing
// the user can do about it.
func (h *Health) Retain(keep map[int64]bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id := range h.clients {
		if !keep[id] {
			delete(h.clients, id)
		}
	}
}
