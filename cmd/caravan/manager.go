package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/watzon/caravan/internal/anilist"
	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/library"
	"github.com/watzon/caravan/internal/stashbox"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/tmdb"
	"github.com/watzon/caravan/internal/tvmaze"
)

// metadataTimeout bounds a single provider HTTP call.
const metadataTimeout = 30 * time.Second

// libraryAdapter satisfies api.Manager on top of *library.Manager.
//
// It exists for two reasons:
//
//   - Signature reconciliation. api.Manager is the narrow slice the HTTP layer
//     needs (see internal/api/manager.go); library.Manager's own methods return
//     richer results the API does not send on the wire.
//
//   - Late binding of the storage root and the TMDB API key. Both live in the
//     settings table and are editable from the UI at runtime (SPEC §10, §10.1:
//     first run *is* setting the storage root and then scanning). A
//     library.Manager captures both at construction, so building one at startup
//     would pin whatever was configured then and quietly ignore every later
//     change. Building one per call keeps the settings table authoritative;
//     cached clients preserve provider-local state between those calls.
//     NewManager does no I/O, so this costs one settings query.
type libraryAdapter struct {
	st *store.Store
	// fallbackRoot is the bootstrap config's storage_root, used only until the
	// settings table has one.
	fallbackRoot string
	hc           *http.Client
	log          *slog.Logger
	// notify is the playback handoff every Manager this adapter builds carries,
	// so an import made through the API notifies Jellyfin exactly as one made
	// by the download watcher does.
	notify library.Notifier
	// notifyAdult is the adult library's handoff (Stash), carried for the same
	// reason notify is. It is a separate seam because the two are told about
	// disjoint sets of imports — see library.AdultNotifier.
	notifyAdult library.AdultNotifier

	// adultMu guards adult, which is read and replaced from concurrent HTTP
	// handlers.
	adultMu sync.Mutex
	// adult is the last stash-box client built, kept alongside the settings it
	// was built from; see stashboxClient.
	adult *cachedStashbox
	// tmdbMu guards tmdb, which is read and replaced from concurrent HTTP
	// handlers.
	tmdbMu sync.Mutex
	// tmdb is the last TMDB client built, kept alongside the API key it was
	// built from; see tmdbClient.
	tmdb *cachedTMDB
	// anilistMu guards anilist, which is read and replaced from concurrent
	// HTTP handlers.
	anilistMu sync.Mutex
	// anilist is THE AniList client for this process — there is no cache key
	// beside it because there is no setting to key on; see anilistClient.
	anilist *anilist.Client
	// tvmazeMu guards tvmaze, which is read and replaced from concurrent HTTP
	// handlers.
	tvmazeMu sync.Mutex
	// tvmaze is THE TVmaze client for this process, for the same reason anilist
	// is the only AniList one; see tvmazeClient.
	tvmaze *tvmaze.Client
}

// cachedTMDB is one TMDB client and the setting that defines it.
type cachedTMDB struct {
	key    string
	client *tmdb.Client
}

// cachedStashbox is one stash-box client and the two settings that define it.
// The settings are the cache key rather than a generation counter because they
// are the whole of what New takes: if neither changed, the client that would be
// built is the client already held.
type cachedStashbox struct {
	key      string
	endpoint string
	client   *stashbox.Client
}

func newLibraryAdapter(st *store.Store, fallbackRoot string, log *slog.Logger, notify library.Notifier, notifyAdult library.AdultNotifier) *libraryAdapter {
	return &libraryAdapter{
		st:           st,
		fallbackRoot: fallbackRoot,
		hc:           &http.Client{Timeout: metadataTimeout},
		log:          log,
		notify:       notify,
		notifyAdult:  notifyAdult,
	}
}

// current builds a library.Manager from the settings in force right now.
func (a *libraryAdapter) current(ctx context.Context) (*library.Manager, error) {
	root, err := a.StorageRoot(ctx)
	if err != nil {
		return nil, err
	}
	return library.NewManager(a.st, a.metadata(ctx), root,
		library.WithNotifier(a.notify),
		library.WithAdultNotifier(a.notifyAdult),
		library.WithAdultProvider(a.adultMetadata(ctx)),
		library.WithProviders(providerRegistry{a}),
	), nil
}

