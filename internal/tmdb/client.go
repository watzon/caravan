// Package tmdb is Caravan's client for The Movie Database v3 API, the
// metadata provider behind library matching (SPEC §4).
//
// *Client implements core.MetadataProvider, so everything above it — the
// scanner, the organizer, the add-movie screen — talks to an interface and
// tests without a network. The client itself is deliberately thin: no caching,
// no rate limiter, one retry. TMDB responses are cached in sqlite by the
// library layer, which is where "rebuildable cache" lives (SPEC §1.2).
package tmdb

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
	"time"

	"github.com/watzon/caravan/internal/core"
)

const (
	// DefaultBaseURL is the TMDB v3 API root.
	DefaultBaseURL = "https://api.themoviedb.org/3"
	// DefaultImageBaseURL is the CDN prefix for poster paths. w500 is the
	// size Caravan stores: large enough for the detail screen, small enough
	// that a full library of posters is not a download in its own right.
	DefaultImageBaseURL = "https://image.tmdb.org/t/p/w500"
	// DefaultBackdropBaseURL is the CDN prefix for backdrop paths. Backdrops
	// are only ever shown full-bleed behind a discover screen, so they get a
	// wider size than posters do.
	DefaultBackdropBaseURL = "https://image.tmdb.org/t/p/w780"

	// defaultTimeout bounds a single request when the caller supplies no
	// client of its own. A metadata lookup that hangs must not wedge a scan.
	defaultTimeout = 15 * time.Second
	// fallbackRetryAfter is how long to wait on a 429 that carries no usable
	// Retry-After header.
	fallbackRetryAfter = time.Second
	// maxRetryAfter caps a hostile or mistaken Retry-After. Waiting minutes
	// inside a scan is worse than surfacing the error.
	maxRetryAfter = 30 * time.Second
	// maxErrorBody bounds how much of an error response is read before
	// giving up on decoding it.
	maxErrorBody = 4 << 10
)

// Errors returned for the HTTP statuses callers act on. They are matched with
// errors.Is; use errors.As with *APIError for TMDB's own status code and
// message.
var (
	// ErrUnauthorized means the API key is missing, wrong, or suspended.
	ErrUnauthorized = errors.New("tmdb: unauthorized")
	// ErrNotFound means TMDB has no record with that id.
	ErrNotFound = errors.New("tmdb: not found")
	// ErrRateLimited means TMDB throttled the request and the one retry did
	// not clear it.
	ErrRateLimited = errors.New("tmdb: rate limited")
)

// APIError is a non-2xx response from TMDB. StatusCode is the HTTP status;
// Code and Message are TMDB's own `status_code`/`status_message` body fields,
// zero and empty when the body was not decodable.
type APIError struct {
	StatusCode int
	Code       int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("tmdb: http %d: %s", e.StatusCode, e.Message)
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

// Client is a TMDB v3 API client.
type Client struct {
	// APIKey is sent as the api_key query parameter (TMDB v3 style).
	APIKey string
	// BaseURL is the API root. Tests point it at an httptest server.
	BaseURL string
	// ImageBaseURL is the prefix poster paths are joined onto.
	ImageBaseURL string
	// BackdropBaseURL is the prefix backdrop paths are joined onto.
	BackdropBaseURL string

	hc *http.Client
	// sleep is the delay used between a throttled request and its retry.
	// It is a field so tests can observe the wait without taking it.
	sleep func(ctx context.Context, d time.Duration) error
}

// Compile-time proof that the client satisfies the seam the library layer
// depends on.
var _ core.MetadataProvider = (*Client)(nil)

// New returns a client using apiKey. A nil hc gets a client with a default
// timeout; pass your own to control transport, proxy, or timeout.
func New(apiKey string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		APIKey:          apiKey,
		BaseURL:         DefaultBaseURL,
		ImageBaseURL:    DefaultImageBaseURL,
		BackdropBaseURL: DefaultBackdropBaseURL,
		hc:              hc,
		sleep:           sleepCtx,
	}
}

// get performs a GET against path and decodes the JSON body into out.
func (c *Client) get(ctx context.Context, path string, q url.Values, out any) error {
	if q == nil {
		q = url.Values{}
	}
	q.Set("api_key", c.APIKey)
	u := strings.TrimSuffix(c.BaseURL, "/") + path + "?" + q.Encode()

	resp, err := c.do(ctx, u, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("tmdb: decode %s: %w", path, err)
	}
	return nil
}

// do issues the request, retrying a 429 exactly once after honoring
// Retry-After. It returns a response with a 2xx status and an open body, or an
// error with the body already closed.
//
// path is passed separately for error messages: u carries the API key and must
// never reach a log line.
func (c *Client) do(ctx context.Context, u, path string) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, fmt.Errorf("tmdb: request %s: %w", path, err)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.hc.Do(req)
		if err != nil {
			// *url.Error stringifies the URL, api key included. Unwrap to
			// the transport error so the key stays out of the message.
			var uerr *url.Error
			if errors.As(err, &uerr) {
				err = uerr.Err
			}
			return nil, fmt.Errorf("tmdb: get %s: %w", path, err)
		}
		if resp.StatusCode/100 == 2 {
			return resp, nil
		}

		apiErr := readError(resp)
		retryAfter := resp.Header.Get("Retry-After")
		resp.Body.Close()

		// TMDB throttles hard but briefly. One retry turns a burst into a
		// pause rather than a failed scan; a second 429 is a real problem
		// and belongs to the caller.
		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			if err := c.sleep(ctx, parseRetryAfter(retryAfter)); err != nil {
				return nil, err
			}
			continue
		}
		return nil, apiErr
	}
}

// readError builds an APIError from a non-2xx response. It does not close the
// body.
func readError(resp *http.Response) *APIError {
	e := &APIError{StatusCode: resp.StatusCode}

	var body struct {
		StatusCode    int    `json:"status_code"`
		StatusMessage string `json:"status_message"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxErrorBody)).Decode(&body); err == nil {
		e.Code = body.StatusCode
		e.Message = body.StatusMessage
	}
	if e.Message == "" {
		e.Message = http.StatusText(resp.StatusCode)
	}
	return e
}

// parseRetryAfter reads TMDB's Retry-After header, which it sends as whole
// seconds. Anything unparseable falls back to a short wait rather than failing
// outright.
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

// posterURL turns a TMDB poster path into an absolute URL. An empty path
// stays empty: core.MovieMeta.PosterURL being blank is how "no poster" is
// expressed.
func (c *Client) posterURL(path string) string {
	return imageURL(c.ImageBaseURL, path)
}

// PosterURL is posterURL exported for core.DiscoverProvider: a request row
// stores the provider's poster path, and the API needs the client that knows
// the CDN prefix to render it back into a URL.
func (c *Client) PosterURL(path string) string {
	return c.posterURL(path)
}

// backdropURL is posterURL for the wider artwork the discover screens use.
func (c *Client) backdropURL(path string) string {
	return imageURL(c.BackdropBaseURL, path)
}

func imageURL(base, path string) string {
	if path == "" {
		return ""
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(path, "/")
}

// dateLayout is TMDB's date format for release, air, and first-air dates.
const dateLayout = "2006-01-02"

// parseDate reads a TMDB date. Missing and malformed dates both yield the zero
// time: an unaired episode or a sloppy record must not fail a whole lookup.
func parseDate(s string) time.Time {
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(dateLayout, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// yearOf returns the year of t, or 0 when t is unset.
func yearOf(t time.Time) int {
	if t.IsZero() {
		return 0
	}
	return t.Year()
}
