package prepare

import (
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

	for _, want := range []string{"portable: true", `storage_root: "."`, `config_dir: "caravan/data"`} {
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

// Re-running prepare on a drive that has been used must not touch its state.
func TestRerunKeepsConfigAndData(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(hostOpts(t, root)); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	// The marks a used drive would carry: an edited config and a database.
	edited := "portable: true\nstorage_root: \".\"\nconfig_dir: \"caravan/data\"\nlog_level: \"debug\"\n"
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

// -force refreshes what this source tree owns, and still not the drive's state.
func TestForceRefreshesLaunchersButNotConfig(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(hostOpts(t, root)); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	edited := "portable: true\n# hand edited\n"
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
