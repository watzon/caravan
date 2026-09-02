// Package stashtest is an in-process Stash endpoint for testing Caravan's Stash
// client and everything built on it.
//
// It is internal/stashbox/stashboxtest's twin and works the same way: it speaks
// the slice of GraphQL-over-HTTP the client uses, a POST to /graphql carrying
// {query, variables, operationName}, answered from a per-operation queue of
// canned replies, and nothing else. It does not parse GraphQL; a test registers
// the reply it wants for an operation name, which keeps this package free of a
// schema that would have to be kept in step with every Stash release.
//
// It records every request it receives, before doing anything else with it. An
// adult import must fire exactly one scoped scan and a television import none,
// so Count() and Operations("MetadataScan") have to see a request the endpoint
// could not answer just as clearly as one it could. A malformed body, a missing
// credential and an unstubbed operation are all still traffic.
//
// It listens on 127.0.0.1 with a kernel-chosen port, and every test that uses
// it is free of the network. It holds no Caravan types on purpose, it does not
// import internal/stash, so the client's own in-package tests can use it
// without an import cycle.
package stashtest

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"time"
)

// apiKeyHeader is Stash's credential header. It is duplicated from
// stash.APIKeyHeader rather than imported, because this package deliberately
// depends on nothing of Caravan's; the client's tests assert the two agree.
const apiKeyHeader = "ApiKey"

// graphQLPath is where Stash serves GraphQL, duplicated for the same reason.
const graphQLPath = "/graphql"

// maxRequestBody bounds how much of a request body is read.
const maxRequestBody = 1 << 20

// Request is one request the fake endpoint saw. The GraphQL fields are best
// effort: a body that is not a JSON object leaves them zero and keeps Body, so
// a malformed request is still visible rather than silently dropped.
type Request struct {
	Method string
	Path   string
	// OperationName is the GraphQL operation name, which is what responses are
	// keyed on.
	OperationName string
	// Query is the GraphQL document. Tests assert on the fields a client asks
	// for: a selection set that quietly grows is how compatibility with an
	// older Stash is lost.
	Query string
	// Variables is the decoded variables object, nil when there was none. It is
	// where the identity-push assertions live.
	Variables map[string]any
	// APIKey is the credential header as received, so a test can prove one is
	// sent.
	APIKey string
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
	// RequireAPIKey answers a request carrying no credential header with 401.
	RequireAPIKey bool
}

// Server is a fake Stash endpoint.
type Server struct {
	srv *httptest.Server

	mu         sync.Mutex
	operations map[string][]Response
	fallback   *Response
	requireKey bool
	requests   []Request
}

// New starts a fake endpoint. The caller must Close it; tests should do that
// with t.Cleanup.
func New(opts Options) *Server {
	s := &Server{
		operations: make(map[string][]Response, len(opts.Operations)),
		fallback:   opts.Fallback,
		requireKey: opts.RequireAPIKey,
	}
	for op, queue := range opts.Operations {
		s.operations[op] = append([]Response(nil), queue...)
	}
	s.srv = httptest.NewServer(s)
	return s
}

// URL is the server root, ready to hand to a client as its base URL. The client
// appends /graphql itself, exactly as it does for a real Stash.
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

// Requests returns a copy of every request the endpoint has seen, oldest first.
func (s *Server) Requests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Request(nil), s.requests...)
}

// Operations returns just the requests for one operation name, oldest first. It
// is the "exactly one scoped scan" assertion's reader.
func (s *Server) Operations(op string) []Request {
	out := []Request{}
	for _, req := range s.Requests() {
		if req.OperationName == op {
			out = append(out, req)
		}
	}
	return out
}

// Count is how many requests the endpoint has seen. It is the assertion behind
// "a television import talks to Stash not at all": cmd/caravan's
// TestTelevisionImportTalksToStashNotAtAll runs a full import and job cycle and
// requires this to be 0.
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
		Method:   r.Method,
		Path:     r.URL.Path,
		APIKey:   r.Header.Get(apiKeyHeader),
		Body:     body,
		Received: time.Now(),
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

	// JSON by default, but a canned response's own headers win: a server
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

	if req.Method != http.MethodPost || req.Path != graphQLPath {
		return Response{
			Status: http.StatusNotFound,
			Body:   []byte(fmt.Sprintf(`{"errors":[{"message":"stashtest: no route for %s %s"}]}`, req.Method, req.Path)),
		}
	}
	if s.requireKey && req.APIKey == "" {
		return Unauthorized()
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
	return GraphQLError(fmt.Sprintf("no stub for operation %q", req.OperationName))
}

// Data wraps a `data` object in the GraphQL envelope and serves it with 200. The
// argument is the raw JSON of the data object.
func Data(raw string) Response {
	return Response{Body: []byte(`{"data":` + raw + `}`)}
}

// Status serves body with an explicit HTTP status, for the failures a server
// reports outside the GraphQL envelope.
func Status(status int, body string) Response {
	return Response{Status: status, Body: []byte(body)}
}

// GraphQLError serves a 200 carrying a one-entry errors array, which is how a
// GraphQL endpoint reports most failures.
func GraphQLError(message string) Response {
	body, err := json.Marshal(struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}{Errors: []struct {
		Message string `json:"message"`
	}{{Message: message}}})
	if err != nil {
		return Response{Body: []byte(`{"errors":[{"message":"stashtest: encode error"}]}`)}
	}
	return Response{Body: body}
}

// Unauthorized is the reply RequireAPIKey serves: a 401, which is what a Stash
// with authentication configured answers an anonymous GraphQL POST with.
func Unauthorized() Response {
	resp := GraphQLError("unauthorized")
	resp.Status = http.StatusUnauthorized
	return resp
}
