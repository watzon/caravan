package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/watzon/caravan/internal/cardigann"
	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/indexer/catalog"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/tmdb"
	"github.com/watzon/caravan/internal/usenet"
	"github.com/watzon/caravan/internal/usenet/nntp"
)

func testAdapter(t *testing.T) (*libraryAdapter, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return newLibraryAdapter(st, "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil), st
}

func TestIndexerFactoryRoutesDefinitionsThroughLocalEngine(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/q.php" || r.URL.Query().Get("q") != "ubuntu" {
			t.Errorf("upstream request = %s", r.URL.String())
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"id":"1","name":"Ubuntu ISO","info_hash":"0123456789abcdef0123456789abcdef01234567","category":"207","added":"1700000000","size":"1024","seeders":"9","leechers":"1"}]`)
	}))
	t.Cleanup(upstream.Close)

	definitions, err := cardigann.LoadBuiltins()
	if err != nil {
		t.Fatalf("LoadBuiltins: %v", err)
	}
	factory := configuredIndexerFactory(definitions, upstream.Client(), upstream.Client(), nil)
	client := factory(core.IndexerConfig{
		ID:           7,
		DefinitionID: "thepiratebay",
		Name:         "Local TPB",
		URL:          "https://thepiratebay.org",
		Type:         core.IndexerTypeTorznab,
		Settings:     map[string]string{"apiurl": upstream.URL},
	})
	releases, err := client.Search(context.Background(), "ubuntu", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 1 || releases[0].IndexerID != 7 || !strings.HasPrefix(releases[0].DownloadURL, "magnet:?") {
		t.Fatalf("releases = %+v", releases)
	}
}

func TestIndexerFactoryNeverResolvesPinnedPackThroughLegacyRegistry(t *testing.T) {
	called := false
	upstream := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	t.Cleanup(upstream.Close)
	definitions, err := cardigann.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	factory := configuredIndexerFactory(definitions, upstream.Client(), upstream.Client(), nil)
	for _, cfg := range []core.IndexerConfig{
		{
			DefinitionID:       "builtin:thepiratebay",
			DefinitionSource:   "builtin",
			DefinitionRevision: "malicious-pack-revision",
			DefinitionDigest:   "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			URL:                "https://thepiratebay.org",
		},
		{
			DefinitionID: "community:thepiratebay",
			URL:          "https://thepiratebay.org",
		},
	} {
		client := factory(cfg)
		if _, err := client.Search(context.Background(), "ubuntu", nil); err == nil || !strings.Contains(err.Error(), "exact pin") {
			t.Fatalf("pinned client Search error = %v", err)
		}
	}
	if called {
		t.Fatal("pinned definition fell through to an executable registry client")
	}
}

func TestEveryLocalCatalogEntryExistsInEmbeddedRegistry(t *testing.T) {
	registry, err := cardigann.LoadBuiltins()
	if err != nil {
		t.Fatalf("LoadBuiltins: %v", err)
	}
	for _, entry := range catalog.All() {
		if entry.DefinitionID == "" {
			continue
		}
		if _, ok := registry.Get(entry.DefinitionID); !ok {
			t.Errorf("catalog entry %q references missing local definition %q", entry.ID, entry.DefinitionID)
		}
	}
}

func TestIndexerFactoryBlocksPrivateLocalDefinitionTarget(t *testing.T) {
	called := false
	private := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = io.WriteString(w, `[]`)
	}))
	t.Cleanup(private.Close)

	factory, err := newIndexerFactory()
	if err != nil {
		t.Fatalf("newIndexerFactory: %v", err)
	}
	client := factory(core.IndexerConfig{
		DefinitionID: "thepiratebay",
		Name:         "private target",
		URL:          "https://thepiratebay.org",
		Type:         core.IndexerTypeTorznab,
		Settings:     map[string]string{"apiurl": private.URL},
	})
	_, err = client.Search(context.Background(), "ubuntu", nil)
	if err == nil || !strings.Contains(err.Error(), "public network") {
		t.Fatalf("Search error = %q, want public-network rejection", err)
	}
	if called {
		t.Fatal("private target received a request")
	}
}

func TestReleasePayloadHTTPClientBlocksPrivateTarget(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer server.Close()
	request, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL+"/release.torrent", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newReleasePayloadHTTPClient().Do(request); err == nil {
		t.Fatal("release payload client accepted a private target")
	}
	if called {
		t.Fatal("private release target received a request")
	}
}

func TestIndexerRuntimeLoadsOnlyExecutableUserDefinitionsFromApplicationData(t *testing.T) {
	dataDir := t.TempDir()
	definitionsDir := filepath.Join(dataDir, "indexer-definitions")
	if err := os.MkdirAll(definitionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	valid := `
