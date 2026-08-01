package api

import (
	"context"
	"net/http"

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

// IndexerClient is the slice of internal/indexer the HTTP layer uses. It is
// declared here, as an interface, for the same reason Manager is: this package
// compiles and tests without the network half of the application.
// *indexer.Client is expected to satisfy it.
type IndexerClient interface {
	// Search queries one indexer. cats are indexer category ids.
	Search(ctx context.Context, q string, cats []int) ([]core.Release, error)
	// Test verifies the indexer answers with the configured credentials.
	Test(ctx context.Context) error
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

// Torznab/Newznab top-level categories (SPEC §5.1). They are the fallback for
// an indexer that carries no category configuration of its own: searching
// every category would drown a movie picker in TV results and vice versa.
const (
	categoryMovies = 2000
	categoryTV     = 5000
)

// Engine-side categories a grab is labelled with, for users who sort their
// download client by label.
const (
	engineCategoryMovies = "movies"
	engineCategoryTV     = "tv"
)

// requireEngine resolves the download engine, writing a 503 and returning
// false when none is configured.
func (s *server) requireEngine(w http.ResponseWriter) (core.Engine, bool) {
	if s.engine == nil {
		writeError(w, http.StatusServiceUnavailable, "no download engine configured")
		return nil, false
	}
	engine := s.engine.Engine()
	if engine == nil {
		writeError(w, http.StatusServiceUnavailable, "no download engine configured")
		return nil, false
	}
	return engine, true
}

// engineName is the backend name recorded on a download row.
func (s *server) engineName() string {
	if s.engine == nil {
		return ""
	}
	return s.engine.Name()
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
