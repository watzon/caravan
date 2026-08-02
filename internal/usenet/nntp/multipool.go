package nntp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// Defaults for Retry.
const (
	// DefaultRetryAttempts is how many times one server is asked for an
	// article before the next server is tried. Three, because the failure it
	// exists for — a connection the provider recycled, a momentary refusal —
	// is over by the second try, and a server that fails three times in a row
	// is a server the backup should be answering for.
	DefaultRetryAttempts = 3
	// DefaultRetryBase is the first backoff delay.
	DefaultRetryBase = 250 * time.Millisecond
	// DefaultRetryMax caps it. A download is thousands of articles; a backoff
	// that grows without a ceiling turns one bad minute into a stalled queue.
	DefaultRetryMax = 5 * time.Second
)

// Retry is MultiPool's per-server retry policy.
type Retry struct {
	// Attempts is the number of tries per server, at least 1. Zero uses
	// DefaultRetryAttempts.
	Attempts int
	// Base is the delay before the second attempt. Zero uses
	// DefaultRetryBase; negative disables waiting entirely.
	Base time.Duration
	// Max caps the delay. Zero uses DefaultRetryMax.
	Max time.Duration
}

func (r Retry) normalized() Retry {
	if r.Attempts <= 0 {
		r.Attempts = DefaultRetryAttempts
	}
	if r.Base == 0 {
		r.Base = DefaultRetryBase
	}
	if r.Max <= 0 {
		r.Max = DefaultRetryMax
	}
	return r
}

// delay is how long to wait before attempt n, counting the first attempt as 1.
// It doubles and is capped at Max.
func (r Retry) delay(attempt int) time.Duration {
	if attempt < 2 || r.Base <= 0 {
		return 0
	}
	d := r.Base
	for i := 2; i < attempt; i++ {
		d *= 2
		if d >= r.Max {
			return r.Max
		}
	}
	if d > r.Max {
		return r.Max
	}
	return d
}

// ServerAttempt is what one server answered during a FetchBody.
type ServerAttempt struct {
	// Server is the server's Label, never its credentials.
	Server string
	// Err is why it did not produce the article.
	Err error
}

// FetchError is a FetchBody that no server satisfied.
//
// Its Unwrap is the part callers depend on: it unwraps to ErrArticleNotFound
// only when every server was asked and every one of them said the article is
// gone. If a single server was unreachable the answer is not "missing", it is
// "unknown", and the difference decides whether the segment is handed to par2
// as a hole or the download is failed as a transport problem.
type FetchError struct {
	// MessageID is the article that was wanted. A message-id is not a
	// credential, so it is safe in a message.
	MessageID string
	// Attempts is one entry per server tried, in the order they were tried.
	Attempts []ServerAttempt

	cause error
}

func (e *FetchError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "nntp: %s: article unavailable", e.MessageID)
	for _, a := range e.Attempts {
		fmt.Fprintf(&b, "; %s: %v", a.Server, a.Err)
	}
	return b.String()
}

// Unwrap is ErrArticleNotFound when every server agreed the article is gone,
// and the first inconclusive failure otherwise.
func (e *FetchError) Unwrap() error { return e.cause }

// NotFound reports whether every server was asked and every one of them said
// the article does not exist.
func (e *FetchError) NotFound() bool { return errors.Is(e.cause, ErrArticleNotFound) }

// ServerStats is one server's share of a MultiPool.
type ServerStats struct {
	// Server is the server's Label, never its credentials.
	Server string
	// Pool is that server's connection accounting.
	Pool PoolStats
}

// MultiPool fetches articles from a set of news servers in priority order. It
// is the entry point everything above the transport uses.
//
// The policy it implements is the one Usenet reliability is made of:
//
//   - Ask the highest-priority server first. Priority is lowest-wins, matching
//     indexers and download clients.
//   - A missing article moves straight to the next server. There is nothing to
//     retry: an article a server does not carry now will not appear a second
//     later, and the backup account exists precisely for this.
//   - A transient failure is retried on the same server with capped backoff
//     before the next server is tried, because moving a whole download to a
//     block account over one dropped connection burns paid blocks.
//   - A cancelled context stops everything immediately.
//
// It is safe for concurrent use; callers are expected to fetch many articles
// at once, which is what the per-server caps are for.
type MultiPool struct {
	pools []*Pool
	retry Retry
	sleep func(ctx context.Context, d time.Duration) error
}