// providerRegistry resolves a library's configured provider id to a client
// (library.Providers). It is a separate type because libraryAdapter already
// answers api.Manager's argument-less Metadata. The ids come from core's
// registry, the clients from the same cached builders the Manager-level
// fields use, so the registry and the fallback can never answer differently
// for the ids that exist today. An unknown id is a genuine untyped nil, for
// the reason the builders' own nils are.
type providerRegistry struct{ a *libraryAdapter }

func (p providerRegistry) Metadata(ctx context.Context, providerID string) core.MetadataProvider {
	switch providerID {
	case core.ProviderTMDB:
		return p.a.metadata(ctx)
	case core.ProviderAniList:
		return p.a.anilistClient()
	case core.ProviderTVmaze:
		return p.a.tvmazeClient()
	}
	return nil
}

func (p providerRegistry) Adult(ctx context.Context, providerID string) core.AdultMetadataProvider {
	if providerID == core.ProviderStashbox {
		return p.a.adultMetadata(ctx)
	}
	return nil
}

// metadataOnlyRegistry is providerRegistry with the adult half removed. The
// watcher is the one Manager that outlives a settings change, and the reason
// it carries no adult provider (see watcherManager) is unchanged by its
// needing the metadata half.
//
// It EMBEDS providerRegistry rather than restating the metadata switch, so a
// provider added there reaches the watcher without a second edit — which is
// how AniList and TVmaze both got there.
type metadataOnlyRegistry struct{ providerRegistry }

func (metadataOnlyRegistry) Adult(context.Context, string) core.AdultMetadataProvider { return nil }

// adultMetadata returns the stash-box provider, or nil.
//
// It is nil unless the module is switched on AND a credential has been entered,
// and the order matters: the switch is read first, so a server with adult
// content disabled builds no client at all. library.adultReady checks the same
// switch, which makes this the second of two independent reasons the endpoint
// cannot be reached when the module is off — the acceptance criterion is zero
// requests, and one guard is one bug away from zero guards.
//
// The nil is a genuine untyped nil for the same reason Metadata's is: callers
// test the interface value against nil, and a typed nil *stashbox.Client would
// pass that test and then make a request.
//
// The settings are still read on every call — turning the module off has to take
// effect at once — but the client built from them is reused; see stashboxClient.
func (a *libraryAdapter) adultMetadata(ctx context.Context) core.AdultMetadataProvider {
	enabled, err := a.st.AdultEnabled(ctx)
	if err != nil {
		a.log.Error("read adult setting", "error", err)
		return nil
	}
	if !enabled {
		return nil
	}
	key, err := a.setting(ctx, store.SettingStashboxAPIKey)
	if err != nil {
		a.log.Error("read stash-box api key", "error", err)
		return nil
	}
	if key == "" {
		return nil
	}
	endpoint, err := a.setting(ctx, store.SettingStashboxEndpoint)
	if err != nil {
		a.log.Error("read stash-box endpoint", "error", err)
		return nil
	}
	return a.stashboxClient(key, endpoint)
}

// stashboxClient returns the client for these settings, reusing the last one
// while they are unchanged.
//
// Unlike the TMDB client, a stash-box client learns something: the first search
// against an endpoint discovers whether it implements queryStudios, and TPDB —
// the default endpoint — does not. That memo lives on the client, so building a
// fresh one per call threw the answer away and sent one doomed request per
// search; a typeahead box did it per keystroke.
//
// A change to either setting still builds a new client, which is the point of
// keying on them: pointing at a different endpoint must re-probe rather than
// inherit what was true of the last one. Nothing is invalidated on a timer — a
// restart is the re-check, the same rule the memo itself follows.
func (a *libraryAdapter) stashboxClient(key, endpoint string) *stashbox.Client {
	a.adultMu.Lock()
	defer a.adultMu.Unlock()

	if c := a.adult; c != nil && c.key == key && c.endpoint == endpoint {
		return c.client
	}
	client := stashbox.New(key, endpoint, a.hc)
	a.adult = &cachedStashbox{key: key, endpoint: endpoint, client: client}
	return client
}

