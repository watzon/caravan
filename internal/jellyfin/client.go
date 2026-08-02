// Package jellyfin is Caravan's client for the Jellyfin server API and the
// playback handoff built on it (SPEC §5.2, PLAN phase 4 task 1).
//
// The surface is deliberately two calls wide. Caravan does not manage a
// Jellyfin library, it only tells one that something changed: GET /System/Info
// answers "are these credentials any good", POST /Library/Refresh answers
// "go and look again". Everything else about the library — layout, NFOs,
// artwork — is already on disk in the conventions Jellyfin reads (SPEC §6), so
// there is nothing to synchronize over an API.
//
// Nothing here retries. A refresh that does not land is not lost: the caller is
// a durable job whose own backoff owns that decision (SPEC §7).
package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	// DefaultTimeout bounds a single request. Jellyfin is normally on the same
	// LAN as Caravan; a handoff that hangs must not hold a job's lease open.
	DefaultTimeout = 10 * time.Second

	// authHeader is Jellyfin's API-key header. The `api_key` query parameter
	// works too, but a credential in a query string is a credential in an
	// access log (SPEC §12).
	authHeader = "X-Emby-Token"

	// maxErrorBody bounds how much of a failure response is read before giving
	// up on quoting it.
	maxErrorBody = 4 << 10
)

// ErrUnauthorized means the API key is missing, wrong, or lacks the rights the
// call needs (a library refresh is an administrator action).
var ErrUnauthorized = errors.New("jellyfin: unauthorized")

// APIError is a non-2xx response from Jellyfin.
type APIError struct {
	StatusCode int
	// Message is Jellyfin's own body text when it sent any, otherwise the HTTP
	// status text.
	Message string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("jellyfin: http %d: %s", e.StatusCode, e.Message)
}

// Unwrap maps the statuses callers branch on onto sentinel errors, so they can
// use errors.Is without knowing about APIError.
func (e *APIError) Unwrap() error {
	switch e.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	}
	return nil
}

// Client talks to one Jellyfin server.
type Client struct {
	// BaseURL is the server root, e.g. http://jellyfin.lan:8096.
	BaseURL string
	// APIKey is an API key created in Jellyfin's dashboard.
	APIKey string

	hc *http.Client
}

// ServerInfo is the part of GET /System/Info the settings screen shows after a
// successful test. The field names are Jellyfin's PascalCase.
type ServerInfo struct {
	Name    string `json:"ServerName"`
	Version string `json:"Version"`
	ID      string `json:"Id"`
}

// NewClient returns a client for baseURL. A nil hc gets one with DefaultTimeout.
func NewClient(baseURL, apiKey string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: DefaultTimeout}
	}
	return &Client{BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"), APIKey: strings.TrimSpace(apiKey), hc: hc}
}

// SystemInfo asks the server who it is. It is the test-connection call: it
// needs a valid API key, so a 200 proves both that the URL is a Jellyfin server
// and that the credential works.
func (c *Client) SystemInfo(ctx context.Context) (*ServerInfo, error) {
	resp, err := c.do(ctx, http.MethodGet, "/System/Info")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var info ServerInfo
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxErrorBody)).Decode(&info); err != nil {
		return nil, fmt.Errorf("jellyfin: decode /System/Info: %w", err)
	}
	return &info, nil
}

// RefreshLibrary asks the server to rescan its libraries. Jellyfin answers 204
// and does the work in the background, so this returning nil means "the scan
// was accepted", never "the scan finished".
func (c *Client) RefreshLibrary(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodPost, "/Library/Refresh")
	if err != nil {
		return err
	}
	// Nothing useful in the body; drain it so the connection can be reused.
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxErrorBody))
	return resp.Body.Close()
}

// do issues one request and returns a 2xx response with an open body, or an
// error with the body already closed.
func (c *Client) do(ctx context.Context, method, path string) (*http.Response, error) {
	if c.BaseURL == "" {
		return nil, errors.New("jellyfin: no server URL is configured")
	}
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("jellyfin: request %s: %w", path, err)
	}
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set(authHeader, c.APIKey)
	}

	resp, err := c.hc.Do(req)
	if err != nil {
		// *url.Error stringifies the whole URL. It carries no credential here
		// (the key is a header) but it is noise in a user-facing message, so
		// unwrap to the transport error.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, fmt.Errorf("jellyfin: %s %s: %w", method, path, err)
	}
	if resp.StatusCode/100 == 2 {
		return resp, nil
	}

	apiErr := readError(resp)
	resp.Body.Close()
	return nil, apiErr
}

// readError builds an APIError from a non-2xx response without closing it.
func readError(resp *http.Response) *APIError {
	e := &APIError{StatusCode: resp.StatusCode}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
	e.Message = strings.TrimSpace(string(body))
	if e.Message == "" {
		e.Message = http.StatusText(resp.StatusCode)
	}
	return e
}
