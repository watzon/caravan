// Package stashbox is Caravan's client for the stash-box GraphQL protocol, the
// metadata provider behind the adult library (PLAN phase 9 task 1).
//
// *Client implements core.AdultMetadataProvider, so everything above it — the
// site-as-series mapping, the refresh job, the discover screens — talks to an
// interface and tests without a network. The client itself is deliberately thin,
// mirroring internal/tmdb: no caching, no rate limiter, one retry. Provider
// responses are cached in sqlite by the library layer, which is where
// "rebuildable cache" lives (SPEC §1.2).
//
// "stash-box" is a protocol, not a service. TPDB is the default endpoint and
// StashDB, FansDB and a self-hosted box are the same code with a different URL
// and key, which is why the endpoint is a setting and there is not one file per
// dialect. Field selections here are deliberately narrow for the same reason: a
// field this client does not ask for is a field a thinner dialect cannot fail
// on.
package stashbox

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/watzon/caravan/internal/core"
)

const (
	// DefaultEndpoint is the TPDB preset: the endpoint a user who has entered
	// nothing but an API key talks to. It is a preset rather than a hardcoded
	// destination — StashDB et al. are config values, not new code (PLAN phase
	// 9 task 1).
	DefaultEndpoint = "https://theporndb.net/graphql"

	// APIKeyHeader is stash-box's own credential header. It is exported because
	// the fake endpoint in stashboxtest and this client have to agree on it.
	APIKeyHeader = "ApiKey"

	// defaultTimeout bounds a single request when the caller supplies no client
	// of its own. A metadata lookup that hangs must not wedge a refresh.
	defaultTimeout = 15 * time.Second
	// defaultPerPage is the page size used when a caller names none. It matches
	// stash-box's own default.
	defaultPerPage = 25
	// maxPerPage is stash-box's server-side page cap. Asking for more is an
	// error on some endpoints and silently truncated on others, so it is
	// clamped here where the behaviour is at least the same everywhere.
	maxPerPage = 100
	// fallbackRetryAfter is how long to wait on a 429 that carries no usable
	// Retry-After header.
	fallbackRetryAfter = time.Second
	// maxRetryAfter caps a hostile or mistaken Retry-After. Waiting minutes
	// inside a refresh is worse than surfacing the error.
	maxRetryAfter = 30 * time.Second
	// maxResponseBody bounds how much of a response is read. A GraphQL reply is
	// a single JSON document with no streaming, so a body past this size is a
	// malfunctioning endpoint rather than a large page.
	maxResponseBody = 8 << 20
)

// GraphQL extension codes that map onto the sentinel errors. Endpoints differ
// on which they send and some send none at all, so the HTTP status is checked
// too; see APIError.Unwrap.
const (
	codeUnauthenticated = "UNAUTHENTICATED"
	codeForbidden       = "FORBIDDEN"
	codeNotFound        = "NOT_FOUND"
	codeRateLimited     = "RATE_LIMITED"
	codeTooManyRequests = "TOO_MANY_REQUESTS"
)

// Errors returned for the conditions callers act on. They are matched with
// errors.Is; use errors.As with *APIError for the endpoint's own code and
// message.
var (
	// ErrUnauthorized means the API key is missing, wrong, or suspended.
	ErrUnauthorized = errors.New("stashbox: unauthorized")
	// ErrNotFound means the endpoint has no record with that id.
	ErrNotFound = errors.New("stashbox: not found")
	// ErrRateLimited means the endpoint throttled the request and the one retry
	// did not clear it.
	ErrRateLimited = errors.New("stashbox: rate limited")
)

// APIError is a failed stash-box operation. StatusCode is the HTTP status;
// Code and Message are the first GraphQL error's `extensions.code` and
// `message`, empty when the reply carried no errors array.
//
// GraphQL reports most failures as HTTP 200 with an errors array, but auth and
// throttling usually arrive as an HTTP status instead. Both funnel into this one
// type so callers only ever branch with errors.Is.
type APIError struct {
	// Operation is the GraphQL operation name that failed ("FindScene"). It is
	// the stash-box equivalent of the request path in a REST error, and it is
	// safe to log: unlike a URL, it can carry no credential.
	Operation  string
	StatusCode int
	Code       string
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("stashbox: %s: %s", e.Operation, e.Message)
}

// Unwrap maps the endpoint's answer onto the sentinel errors so callers can
// branch with errors.Is without knowing about APIError. The GraphQL extension
// code wins over the HTTP status: an endpoint that says UNAUTHENTICATED in a
// 200 body means it, and an endpoint that says nothing still has its status.
func (e *APIError) Unwrap() error {
	switch e.Code {
	case codeUnauthenticated, codeForbidden:
		return ErrUnauthorized
	case codeNotFound:
		return ErrNotFound
	case codeRateLimited, codeTooManyRequests:
		return ErrRateLimited
	}
	switch e.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusTooManyRequests:
		return ErrRateLimited
	}
	return nil
}

