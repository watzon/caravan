package stashboxtest

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
)

// post sends a raw body to the fake endpoint and returns the reply's status.
func post(t *testing.T, s *Server, body string) *http.Response {
	t.Helper()
	resp, err := s.Client().Post(s.URL(), "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("post: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestFreshServerHasSeenNothing(t *testing.T) {
	// The shape of the zero-traffic assertion: with the adult module disabled, a
	// full job cycle leaves Count at zero.
	s := New(Options{})
	t.Cleanup(s.Close)

	if n := s.Count(); n != 0 {
		t.Errorf("Count = %d, want 0 on a server nothing has called", n)
	}
	if got := s.Requests(); len(got) != 0 {
		t.Errorf("Requests = %v, want empty", got)
	}
}

func TestEveryRequestIsRecordedEvenWhenUnanswerable(t *testing.T) {
	// An unanswerable request is still traffic, and traffic is the thing being
	// measured. Each of these would be easy to drop on the floor.
	s := New(Options{RequireAPIKey: true})
	t.Cleanup(s.Close)

	post(t, s, `{"operationName":"NoSuchOperation","query":"query NoSuchOperation { x }"}`)
	post(t, s, `not json at all`)
	post(t, s, ``)

	req, err := http.NewRequest(http.MethodGet, s.URL()+"/graphql", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := s.Client().Do(req)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	resp.Body.Close()

	got := s.Requests()
	if len(got) != 4 {
		t.Fatalf("Count = %d, want 4 (unknown operation, malformed body, empty body, wrong method)", len(got))
	}
	if got[0].OperationName != "NoSuchOperation" {
		t.Errorf("request 0 operation = %q, want NoSuchOperation", got[0].OperationName)
	}
	if string(got[1].Body) != "not json at all" {
		t.Errorf("request 1 body = %q, want the raw unparseable body kept", got[1].Body)
	}
	if got[1].OperationName != "" || got[1].Query != "" {
		t.Errorf("request 1 parsed fields = (%q, %q), want both empty for a malformed body", got[1].OperationName, got[1].Query)
	}
	if got[3].Method != http.MethodGet || got[3].Path != "/graphql" {
		t.Errorf("request 3 = %s %s, want GET /graphql", got[3].Method, got[3].Path)
	}
	if !got[0].Received.Before(got[3].Received) && !got[0].Received.Equal(got[3].Received) {
		t.Errorf("Received timestamps are not ordered: %v then %v", got[0].Received, got[3].Received)
	}
}

func TestMissingStubIsAReadableGraphQLError(t *testing.T) {
	s := New(Options{})
	t.Cleanup(s.Close)

	resp := post(t, s, `{"operationName":"FindScene"}`)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200: GraphQL reports this in the body", resp.StatusCode)
	}

	var envelope struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(envelope.Errors) != 1 || !strings.Contains(envelope.Errors[0].Message, `"FindScene"`) {
		t.Errorf("errors = %+v, want one naming the missing stub", envelope.Errors)
	}
}

func TestQueueIsConsumedInOrderAndTheLastRepeats(t *testing.T) {
	s := New(Options{Operations: map[string][]Response{
		"Op": {Raw([]byte(`{"data":{"n":1}}`)), Raw([]byte(`{"data":{"n":2}}`))},
	}})
	t.Cleanup(s.Close)

	want := []string{`{"data":{"n":1}}`, `{"data":{"n":2}}`, `{"data":{"n":2}}`}
	for i, w := range want {
		body, err := io.ReadAll(post(t, s, `{"operationName":"Op"}`).Body)
		if err != nil {
			t.Fatalf("read %d: %v", i, err)
		}
		if string(body) != w {
			t.Errorf("reply %d = %s, want %s", i, body, w)
		}
	}
}

func TestRequireAPIKeyRejectsAnonymousButStillRecords(t *testing.T) {
	s := New(Options{
		RequireAPIKey: true,
		Operations:    map[string][]Response{"Op": {Raw([]byte(`{"data":{}}`))}},
	})
	t.Cleanup(s.Close)

	resp := post(t, s, `{"operationName":"Op"}`)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
	if n := s.Count(); n != 1 {
		t.Errorf("Count = %d, want 1: a rejected request is still traffic", n)
	}

	// A bearer token satisfies it too: the client sends both headers, and an
	// endpoint that reads either must be representable.
	req, err := http.NewRequest(http.MethodPost, s.URL(), strings.NewReader(`{"operationName":"Op"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer k")
	authed, err := s.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer authed.Body.Close()
	if authed.StatusCode != http.StatusOK {
		t.Errorf("status with a bearer token = %d, want 200", authed.StatusCode)
	}
}

func TestResetClearsRequestsButNotStubs(t *testing.T) {
	s := New(Options{Operations: map[string][]Response{"Op": {Raw([]byte(`{"data":{}}`))}}})
	t.Cleanup(s.Close)

	post(t, s, `{"operationName":"Op"}`)
	s.Reset()
	if n := s.Count(); n != 0 {
		t.Fatalf("Count after Reset = %d, want 0", n)
	}

	resp := post(t, s, `{"operationName":"Op"}`)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200: Reset must not drop the stubs", resp.StatusCode)
	}
}

func TestSetOperationReplacesAndRemoves(t *testing.T) {
	s := New(Options{Operations: map[string][]Response{"Op": {Raw([]byte(`{"data":{"n":1}}`))}}})
	t.Cleanup(s.Close)

	s.SetOperation("Op", Status(http.StatusTeapot, []byte(`{"errors":[]}`)))
	if got := post(t, s, `{"operationName":"Op"}`).StatusCode; got != http.StatusTeapot {
		t.Errorf("status = %d, want 418 after SetOperation", got)
	}

	s.SetOperation("Op")
	resp := post(t, s, `{"operationName":"Op"}`)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want the fallback's 200 after the queue was removed", resp.StatusCode)
	}
}

func TestConcurrentRequestsAreAllRecordedExactlyOnce(t *testing.T) {
	// The runner fans out across workers, so the zero-traffic assertion has to
	// hold under concurrency: a dropped or double-counted request would make it
	// lie in either direction.
	s := New(Options{Operations: map[string][]Response{"Op": {Raw([]byte(`{"data":{}}`))}}})
	t.Cleanup(s.Close)

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			resp, err := s.Client().Post(s.URL(), "application/json", strings.NewReader(`{"operationName":"Op"}`))
			if err != nil {
				return
			}
			resp.Body.Close()
		}()
	}
	wg.Wait()

	if got := s.Count(); got != n {
		t.Errorf("Count = %d, want %d", got, n)
	}
}

func TestWithoutQueryStudiosMimicsTPDB(t *testing.T) {
	// TPDB has no queryStudios in its schema and does not answer the query with
	// a GraphQL validation error: it answers with a bare HTTP 500 whose body is
	// the plain text "Server Error". Every other operation keeps working, which
	// is the whole reason a client can fall back at all.
	s := New(Options{
		WithoutQueryStudios: true,
		Operations: map[string][]Response{
			"SearchSitesByScene": {Data([]byte(`{"searchScene":[]}`))},
		},
	})
	t.Cleanup(s.Close)

	resp := post(t, s, `{"operationName":"SearchSites","query":"query SearchSites { queryStudios(input: $input) { count } }"}`)
	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, "text/plain") {
		t.Errorf("Content-Type = %q, want text/plain: the body is not JSON", got)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if string(body) != "Server Error" {
		t.Errorf("body = %q, want the plain-text %q", body, "Server Error")
	}

	// The operation the client falls back to is untouched.
	fallback := post(t, s, `{"operationName":"SearchSitesByScene","query":"query SearchSitesByScene { searchScene(term: $term) { id } }"}`)
	if fallback.StatusCode != http.StatusOK {
		t.Errorf("fallback status = %d, want 200", fallback.StatusCode)
	}

	// Both are still traffic, and traffic is the thing being measured.
	if n := s.Count(); n != 2 {
		t.Errorf("Count = %d, want 2", n)
	}
}

func TestSiteIndexAnswersTheRESTRouteAndRecordsIt(t *testing.T) {
	// TPDB's REST site index is not GraphQL and carries no operation name, so it
	// is routed by path. A test opts into it exactly as it opts into a missing
	// queryStudios.
	s := New(Options{
		SiteIndex: []Response{Raw([]byte(`{"data":[{"uuid":"u-1","name":"Brazzers"}]}`))},
	})
	t.Cleanup(s.Close)

	resp, err := s.Client().Get(s.URL() + "/sites?q=brazzers&per_page=25")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "Brazzers") {
		t.Errorf("body = %q, want the canned rows", body)
	}

	// The search term lives in the query string, which is why requests record it.
	got := s.Requests()
	if len(got) != 1 {
		t.Fatalf("Count = %d, want 1", len(got))
	}
	if got[0].Path != "/sites" || got[0].RawQuery != "q=brazzers&per_page=25" {
		t.Errorf("recorded %q?%q, want the REST route and its query", got[0].Path, got[0].RawQuery)
	}
	if got[0].Method != http.MethodGet {
		t.Errorf("method = %q, want GET", got[0].Method)
	}
}

func TestWithoutASiteIndexTheRESTRouteIsAbsent(t *testing.T) {
	// Every stash-box but TPDB's has no REST side at all, and that is the
	// default: a client that asks anyway gets a 404, and the attempt is still
	// recorded as traffic.
	s := New(Options{})
	t.Cleanup(s.Close)

	resp, err := s.Client().Get(s.URL() + "/sites?q=x")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
	if n := s.Count(); n != 1 {
		t.Errorf("Count = %d, want 1: an unanswerable request is still traffic", n)
	}
}
