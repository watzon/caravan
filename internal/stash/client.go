// Package stash is Caravan's client for the Stash GraphQL API and the adult
// library handoff built on it (PLAN phase 11).
//
// Stash is the adult counterpart of Jellyfin, and this package is deliberately
// internal/jellyfin's adult twin: a thin client with no retry of its own, plus
// a service that turns "the adult library changed" into durable jobs. What
// makes it the *adult* twin rather than a copy is scope and identity. The scan
// it triggers names one path — the adult library root — because a Caravan that
// asked Stash to rescan everything would have it walk a television library it
// has no business seeing (SPEC §1.2, the exposure rule). And because phase 9
// already sources scene metadata from a stash-box, Caravan holds the same
// vocabulary Stash's own identify step uses, so the scene can be handed over
// already identified instead of arriving as an untagged file.
//
// Nothing in *this file* retries. A scan or a push that does not land is not
// lost: the caller is a durable job (SPEC §7). What owns the waiting is the
// handoff, not the queue's default backoff — the queue spends its five attempts
// inside nine minutes, and the two things being waited on (a metadataScan of a
// real library, a Stash host coming back from a reboot) are routinely slower
// than that. So the one answer that is expected to repeat — looking for a scene
// Stash has not finished indexing yet — is reported as ErrSceneNotFound, and
// handoff.go re-arms the job on it rather than failing it.
//
// Schema note: every field selected here was read off stashapp/stash's own
// `graphql/schema` on the develop branch. Deprecated fields are avoided, and so
// are the newest ones where an older equivalent exists — `studio_filter.name`
// rather than `stash_ids_endpoint`, for instance — because a household's Stash
// is whatever version it last updated to.
//
// Path note: the paths Caravan sends are Caravan's own absolute paths. A Stash
// that sees the same library at a different mount point (a container with a
// different bind) will scan nothing and find nothing; that is a deployment
// mismatch this package reports rather than guesses around.
package stash

import (
	"bytes"
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
	// DefaultTimeout bounds a single request. Stash is normally on the same
	// host or LAN as Caravan; a handoff that hangs must not hold a job's lease
	// open.
	DefaultTimeout = 20 * time.Second

	// APIKeyHeader is Stash's own credential header. Stash also accepts an
	// `apikey` query parameter, which is not used here: a credential in a query
	// string is a credential in an access log (SPEC §12).
	APIKeyHeader = "ApiKey"

	// GraphQLPath is where Stash serves GraphQL. It is appended to the
	// configured base URL, so a user pastes the address they open in a browser
	// rather than an API path.
	GraphQLPath = "/graphql"

	// maxResponseBody bounds how much of a response is read. A GraphQL reply is
	// a single JSON document with no streaming, so a body past this size is a
	// malfunctioning endpoint rather than a large page.
	maxResponseBody = 8 << 20
)

// Errors callers branch on. Match them with errors.Is; use errors.As with
// *APIError for Stash's own message.
var (
	// ErrUnauthorized means the API key is missing or wrong.
	ErrUnauthorized = errors.New("stash: unauthorized")

	// ErrSceneNotFound means Stash has no scene at that path. It is the
	// expected answer between an import and the moment the scan finishes
	// indexing the file, which is why it is a sentinel rather than a failure:
	// the identity push retries on it (see handoff.go).
	ErrSceneNotFound = errors.New("stash: no scene at that path")

	// ErrAmbiguousScene means more than one scene matched the path. Stash's
	// path filter is a string match, so this is possible in principle; pushing
	// identity onto a guess is not, so it is refused.
	ErrAmbiguousScene = errors.New("stash: more than one scene at that path")
)

