// Package tvmaze is Caravan's client for the TVmaze API, a metadata provider
// for television libraries.
//
// *Client implements core.MetadataProvider, so everything above it — the
// scanner, the organizer, the add-series screen — talks to an interface and
// tests without a network. The client itself is deliberately thin, mirroring
// internal/tmdb and internal/anilist: no caching, one retry. TVmaze responses
// are cached in sqlite by the library layer, which is where "rebuildable cache"
// lives (SPEC §1.2).
//
// Two things shape this client, and they are the same two that shape
// internal/anilist.
//
// There is no credential. TVmaze's read API is public and takes none, so
// nothing here authenticates and nothing here can be "unauthorized" in the
// sense core.ErrMetadataUnauthorized means — that sentinel drives the "enter
// your API key" UI, and offering it for a provider with no key to enter would
// send people to fix something that does not exist. A 401 from this endpoint
// means TVmaze is refusing everyone, which is an outage. A rejected request is
// a plain APIError; TestNoUnauthorizedSentinel pins that down so a later
// credential-UI sweep cannot quietly wire this provider into it.
//
// There is a throttle. TVmaze publishes a budget of at least 20 requests per 10
// seconds and documents that leaving 500ms between calls never trips it. One
// logical lookup is not one request here either — GetSeries costs two — so a
// refresh sweep over a few hundred series would find the limit the expensive
// way without a floor between sends. See defaultMinInterval.
package tvmaze

import (
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
	// ProviderID is the value `libraries.provider` and a chain element store
	// for a TVmaze-backed library. It is declared here, next to the client that
	// answers to it, and core.ProviderTVmaze is set from the same literal: a
	// constant in two places that disagree is worse than a literal in one.
	ProviderID = "tvmaze"

	// DefaultBaseURL is TVmaze's API root. Like AniList and unlike stash-box,
	// TVmaze is a service rather than a protocol — there is one host and no
	// dialects — so this is a default rather than a preset, overridden only by
	// tests.
	DefaultBaseURL = "https://api.tvmaze.com"
)

const (
	// defaultTimeout bounds a single request when the caller supplies no client
	// of its own. A metadata lookup that hangs must not wedge a scan.
	defaultTimeout = 15 * time.Second

	// defaultMinInterval is the floor between two sends. TVmaze's documented
	// budget is at least 20 requests per 10 seconds, and its own guidance is
	// that 500ms of spacing never fails — so this is the published answer
	// rather than a guess tuned against the limit. It is what keeps a refresh
	// sweep, two requests per series, inside the budget without any
	// coordination above this client.
	defaultMinInterval = 500 * time.Millisecond

	// fallbackRetryAfter is how long to wait on a 429 that carries no usable
	// Retry-After header.
	fallbackRetryAfter = time.Second
	// maxRetryAfter caps a hostile or mistaken Retry-After: waiting minutes
	// inside a scan is worse than surfacing the error and letting the next
	// sweep retry.
	maxRetryAfter = 60 * time.Second

	// maxResponseBody bounds how much of a response is read. The largest thing
	// TVmaze serves here is one show's whole episode list — a long-runner is
	// under a megabyte — so a body past this size is a malfunctioning endpoint
	// rather than a large series.
	maxResponseBody = 8 << 20
	// maxErrorBody bounds how much of an error response is read before giving
	// up on decoding it.
	maxErrorBody = 4 << 10
)

// Errors returned for the conditions callers act on. They are matched with
// errors.Is; use errors.As with *APIError for TVmaze's own status and message.
//
// There is deliberately no ErrUnauthorized: see the package comment.
var (
	// ErrNotFound means TVmaze has no show with that id.
	ErrNotFound = errors.New("tvmaze: not found")
	// ErrRateLimited means TVmaze throttled the request and the one retry did
	// not clear it.
	ErrRateLimited = errors.New("tvmaze: rate limited")
	// ErrInvalidRef means the ref handed to Get* is not a TVmaze id. It is
	// deliberately NOT ErrNotFound: asking TVmaze for a stash-box UUID is a
	// wiring mistake in Caravan, not a title TVmaze happens to be missing, and
	// it must read like one rather than park a file as "unmatched".
	ErrInvalidRef = errors.New("tvmaze: ref is not a tvmaze id")
)

// parseRef converts a provider-native ref into the numeric id TVmaze's URLs
// need. It is the one place this package turns the seam's string vocabulary
// back into its own, and it answers before any request goes out: a foreign ref
// is a bug to report, not a lookup to spend a rate-limit token on.
func parseRef(ref string) (int, error) {
	id, err := strconv.Atoi(ref)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidRef, ref)
	}
	return id, nil
}

// APIError is a non-2xx response from TVmaze. StatusCode is the HTTP status;
// Message is TVmaze's own `message` or `name` body field, empty when the body
// was not decodable.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("tvmaze: http %d: %s", e.StatusCode, e.Message)
}

// Unwrap maps the HTTP status onto the sentinel errors so callers can branch
// with errors.Is without knowing about APIError.
//
// Neither 401 nor 403 maps anywhere. This client sends no credential, so a
// rejected request means the endpoint is refusing everyone — an outage, not a
// key to go and fix.
func (e *APIError) Unwrap() error {
	switch e.StatusCode {
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusTooManyRequests:
		return ErrRateLimited
	}
	return nil
}

