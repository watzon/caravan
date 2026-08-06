package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

func TestMatchUsesExactEventRules(t *testing.T) {
	all := core.NotificationWebhook{Enabled: true, OnGrab: true, OnImport: true, OnHealth: true}
	tests := []struct {
		name      string
		webhook   core.NotificationWebhook
		event     core.Event
		wantName  string
		wantMatch bool
	}{
		{
			name:      "successful grab",
			webhook:   all,
			event:     core.Event{Level: core.EventLevelInfo, Category: "grab"},
			wantName:  "grabbed",
			wantMatch: true,
		},
		{
			name:      "successful import",
			webhook:   all,
			event:     core.Event{Level: core.EventLevelInfo, Category: "import", Message: "Imported movie.mkv"},
			wantName:  "imported",
			wantMatch: true,
		},
		{
			name:    "import without success prefix",
			webhook: all,
			event:   core.Event{Level: core.EventLevelInfo, Category: "import", Message: "Import failed"},
		},
		{
			name:      "warning is health regardless of category",
			webhook:   all,
			event:     core.Event{Level: core.EventLevelWarn, Category: "grab"},
			wantName:  "health",
			wantMatch: true,
		},
		{
			name:    "info health category does not match",
			webhook: all,
			event:   core.Event{Level: core.EventLevelInfo, Category: "health"},
		},
		{
			name:    "disabled webhook",
			webhook: core.NotificationWebhook{OnGrab: true},
			event:   core.Event{Level: core.EventLevelInfo, Category: "grab"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			name, matched := Match(tt.webhook, tt.event)
			if name != tt.wantName || matched != tt.wantMatch {
				t.Fatalf("Match = (%q, %t), want (%q, %t)", name, matched, tt.wantName, tt.wantMatch)
			}
		})
	}
}

func TestDispatchPostsStableJSONAndAdvancesCursor(t *testing.T) {
	var got Delivery
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if gotContentType := r.Header.Get("Content-Type"); gotContentType != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", gotContentType)
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	ctx := context.Background()
	st := openStore(t)
	webhook := createWebhook(t, ctx, st, server.URL, true)
	createdAt := time.Date(2026, 8, 5, 12, 0, 0, 123, time.UTC)
	event := &core.Event{
		Level: core.EventLevelInfo, Category: "import", Message: "Imported movie.mkv", Detail: "/media/movie.mkv", CreatedAt: createdAt,
	}
	if err := st.InsertEvent(ctx, event); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	if err := New(st).Dispatch(ctx); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	want := Delivery{
		Event: "imported", Level: core.EventLevelInfo, Category: "import", Message: "Imported movie.mkv", Detail: "/media/movie.mkv", CreatedAt: createdAt.Format(time.RFC3339Nano),
	}
	if got != want {
		t.Fatalf("payload = %#v, want %#v", got, want)
	}
	stored, err := st.GetNotificationWebhook(ctx, webhook.ID)
	if err != nil {
		t.Fatalf("GetNotificationWebhook: %v", err)
	}
	if stored.LastEventID != event.ID {
		t.Fatalf("cursor = %d, want %d", stored.LastEventID, event.ID)
	}
}

func TestDispatchRetriesFailedEventWithoutLosingCursor(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := context.Background()
	st := openStore(t)
	webhook := createWebhook(t, ctx, st, server.URL, true)
	event := &core.Event{Level: core.EventLevelInfo, Category: "grab", Message: "Grabbed release"}
	if err := st.InsertEvent(ctx, event); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	if err := New(st).Dispatch(ctx); err == nil {
		t.Fatal("first Dispatch succeeded, want delivery error")
	}
	stored, err := st.GetNotificationWebhook(ctx, webhook.ID)
	if err != nil {
		t.Fatalf("GetNotificationWebhook after failed delivery: %v", err)
	}
	if stored.LastEventID != 0 {
		t.Fatalf("cursor after failure = %d, want 0", stored.LastEventID)
	}
	if err := New(st).Dispatch(ctx); err != nil {
		t.Fatalf("second Dispatch: %v", err)
	}
	stored, err = st.GetNotificationWebhook(ctx, webhook.ID)
	if err != nil {
		t.Fatalf("GetNotificationWebhook after retry: %v", err)
	}
	if attempts != 2 || stored.LastEventID != event.ID {
		t.Fatalf("attempts %d, cursor %d; want 2 attempts and cursor %d", attempts, stored.LastEventID, event.ID)
	}
}

