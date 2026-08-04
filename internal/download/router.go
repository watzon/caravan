package download

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
}

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
	id, err := route.Engine.Add(ctx, rel, opts)
	if err != nil {
		return "", err
	}
	return route.qualify(id), nil
}

// Status returns the snapshot from whichever engine holds id.
func (r *Router) Status(ctx context.Context, id core.DownloadID) (*core.DownloadStatus, error) {
	_, _, status, err := r.owner(ctx, id)
	if err != nil {
		return nil, err
	}
	return status, nil
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
	out := []core.DownloadStatus{}
	var failures []error
	ok := false
	for _, route := range routes {
		statuses, err := route.Engine.List(ctx)
		// List is the poll: it runs every watcher tick against every engine,
		// which makes it the one operation whose success or failure is a fair
		// reading of whether the client is up.
		if route.Report != nil {
			route.Report(err)
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", route.Name, err))
			continue
		}
		ok = true
		for _, status := range statuses {
			status.Engine = route.Name
			status.ID = route.qualify(status.ID)
			out = append(out, status)
		}
	}
	if !ok && len(failures) > 0 {
		return nil, errors.Join(failures...)
	}
	return out, nil
}

// Pause stops the download on the engine holding it.
func (r *Router) Pause(ctx context.Context, id core.DownloadID) error {
	engine, native, _, err := r.owner(ctx, id)
	if err != nil {
		return err
	}
	return engine.Pause(ctx, native)
}

// Resume restarts the download on the engine holding it.
func (r *Router) Resume(ctx context.Context, id core.DownloadID) error {
	engine, native, _, err := r.owner(ctx, id)
	if err != nil {
		return err
	}
	return engine.Resume(ctx, native)
}

// Remove drops the download from the engine holding it.
func (r *Router) Remove(ctx context.Context, id core.DownloadID, deleteData bool) error {
	engine, native, _, err := r.owner(ctx, id)
	if err != nil {
		return err
	}
	return engine.Remove(ctx, native, deleteData)
}

// Close is a no-op. The router borrows its engines; whoever built the table
// owns their shutdown.
func (r *Router) Close() error { return nil }

// Insight implements core.EngineInsight by delegating to the engine holding
// the download, which may not support it.
func (r *Router) Insight(ctx context.Context, id core.DownloadID) (*core.DownloadInsight, error) {
	engine, native, _, err := r.owner(ctx, id)
	if err != nil {
		return nil, err
	}
	insighter, ok := engine.(core.EngineInsight)
	if !ok {
		return nil, fmt.Errorf("%w: peer insight", ErrUnsupported)
	}
	return insighter.Insight(ctx, native)
}

// Retry implements core.EngineRetry by delegating to the engine holding the
// download, which may not support it.
func (r *Router) Retry(ctx context.Context, id core.DownloadID) error {
	engine, native, _, err := r.owner(ctx, id)
	if err != nil {
		return err
	}
	retrier, ok := engine.(core.EngineRetry)
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
	engine, native, _, err := r.owner(ctx, id)
	if err != nil {
		return err
	}
	limiter, ok := engine.(core.EngineRateLimits)
	if !ok {
		return fmt.Errorf("%w: per-download rate limits", ErrUnsupported)
	}
	return limiter.SetDownloadRates(ctx, native, downKbps, upKbps)
}

// owner finds the engine holding id, the engine-native handle to address it
// with, and the status it reported while doing so — the lookup is a Status
// call, so returning it saves the caller a second one.
//
// Every engine's "I do not have this" is a different sentinel, so an error is
// simply read as "not this one". When nobody claims the download the first
// engine's own error is returned rather than a manufactured one, which keeps
// the single-engine case — a stock Caravan with only the embedded engine —
// reporting exactly what it reported before there was a router.
func (r *Router) owner(ctx context.Context, id core.DownloadID) (core.Engine, core.DownloadID, *core.DownloadStatus, error) {
	routes, err := r.table(ctx)
	if err != nil {
		return nil, "", nil, err
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
			return route.Engine, native, status, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	if firstErr != nil {
		return nil, "", nil, firstErr
	}
	return nil, "", nil, fmt.Errorf("download: status %q: %w", id, ErrNotFound)
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
