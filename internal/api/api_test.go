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
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/library"
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
	// adult is the stash-box seam. It is nil by default, which is what a
	// server with the module switched off or no credential entered looks like,
	// so every existing test keeps proving that the adult surfaces reach no
	// provider unless a test deliberately gives them one.
	adult core.AdultMetadataProvider

	// addSiteErr, when set, is what AddSite reports instead of writing a row.
	addSiteErr error

	// addSiteCalls records the stash ids AddSite was asked for, so a test can
	// prove a scene approval added the SITE rather than something else.
	addSiteCalls []string

	// addSiteSceneStashID is the scene AddSiteAndWait files as an episode.
	// Empty derives one from the site id; a test that approves a request for a
	// named scene sets it so the row it looks for is the row it asked for.
	addSiteSceneStashID string

	// scanStarted receives once per Scan call; scanRelease, when non-nil,
	// blocks Scan until the test lets it finish.
	scanStarted chan struct{}
	scanRelease chan struct{}
	scanCount   atomic.Int32
	scanErr     error

	addErr   error
	matchErr error

	// addSeriesEpisodes is how many aired, monitored, file-less episodes
	// AddSeries writes alongside the series. Zero — the default — keeps the
	// stub's historical shape; the search-on-add tests need a series that has
	// something to search for.
	addSeriesEpisodes int

	// addSeriesSeasons is how many monitored season rows AddSeries writes,
	// numbered from 1. Zero keeps the stub's historical shape; the season
	// selection tests need a series with seasons to leave behind.
	addSeriesSeasons int

	removeErr error

	// validateKeys is the verdict ValidateMetadataKey gives each API key. A key
	// with no entry is accepted, so a test only has to name the keys it wants
	// rejected.
	validateKeys map[string]error

	// adultCredentialErr is what ValidateAdultCredential reports, nil by
	// default: the enable gate's happy path.
	adultCredentialErr error

	// searchHits, when set, is what SearchLibrary answers instead of asking
	// provider — the seam a chain of more than one provider is proved through,
	// since the stub provider is a single TMDB.
	searchHits *library.SearchHits

	mu                   sync.Mutex
	adds                 []addCall
	searches             []searchCall
	matches              []matchCall
	removes              []removeCall
	validateCalls        []string
	adultCredentialCalls []adultCredential
}

type matchCall struct {
	id        int64
	mediaType string
	tmdbID    int64
	// ref is the whole identity the handler resolved, so a match made by
	// provider/provider_ref can be told apart from one made by tmdb_id — the
	// tmdbID above is zero for every ref that is not TMDB's.
	ref core.ItemRef
}

// searchCall is one SearchLibrary the handlers made.
type searchCall struct {
	libraryID int64
	mediaType string
	q         string
}

// removeCall records what the handlers asked the manager to remove. Deleting
// files is the manager's job, so the HTTP layer's contract is the flag it
// forwards, not what happens on disk — internal/library owns that half.
type removeCall struct {
	kind        string
	id          int64
	deleteFiles bool
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

// addCall records the identity an add was made with, so a test can prove the
// handler forwarded the ref the body named rather than a TMDB one built from a
// field the body did not carry.
type addCall struct {
	kind string
	ref  core.ItemRef
}

func (m *stubManager) addCalls() []addCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]addCall(nil), m.adds...)
}

func (m *stubManager) AddMovie(ctx context.Context, ref core.ItemRef, minAvailability string, monitored *bool, libraryID int64) (*core.Movie, error) {
	m.mu.Lock()
	m.adds = append(m.adds, addCall{kind: MediaTypeMovie, ref: ref})
	m.mu.Unlock()
	if m.addErr != nil {
		return nil, m.addErr
	}
	tmdbID := ref.TMDBID()
	// The stub persists minAvailability and the monitored choice verbatim (the
	// store defaults an empty availability), so handler tests can read the row
	// back to prove the plumbing. It follows the real manager's rule for an
	// absent choice: nil is monitored.
	mv := &core.Movie{TMDBID: tmdbID, Title: "Stub Movie", SortTitle: "stub movie", Year: 2008,
		Monitored: monitored == nil || *monitored, MinAvailability: minAvailability}
	if err := m.st.UpsertMovie(ctx, mv); err != nil {
		return nil, err
	}
	return mv, nil
}

