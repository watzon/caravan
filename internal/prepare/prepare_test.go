package prepare

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// fakeSelf writes a stand-in for the running binary so the tests never copy the
// real test executable around, and returns its path.
func fakeSelf(t *testing.T, dir, body string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	path := filepath.Join(dir, "caravan")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

// hostOpts is a run pinned to a fixed host target, so every assertion below
// means the same thing on every developer's machine.
func hostOpts(t *testing.T, target string) Options {
	t.Helper()
	return Options{
		Target: target,
		Self:   fakeSelf(t, filepath.Join(t.TempDir(), "release"), "host binary"),
		GOOS:   "darwin",
		GOARCH: "arm64",
	}
}

func read(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func exists(t *testing.T, root, rel string) bool {
	t.Helper()
	_, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel)))
	return err == nil
}

// The whole layout SPEC §2.3 promises, from one run on an empty directory.
func TestRunScaffoldsTheDriveLayout(t *testing.T) {
	root := t.TempDir()
	res, err := Run(hostOpts(t, root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, dir := range dirs {
		info, err := os.Stat(filepath.Join(root, filepath.FromSlash(dir)))
		if err != nil {
			t.Fatalf("missing directory %s: %v", dir, err)
		}
		if !info.IsDir() {
			t.Fatalf("%s is not a directory", dir)
		}
	}
	for _, file := range []string{ConfigFile, ReadmeFile, MacLauncher, LinuxLauncher, WindowsLauncher} {
		if !exists(t, root, file) {
			t.Fatalf("missing file %s", file)
		}
		if !slices.Contains(res.Created, file) {
			t.Fatalf("Created = %v, want it to include %s", res.Created, file)
		}
	}
	if len(res.Skipped) != 0 {
		t.Fatalf("Skipped = %v, want nothing skipped on a fresh drive", res.Skipped)
	}

	// The library tree is the TV-USB browsing experience, so the folder names
	// are part of the contract and not an implementation detail.
	for _, dir := range []string{"library/Movies", "library/TV", "incomplete"} {
		if !exists(t, root, dir) {
			t.Fatalf("missing %s", dir)
		}
	}
}

// The config is what makes the drive mount-point independent, so its contents
// are asserted rather than merely its existence.
func TestConfigIsDriveRelative(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(hostOpts(t, root)); err != nil {
		t.Fatalf("Run: %v", err)
	}
	cfg := read(t, root, ConfigFile)

	for _, want := range []string{"portable: true", `storage_root: "."`, `data_dir: "caravan/data"`} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("caravan.yaml is missing %q:\n%s", want, cfg)
		}
	}
	// An absolute path in a *setting* pins the drive to one computer. The
	// comments name mount points on purpose — that is what they are explaining
	// — so only the settings are checked.
	for _, line := range strings.Split(cfg, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		_, value, ok := strings.Cut(line, ":")
		if !ok {
			t.Fatalf("caravan.yaml line %q is not a setting", line)
		}
		value = strings.Trim(strings.TrimSpace(value), `"`)
		if strings.HasPrefix(value, "/") || strings.Contains(value, `:\`) {
			t.Fatalf("caravan.yaml pins the drive to one computer: %q", line)
		}
	}
}

func TestRunWritesChosenPortableLocations(t *testing.T) {
	root := t.TempDir()
	opts := hostOpts(t, root)
	opts.DataDir = "state/caravan"
	opts.StorageRoot = "media"

	if _, err := Run(opts); err != nil {
		t.Fatalf("Run: %v", err)
	}
	cfg := read(t, root, ConfigFile)
	for _, want := range []string{`data_dir: "state/caravan"`, `storage_root: "media"`} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("caravan.yaml is missing %q:\n%s", want, cfg)
		}
	}
	for _, dir := range []string{"state/caravan", "media/incomplete", "media/library/Movies", "media/library/TV"} {
		if !exists(t, root, dir) {
			t.Errorf("chosen portable layout is missing %s", dir)
		}
	}
	readme := read(t, root, ReadmeFile)
	for _, want := range []string{
		"media/library/Movies/",
		"media/library/TV/",
		"media/incomplete/",
		"state/caravan/",
		"state/caravan/caravan.db",
	} {
		if !strings.Contains(readme, want) {
			t.Errorf("custom-layout README is missing %q", want)
		}
	}
	if strings.Contains(readme, "caravan/data/caravan.db") {
		t.Error("custom-layout README still points at the default database")
	}
}

func TestRunRejectsUnsafePortableLocations(t *testing.T) {
	tests := []struct {
		name        string
		dataDir     string
		storageRoot string
	}{
		{name: "absolute data directory", dataDir: filepath.Join(t.TempDir(), "state")},
		{name: "parent data directory", dataDir: "../state"},
		{name: "absolute storage root", storageRoot: filepath.Join(t.TempDir(), "media")},
		{name: "parent storage root", storageRoot: "../media"},
		{name: "Windows-shaped data directory", dataDir: `C:\caravan`},
		{name: "data directory collides with config file", dataDir: ConfigFile},
		{name: "storage root collides with launcher", storageRoot: MacLauncher},
		{name: "data directory descends through config file", dataDir: ConfigFile + "/state"},
		{name: "storage root descends through case-variant README", storageRoot: strings.ToUpper(ReadmeFile) + "/media"},
		{name: "data directory descends through generated binary", dataDir: Targets[0].RelPath() + "/state"},
		{name: "Windows device-name component", storageRoot: "NUL/media"},
		{name: "Windows trailing-dot component", dataDir: "state./caravan"},
		{name: "Windows outer trailing-space component", dataDir: "state "},
		{name: "Windows-invalid character", storageRoot: "media*/library"},
		{name: "Windows console device component", storageRoot: "CONIN$/media"},
		{name: "Windows superscript COM device component", dataDir: "COM¹/cache"},
		{name: "Windows superscript LPT device component", storageRoot: "LPT³/media"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := hostOpts(t, t.TempDir())
			opts.DataDir = tt.dataDir
			opts.StorageRoot = tt.storageRoot
			if _, err := Run(opts); err == nil {
				t.Fatal("Run succeeded with an unsafe portable location")
			}
		})
	}
}

func TestRunRejectsSymlinkedLayoutAncestorThatEscapesDrive(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "state")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	opts := hostOpts(t, root)
	opts.DataDir = "state/caravan"
	_, err := Run(opts)
	if err == nil {
		t.Fatal("Run succeeded through a symlink that leaves the portable drive")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "caravan")); !os.IsNotExist(statErr) {
		t.Fatalf("outside path was touched: %v", statErr)
	}
}

func TestRunRejectsSymlinkedBinarySlotThatEscapesDrive(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	binDir := filepath.Join(root, filepath.FromSlash(BinDir))
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(binDir, "darwin-arm64")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := Run(hostOpts(t, root)); err == nil {
		t.Fatal("Run succeeded through a binary-slot symlink that leaves the portable drive")
	}
	if _, statErr := os.Stat(filepath.Join(outside, "caravan")); !os.IsNotExist(statErr) {
		t.Fatalf("outside binary path was touched: %v", statErr)
	}
}

func TestRunRejectsInvalidExistingArtifactBeforeCreatingLayout(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ReadmeFile), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := Run(hostOpts(t, root)); err == nil {
		t.Fatal("Run accepted a directory where README.txt must be a regular file")
	}
	if exists(t, root, CaravanDir) {
		t.Error("Run created a partial layout before rejecting the invalid artifact")
	}
}

func TestRunRejectsConcurrentPreparation(t *testing.T) {
	root := t.TempDir()
	drive, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer drive.Close()
	lock, err := acquirePrepareLock(drive)
	if err != nil {
		t.Fatalf("acquire first prepare lock: %v", err)
	}
	defer lock.Close()

	if _, err := Run(hostOpts(t, root)); !errors.Is(err, ErrPrepareLocked) {
		t.Fatalf("concurrent Run error = %v, want ErrPrepareLocked", err)
	}
	if exists(t, root, CaravanDir) {
		t.Error("concurrent Run created a partial layout")
	}
}

// Re-running prepare on a drive that has been used must not touch its state.
func TestRerunKeepsConfigAndData(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(hostOpts(t, root)); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// The marks a used drive would carry: an edited config and a database.
	edited := "portable: true\nstorage_root: \".\"\ndata_dir: \"caravan/data\"\nlog_level: \"debug\"\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(ConfigFile)), []byte(edited), 0o644); err != nil {
		t.Fatalf("edit config: %v", err)
	}
	db := filepath.Join(root, filepath.FromSlash(DataDir), "caravan.db")
	if err := os.WriteFile(db, []byte("sqlite"), 0o644); err != nil {
		t.Fatalf("write db: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, MacLauncher), []byte("stale"), 0o755); err != nil {
		t.Fatalf("stale launcher: %v", err)
	}

	res, err := Run(hostOpts(t, root))
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if got := read(t, root, ConfigFile); got != edited {
		t.Fatalf("caravan.yaml was overwritten:\n%s", got)
	}
	if got := read(t, root, relPath(DataDir, "caravan.db")); got != "sqlite" {
		t.Fatalf("database was touched: %q", got)
	}
	if got := read(t, root, MacLauncher); got != "stale" {
		t.Fatalf("launcher was rewritten without -force")
	}
	for _, rel := range []string{ConfigFile, MacLauncher, ReadmeFile} {
		if !slices.Contains(res.Skipped, rel) {
			t.Fatalf("Skipped = %v, want it to include %s", res.Skipped, rel)
		}
	}
	if len(res.Created) != 0 {
		t.Fatalf("Created = %v, want nothing created on a re-run", res.Created)
	}
}

func TestRerunUsesLocationsAlreadyRecordedOnTheDrive(t *testing.T) {
	root := t.TempDir()
	first := hostOpts(t, root)
	first.DataDir = "state/caravan"
	first.StorageRoot = "media"
	if _, err := Run(first); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	if _, err := Run(hostOpts(t, root)); err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if exists(t, root, DataDir) {
		t.Errorf("rerun created default data directory %s instead of keeping the configured location", DataDir)
	}
	for _, dir := range []string{"state/caravan", "media/library/Movies", "media/incomplete"} {
		if !exists(t, root, dir) {
			t.Errorf("rerun lost configured portable location %s", dir)
		}
	}
}

func TestRerunRejectsConflictingLocationChoices(t *testing.T) {
	root := t.TempDir()
	first := hostOpts(t, root)
	first.DataDir = "state/caravan"
	first.StorageRoot = "media"
	if _, err := Run(first); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	second := hostOpts(t, root)
	second.DataDir = "different-state"
	if _, err := Run(second); err == nil {
		t.Fatal("rerun accepted a data directory that conflicts with the existing config")
	}
	if exists(t, root, "different-state") {
		t.Error("rerun created the conflicting data directory")
	}
}

func TestRerunRejectsExistingConfigWithoutRecordedLocations(t *testing.T) {
	tests := []struct {
		name       string
		configYAML string
		want       string
	}{
		{
			name:       "missing data directory",
			configYAML: "portable: true\nstorage_root: \".\"\n",
			want:       "data_dir",
		},
		{
			name:       "empty data directory",
			configYAML: "portable: true\ndata_dir: \"\"\nstorage_root: \".\"\n",
			want:       "data_dir",
		},
		{
			name:       "missing storage root",
			configYAML: "portable: true\ndata_dir: \"caravan/data\"\n",
			want:       "storage_root",
		},
		{
			name:       "empty storage root",
			configYAML: "portable: true\ndata_dir: \"caravan/data\"\nstorage_root: \"\"\n",
			want:       "storage_root",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, CaravanDir), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(ConfigFile)), []byte(tt.configYAML), 0o644); err != nil {
				t.Fatal(err)
			}

			_, err := Run(hostOpts(t, root))
			if err == nil {
				t.Fatal("rerun accepted an existing config without both recorded locations")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want it to name %s", err, tt.want)
			}
			for _, unexpected := range []string{DataDir, "incomplete", "library"} {
				if exists(t, root, unexpected) {
					t.Errorf("rerun created %s before rejecting the incomplete config", unexpected)
				}
			}
		})
	}
}

// -force refreshes what this source tree owns, and still not the drive's state.
func TestForceRefreshesLaunchersButNotConfig(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(hostOpts(t, root)); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	edited := "portable: true\nstorage_root: \".\"\ndata_dir: \"caravan/data\"\n# hand edited\n"
	if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(ConfigFile)), []byte(edited), 0o644); err != nil {
		t.Fatalf("edit config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, MacLauncher), []byte("stale"), 0o755); err != nil {
		t.Fatalf("stale launcher: %v", err)
	}

	opts := hostOpts(t, root)
	opts.Force = true
	res, err := Run(opts)
	if err != nil {
		t.Fatalf("forced Run: %v", err)
	}

	if got := read(t, root, MacLauncher); got == "stale" {
		t.Fatal("-force did not refresh the launcher")
	}
	if got := read(t, root, ConfigFile); got != edited {
		t.Fatalf("-force overwrote caravan.yaml:\n%s", got)
	}
	if slices.Contains(res.Skipped, MacLauncher) {
		t.Fatalf("Skipped = %v, want the launcher refreshed", res.Skipped)
	}
}

// prepare scaffolds a drive somebody plugged in; it must not invent one.
func TestRunRefusesAMissingOrNonDirectoryTarget(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-mounted")
	if _, err := Run(Options{Target: missing}); err == nil {
		t.Fatal("Run on a missing target succeeded, want an error")
	} else if !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("error = %v, want it to say the drive is not mounted", err)
	}
	if exists(t, missing, ".") {
		t.Fatal("Run created the target directory")
	}

	file := filepath.Join(t.TempDir(), "drive")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, err := Run(Options{Target: file}); err == nil {
		t.Fatal("Run on a file succeeded, want an error")
	}

	if _, err := Run(Options{Target: "  "}); err == nil {
		t.Fatal("Run with a blank target succeeded, want an error")
	}
}

// The GPT advice is unconditional; the exFAT warning is not, and this machine's
// temp directory is never exFAT, so an unsupported platform is the only case
// where it may be absent.
func TestAdviseAlwaysNotesGPT(t *testing.T) {
	root := t.TempDir()
	warnings, notes := advise(root)

	joined := strings.Join(notes, " ")
	if !strings.Contains(joined, "GPT") {
		t.Fatalf("notes = %v, want the GPT recommendation", notes)
	}
	if !strings.Contains(joined, "never formats") {
		t.Fatalf("notes = %v, want the non-destructive promise (SPEC §17.1)", notes)
	}

	name, err := filesystemName(root)
	if err != nil || name == "" {
		t.Skipf("filesystem detection unavailable here (%q, %v)", name, err)
	}
	if name == "exfat" {
		t.Skip("the temp directory really is exFAT")
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0], "exFAT") {
		t.Fatalf("warnings = %v, want one exFAT warning for a %s drive", warnings, name)
	}
	if !strings.Contains(warnings[0], name) {
		t.Fatalf("warning %q does not name the filesystem it found (%s)", warnings[0], name)
	}
}

func TestNormalizeFilesystem(t *testing.T) {
	for in, want := range map[string]string{
		"exFAT":   "exfat",
		"exfat":   "exfat",
		" NTFS  ": "ntfs",
		"apfs":    "apfs",
		"":        "",
	} {
		if got := normalizeFilesystem(in); got != want {
			t.Fatalf("normalizeFilesystem(%q) = %q, want %q", in, got, want)
		}
	}
}

// The report is the only thing a user of the CLI sees, so the parts that tell
// them what to do next are asserted.
func TestReportNamesMissingBinariesAndWarnings(t *testing.T) {
	root := t.TempDir()
	res, err := Run(hostOpts(t, root))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	var out strings.Builder
	res.Report(&out)
	got := out.String()

	if !strings.Contains(got, "windows-amd64") {
		t.Fatalf("report does not mention the missing windows binary:\n%s", got)
	}
	if !strings.Contains(got, "-bin-dir") {
		t.Fatalf("report does not say how to supply the missing binaries:\n%s", got)
	}
	if !strings.Contains(got, MacLauncher) {
		t.Fatalf("report does not tell the user how to start the drive:\n%s", got)
	}
	for _, note := range res.Notes {
		if !strings.Contains(got, note) {
			t.Fatalf("report dropped note %q:\n%s", note, got)
		}
	}
}

func TestReportNamesTheSelectedDataDirectory(t *testing.T) {
	res := &Result{
		Root:    "/media/CARAVAN",
		DataDir: "state/caravan",
		Skipped: []string{ConfigFile},
	}
	var out strings.Builder
	res.Report(&out)
	if got := out.String(); !strings.Contains(got, "state/caravan") {
		t.Fatalf("report does not name the selected data directory:\n%s", got)
	}
}