// APIError is a failed Stash operation. StatusCode is the HTTP status;
// Message is the first GraphQL error's message, or the HTTP status text when
// the reply carried no errors array.
//
// Operation is the GraphQL operation name that failed ("SceneUpdate"). It is
// the GraphQL equivalent of a request path, and — unlike a URL — it can carry
// no credential, so it is safe to log.
type APIError struct {
	Operation  string
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("stash: %s: %s", e.Operation, e.Message)
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

// Client talks to one Stash server.
type Client struct {
	// BaseURL is the server root, e.g. http://stash.lan:9999. GraphQLPath is
	// appended to it.
	BaseURL string
	// APIKey is the key from Stash's Settings → Security screen. Empty sends no
	// credential, which is what an unauthenticated Stash on a private LAN
	// wants.
	APIKey string

	hc *http.Client
}

// NewClient returns a client for baseURL. A nil hc gets one with DefaultTimeout.
func NewClient(baseURL, apiKey string, hc *http.Client) *Client {
	if hc == nil {
		hc = &http.Client{Timeout: DefaultTimeout}
	}
	return &Client{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		APIKey:  strings.TrimSpace(apiKey),
		hc:      hc,
	}
}

// ServerVersion is what a successful test reports: proof the server answered
// and identified itself, which is more convincing than a bare "ok".
type ServerVersion struct {
	Version string `json:"version"`
	Hash    string `json:"hash"`
}

const versionDoc = `query Version { version { version hash } }`

// Version asks the server who it is. It is the test-connection call: Stash
// refuses unauthenticated GraphQL when a key is configured, so a 200 proves
// both that the URL is a Stash and that the credential works.
func (c *Client) Version(ctx context.Context) (ServerVersion, error) {
	var out struct {
		Version ServerVersion `json:"version"`
	}
	if err := c.query(ctx, "Version", versionDoc, nil, &out); err != nil {
		return ServerVersion{}, err
	}
	return out.Version, nil
}

const metadataScanDoc = `mutation MetadataScan($input: ScanMetadataInput!) { metadataScan(input: $input) }`

// Scan asks Stash to scan the given paths and nothing else.
//
// The scope is the whole point: ScanMetadataInput.paths restricts the walk to
// the directories named, so Caravan asks for the adult library root and Stash
// never looks at the television or film shelves. Passing no paths would scan
// every configured library, which is why an empty slice is refused here rather
// than sent.
//
// Stash answers with the id of the job it queued and does the work in the
// background, so a nil error means "the scan was accepted", never "the scan
// finished". Nothing is generated: covers, previews, sprites and phashes are
// Stash's own decisions to make on its own schedule, and phash in particular
// stays Stash's job permanently — Caravan never decodes video frames.
func (c *Client) Scan(ctx context.Context, paths []string) (string, error) {
	clean := make([]string, 0, len(paths))
	for _, p := range paths {
		if p = strings.TrimSpace(p); p != "" {
			clean = append(clean, p)
		}
	}
	if len(clean) == 0 {
		return "", errors.New("stash: a scan needs at least one path")
	}

	var out struct {
		JobID string `json:"metadataScan"`
	}
	vars := map[string]any{"input": map[string]any{"paths": clean}}
	if err := c.query(ctx, "MetadataScan", metadataScanDoc, vars, &out); err != nil {
		return "", err
	}
	return out.JobID, nil
}

// Scene is the slice of a Stash scene this package reads back.
type Scene struct {
	ID    string `json:"id"`
	Title string `json:"title"`
}

const findSceneByPathDoc = `query FindSceneByPath($path: String!) {
  findScenes(scene_filter: {path: {value: $path, modifier: EQUALS}}, filter: {per_page: 2}) {
    count
    scenes { id title }
  }
}`

// SceneByPath finds the one scene whose file is at path.
//
// It asks for two rather than one so "exactly one match" is a fact rather than
// an assumption: a path filter is a string match, and pushing identity onto the
// first of several matches would silently retitle the wrong scene.
//
// ErrSceneNotFound is the ordinary answer while a scan is still in flight.
func (c *Client) SceneByPath(ctx context.Context, path string) (*Scene, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("stash: a scene lookup needs a path")
	}

	var out struct {
		FindScenes struct {
			Count  int     `json:"count"`
			Scenes []Scene `json:"scenes"`
		} `json:"findScenes"`
	}
	if err := c.query(ctx, "FindSceneByPath", findSceneByPathDoc, map[string]any{"path": path}, &out); err != nil {
		return nil, err
	}
	switch len(out.FindScenes.Scenes) {
	case 0:
		return nil, ErrSceneNotFound
	case 1:
		scene := out.FindScenes.Scenes[0]
		return &scene, nil
	default:
		return nil, ErrAmbiguousScene
	}
}

// StashID is one stash-box identity: which box, and the record's id there. It
// is the shared vocabulary that makes an identity push possible at all — the
// same endpoint and UUID phase 9 read the scene's metadata from.
type StashID struct {
	Endpoint string `json:"endpoint"`
	StashID  string `json:"stash_id"`
}

