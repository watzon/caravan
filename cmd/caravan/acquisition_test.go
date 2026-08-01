package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
	return newLibraryAdapter(st, "", slog.New(slog.NewTextHandler(io.Discard, nil))), st
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
