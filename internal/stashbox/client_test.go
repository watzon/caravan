package stashbox

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/stashbox/stashboxtest"
)

// testAPIKey is the key every stubbed client sends; tests assert it never
// escapes into an error message.
const testAPIKey = "test-key-do-not-log"

// newStub returns a client pointed at a fake stash-box serving ops. The retry
// delay is stubbed out so tests never wait.
func newStub(t *testing.T, ops map[string][]stashboxtest.Response) (*Client, *stashboxtest.Server) {
	t.Helper()

	srv := stashboxtest.New(stashboxtest.Options{Operations: ops})
	t.Cleanup(srv.Close)

	c := New(testAPIKey, srv.URL(), srv.Client())
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c, srv
}

// fixture reads a recorded stash-box response. It runs on the test goroutine,
// not the server's, so a missing file fails the test properly.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// okFixture serves a whole recorded GraphQL envelope with 200.
func okFixture(t *testing.T, name string) stashboxtest.Response {
	t.Helper()
	return stashboxtest.Raw(fixture(t, name))
}

// errFixture serves a recorded envelope with a non-2xx status.
func errFixture(t *testing.T, status int, name string) stashboxtest.Response {
	t.Helper()
	return stashboxtest.Status(status, fixture(t, name))
}

func TestNewDefaultsToTPDBEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		endpoint string
		want     string
	}{
		{name: "blank falls back to the preset", endpoint: "", want: DefaultEndpoint},
		{name: "whitespace falls back too", endpoint: "   ", want: DefaultEndpoint},
		{name: "explicit endpoint is kept", endpoint: "https://stashdb.org/graphql", want: "https://stashdb.org/graphql"},
		{name: "surrounding space is trimmed", endpoint: " https://fansdb.cc/graphql ", want: "https://fansdb.cc/graphql"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := New("k", tt.endpoint, nil).Endpoint; got != tt.want {
				t.Errorf("Endpoint = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestQuerySendsOperationAndCredentials(t *testing.T) {
	c, s := newStub(t, map[string][]stashboxtest.Response{
		opFindScene: {okFixture(t, "find_scene.json")},
	})

	if _, err := c.GetScene(context.Background(), "5c5c5c5c-1111-4111-8111-aaaaaaaaaaaa"); err != nil {
		t.Fatalf("GetScene: %v", err)
	}

	got := s.Requests()
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	req := got[0]
	if req.Method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.Method)
	}
	if req.OperationName != opFindScene {
		t.Errorf("operationName = %q, want %q", req.OperationName, opFindScene)
	}
	if !strings.Contains(req.Query, "findScene(id: $id)") {
		t.Errorf("query does not call findScene: %s", req.Query)
	}
	if id := req.Variables["id"]; id != "5c5c5c5c-1111-4111-8111-aaaaaaaaaaaa" {
		t.Errorf("variables.id = %v, want the requested scene id", id)
	}
	// Both credential headers go out: stash-box reads ApiKey, TPDB reads a
	// bearer token, and one client has to satisfy every endpoint in the preset
	// list without a dialect branch.
	if req.APIKey != testAPIKey {
		t.Errorf("%s header = %q, want %q", APIKeyHeader, req.APIKey, testAPIKey)
	}
	if want := "Bearer " + testAPIKey; req.Authorization != want {
		t.Errorf("Authorization = %q, want %q", req.Authorization, want)
	}
}

func TestEmptyAPIKeySendsNoCredential(t *testing.T) {
	srv := stashboxtest.New(stashboxtest.Options{
		Operations: map[string][]stashboxtest.Response{
			opFindScene: {okFixture(t, "find_scene.json")},
		},
	})
	t.Cleanup(srv.Close)

	c := New("", srv.URL(), srv.Client())
	if _, err := c.GetScene(context.Background(), "5c5c5c5c-1111-4111-8111-aaaaaaaaaaaa"); err != nil {
		t.Fatalf("GetScene: %v", err)
	}

	req := srv.Requests()[0]
	if req.APIKey != "" || req.Authorization != "" {
		t.Errorf("credential headers = (%q, %q), want both empty: a blank key is rejected outright by endpoints that allow anonymous reads",
			req.APIKey, req.Authorization)
	}
}

func TestAPIKeyHeaderMatchesTheFakeEndpoint(t *testing.T) {
	// stashboxtest deliberately imports nothing of Caravan's, so it carries its
	// own copy of the header name. This is the assertion that keeps the two in
	// step; without it a rename here would silently stop every fake-endpoint
	// credential assertion from seeing anything.
	srv := stashboxtest.New(stashboxtest.Options{RequireAPIKey: true})
	t.Cleanup(srv.Close)

	req, err := http.NewRequest(http.MethodPost, srv.URL(), strings.NewReader(`{"operationName":"Ping"}`))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set(APIKeyHeader, "k")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		t.Fatalf("the fake endpoint did not recognise the %s header the client sends", APIKeyHeader)
	}
	if got := srv.Requests()[0].APIKey; got != "k" {
		t.Errorf("recorded APIKey = %q, want %q", got, "k")
	}
}