func (m *stubManager) AddSeries(ctx context.Context, ref core.ItemRef, monitored *bool, libraryID int64) (*core.Series, error) {
	m.mu.Lock()
	m.adds = append(m.adds, addCall{kind: MediaTypeSeries, ref: ref})
	m.mu.Unlock()
	if m.addErr != nil {
		return nil, m.addErr
	}
	tmdbID := ref.TMDBID()
	sr := &core.Series{TMDBID: tmdbID, Title: "Stub Series", SortTitle: "stub series", Year: 2016,
		Monitored: monitored == nil || *monitored}
	if err := m.st.UpsertSeries(ctx, sr); err != nil {
		return nil, err
	}
	for i := 1; i <= m.addSeriesSeasons; i++ {
		se := &core.Season{SeriesID: sr.ID, Number: i, Title: "Stub Season", Monitored: true}
		if err := m.st.UpsertSeason(ctx, se); err != nil {
			return nil, err
		}
	}
	for i := 1; i <= m.addSeriesEpisodes; i++ {
		e := &core.Episode{
			SeriesID:      sr.ID,
			SeasonNumber:  1,
			EpisodeNumber: i,
			Title:         "Stub Episode",
			AirDate:       time.Now().UTC().AddDate(0, 0, -7),
			Monitored:     true,
		}
		if err := m.st.UpsertEpisode(ctx, e); err != nil {
			return nil, err
		}
	}
	return sr, nil
}

func (m *stubManager) RemoveMovie(ctx context.Context, id int64, deleteFiles bool) error {
	m.mu.Lock()
	m.removes = append(m.removes, removeCall{kind: "movie", id: id, deleteFiles: deleteFiles})
	m.mu.Unlock()
	if m.removeErr != nil {
		return m.removeErr
	}
	return m.st.DeleteMovie(ctx, id)
}

func (m *stubManager) RemoveSeries(ctx context.Context, id int64, deleteFiles bool) error {
	m.mu.Lock()
	m.removes = append(m.removes, removeCall{kind: "series", id: id, deleteFiles: deleteFiles})
	m.mu.Unlock()
	if m.removeErr != nil {
		return m.removeErr
	}
	return m.st.DeleteSeries(ctx, id)
}

func (m *stubManager) removeCalls() []removeCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]removeCall(nil), m.removes...)
}

func (m *stubManager) MatchUnmatched(ctx context.Context, id int64, mediaType string, ref core.ItemRef) error {
	m.mu.Lock()
	m.matches = append(m.matches, matchCall{id: id, mediaType: mediaType, tmdbID: ref.TMDBID(), ref: ref})
	m.mu.Unlock()
	return m.matchErr
}

// SearchLibrary answers from the same stub provider Metadata does, so every
// test written before search was per-library keeps meaning what it meant: a
// stock database chains both default libraries to TMDB, and the stub provider
// IS that TMDB. What it adds is the record of which library and media type the
// handler asked about, and the searchHits seam for a longer chain.
func (m *stubManager) SearchLibrary(ctx context.Context, libraryID int64, mediaType, q string) (*library.SearchHits, error) {
	m.mu.Lock()
	m.searches = append(m.searches, searchCall{libraryID: libraryID, mediaType: mediaType, q: q})
	m.mu.Unlock()

	if m.searchHits != nil {
		return m.searchHits, nil
	}
	// A nil provider is a chain with nothing configured on it, which is what
	// the real manager reports when no provider on the chain could be built.
	if m.provider == nil {
		return nil, core.ErrNoMetadataProvider
	}
	hits := &library.SearchHits{Providers: []string{core.ProviderTMDB}}
	switch mediaType {
	case MediaTypeMovie:
		movies, err := m.provider.SearchMovies(ctx, q)
		if err != nil {
			return nil, err
		}
		hits.Movies = movies
	case MediaTypeSeries:
		series, err := m.provider.SearchSeries(ctx, q)
		if err != nil {
			return nil, err
		}
		hits.Series = series
	}
	return hits, nil
}

func (m *stubManager) searchCalls() []searchCall {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]searchCall(nil), m.searches...)
}

