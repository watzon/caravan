// Package sabnzbd talks to SABnzbd's HTTP API and adapts it to core.Engine.
// It is one of the two backends that open the Usenet half of acquisition:
// Caravan has no built-in NZB downloader, so a Usenet release is only grabbable
// through a client configured here (SPEC §5.1, PLAN phase 6).
//
// It follows internal/clients/qbittorrent's shape — a thin wire client under a
// stateless core.Engine adapter — with three differences that are SABnzbd's
// rather than choices:
//
//   - A job lives in two lists. SABnzbd moves a job out of the queue and into
//     the history the moment its transfer ends, so "everything Caravan added"
//     is always queue plus history. Only the history knows where the payload
//     landed, in its `storage` field, which is what makes a completed download
//     importable at all.
//   - Numbers arrive as strings. Sizes are formatted with "%.2f" into JSON
//     strings, percentages into decimal strings, and which fields are strings
//     changes between versions — so every numeric field here decodes both.
//   - There is no seeding and no per-job rate. Usenet has no swarm, so a
//     download is never DownloadSeeding and Ratio is always zero; SABnzbd
//     reports one queue-wide speed, which is attributed to the job that is
//     actually transferring.
//
// Like internal/download and internal/clients, this package does not import
// internal/store.
package sabnzbd

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

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
)

const (
	// apiPath is appended to the configured base URL. SABnzbd serves its whole
	// API from this one endpoint, switched by the `mode` parameter.
	apiPath = "/api"
	// defaultTimeout bounds a single call.
	defaultTimeout = 30 * time.Second
	// maxBody bounds how much of a response is read. A history page is tens of
	// kilobytes; past this is a URL pointing at something that is not SABnzbd.
	maxBody = 32 << 20
	// defaultHistoryLimit is how far back a history query reaches when the
	// caller does not say.
	//
	// It has to be a number: `mode=history` with no limit falls back to
	// SABnzbd's own display default, which is small enough (ten rows) that a
	// finished download could be pushed out of sight before the import watcher
	// sees it. It also has to be bounded, because this is polled every few
	// seconds and a years-old history is not worth re-sending.
	defaultHistoryLimit = 100
)

// Errors callers act on.
var (
	// ErrUnauthorized means SABnzbd refused the configured API key. It never
	// carries the key that was refused (SPEC §12).
	ErrUnauthorized = errors.New("sabnzbd: API key rejected")
	// ErrNotFound means SABnzbd knows no job with the requested id, in either
	// its queue or its history.
	ErrNotFound = errors.New("sabnzbd: download not found")
)

// APIError is a non-2xx answer. The mode is included because it says which
// call failed; the query string, which carries the API key, never is.
type APIError struct {
	// Mode is the `mode` parameter of the failed call.
	Mode string
	// Status is the HTTP status code.
	Status int
	// Body is SABnzbd's own (short) complaint, empty when it sent none.
	Body string
}

func (e *APIError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("sabnzbd: %s: http %d: %s", e.Mode, e.Status, e.Body)
	}
	return fmt.Sprintf("sabnzbd: %s: http %d", e.Mode, e.Status)
}

// Client is a thin SABnzbd API client. It holds no session — SABnzbd
// authenticates every request with the API key — so it is safe for concurrent
// use and free to build and throw away.
type Client struct {
	// base is the configured URL with any trailing slash removed. It may carry
	// a reverse-proxy prefix.
	base string
	key  string
	hc   *http.Client
}

// New returns a client for cfg. A nil hc gets one with a default timeout; pass
// your own to share a transport.
func New(cfg core.DownloadClientConfig, hc *http.Client) (*Client, error) {
	t, ok := clients.Lookup(core.DownloadClientSABnzbd)
	if !ok {
		// Unreachable: the type table is a compile-time constant.
		return nil, errors.New("sabnzbd: client type is not registered in internal/clients")
	}
	if err := t.Validate(cfg); err != nil {
		return nil, fmt.Errorf("sabnzbd: %w", err)
	}
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		base: strings.TrimRight(strings.TrimSpace(cfg.URL), "/"),
		key:  strings.TrimSpace(cfg.APIKey),
		hc:   hc,
	}, nil
}

