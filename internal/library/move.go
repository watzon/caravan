package library

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// Moving an item to another library of the same kind.
//
// A move is one item's directory inside one storage root — almost always a
// same-filesystem rename — so it reuses the organizer's own primitives
// (placeFile, transfer) rather than internal/relocate, which moves whole
// trees between storage roots with a pause on the download queue. What it
// keeps from relocate is the ordering invariant: files move first, the row
// moves LAST. A crash in between leaves files at the target with the row
// still naming the source, which the next rescan heals from the filesystem
// (SPEC §1.2 pillar 2); the reverse order would leave a row pointing at
// nothing.

// ErrCrossKindMove refuses a move to a library that cannot hold the item: a tv
// series and an adult site have different identity models, and a movie is not
// a series at all.
//
// What a move MAY change is where a series sits between the television and
// anime shelves, because those two hold the same rows under two names —
// core.LibraryKindAccepts says so once, and MoveSeries rewrites `series.kind`
// to match the destination so the row and its shelf never disagree afterwards.
var ErrCrossKindMove = errors.New("library: the target library holds a different kind of item")

// MoveMovie moves one movie — its files, its sidecars, and finally its row —
// into the target library. Moving to the library the movie is already in is a
// successful no-op, which is what makes the durable job idempotent under
// at-least-once delivery.
func (m *Manager) MoveMovie(ctx context.Context, movieID, targetLibraryID int64) error {
	mv, err := m.store.GetMovie(ctx, movieID)
	if err != nil {
		return err
	}
	target, err := m.store.GetLibrary(ctx, targetLibraryID)
	if err != nil {
		return err
	}
	if target.ID == mv.LibraryID {
		return nil
	}
	if !core.LibraryKindAccepts(target.Kind, core.LibraryKindMovie) {
		return ErrCrossKindMove
	}

	oldDir := mv.Path
	newDir := m.movieDir(target, mv.Title, mv.Year)

	files, err := m.store.ListMediaFilesForMovie(ctx, movieID)
	if err != nil {
		return err
	}
	for _, f := range files {
		finalRel, err := m.moveItemFile(f.Path, oldDir, newDir)
		if err != nil {
			return err
		}
		if err := m.store.UpdateMediaFilePath(ctx, f.ID, finalRel); err != nil {
			return err
		}
	}
	posterRel, err := m.moveSidecars(oldDir, newDir, MovieNFOName)
	if err != nil {
		return err
	}

	// The row moves last — see the file comment for why this order is the
	// crash-safety argument.
	mv.Path = newDir
	mv.LibraryID = target.ID
	if posterRel != "" {
		mv.PosterPath = posterRel
	}
	if err := m.store.UpsertMovie(ctx, mv); err != nil {
		return err
	}
	m.removeEmptyItemDir(oldDir)

	_ = m.store.InsertEvent(ctx, &core.Event{
		Category: EventCategoryLibrary,
		Message:  fmt.Sprintf("Moved %s to %s", mv.Title, target.Name),
		MovieID:  mv.ID,
	})
	m.libraryChanged(ctx)
	return nil
}

