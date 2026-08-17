package anilist

import (
	"context"
	"encoding/json"
	"errors"
	"io"
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

// recordedRequest is what the stub saw. The operation name is the routing key,
// so it is also what assertions identify a request by.
type recordedRequest struct {
	method        string
	authorization string
	operationName string
	query         string
	variables     map[string]any
}

// stub is a fake AniList. Each operation name maps to a queue of responses
// consumed in order; the last one repeats, so a single-element queue answers any
// number of requests. It also records every wait the client asks for, which is
// how the throttle is tested without taking it.
type stub struct {
	mu       sync.Mutex
	routes   map[string][]response
	requests []recordedRequest
	waits    []time.Duration
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	raw, _ := io.ReadAll(io.LimitReader(r.Body, maxResponseBody))
	var body gqlRequest
	_ = json.Unmarshal(raw, &body)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.requests = append(s.requests, recordedRequest{
		method:        r.Method,
		authorization: r.Header.Get("Authorization"),
		operationName: body.OperationName,
		query:         body.Query,
		variables:     body.Variables,
	})

	queue := s.routes[body.OperationName]
	if len(queue) == 0 {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(`{"errors":[{"message":"no stub for this operation","status":501}]}`))
		return
	}
	resp := queue[0]
	if len(queue) > 1 {
		s.routes[body.OperationName] = queue[1:]
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

// newStub returns a client pointed at a fake AniList serving ops. The throttle
// floor is zeroed and every wait is recorded instead of taken, so tests observe
// the pacing without running at its speed.
func newStub(t *testing.T, ops map[string][]response) (*Client, *stub) {
	t.Helper()

	s := &stub{routes: ops}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)

	c := New(srv.Client())
	c.Endpoint = srv.URL
	c.minInterval = 0
	c.sleep = func(_ context.Context, d time.Duration) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.waits = append(s.waits, d)
		return nil
	}
	return c, s
}

// fixture reads a recorded AniList response. It runs on the test goroutine, not
// the server's, so a missing file fails the test properly.
func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// okJSON serves a whole recorded GraphQL envelope with 200.
func okJSON(t *testing.T, name string) response {
	t.Helper()
	return response{status: http.StatusOK, body: fixture(t, name)}
}

// rateLimitHeaders is what AniList puts on every response.
func rateLimitHeaders(remaining int, reset time.Time) http.Header {
	return http.Header{
		headerRateRemaining: {strconv.Itoa(remaining)},
		headerRateReset:     {strconv.FormatInt(reset.Unix(), 10)},
	}
}

func TestQuerySendsOperationAndVariables(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		opSearchSeries: {okJSON(t, "search_anime.json")},
	})

	if _, err := c.SearchSeries(context.Background(), "attack on titan"); err != nil {
		t.Fatalf("SearchSeries: %v", err)
	}

	got := s.seen()
	if len(got) != 1 {
		t.Fatalf("requests = %d, want 1", len(got))
	}
	req := got[0]
	if req.method != http.MethodPost {
		t.Errorf("method = %q, want POST", req.method)
	}
	if req.operationName != opSearchSeries {
		t.Errorf("operationName = %q, want %q", req.operationName, opSearchSeries)
	}
	if !strings.Contains(req.query, "media(type: ANIME, search: $q, sort: [SEARCH_MATCH])") {
		t.Errorf("query does not search anime by relevance: %s", req.query)
	}
	if q := req.variables["q"]; q != "attack on titan" {
		t.Errorf("variables.q = %v, want the typed query", q)
	}
	if p := req.variables["perPage"]; p != float64(defaultPerPage) {
		t.Errorf("variables.perPage = %v, want %d", p, defaultPerPage)
	}
	// AniList's read API takes no credential, and sending an empty one is how a
	// client accidentally starts failing against an endpoint that validates it.
	if req.authorization != "" {
		t.Errorf("Authorization = %q, want no credential header at all", req.authorization)
	}
}

