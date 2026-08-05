// Package qbittorrent talks to a qBittorrent WebUI (API v2) and adapts it to
// core.Engine, so a user who already runs qBittorrent can keep it and let
// Caravan drive it (SPEC §5.1, PLAN phase 6).
//
// Two things about this backend differ from the embedded engine, and both are
// deliberate:
//
//   - It keeps no state. The embedded engine owns its torrents, so it needs a
//     Persistence callback to remember them across a restart. qBittorrent
//     remembers its own queue; asking it is always cheaper and always more
//     truthful than mirroring it, so every method here is one or two HTTP
//     calls and the engine is safe to build and throw away. The `downloads`
//     table stays what it has always been for external clients: a cache the
//     import watcher refreshes each poll.
//   - Its paths are foreign. qBittorrent writes wherever its own configuration
//     says, which is an absolute path outside Caravan's storage root. That is
//     the one legitimate absolute path in download state (see
//     docs/download-clients.md); translating it for the library is the import
//     track's job, not this package's.
//
// Like internal/download and internal/clients, this package does not import
// internal/store.
package qbittorrent

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

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
)

const (
	// apiPath prefixes every WebUI API v2 endpoint.
	apiPath = "/api/v2"
	// sessionCookie is the name qBittorrent gives its session cookie.
	sessionCookie = "SID"
	// loginOK is the body qBittorrent answers a successful login with. A
	// *rejected* login is also HTTP 200 — with the body "Fails." — so the
	// status alone cannot be trusted here.
	loginOK = "Ok."
	// addFailed is the body /torrents/add answers with when it added nothing —
	// a malformed magnet, an unreachable .torrent URL, a duplicate — on
	// qBittorrent 5.0 and older, which report that as HTTP 200. Newer servers
	// answer the same case with 409 and a JSON body on success, so this is
	// matched as the one known failure sentinel rather than by requiring a
	// known success one: a success body this package has never seen must keep
	// counting as success.
	addFailed = "Fails."
	// defaultTimeout bounds a single call. qBittorrent is usually on the same
	// host, but it can be behind a slow reverse proxy, and torrents/info on a
	// large queue is not instant.
	defaultTimeout = 30 * time.Second
	// maxBody bounds how much of a response is read. torrents/info on a very
	// large queue is a few megabytes; past this is a misconfigured URL
	// pointing at something that is not qBittorrent.
	maxBody = 32 << 20
)

// Errors callers act on.
var (
	// ErrUnauthorized means qBittorrent rejected the configured login. It
	// never carries the credential that was rejected (SPEC §12).
	ErrUnauthorized = errors.New("qbittorrent: username or password rejected")
	// ErrNotFound means qBittorrent does not know the requested info hash.
	ErrNotFound = errors.New("qbittorrent: download not found")
)

// APIError is a non-2xx answer from the WebUI. The path is included because it
// says which call failed; the query string and credentials never are.
type APIError struct {
	// Path is the API endpoint, without the base URL.
	Path string
	// Status is the HTTP status code.
	Status int
	// Body is qBittorrent's own (short) complaint, empty when it sent none.
	Body string
}

func (e *APIError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("qbittorrent: %s: http %d: %s", e.Path, e.Status, e.Body)
	}
	return fmt.Sprintf("qbittorrent: %s: http %d", e.Path, e.Status)
}

// Client is a thin qBittorrent WebUI API v2 client. It is safe for concurrent
// use: the only shared state is the session, which is guarded.
type Client struct {
	// base is the configured URL with any trailing slash removed, so it can be
	// concatenated with an endpoint path. It may carry a reverse-proxy prefix.
	base string
	user string
	pass string
	hc   *http.Client

	mu sync.Mutex
	// sid is the session cookie value. It is empty both before the first
	// login and when qBittorrent bypasses authentication for our address —
	// authed distinguishes the two.
	authed bool
	sid    string
	// legacyActions records that this server predates WebAPI 2.11, where
	// torrents/stop and torrents/start were still called pause and resume.
	// It is discovered on first use rather than derived from the reported
	// version, because the version string is advisory and a 404 is not.
	legacyActions bool
}

