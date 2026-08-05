package download

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/watzon/caravan/internal/core"
)

// ErrNoEngine is returned when a release's protocol has no engine behind it:
// a usenet release with no SABnzbd or NZBGet client configured, in practice.
//
// It is a sentinel because the two halves of the answer differ. The grab is a
// recorded rejection rather than a failure — nothing broke, the user has not
// finished configuring Caravan — and the interactive request is a 4xx that
// names what to configure, not the 502 an engine that tried and failed gets.
var ErrNoEngine = errors.New("download: no engine configured for the release protocol")

// ErrUnsupported is returned by the router when the engine holding a download
// does not implement an optional extension (peer insight, per-download rate
// limits). The API answers 400 for it, the same as it does for a plain engine
// that does not implement the extension at all.
var ErrUnsupported = errors.New("download: engine does not support this operation")

// ErrNotRetryable is returned when a retry is asked for a download that has
// not failed. It is separate from ErrUnsupported because the two are different
// mistakes: the engine can retry, this download simply has nothing to retry,
// and the API answers 409 rather than 400.
var ErrNotRetryable = errors.New("download: only a failed download can be retried")

// ErrClientUnreachable is returned by Add when the engine a release would go
// to is an external download client the poller currently cannot reach
// (PLAN phase 6 task 4).
//
// Failing here is the point: handing a release to a machine that has not
// answered its last few polls produces a download nobody can see, and the
// grab's history would say it succeeded. The user gets the poll's own reason,
// which is the same thing the client's test button tells them.
var ErrClientUnreachable = errors.New("download: download client is unreachable")

// Route is one engine the router may dispatch to.
type Route struct {
	// Name is the `downloads.engine` value for downloads this engine holds
	// ("embedded", "qbittorrent", "sabnzbd", "nzbget").
	Name string
	// Protocol is core.ProtocolTorrent or core.ProtocolUsenet when this engine
	// is the configured default for that protocol, and "" when it is not.
	//
	// A route with no protocol still takes part in every other operation: a
	// client that used to be the default is still holding the downloads it
	// took, and those have to stay listable, pausable and removable after the
	// default moved elsewhere.
	Protocol string
	// Engine is the backend. The router never closes it: engine lifetimes
	// belong to whoever built the table.
	Engine core.Engine
	// IDPrefix namespaces this engine's handles, and is "" for an engine whose
	// handles are already globally unique.
	//
	// Engine-native handles are only unique within their own engine: NZBGet
	// hands out small sequential integers, so two NZBGet clients both have a
	// download 5. Every handle is stored and looked up as a bare string
	// (`downloads.engine_id`, `GetGrabByDownloadID`), so a collision makes the
	// router act on one client's download while the database answers with the
	// other's grab — importing B's payload against A's movie. Prefixing keeps
	// the two apart.
	//
	// The embedded engine deliberately has no prefix: info hashes are already
	// globally unique, and prefixing them would orphan every download row
	// written before this field existed.
	IDPrefix string
	// Unhealthy is why this engine is currently considered unreachable, and ""
	// when it is fine. A route that is unhealthy still takes part in every
	// operation that addresses an existing download — the client may have come
	// back since the last poll, and refusing to even ask would strand the
	// downloads it holds — but it will not accept a new release.
	Unhealthy string
	// Report, when set, is told the outcome of every poll the router sends to
	// this engine: nil when List answered, the failure otherwise. It is how
	// per-client health is tracked without the router holding state, which it
	// cannot: it is rebuilt from its table on every operation.
	Report func(error)
	// Method is this route's key in the concurrency ledger, and is set only for
	// routes the router itself must ration — the external clients.
	//
	// The built-in engines leave it empty and ration themselves: they can hold
	// a download without telling anyone, so they ask the coordinator directly
	// and the router stays out of it. A client cannot. Its downloads exist
	// only once it has accepted them, so the only way to hold one is to hand it
	// over paused and unpause it when a slot frees, and that is the router's
	// job because it is the thing that does the handing over.
	//
	// The key is per configured row rather than per kind: two SABnzbd clients
	// are two machines with two sets of connections, and one budget between
	// them would be a cap on the wrong thing.
	Method string
	// Admission rations this route. Nil is unlimited, which is what every
	// route was before caps existed.
	Admission *Admission
}

// gated reports whether the router must ration this route itself.
func (r Route) gated() bool { return r.Admission != nil && r.Method != "" }

