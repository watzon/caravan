// Package nzb parses NZB documents: the index half of the embedded Usenet
// engine (SPEC §5.1).
//
// An NZB is a shopping list. It names the files a release is made of and, for
// each one, the articles those files were split into, so a downloader never has
// to search a news server: it fetches message-ids and reassembles. This package
// turns that XML into Files and Segments and refuses anything it cannot fetch
// from later, because a download that starts against a half-read NZB fails
// slowly and confusingly instead of immediately and clearly.
//
// It also knows which files are par2, because that distinction decides the
// pipeline's shape: content files are assembled and extracted, while par2 files
// are the repair budget and are fetched lazily (SPEC §5.1). Downloading every
// recovery volume of a release that needed no repair is the easiest way to
// waste a paid Usenet account.
//
// The parser holds no Caravan types and touches no database: it is bytes in,
// values out, which makes the pipeline above it testable against
// internal/usenet/nntptest with no network anywhere.
//
// NZBs arrive from indexers, so the document is untrusted. Parse caps it at
// MaxDocumentBytes, and encoding/xml resolves no external entities, so there is
// no XXE surface here.
package nzb

import (
	"bufio"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MaxDocumentBytes is the largest NZB Parse will read. Real NZBs are well
// under a megabyte even for a hundred-gigabyte release; this is a guard
// against a hostile indexer, not a limit users reach.
const MaxDocumentBytes = 64 << 20

// docLimit is MaxDocumentBytes. It is a variable so that the boundary can be
// tested without building a 64 MB document, and nothing else changes it.
var docLimit int64 = MaxDocumentBytes

// Errors callers act on.
var (
	// ErrMalformed is the sentinel every parse failure unwraps to. The
	// message carries which file and segment was at fault; the sentinel is
	// what a caller matches on to report "this NZB is unusable" rather than
	// retrying a grab that will never parse.
	ErrMalformed = errors.New("nzb: malformed nzb")
	// ErrTooLarge means the document ran past MaxDocumentBytes.
	ErrTooLarge = errors.New("nzb: document exceeds the maximum size")
	// ErrUnsupportedCharset means the XML declared an encoding this package
	// cannot decode. UTF-8, US-ASCII and ISO-8859-1 are supported, which
	// covers every indexer in practice.
	ErrUnsupportedCharset = errors.New("nzb: unsupported character encoding")
)

// Segment is one article of a file.
//
// Number is the 1-based part number, and it is the only ordering that
// matters: segments arrive out of order and are written at the offset their
// position implies, so a file with a gap in its numbering is malformed rather
// than merely short.
type Segment struct {
	// Number is the 1-based part number within the file.
	Number int
	// Bytes is the encoded (on-the-wire) size the NZB claims for this
	// article, which is larger than the decoded payload. It is a scheduling
	// hint and a disk-space preflight input, never an assembly offset.
	Bytes int64
	// MessageID is the article's message-id without its angle brackets, in
	// the form the NNTP client wants.
	MessageID string
}

// File is one file of a release, split across Segments.
type File struct {
	// Subject is the article subject the poster used. The filename usually
	// lives inside it: see Filename.
	Subject string
	// Poster is the From header of the posting.
	Poster string
	// Posted is the posting time, or the zero time when the NZB omitted it or
	// wrote it in a form this package could not read. A bad date is not worth
	// failing a download over.
	Posted time.Time
	// Groups are the newsgroups the file was posted to. It may be empty:
	// Caravan fetches by message-id, so a group is documentation, not a
	// requirement.
	Groups []string
	// Segments are the file's articles, sorted by Number.
	Segments []Segment
}

// NZB is a parsed NZB document.
type NZB struct {
	// Meta is the <head> metadata, keyed by each entry's type attribute
	// (commonly "name", "category", "password"). Duplicate types keep the
	// last value.
	Meta map[string]string
	// Files are the document's files in document order.
	Files []File
}

// Filename is the file's name, recovered from Subject.
//
// There is no filename field in an NZB: posters put it in the subject, and
// the conventional shape is
//
//	Some.Release-GRP [01/12] - "some.release-grp.part01.rar" yEnc (1/50)
//
// so a quoted run is the name when there is one. Unquoted subjects are
// handled by stripping the yEnc counter and taking the last token that looks
// like a filename. When nothing looks like a name the whole subject is
// returned, because an odd name is more useful downstream than an empty one:
// obfuscated releases are the stuck-import queue's job (SPEC §5.4), not the
// parser's.
func (f File) Filename() string { return Filename(f.Subject) }

// IsPar2 reports whether this file is part of a par2 set.
func (f File) IsPar2() bool { return IsPar2(f.Filename()) }

// Bytes is the sum of the file's segment sizes: the on-the-wire cost of
// fetching it, which is what a disk-space and quota preflight needs.
func (f File) Bytes() int64 {
	var n int64
	for _, s := range f.Segments {
		n += s.Bytes
	}
	return n
}

// TotalBytes is the on-the-wire cost of fetching every file, par2 included.
func (n *NZB) TotalBytes() int64 {
	var total int64
	for _, f := range n.Files {
		total += f.Bytes()
	}
	return total
}

// ContentFiles are the files that are not par2: the ones that get assembled
// and extracted.
func (n *NZB) ContentFiles() []File {
	out := make([]File, 0, len(n.Files))
	for _, f := range n.Files {
		if !f.IsPar2() {
			out = append(out, f)
		}
	}
	return out
}

// Par2Files are the par2 files, in document order: the repair budget, fetched
// only when verification says they are needed.
func (n *NZB) Par2Files() []File {
	var out []File
	for _, f := range n.Files {
		if f.IsPar2() {
			out = append(out, f)
		}
	}
	return out
}

// RecoveryBlocks is the total number of recovery blocks the NZB's par2
// volumes advertise in their names. It is the ceiling on what repair can fix
// and the number a "needs N more blocks" failure is measured against. The par2
// files themselves are authoritative once downloaded.
func (n *NZB) RecoveryBlocks() int {
	var total int
	for _, f := range n.Files {
		if _, count, ok := Par2Volume(f.Filename()); ok {
			total += count
		}
	}
	return total
}

// Parse reads an NZB document.
//
// Every failure unwraps to ErrMalformed (or ErrTooLarge /
// ErrUnsupportedCharset) and names the file and segment at fault. The parser is
// strict about what it will have to act on later, a segment with no message-id,
// a non-positive part number, two segments claiming the same number, a file
// with no segments at all, and lenient about everything else, because an
// unreadable posting date has never broken a download.
func Parse(r io.Reader) (*NZB, error) {
	dec := xml.NewDecoder(&limitReader{r: r, left: docLimit})
	dec.Strict = true
	dec.CharsetReader = charsetReader

	var doc xmlNZB
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, ErrTooLarge) || errors.Is(err, ErrUnsupportedCharset) {
			return nil, err
		}
		return nil, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	if local := doc.XMLName.Local; local != "nzb" {
		return nil, fmt.Errorf("%w: root element is <%s>, not <nzb>", ErrMalformed, local)
	}
	if len(doc.Files) == 0 {
		return nil, fmt.Errorf("%w: document contains no files", ErrMalformed)
	}

	out := &NZB{Files: make([]File, 0, len(doc.Files))}
	if len(doc.Head) > 0 {
		out.Meta = make(map[string]string, len(doc.Head))
		for _, m := range doc.Head {
			if t := strings.TrimSpace(m.Type); t != "" {
				out.Meta[t] = strings.TrimSpace(m.Value)
			}
		}
	}

	for i, xf := range doc.Files {
		f, err := convertFile(i, xf)
		if err != nil {
			return nil, err
		}
		out.Files = append(out.Files, f)
	}
	return out, nil
}

