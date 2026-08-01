// Package indexer is Caravan's Torznab/Newznab client, the search side of the
// grab flow (SPEC §5.1, §9).
//
// Torznab and Newznab are the same XML dialect over the same query interface —
// Torznab is Newznab plus torrent attributes — so one client covers both and
// core.IndexerConfig.Type decides the dialect-specific details (which protocol
// results default to, which namespace the extra attributes arrive in).
//
// The client is deliberately thin: no caching, no rate limiting, no retries.
// Indexers are third-party and frequently sloppy, so the parsing side is the
// opposite of thin: a junk item is skipped, never fatal, and never a panic.
package indexer

import (
	"bytes"
	"context"
	"encoding/xml"
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
	// defaultTimeout bounds a single request when the caller supplies no
	// client of its own. Indexers are slower than metadata providers, and a
	// search fan-out is only as fast as its slowest member.
	defaultTimeout = 30 * time.Second
	// maxBody bounds how much of a response is read. A search page is tens of
	// kilobytes; anything past this is a misconfigured URL or a hostile
	// server, and reading it would be the whole problem.
	maxBody = 8 << 20
)

// Errors callers act on. They are matched with errors.Is; use errors.As with
// *APIError for the indexer's own error code and description.
var (
	// ErrUnauthorized means the API key is missing, wrong, or not accepted.
	// Torznab reports it as error code 100/101, usually over HTTP 200.
	ErrUnauthorized = errors.New("indexer: unauthorized")
	// ErrBadResponse means the indexer answered with something that is not a
	// Torznab/Newznab document.
	ErrBadResponse = errors.New("indexer: bad response")
)

// Torznab/Newznab error codes Caravan branches on: the 1xx band is the
// account/credential band. Everything else is surfaced as-is.
const (
	codeBadCredentials     = 100
	codeAccountSuspended   = 101
	codeInsufficientAccess = 102
)

// APIError is an indexer-reported failure: either an <error> document or a
// non-2xx status. Code and Description are the indexer's own fields, zero and
// empty when the response carried no error document.
type APIError struct {
	// Indexer is the configured indexer name, so a fan-out failure says which
	// source failed.
	Indexer string
	// StatusCode is the HTTP status, 0 when the failure was an <error>
	// document served with a 2xx.
	StatusCode int
	// Code is the Torznab/Newznab error code.
	Code int
	// Description is the indexer's error text.
	Description string
}

func (e *APIError) Error() string {
	switch {
	case e.Code != 0 && e.Description != "":
		return fmt.Sprintf("indexer %s: error %d: %s", e.Indexer, e.Code, e.Description)
	case e.Description != "":
		return fmt.Sprintf("indexer %s: %s", e.Indexer, e.Description)
	default:
		return fmt.Sprintf("indexer %s: http %d", e.Indexer, e.StatusCode)
	}
}

// Unwrap maps credential failures — however the indexer chose to report them —
// onto ErrUnauthorized so callers can branch without knowing the code table.
func (e *APIError) Unwrap() error {
	if e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden {
		return ErrUnauthorized
	}
	switch e.Code {
	case codeBadCredentials, codeAccountSuspended, codeInsufficientAccess:
		return ErrUnauthorized
	}
	return nil
}

// Client queries one configured indexer.
type Client struct {
	cfg core.IndexerConfig
	hc  *http.Client
}

// New returns a client for cfg. A nil hc gets a client with a default timeout;
// pass your own to share a transport across a search fan-out.
func New(cfg core.IndexerConfig, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{cfg: cfg, hc: hc}
}

// Config returns the indexer this client was built for.
func (c *Client) Config() core.IndexerConfig { return c.cfg }

// Search runs a plain keyword search (t=search) against cats, falling back to
// the indexer's configured categories when cats is empty.
func (c *Client) Search(ctx context.Context, q string, cats []int) ([]core.Release, error) {
	return c.search(ctx, "search", q, cats, nil)
}

// SearchMovie runs a movie search (t=movie). Indexers that do not implement it
// answer with an error document, which surfaces as *APIError; the caller can
// fall back to Search.
func (c *Client) SearchMovie(ctx context.Context, q string, cats []int) ([]core.Release, error) {
	return c.search(ctx, "movie", q, cats, nil)
}

// SearchTV runs a TV search (t=tvsearch). season and episode are sent only
// when positive, so a whole-season search is episode 0 and a whole-series
// search is both zero.
func (c *Client) SearchTV(ctx context.Context, q string, season, episode int, cats []int) ([]core.Release, error) {
	extra := url.Values{}
	if season > 0 {
		extra.Set("season", strconv.Itoa(season))
	}
	if episode > 0 {
		extra.Set("ep", strconv.Itoa(episode))
	}
	return c.search(ctx, "tvsearch", q, cats, extra)
}

