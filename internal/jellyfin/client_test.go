package jellyfin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// systemInfoBody is what a Jellyfin server answers GET /System/Info with. Only
// the three fields the client reads are kept; the real payload is much larger,
// which is the point of decoding into a narrow struct.
const systemInfoBody = `{
	"ServerName": "basement",
	"Version": "10.9.11",
	"Id": "3b0e2f3a",
	"OperatingSystem": "Linux",
	"StartupWizardCompleted": true
}`

// recordingServer is a fake Jellyfin that records what it was asked.
type recordingServer struct {
	*httptest.Server
	calls []recordedCall
}

type recordedCall struct {
	method string
	path   string
	token  string
}

func newRecordingServer(t *testing.T, handler func(w http.ResponseWriter, r *http.Request)) *recordingServer {
	t.Helper()
	rec := &recordingServer{}
	rec.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.calls = append(rec.calls, recordedCall{
			method: r.Method,
			path:   r.URL.Path,
			token:  r.Header.Get(authHeader),
		})
		handler(w, r)
	}))
	t.Cleanup(rec.Close)
	return rec
}

func TestSystemInfoReadsServerIdentity(t *testing.T) {
	srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(systemInfoBody))
	})

	info, err := NewClient(srv.URL+"/", "secret", srv.Client()).SystemInfo(context.Background())
	if err != nil {
		t.Fatalf("SystemInfo: %v", err)
	}
	if info.Name != "basement" || info.Version != "10.9.11" || info.ID != "3b0e2f3a" {
		t.Fatalf("SystemInfo = %+v", info)
	}

	if len(srv.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(srv.calls))
	}
	call := srv.calls[0]
	if call.method != http.MethodGet || call.path != "/System/Info" {
		t.Fatalf("call = %s %s, want GET /System/Info", call.method, call.path)
	}
	// The credential travels in a header, never in the query string (SPEC §12).
	if call.token != "secret" {
		t.Fatalf("%s = %q, want %q", authHeader, call.token, "secret")
	}
}

func TestSystemInfoUnauthorized(t *testing.T) {
	srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "Access token is invalid or expired.", http.StatusUnauthorized)
	})

	_, err := NewClient(srv.URL, "wrong", srv.Client()).SystemInfo(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("SystemInfo error = %v, want ErrUnauthorized", err)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("SystemInfo error = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", apiErr.StatusCode)
	}
	// The server's own words survive: "it did not work" is not fixable advice.
	if apiErr.Message != "Access token is invalid or expired." {
		t.Fatalf("message = %q", apiErr.Message)
	}
}

func TestRefreshLibraryPostsTheScanTrigger(t *testing.T) {
	srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	if err := NewClient(srv.URL, "secret", srv.Client()).RefreshLibrary(context.Background()); err != nil {
		t.Fatalf("RefreshLibrary: %v", err)
	}
	if len(srv.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(srv.calls))
	}
	call := srv.calls[0]
	if call.method != http.MethodPost || call.path != "/Library/Refresh" {
		t.Fatalf("call = %s %s, want POST /Library/Refresh", call.method, call.path)
	}
	if call.token != "secret" {
		t.Fatalf("%s = %q", authHeader, call.token)
	}
}

// A refresh needs administrator rights; a read-only key gets a 403, and that
// has to be distinguishable from "the server is down" so the user is told to
// fix the key rather than the network.
func TestRefreshLibraryForbiddenIsUnauthorized(t *testing.T) {
	srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	err := NewClient(srv.URL, "reader", srv.Client()).RefreshLibrary(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("RefreshLibrary error = %v, want ErrUnauthorized", err)
	}
	if err.Error() != "jellyfin: http 403: Forbidden" {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestClientWithoutURLDoesNotDial(t *testing.T) {
	if err := NewClient("  ", "secret", nil).RefreshLibrary(context.Background()); err == nil {
		t.Fatal("RefreshLibrary with no URL: want error")
	}
}

// A 500 carries no sentinel: it is a server problem, and the job queue's
// backoff, not a special case here, decides what to do about it.
func TestServerErrorHasNoSentinel(t *testing.T) {
	srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})

	err := NewClient(srv.URL, "secret", srv.Client()).RefreshLibrary(context.Background())
	if err == nil {
		t.Fatal("RefreshLibrary: want error")
	}
	if errors.Is(err, ErrUnauthorized) {
		t.Fatalf("500 should not unwrap to ErrUnauthorized: %v", err)
	}
}
