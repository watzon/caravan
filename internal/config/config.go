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

// Environment overrides. The Docker image sets all three (SPEC §2.1) so a
// container needs no config file at all; they exist for any deployment that
// injects configuration rather than writing a file.
//
// Precedence, highest first: command-line flag, environment, config file,
// built-in default. Environment beating the file is what lets one image ship
// the /config + /data conventions while still honouring a file an operator
// bind-mounts in — the flag stays above both so `-listen` always wins.
const (
	// EnvConfigDir overrides ConfigDir.
	EnvConfigDir = "CARAVAN_CONFIG_DIR"
	// EnvListen overrides Listen. Setting it also counts as choosing an
	// address, so portable mode does not narrow it to loopback.
	EnvListen = "CARAVAN_LISTEN"
	// EnvStorageRoot overrides StorageRoot. Like the file's storage_root it
	// only seeds the settings table on first run; a root re-pointed from the
	// UI afterwards is the authoritative one.
	EnvStorageRoot = "CARAVAN_STORAGE_ROOT"
)

// DefaultListen is Caravan's default listen address (SPEC §10).
const DefaultListen = ":8677"

// DefaultPortableListen is what portable mode binds when the config file names
// no address (SPEC §11). A portable install follows its owner onto whatever
// network the laptop is on — a coffee shop, a hotel — so it must not be
// reachable from that network by default. Server mode keeps DefaultListen: it
// is a box other machines are meant to reach.
//
// An address in the config file (or -listen) always wins, in both modes.
const DefaultPortableListen = "127.0.0.1:8677"

// DatabaseFile is the sqlite database's name inside the config directory.
const DatabaseFile = "caravan.db"

// StateFile is the clean-shutdown marker's name inside the config directory
// (SPEC §2.3). It sits beside the database rather than inside it: the database
// is a disposable cache (SPEC §7), and a flag deleted with it could never
// report a dirty eject.
const StateFile = "caravan.state"

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
// CARAVAN_CONFIG_DIR, CARAVAN_LISTEN and CARAVAN_STORAGE_ROOT environment
// variables override the file and the defaults alike.
func Load(path string) (*Config, error) {
	cfg := Default()
	cfg.ConfigDir = filepath.Dir(path)

	// Whether something chose an address, as opposed to inheriting the default.
	// Portable mode narrows the default to loopback, and only the default.
	listenChosen := false

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
		listenChosen = file.Listen != ""
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
	if addr := os.Getenv(EnvListen); addr != "" {
		cfg.Listen = addr
		listenChosen = true
	}
	if root := os.Getenv(EnvStorageRoot); root != "" {
		cfg.StorageRoot = root
	}

	if cfg.Portable && !listenChosen {
		cfg.Listen = DefaultPortableListen
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

// StatePath returns the clean-shutdown marker's path inside ConfigDir.
func (c *Config) StatePath() string {
	return filepath.Join(c.ConfigDir, StateFile)
}
