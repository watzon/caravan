// Package thetvdb is Caravan's client for TheTVDB v4, a metadata provider for
// television libraries.
//
// *Client implements core.MetadataProvider, so everything above it — the
// scanner, the organizer, the add-series screen — talks to an interface and
// tests without a network. The client itself is as thin as internal/tmdb and
// internal/tvmaze: no record caching, one retry. Responses are cached in sqlite
// by the library layer, which is where "rebuildable cache" lives (SPEC §1.2).
//
// It holds exactly one piece of state, and the two decisions about that state
// are the interesting part of this file.
//
// TheTVDB v4 does not accept a credential on an ordinary request. A key — and,
// for a user-supported subscription, a PIN — is posted to /login, which answers
// with a JWT good for about a month; every other request carries that JWT as a
// bearer token. So a client that logged in per lookup would spend a login on
// every search keystroke, which is why the token is held here and why
// cmd/caravan caches the client itself.
//
// The PIN travels only when there is one. A licensed key is REFUSED when the
// login body carries "pin": "", and a user-supported key is refused when the
// field is absent, so the body is assembled rather than marshalled from a
// struct: an `omitempty` tag is one edit away from breaking one of the two, and
// neither failure is visible until somebody with the other kind of key tries it.
//
// The token is refreshed by a 401 and never by a clock. Expiry is the only
// thing a timer could anticipate, and it is the least likely reason a token
// stops working: a revoked key, a rotated subscription and a skewed clock all
// present as a 401, and none of them moves the expiry a timer would be watching.
// So nothing here tracks expiry. A 401 invalidates the token that earned it,
// one re-login is attempted, and a second 401 is the credential's own answer.
package thetvdb

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
	// ProviderID is the value `libraries.provider` and a chain element store
	// for a TheTVDB-backed library. It is declared here, next to the client that
	// answers to it, and core.ProviderTheTVDB is set from the same literal: a
	// constant in two places that disagree is worse than a literal in one.
	ProviderID = "thetvdb"

	// DefaultBaseURL is TheTVDB's v4 API root. Like TMDB and unlike stash-box,
	// TheTVDB is a service rather than a protocol — one host, no dialects — so
	// this is a default rather than a preset, overridden only by tests.
	DefaultBaseURL = "https://api4.thetvdb.com/v4"

	// loginPath is where the key and PIN are exchanged for a bearer token. It is
	// a constant so the string a test stub routes on and the string this client
	// sends are the same expression.
	loginPath = "/login"
)

const (
	// defaultTimeout bounds a single request when the caller supplies no client
	// of its own. A metadata lookup that hangs must not wedge a scan.
	defaultTimeout = 15 * time.Second

	// fallbackRetryAfter is how long to wait on a 429 that carries no usable
	// Retry-After header.
	fallbackRetryAfter = time.Second
	// maxRetryAfter caps a hostile or mistaken Retry-After: waiting minutes
	// inside a scan is worse than surfacing the error and letting the next
	// sweep retry.
	maxRetryAfter = 60 * time.Second

	// maxResponseBody bounds how much of a response is read. The largest thing
	// TheTVDB serves here is one page of episodes, so a body past this size is a
	// malfunctioning endpoint rather than a long-running series.
	maxResponseBody = 8 << 20
	// maxErrorBody bounds how much of an error response is read before giving
	// up on decoding it.
	maxErrorBody = 4 << 10
)

// Errors returned for the conditions callers act on. They are matched with
// errors.Is; use errors.As with *APIError for TheTVDB's own status and message.
var (
	// ErrUnauthorized means the API key — or the key and PIN together — was
	// refused, either at login or by a request whose token the server no longer
	// honors. It wraps core.ErrMetadataUnauthorized so a caller that only wants
	// to know "the credential was rejected" — the credential-health model in
	// internal/api — never has to import this package.
	ErrUnauthorized = fmt.Errorf("thetvdb: unauthorized: %w", core.ErrMetadataUnauthorized)
	// ErrNotFound means TheTVDB has no record with that id.
	ErrNotFound = errors.New("thetvdb: not found")
	// ErrRateLimited means TheTVDB throttled the request and the one retry did
	// not clear it.
	ErrRateLimited = errors.New("thetvdb: rate limited")
	// ErrInvalidRef means the ref handed to Get* is not a TheTVDB id. It is
	// deliberately NOT ErrNotFound: asking TheTVDB for a stash-box UUID is a
	// wiring mistake in Caravan, not a title TheTVDB happens to be missing, and
	// it must read like one rather than park a file as "unmatched".
	ErrInvalidRef = errors.New("thetvdb: ref is not a thetvdb id")
)

