package api

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestNotificationWebhookHandlersCRUDAndDefaults(t *testing.T) {
	_, st, _ := newTestServer(t)
	server := &server{st: st, log: slog.Default()}
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer endpoint.Close()
	secretEndpoint := endpoint.URL + "/services/path-token?key=query-secret"

	createdRec := callNotificationWebhookHandler(t, server.handleCreateNotificationWebhook, http.MethodPost, "/notification-webhooks", `{"name":"  Library updates  ","url":"`+secretEndpoint+`"}`)
	if createdRec.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body %s", createdRec.Code, createdRec.Body.String())
	}
	if strings.Contains(createdRec.Body.String(), "path-token") || strings.Contains(createdRec.Body.String(), "query-secret") {
		t.Fatalf("create response leaked webhook credential: %s", createdRec.Body.String())
	}
	created := decodeNotificationWebhook(t, createdRec)
	if created.Name != "Library updates" || !created.HasURL || !created.OnGrab || !created.OnImport || !created.OnHealth || !created.Enabled || created.ID == 0 {
		t.Fatalf("created webhook = %#v", created)
	}

	ctx := context.Background()
	event := &core.Event{Level: core.EventLevelInfo, Category: "grab", Message: "Grabbed release"}
	if err := st.InsertEvent(ctx, event); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}
	if err := st.AdvanceNotificationWebhookCursor(ctx, created.ID, event.ID); err != nil {
		t.Fatalf("AdvanceNotificationWebhookCursor: %v", err)
	}
	updatedRequest := httptest.NewRequest(http.MethodPut, "/notification-webhooks/"+jsonNumber(created.ID), strings.NewReader(`{"name":"Import updates","on_grab":false,"on_import":true,"on_health":false,"enabled":false}`))
	updatedRequest.Header.Set("Content-Type", "application/json")
	updatedRequest.SetPathValue("id", jsonNumber(created.ID))
	updatedRec := httptest.NewRecorder()
	server.handleUpdateNotificationWebhook(updatedRec, updatedRequest)
	if updatedRec.Code != http.StatusOK {
		t.Fatalf("update status = %d, body %s", updatedRec.Code, updatedRec.Body.String())
	}
	updated := decodeNotificationWebhook(t, updatedRec)
	if updated.ID != created.ID || updated.LastEventID != event.ID || updated.OnGrab || !updated.OnImport || updated.OnHealth || updated.Enabled {
		t.Fatalf("updated webhook = %#v", updated)
	}
	stored, err := st.GetNotificationWebhook(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetNotificationWebhook after update: %v", err)
	}
	if stored.URL != secretEndpoint {
		t.Fatalf("update without URL changed stored endpoint to %q", stored.URL)
	}

	listRec := callNotificationWebhookHandler(t, server.handleListNotificationWebhooks, http.MethodGet, "/notification-webhooks", "")
	if listRec.Code != http.StatusOK || !strings.Contains(listRec.Body.String(), "Import updates") {
		t.Fatalf("list response = %d %s", listRec.Code, listRec.Body.String())
	}
	if strings.Contains(listRec.Body.String(), "path-token") || strings.Contains(listRec.Body.String(), "query-secret") {
		t.Fatalf("list response leaked webhook credential: %s", listRec.Body.String())
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/notification-webhooks/"+jsonNumber(created.ID), nil)
	deleteRequest.SetPathValue("id", jsonNumber(created.ID))
	deleteRec := httptest.NewRecorder()
	server.handleDeleteNotificationWebhook(deleteRec, deleteRequest)
	if deleteRec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d, body %s", deleteRec.Code, deleteRec.Body.String())
	}
}

func TestNotificationWebhookHandlersRejectInvalidURLsWithoutLeakingPassword(t *testing.T) {
	_, st, _ := newTestServer(t)
	server := &server{st: st, log: slog.Default()}

	for _, body := range []string{
		`{"name":"bad scheme","url":"ftp://example.test/hook"}`,
		`{"name":"credential","url":"https://user:password@example.test/hook"}`,
		`{"name":"no events","url":"https://example.test/hook","on_grab":false,"on_import":false,"on_health":false}`,
	} {
		rec := callNotificationWebhookHandler(t, server.handleCreateNotificationWebhook, http.MethodPost, "/notification-webhooks", body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("status = %d for %s, want 400; body %s", rec.Code, body, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "password") {
			t.Errorf("response leaked URL password: %s", rec.Body.String())
		}
	}
	dto := notificationWebhookDTO(core.NotificationWebhook{URL: "https://example.test/services/path-token?key=query-secret"})
	encoded, err := json.Marshal(dto)
	if err != nil {
		t.Fatalf("marshal DTO: %v", err)
	}
	if !dto.HasURL || strings.Contains(string(encoded), "path-token") || strings.Contains(string(encoded), "query-secret") {
		t.Fatalf("DTO leaked webhook URL: %s", encoded)
	}
}

func TestHandleTestNotificationWebhookSendsTestPayloadWithoutChangingCursor(t *testing.T) {
	_, st, _ := newTestServer(t)
	server := &server{st: st, log: slog.Default()}
	var payload map[string]any
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Errorf("decode payload: %v", err)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer endpoint.Close()

	ctx := context.Background()
	webhook := core.NotificationWebhook{Name: "test", URL: endpoint.URL, OnGrab: true, OnImport: true, OnHealth: true, Enabled: true}
	if err := st.CreateNotificationWebhook(ctx, &webhook); err != nil {
		t.Fatalf("CreateNotificationWebhook: %v", err)
	}
	event := &core.Event{Level: core.EventLevelInfo, Category: "grab", Message: "Grabbed release"}
	if err := st.InsertEvent(ctx, event); err != nil {
		t.Fatalf("InsertEvent: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/notification-webhooks/"+jsonNumber(webhook.ID)+"/test", nil)
	req.SetPathValue("id", jsonNumber(webhook.ID))
	rec := httptest.NewRecorder()
	server.handleTestNotificationWebhook(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("test status = %d, body %s", rec.Code, rec.Body.String())
	}
	if payload["event"] != "test" {
		t.Fatalf("test payload = %#v", payload)
	}
	stored, err := st.GetNotificationWebhook(ctx, webhook.ID)
	if err != nil {
		t.Fatalf("GetNotificationWebhook: %v", err)
	}
	if stored.LastEventID != 0 {
		t.Fatalf("test delivery advanced cursor to %d", stored.LastEventID)
	}
}

func callNotificationWebhookHandler(t *testing.T, handler http.HandlerFunc, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	handler(rec, req)
	return rec
}

func decodeNotificationWebhook(t *testing.T, rec *httptest.ResponseRecorder) notificationWebhookJSON {
	t.Helper()
	var webhook notificationWebhookJSON
	if err := json.Unmarshal(rec.Body.Bytes(), &webhook); err != nil {
		t.Fatalf("decode webhook: %v; body %s", err, rec.Body.String())
	}
	return webhook
}

func jsonNumber(value int64) string {
	return strconv.FormatInt(value, 10)
}