// Client is a stash-box GraphQL client.
type Client struct {
	// APIKey is sent as the ApiKey header (and as a bearer token; see
	// authorize). Empty sends neither, which is what an endpoint that allows
	// anonymous reads wants.
	APIKey string
	// Endpoint is the full GraphQL URL, including its path. Tests point it at
	// an httptest server.
	Endpoint string

	hc *http.Client
	// sleep is the delay used between a throttled request and its retry. It is
	// a field so tests can observe the wait without taking it.
	sleep func(ctx context.Context, d time.Duration) error

	// restSites is the TPDB REST site index for this endpoint, or "" for an
	// endpoint that has none — which is every stash-box but TPDB's. It is a
	// field rather than a check at the call site so the dialect is decided once,
	// at construction, from the one thing that decides it: the endpoint. See
	// tpdb.go for what it is and why it is allowed to exist.
	restSites string

	// restScenes is the TPDB REST scene index, set exactly when restSites is:
	// TPDB's queryScenes is a stub (scenes is always null and count merely
	// echoes per_page), so scene listing there has no GraphQL road at all. See
	// tpdb.go.
	restScenes string

	// siteIDs caches TPDB's numeric site id per stash-box uuid. The REST scene
	// index filters by the numeric id, a site's catalogue walk pages the same
	// site many times, and the mapping never changes — one lookup per site per
	// process is the right number.
	siteIDMu sync.Mutex
	siteIDs  map[string]int

	// noQueryStudios records that this endpoint has no usable queryStudios, so
	// SearchSites stops asking. TPDB answers that query with a bare HTTP 500 —
	// one doomed request per keystroke otherwise. It is a per-Client memo rather
	// than a stored setting on purpose: it is a fact about the endpoint that a
	// restart is free to re-check, and an endpoint that gains the query back
	// should not need a config edit to be believed.
	noQueryStudios atomic.Bool
}

// Compile-time proof that the client satisfies the seam the adult library
// layer depends on.
var _ core.AdultMetadataProvider = (*Client)(nil)

// New returns a client for endpoint using apiKey. A blank endpoint falls back
// to DefaultEndpoint, so "TPDB with a key" is the zero configuration. A nil hc
// gets a client with a default timeout; pass your own to control transport,
// proxy, or timeout.
func New(apiKey, endpoint string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	endpoint = strings.TrimSpace(endpoint)
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	c := &Client{
		APIKey:    apiKey,
		Endpoint:  endpoint,
		hc:        hc,
		sleep:     sleepCtx,
		restSites: tpdbSitesURLFor(endpoint),
		// Always allocated, even off-TPDB where nothing writes it: tests
		// assemble TPDB-shaped clients by setting the rest* fields after New,
		// and a nil map is a panic waiting for exactly that.
		siteIDs: make(map[string]int),
	}
	if c.restSites != "" {
		c.restScenes = tpdbScenesURL
	}
	return c
}

// gqlRequest is the standard GraphQL-over-HTTP request body.
type gqlRequest struct {
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables,omitempty"`
	OperationName string         `json:"operationName,omitempty"`
}

// gqlError is one entry of a GraphQL errors array.
type gqlError struct {
	Message    string `json:"message"`
	Extensions struct {
		Code string `json:"code"`
	} `json:"extensions"`
}

// gqlResponse is the GraphQL envelope. Data is kept raw so a reply carrying
// both data and errors is judged before it is decoded.
type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

// query executes doc as the named operation and decodes the `data` object into
// out.
//
// op is passed separately from doc so errors name something short and stable,
// and so the fake endpoint in stashboxtest can route on the same name a real
// one logs.
func (c *Client) query(ctx context.Context, op, doc string, vars map[string]any, out any) error {
	payload, err := json.Marshal(gqlRequest{Query: doc, Variables: vars, OperationName: op})
	if err != nil {
		return fmt.Errorf("stashbox: encode %s: %w", op, err)
	}

	resp, err := c.do(ctx, op, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("stashbox: read %s: %w", op, err)
	}

	var envelope gqlResponse
	decodeErr := json.Unmarshal(raw, &envelope)

	// A non-2xx is an error whether or not its body was JSON: a proxy in front
	// of the endpoint answers 502 with HTML, and that must not read as "decode
	// failed" when the real story is "the endpoint is down".
	if resp.StatusCode/100 != 2 {
		return newAPIError(op, resp.StatusCode, envelope.Errors)
	}
	if decodeErr != nil {
		return fmt.Errorf("stashbox: decode %s: %w", op, decodeErr)
	}
	if len(envelope.Errors) > 0 {
		return newAPIError(op, resp.StatusCode, envelope.Errors)
	}
	// A 200 with neither data nor errors is a broken endpoint, not an empty
	// result: an empty result is `{"data":{"findScene":null}}`, which decodes
	// fine and is the caller's ErrNotFound to raise.
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("stashbox: %s: response carried no data", op)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("stashbox: decode %s: %w", op, err)
	}
	return nil
}