// Table resolves the engines that are configured right now.
//
// It is a function rather than a captured slice because the import watcher
// takes one engine when it starts and drives it for the life of the process:
// a client the user adds five minutes later has to reach it without a
// restart. Resolution is a settings read and a `download_clients` read, both
// local, and the router calls it once per operation.
type Table func(ctx context.Context) ([]Route, error)

// Router is the single choke point every grab passes through (PLAN phase 6,
// task 3). It implements core.Engine, so the interactive grab handler and the
// automation runner route by protocol without either of them knowing that
// more than one engine exists.
//
// Add dispatches on the release's protocol. Every other operation addresses a
// download that already exists, so it dispatches on ownership instead: the
// router asks each engine for the download's status and acts on the one that
// answers. That is deliberate over trying engines in turn — a delete of an
// unknown handle is a no-op success in some clients, and "remove" landing on
// the wrong engine while the right one keeps the data is exactly the silent
// misroute this task exists to prevent.
type Router struct {
	table Table
}

// NewRouter returns a router that resolves its engines through table.
func NewRouter(table Table) *Router {
	return &Router{table: table}
}

// EngineNameFor implements core.EngineRouting: the backend name a release of
// this protocol will land on, or "" when nothing is configured for it.
func (r *Router) EngineNameFor(ctx context.Context, protocol string) string {
	routes, err := r.table(ctx)
	if err != nil {
		return ""
	}
	route, ok := routeFor(routes, protocol)
	if !ok {
		return ""
	}
	return route.Name
}

// Add sends r to the engine configured for its protocol.
func (r *Router) Add(ctx context.Context, rel core.Release, opts core.AddOpts) (core.DownloadID, error) {
	routes, err := r.table(ctx)
	if err != nil {
		return "", err
	}
	route, ok := routeFor(routes, rel.Protocol)
	if !ok {
		return "", fmt.Errorf("%w: %s", ErrNoEngine, configureHint(rel.Protocol))
	}
	if route.Unhealthy != "" {
		return "", fmt.Errorf("%w: %s: %s", ErrClientUnreachable, route.Name, route.Unhealthy)
	}

	// The concurrency decision has to come before the handoff, because after it
	// the client has already started. A refused release is still handed over —
	// there is no way to hold an NZB back and give it the same identity later —
	// but it is handed over paused, so the client registers it and does no work
	// until a slot frees.
	reserved := false
	if route.gated() {
		reserved = route.Admission.Reserve(route.Method)
		opts.Paused = !reserved
	}

	id, err := route.Engine.Add(ctx, rel, opts)
	if err != nil {
		if reserved {
			route.Admission.Cancel(route.Method)
		}
		return "", err
	}
	qualified := route.qualify(id)
	if route.gated() {
		if reserved {
			route.Admission.Commit(route.Method, qualified)
		} else {
			// Paused at the client and waiting its turn. This is what makes it
			// read as queued rather than as something a person paused.
			route.Admission.Wait(route.Method, qualified)
		}
	}
	return qualified, nil
}

// Status returns the snapshot from whichever engine holds id.
func (r *Router) Status(ctx context.Context, id core.DownloadID) (*core.DownloadStatus, error) {
	_, _, status, err := r.owner(ctx, id)
	if err != nil {
		return nil, err
	}
	return status, nil
}

// reconcile squares one client-reported status with the concurrency ledger.
//
// A client has one idea of "paused" and Caravan has two: the user asked for it,
// or Caravan is holding the download until a slot frees. They look identical
// over the client's API and mean opposite things to the person reading the
// queue, so the ledger is what tells them apart.
//
// It also takes the chance to keep the ledger honest about downloads nobody
// told it about — one added out of band, or one already running when a cap was
// first configured — because the global ceiling is only as good as its count.
//
// # Somebody reaching into the client directly
//
// A person who unpauses a Caravan-held download in SABnzbd's own web UI is
// making a clear request, and Caravan does not fight it: the download stops
// being held, is counted if there is room, and simply runs. The cap is honoured
// again from the next admission onward rather than by pausing something a human
// just started. The reverse — pausing a running download at the client — reads
// exactly as a pause here, which is what it is.
func (route Route) reconcile(status *core.DownloadStatus) {
	adm, id := route.Admission, status.ID
	switch {
	case adm.Waiting(id):
		if status.State == core.DownloadPaused {
			// Held by Caravan, not by anyone. It is waiting in line.
			status.State = core.DownloadQueued
			return
		}
		// Somebody started it at the client. Take the slot if there is one and
		// stop holding it either way.
		adm.Request(route.Method, id)
		adm.Unwait(id)
	case status.State == core.DownloadDownloading:
		// Count it. Request is idempotent, so this is free for the downloads
		// that already hold a slot.
		adm.Request(route.Method, id)
	default:
		// Paused, finished, failed: whatever it is, it is not transferring, and
		// a slot it may have held belongs to the queue behind it.
		adm.Release(id)
	}
}

