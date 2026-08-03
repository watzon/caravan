package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/clients/nzbget"
	"github.com/watzon/caravan/internal/clients/qbittorrent"
	"github.com/watzon/caravan/internal/clients/sabnzbd"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/indexer"
	"github.com/watzon/caravan/internal/library"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/usenet"
)

// indexerTimeout bounds a single Torznab/Newznab call. An interactive search
// fans out across every enabled indexer and waits for all of them, so a dead
// indexer must time out well inside a user's patience.
const indexerTimeout = 30 * time.Second

// engineWaitInterval is how often the import watcher re-checks for a download
// engine while none exists. It only matters on a first run, between the
// process starting and the storage root being set from the setup screen.
const engineWaitInterval = 5 * time.Second

// clientEventTimeout bounds the activity-feed write for a download-client
// health transition. It runs off the poll's own context, which may already be
// cancelled by the time a failure is observed.
const clientEventTimeout = 5 * time.Second

// registerDownloadClients installs the external download-client backends this
// build carries into the process-wide registry, which is what
// /api/v1/download-clients probes through. A backend nobody registers is
// storable and configurable but answers its test with 501 (SPEC §5.1, PLAN
// phase 6).
//
// It runs once per process rather than once per serve: the registry is global
// and refuses a duplicate registration, and the smoke tests start and stop
// several servers inside one test binary.
var registerDownloadClients = sync.OnceValue(func() error {
	return errors.Join(
		qbittorrent.Register(clients.Default),
		sabnzbd.Register(clients.Default),
		nzbget.Register(clients.Default),
	)
})

// newIndexerFactory wires api.IndexerFactory to the real Torznab/Newznab
// client. The clients share one http.Client so a fan-out reuses connections
// to an indexer instead of opening one per search.
func newIndexerFactory() api.IndexerFactory {
	hc := &http.Client{Timeout: indexerTimeout}
	return func(cfg core.IndexerConfig) api.IndexerClient {
		return indexer.New(cfg, hc)
	}
}

// downloadPersistence is download.Persistence backed by the `downloads` table.
//
// It exists here rather than in internal/download because that package must
// not import internal/store: the engine is written against a seam so the
// phase-6 external clients can be persisted the same way. This is that seam's
// only production implementation.
type downloadPersistence struct {
	st *store.Store
}

// Save takes d by value per the seam's contract, so the id UpsertDownload
// writes back lands on this copy and is discarded — the engine identifies
// downloads by their engine handle, not by the row id.
func (p downloadPersistence) Save(ctx context.Context, d core.Download) error {
	return p.st.UpsertDownload(ctx, &d)
}

func (p downloadPersistence) Load(ctx context.Context) ([]core.Download, error) {
	return p.st.ListDownloads(ctx)
}

func (p downloadPersistence) Delete(ctx context.Context, id core.DownloadID) error {
	return p.st.DeleteDownloadByEngineID(ctx, id)
}

// engineProvider builds the embedded download engine on first use and hands
// the same one out thereafter.
//
// It is lazy because the engine needs a storage root and a first run does not
// have one yet (SPEC §10.1): the process must start, serve the setup screen,
// and only then be able to download. Construction is also what resumes the
// queue — download.NewEmbedded re-adds everything Persistence remembers — so
// the first Engine call after a restart is the restart's resume.
//
// The root is captured when the engine is built. Re-pointing the storage root
// afterwards does not move a running engine's in-progress data; that takes a
// restart, at which point the new root is picked up like any other.
type engineProvider struct {
	adapter *libraryAdapter
	// paused starts restored downloads paused. Portable mode wants this: a
	// freshly plugged-in drive should not start seeding on its own (SPEC §2.3).
	paused bool
	log    *slog.Logger
	// newClientEngine builds the engine for one external download client row.
	// It is a field so the routing tests can stand in fakes without a machine
	// to talk to.
	newClientEngine func(core.DownloadClientConfig) (core.Engine, error)

	// health tracks which external clients are answering their polls. It is
	// shared by every router the provider hands out — the routers themselves
	// are rebuilt per operation and cannot remember anything.
	health *download.Health

	mu     sync.Mutex
	engine *download.Embedded
	// news is the embedded Usenet engine, built on the same terms as the
	// torrent one: lazily, under the storage root, once there is one.
	news *usenet.Engine
	// lastErr is the last construction failure reported, so an engine that
	// cannot start does not log once per poll.
	lastErr string
	// external holds the engines built from `download_clients` rows, keyed by
	// row id. They are cached rather than rebuilt per call because an engine
	// owns a session (qBittorrent's cookie) and an http.Client's connections;
	// a row that changed, was disabled, or was deleted drops out here.
	external map[int64]*clientEngine
}

