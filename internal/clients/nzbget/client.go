// Package nzbget talks to NZBGet's JSON-RPC API and adapts it to core.Engine.
// It is the second of the two backends that open the Usenet half of
// acquisition (SPEC §5.1, PLAN phase 6).
//
// It follows internal/clients/qbittorrent's shape — a thin wire client under a
// stateless core.Engine adapter — with three differences that are NZBGet's
// rather than choices:
//
//   - A download lives in two lists. NZBGet moves a group out of the queue and
//     into the history when it is finished with it, so "everything Caravan
//     added" is always both. Only the history says a download succeeded, and
//     only its DestDir/FinalDir say where the payload landed.
//   - The NZB is uploaded, not linked. NZBGet's `append` accepts a URL, but it
//     files a URL under a placeholder id and mints a *different* one once the
//     NZB has been fetched — the handle Caravan was given would stop naming
//     anything. Uploading the NZB bytes gets the real id back immediately, and
//     it keeps the indexer's API key out of NZBGet's queue and logs.
//   - There is no seeding. Usenet has no swarm, so a download is never
//     DownloadSeeding and Ratio is always zero.
//
// Like internal/download and internal/clients, this package does not import
// internal/store.
package nzbget

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
)

const (
	// rpcPath is appended to the configured base URL. NZBGet serves its whole
	// API from this one endpoint.
	rpcPath = "/jsonrpc"
	// rpcVersion is the envelope version NZBGet answers with and expects.
	rpcVersion = "1.1"
	// defaultTimeout bounds a single call.
	defaultTimeout = 30 * time.Second
	// maxBody bounds how much of a response is read. A long history is a few
	// megabytes; past this is a URL pointing at something that is not NZBGet.
	maxBody = 32 << 20
)

// Errors callers act on.
var (
	// ErrUnauthorized means NZBGet refused the configured control login. It
	// never carries the credential that was refused (SPEC §12).
	ErrUnauthorized = errors.New("nzbget: username or password rejected")
	// ErrNotFound means NZBGet knows no download with the requested id, in
	// either its queue or its history.
	ErrNotFound = errors.New("nzbget: download not found")
)

// APIError is a non-2xx answer. The method is included because it says which
// call failed; the credentials never are.
type APIError struct {
	// Method is the JSON-RPC method of the failed call.
	Method string
	// Status is the HTTP status code.
	Status int
	// Body is NZBGet's own (short) complaint, empty when it sent none.
	Body string
}

func (e *APIError) Error() string {
	if e.Body != "" {
		return fmt.Sprintf("nzbget: %s: http %d: %s", e.Method, e.Status, e.Body)
	}
	return fmt.Sprintf("nzbget: %s: http %d", e.Method, e.Status)
}

// RPCError is a fault NZBGet reported inside a 200: a method it does not know,
// a parameter it would not take.
type RPCError struct {
	Method  string
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("nzbget: %s: rpc error %d: %s", e.Method, e.Code, e.Message)
}

// Client is a thin NZBGet JSON-RPC client. It holds no session — NZBGet
// authenticates every request with HTTP basic auth — so it is safe for
// concurrent use and free to build and throw away.
type Client struct {
	// endpoint is the configured URL with any trailing slash removed plus the
	// RPC path. The base may carry a reverse-proxy prefix.
	endpoint string
	user     string
	pass     string
	hc       *http.Client
}

// New returns a client for cfg. A nil hc gets one with a default timeout; pass
// your own to share a transport.
func New(cfg core.DownloadClientConfig, hc *http.Client) (*Client, error) {
	t, ok := clients.Lookup(core.DownloadClientNZBGet)
	if !ok {
		// Unreachable: the type table is a compile-time constant.
		return nil, errors.New("nzbget: client type is not registered in internal/clients")
	}
	if err := t.Validate(cfg); err != nil {
		return nil, fmt.Errorf("nzbget: %w", err)
	}
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Client{
		endpoint: strings.TrimRight(strings.TrimSpace(cfg.URL), "/") + rpcPath,
		user:     cfg.Username,
		pass:     cfg.Password,
		hc:       hc,
	}, nil
}

