package yenc_test

import (
	"bytes"
	"errors"
	"fmt"
	"math/rand"
	"os/exec"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/usenet/yenc"
)

func decodeFixture(t *testing.T, name string) *yenc.Part {
	t.Helper()
	part, err := yenc.DecodeBytes(readFixture(t, name))
	if err != nil {
		t.Fatalf("DecodeBytes(%s): %v", name, err)
	}
	return part
}

func TestDecodeCorpus(t *testing.T) {
	single := readFixture(t, singleName)
	multi := readFixture(t, multiName)
	escaped := readFixture(t, "escaped.bin")
	reference := readFixture(t, "reference-single.bin")

	tests := []struct {
		name       string
		fixture    string
		want       []byte
		wantName   string
		part       int
		total      int
		begin      int64
		size       int64
		verified   bool
		hasFileCRC bool
	}{
		{
			name: "single part", fixture: "single.yenc", want: single,
			wantName: singleName, size: int64(len(single)),
			verified: true, hasFileCRC: true,
		},
		{
			name: "multipart first", fixture: "multi.part1.yenc", want: multi[:multiPart],
			wantName: multiName, part: 1, total: 3, begin: 0, size: int64(len(multi)), verified: true,
		},
		{
			name: "multipart middle", fixture: "multi.part2.yenc", want: multi[multiPart : multiPart*2],
			wantName: multiName, part: 2, total: 3, begin: multiPart, size: int64(len(multi)), verified: true,
		},
		{
			name: "multipart last carries the file crc", fixture: "multi.part3.yenc", want: multi[multiPart*2:],
			wantName: multiName, part: 3, total: 3, begin: multiPart * 2, size: int64(len(multi)),
			verified: true, hasFileCRC: true,
		},
		{
			name: "escaped critical bytes", fixture: "escaped.yenc", want: escaped,
			wantName: "caravan.escaped.bin", size: int64(len(escaped)),
			verified: true, hasFileCRC: true,
		},
		{
			// Produced by an independent encoder (testdata/reference.py) with
			// a different line length and a narrower escaping policy, so this
			// is the fixture that proves the decoder reads other people's
			// articles rather than only its own.
			name: "independently encoded article", fixture: "reference-single.yenc", want: reference,
			wantName: "reference.bin", size: int64(len(reference)),
			verified: true, hasFileCRC: true,
		},
		{
			// A poster who omits pcrc32 is not posting a damaged article.
			name: "missing part crc still decodes", fixture: "no-crc.yenc", want: multi[:multiPart],
			wantName: multiName, part: 1, total: 3, begin: 0, size: int64(len(multi)), verified: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			part := decodeFixture(t, tt.fixture)

			if !bytes.Equal(part.Body, tt.want) {
				t.Fatalf("body = %d bytes, want %d bytes (equal=%v)",
					len(part.Body), len(tt.want), bytes.Equal(part.Body, tt.want))
			}
			if part.Name != tt.wantName {
				t.Errorf("name = %q, want %q", part.Name, tt.wantName)
			}
			if part.Number != tt.part {
				t.Errorf("number = %d, want %d", part.Number, tt.part)
			}
			if part.Total != tt.total {
				t.Errorf("total = %d, want %d", part.Total, tt.total)
			}
			if part.Multipart != (tt.part > 0) {
				t.Errorf("multipart = %v, want %v", part.Multipart, tt.part > 0)
			}
			if part.Begin != tt.begin {
				t.Errorf("begin = %d, want %d", part.Begin, tt.begin)
			}
			if want := tt.begin + int64(len(tt.want)); part.End != want {
				t.Errorf("end = %d, want %d", part.End, want)
			}
			if part.Size != tt.size {
				t.Errorf("size = %d, want %d", part.Size, tt.size)
			}
			if part.Verified != tt.verified {
				t.Errorf("verified = %v, want %v", part.Verified, tt.verified)
			}
			if part.Verified && part.CRC32 != crcOf(tt.want) {
				t.Errorf("crc32 = %08x, want %08x", part.CRC32, crcOf(tt.want))
			}
			if part.HasFileCRC != tt.hasFileCRC {
				t.Errorf("hasFileCRC = %v, want %v", part.HasFileCRC, tt.hasFileCRC)
			}
		})
	}
}

