package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileUsesDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "absent.yaml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q) on a missing file: %v", path, err)
	}
	want := Default()
	// ConfigDir is anchored at the requested path, not at Default's ".".
	want.ConfigDir = dir
	if *cfg != want {
		t.Errorf("Load = %+v, want %+v", *cfg, want)
	}
	if cfg.Listen != DefaultListen {
		t.Errorf("Listen = %q, want %q", cfg.Listen, DefaultListen)
	}
}

// A zero-config first run must still keep its database next to the config file
// it was pointed at. Anchoring ConfigDir at the working directory instead
// scatters caravan.db wherever the process happened to be launched from, and
// silently uses a different database when the launch directory changes.
func TestLoadMissingFileAnchorsConfigDirAtRequestedPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "caravan.yaml")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load(%q): %v", path, err)
	}
	if cfg.ConfigDir != dir {
		t.Errorf("ConfigDir = %q, want %q", cfg.ConfigDir, dir)
	}
	if want := filepath.Join(dir, DatabaseFile); cfg.DatabasePath() != want {
		t.Errorf("DatabasePath = %q, want %q", cfg.DatabasePath(), want)
	}
}

func TestLoad(t *testing.T) {
	tests := []struct {
		name string
		yaml string
		want Config
	}{
		{
			name: "empty file keeps defaults except config_dir",
			yaml: "",
			// config_dir defaults to the file's own directory.
			want: Config{ConfigDir: "", Listen: DefaultListen, LogLevel: "info"},
		},
		{
			name: "full file",
			yaml: "config_dir: /config\nlisten: 127.0.0.1:9000\nstorage_root: /data\nportable: true\nlog_level: debug\n",
			want: Config{ConfigDir: "/config", Listen: "127.0.0.1:9000", StorageRoot: "/data", Portable: true, LogLevel: "debug"},
		},
		{
			name: "partial file falls back per field",
			yaml: "storage_root: /mnt/media\n",
			want: Config{ConfigDir: "", Listen: DefaultListen, StorageRoot: "/mnt/media", LogLevel: "info"},
		},
		{
			name: "portable false is respected",
			yaml: "portable: false\nconfig_dir: /c\n",
			want: Config{ConfigDir: "/c", Listen: DefaultListen, LogLevel: "info"},
		},
		{
			name: "explicitly empty values fall back to defaults",
			yaml: "listen: \"\"\nlog_level: \"\"\n",
			want: Config{ConfigDir: "", Listen: DefaultListen, LogLevel: "info"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "caravan.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}
			want := tt.want
			if want.ConfigDir == "" {
				want.ConfigDir = dir
			}

			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if *cfg != want {
				t.Errorf("Load = %+v, want %+v", *cfg, want)
			}
		})
	}
}

// SPEC §11: a portable install must not be reachable from whatever network its
// laptop is plugged into, but an address the operator wrote down always wins.
func TestLoadPortableBindsLoopbackUnlessTold(t *testing.T) {
	tests := []struct {
		name    string
		yaml    string
		written bool
		want    string
	}{
		{name: "portable with no listen", yaml: "portable: true\n", written: true, want: DefaultPortableListen},
		{name: "portable with an explicit listen", yaml: "portable: true\nlisten: 0.0.0.0:9000\n", written: true, want: "0.0.0.0:9000"},
		{name: "server mode keeps the wildcard default", yaml: "portable: false\n", written: true, want: DefaultListen},
		{name: "no config file at all", written: false, want: DefaultListen},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "caravan.yaml")
			if tt.written {
				if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			cfg, err := Load(path)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if cfg.Listen != tt.want {
				t.Errorf("Listen = %q, want %q", cfg.Listen, tt.want)
			}
		})
	}
}

func TestLoadEnvOverridesConfigDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "caravan.yaml")
	if err := os.WriteFile(path, []byte("config_dir: /from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvConfigDir, "/from-env")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ConfigDir != "/from-env" {
		t.Errorf("ConfigDir = %q, want %q", cfg.ConfigDir, "/from-env")
	}
}

