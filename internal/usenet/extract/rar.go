package extract

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/nwaples/rardecode/v2"
)

// extractRAR unpacks a rar set into root.
//
// path is the first volume; rardecode follows the rest of a multi-volume set
// itself, reading it as one continuous stream, which is the only way a file
// split across volumes can come out whole. Unlike zip there is no directory to
// vet up front — a rar is read start to end — so an entry that turns out to be
// unsafe half way through is caught by the caller discarding the staging tree.
func extractRAR(ctx context.Context, path, archive, root string) ([]extracted, error) {
	rc, err := rardecode.OpenReader(path)
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