// clientEngine is one cached external engine and the configuration it was
// built from.
type clientEngine struct {
	name string
	// fingerprint is a hash of every field the engine was constructed with,
	// including credentials, so an edited row is rebuilt rather than kept
	// talking to the old address with the old password. It is hashed so a
	// credential cannot leak through a log line that prints this struct.
	fingerprint string
	engine      core.Engine
}

func newEngineProvider(adapter *libraryAdapter, paused bool, log *slog.Logger) *engineProvider {
	return &engineProvider{
		adapter:         adapter,
		paused:          paused,
		log:             log,
		newClientEngine: newDownloadClientEngine,
		external:        map[int64]*clientEngine{},
		health:          download.NewHealth(download.DefaultUnhealthyAfter),
	}
}

// newDownloadClientEngine builds the engine for a configured external client.
//
// It is a switch here rather than another registry in internal/clients
// because that package must not import the backends that register into it —
// this is the composition root, and the only place that may know all three.
func newDownloadClientEngine(cfg core.DownloadClientConfig) (core.Engine, error) {
	switch cfg.Type {
	case core.DownloadClientQBittorrent:
		return qbittorrent.NewEngine(cfg, nil)
	case core.DownloadClientSABnzbd:
		return sabnzbd.NewEngine(cfg, nil)
	case core.DownloadClientNZBGet:
		return nzbget.NewEngine(cfg, nil)
	default:
		return nil, fmt.Errorf("unsupported download client type %q", cfg.Type)
	}
}

