package tvmaze

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// response is one canned HTTP reply.
type response struct {
	status int
	body   []byte
	header http.Header
}

// recordedRequest is what the stub saw. TVmaze is REST, so the path is the
// routing key and therefore also what assertions identify a request by.
type recordedRequest struct {
	method        string
	path          string
	query         string
	authorization string
}

// stub is a fake TVmaze. Each path maps to a queue of responses consumed in
// order; the last one repeats, so a single-element queue answers any number of
// requests. It also records every wait the client asks for, which is how the
// throttle is tested without taking it.
type stub struct {
	mu       sync.Mutex
	routes   map[string][]response
	requests []recordedRequest
	waits    []time.Duration
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = append(s.requests, recordedRequest{
		method:        r.Method,
		path:          r.URL.Path,
		query:         r.URL.Query().Get("q"),
		authorization: r.Header.Get("Authorization"),
	})

	queue := s.routes[r.URL.Path]
	if len(queue) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"name":"Not Implemented","message":"no stub for this path","status":501}`))
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

func (s *stub) waited() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]time.Duration(nil), s.waits...)
}

// newStub returns a client pointed at a fake TVmaze serving routes. The throttle
// floor is zeroed and every wait is recorded instead of taken, so tests observe
// the pacing without running at its speed.
func newStub(t *testing.T, routes map[string][]response) (*Client, *stub) {
	t.Helper()

	s := &stub{routes: routes}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)

	c := New(srv.Client())
	c.BaseURL = srv.URL
	c.minInterval = 0
	c.sleep = func(_ context.Context, d time.Duration) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.waits = append(s.waits, d)
		return nil
	}
	return c, s
}

// fixture reads a recorded TVmaze response. It runs on the test goroutine, not
// the server's, so a missing file fails the test properly.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// okJSON serves a whole recorded document with 200.
func okJSON(t *testing.T, name string) response {
	t.Helper()
	return response{status: http.StatusOK, body: fixture(t, name)}
}

// showRoutes is the pair of documents one GetSeries reads.
func showRoutes(t *testing.T, id int) map[string][]response {
	t.Helper()
	return map[string][]response{
		showPath(id):     {okJSON(t, "show.json")},
		episodesPath(id): {okJSON(t, "show_episodes.json")},
	}
}

func TestSearchSendsTheQueryAndNoCredential(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		searchShowsPath: {okJSON(t, "search_shows.json")},
	})

	if _, err := c.SearchSeries(context.Background(), "breaking bad"); err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}

	got := s.seen()
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	req := got[0]
	if req.method != http.MethodGet {
		t.Errorf("method = %q, want GET", req.method)
	}
	if req.path != searchShowsPath {
		t.Errorf("path = %q, want %q", req.path, searchShowsPath)
	}
	if req.query != "breaking bad" {
		t.Errorf("q = %q, want the typed query", req.query)
	}
	// TVmaze's read API takes no credential, and sending an empty one is how a
	// client accidentally starts failing against an endpoint that validates it.
	if req.authorization != "" {
		t.Errorf("Authorization = %q, want no credential header at all", req.authorization)
	}
}

func TestGetSeriesReadsBothDocuments(t *testing.T) {
	c, s := newStub(t, showRoutes(t, 169))

	if _, err := c.GetSeries(context.Background(), "169"); err != nil {
		t.Fatalf("GetSeries: %v", err)
	}

	got := s.seen()
	if len(got) != 2 {
		t.Fatalf("requests = %d, want 2 (the show and its episodes)", len(got))
	}
	if got[0].path != "/shows/169" {
		t.Errorf("first path = %q, want /shows/169", got[0].path)
	}
	if got[1].path != "/shows/169/episodes" {
		t.Errorf("second path = %q, want /shows/169/episodes", got[1].path)
	}
}

func TestNotFound(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		showPath(999999): {{status: http.StatusNotFound, body: fixture(t, "error_not_found.json")}},
	})

	_, err := c.GetSeries(context.Background(), "999999")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSeries = %v, want ErrNotFound", err)
	}

	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("errors.As(*APIError) = false; err = %v", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
	if apiErr.Message != "Not Found" {
		t.Errorf("Message = %q, want TVmaze's own name for the condition", apiErr.Message)
	}
}

// A rejected credential is not a condition this provider can be in, see the
// package comment, so nothing here may claim it is:
// core.ErrMetadataUnauthorized is what puts "your API key is wrong" on screen,
// and TVmaze has no key to fix. A 401 from a keyless endpoint means it is
// refusing everyone.
func TestNoUnauthorizedSentinel(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		c, _ := newStub(t, map[string][]response{
			showPath(169): {{status: status, body: []byte(`{"name":"Unauthorized","message":"","status":` + strconv.Itoa(status) + `}`)}},
		})

		_, err := c.GetSeries(context.Background(), "169")
		if err == nil {
			t.Fatalf("GetSeries on %d = nil, want an error", status)
		}
		if errors.Is(err, core.ErrMetadataUnauthorized) {
			t.Errorf("a %d reported itself as a rejected credential: %v", status, err)
		}
	}
}

func TestRateLimitRetriesOnceAndSucceeds(t *testing.T) {
	throttled := response{
		status: http.StatusTooManyRequests,
		body:   fixture(t, "error_rate_limited.json"),
		header: http.Header{"Retry-After": {"3"}},
	}

	c, s := newStub(t, map[string][]response{
		showPath(169):     {throttled, okJSON(t, "show.json")},
		episodesPath(169): {okJSON(t, "show_episodes.json")},
	})

	got, err := c.GetSeries(context.Background(), "169")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.ProviderRef != "169" {
		t.Errorf("ProviderRef = %q, want 169", got.ProviderRef)
	}
	// The throttled attempt, its retry, and the episode list.
	if n := len(s.seen()); n != 3 {
		t.Errorf("requests = %d, want 3 (throttled + retry + episodes)", n)
	}
	// The back-off is recorded as a gate on the next send rather than slept on
	// inline, so the wait comes out of reserve and is a hair under the header's
	// three seconds.
	//
	// There are two of them because the stubbed sleep returns instantly: no
	// real time passes, so the episode list runs into the same gate the retry
	// did. That is the gate doing its job, it is what makes a concurrent caller
	// honor a refusal it was never told about, and against a real clock the
	// first wait satisfies it for both.
	waits := s.waited()
	if len(waits) != 2 {
		t.Fatalf("waits = %v, want the back-off and the still-open gate behind it", waits)
	}
	for i, d := range waits {
		if d > 3*time.Second || d < 2*time.Second {
			t.Errorf("wait %d = %v, want about the 3s Retry-After asked for", i, d)
		}
	}
}

func TestRateLimitGivesUpAfterOneRetry(t *testing.T) {
	throttled := response{status: http.StatusTooManyRequests, body: fixture(t, "error_rate_limited.json")}

	c, s := newStub(t, map[string][]response{
		showPath(169): {throttled},
	})

	_, err := c.GetSeries(context.Background(), "169")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("GetSeries = %v, want ErrRateLimited", err)
	}
	if n := len(s.seen()); n != 2 {
		t.Errorf("requests = %d, want 2 (original + one retry only)", n)
	}
}

func TestRateLimitRetryHonorsContext(t *testing.T) {
	throttled := response{
		status: http.StatusTooManyRequests,
		body:   fixture(t, "error_rate_limited.json"),
		header: http.Header{"Retry-After": {"600"}},
	}

	c, s := newStub(t, map[string][]response{
		showPath(169): {throttled, okJSON(t, "show.json")},
	})
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel as the back-off wait begins, then hand off to the real wait: with
	// a 600s Retry-After (capped to 60s), this test only finishes if sleepCtx
	// observes the cancellation and do() gives up instead of retrying.
	c.sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return sleepCtx(ctx, d)
	}

	_, err := c.GetSeries(ctx, "169")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := len(s.seen()); n != 1 {
		t.Errorf("requests = %d, want 1 (retry must not fire)", n)
	}
}

// The floor between sends is what keeps a refresh sweep, two requests per
// series, inside TVmaze's published budget.
func TestMinIntervalSpacesConsecutiveRequests(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		searchShowsPath: {okJSON(t, "search_shows.json")},
	})
	c.minInterval = 50 * time.Millisecond

	for i := 0; i < 3; i++ {
		if _, err := c.SearchSeries(context.Background(), "breaking bad"); err != nil {
			t.Fatalf("SearchSeries %d: %v", i, err)
		}
	}

	waits := s.waited()
	// The first request goes out immediately; each one after it waits.
	if len(waits) != 2 {
		t.Fatalf("waits = %v, want one per request after the first", waits)
	}
	// The stubbed sleep returns instantly, so no real time passes between the
	// three sends. Each wait therefore lands a further interval out, which is
	// the point: every request claims its own slot instead of all three queueing
	// behind one shared wait.
	for i, d := range waits {
		lo, hi := time.Duration(i)*c.minInterval, time.Duration(i+1)*c.minInterval
		if d <= lo || d > hi {
			t.Errorf("wait %d = %v, want it in (%v, %v]", i, d, lo, hi)
		}
	}
}

// TVmaze catalogues television and nothing else, so a chain walker has to be
// able to skip it rather than fail on it.
func TestMovieMethodsReportTheKindUnsupported(t *testing.T) {
	c, s := newStub(t, nil)

	movies, err := c.SearchMovies(context.Background(), "el camino")
	if !errors.Is(err, core.ErrProviderKindUnsupported) {
		t.Errorf("SearchMovies = %v, want ErrProviderKindUnsupported", err)
	}
	if movies != nil {
		t.Errorf("SearchMovies returned %v, want a nil slice", movies)
	}

	movie, err := c.GetMovie(context.Background(), "169")
	if !errors.Is(err, core.ErrProviderKindUnsupported) {
		t.Errorf("GetMovie = %v, want ErrProviderKindUnsupported", err)
	}
	if movie != nil {
		t.Errorf("GetMovie returned %+v, want nil", movie)
	}

	if seen := s.seen(); len(seen) != 0 {
		t.Errorf("an unsupported kind reached TVmaze as %+v", seen)
	}
}

// A ref this client cannot read is a wiring bug in Caravan, another provider's
// ref reached a TVmaze client, not a title TVmaze is missing. It must NOT read
// as ErrNotFound, which upstream parks a file as "unmatched" and moves on, and
// it must not cost a rate-limit token to discover.
func TestGetSeriesRejectsForeignRefsWithoutAsking(t *testing.T) {
	refs := []string{"9f3b1c2e-0000-4a5b-8c9d-1e2f3a4b5c6d", "", "tt0903747", "-4", "0"}
	for _, ref := range refs {
		c, s := newStub(t, nil)

		_, err := c.GetSeries(context.Background(), ref)
		if !errors.Is(err, ErrInvalidRef) {
			t.Errorf("GetSeries(%q) = %v, want ErrInvalidRef", ref, err)
		} else if errors.Is(err, ErrNotFound) {
			t.Errorf("GetSeries(%q) also reads as ErrNotFound", ref)
		}
		if seen := s.seen(); len(seen) != 0 {
			t.Errorf("ref %q reached TVmaze as %+v", ref, seen)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{name: "seconds", header: "3", want: 3 * time.Second},
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

func TestDecodeErrorIsReported(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		showPath(169): {{status: http.StatusOK, body: []byte("{not json")}},
	})

	_, err := c.GetSeries(context.Background(), "169")
	if err == nil {
		t.Fatal("GetSeries: want a decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode /shows/169") {
		t.Errorf("err = %v, want it to name the failing path", err)
	}
}

func TestTransportErrorNamesThePath(t *testing.T) {
	// A server that is already gone: c.hc.Do fails, and the *url.Error it
	// returns would otherwise bury the path under the address.
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()

	c := New(&http.Client{Timeout: time.Second})
	c.BaseURL = base
	c.minInterval = 0

	_, err := c.GetSeries(context.Background(), "169")
	if err == nil {
		t.Fatal("GetSeries: want a transport error, got nil")
	}
	if !strings.Contains(err.Error(), "/shows/169") {
		t.Errorf("err = %v, want it to name the failing path", err)
	}
}