func TestGraphQLErrorsMapToSentinels(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		fixture  string
		wantErr  error
		wantCode string
		wantMsg  string
	}{
		{
			name:     "unauthenticated in a 200 body",
			status:   http.StatusOK,
			fixture:  "error_unauthorized.json",
			wantErr:  ErrUnauthorized,
			wantCode: codeUnauthenticated,
			wantMsg:  "invalid or missing api key",
		},
		{
			name:     "not found in a 200 body",
			status:   http.StatusOK,
			fixture:  "error_not_found.json",
			wantErr:  ErrNotFound,
			wantCode: codeNotFound,
			wantMsg:  "no scene with that id",
		},
		{
			name:     "rate limited in a 200 body",
			status:   http.StatusOK,
			fixture:  "error_rate_limited.json",
			wantErr:  ErrRateLimited,
			wantCode: codeRateLimited,
			wantMsg:  "too many requests",
		},
		{
			// An endpoint that rejects a field this client selects must surface
			// as a readable error naming the field, not as a decode failure:
			// dialect drift is the failure mode PLAN phase 9 calls out.
			name:     "schema mismatch",
			status:   http.StatusOK,
			fixture:  "error_validation.json",
			wantErr:  nil,
			wantCode: "GRAPHQL_VALIDATION_FAILED",
			wantMsg:  `Cannot query field "code" on type "Scene".`,
		},
		{
			name:     "http 401 with a body",
			status:   http.StatusUnauthorized,
			fixture:  "error_unauthorized.json",
			wantErr:  ErrUnauthorized,
			wantCode: codeUnauthenticated,
			wantMsg:  "invalid or missing api key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newStub(t, map[string][]stashboxtest.Response{
				opFindScene: {errFixture(t, tt.status, tt.fixture)},
			})

			_, err := c.GetScene(context.Background(), "abc")
			if err == nil {
				t.Fatal("GetScene: want error, got nil")
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("errors.Is(err, %v) = false; err = %v", tt.wantErr, err)
			}

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("errors.As(*APIError) = false; err = %v", err)
			}
			if apiErr.Operation != opFindScene {
				t.Errorf("Operation = %q, want %q", apiErr.Operation, opFindScene)
			}
			if apiErr.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tt.status)
			}
			if apiErr.Code != tt.wantCode {
				t.Errorf("Code = %q, want %q", apiErr.Code, tt.wantCode)
			}
			if apiErr.Message != tt.wantMsg {
				t.Errorf("Message = %q, want %q", apiErr.Message, tt.wantMsg)
			}
		})
	}
}