// AddSite writes an adult-kind series the way library.AddSite does, so the
// handler tests read back the same shape a real manager produces — and, like
// the real one, it files NO scenes. The catalogue walk is a job now, and a stub
// that quietly did it inline would hide the very split these tests defend.
func (m *stubManager) AddSite(ctx context.Context, stashID string, monitored *bool, libraryID int64) (*core.Series, error) {
	m.mu.Lock()
	m.addSiteCalls = append(m.addSiteCalls, stashID)
	m.mu.Unlock()
	if m.addSiteErr != nil {
		return nil, m.addSiteErr
	}
	sr := &core.Series{
		StashID: stashID, Title: "Stub Site", SortTitle: "stub site",
		Kind: core.SeriesKindAdult, Monitored: monitored == nil || *monitored,
		Path: store.AdultLibraryRoot + "/Stub Site",
	}
	if err := m.st.UpsertSeries(ctx, sr); err != nil {
		return nil, err
	}
	return sr, nil
}

// AddSiteAndWait is AddSite plus the one scene the walk would have filed.
//
// That scene is what makes the approve-a-scene-request regression real: it
// exists only on this path, so a caller that switched to the deferred AddSite
// would leave the request approved with no episode row behind it, and the test
// would see exactly that.
func (m *stubManager) AddSiteAndWait(ctx context.Context, stashID string, monitored *bool, libraryID int64) (*core.Series, error) {
	sr, err := m.AddSite(ctx, stashID, monitored, libraryID)
	if err != nil {
		return nil, err
	}
	episode := &core.Episode{
		SeriesID: sr.ID, SeasonNumber: 2022, EpisodeNumber: 1,
		StashID: m.addSiteSceneStashID, Title: "Stub Scene",
		AirDate:   time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC),
		Monitored: true,
	}
	if episode.StashID == "" {
		episode.StashID = "stub-scene-" + stashID
	}
	if err := m.st.UpsertEpisode(ctx, episode); err != nil {
		return nil, err
	}
	return sr, nil
}

func (m *stubManager) siteCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.addSiteCalls...)
}

func (m *stubManager) Metadata() core.MetadataProvider { return m.provider }

func (m *stubManager) AdultMetadata() core.AdultMetadataProvider { return m.adult }

// ValidateMetadataKey answers from validateKeys: an entry maps a key to the
// verdict the provider would give it, and a key with no entry is accepted. The
// default therefore matches the pre-phase-10 world, where nothing validated
// anything, so every existing test keeps meaning what it meant.
func (m *stubManager) ValidateMetadataKey(ctx context.Context, apiKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.validateCalls = append(m.validateCalls, apiKey)
	return m.validateKeys[apiKey]
}

// ValidateAdultCredential answers from adultCredentialErr, recording what it
// was asked so the enable-gating tests can prove the request's own credential
// was tested rather than the stored one.
func (m *stubManager) ValidateAdultCredential(ctx context.Context, endpoint, apiKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.adultCredentialCalls = append(m.adultCredentialCalls, adultCredential{endpoint, apiKey})
	return m.adultCredentialErr
}

// adultCredential is one (endpoint, key) pair ValidateAdultCredential was asked
// about.
type adultCredential struct {
	endpoint string
	key      string
}

func (m *stubManager) validatedKeys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.validateCalls...)
}

func (m *stubManager) adultCredentials() []adultCredential {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]adultCredential(nil), m.adultCredentialCalls...)
}

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

func (p *stubProvider) GetMovie(ctx context.Context, ref string) (*core.MovieMeta, error) {
	return nil, store.ErrNotFound
}

func (p *stubProvider) GetSeries(ctx context.Context, ref string) (*core.SeriesMeta, error) {
	return nil, store.ErrNotFound
}

// testDist is a stand-in for the embedded SPA bundle.
func testDist() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    {Data: []byte("<!doctype html><title>caravan</title>")},
		"assets/app.js": {Data: []byte("console.log('caravan')")},
	}
}

