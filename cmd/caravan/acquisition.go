package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/cardigann"
	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/clients/nzbget"
	"github.com/watzon/caravan/internal/clients/qbittorrent"
	"github.com/watzon/caravan/internal/clients/sabnzbd"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/indexer"
	"github.com/watzon/caravan/internal/indexer/catalog"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/usenet"
)

// indexerTimeout bounds a single Torznab/Newznab call. An interactive search
// fans out across every enabled indexer and waits for all of them, so a dead
// indexer must time out well inside a user's patience.
const indexerTimeout = 30 * time.Second

// managedDefinitionSourceURL is a variable only so the command package's real
// runServe tests can disable external network access. Production uses the
// Prowlarr-compatible definition service by default.
var managedDefinitionSourceURL = cardigann.DefaultManagedDefinitionURL

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

// newIndexerFactory routes remote configurations to the Torznab/Newznab client
// and local definition configurations to Caravan's scraper engine. Local
// definitions use a separate restricted transport so tracker-controlled
// redirects cannot reach private services on the Caravan host.
func newIndexerFactory() (api.IndexerFactory, error) {
	runtime, err := newIndexerRuntime("", slog.Default(), nil)
	if err != nil {
		return nil, err
	}
	return runtime.factory, nil
}

type indexerRuntime struct {
	factory          api.IndexerFactory
	definitions      api.LocalDefinitionLookup
	exactDefinitions api.ExactLocalDefinitionLookup
	managedStatuses  []catalog.ExecutionStatus
}

// settingReader is the one store method the indexer factory needs at request time.
type settingReader interface {
	GetSetting(context.Context, string) (string, error)
}

// flareSolverrFromSettings builds the solver from the saved URL on each call so
// a settings change applies to the next search without a restart.
func flareSolverrFromSettings(settings settingReader, logger *slog.Logger) func(context.Context) *cardigann.FlareSolverr {
	return func(ctx context.Context) *cardigann.FlareSolverr {
		if settings == nil {
			return nil
		}
		raw, err := settings.GetSetting(ctx, store.SettingFlareSolverrURL)
		if err != nil || strings.TrimSpace(raw) == "" {
			return nil
		}
		solver, err := cardigann.NewFlareSolverr(raw, nil)
		if err != nil {
			logger.Warn("ignoring invalid FlareSolverr URL", "error", err)
			return nil
		}
		return solver
	}
}