// do POSTs payload, retrying a 429 exactly once after honoring Retry-After. It
// returns the response with its body open at any status: GraphQL puts the
// useful part of a failure in the body, so query reads it either way.
func (c *Client) do(ctx context.Context, op string, payload []byte) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("stashbox: request %s: %w", op, err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")
		c.authorize(req)

		resp, err := c.hc.Do(req)
		if err != nil {
			// *url.Error stringifies the URL. The key travels in a header
			// rather than the query string here, but unwrapping keeps the
			// message about the failure rather than the address, and keeps the
			// habit uniform with internal/tmdb.
			var uerr *url.Error
			if errors.As(err, &uerr) {
				err = uerr.Err
			}
			return nil, fmt.Errorf("stashbox: post %s: %w", op, err)
		}

		// Endpoints throttle hard but briefly. One retry turns a burst into a
		// pause rather than a failed refresh; a second 429 is a real problem
		// and belongs to the caller.
		if resp.StatusCode != http.StatusTooManyRequests || attempt > 0 {
			return resp, nil
		}
		retryAfter := resp.Header.Get("Retry-After")
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
		resp.Body.Close()
		if err := c.sleep(ctx, parseRetryAfter(retryAfter)); err != nil {
			return nil, err
		}
	}
}

// authorize attaches the credential.
//
// stash-box reads its own ApiKey header; TPDB, which speaks the same protocol,
// reads a bearer Authorization. Sending both is what lets one client serve every
// endpoint in the preset list without a per-dialect branch — the PLAN phase 9
// rule that endpoint quirks live in config, not code. Both go to the same host
// the request was already addressed to, so this widens no exposure.
//
// An empty key sends neither header: some endpoints allow anonymous reads and
// reject a blank credential outright.
func (c *Client) authorize(req *http.Request) {
	if c.APIKey == "" {
		return
	}
	req.Header.Set(APIKeyHeader, c.APIKey)
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
}

// newAPIError builds an APIError from a failed operation, taking the first
// GraphQL error as the reportable one. Later errors in the array are additional
// detail on the same failure and would only make the message unreadable.
func newAPIError(op string, status int, errs []gqlError) *APIError {
	e := &APIError{Operation: op, StatusCode: status}
	if len(errs) > 0 {
		e.Message = errs[0].Message
		e.Code = errs[0].Extensions.Code
	}
	if e.Message == "" {
		e.Message = http.StatusText(status)
	}
	if e.Message == "" {
		e.Message = "unknown error"
	}
	return e
}

// parseRetryAfter reads a Retry-After header sent as whole seconds. Anything
// unparseable falls back to a short wait rather than failing outright.
func parseRetryAfter(h string) time.Duration {
	secs, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil || secs < 0 {
		return fallbackRetryAfter
	}
	if d := time.Duration(secs) * time.Second; d < maxRetryAfter {
		return d
	}
	return maxRetryAfter
}

// sleepCtx waits for d, or returns early if the context is cancelled.
func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Date layouts stash-box serves, most precise first. Full dates are the norm,
// but scene and site records are community-edited and a partial date is a
// legitimate "we know the year, not the day" rather than bad data.
var dateLayouts = []string{"2006-01-02", "2006-01", "2006"}

// parseDate reads a stash-box date. Missing and malformed dates both yield the
// zero time: one sloppy record must not fail a whole page of scenes.
//
// A partial date is widened to the first of the period, which is the same
// convention "released in 2019" gets everywhere else in Caravan — and, because
// the season a scene lands in is its year, a year-only date still files the
// scene under the right season.
func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			return t
		}
	}
	return time.Time{}
}

// urlResult is a stash-box URL object. Only `url` is selected: the companion
// field is named `type` on older boxes and `site` on newer ones, and asking for
// either breaks the other.
type urlResult struct {
	URL string `json:"url"`
}

// imageResult is a stash-box Image object.
type imageResult struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// firstURL returns the first non-empty URL, or "" when there are none.
// stash-box lists the canonical page first.
func firstURL(urls []urlResult) string {
	for _, u := range urls {
		if u.URL != "" {
			return u.URL
		}
	}
	return ""
}

// coverURL picks the cover from a stash-box image list: the widest image, which
// is the full-size art rather than one of the thumbnails alongside it. Ties keep
// the first, so the choice is stable across calls for a record whose images have
// not changed — a cover that shuffles between refreshes would re-download art
// forever.
//
// An empty result means "no artwork", the same way a blank PosterURL does.
func coverURL(images []imageResult) string {
	best, bestWidth := "", -1
	for _, img := range images {
		if img.URL == "" {
			continue
		}
		if img.Width > bestWidth {
			best, bestWidth = img.URL, img.Width
		}
	}
	return best
}
