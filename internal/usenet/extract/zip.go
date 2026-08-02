package extract

import (
	"archive/zip"
	"context"
	"fmt"
)

// zipEncrypted is the general-purpose bit flag zip sets when an entry's data
// is encrypted. archive/zip will happily hand back the ciphertext, so the flag
// has to be checked here rather than waiting for a decode error that never
// comes.
const zipEncrypted = 0x1

// extractZip unpacks a zip into root. A zip's central directory is available
// before any data is read, so every entry is vetted first and a bad archive
// produces no output at all rather than relying on the staging directory to
// undo it.
func extractZip(ctx context.Context, path, archive, root string) ([]extracted, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, &Error{Archive: archive, Err: err}
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.Flags&zipEncrypted != 0 {
			return nil, &Error{Archive: archive, Entry: f.Name, Err: ErrEncrypted}
		}
		if _, err := safeEntryName(f.Name); err != nil {
			return nil, &Error{Archive: archive, Entry: f.Name, Err: err}
		}
		if err := checkRegular(f.Mode()); err != nil {
			return nil, &Error{Archive: archive, Entry: f.Name, Err: err}
		}
	}

	var out []extracted
	for _, f := range zr.File {
		if err := ctx.Err(); err != nil {
			return nil, &Error{Archive: archive, Err: err}
		}
		name, err := safeEntryName(f.Name)
		if err != nil {
			return nil, &Error{Archive: archive, Entry: f.Name, Err: err}
		}
		if f.Mode().IsDir() {
			if err := mkdirEntry(root, name); err != nil {
				return nil, &Error{Archive: archive, Entry: f.Name, Err: err}
			}
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return nil, &Error{Archive: archive, Entry: f.Name, Err: err}
		}
		// io.Copy through the entry reader is what checks the CRC32: the
		// reader archive/zip returns fails at EOF when the sum disagrees.
		n, err := writeFile(ctx, root, name, rc)
		rc.Close()
		if err != nil {
			return nil, &Error{Archive: archive, Entry: f.Name, Err: err}
		}
		if uint64(n) != f.UncompressedSize64 {
			return nil, &Error{Archive: archive, Entry: f.Name, Err: fmt.Errorf(
				"%w: wrote %d bytes, header declares %d", ErrSizeMismatch, n, f.UncompressedSize64)}
		}
		out = append(out, extracted{name: name, size: n})
	}
	return out, nil
}
