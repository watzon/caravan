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
	"sync"
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

	// stash is the adult library's handoff (PLAN phase 11). Nil means the
	// process wired none, which the status endpoint reports as no trouble.
	stash StashService

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

	// logins bounds password derivations for both login and first-run setup.
	logins *loginGuard
	// setupMu serializes first-admin creation so only one request can observe
	// the empty-user state and close the open server.
	setupMu sync.Mutex

	// listenAddr is the address the process bound, supplied by the serving
	// process with WithListenAddr. It is reported through GET /system/status so
	// the UI can nag about listening on every interface without a password.
	listenAddr string

	// runtime is the serving process configuration reported to administrators
	// for support diagnostics. It is nil in tests and embedded uses that do not
	// supply process paths.
	runtime   *RuntimeConfig
	startedAt time.Time

	// dirty says the previous session ended without a clean shutdown
	// (SPEC §2.3). It is atomic because POST /system/verify clears it from one
	// request while the status endpoint and the queue read it from others.
	dirty atomic.Bool

	// credentials is the cached verdict on the TMDB API key (SPEC §10.1, PLAN
	// phase 10 task 2). It is why GET /system/status can report credential
	// health on every poll without a single upstream call; see credentials.go.
	credentials metadataCredential

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
		startedAt:  time.Now(),
	}
	for _, opt := range opts {
		opt(s)
	}

	// Registered without the /api/v1 prefix and mounted below, so the prefix
	// lives in exactly one place.
	api := newPolicyMux()
	api.HandleFunc("GET /settings", s.handleGetSettings)
	api.HandleFunc("PUT /settings", s.handlePutSettings)
	api.HandleFunc("POST /settings/apikey", s.handleGenerateAPIKey)
	// The metadata credential's Test button (PLAN phase 10 task 4), the twin of
	// POST /indexers/{id}/test. It takes the key from the body so the first-run
	// wizard can prove one before it is saved.
	api.HandleFunc("POST /settings/metadata/test", s.handleMetadataTest)
	api.HandleFunc("GET /system/status", s.handleSystemStatus)
	api.HandleFunc("GET /system/backup", s.handleBackup)
	api.HandleFunc("POST /system/restore", s.handleRestore)

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
	// First-run setup is the one unauthenticated write. The handler itself
	// refuses once any account exists and issues the first session.
	api.HandleFunc("POST /setup/admin", s.handleSetupAdmin)

	// Accounts and sessions (SPEC §11). Login and logout are exempt from the
	// gate below; /auth/me and the password change are not, so both always
	// speak for the current session. Changing a password here only ever
	// changes the caller's own.
	api.HandleFunc("POST /auth/login", s.handleLogin)
	api.HandleFunc("POST /auth/logout", s.handleLogout)
	api.HandleFunc("GET /auth/me", s.handleMe)
	api.HandleFunc("POST /settings/password", s.handleSetPassword)

	// Managing other people's accounts, which is an admin's job: memberAllowed
	// names none of these, so a member is turned away by the gate itself. On a
	// server with no accounts everyone is an implicit admin, which is what
	// makes POST /users the route that closes an open server.
	api.HandleFunc("GET /users", s.handleListUsers)
	api.HandleFunc("POST /users", s.handleCreateUser)
	api.HandleFunc("DELETE /users/{id}", s.handleDeleteUser)
	api.HandleFunc("POST /users/{id}/password", s.handleResetUserPassword)

	// Quality profiles (PLAN phase 3, task 1) and the wanted list they drive
	// (task 2).
	api.HandleFunc("GET /quality-profiles", s.handleListQualityProfiles)
	api.HandleFunc("POST /quality-profiles", s.handleCreateQualityProfile)
	api.HandleFunc("GET /quality-profiles/export", s.handleExportQualityProfiles)
	api.HandleFunc("POST /quality-profiles/import", s.handleImportQualityProfiles)
	api.HandleFunc("PUT /quality-profiles/{id}", s.handleUpdateQualityProfile)
	api.HandleFunc("PUT /quality-profiles/{id}/default", s.handleSetDefaultQualityProfile)
	api.HandleFunc("GET /notification-webhooks", s.handleListNotificationWebhooks)
	api.HandleFunc("POST /notification-webhooks", s.handleCreateNotificationWebhook)
	api.HandleFunc("PUT /notification-webhooks/{id}", s.handleUpdateNotificationWebhook)
	api.HandleFunc("DELETE /notification-webhooks/{id}", s.handleDeleteNotificationWebhook)
	api.HandleFunc("POST /notification-webhooks/{id}/test", s.handleTestNotificationWebhook)
	api.HandleFunc("POST /quality-profiles/{id}/test", s.handleTestQualityProfile)
	api.HandleFunc("DELETE /quality-profiles/{id}", s.handleDeleteQualityProfile)
	api.HandleFunc("GET /wanted", s.handleWanted)
	api.HandleFunc("POST /wanted/search", s.handleSearchWanted)

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

	// The recurring background tasks, as the Settings screen shows them: what
	// runs on a timer, when it last ran and how that went, and a button that
	// brings the next run forward. Admin-only by the ordinary rule —
	// memberAllowed names neither.
	api.HandleFunc("GET /system/tasks", s.handleListTasks)
	api.HandleFunc("POST /system/tasks/{kind}/run", s.handleRunTask)
	api.HandleFunc("PUT /system/tasks/{kind}", s.handleUpdateTaskInterval)

	api.HandleFunc("GET /library/movies", s.handleListMovies)
	api.HandleFunc("POST /library/movies", s.handleAddMovie)
	api.HandleFunc("GET /library/movies/{id}", s.handleGetMovie)
	api.HandleFunc("PATCH /library/movies/{id}", s.handlePatchMovie)
	api.HandleFunc("DELETE /library/movies/{id}", s.handleDeleteMovie)
	api.HandleFunc("GET /library/series", s.handleListSeries)
	api.HandleFunc("POST /library/series", s.handleAddSeries)
	api.HandleFunc("GET /library/series/{id}", s.handleGetSeries)
	api.HandleFunc("PATCH /library/series/{id}", s.handlePatchSeries)
	api.HandleFunc("DELETE /library/series/{id}", s.handleDeleteSeries)
	api.HandleFunc("PATCH /library/series/{id}/seasons/{season}", s.handlePatchSeason)
	api.HandleFunc("PATCH /library/episodes/{id}", s.handlePatchEpisode)
	api.HandleFunc("POST /library/rescan", s.handleRescan)

	// The interactive picker and its grab (SPEC §9 step 4). The series pair
	// takes ?season= and ?episode= to narrow the search from the whole series
	// down to one episode.
	api.HandleFunc("GET /library/movies/{id}/releases", s.handleMovieReleases)
	api.HandleFunc("POST /library/movies/{id}/grab", s.handleMovieGrab)
	api.HandleFunc("POST /library/movies/{id}/move", s.handleMoveMovie)
	api.HandleFunc("POST /library/series/{id}/move", s.handleMoveSeries)
	api.HandleFunc("GET /library/series/{id}/releases", s.handleSeriesReleases)
	api.HandleFunc("POST /library/series/{id}/grab", s.handleSeriesGrab)

	// Automatic search on demand (SPEC §9): the same jobs the backlog sweep
	// fans out, queued for one item now. They answer 202 and a count rather
	// than releases — the work happens on the job queue, not in the request.
	api.HandleFunc("POST /library/movies/{id}/search", s.handleSearchMovieNow)
	api.HandleFunc("POST /library/series/{id}/search", s.handleSearchSeriesNow)
	api.HandleFunc("POST /library/episodes/{id}/search", s.handleSearchEpisodeNow)

	api.HandleFunc("GET /search", s.handleSearch)

	// Discover: browse the provider rather than search it. /discover is the
	// landing page, /discover/browse pages one curated shelf, and
	// /discover/{type}/{id} is one title's detail screen. Every response is
	// decorated with what the library already holds and what has been
	// requested, so the SPA never has to cross-reference two calls.
	api.HandleFunc("GET /discover", s.handleDiscoverHome)
	api.HandleFunc("GET /discover/browse", s.handleDiscoverBrowse)
	// The filtered scopes (PLAN phase 12 tasks 1 and 3). One per media type
	// rather than one endpoint with a ?type=, because the filter surfaces are
	// not the same shape: only movies can be filtered by a person, and a
	// single endpoint would have to accept the parameter and then refuse it.
	// Their query strings are the whole filter, so a filtered view is a URL.
	api.HandleFunc("GET /discover/movies", s.handleDiscoverMovies)
	api.HandleFunc("GET /discover/series", s.handleDiscoverSeries)
	// What the filter rail's controls are populated from: three typeaheads
	// onto the provider's own indexes, and the fixed genre vocabularies.
	api.HandleFunc("GET /discover/people", s.handleDiscoverPeople)
	api.HandleFunc("GET /discover/companies", s.handleDiscoverCompanies)
	api.HandleFunc("GET /discover/keywords", s.handleDiscoverKeywords)
	api.HandleFunc("GET /discover/genres", s.handleDiscoverGenres)
	api.HandleFunc("GET /discover/{type}/{id}", s.handleDiscoverTitle)

	// Requests: a wish for something not in the library. Approving one takes
	// the same add path the library endpoints do, and any add absorbs a
	// matching pending request (see absorbRequests). Members may make, list
	// and cancel their own; approving is an admin's decision (memberAllowed).
	api.HandleFunc("GET /requests", s.handleListRequests)
	api.HandleFunc("POST /requests", s.handleCreateRequest)
	api.HandleFunc("POST /requests/{id}/approve", s.handleApproveRequest)
	api.HandleFunc("DELETE /requests/{id}", s.handleDismissRequest)

	// The library sections and the settings each may answer for itself
	// (SPEC §7, PLAN phase 8). Read-mostly and admin-only: memberAllowed names
	// none of these, so a member never sees them. The rows themselves are
	// seeded by the migration, so there is no create or delete — a library is
	// part of the layout, not a thing a user adds.
	api.HandleFunc("GET /libraries", s.handleListLibraries)
	api.HandleFunc("POST /libraries", s.handleCreateLibrary)
	api.HandleFunc("GET /libraries/providers", s.handleListProviders)
	api.HandleFunc("PATCH /libraries/{id}", s.handleUpdateLibrary)
	api.HandleFunc("DELETE /libraries/{id}", s.handleDeleteLibrary)
	api.HandleFunc("PUT /libraries/{id}/indexers/{indexerID}", s.handleSetLibraryIndexer)

	// The adult module (PLAN phase 9). Its routes are registered on a mux of
	// their own and mounted behind requireAdult, so the gate is a property of
	// where a route lives rather than of what its handler remembers to check:
	// with the module off, or for a caller who was never granted it,
	// everything under /adult is 404 before any handler runs.
	//
	// The subtree is mounted whether or not the module is enabled, because the
	// alternative — building the routing table from a settings row — would
	// make enabling it require a restart, and would make an unrouted /adult
	// path answer differently from a gated one. Register adult routes here and
	// nowhere else; the mux is not prefix-stripped, so the patterns read
	// exactly as the URLs do.
	// A member reaches only the routes memberAllowed also names; the rest are
	// admin-only by being absent from it, which is the same rule the whole API
	// runs on. Adding a route here without adding it there closes it to
	// members, never opens it — the failure direction that is safe.
	adult := newPolicyMux()
	adult.HandleFunc("GET /adult/sites", s.handleListSites)
	adult.HandleFunc("POST /adult/sites", s.handleAddSite)
	adult.HandleFunc("GET /adult/sites/{id}", s.handleGetSite)
	adult.HandleFunc("GET /adult/search", s.handleSearchSites)
	adult.HandleFunc("GET /adult/discover", s.handleAdultDiscover)
	// The scene filter rail's typeaheads (PLAN phase 12 task 3). They are on
	// this mux and member-allowed for the same reason /adult/discover is: the
	// rail is part of the browse screen a granted member reads, and a control
	// that 403s is worse than no control. They read the provider only — no
	// library row is involved — so there is nothing here an admin has that a
	// granted member does not.
	adult.HandleFunc("GET /adult/performers", s.handleAdultPerformers)
	adult.HandleFunc("GET /adult/tags", s.handleAdultTags)
	// The member-access card. It lives under /adult rather than beside
	// GET /users so that the accounts API carries no adult field on an install
	// that has never enabled the module.
	adult.HandleFunc("GET /adult/users", s.handleListAdultUsers)
	adult.HandleFunc("PUT /adult/users/{id}/access", s.handleSetAdultAccess)
	// The Stash handoff (PLAN phase 11), the adult twin of
	// /handoff/jellyfin. It lives here rather than beside its twin because
	// Stash is an adult-module feature: with the module off it must be absent,
	// not merely disabled, and mounting it on this subtree is what makes that
	// true without a check inside each handler. Admin-only by the ordinary
	// rule — memberAllowed names none of these. The scan and the identity push
	// are not endpoints: they are queued by the import pipeline and run by the
	// job queue, so the API only configures and proves the connection.
	adult.HandleFunc("GET /adult/stash", s.handleGetStash)
	adult.HandleFunc("POST /adult/stash", s.handleSetStash)
	adult.HandleFunc("POST /adult/stash/test", s.handleTestStash)
	api.Handle("/adult/", s.requireAdult(adult))

	// The master switch. It is the one adult route that cannot live behind
	// requireAdult, because turning the module ON is what it is for and the
	// gate would refuse it forever. It is admin-only instead, by the ordinary
	// rule: memberAllowed does not name it.
	api.HandleFunc("POST /settings/adult", s.handleSetAdultEnabled)

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

	// Remote path mappings translate paths reported by external clients into
	// the mount points visible to this Caravan process.
	api.HandleFunc("GET /remote-path-mappings", s.handleListRemotePathMappings)
	api.HandleFunc("POST /remote-path-mappings", s.handleCreateRemotePathMapping)
	api.HandleFunc("PUT /remote-path-mappings/{id}", s.handleUpdateRemotePathMapping)
	api.HandleFunc("DELETE /remote-path-mappings/{id}", s.handleDeleteRemotePathMapping)

	// News servers the embedded engine fetches article bodies from (SPEC §5.1,
	// PLAN phase 7 task 2). These are article sources, not download clients:
	// they are what the built-in engine reads Usenet releases from itself.
	// /test with no id in the path probes an unsaved configuration, exactly as
	// the download-client and indexer forms do.
	api.HandleFunc("GET /usenet-servers", s.handleListUsenetServers)
	api.HandleFunc("POST /usenet-servers", s.handleCreateUsenetServer)
	api.HandleFunc("POST /usenet-servers/test", s.handleTestUsenetServerConfig)
	api.HandleFunc("GET /usenet-servers/{id}", s.handleGetUsenetServer)
	api.HandleFunc("PUT /usenet-servers/{id}", s.handleUpdateUsenetServer)
	api.HandleFunc("DELETE /usenet-servers/{id}", s.handleDeleteUsenetServer)
	api.HandleFunc("POST /usenet-servers/{id}/test", s.handleTestUsenetServer)

	// Queue ids are the engine's own handles, not library ids.
	api.HandleFunc("GET /downloads", s.handleListDownloads)
	api.HandleFunc("POST /downloads/{id}/pause", s.handlePauseDownload)
	api.HandleFunc("POST /downloads/{id}/resume", s.handleResumeDownload)
	api.HandleFunc("POST /downloads/{id}/retry", s.handleRetryDownload)
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

// statusClientClosedRequest is nginx's 499: the caller hung up before the
// answer existed. Nobody receives it — it exists so the request log records
// "caller left" instead of a 5xx that pages someone.
const statusClientClosedRequest = 499

// clientGone reports whether the request's own context ended — a typeahead
// abort or a closed tab. An upstream "failure" that is just that cancellation
// propagating is normal operation, not an error worth an ERROR line.
func clientGone(r *http.Request) bool {
	return r.Context().Err() != nil
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