// Version returns SABnzbd's version string, e.g. "4.3.3".
func (c *Client) Version(ctx context.Context) (string, error) {
	body, err := c.call(ctx, url.Values{"mode": {"version"}})
	if err != nil {
		return "", err
	}
	var out struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("sabnzbd: version: %w", err)
	}
	return strings.TrimSpace(out.Version), nil
}

// Query narrows a queue or history read. A zero Query asks for everything the
// endpoint will give.
type Query struct {
	// Category limits the answer to one SABnzbd category; empty does not
	// filter.
	Category string
	// NZOIDs limits the answer to specific jobs.
	NZOIDs []string
	// Limit caps how many rows come back. It only matters for the history,
	// where zero means defaultHistoryLimit rather than "no limit".
	Limit int
}

func (q Query) apply(params url.Values) {
	if q.Category != "" {
		params.Set("cat", q.Category)
	}
	if len(q.NZOIDs) > 0 {
		params.Set("nzo_ids", strings.Join(q.NZOIDs, ","))
	}
	if q.Limit > 0 {
		params.Set("limit", strconv.Itoa(q.Limit))
	}
}

// Queue returns the jobs SABnzbd is still transferring.
func (c *Client) Queue(ctx context.Context, q Query) (*Queue, error) {
	params := url.Values{"mode": {"queue"}}
	q.apply(params)
	body, err := c.call(ctx, params)
	if err != nil {
		return nil, err
	}
	var out struct {
		Queue Queue `json:"queue"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("sabnzbd: queue: %w", err)
	}
	return &out.Queue, nil
}

// History returns the jobs whose transfer has ended: finished, failed, or
// still in post-processing. This is where a completed download's payload path
// comes from.
func (c *Client) History(ctx context.Context, q Query) ([]HistorySlot, error) {
	params := url.Values{"mode": {"history"}}
	if q.Limit <= 0 {
		q.Limit = defaultHistoryLimit
	}
	q.apply(params)
	body, err := c.call(ctx, params)
	if err != nil {
		return nil, err
	}
	var out struct {
		History struct {
			Slots []HistorySlot `json:"slots"`
		} `json:"history"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("sabnzbd: history: %w", err)
	}
	return out.History.Slots, nil
}

// AddRequest is one `mode=addurl`.
type AddRequest struct {
	// URL is the NZB link. SABnzbd fetches it itself.
	URL string
	// Name is the job name SABnzbd should display, empty to let it use
	// whatever the indexer's response is called.
	Name string
	// Category is the SABnzbd category to file the job under, empty for the
	// client's own default.
	Category string
	// Paused files the job at SABnzbd's paused priority, so it sits in the
	// queue without being fetched until something resumes it.
	Paused bool
}

// priorityPaused is SABnzbd's own value for "in the queue, not running". It is
// a priority rather than a flag because that is how SABnzbd models it: -2 is
// PAUSED, below LOW and above DUPLICATE.
const priorityPaused = "-2"

