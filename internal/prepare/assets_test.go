package prepare

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/config"
)

// The launchers open a hardcoded URL, because a shell script cannot read the
// Go default. This is the pin that keeps the two from drifting apart.
func TestLauncherURLMatchesThePortableListenDefault(t *testing.T) {
	want := "http://" + config.DefaultPortableListen
	if portableURL != want {
		t.Fatalf("portableURL = %q, want %q (config.DefaultPortableListen)", portableURL, want)
	}
	for name, script := range map[string]string{
		MacLauncher:     macLauncher,
		LinuxLauncher:   linuxLauncher,
		WindowsLauncher: windowsLauncherLF,
	} {
		if !strings.Contains(script, portableURL) {
			t.Fatalf("%s does not open %s", name, portableURL)
		}
	}
}

// Every launcher has the same three jobs: find its own folder, pick the binary
// for this machine, and start Caravan against the drive-relative config. A
// launcher that skips the first one starts Caravan against the user's home
// directory and builds a second, empty library there.
func TestLaunchersResolveTheirOwnDirectory(t *testing.T) {
	for _, tc := range []struct {
		name           string
		script         string
		resolvesOwnDir []string
		config         string
	}{
		{
			name:           MacLauncher,
			script:         macLauncher,
			resolvesOwnDir: []string{`cd "$(dirname "$0")"`},
			config:         "serve -config caravan/caravan.yaml",
		},
		{
			name:           LinuxLauncher,
			script:         linuxLauncher,
			resolvesOwnDir: []string{`readlink -f "$self"`, `cd "$(dirname "$self")"`},
			config:         "serve -config caravan/caravan.yaml",
		},
		{
			name:           WindowsLauncher,
			script:         windowsLauncherLF,
			resolvesOwnDir: []string{`cd /d "%~dp0"`},
			config:         `serve -config caravan\caravan.yaml`,
		},
	} {
		for _, want := range tc.resolvesOwnDir {
			if !strings.Contains(tc.script, want) {
				t.Fatalf("%s does not resolve its own directory (%q missing)", tc.name, want)
			}
		}
		if !strings.Contains(tc.script, tc.config) {
			t.Fatalf("%s does not start caravan with the drive-relative config", tc.name)
		}
		if strings.Contains(tc.script, "`") {
			t.Fatalf("%s uses a backtick, which cannot survive a Go raw string literal", tc.name)
		}
	}
}

// The two POSIX launchers detect the CPU, because a drive carried between an
// Intel laptop and an Apple Silicon one has to pick a different binary on each.
func TestPosixLaunchersDetectArch(t *testing.T) {
	for name, script := range map[string]string{MacLauncher: macLauncher, LinuxLauncher: linuxLauncher} {
		if !strings.Contains(script, "uname -m") {
			t.Fatalf("%s does not detect the CPU", name)
		}
		for _, arch := range []string{"arm64", "amd64"} {
			if !strings.Contains(script, arch) {
				t.Fatalf("%s does not handle %s", name, arch)
			}
		}
		if !strings.HasPrefix(script, "#!/bin/sh\n") {
			t.Fatalf("%s has no shebang", name)
		}
	}
}

// cmd.exe is the one shell here that still wants CRLF.
func TestWindowsLauncherUsesCRLF(t *testing.T) {
	if strings.Contains(windowsLauncher, "\n") && !strings.Contains(windowsLauncher, "\r\n") {
		t.Fatal("the .bat launcher has bare LF line endings")
	}
	for _, line := range strings.Split(windowsLauncher, "\r\n") {
		if strings.Contains(line, "\n") {
			t.Fatalf("line %q is not CRLF-terminated", line)
		}
	}
	// The POSIX launchers must not be: a CR in a shebang line makes the kernel
	// look for an interpreter that does not exist.
	for name, script := range map[string]string{MacLauncher: macLauncher, LinuxLauncher: linuxLauncher} {
		if strings.Contains(script, "\r") {
			t.Fatalf("%s contains a carriage return", name)
		}
	}
}

// SPEC §12: unsigned binaries trip SmartScreen, and the README is where the
// user is told what to do about it.
func TestReadmeDocumentsTheFirstRunAndEjectFlows(t *testing.T) {
	for _, want := range []string{
		"SmartScreen",
		"More info",
		"Run anyway",
		"Shut down safely",
		"exFAT",
		"GPT",
		MacLauncher,
		WindowsLauncher,
		LinuxLauncher,
	} {
		if !strings.Contains(readme, want) {
			t.Fatalf("README.txt does not mention %q", want)
		}
	}
}

// The config the drive gets has to be one config.Load actually accepts, and it
// has to come back saying portable with the paths prepare wrote.
func TestConfigYAMLLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "caravan.yaml")
	if err := os.WriteFile(path, []byte(configYAML), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Setenv(config.EnvConfigDir, "")
	t.Setenv(config.EnvDataDir, "")

	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if !cfg.Portable {
		t.Fatal("the prepared config is not portable")
	}
	if cfg.StorageRoot != "." {
		t.Fatalf("StorageRoot = %q, want \".\"", cfg.StorageRoot)
	}
	if cfg.DataDir != "caravan/data" {
		t.Fatalf("DataDir = %q, want \"caravan/data\"", cfg.DataDir)
	}
	// The config names no address, so portable mode's loopback default is what
	// the launchers' URL has to match.
	if cfg.Listen != config.DefaultPortableListen {
		t.Fatalf("Listen = %q, want the portable default %q", cfg.Listen, config.DefaultPortableListen)
	}
}
