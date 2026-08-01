package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/config"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/web"
)

// shutdownTimeout bounds how long in-flight requests get to finish before the
// process exits.
const shutdownTimeout = 10 * time.Second

func runServe(args []string) error {
	fs := newFlagSet("serve")
	configPath := fs.String("config", "caravan.yaml", "path to the bootstrap YAML config")
	listen := fs.String("listen", "", "listen address override (default from config)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}
	if *listen != "" {
		cfg.Listen = *listen
	}

	logger := newLogger(cfg.LogLevel)

	if err := os.MkdirAll(cfg.ConfigDir, 0o755); err != nil {
		return fmt.Errorf("create config dir %s: %w", cfg.ConfigDir, err)
	}

	st, err := store.Open(cfg.DatabasePath())
	if err != nil {
		return err
	}
	defer st.Close()

	schemaVersion, err := st.SchemaVersion()
	if err != nil {
		return err
	}
	logger.Info("database ready",
		"path", cfg.DatabasePath(),
		"schema_version", schemaVersion)

	if err := seedSettings(context.Background(), st, cfg); err != nil {
		return err
	}

	slog.SetDefault(logger)
	api.Version = version

	mgr := newLibraryAdapter(st, cfg.StorageRoot, logger)
	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           api.NewServer(st, mgr, web.DistFS()),
		ReadHeaderTimeout: 10 * time.Second,
	}

	return serveUntilSignal(srv, logger, cfg)
}

// seedSettings reconciles the settings table with the bootstrap config
// (SPEC §10 — the file is bootstrap, the table is runtime config).
//
// The deployment mode is a fact about how this process was launched, so it is
// written every start. The storage root is only seeded when the table has none:
// after first run, or after a re-point from the settings screen, the table's
// value is the authoritative one and the file must not override it.
func seedSettings(ctx context.Context, st *store.Store, cfg *config.Config) error {
	mode := api.ModeServer
	if cfg.Portable {
		mode = api.ModePortable
	}
	if err := st.SetSetting(ctx, api.SettingMode, mode); err != nil {
		return err
	}

	if cfg.StorageRoot == "" {
		return nil
	}
	switch _, err := st.GetSetting(ctx, store.SettingStorageRoot); {
	case err == nil:
		return nil
	case !errors.Is(err, store.ErrNotFound):
		return err
	}
	return st.SetSetting(ctx, store.SettingStorageRoot, cfg.StorageRoot)
}

// serveUntilSignal runs srv until SIGINT or SIGTERM, then drains it.
func serveUntilSignal(srv *http.Server, logger *slog.Logger, cfg *config.Config) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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
