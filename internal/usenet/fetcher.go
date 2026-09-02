package usenet

import (
	"context"
	"sync"

	"github.com/watzon/caravan/internal/usenet/nntp"
)

// fetcher is the engine's stable handle on a set of news servers.
//
// It exists because the two lifetimes underneath it do not line up. A
// *nntp.MultiPool is built from configuration and has to be rebuilt when the
// user edits a server, but a download holds its fetcher for however long the
// release takes: hours, for a big one. Handing the pipeline the pool directly
// would mean either freezing configuration for the life of a download or
// closing sockets out from under an in-flight article.
//
// So callers hold the fetcher, the fetcher holds the pool, and swap replaces
// it. The old pool is closed only once the fetches that were already using it
// have returned, which is what makes a settings change invisible to a download
// that is mid-flight.
//
// A nil pool is the "no news server configured" state and is reported as
// nntp.ErrNoServers, the same error NewMultiPool gives for it, so callers have
// one thing to check.
type fetcher struct {
	mu   sync.RWMutex
	pool *nntp.MultiPool
	// inFlight counts the fetches running against pool. It is replaced along
	// with the pool, so each pool has its own, and swap waits on the outgoing
	// one before closing what it belonged to.
	inFlight *sync.WaitGroup
}

func newFetcher(pool *nntp.MultiPool) *fetcher {
	return &fetcher{pool: pool, inFlight: &sync.WaitGroup{}}
}

// The pipeline's richer contract, so a segment that arrives corrupt is retried
// on a backup server rather than written off as a par2 hole.
var _ nntpFetcher = (*fetcher)(nil)

// nntpFetcher is pipeline.FailoverFetcher, restated here so this file does not
// import the pipeline just to assert against it.
type nntpFetcher interface {
	FetchBody(ctx context.Context, messageID string) ([]byte, error)
	FetchBodyFrom(ctx context.Context, messageID string, from int) ([]byte, int, error)
}

// FetchBody returns one article body from the current pool.
func (f *fetcher) FetchBody(ctx context.Context, messageID string) ([]byte, error) {
	body, _, err := f.FetchBodyFrom(ctx, messageID, 0)
	return body, err
}

// FetchBodyFrom fetches considering only servers from index from downwards.
func (f *fetcher) FetchBodyFrom(ctx context.Context, messageID string, from int) ([]byte, int, error) {
	pool, done := f.acquire()
	if pool == nil {
		return nil, 0, nntp.ErrNoServers
	}
	defer done()
	return pool.FetchBodyFrom(ctx, messageID, from)
}

// acquire returns the pool to fetch against and the function that releases it.
// Registering the fetch under the read lock is what lets swap know, without a
// race, that no further work will start on the pool it is retiring.
func (f *fetcher) acquire() (*nntp.MultiPool, func()) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if f.pool == nil {
		return nil, func() {}
	}
	wg := f.inFlight
	wg.Add(1)
	return f.pool, wg.Done
}

// configured reports whether there is a pool to fetch against.
func (f *fetcher) configured() bool {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.pool != nil
}

// swap installs a new pool and retires the old one.
//
// The close happens on its own goroutine, after the fetches that were already
// running against the old pool have returned. Waiting inline would make a
// settings save block on the slowest article in flight; closing immediately
// would turn that article into a hole for par2 to fill, which is a repair
// budget spent on a configuration change.
func (f *fetcher) swap(pool *nntp.MultiPool) {
	f.mu.Lock()
	old, drained := f.pool, f.inFlight
	f.pool, f.inFlight = pool, &sync.WaitGroup{}
	f.mu.Unlock()

	if old == nil || old == pool {
		return
	}
	go func() {
		drained.Wait()
		old.Close()
	}()
}

// close retires the current pool and leaves the fetcher unconfigured. It
// blocks until in-flight fetches have returned, because the caller is shutting
// the engine down and a socket closed behind a live read is a spurious error
// in the log on the way out.
func (f *fetcher) close() {
	f.mu.Lock()
	old, drained := f.pool, f.inFlight
	f.pool, f.inFlight = nil, &sync.WaitGroup{}
	f.mu.Unlock()

	if old == nil {
		return
	}
	drained.Wait()
	old.Close()
}
