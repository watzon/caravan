// Package anilist is Caravan's client for the AniList GraphQL API, the metadata
// provider behind anime libraries.
//
// *Client implements core.MetadataProvider, so everything above it, the
// scanner, the organizer, the add-series screen, talks to an interface and
// tests without a network. The client itself is deliberately thin, mirroring
// internal/tmdb and internal/stashbox: no caching, one retry. AniList responses
// are cached in sqlite by the library layer, which is where "rebuildable cache"
// lives (SPEC §1.2).
//
// Two things make this client differ from its siblings.
//
// There is no credential. AniList serves anonymous reads, so nothing here
// authenticates and nothing here can be "unauthorized" in the sense
// core.ErrMetadataUnauthorized means: that sentinel drives the "enter your API
// key" UI, and offering it for a provider with no key to enter would send
// people to fix something that does not exist. A rejected request is a plain
// APIError.
//
// There is a throttle. AniList's budget is per-minute and small (roughly 30
// requests/minute while degraded, 90 normally) and, unlike TMDB, one logical
// lookup is not one request: GetSeries for a long-running show pages the airing
// schedule, so a single series can cost several. A refresh sweep over a few
// hundred anime would trip the limit on the first minute without a floor
// between sends, so this client keeps one: see minInterval.
package anilist

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
	"time"

	"github.com/watzon/caravan/internal/core"
)

const (
	// ProviderID is the value `libraries.provider` stores for an AniList-backed
	// library. It is declared here, next to the client that answers to it,
	// rather than taken from core: registering the provider in core's compiled-in
	// list is a later phase, and a constant in two places that disagree is worse
	// than a literal in one.
	ProviderID = "anilist"

	// DefaultEndpoint is AniList's GraphQL endpoint. Unlike stash-box, AniList
	// is a service rather than a protocol, there is one endpoint and no
	// dialects, so this is a default rather than a preset, overridden only by
	// tests.
	DefaultEndpoint = "https://graphql.anilist.co"
)

const (
	// defaultTimeout bounds a single request when the caller supplies no client
	// of its own. A metadata lookup that hangs must not wedge a scan.
	defaultTimeout = 15 * time.Second

	// defaultPerPage is the search page size. A match picker shows a shortlist;
	// asking for more only makes AniList do more work per keystroke.
	defaultPerPage = 25

	// defaultMinInterval is the floor between two sends. 750ms is ~80
	// requests/minute: under AniList's healthy budget with room for the
	// occasional burst, and comfortably under the degraded one once the
	// pre-emptive wait below has kicked in. It is what keeps a refresh sweep,
	// several requests per series, inside the budget without any coordination
	// above this client.
	defaultMinInterval = 750 * time.Millisecond

	// lowRemaining is the X-RateLimit-Remaining value at which the client stops
	// spending and waits for the window to reset. It is 1 rather than 0 because
	// the last request of a window is the one that gets the 429: leaving a token
	// unspent costs one request and saves a failed one.
	lowRemaining = 1

	// fallbackRetryAfter is how long to wait on a 429 that carries no usable
	// Retry-After header.
	fallbackRetryAfter = time.Second
	// maxRetryAfter caps a hostile or mistaken Retry-After, and maxRateLimitWait
	// caps the pre-emptive wait for the same reason: waiting minutes inside a
	// scan is worse than surfacing the error and letting the next sweep retry.
	maxRetryAfter    = 60 * time.Second
	maxRateLimitWait = 60 * time.Second

	// maxResponseBody bounds how much of a response is read. A GraphQL reply is
	// a single JSON document with no streaming, so a body past this size is a
	// malfunctioning endpoint rather than a large page.
	maxResponseBody = 8 << 20
)

// AniList's rate-limit headers. They are on every response, including the 429.
const (
	headerRateRemaining = "X-RateLimit-Remaining"
	headerRateReset     = "X-RateLimit-Reset"
)

// Errors returned for the conditions callers act on. They are matched with
// errors.Is; use errors.As with *APIError for AniList's own status and message.
//
// There is deliberately no ErrUnauthorized: see the package comment.
var (
	// ErrNotFound means AniList has no anime with that id.
	ErrNotFound = errors.New("anilist: not found")
	// ErrRateLimited means AniList throttled the request and the one retry did
	// not clear it.
	ErrRateLimited = errors.New("anilist: rate limited")
	// ErrInvalidRef means the ref handed to Get* is not an AniList id. It is
	// deliberately NOT ErrNotFound: asking AniList for a stash-box UUID is a
	// wiring mistake in Caravan, not a title AniList happens to be missing, and
	// it must read like one rather than park a file as "unmatched".
	ErrInvalidRef = errors.New("anilist: ref is not an anilist id")
)