func convertFile(idx int, xf xmlFile) (File, error) {
	f := File{
		Subject: strings.TrimSpace(xf.Subject),
		Poster:  strings.TrimSpace(xf.Poster),
		Posted:  parseDate(xf.Date),
	}
	for _, g := range xf.Groups {
		if g = strings.TrimSpace(g); g != "" {
			f.Groups = append(f.Groups, g)
		}
	}

	// The subject is what every later error message identifies this file by,
	// so a file without one cannot be reported on usefully.
	if f.Subject == "" {
		return File{}, fmt.Errorf("%w: file %d has an empty subject", ErrMalformed, idx+1)
	}
	if len(xf.Segments) == 0 {
		return File{}, fmt.Errorf("%w: file %d (%q) has no segments", ErrMalformed, idx+1, f.Filename())
	}

	seen := make(map[int]struct{}, len(xf.Segments))
	f.Segments = make([]Segment, 0, len(xf.Segments))
	for j, xs := range xf.Segments {
		s, err := convertSegment(idx, j, f.Filename(), xs)
		if err != nil {
			return File{}, err
		}
		if _, dup := seen[s.Number]; dup {
			return File{}, fmt.Errorf("%w: file %d (%q): duplicate segment number %d",
				ErrMalformed, idx+1, f.Filename(), s.Number)
		}
		seen[s.Number] = struct{}{}
		f.Segments = append(f.Segments, s)
	}
	sort.Slice(f.Segments, func(a, b int) bool { return f.Segments[a].Number < f.Segments[b].Number })
	return f, nil
}