// A rejected credential is not a condition this provider can be in — see the
// package comment — so nothing here may claim it is: core.ErrMetadataUnauthorized
// is what puts "your API key is wrong" on screen, and AniList has no key to fix.
func TestNoErrorClaimsARejectedCredential(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		c, _ := newStub(t, map[string][]response{
			opGetSeries: {{status: status, body: []byte(`{"errors":[{"message":"nope","status":` + strconv.Itoa(status) + `}]}`)}},
		})

		_, err := c.GetSeries(context.Background(), "98202")
		if err == nil {
			t.Fatalf("GetSeries on %d = nil, want an error", status)
		}
		if errors.Is(err, core.ErrMetadataUnauthorized) {
			t.Errorf("a %d reported itself as a rejected credential: %v", status, err)
		}
	}
}

func TestGraphQLNotFound(t *testing.T) {
	// AniList mirrors the condition onto the HTTP status, but not always: a
	// plain 200 carrying the errors array has to read the same way.
	tests := []struct {
		name   string
		status int
	}{
		{name: "http status too", status: http.StatusNotFound},
		{name: "graphql errors only", status: http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newStub(t, map[string][]response{
				opGetSeries: {{status: tt.status, body: fixture(t, "error_not_found.json")}},
			})

			_, err := c.GetSeries(context.Background(), "999999")
			if !errors.Is(err, ErrNotFound) {
				t.Fatalf("GetSeries = %v, want ErrNotFound", err)
			}

			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("errors.As(*APIError) = false; err = %v", err)
			}
			if apiErr.Operation != opGetSeries {
				t.Errorf("Operation = %q, want %q", apiErr.Operation, opGetSeries)
			}
			if apiErr.Status != http.StatusNotFound {
				t.Errorf("Status = %d, want 404 from the GraphQL error", apiErr.Status)
			}
			if apiErr.Message != "Not Found." {
				t.Errorf("Message = %q, want AniList's own message", apiErr.Message)
			}
		})
	}
}

// A null Media in an otherwise clean 200 says the same thing as a 404, and must
// not decode into an empty series that then overwrites a good library row.
func TestNullMediaIsNotFound(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		opGetSeries: {{status: http.StatusOK, body: []byte(`{"data":{"Media":null}}`)}},
	})

	if _, err := c.GetSeries(context.Background(), "999999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetSeries = %v, want ErrNotFound", err)
	}
}

func TestRateLimitRetriesOnceAndSucceeds(t *testing.T) {
	throttled := response{
		status: http.StatusTooManyRequests,
		body:   fixture(t, "error_rate_limited.json"),
		header: http.Header{"Retry-After": {"3"}},
	}

	c, s := newStub(t, map[string][]response{
		opGetSeries:      {throttled, okJSON(t, "media_finished.json")},
		opSeriesSchedule: {okJSON(t, "media_finished_schedule_2.json")},
	})

	got, err := c.GetSeries(context.Background(), "98202")
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.ProviderRef != "98202" {
		t.Errorf("ProviderRef = %q, want 98202", got.ProviderRef)
	}
	// The throttled attempt, its retry, and the second schedule page.
	if n := len(s.seen()); n != 3 {
		t.Errorf("requests = %d, want 3 (throttled + retry + schedule page 2)", n)
	}
	waits := s.waited()
	if len(waits) != 1 {
		t.Fatalf("waits = %v, want exactly the one retry wait", waits)
	}
	if waits[0] != 3*time.Second {
		t.Errorf("waited %v, want 3s from Retry-After", waits[0])
	}
}

func TestRateLimitGivesUpAfterOneRetry(t *testing.T) {
	throttled := response{status: http.StatusTooManyRequests, body: fixture(t, "error_rate_limited.json")}

	c, s := newStub(t, map[string][]response{
		opGetSeries: {throttled},
	})

	_, err := c.GetSeries(context.Background(), "98202")
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
		opGetSeries: {throttled, okJSON(t, "media_finished.json")},
	})
	ctx, cancel := context.WithCancel(context.Background())
	// Cancel as the retry wait begins, then hand off to the real wait: with a
	// 600s Retry-After (capped to 60s), this test only finishes if sleepCtx
	// observes the cancellation and do() gives up instead of retrying.
	c.sleep = func(ctx context.Context, d time.Duration) error {
		cancel()
		return sleepCtx(ctx, d)
	}

	_, err := c.GetSeries(ctx, "98202")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if n := len(s.seen()); n != 1 {
		t.Errorf("requests = %d, want 1 (retry must not fire)", n)
	}
}

