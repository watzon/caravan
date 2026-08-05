// Package stashboxtest is an in-process stash-box endpoint for testing
// Caravan's stash-box client and everything built on it (PLAN phase 9 task 1).
//
// It speaks the slice of GraphQL-over-HTTP that the client uses — a POST
// carrying {query, variables, operationName}, answered from a per-operation
// queue of canned replies — and nothing else. It does not parse GraphQL: a test
// registers the reply it wants for an operation name, which keeps this package
// free of a schema that would have to be kept in step with three dialects.
//
// **It records every request it receives, before doing anything else with it.**
// That is not a convenience: PLAN phase 9's acceptance criterion is that a full
// job cycle with the adult module disabled makes *zero* requests to the
// stash-box endpoint, and Count() is the assertion that proves it. A request
// that is malformed, unauthenticated, sent with the wrong method, or for an
// operation with no stub is still recorded — an unanswerable request is still
// traffic, and traffic is the thing being measured.
//
// It listens on 127.0.0.1 with a kernel-chosen port, never on Caravan's own
// port, and every test that uses it is free of the network (no live calls, no
// fixtures downloaded at test time).
//
// The reason it is a package rather than a _test.go file is that several tracks
// of phase 9 need the same fake: the client, the gating tests, the refresh job
// and the end-to-end suite. It holds no Caravan types on purpose — it does not
// import internal/stashbox — so the client's own in-package tests can use it
// without an import cycle.
package stashboxtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"
)

// apiKeyHeader is stash-box's credential header. It is duplicated from
// stashbox.APIKeyHeader rather than imported, because this package deliberately
// depends on nothing of Caravan's; the client's tests assert the two agree.
const apiKeyHeader = "ApiKey"

// restSitesPath and restScenesPath are where TPDB's REST indexes live. They are
// duplicated from the client for the same reason apiKeyHeader is: this package
// depends on nothing of Caravan's, and the client's own tests assert they agree.
const (
	restSitesPath      = "/sites"
	restScenesPath     = "/scenes"
	restPerformersPath = "/performers"
	restTagsPath       = "/tags"
)

// maxRequestBody bounds how much of a request body is read. A GraphQL query
// document is small; anything past this is a client bug worth truncating rather
// than buffering.
const maxRequestBody = 1 << 20

// Request is one request the fake endpoint saw. The GraphQL fields are best
// effort: a body that is not a JSON object leaves them zero and keeps Body, so
// a malformed request is still visible rather than silently dropped.
type Request struct {
	// Method and Path are recorded because a client that POSTs to the wrong
	// place has still generated traffic, and a test asserting silence has to
	// see that too.
	Method string
	Path   string
	// RawQuery is the request's query string, empty for the GraphQL POSTs. It is
	// recorded because TPDB's REST site index puts its search term there, and a
	// test asserting what was searched for has nowhere else to read it.
	RawQuery string
	// OperationName is the GraphQL operation name, which is what responses are
	// keyed on.
	OperationName string
	// Query is the GraphQL document. Tests assert on the fields a client asks
	// for — a selection set that quietly grows is how dialect compatibility is
	// lost.
	Query string
	// Variables is the decoded variables object, nil when there was none.
	Variables map[string]any
	// APIKey and Authorization are the credential headers as received. They are
	// kept so a test can prove a key is sent, and prove which one.
	APIKey        string
	Authorization string
	// Body is the raw request body, always populated.
	Body []byte
	// Received is when the request arrived, for ordering assertions.
	Received time.Time
}

// Response is one canned reply. A zero Status is served as 200, so the common
// case reads as just a body.
type Response struct {
	Status int
	Header http.Header
	Body   []byte
}

