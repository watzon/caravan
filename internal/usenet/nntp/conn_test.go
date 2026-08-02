package nntp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/usenet/nntptest"
)

func TestDialAuthenticatesBeforeFetching(t *testing.T) {
	s, cfg, opts := newFake(t, authOptions())
	s.Add(testMessageID, sampleBody)

	c, err := Dial(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	body, err := c.Body(context.Background(), testMessageID)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if !bytes.Equal(body, sampleBody) {
		t.Fatalf("body =\n%q\nwant\n%q", body, sampleBody)
	}

	// Authentication happens at dial time, not lazily on the first article:
	// a wrong password has to fail when the connection is made, not halfway
	// through a download.
	cmds := s.Commands()
	want := []string{
		"AUTHINFO USER " + testUser,
		"AUTHINFO PASS " + testPassword,
		"BODY <" + testMessageID + ">",
	}
	if len(cmds) != len(want) {
		t.Fatalf("commands = %q, want %q", cmds, want)
	}
	for i := range want {
		if cmds[i] != want[i] {
			t.Fatalf("command %d = %q, want %q", i, cmds[i], want[i])
		}
	}
	if got := s.Stats().Auths; got != 1 {
		t.Fatalf("auths = %d, want 1", got)
	}
}

func TestDialRejectsBadCredentialsWithoutLeakingThem(t *testing.T) {
	s, cfg, opts := newFake(t, authOptions())
	s.SetRejectAuth(true)

	_, err := Dial(context.Background(), cfg, opts)
	if err == nil {
		t.Fatal("Dial succeeded with rejected credentials")
	}
	if !errors.Is(err, ErrAuthFailed) {
		t.Fatalf("err = %v, want ErrAuthFailed", err)
	}
	// A refused password does not become a right one on the next try.
	if Retryable(err) {
		t.Fatalf("Retryable(%v) = true, want false", err)
	}
	// SPEC §12: the credential never reaches an error string.
	if strings.Contains(err.Error(), testPassword) {
		t.Fatalf("error text leaked the password: %v", err)
	}
}

func TestDialSurfacesARefusedGreeting(t *testing.T) {
	_, cfg, opts := newFake(t, nntptest.Options{Greeting: "400 load shedding, try later"})

	_, err := Dial(context.Background(), cfg, opts)
	if err == nil {
		t.Fatal("Dial succeeded against a refusing server")
	}
	var pe *ProtocolError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v (%T), want *ProtocolError", err, err)
	}
	if pe.Code != 400 || pe.Command != "greeting" {
		t.Fatalf("protocol error = %+v", pe)
	}
	// 4xx is "temporarily cannot", which is exactly what a backoff is for.
	if !Retryable(err) {
		t.Fatalf("Retryable(%v) = false, want true", err)
	}
}