// parseRef converts a provider-native ref into the numeric id AniList's queries
// need. It is the one place this package turns the seam's string vocabulary back
// into its own, and it answers before any request goes out: a foreign ref is a
// bug to report, not a lookup to spend a rate-limit token on.
func parseRef(ref string) (int, error) {
	id, err := strconv.Atoi(ref)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidRef, ref)
	}
	return id, nil
}

// APIError is a failed AniList operation. StatusCode is the HTTP status; Status
// and Message are the first GraphQL error's `status` and `message`, zero and
// empty when the reply carried no errors array.
//
// GraphQL reports failures as an errors array, and AniList also mirrors the
// condition onto the HTTP status. Both funnel into this one type so callers only
// ever branch with errors.Is.
type APIError struct {
	// Operation is the GraphQL operation name that failed ("GetSeries"). It is
	// the AniList equivalent of the request path in a REST error.
	Operation  string
	StatusCode int
	Status     int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("anilist: %s: %s", e.Operation, e.Message)
}

// Unwrap maps AniList's answer onto the sentinel errors so callers can branch
// with errors.Is without knowing about APIError. The GraphQL status wins over
// the HTTP one: AniList answers some failures 200 with the real story in the
// errors array, and a reply that says nothing still has its status.
//
// Neither 401 nor 403 maps anywhere. This client sends no credential, so a
// rejected request means the endpoint is refusing everyone: an outage, not a
// key to go and fix.
func (e *APIError) Unwrap() error {
	switch e.Status {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusTooManyRequests:
		return ErrRateLimited
	}
	switch e.StatusCode {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusTooManyRequests:
		return ErrRateLimited
	}
	return nil
}

// Client is an AniList GraphQL client. The zero value is not usable; call New.
type Client struct {
	// Endpoint is the full GraphQL URL. Tests point it at an httptest server;
	// there is no other reason to change it.
	Endpoint string

	hc *http.Client
	// sleep is how every wait in this client is taken: the throttle floor, the
	// pre-emptive rate-limit wait, and the retry after a 429. It is a field so
	// tests can observe a wait without taking it.
	sleep func(ctx context.Context, d time.Duration) error

	// minInterval is the floor between two sends; see defaultMinInterval. It is
	// a field so tests can zero it.
	minInterval time.Duration

	// mu guards the send schedule below. The throttle is per-Client rather than
	// per-call because the budget it protects is per-client-IP: two concurrent
	// refresh jobs sharing this Client have to share its pacing too, or the
	// floor means nothing.
	mu sync.Mutex
	// lastSend is the instant the most recently reserved request is allowed to
	// go out: a reservation, not a record of the past, so concurrent callers
	// queue behind each other instead of all waking at the same moment.
	lastSend time.Time
	// notBefore holds the window reset AniList asked us to wait for. It is a
	// gate on the NEXT send rather than a sleep taken when the header arrives:
	// the point of waiting is to protect the request after this one, and a
	// lookup that already has its answer must not sit on it for a minute.
	notBefore time.Time
}

// Compile-time proof that the client satisfies the seam the library layer
// depends on.
var _ core.MetadataProvider = (*Client)(nil)

// New returns a client for AniList. A nil hc gets a client with a default
// timeout; pass your own to control transport, proxy, or timeout. There is no
// credential parameter because AniList's read API needs none.
func New(hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		Endpoint:    DefaultEndpoint,
		hc:          hc,
		sleep:       sleepCtx,
		minInterval: defaultMinInterval,
	}
}

// gqlRequest is the standard GraphQL-over-HTTP request body.
type gqlRequest struct {
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables,omitempty"`
	OperationName string         `json:"operationName,omitempty"`
}

// gqlError is one entry of a GraphQL errors array. AniList carries the HTTP-like
// condition in `status` rather than the `extensions.code` other GraphQL servers
// use.
type gqlError struct {
	Message string `json:"message"`
	Status  int    `json:"status"`
}

// gqlResponse is the GraphQL envelope. Data is kept raw so a reply carrying both
// data and errors is judged before it is decoded.
type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []gqlError      `json:"errors"`
}

