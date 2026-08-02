package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// newDirtyServer builds a server that came up after an unclean shutdown, with a
// stub engine attached so the queue endpoints are reachable.
func newDirtyServer(t *testing.T, dirty bool) (http.Handler, *stubEngine, *stubManager) {
	t.Helper()
	engine := &stubEngine{}
	h, _, mgr := newTestServer(t,
		WithEngine(&stubEngineProvider{engine: engine}),
		WithDirtyStart(dirty))
	return h, engine, mgr
}

func systemStatus(t *testing.T, h http.Handler) statusResponse {
	t.Helper()
	rec := do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)
	var status statusResponse
	decodeBody(t, rec, &status)
	return status
}

// A clean start reports dirty=false; a dirty one says so, which is what raises
// the recovery banner (SPEC §2.3).
func TestSystemStatusReportsTheDirtyStart(t *testing.T) {
	clean, _, _ := newDirtyServer(t, false)
	if systemStatus(t, clean).Dirty {
		t.Fatal("a clean start reported dirty=true")
	}

	dirty, _, _ := newDirtyServer(t, true)
	if !systemStatus(t, dirty).Dirty {
		t.Fatal("a dirty start reported dirty=false")
	}
}

// The acceptance criterion: after a dirty eject downloads stay paused until the
// database verifies. Pausing and listing keep working — it is only the
// direction that resumes writes onto an unchecked filesystem that is refused.
func TestDownloadsStayPausedUntilTheDatabaseVerifies(t *testing.T) {
	h, engine, _ := newDirtyServer(t, true)

	rec := do(t, h, http.MethodPost, "/api/v1/downloads/hash-1/resume", "")
	wantStatus(t, rec, http.StatusConflict)
	wantErrorBody(t, rec)
	if got := len(engine.resumed); got != 0 {
		t.Fatalf("engine.Resume was called %d times while dirty; want 0", got)
	}

	// Pausing is still allowed: a dirty start must not trap a running download.
	wantStatus(t, do(t, h, http.MethodPost, "/api/v1/downloads/hash-1/pause", ""), http.StatusNoContent)

	rec = do(t, h, http.MethodPost, "/api/v1/system/verify", "")
	wantStatus(t, rec, http.StatusOK)
	var verified verifyResponse
	decodeBody(t, rec, &verified)
	if verified.Integrity != "ok" || verified.Dirty {
		t.Fatalf("verify = %+v, want integrity ok and dirty false", verified)
	}

	wantStatus(t, do(t, h, http.MethodPost, "/api/v1/downloads/hash-1/resume", ""), http.StatusNoContent)
	if got := len(engine.resumed); got != 1 {
		t.Fatalf("engine.Resume was called %d times after verifying; want 1", got)
	}
	if systemStatus(t, h).Dirty {
		t.Fatal("status still reports dirty after a successful verify")
	}
}

// Verification is not just a flag flip: it rescans the library, because sqlite
// can only vouch for its own pages and the files they describe were on the
// drive that was pulled.
func TestVerifyRescansAndRecordsTheRecovery(t *testing.T) {
	h, _, mgr := newDirtyServer(t, true)

	rec := do(t, h, http.MethodPost, "/api/v1/system/verify", "")
	wantStatus(t, rec, http.StatusOK)
	var verified verifyResponse
	decodeBody(t, rec, &verified)
	if !verified.Scanning {
		t.Fatal("verify did not report a scan")
	}

	deadline := time.Now().Add(2 * time.Second)
	for mgr.scanCount.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := mgr.scanCount.Load(); got != 1 {
		t.Fatalf("Scan was called %d times; want 1", got)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/events", "")
	wantStatus(t, rec, http.StatusOK)
	var feed struct {
		Events []core.Event `json:"events"`
	}
	decodeBody(t, rec, &feed)
	found := false
	for _, e := range feed.Events {
		if e.Category == EventCategorySystem {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %q event in the activity feed after verifying: %+v", EventCategorySystem, feed.Events)
	}
}

// POST /system/shutdown pulls the trigger the serving process wired to its
// signal handler, and answers before it does so the browser can say "safe to
// eject".
func TestShutdownPullsTheStopTrigger(t *testing.T) {
	stopped := make(chan struct{})
	h, _, _ := newTestServer(t, WithShutdown(func() { close(stopped) }))

	rec := do(t, h, http.MethodPost, "/api/v1/system/shutdown", "")
	wantStatus(t, rec, http.StatusAccepted)

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("the shutdown trigger was never pulled")
	}
}

// A process with no way to stop itself says so rather than reporting a
// shutdown that will not happen — the whole point of the endpoint is telling
// the user when the drive is safe.
func TestShutdownWithoutATriggerIsUnavailable(t *testing.T) {
	h, _, _ := newTestServer(t)
	rec := do(t, h, http.MethodPost, "/api/v1/system/shutdown", "")
	wantStatus(t, rec, http.StatusServiceUnavailable)
	wantErrorBody(t, rec)
}

// Stopping the server and clearing the dirty flag are inside the auth gate:
// neither is exempt, so a password-protected Caravan cannot be shut down by a
// stranger on the network.
func TestIntegrityEndpointsRequireAuth(t *testing.T) {
	stopped := make(chan struct{}, 1)
	h, st, _ := newTestServer(t,
		WithShutdown(func() { stopped <- struct{}{} }),
		WithDirtyStart(true))
	setPassword(t, st, testPassword)

	for _, path := range []string{"/api/v1/system/shutdown", "/api/v1/system/verify"} {
		rec := do(t, h, http.MethodPost, path, "")
		wantStatus(t, rec, http.StatusUnauthorized)
		wantErrorBody(t, rec)
	}

	select {
	case <-stopped:
		t.Fatal("an unauthenticated request stopped the server")
	case <-time.After(50 * time.Millisecond):
	}

	// Still dirty, as seen through an authenticated status read.
	cookie := login(t, h, testPassword)
	rec := doAuth(t, h, http.MethodGet, "/api/v1/system/status", "", func(r *http.Request) {
		r.AddCookie(cookie)
	})
	wantStatus(t, rec, http.StatusOK)
	var status statusResponse
	decodeBody(t, rec, &status)
	if !status.Dirty {
		t.Fatal("an unauthenticated request cleared the dirty flag")
	}
}