// List returns every engine's downloads.
//
// A client that cannot be reached contributes nothing rather than failing the
// call: the queue screen and the import watcher both read this, and one
// unreachable machine must not blank the queue or stall imports for the
// engines that are working. Reachability is what the download client's test
// button reports (SPEC §13). Only a table where nothing answered is an error.
func (r *Router) List(ctx context.Context) ([]core.DownloadStatus, error) {
	routes, err := r.table(ctx)
	if err != nil {
		return nil, err
	}
	type pollResult struct {
		statuses []core.DownloadStatus
		err      error
	}
	results := make([]pollResult, len(routes))
	var wg sync.WaitGroup
	for i := range routes {
		wg.Go(func() {
			statuses, err := routes[i].Engine.List(ctx)
			results[i] = pollResult{statuses: statuses, err: err}
		})
	}
	wg.Wait()

	out := []core.DownloadStatus{}
	var failures []error
	ok := false
	for i, result := range results {
		route := routes[i]
		// List is the poll: it runs every watcher tick against every engine,
		// which makes it the one operation whose success or failure is a fair
		// reading of whether the client is up.
		if route.Report != nil {
			route.Report(result.err)
		}
		if result.err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", route.Name, result.err))
			continue
		}
		ok = true
		for _, status := range result.statuses {
			status.Engine = route.Name
			status.ID = route.qualify(status.ID)
			if route.gated() {
				route.reconcile(&status)
			}
			out = append(out, status)
		}
	}
	if !ok && len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return out, nil
}

// ListPage returns a bounded, deterministic merge of page-capable routes.
// The cursor is an opaque route-key and native-ID boundary for the API layer.
func (r *Router) ListPage(ctx context.Context, limit int, cursor string) ([]core.DownloadStatus, string, bool, error) {
	routes, err := r.table(ctx)
	if err != nil {
		return nil, "", false, err
	}
	if limit <= 0 {
		return []core.DownloadStatus{}, "", true, nil
	}

	boundaryRoute, boundaryID := "", core.DownloadID("")
	if cursor != "" {
		rawRoute, rawID, ok := strings.Cut(cursor, "\x00")
		if !ok || rawRoute == "" || rawID == "" {
			return nil, "", false, fmt.Errorf("download: invalid orphan cursor")
		}
		boundaryRoute, boundaryID = rawRoute, core.DownloadID(rawID)
	}

	type pageResult struct {
		route     Route
		key       string
		statuses  []core.DownloadStatus
		next      core.DownloadID
		err       error
		supported bool
	}
	results := make([]pageResult, len(routes))
	var wg sync.WaitGroup
	for i := range routes {
		wg.Go(func() {
			route := routes[i]
			result := pageResult{route: route, key: routePageKey(route)}
			pager, ok := route.Engine.(core.EnginePager)
			if !ok {
				result.supported = false
				results[i] = result
				return
			}
			result.supported = true
			if boundaryRoute != "" && result.key < boundaryRoute {
				results[i] = result
				return
			}
			before := core.DownloadID("")
			if result.key == boundaryRoute {
				before = boundaryID
			}
			result.statuses, result.next, result.err = pager.ListPage(ctx, limit, before)
			if result.err == nil && route.Report != nil {
				route.Report(nil)
			}
			results[i] = result
		})
	}
	wg.Wait()

	for _, result := range results {
		if !result.supported {
			return nil, "", false, nil
		}
		if result.err != nil {
			return nil, "", false, result.err
		}
		for i := range result.statuses {
			status := result.statuses[i]
			status.Engine = result.route.Name
			status.ID = result.route.qualify(status.ID)
			if result.route.gated() {
				result.route.reconcile(&status)
			}
			result.statuses[i] = status
		}
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].key < results[j].key })
	out := make([]core.DownloadStatus, 0, limit)
	lastKey := ""
	lastNative := core.DownloadID("")
	for _, result := range results {
		for _, status := range result.statuses {
			if len(out) == limit {
				break
			}
			out = append(out, status)
			lastKey = result.key
			native, _ := result.route.native(status.ID)
			lastNative = native
		}
		if len(out) == limit {
			break
		}
	}
	if len(out) == 0 {
		return out, "", true, nil
	}
	more := false
	for _, result := range results {
		if result.key > lastKey && len(result.statuses) > 0 {
			more = true
			break
		}
		if result.key == lastKey && result.next != "" {
			more = true
			break
		}
	}
	if !more {
		return out, "", true, nil
	}
	return out, lastKey + "\x00" + string(lastNative), true, nil
}

