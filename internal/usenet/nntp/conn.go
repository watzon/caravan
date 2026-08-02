// Package nntp is Caravan's news-server client: the transport half of the
// embedded Usenet engine (SPEC §5.1, PLAN phase 7 task 2).
//
// It does one useful thing — hand back the body of an article named by
// message-id — and does it against a set of servers rather than one, because
// that is what Usenet reliability is made of: an article missing from your
// main provider is usually present on a cheap block account, and asking the
// backup is the difference between a download that repairs and one that fails.
//
// The three layers stack:
//
//   - Conn is one authenticated socket. It knows the response codes and how a
//     multi-line block is dot-stuffed, and nothing about policy.
//   - Pool is one server's connections, capped at that server's limit, reused
//     while they are healthy and thrown away the moment they are not. Going
//     over a provider's connection cap is not slow, it is refused, so the cap
//     is enforced here rather than hoped for upstream.
//   - MultiPool is the entry point everything above uses. FetchBody asks the
//     highest-priority server, retries transient failures with capped backoff,
//     and fails over to the next server. It distinguishes "this article is
//     missing" from "these servers are broken", because the first is par2's
//     problem and the second is not.
//
// Like internal/download and internal/clients, this package does not import
// internal/store: a server is a plain ServerConfig, so the whole transport is
// testable against internal/usenet/nntptest with no database anywhere.
package nntp

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
)

// Errors callers act on. Everything else arrives as *ProtocolError or as the
// underlying network error.
var (
	// ErrArticleNotFound means a server answered that it does not have the
	// article (430, or 420/423 from a server that indexes differently).
	//
	// It is a sentinel because failover turns on it: an article the top server
	// is missing is the normal reason to ask a backup, and an article every
	// server is missing is a hole par2 has to fill rather than an error to
	// retry.
	ErrArticleNotFound = errors.New("nntp: article not found")
	// ErrAuthFailed means the server rejected the configured credentials. It
	// never carries the credentials that were rejected (SPEC §12), and it is
	// never retried: a wrong password stays wrong.
	ErrAuthFailed = errors.New("nntp: news server rejected the credentials")
	// ErrArticleTooLarge means the body ran past Options.MaxArticleBytes. It
	// is a guard against a server that never sends the terminator, not a
	// limit users are expected to reach: Usenet articles are under a megabyte.
	ErrArticleTooLarge = errors.New("nntp: article exceeds the maximum size")
	// ErrPoolClosed means the pool was closed while a fetch was outstanding or
	// before one started.
	ErrPoolClosed = errors.New("nntp: connection pool is closed")
	// ErrNoServers means no enabled news server was configured, which is the
	// "you have not finished setting Caravan up" answer rather than a failure.
	ErrNoServers = errors.New("nntp: no enabled news servers configured")
)

// ProtocolError is a response code the client could not use.
//
// Command is the command that produced it with any credential argument
// stripped — "AUTHINFO PASS" never carries the password (SPEC §12) — and
// Message is the server's own text.
type ProtocolError struct {
	// Server is the server's Label, never its credentials.
	Server string
	// Command is the command that drew this response, redacted for AUTHINFO.
	Command string
	// Code is the three-digit NNTP response code.
	Code int
	// Message is the server's text after the code, which may be empty.
	Message string
}

func (e *ProtocolError) Error() string {
	if e.Message != "" {
		return fmt.Sprintf("nntp: %s: %s: %d %s", e.Server, e.Command, e.Code, e.Message)
	}
	return fmt.Sprintf("nntp: %s: %s: %d", e.Server, e.Command, e.Code)
}

// Unwrap maps the codes that have a meaning callers branch on onto sentinels,
// so a caller can use errors.Is without knowing NNTP's numbering.
func (e *ProtocolError) Unwrap() error {
	switch e.Code {
	case 420, 423, 430:
		return ErrArticleNotFound
	case 480, 481, 482:
		return ErrAuthFailed
	}
	return nil
}

