package yenc

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"strconv"
	"strings"
)

// maxControlLine caps a "=y" control line. Real ones are well under 200
// bytes; the cap stops a corrupt article from being read into memory as one
// enormous "line".
const maxControlLine = 8 << 10

// maxPreallocBytes caps how much the decoder trusts the header's size= field
// to preallocate. A hostile article claiming a terabyte gets a normal growing
// buffer instead of an instant allocation failure.
const maxPreallocBytes = 4 << 20

// Part is one decoded yEnc article.
type Part struct {
	// Name is the filename from the =ybegin header.
	Name string
	// Number is the 1-based part number, or 0 for a single-part article.
	Number int
	// Total is the number of parts the header claimed, or 0 when it did not
	// say. Posters are inconsistent about this field, so the NZB's segment
	// count is the authority, not this.
	Total int
	// Begin is the 0-based offset of Body within the finished file: the
	// offset assembly writes at. It is 0 for a single-part article.
	Begin int64
	// End is the exclusive end offset of Body within the finished file, so
	// End-Begin is always len(Body).
	End int64
	// Size is the whole file's size from the =ybegin header, which is the
	// number to preallocate the output file to. It is 0 when the header
	// omitted it.
	Size int64
	// Body is the decoded payload.
	Body []byte
	// CRC32 is the CRC32 that was verified: the part CRC for a multipart
	// article, the file CRC for a single-part one. It is 0 when the article
	// declared none (Verified is false).
	CRC32 uint32
	// Verified reports whether a CRC was present and matched. A false here
	// is not a failure: some posters omit pcrc32, and rejecting their
	// articles would fail downloads that par2 could not help with either.
	Verified bool
	// FileCRC32 is the whole-file CRC32 the trailer declared, valid only
	// when HasFileCRC is set. Multipart articles carry it on the final part;
	// assembly checks it with CheckFileCRC.
	FileCRC32 uint32
	// HasFileCRC reports whether FileCRC32 was present.
	HasFileCRC bool
	// Multipart reports whether this article is one part of a larger file.
	Multipart bool
}

// DecodeBytes decodes a single yEnc article body.
func DecodeBytes(article []byte) (*Part, error) {
	return Decode(bytes.NewReader(article))
}

// Decode reads one yEnc article body, what NNTP's BODY command returns, with
// dot-stuffing already undone, and returns its decoded payload.
//
// Lines before "=ybegin" are skipped: posters put announcements above the
// header, and a decoder that insisted on the first line would reject real
// articles. Everything after "=yend" is ignored for the same reason.
//
// Decode never returns a payload alongside an error. A CRC mismatch, a payload
// that is not the declared length, or an article that ends before "=yend"
// returns a *CRCError or *SizeError, both unwrapping to ErrCorrupt, with
// expected and actual values, because a segment that is silently wrong is worse
// than one that is loudly missing.
func Decode(r io.Reader) (*Part, error) {
	// 16 KB is comfortably more than the longest control line, so line reads
	// are single-shot, and small enough that decoding a few hundred articles
	// a second does not churn megabytes of buffer.
	br := bufio.NewReaderSize(r, 16<<10)

	header, err := findHeader(br)
	if err != nil {
		return nil, err
	}

	part := &Part{
		Name:  header.str("name"),
		Total: header.int("total"),
		Size:  header.int64("size"),
	}
	if part.Name == "" {
		return nil, fmt.Errorf("%w: =ybegin has no name", ErrMalformed)
	}
	// part= decides how the whole article is read, so an unreadable one is a
	// malformed article rather than a defaulted field; total= and size= are
	// advisory and are allowed to be nonsense.
	if raw, present := header["part"]; present {
		n, ok := header.lookupInt64("part")
		if !ok || n < 0 {
			return nil, fmt.Errorf("%w: =ybegin part=%q is not a part number", ErrMalformed, raw)
		}
		part.Number = int(n)
	}
	part.Multipart = part.Number > 0

	// A multipart article says where its payload sits in the file. The one
	// exception real posters make is a single-part-of-one posting, which is
	// just the whole file.
	declared := int64(-1)
	if part.Multipart {
		peek, _ := br.Peek(len("=ypart"))
		switch {
		case string(peek) == "=ypart":
			line, err := readControlLine(br)
			if err != nil {
				return nil, err
			}
			kw := parseKeywords(strings.TrimPrefix(line, "=ypart"))
			begin, end := kw.int64("begin"), kw.int64("end")
			if begin < 1 || end < begin-1 {
				return nil, fmt.Errorf("%w: =ypart begin=%d end=%d is not a range", ErrMalformed, begin, end)
			}
			// yEnc offsets are 1-based and inclusive; Go's are 0-based and
			// exclusive, and end-begin+1 == length in both spellings.
			part.Begin, part.End = begin-1, end
			declared = part.End - part.Begin
		case part.Number == 1:
			// part=1 with no =ypart: the payload starts at the beginning.
			part.Begin = 0
		default:
			return nil, fmt.Errorf("%w: part %d has no =ypart header", ErrMalformed, part.Number)
		}
	}

	// Size the output buffer from this part's own length when =ypart gave
	// one; the header's size= is the whole file, which for a multipart
	// article would over-allocate by the part count.
	hint := declared
	if hint < 0 {
		hint = part.Size
	}
	body, trailer, err := decodeBody(br, hint)
	if err != nil {
		return nil, err
	}
	if trailer == nil {
		expected := declared
		if expected < 0 {
			expected = part.Size
		}
		return nil, &SizeError{
			Name: part.Name, Part: part.Number,
			Expected: expected, Actual: int64(len(body)), Truncated: true,
		}
	}

	part.Body = body
	if !part.Multipart {
		part.End = int64(len(body))
	} else if declared < 0 {
		part.End = part.Begin + int64(len(body))
	}

	if err := verify(part, declared, trailer); err != nil {
		return nil, err
	}
	return part, nil
}

