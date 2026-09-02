// Package nntptest is an in-process news server for testing Caravan's NNTP
// client and everything built on it.
//
// It speaks the slice of RFC 3977/4643 that the embedded Usenet engine uses, a
// greeting, AUTHINFO USER/PASS, BODY by message-id, QUIT, and nothing else.
// Articles are registered as raw bytes and written dot-stuffed on the wire, so
// a later track can register yEnc payloads without this package knowing what
// yEnc is.
//
// It listens on 127.0.0.1 with a kernel-chosen port, never on Caravan's own
// port, and every test that uses it is free of the network (no live calls, no
// fixtures downloaded at test time).
//
// It is a package rather than a _test.go file because four suites need the same
// fake: the client, the NZB pipeline, par2 repair and the end-to-end suite. It
// holds no Caravan types and does not import internal/usenet/nntp, so the
// client's own in-package tests can use it without an import cycle.
package nntptest

import (
	"bufio"
	"bytes"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"
)

// FaultMode is how an injected fault breaks a BODY command.
type FaultMode int

const (
	// FaultNone answers normally. It is the zero value, so a zero Fault
	// injects nothing.
	FaultNone FaultMode = iota
	// FaultDropBeforeStatus closes the connection without answering at all:
	// what a news server that recycled the connection under you looks like.
	FaultDropBeforeStatus
	// FaultDropMidBody sends the 222 status line and part of the body, then
	// closes. This is the nastier one: the client has already committed to
	// reading an article and must not hand back a truncated body.
	FaultDropMidBody
	// FaultStatus answers with Fault.Code instead of serving the article.
	FaultStatus
)

// Fault is scripted misbehaviour, installed with SetFault.
//
// It is counted rather than permanent because the interesting client
// behaviours are "retry and then succeed" and "fail over after N tries": both
// need a server that stops failing.
type Fault struct {
	// Bodies is how many BODY commands are broken before the server goes back
	// to answering normally. Zero injects nothing.
	Bodies int
	// Mode is how those BODY commands break.
	Mode FaultMode
	// Code is the status returned when Mode is FaultStatus. Zero means 400
	// (service temporarily unavailable), the generic transient answer.
	Code int
	// Delay is slept before answering every BODY command, faulty or not. It is
	// for testing timeouts and cancellation, never for synchronising a test:
	// use SetBodyHook for that.
	Delay time.Duration
}

// Stats is what the server saw. Peak is the assertion that makes a connection
// pool's cap testable: a pool that leaks connections shows up here as a Peak
// above its own limit.
type Stats struct {
	// Accepted is the total number of connections accepted.
	Accepted int
	// Open is how many connections are open right now.
	Open int
	// Peak is the high-water mark of Open.
	Peak int
	// Bodies is the number of BODY commands received, faulty ones included.
	Bodies int
	// Auths is the number of AUTHINFO PASS commands that succeeded.
	Auths int
}

// Options configures a Server. The zero value is a plaintext server that
// requires no credentials.
type Options struct {
	// Username and Password are the credentials the server accepts. Empty
	// credentials are accepted by any AUTHINFO exchange.
	Username string
	Password string
	// RequireAuth answers 480 to BODY until AUTHINFO PASS has succeeded,
	// which is how a real provider behaves.
	RequireAuth bool
	// TLS wraps the listener in TLS with a throwaway self-signed certificate;
	// TLSConfig then returns the client side of it.
	TLS bool
	// Greeting overrides the banner line, e.g. "201 no posting allowed" or a
	// "400 load shedding" refusal.
	Greeting string
}

// Server is a running fake news server. Close it when the test ends.
type Server struct {
	opts Options
	ln   net.Listener
	host string
	port int
	tlsC *tls.Config

	mu         sync.Mutex
	articles   map[string][]byte
	fault      Fault
	stats      Stats
	commands   []string
	bodyHook   func(messageID string)
	rejectAuth bool
	conns      map[net.Conn]struct{}
	closed     bool

	wg sync.WaitGroup
}