func routePageKey(route Route) string {
	return route.Name + "|" + route.IDPrefix
}

// Pause stops the download on the engine holding it.
func (r *Router) Pause(ctx context.Context, id core.DownloadID) error {
	route, native, _, err := r.owner(ctx, id)
	if err != nil {
		return err
	}
	if err := route.Engine.Pause(ctx, native); err != nil {
		return err
	}
	if route.gated() {
		// A person asked for this, so it stops being Caravan's hold and starts
		// being theirs: it reads as paused from here, and its slot goes to the
		// queue behind it.
		route.Admission.Unwait(id)
		route.Admission.Release(id)
	}
	return nil
}

// Resume restarts the download on the engine holding it.
func (r *Router) Resume(ctx context.Context, id core.DownloadID) error {
	route, native, _, err := r.owner(ctx, id)
	if err != nil {
		return err
	}
	if route.gated() && !route.Admission.Request(route.Method, id) {
		// Resuming into a full queue is queued, not running: it stays paused at
		// the client and Caravan starts holding it, so it reads as waiting its
		// turn rather than as a button that did nothing.
		route.Admission.Wait(route.Method, id)
		return nil
	}
	return route.Engine.Resume(ctx, native)
}

// Remove drops the download from the engine holding it.
func (r *Router) Remove(ctx context.Context, id core.DownloadID, deleteData bool) error {
	route, native, _, err := r.owner(ctx, id)
	if err != nil {
		return err
	}
	if err := route.Engine.Remove(ctx, native, deleteData); err != nil {
		return err
	}
	if route.gated() {
		route.Admission.Unwait(id)
		route.Admission.Release(id)
	}
	return nil
}

// Close is a no-op. The router borrows its engines; whoever built the table
// owns their shutdown.
func (r *Router) Close() error { return nil }

// Insight implements core.EngineInsight by delegating to the engine holding
// the download, which may not support it.
func (r *Router) Insight(ctx context.Context, id core.DownloadID) (*core.DownloadInsight, error) {
	route, native, _, err := r.owner(ctx, id)
	if err != nil {
		return nil, err
	}
	insighter, ok := route.Engine.(core.EngineInsight)
	if !ok {
		return nil, fmt.Errorf("%w: peer insight", ErrUnsupported)
	}
	return insighter.Insight(ctx, native)
}

// Retry implements core.EngineRetry by delegating to the engine holding the
// download, which may not support it.
func (r *Router) Retry(ctx context.Context, id core.DownloadID) error {
	route, native, _, err := r.owner(ctx, id)
	if err != nil {
		return err
	}
	retrier, ok := route.Engine.(core.EngineRetry)
	if !ok {
		return fmt.Errorf("%w: retrying a failed download", ErrUnsupported)
	}
	return retrier.Retry(ctx, native)
}

// SetGlobalRates implements half of core.EngineRateLimits. Global limits are
// Caravan's own setting, so they are pushed to every engine that has them and
// ignored by those that do not.
func (r *Router) SetGlobalRates(ctx context.Context, downKbps, upKbps int64) error {
	routes, err := r.table(ctx)
	if err != nil {
		return err
	}
	var failures []error
	for _, route := range routes {
		limiter, ok := route.Engine.(core.EngineRateLimits)
		if !ok {
			continue
		}
		if err := limiter.SetGlobalRates(ctx, downKbps, upKbps); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", route.Name, err))
		}
	}
	return errors.Join(failures...)
}