// Retryable reports whether trying the same server again could plausibly
// work.
//
// The rule is the shape of NNTP's numbering: 4xx is "temporarily cannot", 5xx
// is "will not". The exceptions are the codes that mean something specific —
// a missing article and a refused login are both 4xx and neither improves by
// asking twice — and a cancelled context, which is the caller leaving.
func Retryable(err error) bool {
	switch {
	case err == nil:
		return false
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return false
	case errors.Is(err, ErrArticleNotFound), errors.Is(err, ErrAuthFailed):
		return false
	case errors.Is(err, ErrArticleTooLarge), errors.Is(err, ErrPoolClosed), errors.Is(err, ErrNoServers):
		return false
	}
	var pe *ProtocolError
	if errors.As(err, &pe) {
		return pe.Code >= 400 && pe.Code < 500
	}
	// What is left is a dial failure, a reset connection or a truncated body:
	// the transient failures a news server hands out all day.
	return true
}

// Options tunes the transport. The zero value is usable; every field has a
// default and Pool ignores the two MultiPool-only ones.
type Options struct {
	// TLSConfig is the base configuration for TLS servers. It is cloned and
	// its ServerName filled in from the host, so one config can be shared.
	// Nil means the system roots with TLS 1.2 as the floor.
	TLSConfig *tls.Config
	// DialTimeout bounds establishing the connection, the TLS handshake, the
	// greeting and the AUTHINFO exchange. Zero uses DefaultDialTimeout.
	DialTimeout time.Duration
	// IdleTimeout is how long a pooled connection is kept before it is closed
	// rather than reused. Zero uses DefaultIdleTimeout; negative never expires
	// a connection.
	IdleTimeout time.Duration
	// MaxArticleBytes caps one article body. Zero uses DefaultMaxArticleBytes.
	MaxArticleBytes int64
	// Retry is MultiPool's per-server retry policy. Pool ignores it.
	Retry Retry
	// Sleep is how MultiPool waits between retries. Nil sleeps on a timer;
	// tests replace it to assert the backoff without spending it.
	Sleep func(ctx context.Context, d time.Duration) error
}

// Defaults for Options.
const (
	// DefaultDialTimeout bounds the whole handshake. Generous, because a
	// provider on another continent behind TLS is not fast.
	DefaultDialTimeout = 30 * time.Second
	// DefaultIdleTimeout is how long an unused connection is kept.
	//
	// Under a provider's own idle timeout, which is commonly a few minutes:
	// reusing a connection the server has already dropped costs a wasted
	// round trip on the next article, and articles are fetched in the
	// thousands.
	DefaultIdleTimeout = 90 * time.Second
	// DefaultMaxArticleBytes bounds one body. Real articles are well under a
	// megabyte; this is the runaway guard, not a policy.
	DefaultMaxArticleBytes = 32 << 20
	// maxStatusLine bounds a single response line.
	maxStatusLine = 8 << 10
	// readBufSize is the per-connection read buffer. Article lines are short,
	// but a big buffer means far fewer syscalls over a 700 KB body.
	readBufSize = 64 << 10
)

func (o Options) normalized() Options {
	if o.DialTimeout == 0 {
		o.DialTimeout = DefaultDialTimeout
	}
	if o.IdleTimeout == 0 {
		o.IdleTimeout = DefaultIdleTimeout
	}
	if o.MaxArticleBytes <= 0 {
		o.MaxArticleBytes = DefaultMaxArticleBytes
	}
	o.Retry = o.Retry.normalized()
	if o.Sleep == nil {
		o.Sleep = sleepCtx
	}
	return o
}

// Conn is one connection to a news server, authenticated if the server
// configuration carries credentials.
//
// It is not safe for concurrent use: one command occupies the socket until its
// response has been read. Pool is what makes concurrency safe.
type Conn struct {
	cfg        ServerConfig
	nc         net.Conn
	br         *bufio.Reader
	bw         *bufio.Writer
	maxArticle int64

	authed bool
	// broken records that the stream can no longer be trusted — a read error,
	// a write error, a response that did not parse, or a body that stopped
	// early. A broken connection is closed rather than pooled.
	broken bool
}