// New returns a client for cfg. A nil hc gets one with a default timeout; pass
// your own to share a transport.
func New(cfg core.DownloadClientConfig, hc *http.Client) (*Client, error) {
	t, ok := clients.Lookup(core.DownloadClientQBittorrent)
	if !ok {
		// Unreachable: the type table is a compile-time constant.
		return nil, errors.New("qbittorrent: client type is not registered in internal/clients")
	}
	if err := t.Validate(cfg); err != nil {
		return nil, fmt.Errorf("qbittorrent: %w", err)
	}
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		base: strings.TrimRight(strings.TrimSpace(cfg.URL), "/"),
		user: cfg.Username,
		pass: cfg.Password,
		hc:   hc,
	}, nil
}

// WebAPIVersion returns the WebUI API version string, e.g. "2.11.3". It is the
// cheapest authenticated call there is, which makes it the connection probe.
func (c *Client) WebAPIVersion(ctx context.Context) (string, error) {
	body, err := c.get(ctx, "/app/webapiVersion", nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(body)), nil
}

// AddRequest is one POST /torrents/add.
//
// SavePath is deliberately absent: where qBittorrent writes is qBittorrent's
// configuration, and overriding it from here would put Caravan in charge of a
// filesystem it cannot see.
type AddRequest struct {
	// URL is a magnet link or an http(s) .torrent URL.
	URL string
	// Category is the qBittorrent category to file the torrent under, empty
	// for the client's own default.
	Category string
	// Tags are the qBittorrent tags to mark the torrent with.
	Tags []string
	// Paused adds the torrent stopped, so qBittorrent registers it and
	// connects to nothing until it is resumed.
	Paused bool
}

// Add hands qBittorrent a torrent by URL or magnet link.
//
// qBittorrent answers 200 for "queued" rather than "added": the reply says
// nothing about the resulting torrent, which is why the engine has to look the
// info hash up afterwards when the release did not carry one.
//
// Servers up to 5.0 also answer 200 for "added nothing" — a malformed magnet,
// an unreachable .torrent URL, a duplicate — with the body "Fails.", so on
// those the status alone cannot be trusted. Reading it alone would record the
// grab as succeeded and write a `downloads` row for a handle qBittorrent never
// accepted: a queue row that never progresses, never imports, and never
// retries, because a grab that is not failed is not retried.
func (c *Client) Add(ctx context.Context, req AddRequest) error {
	form := url.Values{}
	// urls is newline-separated; we only ever send one.
	form.Set("urls", req.URL)
	if req.Category != "" {
		form.Set("category", req.Category)
	}
	if len(req.Tags) > 0 {
		form.Set("tags", strings.Join(req.Tags, ","))
	}
	if req.Paused {
		// "paused" is the long-standing WebAPI spelling. qBittorrent 5 renamed
		// it to "stopped" and still accepts this one, and both are sent so a
		// cap is honoured on either version rather than silently ignored on
		// one of them — an unknown form field is discarded, not an error.
		form.Set("paused", "true")
		form.Set("stopped", "true")
	}
	body, err := c.post(ctx, "/torrents/add", form)
	if err != nil {
		return err
	}
	if strings.HasPrefix(strings.TrimSpace(string(body)), addFailed) {
		return &APIError{Path: "/torrents/add", Status: http.StatusOK, Body: clients.Snippet(body)}
	}
	return nil
}

// InfoQuery filters GET /torrents/info.
type InfoQuery struct {
	// Category limits the answer to one category; empty does not filter.
	Category string
	// Tag limits the answer to one tag; empty does not filter. Servers older
	// than WebAPI 2.8.3 ignore it, so callers that rely on it must re-check
	// the returned Tags field.
	Tag string
	// Hashes limits the answer to specific info hashes.
	Hashes []string
}

