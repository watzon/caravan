package jellyfin

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// newTestService builds a service over a real sqlite store with the coalescing
// window collapsed, so a queued job is claimable immediately instead of twenty
// seconds from now.
func newTestService(t *testing.T, hc *http.Client) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	svc := NewService(st, hc, discardLogger())
	svc.window = 0
	return svc, st
}

func configure(t *testing.T, st *store.Store, url, key string, enabled bool) {
	t.Helper()
	ctx := context.Background()
	for k, v := range map[string]string{
		store.SettingJellyfinURL:     url,
		store.SettingJellyfinAPIKey:  key,
		store.SettingJellyfinEnabled: strconv.FormatBool(enabled),
	} {
		if err := st.SetSetting(ctx, k, v); err != nil {
			t.Fatalf("SetSetting %s: %v", k, err)
		}
	}
}

func openJobs(t *testing.T, st *store.Store) []core.Job {
	t.Helper()
	jobs, err := st.ListJobs(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	out := []core.Job{}
	for _, j := range jobs {
		if j.Kind == JobKind {
			out = append(out, j)
		}
	}
	return out
}

func TestConfigOnAFreshDatabaseIsDisabled(t *testing.T) {
	svc, _ := newTestService(t, nil)

	cfg, err := svc.Config(context.Background())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.Enabled || cfg.URL != "" || cfg.APIKey != "" {
		t.Fatalf("Config = %+v, want zero", cfg)
	}
	if cfg.Ready() {
		t.Fatal("an unconfigured handoff must not be Ready")
	}
}

func TestLibraryChangedQueuesOneScan(t *testing.T) {
	svc, st := newTestService(t, nil)
	configure(t, st, "http://jellyfin.lan:8096", "secret", true)

	if err := svc.LibraryChanged(context.Background()); err != nil {
		t.Fatalf("LibraryChanged: %v", err)
	}
	jobs := openJobs(t, st)
	if len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1", len(jobs))
	}
	if jobs[0].Payload != jobPayload {
		t.Fatalf("payload = %q, want %q", jobs[0].Payload, jobPayload)
	}
}

// The debounce: a burst of imports owes one scan, not one per file.
func TestLibraryChangedCoalescesABurst(t *testing.T) {
	svc, st := newTestService(t, nil)
	configure(t, st, "http://jellyfin.lan:8096", "secret", true)

	for range 5 {
		if err := svc.LibraryChanged(context.Background()); err != nil {
			t.Fatalf("LibraryChanged: %v", err)
		}
	}
	if jobs := openJobs(t, st); len(jobs) != 1 {
		t.Fatalf("jobs = %d, want 1 after a burst of 5", len(jobs))
	}
}

func TestLibraryChangedQueuesNothingWhenDisabled(t *testing.T) {
	svc, st := newTestService(t, nil)
	configure(t, st, "http://jellyfin.lan:8096", "secret", false)

	if err := svc.LibraryChanged(context.Background()); err != nil {
		t.Fatalf("LibraryChanged: %v", err)
	}
	if jobs := openJobs(t, st); len(jobs) != 0 {
		t.Fatalf("jobs = %d, want 0 while the handoff is off", len(jobs))
	}
}

// Enabled with a blank URL is a half-finished settings form. Queueing a job
// that can only ever fail would fill the activity feed with the user's typo.
func TestLibraryChangedQueuesNothingWithoutAURL(t *testing.T) {
	svc, st := newTestService(t, nil)
	configure(t, st, "", "secret", true)

	if err := svc.LibraryChanged(context.Background()); err != nil {
		t.Fatalf("LibraryChanged: %v", err)
	}
	if jobs := openJobs(t, st); len(jobs) != 0 {
		t.Fatalf("jobs = %d, want 0 without a URL", len(jobs))
	}
}

func TestHandleTriggersTheScan(t *testing.T) {
	srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL, "secret", true)

	if err := svc.Handle(context.Background(), st, nil); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(srv.calls) != 1 || srv.calls[0].path != "/Library/Refresh" {
		t.Fatalf("calls = %+v, want one POST /Library/Refresh", srv.calls)
	}

	// A successful handoff is a log line, not feed noise: the import that
	// caused it already wrote an entry.
	if events := listEvents(t, st); len(events) != 0 {
		t.Fatalf("events = %+v, want none on success", events)
	}
}

func TestHandleReportsAFailureAndLetsTheQueueRetry(t *testing.T) {
	srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL, "secret", true)

	// The error is handed back so the job queue backs off and retries.
	if err := svc.Handle(context.Background(), st, nil); err == nil {
		t.Fatal("Handle: want the server error back")
	}

	events := listEvents(t, st)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Level != core.EventLevelWarn || events[0].Category != EventCategory {
		t.Fatalf("event = %+v", events[0])
	}
}

// Switching the handoff off between the import and the job must stop the call,
// because the configuration is re-read at run time rather than carried in the
// payload.
func TestHandleSkipsWhenDisabledAfterQueueing(t *testing.T) {
	srv := newRecordingServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL, "secret", true)

	if err := svc.LibraryChanged(context.Background()); err != nil {
		t.Fatalf("LibraryChanged: %v", err)
	}
	configure(t, st, srv.URL, "secret", false)

	if err := svc.Handle(context.Background(), st, nil); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(srv.calls) != 0 {
		t.Fatalf("calls = %+v, want none once the handoff is off", srv.calls)
	}
}

func listEvents(t *testing.T, st *store.Store) []core.Event {
	t.Helper()
	events, err := st.ListEvents(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	return events
}

// discardLogger keeps a test's expected warnings out of the test output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
