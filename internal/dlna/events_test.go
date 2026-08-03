package dlna

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// gena issues a SUBSCRIBE/UNSUBSCRIBE the way a client stack does.
func gena(t *testing.T, h http.Handler, method, path string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	req.Host = "caravan.lan:8677"
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// callbackServer records NOTIFY deliveries.
type callbackServer struct {
	*httptest.Server
	mu     sync.Mutex
	bodies []string
	heads  []http.Header
}

func newCallbackServer(t *testing.T) *callbackServer {
	t.Helper()
	cb := &callbackServer{}
	cb.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		cb.mu.Lock()
		cb.bodies = append(cb.bodies, string(body))
		cb.heads = append(cb.heads, r.Header.Clone())
		cb.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(cb.Close)
	return cb
}

// waitForNotify polls for the async initial event.
func (cb *callbackServer) waitForNotify(t *testing.T) (string, http.Header) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		cb.mu.Lock()
		if len(cb.bodies) > 0 {
			body, head := cb.bodies[0], cb.heads[0]
			cb.mu.Unlock()
			return body, head
		}
		cb.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("no NOTIFY arrived within 5s of subscribing")
	return "", nil
}

// TestSubscribeDeliversInitialEvent covers the handshake clients gate browsing
// on: SUBSCRIBE answers with a SID, and the SEQ-0 event with the current
// SystemUpdateID follows to the CALLBACK.
func TestSubscribeDeliversInitialEvent(t *testing.T) {
	svc, _, _ := newTestService(t)
	cb := newCallbackServer(t)

	rec := gena(t, svc.Handler(), "SUBSCRIBE", contentDirectoryEventURL, map[string]string{
		"CALLBACK": "<" + cb.URL + "/notify>",
		"NT":       "upnp:event",
		"TIMEOUT":  "Second-600",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("SUBSCRIBE = %d, want 200", rec.Code)
	}
	sid := rec.Header().Get("SID")
	if !strings.HasPrefix(sid, "uuid:") {
		t.Fatalf("SID = %q, want a uuid", sid)
	}
	if got := rec.Header().Get("TIMEOUT"); got != "Second-600" {
		t.Fatalf("TIMEOUT = %q, want the granted Second-600", got)
	}

	body, head := cb.waitForNotify(t)
	if head.Get("SID") != sid {
		t.Errorf("NOTIFY SID = %q, want %q", head.Get("SID"), sid)
	}
	if head.Get("NT") != "upnp:event" || head.Get("NTS") != "upnp:propchange" || head.Get("SEQ") != "0" {
		t.Errorf("NOTIFY headers = NT %q NTS %q SEQ %q, want the initial-event triple",
			head.Get("NT"), head.Get("NTS"), head.Get("SEQ"))
	}
	if !strings.Contains(body, "<SystemUpdateID>"+defaultSystemUpdateID+"</SystemUpdateID>") {
		t.Errorf("NOTIFY body = %q, want the current SystemUpdateID", body)
	}
}

// TestSubscribeConnectionManagerEventsProtocolInfo: the cms event carries what
// its SCPD declares evented, protocolInfo first.
func TestSubscribeConnectionManagerEventsProtocolInfo(t *testing.T) {
	svc, _, _ := newTestService(t)
	cb := newCallbackServer(t)

	rec := gena(t, svc.Handler(), "SUBSCRIBE", connectionManagerEventURL, map[string]string{
		"CALLBACK": "<" + cb.URL + ">",
		"NT":       "upnp:event",
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("SUBSCRIBE = %d, want 200", rec.Code)
	}
	body, _ := cb.waitForNotify(t)
	if !strings.Contains(body, "<SourceProtocolInfo>") || !strings.Contains(body, "<CurrentConnectionIDs>0</CurrentConnectionIDs>") {
		t.Errorf("cms NOTIFY body = %q, want SourceProtocolInfo and CurrentConnectionIDs", body)
	}
}

func TestSubscribeRenewalAndUnsubscribe(t *testing.T) {
	svc, _, _ := newTestService(t)
	cb := newCallbackServer(t)
	h := svc.Handler()

	sid := gena(t, h, "SUBSCRIBE", contentDirectoryEventURL, map[string]string{
		"CALLBACK": "<" + cb.URL + ">",
		"NT":       "upnp:event",
	}).Header().Get("SID")

	if rec := gena(t, h, "SUBSCRIBE", contentDirectoryEventURL, map[string]string{"SID": sid}); rec.Code != http.StatusOK {
		t.Fatalf("renewal = %d, want 200", rec.Code)
	}
	if rec := gena(t, h, "SUBSCRIBE", contentDirectoryEventURL, map[string]string{"SID": "uuid:nope"}); rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("unknown-SID renewal = %d, want 412", rec.Code)
	}
	// Mixing renewal and subscription forms is a client bug the spec names.
	if rec := gena(t, h, "SUBSCRIBE", contentDirectoryEventURL, map[string]string{
		"SID": sid, "NT": "upnp:event",
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("mixed-form SUBSCRIBE = %d, want 400", rec.Code)
	}

	if rec := gena(t, h, "UNSUBSCRIBE", contentDirectoryEventURL, map[string]string{"SID": sid}); rec.Code != http.StatusOK {
		t.Fatalf("UNSUBSCRIBE = %d, want 200", rec.Code)
	}
	if rec := gena(t, h, "UNSUBSCRIBE", contentDirectoryEventURL, map[string]string{"SID": sid}); rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("second UNSUBSCRIBE = %d, want 412", rec.Code)
	}
}

func TestSubscribeRejectsBadRequests(t *testing.T) {
	svc, _, _ := newTestService(t)
	h := svc.Handler()

	// No CALLBACK and no SID: nothing to subscribe.
	if rec := gena(t, h, "SUBSCRIBE", contentDirectoryEventURL, map[string]string{"NT": "upnp:event"}); rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("no-callback SUBSCRIBE = %d, want 412", rec.Code)
	}
	// Wrong NT.
	if rec := gena(t, h, "SUBSCRIBE", contentDirectoryEventURL, map[string]string{
		"CALLBACK": "<http://127.0.0.1:1/>", "NT": "upnp:rollcall",
	}); rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("wrong-NT SUBSCRIBE = %d, want 412", rec.Code)
	}
}

