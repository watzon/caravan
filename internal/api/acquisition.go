package api

import (
	"context"
	"net/http"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
)

// The acquisition endpoints (indexers, interactive search, grabs, the download
// queue) need two collaborators phase 1 had no use for: a download engine and
// a way to build an indexer client from a stored configuration. Both arrive
// through NewServer options rather than as required parameters, so a server
// built without them still serves the whole phase-1 API and answers the
// acquisition endpoints with a 503 the UI can explain.

// EngineProvider hands the HTTP layer the download engine to drive.
//
// It is an indirection rather than a plain core.Engine for the same reason
// Manager.Metadata is one: the engine cannot exist until the storage root is
// configured, so the serving process needs to be able to supply (or replace)
// it after the handler has been built.
type EngineProvider interface {
	// Engine returns the configured engine, or nil when none is configured.
	Engine() core.Engine
	// Name identifies the backend for the `downloads` table ("embedded",
	// "qbittorrent", …). It is recorded per download because a library can
	// outlive the engine that fetched it.
	Name() string
}

// LibraryEngineProvider is an optional EngineProvider extension for a provider
// that honours per-library download routing (PLAN phase 8 task 2).
//
// It is optional for the same reason the health extensions are: a provider
// with a single engine behind it — a test double, a build with nothing
// configured — is a complete EngineProvider already, and routes globally.
type LibraryEngineProvider interface {
	// EngineFor returns the engine a grab on behalf of the library of this
	// core.LibraryKind* must go through, or nil when none is configured.
	EngineFor(kind string) core.Engine
}

// HealthReporter is an optional EngineProvider extension for providers that
// can distinguish "not built yet" from "tried and failed" — the system panel
// shows the difference.
type HealthReporter interface {
	// Health returns "ok", "unconfigured", or "error".
	Health() string
}

// DownloadClientHealthReporter is an optional EngineProvider extension for a
// provider that polls external download clients and can say which of them are
// currently unreachable (PLAN phase 6 task 4).
//
// It is separate from HealthReporter because the two answer different
// questions: HealthReporter is about the embedded engine existing at all, this
// is about a machine on the network having stopped answering. A provider with
// no external clients configured returns nothing, and the UI shows no banner.
type DownloadClientHealthReporter interface {
	UnhealthyDownloadClients() []core.DownloadClientHealth
}

// IndexerClient is the slice of internal/indexer the HTTP layer uses. It is
// declared here, as an interface, for the same reason Manager is: this package
// compiles and tests without the network half of the application.
// *indexer.Client is expected to satisfy it.
type IndexerClient interface {
	// Search queries one indexer. cats are indexer category ids.
	Search(ctx context.Context, q string, cats []int) ([]core.Release, error)
	// Test verifies the indexer answers with the configured credentials.
	Test(ctx context.Context) error
	// Categories returns the category tree the indexer advertises, for the
	// settings picker.
	Categories(ctx context.Context) ([]core.IndexerCategory, error)
}

// IndexerFactory builds a client for a stored indexer configuration. The
// serving process wires this to indexer.New so the HTTP layer never has to
// know how a client is constructed.
//
// It is called concurrently: one interactive search fans out across every
// enabled indexer at once.
type IndexerFactory func(cfg core.IndexerConfig) IndexerClient

// Option configures the optional dependencies of NewServer.
type Option func(*server)

// WithEngine supplies the download engine the grab and queue endpoints drive.
func WithEngine(p EngineProvider) Option {
	return func(s *server) { s.engine = p }
}

// WithIndexerClients supplies the factory the search and indexer-test
// endpoints build clients with.
func WithIndexerClients(f IndexerFactory) Option {
	return func(s *server) { s.indexers = f }
}

// WithDownloadClients supplies the external download-client registry the
// /download-clients test endpoints probe through (SPEC §5.1, PLAN phase 6).
// Without it the process-wide clients.Default is used, which is what the
// serving process registers its backends into.
func WithDownloadClients(r *clients.Registry) Option {
	return func(s *server) { s.downloadClients = r }
}

// Engine-side categories a grab is labelled with, for users who sort their
// download client by label.
const (
	engineCategoryMovies = "movies"
	engineCategoryTV     = "tv"
)

// requireEngine resolves the download engine for an operation that belongs to
// no library — the queue, a pause, a removal — writing a 503 and returning
// false when none is configured.
func (s *server) requireEngine(w http.ResponseWriter) (core.Engine, bool) {
	return s.requireEngineFor(w, "")
}

// requireEngineFor is requireEngine for a grab made on behalf of one library,
// so a library that routes its downloads elsewhere is honoured rather than
// stored and ignored (PLAN phase 8 task 2). A provider that does not implement
// LibraryEngineProvider routes globally, exactly as it did before.
func (s *server) requireEngineFor(w http.ResponseWriter, kind string) (core.Engine, bool) {
	if s.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "no download engine configured")
		return nil, false
	}
	engine := s.libraryEngine(kind)
	if engine == nil {
		writeError(w, http.StatusServiceUnavailable, "no download engine configured")
		return nil, false
	}
	return engine, true
}

// libraryEngine picks the engine for a library kind, falling back to the
// provider's single engine when it has no per-library answer.
func (s *server) libraryEngine(kind string) core.Engine {
	if p, ok := s.engine.(LibraryEngineProvider); ok && kind != "" {
		return p.EngineFor(kind)
	}
	return s.engine.Engine()
}

// engineName is the backend name recorded on a download row.
func (s *server) engineName() string {
	if s.engine == nil {
		return ""
	}
	return s.engine.Name()
}

// engineNameFor is engineName for a specific release protocol.
//
// A routing engine is several engines behind one interface (PLAN phase 6 task
// 3), so the download row has to record the backend that actually took the
// release — that column is what addresses the download afterwards, and a
// library can outlive the engine that fetched it. An engine that does not
// route, or a protocol it will not take, falls back to the provider's name.
func (s *server) engineNameFor(ctx context.Context, engine core.Engine, protocol string) string {
	if router, ok := engine.(core.EngineRouting); ok {
		if name := router.EngineNameFor(ctx, protocol); name != "" {
			return name
		}
	}
	return s.engineName()
}

// requireIndexerClients resolves the indexer client factory, writing a 503 and
// returning false when none is configured.
func (s *server) requireIndexerClients(w http.ResponseWriter) (IndexerFactory, bool) {
	if s.indexers == nil {
		writeError(w, http.StatusServiceUnavailable, "no indexer client configured")
		return nil, false
	}
	return s.indexers, true
}

// writeEngineError reports a download-engine failure. Like the metadata
// provider, the engine is a system of its own: its failures are 502s, and the
// detail stays in the log rather than in the response.
func (s *server) writeEngineError(w http.ResponseWriter, msg string, err error) {
	s.log.Error(msg, "error", err)
	writeError(w, http.StatusBadGateway, msg)
}
