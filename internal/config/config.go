// Package config loads Caravan's bootstrap configuration (SPEC §10).
//
// Only the handful of settings needed to start the process live here: where
// state is kept, what address to listen on, the storage root, the portable
// flag, and the log level. Everything else is runtime configuration owned by
// the `settings` table and managed from the web UI.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// EnvConfigDir overrides the config directory. The Docker image sets it
// (SPEC §2.1).
const EnvConfigDir = "CARAVAN_CONFIG_DIR"

// DefaultListen is Caravan's default listen address (SPEC §10).
const DefaultListen = ":8677"

// DatabaseFile is the sqlite database's name inside the config directory.
const DatabaseFile = "caravan.db"

// Config is the bootstrap configuration.
type Config struct {
	// ConfigDir is where Caravan keeps its own state: the sqlite database,
	// logs, and caches. Defaults to the directory of the config file path that
	// was asked for, whether or not that file exists.
	ConfigDir string `yaml:"config_dir"`

	// Listen is the HTTP listen address, host:port. Portable mode should bind
	// loopback only (SPEC §11).
	Listen string `yaml:"listen"`

	// StorageRoot is the root every path in the database is relative to. Empty
	// means "not chosen yet": the first-run flow (SPEC §10.1) asks for it and
	// stores it in the settings table, and this field is only an override for
	// deployments that pin it in the file.
	StorageRoot string `yaml:"storage_root"`

	// Portable enables portable-disk behavior: dirty-eject detection, safe
	// shutdown, seeding paused by default (SPEC §2.3).
	Portable bool `yaml:"portable"`

	// LogLevel is one of debug, info, warn, error.
	LogLevel string `yaml:"log_level"`
}

// Valid log levels, in increasing severity.
var logLevels = []string{"debug", "info", "warn", "error"}

// Default returns the baseline configuration every load starts from. Its
// ConfigDir is the working directory; Load replaces that with the directory of
// the config path it was given.
func Default() Config {
	return Config{
		ConfigDir: ".",
		Listen:    DefaultListen,
		LogLevel:  "info",
	}
}

// Load reads the YAML config at path.
//
// A missing file is not an error: Caravan is expected to run with zero
// configuration (SPEC §10.1 — "everything else ships with defaults"), so the
// defaults are returned instead. Any other read, parse, or validation failure
// is reported.
//
// Values absent from the file fall back to Default. ConfigDir additionally
// falls back to the directory containing path — the path that was *asked* for,
// so `caravan serve --config /srv/caravan/caravan.yaml` keeps its database next
// to its config whether or not that config file exists yet. The
// CARAVAN_CONFIG_DIR environment variable overrides both.
func Load(path string) (*Config, error) {
	cfg := Default()
	cfg.ConfigDir = filepath.Dir(path)

	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		// A config file that exists but is unreadable YAML is a real error:
		// silently falling back to defaults would point Caravan at the wrong
		// storage root.
		var file Config
		if err := yaml.Unmarshal(data, &file); err != nil {
			return nil, fmt.Errorf("config: parse %s: %w", path, err)
		}
		cfg = merge(cfg, file)
	case errors.Is(err, os.ErrNotExist):
		// Zero-config start: keep the defaults, still anchored at the
		// requested path's directory. Anchoring at the working directory
		// instead would scatter a first run's database wherever it happened
		// to be launched from.
	default:
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}

	if dir := os.Getenv(EnvConfigDir); dir != "" {
		cfg.ConfigDir = dir
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// merge overlays the non-zero fields of file onto base.
func merge(base, file Config) Config {
	if file.ConfigDir != "" {
		base.ConfigDir = file.ConfigDir
	}
	if file.Listen != "" {
		base.Listen = file.Listen
	}
	if file.StorageRoot != "" {
		base.StorageRoot = file.StorageRoot
	}
	if file.LogLevel != "" {
		base.LogLevel = file.LogLevel
	}
	// Portable has no "unset" state in YAML; false is both the zero value and
	// a legitimate choice, and false is also the default, so taking the file's
	// value unconditionally is correct.
	base.Portable = file.Portable
	return base
}

func (c *Config) validate() error {
	if c.ConfigDir == "" {
		return errors.New("config: config_dir must not be empty")
	}
	if c.Listen == "" {
		return errors.New("config: listen must not be empty")
	}
	for _, level := range logLevels {
		if c.LogLevel == level {
			return nil
		}
	}
	return fmt.Errorf("config: log_level %q is not one of %v", c.LogLevel, logLevels)
}

// DatabasePath returns the sqlite database's path inside ConfigDir. It is
// relative exactly when ConfigDir is.
func (c *Config) DatabasePath() string {
	return filepath.Join(c.ConfigDir, DatabaseFile)
}