// Options configures a Server.
type Options struct {
	// Operations maps a GraphQL operation name to the queue of responses served
	// for it, consumed in order. The last response in a queue repeats, so a
	// single-element queue answers any number of requests.
	Operations map[string][]Response
	// Fallback answers an operation with no queue. Nil serves a GraphQL error
	// naming the missing stub, which fails the calling test with a readable
	// message instead of a decode error.
	Fallback *Response
	// RequireAPIKey answers a request carrying neither credential header with
	// 401. It is off by default so a test about anonymous reads does not have
	// to opt out of it.
	RequireAPIKey bool
	// WithoutQueryStudios makes the endpoint behave like TPDB, whose schema has
	// no queryStudios at all: any document mentioning that field is answered
	// with a bare HTTP 500 and the plain-text body "Server Error" — not a
	// GraphQL validation error, and not JSON of any kind. Every other operation
	// is served normally, which is the point: TPDB answers searchScene,
	// queryScenes and findStudio perfectly well.
	//
	// It matches on the query document rather than the operation name because
	// the thing the endpoint lacks is the field, not whatever a client chose to
	// call its operation.
	WithoutQueryStudios bool
	// SiteIndex answers TPDB's REST site index at GET /sites, from a queue
	// consumed exactly as Operations is (the last response repeats). Leaving it
	// empty is an endpoint with no REST side at all, which answers 404 — that is
	// every stash-box but TPDB's, and it is the default so a test has to opt in
	// to the dialect the same way it opts in to a missing queryStudios.
	SiteIndex []Response
	// SceneIndex answers TPDB's REST scene index at GET /scenes, with the same
	// queue-and-404 semantics SiteIndex has.
	SceneIndex []Response
	// SiteLookup answers GET /sites/{anything} — the uuid-to-numeric-id
	// resolution the REST scene index needs — with the same queue-and-404
	// semantics. It is separate from SiteIndex because a test asserting "one
	// lookup, then cached" must tell the two apart.
	SiteLookup []Response
	// PerformerIndex and TagIndex answer TPDB's REST typeahead indexes at
	// GET /performers and GET /tags, with SiteIndex's queue-and-404 semantics.
	PerformerIndex []Response
	TagIndex       []Response
}

// Server is a fake stash-box endpoint.
type Server struct {
	srv *httptest.Server

	mu             sync.Mutex
	operations     map[string][]Response
	fallback       *Response
	requireKey     bool
	noQueryStudios bool
	siteIndex      []Response
	sceneIndex     []Response
	siteLookup     []Response
	performerIndex []Response
	tagIndex       []Response
	requests       []Request
}

// New starts a fake endpoint. The caller must Close it; tests should do that
// with t.Cleanup.
func New(opts Options) *Server {
	s := &Server{
		operations:     make(map[string][]Response, len(opts.Operations)),
		fallback:       opts.Fallback,
		requireKey:     opts.RequireAPIKey,
		noQueryStudios: opts.WithoutQueryStudios,
		siteIndex:      append([]Response(nil), opts.SiteIndex...),
		sceneIndex:     append([]Response(nil), opts.SceneIndex...),
		siteLookup:     append([]Response(nil), opts.SiteLookup...),
		performerIndex: append([]Response(nil), opts.PerformerIndex...),
		tagIndex:       append([]Response(nil), opts.TagIndex...),
	}
	for op, queue := range opts.Operations {
		s.operations[op] = append([]Response(nil), queue...)
	}
	s.srv = httptest.NewServer(s)
	return s
}

// URL is the endpoint address, ready to hand to a client as its GraphQL URL.
func (s *Server) URL() string { return s.srv.URL }

// Client is an http.Client configured for this server.
func (s *Server) Client() *http.Client { return s.srv.Client() }

// Close shuts the endpoint down.
func (s *Server) Close() { s.srv.Close() }

// SetOperation replaces the response queue for op. Passing no responses removes
// the queue, which sends op back to the fallback.
func (s *Server) SetOperation(op string, responses ...Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(responses) == 0 {
		delete(s.operations, op)
		return
	}
	s.operations[op] = append([]Response(nil), responses...)
}