// The dot is NNTP's escape and its terminator at once. Getting this wrong
// corrupts an article silently, which is the one failure mode phase 7 must not
// have: yEnc would decode the damage into the file.
func TestBodyUnstuffsDots(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	s.Add(testMessageID, sampleBody)

	c, err := Dial(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	body, err := c.Body(context.Background(), testMessageID)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if !bytes.Equal(body, sampleBody) {
		t.Fatalf("body =\n%q\nwant\n%q", body, sampleBody)
	}
	// CRLF survives: yEnc's framing counts the line terminators.
	if bytes.Contains(bytes.ReplaceAll(body, []byte("\r\n"), nil), []byte("\n")) {
		t.Fatal("body contains a bare LF: line endings were rewritten")
	}
}

func TestBodyHandlesEmptyAndSingleLineArticles(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	s.Add("empty@news", nil)
	s.Add("oneline@news", []byte("just one line\r\n"))
	s.Add("dotonly@news", []byte(".\r\n"))

	c, err := Dial(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	for _, tc := range []struct {
		id   string
		want []byte
	}{
		{"empty@news", []byte{}},
		{"oneline@news", []byte("just one line\r\n")},
		{"dotonly@news", []byte(".\r\n")},
	} {
		got, err := c.Body(context.Background(), tc.id)
		if err != nil {
			t.Fatalf("Body(%s): %v", tc.id, err)
		}
		if !bytes.Equal(got, tc.want) {
			t.Fatalf("Body(%s) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

func TestBodyReportsAMissingArticleAndKeepsTheConnection(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	s.Add(testMessageID, sampleBody)

	c, err := Dial(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	_, err = c.Body(context.Background(), "gone@news.example")
	if !errors.Is(err, ErrArticleNotFound) {
		t.Fatalf("err = %v, want ErrArticleNotFound", err)
	}
	// Retrying the same server for an article it does not carry is wasted
	// time; the backup account is the answer.
	if Retryable(err) {
		t.Fatalf("Retryable(%v) = true, want false", err)
	}
	// 430 is an answer, not a broken stream: the connection is still good.
	body, err := c.Body(context.Background(), testMessageID)
	if err != nil {
		t.Fatalf("Body after 430: %v", err)
	}
	if !bytes.Equal(body, sampleBody) {
		t.Fatalf("body after 430 = %q", body)
	}
}

// Some servers only demand AUTHINFO when a reader command arrives.
func TestBodyReauthenticatesOn480(t *testing.T) {
	o := authOptions()
	o.RequireAuth = false
	s, cfg, opts := newFake(t, o)
	s.Add(testMessageID, sampleBody)
	s.SetFault(nntptest.Fault{Bodies: 1, Mode: nntptest.FaultStatus, Code: 480})

	c, err := Dial(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	body, err := c.Body(context.Background(), testMessageID)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if !bytes.Equal(body, sampleBody) {
		t.Fatalf("body = %q", body)
	}
	// Once at dial, once after the 480.
	if got := s.Stats().Auths; got != 2 {
		t.Fatalf("auths = %d, want 2", got)
	}
}

func TestBodyTruncatedMidStreamIsATransientFailure(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	s.Add(testMessageID, sampleBody)
	s.SetFault(nntptest.Fault{Bodies: 1, Mode: nntptest.FaultDropMidBody})

	c, err := Dial(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	body, err := c.Body(context.Background(), testMessageID)
	if err == nil {
		t.Fatalf("Body returned %d bytes of a truncated article", len(body))
	}
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err = %v, want io.ErrUnexpectedEOF", err)
	}
	if !Retryable(err) {
		t.Fatalf("Retryable(%v) = false, want true", err)
	}
	if !c.broken {
		t.Fatal("connection was not marked broken after a truncated body")
	}
}

func TestBodyRefusesAnArticleOverTheLimit(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	opts.MaxArticleBytes = 64
	s.Add(testMessageID, bytes.Repeat([]byte("padding line\r\n"), 64))

	c, err := Dial(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	if _, err := c.Body(context.Background(), testMessageID); !errors.Is(err, ErrArticleTooLarge) {
		t.Fatalf("err = %v, want ErrArticleTooLarge", err)
	}
}

func TestBodyStopsWhenTheCallerCancels(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	s.Add(testMessageID, sampleBody)

	entered := make(chan struct{})
	release := make(chan struct{})
	s.SetBodyHook(func(string) {
		close(entered)
		<-release
	})
	defer close(release)

	c, err := Dial(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := c.Body(ctx, testMessageID)
		done <- err
	}()

	<-entered
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
		if Retryable(err) {
			t.Fatalf("Retryable(%v) = true, want false", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Body did not return after the context was cancelled")
	}
}

// A message-id comes out of a downloaded NZB. One containing CRLF would append
// a command to this connection, so it is rejected before it is written.
func TestMessageIDIsNormalisedAndSanitised(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	s.Add(testMessageID, sampleBody)

	c, err := Dial(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	for _, id := range []string{testMessageID, "<" + testMessageID + ">", "  " + testMessageID + "  "} {
		if _, err := c.Body(context.Background(), id); err != nil {
			t.Fatalf("Body(%q): %v", id, err)
		}
	}
	for _, cmd := range s.Commands() {
		if strings.HasPrefix(cmd, "BODY ") && cmd != "BODY <"+testMessageID+">" {
			t.Fatalf("command %q: message id was not normalised", cmd)
		}
	}

	before := len(s.Commands())
	for _, bad := range []string{"", "   ", "evil@news>\r\nQUIT", "has space@news"} {
		if _, err := c.Body(context.Background(), bad); err == nil {
			t.Fatalf("Body(%q) succeeded, want a rejection", bad)
		}
	}
	if got := len(s.Commands()); got != before {
		t.Fatalf("%d commands reached the server from rejected ids", got-before)
	}
}

func TestQuitSaysGoodbye(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})

	c, err := Dial(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	if err := c.Quit(context.Background()); err != nil {
		t.Fatalf("Quit: %v", err)
	}
	cmds := s.Commands()
	if len(cmds) != 1 || cmds[0] != "QUIT" {
		t.Fatalf("commands = %q, want [QUIT]", cmds)
	}
}

func TestDialOverTLS(t *testing.T) {
	o := authOptions()
	o.TLS = true
	s, cfg, opts := newFake(t, o)
	s.Add(testMessageID, sampleBody)

	if !cfg.TLS {
		t.Fatal("fixture did not configure TLS")
	}
	c, err := Dial(context.Background(), cfg, opts)
	if err != nil {
		t.Fatalf("Dial: %v", err)
	}
	defer c.Close()

	body, err := c.Body(context.Background(), testMessageID)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	if !bytes.Equal(body, sampleBody) {
		t.Fatalf("body = %q", body)
	}
}

func TestRetryableClassifiesResponses(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"missing article", &ProtocolError{Code: 430}, false},
		{"no such article number", &ProtocolError{Code: 423}, false},
		{"auth required", &ProtocolError{Code: 480}, false},
		{"auth failed", &ProtocolError{Code: 481}, false},
		{"service unavailable", &ProtocolError{Code: 400}, true},
		{"internal fault", &ProtocolError{Code: 403}, true},
		{"unknown command", &ProtocolError{Code: 500}, false},
		{"permanent unavailable", &ProtocolError{Code: 502}, false},
		{"truncated body", io.ErrUnexpectedEOF, true},
		{"cancelled", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"pool closed", ErrPoolClosed, false},
		{"article too large", ErrArticleTooLarge, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Retryable(tc.err); got != tc.want {
				t.Fatalf("Retryable(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