// newTestServer builds a handler over a real store in a temp directory. Extra
// options are passed through, which is how the acquisition tests attach a stub
// engine and indexer factory.
func newTestServer(t *testing.T, opts ...Option) (http.Handler, *store.Store, *stubManager) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	mgr := &stubManager{st: st}
	return NewServer(st, mgr, testDist(), opts...), st, mgr
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
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	rec := do(t, h, http.MethodGet, "/api/v1/settings", "")
	wantStatus(t, rec, http.StatusOK)
	var settings map[string]string
	decodeBody(t, rec, &settings)
	if settings[settingTMDBAPIKeySet] != "false" {
		t.Fatalf("fresh settings = %v, want tmdb_api_key_set=false", settings)
	}
	if _, ok := settings[store.SettingTMDBAPIKey]; ok {
		t.Fatalf("fresh settings exposed tmdb_api_key: %v", settings)
	}

	secretSettings := map[string]string{
		store.SettingTMDBAPIKey:     "tmdb-secret",
		store.SettingStashboxAPIKey: "stashbox-secret",
		store.SettingJellyfinAPIKey: "jellyfin-secret",
		store.SettingStashAPIKey:    "stash-secret",
		store.SettingAPIKey:         "caravan-secret",
		store.SettingPasswordHash:   "password-hash-secret",
	}
	for key, value := range secretSettings {
		if err := st.SetSetting(ctx, key, value); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}
	publicSettings := map[string]string{
		store.SettingAdultEnabled:           "true",
		store.SettingStashURL:               "http://stash.example.test",
		store.SettingStashEnabled:           "true",
		store.SettingJellyfinURL:            "http://jellyfin.example.test",
		store.SettingJellyfinEnabled:        "true",
		store.SettingRSSSyncIntervalMinutes: "20",
	}
	for key, value := range publicSettings {
		if err := st.SetSetting(ctx, key, value); err != nil {
			t.Fatalf("seed %s: %v", key, err)
		}
	}

	assertCredentialsRedacted := func(rec *httptest.ResponseRecorder) {
		t.Helper()
		for key, value := range secretSettings {
			if strings.Contains(rec.Body.String(), value) ||
				strings.Contains(rec.Body.String(), `"`+key+`"`) {
				t.Fatalf("settings response leaked %s: %s", key, rec.Body.String())
			}
		}
	}

	rec = do(t, h, http.MethodGet, "/api/v1/settings", "")
	wantStatus(t, rec, http.StatusOK)
	assertCredentialsRedacted(rec)
	decodeBody(t, rec, &settings)
	if settings[settingTMDBAPIKeySet] != "true" ||
		settings[store.SettingRSSSyncIntervalMinutes] != "20" {
		t.Fatalf("settings = %v, want redacted key flag and interval", settings)
	}
	if settings[store.SettingAdultEnabled] != "true" ||
		settings[store.SettingStashURL] != "http://stash.example.test" ||
		settings[store.SettingStashEnabled] != "true" {
		t.Fatalf("adult-visible settings = %v, want public adult settings", settings)
	}

	// A partial update leaves stored credentials untouched and its response
	// follows the same projection as GET, even while adult settings are visible.
	rec = do(t, h, http.MethodPut, "/api/v1/settings", `{"rss_sync_interval_minutes":"45"}`)
	wantStatus(t, rec, http.StatusOK)
	assertCredentialsRedacted(rec)
	decodeBody(t, rec, &settings)
	if settings[store.SettingRSSSyncIntervalMinutes] != "45" ||
		settings[settingTMDBAPIKeySet] != "true" {
		t.Fatalf("settings = %v, want partial update with preserved key flag", settings)
	}
	if settings[store.SettingAdultEnabled] != "true" ||
		settings[store.SettingStashURL] != "http://stash.example.test" ||
		settings[store.SettingStashEnabled] != "true" {
		t.Fatalf("adult-visible PUT settings = %v, want public adult settings", settings)
	}
	stored, err := st.GetSetting(ctx, store.SettingTMDBAPIKey)
	if err != nil {
		t.Fatalf("GetSetting after partial update: %v", err)
	}
	if stored != "tmdb-secret" {
		t.Fatalf("stored TMDB key after partial update = %q, want preserved secret", stored)
	}

	// An explicit empty value clears the credential.
	rec = do(t, h, http.MethodPut, "/api/v1/settings", `{"tmdb_api_key":""}`)
	wantStatus(t, rec, http.StatusOK)
	assertCredentialsRedacted(rec)
	decodeBody(t, rec, &settings)
	if settings[settingTMDBAPIKeySet] != "false" {
		t.Fatalf("settings after clear = %v, want tmdb_api_key_set=false", settings)
	}
	stored, err = st.GetSetting(ctx, store.SettingTMDBAPIKey)
	if err != nil {
		t.Fatalf("GetSetting after clear: %v", err)
	}
	if stored != "" {
		t.Fatalf("stored TMDB key after clear = %q, want empty", stored)
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
		// An adult metadata endpoint that could never be dialled is rejected
		// where the user can see it, not swallowed and re-surfaced much later
		// as a request error inside a refresh nobody is watching.
		{"stashbox endpoint with no scheme", `{"stashbox_endpoint":"theporndb.net/graphql"}`},
		{"stashbox endpoint with an undialable scheme", `{"stashbox_endpoint":"ftp://theporndb.net/graphql"}`},
		{"stashbox endpoint with no host", `{"stashbox_endpoint":"https:///graphql"}`},
	}
	baseline, err := st.AllSettings(context.Background())
	if err != nil {
		t.Fatalf("AllSettings before rejected requests: %v", err)
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
		t.Fatalf("AllSettings after rejected requests: %v", err)
	}
	if !reflect.DeepEqual(settings, baseline) {
		t.Fatalf("settings = %v, want unchanged baseline %v after rejected requests", settings, baseline)
	}
}

