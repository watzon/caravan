package api

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

func TestEventStreamRefusesMember(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	createUser(t, st, testMember, testPassword, core.RoleMember)
	member := login(t, h, testMember, testPassword)

	rec := doAuth(t, h, http.MethodGet, "/api/v1/events/stream", "", withCookie(member))
	wantStatus(t, rec, http.StatusForbidden)
	wantErrorBody(t, rec)
}

func TestEventStreamRefusesAnonymousWhenAccountsExist(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)

	rec := do(t, h, http.MethodGet, "/api/v1/events/stream", "")
	wantStatus(t, rec, http.StatusUnauthorized)
}

func TestEventStreamDeliversStoreInvalidation(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	cookie := login(t, h, testAdmin, testPassword)

	ts := httptest.NewServer(h)
	t.Cleanup(ts.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	t.Cleanup(cancel)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/api/v1/events/stream", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /events/stream: %v", err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /events/stream = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	r := core.Request{
		MediaType: core.MediaTypeMovie,
		TMDBID:    78,
		Title:     "Blade Runner",
		Year:      1982,
	}
	if err := st.CreateRequest(context.Background(), &r); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	if got := readInvalidate(t, resp.Body); got != "requests" {
		t.Fatalf("invalidate resource = %q, want requests", got)
	}
}

func readInvalidate(t *testing.T, r io.Reader) string {
	t.Helper()
	sc := bufio.NewScanner(r)
	var event string
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimPrefix(line, "event: ")
			continue
		}
		if strings.HasPrefix(line, "data: ") && event == "invalidate" {
			var body struct {
				Resource string `json:"resource"`
			}
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &body); err != nil {
				t.Fatalf("decode invalidate: %v", err)
			}
			return body.Resource
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read stream: %v", err)
	}
	t.Fatal("stream closed before invalidate")
	return ""
}

func TestInvalidationHubDropsWhenFull(t *testing.T) {
	h := newInvalidationHub()
	ch, ok := h.subscribe()
	if !ok {
		t.Fatal("subscribe failed")
	}
	for range streamBuf + 8 {
		h.Invalidate("requests")
	}
	if len(ch) != streamBuf {
		t.Fatalf("buffered hints = %d, want %d (drops when full)", len(ch), streamBuf)
	}
	h.unsubscribe(ch)
}

func TestInvalidationHubNilSafe(t *testing.T) {
	var h *invalidationHub
	h.Invalidate("requests")
}

func TestInvalidationHubRejectsPastCapacity(t *testing.T) {
	h := newInvalidationHub()
	chans := make([]chan streamMessage, 0, streamMaxConns)
	for i := 0; i < streamMaxConns; i++ {
		ch, ok := h.subscribe()
		if !ok {
			t.Fatalf("subscribe %d failed", i)
		}
		chans = append(chans, ch)
	}
	if _, ok := h.subscribe(); ok {
		t.Fatal("subscribe past capacity succeeded")
	}
	for _, ch := range chans {
		h.unsubscribe(ch)
	}
}
