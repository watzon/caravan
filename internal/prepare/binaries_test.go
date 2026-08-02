package prepare

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// The host binary is the running executable, and it lands in the slot the
// launcher for this platform looks in.
func TestPlacesTheRunningBinaryInItsOwnSlot(t *testing.T) {
	root := t.TempDir()
	opts := hostOpts(t, root)
	res, err := Run(opts)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	host := Target{GOOS: "darwin", GOARCH: "arm64"}
	if got := read(t, root, host.RelPath()); got != "host binary" {
		t.Fatalf("%s = %q, want the running binary copied in", host.RelPath(), got)
	}
	if !slices.Contains(res.Placed, host.RelPath()) {
		t.Fatalf("Placed = %v, want it to include %s", res.Placed, host.RelPath())
	}
	// Everything else is missing, and that is a warning, not a failure.
	if len(res.Missing) != len(Targets)-1 {
		t.Fatalf("Missing = %v, want the other %d targets", res.Missing, len(Targets)-1)
	}
	if slices.Contains(res.Missing, host) {
		t.Fatalf("Missing = %v, want the host target placed", res.Missing)
	}
}

// The release-bundle case: unpack a release, run the binary for this machine,
// and it finds its siblings next to itself.
func TestPlacesSiblingReleaseBinaries(t *testing.T) {
	release := t.TempDir()
	self := fakeSelf(t, release, "darwin build")

	// One file per accepted naming, so a change to the accepted set has to
	// come with a decision about which spellings still work.
	writeAt := func(rel, body string) {
		full := filepath.Join(release, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(full, []byte(body), 0o755); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	writeAt("linux-amd64/caravan", "linux amd64 build")
	writeAt("linux_arm64/caravan", "linux arm64 build")
	writeAt("caravan-windows-amd64.exe", "windows build")
	writeAt("caravan_darwin_amd64", "darwin amd64 build")

	root := t.TempDir()
	res, err := Run(Options{Target: root, Self: self, GOOS: "darwin", GOARCH: "arm64"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Missing) != 0 {
		t.Fatalf("Missing = %v, want every target placed", res.Missing)
	}

	for rel, want := range map[string]string{
		"caravan/bin/darwin-arm64/caravan":      "darwin build",
		"caravan/bin/linux-amd64/caravan":       "linux amd64 build",
		"caravan/bin/linux-arm64/caravan":       "linux arm64 build",
		"caravan/bin/windows-amd64/caravan.exe": "windows build",
		"caravan/bin/darwin-amd64/caravan":      "darwin amd64 build",
	} {
		if got := read(t, root, rel); got != want {
			t.Fatalf("%s = %q, want %q", rel, got, want)
		}
	}
}

// -bin-dir is the offline path SPEC §2.3 asks for: point prepare at an unpacked
// release somewhere else on disk.
func TestBinDirOverridesTheSearchDirectory(t *testing.T) {
	self := fakeSelf(t, filepath.Join(t.TempDir(), "installed"), "host build")
	bundle := t.TempDir()
	if err := os.WriteFile(filepath.Join(bundle, "caravan_windows_amd64.exe"), []byte("windows build"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	root := t.TempDir()
	res, err := Run(Options{Target: root, Self: self, BinDir: bundle, GOOS: "darwin", GOARCH: "arm64"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := read(t, root, "caravan/bin/windows-amd64/caravan.exe"); got != "windows build" {
		t.Fatalf("windows binary = %q, want the one from -bin-dir", got)
	}
	if slices.Contains(res.Missing, Target{GOOS: "windows", GOARCH: "amd64"}) {
		t.Fatalf("Missing = %v, want windows found in -bin-dir", res.Missing)
	}
}

// Copying hundreds of megabytes on every re-run is the reason binaries are
// skipped by default.
func TestBinariesAreKeptWithoutForce(t *testing.T) {
	root := t.TempDir()
	opts := hostOpts(t, root)
	if _, err := Run(opts); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	host := Target{GOOS: "darwin", GOARCH: "arm64"}
	dst := filepath.Join(root, filepath.FromSlash(host.RelPath()))
	if err := os.WriteFile(dst, []byte("older build"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}

	res, err := Run(opts)
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if got := read(t, root, host.RelPath()); got != "older build" {
		t.Fatalf("binary = %q, want it kept without -force", got)
	}
	if !slices.Contains(res.Skipped, host.RelPath()) {
		t.Fatalf("Skipped = %v, want it to include %s", res.Skipped, host.RelPath())
	}

	opts.Force = true
	if _, err := Run(opts); err != nil {
		t.Fatalf("forced Run: %v", err)
	}
	if got := read(t, root, host.RelPath()); got != "host binary" {
		t.Fatalf("binary = %q, want -force to refresh it", got)
	}
}

// A directory named like a binary is not a binary; findBinary must not offer it.
func TestFindBinaryIgnoresNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "caravan-linux-amd64"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, ok := findBinary(dir, Target{GOOS: "linux", GOARCH: "amd64"}); ok {
		t.Fatal("findBinary matched a directory")
	}
	if _, ok := findBinary("", Target{GOOS: "linux", GOARCH: "amd64"}); ok {
		t.Fatal("findBinary matched with no search directory")
	}
}

// The release matrix is a contract with .github/workflows/ci.yml and with the
// launcher scripts; both would silently break if a target were renamed.
func TestTargetNaming(t *testing.T) {
	for _, tc := range []struct {
		target       Target
		slug, binary string
	}{
		{Target{"linux", "amd64"}, "linux-amd64", "caravan"},
		{Target{"linux", "arm64"}, "linux-arm64", "caravan"},
		{Target{"windows", "amd64"}, "windows-amd64", "caravan.exe"},
		{Target{"darwin", "amd64"}, "darwin-amd64", "caravan"},
		{Target{"darwin", "arm64"}, "darwin-arm64", "caravan"},
	} {
		if got := tc.target.Slug(); got != tc.slug {
			t.Fatalf("Slug() = %q, want %q", got, tc.slug)
		}
		if got := tc.target.BinaryName(); got != tc.binary {
			t.Fatalf("BinaryName() = %q, want %q", got, tc.binary)
		}
		want := "caravan/bin/" + tc.slug + "/" + tc.binary
		if got := tc.target.RelPath(); got != want {
			t.Fatalf("RelPath() = %q, want %q", got, want)
		}
		if !slices.Contains(Targets, tc.target) {
			t.Fatalf("Targets = %v, want it to include %v", Targets, tc.target)
		}
	}
	if len(Targets) != 5 {
		t.Fatalf("Targets = %v, want exactly the five in the CI matrix", Targets)
	}
	// RelPath is what the launchers exec, so the two must agree.
	for _, tc := range []struct{ script, path string }{
		{macLauncher, "caravan/bin/darwin-$arch/caravan"},
		{linuxLauncher, "caravan/bin/linux-$arch/caravan"},
		{windowsLauncherLF, `caravan\bin\windows-amd64\caravan.exe`},
	} {
		if !strings.Contains(tc.script, tc.path) {
			t.Fatalf("a launcher no longer references %q", tc.path)
		}
	}
}

// A slot a launcher can execute but the matrix does not build is a slot prepare
// never warns about: the drive reports "no binary for ..." for the targets it
// knows and says nothing at all about the one it does not, so the user finds
// out by double-clicking the launcher on the other machine and reading
// "caravan/bin/darwin-amd64/caravan is missing".
//
// That is exactly what an Intel Mac hit before darwin/amd64 joined Targets, and
// it is why AC2's "at least two OSes" could not include Intel macOS.
func TestTargetsCoverEverySlotALauncherCanExecute(t *testing.T) {
	// The POSIX launchers resolve `uname -m` into one of these and exec
	// caravan/bin/<goos>-<arch>/caravan (see assets.go).
	posixArches := []string{"amd64", "arm64"}
	want := []Target{{GOOS: "windows", GOARCH: "amd64"}}
	for _, goos := range []string{"darwin", "linux"} {
		for _, arch := range posixArches {
			want = append(want, Target{GOOS: goos, GOARCH: arch})
		}
	}

	for _, w := range want {
		if !slices.Contains(Targets, w) {
			t.Errorf("%s is executable by a launcher but is not a release target", w.Slug())
		}
	}

	// And the launchers really do resolve every arch this test assumes, so the
	// assertion above cannot pass by having been written against a stale idea
	// of what the scripts do.
	for name, script := range map[string]string{MacLauncher: macLauncher, LinuxLauncher: linuxLauncher} {
		for _, arch := range posixArches {
			if !strings.Contains(script, "arch="+arch) {
				t.Errorf("%s does not resolve %s", name, arch)
			}
		}
	}
}