// The floor between sends is what keeps a refresh sweep — several requests per
// series — inside AniList's per-minute budget.
func TestMinIntervalSpacesConsecutiveRequests(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		opSearchSeries: {okJSON(t, "search_anime.json")},
	})
	c.minInterval = 50 * time.Millisecond

	for i := 0; i < 3; i++ {
		if _, err := c.SearchSeries(context.Background(), "yuru camp"); err != nil {
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
	// the point: every request claims its own slot instead of all three
	// queueing behind one shared wait.
	for i, d := range waits {
		lo, hi := time.Duration(i)*c.minInterval, time.Duration(i+1)*c.minInterval
		if d <= lo || d > hi {
			t.Errorf("wait %d = %v, want it in (%v, %v]", i, d, lo, hi)
		}
	}
}

// Discovering the rate limit by being refused costs a request and a retry.
// Reading the headers costs neither, so a nearly-spent window gates the next
// send rather than the current answer.
func TestNearlySpentWindowGatesTheNextRequest(t *testing.T) {
	tests := []struct {
		name    string
		reset   time.Duration
		wantMin time.Duration
		wantMax time.Duration
	}{
		{name: "waits for the reset", reset: 30 * time.Second, wantMin: 25 * time.Second, wantMax: 30 * time.Second},
		{name: "caps an implausible reset", reset: time.Hour, wantMin: maxRateLimitWait - 5*time.Second, wantMax: maxRateLimitWait},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spent := okJSON(t, "search_anime.json")
			spent.header = rateLimitHeaders(0, time.Now().Add(tt.reset))

			c, s := newStub(t, map[string][]response{opSearchSeries: {spent}})

			if _, err := c.SearchSeries(context.Background(), "first"); err != nil {
				t.Fatalf("first SearchSeries: %v", err)
			}
			if waits := s.waited(); len(waits) != 0 {
				t.Fatalf("waits after the first request = %v, want none: the answer is already in hand", waits)
			}

			if _, err := c.SearchSeries(context.Background(), "second"); err != nil {
				t.Fatalf("second SearchSeries: %v", err)
			}
			waits := s.waited()
			if len(waits) != 1 {
				t.Fatalf("waits = %v, want exactly one", waits)
			}
			if waits[0] < tt.wantMin || waits[0] > tt.wantMax {
				t.Errorf("waited %v, want between %v and %v", waits[0], tt.wantMin, tt.wantMax)
			}
		})
	}
}

// A healthy window must not slow anything down: the floor is the only pacing
// when there is budget left.
func TestHealthyWindowAddsNoWait(t *testing.T) {
	ok := okJSON(t, "search_anime.json")
	ok.header = rateLimitHeaders(60, time.Now().Add(time.Minute))

	c, s := newStub(t, map[string][]response{opSearchSeries: {ok}})

	for i := 0; i < 2; i++ {
		if _, err := c.SearchSeries(context.Background(), "yuru camp"); err != nil {
			t.Fatalf("SearchSeries %d: %v", i, err)
		}
	}
	if waits := s.waited(); len(waits) != 0 {
		t.Errorf("waits = %v, want none with 60 requests left in the window", waits)
	}
}

// An anime library holds films beside its series, so the film half has to come
// out of the same catalogue. The search asks AniList for format MOVIE, and the
// mapping is GetMovie's documented one.
func TestSearchMoviesFiltersToFilmsAndMaps(t *testing.T) {
	c, s := newStub(t, map[string][]response{
		opSearchMovies: {okJSON(t, "search_movies.json")},
	})

	movies, err := c.SearchMovies(context.Background(), "your name")
	if err != nil {
		t.Fatalf("SearchMovies: %v", err)
	}
	if len(movies) != 1 {
		t.Fatalf("SearchMovies returned %d results, want 1", len(movies))
	}
	got := movies[0]
	// English is null on this record, so the romaji is the title — and with the
	// title taken from romaji the original falls through to the native one.
	want := core.MovieMeta{
		Provider: ProviderID, ProviderRef: "21519",
		Title: "Kimi no Na wa.", OriginalTitle: "君の名は。", Year: 2016,
		ReleaseDate: time.Date(2016, 1, 1, 0, 0, 0, 0, time.UTC),
		PosterURL:   "https://s4.anilist.co/file/anilistcdn/media/anime/cover/medium/bx21519-1.jpg",
	}
	if got != want {
		t.Errorf("SearchMovies[0] = %+v, want %+v", got, want)
	}
	// The filter is AniList's, not ours: nothing television-shaped may reach the
	// picker, and asking for it in the document is what guarantees that.
	if q := s.seen()[0].query; !strings.Contains(q, "format: MOVIE") {
		t.Errorf("search document = %q, want it to ask for format MOVIE", q)
	}
}