// Test checks that the indexer answers its capabilities endpoint (t=caps) with
// a usable document. It is what the "test" button in indexer settings calls,
// so it must fail loudly on bad credentials rather than reporting a healthy
// indexer that returns nothing.
func (c *Client) Test(ctx context.Context) error {
	body, err := c.fetch(ctx, url.Values{"t": {"caps"}})
	if err != nil {
		return err
	}

	var caps capsDoc
	if err := decodeDoc(body, "caps", &caps); err != nil {
		return fmt.Errorf("indexer %s: %w: caps: %v", c.cfg.Name, ErrBadResponse, err)
	}
	// A caps document with no search modes at all is not an indexer Caravan
	// can use, whatever else it claims to be.
	if len(caps.Searching.Modes) == 0 {
		return fmt.Errorf("indexer %s: %w: caps advertises no search modes", c.cfg.Name, ErrBadResponse)
	}
	return nil
}

// search issues one query and converts the feed into releases.
func (c *Client) search(ctx context.Context, mode, q string, cats []int, extra url.Values) ([]core.Release, error) {
	params := url.Values{"t": {mode}}
	if q = strings.TrimSpace(q); q != "" {
		params.Set("q", q)
	}
	if len(cats) == 0 {
		cats = c.cfg.Categories
	}
	if s := joinCategories(cats); s != "" {
		params.Set("cat", s)
	}
	// Newznab hides the seeders/size/usenetdate attributes behind extended=1;
	// Torznab returns them regardless and ignores the parameter.
	params.Set("extended", "1")
	for k, vs := range extra {
		params[k] = vs
	}

	body, err := c.fetch(ctx, params)
	if err != nil {
		return nil, err
	}

	var feed feedDoc
	if err := decodeDoc(body, "rss", &feed); err != nil {
		return nil, fmt.Errorf("indexer %s: %w: %v", c.cfg.Name, ErrBadResponse, err)
	}
	return c.releases(feed.Channel.Items), nil
}

// fetch performs the request and returns the response body, having already
// turned an <error> document or a non-2xx status into an *APIError.
func (c *Client) fetch(ctx context.Context, params url.Values) ([]byte, error) {
	if c.cfg.APIKey != "" {
		params.Set("apikey", c.cfg.APIKey)
	}
	u := apiURL(c.cfg.URL) + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, fmt.Errorf("indexer %s: request: %w", c.cfg.Name, err)
	}
	req.Header.Set("Accept", "application/xml, text/xml")

	resp, err := c.hc.Do(req)
	if err != nil {
		// *url.Error stringifies the URL, apikey included. Unwrap to the
		// transport error so the key stays out of logs (SPEC §12).
		var uerr *url.Error
		if errors.As(err, &uerr) {
			err = uerr.Err
		}
		return nil, fmt.Errorf("indexer %s: get: %w", c.cfg.Name, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("indexer %s: read body: %w", c.cfg.Name, err)
	}

	// Indexers report credential and parameter failures as an <error>
	// document, usually with HTTP 200, so the body is checked before the
	// status.
	if apiErr := c.errorDoc(body); apiErr != nil {
		return nil, apiErr
	}
	if resp.StatusCode/100 != 2 {
		return nil, &APIError{Indexer: c.cfg.Name, StatusCode: resp.StatusCode, Description: http.StatusText(resp.StatusCode)}
	}
	return body, nil
}

// errorDoc returns an *APIError when body is an <error> document, and nil for
// anything else — including unparseable bytes, which are the caller's problem
// to report with the context it has.
func (c *Client) errorDoc(body []byte) *APIError {
	var doc errorDoc
	if err := decodeDoc(body, "error", &doc); err != nil {
		return nil
	}
	return &APIError{Indexer: c.cfg.Name, Code: doc.Code, Description: doc.Description}
}

// decodeDoc decodes body into out, requiring the document's root element to be
// named root. The root check is what catches the common indexer failure of
// answering a search with a login page, a maintenance notice, or a bare error
// string: encoding/xml skips unknown elements, so without it an HTML page
// decodes into an empty feed and reads as "no results".
func decodeDoc(body []byte, root string, out any) error {
	dec := xml.NewDecoder(bytes.NewReader(body))
	for {
		tok, err := dec.Token()
		if err != nil {
			return fmt.Errorf("no <%s> element: %w", root, err)
		}
		start, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if start.Name.Local != root {
			return fmt.Errorf("root element is <%s>, want <%s>", start.Name.Local, root)
		}
		return dec.DecodeElement(out, &start)
	}
}

// apiURL turns a configured base URL into the api endpoint. Users paste both
// the base ("https://x/") and the endpoint ("https://x/api"), and both are
// reasonable readings of the field, so both are accepted.
func apiURL(base string) string {
	u := strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(u, "/api") {
		return u
	}
	return u + "/api"
}

// joinCategories renders category ids as the comma-separated `cat` parameter,
// dropping non-positive ids rather than sending "0" and getting nothing back.
func joinCategories(cats []int) string {
	var b strings.Builder
	for _, c := range cats {
		if c <= 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(c))
	}
	return b.String()
}
