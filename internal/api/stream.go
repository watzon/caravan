package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// Live-update resources the sidebar stores re-read. Names match the
// activity-stream invalidate keys in docs/design/activity-stream.md.
const (
	resourceDownloads = "downloads"
	resourceRequests  = "requests"
	resourceJobs      = "jobs"
	resourceLibrary   = "library"
)

const (
	streamHeartbeat = 20 * time.Second
	streamLifetime  = 15 * time.Minute
	streamBuf       = 8
	streamMaxConns  = 100
)

type streamMessage struct {
	Event string
	Data  string
}

// invalidationHub fans non-authoritative resource hints to SSE subscribers.
// A full buffer drops the hint; the next write or a REST snapshot covers it.
type invalidationHub struct {
	mu   sync.Mutex
	subs map[chan streamMessage]struct{}
}

func newInvalidationHub() *invalidationHub {
	return &invalidationHub{subs: map[chan streamMessage]struct{}{}}
}

func (h *invalidationHub) subscribe() (chan streamMessage, bool) {
	ch := make(chan streamMessage, streamBuf)
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.subs) >= streamMaxConns {
		return nil, false
	}
	h.subs[ch] = struct{}{}
	return ch, true
}

func (h *invalidationHub) unsubscribe(ch chan streamMessage) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[ch]; !ok {
		return
	}
	delete(h.subs, ch)
	close(ch)
}

// Invalidate tells every subscriber to re-read resource. Never blocks a writer.
func (h *invalidationHub) Invalidate(resource string) {
	if h == nil || resource == "" {
		return
	}
	body, err := json.Marshal(map[string]string{"resource": resource})
	if err != nil {
		return
	}
	msg := streamMessage{Event: "invalidate", Data: string(body)}
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (s *server) handleEventStream(w http.ResponseWriter, r *http.Request) {
	if s.stream == nil {
		writeError(w, http.StatusServiceUnavailable, "live updates unavailable")
		return
	}
	ch, ok := s.stream.subscribe()
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "too many live connections")
		return
	}
	defer s.stream.unsubscribe(ch)

	rc := http.NewResponseController(w)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if err := rc.Flush(); err != nil {
		return
	}

	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()
	lifetime := time.NewTimer(streamLifetime)
	defer lifetime.Stop()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			return
		case <-lifetime.C:
			return
		case <-heartbeat.C:
			if _, err := fmt.Fprintf(w, ": heartbeat\n\n"); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		case msg, open := <-ch:
			if !open {
				return
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", msg.Event, msg.Data); err != nil {
				return
			}
			if err := rc.Flush(); err != nil {
				return
			}
		}
	}
}