// TestDecodeMultipartAssembles is the property the pipeline depends on:
// writing every part at its own Begin reconstructs the file, whatever order
// the parts arrive in, and the whole-file CRC the last part carries confirms
// it.
func TestDecodeMultipartAssembles(t *testing.T) {
	multi := readFixture(t, multiName)
	out := make([]byte, len(multi))

	var fileCRC uint32
	var haveFileCRC bool
	// Deliberately out of order: parts come back from a pool of connections.
	for _, name := range []string{"multi.part3.yenc", "multi.part1.yenc", "multi.part2.yenc"} {
		part := decodeFixture(t, name)
		if part.End-part.Begin != int64(len(part.Body)) {
			t.Fatalf("%s: end-begin = %d, want %d", name, part.End-part.Begin, len(part.Body))
		}
		copy(out[part.Begin:], part.Body)
		if part.HasFileCRC {
			fileCRC, haveFileCRC = part.FileCRC32, true
		}
	}

	if !bytes.Equal(out, multi) {
		t.Fatal("assembled file does not match the source")
	}
	if !haveFileCRC {
		t.Fatal("no part carried the whole-file crc32")
	}
	if err := yenc.CheckFileCRC(multiName, fileCRC, crcOf(out)); err != nil {
		t.Fatalf("CheckFileCRC: %v", err)
	}
}

// TestDecodeReferenceMultipartAssembles is the multipart half of the
// independently-encoded corpus.
//
// reference-single.yenc only ever exercised the single-part path, so nothing
// checked the =ypart offset convention against an article this package did not
// write: if the 1-based/inclusive reading were inverted, the round-trip tests
// and the generated multipart fixtures would all still agree with each other.
// These three articles are committed text produced by testdata/reference.py's
// own reading of the yEnc 1.3 draft (`=ypart begin=201 end=400` for part 2 of a
// 200-byte-part posting), and `go test -update` never rewrites them: so a
// decoder that changed its mind about the convention fails here.
func TestDecodeReferenceMultipartAssembles(t *testing.T) {
	whole := readFixture(t, "reference-multi.bin")
	const partSize = 200

	out := make([]byte, len(whole))
	var fileCRC uint32
	var haveFileCRC bool
	covered := 0
	// Out of order, as a pool of connections delivers them.
	for _, n := range []int{3, 1, 2} {
		name := fmt.Sprintf("reference-multi.part%d.yenc", n)
		part := decodeFixture(t, name)

		if part.Number != n || part.Total != 3 {
			t.Errorf("%s: part %d of %d, want %d of 3", name, part.Number, part.Total, n)
		}
		if part.Size != int64(len(whole)) {
			t.Errorf("%s: size = %d, want %d", name, part.Size, len(whole))
		}
		// The article text says begin=(n-1)*200+1; the decoder must report the
		// 0-based offset that assembly writes at.
		if want := int64((n - 1) * partSize); part.Begin != want {
			t.Errorf("%s: begin = %d, want %d", name, part.Begin, want)
		}
		if want := part.Begin + int64(len(part.Body)); part.End != want {
			t.Errorf("%s: end = %d, want %d", name, part.End, want)
		}
		if !part.Verified {
			t.Errorf("%s: verified = false, want true", name)
		}
		copy(out[part.Begin:], part.Body)
		covered += len(part.Body)
		if part.HasFileCRC {
			fileCRC, haveFileCRC = part.FileCRC32, true
		}
	}

	if covered != len(whole) {
		t.Fatalf("parts cover %d bytes, want %d", covered, len(whole))
	}
	if !bytes.Equal(out, whole) {
		t.Fatal("reassembling the independently encoded parts did not reproduce reference-multi.bin")
	}
	if !haveFileCRC {
		t.Fatal("no reference part carried the whole-file crc32")
	}
	if err := yenc.CheckFileCRC("reference-multi.bin", fileCRC, crcOf(whole)); err != nil {
		t.Fatalf("CheckFileCRC: %v", err)
	}
}

// TestReferenceImplementationVerifiesTheCorpus runs testdata/reference.py over
// every fixture, which is what proves Caravan's encoder emits articles a foreign
// decoder accepts. It runs in the suite rather than by hand, so encoder drift
// shared by both halves cannot go unnoticed.
func TestReferenceImplementationVerifiesTheCorpus(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not installed; run `python3 testdata/reference.py verify` by hand")
	}
	cmd := exec.Command(python, "reference.py", "verify")
	cmd.Dir = "testdata"
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("reference.py verify: %v\n%s", err, out)
	}
}

