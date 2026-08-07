package main

import (
	"context"
	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/library"
	"log/slog"
	"time"
)

// await blocks until an engine for the given library exists, returning nil
// if ctx is done first. (0, "") is an operation belonging to no library.
func (p *engineProvider) await(ctx context.Context, libraryID int64, kind string) core.Engine {
	ticker := time.NewTicker(engineWaitInterval)
	defer ticker.Stop()
	for {
		if engine := p.EngineFor(libraryID, kind); engine != nil {
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
	engine := engines.await(ctx, 0, "")
	if engine == nil {
		return
	}

	root, err := adapter.StorageRoot(ctx)
	if err != nil {
		log.Error("import watcher: read storage root", "error", err)
		return
	}
	mgr := adapter.watcherManager(root)

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

func (l lateMetadata) GetMovie(ctx context.Context, ref string) (*core.MovieMeta, error) {
	p, err := l.provider(ctx)
	if err != nil {
		return nil, err
	}
	return p.GetMovie(ctx, ref)
}

func (l lateMetadata) GetSeries(ctx context.Context, ref string) (*core.SeriesMeta, error) {
	p, err := l.provider(ctx)
	if err != nil {
		return nil, err
	}
	return p.GetSeries(ctx, ref)
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
	_ api.EngineProvider        = (*engineProvider)(nil)
	_ api.LibraryEngineProvider = (*engineProvider)(nil)
	_ core.MetadataProvider     = lateMetadata{}
	_ download.Persistence      = downloadPersistence{}
)