// The Docker image configures itself entirely through the environment
// (SPEC §2.1): no config file is bind-mounted on a fresh install, so the
// container's /config + /data + bind-all conventions have to survive a
// completely absent caravan.yaml.
func TestLoadEnvOverridesWithoutAConfigFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caravan.yaml")
	t.Setenv(EnvConfigDir, "/config")
	t.Setenv(EnvListen, "0.0.0.0:8677")
	t.Setenv(EnvStorageRoot, "/data")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{ConfigDir: "/config", Listen: "0.0.0.0:8677", StorageRoot: "/data", LogLevel: "info"}
	if *cfg != want {
		t.Errorf("Load = %+v, want %+v", *cfg, want)
	}
}

// Environment beats the file: one image ships the conventions, and an operator
// who bind-mounts a caravan.yaml written for a bare-binary install does not
// silently drag that install's paths into the container.
func TestLoadEnvOverridesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caravan.yaml")
	yaml := "config_dir: /from-file\nlisten: 127.0.0.1:9999\nstorage_root: /from-file-root\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvConfigDir, "/config")
	t.Setenv(EnvListen, "0.0.0.0:8677")
	t.Setenv(EnvStorageRoot, "/data")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{ConfigDir: "/config", Listen: "0.0.0.0:8677", StorageRoot: "/data", LogLevel: "info"}
	if *cfg != want {
		t.Errorf("Load = %+v, want %+v", *cfg, want)
	}
}

// An empty environment variable is "unset", not "use the empty string": an
// exported-but-blank CARAVAN_STORAGE_ROOT in a compose .env must not erase the
// file's value or trip validation.
func TestLoadEmptyEnvIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caravan.yaml")
	yaml := "config_dir: /from-file\nlisten: 127.0.0.1:9999\nstorage_root: /from-file-root\n"
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvConfigDir, "")
	t.Setenv(EnvListen, "")
	t.Setenv(EnvStorageRoot, "")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	want := Config{ConfigDir: "/from-file", Listen: "127.0.0.1:9999", StorageRoot: "/from-file-root", LogLevel: "info"}
	if *cfg != want {
		t.Errorf("Load = %+v, want %+v", *cfg, want)
	}
}

// CARAVAN_LISTEN is a chosen address, exactly like one written in the file, so
// portable mode must not narrow it back to loopback. A portable drive plugged
// into a machine whose launcher exports CARAVAN_LISTEN is asking to be
// reachable; silently rebinding to 127.0.0.1 would look like a broken install.
func TestLoadPortableKeepsEnvListen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caravan.yaml")
	if err := os.WriteFile(path, []byte("portable: true\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvListen, "0.0.0.0:8677")

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen != "0.0.0.0:8677" {
		t.Errorf("Listen = %q, want %q", cfg.Listen, "0.0.0.0:8677")
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name string
		yaml string
	}{
		{name: "malformed yaml", yaml: "listen: [unclosed\n"},
		{name: "wrong type", yaml: "portable: not-a-bool\n"},
		{name: "unknown log level", yaml: "log_level: verbose\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "caravan.yaml")
			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}

			if _, err := Load(path); err == nil {
				t.Errorf("Load(%q) = nil error, want error", tt.yaml)
			}
		})
	}
}

func TestLoadUnreadableFile(t *testing.T) {
	// A directory where a config file is expected: readable-but-not-a-file
	// must surface as an error rather than silently degrading to defaults.
	dir := filepath.Join(t.TempDir(), "caravan.yaml")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(dir); err == nil {
		t.Error("Load(directory) = nil error, want error")
	}
}

func TestDatabasePath(t *testing.T) {
	cfg := Config{ConfigDir: "/config"}
	if got, want := cfg.DatabasePath(), filepath.Join("/config", DatabaseFile); got != want {
		t.Errorf("DatabasePath = %q, want %q", got, want)
	}
}
