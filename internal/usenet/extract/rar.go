package extract

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"

	"github.com/nwaples/rardecode/v2"
)

// volumeFS opens rar volumes for rardecode, resolving the successor names it
// infers onto the files [Detect] actually found. Posters pad volume numbers
// inconsistently — part01…part09, then part010 — so the inferred successor
// ("part10.rar") may not exist under that exact spelling. The part number is
// the volume's identity; the width is an accident of the packer.
type volumeFS struct {
	byNum map[int]string // part number -> absolute path of the on-disk volume
}

func newVolumeFS(dir string, volumes []string) volumeFS {
	byNum := make(map[int]string, len(volumes))
	for _, v := range volumes {
		m := partRE.FindStringSubmatch(v)
		if m == nil {
			continue
		}
		if n, err := strconv.Atoi(m[2]); err == nil {
			byNum[n] = filepath.Join(dir, v)
		}
	}
	return volumeFS{byNum: byNum}
}

func (v volumeFS) Open(name string) (fs.File, error) {
	f, err := os.Open(name)
	if !errors.Is(err, fs.ErrNotExist) {
		return f, err
	}
	if m := partRE.FindStringSubmatch(filepath.Base(name)); m != nil {
		if n, aerr := strconv.Atoi(m[2]); aerr == nil {
			if real, ok := v.byNum[n]; ok {
				return os.Open(real)
			}
		}
	}
	// The original not-exist error, so rardecode's own old-naming fallback
	// still runs.
	return nil, err
}

// extractRAR unpacks a rar set into root.
//
// path is the first volume; rardecode follows the rest of a multi-volume set
// itself, reading it as one continuous stream, which is the only way a file
// split across volumes can come out whole. Unlike zip there is no directory to
// vet up front — a rar is read start to end — so an entry that turns out to be
// unsafe half way through is caught by the caller discarding the staging tree.
func extractRAR(ctx context.Context, path, archive, root string, vfs fs.FS) ([]extracted, error) {
	rc, err := rardecode.OpenReader(path, rardecode.FileSystem(vfs))
	if err != nil {
		return nil, rarError(archive, "", err)
	}
	defer rc.Close()

	var out []extracted
	for {
		if err := ctx.Err(); err != nil {
			return nil, &Error{Archive: archive, Err: err}
		}

		h, err := rc.Next()
		if errors.Is(err, io.EOF) {
			return out, nil
		}
		if err != nil {
			return nil, rarError(archive, "", err)
		}
		if h.Encrypted || h.HeaderEncrypted {
			return nil, &Error{Archive: archive, Entry: h.Name, Err: ErrEncrypted}
		}
		name, err := safeEntryName(h.Name)
		if err != nil {
			return nil, &Error{Archive: archive, Entry: h.Name, Err: err}
		}
		if h.IsDir {
			if err := mkdirEntry(root, name); err != nil {
				return nil, &Error{Archive: archive, Entry: h.Name, Err: err}
			}
			continue
		}
		if err := checkRegular(h.Mode()); err != nil {
			return nil, &Error{Archive: archive, Entry: h.Name, Err: err}
		}

		// Reading the entry to EOF is what checks its CRC32: rardecode
		// compares the sum in the file header at the end of the stream.
		n, err := writeFile(ctx, root, name, rc)
		if err != nil {
			return nil, rarError(archive, h.Name, err)
		}
		if !h.UnKnownSize && n != h.UnPackedSize {
			return nil, &Error{Archive: archive, Entry: h.Name, Err: fmt.Errorf(
				"%w: wrote %d bytes, header declares %d", ErrSizeMismatch, n, h.UnPackedSize)}
		}
		out = append(out, extracted{name: name, size: n})
	}
}

// rarError maps rardecode's two "needs a password" failures onto ErrEncrypted
// so callers have one thing to match, and passes everything else through.
func rarError(archive, entry string, err error) *Error {
	if errors.Is(err, rardecode.ErrArchiveEncrypted) || errors.Is(err, rardecode.ErrArchivedFileEncrypted) {
		return &Error{Archive: archive, Entry: entry, Err: fmt.Errorf("%w: %v", ErrEncrypted, err)}
	}
	return &Error{Archive: archive, Entry: entry, Err: err}
}
