// Package extract unpacks a completed Usenet download in place: it finds the
// rar or zip set the release was posted as, writes the files out beside the
// archives, and then sweeps the archives and par2 files away so nothing but
// content reaches the import stage (SPEC §5.1, PLAN phase 7 task 5).
//
// It runs after par2 verify/repair, on a directory that is believed to be
// complete and correct. Everything it looks at is top level: releases are
// posted flat, and an archive nested in a subdirectory is left alone rather
// than guessed at.
//
// # Fail loudly
//
// Extraction is all-or-nothing. Entries are written into a staging directory
// inside the download directory and only moved into place once every one of
// them has been written and checked, so an archive that turns out to be
// truncated, corrupt, or encrypted leaves the directory exactly as it was
// found and returns a typed error. Nothing is ever half-unpacked and then
// imported: a release that fails here should fail visibly in the queue, not
// quietly produce a 40%-complete video file.
//
// The cleanup is the other half of that rule and runs in the other order: the
// archive volumes and par2 files are deleted only after a verified extract.
// If extraction fails the archives are still there, which is what a retry —
// or a human — needs.
//
// # Hostile input
//
// An archive from a public news server is attacker-controlled. Entry names are
// normalised and any that would escape the download directory (absolute paths,
// "..", drive letters) are rejected outright rather than clamped, and so are
// symlinks and other non-regular entries. Rejecting the archive rather than
// skipping the entry is deliberate: an archive that tries this is not one to
// extract the rest of.
//
// # Obfuscated names
//
// Files come out under whatever name the archive gives them, however useless.
// De-obfuscation is the release parser's job and the stuck-import queue is the
// designed fallback (PLAN phase 7 task 5); this package does not rename, sort,
// or second-guess anything it unpacks.
package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// Errors callers act on. Every error this package returns is an [*Error]
// wrapping one of these or an underlying I/O or decoder failure.
var (
	// ErrUnsafePath means an archive entry named a path that would be written
	// outside the download directory, or an entry that is not a plain file or
	// directory. The archive is rejected whole.
	ErrUnsafePath = errors.New("extract: archive entry escapes the download directory")

	// ErrEncrypted means the archive, or a file inside it, is password
	// protected. There is no password to try, so this is terminal: no output
	// is produced and the archives are left in place.
	ErrEncrypted = errors.New("extract: archive is encrypted")

	// ErrIncomplete means a multi-volume set is missing volumes — a gap in
	// the .partNN.rar numbering, or a .r00 with no .rar to start it. Better
	// to say which volume is missing than to extract a prefix of the release.
	ErrIncomplete = errors.New("extract: archive set is missing volumes")

	// ErrExists means an extracted entry collides with a file already in the
	// download directory. Overwriting could destroy the very file the release
	// was posted for, so the extract is abandoned instead.
	ErrExists = errors.New("extract: extracted entry already exists in the download directory")

	// ErrSizeMismatch means an entry decoded to a different length than its
	// header declared. The decoders check their own CRCs; this catches the
	// remaining case of a stream that simply stopped early.
	ErrSizeMismatch = errors.New("extract: extracted entry does not match its declared size")
)

// Error is the typed extraction failure. It names the archive volume and,
// where the failure belongs to one entry, the entry, so the queue can show
// "movie.part03.rar: sample.mkv: ..." without anyone parsing a string.
type Error struct {
	// Archive is the archive the failure belongs to, relative to the
	// download directory. Empty for failures that are not about one archive.
	Archive string
	// Entry is the name inside the archive, as the archive gave it. Empty
	// when the failure is not entry-specific.
	Entry string
	// Err is the cause: one of this package's sentinels, or the underlying
	// decoder or filesystem error.
	Err error
}

func (e *Error) Error() string {
	switch {
	case e.Archive == "" && e.Entry == "":
		return e.Err.Error()
	case e.Entry == "":
		return fmt.Sprintf("%s: %v", e.Archive, e.Err)
	case e.Archive == "":
		return fmt.Sprintf("%s: %v", e.Entry, e.Err)
	default:
		return fmt.Sprintf("%s: %s: %v", e.Archive, e.Entry, e.Err)
	}
}