// SetDownloadRates implements the per-download half of core.EngineRateLimits.
func (r *Router) SetDownloadRates(ctx context.Context, id core.DownloadID, downKbps, upKbps int64) error {
	route, native, _, err := r.owner(ctx, id)
	if err != nil {
		return err
	}
	limiter, ok := route.Engine.(core.EngineRateLimits)
	if !ok {
		return fmt.Errorf("%w: per-download rate limits", ErrUnsupported)
	}
	return limiter.SetDownloadRates(ctx, native, downKbps, upKbps)
}

// Every engine's "I do not have this" is a different sentinel, so an error is
// simply read as "not this one". When nobody claims the download the first
// engine's own error is returned rather than a manufactured one, which keeps
// the single-engine case — a stock Caravan with only the embedded engine —
// reporting exactly what it reported before there was a router.
// owner finds the route holding id, and returns it alongside the engine's own
// handle and the status that proved ownership. The Route comes back because
// what the caller may do next — ration it, hold it — is the route's business,
// not the engine's.
func (r *Router) owner(ctx context.Context, id core.DownloadID) (Route, core.DownloadID, *core.DownloadStatus, error) {
	routes, err := r.table(ctx)
	if err != nil {
		return Route{}, "", nil, err
	}
	var firstErr error
	for _, route := range candidates(routes, id) {
		native, _ := route.native(id)
		status, err := route.Engine.Status(ctx, native)
		if err == nil {
			if status != nil {
				status.Engine = route.Name
				// The caller asked with the qualified handle and everything
				// downstream — the queue row, the grab lookup — is keyed on it,
				// so the engine's bare handle must not leak back out here.
				status.ID = id
			}
			return route, native, status, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return Route{}, "", nil, firstErr
	}
	return Route{}, "", nil, fmt.Errorf("download: status %q: %w", id, ErrNotFound)
}

// candidates narrows the routes a handle may belong to.
//
// A handle carrying a known namespace is asked only of the engine owning that
// namespace — that is the whole point of the prefix, and probing the others
// would reintroduce the "first engine to answer wins" race it removes. A bare
// handle is asked only of the engines that do not namespace, since a prefixing
// engine could not have issued it.
func candidates(routes []Route, id core.DownloadID) []Route {
	var owned []Route
	for _, route := range routes {
		if _, ok := route.native(id); ok && route.IDPrefix != "" {
			owned = append(owned, route)
		}
	}
	if len(owned) > 0 {
		return owned
	}
	var bare []Route
	for _, route := range routes {
		if route.IDPrefix == "" {
			bare = append(bare, route)
		}
	}
	return bare
}

// qualify turns one of this engine's own handles into the namespaced handle
// the rest of Caravan stores and addresses it by.
func (r Route) qualify(id core.DownloadID) core.DownloadID {
	if r.IDPrefix == "" || id == "" {
		return id
	}
	return core.DownloadID(r.IDPrefix + string(id))
}

// native is qualify's inverse: the handle this engine knows the download by,
// and whether the namespaced handle belongs to this route at all.
func (r Route) native(id core.DownloadID) (core.DownloadID, bool) {
	if r.IDPrefix == "" {
		return id, true
	}
	rest, ok := strings.CutPrefix(string(id), r.IDPrefix)
	if !ok {
		return "", false
	}
	return core.DownloadID(rest), true
}

// routeFor picks the engine that is the default for a protocol.
func routeFor(routes []Route, protocol string) (Route, bool) {
	if protocol == "" {
		return Route{}, false
	}
	for _, route := range routes {
		if route.Protocol == protocol {
			return route, true
		}
	}
	return Route{}, false
}

// configureHint says what to configure, in the words the settings screen uses.
// A rejection the user cannot act on is the same as a silent drop.
func configureHint(protocol string) string {
	switch protocol {
	case core.ProtocolUsenet:
		return "set a storage root so the built-in Usenet engine can start, then add a news server under Settings → Usenet servers"
	case core.ProtocolTorrent:
		return "set a storage root so the built-in torrent engine can start, or pick a torrent default under Settings → Download clients"
	default:
		return fmt.Sprintf("unknown release protocol %q", protocol)
	}
}

// Compile-time proof the router is a drop-in for a single engine, optional
// extensions included.
var (
	_ core.Engine           = (*Router)(nil)
	_ core.EngineRouting    = (*Router)(nil)
	_ core.EngineInsight    = (*Router)(nil)
	_ core.EngineRateLimits = (*Router)(nil)
	_ core.EngineRetry      = (*Router)(nil)
)
