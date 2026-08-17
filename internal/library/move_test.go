package library

import (
	"context"
	"errors"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// A movie move carries the file, the sidecars and finally the row into the
// target library, empties the old folder, and is idempotent under the job
// queue's at-least-once redelivery.
func TestMoveMovieRelocatesFilesAndRow(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedMovie(h)
	h.writeVideo(rawMovieRel, "movie bytes")
	if res := h.scan(); res.Added != 1 {
		t.Fatalf("seed scan: %+v", res)
	}

	films := &core.Library{Kind: core.LibraryKindMovie, Name: "Films",
		RootPath: "library/Films", Provider: core.ProviderTMDB}
	if err := h.st.CreateLibrary(ctx, films); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	mv, err := h.st.GetMovieByTMDBID(ctx, 10378)
	if err != nil {
		t.Fatalf("GetMovieByTMDBID: %v", err)
	}

	if err := h.mgr.MoveMovie(ctx, mv.ID, films.ID); err != nil {
		t.Fatalf("MoveMovie: %v", err)
	}

	moved := "library/Films/Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv"
	if got := h.read(moved); got != "movie bytes" {
		t.Fatalf("moved file missing at %q (content %q)", moved, got)
	}
	if h.exists(organizedRel) {
		t.Errorf("old file %s still present", organizedRel)
	}
	if h.exists(movieDirRel) {
		t.Errorf("old movie folder %s survived the move", movieDirRel)
	}
	row, err := h.st.GetMovie(ctx, mv.ID)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if row.LibraryID != films.ID || row.Path != "library/Films/Big Buck Bunny (2008)" {
		t.Errorf("row = {library %d, path %q}, want it in Films", row.LibraryID, row.Path)
	}
	files, err := h.st.ListMediaFilesForMovie(ctx, mv.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForMovie: %v", err)
	}
	if len(files) != 1 || files[0].Path != moved {
		t.Errorf("media files = %+v, want the moved path", files)
	}

	// Redelivery of the same job is a successful no-op.
	if err := h.mgr.MoveMovie(ctx, mv.ID, films.ID); err != nil {
		t.Fatalf("MoveMovie(redelivered): %v", err)
	}

	// A rescan after the move is drift-free: same row, same library.
	res := h.scan()
	if res.Updated != 1 || res.Added != 0 || len(res.Errors) != 0 {
		t.Fatalf("rescan after move: %+v", res)
	}
	again, err := h.st.GetMovie(ctx, mv.ID)
	if err != nil {
		t.Fatalf("GetMovie after rescan: %v", err)
	}
	if again.LibraryID != films.ID {
		t.Errorf("rescan moved the movie back to library %d", again.LibraryID)
	}
}

// A series move keeps the season layout, and a move into a library of another
// kind is refused before anything touches the disk.
func TestMoveSeriesKeepsSeasonsAndRefusesCrossKind(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedSeries(h)
	raw := "library/TV/Planet.Earth.II.S01E01.720p.mkv"
	h.parser["Planet.Earth.II.S01E01.720p.mkv"] = episodeParse("Planet Earth II", 1, 1)
	h.writeVideo(raw, "episode bytes")
	if res := h.scan(); res.Added != 1 {
		t.Fatalf("seed scan: %+v", res)
	}

	anime := &core.Library{Kind: core.LibraryKindTV, Name: "Kids",
		RootPath: "library/Kids", Provider: core.ProviderTMDB}
	if err := h.st.CreateLibrary(ctx, anime); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	movies, err := h.st.GetDefaultLibrary(ctx, core.LibraryKindMovie)
	if err != nil {
		t.Fatalf("GetDefaultLibrary: %v", err)
	}
	sr, err := h.st.GetSeriesByTMDBID(ctx, 42)
	if err != nil {
		t.Fatalf("GetSeriesByTMDBID: %v", err)
	}

	if err := h.mgr.MoveSeries(ctx, sr.ID, movies.ID); !errors.Is(err, ErrCrossKindMove) {
		t.Fatalf("MoveSeries(into movie library) = %v, want ErrCrossKindMove", err)
	}

	if err := h.mgr.MoveSeries(ctx, sr.ID, anime.ID); err != nil {
		t.Fatalf("MoveSeries: %v", err)
	}
	moved := "library/Kids/Planet Earth II (2016)/Season 01/Planet Earth II (2016) - S01E01 - Islands.mkv"
	if got := h.read(moved); got != "episode bytes" {
		t.Fatalf("moved episode missing at %q (content %q)", moved, got)
	}
	row, err := h.st.GetSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if row.LibraryID != anime.ID || row.Path != "library/Kids/Planet Earth II (2016)" {
		t.Errorf("row = {library %d, path %q}, want it in Kids", row.LibraryID, row.Path)
	}
}