// SetSiteIndex replaces the REST site index queue. Passing no responses turns
// the REST side off again, which answers 404.
func (s *Server) SetSiteIndex(responses ...Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.siteIndex = append([]Response(nil), responses...)
}

// SetSceneIndex replaces the REST scene index queue, with SetSiteIndex's
// semantics.
func (s *Server) SetSceneIndex(responses ...Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sceneIndex = append([]Response(nil), responses...)
}

// SetSiteLookup replaces the /sites/{id} lookup queue, with SetSiteIndex's
// semantics.
func (s *Server) SetSiteLookup(responses ...Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.siteLookup = append([]Response(nil), responses...)
}

// SetPerformerIndex replaces the REST performer index queue, with
// SetSiteIndex's semantics.
func (s *Server) SetPerformerIndex(responses ...Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.performerIndex = append([]Response(nil), responses...)
}

// SetTagIndex replaces the REST tag index queue, with SetSiteIndex's
// semantics.
func (s *Server) SetTagIndex(responses ...Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tagIndex = append([]Response(nil), responses...)
}

// serveQueue pops the next response from a queue, repeating its last entry, or
// answers 404 for an empty queue — an endpoint that does not have that route.
// Called under s.mu via record.
func serveQueue(queue *[]Response, what string) Response {
	q := *queue
	if len(q) == 0 {
		return Response{
			Status: http.StatusNotFound,
			Body:   []byte(fmt.Sprintf(`{"message":"stashboxtest: this endpoint has no %s"}`, what)),
		}
	}
	resp := q[0]
	if len(q) > 1 {
		*queue = q[1:]
	}
	return resp
}

// SetFallback replaces the reply for operations with no queue. Nil restores the
// default "no stub" GraphQL error.
func (s *Server) SetFallback(resp *Response) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fallback = resp
}

// Requests returns a copy of every request the endpoint has seen, oldest first.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Count is how many requests the endpoint has seen. It is the assertion behind
// "zero stash-box traffic when the adult module is disabled": a test runs a full
// job cycle and requires this to be 0.
func (s *Server) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.requests)
}

// Reset clears the recorded requests, leaving the response queues alone. It is
// for a test that sets up through the endpoint and then measures traffic from a
// known-zero baseline.
func (s *Server) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.requests = nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Read and record first, unconditionally. Everything after this line is
	// about answering; nothing after it may decide a request did not happen.
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxRequestBody))

	req := Request{
		Method:        r.Method,
		Path:          r.URL.Path,
		RawQuery:      r.URL.RawQuery,
		APIKey:        r.Header.Get(apiKeyHeader),
		Authorization: r.Header.Get("Authorization"),
		Body:          body,
		Received:      time.Now(),
	}
	var parsed struct {
		Query         string         `json:"query"`
		OperationName string         `json:"operationName"`
		Variables     map[string]any `json:"variables"`
	}
	if err := json.Unmarshal(body, &parsed); err == nil {
		req.OperationName = parsed.OperationName
		req.Query = parsed.Query
		req.Variables = parsed.Variables
	}

	resp := s.record(req)

	// JSON by default, but a canned response's own headers win: an endpoint
	// failing outside the GraphQL envelope answers with plain text or HTML, and
	// a fake that relabelled that as JSON would hide the case a client has to
	// survive.
	w.Header().Set("Content-Type", "application/json")
	for k, vs := range resp.Header {
		w.Header().Del(k)
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = w.Write(resp.Body)
}

