package api

import (
	"net/http"
	"testing"
)

func TestHealthReportsReadyWithJSON(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/health", "")
	wantStatus(t, rec, http.StatusOK)
	if got := rec.Header().Get("Content-Type"); got != contentTypeJSON {
		t.Fatalf("Content-Type = %q, want %q", got, contentTypeJSON)
	}
	var health healthResponse
	decodeBody(t, rec, &health)
	if health.Status != "ok" {
		t.Fatalf("health status = %q, want ok", health.Status)
	}
}

func TestHealthReportsUnavailableWhenDatabaseIsClosed(t *testing.T) {
	h, st, _ := newTestServer(t)
	if err := st.Close(); err != nil {
		t.Fatalf("store.Close: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/health", "")
	wantStatus(t, rec, http.StatusServiceUnavailable)
	wantErrorBody(t, rec)
}

func TestHealthIsUnauthenticated(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)

	rec := do(t, h, http.MethodGet, "/api/v1/health", "")
	wantStatus(t, rec, http.StatusOK)
}