// anilistClient returns THE AniList client, building it once.
//
// It takes no cache key because there is nothing to key on: AniList serves
// anonymous reads, so no setting can change what a client for it would be, and
// the endpoint is a constant rather than a preset (see anilist.DefaultEndpoint).
// "Same settings, same client" therefore collapses into "same client, always".
//
// Reuse is not an optimisation here, it is the contract. The client carries the
// rate limiter for a per-minute budget that AniList enforces per CALLER, not per
// connection — one logical lookup can cost several requests — so a client built
// per call would hand every caller a fresh, empty budget and the process would
// walk straight into the limit it exists to respect. That is the same
// provider-local state stashboxClient's cache exists to preserve; only the key
// differs.
func (a *libraryAdapter) anilistClient() *anilist.Client {
	a.anilistMu.Lock()
	defer a.anilistMu.Unlock()

	if a.anilist == nil {
		a.anilist = anilist.New(a.hc)
	}
	return a.anilist
}

// tvmazeClient returns THE TVmaze client, building it once.
//
// It takes no cache key for the same reason anilistClient does not: TVmaze's
// read API is public, so no setting can change what a client for it would be,
// and the host is a constant rather than a preset (see tvmaze.DefaultBaseURL).
//
// Reuse is the contract here too, not an optimisation. The client carries the
// throttle for a budget TVmaze enforces per CALLER — and one GetSeries costs
// two requests — so a client built per call would hand every caller a fresh,
// empty budget and the process would walk straight into the limit the throttle
// exists to respect.
func (a *libraryAdapter) tvmazeClient() *tvmaze.Client {
	a.tvmazeMu.Lock()
	defer a.tvmazeMu.Unlock()

	if a.tvmaze == nil {
		a.tvmaze = tvmaze.New(a.hc)
	}
	return a.tvmaze
}

// watcherManager builds the one library.Manager the import watcher holds for
// the life of the process.
//
// It is here rather than inline in the watcher so it cannot drift from current:
// the two differ only in where the root and the provider come from — the
// watcher's root is fixed at startup and its provider is resolved per call
// (lateMetadata) — and everything else, the playback handoff included, has to
// be the same. A watcher without the notifier is the phase-4 acceptance
// criterion silently unmet: automatic imports would land files and never tell
// Jellyfin to rescan.
//
// It carries no adult provider, deliberately. Importing a finished scene
// download makes no stash-box call — the site's catalogue was walked when the
// site was added, and a grab is only ever made against an episode row that
// already exists — so handing the one long-lived Manager in the process a
// client for the endpoint would create a path to it that nothing uses and the
// zero-traffic acceptance would have to defend.
// The adult *notifier* is a different thing and is carried: it makes no
// provider call either — it records that a scan and an identity push are owed —
// and the watcher is the path a downloaded scene actually arrives by, so
// leaving it out would make PLAN phase 11's acceptance silently unmet in
// exactly the way a watcher without the playback handoff would.
//
// It DOES carry the metadata registry, through metadataOnlyRegistry. Without
// one, every library the watcher imports for resolves to the Manager-level
// provider alone, so an item pinned to any other provider could not be fetched
// at all and its download would park — a Manager that answers differently from
// every other Manager in the process, which is precisely what this function
// exists to prevent.
func (a *libraryAdapter) watcherManager(root string) *library.Manager {
	return library.NewManager(a.st, lateMetadata{adapter: a}, root,
		library.WithNotifier(a.notify),
		library.WithAdultNotifier(a.notifyAdult),
		library.WithProviders(metadataOnlyRegistry{providerRegistry{a}}))
}

// StorageRoot is the storage root in force right now: the settings table's
// value, or the bootstrap config's until the table has one. It returns the
// empty string when neither has been configured — a first run, before the
// setup screen has been through (SPEC §10.1).
//
// The download engine resolves its data directory through here too, so the
// library and the queue can never disagree about where the storage root is.
func (a *libraryAdapter) StorageRoot(ctx context.Context) (string, error) {
	root, err := a.setting(ctx, store.SettingStorageRoot)
	if err != nil {
		return "", err
	}
	if root == "" {
		root = a.fallbackRoot
	}
	return root, nil
}

// setting reads one setting, treating "never set" as the empty string.
func (a *libraryAdapter) setting(ctx context.Context, key string) (string, error) {
	value, err := a.st.GetSetting(ctx, key)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return "", nil
		}
		return "", err
	}
	return value, nil
}

