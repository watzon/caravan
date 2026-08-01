package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

func TestMain(m *testing.M) {
	// The handlers log through slog.Default; keep the test output readable.
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

// stubManager stands in for *library.Manager. It writes through to the real
// test store so the handlers see the same data a real manager would produce.
type stubManager struct {
	st       *store.Store
	provider core.MetadataProvider

	// scanStarted receives once per Scan call; scanRelease, when non-nil,
	// blocks Scan until the test lets it finish.
	scanStarted chan struct{}
	scanRelease chan struct{}
	scanCount   atomic.Int32
	scanErr     error

	addErr   error
	matchErr error

	mu      sync.Mutex
	matches []matchCall
}

type matchCall struct {
	id        int64
	mediaType string
	tmdbID    int64
}

func (m *stubManager) Scan(ctx context.Context) error {
	m.scanCount.Add(1)
	if m.scanStarted != nil {
		m.scanStarted <- struct{}{}
	}
	if m.scanRelease != nil {
		<-m.scanRelease
	}
	return m.scanErr
}

func (m *stubManager) AddMovie(ctx context.Context, tmdbID int64) (*core.Movie, error) {
	if m.addErr != nil {
		return nil, m.addErr
	}
	mv := &core.Movie{TMDBID: tmdbID, Title: "Stub Movie", SortTitle: "stub movie", Year: 2008, Monitored: true}
	if err := m.st.UpsertMovie(ctx, mv); err != nil {
		return nil, err
	}
	return mv, nil
}

func (m *stubManager) AddSeries(ctx context.Context, tmdbID int64) (*core.Series, error) {
	if m.addErr != nil {
		return nil, m.addErr
	}
	sr := &core.Series{TMDBID: tmdbID, Title: "Stub Series", SortTitle: "stub series", Year: 2016, Monitored: true}
	if err := m.st.UpsertSeries(ctx, sr); err != nil {
		return nil, err
	}
	return sr, nil
}

func (m *stubManager) MatchUnmatched(ctx context.Context, id int64, mediaType string, tmdbID int64) error {
	m.mu.Lock()
	m.matches = append(m.matches, matchCall{id: id, mediaType: mediaType, tmdbID: tmdbID})
	m.mu.Unlock()
	return m.matchErr
}

func (m *stubManager) Metadata() core.MetadataProvider { return m.provider }

func (m *stubManager) matchCalls() []matchCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]matchCall(nil), m.matches...)
}

// stubProvider is a canned core.MetadataProvider.
type stubProvider struct {
	movies []core.MovieMeta
	series []core.SeriesMeta
	err    error
}

func (p *stubProvider) SearchMovies(ctx context.Context, q string) ([]core.MovieMeta, error) {
	return p.movies, p.err
}

func (p *stubProvider) SearchSeries(ctx context.Context, q string) ([]core.SeriesMeta, error) {
	return p.series, p.err
}

func (p *stubProvider) GetMovie(ctx context.Context, tmdbID int64) (*core.MovieMeta, error) {
	return nil, store.ErrNotFound
}

func (p *stubProvider) GetSeries(ctx context.Context, tmdbID int64) (*core.SeriesMeta, error) {
	return nil, store.ErrNotFound
}

// testDist is a stand-in for the embedded SPA bundle.
func testDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>caravan</title>")},
		"assets/app.js": {Data: []byte("console.log('caravan')")},
	}
}

// newTestServer builds a handler over a real store in a temp directory.
func newTestServer(t *testing.T) (http.Handler, *store.Store, *stubManager) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	mgr := &stubManager{st: st}
	return NewServer(st, mgr, testDist()), st, mgr
}

// do issues a request against h. body may be empty.
func do(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, strings.NewReader(body))
		r.Header.Set("Content-Type", contentTypeJSON)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	return rec
}

