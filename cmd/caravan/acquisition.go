package main

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/indexer"
	"github.com/watzon/caravan/internal/library"
	"github.com/watzon/caravan/internal/store"
)

// indexerTimeout bounds a single Torznab/Newznab call. An interactive search
// fans out across every enabled indexer and waits for all of them, so a dead
// indexer must time out well inside a user's patience.
const indexerTimeout = 30 * time.Second

// engineWaitInterval is how often the import watcher re-checks for a download
// engine while none exists. It only matters on a first run, between the
// process starting and the storage root being set from the setup screen.
const engineWaitInterval = 5 * time.Second

// newIndexerFactory wires api.IndexerFactory to the real Torznab/Newznab
// client. The clients share one http.Client so a fan-out reuses connections
// to an indexer instead of opening one per search.
func newIndexerFactory() api.IndexerFactory {
	hc := &http.Client{Timeout: indexerTimeout}
	return func(cfg core.IndexerConfig) api.IndexerClient {
		return indexer.New(cfg, hc)
	}
}

// downloadPersistence is download.Persistence backed by the `downloads` table.
//
// It exists here rather than in internal/download because that package must
// not import internal/store: the engine is written against a seam so the
// phase-6 external clients can be persisted the same way. This is that seam's
// only production implementation.
type downloadPersistence struct {
	st *store.Store
}

// Save takes d by value per the seam's contract, so the id UpsertDownload
// writes back lands on this copy and is discarded — the engine identifies
// downloads by their engine handle, not by the row id.
func (p downloadPersistence) Save(ctx context.Context, d core.Download) error {
	return p.st.UpsertDownload(ctx, &d)
}

func (p downloadPersistence) Load(ctx context.Context) ([]core.Download, error) {
	return p.st.ListDownloads(ctx)
}

func (p downloadPersistence) Delete(ctx context.Context, id core.DownloadID) error {
	return p.st.DeleteDownloadByEngineID(ctx, id)
}

// engineProvider builds the embedded download engine on first use and hands
// the same one out thereafter.
//
// It is lazy because the engine needs a storage root and a first run does not
// have one yet (SPEC §10.1): the process must start, serve the setup screen,
// and only then be able to download. Construction is also what resumes the
// queue — download.NewEmbedded re-adds everything Persistence remembers — so
// the first Engine call after a restart is the restart's resume.
//
// The root is captured when the engine is built. Re-pointing the storage root
// afterwards does not move a running engine's in-progress data; that takes a
// restart, at which point the new root is picked up like any other.
type engineProvider struct {
	adapter *libraryAdapter
	// paused starts restored downloads paused. Portable mode wants this: a
	// freshly plugged-in drive should not start seeding on its own (SPEC §2.3).
	paused bool
	log    *slog.Logger

	mu     sync.Mutex
	engine *download.Embedded
	// lastErr is the last construction failure reported, so an engine that
	// cannot start does not log once per poll.
	lastErr string
}

func newEngineProvider(adapter *libraryAdapter, paused bool, log *slog.Logger) *engineProvider {
	return &engineProvider{adapter: adapter, paused: paused, log: log}
}

// Name identifies the backend on the download rows this engine creates.
func (p *engineProvider) Name() string { return download.EngineName }

// Health implements api.HealthReporter for the system panel: "ok" once the
// engine runs, "error" when building it failed, "unconfigured" before a
// storage root exists to build it under.
func (p *engineProvider) Health() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.engine != nil:
		return "ok"
	case p.lastErr != "":
		return "error"
	default:
		return "unconfigured"
	}
}

// Engine returns the download engine, or nil when there is no storage root to
// build one under or building one failed. Callers treat nil as "not
// configured" and answer 503; a failure is therefore logged here, since it is
// the only place that knows the difference.
func (p *engineProvider) Engine() core.Engine {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.engine != nil {
		return p.engine
	}

	ctx := context.Background()
	root, err := p.adapter.StorageRoot(ctx)
	if err != nil {
		p.reportLocked("read storage root", err)
		return nil
	}
	if root == "" {
		return nil
	}

	engine, err := download.NewEmbedded(root, download.EmbeddedOpts{
		Paused: p.paused,
		Store:  downloadPersistence{st: p.adapter.st},
		Logger: p.log,
	})
	if err != nil {
		p.reportLocked("start download engine", err)
		return nil
	}

	p.engine = engine
	p.lastErr = ""
	p.log.Info("download engine ready", "root", root, "incomplete", download.IncompleteDir)
	return engine
}

