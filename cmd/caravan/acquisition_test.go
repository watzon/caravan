package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

func testAdapter(t *testing.T) (*libraryAdapter, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return newLibraryAdapter(st, "", slog.New(slog.NewTextHandler(io.Discard, nil)), nil), st
}

// The watcher holds one library.Manager for the life of the process, so the
// provider it was built with has to keep up with the settings table. A TMDB
// key set after startup must reach the next import.
func TestLateMetadataFollowsTheSettingsTable(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	meta := lateMetadata{adapter: adapter}

	if _, err := meta.GetMovie(ctx, smokeTMDBID); !errors.Is(err, core.ErrNoMetadataProvider) {
		t.Fatalf("GetMovie with no key = %v, want ErrNoMetadataProvider", err)
	}

	// Configure TMDB the way the settings screen does, after the fact.
	redirectTMDB(t, startFakeTMDB(t))
	if err := st.SetSetting(ctx, store.SettingTMDBAPIKey, "set-after-startup"); err != nil {
		t.Fatalf("set tmdb key: %v", err)
	}

	got, err := meta.GetMovie(ctx, smokeTMDBID)
	if err != nil {
		t.Fatalf("GetMovie after the key was set: %v", err)
	}
	if got.Title != smokeMovieTitle || got.Year != smokeMovieYear {
		t.Errorf("GetMovie = %q (%d), want %q (%d)", got.Title, got.Year, smokeMovieTitle, smokeMovieYear)
	}
}

// The engine cannot exist before a storage root does, and a first run has
// none: the process must still start and serve the setup screen.
func TestEngineProviderIsLazyUntilTheStorageRootExists(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	provider := newEngineProvider(adapter, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() {
		if err := provider.Close(); err != nil {
			t.Errorf("close engine: %v", err)
		}
	})

	if engine := provider.Engine(); engine != nil {
		t.Fatal("Engine() built an engine with no storage root configured")
	}
	if name := provider.Name(); name != "embedded" {
		t.Errorf("Name() = %q, want %q", name, "embedded")
	}

	root := t.TempDir()
	if err := st.SetSetting(ctx, store.SettingStorageRoot, root); err != nil {
		t.Fatalf("set storage root: %v", err)
	}

	engine := provider.Engine()
	if engine == nil {
		t.Fatal("Engine() = nil after the storage root was set")
	}
	// The same engine thereafter: a second one would bind a second port and
	// fight the first over the same files.
	if again := provider.Engine(); again != engine {
		t.Error("Engine() built a second engine")
	}

	// In-progress data belongs under the storage root, not beside it.
	if _, err := provider.Engine().List(ctx); err != nil {
		t.Errorf("List on a fresh engine: %v", err)
	}
	if _, err := filepath.Glob(filepath.Join(root, "incomplete")); err != nil {
		t.Errorf("glob incomplete dir: %v", err)
	}
}

// Closing without ever building an engine is what a shutdown before setup
// looks like.
func TestEngineProviderCloseWithoutEngine(t *testing.T) {
	adapter, _ := testAdapter(t)
	provider := newEngineProvider(adapter, false, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := provider.Close(); err != nil {
		t.Errorf("Close with no engine = %v, want nil", err)
	}
}

func TestEngineOptionsReadsSettings(t *testing.T) {
	opts, err := engineOptions(map[string]string{
		store.SettingEngineListenPort:     "51413",
		store.SettingEngineMaxConnections: "12",
		store.SettingEngineMaxDownKBps:    "4096",
		store.SettingEngineMaxUpKBps:      "512",
		store.SettingEngineSeedRatio:      "1.5",
		store.SettingEngineSeedDays:       "7",
	}, true, nil)
	if err != nil {
		t.Fatalf("engineOptions: %v", err)
	}
	if opts.ListenPort != 51413 || opts.MaxConnections != 12 {
		t.Fatalf("connection settings = port %d, max %d, want 51413 and 12", opts.ListenPort, opts.MaxConnections)
	}
	if opts.MaxDownKBps != 4096 || opts.MaxUpKBps != 512 {
		t.Fatalf("rate settings = %d/%d, want 4096/512", opts.MaxDownKBps, opts.MaxUpKBps)
	}
	if opts.SeedRatio != 1.5 || opts.SeedDays != 7 || !opts.Paused {
		t.Fatalf("seeding settings = ratio %v days %d paused %t", opts.SeedRatio, opts.SeedDays, opts.Paused)
	}
}

// watcherNotifier stands in for the playback handoff (internal/jellyfin).
type watcherNotifier struct{ calls int }

func (n *watcherNotifier) LibraryChanged(context.Context) error {
	n.calls++
	return nil
}

// TestImportWatcherNotifiesTheHandoff is PLAN phase 4 acceptance criterion 1 at
// the wiring, which is where it was actually broken: the pipeline notifies, but
// only if the Manager it runs on was built with the notifier. The watcher builds
// its own and holds it for the life of the process, so an automatic import — the
// path every finished download takes — silently never triggered a Jellyfin scan
// while the manual match did.
func TestImportWatcherNotifiesTheHandoff(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	root := t.TempDir()
	notify := &watcherNotifier{}
	adapter := newLibraryAdapter(st, root, slog.New(slog.NewTextHandler(io.Discard, nil)), notify)

	redirectTMDB(t, startFakeTMDB(t))
	if err := st.SetSetting(ctx, store.SettingTMDBAPIKey, "key"); err != nil {
		t.Fatalf("set tmdb key: %v", err)
	}
	movie := core.Movie{TMDBID: smokeTMDBID, Title: smokeMovieTitle, Year: smokeMovieYear, Monitored: true}
	if err := st.UpsertMovie(ctx, &movie); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	// A finished download sitting under the storage root, as the engine leaves it.
	const saveDir = "incomplete/Big.Buck.Bunny.2008.1080p.BluRay.x264-CARAVAN"
	if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(saveDir)), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	content := filepath.Join(root, filepath.FromSlash(saveDir), smokeContentName)
	if err := os.WriteFile(content, []byte("movie bytes"), 0o644); err != nil {
		t.Fatalf("write download: %v", err)
	}

	mgr := adapter.watcherManager(root)
	dl := core.DownloadStatus{ID: "infohash", State: core.DownloadCompleted, SavePath: saveDir}
	grab := core.GrabInfo{MovieID: movie.ID, ReleaseTitle: smokeReleaseTitle}
	if err := mgr.ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}

	files, err := st.ListMediaFilesForMovie(ctx, movie.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForMovie: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("imported %d files, want 1 — the fixture is not exercising an import", len(files))
	}
	if notify.calls != 1 {
		t.Fatalf("handoff notifications = %d, want 1 after an automatic import", notify.calls)
	}
}