// parseRef converts a provider-native ref into the numeric id TheTVDB's URLs
// need. It is the one place this package turns the seam's string vocabulary
// back into its own, and it answers before any request goes out: a foreign ref
// is a bug to report, not a lookup to spend a login and a round trip on.
func parseRef(ref string) (int64, error) {
	id, err := strconv.ParseInt(ref, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("%w: %q", ErrInvalidRef, ref)
	}
	return id, nil
}

// APIError is a non-2xx response from TheTVDB. StatusCode is the HTTP status;
// Message is TheTVDB's own `message` body field, empty when the body was not
// decodable.
type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("thetvdb: http %d: %s", e.StatusCode, e.Message)
}

// Unwrap maps the HTTP status onto the sentinel errors so callers can branch
// with errors.Is without knowing about APIError.
func (e *APIError) Unwrap() error {
	switch e.StatusCode {
	case http.StatusUnauthorized:
		return ErrUnauthorized
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusTooManyRequests:
		return ErrRateLimited
	}
	return nil
}

// Client is a TheTVDB v4 API client. The zero value is not usable; call New.
type Client struct {
	// APIKey is the subscription key posted to /login.
	APIKey string
	// PIN is the subscriber PIN a user-supported key needs. Empty for a
	// licensed key, and empty means the field is omitted entirely; see the
	// package comment.
	PIN string
	// BaseURL is the API root. Tests point it at an httptest server; there is
	// no other reason to change it.
	BaseURL string

	hc *http.Client
	// sleep is how the back-off after a 429 is taken. It is a field so tests can
	// observe a wait without taking it.
	sleep func(ctx context.Context, d time.Duration) error

	// tokenMu guards token. The login itself happens under this lock, which is
	// what collapses a burst of concurrent callers — or a burst of concurrent
	// 401s — into a single login: everyone else waits and then finds the token
	// the first one obtained.
	tokenMu sync.Mutex
	// token is the bearer token in force, empty when there is none yet or the
	// last one was refused.
	token string
}

// Compile-time proof that the client satisfies the seam the library layer
// depends on.
var _ core.MetadataProvider = (*Client)(nil)

// New returns a client for TheTVDB using apiKey, and pin when the subscription
// is user-supported. A nil hc gets a client with a default timeout; pass your
// own to control transport, proxy, or timeout.
func New(apiKey, pin string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		APIKey:  apiKey,
		PIN:     pin,
		BaseURL: DefaultBaseURL,
		hc:      hc,
		sleep:   sleepCtx,
	}
}

// Test proves the credential against TheTVDB, mirroring the indexer and
// download client "Test" buttons.
//
// A login is the cheapest authenticated exchange TheTVDB has, and it is the
// exact question the Test button asks: the key and the PIN are what /login
// consumes, and everything else this client does is a bearer token away from
// them. Nothing in the reply is used — a search would answer the same question
// with an unbounded body and a dependency on whatever the query happened to
// match.
//
// The token it obtains is deliberately dropped rather than cached: the caller
// of a Test is proving a candidate credential, which may not be the one this
// client is otherwise built from.
func (c *Client) Test(ctx context.Context) error {
	_, err := c.login(ctx)
	return err
}

// SearchMovies reports that TheTVDB does not serve films here. TheTVDB has a
// movie catalogue, but its movie record carries no typed release list, and
// core.MovieMeta.DigitalRelease is what gates minimum availability — see the
// descriptor comment in internal/core/provider.go. A chain walker skips this
// rung rather than failing on it (see core.ErrProviderKindUnsupported).
func (c *Client) SearchMovies(ctx context.Context, q string) ([]core.MovieMeta, error) {
	return nil, core.ErrProviderKindUnsupported
}

// GetMovie reports that TheTVDB does not serve films here; see SearchMovies.
func (c *Client) GetMovie(ctx context.Context, ref string) (*core.MovieMeta, error) {
	return nil, core.ErrProviderKindUnsupported
}