func TestDispatchContinuesOtherWebhooksAfterTargetFailure(t *testing.T) {
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer failing.Close()
	deliveries := 0
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		deliveries++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer healthy.Close()

	ctx := context.Background()
	st := openStore(t)
	failedWebhook := createWebhookNamed(t, ctx, st, "a failing", failing.URL, true)
	healthyWebhook := createWebhookNamed(t, ctx, st, "b healthy", healthy.URL, true)
	event := &core.Event{Level: core.EventLevelInfo, Category: "grab", Message: "Grabbed release"}
	if err := st.InsertEvent(ctx, event); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	if err := New(st).Dispatch(ctx); err == nil {
		t.Fatal("Dispatch succeeded, want aggregated delivery error")
	}
	failedStored, err := st.GetNotificationWebhook(ctx, failedWebhook.ID)
	if err != nil {
		t.Fatalf("GetNotificationWebhook failed target: %v", err)
	}
	healthyStored, err := st.GetNotificationWebhook(ctx, healthyWebhook.ID)
	if err != nil {
		t.Fatalf("GetNotificationWebhook healthy target: %v", err)
	}
	if failedStored.LastEventID != 0 {
		t.Fatalf("failed cursor = %d, want 0", failedStored.LastEventID)
	}
	if deliveries != 1 || healthyStored.LastEventID != event.ID {
		t.Fatalf("healthy deliveries = %d, cursor = %d; want 1 and %d", deliveries, healthyStored.LastEventID, event.ID)
	}
}

func TestDispatchProcessesOneBoundedBatchPerWebhook(t *testing.T) {
	deliveries := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		deliveries++
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	ctx := context.Background()
	st := openStore(t)
	webhook := createWebhook(t, ctx, st, server.URL, true)
	events := make([]*core.Event, deliveryBatchSize+1)
	for i := range events {
		events[i] = &core.Event{Level: core.EventLevelInfo, Category: "grab", Message: fmt.Sprintf("Grabbed release %d", i)}
		if err := st.InsertEvent(ctx, events[i]); err != nil {
			t.Fatalf("InsertEvent %d: %v", i, err)
		}
	}

	if err := New(st).Dispatch(ctx); err != nil {
		t.Fatalf("first Dispatch: %v", err)
	}
	stored, err := st.GetNotificationWebhook(ctx, webhook.ID)
	if err != nil {
		t.Fatalf("GetNotificationWebhook after first batch: %v", err)
	}
	if deliveries != deliveryBatchSize || stored.LastEventID != events[deliveryBatchSize-1].ID {
		t.Fatalf("first batch deliveries = %d, cursor = %d; want %d and %d", deliveries, stored.LastEventID, deliveryBatchSize, events[deliveryBatchSize-1].ID)
	}

	if err := New(st).Dispatch(ctx); err != nil {
		t.Fatalf("second Dispatch: %v", err)
	}
	stored, err = st.GetNotificationWebhook(ctx, webhook.ID)
	if err != nil {
		t.Fatalf("GetNotificationWebhook after second batch: %v", err)
	}
	if deliveries != deliveryBatchSize+1 || stored.LastEventID != events[deliveryBatchSize].ID {
		t.Fatalf("final deliveries = %d, cursor = %d; want %d and %d", deliveries, stored.LastEventID, deliveryBatchSize+1, events[deliveryBatchSize].ID)
	}
}

func TestDispatchSkipsDisabledWebhook(t *testing.T) {
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	ctx := context.Background()
	st := openStore(t)
	webhook := createWebhook(t, ctx, st, server.URL, false)
	event := &core.Event{Level: core.EventLevelInfo, Category: "grab", Message: "Grabbed release"}
	if err := st.InsertEvent(ctx, event); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	if err := New(st).Dispatch(ctx); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	stored, err := st.GetNotificationWebhook(ctx, webhook.ID)
	if err != nil {
		t.Fatalf("GetNotificationWebhook: %v", err)
	}
	if requests != 0 || stored.LastEventID != 0 {
		t.Fatalf("disabled webhook made %d requests and cursor is %d, want no requests and cursor 0", requests, stored.LastEventID)
	}
}

func TestTestPostsTestPayload(t *testing.T) {
	var got Delivery
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	st := openStore(t)
	webhook := core.NotificationWebhook{URL: server.URL}
	if err := New(st).Test(context.Background(), webhook); err != nil {
		t.Fatalf("Test: %v", err)
	}
	if got.Event != "test" || got.Level != core.EventLevelInfo || got.Category != "notification" || got.Message != "Webhook test" || got.CreatedAt == "" {
		t.Fatalf("test payload = %#v", got)
	}
}

func TestValidateURLRejectsUnsupportedSchemesAndUserinfo(t *testing.T) {
	for _, raw := range []string{"", "example.test/hook", "ftp://example.test/hook", "http://", "https://user:password@example.test/hook"} {
		if _, err := ValidateURL(raw); err == nil {
			t.Errorf("ValidateURL(%q) succeeded, want error", raw)
		}
	}
}

func openStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() {
		if err := st.Close(); err != nil {
			t.Errorf("Store.Close: %v", err)
		}
	})
	return st
}

func createWebhook(t *testing.T, ctx context.Context, st *store.Store, endpoint string, enabled bool) core.NotificationWebhook {
	t.Helper()
	return createWebhookNamed(t, ctx, st, "test", endpoint, enabled)
}

func createWebhookNamed(t *testing.T, ctx context.Context, st *store.Store, name, endpoint string, enabled bool) core.NotificationWebhook {
	t.Helper()
	webhook := core.NotificationWebhook{
		Name: name, URL: endpoint, OnGrab: true, OnImport: true, OnHealth: true, Enabled: enabled,
	}
	if err := st.CreateNotificationWebhook(ctx, &webhook); err != nil {
		t.Fatalf("CreateNotificationWebhook: %v", err)
	}
	return webhook
}
