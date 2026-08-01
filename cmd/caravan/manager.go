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
}

func newLibraryAdapter(st *store.Store, fallbackRoot string, log *slog.Logger) *libraryAdapter {
	return &libraryAdapter{
		st:           st,
		fallbackRoot: fallbackRoot,
		hc:           &http.Client{Timeout: metadataTimeout},
		log:          log,
	}
}

// current builds a library.Manager from the settings in force right now.
func (a *libraryAdapter) current(ctx context.Context) (*library.Manager, error) {
	root, err := a.setting(ctx, store.SettingStorageRoot)
	if err != nil {
		return nil, err
	}
	if root == "" {
		root = a.fallbackRoot
	}
	return library.NewManager(a.st, a.metadata(ctx), root), nil
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

func (a *libraryAdapter) AddMovie(ctx context.Context, tmdbID int64) (*core.Movie, error) {
	mgr, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	return mgr.AddMovie(ctx, tmdbID)
}

func (a *libraryAdapter) AddSeries(ctx context.Context, tmdbID int64) (*core.Series, error) {
	mgr, err := a.current(ctx)
	if err != nil {
		return nil, err
	}
	return mgr.AddSeries(ctx, tmdbID)
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