// verify checks the payload against the trailer. It is the whole point of the
// format: everything above this decodes, this is what refuses to lie.
func verify(part *Part, declared int64, trailer keywords) error {
	actual := int64(len(part.Body))

	// A trailer that names a different part than the header is an article
	// spliced from two postings; assembling it would put the right bytes at
	// the wrong offset.
	if tp, ok := trailer.lookupInt64("part"); ok && part.Multipart && int(tp) != part.Number {
		return fmt.Errorf("%w: =yend part=%d does not match =ybegin part=%d", ErrMalformed, tp, part.Number)
	}

	// A part has to sit inside the file its own header describes. pcrc32
	// covers the payload and nothing else, so a damaged begin= survives every
	// other check the article carries: the bytes are right, the offset is not,
	// and assembly would drop them terabytes away from where they belong. This
	// is the same refusal as the =yend part= check above, spelled in offsets.
	if part.Multipart && part.Size > 0 && (part.Begin >= part.Size || part.End > part.Size) {
		return fmt.Errorf("%w: =ypart begin=%d end=%d lies outside the %d-byte file =ybegin declares",
			ErrMalformed, part.Begin+1, part.End, part.Size)
	}

	// The =ypart range and the payload must agree, or assembly would write the
	// wrong bytes at the right offset: the worst possible outcome.
	if declared >= 0 && declared != actual {
		return &SizeError{Name: part.Name, Part: part.Number, Expected: declared, Actual: actual}
	}
	switch size, state := trailer.int64Field("size"); state {
	case fieldMalformed:
		return fmt.Errorf("%w: =yend size=%q is not a length", ErrMalformed, trailer["size"])
	case fieldPresent:
		if size != actual {
			return &SizeError{Name: part.Name, Part: part.Number, Expected: size, Actual: actual}
		}
	}
	// A single-part article's =ybegin size is the payload length too.
	if !part.Multipart && part.Size > 0 && part.Size != actual {
		return &SizeError{Name: part.Name, Part: part.Number, Expected: part.Size, Actual: actual}
	}

	switch crc, state := trailer.crcField("crc32"); state {
	case fieldMalformed:
		return fmt.Errorf("%w: =yend crc32=%q is not a checksum", ErrMalformed, trailer["crc32"])
	case fieldPresent:
		part.FileCRC32, part.HasFileCRC = crc, true
	}

	sum := crc32.ChecksumIEEE(part.Body)
	// Multipart articles carry pcrc32 for their own payload; a single-part
	// article's crc32 covers the same bytes, since the part is the file.
	pcrc, pcrcState := trailer.crcField("pcrc32")
	if pcrcState == fieldMalformed {
		return fmt.Errorf("%w: =yend pcrc32=%q is not a checksum", ErrMalformed, trailer["pcrc32"])
	}
	if pcrcState == fieldPresent {
		if pcrc != sum {
			return &CRCError{Name: part.Name, Part: part.Number, Expected: pcrc, Actual: sum}
		}
		part.CRC32, part.Verified = sum, true
		return nil
	}
	if !part.Multipart && part.HasFileCRC {
		if part.FileCRC32 != sum {
			return &CRCError{Name: part.Name, Part: part.Number, Whole: true, Expected: part.FileCRC32, Actual: sum}
		}
		part.CRC32, part.Verified = sum, true
	}
	return nil
}

// findHeader skips anything before "=ybegin" and parses it.
func findHeader(br *bufio.Reader) (keywords, error) {
	for {
		line, err := readControlLine(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil, ErrNotYenc
			}
			return nil, err
		}
		if strings.HasPrefix(line, "=ybegin") {
			return parseKeywords(strings.TrimPrefix(line, "=ybegin")), nil
		}
	}
}