func newIndexerRuntime(dataDir string, logger *slog.Logger, settings settingReader, packStores ...definitionPackRuntimeStore) (*indexerRuntime, error) {
	if len(packStores) > 1 {
		return nil, fmt.Errorf("indexer runtime accepts at most one definition pack store")
	}
	remoteHTTP := &http.Client{Timeout: indexerTimeout}
	localHTTP := cardigann.NewRestrictedHTTPClient(indexerTimeout)
	definitions, err := cardigann.LoadBuiltins()
	if err != nil {
		return nil, fmt.Errorf("load local indexer definitions: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}
	managedStatuses := []catalog.ExecutionStatus{}
	if strings.TrimSpace(dataDir) != "" {
		snapshot, snapshotErr := cardigann.LoadManagedSnapshot(dataDir)
		switch {
		case snapshotErr == nil:
			provider := snapshot.Provider()
			descriptors, describeErr := cardigann.DescribeProvider(provider)
			if describeErr != nil {
				return nil, fmt.Errorf("describe managed indexer definitions: %w", describeErr)
			}
			manifests, problems := definitions.LoadProvider(provider)
			for _, problem := range problems {
				logger.Warn("managed indexer definition is not executable", "error", problem)
			}
			managedStatuses = managedCatalogExecutionStatuses(descriptors, definitions)
			logger.Info("loaded managed indexer definitions", "revision", snapshot.Revision, "manifests", len(manifests), "addable", countAddableStatuses(managedStatuses), "catalogued", len(managedStatuses))
		case errors.Is(snapshotErr, fs.ErrNotExist):
			// A first run has no cache yet. The serving path refreshes it before
			// constructing this runtime; tests and offline starts stay deterministic.
		default:
			logger.Warn("managed indexer definition cache is unavailable", "error", snapshotErr)
		}
	}
	if len(packStores) == 1 && packStores[0] != nil {
		if err := loadPersistedDefinitionPacks(context.Background(), packStores[0], dataDir, definitions, storeInstalledPackOpener(dataDir, api.Version)); err != nil {
			return nil, fmt.Errorf("load persisted definition packs: %w", err)
		}
	}
	if strings.TrimSpace(dataDir) != "" {
		directory := filepath.Join(dataDir, "indexer-definitions")
		info, statErr := os.Lstat(directory)
		switch {
		case statErr == nil:
			if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
				return nil, fmt.Errorf("user indexer definitions path must be a regular directory")
			}
			canonical, resolveErr := filepath.EvalSymlinks(directory)
			if resolveErr != nil {
				return nil, fmt.Errorf("resolve user indexer definitions directory: %w", resolveErr)
			}
			provider, providerErr := cardigann.NewDirectoryProvider("user", canonical)
			if providerErr != nil {
				return nil, fmt.Errorf("open user indexer definitions: %w", providerErr)
			}
			manifests, problems := definitions.LoadProvider(provider)
			for _, problem := range problems {
				logger.Warn("user indexer definition quarantined", "error", problem)
			}
			logger.Info("user indexer definitions classified",
				"manifests", len(manifests), "quarantined", len(problems))
		case errors.Is(statErr, os.ErrNotExist):
			// Optional by design. The static built-in cohort remains the only
			// advertised catalog when the owner has supplied no definitions.
		case statErr != nil:
			return nil, fmt.Errorf("inspect user indexer definitions directory: %w", statErr)
		}
	}
	lookup := api.LocalDefinitionLookup(func(id string) (api.LocalDefinitionSchema, bool) {
		settings, ok := definitions.SettingNames(id)
		if !ok {
			return api.LocalDefinitionSchema{}, false
		}
		fields, ok := definitions.SettingSchemas(id)
		if !ok {
			return api.LocalDefinitionSchema{}, false
		}
		baseURLs, ok := definitions.BaseURLs(id)
		if !ok {
			return api.LocalDefinitionSchema{}, false
		}
		return api.LocalDefinitionSchema{Settings: settings, Fields: catalogSettingSchemas(fields), BaseURLs: baseURLs}, true
	})
	exactLookup := api.ExactLocalDefinitionLookup(func(id, source, revision, digest string) (api.LocalDefinitionSchema, bool) {
		settings, ok := definitions.SettingNamesExact(id, source, revision, digest)
		if !ok {
			return api.LocalDefinitionSchema{}, false
		}
		fields, ok := definitions.SettingSchemasExact(id, source, revision, digest)
		if !ok {
			return api.LocalDefinitionSchema{}, false
		}
		baseURLs, ok := definitions.BaseURLsExact(id, source, revision, digest)
		if !ok {
			return api.LocalDefinitionSchema{}, false
		}
		return api.LocalDefinitionSchema{Settings: settings, Fields: catalogSettingSchemas(fields), BaseURLs: baseURLs}, true
	})
	return &indexerRuntime{
		factory:          configuredIndexerFactory(definitions, remoteHTTP, localHTTP, flareSolverrFromSettings(settings, logger)),
		definitions:      lookup,
		exactDefinitions: exactLookup,
		managedStatuses:  managedStatuses,
	}, nil
}

func refreshManagedDefinitionCache(ctx context.Context, dataDir string, logger *slog.Logger, client *http.Client, sourceURL string) error {
	if strings.TrimSpace(dataDir) == "" || strings.TrimSpace(sourceURL) == "" {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	snapshot, err := cardigann.FetchManagedSnapshot(ctx, client, sourceURL)
	if err != nil {
		return fmt.Errorf("fetch managed indexer definitions: %w", err)
	}
	if err := cardigann.InstallManagedSnapshot(dataDir, snapshot); err != nil {
		return fmt.Errorf("install managed indexer definitions: %w", err)
	}
	logger.Info("updated managed indexer definitions", "revision", snapshot.Revision)
	return nil
}

func catalogSettingSchemas(fields []cardigann.SettingSchema) []catalog.Setting {
	out := make([]catalog.Setting, 0, len(fields))
	for _, field := range fields {
		options := make([]catalog.SettingOption, 0, len(field.Options))
		for _, option := range field.Options {
			options = append(options, catalog.SettingOption{Value: option.Value, Label: option.Label})
		}
		out = append(out, catalog.Setting{
			Name: field.Name, Label: field.Label, Type: field.Type, Default: field.Default,
			Options: options, Secret: field.Secret, Editable: field.Editable,
		})
	}
	return out
}

func managedCatalogExecutionStatuses(descriptors []cardigann.DefinitionDescriptor, definitions *cardigann.Registry) []catalog.ExecutionStatus {
	statuses := make([]catalog.ExecutionStatus, 0, len(descriptors))
	for _, descriptor := range descriptors {
		if descriptor.Ref.Source != cardigann.ManagedSource || descriptor.MetadataID == "" || !catalog.HasMetadataID(descriptor.MetadataID) {
			continue
		}
		unsupported := make([]string, len(descriptor.Unsupported))
		for i, capability := range descriptor.Unsupported {
			unsupported[i] = string(capability)
		}
		status := catalog.ExecutionStatus{
			MetadataID: descriptor.MetadataID, DefinitionID: descriptor.Ref.String(), Source: descriptor.Ref.Source,
			Revision: descriptor.Revision, Digest: descriptor.Digest, Unsupported: unsupported,
		}
		switch descriptor.State {
		case cardigann.DefinitionStateQuarantined:
			status.State = catalog.InventoryStateQuarantined
			status.BlockedCode = "compiler.invalid"
		case cardigann.DefinitionStateUnsupported:
			status.State = catalog.InventoryStateUnsupported
		case cardigann.DefinitionStateRunnableUnverified:
			fields, fieldsOK := definitions.SettingSchemasExact(status.DefinitionID, status.Source, status.Revision, status.Digest)
			baseURLs, urlsOK := definitions.BaseURLsExact(status.DefinitionID, status.Source, status.Revision, status.Digest)
			if !fieldsOK || !urlsOK || len(baseURLs) == 0 {
				status.State = catalog.InventoryStateRunnableUnverified
				status.BlockedCode = "definition.runtime.unavailable"
				break
			}
			status.State, status.Addable = catalog.InventoryStateVerified, true
			status.BaseURLs = baseURLs
			status.Settings = catalogSettingSchemas(fields)
			if definition, ok := definitions.GetExact(status.DefinitionID, status.Source, status.Revision, status.Digest); ok {
				status.RequiresFlareSolverr = definition.RequiresFlareSolverr()
			}
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func countAddableStatuses(statuses []catalog.ExecutionStatus) int {
	count := 0
	for _, status := range statuses {
		if status.Addable {
			count++
		}
	}
	return count
}

func configuredIndexerFactory(definitions *cardigann.Registry, remoteHTTP, localHTTP *http.Client, solver func(context.Context) *cardigann.FlareSolverr) api.IndexerFactory {
	if solver == nil {
		solver = func(context.Context) *cardigann.FlareSolverr { return nil }
	}
	return func(cfg core.IndexerConfig) api.IndexerClient {
		ref, refErr := cardigann.ParseDefinitionRef(cfg.DefinitionID)
		exactNamespace := refErr == nil && ref.Source != cardigann.BuiltinSource && ref.Source != "user" && ref.Source != cardigann.ManagedSource
		if exactNamespace || cfg.DefinitionSource != "" || cfg.DefinitionRevision != "" || cfg.DefinitionDigest != "" {
			if _, ok := definitions.GetExact(cfg.DefinitionID, cfg.DefinitionSource, cfg.DefinitionRevision, cfg.DefinitionDigest); !ok {
				return unavailableIndexerClient{err: fmt.Errorf("definition %q exact pin is not active in this runtime", cfg.DefinitionID)}
			}
			return cardigann.NewClient(definitions, cfg, localHTTP, cardigann.WithFlareSolverr(solver(context.Background())))
		}
		if cfg.DefinitionID != "" {
			return cardigann.NewClient(definitions, cfg, localHTTP, cardigann.WithFlareSolverr(solver(context.Background())))
		}
		return indexer.New(cfg, remoteHTTP)
	}
}

type unavailableIndexerClient struct {
	err error
}

func (client unavailableIndexerClient) Search(context.Context, string, []int) ([]core.Release, error) {
	return nil, client.err
}

func (client unavailableIndexerClient) Test(context.Context) error {
	return client.err
}

func (client unavailableIndexerClient) Categories(context.Context) ([]core.IndexerCategory, error) {
	return nil, client.err
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
	// seedOnce guards the one-time rebuild of what the external clients are
	// holding paused for us, which only the persisted rows can answer.
	seedOnce sync.Once
	// admission rations download slots across every engine. Like health it is
	// provider-owned and shared: a ceiling that spans the torrent engine and
	// the Usenet engine cannot live inside either of them, and the routers are
	// rebuilt per operation so it cannot live there either.
	admission *download.Admission

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
		admission:       download.NewAdmission(download.Caps{}),
	}
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
	return p.EngineFor(0, "")
}

// EngineFor is Engine for a grab made on behalf of one library: libraryID is
// the item's own library, kind (a core.LibraryKind* constant) answers for an
// item that names none, and (0, "") is an operation that belongs to no
// library — polling the queue, pausing a download — which routes globally.
//
// A library's routing override only ever moves a protocol's *default* (PLAN
// phase 8 task 2). Every other engine stays in the table, because a download
// the library took yesterday through a client it no longer defaults to still
// has to be listable, pausable and removable.
func (p *engineProvider) EngineFor(libraryID int64, kind string) core.Engine {
	if p.embedded() == nil {
		// No storage root, or the engine would not start. Without it there is
		// nowhere for an external client's downloads to be imported to
		// either, so this stays the single "not configured" answer.
		return nil
	}
	return download.NewRouter(func(ctx context.Context) ([]download.Route, error) {
		return p.routesFor(ctx, libraryID, kind)
	})
}

// routes is the download.Table for an operation that belongs to no library.
func (p *engineProvider) routes(ctx context.Context) ([]download.Route, error) {
	return p.routesFor(ctx, 0, "")
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
func (p *engineProvider) routesFor(ctx context.Context, libraryID int64, kind string) ([]download.Route, error) {
	embedded := p.embedded()
	if embedded == nil {
		return nil, nil
	}
	torrentPick, usenetPick, err := p.routePicks(ctx, libraryID, kind)
	if err != nil {
		return nil, err
	}
	configured, err := p.adapter.st.ListEnabledDownloadClients(ctx)
	if err != nil {
		return nil, err
	}
	engines := p.syncClientEngines(configured)
	// A client's own cap lives on its row, so the ledger is refreshed here
	// rather than only on a settings save: editing a client has to take effect
	// on the next queue operation, exactly as its credentials do.
	if settings, err := p.adapter.st.AllSettings(ctx); err == nil {
		p.applyCaps(ctx, settings, configured)
	}
	for _, pick := range engines {
		p.registerClientWake(pick)
	}
	p.seedWaiting(ctx, configured)

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

// routePicks resolves the two routing values a grab for this library must use:
// the library's own override where it set one, the global setting everywhere
// else. An operation belonging to no library ("" kind) reads the globals
// directly, which is what every queue operation does.
//
// A library that is not there — a database not yet migrated past 0012 — falls
// back to the globals rather than failing. Routing is how a download reaches
// an engine at all, and it must keep working while the rest of the schema
// catches up.
func (p *engineProvider) routePicks(ctx context.Context, libraryID int64, kind string) (torrent, usenet int64, err error) {
	settings, err := p.adapter.st.AllSettings(ctx)
	if err != nil {
		return 0, 0, err
	}
	torrentValue, usenetValue := settings[store.SettingRouteTorrent], settings[store.SettingRouteUsenet]
	if libraryID != 0 || kind != "" {
		resolved, err := p.adapter.st.ResolveLibrarySettingsForItem(ctx, libraryID, kind)
		switch {
		case err == nil:
			torrentValue, usenetValue = resolved.RouteTorrent, resolved.RouteUsenet
		case !errors.Is(err, store.ErrNotFound):
			return 0, 0, err
		}
	}
	return routePick(torrentValue), routePick(usenetValue), nil
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
