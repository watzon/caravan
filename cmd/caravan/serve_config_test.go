package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/config"
)

func TestLoadServeConfigUsesPlatformDefaults(t *testing.T) {
	root := t.TempDir()
	configHome := filepath.Join(root, "config-home")
	dataHome := filepath.Join(root, "data-home")
	t.Setenv("XDG_CONFIG_HOME", configHome)
	t.Setenv("XDG_DATA_HOME", dataHome)

	cfg, configPath, err := loadServeConfig(nil)
	if err != nil {
		t.Fatalf("loadServeConfig: %v", err)
	}
	if want := filepath.Join(configHome, "caravan", "caravan.yaml"); configPath != want {
		t.Errorf("config path = %q, want %q", configPath, want)
	}
	if want := filepath.Join(dataHome, "caravan"); cfg.DataDir != want {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, want)
	}
}

func TestLoadServeConfigDataDirFlagWins(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "caravan.yaml")
	fileDataDir := filepath.Join(root, "from-file")
	flagDataDir := filepath.Join(root, "from-flag")
	if err := os.WriteFile(configPath, []byte("data_dir: "+fileDataDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, gotConfigPath, err := loadServeConfig([]string{
		"--config", configPath,
		"--data-dir", flagDataDir,
	})
	if err != nil {
		t.Fatalf("loadServeConfig: %v", err)
	}
	if gotConfigPath != configPath {
		t.Errorf("config path = %q, want %q", gotConfigPath, configPath)
	}
	if cfg.DataDir != flagDataDir {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, flagDataDir)
	}
}

func TestLoadServeConfigExplicitLocationsDoNotNeedHome(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv(config.EnvDataDir, "")
	t.Setenv(config.EnvConfigDir, "")

	root := t.TempDir()
	fileDataDir := filepath.Join(root, "from-file")
	configPath := filepath.Join(root, "caravan.yaml")
	if err := os.WriteFile(configPath, []byte("data_dir: "+fileDataDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("config file supplies data directory", func(t *testing.T) {
		cfg, gotConfigPath, err := loadServeConfig([]string{"--config", configPath})
		if err != nil {
			t.Fatalf("loadServeConfig: %v", err)
		}
		if gotConfigPath != configPath {
			t.Errorf("config path = %q, want %q", gotConfigPath, configPath)
		}
		if cfg.DataDir != fileDataDir {
			t.Errorf("DataDir = %q, want %q", cfg.DataDir, fileDataDir)
		}
	})

	t.Run("command line supplies data directory", func(t *testing.T) {
		flagDataDir := filepath.Join(root, "from-flag")
		missingConfig := filepath.Join(root, "missing.yaml")
		cfg, _, err := loadServeConfig([]string{
			"--config", missingConfig,
			"--data-dir", flagDataDir,
		})
		if err != nil {
			t.Fatalf("loadServeConfig: %v", err)
		}
		if cfg.DataDir != flagDataDir {
			t.Errorf("DataDir = %q, want %q", cfg.DataDir, flagDataDir)
		}
	})
}
