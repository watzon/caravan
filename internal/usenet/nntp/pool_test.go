package nntp

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/usenet/nntptest"
)

func newPool(t *testing.T, cfg ServerConfig, opts Options) *Pool {
	t.Helper()
	p, err := NewPool(cfg, opts)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(func() { p.Close() })
	return p
}

// Reuse is the whole point of pooling here: a download is thousands of
// articles, and a fresh TLS handshake and AUTHINFO exchange per article would
// cost more than the transfer.
func TestPoolReusesOneConnection(t *testing.T) {
	s, cfg, opts := newFake(t, authOptions())
	s.Add(testMessageID, sampleBody)
	p := newPool(t, cfg, opts)

	for i := range 5 {
		body, err := p.Body(context.Background(), testMessageID)
		if err != nil {
			t.Fatalf("Body %d: %v", i, err)
		}
		if !bytes.Equal(body, sampleBody) {
			t.Fatalf("Body %d = %q", i, body)
		}
	}

	if got := s.Stats().Accepted; got != 1 {
		t.Fatalf("server accepted %d connections for 5 sequential fetches, want 1", got)
	}
	if got := s.Stats().Auths; got != 1 {
		t.Fatalf("authenticated %d times, want 1", got)
	}
	st := p.Stats()
	if st.Dialed != 1 || st.Open != 1 || st.Idle != 1 || st.InUse != 0 {
		t.Fatalf("stats = %+v, want one idle connection dialled once", st)
	}
}

// Going over a provider's connection allowance is not slow, it is refused, so
// the cap has to hold under concurrency.
func TestPoolNeverExceedsMaxConnections(t *testing.T) {
	const limit = 3
	const fetches = 12

	s, cfg, opts := newFake(t, authOptions())
	cfg.MaxConnections = limit
	s.Add(testMessageID, sampleBody)

	// The hook holds the first `limit` articles until all of them have
	// arrived. If the pool allowed fewer than the cap the barrier never fills
	// and the guard below fails the test; if it allowed more, peak goes over.
	var mu sync.Mutex
	inFlight, peak := 0, 0
	gate := make(chan struct{})
	var once sync.Once
	stuck := make(chan struct{}, fetches)
	s.SetBodyHook(func(string) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		full := inFlight >= limit
		mu.Unlock()
		if full {
			once.Do(func() { close(gate) })
		}
		select {
		case <-gate:
		case <-time.After(3 * time.Second):
			stuck <- struct{}{}
		}
		mu.Lock()
		inFlight--
		mu.Unlock()
	})

	p := newPool(t, cfg, opts)
	var wg sync.WaitGroup
	errs := make(chan error, fetches)
	for range fetches {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := p.Body(context.Background(), testMessageID); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("Body: %v", err)
	}
	if len(stuck) > 0 {
		t.Fatalf("%d fetches waited out the barrier: the pool served fewer than %d at once", len(stuck), limit)
	}

	mu.Lock()
	gotPeak := peak
	mu.Unlock()
	if gotPeak != limit {
		t.Fatalf("peak concurrent articles = %d, want exactly %d", gotPeak, limit)
	}
	if got := s.Stats().Peak; got > limit {
		t.Fatalf("server saw %d concurrent connections, over the cap of %d", got, limit)
	}
	if got := p.Stats().PeakOpen; got > limit {
		t.Fatalf("pool opened %d connections, over the cap of %d", got, limit)
	}
}

func TestPoolDiscardsABrokenConnection(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	s.Add(testMessageID, sampleBody)
	s.SetFault(nntptest.Fault{Bodies: 1, Mode: nntptest.FaultDropMidBody})
	p := newPool(t, cfg, opts)

	if _, err := p.Body(context.Background(), testMessageID); err == nil {
		t.Fatal("Body succeeded on a truncated article")
	}
	if st := p.Stats(); st.Open != 0 || st.Idle != 0 {
		t.Fatalf("stats = %+v, want the broken connection dropped", st)
	}

	body, err := p.Body(context.Background(), testMessageID)
	if err != nil {
		t.Fatalf("Body after the fault: %v", err)
	}
	if !bytes.Equal(body, sampleBody) {
		t.Fatalf("body = %q", body)
	}
	if got := p.Stats().Dialed; got != 2 {
		t.Fatalf("dialled %d times, want 2 (the broken one was not reused)", got)
	}
}

