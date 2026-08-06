package store

import (
	"context"
	"errors"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestNotificationWebhookCRUDPreservesCursor(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	for _, message := range []string{"before one", "before two"} {
		if err := st.InsertEvent(ctx, &core.Event{Category: "grab", Message: message}); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}
	lastBeforeCreate, err := st.CurrentEventID(ctx)
	if err != nil {
		t.Fatalf("CurrentEventID: %v", err)
	}

	webhook := core.NotificationWebhook{
		Name:     "Library feed",
		URL:      "https://example.test/hooks/library",
		OnGrab:   true,
		OnImport: false,
		OnHealth: true,
		Enabled:  true,
	}
	if err := st.CreateNotificationWebhook(ctx, &webhook); err != nil {
		t.Fatalf("CreateNotificationWebhook: %v", err)
	}
	if webhook.ID == 0 || webhook.CreatedAt.IsZero() || webhook.UpdatedAt.IsZero() {
		t.Fatalf("created webhook missing identity or times: %#v", webhook)
	}
	if webhook.LastEventID != lastBeforeCreate {
		t.Fatalf("LastEventID = %d, want current event %d", webhook.LastEventID, lastBeforeCreate)
	}

	if err := st.AdvanceNotificationWebhookCursor(ctx, webhook.ID, lastBeforeCreate+4); err != nil {
		t.Fatalf("AdvanceNotificationWebhookCursor: %v", err)
	}
	webhook.Name = "Renamed feed"
	webhook.URL = "https://example.test/hooks/renamed"
	webhook.OnGrab = false
	webhook.OnImport = true
	webhook.OnHealth = false
	webhook.Enabled = false
	webhook.LastEventID = 0
	if err := st.UpdateNotificationWebhook(ctx, &webhook); err != nil {
		t.Fatalf("UpdateNotificationWebhook: %v", err)
	}
	if webhook.LastEventID != lastBeforeCreate+4 {
		t.Fatalf("update changed cursor to %d, want %d", webhook.LastEventID, lastBeforeCreate+4)
	}
	if webhook.Name != "Renamed feed" || webhook.OnImport != true || webhook.Enabled != false {
		t.Fatalf("updated webhook = %#v", webhook)
	}

	list, err := st.ListNotificationWebhooks(ctx)
	if err != nil {
		t.Fatalf("ListNotificationWebhooks: %v", err)
	}
	if len(list) != 1 || list[0].ID != webhook.ID {
		t.Fatalf("ListNotificationWebhooks = %#v, want one webhook %d", list, webhook.ID)
	}

	if err := st.DeleteNotificationWebhook(ctx, webhook.ID); err != nil {
		t.Fatalf("DeleteNotificationWebhook: %v", err)
	}
	_, err = st.GetNotificationWebhook(ctx, webhook.ID)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetNotificationWebhook after delete error = %v, want ErrNotFound", err)
	}
}

func TestCreateNotificationWebhookSkipsHistoricalEvents(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	old := &core.Event{Category: "grab", Message: "old"}
	if err := st.InsertEvent(ctx, old); err != nil {
		t.Fatalf("InsertEvent old: %v", err)
	}
	webhook := core.NotificationWebhook{
		Name:     "No history",
		URL:      "https://example.test/hook",
		OnGrab:   true,
		OnImport: true,
		OnHealth: true,
		Enabled:  true,
	}
	if err := st.CreateNotificationWebhook(ctx, &webhook); err != nil {
		t.Fatalf("CreateNotificationWebhook: %v", err)
	}
	newEvent := &core.Event{Category: "grab", Message: "new"}
	if err := st.InsertEvent(ctx, newEvent); err != nil {
		t.Fatalf("InsertEvent new: %v", err)
	}

	events, err := st.ListEventsAfter(ctx, webhook.LastEventID, 100)
	if err != nil {
		t.Fatalf("ListEventsAfter: %v", err)
	}
	if len(events) != 1 || events[0].ID != newEvent.ID {
		t.Fatalf("ListEventsAfter = %#v, want only event %d", events, newEvent.ID)
	}
}

func TestListEventsAfterBoundsBatch(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)
	for _, message := range []string{"one", "two", "three"} {
		if err := st.InsertEvent(ctx, &core.Event{Category: "grab", Message: message}); err != nil {
			t.Fatalf("InsertEvent %q: %v", message, err)
		}
	}

	events, err := st.ListEventsAfter(ctx, 0, 2)
	if err != nil {
		t.Fatalf("ListEventsAfter: %v", err)
	}
	if len(events) != 2 || events[0].Message != "one" || events[1].Message != "two" {
		t.Fatalf("ListEventsAfter = %#v, want first two events", events)
	}
}