func TestDecodeRejectsDamage(t *testing.T) {
	single := readFixture(t, singleName)

	t.Run("corrupt payload", func(t *testing.T) {
		part, err := yenc.DecodeBytes(readFixture(t, "corrupt-payload.yenc"))
		if err == nil {
			t.Fatalf("decoded a corrupt article: %d bytes", len(part.Body))
		}
		if part != nil {
			t.Errorf("part = %+v, want nil alongside an error", part)
		}
		if !errors.Is(err, yenc.ErrCorrupt) {
			t.Fatalf("error = %v, want ErrCorrupt", err)
		}
		var crcErr *yenc.CRCError
		if !errors.As(err, &crcErr) {
			t.Fatalf("error = %T (%v), want *yenc.CRCError", err, err)
		}
		if crcErr.Expected != crcOf(single) {
			t.Errorf("expected crc = %08x, want %08x", crcErr.Expected, crcOf(single))
		}
		if crcErr.Actual == crcErr.Expected {
			t.Error("actual crc equals expected; the fixture is not corrupt")
		}
		if crcErr.Name != singleName {
			t.Errorf("name = %q, want %q", crcErr.Name, singleName)
		}
		// The message has to carry both numbers or a bug report cannot say
		// whether one article is damaged or the decoder is.
		for _, want := range []string{"crc32 mismatch", "expected", singleName} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error = %q, want it to mention %q", err, want)
			}
		}
	})

	t.Run("truncated", func(t *testing.T) {
		_, err := yenc.DecodeBytes(readFixture(t, "truncated.yenc"))
		if !errors.Is(err, yenc.ErrCorrupt) {
			t.Fatalf("error = %v, want ErrCorrupt", err)
		}
		var sizeErr *yenc.SizeError
		if !errors.As(err, &sizeErr) {
			t.Fatalf("error = %T (%v), want *yenc.SizeError", err, err)
		}
		if !sizeErr.Truncated {
			t.Error("truncated = false, want true")
		}
		if sizeErr.Expected != int64(len(single)) {
			t.Errorf("expected = %d, want %d", sizeErr.Expected, len(single))
		}
		if sizeErr.Actual >= sizeErr.Expected {
			t.Errorf("actual = %d, want fewer than %d", sizeErr.Actual, sizeErr.Expected)
		}
		if !strings.Contains(err.Error(), "=yend") {
			t.Errorf("error = %q, want it to say the article ended before =yend", err)
		}
	})

	t.Run("trailer size disagrees with the payload", func(t *testing.T) {
		_, err := yenc.DecodeBytes(readFixture(t, "size-mismatch.yenc"))
		var sizeErr *yenc.SizeError
		if !errors.As(err, &sizeErr) {
			t.Fatalf("error = %T (%v), want *yenc.SizeError", err, err)
		}
		if sizeErr.Truncated {
			t.Error("truncated = true, want false: the article was complete")
		}
		if sizeErr.Expected != multiPart-1 || sizeErr.Actual != multiPart {
			t.Errorf("expected/actual = %d/%d, want %d/%d",
				sizeErr.Expected, sizeErr.Actual, multiPart-1, multiPart)
		}
	})

	t.Run("ypart range disagrees with the payload", func(t *testing.T) {
		// This is the dangerous one: the bytes are fine, the offsets are not,
		// so accepting it would write good data in the wrong place.
		_, err := yenc.DecodeBytes(readFixture(t, "ypart-mismatch.yenc"))
		var sizeErr *yenc.SizeError
		if !errors.As(err, &sizeErr) {
			t.Fatalf("error = %T (%v), want *yenc.SizeError", err, err)
		}
		if sizeErr.Part != 2 {
			t.Errorf("part = %d, want 2", sizeErr.Part)
		}
		if sizeErr.Expected != multiPart-1 || sizeErr.Actual != multiPart {
			t.Errorf("expected/actual = %d/%d, want %d/%d",
				sizeErr.Expected, sizeErr.Actual, multiPart-1, multiPart)
		}
	})

	t.Run("ypart range lies outside the declared file", func(t *testing.T) {
		// The article is self-consistent, end-begin matches the payload, the
		// trailer size matches, pcrc32 matches, and the offset is still a
		// terabyte past the end of a 603-byte file. Accepting it makes the
		// pipeline write a multi-terabyte sparse file par2 cannot recognise.
		part, err := yenc.DecodeBytes(readFixture(t, "ypart-out-of-range.yenc"))
		if err == nil {
			t.Fatalf("decoded a part at offset %d of a %d-byte file", part.Begin, part.Size)
		}
		if part != nil {
			t.Errorf("part = %+v, want nil alongside an error", part)
		}
		if !errors.Is(err, yenc.ErrMalformed) {
			t.Fatalf("error = %v, want ErrMalformed", err)
		}
	})

	t.Run("unreadable pcrc32 field is damage, not a poster who omitted it", func(t *testing.T) {
		// The payload is wrong *and* the field that would have caught it is
		// unreadable. Returning the payload with a nil error here is the one
		// outcome the package promises never to produce.
		part, err := yenc.DecodeBytes(readFixture(t, "bad-pcrc-field.yenc"))
		if err == nil {
			t.Fatalf("decoded %d bytes whose only checksum was unreadable", len(part.Body))
		}
		if part != nil {
			t.Errorf("part = %+v, want nil alongside an error", part)
		}
		if !errors.Is(err, yenc.ErrMalformed) {
			t.Fatalf("error = %v, want ErrMalformed", err)
		}
		if !strings.Contains(err.Error(), "pcrc32") {
			t.Errorf("error = %q, want it to name the field it could not read", err)
		}
	})

	t.Run("trailer names another part", func(t *testing.T) {
		_, err := yenc.DecodeBytes(readFixture(t, "part-mismatch.yenc"))
		if !errors.Is(err, yenc.ErrMalformed) {
			t.Fatalf("error = %v, want ErrMalformed", err)
		}
	})

	t.Run("not a yEnc article", func(t *testing.T) {
		_, err := yenc.DecodeBytes(readFixture(t, "no-header.yenc"))
		if !errors.Is(err, yenc.ErrNotYenc) {
			t.Fatalf("error = %v, want ErrNotYenc", err)
		}
		if errors.Is(err, yenc.ErrCorrupt) {
			t.Error("a plain-text article is not a corrupt one")
		}
	})
}

