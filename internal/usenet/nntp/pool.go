package nntp

import (
	"context"
	"errors"
	"sync"
	"time"
)

// PoolStats is one server's connection accounting. It exists so a test can
// prove the cap and reuse, and so a later track can show the user what their
// provider account is actually doing.
type PoolStats struct {
	// Open is idle plus in-use connections.
	Open int
	// Idle is connections held for reuse.
	Idle int
	// InUse is connections currently carrying a command.
	InUse int
	// Dialed is the total number of connections opened since NewPool, so
	// "reused the connection" is an assertion rather than a hope.
	Dialed int64
	// PeakOpen is the high-water mark of Open. It never exceeds the server's
	// MaxConnections.
	PeakOpen int
	// Limit is the cap Open is held under.
	Limit int
}

// Pool is a capped, reusing set of connections to one news server.
//
// The cap is the reason this type exists. A Usenet account sells a fixed
// number of connections and a request over that number is refused, not queued,
// so the limit has to be enforced before the socket is opened. It is enforced
// with a token channel: a fetch holds a token from the moment it wants a
// connection until it gives one back, which also gives waiting fetches
// something to block on that a context can cancel.
//
// A Pool is safe for concurrent use and its zero value is not: use NewPool.
type Pool struct {
	cfg  ServerConfig
	opts Options
	// slots caps concurrent connections; one token is one connection's worth
	// of the provider's allowance.
	slots chan struct{}

	mu     sync.Mutex
	idle   []idleConn
	closed bool
	stats  PoolStats
}

// idleConn is a connection waiting to be reused, and when it went idle.
type idleConn struct {
	c     *Conn
	since time.Time
}

// NewPool returns a pool for one server. The server does not have to be
// reachable — connections are opened on demand — but it does have to be
// valid.
func NewPool(cfg ServerConfig, opts Options) (*Pool, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	cfg = cfg.normalized()
	limit := cfg.connections()
	return &Pool{
		cfg:   cfg,
		opts:  opts.normalized(),
		slots: make(chan struct{}, limit),
		stats: PoolStats{Limit: limit},
	}, nil
}

// Server is the configuration this pool dials, with the credentials cleared so
// it is safe to log or serialise (SPEC §12).
func (p *Pool) Server() ServerConfig {
	cfg := p.cfg
	cfg.Username, cfg.Password = "", ""
	return cfg
}

// Label names this pool's server in errors and logs.
func (p *Pool) Label() string { return p.cfg.Label() }

// Stats reports the pool's current accounting.
func (p *Pool) Stats() PoolStats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

// Body fetches one article body from this server, on a pooled connection.
//
// A connection that comes back from the idle set may have been dropped by the
// server since it was put there, which surfaces as a failure on the first
// read. That one case is retried on a fresh connection, because it is not a
// failure of the request: it is the pool's own stale bookkeeping, and letting
// it escape would turn a healthy server into a "transient error" on every
// download that pauses long enough.
func (p *Pool) Body(ctx context.Context, messageID string) ([]byte, error) {
	body, err, reused := p.fetch(ctx, messageID)
	if err == nil || !reused || !staleConn(err) {
		return body, err
	}
	body, err, _ = p.fetch(ctx, messageID)
	return body, err
}

// fetch runs one attempt and reports whether it ran on a reused connection.
func (p *Pool) fetch(ctx context.Context, messageID string) ([]byte, error, bool) {
	c, reused, err := p.get(ctx)
	if err != nil {
		return nil, err, false
	}
	body, err := c.Body(ctx, messageID)
	p.put(c)
	return body, err, reused
}

// staleConn reports whether err is the kind of failure a connection the server
// had already dropped produces. A protocol error is the server answering, so
// it is never stale.
func staleConn(err error) bool {
	var pe *ProtocolError
	if errors.As(err, &pe) {
		return false
	}
	return Retryable(err)
}

// get takes a connection, reusing an idle one when there is a live one.
func (p *Pool) get(ctx context.Context) (*Conn, bool, error) {
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, false, ctx.Err()
	}

	c, expired, err := p.takeIdle()
	// Closing outside the lock keeps a slow close from blocking the pool.
	for _, e := range expired {
		e.Close()
	}
	if err != nil {
		<-p.slots
		return nil, false, err
	}
	if c != nil {
		return c, true, nil
	}

	c, err = Dial(ctx, p.cfg, p.opts)
	if err != nil {
		<-p.slots
		return nil, false, err
	}

	p.mu.Lock()
	closed := p.closed
	if !closed {
		p.stats.Dialed++
		p.stats.Open++
		p.stats.InUse++
		if p.stats.Open > p.stats.PeakOpen {
			p.stats.PeakOpen = p.stats.Open
		}
	}
	p.mu.Unlock()
	if closed {
		c.Close()
		<-p.slots
		return nil, false, ErrPoolClosed
	}
	return c, false, nil
}

// takeIdle pops a reusable connection and the expired ones to close.
func (p *Pool) takeIdle() (*Conn, []*Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, nil, ErrPoolClosed
	}
	var expired []*Conn
	now := time.Now()
	for len(p.idle) > 0 {
		last := p.idle[len(p.idle)-1]
		p.idle = p.idle[:len(p.idle)-1]
		p.stats.Idle--
		if p.opts.IdleTimeout > 0 && now.Sub(last.since) > p.opts.IdleTimeout {
			p.stats.Open--
			expired = append(expired, last.c)
			continue
		}
		p.stats.InUse++
		return last.c, expired, nil
	}
	return nil, expired, nil
}

// put returns a connection to the idle set, or closes it when it is broken or
// the pool has been closed, and releases its slot either way.
func (p *Pool) put(c *Conn) {
	p.mu.Lock()
	p.stats.InUse--
	discard := c.broken || p.closed
	if discard {
		p.stats.Open--
	} else {
		p.idle = append(p.idle, idleConn{c: c, since: time.Now()})
		p.stats.Idle++
	}
	p.mu.Unlock()

	if discard {
		c.Close()
	}
	<-p.slots
}

// Close closes every idle connection and refuses new work. Fetches already in
// flight finish; their connections are closed instead of pooled.
//
// It is safe to call more than once.
func (p *Pool) Close() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	idle := p.idle
	p.idle = nil
	p.stats.Idle = 0
	p.stats.Open -= len(idle)
	p.mu.Unlock()

	// QUIT is a courtesy the server notices: it frees the account's connection
	// immediately instead of after its idle timeout. It is bounded because a
	// shutdown must not hang on a server that stopped answering.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, e := range idle {
		_ = e.c.Quit(ctx)
	}
	return nil
}
