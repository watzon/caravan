package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// stubEngine is a core.Engine that records what it was asked to do and answers
// from canned state. The real engine is internal/download; what these tests
// need to know is that the handlers call it with the right arguments and
// persist the right rows around it.
type stubEngine struct {
	mu sync.Mutex

	// Add hands out "hash-N" handles so two grabs in one test never collide.
	adds    []engineAdd
	paused  []core.DownloadID
	resumed []core.DownloadID
	removed []engineRemove

	statuses []core.DownloadStatus

	addErr     error
	listErr    error
	controlErr error
}

type engineAdd struct {
	release core.Release
	opts    core.AddOpts
}

type engineRemove struct {
	id         core.DownloadID
	deleteData bool
}

func (e *stubEngine) Add(ctx context.Context, r core.Release, opts core.AddOpts) (core.DownloadID, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.addErr != nil {
		return "", e.addErr
	}
	e.adds = append(e.adds, engineAdd{release: r, opts: opts})
	return core.DownloadID(fmt.Sprintf("hash-%d", len(e.adds))), nil
}

func (e *stubEngine) Status(ctx context.Context, id core.DownloadID) (*core.DownloadStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for _, st := range e.statuses {
		if st.ID == id {
			return &st, nil
		}
	}
	return nil, errors.New("stub engine: unknown download")
}

func (e *stubEngine) List(ctx context.Context) ([]core.DownloadStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.listErr != nil {
		return nil, e.listErr
	}
	return append([]core.DownloadStatus(nil), e.statuses...), nil
}

func (e *stubEngine) ListPage(ctx context.Context, limit int, before string) ([]core.DownloadStatus, string, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.listErr != nil {
		return nil, "", true, e.listErr
	}
	statuses := append([]core.DownloadStatus(nil), e.statuses...)
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID })
	start := 0
	for start < len(statuses) && before != "" && string(statuses[start].ID) <= before {
		start++
	}
	if start == len(statuses) {
		return []core.DownloadStatus{}, "", true, nil
	}
	end := min(start+limit, len(statuses))
	next := ""
	if end < len(statuses) {
		next = string(statuses[end-1].ID)
	}
	return statuses[start:end], next, true, nil
}

func (e *stubEngine) Pause(ctx context.Context, id core.DownloadID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.controlErr != nil {
		return e.controlErr
	}
	e.paused = append(e.paused, id)
	return nil
}

func (e *stubEngine) Resume(ctx context.Context, id core.DownloadID) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.controlErr != nil {
		return e.controlErr
	}
	e.resumed = append(e.resumed, id)
	return nil
}

func (e *stubEngine) Remove(ctx context.Context, id core.DownloadID, deleteData bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.controlErr != nil {
		return e.controlErr
	}
	e.removed = append(e.removed, engineRemove{id: id, deleteData: deleteData})
	return nil
}

func (e *stubEngine) Close() error { return nil }

func (e *stubEngine) addCalls() []engineAdd {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]engineAdd(nil), e.adds...)
}

// stubEngineProvider hands the server an engine, or nothing when engine is nil
// — which is how the "engine configured but not started" path is exercised.
type stubEngineProvider struct {
	engine core.Engine
}

func (p *stubEngineProvider) Engine() core.Engine {
	if p.engine == nil {
		return nil
	}
	return p.engine
}

func (p *stubEngineProvider) Name() string { return "stub" }

// fakeIndexer is a stand-in for a Torznab endpoint. internal/indexer is built
// separately, so these tests go over real HTTP against a trivial JSON protocol
// instead of Torznab XML: what is under test here is the fan-out — concurrency,
// merging, per-indexer failures — not the wire format.
//
// An indexer's behavior is selected by the name in its URL path, so a test
// configures a "good" and a "broken" indexer by pointing them at different
// paths on the same server.
type fakeIndexer struct {
	server *httptest.Server

	mu         sync.Mutex
	responses  map[string][]core.Release
	byQuery    map[string][]core.Release
	categories map[string][]core.IndexerCategory
	broken     map[string]bool
	searches   []fakeSearch
}

// fakeSearch is one recorded query, so a test can assert what the handler
// actually asked the indexer for.
type fakeSearch struct {
	name  string
	query string
	cats  string
}