// A pooled connection the server dropped in the meantime is the pool's own
// stale bookkeeping, not a failed request: it is retried once on a fresh
// connection rather than reported.
func TestPoolRetriesAStaleIdleConnection(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	s.Add(testMessageID, sampleBody)
	p := newPool(t, cfg, opts)

	if _, err := p.Body(context.Background(), testMessageID); err != nil {
		t.Fatalf("first Body: %v", err)
	}
	// The next command on the pooled connection finds it gone.
	s.SetFault(nntptest.Fault{Bodies: 1, Mode: nntptest.FaultDropBeforeStatus})

	body, err := p.Body(context.Background(), testMessageID)
	if err != nil {
		t.Fatalf("Body on a stale connection: %v", err)
	}
	if !bytes.Equal(body, sampleBody) {
		t.Fatalf("body = %q", body)
	}
	if got := p.Stats().Dialed; got != 2 {
		t.Fatalf("dialled %d times, want 2", got)
	}
}

// A fresh connection that fails is a real failure: retrying it inside the pool
// would hide a server that is down from the failover above.
func TestPoolDoesNotRetryAFreshConnection(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	s.Add(testMessageID, sampleBody)
	s.SetFault(nntptest.Fault{Bodies: 1, Mode: nntptest.FaultDropBeforeStatus})
	p := newPool(t, cfg, opts)

	if _, err := p.Body(context.Background(), testMessageID); err == nil {
		t.Fatal("Body succeeded against a dropped connection")
	}
	if got := p.Stats().Dialed; got != 1 {
		t.Fatalf("dialled %d times, want 1", got)
	}
}

func TestPoolWaitForASlotIsCancellable(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	cfg.MaxConnections = 1
	s.Add(testMessageID, sampleBody)

	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	s.SetBodyHook(func(string) {
		once.Do(func() { close(entered) })
		<-release
	})
	defer close(release)

	p := newPool(t, cfg, opts)
	held := make(chan struct{})
	go func() {
		defer close(held)
		p.Body(context.Background(), testMessageID)
	}()
	<-entered

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := p.Body(ctx, testMessageID); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if got := s.Stats().Peak; got != 1 {
		t.Fatalf("server saw %d concurrent connections, want 1", got)
	}
}

func TestPoolDropsConnectionsIdleTooLong(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	opts.IdleTimeout = time.Nanosecond
	s.Add(testMessageID, sampleBody)
	p := newPool(t, cfg, opts)

	for i := range 2 {
		if _, err := p.Body(context.Background(), testMessageID); err != nil {
			t.Fatalf("Body %d: %v", i, err)
		}
	}
	if got := p.Stats().Dialed; got != 2 {
		t.Fatalf("dialled %d times, want 2: the expired connection was reused", got)
	}
}

func TestPoolClosedRefusesWork(t *testing.T) {
	s, cfg, opts := newFake(t, nntptest.Options{})
	s.Add(testMessageID, sampleBody)
	p := newPool(t, cfg, opts)

	if _, err := p.Body(context.Background(), testMessageID); err != nil {
		t.Fatalf("Body: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// Idempotent, because the composition root closes things more than once.
	if err := p.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	if _, err := p.Body(context.Background(), testMessageID); !errors.Is(err, ErrPoolClosed) {
		t.Fatalf("err = %v, want ErrPoolClosed", err)
	}
	// Close says goodbye rather than dropping the socket, so the provider's
	// allowance is freed now instead of at its own idle timeout.
	cmds := s.Commands()
	if len(cmds) == 0 || cmds[len(cmds)-1] != "QUIT" {
		t.Fatalf("commands = %q, want a trailing QUIT", cmds)
	}
}

func TestPoolServerHidesCredentials(t *testing.T) {
	_, cfg, opts := newFake(t, authOptions())
	p := newPool(t, cfg, opts)

	got := p.Server()
	if got.Username != "" || got.Password != "" {
		t.Fatalf("Server() = %+v, want the credentials cleared", got)
	}
	if got.Host != cfg.Host || got.Port != cfg.Port {
		t.Fatalf("Server() = %+v, want the address kept", got)
	}
}
