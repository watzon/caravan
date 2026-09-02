package thetvdb

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// The tokens testdata/login.json and testdata/login_refreshed.json hand out, in
// that order. They are named here because most of what this file asserts is
// which of the two a request carried.
const (
	firstToken   = "tvdb-token-1"
	refreshToken = "tvdb-token-2"
)

// response is one canned HTTP reply.
type response struct {
	status int
	body   []byte
	header http.Header
}

// recordedRequest is what the stub saw. TheTVDB v4 is REST, so the path is the
// routing key and therefore also what assertions identify a request by. The
// bearer token and the login body are recorded because they are the two things
// this client's auth is made of.
type recordedRequest struct {
	method string
	path   string
	query  string
	token  string
	body   string
}

// stub is a fake TheTVDB. Each path maps to a queue of responses consumed in
// order; the last one repeats, so a single-element queue answers any number of
// requests. It also records every wait the client asks for, which is how the
// back-off is tested without taking it.
//
// It models the token as well as the routes, because a token cache cannot be
// tested against a server that accepts anything: `expired` names bearer tokens
// this server no longer honors, and a request carrying one is refused exactly
// as a revoked or timed-out JWT would be.
type stub struct {
	mu      sync.Mutex
	routes  map[string][]response
	expired map[string]bool
	// unauthorized is the body served to a request bearing an expired token. It
	// is read on the test goroutine at construction so the handler never touches
	// the filesystem.
	unauthorized []byte
	requests     []recordedRequest
	waits        []time.Duration
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(io.LimitReader(r.Body, maxErrorBody))

	s.mu.Lock()
	defer s.mu.Unlock()

	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	s.requests = append(s.requests, recordedRequest{
		method: r.Method,
		path:   r.URL.Path,
		query:  r.URL.RawQuery,
		token:  token,
		body:   string(body),
	})

	w.Header().Set("Content-Type", "application/json")

	if r.URL.Path != loginPath && s.expired[token] {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write(s.unauthorized)
		return
	}

	queue := s.routes[r.URL.Path]
	if len(queue) == 0 {
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"status":"failure","message":"no stub for this path"}`))
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

// count reports how many requests reached path.
func (s *stub) count(path string) int {
	n := 0
	for _, req := range s.seen() {
		if req.path == path {
			n++
		}
	}
	return n
}

// newStub returns a client pointed at a fake TheTVDB serving routes. Every wait
// is recorded instead of taken, so tests observe the back-off without running at
// its speed.
func newStub(t *testing.T, pin string, routes map[string][]response) (*Client, *stub) {
	t.Helper()

	if routes == nil {
		routes = map[string][]response{}
	}
	s := &stub{routes: routes, expired: map[string]bool{}, unauthorized: fixture(t, "error_unauthorized.json")}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)

	c := New("secret-key", pin, srv.Client())
	c.BaseURL = srv.URL
	c.sleep = func(_ context.Context, d time.Duration) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.waits = append(s.waits, d)
		return nil
	}
	return c, s
}

// fixture reads a recorded TheTVDB response. It runs on the test goroutine, not
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

// loginRoutes is the login exchange alone: one token, served to every login.
func loginRoutes(t *testing.T) map[string][]response {
	t.Helper()
	return map[string][]response{loginPath: {okJSON(t, "login.json")}}
}

// seriesRoutes is everything one GetSeries reads: the login, the extended
// series document and both episode pages.
func seriesRoutes(t *testing.T, id int64) map[string][]response {
	t.Helper()
	return map[string][]response{
		loginPath:              {okJSON(t, "login.json")},
		seriesExtendedPath(id): {okJSON(t, "series_extended.json")},
		seriesEpisodesPath(id): {okJSON(t, "series_episodes_page0.json"), okJSON(t, "series_episodes_page1.json")},
	}
}

// loginBody decodes the JSON the client posted to /login.
func loginBody(t *testing.T, req recordedRequest) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(req.body), &out); err != nil {
		t.Fatalf("decode login body %q: %v", req.body, err)
	}
	return out
}

// The PIN has to be absent, not empty, for a licensed key: and present for a
// user-supported one. Both directions are pinned because each failure is
// invisible to anybody holding the other kind of key.
func TestLoginSendsThePINOnlyWhenThereIsOne(t *testing.T) {
	t.Run("licensed key omits the field entirely", func(t *testing.T) {
		c, s := newStub(t, "", loginRoutes(t))

		if err := c.Test(context.Background()); err != nil {
			t.Fatalf("Test: %v", err)
		}

		got := s.seen()
		if len(got) != 1 || got[0].path != loginPath || got[0].method != http.MethodPost {
			t.Fatalf("requests = %+v, want one POST %s", got, loginPath)
		}
		body := loginBody(t, got[0])
		if body["apikey"] != "secret-key" {
			t.Errorf("apikey = %v, want the key the client was built with", body["apikey"])
		}
		if _, ok := body["pin"]; ok {
			// A licensed key is refused when "pin" arrives at all, empty
			// included, so this must be an absent field rather than a blank one.
			t.Errorf("login body = %v, want no pin field at all", body)
		}
	})

	t.Run("user-supported key sends the pin", func(t *testing.T) {
		c, s := newStub(t, "1234", loginRoutes(t))

		if err := c.Test(context.Background()); err != nil {
			t.Fatalf("Test: %v", err)
		}

		body := loginBody(t, s.seen()[0])
		if body["pin"] != "1234" {
			t.Errorf("pin = %v, want the pin the client was built with", body["pin"])
		}
	})
}

// Test is a login and nothing else: it is the cheapest exchange that answers
// the Test button's question, and anything more would make proving a credential
// depend on whatever record the extra call happened to ask for.
func TestTestIsALoginAndNothingMore(t *testing.T) {
	c, s := newStub(t, "", loginRoutes(t))

	if err := c.Test(context.Background()); err != nil {
		t.Fatalf("Test: %v", err)
	}

	got := s.seen()
	if len(got) != 1 || got[0].path != loginPath {
		t.Fatalf("requests = %+v, want the login alone", got)
	}
}

// A rejected login is the credential's answer, and it has to read as one
// without the layers above importing this package.
func TestRejectedLoginIsTheCoreSentinel(t *testing.T) {
	c, _ := newStub(t, "", map[string][]response{
		loginPath: {{status: http.StatusUnauthorized, body: fixture(t, "error_unauthorized.json")}},
	})

	err := c.Test(context.Background())
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Test = %v, want ErrUnauthorized", err)
	}
	if !errors.Is(err, core.ErrMetadataUnauthorized) {
		t.Fatalf("Test = %v, want it to wrap core.ErrMetadataUnauthorized", err)
	}
}

// The token is the reason cmd/caravan caches the client. Two lookups through
// one client must cost one login, or every search keystroke would spend a
// credential's login budget.
func TestTokenIsReusedAcrossCalls(t *testing.T) {
	c, s := newStub(t, "", seriesRoutes(t, 81189))

	for i := 0; i < 2; i++ {
		if _, err := c.GetSeries(context.Background(), "81189"); err != nil {
			t.Fatalf("GetSeries %d: %v", i, err)
		}
	}

	if n := s.count(loginPath); n != 1 {
		t.Errorf("logins = %d, want 1 for two lookups through one client", n)
	}
	for _, req := range s.seen() {
		if req.path == loginPath {
			continue
		}
		if req.token != firstToken {
			t.Errorf("%s carried token %q, want the cached %q", req.path, req.token, firstToken)
		}
	}
}

// A token that stopped working is refreshed by the 401 it caused, and the
// retried request carries the new one. Nothing here watches a clock: expiry is
// the only thing a timer could anticipate, and revocation, rotation and clock
// skew all arrive as this exact 401.
func TestExpiredTokenIsRefreshedOnceAndTheCallSucceeds(t *testing.T) {
	routes := seriesRoutes(t, 81189)
	routes[loginPath] = []response{okJSON(t, "login.json"), okJSON(t, "login_refreshed.json")}

	c, s := newStub(t, "", routes)
	s.expired[firstToken] = true

	got, err := c.GetSeries(context.Background(), "81189")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.Title != "Breaking Bad" {
		t.Errorf("series = %+v, want the record the retry fetched", got)
	}
	if n := s.count(loginPath); n != 2 {
		t.Errorf("logins = %d, want exactly 2 (the first and the refresh)", n)
	}

	// The extended document was asked for twice: once with the dead token and
	// once with the fresh one.
	var tokens []string
	for _, req := range s.seen() {
		if req.path == seriesExtendedPath(81189) {
			tokens = append(tokens, req.token)
		}
	}
	if len(tokens) != 2 || tokens[0] != firstToken || tokens[1] != refreshToken {
		t.Errorf("extended tokens = %v, want the dead one then the refreshed one", tokens)
	}
}

// A second rejection is the credential's answer rather than the token's: the
// re-login already proved the key cannot produce a token this endpoint accepts,
// and retrying forever would turn a revoked key into a request storm.
func TestASecondRejectionIsTheCredentialsAnswer(t *testing.T) {
	routes := seriesRoutes(t, 81189)
	routes[loginPath] = []response{okJSON(t, "login.json"), okJSON(t, "login_refreshed.json")}

	c, s := newStub(t, "", routes)
	s.expired[firstToken] = true
	s.expired[refreshToken] = true

	_, err := c.GetSeries(context.Background(), "81189")
	if !errors.Is(err, core.ErrMetadataUnauthorized) {
		t.Fatalf("GetSeries = %v, want it to wrap core.ErrMetadataUnauthorized", err)
	}
	if n := s.count(loginPath); n != 2 {
		t.Errorf("logins = %d, want exactly 2 (one refresh, not a loop)", n)
	}
	if n := s.count(seriesExtendedPath(81189)); n != 2 {
		t.Errorf("extended requests = %d, want 2 (original + one retry only)", n)
	}
}

// N callers refused at the same moment must produce ONE re-login. The
// compare-and-clear in invalidateToken is what makes that true: without it a
// straggler clears the token the others just obtained and the whole group goes
// round again.
func TestConcurrentRejectionsShareOneRelogin(t *testing.T) {
	routes := seriesRoutes(t, 81189)
	routes[loginPath] = []response{okJSON(t, "login.json"), okJSON(t, "login_refreshed.json")}
	// The episode pages repeat rather than advance, so eight concurrent walks
	// all see the same first page; pagination is tested on its own.
	routes[seriesEpisodesPath(81189)] = []response{okJSON(t, "series_episodes_page1.json")}

	c, s := newStub(t, "", routes)
	s.expired[firstToken] = true

	const callers = 8
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = c.GetSeries(context.Background(), "81189")
		}(i)
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("caller %d: %v", i, err)
		}
	}
	if n := s.count(loginPath); n > 2 {
		t.Errorf("logins = %d, want at most 2 however many callers were refused at once", n)
	}
}

func TestNotFound(t *testing.T) {
	routes := loginRoutes(t)
	routes[seriesExtendedPath(999999)] = []response{
		{status: http.StatusNotFound, body: fixture(t, "error_not_found.json")},
	}
	c, _ := newStub(t, "", routes)

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
		t.Errorf("Message = %q, want TheTVDB's own words", apiErr.Message)
	}
}

func TestRateLimitRetriesOnceAndSucceeds(t *testing.T) {
	throttled := response{
		status: http.StatusTooManyRequests,
		body:   fixture(t, "error_rate_limited.json"),
		header: http.Header{"Retry-After": {"3"}},
	}
	routes := seriesRoutes(t, 81189)
	routes[seriesExtendedPath(81189)] = []response{throttled, okJSON(t, "series_extended.json")}

	c, s := newStub(t, "", routes)

	got, err := c.GetSeries(context.Background(), "81189")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.ProviderRef != "81189" {
		t.Errorf("ProviderRef = %q, want 81189", got.ProviderRef)
	}
	if waits := s.waited(); len(waits) != 1 || waits[0] != 3*time.Second {
		t.Errorf("waits = %v, want the one 3s the header asked for", waits)
	}
}

func TestRateLimitGivesUpAfterOneRetry(t *testing.T) {
	throttled := response{status: http.StatusTooManyRequests, body: fixture(t, "error_rate_limited.json")}
	routes := loginRoutes(t)
	routes[seriesExtendedPath(81189)] = []response{throttled}

	c, s := newStub(t, "", routes)

	_, err := c.GetSeries(context.Background(), "81189")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("GetSeries = %v, want ErrRateLimited", err)
	}
	if n := s.count(seriesExtendedPath(81189)); n != 2 {
		t.Errorf("requests = %d, want 2 (original + one retry only)", n)
	}
}

func TestRateLimitRetryHonorsContext(t *testing.T) {
	throttled := response{
		status: http.StatusTooManyRequests,
		body:   fixture(t, "error_rate_limited.json"),
		header: http.Header{"Retry-After": {"600"}},
	}
	routes := loginRoutes(t)
	routes[seriesExtendedPath(81189)] = []response{throttled, okJSON(t, "series_extended.json")}

	c, s := newStub(t, "", routes)
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel as the back-off begins, then hand off to the real wait: with a 600s
	// Retry-After (capped to 60s), this test only finishes if sleepCtx observes
	// the cancellation and do() gives up instead of retrying.
	c.sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return sleepCtx(ctx, d)
	}

	_, err := c.GetSeries(ctx, "81189")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := s.count(seriesExtendedPath(81189)); n != 1 {
		t.Errorf("requests = %d, want 1 (retry must not fire)", n)
	}
}

// TheTVDB's movie catalogue is deliberately not served here (see the descriptor
// comment in internal/core/provider.go), so a chain walker has to be able to
// skip this rung rather than fail on it: and it must cost neither a login nor a
// request to find out.
func TestMovieMethodsReportTheKindUnsupported(t *testing.T) {
	c, s := newStub(t, "", nil)

	movies, err := c.SearchMovies(context.Background(), "el camino")
	if !errors.Is(err, core.ErrProviderKindUnsupported) {
		t.Errorf("SearchMovies = %v, want ErrProviderKindUnsupported", err)
	}
	if movies != nil {
		t.Errorf("SearchMovies returned %v, want a nil slice", movies)
	}

	movie, err := c.GetMovie(context.Background(), "81189")
	if !errors.Is(err, core.ErrProviderKindUnsupported) {
		t.Errorf("GetMovie = %v, want ErrProviderKindUnsupported", err)
	}
	if movie != nil {
		t.Errorf("GetMovie returned %+v, want nil", movie)
	}

	if seen := s.seen(); len(seen) != 0 {
		t.Errorf("an unsupported kind reached TheTVDB as %+v", seen)
	}
}

// A ref this client cannot read is a wiring bug in Caravan, another provider's
// ref reached a TheTVDB client, not a title TheTVDB is missing. It must NOT
// read as ErrNotFound, which upstream parks a file as "unmatched" and moves on,
// and it must not cost a login to discover.
func TestGetSeriesRejectsForeignRefsWithoutAsking(t *testing.T) {
	// "series-81189" is the trap this provider carries in its own search
	// results: TheTVDB's search `id` is prefixed, and a ref built from it must
	// fail loudly rather than look like a missing show.
	refs := []string{"series-81189", "9f3b1c2e-0000-4a5b-8c9d-1e2f3a4b5c6d", "", "tt0903747", "-4", "0"}
	for _, ref := range refs {
		c, s := newStub(t, "", loginRoutes(t))

		_, err := c.GetSeries(context.Background(), ref)
		if !errors.Is(err, ErrInvalidRef) {
			t.Errorf("GetSeries(%q) = %v, want ErrInvalidRef", ref, err)
		} else if errors.Is(err, ErrNotFound) {
			t.Errorf("GetSeries(%q) also reads as ErrNotFound", ref)
		}
		if seen := s.seen(); len(seen) != 0 {
			t.Errorf("ref %q reached TheTVDB as %+v", ref, seen)
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

// A 2xx login with no token in it is an endpoint that changed shape. Reporting
// it beats sending "Bearer " on every request afterwards and reading the
// resulting 401s as a rejected credential.
func TestLoginWithoutATokenIsAnError(t *testing.T) {
	c, _ := newStub(t, "", map[string][]response{
		loginPath: {{status: http.StatusOK, body: []byte(`{"status":"success","data":{}}`)}},
	})

	err := c.Test(context.Background())
	if err == nil {
		t.Fatal("Test: want an error, got nil")
	}
	if errors.Is(err, core.ErrMetadataUnauthorized) {
		t.Errorf("a missing token reported itself as a rejected credential: %v", err)
	}
}

func TestDecodeErrorIsReported(t *testing.T) {
	routes := loginRoutes(t)
	routes[seriesExtendedPath(81189)] = []response{{status: http.StatusOK, body: []byte("{not json")}}
	c, _ := newStub(t, "", routes)

	_, err := c.GetSeries(context.Background(), "81189")
	if err == nil {
		t.Fatal("GetSeries: want a decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode /series/81189/extended") {
		t.Errorf("err = %v, want it to name the failing path", err)
	}
}

func TestTransportErrorNamesThePath(t *testing.T) {
	// A server that is already gone: c.hc.Do fails, and the *url.Error it
	// returns would otherwise bury the path under the address.
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()

	c := New("secret-key", "", &http.Client{Timeout: time.Second})
	c.BaseURL = base

	_, err := c.GetSeries(context.Background(), "81189")
	if err == nil {
		t.Fatal("GetSeries: want a transport error, got nil")
	}
	// The login is the first thing out, so that is the path the error names.
	if !strings.Contains(err.Error(), loginPath) {
		t.Errorf("err = %v, want it to name the failing path", err)
	}
}
