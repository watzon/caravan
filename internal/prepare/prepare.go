// Package prepare scaffolds a portable Caravan drive (SPEC §2.3).
//
// The layout it writes is two things at once: a self-contained Caravan install
// that a launcher script starts on whichever computer the drive is plugged
// into, and a plain folder tree a television's USB browser can read when no
// server is running at all. That second job is why the library sits at the
// drive root in named folders rather than inside an application directory.
//
// prepare never formats, repartitions, or deletes anything (SPEC §17.1). It
// creates what is missing, refuses to clobber what holds state, and warns about
// the rest.
package prepare

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/library"
)

// Drive layout. Paths are slash-separated and relative to the drive root; they
// are the strings the report prints, so they are converted to OS paths only at
// the moment they touch the filesystem.
const (
	// CaravanDir holds everything Caravan owns. Keeping it out of the drive
	// root's top level is what leaves the root readable as a media drive.
	CaravanDir = "caravan"
	// BinDir holds one subdirectory per release target.
	BinDir = CaravanDir + "/bin"
	// DataDir holds caravan.db, the clean-shutdown marker, and logs. Its
	// contents are this drive's state and are never written by prepare.
	DataDir = CaravanDir + "/data"
	// ConfigFile is the drive-relative bootstrap config.
	ConfigFile = CaravanDir + "/caravan.yaml"
	// ReadmeFile explains the drive to whoever plugs it in.
	ReadmeFile = "README.txt"
)

// Launcher script names, at the drive root so they are the first thing a file
// manager shows.
const (
	MacLauncher     = "Start-Mac.command"
	WindowsLauncher = "Start-Windows.bat"
	LinuxLauncher   = "Start-Linux.sh"
)

// dirs is every directory the layout needs, parents before children.
var dirs = []string{
	CaravanDir,
	BinDir,
	DataDir,
	download.IncompleteDir,
	library.LibraryDir,
	library.LibraryDir + "/" + library.MoviesDir,
	library.LibraryDir + "/" + library.TVDir,
}

// Options configures one prepare run.
type Options struct {
	// Target is the drive's mount point. It must already exist: prepare
	// scaffolds a drive somebody has plugged in, and creating the directory
	// instead would silently build the layout inside a mistyped path on the
	// internal disk.
	Target string

	// Force refreshes the binaries, launchers and README even when they are
	// already there. It never touches the config file or the data directory.
	Force bool

	// BinDir is where to look for release binaries for the other operating
	// systems. Empty means "next to the running binary", which is the
	// release-bundle case: unpack the release, run the one for this machine,
	// and it finds its siblings.
	BinDir string

	// Self is the binary to install as this machine's target. Empty means the
	// running executable.
	Self string

	// GOOS and GOARCH identify which target Self satisfies. Empty means this
	// build's own. They exist so the tests can drive every branch on one
	// machine.
	GOOS, GOARCH string
}

// Result is what a run did, in the order it did it.
type Result struct {
	// Root is the drive root, as given.
	Root string
	// Created and Skipped are drive-relative paths: written now, and left
	// alone because they already existed.
	Created []string
	Skipped []string
	// Placed is one line per binary installed, drive-relative.
	Placed []string
	// Missing is the targets no binary could be found for. Never an error: a
	// drive with one OS's binary on it is a working drive for that OS.
	Missing []Target
	// Warnings are things that will bite the user later.
	Warnings []string
	// Notes are advisory, and always worth printing.
	Notes []string
}

// Run scaffolds the drive at opts.Target.
func Run(opts Options) (*Result, error) {
	root := strings.TrimSpace(opts.Target)
	if root == "" {
		return nil, errors.New("prepare: the target drive is required")
	}
	info, err := os.Stat(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("prepare: %s does not exist; mount the drive first", root)
	}
	if err != nil {
		return nil, fmt.Errorf("prepare: %s cannot be read: %w", root, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("prepare: %s is not a folder", root)
	}

	res := &Result{Root: root}
	res.Warnings, res.Notes = advise(root)

	for _, dir := range dirs {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0o755); err != nil {
			return nil, fmt.Errorf("prepare: create %s: %w", dir, err)
		}
	}

	// The config is written once and never again. It carries the drive's
	// identity — its storage root and where its database lives — and a drive
	// that has been used has a database that agrees with it.
	if err := res.write(root, ConfigFile, []byte(configYAML), 0o644, false); err != nil {
		return nil, err
	}

	// Launchers and README are derived entirely from this source tree, so
	// refreshing them loses nothing.
	for name, body := range map[string]string{
		MacLauncher:     macLauncher,
		LinuxLauncher:   linuxLauncher,
		WindowsLauncher: windowsLauncher,
	} {
		if err := res.write(root, name, []byte(body), 0o755, opts.Force); err != nil {
			return nil, err
		}
	}
	if err := res.write(root, ReadmeFile, []byte(readme), 0o644, opts.Force); err != nil {
		return nil, err
	}
	sort.Strings(res.Created)
	sort.Strings(res.Skipped)

	if err := placeBinaries(res, root, opts); err != nil {
		return nil, err
	}
	return res, nil
}