func TestHTTPStatusWithoutGraphQLBody(t *testing.T) {
	// A proxy in front of the endpoint answers with HTML. That is "the endpoint
	// is down", not "decode failed", and it must not be reported as the latter.
	c, _ := newStub(t, map[string][]stashboxtest.Response{
		opFindSite: {stashboxtest.Status(http.StatusBadGateway, []byte("<html>502</html>"))},
	})

	_, err := c.GetSite(context.Background(), "abc")
	if err == nil {
		t.Fatal("GetSite: want error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As(*APIError) = false; err = %v", err)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Errorf("StatusCode = %d, want 502", apiErr.StatusCode)
	}
	if apiErr.Message != http.StatusText(http.StatusBadGateway) {
		t.Errorf("Message = %q, want the status text", apiErr.Message)
	}
	if strings.Contains(err.Error(), "decode") {
		t.Errorf("err = %v, want it to report the status rather than a decode failure", err)
	}
}

func TestRateLimitRetriesOnceAndSucceeds(t *testing.T) {
	c, s := newStub(t, map[string][]stashboxtest.Response{
		opFindScene: {stashboxtest.RateLimited(3), okFixture(t, "find_scene.json")},
	})

	var waited time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error {
		waited = d
		return nil
	}

	scene, err := c.GetScene(context.Background(), "5c5c5c5c-1111-4111-8111-aaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("GetScene: %v", err)
	}
	if scene.Title != "The Long Way Home" {
		t.Errorf("Title = %q, want the retried response's scene", scene.Title)
	}
	if n := s.Count(); n != 2 {
		t.Errorf("requests = %d, want 2 (original + retry)", n)
	}
	if waited != 3*time.Second {
		t.Errorf("waited %v, want 3s from Retry-After", waited)
	}
}

func TestRateLimitGivesUpAfterOneRetry(t *testing.T) {
	c, s := newStub(t, map[string][]stashboxtest.Response{
		opFindScene: {stashboxtest.RateLimited(1)},
	})

	_, err := c.GetScene(context.Background(), "abc")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("errors.Is(err, ErrRateLimited) = false; err = %v", err)
	}
	if n := s.Count(); n != 2 {
		t.Errorf("requests = %d, want 2 (original + one retry only)", n)
	}
}

func TestRateLimitRetryHonorsContext(t *testing.T) {
	c, s := newStub(t, map[string][]stashboxtest.Response{
		opFindScene: {stashboxtest.RateLimited(600), okFixture(t, "find_scene.json")},
	})

	ctx, cancel := context.WithCancel(context.Background())
	// Cancel as the retry wait begins, then hand off to the real wait: with a
	// 600s Retry-After, this test only finishes if sleepCtx observes the
	// cancellation and do() gives up instead of retrying.
	c.sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return sleepCtx(ctx, d)
	}

	_, err := c.GetScene(ctx, "abc")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := s.Count(); n != 1 {
		t.Errorf("requests = %d, want 1 (retry must not fire)", n)
	}
}