// SceneUpdate is the identity Caravan pushes onto a scene.
//
// Every field is optional on the wire except ID: a nil StudioID or empty
// PerformerIDs leaves what Stash already has alone, rather than clearing it.
// That is deliberate — the studio and performer halves of the push are
// best effort (see handoff.go), and a failed lookup must not erase a value a
// user set by hand.
type SceneUpdate struct {
	ID           string
	Title        string
	StashIDs     []StashID
	StudioID     string
	PerformerIDs []string
	// URLs is the scene's page on the site, empty when unknown.
	URLs []string
	// Date is the release date as YYYY-MM-DD, empty when unknown.
	Date string
}

// input renders the update as SceneUpdateInput, omitting everything that was
// not supplied.
func (u SceneUpdate) input() map[string]any {
	in := map[string]any{"id": u.ID}
	if u.Title != "" {
		in["title"] = u.Title
	}
	if len(u.StashIDs) > 0 {
		ids := make([]map[string]any, 0, len(u.StashIDs))
		for _, id := range u.StashIDs {
			ids = append(ids, map[string]any{"endpoint": id.Endpoint, "stash_id": id.StashID})
		}
		in["stash_ids"] = ids
	}
	if u.StudioID != "" {
		in["studio_id"] = u.StudioID
	}
	if len(u.PerformerIDs) > 0 {
		in["performer_ids"] = u.PerformerIDs
	}
	if len(u.URLs) > 0 {
		in["urls"] = u.URLs
	}
	if u.Date != "" {
		in["date"] = u.Date
	}
	return in
}

const sceneUpdateDoc = `mutation SceneUpdate($input: SceneUpdateInput!) { sceneUpdate(input: $input) { id } }`

// UpdateScene writes the identity onto an existing scene.
func (c *Client) UpdateScene(ctx context.Context, u SceneUpdate) error {
	if strings.TrimSpace(u.ID) == "" {
		return errors.New("stash: a scene update needs a scene id")
	}
	var out struct {
		SceneUpdate *Scene `json:"sceneUpdate"`
	}
	return c.query(ctx, "SceneUpdate", sceneUpdateDoc, map[string]any{"input": u.input()}, &out)
}

const findStudioDoc = `query FindStudioByName($name: String!) {
  findStudios(studio_filter: {name: {value: $name, modifier: EQUALS}}, filter: {per_page: 1}) {
    studios { id name }
  }
}`

// StudioByName returns the id of the studio with exactly this name, or "" when
// Stash has none.
//
// By name rather than by stash-box id on purpose. `stash_ids_endpoint` is a
// recent filter and its older spelling is deprecated, so keying on it would
// make this work on some households' Stash and not others; a name is a field
// every version has had. The stash-box id still travels — it goes *onto* the
// studio at creation (see CreateStudio), which is what makes the next lookup
// unambiguous to a human even though this one was not.
func (c *Client) StudioByName(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	var out struct {
		FindStudios struct {
			Studios []struct {
				ID string `json:"id"`
			} `json:"studios"`
		} `json:"findStudios"`
	}
	if err := c.query(ctx, "FindStudioByName", findStudioDoc, map[string]any{"name": name}, &out); err != nil {
		return "", err
	}
	if len(out.FindStudios.Studios) == 0 {
		return "", nil
	}
	return out.FindStudios.Studios[0].ID, nil
}

const studioCreateDoc = `mutation StudioCreate($input: StudioCreateInput!) { studioCreate(input: $input) { id } }`

// CreateStudio adds a studio, carrying its stash-box identity so Stash's own
// tooling can reconcile it later.
func (c *Client) CreateStudio(ctx context.Context, name string, ids []StashID) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("stash: a studio needs a name")
	}
	in := map[string]any{"name": name}
	if len(ids) > 0 {
		in["stash_ids"] = stashIDInputs(ids)
	}
	var out struct {
		StudioCreate *struct {
			ID string `json:"id"`
		} `json:"studioCreate"`
	}
	if err := c.query(ctx, "StudioCreate", studioCreateDoc, map[string]any{"input": in}, &out); err != nil {
		return "", err
	}
	if out.StudioCreate == nil {
		return "", &APIError{Operation: "StudioCreate", StatusCode: http.StatusOK, Message: "studio was not created"}
	}
	return out.StudioCreate.ID, nil
}

const findPerformerDoc = `query FindPerformerByName($name: String!) {
  findPerformers(performer_filter: {name: {value: $name, modifier: EQUALS}}, filter: {per_page: 1}) {
    performers { id name }
  }
}`

