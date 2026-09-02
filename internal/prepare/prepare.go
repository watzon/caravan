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
	"crypto/rand"
	"encoding/hex"
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
	"gopkg.in/yaml.v3"
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
	// DataDir holds caravan.db, the clean-shutdown marker, and related state. Its
	// contents are this drive's state and are never written by prepare.
	DataDir = CaravanDir + "/data"
	// StorageRoot is the default portable media root: the drive itself.
	StorageRoot = "."
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
//
// library/Adult is deliberately NOT here. A prepared drive is carried to
// another house and plugged into a television, and the layout it arrives with
// is a statement about what is on it: a folder named Adult at the root of a
// drive somebody borrowed is the disclosure this phase exists to prevent, even
// empty. Opting in is layoutDirs' job.
var dirs = []string{
	CaravanDir,
	BinDir,
	DataDir,
	download.IncompleteDir,
	library.LibraryDir,
	library.LibraryDir + "/" + library.MoviesDir,
	library.LibraryDir + "/" + library.TVDir,
}

// adultDir is the adult library's folder on a prepared drive. It is derived
// from the same constants the organizer writes under, so a drive prepared with
// --include-adult is a drive the scanner recognises.
var adultDir = library.LibraryDir + "/" + library.AdultDir

// layoutDirs is the directory list for one run.
//
// The adult root is appended rather than filtered out, so "excluded" is the
// absence of a decision rather than a rule that has to keep working: a bug in
// the flag handling leaves the drive with no adult folder, which is the safe
// direction to fail in.
func layoutDirs(includeAdult bool) []string {
	if !includeAdult {
		return dirs
	}
	return append(append([]string(nil), dirs...), adultDir)
}

func layoutDirsFor(dataDir, storageRoot string, includeAdult bool) []string {
	dirs := []string{
		CaravanDir,
		BinDir,
		dataDir,
		relPath(storageRoot, download.IncompleteDir),
		relPath(storageRoot, library.LibraryDir),
		relPath(storageRoot, library.LibraryDir, library.MoviesDir),
		relPath(storageRoot, library.LibraryDir, library.TVDir),
	}
	if includeAdult {
		dirs = append(dirs, relPath(storageRoot, library.LibraryDir, library.AdultDir))
	}
	return dirs
}

// Options configures one prepare run.
type Options struct {
	// Target is the drive's mount point. It must already exist: prepare
	// scaffolds a drive somebody has plugged in, and creating the directory
	// instead would silently build the layout inside a mistyped path on the
	// internal disk.
	Target string

	// DataDir is the drive-relative directory for Caravan's database and
	// runtime state. Empty selects DataDir.
	DataDir string

	// StorageRoot is the drive-relative root for media and downloads. Empty
	// selects StorageRoot, the drive root itself.
	StorageRoot string

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

	// IncludeAdult scaffolds library/Adult as well. The default is off, and that
	// is the point: a drive is a thing that leaves the house, so the adult
	// library goes onto one only when somebody
	// says so at the command line, every single time.
	//
	// It changes the layout and nothing else. prepare copies no media at all
	// (SPEC §17.1: it scaffolds, it never moves the library), so the flag is
	// the difference between an empty folder and no folder.
	IncludeAdult bool
}