// record appends req to the log and picks its reply, both under one lock so a
// concurrent caller cannot consume a queue entry that belongs to another
// request.
func (s *Server) record(req Request) Response {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = append(s.requests, req)

	if s.requireKey && req.APIKey == "" && req.Authorization == "" {
		return Unauthorized()
	}
	if s.noQueryStudios && strings.Contains(req.Query, "queryStudios") {
		return ServerError()
	}
	// The REST routes are addressed by path, not by operation name: they are
	// not GraphQL and have none. Order matters: /sites/{uuid} before /sites.
	if strings.HasPrefix(req.Path, restSitesPath+"/") {
		return serveQueue(&s.siteLookup, "site lookup")
	}
	if req.Path == restSitesPath {
		return serveQueue(&s.siteIndex, "REST site index")
	}
	if req.Path == restScenesPath {
		return serveQueue(&s.sceneIndex, "REST scene index")
	}
	if req.Path == restPerformersPath {
		return serveQueue(&s.performerIndex, "REST performer index")
	}
	if req.Path == restTagsPath {
		return serveQueue(&s.tagIndex, "REST tag index")
	}

	queue := s.operations[req.OperationName]
	if len(queue) > 0 {
		resp := queue[0]
		if len(queue) > 1 {
			s.operations[req.OperationName] = queue[1:]
		}
		return resp
	}
	if s.fallback != nil {
		return *s.fallback
	}
	return GraphQLError(fmt.Sprintf("no stub for operation %q", req.OperationName), "INTERNAL_SERVER_ERROR")
}

// Data wraps a `data` object in the GraphQL envelope and serves it with 200. The
// argument is the raw JSON of the data object — typically a recorded fixture's
// inner payload.
func Data(raw []byte) Response {
	return Response{Body: []byte(`{"data":` + string(raw) + `}`)}
}

// Raw serves body verbatim with 200. Use it for a fixture recorded as a whole
// GraphQL envelope, including its own `data` or `errors` key.
func Raw(body []byte) Response {
	return Response{Body: body}
}

// Status serves body with an explicit HTTP status, for the failures an endpoint
// reports outside the GraphQL envelope.
func Status(status int, body []byte) Response {
	return Response{Status: status, Body: body}
}

// GraphQLError serves a 200 carrying a one-entry errors array, which is how a
// GraphQL endpoint reports most failures.
func GraphQLError(message, code string) Response {
	body, err := json.Marshal(struct {
		Errors []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		} `json:"errors"`
	}{
		Errors: []struct {
			Message    string `json:"message"`
			Extensions struct {
				Code string `json:"code"`
			} `json:"extensions"`
		}{{Message: message, Extensions: struct {
			Code string `json:"code"`
		}{Code: code}}},
	})
	if err != nil {
		// Marshalling a fixed struct of strings cannot fail; if it somehow
		// does, a valid envelope still beats an empty body.
		return Response{Body: []byte(`{"errors":[{"message":"stashboxtest: encode error"}]}`)}
	}
	return Response{Body: body}
}

// Unauthorized is the reply RequireAPIKey serves: a 401 whose body is a
// well-formed GraphQL error, matching what a real stash-box sends.
func Unauthorized() Response {
	resp := GraphQLError("invalid or missing api key", "UNAUTHENTICATED")
	resp.Status = http.StatusUnauthorized
	return resp
}

// ServerError is TPDB's answer to a query for a field its schema does not have:
// HTTP 500 with the plain-text body "Server Error". It is not a GraphQL error
// and not JSON, which is exactly what makes it worth having a constructor for —
// a client that assumes every reply decodes reports this as a decode failure
// instead of as the endpoint failing.
func ServerError() Response {
	return Response{
		Status: http.StatusInternalServerError,
		Header: http.Header{"Content-Type": {"text/plain; charset=utf-8"}},
		Body:   []byte("Server Error"),
	}
}

// RateLimited is a 429 carrying a Retry-After of retryAfterSeconds, for
// exercising a client's throttle handling.
func RateLimited(retryAfterSeconds int) Response {
	resp := GraphQLError("too many requests", "RATE_LIMITED")
	resp.Status = http.StatusTooManyRequests
	resp.Header = http.Header{"Retry-After": {fmt.Sprint(retryAfterSeconds)}}
	return resp
}