// clientFingerprint hashes everything an engine is constructed from, so a
// changed row is detected without keeping the credentials it changed in a
// comparable field.
func clientFingerprint(cfg core.DownloadClientConfig) string {
	sum := sha256.Sum256([]byte(strings.Join([]string{
		cfg.Type, cfg.URL, cfg.Username, cfg.Password, cfg.APIKey, cfg.Category,
	}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// Name identifies the backend on the download rows this engine creates.
func (p *engineProvider) Name() string { return download.EngineName }

// Health implements api.HealthReporter for the system panel: "ok" once the
// engine runs, "error" when building it failed, "unconfigured" before a
// storage root exists to build it under.
func (p *engineProvider) Health() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	switch {
	case p.engine != nil:
		return "ok"
	case p.lastErr != "":
		return "error"
	default:
		return "unconfigured"
	}
}

// Engine returns the engine every grab and every queue operation goes
// through: a router over the embedded engine and whatever external clients
// are configured and enabled (PLAN phase 6 task 3).
//
// It is nil when nothing at all is configured, which callers treat as "no
// download engine" and answer 503.
//
// The router is rebuilt from the routing table on each operation rather than
// captured, so adding a download client, disabling one, or moving a
// protocol's default takes effect without a restart — including inside the
// import watcher, which takes one engine at startup and drives it for the
// life of the process.
func (p *engineProvider) Engine() core.Engine {
	return p.EngineFor("")
}

// EngineFor is Engine for a grab made on behalf of one library: kind is one of
// the core.LibraryKind* constants, and "" is an operation that belongs to no
// library — polling the queue, pausing a download — which routes globally.
//
// A library's routing override only ever moves a protocol's *default* (PLAN
// phase 8 task 2). Every other engine stays in the table, because a download
// the library took yesterday through a client it no longer defaults to still
// has to be listable, pausable and removable.
func (p *engineProvider) EngineFor(kind string) core.Engine {
	if p.embedded() == nil {
		// No storage root, or the engine would not start. Without it there is
		// nowhere for an external client's downloads to be imported to
		// either, so this stays the single "not configured" answer.
		return nil
	}
	return download.NewRouter(func(ctx context.Context) ([]download.Route, error) {
		return p.routesFor(ctx, kind)
	})
}

// routes is the download.Table for an operation that belongs to no library.
func (p *engineProvider) routes(ctx context.Context) ([]download.Route, error) {
	return p.routesFor(ctx, "")
}

// routesFor is the download.Table the router resolves through: the embedded
// engine plus every enabled external client, with the default for each
// protocol marked — the library's own pick when it has one, the global setting
// otherwise.
//
// Every enabled client is a route even when it is nobody's default. A client
// that was the default yesterday is still holding the downloads it took, and
// those have to stay listable, pausable and removable — the `downloads.engine`
// column addresses them, not the current routing settings.
func (p *engineProvider) routesFor(ctx context.Context, kind string) ([]download.Route, error) {
	embedded := p.embedded()
	if embedded == nil {
		return nil, nil
	}
	torrentPick, usenetPick, err := p.routePicks(ctx, kind)
	if err != nil {
		return nil, err
	}
	configured, err := p.adapter.st.ListEnabledDownloadClients(ctx)
	if err != nil {
		return nil, err
	}
	engines := p.syncClientEngines(configured)

	// The torrent default is the embedded engine unless a torrent client is
	// picked: a stock Caravan downloads torrents with nothing configured, and
	// a picked client that has since been deleted or disabled must fall back
	// to something that works rather than reject every torrent grab.
	torrentID := int64(0)
	torrent := download.Route{Name: download.EngineName, Protocol: core.ProtocolTorrent, Engine: embedded}
	if pick, ok := engines[torrentPick]; ok && pick.protocol == core.ProtocolTorrent {
		torrentID = pick.id
		torrent = p.clientRoute(pick, core.ProtocolTorrent)
	}
	// Usenet's default is the embedded Usenet engine unless a usenet client is
	// picked — the same rule torrents follow, and the reason a stock Caravan
	// needs no external download client for either protocol (PLAN phase 7
	// task 6). A picked client that has since been deleted or disabled falls
	// back to it rather than leaving every usenet grab unrouted.
	usenetID := int64(0)
	if pick, ok := engines[usenetPick]; ok && pick.protocol == core.ProtocolUsenet {
		usenetID = pick.id
	}
	news := p.embeddedUsenet()
	if news != nil {
		// News-server configuration reaches a running engine here rather than
		// at construction, so adding or editing a server takes effect on the
		// next queue operation instead of at the next restart. It is a no-op
		// when nothing changed.
		p.syncUsenetServers(ctx, news)
	}

	routes := []download.Route{torrent}
	if torrentID != 0 {
		// The embedded engine is not the torrent default any more, but it is
		// still holding whatever it took while it was: in-flight downloads that
		// have to keep reaching the import watcher, and seeding ones the user
		// has to be able to pause and remove. It rejoins the table as a
		// protocol-less route, the same rule every non-default client follows.
		routes = append(routes, download.Route{Name: download.EngineName, Engine: embedded})
	}
	switch {
	case usenetID != 0:
		routes = append(routes, p.clientRoute(engines[usenetID], core.ProtocolUsenet))
		if news != nil {
			// Same reasoning as the embedded torrent engine above: a client
			// taking the default must not strand the downloads the built-in
			// engine is still holding.
			routes = append(routes, download.Route{Name: usenet.EngineName, Engine: news})
		}
	case news != nil:
		routes = append(routes, download.Route{Name: usenet.EngineName, Protocol: core.ProtocolUsenet, Engine: news})
	}
	for _, cfg := range configured {
		pick, ok := engines[cfg.ID]
		// Matched by row id rather than by engine identity: the two defaults
		// are already in the table, and everything else joins as a route with
		// no protocol.
		if !ok || pick.id == usenetID || pick.id == torrentID {
			continue
		}
		routes = append(routes, p.clientRoute(pick, ""))
	}
	return routes, nil
}

// clientRoute builds one external client's route, wiring the health tracker
// into it in both directions: what the last polls said (Unhealthy), and where
// this poll's outcome goes (Report).
//
// The embedded engine deliberately has neither. It is not a client, it cannot
// be unreachable, and a dead seedbox must leave it working exactly as before
// (PLAN phase 6 task 4).
func (p *engineProvider) clientRoute(pick routedEngine, protocol string) download.Route {
	return download.Route{
		Name:      pick.name,
		Protocol:  protocol,
		Engine:    pick.engine,
		IDPrefix:  clientIDPrefix(pick.id),
		Unhealthy: p.health.Reason(pick.id),
		Report:    func(err error) { p.observeClient(pick, err) },
	}
}

// clientIDPrefix namespaces one external client's download handles by the
// `download_clients` row that configured it.
//
// The row id is what makes the namespace stable: it survives restarts, edits
// and renames, so a handle stored today still resolves to the same client
// tomorrow. Two NZBGet clients would otherwise both hand out a download "5",
// and Caravan stores handles bare.
//
// The trailing "." both separates the id from the handle and keeps "c1." from
// prefix-matching "c11."; it is not a character any backend's handles contain
// (info hashes are hex, nzo_ids are word characters, NZBIDs are integers), so a
// namespaced handle can never be mistaken for a bare one.
func clientIDPrefix(id int64) string { return "c" + strconv.FormatInt(id, 10) + "." }

// observeClient feeds one poll outcome to the health tracker and records the
// transitions it causes. Only transitions reach the feed: a client that is
// down is down every poll, and the activity feed is for the user.
func (p *engineProvider) observeClient(pick routedEngine, err error) {
	switch p.health.Observe(pick.id, pick.label, pick.name, err) {
	case download.HealthDown:
		p.log.Warn("download client unreachable", "client", pick.label, "type", pick.name, "error", err)
		p.recordClientEvent(core.EventLevelWarn,
			fmt.Sprintf("Download client %s is unreachable", pick.label),
			fmt.Sprintf("%s: its downloads stop updating and new grabs routed to it are refused until it answers again", err))
	case download.HealthUp:
		p.log.Info("download client reachable again", "client", pick.label, "type", pick.name)
		p.recordClientEvent(core.EventLevelInfo,
			fmt.Sprintf("Download client %s is reachable again", pick.label),
			"its queue is being polled again")
	}
}

// recordClientEvent appends a health transition to the activity feed. It uses
// its own context because the poll's may already be cancelled by the time a
// failure is observed, and it swallows failures: an event is history, and
// losing one must never break the poll that produced it (SPEC §7).
func (p *engineProvider) recordClientEvent(level, message, detail string) {
	ctx, cancel := context.WithTimeout(context.Background(), clientEventTimeout)
	defer cancel()
	if err := p.adapter.st.InsertEvent(ctx, &core.Event{
		Level:    level,
		Category: "download",
		Message:  message,
		Detail:   detail,
	}); err != nil {
		p.log.Error("record download client event", "error", err)
	}
}

// UnhealthyDownloadClients implements api.DownloadClientHealthReporter, which
// is what puts the "client X unreachable" banner on screen.
func (p *engineProvider) UnhealthyDownloadClients() []core.DownloadClientHealth {
	return p.health.Unhealthy()
}

// routedEngine is one enabled client's engine plus what routes decides on.
type routedEngine struct {
	id int64
	// name is the backend's type ("qbittorrent"), which is what a download row
	// records; label is the user's own name for this client, which is what a
	// banner or an event has to say.
	name     string
	label    string
	protocol string
	engine   core.Engine
}

// syncClientEngines brings the external engine cache in line with the enabled
// rows: builds what is new or changed, and closes what is no longer enabled,
// no longer present, or was edited. Returns the live set keyed by row id.
func (p *engineProvider) syncClientEngines(configured []core.DownloadClientConfig) map[int64]routedEngine {
	p.mu.Lock()
	defer p.mu.Unlock()

	live := make(map[int64]routedEngine, len(configured))
	keep := make(map[int64]bool, len(configured))
	for _, cfg := range configured {
		t, ok := clients.Lookup(cfg.Type)
		if !ok {
			continue
		}
		fingerprint := clientFingerprint(cfg)
		cached, hit := p.external[cfg.ID]
		if hit && cached.fingerprint != fingerprint {
			p.closeClientEngine(cached)
			delete(p.external, cfg.ID)
			hit = false
		}
		if !hit {
			engine, err := p.newClientEngine(cfg)
			if err != nil {
				// Reported once: an unusable row must not log per poll, and
				// the download client's test button is where the user is told
				// about it in detail.
				p.reportLocked("build download client engine", fmt.Errorf("%s: %w", cfg.Name, err))
				continue
			}
			cached = &clientEngine{name: cfg.Type, fingerprint: fingerprint, engine: engine}
			p.external[cfg.ID] = cached
			p.log.Info("download client ready", "client", cfg.Name, "type", cfg.Type)
		}
		keep[cfg.ID] = true
		live[cfg.ID] = routedEngine{
			id: cfg.ID, name: cached.name, label: cfg.Name, protocol: t.Protocol, engine: cached.engine,
		}
	}
	for id, cached := range p.external {
		if keep[id] {
			continue
		}
		p.closeClientEngine(cached)
		delete(p.external, id)
	}
	// A client that is gone or switched off cannot be unreachable, and a
	// banner about one the user just deleted is a banner they cannot dismiss.
	p.health.Retain(keep)
	return live
}

// closeClientEngine shuts a cached engine down. A close that fails costs a
// session, never media, so it is logged rather than propagated.
func (p *engineProvider) closeClientEngine(c *clientEngine) {
	if err := c.engine.Close(); err != nil {
		p.log.Error("closing download client engine", "error", err, "type", c.name)
	}
}

// routePicks resolves the two routing values a grab for this library must use:
// the library's own override where it set one, the global setting everywhere
// else. An operation belonging to no library ("" kind) reads the globals
// directly, which is what every queue operation does.
//
// A library that is not there — a database not yet migrated past 0012 — falls
// back to the globals rather than failing. Routing is how a download reaches
// an engine at all, and it must keep working while the rest of the schema
// catches up.
func (p *engineProvider) routePicks(ctx context.Context, kind string) (torrent, usenet int64, err error) {
	settings, err := p.adapter.st.AllSettings(ctx)
	if err != nil {
		return 0, 0, err
	}
	torrentValue, usenetValue := settings[store.SettingRouteTorrent], settings[store.SettingRouteUsenet]
	if kind != "" {
		resolved, err := p.adapter.st.ResolveLibrarySettingsByKind(ctx, kind)
		switch {
		case err == nil:
			torrentValue, usenetValue = resolved.RouteTorrent, resolved.RouteUsenet
		case !errors.Is(err, store.ErrNotFound):
			return 0, 0, err
		}
	}
	return routePick(torrentValue), routePick(usenetValue), nil
}

// routePick reads a routing value as a `download_clients.id`. The embedded
// engine and an unset or malformed value are both 0, which matches no row.
func routePick(value string) int64 {
	id, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || id <= 0 {
		return 0
	}
	return id
}

// embedded returns the built-in torrent engine, or nil when there is no
// storage root to build one under or building one failed. A failure is logged
// here, since it is the only place that knows the difference.
func (p *engineProvider) embedded() core.Engine {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.engine != nil {
		return p.engine
	}

	ctx := context.Background()
	root, err := p.adapter.StorageRoot(ctx)
	if err != nil {
		p.reportLocked("read storage root", err)
		return nil
	}
	if root == "" {
		return nil
	}
	settings, err := p.adapter.st.AllSettings(ctx)
	if err != nil {
		p.reportLocked("read engine settings", err)
		return nil
	}
	opts, err := engineOptions(settings, p.paused, p.log)
	if err != nil {
		p.reportLocked("read engine settings", err)
		return nil
	}
	opts.Store = downloadPersistence{st: p.adapter.st}
	engine, err := download.NewEmbedded(root, opts)
	if err != nil {
		p.reportLocked("start download engine", err)
		return nil
	}

	p.engine = engine
	p.lastErr = ""
	p.log.Info("download engine ready", "root", root, "incomplete", download.IncompleteDir)
	return engine
}

// embeddedUsenet returns the built-in Usenet engine, or nil when there is no
// storage root to build one under or building one failed.
//
// It is built on exactly the terms the torrent engine is: lazily, because a
// first run has no storage root yet (SPEC §10.1), and once, because
// construction is also what resumes the queue. No configured news server is
// not a reason to withhold it — the engine still has to list, pause and remove
// whatever it already holds, and it tells the user to add a server when they
// try to grab.
func (p *engineProvider) embeddedUsenet() *usenet.Engine {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.news != nil {
		return p.news
	}

	ctx := context.Background()
	root, err := p.adapter.StorageRoot(ctx)
	if err != nil {
		p.reportLocked("read storage root", err)
		return nil
	}
	if root == "" {
		return nil
	}
	servers, err := p.adapter.st.ListEnabledUsenetServers(ctx)
	if err != nil {
		p.reportLocked("read usenet servers", err)
		return nil
	}
	engine, err := usenet.NewEngine(root, usenet.EngineOpts{
		Servers: usenet.ServerConfigs(servers),
		Store:   downloadPersistence{st: p.adapter.st},
		Paused:  p.paused,
		Logger:  p.log,
	})
	if err != nil {
		p.reportLocked("start usenet engine", err)
		return nil
	}

	p.news = engine
	p.log.Info("usenet engine ready", "root", root, "servers", len(servers))
	return engine
}

// syncUsenetServers pushes the enabled `usenet_servers` rows into a running
// engine.
//
// This is how a settings change propagates without a restart, and it is
// deliberately the same shape as syncClientEngines: the routing table is
// rebuilt per operation, so re-reading the rows here is what makes "add a
// backup provider" take effect on the next queue poll. SetServers compares a
// fingerprint and does nothing when the configuration is unchanged, so the
// common case costs one small query and no dropped connections.
func (p *engineProvider) syncUsenetServers(ctx context.Context, engine *usenet.Engine) {
	servers, err := p.adapter.st.ListEnabledUsenetServers(ctx)
	if err != nil {
		p.mu.Lock()
		p.reportLocked("read usenet servers", err)
		p.mu.Unlock()
		return
	}
	if err := engine.SetServers(usenet.ServerConfigs(servers)); err != nil {
		p.mu.Lock()
		p.reportLocked("apply usenet servers", err)
		p.mu.Unlock()
	}
}

// reportLocked logs a construction failure, skipping a repeat of the one
// already reported. Must be called with p.mu held.
func (p *engineProvider) reportLocked(msg string, err error) {
	if err.Error() == p.lastErr {
		return
	}
	p.lastErr = err.Error()
	p.log.Error(msg, "error", err)
}

// Close shuts the engines down, flushing the embedded queue's state so the
// next start resumes it. Closing before an engine was ever built is not an
// error.
func (p *engineProvider) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	for id, cached := range p.external {
		p.closeClientEngine(cached)
		delete(p.external, id)
	}

	var failures []error
	if news := p.news; news != nil {
		p.news = nil
		// Closing flushes each download's resume sidecar, which is what makes
		// the next start continue a half-finished release rather than refetch
		// the articles it already has.
		failures = append(failures, news.Close())
	}
	if engine := p.engine; engine != nil {
		p.engine = nil
		failures = append(failures, engine.Close())
	}
	return errors.Join(failures...)
}

// ApplyEngineSettings updates rates and seeding targets on an existing engine.
// Listen port and connection count are ClientConfig fields, so they apply when
// the next engine starts rather than disrupting active peer connections.
func (p *engineProvider) ApplyEngineSettings(ctx context.Context, settings map[string]string) error {
	opts, err := engineOptions(settings, p.paused, p.log)
	if err != nil {
		return err
	}
	p.mu.Lock()
	engine := p.engine
	p.mu.Unlock()
	if engine == nil {
		return nil
	}
	if err := engine.SetGlobalRates(ctx, opts.MaxDownKBps, opts.MaxUpKBps); err != nil {
		return err
	}
	return engine.SetSeedingTargets(opts.SeedRatio, opts.SeedDays)
}

func engineOptions(settings map[string]string, paused bool, log *slog.Logger) (download.EmbeddedOpts, error) {
	listenPort, err := engineSettingInt(settings, store.SettingEngineListenPort)
	if err != nil {
		return download.EmbeddedOpts{}, err
	}
	maxConnections, err := engineSettingInt(settings, store.SettingEngineMaxConnections)
	if err != nil {
		return download.EmbeddedOpts{}, err
	}
	maxDownKBps, err := engineSettingInt64(settings, store.SettingEngineMaxDownKBps)
	if err != nil {
		return download.EmbeddedOpts{}, err
	}
	maxUpKBps, err := engineSettingInt64(settings, store.SettingEngineMaxUpKBps)
	if err != nil {
		return download.EmbeddedOpts{}, err
	}
	seedRatio, err := engineSettingFloat(settings, store.SettingEngineSeedRatio)
	if err != nil {
		return download.EmbeddedOpts{}, err
	}
	seedDays, err := engineSettingInt(settings, store.SettingEngineSeedDays)
	if err != nil {
		return download.EmbeddedOpts{}, err
	}
	return download.EmbeddedOpts{
		ListenPort:     listenPort,
		MaxConnections: maxConnections,
		MaxDownKBps:    maxDownKBps,
		MaxUpKBps:      maxUpKBps,
		SeedRatio:      seedRatio,
		SeedDays:       seedDays,
		Paused:         paused,
		Logger:         log,
	}, nil
}

func engineSettingInt(settings map[string]string, key string) (int, error) {
	value := strings.TrimSpace(settings[key])
	if value == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return n, nil
}

func engineSettingInt64(settings map[string]string, key string) (int64, error) {
	value := strings.TrimSpace(settings[key])
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return n, nil
}

func engineSettingFloat(settings map[string]string, key string) (float64, error) {
	value := strings.TrimSpace(settings[key])
	if value == "" {
		return 0, nil
	}
	n, err := strconv.ParseFloat(value, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("invalid %s", key)
	}
	return n, nil
}

// await blocks until an engine for the given library kind exists, returning nil
// if ctx is done first. "" is an operation belonging to no library.
func (p *engineProvider) await(ctx context.Context, kind string) core.Engine {
	ticker := time.NewTicker(engineWaitInterval)
	defer ticker.Stop()
	for {
		if engine := p.EngineFor(kind); engine != nil {
			return engine
		}
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

// runImportWatcher runs the import pipeline's clock (SPEC §5.1) until ctx is
// done: poll the engine, persist what it reports, and import what finished.
//
// It waits for an engine rather than requiring one, so a first run reaches the
// setup screen and then starts importing without a restart.
func runImportWatcher(ctx context.Context, engines *engineProvider, adapter *libraryAdapter, log *slog.Logger) {
	engine := engines.await(ctx, "")
	if engine == nil {
		return
	}

	root, err := adapter.StorageRoot(ctx)
	if err != nil {
		log.Error("import watcher: read storage root", "error", err)
		return
	}
	mgr := adapter.watcherManager(root)

	log.Info("import watcher started", "interval", library.DefaultWatchInterval)
	if err := mgr.RunWatcher(ctx, engine, library.DefaultWatchInterval); err != nil && ctx.Err() == nil {
		log.Error("import watcher stopped", "error", err)
	}
}

// lateMetadata is a core.MetadataProvider that resolves the real one on every
// call from the settings in force at that moment.
//
// The HTTP layer gets late binding for free, because it asks the adapter for a
// provider per request (see libraryAdapter). A watcher does not: it holds one
// library.Manager for the life of the process, so without this a TMDB key
// added after startup would never reach it — and an import with no provider
// does not park, it errors, so the job would burn its retries and fail for
// good.
type lateMetadata struct {
	adapter *libraryAdapter
}

func (l lateMetadata) SearchMovies(ctx context.Context, q string) ([]core.MovieMeta, error) {
	p, err := l.provider(ctx)
	if err != nil {
		return nil, err
	}
	return p.SearchMovies(ctx, q)
}

func (l lateMetadata) SearchSeries(ctx context.Context, q string) ([]core.SeriesMeta, error) {
	p, err := l.provider(ctx)
	if err != nil {
		return nil, err
	}
	return p.SearchSeries(ctx, q)
}

func (l lateMetadata) GetMovie(ctx context.Context, tmdbID int64) (*core.MovieMeta, error) {
	p, err := l.provider(ctx)
	if err != nil {
		return nil, err
	}
	return p.GetMovie(ctx, tmdbID)
}

func (l lateMetadata) GetSeries(ctx context.Context, tmdbID int64) (*core.SeriesMeta, error) {
	p, err := l.provider(ctx)
	if err != nil {
		return nil, err
	}
	return p.GetSeries(ctx, tmdbID)
}

// provider resolves the configured provider, reporting the absence of one as
// core.ErrNoMetadataProvider — the same error a nil provider produces, so
// callers cannot tell the two apart.
func (l lateMetadata) provider(ctx context.Context) (core.MetadataProvider, error) {
	p := l.adapter.metadata(ctx)
	if p == nil {
		return nil, core.ErrNoMetadataProvider
	}
	return p, nil
}

// Compile-time proof that the wiring is what its consumers expect.
var (
	_ api.EngineProvider        = (*engineProvider)(nil)
	_ api.LibraryEngineProvider = (*engineProvider)(nil)
	_ core.MetadataProvider     = lateMetadata{}
	_ download.Persistence      = downloadPersistence{}
)
