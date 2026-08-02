package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/config"
	"github.com/watzon/caravan/internal/integrity"
	"github.com/watzon/caravan/internal/prepare"
)

// storageRoot reports what the running server thinks its storage root is. It is
// the one setting a portable drive must never see turned into an absolute path.
func (p *portableServer) storageRoot(t *testing.T) string {
	t.Helper()
	resp, err := http.Get(p.baseURL + "/system/status")
	if err != nil {
		t.Fatalf("GET /system/status: %v", err)
	}
	defer resp.Body.Close()
	var status struct {
		Mode        string `json:"mode"`
		StorageRoot string `json:"storage_root"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&status); err != nil {
		t.Fatalf("decode status: %v", err)
	}
	if status.Mode != "portable" {
		t.Fatalf("mode = %q, want portable", status.Mode)
	}
	return status.StorageRoot
}

// The subcommand is reachable from the top-level dispatcher and scaffolds a
// real drive, including this machine's binary — which for a test is the test
// binary, exactly the way it is the caravan binary in production.
func TestPrepareSubcommandScaffoldsADrive(t *testing.T) {
	root := t.TempDir()
	if err := run([]string{"prepare", root}); err != nil {
		t.Fatalf("run prepare: %v", err)
	}

	for _, rel := range []string{
		prepare.ConfigFile,
		prepare.ReadmeFile,
		prepare.MacLauncher,
		prepare.WindowsLauncher,
		prepare.LinuxLauncher,
		"library/Movies",
		"library/TV",
		"incomplete",
		"caravan/data",
	} {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("missing %s: %v", rel, err)
		}
	}

	host := prepare.Target{GOOS: runtime.GOOS, GOARCH: runtime.GOARCH}
	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(host.RelPath())))
	if err != nil {
		t.Fatalf("this machine's binary was not placed at %s: %v", host.RelPath(), err)
	}
	if info.Size() == 0 {
		t.Fatalf("%s is empty", host.RelPath())
	}
}

// One argument, exactly: a prepare with no target has nothing to prepare, and a
// prepare with two would silently ignore one of the drives it was given.
func TestPrepareRequiresExactlyOneTarget(t *testing.T) {
	for _, args := range [][]string{
		{"prepare"},
		{"prepare", t.TempDir(), t.TempDir()},
	} {
		if err := run(args); err == nil {
			t.Fatalf("run(%v) succeeded, want an error", args)
		}
	}
}

// -bin-dir reaches the scaffolder, so an offline release bundle can supply the
// builds for the other operating systems.
func TestPrepareBinDirFlag(t *testing.T) {
	bundle := t.TempDir()
	other := prepare.Target{GOOS: "windows", GOARCH: "amd64"}
	if runtime.GOOS == "windows" {
		other = prepare.Target{GOOS: "linux", GOARCH: "amd64"}
	}
	name := "caravan-" + other.Slug()
	if other.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(bundle, name), []byte("release build"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	root := t.TempDir()
	if err := run([]string{"prepare", "-bin-dir", bundle, root}); err != nil {
		t.Fatalf("run prepare: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(other.RelPath())))
	if err != nil {
		t.Fatalf("read %s: %v", other.RelPath(), err)
	}
	if string(got) != "release build" {
		t.Fatalf("%s = %q, want the build from -bin-dir", other.RelPath(), got)
	}
}

// The point of the whole layout: a prepared drive boots from its own launcher's
// working directory, and boots again unchanged after the drive is mounted
// somewhere else — a different letter on Windows, a different /Volumes path on
// a Mac (SPEC §2.3, PLAN phase 5 task 2).
//
// This is the automated half of that acceptance criterion. It runs the real
// runServe against the real caravan.yaml prepare wrote, exactly as the
// launchers do: chdir to the drive root, then "serve -config
// caravan/caravan.yaml". The listen address is overridden to a free port
// because the test must not take the portable default.
func TestPreparedDriveBootsAtEveryMountPoint(t *testing.T) {
	base := t.TempDir()
	first := filepath.Join(base, "CARAVAN")
	if err := os.Mkdir(first, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := run([]string{"prepare", first}); err != nil {
		t.Fatalf("run prepare: %v", err)
	}
	before := readFileForTest(t, filepath.Join(first, filepath.FromSlash(prepare.ConfigFile)))

	t.Run("first mount point", func(t *testing.T) { bootPreparedDrive(t, first) })

	// The same bytes, somewhere else entirely. Nothing on the drive is edited
	// in between: that is the property under test.
	second := filepath.Join(base, "PLUGGED-IN-ELSEWHERE")
	if err := os.Rename(first, second); err != nil {
		t.Fatalf("remount: %v", err)
	}
	t.Run("second mount point", func(t *testing.T) { bootPreparedDrive(t, second) })

	if got := readFileForTest(t, filepath.Join(second, filepath.FromSlash(prepare.ConfigFile))); got != before {
		t.Fatal("caravan.yaml changed between mount points; the drive is not portable")
	}
	if _, err := os.Stat(first); err == nil {
		t.Fatalf("%s came back: something resolved a path against the old mount point", first)
	}
}

// bootPreparedDrive starts Caravan the way a launcher does and shuts it down
// the way the UI's "Shut down safely" button does.
func bootPreparedDrive(t *testing.T, drive string) {
	t.Helper()
	t.Chdir(drive)

	p := startPortable(t, filepath.FromSlash(prepare.ConfigFile))
	if root := p.storageRoot(t); root != "." {
		t.Fatalf("storage_root = %q, want \".\" — an absolute root pins the drive to one machine", root)
	}
	if status, body := p.post(t, "/system/shutdown"); status != http.StatusAccepted {
		t.Fatalf("POST /system/shutdown = %d %s", status, body)
	}
	p.waitStopped(t)

	// config_dir sent the database and the clean-shutdown marker into the
	// drive's own folder, and nothing leaked outside it.
	data := filepath.Join(drive, filepath.FromSlash(prepare.DataDir))
	for _, name := range []string{config.DatabaseFile, config.StateFile} {
		if _, err := os.Stat(filepath.Join(data, name)); err != nil {
			t.Fatalf("%s is not in %s: %v", name, prepare.DataDir, err)
		}
	}
	wantMarker(t, data, integrity.StateClean)
}

func readFileForTest(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}

// The usage text is how somebody discovers the subcommand exists.
func TestUsageMentionsPrepare(t *testing.T) {
	if !strings.Contains(usage, "caravan prepare") {
		t.Fatalf("usage does not mention prepare:\n%s", usage)
	}
}