id: fixture
name: Fixture
type: public
links: [https://tracker.example]
caps: {modes: {search: [q]}}
settings:
  - {name: token, type: text}
search:
  paths:
    - path: /search
      method: post
      inputs: {q: "{{ .Keywords }}"}
      response: {type: xml}
  rows: {selector: rss.channel.item}
  fields:
    title: {selector: title}
    download: {selector: link}
`
	unsupported := `
id: login-required
name: Login Required
type: private
links: [https://tracker.example]
caps: {modes: {search: [q]}}
login: {path: /login, method: oneurl}
search: {paths: [{path: /search}], rows: {selector: tr}, fields: {title: {selector: td}, download: {selector: a, attribute: href}}}
`
	for name, contents := range map[string]string{
		"valid.yml":       valid,
		"malformed.yml":   "name: missing identifier\n",
		"unsupported.yml": unsupported,
	} {
		if err := os.WriteFile(filepath.Join(definitionsDir, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	runtime, err := newIndexerRuntime(dataDir, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatalf("newIndexerRuntime: %v", err)
	}
	schema, ok := runtime.definitions("user:fixture")
	if !ok || !slices.Equal(schema.Settings, []string{"token"}) {
		t.Fatalf("user:fixture schema = %+v, %v", schema, ok)
	}
	if _, ok := runtime.definitions("user:login-required"); ok {
		t.Fatal("unsupported login definition entered executable registry")
	}
	if _, err := runtime.factory(core.IndexerConfig{
		DefinitionID: "user:fixture", Name: "Fixture", URL: "https://tracker.example",
		Type: core.IndexerTypeTorznab, Settings: map[string]string{"token": "secret"},
	}).Categories(context.Background()); err != nil {
		t.Fatalf("user definition client: %v", err)
	}
}

// The watcher holds one library.Manager for the life of the process, so the
// provider it was built with has to keep up with the settings table. A TMDB
// key set after startup must reach the next import.
func TestLateMetadataFollowsTheSettingsTable(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	meta := lateMetadata{adapter: adapter}

	if _, err := meta.GetMovie(ctx, strconv.FormatInt(smokeTMDBID, 10)); !errors.Is(err, core.ErrNoMetadataProvider) {
		t.Fatalf("GetMovie with no key = %v, want ErrNoMetadataProvider", err)
	}

	// Configure TMDB the way the settings screen does, after the fact.
	redirectTMDB(t, startFakeTMDB(t))
	if err := st.SetSetting(ctx, store.SettingTMDBAPIKey, "set-after-startup"); err != nil {
		t.Fatalf("set tmdb key: %v", err)
	}

	got, err := meta.GetMovie(ctx, strconv.FormatInt(smokeTMDBID, 10))
	if err != nil {
		t.Fatalf("GetMovie after the key was set: %v", err)
	}
	if got.Title != smokeMovieTitle || got.Year != smokeMovieYear {
		t.Errorf("GetMovie = %q (%d), want %q (%d)", got.Title, got.Year, smokeMovieTitle, smokeMovieYear)
	}
}

type tmdbProbe struct {
	mu       sync.Mutex
	requests []tmdbProbeRequest
}

type tmdbProbeRequest struct {
	path string
	key  string
}

func (p *tmdbProbe) record(r *http.Request) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, tmdbProbeRequest{
		path: r.URL.Path,
		key:  r.URL.Query().Get("api_key"),
	})
}

func (p *tmdbProbe) count(path, key string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, request := range p.requests {
		if request.path == path && (key == "" || request.key == key) {
			n++
		}
	}
	return n
}

func startTMDBGenreProbe(t *testing.T, probe *tmdbProbe) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		probe.record(r)
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/3/genre/movie/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"genres": []map[string]any{{"id": 28, "name": "Action"}},
			})
		case "/3/configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	redirectTMDB(t, srv.URL)
}

func tmdbMovieGenres(t *testing.T, provider core.MetadataProvider) {
	t.Helper()
	client, ok := provider.(*tmdb.Client)
	if !ok {
		t.Fatalf("metadata provider has type %T, want *tmdb.Client", provider)
	}
	if _, err := client.Genres(context.Background(), core.MediaTypeMovie); err != nil {
		t.Fatalf("Genres: %v", err)
	}
}

func TestTMDBProviderIsReusedAndInvalidatedByStoredKey(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	probe := new(tmdbProbe)
	startTMDBGenreProbe(t, probe)

	if err := st.SetSetting(ctx, store.SettingTMDBAPIKey, "working-key"); err != nil {
		t.Fatalf("set working key: %v", err)
	}
	first := adapter.metadata(ctx)
	tmdbMovieGenres(t, first)
	second := adapter.metadata(ctx)
	tmdbMovieGenres(t, second)
	if first != second {
		t.Fatal("same stored key returned different metadata clients")
	}
	if got := probe.count("/3/genre/movie/list", "working-key"); got != 1 {
		t.Fatalf("working genre requests = %d, want 1", got)
	}

	if err := st.SetSetting(ctx, store.SettingTMDBAPIKey, "rotated-key"); err != nil {
		t.Fatalf("rotate key: %v", err)
	}
	rotated := adapter.metadata(ctx)
	tmdbMovieGenres(t, rotated)
	if rotated == first {
		t.Fatal("rotated key reused the working metadata client")
	}
	if got := probe.count("/3/genre/movie/list", "rotated-key"); got != 1 {
		t.Fatalf("rotated genre requests = %d, want 1", got)
	}
	if got := probe.count("/3/genre/movie/list", ""); got != 2 {
		t.Fatalf("total genre requests after rotation = %d, want 2", got)
	}

	if err := st.SetSetting(ctx, store.SettingTMDBAPIKey, ""); err != nil {
		t.Fatalf("clear key: %v", err)
	}
	if got := adapter.metadata(ctx); got != nil {
		t.Fatalf("metadata after clearing key = %T, want nil", got)
	}
	if err := st.SetSetting(ctx, store.SettingTMDBAPIKey, "rotated-key"); err != nil {
		t.Fatalf("restore key: %v", err)
	}
	restored := adapter.metadata(ctx)
	tmdbMovieGenres(t, restored)
	if restored == rotated {
		t.Fatal("restored key reused a client that survived clearing")
	}
	if got := probe.count("/3/genre/movie/list", "rotated-key"); got != 2 {
		t.Fatalf("rotated genre requests after clear/re-add = %d, want 2", got)
	}
}

func TestTMDBProviderIsSafeForConcurrentCallers(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	if err := st.SetSetting(ctx, store.SettingTMDBAPIKey, "working-key"); err != nil {
		t.Fatalf("set working key: %v", err)
	}

	const callers = 8
	got := make([]core.MetadataProvider, callers)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = adapter.metadata(ctx)
		}(i)
	}
	wg.Wait()

	for i, provider := range got {
		if provider == nil {
			t.Fatalf("metadata = nil on caller %d", i)
		}
		if provider != got[0] {
			t.Fatalf("caller %d got a different metadata client", i)
		}
	}
}

func TestTMDBCredentialValidationDoesNotEvictWorkingClient(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	probe := new(tmdbProbe)
	startTMDBGenreProbe(t, probe)
	if err := st.SetSetting(ctx, store.SettingTMDBAPIKey, "working-key"); err != nil {
		t.Fatalf("set working key: %v", err)
	}

	working := adapter.metadata(ctx)
	tmdbMovieGenres(t, working)
	if err := adapter.ValidateMetadataKey(ctx, core.ProviderTMDB, "candidate-key", ""); err != nil {
		t.Fatalf("ValidateMetadataKey: %v", err)
	}
	afterValidation := adapter.metadata(ctx)
	tmdbMovieGenres(t, afterValidation)

	if afterValidation != working {
		t.Fatal("candidate validation replaced the working metadata client")
	}
	if got := probe.count("/3/configuration", "candidate-key"); got != 1 {
		t.Fatalf("candidate configuration requests = %d, want 1", got)
	}
	if got := probe.count("/3/genre/movie/list", "working-key"); got != 1 {
		t.Fatalf("working genre requests after candidate validation = %d, want 1", got)
	}
	if got := probe.count("/3/genre/movie/list", "candidate-key"); got != 0 {
		t.Fatalf("candidate genre requests = %d, want 0", got)
	}
}

// The engine cannot exist before a storage root does, and a first run has
// none: the process must still start and serve the setup screen.
func TestEngineProviderIsLazyUntilTheStorageRootExists(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	provider := newEngineProvider(adapter, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Errorf("close engine: %v", err)
		}
	})

	if engine := provider.Engine(); engine != nil {
		t.Fatal("Engine() built an engine with no storage root configured")
	}
	if name := provider.Name(); name != "embedded" {
		t.Errorf("Name() = %q, want %q", name, "embedded")
	}

	root := t.TempDir()
	if err := st.SetSetting(ctx, store.SettingStorageRoot, root); err != nil {
		t.Fatalf("set storage root: %v", err)
	}

	engine := provider.Engine()
	if engine == nil {
		t.Fatal("Engine() = nil after the storage root was set")
	}
	// Engine() hands out a fresh router per call — it is a view over the
	// routing table, resolved live so a client added later is reachable
	// without a restart — but the embedded engine behind it is built once. A
	// second one would bind a second port and fight the first over the same
	// files.
	if again := provider.embedded(); again != provider.embedded() {
		t.Error("embedded() built a second engine")
	}

	// In-progress data belongs under the storage root, not beside it.
	if _, err := provider.Engine().List(ctx); err != nil {
		t.Errorf("List on a fresh engine: %v", err)
	}
	if _, err := filepath.Glob(filepath.Join(root, "incomplete")); err != nil {
		t.Errorf("glob incomplete dir: %v", err)
	}
}

// Closing without ever building an engine is what a shutdown before setup
// looks like.
func TestEngineProviderCloseWithoutEngine(t *testing.T) {
	adapter, _ := testAdapter(t)
	provider := newEngineProvider(adapter, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := provider.Close(); err != nil {
		t.Errorf("Close with no engine = %v, want nil", err)
	}
}

func TestEngineOptionsReadsSettings(t *testing.T) {
	opts, err := engineOptions(map[string]string{
		store.SettingEngineListenPort:     "51413",
		store.SettingEngineMaxConnections: "12",
		store.SettingEngineMaxDownKBps:    "4096",
		store.SettingEngineMaxUpKBps:      "512",
		store.SettingEngineSeedRatio:      "1.5",
		store.SettingEngineSeedDays:       "7",
	}, true, nil)
	if err != nil {
		t.Fatalf("engineOptions: %v", err)
	}
	if opts.ListenPort != 51413 || opts.MaxConnections != 12 {
		t.Fatalf("connection settings = port %d, max %d, want 51413 and 12", opts.ListenPort, opts.MaxConnections)
	}
	if opts.MaxDownKBps != 4096 || opts.MaxUpKBps != 512 {
		t.Fatalf("rate settings = %d/%d, want 4096/512", opts.MaxDownKBps, opts.MaxUpKBps)
	}
	if opts.SeedRatio != 1.5 || opts.SeedDays != 7 || !opts.Paused {
		t.Fatalf("seeding settings = ratio %v days %d paused %t", opts.SeedRatio, opts.SeedDays, opts.Paused)
	}
}

// watcherNotifier stands in for the playback handoff (internal/jellyfin).
type watcherNotifier struct{ calls int }

func (n *watcherNotifier) LibraryChanged(context.Context) error {
	n.calls++
	return nil
}

// TestImportWatcherNotifiesTheHandoff is PLAN phase 4 acceptance criterion 1 at
// the wiring, which is where it was actually broken: the pipeline notifies, but
// only if the Manager it runs on was built with the notifier. The watcher builds
// its own and holds it for the life of the process, so an automatic import — the
// path every finished download takes — silently never triggered a Jellyfin scan
// while the manual match did.
func TestImportWatcherNotifiesTheHandoff(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	root := t.TempDir()
	notify := &watcherNotifier{}
	adapter := newLibraryAdapter(st, root, slog.New(slog.NewTextHandler(io.Discard, nil)), notify, nil)

	redirectTMDB(t, startFakeTMDB(t))
	if err := st.SetSetting(ctx, store.SettingTMDBAPIKey, "key"); err != nil {
		t.Fatalf("set tmdb key: %v", err)
	}
	movie := core.Movie{TMDBID: smokeTMDBID, Title: smokeMovieTitle, Year: smokeMovieYear, Monitored: true}
	if err := st.UpsertMovie(ctx, &movie); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	// A finished download sitting under the storage root, as the engine leaves it.
	const saveDir = "incomplete/Big.Buck.Bunny.2008.1080p.BluRay.x264-CARAVAN"
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(saveDir)), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := filepath.Join(root, filepath.FromSlash(saveDir), smokeContentName)
	if err := os.WriteFile(content, []byte("movie bytes"), 0o644); err != nil {
		t.Fatalf("write download: %v", err)
	}

	mgr := adapter.watcherManager(root)
	dl := core.DownloadStatus{ID: "infohash", State: core.DownloadCompleted, SavePath: saveDir}
	grab := core.GrabInfo{MovieID: movie.ID, ReleaseTitle: smokeReleaseTitle}
	if err := mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	files, err := st.ListMediaFilesForMovie(ctx, movie.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForMovie: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("imported %d files, want 1 — the fixture is not exercising an import", len(files))
	}
	if notify.calls != 1 {
		t.Fatalf("handoff notifications = %d, want 1 after an automatic import", notify.calls)
	}
}

// Every backend this build carries has to reach the process-wide registry, or
// a user can configure a client the serving process then answers 501 for. It
// is one line per backend and exactly the line that gets forgotten.
func TestRegisterDownloadClientsInstallsEveryBackend(t *testing.T) {
	if err := registerDownloadClients(); err != nil {
		t.Fatalf("registerDownloadClients: %v", err)
	}
	// Running twice is a no-op: the smoke tests start several servers in one
	// binary and the registry refuses a duplicate registration.
	if err := registerDownloadClients(); err != nil {
		t.Fatalf("registerDownloadClients twice: %v", err)
	}

	for _, ty := range clients.Types() {
		if !clients.Default.Supported(ty.Name) {
			t.Errorf("%s is configurable but no implementation was registered for it", ty.Name)
		}
	}
}

// fakeClientEngine stands in for a real external client's engine so the
// routing table can be exercised without a machine to talk to.
type fakeClientEngine struct {
	cfg core.DownloadClientConfig
	// listErr makes the poll fail, which is how a client that has been
	// switched off looks from here.
	listErr error
	closed  bool
}

func (e *fakeClientEngine) Add(context.Context, core.Release, core.AddOpts) (core.DownloadID, error) {
	return core.DownloadID(e.cfg.Type + ":added"), nil
}

func (e *fakeClientEngine) Status(context.Context, core.DownloadID) (*core.DownloadStatus, error) {
	return nil, download.ErrNotFound
}
func (e *fakeClientEngine) List(context.Context) ([]core.DownloadStatus, error) {
	return nil, e.listErr
}

func (e *fakeClientEngine) Pause(context.Context, core.DownloadID) error        { return nil }
func (e *fakeClientEngine) Resume(context.Context, core.DownloadID) error       { return nil }
func (e *fakeClientEngine) Remove(context.Context, core.DownloadID, bool) error { return nil }
func (e *fakeClientEngine) Close() error                                        { e.closed = true; return nil }

// routingProvider builds a provider whose external engines are fakes, and
// returns the list of every engine it built in order.
func routingProvider(t *testing.T) (*engineProvider, *store.Store, *[]*fakeClientEngine) {
	t.Helper()
	adapter, st := testAdapter(t)
	provider := newEngineProvider(adapter, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	built := &[]*fakeClientEngine{}
	provider.newClientEngine = func(cfg core.DownloadClientConfig) (core.Engine, error) {
		engine := &fakeClientEngine{cfg: cfg}
		*built = append(*built, engine)
		return engine, nil
	}
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Errorf("close provider: %v", err)
		}
	})
	if err := st.SetSetting(context.Background(), store.SettingStorageRoot, t.TempDir()); err != nil {
		t.Fatalf("set storage root: %v", err)
	}
	return provider, st, built
}

// routeNames renders the resolved table as "protocol=name" strings, so a test
// can assert the whole routing decision in one comparison.
func routeNames(t *testing.T, provider *engineProvider) []string {
	t.Helper()
	routes, err := provider.routes(context.Background())
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	out := []string{}
	for _, r := range routes {
		out = append(out, r.Protocol+"="+r.Name)
	}
	return out
}

// The engines the router dispatches to are built from `download_clients` rows,
// and a row that is disabled, edited or deleted has to take effect without a
// restart — the import watcher holds one engine for the life of the process.
func TestEngineProviderBuildsEnginesFromDownloadClientRows(t *testing.T) {
	ctx := context.Background()
	provider, st, built := routingProvider(t)

	// Nothing configured: both protocols go to their built-in engine, which
	// is a stock Caravan with no external download client anywhere
	// (PLAN phase 7 task 6).
	if got := routeNames(t, provider); len(got) != 2 ||
		got[0] != "torrent=embedded" || got[1] != "usenet=embedded-usenet" {
		t.Fatalf("routes with nothing configured = %v, want [torrent=embedded usenet=embedded-usenet]", got)
	}
	if len(*built) != 0 {
		t.Errorf("built %d client engines with no rows", len(*built))
	}

	sab := core.DownloadClientConfig{
		Type: core.DownloadClientSABnzbd, Name: "sab", URL: "http://sab.example",
		APIKey: "secret", Category: "caravan", Priority: 25, Enabled: true,
	}
	if err := st.UpsertDownloadClient(ctx, &sab); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}
	// A client exists but is nobody's default: it is addressable for the
	// downloads it holds, and takes no new work.
	if got := routeNames(t, provider); len(got) != 3 || got[2] != "=sabnzbd" {
		t.Fatalf("routes with an unpicked client = %v, want the client with no protocol", got)
	}

	if err := st.SetSetting(ctx, store.SettingRouteUsenet, strconv.FormatInt(sab.ID, 10)); err != nil {
		t.Fatalf("set usenet route: %v", err)
	}
	// The client takes the default; the built-in engine rejoins without a
	// protocol, still holding whatever it took before the default moved.
	if got := routeNames(t, provider); len(got) != 3 ||
		got[1] != "usenet=sabnzbd" || got[2] != "=embedded-usenet" {
		t.Fatalf("routes after picking the usenet default = %v, want [torrent=embedded usenet=sabnzbd =embedded-usenet]", got)
	}
	if len(*built) != 1 {
		t.Fatalf("built %d client engines, want 1 reused across resolutions", len(*built))
	}

	// An edit rebuilds the engine: the old one is still pointed at the old
	// address with the old credential.
	sab.URL = "http://sab.example:8080"
	if err := st.UpsertDownloadClient(ctx, &sab); err != nil {
		t.Fatalf("UpsertDownloadClient (edit): %v", err)
	}
	if got := routeNames(t, provider); len(got) != 3 || got[1] != "usenet=sabnzbd" {
		t.Fatalf("routes after an edit = %v, want usenet=sabnzbd", got)
	}
	if len(*built) != 2 {
		t.Fatalf("built %d client engines after an edit, want the engine rebuilt", len(*built))
	}
	if !(*built)[0].closed {
		t.Error("the pre-edit engine was left open")
	}

	// Disabling the client takes its engine out of the table, and usenet falls
	// back to the built-in engine rather than going unrouted.
	sab.Enabled = false
	if err := st.UpsertDownloadClient(ctx, &sab); err != nil {
		t.Fatalf("UpsertDownloadClient (disable): %v", err)
	}
	if got := routeNames(t, provider); len(got) != 2 ||
		got[0] != "torrent=embedded" || got[1] != "usenet=embedded-usenet" {
		t.Fatalf("routes with the client disabled = %v, want [torrent=embedded usenet=embedded-usenet]", got)
	}
	if !(*built)[1].closed {
		t.Error("the disabled client's engine was left open")
	}

	// A routing setting pointing at a row that is gone falls back to the
	// built-in engine rather than silently routing somewhere else.
	if err := st.DeleteDownloadClient(ctx, sab.ID); err != nil {
		t.Fatalf("DeleteDownloadClient: %v", err)
	}
	if got := routeNames(t, provider); len(got) != 2 ||
		got[0] != "torrent=embedded" || got[1] != "usenet=embedded-usenet" {
		t.Fatalf("routes with the client deleted = %v, want [torrent=embedded usenet=embedded-usenet]", got)
	}
}

// The torrent default may be an external client, and a torrent default that
// has gone away falls back to the embedded engine — there is always a working
// torrent engine, so rejecting torrent grabs would be a self-inflicted outage.
func TestEngineProviderTorrentDefaultFallsBackToEmbedded(t *testing.T) {
	ctx := context.Background()
	provider, st, _ := routingProvider(t)

	qb := core.DownloadClientConfig{
		Type: core.DownloadClientQBittorrent, Name: "qb", URL: "http://qb.example",
		Username: "admin", Password: "pw", Priority: 25, Enabled: true,
	}
	if err := st.UpsertDownloadClient(ctx, &qb); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingRouteTorrent, strconv.FormatInt(qb.ID, 10)); err != nil {
		t.Fatalf("set torrent route: %v", err)
	}
	// The client takes the torrent default, and the embedded engine stays in
	// the table without one: it is still holding whatever it took before the
	// default moved, and those downloads have to stay listable, importable and
	// removable.
	if got := routeNames(t, provider); len(got) != 3 ||
		got[0] != "torrent=qbittorrent" || got[1] != "=embedded" || got[2] != "usenet=embedded-usenet" {
		t.Fatalf("routes = %v, want [torrent=qbittorrent =embedded usenet=embedded-usenet]", got)
	}

	if err := st.DeleteDownloadClient(ctx, qb.ID); err != nil {
		t.Fatalf("DeleteDownloadClient: %v", err)
	}
	if got := routeNames(t, provider); len(got) != 2 ||
		got[0] != "torrent=embedded" || got[1] != "usenet=embedded-usenet" {
		t.Fatalf("routes after the torrent default vanished = %v, want [torrent=embedded usenet=embedded-usenet]", got)
	}
}

// A torrent client taking the default must not evict the embedded engine from
// the routing table.
//
// The embedded engine keeps running and keeps holding whatever it took before
// the default moved — downloads mid-flight, downloads seeding, downloads about
// to finish. Dropping it strands every one of them: Router.List stops reporting
// them so they vanish from the queue and the import watcher never sees them
// complete, and Router.owner stops finding them so pause, resume and remove all
// fail. This is the ordinary upgrade path — an existing user adds qBittorrent —
// so it is the case that must not regress.
func TestEngineProviderKeepsTheEmbeddedEngineWhenAClientTakesTheTorrentDefault(t *testing.T) {
	ctx := context.Background()
	provider, st, _ := routingProvider(t)

	embedded := provider.embedded()
	if embedded == nil {
		t.Fatal("no embedded engine to keep")
	}

	qb := core.DownloadClientConfig{
		Type: core.DownloadClientQBittorrent, Name: "qb", URL: "http://qb.example",
		Username: "admin", Password: "pw", Priority: 25, Enabled: true,
	}
	if err := st.UpsertDownloadClient(ctx, &qb); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingRouteTorrent, strconv.FormatInt(qb.ID, 10)); err != nil {
		t.Fatalf("set torrent route: %v", err)
	}

	routes, err := provider.routes(ctx)
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	var kept *download.Route
	for i := range routes {
		if routes[i].Engine == core.Engine(embedded) {
			kept = &routes[i]
		}
	}
	if kept == nil {
		t.Fatalf("routes = %v: the embedded engine was dropped when the client took the torrent default, stranding every download it still holds", routeNames(t, provider))
	}
	// Protocol-less, exactly like a client that is nobody's default: it is
	// addressable for what it holds, and takes no new work.
	if kept.Protocol != "" {
		t.Errorf("embedded route protocol = %q, want none — the client is the torrent default now", kept.Protocol)
	}
	// And the client really is where new torrents go, so keeping the embedded
	// engine has not quietly undone the routing choice.
	if got := provider.Engine().(*download.Router).EngineNameFor(ctx, core.ProtocolTorrent); got != core.DownloadClientQBittorrent {
		t.Errorf("torrent releases route to %q, want %q", got, core.DownloadClientQBittorrent)
	}
}

// Every external client's route carries a namespace keyed on its own
// `download_clients` row, and the embedded engine's carries none.
//
// This is the wiring behind download.Route.IDPrefix: two NZBGet clients both
// hand out a download "5", and Caravan stores handles bare, so without distinct
// prefixes one client's download resolves to the other's grab. The embedded
// engine stays bare because info hashes are already unique and prefixing them
// would orphan every download row that predates external clients.
func TestEngineProviderNamespacesEachClientsDownloadHandles(t *testing.T) {
	ctx := context.Background()
	provider, st, _ := routingProvider(t)

	first := core.DownloadClientConfig{
		Type: core.DownloadClientNZBGet, Name: "nzb-one", URL: "http://one.example",
		Username: "u", Password: "p", Priority: 25, Enabled: true,
	}
	second := core.DownloadClientConfig{
		Type: core.DownloadClientNZBGet, Name: "nzb-two", URL: "http://two.example",
		Username: "u", Password: "p", Priority: 25, Enabled: true,
	}
	for _, cfg := range []*core.DownloadClientConfig{&first, &second} {
		if err := st.UpsertDownloadClient(ctx, cfg); err != nil {
			t.Fatalf("UpsertDownloadClient: %v", err)
		}
	}
	if err := st.SetSetting(ctx, store.SettingRouteUsenet, strconv.FormatInt(first.ID, 10)); err != nil {
		t.Fatalf("set usenet route: %v", err)
	}

	routes, err := provider.routes(ctx)
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	prefixes := map[string]string{}
	for _, r := range routes {
		key := r.Name
		if r.Engine == core.Engine(provider.embedded()) {
			key = download.EngineName
		}
		if _, dup := prefixes[key+r.IDPrefix]; dup {
			t.Fatalf("two routes share the namespace %q; their handles can collide", r.IDPrefix)
		}
		prefixes[key+r.IDPrefix] = r.IDPrefix
	}

	var got []string
	for _, r := range routes {
		got = append(got, r.IDPrefix)
	}
	wantFirst := "c" + strconv.FormatInt(first.ID, 10) + "."
	wantSecond := "c" + strconv.FormatInt(second.ID, 10) + "."
	if !slices.Contains(got, wantFirst) || !slices.Contains(got, wantSecond) {
		t.Fatalf("route namespaces = %v, want both %q and %q keyed on the client rows", got, wantFirst, wantSecond)
	}
	if !slices.Contains(got, "") {
		t.Fatalf("route namespaces = %v, want the embedded engine's handles left bare", got)
	}
}

// A routing setting naming a client of the wrong protocol must not route:
// SABnzbd as the torrent default is a configuration mistake, and honouring it
// would hand every torrent to a machine that cannot read one.
func TestEngineProviderIgnoresARouteOfTheWrongProtocol(t *testing.T) {
	ctx := context.Background()
	provider, st, _ := routingProvider(t)

	sab := core.DownloadClientConfig{
		Type: core.DownloadClientSABnzbd, Name: "sab", URL: "http://sab.example",
		APIKey: "secret", Priority: 25, Enabled: true,
	}
	if err := st.UpsertDownloadClient(ctx, &sab); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}
	id := strconv.FormatInt(sab.ID, 10)
	if err := st.SetSetting(ctx, store.SettingRouteTorrent, id); err != nil {
		t.Fatalf("set torrent route: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingRouteUsenet, id); err != nil {
		t.Fatalf("set usenet route: %v", err)
	}
	got := routeNames(t, provider)
	if len(got) != 3 || got[0] != "torrent=embedded" || got[1] != "usenet=sabnzbd" || got[2] != "=embedded-usenet" {
		t.Fatalf("routes = %v, want [torrent=embedded usenet=sabnzbd =embedded-usenet]", got)
	}
}

// The health model end to end at the wiring level (PLAN phase 6 task 4):
// killing a client mid-poll marks only that client unreachable, announces the
// transition once, refuses grabs routed to it, and leaves the embedded engine
// and every other client completely alone. Reviving it clears the banner.
func TestUnreachableClientIsQuarantinedAndRecovers(t *testing.T) {
	ctx := context.Background()
	provider, st, built := routingProvider(t)

	sab := core.DownloadClientConfig{
		Type: core.DownloadClientSABnzbd, Name: "sab", URL: "http://sab.example",
		APIKey: "secret", Priority: 25, Enabled: true,
	}
	if err := st.UpsertDownloadClient(ctx, &sab); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingRouteUsenet, strconv.FormatInt(sab.ID, 10)); err != nil {
		t.Fatalf("set usenet route: %v", err)
	}

	engine := provider.Engine()
	if engine == nil {
		t.Fatal("no engine")
	}
	// One poll builds the client engine; then it stops answering.
	if _, err := engine.List(ctx); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	if len(*built) != 1 {
		t.Fatalf("built %d client engines, want 1", len(*built))
	}
	(*built)[0].listErr = errors.New("connection refused")

	for range download.DefaultUnhealthyAfter {
		if _, err := engine.List(ctx); err != nil {
			t.Fatalf("poll with an unreachable client failed the whole list: %v", err)
		}
	}

	unhealthy := provider.UnhealthyDownloadClients()
	if len(unhealthy) != 1 || unhealthy[0].ID != sab.ID || unhealthy[0].Name != "sab" {
		t.Fatalf("unhealthy = %+v, want only the sab client", unhealthy)
	}
	if unhealthy[0].Error != "connection refused" {
		t.Fatalf("reason = %q, want the poll's own message", unhealthy[0].Error)
	}
	if provider.Health() != "ok" {
		t.Fatalf("embedded engine health = %q, want it unaffected", provider.Health())
	}

	// The transition is announced once, however long the client stays down.
	for range 3 {
		if _, err := engine.List(ctx); err != nil {
			t.Fatalf("poll: %v", err)
		}
	}
	if got := countClientEvents(t, st, "is unreachable"); got != 1 {
		t.Fatalf("unreachable events = %d, want exactly 1", got)
	}

	// A usenet grab fails fast and by name.
	if _, err := engine.Add(ctx, core.Release{Title: "x.nzb", Protocol: core.ProtocolUsenet}, core.AddOpts{}); !errors.Is(err, download.ErrClientUnreachable) {
		t.Fatalf("usenet grab error = %v, want %v", err, download.ErrClientUnreachable)
	}
	// The torrent route — the embedded engine — is not quarantined by a dead
	// usenet client, so torrent grabs keep working exactly as before.
	routes, err := provider.routes(ctx)
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	for _, r := range routes {
		if r.Protocol == core.ProtocolTorrent && r.Unhealthy != "" {
			t.Fatalf("the torrent route was quarantined by a dead usenet client: %q", r.Unhealthy)
		}
	}

	// Revived: the next successful poll clears it, and says so once.
	(*built)[0].listErr = nil
	if _, err := engine.List(ctx); err != nil {
		t.Fatalf("poll after recovery: %v", err)
	}
	if got := provider.UnhealthyDownloadClients(); len(got) != 0 {
		t.Fatalf("unhealthy after recovery = %+v, want none", got)
	}
	if got := countClientEvents(t, st, "is reachable again"); got != 1 {
		t.Fatalf("recovery events = %d, want exactly 1", got)
	}
	if _, err := engine.Add(ctx, core.Release{Title: "x.nzb", Protocol: core.ProtocolUsenet}, core.AddOpts{}); err != nil {
		t.Fatalf("usenet grab after recovery: %v", err)
	}
}

// A client the user deletes cannot leave a banner behind: there is nothing
// left to act on.
func TestUnreachableClientIsForgottenWhenItIsDeleted(t *testing.T) {
	ctx := context.Background()
	provider, st, built := routingProvider(t)

	sab := core.DownloadClientConfig{
		Type: core.DownloadClientSABnzbd, Name: "sab", URL: "http://sab.example",
		APIKey: "secret", Priority: 25, Enabled: true,
	}
	if err := st.UpsertDownloadClient(ctx, &sab); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingRouteUsenet, strconv.FormatInt(sab.ID, 10)); err != nil {
		t.Fatalf("set usenet route: %v", err)
	}

	engine := provider.Engine()
	if _, err := engine.List(ctx); err != nil {
		t.Fatalf("first poll: %v", err)
	}
	(*built)[0].listErr = errors.New("connection refused")
	for range download.DefaultUnhealthyAfter {
		if _, err := engine.List(ctx); err != nil {
			t.Fatalf("poll: %v", err)
		}
	}
	if len(provider.UnhealthyDownloadClients()) != 1 {
		t.Fatal("client was not marked unreachable")
	}

	if err := st.DeleteDownloadClient(ctx, sab.ID); err != nil {
		t.Fatalf("DeleteDownloadClient: %v", err)
	}
	if _, err := engine.List(ctx); err != nil {
		t.Fatalf("poll after delete: %v", err)
	}
	if got := provider.UnhealthyDownloadClients(); len(got) != 0 {
		t.Fatalf("unhealthy = %+v, want the deleted client forgotten", got)
	}
}

// countClientEvents counts activity-feed entries whose message contains want.
func countClientEvents(t *testing.T, st *store.Store, want string) int {
	t.Helper()
	events, err := st.ListEvents(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	n := 0
	for _, e := range events {
		if strings.Contains(e.Message, want) {
			n++
		}
	}
	return n
}

// A usenet client taking the default must not evict the embedded Usenet engine
// from the routing table.
//
// This is the phase-6 regression class applied to phase 7's engine: the
// built-in engine keeps running and keeps holding whatever it took before the
// default moved — releases mid-download, ones waiting on par2, ones about to
// finish extracting. Dropping it strands every one of them, because
// Router.List stops reporting them (so they vanish from the queue and the
// import watcher never sees them complete) and Router.owner stops finding them
// (so pause, resume and remove all fail).
func TestEngineProviderKeepsTheEmbeddedUsenetEngineWhenAClientTakesTheUsenetDefault(t *testing.T) {
	ctx := context.Background()
	provider, st, _ := routingProvider(t)

	news := provider.embeddedUsenet()
	if news == nil {
		t.Fatal("no embedded usenet engine to keep")
	}

	sab := core.DownloadClientConfig{
		Type: core.DownloadClientSABnzbd, Name: "sab", URL: "http://sab.example",
		APIKey: "secret", Priority: 25, Enabled: true,
	}
	if err := st.UpsertDownloadClient(ctx, &sab); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingRouteUsenet, strconv.FormatInt(sab.ID, 10)); err != nil {
		t.Fatalf("set usenet route: %v", err)
	}

	routes, err := provider.routes(ctx)
	if err != nil {
		t.Fatalf("routes: %v", err)
	}
	var kept *download.Route
	for i := range routes {
		if routes[i].Engine == core.Engine(news) {
			kept = &routes[i]
		}
	}
	if kept == nil {
		t.Fatalf("routes = %v: the embedded Usenet engine was dropped when the client took the usenet default, stranding every download it still holds", routeNames(t, provider))
	}
	// Protocol-less, exactly like a client that is nobody's default: it is
	// addressable for what it holds, and takes no new work.
	if kept.Protocol != "" {
		t.Errorf("embedded usenet route protocol = %q, want none — the client is the usenet default now", kept.Protocol)
	}
	// And the client really is where new NZBs go, so keeping the built-in
	// engine has not quietly undone the routing choice.
	if got := provider.Engine().(*download.Router).EngineNameFor(ctx, core.ProtocolUsenet); got != core.DownloadClientSABnzbd {
		t.Errorf("usenet releases route to %q, want %q", got, core.DownloadClientSABnzbd)
	}
}

// A stock Caravan downloads both protocols with nothing configured. This is
// the "default default" (PLAN phase 7 task 6) and the acceptance criterion
// that no external client is required for either protocol.
func TestEngineProviderRoutesUsenetToTheBuiltInEngineWithNothingConfigured(t *testing.T) {
	ctx := context.Background()
	provider, _, _ := routingProvider(t)

	router, ok := provider.Engine().(*download.Router)
	if !ok {
		t.Fatal("provider did not hand out a router")
	}
	if got := router.EngineNameFor(ctx, core.ProtocolUsenet); got != usenet.EngineName {
		t.Errorf("usenet releases route to %q, want the built-in %q", got, usenet.EngineName)
	}
	if got := router.EngineNameFor(ctx, core.ProtocolTorrent); got != download.EngineName {
		t.Errorf("torrent releases route to %q, want the built-in %q", got, download.EngineName)
	}
}

// Adding a news server has to reach a running engine, because the import
// watcher takes one engine at startup and drives it for the life of the
// process. Without this a provider added after boot would never be dialled.
func TestEngineProviderPropagatesUsenetServerChangesWithoutARestart(t *testing.T) {
	ctx := context.Background()
	provider, st, _ := routingProvider(t)

	news := provider.embeddedUsenet()
	if news == nil {
		t.Fatal("no embedded usenet engine")
	}
	if _, err := news.Add(ctx, core.Release{
		Title: "Before.Any.Server", Protocol: core.ProtocolUsenet, DownloadURL: "http://example.invalid/a.nzb",
	}, core.AddOpts{}); !errors.Is(err, nntp.ErrNoServers) {
		t.Fatalf("Add with no servers = %v, want nntp.ErrNoServers", err)
	}

	server := core.UsenetServerConfig{
		Name: "primary", Host: "news.example", Port: 563, TLS: true,
		Username: "reader", Password: "secret", MaxConnections: 8, Priority: 1, Enabled: true,
	}
	if err := st.UpsertUsenetServer(ctx, &server); err != nil {
		t.Fatalf("UpsertUsenetServer: %v", err)
	}

	// Resolving the routing table is what a queue operation does, and it is
	// where the new configuration lands.
	if _, err := provider.routes(ctx); err != nil {
		t.Fatalf("routes: %v", err)
	}

	// The engine now has somewhere to fetch from, so the refusal is gone. The
	// grab fails on the unreachable indexer instead, which is the next thing
	// to go wrong and proves the server check was passed.
	_, err := news.Add(ctx, core.Release{
		Title: "After.A.Server", Protocol: core.ProtocolUsenet, DownloadURL: "http://127.0.0.1:1/a.nzb",
	}, core.AddOpts{})
	if errors.Is(err, nntp.ErrNoServers) {
		t.Fatal("the new news server never reached the running engine")
	}
	if err == nil {
		t.Fatal("Add against an unreachable indexer unexpectedly succeeded")
	}
}

// A library that routes its downloads somewhere else has to actually send them
// there. The setting is stored, validated and rendered as an active override,
// so a router that kept reading the global setting would be a silent misroute:
// the user picks a seedbox for Series, the UI says Series uses the seedbox, and
// every episode keeps landing in the global client with nothing to say so.
func TestEngineForRoutesGrabsThroughTheLibrarysOwnClient(t *testing.T) {
	ctx := context.Background()
	provider, st, _ := routingProvider(t)

	local := core.DownloadClientConfig{
		Type: core.DownloadClientQBittorrent, Name: "local", URL: "http://local.example",
		Username: "admin", Password: "pw", Priority: 25, Enabled: true,
	}
	seedbox := core.DownloadClientConfig{
		Type: core.DownloadClientQBittorrent, Name: "seedbox", URL: "http://seedbox.example",
		Username: "admin", Password: "pw", Priority: 25, Enabled: true,
	}
	for _, cfg := range []*core.DownloadClientConfig{&local, &seedbox} {
		if err := st.UpsertDownloadClient(ctx, cfg); err != nil {
			t.Fatalf("UpsertDownloadClient %q: %v", cfg.Name, err)
		}
	}

	// A library with no override of its own reads the global default, which is
	// the built-in engine until something else is picked.
	if got := provider.EngineFor(0, core.LibraryKindTV).(*download.Router).EngineNameFor(ctx, core.ProtocolTorrent); got != download.EngineName {
		t.Fatalf("torrents route to %q before any override, want the built-in %q", got, download.EngineName)
	}

	if err := st.SetSetting(ctx, store.SettingRouteTorrent, strconv.FormatInt(local.ID, 10)); err != nil {
		t.Fatalf("set global torrent route: %v", err)
	}
	tv, err := st.GetLibraryByKind(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("get tv library: %v", err)
	}
	tv.RouteTorrent = strconv.FormatInt(seedbox.ID, 10)
	if err := st.UpdateLibrary(ctx, tv); err != nil {
		t.Fatalf("update tv library: %v", err)
	}

	// Both clients are the same backend, so the name alone cannot tell them
	// apart: the handle prefix names the `download_clients` row that took it.
	got, err := provider.EngineFor(0, core.LibraryKindTV).Add(ctx, core.Release{
		Title: "Example Series S01E01", Protocol: core.ProtocolTorrent,
		DownloadURL: "magnet:?xt=urn:btih:0000000000000000000000000000000000000000",
	}, core.AddOpts{Category: "tv"})
	if err != nil {
		t.Fatalf("add through the tv library's engine: %v", err)
	}
	if !strings.HasPrefix(string(got), clientIDPrefix(seedbox.ID)) {
		t.Errorf("tv grab produced handle %q, want one issued by the seedbox the library routes to", got)
	}

	// The library that set no override is untouched, and so is every operation
	// that belongs to no library at all.
	for _, kind := range []string{core.LibraryKindMovie, ""} {
		id, err := provider.EngineFor(0, kind).Add(ctx, core.Release{
			Title: "Example Movie 2024", Protocol: core.ProtocolTorrent,
			DownloadURL: "magnet:?xt=urn:btih:1111111111111111111111111111111111111111",
		}, core.AddOpts{Category: "movies"})
		if err != nil {
			t.Fatalf("add through the %q engine: %v", kind, err)
		}
		if !strings.HasPrefix(string(id), clientIDPrefix(local.ID)) {
			t.Errorf("%q grab produced handle %q, want one issued by the global default", kind, id)
		}
	}
}

// The library's usenet route is a separate setting and gets separately ignored.
// A usenet override must not be answered by the torrent one, and must not leak
// into the libraries that did not set it.
func TestEngineForRoutesUsenetThroughTheLibrarysOwnClient(t *testing.T) {
	ctx := context.Background()
	provider, st, _ := routingProvider(t)

	sab := core.DownloadClientConfig{
		Type: core.DownloadClientSABnzbd, Name: "sab", URL: "http://sab.example",
		APIKey: "secret", Category: "caravan", Priority: 25, Enabled: true,
	}
	if err := st.UpsertDownloadClient(ctx, &sab); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}
	movies, err := st.GetLibraryByKind(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("get movie library: %v", err)
	}
	movies.RouteUsenet = strconv.FormatInt(sab.ID, 10)
	if err := st.UpdateLibrary(ctx, movies); err != nil {
		t.Fatalf("update movie library: %v", err)
	}

	movieRouter := provider.EngineFor(0, core.LibraryKindMovie).(*download.Router)
	if got := movieRouter.EngineNameFor(ctx, core.ProtocolUsenet); got != core.DownloadClientSABnzbd {
		t.Errorf("movie usenet releases route to %q, want %q", got, core.DownloadClientSABnzbd)
	}
	if got := movieRouter.EngineNameFor(ctx, core.ProtocolTorrent); got != download.EngineName {
		t.Errorf("the usenet override moved torrents to %q as well, want the built-in %q", got, download.EngineName)
	}
	tvRouter := provider.EngineFor(0, core.LibraryKindTV).(*download.Router)
	if got := tvRouter.EngineNameFor(ctx, core.ProtocolUsenet); got != usenet.EngineName {
		t.Errorf("the movie library's override reached tv usenet releases as %q, want the built-in %q", got, usenet.EngineName)
	}
}