func (e *Error) Unwrap() error { return e.Err }

// Result is what an [Extract] call did. A Result with no Archives means the
// directory held nothing to extract and nothing was touched.
type Result struct {
	// Archives are the archive volumes that were read, relative to the
	// download directory, in reading order.
	Archives []string
	// Files are the files that were written, relative to the download
	// directory with '/' separators. Directories created along the way are
	// not listed.
	Files []string
	// Bytes is the total size of Files.
	Bytes int64
	// Removed are the files deleted after the verified extract: the archive
	// volumes plus every par2 file that was in the directory.
	Removed []string
}

// stagingPrefix names the temporary directory extraction writes into. It lives
// inside the download directory so moving finished files into place is a
// same-filesystem rename rather than a copy of a 40GB video.
const stagingPrefix = ".caravan-extract-"

// Extract unpacks every archive set in dir and cleans up after itself.
//
// A directory with no archives is a no-op: it returns an empty Result and
// removes nothing, since the par2 sweep is the tail of a successful extract
// and not a thing to do to a directory that was never packed.
//
// On any extraction failure dir is left exactly as it was found and the error
// is an [*Error]. The one exception is a failure during the final cleanup,
// where the files are already extracted and in place: that returns both a
// non-nil Result describing what landed and a non-nil error, so a caller can
// tell "nothing happened" from "it worked but the debris is still there".
func Extract(ctx context.Context, dir string) (*Result, error) {
	sets, err := Detect(dir)
	if err != nil {
		return nil, err
	}
	res := &Result{}
	if len(sets) == 0 {
		return res, nil
	}

	staging, err := os.MkdirTemp(dir, stagingPrefix+"*")
	if err != nil {
		return nil, &Error{Err: fmt.Errorf("extract: staging directory: %w", err)}
	}
	moved := false
	defer func() {
		if !moved {
			os.RemoveAll(staging)
		}
	}()

	for _, s := range sets {
		// Only the first volume is opened; the decoders follow the rest of a
		// multi-volume set themselves.
		archive := s.Volumes[0]

		var files []extracted
		switch s.Kind {
		case KindZip:
			files, err = extractZip(ctx, filepath.Join(dir, archive), archive, staging)
		case KindRAR:
			files, err = extractRAR(ctx, archive, staging, resolveVolumes(dir, s.Volumes))
		default:
			err = &Error{Archive: archive, Err: fmt.Errorf("extract: unknown archive kind %d", s.Kind)}
		}
		if err != nil {
			return nil, err
		}

		res.Archives = append(res.Archives, s.Volumes...)
		for _, f := range files {
			res.Files = append(res.Files, f.name)
			res.Bytes += f.size
		}
	}

	if err := moveIntoPlace(staging, dir); err != nil {
		return nil, err
	}
	moved = true
	if err := os.Remove(staging); err != nil {
		return res, &Error{Err: fmt.Errorf("extract: staging directory: %w", err)}
	}

	res.Removed, err = cleanup(dir, res.Archives)
	if err != nil {
		return res, err
	}
	return res, nil
}

// extracted is one file written during a single archive's extraction.
type extracted struct {
	name string
	size int64
}