// Metadata returns the configured provider, or nil when there is no API key.
//
// The setting is read on every call so runtime edits take effect immediately.
// A non-empty key reuses the matching client and its provider-local caches.
// The nil is a genuine untyped nil rather than a nil *tmdb.Client, because
// callers test the interface value against nil and a typed nil would pass that
// test and then panic (SPEC §13: no key degrades to parse-only, it does not
// crash).
func (a *libraryAdapter) Metadata() core.MetadataProvider {
	return a.metadata(context.Background())
}

func (a *libraryAdapter) metadata(ctx context.Context) core.MetadataProvider {
	key, err := a.setting(ctx, store.SettingTMDBAPIKey)
	if err != nil {
		a.log.Error("read tmdb api key", "error", err)
		return nil
	}

	a.tmdbMu.Lock()
	defer a.tmdbMu.Unlock()

	if key == "" {
		a.tmdb = nil
		return nil
	}
	if c := a.tmdb; c != nil && c.key == key {
		return c.client
	}
	client := tmdb.New(key, a.hc)
	a.tmdb = &cachedTMDB{key: key, client: client}
	return client
}

// ValidateMetadataKey proves a TMDB API key with one live call (PLAN phase 10
// tasks 2 and 4).
//
// It builds a client for the key it was handed rather than reading the settings
// table, because the callers that matter are testing a key that is not stored
// yet. The client is not cached: a validation happens when a person presses a
// button, which is rare enough that one allocation is cheaper than another
// cache to invalidate.
func (a *libraryAdapter) ValidateMetadataKey(ctx context.Context, apiKey string) error {
	return tmdb.New(apiKey, a.hc).Test(ctx)
}

// ValidateAdultCredential proves a stash-box endpoint and key.
//
// The client is a throwaway rather than the cached one, because a candidate
// credential is exactly what the cache must not hold: going through
// stashboxClient installs the unproven pair on a miss, so a typo'd key in the
// enable modal evicted the working client of a module that is already on and
// the next search re-probed the endpoint's dialect from scratch. Nothing is
// lost by not caching — the credential is committed immediately afterwards, and
// the next adultMetadata() call builds and caches the client for the pair that
// was actually stored.
func (a *libraryAdapter) ValidateAdultCredential(ctx context.Context, endpoint, apiKey string) error {
	return stashbox.New(apiKey, endpoint, a.hc).Test(ctx)
}

// Scan reconciles the database with the storage root. api.Manager discards the
// summary, so it is logged here — this is the only place it would otherwise be
// dropped, and it is the evidence that a scan did anything.
func (a *libraryAdapter) Scan(ctx context.Context) error {
	mgr, err := a.current(ctx)
	if err != nil {
		return err
	}

	result, err := mgr.Scan(ctx)
	if err != nil {
		return err
	}
	a.log.Info("library scan finished",
		"scanned", result.Scanned,
		"added", result.Added,
		"updated", result.Updated,
		"removed", result.Removed,
		"unmatched", result.Unmatched,
		"errors", len(result.Errors))
	for _, msg := range result.Errors {
		a.log.Warn("library scan problem", "detail", msg)
	}
	return nil
}

func (a *libraryAdapter) AddMovie(ctx context.Context, ref core.ItemRef, minAvailability string, monitored *bool, libraryID int64) (*core.Movie, error) {
	mgr, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	return mgr.AddMovie(ctx, ref, minAvailability, monitored, libraryID)
}

// SearchLibrary walks a library's provider chain. Through current for the usual
// reason: which providers are on the chain is a settings-table fact, and so is
// whether each one has the credential it needs to be built at all.
func (a *libraryAdapter) SearchLibrary(ctx context.Context, libraryID int64, mediaType, q string) (*library.SearchHits, error) {
	mgr, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	return mgr.SearchLibrary(ctx, libraryID, mediaType, q)
}

func (a *libraryAdapter) AddSeries(ctx context.Context, ref core.ItemRef, monitored *bool, libraryID int64) (*core.Series, error) {
	mgr, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	return mgr.AddSeries(ctx, ref, monitored, libraryID)
}

