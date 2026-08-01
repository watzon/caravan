// Package api is Caravan's HTTP surface: the REST API under /api/v1
// (SPEC §11) and the embedded Svelte SPA that consumes it.
//
// Phase 1 scope (PLAN phase 1, task 8): settings, system status, the library
// (movies and series), rescan, metadata search, the scan-review/import queue,
// and the activity feed. Phase 2 adds acquisition (PLAN phase 2, tasks 1, 2 and
// 4): indexer configuration, the interactive release picker, grabs, and the
// download queue. There is no authentication yet — SPEC §11 puts the optional
// password and API key in phase 5.
//
// Every failure answers with the same envelope, {"error": "..."}, including
// the routing failures the standard ServeMux would otherwise answer in plain
// text.
package api

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/watzon/caravan/internal/store"
)

// Version is the build version reported by GET /system/status. The main
// package overwrites it at startup; it is a package variable rather than a
// NewServer parameter because that signature is fixed by the cross-package
// contract.
var Version = "dev"

// server holds the dependencies every handler shares. It is unexported
// because NewServer returns an http.Handler: the routing table is the API,
// not the struct.
type server struct {
	st   *store.Store
	mgr  Manager
	dist fs.FS
	log  *slog.Logger

	// engine and indexers are the phase-2 acquisition dependencies, supplied
	// through options. Either may be nil, in which case the endpoints that
	// need it answer 503 rather than the server refusing to start.
	engine   EngineProvider
	indexers IndexerFactory

	// scanning is the single-flight guard for POST /library/rescan. A scan
	// walks the whole storage root and reconciles the database; running two
	// at once would have them fight over the same rows.
	scanning atomic.Bool
}

// NewServer builds the HTTP handler: the JSON API under /api/v1 and the SPA
// served from dist for everything else, with unknown non-API paths falling
// back to index.html so client-side routes survive a page reload.
//
// dist may be nil, in which case only the API is served; this is what a test
// or a headless deployment wants.
//
// The acquisition endpoints need a download engine and an indexer client
// factory, supplied with WithEngine and WithIndexerClients. They are options
// rather than parameters because they are configuration-dependent: a server
// with neither still serves the library.
func NewServer(st *store.Store, mgr Manager, dist fs.FS, opts ...Option) http.Handler {
	s := &server{st: st, mgr: mgr, dist: dist, log: slog.Default()}
	for _, opt := range opts {
		opt(s)
	}

	// Registered without the /api/v1 prefix and mounted below, so the prefix
	// lives in exactly one place.
	api := http.NewServeMux()
	api.HandleFunc("GET /settings", s.handleGetSettings)
	api.HandleFunc("PUT /settings", s.handlePutSettings)
	api.HandleFunc("GET /system/status", s.handleSystemStatus)

	api.HandleFunc("GET /library/movies", s.handleListMovies)
	api.HandleFunc("POST /library/movies", s.handleAddMovie)
	api.HandleFunc("GET /library/movies/{id}", s.handleGetMovie)
	api.HandleFunc("PATCH /library/movies/{id}", s.handlePatchMovie)
	api.HandleFunc("DELETE /library/movies/{id}", s.handleDeleteMovie)
	api.HandleFunc("GET /library/series", s.handleListSeries)
	api.HandleFunc("POST /library/series", s.handleAddSeries)
	api.HandleFunc("GET /library/series/{id}", s.handleGetSeries)
	api.HandleFunc("PATCH /library/series/{id}", s.handlePatchSeries)
	api.HandleFunc("PATCH /library/series/{id}/seasons/{season}", s.handlePatchSeason)
	api.HandleFunc("PATCH /library/episodes/{id}", s.handlePatchEpisode)
	api.HandleFunc("POST /library/rescan", s.handleRescan)

	// The interactive picker and its grab (SPEC §9 step 4). The series pair
	// takes ?season= and ?episode= to narrow the search from the whole series
	// down to one episode.
	api.HandleFunc("GET /library/movies/{id}/releases", s.handleMovieReleases)
	api.HandleFunc("POST /library/movies/{id}/grab", s.handleMovieGrab)
	api.HandleFunc("GET /library/series/{id}/releases", s.handleSeriesReleases)
	api.HandleFunc("POST /library/series/{id}/grab", s.handleSeriesGrab)

	api.HandleFunc("GET /search", s.handleSearch)

	api.HandleFunc("GET /indexers", s.handleListIndexers)
	api.HandleFunc("POST /indexers", s.handleCreateIndexer)
	api.HandleFunc("PUT /indexers/{id}", s.handleUpdateIndexer)
	api.HandleFunc("DELETE /indexers/{id}", s.handleDeleteIndexer)
	api.HandleFunc("POST /indexers/{id}/test", s.handleTestIndexer)
	api.HandleFunc("POST /indexers/categories", s.handleIndexerCategories)

	// Queue ids are the engine's own handles, not library ids.
	api.HandleFunc("GET /downloads", s.handleListDownloads)
	api.HandleFunc("POST /downloads/{id}/pause", s.handlePauseDownload)
	api.HandleFunc("POST /downloads/{id}/resume", s.handleResumeDownload)
	api.HandleFunc("DELETE /downloads/{id}", s.handleDeleteDownload)

	// Artwork the organizer wrote into the storage root, addressed by the same
	// relative path the library rows carry.
	api.HandleFunc("GET /images/{path...}", s.handleImage)

	api.HandleFunc("GET /import/queue", s.handleImportQueue)
	api.HandleFunc("POST /import/queue/{id}/match", s.handleImportMatch)
	api.HandleFunc("DELETE /import/queue/{id}", s.handleImportDelete)

	api.HandleFunc("GET /events", s.handleEvents)

	root := http.NewServeMux()
	root.Handle("/api/v1/", http.StripPrefix("/api/v1", jsonErrors(api)))
	// A mistyped API path must not be answered with the SPA's index.html.
	root.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	root.HandleFunc("/", s.handleSPA)

	return logRequests(s.log, root)
}

