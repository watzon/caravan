package prepare

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Target is one release build.
type Target struct{ GOOS, GOARCH string }

// Targets is the release matrix, and has to stay identical to the
// cross-compile matrix in .github/workflows/ci.yml: a target CI does not build
// is a target prepare would tell users to download something that does not
// exist.
var Targets = []Target{
	{GOOS: "linux", GOARCH: "amd64"},
	{GOOS: "linux", GOARCH: "arm64"},
	{GOOS: "windows", GOARCH: "amd64"},
	// Both Mac architectures, because Start-Mac.command resolves `uname -m`
	// itself (see assets.go): an Intel Mac execs caravan/bin/darwin-amd64, and
	// leaving that target out of the matrix meant prepare never warned the slot
	// was empty — the user found out by double-clicking the launcher on the
	// other machine and reading "caravan/bin/darwin-amd64/caravan is missing".
	{GOOS: "darwin", GOARCH: "amd64"},
	{GOOS: "darwin", GOARCH: "arm64"},
}

// Slug is the target's directory name under BinDir.
func (t Target) Slug() string { return t.GOOS + "-" + t.GOARCH }

// BinaryName is what the launcher for this target executes.
func (t Target) BinaryName() string {
	if t.GOOS == "windows" {
		return "caravan.exe"
	}
	return "caravan"
}

// RelPath is the target's binary, drive-relative.
func (t Target) RelPath() string { return relPath(BinDir, t.Slug(), t.BinaryName()) }

// candidates lists the file names a release build for this target plausibly has
// inside a directory of release artifacts, most specific first.
//
// Several spellings are accepted because the naming is the release pipeline's
// choice, not this package's, and a user who unpacked an archive should not
// have to rename anything to be understood.
func (t Target) candidates() []string {
	name := t.BinaryName()
	return []string{
		relPath(t.Slug(), name),
		relPath(t.GOOS+"_"+t.GOARCH, name),
		"caravan-" + t.Slug() + exeSuffix(t),
		"caravan_" + t.GOOS + "_" + t.GOARCH + exeSuffix(t),
	}
}

func exeSuffix(t Target) string {
	if t.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// placeBinaries installs this machine's binary and whatever sibling release
// builds it can find, recording the rest as missing.
func placeBinaries(res *Result, root *os.Root, opts Options) error {
	self := opts.Self
	if self == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("prepare: locate the running binary: %w", err)
		}
		self = exe
	}
	// Symlinks matter here: on Linux the running binary is often reached
	// through one, and copying the link's target is what actually produces a
	// runnable file on the drive.
	if resolved, err := filepath.EvalSymlinks(self); err == nil {
		self = resolved
	}

	searchDir := opts.BinDir
	if searchDir == "" {
		searchDir = filepath.Dir(self)
	}

	host := hostTarget(opts)
	for _, t := range Targets {
		source := ""
		if t == host {
			source = self
		} else if found, ok := findBinary(searchDir, t); ok {
			source = found
		}
		if source == "" {
			res.Missing = append(res.Missing, t)
			continue
		}
		placed, err := installBinary(root, t, source, opts.Force)
		if err != nil {
			return err
		}
		if placed {
			res.Placed = append(res.Placed, t.RelPath())
		} else {
			res.Skipped = append(res.Skipped, t.RelPath())
		}
	}
	return nil
}

// findBinary looks for a release build of t inside dir.
func findBinary(dir string, t Target) (string, bool) {
	if dir == "" {
		return "", false
	}
	for _, candidate := range t.candidates() {
		full := filepath.Join(dir, filepath.FromSlash(candidate))
		info, err := os.Stat(full)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		return full, true
	}
	return "", false
}

// installBinary copies source into the drive's slot for t. It reports whether
// it wrote anything: an existing binary is kept unless force is set, so
// re-running prepare on a drive somebody already carries does not rewrite
// hundreds of megabytes for nothing.
func installBinary(root *os.Root, t Target, source string, force bool) (bool, error) {
	dst := t.RelPath()
	if !force {
		switch info, err := root.Lstat(filepath.FromSlash(dst)); {
		case err == nil:
			if !info.Mode().IsRegular() {
				return false, fmt.Errorf("prepare: %s exists and is not a regular file", t.RelPath())
			}
			return false, nil
		case !errors.Is(err, fs.ErrNotExist):
			return false, fmt.Errorf("prepare: %s cannot be read: %w", t.RelPath(), err)
		}
	}
	if err := makePortableDir(root, relPath(BinDir, t.Slug())); err != nil {
		return false, fmt.Errorf("prepare: create %s: %w", relPath(BinDir, t.Slug()), err)
	}

	err := writeAtomic(root, dst, 0o755, func(w io.Writer) error {
		in, err := os.Open(source)
		if err != nil {
			return err
		}
		defer in.Close()
		_, err = io.Copy(w, in)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("prepare: copy %s to %s: %w", source, t.RelPath(), err)
	}
	return true, nil
}