// New starts a server on 127.0.0.1 with a kernel-chosen port.
func New(opts Options) (*Server, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("nntptest: listen: %w", err)
	}
	s := &Server{
		opts:     opts,
		articles: map[string][]byte{},
		conns:    map[net.Conn]struct{}{},
	}
	if opts.TLS {
		cert, pool, err := selfSigned()
		if err != nil {
			ln.Close()
			return nil, err
		}
		s.tlsC = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
		ln = tls.NewListener(ln, &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})
	}
	s.ln = ln
	host, portStr, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("nntptest: address %q: %w", ln.Addr(), err)
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		ln.Close()
		return nil, fmt.Errorf("nntptest: port %q: %w", portStr, err)
	}
	s.host, s.port = host, port

	s.wg.Add(1)
	go s.accept()
	return s, nil
}

// Addr is the "host:port" the server is listening on.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Host is the address half of Addr.
func (s *Server) Host() string { return s.host }

// Port is the port half of Addr.
func (s *Server) Port() int { return s.port }

// TLSConfig is the client-side configuration that trusts this server's
// throwaway certificate, or nil when the server is plaintext.
func (s *Server) TLSConfig() *tls.Config {
	if s.tlsC == nil {
		return nil
	}
	return s.tlsC.Clone()
}

// Add registers an article body. The message-id may be given with or without
// its angle brackets; body is written verbatim except that line endings are
// normalised to CRLF and dot-stuffing is applied, as a real server does.
func (s *Server) Add(messageID string, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.articles[normalizeID(messageID)] = body
}

// Remove unregisters an article, so the server answers 430 for it.
func (s *Server) Remove(messageID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.articles, normalizeID(messageID))
}

// SetFault installs scripted misbehaviour, replacing any fault still pending.
func (s *Server) SetFault(f Fault) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fault = f
}

// SetRejectAuth makes AUTHINFO PASS answer 481 regardless of the credentials.
func (s *Server) SetRejectAuth(reject bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rejectAuth = reject
}

// SetBodyHook installs fn, called on the serving goroutine before each BODY is
// answered. It is the synchronisation seam: block in fn to hold a connection
// open, so a test can prove a pool's concurrency limit without sleeping.
func (s *Server) SetBodyHook(fn func(messageID string)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.bodyHook = fn
}

// Stats reports what the server has seen so far.
func (s *Server) Stats() Stats {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.stats
}

// Commands returns every command line the server received, in order. It
// includes AUTHINFO PASS with its argument: this is a test double, and a test
// that asserts the right password went over the wire needs to see it.
func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

// Close stops the listener, drops every open connection and waits for the
// serving goroutines. It is safe to call more than once.
func (s *Server) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conns := make([]net.Conn, 0, len(s.conns))
	for c := range s.conns {
		conns = append(conns, c)
	}
	s.mu.Unlock()

	err := s.ln.Close()
	for _, c := range conns {
		c.Close()
	}
	s.wg.Wait()
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

func (s *Server) accept() {
	defer s.wg.Done()
	for {
		nc, err := s.ln.Accept()
		if err != nil {
			return
		}
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			nc.Close()
			return
		}
		s.conns[nc] = struct{}{}
		s.stats.Accepted++
		s.stats.Open++
		if s.stats.Open > s.stats.Peak {
			s.stats.Peak = s.stats.Open
		}
		s.mu.Unlock()

		s.wg.Add(1)
		go s.serve(nc)
	}
}

func (s *Server) serve(nc net.Conn) {
	defer s.wg.Done()
	defer func() {
		nc.Close()
		s.mu.Lock()
		if _, ok := s.conns[nc]; ok {
			delete(s.conns, nc)
			s.stats.Open--
		}
		s.mu.Unlock()
	}()

	br := bufio.NewReader(nc)
	bw := bufio.NewWriter(nc)

	greeting := s.opts.Greeting
	if greeting == "" {
		greeting = "200 nntptest ready"
	}
	if !writeLine(bw, greeting) {
		return
	}
	// A refusal banner is the end of the conversation, exactly as a real
	// server that is shedding load answers.
	if code := statusCode(greeting); code >= 400 {
		return
	}

	authed := !s.opts.RequireAuth
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.TrimRight(line, "\r\n")
		if cmd == "" {
			continue
		}
		s.record(cmd)

		verb, rest, _ := strings.Cut(cmd, " ")
		switch strings.ToUpper(verb) {
		case "QUIT":
			writeLine(bw, "205 closing connection")
			return
		case "MODE":
			if !writeLine(bw, "200 reader mode") {
				return
			}
		case "AUTHINFO":
			ok, keep := s.authinfo(bw, rest)
			if !keep {
				return
			}
			if ok {
				authed = true
			}
		case "BODY":
			if !s.body(bw, strings.TrimSpace(rest), authed) {
				return
			}
		default:
			if !writeLine(bw, "500 command not recognized") {
				return
			}
		}
	}
}