// decodeBody unmarshals a JSON response, failing the test on a mismatch.
func decodeBody(t *testing.T, rec *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != contentTypeJSON {
		t.Fatalf("Content-Type = %q, want %q (body %q)", got, contentTypeJSON, rec.Body.String())
	}
	if err := json.Unmarshal(rec.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
}

// wantStatus asserts the status code, reporting the body on failure.
func wantStatus(t *testing.T, rec *httptest.ResponseRecorder, want int) {
	t.Helper()
	if rec.Code != want {
		t.Fatalf("status = %d, want %d (body %q)", rec.Code, want, rec.Body.String())
	}
}

// wantErrorBody asserts the failure envelope is present and non-empty.
func wantErrorBody(t *testing.T, rec *httptest.ResponseRecorder) {
	t.Helper()
	var body errorResponse
	decodeBody(t, rec, &body)
	if body.Error == "" {
		t.Fatalf("error envelope is empty: %q", rec.Body.String())
	}
}

func TestSPAServesBundleAndFallsBackToIndex(t *testing.T) {
	h, _, _ := newTestServer(t)

	tests := []struct {
		name string
		path string
		want string
	}{
		{"root", "/", "<!doctype html>"},
		{"asset", "/assets/app.js", "console.log"},
		{"client route falls back", "/library/movies", "<!doctype html>"},
		{"deep client route falls back", "/settings/storage", "<!doctype html>"},
		{"missing asset falls back", "/assets/missing.js", "<!doctype html>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, tt.path, "")
			wantStatus(t, rec, http.StatusOK)
			if !strings.Contains(rec.Body.String(), tt.want) {
				t.Fatalf("body = %q, want it to contain %q", rec.Body.String(), tt.want)
			}
		})
	}
}

func TestSPARejectsNonGETMethods(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/library", "")
	wantStatus(t, rec, http.StatusMethodNotAllowed)
	wantErrorBody(t, rec)
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Fatalf("Allow = %q, want %q", got, "GET, HEAD")
	}
}

func TestServerWithoutBundleStillServesAPI(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	h := NewServer(st, &stubManager{st: st}, nil)

	rec := do(t, h, http.MethodGet, "/", "")
	wantStatus(t, rec, http.StatusNotFound)
	wantErrorBody(t, rec)

	rec = do(t, h, http.MethodGet, "/api/v1/library/movies", "")
	wantStatus(t, rec, http.StatusOK)
}

func TestAPIRoutingErrorsUseJSONEnvelope(t *testing.T) {
	h, _, _ := newTestServer(t)

	tests := []struct {
		name       string
		method     string
		path       string
		wantStatus int
	}{
		{"unknown v1 path", http.MethodGet, "/api/v1/nope", http.StatusNotFound},
		{"api root", http.MethodGet, "/api/v1/", http.StatusNotFound},
		{"non-v1 api path", http.MethodGet, "/api/v2/settings", http.StatusNotFound},
		{"wrong method", http.MethodDelete, "/api/v1/settings", http.StatusMethodNotAllowed},
		{"wrong method on collection", http.MethodPut, "/api/v1/library/movies", http.StatusMethodNotAllowed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, tt.method, tt.path, "")
			wantStatus(t, rec, tt.wantStatus)
			wantErrorBody(t, rec)
		})
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/settings", "")
	wantStatus(t, rec, http.StatusOK)
	var settings map[string]string
	decodeBody(t, rec, &settings)
	if len(settings) != 0 {
		t.Fatalf("settings = %v, want empty on a fresh database", settings)
	}

	rec = do(t, h, http.MethodPut, "/api/v1/settings", `{"storage_root":"/data","tmdb_api_key":"k"}`)
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &settings)
	if settings[store.SettingStorageRoot] != "/data" || settings[store.SettingTMDBAPIKey] != "k" {
		t.Fatalf("settings = %v, want the values just written", settings)
	}

	// A partial update leaves untouched keys alone.
	rec = do(t, h, http.MethodPut, "/api/v1/settings", `{"storage_root":"/mnt/media"}`)
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &settings)
	if settings[store.SettingStorageRoot] != "/mnt/media" {
		t.Fatalf("storage_root = %q, want %q", settings[store.SettingStorageRoot], "/mnt/media")
	}
	if settings[store.SettingTMDBAPIKey] != "k" {
		t.Fatalf("tmdb_api_key = %q, want it preserved", settings[store.SettingTMDBAPIKey])
	}
}

