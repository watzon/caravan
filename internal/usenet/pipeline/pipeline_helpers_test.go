package pipeline

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/usenet/nntp"
	"github.com/watzon/caravan/internal/usenet/nntptest"
	"github.com/watzon/caravan/internal/usenet/nzb"
	"github.com/watzon/caravan/internal/usenet/yenc"
)

// staged is a file encoded into yEnc articles, ready to register on a fake
// server and to name in an NZB. Building both from the same articles is the
// point: the NZB's segment sizes and message-ids are the real ones, so a test
// exercises the same path a grab does.
type staged struct {
	name     string
	data     []byte
	ids      []string
	articles [][]byte
}

func stage(t *testing.T, name string, data []byte, partSize int) staged {
	t.Helper()
	articles, err := yenc.EncodeFile(name, data, partSize)
	if err != nil {
		t.Fatalf("EncodeFile %s: %v", name, err)
	}
	ids := make([]string, len(articles))
	for i := range articles {
		ids[i] = yenc.MessageID(name, i+1)
	}
	return staged{name: name, data: data, ids: ids, articles: articles}
}

// publish registers every article on a server.
func (s staged) publish(srv *nntptest.Server) {
	for i, a := range s.articles {
		srv.Add(s.ids[i], a)
	}
}

// publishExcept registers every article but the given 1-based part numbers, so
// a server can be missing exactly the articles a test cares about.
func (s staged) publishExcept(srv *nntptest.Server, parts ...int) {
	skip := make(map[int]struct{}, len(parts))
	for _, p := range parts {
		skip[p] = struct{}{}
	}
	for i, a := range s.articles {
		if _, ok := skip[i+1]; ok {
			continue
		}
		srv.Add(s.ids[i], a)
	}
}

// publishDamaged registers every article, with the given 1-based parts flipped
// so they fail their own pcrc32.
func (s staged) publishDamaged(t *testing.T, srv *nntptest.Server, parts ...int) {
	t.Helper()
	bad := make(map[int]struct{}, len(parts))
	for _, p := range parts {
		bad[p] = struct{}{}
	}
	for i, a := range s.articles {
		if _, ok := bad[i+1]; ok {
			a = corruptPayload(t, a)
		}
		srv.Add(s.ids[i], a)
	}
}

// corruptPayload flips one byte of an article's encoded payload, which is what
// a bit-rotted article on a provider looks like: the article arrives whole and
// then does not match the CRC it carries.
func corruptPayload(t *testing.T, article []byte) []byte {
	t.Helper()
	end := bytes.LastIndex(article, []byte("=yend"))
	if end <= 0 {
		t.Fatalf("article has no =yend trailer")
	}
	for i := end - 3; i > 0; i-- {
		c := article[i]
		if c == '\r' || c == '\n' || c == '=' || article[i-1] == '=' {
			continue
		}
		flipped := c ^ 0x20
		if flipped == 0 || flipped == '\r' || flipped == '\n' || flipped == '=' {
			continue
		}
		out := append([]byte(nil), article...)
		out[i] = flipped
		return out
	}
	t.Fatalf("found no payload byte to damage")
	return nil
}

// document renders an NZB naming the staged files and parses it, so every test
// goes through the real parser rather than a hand-built struct.
func document(t *testing.T, files ...staged) *nzb.NZB {
	t.Helper()
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="utf-8"?>` + "\n<nzb>\n")
	for _, f := range files {
		subject := fmt.Sprintf(`Some.Release-GRP [1/%d] - "%s" yEnc (1/%d)`, len(files), f.name, len(f.ids))
		fmt.Fprintf(&b, "  <file poster=\"poster@example.invalid\" date=\"1700000000\" subject=\"%s\">\n", escape(subject))
		b.WriteString("    <groups><group>alt.binaries.test</group></groups>\n    <segments>\n")
		for i, id := range f.ids {
			fmt.Fprintf(&b, "      <segment bytes=\"%d\" number=\"%d\">%s</segment>\n",
				len(f.articles[i]), i+1, escape(id))
		}
		b.WriteString("    </segments>\n  </file>\n")
	}
	b.WriteString("</nzb>\n")

	doc, err := nzb.Parse(strings.NewReader(b.String()))
	if err != nil {
		t.Fatalf("nzb.Parse: %v\n%s", err, b.String())
	}
	return doc
}

func escape(s string) string {
	var b bytes.Buffer
	if err := xml.EscapeText(&b, []byte(s)); err != nil {
		panic(err)
	}
	return b.String()
}

// payload is deterministic pseudo-random data: compressible-looking bytes
// would hide an assembly bug that a repeating pattern happens to paper over.
func payload(seed, n int) []byte {
	out := make([]byte, n)
	x := uint32(seed)*2654435761 + 1
	for i := range out {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		out[i] = byte(x >> 24)
	}
	return out
}

// newServer starts a fake news server that the test closes.
func newServer(t *testing.T) *nntptest.Server {
	t.Helper()
	s, err := nntptest.New(nntptest.Options{})
	if err != nil {
		t.Fatalf("nntptest.New: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// newPool wires the given servers into a MultiPool in the order they are
// passed, which becomes their priority order. Retries are single-shot and the
// backoff is a no-op so a failure test does not spend its budget sleeping.
func newPool(t *testing.T, servers ...*nntptest.Server) *nntp.MultiPool {
	t.Helper()
	cfgs := make([]nntp.ServerConfig, 0, len(servers))
	for i, s := range servers {
		cfgs = append(cfgs, nntp.ServerConfig{
			ID:             int64(i + 1),
			Name:           fmt.Sprintf("server%d", i+1),
			Host:           s.Host(),
			Port:           s.Port(),
			MaxConnections: 4,
			Priority:       i + 1,
			Enabled:        true,
		})
	}
	m, err := nntp.NewMultiPool(cfgs, nntp.Options{
		IdleTimeout: time.Minute,
		Retry:       nntp.Retry{Attempts: 1},
	})
	if err != nil {
		t.Fatalf("NewMultiPool: %v", err)
	}
	t.Cleanup(func() { m.Close() })
	return m
}

// bodyCount counts the BODY commands a server received for one message-id.
func bodyCount(cmds []string, messageID string) int {
	n := 0
	for _, c := range cmds {
		if strings.EqualFold(c, "BODY <"+messageID+">") {
			n++
		}
	}
	return n
}