// authinfo answers one AUTHINFO command. It reports whether the session is now
// authenticated and whether the connection stays open.
func (s *Server) authinfo(bw *bufio.Writer, rest string) (ok, keep bool) {
	kind, arg, _ := strings.Cut(rest, " ")
	switch strings.ToUpper(kind) {
	case "USER":
		return false, writeLine(bw, "381 password required")
	case "PASS":
		s.mu.Lock()
		reject := s.rejectAuth
		s.mu.Unlock()
		if reject || arg != s.opts.Password {
			return false, writeLine(bw, "481 authentication failed")
		}
		s.mu.Lock()
		s.stats.Auths++
		s.mu.Unlock()
		return true, writeLine(bw, "281 authentication accepted")
	default:
		return false, writeLine(bw, "501 unsupported authinfo")
	}
}

// body answers one BODY command and reports whether the connection stays open.
func (s *Server) body(bw *bufio.Writer, id string, authed bool) bool {
	s.mu.Lock()
	s.stats.Bodies++
	fault := s.fault
	if fault.Bodies > 0 {
		s.fault.Bodies--
	} else {
		fault.Mode = FaultNone
	}
	hook := s.bodyHook
	article, known := s.articles[normalizeID(id)]
	s.mu.Unlock()

	if hook != nil {
		hook(id)
	}
	if fault.Delay > 0 {
		time.Sleep(fault.Delay)
	}

	switch fault.Mode {
	case FaultDropBeforeStatus:
		return false
	case FaultDropMidBody:
		if !writeLine(bw, "222 0 "+id+" body follows") {
			return false
		}
		bw.WriteString("this article stops half way\r\nand the connect")
		bw.Flush()
		return false
	case FaultStatus:
		code := fault.Code
		if code == 0 {
			code = 400
		}
		return writeLine(bw, fmt.Sprintf("%d injected fault", code))
	}

	if !authed {
		return writeLine(bw, "480 authentication required")
	}
	if !known {
		return writeLine(bw, "430 no such article")
	}
	if !writeLine(bw, "222 0 "+id+" body follows") {
		return false
	}
	writeDotted(bw, article)
	return bw.Flush() == nil
}

func (s *Server) record(cmd string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.commands = append(s.commands, cmd)
}

// writeDotted writes body as an NNTP multi-line block: CRLF line endings, a
// leading '.' doubled on every line, and a lone "." to end it.
func writeDotted(bw *bufio.Writer, body []byte) {
	for len(body) > 0 {
		var line []byte
		if i := bytes.IndexByte(body, '\n'); i >= 0 {
			line, body = body[:i], body[i+1:]
		} else {
			line, body = body, nil
		}
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) > 0 && line[0] == '.' {
			bw.WriteByte('.')
		}
		bw.Write(line)
		bw.WriteString("\r\n")
	}
	bw.WriteString(".\r\n")
}

func writeLine(bw *bufio.Writer, line string) bool {
	if _, err := bw.WriteString(line + "\r\n"); err != nil {
		return false
	}
	return bw.Flush() == nil
}

func statusCode(line string) int {
	if len(line) < 3 {
		return 0
	}
	code, err := strconv.Atoi(line[:3])
	if err != nil {
		return 0
	}
	return code
}

// normalizeID strips the angle brackets so a message-id registered bare and
// requested wrapped (or the other way round) is the same article.
func normalizeID(id string) string {
	id = strings.TrimSpace(id)
	id = strings.TrimPrefix(id, "<")
	id = strings.TrimSuffix(id, ">")
	return id
}
