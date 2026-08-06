package api

import (
	"net/http"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/notify"
)

// notificationWebhookJSON is one notification target on the wire. The URL is
// a write-only credential because real webhook tokens live in its path or
// query string; responses report only whether one is configured.
type notificationWebhookJSON struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	HasURL      bool   `json:"has_url"`
	OnGrab      bool   `json:"on_grab"`
	OnImport    bool   `json:"on_import"`
	OnHealth    bool   `json:"on_health"`
	Enabled     bool   `json:"enabled"`
	LastEventID int64  `json:"last_event_id"`
	CreatedAt   string `json:"created_at"`
	UpdatedAt   string `json:"updated_at"`
}

func notificationWebhookDTO(webhook core.NotificationWebhook) notificationWebhookJSON {
	return notificationWebhookJSON{
		ID:          webhook.ID,
		Name:        webhook.Name,
		HasURL:      webhook.URL != "",
		OnGrab:      webhook.OnGrab,
		OnImport:    webhook.OnImport,
		OnHealth:    webhook.OnHealth,
		Enabled:     webhook.Enabled,
		LastEventID: webhook.LastEventID,
		CreatedAt:   jsonTime(webhook.CreatedAt),
		UpdatedAt:   jsonTime(webhook.UpdatedAt),
	}
}

// notificationWebhookRequest is the body for create and update. URL is a
// pointer so an update can omit the write-only credential and preserve it.
// Pointer booleans distinguish omitted create values from disabled toggles.
type notificationWebhookRequest struct {
	Name     string  `json:"name"`
	URL      *string `json:"url"`
	OnGrab   *bool   `json:"on_grab"`
	OnImport *bool   `json:"on_import"`
	OnHealth *bool   `json:"on_health"`
	Enabled  *bool   `json:"enabled"`
}

func (b notificationWebhookRequest) config(existingURL string) (core.NotificationWebhook, string) {
	webhook := core.NotificationWebhook{
		Name:     strings.TrimSpace(b.Name),
		URL:      existingURL,
		OnGrab:   true,
		OnImport: true,
		OnHealth: true,
		Enabled:  true,
	}
	if webhook.Name == "" {
		return core.NotificationWebhook{}, "name is required"
	}
	if b.URL != nil {
		endpoint, err := notify.ValidateURL(*b.URL)
		if err != nil {
			return core.NotificationWebhook{}, err.Error()
		}
		webhook.URL = endpoint
	}
	if webhook.URL == "" {
		return core.NotificationWebhook{}, "URL is required"
	}
	if b.OnGrab != nil {
		webhook.OnGrab = *b.OnGrab
	}
	if b.OnImport != nil {
		webhook.OnImport = *b.OnImport
	}
	if b.OnHealth != nil {
		webhook.OnHealth = *b.OnHealth
	}
	if b.Enabled != nil {
		webhook.Enabled = *b.Enabled
	}
	if !webhook.OnGrab && !webhook.OnImport && !webhook.OnHealth {
		return core.NotificationWebhook{}, "at least one event toggle is required"
	}
	return webhook, ""
}

// handleListNotificationWebhooks returns every configured webhook.
func (s *server) handleListNotificationWebhooks(w http.ResponseWriter, r *http.Request) {
	webhooks, err := s.st.ListNotificationWebhooks(r.Context())
	if err != nil {
		s.writeStoreError(w, "list notification webhooks", err)
		return
	}
	out := make([]notificationWebhookJSON, 0, len(webhooks))
	for _, webhook := range webhooks {
		out = append(out, notificationWebhookDTO(webhook))
	}
	writeJSON(w, http.StatusOK, map[string]any{"notification_webhooks": out})
}

// handleCreateNotificationWebhook creates a webhook. The store starts its
// cursor at the newest current event so historical activity is not sent.
func (s *server) handleCreateNotificationWebhook(w http.ResponseWriter, r *http.Request) {
	var body notificationWebhookRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	webhook, msg := body.config("")
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	if err := s.st.CreateNotificationWebhook(r.Context(), &webhook); err != nil {
		s.writeStoreError(w, "create notification webhook", err)
		return
	}
	writeJSON(w, http.StatusCreated, notificationWebhookDTO(webhook))
}

// handleUpdateNotificationWebhook replaces configurable fields while keeping
// the stored webhook's identity and delivery cursor.
func (s *server) handleUpdateNotificationWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body notificationWebhookRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	stored, err := s.st.GetNotificationWebhook(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get notification webhook", err)
		return
	}
	webhook, msg := body.config(stored.URL)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	webhook.ID = stored.ID
	webhook.LastEventID = stored.LastEventID
	webhook.CreatedAt = stored.CreatedAt
	if err := s.st.UpdateNotificationWebhook(r.Context(), &webhook); err != nil {
		s.writeStoreError(w, "update notification webhook", err)
		return
	}
	writeJSON(w, http.StatusOK, notificationWebhookDTO(webhook))
}

// handleDeleteNotificationWebhook deletes one configured webhook.
func (s *server) handleDeleteNotificationWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.st.DeleteNotificationWebhook(r.Context(), id); err != nil {
		s.writeStoreError(w, "delete notification webhook", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleTestNotificationWebhook sends a test payload to a stored webhook. It
// does not advance the cursor, so a settings check cannot lose a real event.
func (s *server) handleTestNotificationWebhook(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	webhook, err := s.st.GetNotificationWebhook(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get notification webhook", err)
		return
	}
	if err := notify.New(s.st).Test(r.Context(), *webhook); err != nil {
		writeError(w, http.StatusBadGateway, "webhook test delivery failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
