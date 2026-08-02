package yenc_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/usenet/yenc"
)

func TestEncodeSinglePartHeaders(t *testing.T) {
	body := []byte("caravan")
	article, err := yenc.Encode(yenc.Article{Name: "a.bin"}, body)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	text := string(article)
	wantHead := fmt.Sprintf("=ybegin line=%d size=%d name=a.bin\r\n", yenc.DefaultLineLength, len(body))
	if !strings.HasPrefix(text, wantHead) {
		t.Errorf("header = %q, want prefix %q", firstLine(text), wantHead)
	}
	wantTail := fmt.Sprintf("=yend size=%d crc32=%s\r\n", len(body), hex8(crcOf(body)))
	if !strings.HasSuffix(text, wantTail) {
		t.Errorf("trailer = %q, want suffix %q", lastLine(text), wantTail)
	}
	if strings.Contains(text, "=ypart") {
		t.Error("single-part article carries an =ypart header")
	}
}

func TestEncodeMultipartHeaders(t *testing.T) {
	body := []byte("second chunk")
	article, err := yenc.Encode(yenc.Article{
		Name: "a.bin", Part: 2, Total: 3, Begin: 100, Size: 512,
		FileCRC32: 0xdeadbeef, HasFileCRC: true,
	}, body)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}

	text := string(article)
	wantHead := fmt.Sprintf("=ybegin part=2 total=3 line=%d size=512 name=a.bin\r\n", yenc.DefaultLineLength)
	if !strings.HasPrefix(text, wantHead) {
		t.Errorf("header = %q, want prefix %q", firstLine(text), wantHead)
	}
	// =ypart is 1-based and inclusive: offset 100 for 12 bytes is 101..112.
	if want := "=ypart begin=101 end=112\r\n"; !strings.Contains(text, want) {
		t.Errorf("article missing %q", want)
	}
	wantTail := fmt.Sprintf("=yend size=%d part=2 pcrc32=%s crc32=deadbeef\r\n", len(body), hex8(crcOf(body)))
	if !strings.HasSuffix(text, wantTail) {
		t.Errorf("trailer = %q, want suffix %q", lastLine(text), wantTail)
	}
}

func TestEncodeEscapesTheDangerousBytes(t *testing.T) {
	tests := []struct {
		name string
		in   byte
		want string
	}{
		{name: "NUL", in: 0xD6, want: "=@"},
		{name: "LF", in: 0xE0, want: "=J"},
		{name: "CR", in: 0xE3, want: "=M"},
		{name: "equals", in: 0x13, want: "=}"},
		{name: "leading dot", in: 0x04, want: "=n"},
		{name: "leading tab", in: 0xDF, want: "=I"},
		{name: "leading space", in: 0xF6, want: "=`"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			article, err := yenc.Encode(yenc.Article{Name: "a.bin"}, []byte{tt.in})
			if err != nil {
				t.Fatalf("Encode: %v", err)
			}
			payload := payloadLines(t, string(article))
			if len(payload) != 1 || payload[0] != tt.want {
				t.Fatalf("payload = %q, want [%q]", payload, tt.want)
			}
			// Whatever the escaping, the value has to survive it.
			part, err := yenc.DecodeBytes(article)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !bytes.Equal(part.Body, []byte{tt.in}) {
				t.Errorf("round trip = %v, want %v", part.Body, []byte{tt.in})
			}
		})
	}
}

func TestEncodeEscapesTrailingWhitespace(t *testing.T) {
	// A space as the last byte of a line would be trimmed by any middlebox
	// that tidies whitespace, and the article would decode short.
	body := []byte{0x00, 0xF6, 0x00, 0xDF}
	article, err := yenc.Encode(yenc.Article{Name: "a.bin", LineLength: 2}, body)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	for i, line := range payloadLines(t, string(article)) {
		if strings.HasSuffix(line, " ") || strings.HasSuffix(line, "\t") {
			t.Errorf("payload line %d (%q) ends with whitespace", i+1, line)
		}
	}
	part, err := yenc.DecodeBytes(article)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !bytes.Equal(part.Body, body) {
		t.Errorf("round trip = %v, want %v", part.Body, body)
	}
}