// query executes doc as the named operation and decodes the `data` object into
// out.
//
// op is passed separately from doc so errors name something short and stable,
// and so a test stub can route on the same name a real endpoint logs.
func (c *Client) query(ctx context.Context, op, doc string, vars map[string]any, out any) error {
	payload, err := json.Marshal(gqlRequest{Query: doc, Variables: vars, OperationName: op})
	if err != nil {
		return fmt.Errorf("anilist: encode %s: %w", op, err)
	}

	resp, err := c.do(ctx, op, payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("anilist: read %s: %w", op, err)
	}

	var envelope gqlResponse
	decodeErr := json.Unmarshal(raw, &envelope)

	// A non-2xx is an error whether or not its body was JSON: a proxy in front
	// of AniList answers 502 with HTML, and that must not read as "decode
	// failed" when the real story is "the endpoint is down".
	if resp.StatusCode/100 != 2 {
		return newAPIError(op, resp.StatusCode, envelope.Errors)
	}
	if decodeErr != nil {
		return fmt.Errorf("anilist: decode %s: %w", op, decodeErr)
	}
	if len(envelope.Errors) > 0 {
		return newAPIError(op, resp.StatusCode, envelope.Errors)
	}
	// A 200 with neither data nor errors is a broken endpoint, not an empty
	// result: an empty result is `{"data":{"Media":null}}`, which decodes fine
	// and is the caller's ErrNotFound to raise.
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("anilist: %s: response carried no data", op)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("anilist: decode %s: %w", op, err)
	}
	return nil
}

// do POSTs payload, waiting for this client's send slot first and retrying a 429
// exactly once after honoring Retry-After. It returns the response with its body
// open at any status: GraphQL puts the useful part of a failure in the body, so
// query reads it either way.
func (c *Client) do(ctx context.Context, op string, payload []byte) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		if d := c.reserve(); d > 0 {
			if err := c.sleep(ctx, d); err != nil {
				return nil, err
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.Endpoint, bytes.NewReader(payload))
		if err != nil {
			return nil, fmt.Errorf("anilist: request %s: %w", op, err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := c.hc.Do(req)
		if err != nil {
			// *url.Error stringifies the URL. Nothing secret travels in it here,
			// but unwrapping keeps the message about the failure rather than the
			// address, and keeps the habit uniform with internal/tmdb.
			var uerr *url.Error
			if errors.As(err, &uerr) {
				err = uerr.Err
			}
			return nil, fmt.Errorf("anilist: post %s: %w", op, err)
		}

		// AniList throttles hard but briefly. One retry turns a burst into a
		// pause rather than a failed scan; a second 429 is a real problem and
		// belongs to the caller.
		if resp.StatusCode != http.StatusTooManyRequests || attempt > 0 {
			c.observeRateLimit(resp.Header)
			return resp, nil
		}
		// The 429's own headers are deliberately not observed: Retry-After is
		// the endpoint's answer for this exact case, and stacking the window
		// gate on top of it would wait twice for one refusal.
		retryAfter := resp.Header.Get("Retry-After")
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBody))
		resp.Body.Close()
		if err := c.sleep(ctx, parseRetryAfter(retryAfter)); err != nil {
			return nil, err
		}
	}
}

// reserve claims the next send slot and returns how long the caller must wait
// before using it: the later of the minInterval floor and any window reset
// AniList asked for. The slot is claimed under the lock and waited for outside
// it, so N concurrent callers get N spaced slots rather than N simultaneous
// sends after one shared wait.
func (c *Client) reserve() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now()
	earliest := c.lastSend.Add(c.minInterval)
	if c.notBefore.After(earliest) {
		earliest = c.notBefore
	}
	if earliest.Before(now) {
		earliest = now
	}
	c.lastSend = earliest
	return earliest.Sub(now)
}

// observeRateLimit reads the rate-limit headers off a response and, when the
// window is nearly spent, gates the next send until it resets.
//
// This is what keeps a long refresh from discovering the limit the expensive
// way. Anything missing or unparseable is ignored: the minInterval floor is the
// guarantee, and this is the refinement on top of it.
func (c *Client) observeRateLimit(h http.Header) {
	remaining, err := strconv.Atoi(strings.TrimSpace(h.Get(headerRateRemaining)))
	if err != nil || remaining > lowRemaining {
		return
	}
	resetUnix, err := strconv.ParseInt(strings.TrimSpace(h.Get(headerRateReset)), 10, 64)
	if err != nil {
		return
	}

	now := time.Now()
	until := time.Unix(resetUnix, 0)
	if !until.After(now) {
		return
	}
	// A reset far in the future is a clock skew or a mistake. Cap it for the
	// same reason Retry-After is capped.
	if limit := now.Add(maxRateLimitWait); until.After(limit) {
		until = limit
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if until.After(c.notBefore) {
		c.notBefore = until
	}
}

// newAPIError builds an APIError from a failed operation, taking the first
// GraphQL error as the reportable one. Later errors in the array are additional
// detail on the same failure and would only make the message unreadable.
func newAPIError(op string, status int, errs []gqlError) *APIError {
	e := &APIError{Operation: op, StatusCode: status}
	if len(errs) > 0 {
		e.Message = errs[0].Message
		e.Status = errs[0].Status
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