// Client is a TVmaze API client. The zero value is not usable; call New.
type Client struct {
	// BaseURL is the API root. Tests point it at an httptest server; there is
	// no other reason to change it.
	BaseURL string

	hc *http.Client
	// sleep is how every wait in this client is taken — the throttle floor and
	// the back-off after a 429. It is a field so tests can observe a wait
	// without taking it.
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
	// go out — a reservation, not a record of the past, so concurrent callers
	// queue behind each other instead of all waking at the same moment.
	lastSend time.Time
	// notBefore holds a refusal TVmaze asked us to wait out. Unlike AniList,
	// TVmaze publishes no rate-limit headers, so nothing here reads a window:
	// a 429's Retry-After is the only signal, and it is recorded as a gate on
	// the next send rather than slept on inline. That way the caller that was
	// refused and every other caller sharing this client back off together,
	// and the wait is taken exactly once — by reserve, like every other wait.
	notBefore time.Time
}

// Compile-time proof that the client satisfies the seam the library layer
// depends on.
var _ core.MetadataProvider = (*Client)(nil)

// New returns a client for TVmaze. A nil hc gets a client with a default
// timeout; pass your own to control transport, proxy, or timeout. There is no
// credential parameter because TVmaze's read API needs none.
func New(hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		BaseURL:     DefaultBaseURL,
		hc:          hc,
		sleep:       sleepCtx,
		minInterval: defaultMinInterval,
	}
}

// SearchMovies reports that TVmaze does not serve films. TVmaze catalogues
// television and nothing else; a chain walker skips this rung rather than
// failing on it (see core.ErrProviderKindUnsupported).
func (c *Client) SearchMovies(ctx context.Context, q string) ([]core.MovieMeta, error) {
	return nil, core.ErrProviderKindUnsupported
}

// GetMovie reports that TVmaze does not serve films; see SearchMovies.
func (c *Client) GetMovie(ctx context.Context, ref string) (*core.MovieMeta, error) {
	return nil, core.ErrProviderKindUnsupported
}

// get performs a GET against path and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	u := strings.TrimSuffix(c.BaseURL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	resp, err := c.do(ctx, u, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		return fmt.Errorf("tvmaze: decode %s: %w", path, err)
	}
	return nil
}

// do issues the request, waiting for this client's send slot first and retrying
// a 429 exactly once after honoring Retry-After. It returns a response with a
// 2xx status and an open body, or an error with the body already closed.
//
// path is passed separately for error messages so they name something short and
// stable rather than the whole URL.
func (c *Client) do(ctx context.Context, u, path string) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		if d := c.reserve(); d > 0 {
			if err := c.sleep(ctx, d); err != nil {
				return nil, err
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("tvmaze: request %s: %w", path, err)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.hc.Do(req)
		if err != nil {
			// *url.Error stringifies the URL. Nothing secret travels in it
			// here, but unwrapping keeps the message about the failure rather
			// than the address, and keeps the habit uniform with internal/tmdb.
			var uerr *url.Error
			if errors.As(err, &uerr) {
				err = uerr.Err
			}
			return nil, fmt.Errorf("tvmaze: get %s: %w", path, err)
		}
		if resp.StatusCode/100 == 2 {
			return resp, nil
		}

		apiErr := readError(resp)
		retryAfter := resp.Header.Get("Retry-After")
		resp.Body.Close()

		// TVmaze throttles hard but briefly. One retry turns a burst into a
		// pause rather than a failed scan; a second 429 is a real problem and
		// belongs to the caller.
		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			c.backOff(parseRetryAfter(retryAfter))
			continue
		}
		return nil, apiErr
	}
}

// reserve claims the next send slot and returns how long the caller must wait
// before using it: the later of the minInterval floor and any back-off TVmaze
// asked for. The slot is claimed under the lock and waited for outside it, so N
// concurrent callers get N spaced slots rather than N simultaneous sends after
// one shared wait.
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

// backOff gates the next send on a refusal TVmaze just made. It is a gate
// rather than a sleep taken here so that every caller sharing this client
// honors the refusal, not only the one that happened to be told about it.
func (c *Client) backOff(d time.Duration) {
	until := time.Now().Add(d)

	c.mu.Lock()
	defer c.mu.Unlock()
	if until.After(c.notBefore) {
		c.notBefore = until
	}
}

// readError builds an APIError from a non-2xx response. It does not close the
// body.
func readError(resp *http.Response) *APIError {
	e := &APIError{StatusCode: resp.StatusCode}

	// TVmaze's error documents carry both a short `name` ("Not Found") and a
	// `message` that is often empty, so the more specific one wins and the
	// other is the fallback.
	var body struct {
		Name    string `json:"name"`
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxErrorBody)).Decode(&body); err == nil {
		e.Message = body.Message
		if e.Message == "" {
			e.Message = body.Name
		}
	}
	if e.Message == "" {
		e.Message = http.StatusText(resp.StatusCode)
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