func TestEncodeLineLength(t *testing.T) {
	// Plain bytes so nothing is escaped and every full line is exactly the
	// requested length.
	body := bytes.Repeat([]byte{'A' - 42}, 300)
	article, err := yenc.Encode(yenc.Article{Name: "a.bin", LineLength: 64}, body)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	lines := payloadLines(t, string(article))
	if got, want := len(lines), 5; got != want {
		t.Fatalf("payload lines = %d, want %d", got, want)
	}
	for i, line := range lines[:len(lines)-1] {
		if len(line) != 64 {
			t.Errorf("line %d has %d bytes, want 64", i+1, len(line))
		}
	}
	if got, want := len(lines[len(lines)-1]), 300%64; got != want {
		t.Errorf("last line has %d bytes, want %d", got, want)
	}
}

func TestEncodeRejectsBadArticles(t *testing.T) {
	tests := []struct {
		name string
		a    yenc.Article
	}{
		{name: "no name", a: yenc.Article{}},
		{name: "name with a line break", a: yenc.Article{Name: "a\r\nb.bin"}},
		{name: "negative part", a: yenc.Article{Name: "a.bin", Part: -1}},
		{name: "negative begin", a: yenc.Article{Name: "a.bin", Part: 1, Begin: -1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := yenc.Encode(tt.a, []byte("x")); !errors.Is(err, yenc.ErrMalformed) {
				t.Fatalf("error = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestEncodeFile(t *testing.T) {
	data := randomBytes(9, 1000)

	t.Run("splits into parts that reassemble", func(t *testing.T) {
		articles, err := yenc.EncodeFile("movie.mkv", data, 300)
		if err != nil {
			t.Fatalf("EncodeFile: %v", err)
		}
		if got, want := len(articles), 4; got != want {
			t.Fatalf("parts = %d, want %d", got, want)
		}

		out := make([]byte, len(data))
		var fileCRC uint32
		for i, article := range articles {
			part, err := yenc.DecodeBytes(article)
			if err != nil {
				t.Fatalf("part %d: %v", i+1, err)
			}
			if part.Number != i+1 || part.Total != 4 {
				t.Errorf("part %d: number/total = %d/%d, want %d/4", i+1, part.Number, part.Total, i+1)
			}
			if part.Size != int64(len(data)) {
				t.Errorf("part %d: size = %d, want %d", i+1, part.Size, len(data))
			}
			if !part.Verified {
				t.Errorf("part %d: not verified", i+1)
			}
			copy(out[part.Begin:], part.Body)
			if part.HasFileCRC {
				if i != len(articles)-1 {
					t.Errorf("part %d carries the file crc, want it only on the last part", i+1)
				}
				fileCRC = part.FileCRC32
			}
		}
		if !bytes.Equal(out, data) {
			t.Error("reassembled file does not match the source")
		}
		if err := yenc.CheckFileCRC("movie.mkv", fileCRC, crcOf(out)); err != nil {
			t.Fatalf("CheckFileCRC: %v", err)
		}
	})

	t.Run("a file that fits in one part is single-part", func(t *testing.T) {
		articles, err := yenc.EncodeFile("small.bin", data, len(data))
		if err != nil {
			t.Fatalf("EncodeFile: %v", err)
		}
		if got, want := len(articles), 1; got != want {
			t.Fatalf("parts = %d, want %d", got, want)
		}
		part, err := yenc.DecodeBytes(articles[0])
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if part.Multipart {
			t.Error("multipart = true, want false")
		}
		if !bytes.Equal(part.Body, data) {
			t.Error("body does not match the source")
		}
	})

	t.Run("an empty file is one empty part", func(t *testing.T) {
		articles, err := yenc.EncodeFile("empty.bin", nil, 100)
		if err != nil {
			t.Fatalf("EncodeFile: %v", err)
		}
		if got, want := len(articles), 1; got != want {
			t.Fatalf("parts = %d, want %d", got, want)
		}
		part, err := yenc.DecodeBytes(articles[0])
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if len(part.Body) != 0 {
			t.Errorf("body = %v, want empty", part.Body)
		}
	})

	t.Run("rejects a non-positive part size", func(t *testing.T) {
		if _, err := yenc.EncodeFile("a.bin", data, 0); !errors.Is(err, yenc.ErrMalformed) {
			t.Fatalf("error = %v, want ErrMalformed", err)
		}
	})
}

// TestDecodeEncodeRoundTrip is the property test the corpus cannot be: random
// payloads, random part sizes, random line lengths, encode then decode, and
// the bytes must come back identical. The seed is fixed so a failure is
// reproducible.
func TestDecodeEncodeRoundTrip(t *testing.T) {
	r := rand.New(rand.NewSource(20260201))

	for i := 0; i < 300; i++ {
		size := r.Intn(4096)
		body := make([]byte, size)
		switch i % 3 {
		case 0:
			// Uniform random: hits every byte value including the ones that
			// encode to critical characters.
			for j := range body {
				body[j] = byte(r.Intn(256))
			}
		case 1:
			// Biased to the bytes that must be escaped, so escapes cluster
			// and land on line boundaries.
			critical := []byte{0x00, 0x04, 0x13, 0xD6, 0xDF, 0xE0, 0xE3, 0xF6}
			for j := range body {
				body[j] = critical[r.Intn(len(critical))]
			}
		case 2:
			// A single repeated value, which is what a run of padding or
			// silence in real media looks like.
			fill := byte(r.Intn(256))
			for j := range body {
				body[j] = fill
			}
		}

		lineLen := 1 + r.Intn(256)
		article, err := yenc.Encode(yenc.Article{Name: "round.trip.bin", LineLength: lineLen}, body)
		if err != nil {
			t.Fatalf("iteration %d: Encode: %v", i, err)
		}
		part, err := yenc.DecodeBytes(article)
		if err != nil {
			t.Fatalf("iteration %d (size=%d line=%d): Decode: %v", i, size, lineLen, err)
		}
		if !bytes.Equal(part.Body, body) {
			t.Fatalf("iteration %d (size=%d line=%d): round trip changed the payload", i, size, lineLen)
		}
		if part.Verified != true {
			t.Fatalf("iteration %d: verified = false", i)
		}
		if part.End != int64(size) {
			t.Fatalf("iteration %d: end = %d, want %d", i, part.End, size)
		}
	}
}

// TestDecodeEncodeRoundTripMultipart does the same across part boundaries,
// where an off-by-one in =ypart would corrupt a file without touching a CRC.
func TestDecodeEncodeRoundTripMultipart(t *testing.T) {
	r := rand.New(rand.NewSource(20260202))

	for i := 0; i < 100; i++ {
		size := 1 + r.Intn(8192)
		body := make([]byte, size)
		for j := range body {
			body[j] = byte(r.Intn(256))
		}
		partSize := 1 + r.Intn(size)

		articles, err := yenc.EncodeFile("multi.round.bin", body, partSize)
		if err != nil {
			t.Fatalf("iteration %d: EncodeFile: %v", i, err)
		}

		out := make([]byte, size)
		covered := 0
		for n, article := range articles {
			part, err := yenc.DecodeBytes(article)
			if err != nil {
				t.Fatalf("iteration %d part %d: Decode: %v", i, n+1, err)
			}
			if part.Begin != int64(n*partSize) {
				t.Fatalf("iteration %d part %d: begin = %d, want %d", i, n+1, part.Begin, n*partSize)
			}
			copy(out[part.Begin:], part.Body)
			covered += len(part.Body)
		}
		if covered != size {
			t.Fatalf("iteration %d: parts cover %d bytes, want %d", i, covered, size)
		}
		if !bytes.Equal(out, body) {
			t.Fatalf("iteration %d (size=%d partSize=%d): reassembly changed the payload", i, size, partSize)
		}
	}
}

// TestCorruptionIsAlwaysCaught flips one bit in a real article and insists the
// decoder notices. This is the guarantee the repair pipeline is built on: a
// damaged segment must never decode cleanly.
func TestCorruptionIsAlwaysCaught(t *testing.T) {
	body := randomBytes(11, 512)
	article, err := yenc.Encode(yenc.Article{Name: "damage.bin", LineLength: 64}, body)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	headerEnd := bytes.Index(article, []byte("\r\n")) + 2

	r := rand.New(rand.NewSource(20260203))
	for i := 0; i < 200; i++ {
		damaged := bytes.Clone(article)
		pos := headerEnd + r.Intn(len(article)-headerEnd)
		damaged[pos] ^= 1 << r.Intn(8)
		if bytes.Equal(damaged, article) {
			continue
		}

		part, err := yenc.DecodeBytes(damaged)
		if err != nil {
			continue // caught, which is the point
		}
		// A flip that lands on a byte the decoder ignores (the trailing
		// junk after =yend, say) is allowed to decode — but only if the
		// payload is still exactly right.
		if !bytes.Equal(part.Body, body) {
			t.Fatalf("iteration %d: a bit flip at offset %d decoded to the wrong payload with no error", i, pos)
		}
	}
}

// TestTwoBitCorruptionIsAlwaysCaught is the case a single flip cannot reach:
// one bit inside a "=yend" field's value and one in the payload. A single flip
// in the checksum leaves the payload correct, so the single-flip fuzz above
// passes on it; the damage that matters is a field the decoder cannot read
// sitting over bytes that are genuinely wrong.
//
// Only the *values* are damaged. A flip in a keyword's name ("pcrc32=" ->
// "pcrq32=") turns the field into one this decoder has never heard of, which is
// indistinguishable from the poster who simply omitted pcrc32 — a posting the
// package deliberately accepts (testdata/no-crc.yenc). Catching that would mean
// rejecting every article carrying a keyword this decoder does not know, which
// fails loudly on healthy releases to defend against damage that needs two
// independent flips to reach.
func TestTwoBitCorruptionIsAlwaysCaught(t *testing.T) {
	body := randomBytes(12, 512)
	article, err := yenc.Encode(yenc.Article{Name: "damage.bin", LineLength: 64, Part: 1, Total: 2, Size: 2048}, body)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	trailer := bytes.LastIndex(article, []byte("=yend"))
	if trailer < 0 {
		t.Fatal("encoded article has no =yend trailer")
	}
	headerEnd := bytes.Index(article, []byte("\r\n")) + 2
	values := fieldValueOffsets(t, article, trailer)

	r := rand.New(rand.NewSource(20260801))
	for i := 0; i < 400; i++ {
		damaged := bytes.Clone(article)
		tpos := values[r.Intn(len(values))]
		ppos := headerEnd + r.Intn(trailer-headerEnd)
		damaged[tpos] ^= 1 << r.Intn(8)
		damaged[ppos] ^= 1 << r.Intn(8)
		if bytes.Equal(damaged, article) {
			continue
		}

		part, err := yenc.DecodeBytes(damaged)
		if err != nil {
			continue // caught, which is the point
		}
		if !bytes.Equal(part.Body, body) {
			t.Fatalf("iteration %d: flips at %d (a =yend field value) and %d (payload) decoded to the wrong payload with no error",
				i, tpos, ppos)
		}
	}
}

// fieldValueOffsets lists the offsets of every "key=value" value byte on the
// control line starting at from.
func fieldValueOffsets(t *testing.T, article []byte, from int) []int {
	t.Helper()
	line := article[from:]
	if end := bytes.IndexByte(line, '\r'); end >= 0 {
		line = line[:end]
	}
	var out []int
	inValue := false
	for i := len("=yend"); i < len(line); i++ {
		switch {
		case line[i] == ' ':
			inValue = false
		case line[i] == '=' && !inValue:
			inValue = true
		case inValue:
			out = append(out, from+i)
		}
	}
	if len(out) == 0 {
		t.Fatalf("no field values on %q", line)
	}
	return out
}

func firstLine(s string) string {
	if i := strings.Index(s, "\r\n"); i >= 0 {
		return s[:i+2]
	}
	return s
}

func lastLine(s string) string {
	trimmed := strings.TrimSuffix(s, "\r\n")
	if i := strings.LastIndex(trimmed, "\r\n"); i >= 0 {
		return trimmed[i+2:] + "\r\n"
	}
	return s
}

// payloadLines is an article's data lines: everything between the "=y" control
// lines.
func payloadLines(t *testing.T, article string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(strings.TrimSuffix(article, "\r\n"), "\r\n") {
		if strings.HasPrefix(line, "=y") {
			continue
		}
		out = append(out, line)
	}
	return out
}