// PerformerByName returns the id of the performer with exactly this name, or ""
// when Stash has none. See StudioByName for why the key is a name.
func (c *Client) PerformerByName(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", nil
	}
	var out struct {
		FindPerformers struct {
			Performers []struct {
				ID string `json:"id"`
			} `json:"performers"`
		} `json:"findPerformers"`
	}
	if err := c.query(ctx, "FindPerformerByName", findPerformerDoc, map[string]any{"name": name}, &out); err != nil {
		return "", err
	}
	if len(out.FindPerformers.Performers) == 0 {
		return "", nil
	}
	return out.FindPerformers.Performers[0].ID, nil
}

const performerCreateDoc = `mutation PerformerCreate($input: PerformerCreateInput!) { performerCreate(input: $input) { id } }`

// CreatePerformer adds a performer by name.
//
// No stash-box id is passed, and that is not an oversight: what Caravan stores
// on a scene is the credited *names* (core.SceneInfo.Performers), because that
// is what a scene row renders and what a release filename contains. The
// provider's performer ids are not carried into the library, so there is
// nothing truthful to attach here — inventing one would be worse than omitting
// it.
func (c *Client) CreatePerformer(ctx context.Context, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("stash: a performer needs a name")
	}
	var out struct {
		PerformerCreate *struct {
			ID string `json:"id"`
		} `json:"performerCreate"`
	}
	vars := map[string]any{"input": map[string]any{"name": name}}
	if err := c.query(ctx, "PerformerCreate", performerCreateDoc, vars, &out); err != nil {
		return "", err
	}
	if out.PerformerCreate == nil {
		return "", &APIError{Operation: "PerformerCreate", StatusCode: http.StatusOK, Message: "performer was not created"}
	}
	return out.PerformerCreate.ID, nil
}

func stashIDInputs(ids []StashID) []map[string]any {
	out := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		out = append(out, map[string]any{"endpoint": id.Endpoint, "stash_id": id.StashID})
	}
	return out
}

// gqlRequest is the standard GraphQL-over-HTTP request body.
type gqlRequest struct {
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables,omitempty"`
	OperationName string         `json:"operationName,omitempty"`
}

// gqlResponse is the GraphQL envelope. Data is kept raw so a reply carrying
// both data and errors is judged before it is decoded.
type gqlResponse struct {
	Data   json.RawMessage `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

// query executes doc as the named operation and decodes the `data` object into
// out.
//
// op is passed separately from doc so errors name something short and stable,
// and so a fake endpoint can route on the same name a real one logs — the same
// arrangement internal/stashbox uses.
func (c *Client) query(ctx context.Context, op, doc string, vars map[string]any, out any) error {
	if c.BaseURL == "" {
		return errors.New("stash: no server URL is configured")
	}
	payload, err := json.Marshal(gqlRequest{Query: doc, Variables: vars, OperationName: op})
	if err != nil {
		return fmt.Errorf("stash: encode %s: %w", op, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+GraphQLPath, bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("stash: request %s: %w", op, err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if c.APIKey != "" {
		req.Header.Set(APIKeyHeader, c.APIKey)
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
		return fmt.Errorf("stash: post %s: %w", op, err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBody))
	if err != nil {
		return fmt.Errorf("stash: read %s: %w", op, err)
	}

	var envelope gqlResponse
	decodeErr := json.Unmarshal(raw, &envelope)

	// A non-2xx is an error whether or not its body was JSON: a proxy in front
	// of Stash answers 502 with HTML, and that must not read as "decode failed"
	// when the real story is "the server is down".
	if resp.StatusCode/100 != 2 {
		return newAPIError(op, resp.StatusCode, envelope)
	}
	if decodeErr != nil {
		return fmt.Errorf("stash: decode %s: %w", op, decodeErr)
	}
	if len(envelope.Errors) > 0 {
		return newAPIError(op, resp.StatusCode, envelope)
	}
	if len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		return fmt.Errorf("stash: %s: response carried no data", op)
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return fmt.Errorf("stash: decode %s: %w", op, err)
	}
	return nil
}

// newAPIError builds an APIError, taking the first GraphQL error as the
// reportable one. Later errors in the array are additional detail on the same
// failure and would only make the message unreadable.
func newAPIError(op string, status int, envelope gqlResponse) *APIError {
	e := &APIError{Operation: op, StatusCode: status}
	if len(envelope.Errors) > 0 {
		e.Message = strings.TrimSpace(envelope.Errors[0].Message)
	}
	if e.Message == "" {
		e.Message = http.StatusText(status)
	}
	if e.Message == "" {
		e.Message = "unknown error"
	}
	return e
}
