package prepare

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// walkRelPaths returns every path on the drive, root-relative and
// slash-separated, so an assertion can be made about the WHOLE layout rather
// than about the one folder the test remembered to look at.
func walkRelPaths(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
	return out
}

// The phase-9 acceptance: a prepared drive has ZERO adult paths unless the flag
// was passed. The assertion is over the whole tree and over the file CONTENTS
// as well, because a README or a config that named the adult library would
// disclose it just as loudly as a folder would — the drive is a thing somebody
// else plugs in.
func TestPrepareLeavesNoAdultTraceByDefault(t *testing.T) {
	root := t.TempDir()
	if _, err := Run(hostOpts(t, root)); err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, rel := range walkRelPaths(t, root) {
		if strings.Contains(strings.ToLower(rel), "adult") {
			t.Errorf("a default drive carries the path %q", rel)
		}
	}
	for _, rel := range []string{ConfigFile, ReadmeFile, MacLauncher, LinuxLauncher, WindowsLauncher} {
		if strings.Contains(strings.ToLower(read(t, root, rel)), "adult") {
			t.Errorf("%s mentions the adult library", rel)
		}
	}
	if exists(t, root, adultDir) {
		t.Errorf("%s was created without --include-adult", adultDir)
	}
	// The rest of the layout is untouched, so "excluded" is not "broken".
	for _, rel := range []string{"library/Movies", "library/TV", "incomplete"} {
		if !exists(t, root, rel) {
			t.Errorf("missing %s", rel)
		}
	}
}

// With the flag, the folder is there — and it is the one the organizer writes
// under, so a drive prepared this way is a drive the scanner recognises.
func TestPrepareIncludeAdultCreatesTheAdultRoot(t *testing.T) {
	root := t.TempDir()
	opts := hostOpts(t, root)
	opts.IncludeAdult = true
	if _, err := Run(opts); err != nil {
		t.Fatalf("Run: %v", err)
	}

	info, err := os.Stat(filepath.Join(root, filepath.FromSlash(adultDir)))
	if err != nil {
		t.Fatalf("missing %s: %v", adultDir, err)
	}
	if !info.IsDir() {
		t.Fatalf("%s is not a directory", adultDir)
	}
	if adultDir != "library/Adult" {
		t.Errorf("adultDir = %q, want the storage root's own library/Adult", adultDir)
	}
}

// The default list is the one the flag adds to, so a future edit to `dirs`
// cannot accidentally introduce the adult root for everyone.
func TestLayoutDirsAddsTheAdultRootOnlyWhenAsked(t *testing.T) {
	base := layoutDirs(false)
	with := layoutDirs(true)
	if len(with) != len(base)+1 || with[len(with)-1] != adultDir {
		t.Fatalf("layoutDirs(true) = %v, want the default list plus %q", with, adultDir)
	}

	// Asked for once, excluded ever after. The copy inside layoutDirs is what
	// makes this hold: appending straight onto the package-level `dirs` would
	// leave the adult root in it for every later caller the moment that slice
	// is given spare capacity, and the exclusion would then depend on the
	// order two commands happened to run in.
	for _, rel := range layoutDirs(false) {
		if strings.Contains(strings.ToLower(rel), "adult") {
			t.Fatalf("layoutDirs(false) contains %q after an --include-adult run", rel)
		}
	}
}