// Dial opens and prepares a connection: TCP (or TLS), greeting, and AUTHINFO
// when the configuration has a username.
//
// Authenticating eagerly rather than waiting for a 480 costs one round trip
// per connection and buys a much better failure: a wrong password is reported
// when the connection is made, not on some article halfway through a download.
func Dial(ctx context.Context, cfg ServerConfig, opts Options) (*Conn, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.normalized()
	opts = opts.normalized()

	ctx, cancel := context.WithTimeout(ctx, opts.DialTimeout)
	defer cancel()

	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", cfg.Address())
	if err != nil {
		return nil, fmt.Errorf("nntp: dial %s: %w", cfg.Label(), err)
	}
	if cfg.TLS {
		tc := tls.Client(nc, tlsConfigFor(opts.TLSConfig, cfg.Host))
		if err := tc.HandshakeContext(ctx); err != nil {
			nc.Close()
			return nil, fmt.Errorf("nntp: tls handshake with %s: %w", cfg.Label(), err)
		}
		nc = tc
	}

	c := &Conn{
		cfg:        cfg,
		nc:         nc,
		br:         bufio.NewReaderSize(nc, readBufSize),
		bw:         bufio.NewWriter(nc),
		maxArticle: opts.MaxArticleBytes,
	}
	if err := c.greet(ctx); err != nil {
		c.Close()
		return nil, err
	}
	if cfg.Username != "" {
		if err := c.authenticate(ctx); err != nil {
			c.Close()
			return nil, err
		}
	}
	return c, nil
}

// tlsConfigFor clones base and fills in what the dial needs.
func tlsConfigFor(base *tls.Config, host string) *tls.Config {
	var tc *tls.Config
	if base != nil {
		tc = base.Clone()
	} else {
		tc = &tls.Config{}
	}
	if tc.ServerName == "" {
		tc.ServerName = host
	}
	if tc.MinVersion == 0 {
		tc.MinVersion = tls.VersionTLS12
	}
	return tc
}

// Body fetches the body of one article. The message-id may be given with or
// without its angle brackets, as NZB files write it both ways.
//
// The returned bytes are the article exactly as the server sent it, with the
// dot-stuffing removed and the terminating line dropped: CRLF line endings are
// preserved because yEnc's own framing counts on them.
func (c *Conn) Body(ctx context.Context, messageID string) ([]byte, error) {
	id, err := messageIDArg(messageID)
	if err != nil {
		return nil, err
	}
	body, err := c.body(ctx, id)
	// A server may only demand AUTHINFO when a reader command arrives. If it
	// does and we have credentials, log in and ask once more; a second 480 is
	// a real refusal.
	var pe *ProtocolError
	if errors.As(err, &pe) && pe.Code == 480 && c.cfg.Username != "" && !c.broken {
		if aerr := c.authenticate(ctx); aerr != nil {
			return nil, aerr
		}
		return c.body(ctx, id)
	}
	return body, err
}

func (c *Conn) body(ctx context.Context, id string) ([]byte, error) {
	code, msg, err := c.command(ctx, "BODY "+id, "BODY "+id)
	if err != nil {
		return nil, err
	}
	if code != 222 {
		return nil, &ProtocolError{Server: c.cfg.Label(), Command: "BODY " + id, Code: code, Message: msg}
	}
	var body []byte
	err = c.guard(ctx, func() error {
		var err error
		body, err = c.readBody()
		return err
	})
	if err != nil {
		// The stream is mid-block and cannot be resynchronised.
		c.broken = true
		return nil, fmt.Errorf("nntp: %s: body %s: %w", c.cfg.Label(), id, err)
	}
	return body, nil
}

