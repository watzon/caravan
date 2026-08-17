package main

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// A freshly migrated database holds all four seeded shelves, so the start is
// quiet. This is the half that would break first if a kind were added to the
// warning list without being added to migration 0011.
func TestWarnOnUnseededShelvesIsQuietOnAFreshInstall(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	if err := warnOnUnseededShelves(t.Context(), st, logger); err != nil {
		t.Fatalf("warnOnUnseededShelves: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("logged %q, want nothing: a fresh install has every shelf", buf.String())
	}
}

// The state migration 0011's silent skip leaves behind: a kind with no library
// at all. The warning is the only thing that says so.
func TestWarnOnUnseededShelvesNamesTheMissingKind(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()

	// The delete guard refuses a default, so the skip is reproduced the way the
	// migration would have left it: with the row simply absent.
	if _, err := st.DB().ExecContext(t.Context(),
		"DELETE FROM libraries WHERE kind = ?", core.LibraryKindAnime); err != nil {
		t.Fatalf("remove the anime shelf: %v", err)
	}

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	if err := warnOnUnseededShelves(t.Context(), st, logger); err != nil {
		t.Fatalf("warnOnUnseededShelves: %v", err)
	}
	logged := buf.String()
	if !strings.Contains(logged, "kind="+core.LibraryKindAnime) {
		t.Errorf("logged %q, want a warning naming the anime kind", logged)
	}
	if strings.Contains(logged, "kind="+core.LibraryKindMovie) {
		t.Errorf("logged %q, want no warning for a kind that has a shelf", logged)
	}
}
