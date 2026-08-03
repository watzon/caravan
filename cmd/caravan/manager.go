package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/library"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/tmdb"
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
}

func newLibraryAdapter(st *store.Store, fallbackRoot string, log *slog.Logger, notify library.Notifier) *libraryAdapter {
	return &libraryAdapter{
		st:           st,
		fallbackRoot: fallbackRoot,
		hc:           &http.Client{Timeout: metadataTimeout},
		log:          log,
		notify:       notify,
	}
}

// current builds a library.Manager from the settings in force right now.
func (a *libraryAdapter) current(ctx context.Context) (*library.Manager, error) {
	root, err := a.StorageRoot(ctx)
	if err != nil {
		return nil, err
	}
	return library.NewManager(a.st, a.metadata(ctx), root, library.WithNotifier(a.notify)), nil
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
func (a *libraryAdapter) watcherManager(root string) *library.Manager {
	return library.NewManager(a.st, lateMetadata{adapter: a}, root, library.WithNotifier(a.notify))
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
	if key == "" {
		return nil
	}
	return tmdb.New(key, a.hc)
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

func (a *libraryAdapter) AddMovie(ctx context.Context, tmdbID int64, minAvailability string) (*core.Movie, error) {
	mgr, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	return mgr.AddMovie(ctx, tmdbID, minAvailability)
}

func (a *libraryAdapter) AddSeries(ctx context.Context, tmdbID int64) (*core.Series, error) {
	mgr, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	return mgr.AddSeries(ctx, tmdbID)
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
func (a *libraryAdapter) MatchUnmatched(ctx context.Context, unmatchedID int64, mediaType string, tmdbID int64) error {
	mgr, err := a.current(ctx)
	if err != nil {
		return err
	}

	result, err := mgr.ImportUnmatched(ctx, unmatchedID, tmdbID, mediaType)
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