// Quit says goodbye and closes the connection. The error is the protocol's,
// not the socket's: the connection is closed either way.
func (c *Conn) Quit(ctx context.Context) error {
	defer c.Close()
	if c.broken {
		return nil
	}
	code, msg, err := c.command(ctx, "QUIT", "QUIT")
	if err != nil {
		return err
	}
	if code != 205 {
		return &ProtocolError{Server: c.cfg.Label(), Command: "QUIT", Code: code, Message: msg}
	}
	return nil
}

// Close drops the connection without a QUIT. It is safe to call more than
// once.
func (c *Conn) Close() error {
	c.broken = true
	return c.nc.Close()
}

// greet reads the banner. 200 and 201 differ only in whether posting is
// allowed, which Caravan never does; anything else is a server turning us
// away.
func (c *Conn) greet(ctx context.Context) error {
	var code int
	var msg string
	err := c.guard(ctx, func() error {
		var err error
		code, msg, err = c.readStatus()
		return err
	})
	if err != nil {
		return fmt.Errorf("nntp: greeting from %s: %w", c.cfg.Label(), err)
	}
	if code != 200 && code != 201 {
		return &ProtocolError{Server: c.cfg.Label(), Command: "greeting", Code: code, Message: msg}
	}
	return nil
}

// authenticate runs the AUTHINFO USER/PASS exchange (RFC 4643).
//
// The command labels carried into errors are the verbs alone: the password is
// an argument of a command whose text would otherwise end up in a log line
// (SPEC §12).
func (c *Conn) authenticate(ctx context.Context) error {
	c.authed = false
	code, msg, err := c.command(ctx, "AUTHINFO USER "+c.cfg.Username, "AUTHINFO USER")
	if err != nil {
		return err
	}
	switch code {
	case 281:
		// Some servers accept a username alone.
		c.authed = true
		return nil
	case 381:
		// Password wanted, which is the normal path.
	default:
		return &ProtocolError{Server: c.cfg.Label(), Command: "AUTHINFO USER", Code: code, Message: msg}
	}

	code, msg, err = c.command(ctx, "AUTHINFO PASS "+c.cfg.Password, "AUTHINFO PASS")
	if err != nil {
		return err
	}
	if code != 281 {
		return &ProtocolError{Server: c.cfg.Label(), Command: "AUTHINFO PASS", Code: code, Message: msg}
	}
	c.authed = true
	return nil
}

// command writes one command line and reads its status line. label is what
// errors name the command, which is not always the command itself.
func (c *Conn) command(ctx context.Context, line, label string) (int, string, error) {
	if c.broken {
		return 0, "", fmt.Errorf("nntp: %s: %s: %w", c.cfg.Label(), label, net.ErrClosed)
	}
	var code int
	var msg string
	err := c.guard(ctx, func() error {
		if _, err := c.bw.WriteString(line + "\r\n"); err != nil {
			return err
		}
		if err := c.bw.Flush(); err != nil {
			return err
		}
		var err error
		code, msg, err = c.readStatus()
		return err
	})
	if err != nil {
		c.broken = true
		return 0, "", fmt.Errorf("nntp: %s: %s: %w", c.cfg.Label(), label, err)
	}
	return code, msg, nil
}

// guard applies ctx to the raw socket for the length of fn.
//
// net.Conn has deadlines, not contexts, so cancellation is a watcher that
// pokes the deadline into the past to unblock the read. The context error only
// replaces fn's when fn actually failed, so a fetch that finished in the same
// instant the caller gave up is still a fetch that finished.
func (c *Conn) guard(ctx context.Context, fn func() error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = c.nc.SetDeadline(deadline)
	} else {
		_ = c.nc.SetDeadline(time.Time{})
	}
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		select {
		case <-ctx.Done():
			// Any past instant unblocks a read in progress. Not the zero time,
			// which means "no deadline".
			_ = c.nc.SetDeadline(time.Unix(1, 0))
		case <-stop:
		}
	}()

	err := fn()

	close(stop)
	<-done
	_ = c.nc.SetDeadline(time.Time{})

	if err != nil && ctx.Err() != nil {
		c.broken = true
		return ctx.Err()
	}
	return err
}

