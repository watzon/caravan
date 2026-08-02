package nntp

import (
	"testing"
	"time"

	"github.com/watzon/caravan/internal/usenet/nntptest"
)

// testPassword is a recognisable string so a test can assert it never reaches
// an error message (SPEC §12).
const testPassword = "nntp-password-do-not-log"

// testUser is the matching username.
const testUser = "caravan"

// testMessageID is written without angle brackets, the way NZB files carry it.
const testMessageID = "part1of40.abc123@news.example"

// sampleBody is one article body with every framing hazard in it: a line that
// starts with a dot, a line that is two dots, a line that is a single dot
// (which the wire has to stuff so it is not mistaken for the terminator), raw
// bytes over 0x7f, and the yEnc markers a later track will actually parse.
var sampleBody = []byte("=ybegin part=1 line=128 size=1024 name=Some.Release.mkv\r\n" +
	"ordinary encoded line\r\n" +
	".dot leader\r\n" +
	"..two dots\r\n" +
	".\r\n" +
	"after a lone dot\r\n" +
	"\x80\x81\xfe\xff binary-ish\r\n" +
	"=yend size=1024 crc32=deadbeef\r\n")

// newFake starts a fake news server and returns it with a client-side
// configuration and options pointed at it.
func newFake(t *testing.T, o nntptest.Options) (*nntptest.Server, ServerConfig, Options) {
	t.Helper()
	s, err := nntptest.New(o)
	if err != nil {
		t.Fatalf("nntptest.New: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close fake server: %v", err)
		}
	})
	cfg := ServerConfig{
		ID:             1,
		Name:           "fake",
		Host:           s.Host(),
		Port:           s.Port(),
		TLS:            o.TLS,
		Username:       o.Username,
		Password:       o.Password,
		MaxConnections: 4,
		Enabled:        true,
	}
	opts := Options{TLSConfig: s.TLSConfig(), IdleTimeout: time.Minute}
	return s, cfg, opts
}

// authOptions is a fake that wants credentials, which is what every commercial
// provider does.
func authOptions() nntptest.Options {
	return nntptest.Options{Username: testUser, Password: testPassword, RequireAuth: true}
}