// write puts body at root/rel.
//
// An existing file is left alone and recorded as skipped unless overwrite is
// set; that is the whole of prepare's idempotency rule. The write itself goes
// through a temporary file in the same directory so an interrupted run cannot
// leave a half-written launcher that starts nothing.
func (r *Result) write(root, rel string, body []byte, mode fs.FileMode, overwrite bool) error {
	full := filepath.Join(root, filepath.FromSlash(rel))
	if !overwrite {
		switch _, err := os.Stat(full); {
		case err == nil:
			r.Skipped = append(r.Skipped, rel)
			return nil
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("prepare: %s cannot be read: %w", rel, err)
		}
	}
	if err := writeAtomic(full, mode, func(w io.Writer) error {
		_, err := w.Write(body)
		return err
	}); err != nil {
		return fmt.Errorf("prepare: write %s: %w", rel, err)
	}
	r.Created = append(r.Created, rel)
	return nil
}

// writeAtomic fills a temporary file beside dst and renames it into place.
//
// The chmod is best effort on purpose: exFAT carries no permission bits, so on
// the filesystem this command exists for the call is either a no-op or an
// error, and neither is a reason to fail. Where the bit does matter — a drive
// somebody formatted ext4 or APFS — it is set.
func writeAtomic(dst string, mode fs.FileMode, fill func(io.Writer) error) error {
	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		tmp.Close()
		os.Remove(name)
	}()

	if err := fill(tmp); err != nil {
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	_ = os.Chmod(name, mode)
	return os.Rename(name, dst)
}

// advise reports what prepare found out about the drive without touching it.
//
// Filesystem detection is best effort by design (SPEC §17.1 keeps prepare
// non-destructive, so the answer only ever changes what is printed): a platform
// with no implementation, or a path the kernel will not describe, produces no
// warning rather than a scary one.
func advise(root string) (warnings, notes []string) {
	notes = []string{
		"Drives larger than 2 TiB have to be GPT-partitioned. Some older televisions " +
			"only read MBR, so check the TV's manual before repartitioning.",
		"Caravan never formats a drive. If this one is not exFAT, reformat it yourself " +
			"— that erases it — and run prepare again.",
	}

	name, err := filesystemName(root)
	if err != nil || name == "" {
		return nil, notes
	}
	if strings.EqualFold(name, "exfat") {
		return nil, notes
	}
	return []string{fmt.Sprintf(
		"%s is formatted as %s, not exFAT. exFAT is the only filesystem that reads and "+
			"writes on Windows, macOS and Linux alike, so this drive will not be portable "+
			"across all three as it is.", root, name)}, notes
}

// Report writes a human summary of the run.
func (r *Result) Report(w io.Writer) {
	fmt.Fprintf(w, "Prepared %s\n", r.Root)
	for _, p := range r.Created {
		fmt.Fprintf(w, "  created  %s\n", p)
	}
	for _, p := range r.Placed {
		fmt.Fprintf(w, "  binary   %s\n", p)
	}
	for _, p := range r.Skipped {
		fmt.Fprintf(w, "  kept     %s\n", p)
	}
	for _, t := range r.Missing {
		fmt.Fprintf(w, "\nwarning: no binary for %s/%s.\n", t.GOOS, t.GOARCH)
		fmt.Fprintf(w, "  Download the %s/%s build from the Caravan release and put it at\n", t.GOOS, t.GOARCH)
		fmt.Fprintf(w, "  %s, or unpack the release somewhere and re-run:\n", t.RelPath())
		fmt.Fprintf(w, "    caravan prepare -bin-dir <unpacked-release> %s\n", r.Root)
	}
	for _, warning := range r.Warnings {
		fmt.Fprintf(w, "\nwarning: %s\n", warning)
	}
	for _, note := range r.Notes {
		fmt.Fprintf(w, "\nnote: %s\n", note)
	}
	if len(r.Skipped) > 0 {
		fmt.Fprintf(w, "\nSome files were kept as they were. Re-run with -force to refresh the\n"+
			"launchers and binaries; %s and %s/ are never overwritten.\n", ConfigFile, DataDir)
	}
	fmt.Fprintf(w, "\nEject the drive, plug it into the target machine, and double-click %s,\n"+
		"%s or %s. See %s first on Windows.\n",
		MacLauncher, WindowsLauncher, LinuxLauncher, ReadmeFile)
}

// hostTarget is the target opts.Self satisfies.
func hostTarget(opts Options) Target {
	t := Target{GOOS: opts.GOOS, GOARCH: opts.GOARCH}
	if t.GOOS == "" {
		t.GOOS = runtime.GOOS
	}
	if t.GOARCH == "" {
		t.GOARCH = runtime.GOARCH
	}
	return t
}

// relPath keeps the layout's slash convention in one place.
func relPath(elem ...string) string { return path.Join(elem...) }
