// Package api is Caravan's HTTP surface: the REST API under /api/v1
// (SPEC §11) and the embedded Svelte SPA that consumes it.
//
// Phase 1 scope (PLAN phase 1, task 8): settings, system status, the library
// (movies and series), rescan, metadata search, the scan-review/import queue,
// and the activity feed. Phase 2 adds acquisition (PLAN phase 2, tasks 1, 2 and
// 4): indexer configuration, the interactive release picker, grabs, and the
// download queue. Phase 5 adds the optional single-user password, its session
// cookie and the API key (SPEC §11): with no password set every route behaves
// as it always did, and with one set /api/v1 needs a session or the API key —
// see auth.go for the deliberate exemptions.
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

	"github.com/watzon/caravan/internal/clients"
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

	// downloadClients is the phase-6 external-client registry (SPEC §5.1).
	// Nil means the process-wide one; see (*server).clients.
	downloadClients *clients.Registry

	// converter is the phase-4 convert-for-TV queue's ffmpeg availability
	// (SPEC §8). Nil means the same thing an ffmpeg-less host does: the queue
	// is readable, but nothing new can be enqueued.
	converter Converter

	// dlna is the built-in media server (SPEC §5.1). Nil means the feature is
	// not built in, which GET /dlna reports as off.
	dlna DLNAService

	// scanning is the single-flight guard for POST /library/rescan. A scan
	// walks the whole storage root and reconciles the database; running two
	// at once would have them fight over the same rows.
	scanning atomic.Bool

	// startupScan queues one scan as the server is built; see WithStartupScan.
	startupScan bool

	// sessions holds the live logins and sessionTTL is how long a new one
	// lasts (SPEC §11). Both are inert until a password is set.
	sessions   *sessionStore
	sessionTTL time.Duration

	// logins bounds POST /auth/login, the one gated route an unauthenticated
	// caller reaches and the only one that runs a 19 MiB key derivation.
	logins *loginGuard

	// listenAddr is the address the process bound, supplied by the serving
	// process with WithListenAddr. It is reported through GET /system/status so
	// the UI can nag about listening on every interface without a password.
	listenAddr string

	// dirty says the previous session ended without a clean shutdown
	// (SPEC §2.3). It is atomic because POST /system/verify clears it from one
	// request while the status endpoint and the queue read it from others.
	dirty atomic.Bool

	// shutdown is the orderly-stop trigger POST /system/shutdown pulls, wired
	// by the serving process to the same cancel a signal uses. Nil means this
	// process cannot stop itself, which the endpoint reports rather than fakes.
	shutdown func()
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
	s := &server{
		st:         st,
		mgr:        mgr,
		dist:       dist,
		log:        slog.Default(),
		sessions:   newSessionStore(),
		sessionTTL: defaultSessionTTL,
		logins:     newLoginGuard(),
	}
	for _, opt := range opts {
		opt(s)
	}

	// Registered without the /api/v1 prefix and mounted below, so the prefix
	// lives in exactly one place.
	api := http.NewServeMux()
	api.HandleFunc("GET /settings", s.handleGetSettings)
	api.HandleFunc("PUT /settings", s.handlePutSettings)
	api.HandleFunc("POST /settings/apikey", s.handleGenerateAPIKey)
	api.HandleFunc("GET /system/status", s.handleSystemStatus)

	// The portable integrity flow (SPEC §2.3, §13). Both are deliberately
	// inside the auth gate: stopping the server and clearing the dirty flag
	// are the last two things an unauthenticated visitor should be able to do.
	api.HandleFunc("POST /system/shutdown", s.handleShutdown)
	api.HandleFunc("POST /system/verify", s.handleVerify)

	// Moving the storage root (SPEC §10, PLAN phase 5 task 4). Re-pointing is
	// synchronous because it is one settings write; migrating answers 202 and
	// the progress endpoint is polled, because it moves the library.
	api.HandleFunc("POST /system/storage-root/repoint", s.handleRepointStorageRoot)
	api.HandleFunc("POST /system/storage-root/migrate", s.handleMigrateStorageRoot)
	api.HandleFunc("GET /system/storage-root/migration", s.handleStorageMigration)

	// The optional single-user password and its session (SPEC §11, PLAN phase 5
	// task 5). Login and logout are exempt from the gate below; setting the
	// password is not, so changing it always needs the current session.
	api.HandleFunc("POST /auth/login", s.handleLogin)
	api.HandleFunc("POST /auth/logout", s.handleLogout)
	api.HandleFunc("POST /settings/password", s.handleSetPassword)

	// Quality profiles (PLAN phase 3, task 1) and the wanted list they drive
	// (task 2).
	api.HandleFunc("GET /quality-profiles", s.handleListQualityProfiles)
	api.HandleFunc("POST /quality-profiles", s.handleCreateQualityProfile)
	api.HandleFunc("PUT /quality-profiles/{id}", s.handleUpdateQualityProfile)
	api.HandleFunc("DELETE /quality-profiles/{id}", s.handleDeleteQualityProfile)
	api.HandleFunc("GET /wanted", s.handleWanted)

	// The built-in TV profiles (SPEC §8, PLAN phase 4 task 3). Read-only: the
	// active choice is a settings key, not a row.
	api.HandleFunc("GET /tv-profiles", s.handleListTVProfiles)

	// The convert-for-TV queue (SPEC §8, PLAN phase 4 task 4). Listing works
	// without ffmpeg; queueing does not, and says so with a 503.
	api.HandleFunc("GET /convert", s.handleListConversions)
	api.HandleFunc("POST /convert", s.handleCreateConversion)
	api.HandleFunc("POST /convert/{id}/cancel", s.handleCancelConversion)
	api.HandleFunc("POST /convert/{id}/retry", s.handleRetryConversion)

	// The Jellyfin playback handoff (SPEC §5.2, PLAN phase 4 task 1). The scan
	// itself is not an endpoint: it is queued by the import pipeline and run by
	// the job queue, so the API only configures and proves the connection.
	// The built-in DLNA media server (SPEC §5.1, PLAN phase 4 task 2). Read
	// only: the toggle and the friendly name are settings keys, so they are
	// written through PUT /settings; what cannot be read from that table is
	// whether SSDP actually came up, which is what this reports.
	api.HandleFunc("GET /dlna", s.handleDLNAStatus)

	api.HandleFunc("GET /handoff/jellyfin", s.handleGetJellyfin)
	api.HandleFunc("POST /handoff/jellyfin", s.handleSetJellyfin)
	api.HandleFunc("POST /handoff/jellyfin/test", s.handleTestJellyfin)

	// The combined calendar and its iCal feed (PLAN phase 3, task 9).
	api.HandleFunc("GET /calendar", s.handleCalendar)
	api.HandleFunc("GET /calendar.ics", s.handleCalendarICS)

	// The job queue's activity feed (PLAN phase 3, task 8).
	api.HandleFunc("GET /jobs", s.handleListJobs)

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

	// External download clients (SPEC §5.1, PLAN phase 6 task 1). The type
	// list is served rather than hard-coded in the SPA so a build without a
	// backend says so instead of offering it. /test with no id in the path
	// probes an unsaved configuration, as the indexer category endpoint does.
	api.HandleFunc("GET /download-clients", s.handleListDownloadClients)
	api.HandleFunc("POST /download-clients", s.handleCreateDownloadClient)
	api.HandleFunc("GET /download-clients/types", s.handleListDownloadClientTypes)
	api.HandleFunc("POST /download-clients/test", s.handleTestDownloadClientConfig)
	api.HandleFunc("PUT /download-clients/{id}", s.handleUpdateDownloadClient)
	api.HandleFunc("DELETE /download-clients/{id}", s.handleDeleteDownloadClient)
	api.HandleFunc("POST /download-clients/{id}/test", s.handleTestDownloadClient)

	// Queue ids are the engine's own handles, not library ids.
	api.HandleFunc("GET /downloads", s.handleListDownloads)
	api.HandleFunc("POST /downloads/{id}/pause", s.handlePauseDownload)
	api.HandleFunc("POST /downloads/{id}/resume", s.handleResumeDownload)
	api.HandleFunc("DELETE /downloads/{id}", s.handleDeleteDownload)
	// Per-download insight and rate limits (PLAN phase 3, task 10).
	api.HandleFunc("GET /downloads/{id}/insight", s.handleDownloadInsight)
	api.HandleFunc("PUT /downloads/{id}/limits", s.handleSetDownloadLimits)

	// Artwork the organizer wrote into the storage root, addressed by the same
	// relative path the library rows carry.
	api.HandleFunc("GET /images/{path...}", s.handleImage)

	api.HandleFunc("GET /import/queue", s.handleImportQueue)
	api.HandleFunc("POST /import/queue/{id}/match", s.handleImportMatch)
	api.HandleFunc("DELETE /import/queue/{id}", s.handleImportDelete)

	api.HandleFunc("GET /events", s.handleEvents)

	root := http.NewServeMux()
	// requireAuth wraps the JSON API and nothing else: the SPA (whose login
	// screen has to load) and the DLNA surface (whose clients are televisions)
	// are outside this subtree, so they need no exemption list of their own.
	// It sits inside StripPrefix so its exemptions are written against the same
	// paths the routing table uses.
	//
	// requireSameOrigin wraps the gate rather than sitting inside it: the
	// cross-site defence has to hold in the passwordless default, where
	// requireAuth lets everything through (see origin.go).
	root.Handle("/api/v1/", http.StripPrefix("/api/v1",
		jsonErrors(requireSameOrigin(s.requireAuth(api)))))
	// The DLNA protocol surface sits outside /api/v1 and outside the JSON error
	// envelope: its URLs are the ones SSDP advertises and its clients are
	// televisions, which speak SOAP and expect SOAP faults.
	if s.dlna != nil {
		root.Handle(dlnaMountPrefix, s.dlna.Handler())
	}
	// A mistyped API path must not be answered with the SPA's index.html.
	root.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not found")
	})
	root.HandleFunc("/", s.handleSPA)

	// Last, so nothing is scanned by a server that failed to build.
	if s.startupScan {
		s.startScan()
	}

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
