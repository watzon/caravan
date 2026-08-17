package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// The schema admits several libraries per kind but exactly one default among
// them, and store-level CRUD keeps every guard: root uniqueness, guarded
// deletion, and the clear-then-set default handoff.
func TestMultipleLibrariesPerKind(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	anime := &core.Library{Kind: core.LibraryKindTV, Name: "Kids",
		RootPath: "library/Kids", DLNAVisible: true, Provider: core.ProviderTMDB}
	if err := st.CreateLibrary(ctx, anime); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	if anime.ID == 0 {
		t.Fatal("CreateLibrary assigned no id")
	}

	// A second root at the same path is the schema's refusal, not a guard
	// anyone has to remember.
	dup := &core.Library{Kind: core.LibraryKindMovie, Name: "Dup",
		RootPath: "library/Kids", Provider: core.ProviderTMDB}
	if err := st.CreateLibrary(ctx, dup); err == nil {
		t.Error("CreateLibrary accepted a duplicate root_path")
	}

	// By-kind lookups keep answering with the seeded default, not the newcomer.
	def, err := st.GetDefaultLibrary(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetDefaultLibrary: %v", err)
	}
	if def.Name != "Series" || !def.IsDefault {
		t.Errorf("default tv library = %+v, want the seeded Series row", def)
	}

	// Items pin the newcomer open: deletion refuses until it is empty.
	sr := &core.Series{TMDBID: 100, Title: "Frieren", Kind: core.SeriesKindTV, LibraryID: anime.ID}
	if err := st.UpsertSeries(ctx, sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := st.DeleteLibrary(ctx, anime.ID); !errors.Is(err, ErrLibraryNotEmpty) {
		t.Errorf("DeleteLibrary(non-empty) = %v, want ErrLibraryNotEmpty", err)
	}
	if err := st.DeleteSeries(ctx, sr.ID); err != nil {
		t.Fatalf("DeleteSeries: %v", err)
	}

	// The default flag moves transactionally and the old default survives as a
	// plain library; deleting a default is refused however empty it is.
	if err := st.SetDefaultLibrary(ctx, anime.ID); err != nil {
		t.Fatalf("SetDefaultLibrary: %v", err)
	}
	def, err = st.GetDefaultLibrary(ctx, core.LibraryKindTV)
	if err != nil {
		t.Fatalf("GetDefaultLibrary after handoff: %v", err)
	}
	if def.ID != anime.ID {
		t.Errorf("default tv library = %+v, want Kids", def)
	}
	if err := st.DeleteLibrary(ctx, anime.ID); !errors.Is(err, ErrLibraryIsDefault) {
		t.Errorf("DeleteLibrary(default) = %v, want ErrLibraryIsDefault", err)
	}

	// A series bound for the wrong shelf is refused at the write.
	wrong := &core.Series{StashID: "uuid-x", Title: "Nope", Kind: core.SeriesKindAdult, LibraryID: anime.ID}
	if err := st.UpsertSeries(ctx, wrong); err == nil {
		t.Error("UpsertSeries accepted an adult series in a tv library")
	}

	// A refresh that carries no library keeps the stored one.
	sr2 := &core.Series{TMDBID: 200, Title: "Mushishi", Kind: core.SeriesKindTV, LibraryID: anime.ID}
	if err := st.UpsertSeries(ctx, sr2); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	refresh := &core.Series{ID: sr2.ID, TMDBID: 200, Title: "Mushishi", Kind: core.SeriesKindTV}
	if err := st.UpsertSeries(ctx, refresh); err != nil {
		t.Fatalf("UpsertSeries(refresh): %v", err)
	}
	got, err := st.GetSeries(ctx, sr2.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if got.LibraryID != anime.ID {
		t.Errorf("refresh moved series to library %d, want it kept in %d", got.LibraryID, anime.ID)
	}
}