// Info returns the torrents matching q.
func (c *Client) Info(ctx context.Context, q InfoQuery) ([]Torrent, error) {
	params := url.Values{}
	if q.Category != "" {
		params.Set("category", q.Category)
	}
	if q.Tag != "" {
		params.Set("tag", q.Tag)
	}
	if len(q.Hashes) > 0 {
		params.Set("hashes", strings.Join(q.Hashes, "|"))
	}
	body, err := c.get(ctx, "/torrents/info", params)
	if err != nil {
		return nil, err
	}
	var out []Torrent
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("qbittorrent: /torrents/info: %w", err)
	}
	return out, nil
}

// Files returns the file list of one torrent, for locating the payload once a
// download finishes.
func (c *Client) Files(ctx context.Context, hash string) ([]File, error) {
	body, err := c.get(ctx, "/torrents/files", url.Values{"hash": {hash}})
	if err != nil {
		return nil, err
	}
	var out []File
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("qbittorrent: /torrents/files: %w", err)
	}
	return out, nil
}

// Stop pauses torrents. qBittorrent 5.0 (WebAPI 2.11) renamed this endpoint
// from pause to stop; older servers answer the new name with a 404 and are
// retried on the old one.
func (c *Client) Stop(ctx context.Context, hashes ...string) error {
	return c.action(ctx, "/torrents/stop", "/torrents/pause", hashes)
}

// Start resumes torrents. See Stop for the endpoint rename.
func (c *Client) Start(ctx context.Context, hashes ...string) error {
	return c.action(ctx, "/torrents/start", "/torrents/resume", hashes)
}

// Delete removes torrents, and their data when deleteFiles is set.
func (c *Client) Delete(ctx context.Context, deleteFiles bool, hashes ...string) error {
	form := url.Values{
		"hashes":      {strings.Join(hashes, "|")},
		"deleteFiles": {strconv.FormatBool(deleteFiles)},
	}
	_, err := c.post(ctx, "/torrents/delete", form)
	return err
}

// action posts to modern, falling back to legacy once the server has shown it
// does not know the modern name.
func (c *Client) action(ctx context.Context, modern, legacy string, hashes []string) error {
	form := url.Values{"hashes": {strings.Join(hashes, "|")}}

	c.mu.Lock()
	useLegacy := c.legacyActions
	c.mu.Unlock()
	if useLegacy {
		_, err := c.post(ctx, legacy, form)
		return err
	}

	_, err := c.post(ctx, modern, form)
	if !renamedEndpoint(err) {
		return err
	}
	c.mu.Lock()
	c.legacyActions = true
	c.mu.Unlock()
	_, err = c.post(ctx, legacy, form)
	return err
}

// renamedEndpoint reports whether err is the answer a server gives for an
// endpoint it does not carry, as opposed to a call that failed.
func renamedEndpoint(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.Status == http.StatusNotFound || apiErr.Status == http.StatusMethodNotAllowed
}

// Close forgets the session. The next call logs in again; nothing is flushed,
// because qBittorrent holds all the state there is.
func (c *Client) Close() error {
	c.forget()
	return nil
}

func (c *Client) get(ctx context.Context, path string, params url.Values) ([]byte, error) {
	return c.do(ctx, http.MethodGet, path, params)
}

func (c *Client) post(ctx context.Context, path string, form url.Values) ([]byte, error) {
	return c.do(ctx, http.MethodPost, path, form)
}