// decodeBody decodes payload lines until "=yend". A nil trailer with a nil
// error means the article ran out before its trailer.
func decodeBody(br *bufio.Reader, size int64) ([]byte, keywords, error) {
	capacity := size
	if capacity < 0 || capacity > maxPreallocBytes {
		capacity = maxPreallocBytes
	}
	out := make([]byte, 0, capacity)

	var escaped bool
	atLineStart := true
	for {
		if atLineStart {
			// "=y" at the start of a line is a control line. This is the
			// format's one genuine ambiguity, an escape happens to be able to
			// spell it, but no encoder escapes the byte that would, and every
			// decoder resolves it this way.
			if p, err := br.Peek(2); err == nil && p[0] == '=' && p[1] == 'y' {
				line, err := readControlLine(br)
				if err != nil {
					return out, nil, err
				}
				if strings.HasPrefix(line, "=yend") {
					return out, parseKeywords(strings.TrimPrefix(line, "=yend")), nil
				}
				// A stray "=ybegin"/"=ypart" mid-payload is a malformed
				// article, not payload to keep decoding.
				return nil, nil, fmt.Errorf("%w: unexpected %q inside the payload", ErrMalformed, firstWord(line))
			}
		}

		b, err := br.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return out, nil, nil
			}
			return nil, nil, err
		}

		switch {
		case b == '\r':
			// Line separators are structure, not data: every data byte that
			// could look like one is escaped.
			continue
		case b == '\n':
			atLineStart = true
			continue
		case escaped:
			out = append(out, b-64-42)
			escaped = false
		case b == '=':
			escaped = true
		default:
			out = append(out, b-42)
		}
		atLineStart = false
	}
}

// readControlLine reads one line, without its terminator.
func readControlLine(br *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		chunk, err := br.ReadSlice('\n')
		if sb.Len()+len(chunk) > maxControlLine {
			return "", fmt.Errorf("%w: control line over %d bytes", ErrMalformed, maxControlLine)
		}
		sb.Write(chunk)
		if err == nil {
			break
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if errors.Is(err, io.EOF) {
			if sb.Len() == 0 {
				return "", io.EOF
			}
			break
		}
		return "", err
	}
	return strings.TrimRight(sb.String(), "\r\n"), nil
}

func firstWord(line string) string {
	if i := strings.IndexByte(line, ' '); i >= 0 {
		return line[:i]
	}
	return line
}

// keywords is a yEnc control line's "key=value" fields.
type keywords map[string]string

// parseKeywords reads the "key=value" pairs of a control line.
//
// name= is special: it is always last and its value may contain spaces, so
// everything after it is the name.
func parseKeywords(rest string) keywords {
	kw := make(keywords, 6)
	for {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			return kw
		}
		if lower := strings.ToLower(rest); strings.HasPrefix(lower, "name=") {
			kw["name"] = strings.TrimSpace(rest[len("name="):])
			return kw
		}
		field := rest
		if i := strings.IndexAny(rest, " \t"); i >= 0 {
			field, rest = rest[:i], rest[i:]
		} else {
			rest = ""
		}
		if eq := strings.IndexByte(field, '='); eq > 0 {
			kw[strings.ToLower(field[:eq])] = field[eq+1:]
		}
	}
}

func (k keywords) str(key string) string { return strings.TrimSpace(k[key]) }

func (k keywords) int(key string) int { return int(k.int64(key)) }

// int64 returns 0 for an absent or unreadable value: yEnc headers carry
// optional fields, and the ones that matter are validated by their caller.
func (k keywords) int64(key string) int64 {
	v, _ := k.lookupInt64(key)
	return v
}

func (k keywords) lookupInt64(key string) (int64, bool) {
	v, state := k.int64Field(key)
	return v, state == fieldPresent
}

// fieldState is what a control-line field turned out to be.
//
// The three-way split is the point: collapsing "absent" and "present but
// unreadable" into one answer is how a single flipped bit inside a "=yend
// pcrc32=" field disables the only check that would have caught a flipped bit
// in the payload beside it. A field the poster never wrote is a fact about the
// posting; a field that is there and is not a number is a damaged article, and
// this package says so rather than decoding on without it.
type fieldState int

const (
	fieldAbsent fieldState = iota
	fieldMalformed
	fieldPresent
)

func (k keywords) int64Field(key string) (int64, fieldState) {
	raw, ok := k[key]
	if !ok {
		return 0, fieldAbsent
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0, fieldMalformed
	}
	return n, fieldPresent
}

func (k keywords) crcField(key string) (uint32, fieldState) {
	raw, ok := k[key]
	if !ok {
		return 0, fieldAbsent
	}
	// Some posters pad the CRC to more than eight hex digits or prefix it
	// with zeroes; ParseUint with a 32-bit width rejects real overflow.
	n, err := strconv.ParseUint(strings.TrimSpace(raw), 16, 32)
	if err != nil {
		return 0, fieldMalformed
	}
	return uint32(n), fieldPresent
}