func TestPutSettingsRejectsMode(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := t.Context()
	if err := st.SetSetting(ctx, SettingMode, ModeServer); err != nil {
		t.Fatalf("seed mode: %v", err)
	}

	rec := do(t, h, http.MethodPut, "/api/v1/settings", `{"mode":"portable"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	var failure errorResponse
	decodeBody(t, rec, &failure)
	if failure.Error != "unknown setting: mode" {
		t.Fatalf("mode rejection = %q, want normal unknown-setting error", failure.Error)
	}

	stored, err := st.GetSetting(ctx, SettingMode)
	if err != nil {
		t.Fatalf("GetSetting mode after rejected PUT: %v", err)
	}
	if stored != ModeServer {
		t.Fatalf("stored mode after rejected PUT = %q, want %q", stored, ModeServer)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)
	var status statusResponse
	decodeBody(t, rec, &status)
	if status.Mode != ModeServer {
		t.Fatalf("status mode = %q, want %q", status.Mode, ModeServer)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/settings", "")
	wantStatus(t, rec, http.StatusOK)
	var settings map[string]string
	decodeBody(t, rec, &settings)
	if _, ok := settings[SettingMode]; ok {
		t.Fatalf("settings exposed status-only mode: %v", settings)
	}
}

func TestPutSettingsAcceptsStashboxCredentials(t *testing.T) {
	h, st, _ := newTestServer(t)

	// A blank endpoint is legal and means "the TPDB preset": pasting a key is
	// the whole configuration for the default provider.
	rec := do(t, h, http.MethodPut, "/api/v1/settings",
		`{"stashbox_endpoint":"","stashbox_api_key":"sk-adult"}`)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), "sk-adult") ||
		strings.Contains(rec.Body.String(), `"`+store.SettingStashboxAPIKey+`"`) {
		t.Fatalf("settings response exposed stashbox credential: %s", rec.Body.String())
	}

	var settings map[string]string
	decodeBody(t, rec, &settings)
	if _, ok := settings[store.SettingStashboxEndpoint]; !ok {
		t.Fatalf("stashbox_endpoint missing from %v, want the blank value stored", settings)
	}
	stored, err := st.GetSetting(t.Context(), store.SettingStashboxAPIKey)
	if err != nil {
		t.Fatalf("GetSetting stashbox_api_key: %v", err)
	}
	if stored != "sk-adult" {
		t.Fatalf("stored stashbox_api_key = %q, want %q", stored, "sk-adult")
	}

	// Naming another box — StashDB, FansDB, a self-hosted one — is a config
	// change, not a code change (PLAN phase 9 task 1).
	rec = do(t, h, http.MethodPut, "/api/v1/settings",
		`{"stashbox_endpoint":"https://stashdb.org/graphql"}`)
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &settings)
	if settings[store.SettingStashboxEndpoint] != "https://stashdb.org/graphql" {
		t.Fatalf("stashbox_endpoint = %q, want the new endpoint", settings[store.SettingStashboxEndpoint])
	}

	// An explicit empty field remains the credential's clear operation.
	rec = do(t, h, http.MethodPut, "/api/v1/settings", `{"stashbox_api_key":""}`)
	wantStatus(t, rec, http.StatusOK)
	if strings.Contains(rec.Body.String(), `"`+store.SettingStashboxAPIKey+`"`) {
		t.Fatalf("settings clear response exposed stashbox credential field: %s", rec.Body.String())
	}
	stored, err = st.GetSetting(t.Context(), store.SettingStashboxAPIKey)
	if err != nil {
		t.Fatalf("GetSetting cleared stashbox_api_key: %v", err)
	}
	if stored != "" {
		t.Fatalf("stored stashbox_api_key after clear = %q, want empty", stored)
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
		// "/data" does not exist on the test machine, so the disk stays
		// unknown (zeros); no engine provider is wired, so unconfigured.
		DiskFreeBytes:  0,
		DiskTotalBytes: 0,
		EngineHealth:   "unconfigured",
		// A fresh database has no TMDB key, which is the first-run state the
		// wizard's metadata step exists to fix.
		MetadataCredential: CredentialAbsent,
		NeedsSetup:         true,
		// No provider means nothing polls external clients, and the banner
		// input is an empty list rather than null.
		UnhealthyDownloadClients: []unhealthyClientJSON{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("status = %+v, want %+v", got, want)
	}
	if got.SchemaVersion < 1 {
		t.Fatalf("schema_version = %d, want >= 1", got.SchemaVersion)
	}
}

// The system panel's numbers: a real storage root reports its filesystem, and
// a wired engine provider reports healthy.
func TestSystemStatusReportsDiskAndEngine(t *testing.T) {
	h, st, _, _ := newAcquisitionServer(t)

	root := t.TempDir()
	if err := st.SetSetting(context.Background(), store.SettingStorageRoot, root); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)
	var got statusResponse
	decodeBody(t, rec, &got)

	if got.EngineHealth != "ok" {
		t.Fatalf("engine_health = %q, want ok with a wired engine", got.EngineHealth)
	}
	if got.DiskTotalBytes <= 0 || got.DiskFreeBytes <= 0 {
		t.Fatalf("disk = %d/%d, want real numbers for an existing root", got.DiskFreeBytes, got.DiskTotalBytes)
	}
	if got.DiskFreeBytes > got.DiskTotalBytes {
		t.Fatalf("disk free %d exceeds total %d", got.DiskFreeBytes, got.DiskTotalBytes)
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
		Events     []eventJSON `json:"events"`
		NextCursor string      `json:"next_cursor"`
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

	if body.NextCursor == "" {
		t.Fatal("paged event response has empty continuation cursor")
	}
	cursor := body.NextCursor
	rec = do(t, h, http.MethodGet, "/api/v1/events?limit=1&cursor="+cursor, "")
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &body)
	if len(body.Events) != 1 || body.Events[0].Message != "second" {
		t.Fatalf("second event page = %+v, want second event", body.Events)
	}

	if body.NextCursor == "" {
		t.Fatal("second event page has empty continuation cursor")
	}
	rec = do(t, h, http.MethodGet, "/api/v1/events?limit=1&cursor="+body.NextCursor, "")
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &body)
	if len(body.Events) != 1 || body.Events[0].Message != "first" || body.NextCursor != "" {
		t.Fatalf("final event page = %+v cursor %q, want first event and no cursor", body.Events, body.NextCursor)
	}
}

func TestEventsRejectsBadLimit(t *testing.T) {
	h, _, _ := newTestServer(t)

	for _, limit := range []string{"0", "-1", "many"} {
		rec := do(t, h, http.MethodGet, "/api/v1/events?limit="+limit, "")
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
	}
	for _, cursor := range []string{"", "0", "-1", "many"} {
		if cursor == "" {
			continue
		}
		rec := do(t, h, http.MethodGet, "/api/v1/events?cursor="+cursor, "")
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
	}
}
