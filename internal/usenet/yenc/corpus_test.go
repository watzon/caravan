package yenc_test

import (
	"bytes"
	"flag"
	"fmt"
	"hash/crc32"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/usenet/yenc"
)

// The corpus in testdata is checked in, and this file is how it is
// reproduced: `go test ./internal/usenet/yenc -run Corpus -update` rewrites
// every generated fixture, and a normal run fails if a checked-in fixture no
// longer matches what the generator produces. That turns "the corpus is
// stale" into a test failure instead of a mystery.
//
// The generated corpus is encoder output, so on its own it would only prove
// the encoder and decoder agree with each other. Two things break that
// circle: testdata/reference-single.yenc and testdata/reference-multi.part*.yenc
// are yEnc articles produced by an independent implementation
// (testdata/reference.py, which also decodes and checks every generated fixture
// — TestReferenceImplementationVerifiesTheCorpus runs that pass, and
// `python3 reference.py emit` regenerates the reference articles), and
// TestDecodeEncodeRoundTrip drives random payloads through both halves.
//
// The reference fixtures are deliberately outside `corpus`: -update must never
// rewrite them, or they would stop being independent evidence.
var update = flag.Bool("update", false, "rewrite the generated yEnc fixtures in testdata")

const (
	singleName = "caravan.single.bin"
	multiName  = "caravan.multi.bin"
	multiPart  = 256
)

// corpus is every generated fixture, by filename.
func corpus(t *testing.T) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}

	// A single-part article over a payload that contains every byte value,
	// so no value can be mis-shifted without the CRC noticing.
	single := allBytes(3)
	single = append(single, randomBytes(1, 700)...)
	out[singleName] = single
	out["single.yenc"] = mustEncode(t, yenc.Article{Name: singleName}, single)

	// A three-part file: the shape assembly actually deals with, including a
	// short final part and the whole-file crc on it.
	multi := randomBytes(2, multiPart*2+91)
	out[multiName] = multi
	parts, err := yenc.EncodeFile(multiName, multi, multiPart)
	if err != nil {
		t.Fatalf("EncodeFile: %v", err)
	}
	if len(parts) != 3 {
		t.Fatalf("EncodeFile produced %d parts, want 3", len(parts))
	}
	for i, p := range parts {
		out[fmt.Sprintf("multi.part%d.yenc", i+1)] = p
	}

	// Every byte that has to be escaped, in the positions that make escaping
	// necessary: '.' first on a line, whitespace last on one, and NUL/CR/LF/'='
	// anywhere. The payload bytes are chosen for what they encode *to*
	// (value+42), which is where the critical bytes appear.
	escaped := escapedPayload()
	out["escaped.bin"] = escaped
	out["escaped.yenc"] = mustEncode(t, yenc.Article{Name: "caravan.escaped.bin", LineLength: 32}, escaped)

	// Damage. Each of these is a real failure mode: a flipped byte on the
	// wire, a connection dropped mid-article, a trailer that disagrees with
	// its payload, a part spliced from another posting.
	out["corrupt-payload.yenc"] = flipPayloadByte(t, out["single.yenc"])
	out["truncated.yenc"] = truncateArticle(out["single.yenc"])
	out["size-mismatch.yenc"] = replaceOnce(t, out["multi.part1.yenc"],
		fmt.Sprintf("=yend size=%d", multiPart), fmt.Sprintf("=yend size=%d", multiPart-1))
	out["ypart-mismatch.yenc"] = replaceOnce(t, out["multi.part2.yenc"],
		fmt.Sprintf("end=%d", multiPart*2), fmt.Sprintf("end=%d", multiPart*2-1))
	// A =ypart range that is internally consistent — end-begin still matches
	// the payload, so pcrc32 and the trailer size both pass — but points a
	// terabyte past the file =ybegin describes. This is what a flipped bit in
	// begin= produces, and the only thing left to catch it is the file size.
	out["ypart-out-of-range.yenc"] = replaceOnce(t, out["multi.part2.yenc"],
		fmt.Sprintf("=ypart begin=%d end=%d", multiPart+1, multiPart*2),
		fmt.Sprintf("=ypart begin=%d end=%d", 1<<40+1, 1<<40+multiPart))
	// A checksum field that is present and unreadable, over a payload that has
	// also been damaged. Treating the unreadable field as "no checksum
	// declared" would wave the wrong bytes through with a nil error.
	out["bad-pcrc-field.yenc"] = replaceOnce(t, flipPayloadByte(t, out["multi.part1.yenc"]),
		fmt.Sprintf("pcrc32=%08x", crcOf(multi[:multiPart])), "pcrc32=deadbeefzz")
	out["part-mismatch.yenc"] = replaceOnce(t, out["multi.part2.yenc"], "part=2 pcrc32=", "part=3 pcrc32=")
	out["no-header.yenc"] = []byte("This is a plain text announcement article.\r\nNothing to decode here.\r\n")

	// Not damage: a poster who omits pcrc32. Decoding must succeed and say
	// it could not verify, rather than failing a download par2 cannot help.
	out["no-crc.yenc"] = stripPCRC(t, out["multi.part1.yenc"])

	return out
}

func TestCorpusIsReproducible(t *testing.T) {
	for name, want := range corpus(t) {
		path := filepath.Join("testdata", name)
		if *update {
			if err := os.WriteFile(path, want, 0o644); err != nil {
				t.Fatalf("write %s: %v", path, err)
			}
			continue
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v (run: go test ./internal/usenet/yenc -run Corpus -update)", path, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("%s is stale: the generator now produces different bytes "+
				"(run: go test ./internal/usenet/yenc -run Corpus -update)", path)
		}
	}
}