func newFakeIndexer(t *testing.T) *fakeIndexer {
	t.Helper()
	f := &fakeIndexer{
		responses:  map[string][]core.Release{},
		byQuery:    map[string][]core.Release{},
		categories: map[string][]core.IndexerCategory{},
		broken:     map[string]bool{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /{name}/search", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		f.mu.Lock()
		f.searches = append(f.searches, fakeSearch{
			name:  name,
			query: r.URL.Query().Get("q"),
			cats:  r.URL.Query().Get("cats"),
		})
		broken := f.broken[name]
		// A query-specific answer wins: a scene is searched for twice, and a
		// test about the two variants has to be able to answer them
		// differently.
		releases, ok := f.byQuery[r.URL.Query().Get("q")]
		if !ok {
			releases = f.responses[name]
		}
		f.mu.Unlock()

		if broken {
			http.Error(w, "indexer is down", http.StatusInternalServerError)
			return
		}
		_ = json.NewEncoder(w).Encode(releases)
	})
	mux.HandleFunc("GET /{name}/categories", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		f.mu.Lock()
		broken := f.broken[name]
		cats := f.categories[name]
		f.mu.Unlock()
		if broken {
			http.Error(w, "bad api key", http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(cats)
	})
	mux.HandleFunc("GET /{name}/test", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		broken := f.broken[r.PathValue("name")]
		f.mu.Unlock()
		if broken {
			http.Error(w, "bad api key", http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
	})

	f.server = httptest.NewServer(mux)
	t.Cleanup(f.server.Close)
	return f
}

// url is the base URL an indexer configuration points at to get the behavior
// registered under name.
func (f *fakeIndexer) url(name string) string { return f.server.URL + "/" + name }

func (f *fakeIndexer) serve(name string, releases ...core.Release) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.responses[name] = releases
}

// servesQuery answers one exact query with these releases, whichever indexer is
// asked. It is how a test drives the scene search's date and title variants
// apart.
func (f *fakeIndexer) servesQuery(query string, releases ...core.Release) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.byQuery[query] = releases
}

func (f *fakeIndexer) servesCategories(name string, cats ...core.IndexerCategory) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.categories[name] = cats
}

func (f *fakeIndexer) breaks(name string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.broken[name] = true
}

func (f *fakeIndexer) recorded() []fakeSearch {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeSearch(nil), f.searches...)
}

// factory is the IndexerFactory the server is built with.
func (f *fakeIndexer) factory() IndexerFactory {
	return func(cfg core.IndexerConfig) IndexerClient {
		return &fakeIndexerClient{cfg: cfg, hc: f.server.Client()}
	}
}

type fakeIndexerClient struct {
	cfg core.IndexerConfig
	hc  *http.Client
}

func (c *fakeIndexerClient) Search(ctx context.Context, q string, cats []int) ([]core.Release, error) {
	catStrings := make([]string, 0, len(cats))
	for _, cat := range cats {
		catStrings = append(catStrings, fmt.Sprint(cat))
	}
	target := fmt.Sprintf("%s/search?q=%s&cats=%s", c.cfg.URL, url.QueryEscape(q), strings.Join(catStrings, ","))

	res, err := c.get(ctx, target)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search: unexpected status %d", res.StatusCode)
	}

	var releases []core.Release
	if err := json.NewDecoder(res.Body).Decode(&releases); err != nil {
		return nil, fmt.Errorf("search: decode: %w", err)
	}
	return releases, nil
}

func (c *fakeIndexerClient) Test(ctx context.Context) error {
	res, err := c.get(ctx, c.cfg.URL+"/test")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("test: unexpected status %d", res.StatusCode)
	}
	return nil
}

func (c *fakeIndexerClient) Categories(ctx context.Context) ([]core.IndexerCategory, error) {
	res, err := c.get(ctx, c.cfg.URL+"/categories")
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("categories: unexpected status %d", res.StatusCode)
	}
	var cats []core.IndexerCategory
	if err := json.NewDecoder(res.Body).Decode(&cats); err != nil {
		return nil, fmt.Errorf("categories: decode: %w", err)
	}
	return cats, nil
}

func (c *fakeIndexerClient) get(ctx context.Context, target string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	return c.hc.Do(req)
}

// newAcquisitionServer builds a server with both phase-2 dependencies wired to
// stubs, over a real store.
func newAcquisitionServer(t *testing.T) (http.Handler, *store.Store, *stubEngine, *fakeIndexer) {
	t.Helper()
	engine := &stubEngine{}
	fake := newFakeIndexer(t)
	h, st, _ := newTestServer(t,
		WithEngine(&stubEngineProvider{engine: engine}),
		WithIndexerClients(fake.factory()))
	return h, st, engine, fake
}

// addIndexer stores an enabled indexer pointing at the fake server's named
// behavior and returns it.
func addIndexer(t *testing.T, st *store.Store, fake *fakeIndexer, name string, categories ...int) core.IndexerConfig {
	t.Helper()
	cfg := core.IndexerConfig{
		Name:       name,
		URL:        fake.url(name),
		APIKey:     "secret",
		Type:       core.IndexerTypeTorznab,
		Categories: categories,
		Enabled:    true,
	}
	if err := st.UpsertIndexer(context.Background(), &cfg); err != nil {
		t.Fatalf("UpsertIndexer: %v", err)
	}
	return cfg
}
