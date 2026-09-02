package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/automation"
	"github.com/watzon/caravan/internal/config"
	"github.com/watzon/caravan/internal/convert"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/dlna"
	"github.com/watzon/caravan/internal/indexer/packs"
	"github.com/watzon/caravan/internal/integrity"
	"github.com/watzon/caravan/internal/jellyfin"
	"github.com/watzon/caravan/internal/notify"
	"github.com/watzon/caravan/internal/relocate"
	"github.com/watzon/caravan/internal/stash"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/web"
)

// shutdownTimeout bounds how long in-flight requests get to finish before the
// process exits.
const shutdownTimeout = 10 * time.Second

func runServe(args []string) error {
	cfg, configPath, err := loadServeConfig(args)
	if err != nil {
		return err
	}

	configDir, err := filepath.Abs(filepath.Dir(configPath))
	if err != nil {
		return fmt.Errorf("resolve config dir: %w", err)
	}
	configFile, err := filepath.Abs(configPath)
	if err != nil {
		return fmt.Errorf("resolve config file: %w", err)
	}
	databasePath, err := filepath.Abs(cfg.DatabasePath())
	if err != nil {
		return fmt.Errorf("resolve database path: %w", err)
	}

	logger := newLogger(cfg.LogLevel)

	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("create data dir %s: %w", cfg.DataDir, err)
	}

	return serve(cfg, configDir, configFile, databasePath, logger)
}

func loadServeConfig(args []string) (*config.Config, string, error) {
	fs := newFlagSet("serve")
	configPath := fs.String("config", "", "path to the bootstrap YAML config (default: platform config directory)")
	dataDir := fs.String("data-dir", "", "application data directory override (database and runtime state)")
	listen := fs.String("listen", "", "listen address override (default from config)")
	if err := fs.Parse(args); err != nil {
		return nil, "", err
	}
	if *configPath == "" {
		defaultConfigPath, err := config.DefaultConfigPath()
		if err != nil {
			return nil, "", err
		}
		*configPath = defaultConfigPath
	}

	cfg, err := config.LoadWithDataDir(*configPath, *dataDir)
	if err != nil {
		return nil, "", err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}
	return cfg, *configPath, nil
}