// Version returns NZBGet's version string, e.g. "24.3". It is the cheapest
// authenticated call there is, which makes it the connection probe.
func (c *Client) Version(ctx context.Context) (string, error) {
	var out string
	if err := c.call(ctx, "version", nil, &out); err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// ServerStatus is the subset of `status` Caravan reads. NZBGet reports no
// per-group rate — it downloads one group at a time — so the server-wide rate
// is the rate of whichever group is actually transferring.
type ServerStatus struct {
	// DownloadRate is the current rate in bytes per second.
	DownloadRate int64 `json:"DownloadRate"`
	// DownloadPaused is the server-wide pause switch, which is not the same as
	// a group being paused: it stops everything at once.
	DownloadPaused bool `json:"DownloadPaused"`
}

// Status returns the server-wide download state.
func (c *Client) Status(ctx context.Context) (*ServerStatus, error) {
	var out ServerStatus
	if err := c.call(ctx, "status", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// ListGroups returns the downloads NZBGet is still working on — transferring
// or post-processing.
func (c *Client) ListGroups(ctx context.Context) ([]Group, error) {
	var out []Group
	// The parameter is how many log entries to include per group; none.
	if err := c.call(ctx, "listgroups", []any{0}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// History returns the downloads NZBGet has finished with: succeeded, failed or
// deleted. This is where a completed download's payload path comes from.
//
// Hidden records are not asked for: they are the tombstones NZBGet keeps for
// duplicate detection, not downloads.
func (c *Client) History(ctx context.Context) ([]HistoryItem, error) {
	var out []HistoryItem
	if err := c.call(ctx, "history", []any{false}, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// AppendRequest is one `append`.
type AppendRequest struct {
	// Filename is what NZBGet should call the job; it names the destination
	// directory. The ".nzb" suffix matters: NZBGet decides how to read the
	// content from it.
	Filename string
	// Content is the NZB file itself. It is base64-encoded on the wire.
	Content []byte
	// Category is the NZBGet category, empty for none.
	Category string
	// Priority is NZBGet's queue priority; 0 is normal.
	Priority int
	// Paused adds the download without starting it.
	Paused bool
}

// dupeModeScore is NZBGet's default duplicate handling. The field is
// mandatory once AddPaused is sent and NZBGet rejects any value but
// score/all/force.
const dupeModeScore = "score"

// Append uploads an NZB and returns the NZBID NZBGet filed it under.
//
// The positional parameter list is NZBGet's whole calling convention — names
// are ignored and order is everything — so it is spelled out here rather than
// built from a struct. The two trailing optional parameters (AutoCategory and
// the post-processing parameters) are omitted, which NZBGet reads as their
// defaults.
func (c *Client) Append(ctx context.Context, req AppendRequest) (int64, error) {
	params := []any{
		req.Filename,
		base64.StdEncoding.EncodeToString(req.Content),
		req.Category,
		req.Priority,
		false, // AddToTop: Caravan does not reorder the user's queue.
		req.Paused,
		"", // DupeKey: Caravan does its own duplicate checking.
		0,  // DupeScore
		dupeModeScore,
	}
	var id int64
	if err := c.call(ctx, "append", params, &id); err != nil {
		return 0, err
	}
	if id <= 0 {
		// NZBGet reports a refusal as a zero or negative id with no message.
		// The NZB is not repeated here: it came from a URL carrying the
		// indexer's API key.
		return 0, fmt.Errorf("nzbget: append: NZBGet would not take the NZB %q", req.Filename)
	}
	return id, nil
}

// Edit commands Caravan issues.
const (
	// EditGroupPause and EditGroupResume act on a download still in the queue.
	EditGroupPause  = "GroupPause"
	EditGroupResume = "GroupResume"
	// EditGroupFinalDelete removes a queued download without leaving a history
	// row behind. NZBGet cleans up its own partial files.
	EditGroupFinalDelete = "GroupFinalDelete"
	// EditHistoryFinalDelete removes a history row outright, rather than
	// hiding it as a duplicate-detection tombstone.
	EditHistoryFinalDelete = "HistoryFinalDelete"
)

// EditQueue runs one edit command against a set of ids and reports whether
// NZBGet matched any of them.
//
// The false result is information, not a failure: NZBGet answers it for a
// command aimed at a list the id is not in, which is how a caller finds out
// whether a download is still queued or already in the history.
//
// The leading zero is the pre-18.0 Offset parameter. Current NZBGet skips it
// when it is not an integer, and older NZBGet requires it, so sending it works
// against both.
func (c *Client) EditQueue(ctx context.Context, command string, ids ...int64) (bool, error) {
	params := make([]any, 0, len(ids)+3)
	params = append(params, command, 0, "")
	for _, id := range ids {
		params = append(params, id)
	}
	var ok bool
	if err := c.call(ctx, "editqueue", params, &ok); err != nil {
		return false, err
	}
	return ok, nil
}

// Close releases nothing: NZBGet holds all the state there is and this client
// keeps no session. It exists so the engine can satisfy core.Engine.
func (c *Client) Close() error { return nil }

// rpcRequest is the JSON-RPC envelope NZBGet expects.
type rpcRequest struct {
	Version string `json:"version"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int    `json:"id"`
}

// rpcResponse is the envelope NZBGet answers with. A fault arrives inside a
// 200, in `error`, so the status code alone cannot be trusted.
type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Name    string `json:"name"`
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// call runs one JSON-RPC request and decodes its result into out.
func (c *Client) call(ctx context.Context, method string, params []any, out any) error {
	if params == nil {
		params = []any{}
	}
	body, err := json.Marshal(rpcRequest{Version: rpcVersion, Method: method, Params: params, ID: 1})
	if err != nil {
		return fmt.Errorf("nzbget: %s: %w", method, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("nzbget: %s: %w", method, clients.Scrub(err))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	// NZBGet's control login is HTTP basic auth. It is set unconditionally:
	// NZBGet ignores it when it is configured without a password, and a server
	// that wants one answers 401 rather than prompting.
	req.SetBasicAuth(c.user, c.pass)

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("nzbget: %s: %w", method, clients.Scrub(err))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return fmt.Errorf("nzbget: %s: %w", method, clients.Scrub(err))
	}

	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return fmt.Errorf("nzbget: %s: %w", method, ErrUnauthorized)
	default:
		return &APIError{Method: method, Status: resp.StatusCode, Body: clients.Snippet(raw)}
	}

	var env rpcResponse
	if err := json.Unmarshal(raw, &env); err != nil {
		// Not a JSON-RPC envelope: a reverse proxy or a login page on NZBGet's
		// port.
		return fmt.Errorf("nzbget: %s: unexpected answer: %s", method, clients.Snippet(raw))
	}
	if env.Error != nil {
		return &RPCError{Method: method, Code: env.Error.Code, Message: env.Error.Message}
	}
	if out == nil {
		return nil
	}
	if len(env.Result) == 0 {
		return fmt.Errorf("nzbget: %s: answer carried no result", method)
	}
	if err := json.Unmarshal(env.Result, out); err != nil {
		return fmt.Errorf("nzbget: %s: %w", method, err)
	}
	return nil
}
