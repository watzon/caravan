package library

import (
	"context"
	"errors"
	"os"
	"path"
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

// A failed database update leaves the file moved but its row at the source.
// On retry, an occupied unsuffixed destination belongs to another file, so the
// row must recover the collision-suffixed file Caravan actually moved.
func TestMoveMovieRetryUsesTheCollisionSuffixedFile(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedMovie(h)
	h.writeVideo(rawMovieRel, "movie bytes")
	if res := h.scan(); res.Added != 1 {
		t.Fatalf("seed scan: %+v", res)
	}
	films := &core.Library{Kind: core.LibraryKindMovie, Name: "Films", RootPath: "library/Films", Provider: core.ProviderTMDB}
	if err := h.st.CreateLibrary(ctx, films); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	mv, err := h.st.GetMovieByTMDBID(ctx, 10378)
	if err != nil {
		t.Fatalf("GetMovieByTMDBID: %v", err)
	}
	unsuffixed := "library/Films/Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv"
	h.writeVideo(unsuffixed, "an unrelated pre-existing file")
	h.mgr.updateMediaFilePath = func(context.Context, int64, string) error {
		return errors.New("injected update failure")
	}
	if err := h.mgr.MoveMovie(ctx, mv.ID, films.ID); err == nil {
		t.Fatal("MoveMovie succeeded despite the injected media-file update failure")
	}

	h.mgr.updateMediaFilePath = h.st.UpdateMediaFilePath
	if err := h.mgr.MoveMovie(ctx, mv.ID, films.ID); err != nil {
		t.Fatalf("MoveMovie retry: %v", err)
	}
	suffixed := "library/Films/Big Buck Bunny (2008)/Big Buck Bunny (2008) (1).mkv"
	if got := h.read(unsuffixed); got != "an unrelated pre-existing file" {
		t.Fatalf("unsuffixed file = %q, want the unrelated file", got)
	}
	if got := h.read(suffixed); got != "movie bytes" {
		t.Fatalf("suffixed file = %q, want the moved file", got)
	}
	files, err := h.st.ListMediaFilesForMovie(ctx, mv.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForMovie: %v", err)
	}
	if len(files) != 1 || files[0].Path != suffixed {
		t.Fatalf("media files = %+v, want the collision-suffixed path", files)
	}
}

func TestMoveMovieRejectsJournalForAnotherDestination(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedMovie(h)
	h.writeVideo(rawMovieRel, "movie bytes")
	if res := h.scan(); res.Added != 1 {
		t.Fatalf("seed scan: %+v", res)
	}
	first := &core.Library{Kind: core.LibraryKindMovie, Name: "First", RootPath: "library/First", Provider: core.ProviderTMDB}
	second := &core.Library{Kind: core.LibraryKindMovie, Name: "Second", RootPath: "library/Second", Provider: core.ProviderTMDB}
	for _, lib := range []*core.Library{first, second} {
		if err := h.st.CreateLibrary(ctx, lib); err != nil {
			t.Fatalf("CreateLibrary: %v", err)
		}
	}
	mv, err := h.st.GetMovieByTMDBID(ctx, 10378)
	if err != nil {
		t.Fatalf("GetMovieByTMDBID: %v", err)
	}
	h.mgr.updateMediaFilePath = func(context.Context, int64, string) error { return errors.New("injected update failure") }
	if err := h.mgr.MoveMovie(ctx, mv.ID, first.ID); err == nil {
		t.Fatal("first MoveMovie succeeded despite the injected failure")
	}
	h.mgr.updateMediaFilePath = h.st.UpdateMediaFilePath
	if err := h.mgr.MoveMovie(ctx, mv.ID, second.ID); err == nil {
		t.Fatal("MoveMovie accepted a journal from another destination")
	}
	files, err := h.st.ListMediaFilesForMovie(ctx, mv.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForMovie: %v", err)
	}
	if len(files) != 1 || files[0].Path != organizedRel {
		t.Fatalf("media files = %+v, want the original path", files)
	}
}

func TestMoveMovieSidecarRetryKeepsPosterAndNFOSeparate(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	mv, _ := h.addMovieWithFile("Big Buck Bunny", 2008)
	dir := movieDir(stockMovieLib(), mv.Title, mv.Year)
	h.writeVideo(path.Join(dir, PosterName), "poster")
	h.writeVideo(path.Join(dir, MovieNFOName), "nfo")
	films := &core.Library{Kind: core.LibraryKindMovie, Name: "Films", RootPath: "library/Films", Provider: core.ProviderTMDB}
	if err := h.st.CreateLibrary(ctx, films); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	h.mgr.upsertMovie = func(context.Context, *core.Movie) error { return errors.New("injected item update failure") }
	if err := h.mgr.MoveMovie(ctx, mv.ID, films.ID); err == nil {
		t.Fatal("MoveMovie succeeded despite the injected item update failure")
	}
	h.mgr.upsertMovie = h.st.UpsertMovie
	if err := h.mgr.MoveMovie(ctx, mv.ID, films.ID); err != nil {
		t.Fatalf("MoveMovie retry: %v", err)
	}
	newDir := movieDir(films, mv.Title, mv.Year)
	if got := h.read(path.Join(newDir, PosterName)); got != "poster" {
		t.Fatalf("poster = %q, want poster", got)
	}
	if got := h.read(path.Join(newDir, MovieNFOName)); got != "nfo" {
		t.Fatalf("NFO = %q, want nfo", got)
	}
	row, err := h.st.GetMovie(ctx, mv.ID)
	if err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
	if row.PosterPath != path.Join(newDir, PosterName) {
		t.Fatalf("PosterPath = %q, want poster path", row.PosterPath)
	}
}

func TestMoveMovieFreshManagerRejectsMissingSource(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedMovie(h)
	h.writeVideo(rawMovieRel, "movie bytes")
	if res := h.scan(); res.Added != 1 {
		t.Fatalf("seed scan: %+v", res)
	}
	films := &core.Library{Kind: core.LibraryKindMovie, Name: "Films", RootPath: "library/Films", Provider: core.ProviderTMDB}
	if err := h.st.CreateLibrary(ctx, films); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	mv, err := h.st.GetMovieByTMDBID(ctx, 10378)
	if err != nil {
		t.Fatalf("GetMovieByTMDBID: %v", err)
	}
	h.mgr.updateMediaFilePath = func(context.Context, int64, string) error { return errors.New("injected update failure") }
	if err := h.mgr.MoveMovie(ctx, mv.ID, films.ID); err == nil {
		t.Fatal("MoveMovie succeeded despite the injected failure")
	}
	fresh := h.newManager(h.st, h.provider)
	if err := fresh.MoveMovie(ctx, mv.ID, films.ID); err == nil {
		t.Fatal("fresh Manager inferred a missing source had moved")
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

// A move across filesystems cannot rename, and two libraries may sit on two
// mounts. The organizer already falls back to copy-then-replace; a library move
// lost that fallback and failed outright.
func TestMoveMovieFallsBackToCopyWhenRenameCannotCrossDevices(t *testing.T) {
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

	// A test machine has one filesystem, so the cross-device refusal is
	// injected rather than provoked.
	original := rootRename
	rootRename = func(*os.Root, string, string) error {
		return &os.LinkError{Op: "rename", Err: errors.New("invalid cross-device link")}
	}
	t.Cleanup(func() { rootRename = original })

	if err := h.mgr.MoveMovie(ctx, mv.ID, films.ID); err != nil {
		t.Fatalf("MoveMovie across devices: %v", err)
	}
	moved := "library/Films/Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv"
	if got := h.read(moved); got != "movie bytes" {
		t.Fatalf("moved file = %q, want the copied bytes at %s", got, moved)
	}
	// The fallback consumes the source, exactly as the rename would have.
	if h.exists(organizedRel) {
		t.Errorf("old file %s survived the copy fallback", organizedRel)
	}
	files, err := h.st.ListMediaFilesForMovie(ctx, mv.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForMovie: %v", err)
	}
	if len(files) != 1 || files[0].Path != moved {
		t.Fatalf("media files = %+v, want the copied path", files)
	}
}

// A retried series move must not flatten the season folders. On the retry the
// media-file rows already name the new directory, and reading the layout only
// below the OLD directory collapsed every episode onto the item folder.
func TestMoveSeriesRetryKeepsEpisodesInTheirSeasonFolder(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	seedSeries(h)
	raw := "library/TV/Planet.Earth.II.S01E01.720p.mkv"
	h.parser["Planet.Earth.II.S01E01.720p.mkv"] = episodeParse("Planet Earth II", 1, 1)
	h.writeVideo(raw, "episode bytes")
	if res := h.scan(); res.Added != 1 {
		t.Fatalf("seed scan: %+v", res)
	}
	kids := &core.Library{Kind: core.LibraryKindTV, Name: "Kids",
		RootPath: "library/Kids", Provider: core.ProviderTMDB}
	if err := h.st.CreateLibrary(ctx, kids); err != nil {
		t.Fatalf("CreateLibrary: %v", err)
	}
	sr, err := h.st.GetSeriesByTMDBID(ctx, 42)
	if err != nil {
		t.Fatalf("GetSeriesByTMDBID: %v", err)
	}

	h.mgr.upsertSeries = func(context.Context, *core.Series) error {
		return errors.New("injected series update failure")
	}
	if err := h.mgr.MoveSeries(ctx, sr.ID, kids.ID); err == nil {
		t.Fatal("MoveSeries succeeded despite the injected series update failure")
	}
	h.mgr.upsertSeries = h.st.UpsertSeries
	if err := h.mgr.MoveSeries(ctx, sr.ID, kids.ID); err != nil {
		t.Fatalf("MoveSeries retry: %v", err)
	}

	episode := "Planet Earth II (2016) - S01E01 - Islands.mkv"
	moved := path.Join("library/Kids/Planet Earth II (2016)/Season 01", episode)
	if got := h.read(moved); got != "episode bytes" {
		t.Fatalf("episode = %q, want it still under Season 01 at %s", got, moved)
	}
	if flattened := path.Join("library/Kids/Planet Earth II (2016)", episode); h.exists(flattened) {
		t.Fatalf("the retry flattened the episode to %s", flattened)
	}
	pairs, err := h.st.ListEpisodeMediaFilesForSeries(ctx, sr.ID)
	if err != nil {
		t.Fatalf("ListEpisodeMediaFilesForSeries: %v", err)
	}
	if len(pairs) != 1 || pairs[0].File.Path != moved {
		t.Fatalf("media files = %+v, want the season-folder path", pairs)
	}
}
