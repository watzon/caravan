package dlna

// Request tracing for TV debugging. DLNA clients fail silently, a TV or an app
// that dislikes one response just renders an empty shelf, and the only way to
// tell "it never asked" from "it asked and hated the answer" is a record of
// what it asked. The ring is small, in memory only, and surfaced through GET
// /api/v1/dlna, so "what did the TV actually do" is a curl away.

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

// traceCap bounds the ring. A browse session is a few dozen requests; the
// point is the most recent conversation, not history.
const traceCap = 200

// TraceEntry is one request the DLNA surface saw.
type TraceEntry struct {
	Time   time.Time `json:"time"`
	Remote string    `json:"remote"`
	// Kind is "http" for the description/control/media surface and "ssdp"
	// for discovery datagrams.
	Kind string `json:"kind"`
	// Detail is the human-readable request line, e.g.
	// "POST /dlna/control/cds Browse → 200" or "M-SEARCH ssdp:all → 5 targets".
	Detail string `json:"detail"`
	// UserAgent identifies the client stack where the request carried one.
	UserAgent string `json:"user_agent,omitempty"`
	// Request and Response are the SOAP bodies of a control exchange,
	// truncated at traceBodyCap. Control only: descriptions are static and
	// media bodies are the movie.
	Request  string `json:"request,omitempty"`
	Response string `json:"response,omitempty"`
}

// traceBodyCap bounds a recorded control body. Browse responses of big
// containers can run long; the head is where the disagreements live.
const traceBodyCap = 16 * 1024

// trace is the shared ring.
type trace struct {
	mu      sync.Mutex
	entries []TraceEntry
}

func (t *trace) add(e TraceEntry) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.entries = append(t.entries, e)
	if len(t.entries) > traceCap {
		t.entries = t.entries[len(t.entries)-traceCap:]
	}
}

// snapshot returns the entries oldest-first.
func (t *trace) snapshot() []TraceEntry {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]TraceEntry, len(t.entries))
	copy(out, t.entries)
	return out
}

// Recent returns the most recent DLNA requests, oldest first.
func (s *Service) Recent() []TraceEntry {
	return s.traceRing().snapshot()
}

func (s *Service) traceRing() *trace {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tr == nil {
		s.tr = &trace{}
	}
	return s.tr
}

// statusWriter captures the response code, and, when capture is set, the head
// of the body, for the trace.
type statusWriter struct {
	http.ResponseWriter
	code    int
	capture *bytes.Buffer
}

func (w *statusWriter) WriteHeader(code int) {
	w.code = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(p []byte) (int, error) {
	if w.capture != nil && w.capture.Len() < traceBodyCap {
		w.capture.Write(p[:min(len(p), traceBodyCap-w.capture.Len())])
	}
	return w.ResponseWriter.Write(p)
}

// withTrace records every request the HTTP surface serves; control exchanges
// keep their SOAP bodies so a disagreement is diffable after the fact.
func (s *Service) withTrace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		control := r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/control/")

		var reqBody string
		if control {
			head, err := io.ReadAll(io.LimitReader(r.Body, traceBodyCap))
			if err == nil {
				reqBody = string(head)
				r.Body = struct {
					io.Reader
					io.Closer
				}{io.MultiReader(bytes.NewReader(head), r.Body), r.Body}
			}
		}

		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		if control {
			sw.capture = &bytes.Buffer{}
		}
		next.ServeHTTP(sw, r)

		detail := r.Method + " " + r.URL.Path
		if action := soapActionName(r.Header.Get("SOAPACTION")); action != "" {
			detail += " " + action
		}
		entry := TraceEntry{
			Time:      time.Now(),
			Remote:    r.RemoteAddr,
			Kind:      "http",
			Detail:    detail + " → " + http.StatusText(sw.code) + " (" + itoaTrace(sw.code) + ")",
			UserAgent: r.Header.Get("User-Agent"),
			Request:   reqBody,
		}
		if sw.capture != nil {
			entry.Response = sw.capture.String()
		}
		s.traceRing().add(entry)
	})
}

// soapActionName extracts the bare action out of a SOAPACTION header.
func soapActionName(header string) string {
	_, action, ok := parseSOAPAction(header)
	if !ok {
		return ""
	}
	return action
}

func itoaTrace(n int) string {
	if n == 0 {
		return "0"
	}
	digits := ""
	for ; n > 0; n /= 10 {
		digits = string(rune('0'+n%10)) + digits
	}
	return digits
}

// traceSSDP records one discovery datagram; handed to the advertiser as a
// callback so the SSDP loop does not know the Service.
func (s *Service) traceSSDP(remote, detail string) {
	s.traceRing().add(TraceEntry{
		Time:   time.Now(),
		Remote: remote,
		Kind:   "ssdp",
		Detail: detail,
	})
}