// reportLocked logs a construction failure, skipping a repeat of the one
// already reported. Must be called with p.mu held.
func (p *engineProvider) reportLocked(msg string, err error) {
	if err.Error() == p.lastErr {
		return
	}
	p.lastErr = err.Error()
	p.log.Error(msg, "error", err)
}

// Close shuts the engine down, flushing the queue's state so the next start
// resumes it. Closing before an engine was ever built is not an error.
func (p *engineProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.engine == nil {
		return nil
	}
	engine := p.engine
	p.engine = nil
	return engine.Close()
}

// await blocks until an engine exists, returning nil if ctx is done first.
func (p *engineProvider) await(ctx context.Context) core.Engine {
	ticker := time.NewTicker(engineWaitInterval)
	defer ticker.Stop()
	for {
		if engine := p.Engine(); engine != nil {
			return engine
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// runImportWatcher runs the import pipeline's clock (SPEC §5.1) until ctx is
// done: poll the engine, persist what it reports, and import what finished.
//
// It waits for an engine rather than requiring one, so a first run reaches the
// setup screen and then starts importing without a restart.
func runImportWatcher(ctx context.Context, engines *engineProvider, adapter *libraryAdapter, log *slog.Logger) {
	engine := engines.await(ctx)
	if engine == nil {
		return
	}

	root, err := adapter.StorageRoot(ctx)
	if err != nil {
		log.Error("import watcher: read storage root", "error", err)
		return
	}
	// The provider is resolved per call rather than captured: the watcher
	// outlives any one settings state, and a TMDB key set after it started
	// must reach the next import rather than the next restart.
	mgr := library.NewManager(adapter.st, lateMetadata{adapter: adapter}, root)

	log.Info("import watcher started", "interval", library.DefaultWatchInterval)
	if err := mgr.RunWatcher(ctx, engine, library.DefaultWatchInterval); err != nil && ctx.Err() == nil {
		log.Error("import watcher stopped", "error", err)
	}
}

// lateMetadata is a core.MetadataProvider that resolves the real one on every
// call from the settings in force at that moment.
//
// The HTTP layer gets late binding for free, because it asks the adapter for a
// provider per request (see libraryAdapter). A watcher does not: it holds one
// library.Manager for the life of the process, so without this a TMDB key
// added after startup would never reach it — and an import with no provider
// does not park, it errors, so the job would burn its retries and fail for
// good.
type lateMetadata struct {
	adapter *libraryAdapter
}

func (l lateMetadata) SearchMovies(ctx context.Context, q string) ([]core.MovieMeta, error) {
	p, err := l.provider(ctx)
	if err != nil {
		return nil, err
	}
	return p.SearchMovies(ctx, q)
}

func (l lateMetadata) SearchSeries(ctx context.Context, q string) ([]core.SeriesMeta, error) {
	p, err := l.provider(ctx)
	if err != nil {
		return nil, err
	}
	return p.SearchSeries(ctx, q)
}

func (l lateMetadata) GetMovie(ctx context.Context, tmdbID int64) (*core.MovieMeta, error) {
	p, err := l.provider(ctx)
	if err != nil {
		return nil, err
	}
	return p.GetMovie(ctx, tmdbID)
}

func (l lateMetadata) GetSeries(ctx context.Context, tmdbID int64) (*core.SeriesMeta, error) {
	p, err := l.provider(ctx)
	if err != nil {
		return nil, err
	}
	return p.GetSeries(ctx, tmdbID)
}

// provider resolves the configured provider, reporting the absence of one as
// core.ErrNoMetadataProvider — the same error a nil provider produces, so
// callers cannot tell the two apart.
func (l lateMetadata) provider(ctx context.Context) (core.MetadataProvider, error) {
	p := l.adapter.metadata(ctx)
	if p == nil {
		return nil, core.ErrNoMetadataProvider
	}
	return p, nil
}

// Compile-time proof that the wiring is what its consumers expect.
var (
	_ api.EngineProvider    = (*engineProvider)(nil)
	_ core.MetadataProvider = lateMetadata{}
	_ download.Persistence  = downloadPersistence{}
)