// moveIntoPlace promotes everything staged into the download directory.
//
// Destinations are all checked before anything moves, so the ordinary
// collision case fails with nothing half-promoted. A rename that then fails
// anyway is a filesystem-level problem, and the error says which entry.
func moveIntoPlace(staging, dir string) error {
	entries, err := os.ReadDir(staging)
	if err != nil {
		return &Error{Err: fmt.Errorf("extract: staging directory: %w", err)}
	}
	for _, e := range entries {
		dst := filepath.Join(dir, e.Name())
		switch _, err := os.Lstat(dst); {
		case err == nil:
			return &Error{Entry: e.Name(), Err: ErrExists}
		case !errors.Is(err, fs.ErrNotExist):
			return &Error{Entry: e.Name(), Err: err}
		}
	}
	for _, e := range entries {
		if err := os.Rename(filepath.Join(staging, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			return &Error{Entry: e.Name(), Err: err}
		}
	}
	return nil
}

// cleanup deletes the archive volumes and every par2 file in dir. It runs only
// after a verified extract; the par2 files go with the archives because their
// only job — proving the archives arrived intact — is finished, and neither is
// something the library should ever see.
func cleanup(dir string, archives []string) ([]string, error) {
	var removed []string
	seen := make(map[string]struct{}, len(archives))
	for _, name := range archives {
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return removed, &Error{Archive: name, Err: fmt.Errorf("extract: cleanup: %w", err)}
		}
		removed = append(removed, name)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return removed, &Error{Err: fmt.Errorf("extract: cleanup: %w", err)}
	}
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".par2") {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
			return removed, &Error{Archive: e.Name(), Err: fmt.Errorf("extract: cleanup: %w", err)}
		}
		removed = append(removed, e.Name())
	}
	return removed, nil
}

// safeEntryName normalises a name from an archive and rejects anything that
// would escape the target directory. This is the twin of par2's safeName and
// exists for the same reason: the name comes off a public news server, so
// "../../.ssh/authorized_keys" has to be a hard error rather than something
// the extract writes.
func safeEntryName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty name", ErrUnsafePath)
	}
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: %q contains a NUL", ErrUnsafePath, name)
	}
	// Zip uses '/' and rar uses '\', and rardecode already rewrites its own.
	// Treating both as separators everywhere means one archive cannot mean
	// two different things on two different platforms.
	clean := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(clean, "/") || filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" {
		return "", fmt.Errorf("%w: %q is absolute", ErrUnsafePath, name)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", fmt.Errorf("%w: %q escapes the download directory", ErrUnsafePath, name)
		}
	}
	// Normalise so the name a Result reports is the name on disk: "subs/" and
	// "a/./b.mkv" would otherwise disagree with where the file actually went.
	// Cleaning after the ".." scan, never before, so nothing can be cleaned
	// into looking harmless.
	clean = path.Clean(clean)
	if clean == "." || clean == "/" {
		return "", fmt.Errorf("%w: %q names no file", ErrUnsafePath, name)
	}
	return clean, nil
}

// resolve turns an archive entry name into a path under root, refusing to
// return anything outside it. safeEntryName should already have made that
// impossible; this is the second lock on the same door.
func resolve(root, name string) (string, error) {
	clean, err := safeEntryName(name)
	if err != nil {
		return "", err
	}
	target := filepath.Join(root, filepath.FromSlash(clean))
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: %q", ErrUnsafePath, name)
	}
	return target, nil
}

// writeFile writes one entry into the staging tree and returns its length.
// O_EXCL makes a duplicated entry name an error rather than a silent overwrite.
func writeFile(ctx context.Context, root, name string, src io.Reader) (int64, error) {
	target, err := resolve(root, name)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(&ctxWriter{ctx: ctx, w: f}, src)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return n, err
}

// mkdirEntry creates a directory an archive declares explicitly.
func mkdirEntry(root, name string) error {
	target, err := resolve(root, name)
	if err != nil {
		return err
	}
	return os.MkdirAll(target, 0o755)
}

// ctxWriter aborts a copy when the context is done. Unpacking a large release
// is minutes of work, so cancellation has to bite somewhere inside a single
// file rather than only between files.
type ctxWriter struct {
	ctx context.Context
	w   io.Writer
}

func (c *ctxWriter) Write(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.w.Write(p)
}

// checkRegular rejects entries that are neither a plain file nor a directory:
// symlinks especially, which are a way to write outside the directory that no
// amount of name checking catches.
func checkRegular(mode fs.FileMode) error {
	if mode&fs.ModeType == 0 || mode.IsDir() {
		return nil
	}
	return fmt.Errorf("%w: not a regular file (%s)", ErrUnsafePath, mode.Type())
}