func TestDecodeErrorNamesTheOperation(t *testing.T) {
	c, _ := newStub(t, map[string][]stashboxtest.Response{
		opSearchSites: {stashboxtest.Raw([]byte("{not json"))},
	})

	_, err := c.SearchSites(context.Background(), "tushy")
	if err == nil {
		t.Fatal("SearchSites: want decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode "+opSearchSites) {
		t.Errorf("err = %v, want it to name the failing operation", err)
	}
}

func TestEmptyEnvelopeIsAnError(t *testing.T) {
	// A 200 with neither data nor errors is a broken endpoint. An *empty
	// result* is `{"data":{"findScene":null}}`, which is a different case and
	// is covered by TestGetSceneNotFound.
	c, _ := newStub(t, map[string][]stashboxtest.Response{
		opFindScene: {stashboxtest.Raw([]byte(`{}`))},
	})

	_, err := c.GetScene(context.Background(), "abc")
	if err == nil {
		t.Fatal("GetScene: want error, got nil")
	}
	if !strings.Contains(err.Error(), "carried no data") {
		t.Errorf("err = %v, want it to report an empty response", err)
	}
}

func TestTransportErrorDoesNotLeakAPIKey(t *testing.T) {
	// A server that is already gone: c.hc.Do fails, and the *url.Error it
	// returns would otherwise carry the full endpoint address.
	srv := httptest.NewServer(http.NotFoundHandler())
	endpoint := srv.URL
	srv.Close()

	c := New(testAPIKey, endpoint, &http.Client{Timeout: time.Second})

	_, err := c.GetScene(context.Background(), "abc")
	if err == nil {
		t.Fatal("GetScene: want transport error, got nil")
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Errorf("error message leaks the api key: %v", err)
	}
	if !strings.Contains(err.Error(), opFindScene) {
		t.Errorf("err = %v, want it to name the failing operation", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{name: "seconds", header: "5", want: 5 * time.Second},
		{name: "padded", header: " 2 ", want: 2 * time.Second},
		{name: "zero", header: "0", want: 0},
		{name: "missing", header: "", want: fallbackRetryAfter},
		{name: "http date unsupported", header: "Wed, 21 Oct 2015 07:28:00 GMT", want: fallbackRetryAfter},
		{name: "negative", header: "-1", want: fallbackRetryAfter},
		{name: "capped", header: "99999", want: maxRetryAfter},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseRetryAfter(tt.header); got != tt.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tt.header, got, tt.want)
			}
		})
	}
}

func TestParseDate(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want time.Time
	}{
		{name: "full date", in: "2023-11-04", want: time.Date(2023, 11, 4, 0, 0, 0, 0, time.UTC)},
		{name: "padded", in: " 2023-11-04 ", want: time.Date(2023, 11, 4, 0, 0, 0, 0, time.UTC)},
		// Community-edited records carry partial dates. They widen to the start
		// of the period, which still files a scene under the right season —
		// the season is the year.
		{name: "year and month", in: "2019-05", want: time.Date(2019, 5, 1, 0, 0, 0, 0, time.UTC)},
		{name: "year only", in: "2019", want: time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC)},
		{name: "empty", in: "", want: time.Time{}},
		{name: "malformed", in: "not-a-date", want: time.Time{}},
		{name: "day out of range", in: "2019-02-30", want: time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseDate(tt.in); !got.Equal(tt.want) {
				t.Errorf("parseDate(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestCoverURL(t *testing.T) {
	tests := []struct {
		name   string
		images []imageResult
		want   string
	}{
		{
			name: "widest wins regardless of order",
			images: []imageResult{
				{URL: "thumb.jpg", Width: 320, Height: 180},
				{URL: "cover.jpg", Width: 1920, Height: 1080},
				{URL: "mid.jpg", Width: 800, Height: 450},
			},
			want: "cover.jpg",
		},
		{
			// A stable choice matters: a cover that shuffles between refreshes
			// would re-download art forever.
			name: "ties keep the first",
			images: []imageResult{
				{URL: "a.jpg", Width: 500},
				{URL: "b.jpg", Width: 500},
			},
			want: "a.jpg",
		},
		{
			name:   "no images means no artwork",
			images: nil,
			want:   "",
		},
		{
			name:   "blank urls are skipped",
			images: []imageResult{{URL: "", Width: 4000}, {URL: "real.jpg", Width: 10}},
			want:   "real.jpg",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := coverURL(tt.images); got != tt.want {
				t.Errorf("coverURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFirstURL(t *testing.T) {
	tests := []struct {
		name string
		urls []urlResult
		want string
	}{
		{name: "first wins", urls: []urlResult{{URL: "a"}, {URL: "b"}}, want: "a"},
		{name: "blanks are skipped", urls: []urlResult{{URL: ""}, {URL: "b"}}, want: "b"},
		{name: "none", urls: nil, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := firstURL(tt.urls); got != tt.want {
				t.Errorf("firstURL = %q, want %q", got, tt.want)
			}
		})
	}
}