// MoveSeries is MoveMovie's series twin, covering television series and adult
// sites alike: the target must speak the series' kind, so a site moves only
// between adult libraries and a series only between tv ones.
func (m *Manager) MoveSeries(ctx context.Context, seriesID, targetLibraryID int64) error {
	sr, err := m.store.GetSeries(ctx, seriesID)
	if err != nil {
		return err
	}
	target, err := m.store.GetLibrary(ctx, targetLibraryID)
	if err != nil {
		return err
	}
	if target.ID == sr.LibraryID {
		return nil
	}
	if !core.LibraryKindAccepts(target.Kind, core.LibraryKindForSeries(sr.Kind)) {
		return ErrCrossKindMove
	}
	// The destination decides what the row IS from here on. Only the
	// television/anime pair can differ (nothing else is accepted above), and
	// leaving `kind` behind would leave a row the store then refuses to write —
	// store.UpsertSeries insists the two line up — and, if it did write, one
	// that the Series screen and the Anime screen would both claim or both drop.
	sr.Kind = core.SeriesKindForLibrary(target.Kind)

	oldDir := sr.Path
	var newDir string
	if sr.Kind == core.SeriesKindAdult {
		newDir = adultSeriesDir(target, sr.Title)
	} else {
		newDir = m.seriesDir(target, sr.Title, sr.Year)
	}

	pairs, err := m.store.ListEpisodeMediaFilesForSeries(ctx, seriesID)
	if err != nil {
		return err
	}
	// A multi-episode file is listed once per episode it covers; moving it
	// twice would turn the second pass into a spurious missing-source error.
	seen := make(map[int64]bool, len(pairs))
	for _, pair := range pairs {
		if seen[pair.File.ID] {
			continue
		}
		seen[pair.File.ID] = true
		finalRel, err := m.moveItemFile(pair.File.Path, oldDir, newDir)
		if err != nil {
			return err
		}
		if err := m.store.UpdateMediaFilePath(ctx, pair.File.ID, finalRel); err != nil {
			return err
		}
	}
	posterRel, err := m.moveSidecars(oldDir, newDir, TVShowNFOName)
	if err != nil {
		return err
	}

	sr.Path = newDir
	sr.LibraryID = target.ID
	if posterRel != "" {
		sr.PosterPath = posterRel
	}
	if err := m.store.UpsertSeries(ctx, sr); err != nil {
		return err
	}
	m.removeEmptyItemDir(oldDir)

	// An adult site's move must stay as invisible to ungranted callers as the
	// site itself is, which is what the adult-only category promises.
	category := EventCategoryLibrary
	if sr.Kind == core.SeriesKindAdult {
		category = core.EventCategoryAdultOnly
	}
	_ = m.store.InsertEvent(ctx, &core.Event{
		Category: category,
		Message:  fmt.Sprintf("Moved %s to %s", sr.Title, target.Name),
		SeriesID: sr.ID,
	})
	m.libraryChanged(ctx)
	return nil
}

// moveItemFile relocates one media file, preserving its layout below the item
// directory (season folders survive the move). A file stored outside the item
// directory — a pre-organize path, a manual edit — keeps only its base name.
// A source that is already gone resolves to its destination when the
// destination exists: that is a re-run of a crashed move, not a lost file.
func (m *Manager) moveItemFile(rel, oldDir, newDir string) (string, error) {
	suffix := path.Base(rel)
	if oldDir != "" && strings.HasPrefix(rel, oldDir+"/") {
		suffix = strings.TrimPrefix(rel, oldDir+"/")
	}
	dst := path.Join(newDir, suffix)
	if !fileExists(m.abs(rel)) && fileExists(m.abs(dst)) {
		return dst, nil
	}
	return m.placeFile(rel, dst, consumeSource)
}

// moveSidecars carries the poster and the NFO along with the files. They are
// moved rather than re-derived so a move needs no provider round trip; a
// sidecar that is missing is simply skipped — healStaleArtwork and the next
// refresh already own that repair. It returns the poster's new path, or ""
// when there was none to move.
func (m *Manager) moveSidecars(oldDir, newDir, nfoName string) (string, error) {
	if oldDir == "" {
		return "", nil
	}
	posterRel := ""
	for _, name := range []string{PosterName, nfoName} {
		src := path.Join(oldDir, name)
		if !fileExists(m.abs(src)) {
			continue
		}
		finalRel, err := m.placeFile(src, path.Join(newDir, name), consumeSource)
		if err != nil {
			return "", err
		}
		if name == PosterName {
			posterRel = finalRel
		}
	}
	return posterRel, nil
}

// removeEmptyItemDir removes the moved-out item directory and any season
// folders the move emptied. Best-effort on purpose: anything the user left in
// the folder keeps it alive, exactly as removeItemFiles promises, and a
// folder that stays behind is cosmetic rather than wrong.
func (m *Manager) removeEmptyItemDir(dir string) {
	if dir == "" {
		return
	}
	abs := m.abs(dir)
	entries, err := os.ReadDir(abs)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			_ = os.Remove(filepath.Join(abs, entry.Name()))
		}
	}
	_ = os.Remove(abs)
}