func TestDecodeMalformedHeaders(t *testing.T) {
	tests := []struct {
		name    string
		article string
		want    error
	}{
		{
			name:    "empty article",
			article: "",
			want:    yenc.ErrNotYenc,
		},
		{
			name:    "header without a name",
			article: "=ybegin line=128 size=3\r\nabc\r\n=yend size=3\r\n",
			want:    yenc.ErrMalformed,
		},
		{
			name:    "unreadable part number",
			article: "=ybegin part=x total=2 line=128 size=3 name=a.bin\r\nabc\r\n=yend size=3 part=1\r\n",
			want:    yenc.ErrMalformed,
		},
		{
			name:    "negative part number",
			article: "=ybegin part=-1 line=128 size=3 name=a.bin\r\nabc\r\n=yend size=3\r\n",
			want:    yenc.ErrMalformed,
		},
		{
			name:    "later part with no =ypart",
			article: "=ybegin part=2 total=3 line=128 size=9 name=a.bin\r\nabc\r\n=yend size=3 part=2\r\n",
			want:    yenc.ErrMalformed,
		},
		{
			name:    "=ypart with a backwards range",
			article: "=ybegin part=1 total=2 line=128 size=9 name=a.bin\r\n=ypart begin=5 end=1\r\nabc\r\n=yend size=3 part=1\r\n",
			want:    yenc.ErrMalformed,
		},
		{
			name:    "=ypart begin is not 1-based",
			article: "=ybegin part=1 total=2 line=128 size=9 name=a.bin\r\n=ypart begin=0 end=3\r\nabc\r\n=yend size=3 part=1\r\n",
			want:    yenc.ErrMalformed,
		},
		{
			// Realistic poster spellings that are not bare hex. Each one used
			// to disable verification for the whole article instead of being
			// reported, which is worse than having no checksum at all: the
			// article claims one and the decoder cannot read it.
			name:    "trailer crc32 with an 0x prefix",
			article: "=ybegin line=128 size=3 name=a.bin\r\nabc\r\n=yend size=3 crc32=0x1a2b3c4d\r\n",
			want:    yenc.ErrMalformed,
		},
		{
			name:    "trailer crc32 with trailing junk",
			article: "=ybegin line=128 size=3 name=a.bin\r\nabc\r\n=yend size=3 crc32=1a2b3c4d-\r\n",
			want:    yenc.ErrMalformed,
		},
		{
			name:    "unreadable trailer pcrc32",
			article: "=ybegin part=1 total=2 line=128 size=9 name=a.bin\r\n=ypart begin=1 end=3\r\nabc\r\n=yend size=3 part=1 pcrc32=deadbeefzz\r\n",
			want:    yenc.ErrMalformed,
		},
		{
			name:    "unreadable trailer size",
			article: "=ybegin line=128 size=3 name=a.bin\r\nabc\r\n=yend size=three\r\n",
			want:    yenc.ErrMalformed,
		},
		{
			name:    "=ypart end past the declared file size",
			article: "=ybegin part=2 total=2 line=128 size=4 name=a.bin\r\n=ypart begin=1099511627777 end=1099511627779\r\nabc\r\n=yend size=3 part=2\r\n",
			want:    yenc.ErrMalformed,
		},
		{
			name:    "second =ybegin inside the payload",
			article: "=ybegin line=128 size=6 name=a.bin\r\nabc\r\n=ybegin line=128 size=3 name=b.bin\r\nabc\r\n=yend size=6\r\n",
			want:    yenc.ErrMalformed,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			part, err := yenc.DecodeBytes([]byte(tt.article))
			if err == nil {
				t.Fatalf("DecodeBytes = %+v, want %v", part, tt.want)
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestDecodeLenience covers the article shapes real posters produce that a
// strict reading of the format would reject.
func TestDecodeLenience(t *testing.T) {
	t.Run("preamble before the header is skipped", func(t *testing.T) {
		article := "Posted by CaravanPoster v1.0\r\n\r\n" +
			"=ybegin line=128 size=3 name=a.bin\r\n" + encodedLine("abc") + "\r\n=yend size=3\r\n"
		part, err := yenc.DecodeBytes([]byte(article))
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if string(part.Body) != "abc" {
			t.Errorf("body = %q, want %q", part.Body, "abc")
		}
	})

	t.Run("trailing junk after =yend is ignored", func(t *testing.T) {
		article := "=ybegin line=128 size=3 name=a.bin\r\n" + encodedLine("abc") +
			"\r\n=yend size=3\r\nposted with a signature\r\n"
		part, err := yenc.DecodeBytes([]byte(article))
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if string(part.Body) != "abc" {
			t.Errorf("body = %q, want %q", part.Body, "abc")
		}
	})

	t.Run("bare LF line endings", func(t *testing.T) {
		article := "=ybegin line=128 size=3 name=a.bin\n" + encodedLine("abc") + "\n=yend size=3\n"
		part, err := yenc.DecodeBytes([]byte(article))
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if string(part.Body) != "abc" {
			t.Errorf("body = %q, want %q", part.Body, "abc")
		}
	})

	t.Run("single part of one without =ypart", func(t *testing.T) {
		article := "=ybegin part=1 total=1 line=128 size=3 name=a.bin\r\n" +
			encodedLine("abc") + "\r\n=yend size=3 part=1\r\n"
		part, err := yenc.DecodeBytes([]byte(article))
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if part.Begin != 0 || part.End != 3 {
			t.Errorf("range = [%d,%d), want [0,3)", part.Begin, part.End)
		}
	})

	t.Run("escape split across a line break", func(t *testing.T) {
		// No conforming encoder emits this, but a decoder that dropped the
		// pending escape would silently produce the wrong byte.
		article := "=ybegin line=4 size=2 name=a.bin\r\na=\r\n}\r\n=yend size=2\r\n"
		part, err := yenc.DecodeBytes([]byte(article))
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		// 'a' (0x61) decodes to 0x37; the escaped '}' (0x7D) decodes to 0x13.
		if want := []byte{0x37, 0x13}; !bytes.Equal(part.Body, want) {
			t.Errorf("body = %v, want %v", part.Body, want)
		}
	})

	t.Run("uppercase keywords and a padded crc", func(t *testing.T) {
		article := "=ybegin LINE=128 SIZE=3 NAME=a.bin\r\n" + encodedLine("abc") +
			"\r\n=yend SIZE=3 CRC32=" + strings.ToUpper(hex8(crcOf([]byte("abc")))) + "\r\n"
		part, err := yenc.DecodeBytes([]byte(article))
		if err != nil {
			t.Fatalf("Decode: %v", err)
		}
		if !part.Verified {
			t.Error("verified = false, want true")
		}
	})
}

// encodedLine yEnc-encodes s onto one line, for hand-written article fixtures.
func encodedLine(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i] + 42
		switch c {
		case 0x00, '\n', '\r', '=':
			b.WriteByte('=')
			b.WriteByte(c + 64)
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func hex8(v uint32) string {
	const digits = "0123456789abcdef"
	out := make([]byte, 8)
	for i := 7; i >= 0; i-- {
		out[i] = digits[v&0xF]
		v >>= 4
	}
	return string(out)
}

// BenchmarkDecode measures a realistically sized article (the ~700 KB posters
// use), since every segment of every download passes through here.
func BenchmarkDecode(b *testing.B) {
	body := make([]byte, 700<<10)
	r := rand.New(rand.NewSource(7))
	for i := range body {
		body[i] = byte(r.Intn(256))
	}
	article, err := yenc.Encode(yenc.Article{Name: "bench.bin", Part: 1, Total: 2, Size: 1 << 21}, body)
	if err != nil {
		b.Fatalf("Encode: %v", err)
	}

	b.SetBytes(int64(len(body)))
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := yenc.DecodeBytes(article); err != nil {
			b.Fatalf("Decode: %v", err)
		}
	}
}