// do runs one API call, logging in first and re-logging in once if the session
// turned out to be gone.
//
// A dropped session is the normal case, not an edge case: qBittorrent expires
// idle sessions and forgets all of them when it restarts, and Caravan polls
// the queue for as long as it runs. Retrying exactly once is what makes that
// invisible without risking a loop against a server that always refuses.
func (c *Client) do(ctx context.Context, method, path string, params url.Values) ([]byte, error) {
	body, status, err := c.attempt(ctx, method, path, params)
	if err != nil {
		return nil, err
	}
	if status == http.StatusForbidden || status == http.StatusUnauthorized {
		c.forget()
		if body, status, err = c.attempt(ctx, method, path, params); err != nil {
			return nil, err
		}
	}
	if status != http.StatusOK {
		return nil, &APIError{Path: path, Status: status, Body: clients.Snippet(body)}
	}
	return body, nil
}

func (c *Client) attempt(ctx context.Context, method, path string, params url.Values) ([]byte, int, error) {
	sid, err := c.session(ctx)
	if err != nil {
		return nil, 0, err
	}

	// Reads go in the query string, writes in a form-encoded body: qBittorrent
	// parses either, but that is the split its own documentation describes and
	// the one a reverse proxy in front of it will expect.
	target := c.base + apiPath + path
	var form io.Reader
	if method == http.MethodGet {
		if len(params) > 0 {
			target += "?" + params.Encode()
		}
	} else {
		form = strings.NewReader(params.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, target, form)
	if err != nil {
		return nil, 0, fmt.Errorf("qbittorrent: %s: %w", path, err)
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	c.stamp(req, sid)

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("qbittorrent: %s: %w", path, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, 0, fmt.Errorf("qbittorrent: %s: %w", path, err)
	}
	return body, resp.StatusCode, nil
}

// stamp adds the session cookie and the Referer qBittorrent's CSRF check wants.
// Current versions are permissive about a missing Referer, but a reverse proxy
// or an older build may not be, and sending our own base URL costs nothing.
func (c *Client) stamp(req *http.Request, sid string) {
	req.Header.Set("Accept", "*/*")
	if c.base != "" {
		req.Header.Set("Referer", c.base)
	}
	if sid != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sid})
	}
}

// session returns the current session cookie, logging in when there is none.
//
// Two callers racing here log in twice, which qBittorrent allows and which
// costs one extra request; that is cheaper than holding a mutex across the
// network for every poll.
func (c *Client) session(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.authed {
		sid := c.sid
		c.mu.Unlock()
		return sid, nil
	}
	c.mu.Unlock()

	sid, err := c.login(ctx)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	c.sid, c.authed = sid, true
	c.mu.Unlock()
	return sid, nil
}

func (c *Client) forget() {
	c.mu.Lock()
	c.sid, c.authed = "", false
	c.mu.Unlock()
}

// login exchanges the configured credentials for a session cookie.
//
// An empty cookie with a successful body is not a failure: qBittorrent can be
// configured to bypass authentication for a client's address, and then it
// answers "Ok." and sets nothing. The caller sends no cookie and is let in.
func (c *Client) login(ctx context.Context) (string, error) {
	form := url.Values{"username": {c.user}, "password": {c.pass}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+apiPath+"/auth/login", strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("qbittorrent: login: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	c.stamp(req, "")

	resp, err := c.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("qbittorrent: login: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", fmt.Errorf("qbittorrent: login: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusForbidden:
		// qBittorrent bans an address after repeated failures, and says so
		// with a 403 rather than with the "Fails." body. Naming the ban is
		// the difference between "fix the password" and "wait it out".
		return "", fmt.Errorf("qbittorrent: login refused, the address may be banned after repeated failures: %w", ErrUnauthorized)
	case resp.StatusCode != http.StatusOK:
		return "", &APIError{Path: "/auth/login", Status: resp.StatusCode, Body: clients.Snippet(body)}
	case !strings.HasPrefix(strings.TrimSpace(string(body)), loginOK):
		return "", ErrUnauthorized
	}

	for _, ck := range resp.Cookies() {
		if ck.Name == sessionCookie {
			return ck.Value, nil
		}
	}
	return "", nil
}