// readStatus reads and parses one response line.
func (c *Conn) readStatus() (int, string, error) {
	line, err := c.readLine(maxStatusLine)
	if err != nil {
		return 0, "", err
	}
	text := string(trimEOL(line))
	if len(text) < 3 {
		return 0, "", fmt.Errorf("nntp: unreadable response %q", text)
	}
	code, err := strconv.Atoi(text[:3])
	if err != nil || code < 100 || code > 599 {
		return 0, "", fmt.Errorf("nntp: unreadable response %q", text)
	}
	return code, strings.TrimSpace(text[3:]), nil
}

// readLine reads one CRLF-terminated line, including its terminator.
func (c *Conn) readLine(limit int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := c.br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			line = append(line, chunk...)
			if len(line) > limit {
				return nil, fmt.Errorf("nntp: response line over %d bytes", limit)
			}
			continue
		}
		if err != nil {
			return nil, eofAsUnexpected(err)
		}
		line = append(line, chunk...)
		if len(line) > limit {
			return nil, fmt.Errorf("nntp: response line over %d bytes", limit)
		}
		return line, nil
	}
}

// readBody reads a multi-line block: dot-stuffing removed, the terminating
// "." line dropped, CRLF endings left exactly as they arrived.
//
// It accumulates into one buffer and unstuffs in place rather than assembling
// per-line slices, because a segment is thousands of lines and this runs for
// every article of every download.
func (c *Conn) readBody() ([]byte, error) {
	body := make([]byte, 0, 8<<10)
	// lineStart is where the line currently being read begins in body.
	lineStart := 0
	for {
		chunk, err := c.br.ReadSlice('\n')
		partial := errors.Is(err, bufio.ErrBufferFull)
		if err != nil && !partial {
			return nil, eofAsUnexpected(err)
		}
		body = append(body, chunk...)
		if int64(len(body)) > c.maxArticle {
			return nil, ErrArticleTooLarge
		}
		if partial {
			continue
		}

		line := body[lineStart:]
		if len(line) > 0 && line[0] == '.' {
			if len(trimEOL(line)) == 1 {
				// The terminator: everything before it is the body.
				return body[:lineStart], nil
			}
			// Dot-stuffed: drop the added dot, in place.
			copy(body[lineStart:], body[lineStart+1:])
			body = body[:len(body)-1]
		}
		lineStart = len(body)
	}
}

// eofAsUnexpected turns a clean EOF mid-response into the truncation it
// actually is, so it reads as a transient failure rather than a success.
func eofAsUnexpected(err error) error {
	if errors.Is(err, io.EOF) {
		return io.ErrUnexpectedEOF
	}
	return err
}

// trimEOL drops a trailing CRLF or LF without copying.
func trimEOL(line []byte) []byte {
	if n := len(line); n > 0 && line[n-1] == '\n' {
		line = line[:n-1]
	}
	if n := len(line); n > 0 && line[n-1] == '\r' {
		line = line[:n-1]
	}
	return line
}

// messageIDArg normalises a message-id into the BODY argument.
//
// The rejection of whitespace and control characters is the security check: a
// message-id comes from a downloaded NZB, and one containing CRLF would inject
// a command into this connection.
func messageIDArg(id string) (string, error) {
	s := strings.TrimSpace(id)
	if s == "" {
		return "", errors.New("nntp: empty message id")
	}
	for _, r := range s {
		if r <= ' ' || r == 0x7f {
			return "", fmt.Errorf("nntp: message id %q contains a control character", s)
		}
	}
	if !strings.HasPrefix(s, "<") {
		s = "<" + s
	}
	if !strings.HasSuffix(s, ">") {
		s += ">"
	}
	return s, nil
}
