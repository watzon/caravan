package nzb

import (
	"errors"
	"strings"
	"testing"
)

// The size cap is 64 MB, which is not a size a test should build. These tests
// lower docLimit instead, so the boundary, the one place an off-by-one would
// reject legitimate NZBs, is exercised on documents measured in bytes.
func withLimit(t *testing.T, limit int64) {
	t.Helper()
	previous := docLimit
	docLimit = limit
	t.Cleanup(func() { docLimit = previous })
}

// document returns a valid NZB padded with an XML comment to exactly n bytes.
func document(t *testing.T, n int) string {
	t.Helper()
	const head = `<nzb><file subject="x.mkv"><segments><segment bytes="1" number="1">a@b</segment></segments></file><!--`
	const tail = `--></nzb>`
	pad := n - len(head) - len(tail)
	if pad < 0 {
		t.Fatalf("cannot build a %d byte document; the skeleton is %d bytes", n, len(head)+len(tail))
	}
	return head + strings.Repeat("x", pad) + tail
}

func TestParseDocumentSizeBoundary(t *testing.T) {
	const limit = 4096
	withLimit(t, limit)

	t.Run("exactly at the limit parses", func(t *testing.T) {
		doc, err := Parse(strings.NewReader(document(t, limit)))
		if err != nil {
			t.Fatalf("Parse(%d bytes) with a %d byte limit: %v", limit, limit, err)
		}
		if got, want := len(doc.Files), 1; got != want {
			t.Fatalf("files = %d, want %d", got, want)
		}
	})

	t.Run("one byte over the limit is rejected", func(t *testing.T) {
		_, err := Parse(strings.NewReader(document(t, limit+1)))
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("Parse(%d bytes) error = %v, want ErrTooLarge", limit+1, err)
		}
	})

	t.Run("an endless document is rejected rather than read forever", func(t *testing.T) {
		endless := strings.NewReader(document(t, limit)[:limit-9]) // drop the closing tags
		_, err := Parse(&repeatingReader{prefix: endless, fill: 'x'})
		if !errors.Is(err, ErrTooLarge) {
			t.Fatalf("Parse(endless) error = %v, want ErrTooLarge", err)
		}
	})
}

// repeatingReader reads a prefix and then never stops, which is what a hostile
// indexer response looks like.
type repeatingReader struct {
	prefix *strings.Reader
	fill   byte
}

func (r *repeatingReader) Read(p []byte) (int, error) {
	if r.prefix.Len() > 0 {
		return r.prefix.Read(p)
	}
	for i := range p {
		p[i] = r.fill
	}
	return len(p), nil
}

// TestDocumentLimitIsTheShippedConstant is the wiring check the boundary tests
// cannot make: they run with docLimit lowered, so something has to assert that
// what ships is MaxDocumentBytes.
func TestDocumentLimitIsTheShippedConstant(t *testing.T) {
	if docLimit != MaxDocumentBytes {
		t.Fatalf("docLimit = %d, want MaxDocumentBytes (%d)", docLimit, MaxDocumentBytes)
	}
}