// TestDeviceDescriptionAdvertisesEventURLs pins the regression this exists
// for: an empty eventSubURL reads to some clients as a dead service and an
// empty library.
func TestDeviceDescriptionAdvertisesEventURLs(t *testing.T) {
	desc := deviceDescription("Caravan", "0000")
	if strings.Contains(desc, "<eventSubURL></eventSubURL>") {
		t.Fatal("device description still carries an empty eventSubURL")
	}
	for _, url := range []string{contentDirectoryEventURL, connectionManagerEventURL} {
		if !strings.Contains(desc, "<eventSubURL>"+url+"</eventSubURL>") {
			t.Errorf("device description does not advertise %s", url)
		}
	}
}

func TestParseSubTimeout(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
	}{
		{"Second-600", 600 * time.Second},
		{"Second-1800", 1800 * time.Second},
		{"infinite", defaultSubTimeout},
		{"", defaultSubTimeout},
		{"Second-0", defaultSubTimeout},
		{"Second-99999999", maxSubTimeout},
	}
	for _, tt := range tests {
		if got := parseSubTimeout(tt.in); got != tt.want {
			t.Errorf("parseSubTimeout(%q) = %v, want %v", tt.in, got, tt.want)
		}
	}
}

func TestCallbackURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"<http://192.168.1.10:5000/cb>", "http://192.168.1.10:5000/cb"},
		{"<https://x/><http://192.168.1.10/cb>", "http://192.168.1.10/cb"},
		{"http://bare.example/", ""},
		{"<not-a-url>", ""},
		{"", ""},
	}
	for _, tt := range tests {
		if got := callbackURL(tt.in); got != tt.want {
			t.Errorf("callbackURL(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}
