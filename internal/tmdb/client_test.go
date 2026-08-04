package tmdb

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// testAPIKey is the key every stubbed client sends; tests assert it never
// escapes into an error message.
const testAPIKey = "test-key-do-not-log"

// response is one canned HTTP reply.
type response struct {
	status int
	body   []byte
	header http.Header
}

// recordedRequest is what the stub saw.
type recordedRequest struct {
	path  string
	query url.Values
}

// stub is a fake TMDB. Each path maps to a queue of responses consumed in
// order; the last one repeats, so a single-element queue answers any number of
// requests.
type stub struct {
	mu       sync.Mutex
	routes   map[string][]response
	requests []recordedRequest
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = append(s.requests, recordedRequest{path: r.URL.Path, query: r.URL.Query()})

	queue := s.routes[r.URL.Path]
	if len(queue) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"status_message":"no stub for this path"}`))
		return
	}
	resp := queue[0]
	if len(queue) > 1 {
		s.routes[r.URL.Path] = queue[1:]
	}

	for k, vs := range resp.header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.status)
	_, _ = w.Write(resp.body)
}

func (s *stub) seen() []recordedRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]recordedRequest(nil), s.requests...)
}

// newStub returns a client pointed at a fake TMDB serving routes. The retry
// delay is stubbed out so tests never wait.
func newStub(t *testing.T, routes map[string][]response) (*Client, *stub) {
	t.Helper()

	s := &stub{routes: routes}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)

	c := New(testAPIKey, srv.Client())
	c.BaseURL = srv.URL
	c.sleep = func(context.Context, time.Duration) error { return nil }
	return c, s
}

// okJSON serves a fixture with 200.
func okJSON(t *testing.T, name string) response {
	t.Helper()
	return response{status: http.StatusOK, body: fixture(t, name)}
}

// errJSON serves a fixture with a non-2xx status.
func errJSON(t *testing.T, status int, name string) response {
	t.Helper()
	return response{status: status, body: fixture(t, name)}
}

// fixture reads a recorded TMDB response. It runs on the test goroutine, not
// the server's, so a missing file fails the test properly.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestGetSendsAPIKeyAndPath(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/movie/78": {okJSON(t, "movie_detail.json")},
	})

	if _, err := c.GetMovie(context.Background(), 78); err != nil {
		t.Fatalf("GetMovie: %v", err)
	}

	got := s.seen()
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	if got[0].path != "/movie/78" {
		t.Errorf("path = %q, want /movie/78", got[0].path)
	}
	if key := got[0].query.Get("api_key"); key != testAPIKey {
		t.Errorf("api_key = %q, want %q", key, testAPIKey)
	}
}

func TestSearchSendsQuery(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		"/search/movie": {okJSON(t, "search_movie.json")},
	})

	if _, err := c.SearchMovies(context.Background(), "blade runner"); err != nil {
		t.Fatalf("SearchMovies: %v", err)
	}

	got := s.seen()
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	if q := got[0].query.Get("query"); q != "blade runner" {
		t.Errorf("query = %q, want %q", q, "blade runner")
	}
}

func TestAPIErrors(t *testing.T) {
	tests := []struct {
		name     string
		status   int
		fixture  string
		wantErr  error
		wantCode int
		wantMsg  string
	}{
		{
			name:     "not found",
			status:   http.StatusNotFound,
			fixture:  "error_404.json",
			wantErr:  ErrNotFound,
			wantCode: 34,
			wantMsg:  "The resource you requested could not be found.",
		},
		{
			name:     "unauthorized",
			status:   http.StatusUnauthorized,
			fixture:  "error_401.json",
			wantErr:  ErrUnauthorized,
			wantCode: 7,
			wantMsg:  "Invalid API key: You must be granted a valid key.",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newStub(t, map[string][]response{
				"/movie/78": {errJSON(t, tt.status, tt.fixture)},
			})

			_, err := c.GetMovie(context.Background(), 78)
			if err == nil {
				t.Fatal("GetMovie: want error, got nil")
			}
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("errors.Is(err, %v) = false; err = %v", tt.wantErr, err)
			}

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("errors.As(*APIError) = false; err = %v", err)
			}
			if apiErr.StatusCode != tt.status {
				t.Errorf("StatusCode = %d, want %d", apiErr.StatusCode, tt.status)
			}
			if apiErr.Code != tt.wantCode {
				t.Errorf("Code = %d, want %d", apiErr.Code, tt.wantCode)
			}
			if apiErr.Message != tt.wantMsg {
				t.Errorf("Message = %q, want %q", apiErr.Message, tt.wantMsg)
			}
		})
	}
}

func TestRateLimitRetriesOnceAndSucceeds(t *testing.T) {
	throttled := errJSON(t, http.StatusTooManyRequests, "error_429.json")
	throttled.header = http.Header{"Retry-After": {"3"}}

	c, s := newStub(t, map[string][]response{
		"/movie/78": {throttled, okJSON(t, "movie_detail.json")},
	})

	var waited time.Duration
	c.sleep = func(_ context.Context, d time.Duration) error {
		waited = d
		return nil
	}

	m, err := c.GetMovie(context.Background(), 78)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if m.TMDBID != 78 {
		t.Errorf("TMDBID = %d, want 78", m.TMDBID)
	}
	if n := len(s.seen()); n != 2 {
		t.Errorf("requests = %d, want 2 (original + retry)", n)
	}
	if waited != 3*time.Second {
		t.Errorf("waited %v, want 3s from Retry-After", waited)
	}
}

func TestRateLimitGivesUpAfterOneRetry(t *testing.T) {
	throttled := errJSON(t, http.StatusTooManyRequests, "error_429.json")

	c, s := newStub(t, map[string][]response{
		"/movie/78": {throttled},
	})

	_, err := c.GetMovie(context.Background(), 78)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("errors.Is(err, ErrRateLimited) = false; err = %v", err)
	}
	if n := len(s.seen()); n != 2 {
		t.Errorf("requests = %d, want 2 (original + one retry only)", n)
	}
}

func TestRateLimitRetryHonorsContext(t *testing.T) {
	throttled := errJSON(t, http.StatusTooManyRequests, "error_429.json")
	throttled.header = http.Header{"Retry-After": {"600"}}

	c, s := newStub(t, map[string][]response{
		"/movie/78": {throttled, okJSON(t, "movie_detail.json")},
	})
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel as the retry wait begins, then hand off to the real wait: with
	// a 600s Retry-After, this test only finishes if sleepCtx observes the
	// cancellation and do() gives up instead of retrying.
	c.sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return sleepCtx(ctx, d)
	}

	_, err := c.GetMovie(ctx, 78)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := len(s.seen()); n != 1 {
		t.Errorf("requests = %d, want 1 (retry must not fire)", n)
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

func TestPosterURL(t *testing.T) {
	tests := []struct {
		name  string
		base  string
		path  string
		want  string
		blank bool
	}{
		{
			name: "default base",
			base: DefaultImageBaseURL,
			path: "/63N9uy8nd9j7Eog2axPQ8lbr3Wj.jpg",
			want: "https://image.tmdb.org/t/p/w500/63N9uy8nd9j7Eog2axPQ8lbr3Wj.jpg",
		},
		{
			name: "trailing slash on base",
			base: "https://images.example/t/p/w500/",
			path: "/a.jpg",
			want: "https://images.example/t/p/w500/a.jpg",
		},
		{
			name: "path without leading slash",
			base: DefaultImageBaseURL,
			path: "a.jpg",
			want: "https://image.tmdb.org/t/p/w500/a.jpg",
		},
		{
			name: "no poster stays empty",
			base: DefaultImageBaseURL,
			path: "",
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := New("k", nil)
			c.ImageBaseURL = tt.base
			if got := c.posterURL(tt.path); got != tt.want {
				t.Errorf("posterURL(%q) = %q, want %q", tt.path, got, tt.want)
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
		{name: "full date", in: "1982-06-25", want: time.Date(1982, 6, 25, 0, 0, 0, 0, time.UTC)},
		{name: "empty", in: "", want: time.Time{}},
		{name: "malformed", in: "not-a-date", want: time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseDate(tt.in); !got.Equal(tt.want) {
				t.Errorf("parseDate(%q) = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

func TestDecodeErrorIsReported(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		"/movie/78": {{status: http.StatusOK, body: []byte("{not json")}},
	})

	_, err := c.GetMovie(context.Background(), 78)
	if err == nil {
		t.Fatal("GetMovie: want decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode /movie/78") {
		t.Errorf("err = %v, want it to name the failing path", err)
	}
}

func TestTransportErrorDoesNotLeakAPIKey(t *testing.T) {
	// A server that is already gone: c.hc.Do fails, and the *url.Error it
	// returns would otherwise carry the full URL — api_key included.
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()

	c := New(testAPIKey, &http.Client{Timeout: time.Second})
	c.BaseURL = base

	_, err := c.GetMovie(context.Background(), 78)
	if err == nil {
		t.Fatal("GetMovie: want transport error, got nil")
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Errorf("error message leaks the api key: %v", err)
	}
	if !strings.Contains(err.Error(), "/movie/78") {
		t.Errorf("err = %v, want it to name the failing path", err)
	}
}

// TestTestProvesTheKey covers the credential check behind Settings → Metadata
// and the first-run wizard (PLAN phase 10 task 4).
func TestTestProvesTheKey(t *testing.T) {
	c, stub := newStub(t, map[string][]response{
		"/configuration": {{status: http.StatusOK, body: []byte(`{"images":{}}`)}},
	})

	if err := c.Test(context.Background()); err != nil {
		t.Fatalf("Test with a good key = %v, want nil", err)
	}
	seen := stub.seen()
	if len(seen) != 1 || seen[0].path != "/configuration" {
		t.Fatalf("Test requested %v, want one /configuration call", seen)
	}
	if seen[0].query.Get("api_key") != testAPIKey {
		t.Errorf("Test sent api_key=%q, want the client's key", seen[0].query.Get("api_key"))
	}
}

// A rejected key has to be distinguishable from an unreachable TMDB all the way
// up in internal/api, which never imports this package: that is what the core
// sentinel is for.
func TestTestReportsARejectedKeyAsUnauthorized(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		"/configuration": {{
			status: http.StatusUnauthorized,
			body:   []byte(`{"status_code":7,"status_message":"Invalid API key: You must be granted a valid key."}`),
		}},
	})

	err := c.Test(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Test with a bad key = %v, want ErrUnauthorized", err)
	}
	if !errors.Is(err, core.ErrMetadataUnauthorized) {
		t.Fatalf("Test with a bad key = %v, want it to wrap core.ErrMetadataUnauthorized", err)
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Errorf("the API key leaked into the error: %q", err)
	}
}

// A TMDB that is merely down must NOT read as a wrong credential, or the UI
// sends people to fix a key that is fine.
func TestTestDoesNotConfuseAnOutageWithABadKey(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		"/configuration": {{status: http.StatusBadGateway, body: []byte(`<html>bad gateway</html>`)}},
	})

	err := c.Test(context.Background())
	if err == nil {
		t.Fatal("Test against a broken TMDB = nil, want an error")
	}
	if errors.Is(err, core.ErrMetadataUnauthorized) {
		t.Fatalf("a 502 reported itself as a rejected credential: %v", err)
	}
}