// NewMultiPool builds a pool per enabled server, ordered by priority.
//
// Disabled servers are skipped rather than rejected: a user turning a provider
// off is configuration, not an error. No enabled server at all is
// ErrNoServers, which callers report as "configure a news server" rather than
// as a failure.
func NewMultiPool(servers []ServerConfig, opts Options) (*MultiPool, error) {
	opts = opts.normalized()

	enabled := make([]ServerConfig, 0, len(servers))
	for _, s := range servers {
		if !s.Enabled {
			continue
		}
		if err := s.Validate(); err != nil {
			return nil, err
		}
		enabled = append(enabled, s)
	}
	if len(enabled) == 0 {
		return nil, ErrNoServers
	}
	// Stable on priority, then id, then label, so the order two servers with
	// the same priority are tried in does not change between restarts.
	sort.SliceStable(enabled, func(i, j int) bool {
		a, b := enabled[i], enabled[j]
		switch {
		case a.Priority != b.Priority:
			return a.Priority < b.Priority
		case a.ID != b.ID:
			return a.ID < b.ID
		default:
			return a.Label() < b.Label()
		}
	})

	m := &MultiPool{
		pools: make([]*Pool, 0, len(enabled)),
		retry: opts.Retry,
		sleep: opts.Sleep,
	}
	for _, s := range enabled {
		p, err := NewPool(s, opts)
		if err != nil {
			m.Close()
			return nil, err
		}
		m.pools = append(m.pools, p)
	}
	return m, nil
}

// FetchBody returns the body of one article, trying servers in priority order.
//
// The returned bytes are the article as the server sent it: dot-stuffing
// removed, terminator dropped, CRLF endings intact.
//
// On failure the error is a *FetchError carrying what each server said. Use
// errors.Is(err, ErrArticleNotFound) to ask the only question that changes
// what a caller does — whether the article is gone everywhere, and therefore
// a hole for par2 rather than a transport failure to retry later.
func (m *MultiPool) FetchBody(ctx context.Context, messageID string) ([]byte, error) {
	body, _, err := m.FetchBodyFrom(ctx, messageID, 0)
	return body, err
}

// FetchBodyFrom is FetchBody restricted to the servers from index from
// downwards in priority order, and it reports the index of the server that
// answered.
//
// It exists for one case, and the NZB pipeline is the only caller that has it:
// an article that arrived complete and then failed its own yEnc CRC. Asking
// the same server again returns the same damaged bytes, but a backup
// provider's copy of that article is frequently clean, so the pipeline retries
// from answered+1 before writing the segment off as a hole for par2 (PLAN
// phase 7 task 3). Index 0 is FetchBody.
//
// A from past the last server is a *FetchError unwrapping to ErrNoServers:
// there was no backup left to ask, which is a different answer from "the
// article is gone".
func (m *MultiPool) FetchBodyFrom(ctx context.Context, messageID string, from int) ([]byte, int, error) {
	if _, err := messageIDArg(messageID); err != nil {
		return nil, 0, err
	}
	if from < 0 {
		from = 0
	}

	fail := &FetchError{MessageID: messageID}
	if from >= len(m.pools) {
		fail.cause = ErrNoServers
		return nil, 0, fail
	}
	for i := from; i < len(m.pools); i++ {
		p := m.pools[i]
		body, err := m.fetchFrom(ctx, p, messageID)
		if err == nil {
			return body, i, nil
		}
		fail.Attempts = append(fail.Attempts, ServerAttempt{Server: p.Label(), Err: err})
		// A cancelled caller is not a failover: stop asking.
		if ctx.Err() != nil {
			fail.cause = ctx.Err()
			return nil, 0, fail
		}
	}

	missing := 0
	for _, a := range fail.Attempts {
		if errors.Is(a.Err, ErrArticleNotFound) {
			missing++
			continue
		}
		// The first inconclusive answer is the cause: it is what keeps this
		// from unwrapping to ErrArticleNotFound.
		if fail.cause == nil {
			fail.cause = a.Err
		}
	}
	if fail.cause == nil && missing == len(fail.Attempts) {
		fail.cause = ErrArticleNotFound
	}
	return nil, 0, fail
}

// fetchFrom asks one server, retrying transient failures with capped backoff.
func (m *MultiPool) fetchFrom(ctx context.Context, p *Pool, messageID string) ([]byte, error) {
	var last error
	for attempt := 1; attempt <= m.retry.Attempts; attempt++ {
		if d := m.retry.delay(attempt); d > 0 {
			if err := m.sleep(ctx, d); err != nil {
				return nil, err
			}
		}
		body, err := p.Body(ctx, messageID)
		if err == nil {
			return body, nil
		}
		last = err
		if !Retryable(err) {
			return nil, err
		}
		if ctx.Err() != nil {
			return nil, err
		}
	}
	return nil, last
}

// Stats reports each server's connection accounting, in priority order.
func (m *MultiPool) Stats() []ServerStats {
	out := make([]ServerStats, 0, len(m.pools))
	for _, p := range m.pools {
		out = append(out, ServerStats{Server: p.Label(), Pool: p.Stats()})
	}
	return out
}

// Servers lists the configured servers in priority order, with credentials
// cleared (SPEC §12).
func (m *MultiPool) Servers() []ServerConfig {
	out := make([]ServerConfig, 0, len(m.pools))
	for _, p := range m.pools {
		out = append(out, p.Server())
	}
	return out
}

// Close closes every pool. It is safe to call more than once.
func (m *MultiPool) Close() error {
	var errs []error
	for _, p := range m.pools {
		if err := p.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// sleepCtx waits for d, or returns early when the caller gives up.
func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
