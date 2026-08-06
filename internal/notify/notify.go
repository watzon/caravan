// Package notify delivers selected Caravan activity events to configured webhooks.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

const (
	deliveryTimeout   = 10 * time.Second
	deliveryBatchSize = 100
)

// Delivery is the stable JSON document sent to notification webhooks.
type Delivery struct {
	Event     string `json:"event"`
	Level     string `json:"level"`
	Category  string `json:"category"`
	Message   string `json:"message"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"created_at"`
}

// Notifier delivers new activity events from a Store.
type Notifier struct {
	store  *store.Store
	client *http.Client
}

// New returns a notifier with the required ten-second delivery timeout.
func New(st *store.Store) *Notifier {
	return &Notifier{
		store: st,
		client: &http.Client{
			Timeout: deliveryTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// ValidateURL accepts only absolute HTTP or HTTPS URLs with a host. Userinfo
// is rejected because it is a credential that can leak through an API response
// or an HTTP transport error.
func ValidateURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	u, err := url.Parse(trimmed)
	if err != nil || !u.IsAbs() || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "", fmt.Errorf("URL must be an absolute HTTP or HTTPS URL")
	}
	if u.User != nil {
		return "", fmt.Errorf("URL userinfo is not allowed")
	}
	return trimmed, nil
}

// Match reports whether event is enabled for webhook and, when it is, the
// stable delivery event name.
func Match(webhook core.NotificationWebhook, event core.Event) (string, bool) {
	if !webhook.Enabled {
		return "", false
	}
	switch event.Level {
	case core.EventLevelWarn, core.EventLevelError:
		if webhook.OnHealth {
			return "health", true
		}
	case core.EventLevelInfo:
		switch {
		case webhook.OnGrab && event.Category == "grab":
			return "grabbed", true
		case webhook.OnImport && event.Category == "import" && strings.HasPrefix(event.Message, "Imported "):
			return "imported", true
		}
	}
	return "", false
}

// Dispatch gives each enabled target one bounded batch. A target keeps strict
// event order: its failed event is retried because its cursor does not move.
// Target failures are aggregated only after every other target gets a turn, so
// one broken endpoint cannot starve the rest.
func (n *Notifier) Dispatch(ctx context.Context) error {
	webhooks, err := n.store.ListNotificationWebhooks(ctx)
	if err != nil {
		return fmt.Errorf("notify: list webhooks: %w", err)
	}
	var dispatchErrors []error
	for _, webhook := range webhooks {
		if !webhook.Enabled {
			continue
		}
		events, err := n.store.ListEventsAfter(ctx, webhook.LastEventID, deliveryBatchSize)
		if err != nil {
			dispatchErrors = append(dispatchErrors,
				fmt.Errorf("notify: list events for webhook %d: %w", webhook.ID, err))
			continue
		}
		for _, event := range events {
			if eventName, matched := Match(webhook, event); matched {
				if err := n.deliver(ctx, webhook.URL, deliveryForEvent(eventName, event)); err != nil {
					dispatchErrors = append(dispatchErrors,
						fmt.Errorf("notify: deliver webhook %d: %w", webhook.ID, err))
					break
				}
			}
			if err := n.store.AdvanceNotificationWebhookCursor(ctx, webhook.ID, event.ID); err != nil {
				dispatchErrors = append(dispatchErrors,
					fmt.Errorf("notify: advance webhook %d cursor: %w", webhook.ID, err))
				break
			}
		}
	}
	return errors.Join(dispatchErrors...)
}

// Test sends a test payload to webhook without changing its delivery cursor.
func (n *Notifier) Test(ctx context.Context, webhook core.NotificationWebhook) error {
	return n.deliver(ctx, webhook.URL, Delivery{
		Event:     "test",
		Level:     core.EventLevelInfo,
		Category:  "notification",
		Message:   "Webhook test",
		Detail:    "",
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})
}

func deliveryForEvent(eventName string, event core.Event) Delivery {
	return Delivery{
		Event:     eventName,
		Level:     event.Level,
		Category:  event.Category,
		Message:   event.Message,
		Detail:    event.Detail,
		CreatedAt: event.CreatedAt.UTC().Format(time.RFC3339Nano),
	}
}

func (n *Notifier) deliver(ctx context.Context, rawURL string, payload Delivery) error {
	endpoint, err := ValidateURL(rawURL)
	if err != nil {
		return err
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode notification payload: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build webhook request failed")
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := n.client.Do(req)
	if err != nil {
		// net/http errors include the request URL, which may contain a token in a
		// query string. Keep the endpoint out of errors and logs.
		return fmt.Errorf("webhook request failed")
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("webhook returned HTTP %d", resp.StatusCode)
	}
	return nil
}
