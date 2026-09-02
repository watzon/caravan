// Package yenc encodes and decodes yEnc articles: the codec half of the
// embedded Usenet engine (SPEC §5.1).
//
// yEnc is how binaries travel over a text protocol. Each byte is shifted by
// 42, the four values that would confuse a news server (NUL, CR, LF and '=')
// are escaped with '=', and the result is broken into lines between a
// "=ybegin" header and a "=yend" trailer. A large file is posted as many
// articles, each carrying "=ypart begin=/end=" so the decoder knows where its
// payload belongs in the finished file.
//
// Every article carries a CRC32 of its own payload, and this package treats that
// as the point of the format: a mismatched CRC, a payload that is not the length
// the trailer claims, or an article that stops before "=yend" all return a typed
// error carrying expected and actual values, never a partial payload with a nil
// error. Callers match on ErrCorrupt to hand the segment to par2 instead.
//
// Part.Begin is the 0-based offset of Part.Body in the finished file, so assembly
// is a WriteAt at that offset and never a sequential append. Parts arrive out of
// order from a pool of connections, and buffering them to reorder would cost the
// memory the pipeline is trying not to spend.
//
// The encoder exists so the test corpus, the fake news server and the pipeline's
// fixtures can produce articles a real client would accept; it is not used to
// post. It is conservative about escaping (leading '.', TAB and space are escaped
// too) so its output survives dot-stuffing and whitespace-trimming
// middleboxes.
package yenc

import (
	"errors"
	"fmt"
)

// DefaultLineLength is the encoded line length yEnc posters conventionally
// use. It is advisory: decoders take whatever line lengths they are given.
const DefaultLineLength = 128

// Errors callers act on.
var (
	// ErrCorrupt is the sentinel every integrity failure unwraps to: a CRC
	// mismatch, a short or long payload, or an article that ended before its
	// trailer. It is the signal to treat the segment as a hole and let par2
	// fill it, rather than to retry or to accept what arrived.
	ErrCorrupt = errors.New("yenc: article failed its own integrity check")
	// ErrNotYenc means the article contains no "=ybegin" line at all: it is not
	// a damaged yEnc article, it is not one. A poster's plain-text announcement
	// article looks like this.
	ErrNotYenc = errors.New("yenc: article has no =ybegin header")
	// ErrMalformed means a yEnc control line could not be understood: a
	// missing name, an unreadable size, a multipart article with no =ypart.
	ErrMalformed = errors.New("yenc: malformed yEnc article")
)

// CRCError is a decoded payload whose CRC32 does not match the one the article
// declared. It carries both values because the pair is what makes a bug report
// actionable: a mismatch on every segment is a decoder bug, a mismatch on one
// is a damaged article.
type CRCError struct {
	// Name is the filename from the =ybegin header.
	Name string
	// Part is the 1-based part number, or 0 for a single-part article.
	Part int
	// Whole reports whether the failure is the whole-file CRC (crc32=)
	// rather than this part's CRC (pcrc32=).
	Whole bool
	// Expected is the CRC32 the article declared.
	Expected uint32
	// Actual is the CRC32 of the bytes that were decoded.
	Actual uint32
}

func (e *CRCError) Error() string {
	scope := "part crc32"
	if e.Whole {
		scope = "file crc32"
	}
	return fmt.Sprintf("yenc: %s: %s mismatch: expected %08x, got %08x", e.where(), scope, e.Expected, e.Actual)
}

// Unwrap is ErrCorrupt, the sentinel a pipeline branches on.
func (e *CRCError) Unwrap() error { return ErrCorrupt }

func (e *CRCError) where() string { return describe(e.Name, e.Part) }

// SizeError is a decoded payload that is not the length the article said it
// would be, including the truncated case where the article stopped before its
// "=yend" trailer.
type SizeError struct {
	// Name is the filename from the =ybegin header.
	Name string
	// Part is the 1-based part number, or 0 for a single-part article.
	Part int
	// Expected is the payload length the article declared.
	Expected int64
	// Actual is the number of bytes that were decoded.
	Actual int64
	// Truncated reports whether the article ended without a "=yend" line,
	// which is what a connection dropped mid-article looks like.
	Truncated bool
}

func (e *SizeError) Error() string {
	if e.Truncated {
		return fmt.Sprintf("yenc: %s: article ended before =yend: expected %d bytes, got %d", e.where(), e.Expected, e.Actual)
	}
	return fmt.Sprintf("yenc: %s: size mismatch: expected %d bytes, got %d", e.where(), e.Expected, e.Actual)
}

// Unwrap is ErrCorrupt, the sentinel a pipeline branches on.
func (e *SizeError) Unwrap() error { return ErrCorrupt }

func (e *SizeError) where() string { return describe(e.Name, e.Part) }

func describe(name string, part int) string {
	if name == "" {
		name = "<unnamed>"
	}
	if part > 0 {
		return fmt.Sprintf("%s part %d", name, part)
	}
	return name
}

// CheckFileCRC compares the CRC32 of a fully assembled file against the value
// a final part declared in its "=yend crc32=" field, returning a *CRCError
// when they differ and nil when they match.
//
// Assembly is the only place the whole-file CRC can be checked: a decoder
// that sees one article has only that article's bytes.
func CheckFileCRC(name string, expected, actual uint32) error {
	if expected == actual {
		return nil
	}
	return &CRCError{Name: name, Whole: true, Expected: expected, Actual: actual}
}