// Result is what a run did, in the order it did it.
type Result struct {
	// Root is the drive root, as given.
	Root string
	// DataDir is the selected drive-relative application data directory.
	DataDir string
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
	drive, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("prepare: open %s: %w", root, err)
	}
	defer drive.Close()
	prepareLock, err := acquirePrepareLock(drive)
	if err != nil {
		return nil, err
	}
	defer prepareLock.Close()
	if err := validateExistingArtifacts(drive); err != nil {
		return nil, err
	}

	res := &Result{Root: root}
	res.Warnings, res.Notes = advise(root)
	requestedDataDir, requestedStorageRoot := opts.DataDir, opts.StorageRoot
	if existingDataDir, existingStorageRoot, ok, err := existingLocations(drive); err != nil {
		return nil, err
	} else if ok {
		requestedDataDir, err = existingLocation("data directory", requestedDataDir, existingDataDir, DataDir)
		if err != nil {
			return nil, err
		}
		requestedStorageRoot, err = existingLocation("storage root", requestedStorageRoot, existingStorageRoot, StorageRoot)
		if err != nil {
			return nil, err
		}
	}
	dataDir, err := portableRelativePath("data directory", requestedDataDir, DataDir)
	if err != nil {
		return nil, err
	}
	storageRoot, err := portableRelativePath("storage root", requestedStorageRoot, StorageRoot)
	if err != nil {
		return nil, err
	}
	res.DataDir = dataDir

	for _, dir := range layoutDirsFor(dataDir, storageRoot, opts.IncludeAdult) {
		if err := makePortableDir(drive, dir); err != nil {
			return nil, fmt.Errorf("prepare: create %s: %w", dir, err)
		}
	}

	// The config is written once and never again. It carries the drive's
	// identity, its storage root and where its database lives, and a drive that
	// has been used has a database that agrees with it.
	if err := res.write(drive, ConfigFile, []byte(portableConfigYAML(dataDir, storageRoot)), 0o644, false); err != nil {
		return nil, err
	}

	// Launchers and README are derived entirely from this source tree, so
	// refreshing them loses nothing.
	for name, body := range map[string]string{
		MacLauncher:     macLauncher,
		LinuxLauncher:   linuxLauncher,
		WindowsLauncher: windowsLauncher,
	} {
		if err := res.write(drive, name, []byte(body), 0o755, opts.Force); err != nil {
			return nil, err
		}
	}
	if err := res.write(drive, ReadmeFile, []byte(portableReadme(dataDir, storageRoot)), 0o644, opts.Force); err != nil {
		return nil, err
	}
	sort.Strings(res.Created)
	sort.Strings(res.Skipped)

	if err := placeBinaries(res, drive, opts); err != nil {
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
func (r *Result) write(root *os.Root, rel string, body []byte, mode fs.FileMode, overwrite bool) error {
	if !overwrite {
		switch info, err := root.Lstat(filepath.FromSlash(rel)); {
		case err == nil:
			if !info.Mode().IsRegular() {
				return fmt.Errorf("prepare: %s exists and is not a regular file", rel)
			}
			r.Skipped = append(r.Skipped, rel)
			return nil
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("prepare: %s cannot be read: %w", rel, err)
		}
	}
	if err := writeAtomic(root, rel, mode, func(w io.Writer) error {
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
// error, and neither is a reason to fail. Where the bit does matter, a drive
// somebody formatted ext4 or APFS, it is set.
func writeAtomic(root *os.Root, dst string, mode fs.FileMode, fill func(io.Writer) error) error {
	var tmp *os.File
	var name string
	for range 100 {
		random := make([]byte, 8)
		if _, err := rand.Read(random); err != nil {
			return err
		}
		name = path.Join(path.Dir(dst), "."+path.Base(dst)+"."+hex.EncodeToString(random))
		var err error
		tmp, err = root.OpenFile(filepath.FromSlash(name), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if errors.Is(err, fs.ErrExist) {
			continue
		}
		if err != nil {
			return err
		}
		break
	}
	if tmp == nil {
		return errors.New("could not allocate a temporary file")
	}
	defer func() {
		tmp.Close()
		root.Remove(filepath.FromSlash(name))
	}()

	if err := fill(tmp); err != nil {
		return err
	}
	_ = tmp.Chmod(mode)
	if err := tmp.Close(); err != nil {
		return err
	}
	return root.Rename(filepath.FromSlash(name), filepath.FromSlash(dst))
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
		dataDir := r.DataDir
		if dataDir == "" {
			dataDir = DataDir
		}
		fmt.Fprintf(w, "\nSome files were kept as they were. Re-run with -force to refresh the\n"+
			"launchers and binaries; %s and %s/ are never overwritten.\n", ConfigFile, dataDir)
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

// makePortableDir uses an os.Root capability so symlinks and concurrent path
// changes cannot redirect creation outside the selected drive.
func makePortableDir(root *os.Root, rel string) error {
	return root.MkdirAll(filepath.FromSlash(rel), 0o755)
}

func portableRelativePath(name, value, fallback string) (string, error) {
	if value == "" {
		value = fallback
	}
	if path.IsAbs(value) || filepath.IsAbs(value) || filepath.VolumeName(value) != "" ||
		strings.ContainsAny(value, `\:`) {
		return "", fmt.Errorf("prepare: %s %q must be relative to the portable drive", name, value)
	}
	if err := validatePortableComponents(name, value); err != nil {
		return "", err
	}
	clean := path.Clean(value)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("prepare: %s %q escapes the portable drive", name, value)
	}
	for _, reserved := range generatedFiles() {
		if strings.EqualFold(clean, reserved) ||
			strings.HasPrefix(strings.ToLower(clean), strings.ToLower(reserved)+"/") {
			return "", fmt.Errorf("prepare: %s %q collides with generated file %s", name, value, reserved)
		}
	}
	return clean, nil
}

func validatePortableComponents(name, value string) error {
	for _, component := range strings.Split(value, "/") {
		if component == "" || component == "." || component == ".." {
			continue
		}
		if strings.ContainsAny(component, `<>"|?*`) ||
			strings.HasSuffix(component, ".") || strings.HasSuffix(component, " ") {
			return fmt.Errorf("prepare: %s %q is not a portable Windows/exFAT path", name, value)
		}
		for _, r := range component {
			if r < 32 {
				return fmt.Errorf("prepare: %s %q contains a control character", name, value)
			}
		}
		base := strings.ToUpper(strings.SplitN(component, ".", 2)[0])
		portSuffix := ""
		if strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT") {
			portSuffix = base[3:]
		}
		reservedPort := (len(portSuffix) == 1 && portSuffix[0] >= '1' && portSuffix[0] <= '9') ||
			portSuffix == "¹" || portSuffix == "²" || portSuffix == "³"
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || base == "CLOCK$" ||
			base == "CONIN$" || base == "CONOUT$" || reservedPort {
			return fmt.Errorf("prepare: %s %q uses Windows reserved name %s", name, value, component)
		}
	}
	return nil
}

func existingLocation(name, requested, recorded, fallback string) (string, error) {
	recorded, err := portableRelativePath(name, recorded, fallback)
	if err != nil {
		return "", err
	}
	if requested == "" {
		return recorded, nil
	}
	requested, err = portableRelativePath(name, requested, fallback)
	if err != nil {
		return "", err
	}
	if requested != recorded {
		return "", fmt.Errorf("prepare: %s is already %q in %s; migrate the data and edit that config before changing it", name, recorded, ConfigFile)
	}
	return recorded, nil
}

func generatedFiles() []string {
	files := []string{PrepareLockFile, ConfigFile, ReadmeFile, MacLauncher, WindowsLauncher, LinuxLauncher}
	for _, target := range Targets {
		files = append(files, target.RelPath())
	}
	return files
}

func validateExistingArtifacts(root *os.Root) error {
	for _, rel := range generatedFiles() {
		info, err := root.Lstat(filepath.FromSlash(rel))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return fmt.Errorf("prepare: inspect %s: %w", rel, err)
		case !info.Mode().IsRegular():
			return fmt.Errorf("prepare: %s exists and is not a regular file", rel)
		}
	}
	return nil
}

func existingLocations(root *os.Root) (dataDir, storageRoot string, exists bool, err error) {
	body, err := root.ReadFile(filepath.FromSlash(ConfigFile))
	if errors.Is(err, fs.ErrNotExist) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, fmt.Errorf("prepare: read %s: %w", ConfigFile, err)
	}
	var existing struct {
		DataDir       string `yaml:"data_dir"`
		LegacyDataDir string `yaml:"config_dir"`
		StorageRoot   string `yaml:"storage_root"`
	}
	if err := yaml.Unmarshal(body, &existing); err != nil {
		return "", "", false, fmt.Errorf("prepare: parse existing %s: %w", ConfigFile, err)
	}
	dataDir = existing.DataDir
	if dataDir == "" {
		dataDir = existing.LegacyDataDir
	}
	if dataDir == "" {
		return "", "", false, fmt.Errorf("prepare: existing %s must record data_dir (or legacy config_dir) before it can be reused", ConfigFile)
	}
	if existing.StorageRoot == "" {
		return "", "", false, fmt.Errorf("prepare: existing %s must record storage_root before it can be reused", ConfigFile)
	}
	return dataDir, existing.StorageRoot, true, nil
}