func TestGetMovieMapsTheFilmRecord(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		opGetMovie: {okJSON(t, "media_movie.json")},
	})

	movie, err := c.GetMovie(context.Background(), "21519")
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	want := core.MovieMeta{
		Provider: ProviderID, ProviderRef: "21519",
		Title: "Your Name.", OriginalTitle: "Kimi no Na wa.", Year: 2016,
		Overview:    "Mitsuha and Taki swap bodies across time.\n\n(Source: CoMix Wave)",
		VoteAverage: 8.5, VoteCount: 7400,
		ReleaseDate: time.Date(2016, 8, 26, 0, 0, 0, 0, time.UTC),
		PosterURL:   "https://s4.anilist.co/file/anilistcdn/media/anime/cover/large/bx21519-1.jpg",
	}
	if *movie != want {
		t.Errorf("GetMovie = %+v, want %+v", *movie, want)
	}
}

// A ref that names a real AniList record of any other format is ErrNotFound. It
// exists, but not as a film, and answering with it would pin a movie row to a
// television record that every later refresh would rewrite it from.
func TestGetMovieRefusesANonFilmRecord(t *testing.T) {
	c, _ := newStub(t, map[string][]response{
		opGetMovie: {okJSON(t, "media_finished.json")},
	})

	movie, err := c.GetMovie(context.Background(), "98202")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetMovie(tv record) = %v, want ErrNotFound", err)
	}
	if movie != nil {
		t.Errorf("GetMovie(tv record) returned %+v, want nil", movie)
	}
}

// A foreign ref is a wiring bug, and it must not cost a rate-limit token to
// discover — the same rule GetSeries follows.
func TestGetMovieRejectsForeignRefsWithoutAsking(t *testing.T) {
	c, s := newStub(t, nil)

	if _, err := c.GetMovie(context.Background(), "tt0903747"); !errors.Is(err, ErrInvalidRef) {
		t.Errorf("GetMovie(imdb ref) = %v, want ErrInvalidRef", err)
	}
	if seen := s.seen(); len(seen) != 0 {
		t.Errorf("a foreign ref reached AniList as %+v", seen)
	}
}

// A ref this client cannot read is a wiring bug in Caravan — another provider's
// ref reached an AniList client — not a title AniList is missing. It must NOT
// read as ErrNotFound, which upstream parks a file as "unmatched" and moves on,
// and it must not cost a rate-limit token to discover.
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
			t.Errorf("ref %q reached AniList as %+v", ref, seen)
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
		opGetSeries: {{status: http.StatusOK, body: []byte("{not json")}},
	})

	_, err := c.GetSeries(context.Background(), "98202")
	if err == nil {
		t.Fatal("GetSeries: want a decode error, got nil")
	}
	if !strings.Contains(err.Error(), "decode "+opGetSeries) {
		t.Errorf("err = %v, want it to name the failing operation", err)
	}
}

func TestTransportErrorNamesTheOperation(t *testing.T) {
	// A server that is already gone: c.hc.Do fails, and the *url.Error it
	// returns would otherwise bury the operation under the address.
	srv := httptest.NewServer(http.NotFoundHandler())
	endpoint := srv.URL
	srv.Close()

	c := New(&http.Client{Timeout: time.Second})
	c.Endpoint = endpoint

	_, err := c.GetSeries(context.Background(), "98202")
	if err == nil {
		t.Fatal("GetSeries: want a transport error, got nil")
	}
	if !strings.Contains(err.Error(), opGetSeries) {
		t.Errorf("err = %v, want it to name the failing operation", err)
	}
}