// jsonErrors rewrites the plain-text 404 and 405 replies http.ServeMux
// generates for unrouted requests into the API's JSON error envelope, so
// clients can parse every failure the same way. Handlers that write their own
// JSON are untouched.
func jsonErrors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(&jsonErrorWriter{ResponseWriter: w}, r)
	})
}

type jsonErrorWriter struct {
	http.ResponseWriter
	// replaced records that the plain-text body has already been swapped for
	// a JSON one, so the handler's own Write must be discarded.
	replaced bool
}

func (w *jsonErrorWriter) WriteHeader(status int) {
	// http.Error is the only thing in this path that sets a text/plain
	// content type on an error, which makes it a reliable marker.
	if status >= 400 && strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") {
		w.replaced = true
		w.Header().Set("Content-Type", contentTypeJSON)
	}
	w.ResponseWriter.WriteHeader(status)
	if w.replaced {
		_ = json.NewEncoder(w.ResponseWriter).Encode(errorResponse{Error: strings.ToLower(http.StatusText(status))})
	}
}

func (w *jsonErrorWriter) Write(b []byte) (int, error) {
	if w.replaced {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}

// logRequests logs one line per request. The query string is deliberately not
// logged: SPEC §12 keeps credentials out of the logs, and query parameters are
// where they would leak from.
func logRequests(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)

		level := slog.LevelInfo
		if rec.status >= 500 {
			level = slog.LevelError
		}
		log.Log(r.Context(), level, "http request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rec.status,
			"duration", time.Since(start))
	})
}

// statusRecorder captures the response status for the access log.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if !r.wrote {
		r.status = status
		r.wrote = true
	}
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(b []byte) (int, error) {
	r.wrote = true
	return r.ResponseWriter.Write(b)
}