func convertSegment(fileIdx, segIdx int, name string, xs xmlSegment) (Segment, error) {
	fail := func(format string, args ...any) (Segment, error) {
		return Segment{}, fmt.Errorf("%w: file %d (%q): segment %d: "+format,
			append([]any{ErrMalformed, fileIdx + 1, name, segIdx + 1}, args...)...)
	}

	id := strings.TrimSpace(xs.MessageID)
	// Some indexers write the brackets the NNTP wire format uses; the client
	// adds its own, so they are stripped here rather than doubled there.
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	if id == "" {
		return fail("empty message-id")
	}
	if strings.ContainsAny(id, "<>\r\n") || strings.IndexFunc(id, func(r rune) bool { return r < ' ' }) >= 0 {
		return fail("message-id %q contains an illegal character", id)
	}

	numText := strings.TrimSpace(xs.Number)
	if numText == "" {
		return fail("missing number attribute")
	}
	number, err := strconv.Atoi(numText)
	if err != nil {
		return fail("number %q is not a number", numText)
	}
	if number < 1 {
		return fail("number %d is not positive", number)
	}

	// bytes is advisory (it is the encoded size, not the payload size), so an
	// absent one costs nothing; a nonsensical one would corrupt a preflight.
	var size int64
	if b := strings.TrimSpace(xs.Bytes); b != "" {
		size, err = strconv.ParseInt(b, 10, 64)
		if err != nil {
			return fail("bytes %q is not a number", b)
		}
		if size < 0 {
			return fail("bytes %d is negative", size)
		}
	}

	return Segment{Number: number, Bytes: size, MessageID: id}, nil
}

// parseDate reads the unix-seconds date attribute, returning the zero time for
// anything it cannot read.
func parseDate(s string) time.Time {
	s = strings.TrimSpace(s)
	if s == "" {
		return time.Time{}
	}
	secs, err := strconv.ParseInt(s, 10, 64)
	if err != nil || secs <= 0 {
		return time.Time{}
	}
	return time.Unix(secs, 0).UTC()
}

// XML shapes. Numeric attributes are read as strings so that a bad value
// produces this package's error message rather than encoding/xml's.
type xmlNZB struct {
	XMLName xml.Name  `xml:"nzb"`
	Head    []xmlMeta `xml:"head>meta"`
	Files   []xmlFile `xml:"file"`
}

type xmlMeta struct {
	Type  string `xml:"type,attr"`
	Value string `xml:",chardata"`
}

type xmlFile struct {
	Poster   string       `xml:"poster,attr"`
	Date     string       `xml:"date,attr"`
	Subject  string       `xml:"subject,attr"`
	Groups   []string     `xml:"groups>group"`
	Segments []xmlSegment `xml:"segments>segment"`
}

type xmlSegment struct {
	Bytes     string `xml:"bytes,attr"`
	Number    string `xml:"number,attr"`
	MessageID string `xml:",chardata"`
}

// limitReader fails with ErrTooLarge rather than EOF, so an oversized document
// is reported as oversized instead of as truncated XML.
type limitReader struct {
	r    io.Reader
	left int64
}

func (l *limitReader) Read(p []byte) (int, error) {
	if l.left < 0 {
		return 0, ErrTooLarge
	}
	// Read at most one byte past the budget. That one byte is what makes the
	// overshoot detectable on the Read that causes it: a document exactly at
	// the limit must parse, and a parser that finishes on the last byte will
	// never make another Read for a later check to fire on.
	if int64(len(p)) > l.left+1 {
		p = p[:l.left+1]
	}
	n, err := l.r.Read(p)
	l.left -= int64(n)
	if l.left < 0 {
		return 0, ErrTooLarge
	}
	return n, err
}

// charsetReader decodes the handful of encodings indexers actually emit.
// Anything else is rejected by name rather than silently mangled.
func charsetReader(charset string, input io.Reader) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(charset)) {
	case "", "utf-8", "utf8", "us-ascii", "ascii":
		return input, nil
	case "iso-8859-1", "iso8859-1", "latin1", "latin-1", "windows-1252", "cp1252":
		// Not a faithful windows-1252 decode (0x80: 0x9F differ), but those
		// bytes do not appear in release names, and mapping them to their
		// latin-1 equivalents beats failing the download.
		return &latin1Reader{r: bufio.NewReader(input)}, nil
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedCharset, charset)
	}
}

// latin1Reader widens ISO-8859-1 bytes to UTF-8. Every input byte becomes one
// or two output bytes, so the second half of a widened rune is held in pend
// when the caller's buffer fills between them.
type latin1Reader struct {
	r    *bufio.Reader
	pend byte
	has  bool
}

func (l *latin1Reader) Read(p []byte) (int, error) {
	var n int
	for n < len(p) {
		if l.has {
			p[n] = l.pend
			l.has = false
			n++
			continue
		}
		// Block for the first byte only; after that take what is already
		// buffered and return, so this reader is no less streaming than the
		// one it wraps.
		if n > 0 && l.r.Buffered() == 0 {
			break
		}
		b, err := l.r.ReadByte()
		if err != nil {
			if n > 0 && errors.Is(err, io.EOF) {
				return n, nil
			}
			return n, err
		}
		if b < 0x80 {
			p[n] = b
			n++
			continue
		}
		p[n] = 0xC0 | b>>6
		n++
		l.pend = 0x80 | b&0x3F
		l.has = true
	}
	return n, nil
}
