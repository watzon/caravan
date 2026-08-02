package nzb_test

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/usenet/nzb"
)

func load(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func parseFixture(t *testing.T, name string) *nzb.NZB {
	t.Helper()
	doc, err := nzb.Parse(bytes.NewReader(load(t, name)))
	if err != nil {
		t.Fatalf("Parse(%s): %v", name, err)
	}
	return doc
}

func TestParseRelease(t *testing.T) {
	doc := parseFixture(t, "release.nzb")

	if got, want := len(doc.Files), 4; got != want {
		t.Fatalf("files = %d, want %d", got, want)
	}
	if got, want := doc.Meta["name"], "Big Buck Bunny 2008 1080p BluRay x264-CARAVAN"; got != want {
		t.Errorf("meta name = %q, want %q", got, want)
	}
	if got, want := doc.Meta["category"], "Movies > HD"; got != want {
		t.Errorf("meta category = %q, want %q", got, want)
	}

	media := doc.Files[0]
	if got, want := media.Filename(), "big.buck.bunny.2008.1080p.bluray.x264-caravan.mkv"; got != want {
		t.Errorf("media filename = %q, want %q", got, want)
	}
	if media.IsPar2() {
		t.Error("media file reported as par2")
	}
	if got, want := media.Groups, []string{"alt.binaries.movies.divx", "alt.binaries.hdtv.x264"}; !equalStrings(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
	if got, want := media.Posted, time.Unix(1740000000, 0).UTC(); !got.Equal(want) {
		t.Errorf("posted = %v, want %v", got, want)
	}
	if got, want := media.Poster, "poster@example.invalid (poster)"; got != want {
		t.Errorf("poster = %q, want %q", got, want)
	}

	// The fixture lists segment 4 before segment 3 on purpose: assembly
	// depends on part order, so the parser must impose it.
	if got, want := len(media.Segments), 5; got != want {
		t.Fatalf("segments = %d, want %d", got, want)
	}
	for i, s := range media.Segments {
		if s.Number != i+1 {
			t.Fatalf("segment %d has number %d, want %d (segments not sorted)", i, s.Number, i+1)
		}
	}
	if got, want := media.Segments[2].MessageID, "bbb.part003.a1b2c3@news.example.invalid"; got != want {
		t.Errorf("segment 3 message-id = %q, want %q", got, want)
	}
	if got, want := media.Segments[4].Bytes, int64(221184); got != want {
		t.Errorf("segment 5 bytes = %d, want %d", got, want)
	}
	if got, want := media.Bytes(), int64(768000*4+221184); got != want {
		t.Errorf("file bytes = %d, want %d", got, want)
	}
}

func TestParseReleaseSplitsPar2(t *testing.T) {
	doc := parseFixture(t, "release.nzb")

	content := doc.ContentFiles()
	if got, want := len(content), 1; got != want {
		t.Fatalf("content files = %d, want %d", got, want)
	}
	if !strings.HasSuffix(content[0].Filename(), ".mkv") {
		t.Errorf("content file = %q, want the mkv", content[0].Filename())
	}

	par2 := doc.Par2Files()
	if got, want := len(par2), 3; got != want {
		t.Fatalf("par2 files = %d, want %d", got, want)
	}
	// One index file plus two recovery volumes worth 1 and 2 blocks.
	if got, want := doc.RecoveryBlocks(), 3; got != want {
		t.Errorf("recovery blocks = %d, want %d", got, want)
	}

	set := nzb.Par2SetName(par2[0].Filename())
	for _, f := range par2 {
		if got := nzb.Par2SetName(f.Filename()); got != set {
			t.Errorf("par2 %q in set %q, want %q", f.Filename(), got, set)
		}
	}

	wantTotal := int64(768000*4+221184) + 40960 + 786432 + 786432*2
	if got := doc.TotalBytes(); got != wantTotal {
		t.Errorf("total bytes = %d, want %d", got, wantTotal)
	}
}

func TestParseUnquotedSubjects(t *testing.T) {
	doc := parseFixture(t, "unquoted-subjects.nzb")

	if got, want := len(doc.Files), 3; got != want {
		t.Fatalf("files = %d, want %d", got, want)
	}
	if got, want := doc.Files[0].Filename(), "ab12cd34ef.mkv"; got != want {
		t.Errorf("file 1 name = %q, want %q", got, want)
	}
	if got, want := doc.Files[1].Filename(), "ab12cd34ef.vol000+02.PAR2"; got != want {
		t.Errorf("file 2 name = %q, want %q", got, want)
	}
	if !doc.Files[1].IsPar2() {
		t.Error("uppercase .PAR2 not recognised as par2")
	}
	if got, want := doc.RecoveryBlocks(), 2; got != want {
		t.Errorf("recovery blocks = %d, want %d", got, want)
	}
	// No extension anywhere: the subject itself is the best answer.
	if got, want := doc.Files[2].Filename(), "no extension here at all"; got != want {
		t.Errorf("file 3 name = %q, want %q", got, want)
	}
	// Angle brackets belong to the wire format, not to the stored id.
	if got, want := doc.Files[2].Segments[0].MessageID, "unq.bracketed@news.example.invalid"; got != want {
		t.Errorf("message-id = %q, want %q", got, want)
	}
	// A file without <groups> is still fetchable by message-id.
	if len(doc.Files[2].Groups) != 0 {
		t.Errorf("groups = %v, want none", doc.Files[2].Groups)
	}
	// A file without a date parses with a zero time rather than failing.
	if !doc.Files[2].Posted.IsZero() {
		t.Errorf("posted = %v, want zero", doc.Files[2].Posted)
	}
}

func TestParseLatin1(t *testing.T) {
	doc := parseFixture(t, "latin1.nzb")

	if got, want := len(doc.Files), 1; got != want {
		t.Fatalf("files = %d, want %d", got, want)
	}
	if got, want := doc.Files[0].Filename(), "Amélie.2001.1080p.mkv"; got != want {
		t.Errorf("filename = %q, want %q", got, want)
	}
}

func TestParseRejects(t *testing.T) {
	tests := []struct {
		name     string
		fixture  string
		want     error
		contains string
	}{
		{name: "truncated xml", fixture: "broken-xml.nzb", want: nzb.ErrMalformed},
		{name: "wrong root element", fixture: "wrong-root.nzb", want: nzb.ErrMalformed, contains: "<rss>"},
		{name: "no files", fixture: "no-files.nzb", want: nzb.ErrMalformed, contains: "no files"},
		{name: "file without segments", fixture: "no-segments.nzb", want: nzb.ErrMalformed, contains: "no segments"},
		{name: "empty message id", fixture: "empty-message-id.nzb", want: nzb.ErrMalformed, contains: "empty message-id"},
		{name: "non numeric segment number", fixture: "bad-number.nzb", want: nzb.ErrMalformed, contains: "not a number"},
		{name: "zero segment number", fixture: "zero-number.nzb", want: nzb.ErrMalformed, contains: "not positive"},
		{name: "negative bytes", fixture: "bad-bytes.nzb", want: nzb.ErrMalformed, contains: "negative"},
		{name: "duplicate segment number", fixture: "duplicate-segment.nzb", want: nzb.ErrMalformed, contains: "duplicate segment number 1"},
		{name: "empty subject", fixture: "empty-subject.nzb", want: nzb.ErrMalformed, contains: "empty subject"},
		{name: "control character in message id", fixture: "control-char-id.nzb", want: nzb.ErrMalformed, contains: "illegal character"},
		{name: "unsupported charset", fixture: "unknown-charset.nzb", want: nzb.ErrUnsupportedCharset, contains: "Shift_JIS"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc, err := nzb.Parse(bytes.NewReader(load(t, tt.fixture)))
			if err == nil {
				t.Fatalf("Parse(%s) = %+v, want error", tt.fixture, doc)
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("Parse(%s) error = %v, want %v", tt.fixture, err, tt.want)
			}
			if tt.contains != "" && !strings.Contains(err.Error(), tt.contains) {
				t.Errorf("Parse(%s) error = %q, want it to mention %q", tt.fixture, err, tt.contains)
			}
		})
	}
}

func TestParseErrorNamesTheFileAndSegment(t *testing.T) {
	// A useful error says which segment of which file is bad; "malformed nzb"
	// alone sends someone to a hex editor.
	_, err := nzb.Parse(bytes.NewReader(load(t, "empty-message-id.nzb")))
	if err == nil {
		t.Fatal("want error")
	}
	for _, want := range []string{"file 1", "a.mkv", "segment 2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want it to mention %q", err, want)
		}
	}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