// login exchanges the key — and the PIN, when there is one — for a bearer
// token.
//
// The body is a map rather than a struct because the PIN's presence is the
// whole point: a licensed key is refused when "pin" arrives empty, and a
// user-supported key is refused when it is missing, so the field has to be
// absent rather than zero. See the package comment.
func (c *Client) login(ctx context.Context) (string, error) {
	body := map[string]string{"apikey": c.APIKey}
	if c.PIN != "" {
		body["pin"] = c.PIN
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("thetvdb: encode %s: %w", loginPath, err)
	}

	u := strings.TrimSuffix(c.BaseURL, "/") + loginPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(encoded))
	if err != nil {
		return "", fmt.Errorf("thetvdb: request %s: %w", loginPath, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		// *url.Error stringifies the URL. Nothing secret travels in it here —
		// the credential is in the body — but unwrapping keeps the message about
		// the failure rather than the address, and keeps the habit uniform with
		// internal/tmdb.
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return "", fmt.Errorf("thetvdb: post %s: %w", loginPath, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		return "", readError(resp)
	}

	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxErrorBody)).Decode(&out); err != nil {
		return "", fmt.Errorf("thetvdb: decode %s: %w", loginPath, err)
	}
	if out.Data.Token == "" {
		// A 2xx with no token is an endpoint that changed shape. Reporting it
		// here beats sending "Bearer " on every request afterwards and reading
		// the resulting 401s as a rejected credential.
		return "", fmt.Errorf("thetvdb: %s returned no token", loginPath)
	}
	return out.Data.Token, nil
}

// authToken returns the bearer token in force, logging in when there is none.
//
// The login runs under the lock deliberately. A burst of callers arriving at an
// empty token — the first search after a restart, or every goroutine that just
// had its token refused — would otherwise each log in, and a subscription's
// login budget is not a place to spend N calls to learn one answer.
func (c *Client) authToken(ctx context.Context) (string, error) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.token != "" {
		return c.token, nil
	}
	token, err := c.login(ctx)
	if err != nil {
		return "", err
	}
	c.token = token
	return token, nil
}

// invalidateToken drops the cached token, but only when it is still the one the
// caller's refused request carried.
//
// The comparison is the point. N concurrent requests can be refused with the
// same token; the first of them clears it and the next authToken logs in, and
// without this check a straggler arriving after that would clear the FRESH
// token and send everyone back to /login. Compare-and-clear turns a burst of
// 401s into exactly one re-login.
func (c *Client) invalidateToken(used string) {
	c.tokenMu.Lock()
	defer c.tokenMu.Unlock()

	if c.token == used {
		c.token = ""
	}
}

// get performs a GET against path and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	resp, err := c.do(ctx, path, q)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(out); err != nil {
		return fmt.Errorf("thetvdb: decode %s: %w", path, err)
	}
	return nil
}

// do issues the request with the bearer token attached, re-logging in once on a
// 401 and backing off once on a 429. It returns a response with a 2xx status
// and an open body, or an error with the body already closed.
//
// The two retries are counted separately: a request that was throttled and then
// found an expired token has met two unrelated conditions, and folding them into
// one attempt counter would make whichever came second unrecoverable.
func (c *Client) do(ctx context.Context, path string, q url.Values) (*http.Response, error) {
	u := strings.TrimSuffix(c.BaseURL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}

	var reloggedIn, backedOff bool
	for {
		token, err := c.authToken(ctx)
		if err != nil {
			return nil, err
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("thetvdb: request %s: %w", path, err)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		resp, err := c.hc.Do(req)
		if err != nil {
			// See login for why the *url.Error is unwrapped.
			var uerr *url.Error
			if errors.As(err, &uerr) {
				err = uerr.Err
			}
			return nil, fmt.Errorf("thetvdb: get %s: %w", path, err)
		}
		if resp.StatusCode/100 == 2 {
			return resp, nil
		}

		apiErr := readError(resp)
		retryAfter := resp.Header.Get("Retry-After")
		resp.Body.Close()

		switch {
		case resp.StatusCode == http.StatusUnauthorized && !reloggedIn:
			// The token this attempt used is the one that was refused, and only
			// that one is dropped; see invalidateToken. A second 401 is the
			// credential's answer rather than the token's, and falls through to
			// ErrUnauthorized.
			reloggedIn = true
			c.invalidateToken(token)
		case resp.StatusCode == http.StatusTooManyRequests && !backedOff:
			// One retry turns a burst into a pause rather than a failed scan; a
			// second 429 is a real problem and belongs to the caller.
			backedOff = true
			if err := c.sleep(ctx, parseRetryAfter(retryAfter)); err != nil {
				return nil, err
			}
		default:
			return nil, apiErr
		}
	}
}

// readError builds an APIError from a non-2xx response. It does not close the
// body.
func readError(resp *http.Response) *APIError {
	e := &APIError{StatusCode: resp.StatusCode}

	var body struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxErrorBody)).Decode(&body); err == nil {
		e.Message = body.Message
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