// TestCorpusEscapingIsSafeOnTheWire asserts the properties that make an
// encoded article survive NNTP: no line may begin with a character a server
// would dot-stuff or a middlebox would eat, none may end with whitespace a
// middlebox would trim, and no payload byte may look like a line terminator.
func TestCorpusEscapingIsSafeOnTheWire(t *testing.T) {
	for _, name := range []string{"single.yenc", "escaped.yenc", "multi.part1.yenc", "multi.part2.yenc", "multi.part3.yenc"} {
		t.Run(name, func(t *testing.T) {
			article := readFixture(t, name)
			lines := strings.Split(strings.TrimSuffix(string(article), "\r\n"), "\r\n")
			for i, line := range lines {
				if line == "" {
					t.Errorf("line %d is empty", i+1)
				}
				if strings.ContainsAny(line, "\x00\r\n") {
					t.Errorf("line %d contains a raw control byte", i+1)
				}
				switch line[0] {
				case '.', ' ', '\t':
					t.Errorf("line %d starts with %q", i+1, line[0])
				}
				switch line[len(line)-1] {
				case ' ', '\t':
					t.Errorf("line %d ends with %q", i+1, line[len(line)-1])
				}
			}
		})
	}

	// The escaped fixture must actually exercise every escape, or it is
	// testing nothing.
	article := string(readFixture(t, "escaped.yenc"))
	for _, esc := range []struct {
		seq  string
		what string
	}{
		{"=@", "NUL"},
		{"=J", "LF"},
		{"=M", "CR"},
		{"=}", "'='"},
		{"=n", "leading '.'"},
	} {
		if !strings.Contains(article, esc.seq) {
			t.Errorf("escaped.yenc never escapes %s (%q)", esc.what, esc.seq)
		}
	}
}

// allBytes returns every byte value, repeated.
func allBytes(times int) []byte {
	out := make([]byte, 0, 256*times)
	for i := 0; i < times; i++ {
		for b := 0; b < 256; b++ {
			out = append(out, byte(b))
		}
	}
	return out
}

// randomBytes is deterministic: a fixed seed through math/rand's guaranteed
// stable source, so the corpus regenerates byte-for-byte on any machine.
func randomBytes(seed int64, n int) []byte {
	r := rand.New(rand.NewSource(seed))
	out := make([]byte, n)
	for i := range out {
		out[i] = byte(r.Intn(256))
	}
	return out
}

// escapedPayload is built from the input bytes whose *encoded* value is a
// character that must be escaped: value+42 lands on NUL, LF, CR, '=', '.',
// space or TAB.
func escapedPayload() []byte {
	const (
		toDot   = 0x04 // -> '.'
		toEq    = 0x13 // -> '='
		toTab   = 0xDF // -> TAB
		toNUL   = 0xD6 // -> NUL
		toLF    = 0xE0 // -> LF
		toCR    = 0xE3 // -> CR
		toSpace = 0xF6 // -> ' '
	)
	// A '.' first, so the first line would be dot-stuffed if it were not
	// escaped; a space last, so the final line would end in trimmable
	// whitespace.
	out := []byte{toDot, toNUL, toLF, toCR, toEq, toTab, toSpace}
	out = append(out, randomBytes(3, 180)...)
	out = append(out, toDot, toEq, toNUL, toLF, toCR, toTab, toSpace)
	return out
}

func mustEncode(t *testing.T, a yenc.Article, body []byte) []byte {
	t.Helper()
	article, err := yenc.Encode(a, body)
	if err != nil {
		t.Fatalf("Encode(%s): %v", a.Name, err)
	}
	return article
}

// flipPayloadByte changes one payload character to another letter, so the
// article stays structurally valid — same length, no new escape, no new line
// start — and only its CRC is wrong. That is exactly what a flipped bit on
// the wire looks like, and the case a decoder must never wave through.
func flipPayloadByte(t *testing.T, article []byte) []byte {
	t.Helper()
	out := bytes.Clone(article)
	offset := 0
	for _, line := range bytes.SplitAfter(out, []byte("\n")) {
		start := offset
		offset += len(line)
		if bytes.HasPrefix(line, []byte("=y")) {
			continue
		}
		// Start at 1: the first byte of a line is the one position where a
		// new value could change how the line is framed. Skip the byte after
		// an '=' for the same reason — it is half of an escape.
		for i := 1; i < len(line); i++ {
			if line[i-1] == '=' {
				continue
			}
			if c := line[i]; (c >= 'a' && c <= 'y') || (c >= 'A' && c <= 'Y') {
				out[start+i] = c + 1
				return out
			}
		}
	}
	t.Fatal("no plain payload byte to corrupt")
	return nil
}

// truncateArticle cuts the article mid-payload, terminator and all: a
// connection that dropped while the article was still arriving.
func truncateArticle(article []byte) []byte {
	lines := bytes.SplitAfter(article, []byte("\r\n"))
	keep := len(lines) - 3
	if keep < 2 {
		keep = 2
	}
	return bytes.Join(lines[:keep], nil)
}

func replaceOnce(t *testing.T, article []byte, old, replacement string) []byte {
	t.Helper()
	if bytes.Count(article, []byte(old)) != 1 {
		t.Fatalf("expected exactly one %q in the article", old)
	}
	return bytes.Replace(article, []byte(old), []byte(replacement), 1)
}

var pcrcField = regexp.MustCompile(` pcrc32=[0-9a-f]{8}`)

func stripPCRC(t *testing.T, article []byte) []byte {
	t.Helper()
	out := pcrcField.ReplaceAll(article, nil)
	if bytes.Equal(out, article) {
		t.Fatal("article has no pcrc32 field to strip")
	}
	return out
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

// crcOf is the checksum the corpus's expectations are stated in terms of.
func crcOf(b []byte) uint32 { return crc32.ChecksumIEEE(b) }