func TestPutSettingsRejectsBadRequests(t *testing.T) {
	h, st, _ := newTestServer(t)

	tests := []struct {
		name string
		body string
	}{
		{"unknown key", `{"nonsense":"1"}`},
		{"malformed json", `{`},
		{"wrong value type", `{"storage_root":42}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPut, "/api/v1/settings", tt.body)
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
		})
	}

	// Nothing was written by any of the rejected requests.
	settings, err := st.AllSettings(context.Background())
	if err != nil {
		t.Fatalf("AllSettings: %v", err)
	}
	if len(settings) != 0 {
		t.Fatalf("settings = %v, want no writes from rejected requests", settings)
	}
}

func TestPutSettingsRequiresBody(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPut, "/api/v1/settings", "")
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}

func TestSystemStatus(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	if err := st.SetSetting(ctx, store.SettingStorageRoot, "/data"); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	if err := st.UpsertMovie(ctx, &core.Movie{TMDBID: 1, Title: "A"}); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	if err := st.UpsertSeries(ctx, &core.Series{TMDBID: 2, Title: "B"}); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := st.UpsertMediaFile(ctx, &core.MediaFile{Path: "Movies/A (2001)/A (2001).mkv", Size: 10}); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	if err := st.UpsertUnmatchedFile(ctx, &core.UnmatchedFile{Path: "junk.mkv", Reason: "no match"}); err != nil {
		t.Fatalf("UpsertUnmatchedFile: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)

	var got statusResponse
	decodeBody(t, rec, &got)
	want := statusResponse{
		Version:       Version,
		Mode:          ModeServer,
		StorageRoot:   "/data",
		SchemaVersion: got.SchemaVersion,
		Scanning:      false,
		Counts:        statusCounts{Movies: 1, Series: 1, MediaFiles: 1, Unmatched: 1},
	}
	if got != want {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
	if got.SchemaVersion < 1 {
		t.Fatalf("schema_version = %d, want >= 1", got.SchemaVersion)
	}
}

func TestSystemStatusReportsPortableMode(t *testing.T) {
	h, st, _ := newTestServer(t)

	if err := st.SetSetting(context.Background(), SettingMode, ModePortable); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)
	var got statusResponse
	decodeBody(t, rec, &got)
	if got.Mode != ModePortable {
		t.Fatalf("mode = %q, want %q", got.Mode, ModePortable)
	}
}

func TestEvents(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	for _, message := range []string{"first", "second", "third"} {
		if err := st.InsertEvent(ctx, &core.Event{Category: "scan", Message: message}); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	rec := do(t, h, http.MethodGet, "/api/v1/events", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Events []eventJSON `json:"events"`
	}
	decodeBody(t, rec, &body)
	if len(body.Events) != 3 {
		t.Fatalf("events = %d, want 3", len(body.Events))
	}
	if body.Events[0].Message != "third" {
		t.Fatalf("first event = %q, want the newest (%q)", body.Events[0].Message, "third")
	}
	if body.Events[0].Level != core.EventLevelInfo || body.Events[0].CreatedAt == "" {
		t.Fatalf("event = %+v, want a level and a timestamp", body.Events[0])
	}

	rec = do(t, h, http.MethodGet, "/api/v1/events?limit=1", "")
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &body)
	if len(body.Events) != 1 {
		t.Fatalf("events with limit=1 = %d, want 1", len(body.Events))
	}
}

func TestEventsRejectsBadLimit(t *testing.T) {
	h, _, _ := newTestServer(t)

	for _, limit := range []string{"0", "-1", "many"} {
		rec := do(t, h, http.MethodGet, "/api/v1/events?limit="+limit, "")
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
	}
}
