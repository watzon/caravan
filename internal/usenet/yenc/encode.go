package yenc

import (
	"bufio"
	"bytes"
	"fmt"
	"hash/crc32"
	"io"
	"strings"
)

// Article describes the headers of one yEnc article to encode.
//
// The zero value plus a Name produces a single-part article, which is what a
// file small enough to post in one piece looks like.
type Article struct {
	// Name is the filename in the =ybegin header. It is required and may not
	// contain a line break.
	Name string
	// Part is the 1-based part number. Zero means a single-part article: no
	// part=, total= or =ypart line is written.
	Part int
	// Total is the number of parts, written as total= when positive.
	Total int
	// Begin is the 0-based offset of the body within the whole file. It is
	// written as the 1-based =ypart begin=, and is ignored when Part is 0.
	Begin int64
	// Size is the whole file's size for the =ybegin size= field. Zero means
	// the body's length, which is right for a single-part article.
	Size int64
	// LineLength is the encoded line length. Zero means DefaultLineLength.
	LineLength int
	// FileCRC32 is written as the trailer's crc32= when HasFileCRC is set.
	// Posters put it on the final part of a multipart file; a single-part
	// article always gets one, computed from the body.
	FileCRC32 uint32
	// HasFileCRC requests the whole-file crc32= field.
	HasFileCRC bool
}

// Encode returns one yEnc article body: exactly what an NNTP BODY command
// would return for it, with no dot-stuffing (that belongs to the wire).
func Encode(a Article, body []byte) ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(len(body) + len(body)/64 + 256)
	if err := EncodeTo(&buf, a, body); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// EncodeTo writes one yEnc article body to w.
func EncodeTo(w io.Writer, a Article, body []byte) error {
	if a.Name == "" {
		return fmt.Errorf("%w: article has no name", ErrMalformed)
	}
	if strings.ContainsAny(a.Name, "\r\n") {
		return fmt.Errorf("%w: name %q contains a line break", ErrMalformed, a.Name)
	}
	if a.Part < 0 {
		return fmt.Errorf("%w: part %d is negative", ErrMalformed, a.Part)
	}
	if a.Begin < 0 {
		return fmt.Errorf("%w: begin %d is negative", ErrMalformed, a.Begin)
	}

	lineLen := a.LineLength
	if lineLen <= 0 {
		lineLen = DefaultLineLength
	}
	size := a.Size
	if size <= 0 {
		size = int64(len(body))
	}

	bw := bufio.NewWriter(w)
	if a.Part > 0 {
		fmt.Fprintf(bw, "=ybegin part=%d", a.Part)
		if a.Total > 0 {
			fmt.Fprintf(bw, " total=%d", a.Total)
		}
		fmt.Fprintf(bw, " line=%d size=%d name=%s\r\n", lineLen, size, a.Name)
		// =ypart is 1-based and inclusive on both ends.
		fmt.Fprintf(bw, "=ypart begin=%d end=%d\r\n", a.Begin+1, a.Begin+int64(len(body)))
	} else {
		fmt.Fprintf(bw, "=ybegin line=%d size=%d name=%s\r\n", lineLen, size, a.Name)
	}

	encodeBody(bw, body, lineLen)

	sum := crc32.ChecksumIEEE(body)
	if a.Part > 0 {
		fmt.Fprintf(bw, "=yend size=%d part=%d pcrc32=%08x", len(body), a.Part, sum)
		if a.HasFileCRC {
			fmt.Fprintf(bw, " crc32=%08x", a.FileCRC32)
		}
		bw.WriteString("\r\n")
	} else {
		fmt.Fprintf(bw, "=yend size=%d crc32=%08x\r\n", len(body), sum)
	}
	return bw.Flush()
}

// encodeBody writes the escaped, line-wrapped payload.
//
// The escaping is deliberately wider than the four bytes the format requires:
// a leading '.', TAB or space is escaped too, so an article survives
// dot-stuffing and any middlebox that trims trailing whitespace. A decoder
// cannot tell the difference — an escape is an escape — so the extra safety
// is free.
func encodeBody(bw *bufio.Writer, body []byte, lineLen int) {
	line := make([]byte, 0, lineLen+8)

	flush := func() {
		// Trailing whitespace is the one hazard that cannot be seen when the
		// byte is written, only when the line ends. Rewriting it as an escape
		// keeps the decoded value identical.
		if n := len(line); n > 0 {
			if c := line[n-1]; c == ' ' || c == '\t' {
				line = append(line[:n-1], '=', c+64)
			}
		}
		bw.Write(line)
		bw.WriteString("\r\n")
		line = line[:0]
	}

	for _, b := range body {
		c := b + 42
		switch {
		case c == 0x00 || c == '\n' || c == '\r' || c == '=':
			line = append(line, '=', c+64)
		case len(line) == 0 && (c == '.' || c == '\t' || c == ' '):
			line = append(line, '=', c+64)
		default:
			line = append(line, c)
		}
		if len(line) >= lineLen {
			flush()
		}
	}
	if len(line) > 0 {
		flush()
	}
}

// EncodeFile splits data into parts of at most partSize bytes and encodes each
// one, returning the article bodies in part order.
//
// A file that fits in one part is encoded as a single-part article, which is
// what a poster would do; anything larger is multipart, with the whole-file
// crc32 on the final part so assembly can verify what it wrote.
func EncodeFile(name string, data []byte, partSize int) ([][]byte, error) {
	if partSize <= 0 {
		return nil, fmt.Errorf("%w: part size %d is not positive", ErrMalformed, partSize)
	}

	total := (len(data) + partSize - 1) / partSize
	if total <= 1 {
		article, err := Encode(Article{Name: name}, data)
		if err != nil {
			return nil, err
		}
		return [][]byte{article}, nil
	}

	fileCRC := crc32.ChecksumIEEE(data)
	out := make([][]byte, 0, total)
	for i := 0; i < total; i++ {
		begin := i * partSize
		end := min(begin+partSize, len(data))
		a := Article{
			Name:  name,
			Part:  i + 1,
			Total: total,
			Begin: int64(begin),
			Size:  int64(len(data)),
		}
		if i == total-1 {
			a.FileCRC32, a.HasFileCRC = fileCRC, true
		}
		article, err := Encode(a, data[begin:end])
		if err != nil {
			return nil, err
		}
		out = append(out, article)
	}
	return out, nil
}