// AddURL hands SABnzbd an NZB link and returns the nzo_id it filed it under.
//
// SABnzbd fetches the link itself, in the background: the job appears
// immediately with status Grabbing and the *same* nzo_id it keeps once the NZB
// has been read, which is why the id is usable as a download handle from the
// moment this returns.
func (c *Client) AddURL(ctx context.Context, req AddRequest) (string, error) {
	params := url.Values{"mode": {"addurl"}, "name": {req.URL}}
	if req.Name != "" {
		params.Set("nzbname", req.Name)
	}
	if req.Category != "" {
		params.Set("cat", req.Category)
	}
	if req.Paused {
		params.Set("priority", priorityPaused)
	}
	body, err := c.call(ctx, params)
	if err != nil {
		return "", err
	}
	var out struct {
		Status bool     `json:"status"`
		NZOIDs []string `json:"nzo_ids"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("sabnzbd: addurl: %w", err)
	}
	if !out.Status || len(out.NZOIDs) == 0 {
		// SABnzbd reports a refused add as a plain false with no message. The
		// link is not repeated here: it carries the indexer's API key.
		return "", errors.New("sabnzbd: addurl: SABnzbd would not take the NZB link")
	}
	return out.NZOIDs[0], nil
}

// PauseJob pauses one queued job. SABnzbd answers 200 for an id it does not
// know, so this cannot report ErrNotFound without a second round trip.
func (c *Client) PauseJob(ctx context.Context, id string) error {
	_, err := c.call(ctx, url.Values{"mode": {"queue"}, "name": {"pause"}, "value": {id}})
	return err
}

// ResumeJob resumes one paused job. See the note on PauseJob.
func (c *Client) ResumeJob(ctx context.Context, id string) error {
	_, err := c.call(ctx, url.Values{"mode": {"queue"}, "name": {"resume"}, "value": {id}})
	return err
}

// DeleteQueue drops a job from the queue, and its partial data when delFiles is
// set. An id that is not in the queue is not an error: it may have finished
// into the history between the caller's last poll and this call.
func (c *Client) DeleteQueue(ctx context.Context, id string, delFiles bool) error {
	_, err := c.call(ctx, url.Values{
		"mode":      {"queue"},
		"name":      {"delete"},
		"value":     {id},
		"del_files": {boolParam(delFiles)},
	})
	return err
}

// DeleteHistory drops a job from the history, and its downloaded data when
// delFiles is set.
//
// `archive=0` is deliberate: SABnzbd's default is to move the row to an
// archive rather than forget it, and an archived row still answers a lookup by
// id — so the download would never actually leave Caravan's queue.
func (c *Client) DeleteHistory(ctx context.Context, id string, delFiles bool) error {
	_, err := c.call(ctx, url.Values{
		"mode":      {"history"},
		"name":      {"delete"},
		"value":     {id},
		"archive":   {"0"},
		"del_files": {boolParam(delFiles)},
	})
	return err
}

// Close releases nothing: SABnzbd holds all the state there is and this client
// keeps no session. It exists so the engine can satisfy core.Engine.
func (c *Client) Close() error { return nil }

// call runs one API request and returns the raw JSON envelope.
//
// Every failure mode SABnzbd has is folded in here, because two of them are
// invisible from the status code: a refused API key and a rejected request are
// both HTTP 200 with an `error` field in the body.
func (c *Client) call(ctx context.Context, params url.Values) (json.RawMessage, error) {
	mode := params.Get("mode")
	params.Set("output", "json")
	// The key goes in the query string because that is the only place SABnzbd
	// reads it. Nothing below ever puts a request URL into an error (SPEC §12).
	params.Set("apikey", c.key)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+apiPath+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("sabnzbd: %s: %w", mode, clients.Scrub(err))
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sabnzbd: %s: %w", mode, clients.Scrub(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, fmt.Errorf("sabnzbd: %s: %w", mode, clients.Scrub(err))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &APIError{Mode: mode, Status: resp.StatusCode, Body: clients.Snippet(body)}
	}

	var env struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		// Not JSON at all: a reverse proxy or a login page on SABnzbd's port.
		return nil, fmt.Errorf("sabnzbd: %s: unexpected answer: %s", mode, clients.Snippet(body))
	}
	if env.Error != "" {
		if refusedKey(env.Error) {
			return nil, fmt.Errorf("sabnzbd: %s: %s: %w", mode, env.Error, ErrUnauthorized)
		}
		return nil, fmt.Errorf("sabnzbd: %s: %s", mode, env.Error)
	}
	return body, nil
}

// refusedKey reports whether SABnzbd's complaint is about the API key.
//
// It matches on the phrase rather than on the exact sentence because SABnzbd
// has two of them ("API Key Required", "API Key Incorrect") and has reworded
// them before. A false negative only costs the caller the ErrUnauthorized
// classification; the message still reaches the user either way.
func refusedKey(msg string) bool {
	return strings.Contains(strings.ToLower(msg), "api key")
}

// boolParam spells a flag the way SABnzbd's int_conv reads it.
func boolParam(v bool) string {
	if v {
		return "1"
	}
	return "0"
}