func serve(cfg *config.Config, configDir, configFile, databasePath string, logger *slog.Logger) error {
	// The clean-shutdown marker (SPEC §2.3) is claimed before the database is
	// opened, because "did the last session end properly?" has to be answered
	// while the answer is still on disk, opening the database is already a
	// write. Its deferred release is registered first so it runs last: the
	// marker may only say "clean" once everything below has been torn down.
	marker := integrity.NewMarker(cfg.StatePath())
	dirty, err := marker.Begin()
	if errors.Is(err, integrity.ErrLocked) {
		// A second launcher double-click, most often. Serving anyway would open
		// the same database from two processes and let whichever exited first
		// declare the drive clean while the other was still writing.
		return fmt.Errorf("another Caravan is already using %s; stop it before starting a second one", cfg.DataDir)
	}
	if err != nil {
		// A marker that cannot be read or written is not a reason to refuse to
		// start; Begin already reported the conservative answer.
		logger.Warn("clean-shutdown marker unavailable, assuming an unclean shutdown",
			"path", marker.Path(), "error", err)
	}
	// Only a start that got as far as opening the database may declare a clean
	// shutdown. If the database is *why* this start failed (the likeliest way a
	// dirty eject shows itself) releasing the marker would erase the evidence
	// and the next start would offer no recovery at all.
	storeOpen := false
	// flushed is what the marker actually vouches for. The checkpoint and the
	// close below feed their verdict into it, because a drive that returned an
	// error on either is precisely the drive whose next start must offer
	// recovery, even though the marker's own tiny write may still succeed.
	flushed := true
	defer func() {
		if !storeOpen {
			return
		}
		if !flushed {
			logger.Error("leaving the shutdown marked unclean: the database did not flush",
				"marker", marker.Path())
			return
		}
		if err := marker.Finish(); err != nil {
			logger.Error("writing the clean-shutdown marker", "error", err)
		}
	}()

	st, err := store.Open(cfg.DatabasePath())
	if err != nil {
		return err
	}
	storeOpen = true
	defer func() {
		if err := st.Close(); err != nil {
			logger.Error("closing the database", "error", err)
			flushed = false
		}
	}()
	// Registered after the close above, so it runs immediately before it: the
	// write-ahead log is folded into the database file while the handle that
	// owns it is still open. Without this an ejected drive carries a database
	// whose last minutes live in a -wal file (SPEC §2.3).
	defer func() {
		if err := st.Checkpoint(); err != nil {
			logger.Error("checkpointing the write-ahead log", "error", err)
			flushed = false
		}
	}()

	schemaVersion, err := st.SchemaVersion()
	if err != nil {
		return err
	}
	logger.Info("database ready",
		"path", cfg.DatabasePath(),
		"schema_version", schemaVersion)
	if err := warnOnUnseededShelves(context.Background(), st, logger); err != nil {
		return err
	}

	seededRoot, err := seedSettings(context.Background(), st, cfg)
	if err != nil {
		return err
	}

	// Only portable mode acts on a dirty start: the recovery flow is about a
	// drive somebody pulled, and a server install that was killed by its
	// service manager has neither the drive nor the user to prompt. The marker
	// itself is kept identically in both modes, so the fact is always recorded
	// even where nothing is done with it.
	portableDirty := dirty && cfg.Portable
	if dirty {
		logger.Warn("previous session did not shut down cleanly",
			"marker", marker.Path(), "recovery_offered", portableDirty)
		if err := st.InsertEvent(context.Background(), &core.Event{
			Level:    core.EventLevelWarn,
			Category: api.EventCategorySystem,
			Message:  "Caravan did not shut down cleanly",
			Detail:   "Check the drive's filesystem, then verify and rescan the library before resuming downloads.",
		}); err != nil {
			logger.Error("recording the unclean shutdown", "error", err)
		}
	}

	slog.SetDefault(logger)
	api.Version = version

	// The signal context is the process's shutdown signal: the HTTP server and
	// the import watcher both stop on it, so a Ctrl-C drains requests and the
	// watcher together rather than killing one out from under the other.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The playback handoff (SPEC §5.2): imports hand it a "the library
	// changed" notification, it turns that into a durable job, and the job
	// tells Jellyfin to rescan. It is always constructed. An unconfigured or
	// disabled handoff is a no-op, not an absent dependency.
	handoff := jellyfin.NewService(st, nil, logger)

	// The adult library's handoff, the same shape for Stash: a scene import
	// records that a scoped scan and an identity push are owed, and the jobs
	// carry them out. Always constructed for the reason the Jellyfin one is (an
	// unconfigured handoff is a no-op) and doubly inert besides, because it
	// also refuses to run while the adult module is off.
	stashHandoff := stash.NewService(st, nil, logger)

	mgr := newLibraryAdapter(st, cfg.StorageRoot, logger, handoff, stashHandoff)
	engines := newEngineProvider(mgr, cfg.Portable, logger)
	// Closed before the store (deferred later, so it runs first): the engine
	// flushes the queue's state through the store on the way out.
	defer func() {
		if err := engines.Close(); err != nil {
			logger.Error("closing download engine", "error", err)
		}
	}()

	if err := refreshManagedDefinitionCache(ctx, cfg.DataDir, logger, &http.Client{Timeout: indexerTimeout}, managedDefinitionSourceURL); err != nil {
		logger.Warn("managed indexer definition refresh failed; using last verified cache", "error", err)
	}
	indexerRuntime, err := newIndexerRuntime(cfg.DataDir, logger, st, st)
	if err != nil {
		return err
	}
	definitionPacks := &packs.Service{Store: st, DataDir: cfg.DataDir, Version: api.Version}
	indexers := indexerRuntime.factory
	// Which external download clients this build can actually talk to. Nothing
	// is configured by default (SPEC §12); this only decides whether a client
	// the user adds can be tested, or is stored and answered with a 501.
	if err := registerDownloadClients(); err != nil {
		return err
	}
	if err := automation.Bootstrap(ctx, st); err != nil {
		return err
	}

	// The convert-for-TV queue is optional (SPEC §8): without ffmpeg on path
	// the service reports itself unavailable, the API refuses to queue work,
	// and the UI hides the affordance. Detection happens once at startup. A
	// binary appearing mid-run is a restart, not a poll.
	converter := convert.New(st, mgr.StorageRoot, convert.Detect(), logger)
	logger.Info("convert-for-tv queue", "ffmpeg", converter.Available())

	devUI, err := devUIOrigin(logger)
	if err != nil {
		return err
	}

	// The built-in DLNA media server (SPEC §5.1): it shares this process's
	// HTTP listener, so it needs that port to advertise a location clients can
	// fetch. A listen address without a usable port means no advertisement. The
	// API and the SPA still serve, and GET /dlna says why.
	dlnaServer := dlna.New(st, mgr.StorageRoot, listenPort(cfg.Listen, logger), logger)
	// Deferred before Start so a Start that fails halfway is still torn down,
	// and ordered so byebye goes out while the HTTP server can still answer the
	// device description a client re-fetches on the way past.
	defer func() {
		if err := dlnaServer.Close(); err != nil {
			logger.Error("closing dlna server", "error", err)
		}
	}()

	// Moving the storage root (SPEC §10). Re-pointing is a settings write the
	// API does itself; migrating moves the library, so it is a durable job. It
	// is handed the engine getter rather than an engine because it has to pause
	// the queue for the duration, and the engine may not exist yet.
	relocator := relocate.New(st, engines.Engine, logger)
	notifier := notify.New(st)

	// Conversions get a worker of their own: a two-hour transcode on the shared
	// worker would hold up the Jellyfin handoff, the RSS sync and every
	// monitored search for as long as it ran. A storage migration is the same
	// shape of work (hours of it) for the same reason.
	runner := automation.NewRunner(st, indexers, engines.await,
		automation.WithDedicatedWorker(convert.JobKind, converter.Handle),
		automation.WithDedicatedWorker(relocate.JobKind, relocator.Handle),
		automation.WithHandler(jellyfin.JobKind, handoff.Handle),
		automation.WithHandler(core.JobNotificationDispatch,
			func(ctx context.Context, _ *store.Store, _ json.RawMessage) error {
				return notifier.Dispatch(ctx)
			}),
		// The adult twin, in two kinds: one scoped scan per burst of scene
		// imports, and one identity push per scene. The push retries on the
		// queue's own backoff while Stash finishes indexing the file, which is
		// why it is a job rather than a step inside the scan handler.
		automation.WithHandler(stash.ScanJobKind, stashHandoff.HandleScan),
		automation.WithHandler(stash.IdentifyJobKind, stashHandoff.HandleIdentify),
		// The metadata refresh needs the library manager, which the automation
		// package deliberately does not know about, same registration story as
		// the Jellyfin handoff. A process with no TMDB key yet skips the sweep
		// cleanly rather than burning the recurring job's retries on ordinary
		// first-run state.
		automation.WithHandler(core.JobRefreshMetadata,
			func(ctx context.Context, _ *store.Store, _ json.RawMessage) error {
				res, err := mgr.RefreshLibrary(ctx)
				if err != nil {
					if errors.Is(err, core.ErrNoMetadataProvider) {
						return nil
					}
					return err
				}
				for _, warning := range res.Errors {
					logger.Warn("metadata refresh", "problem", warning)
				}
				logger.Info("metadata refresh complete",
					"movies", res.Movies, "series", res.Series, "problems", len(res.Errors))
				return nil
			}),
		automation.WithHandler(core.JobRecycleCleanup, mgr.HandleRecycleCleanup),
		// The deferred catalogue walk POST /adult/sites queues instead of
		// making its caller wait. Registered from here for the same reason the
		// refresh is: the walk is the library manager's, and the queue is not
		// allowed to know about it.
		automation.WithHandler(core.JobSyncSite, automation.SyncSiteHandler(mgr.SyncSite)),
		// One item's directory moving between libraries. Durable because a
		// series can be hundreds of files; registered from here because the
		// move is the library manager's, like the refresh and the site walk.
		automation.WithHandler(core.JobMoveItem,
			func(ctx context.Context, _ *store.Store, payload json.RawMessage) error {
				var input core.JobMoveItemPayload
				if err := json.Unmarshal(payload, &input); err != nil || input.ItemID <= 0 || input.LibraryID <= 0 {
					return fmt.Errorf("decode %s payload", core.JobMoveItem)
				}
				switch input.ItemType {
				case core.MediaTypeMovie:
					return mgr.MoveMovie(ctx, input.ItemID, input.LibraryID)
				case core.MediaTypeSeries:
					return mgr.MoveSeries(ctx, input.ItemID, input.LibraryID)
				}
				return fmt.Errorf("unknown move item type %q", input.ItemType)
			}))

	var watcher sync.WaitGroup
	watcher.Add(2)
	go func() {
		defer watcher.Done()
		runImportWatcher(ctx, engines, mgr, logger)
	}()
	go func() {
		defer watcher.Done()
		runner.Run(ctx)
	}()

	srv := &http.Server{
		Addr: cfg.Listen,
		Handler: api.NewServer(st, mgr, web.DistFS(),
			api.WithEngine(engines),
			api.WithIndexerClients(indexers),
			api.WithLocalDefinitions(indexerRuntime.definitions),
			api.WithExactLocalDefinitions(indexerRuntime.exactDefinitions),
			api.WithDefinitionInventoryStatuses(indexerRuntime.managedStatuses),
			api.WithDefinitionPacks(definitionPacks),
			api.WithConverter(converter),
			api.WithDLNA(dlnaServer),
			// So GET /system/status can raise the unreachable-Stash banner for
			// a caller the adult module is visible to.
			api.WithStash(stashHandoff),
			// So GET /system/status can tell the UI whether this process is
			// reachable from other machines, which is half of the
			// "no password on a public bind" nag (SPEC §11).
			api.WithListenAddr(cfg.Listen),
			// The portable integrity flow (SPEC §2.3, §13). The stop trigger is
			// literally the signal context's cancel, so POST /system/shutdown
			// and a Ctrl-C run the same teardown and both end in a clean marker.
			api.WithDirtyStart(portableDirty),
			api.WithRuntimeDiagnostics(api.RuntimeConfig{
				ConfigDir:    configDir,
				ConfigFile:   configFile,
				DatabasePath: databasePath,
				LogLevel:     cfg.LogLevel,
			}),
			api.WithShutdown(stop),
			// SPEC §10.1's second first-run step, for the two modes that never
			// see the first-run screen because they brought a storage root with
			// them. Only on the start that actually wrote it.
			api.WithStartupScan(seededRoot),
			// `just dev` sets CARAVAN_DEV_UI so the SPA is Vite (HMR) instead
			// of the last embedded snapshot. Empty in every other start.
			api.WithDevUI(devUI)),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Started after the handler exists so nothing is advertised before there is
	// something to answer with.
	dlnaServer.Start(ctx)

	err = serveUntilSignal(ctx, srv, logger, cfg)

	// Explicit rather than left to the deferred stop: when the server failed to
	// listen at all, nothing has cancelled ctx and the watcher would never
	// return.
	stop()
	watcher.Wait()
	return err
}

// devUIOrigin reads CARAVAN_DEV_UI. Release binaries ignore it so a leftover
// env var cannot turn a shipped listener into a reverse proxy. The empty
// string is the production path: serve the embedded SPA.
func devUIOrigin(logger *slog.Logger) (string, error) {
	raw := os.Getenv(api.EnvDevUI)
	origin, err := api.ParseDevUIOrigin(raw)
	if err != nil {
		return "", fmt.Errorf("%s: %w", api.EnvDevUI, err)
	}
	if origin == "" {
		return "", nil
	}
	if api.Version != "dev" {
		logger.Warn("ignoring "+api.EnvDevUI+" in a non-dev build", "origin", origin)
		return "", nil
	}
	logger.Info("proxying the SPA to the Vite dev server", "origin", origin)
	return origin, nil
}

// listenPort extracts the port from a listen address, for the DLNA server to
// advertise. A zero return disables advertising rather than failing the start:
// the address is already good enough for net/http, and the LAN discovery half
// of the feature is not worth refusing to boot over.
func listenPort(addr string, logger *slog.Logger) int {
	_, raw, err := net.SplitHostPort(addr)
	if err != nil {
		logger.Warn("dlna: cannot read the listen port, not advertising", "listen", addr, "error", err)
		return 0
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port <= 0 {
		logger.Warn("dlna: listen port is not advertisable", "listen", addr)
		return 0
	}
	return port
}

// warnOnUnseededShelves reports a library kind that ended up with no library at
// all, which after migration 0011 has exactly one cause worth naming.
//
// 0011 seeds a dormant shelf for every kind, and skips a seed only when both
// the preferred root path and its suffixed fallback are already taken by some
// other library. That skip is deliberate (a refused upgrade would be a worse
// answer than a missing optional shelf) but it is otherwise silent, and a
// silent skip leaves an owner looking for an Anime shelf that the release notes
// promised. Nothing else produces the state: the seeded rows are their kind's
// default, and the delete guard refuses to remove a default.
//
// It warns rather than repairing. Choosing a root path is the one decision here
// that belongs to the person who owns the disk.
func warnOnUnseededShelves(ctx context.Context, st *store.Store, logger *slog.Logger) error {
	libs, err := st.ListLibraries(ctx)
	if err != nil {
		return err
	}
	seen := make(map[string]bool, len(libs))
	for _, l := range libs {
		seen[l.Kind] = true
	}
	for _, kind := range []string{
		core.LibraryKindMovie, core.LibraryKindTV,
		core.LibraryKindAnime, core.LibraryKindAdult,
	} {
		if seen[kind] {
			continue
		}
		logger.Warn("no library of this kind exists; its default shelf could not be seeded "+
			"because the root path it wanted was already taken. Create one in Settings → Libraries.",
			"kind", kind)
	}
	return nil
}

// seedSettings reconciles the settings table with the bootstrap config
// (SPEC §10 - the file is bootstrap, the table is runtime config).
//
// The deployment mode is a fact about how this process was launched, so it is
// written every start. The storage root is only seeded when the table has none:
// after first run, or after a re-point from the settings screen, the table's
// value is the authoritative one and the file must not override it.
//
// It reports whether it just seeded the root, which is the closest thing Docker
// and the portable drive have to a first run - see api.WithStartupScan.
func seedSettings(ctx context.Context, st *store.Store, cfg *config.Config) (bool, error) {
	mode := api.ModeServer
	if cfg.Portable {
		mode = api.ModePortable
	}
	if err := st.SetSetting(ctx, api.SettingMode, mode); err != nil {
		return false, err
	}

	if cfg.StorageRoot == "" {
		return false, nil
	}
	switch _, err := st.GetSetting(ctx, store.SettingStorageRoot); {
	case err == nil:
		return false, nil
	case !errors.Is(err, store.ErrNotFound):
		return false, err
	}
	if err := st.SetSetting(ctx, store.SettingStorageRoot, cfg.StorageRoot); err != nil {
		return false, err
	}
	return true, nil
}

// serveUntilSignal runs srv until ctx is done - the signal context from
// runServe - then drains it.
func serveUntilSignal(ctx context.Context, srv *http.Server, logger *slog.Logger, cfg *config.Config) error {
	errCh := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", cfg.Listen, "portable", cfg.Portable)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("listen on %s: %w", cfg.Listen, err)
		}
		return nil
	case <-ctx.Done():
		logger.Info("shutting down")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shutdown: %w", err)
	}
	return nil
}

// newLogger builds the structured logger. The level is already validated by
// config.Load, so an unrecognized value here can only mean a config bug and
// falls back to info rather than failing the start.
func newLogger(level string) *slog.Logger {
	var l slog.Level
	switch level {
	case "debug":
		l = slog.LevelDebug
	case "warn":
		l = slog.LevelWarn
	case "error":
		l = slog.LevelError
	default:
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}