// AddSite adds a site by stash-box id. It goes through current like every other
// add, so the storage root and both providers are whatever the settings table
// says right now — including "the module was switched off a moment ago", which
// current resolves to a nil adult provider and library.AddSite refuses.
func (a *libraryAdapter) AddSite(ctx context.Context, stashID string, monitored *bool, libraryID int64) (*core.Series, error) {
	mgr, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	return mgr.AddSite(ctx, stashID, monitored, libraryID)
}

// AddSiteAndWait is AddSite with the catalogue walk inline, for the scene
// approval path. Same current() resolution, same refusal when the module is off.
func (a *libraryAdapter) AddSiteAndWait(ctx context.Context, stashID string, monitored *bool, libraryID int64) (*core.Series, error) {
	mgr, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	return mgr.AddSiteAndWait(ctx, stashID, monitored, libraryID)
}

// SyncSite walks one site's catalogue, and is what the core.JobSyncSite handler
// runs. It resolves the manager per call like every other adapter method, so a
// job queued before the module was switched off finds a Manager that refuses it
// rather than one captured when the queue was still allowed to talk to
// stash-box.
func (a *libraryAdapter) SyncSite(ctx context.Context, seriesID int64) error {
	mgr, err := a.current(ctx)
	if err != nil {
		return err
	}
	return mgr.SyncSite(ctx, seriesID)
}

// MoveMovie and MoveSeries run the core.JobMoveItem handler's body. Through
// current for the usual reason: the storage root and providers are whatever
// the settings say when the job RUNS, not when it was queued.
func (a *libraryAdapter) MoveMovie(ctx context.Context, movieID, libraryID int64) error {
	mgr, err := a.current(ctx)
	if err != nil {
		return err
	}
	return mgr.MoveMovie(ctx, movieID, libraryID)
}

func (a *libraryAdapter) MoveSeries(ctx context.Context, seriesID, libraryID int64) error {
	mgr, err := a.current(ctx)
	if err != nil {
		return err
	}
	return mgr.MoveSeries(ctx, seriesID, libraryID)
}

// AdultMetadata is the provider the HTTP layer searches sites and scenes
// through. It reads the settings table on every call, exactly as Metadata does,
// so enabling the module or pasting a key takes effect without a restart — and,
// more importantly, so does turning it off.
func (a *libraryAdapter) AdultMetadata() core.AdultMetadataProvider {
	return a.adultMetadata(context.Background())
}

func (a *libraryAdapter) RefreshLibrary(ctx context.Context) (*library.RefreshResult, error) {
	mgr, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	return mgr.RefreshLibrary(ctx)
}

// HandleRecycleCleanup resolves the current storage root before deleting
// expired recycle batches, so a migrated library is cleaned in its new home.
func (a *libraryAdapter) HandleRecycleCleanup(ctx context.Context, st *store.Store, payload json.RawMessage) error {
	mgr, err := a.current(ctx)
	if err != nil {
		return err
	}
	return mgr.HandleRecycleCleanup(ctx, st, payload)
}

func (a *libraryAdapter) RemoveMovie(ctx context.Context, id int64, deleteFiles bool) error {
	mgr, err := a.current(ctx)
	if err != nil {
		return err
	}
	return mgr.RemoveMovie(ctx, id, deleteFiles)
}

func (a *libraryAdapter) RemoveSeries(ctx context.Context, id int64, deleteFiles bool) error {
	mgr, err := a.current(ctx)
	if err != nil {
		return err
	}
	return mgr.RemoveSeries(ctx, id, deleteFiles)
}

// MatchUnmatched adapts the argument order and drops the import result, which
// the HTTP layer does not return.
func (a *libraryAdapter) MatchUnmatched(ctx context.Context, unmatchedID int64, mediaType string, ref core.ItemRef) error {
	mgr, err := a.current(ctx)
	if err != nil {
		return err
	}

	result, err := mgr.ImportUnmatched(ctx, unmatchedID, ref, mediaType)
	if err != nil {
		return err
	}
	for _, msg := range result.Warnings {
		a.log.Warn("manual match warning", "path", result.Path, "detail", msg)
	}
	return nil
}

// Compile-time proof that the adapter is what the API expects.
var _ api.Manager = (*libraryAdapter)(nil)
