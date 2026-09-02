package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

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
		finalRel, err := m.moveItemFile(f, oldDir, newDir)
		if err != nil {
			return err
		}
		if err := m.updateMediaFilePath(ctx, f.ID, finalRel); err != nil {
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
	if err := m.upsertMovie(ctx, mv); err != nil {
		return err
	}
	m.clearMoveJournal(newDir)
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
		finalRel, err := m.moveItemFile(pair.File, oldDir, newDir)
		if err != nil {
			return err
		}
		if err := m.updateMediaFilePath(ctx, pair.File.ID, finalRel); err != nil {
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
	if err := m.upsertSeries(ctx, sr); err != nil {
		return err
	}
	m.clearMoveJournal(newDir)
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
// A retry accepts a missing source only when this Manager journaled the exact
// destination before its database write failed. The unsuffixed destination can
// belong to an unrelated file, so its mere presence proves nothing about an
// earlier move.
func (m *Manager) moveItemFile(file core.MediaFile, oldDir, newDir string) (string, error) {
	suffix := path.Base(file.Path)
	switch {
	case oldDir != "" && strings.HasPrefix(file.Path, oldDir+"/"):
		suffix = strings.TrimPrefix(file.Path, oldDir+"/")
	case newDir != "" && strings.HasPrefix(file.Path, newDir+"/"):
		// A retry reads the path the previous attempt already wrote, so the
		// layout to keep is the one below the NEW directory. Without this the
		// suffix collapses to the base name and the retry flattens every
		// episode out of its season folder.
		suffix = strings.TrimPrefix(file.Path, newDir+"/")
	}
	dst := path.Join(newDir, suffix)
	root, err := os.OpenRoot(m.root)
	if err != nil {
		return "", fmt.Errorf("library: open storage root: %w", err)
	}
	defer root.Close()
	srcInfo, err := root.Stat(filepath.FromSlash(file.Path))
	if errors.Is(err, fs.ErrNotExist) {
		return m.journaledMoveDestination(root, file.Path, dst)
	} else if err != nil {
		return "", fmt.Errorf("library: inspect move source %s: %w", file.Path, err)
	}
	finalRel, err := m.placeLibraryFile(root, file.Path, dst)
	if err != nil {
		return "", err
	}
	m.rememberMovedFile(file.Path, dst, finalRel, srcInfo)
	return finalRel, nil
}

type movedFile struct {
	path     string
	expected string
	size     int64
	modTime  time.Time
}

type moveJournalKey struct {
	source      string
	destination string
}

func (m *Manager) rememberMovedFile(source, expected, destination string, info fs.FileInfo) {
	m.moveMu.Lock()
	defer m.moveMu.Unlock()
	m.movedFiles[moveJournalKey{source: source, destination: expected}] = movedFile{
		path: destination, expected: expected, size: info.Size(), modTime: info.ModTime(),
	}
}

func (m *Manager) clearMoveJournal(destinationDir string) {
	m.moveMu.Lock()
	defer m.moveMu.Unlock()
	for key := range m.movedFiles {
		if key.destination == destinationDir || strings.HasPrefix(key.destination, destinationDir+"/") {
			delete(m.movedFiles, key)
		}
	}
}

func (m *Manager) hasJournaledMove(source, destination string) bool {
	m.moveMu.Lock()
	defer m.moveMu.Unlock()
	_, ok := m.movedFiles[moveJournalKey{source: source, destination: destination}]
	return ok
}

func (m *Manager) journaledMoveDestination(root *os.Root, source, expected string) (string, error) {
	m.moveMu.Lock()
	key := moveJournalKey{source: source, destination: expected}
	moved, ok := m.movedFiles[key]
	if !ok {
		for stale := range m.movedFiles {
			if stale.source == source {
				delete(m.movedFiles, stale)
			}
		}
	}
	m.moveMu.Unlock()
	if !ok {
		return "", fmt.Errorf("library: move source %s is missing and no journaled destination exists for %s", source, expected)
	}
	if moved.expected != expected {
		m.moveMu.Lock()
		delete(m.movedFiles, key)
		m.moveMu.Unlock()
		return "", fmt.Errorf("library: journaled move destination %s differs from requested destination %s", moved.expected, expected)
	}
	info, err := root.Stat(filepath.FromSlash(moved.path))
	if err != nil {
		return "", fmt.Errorf("library: inspect journaled move destination %s: %w", moved.path, err)
	}
	if info.Size() != moved.size || !info.ModTime().Equal(moved.modTime) {
		return "", fmt.Errorf("library: journaled move destination %s no longer matches its source", moved.path)
	}
	return moved.path, nil
}

// placeLibraryFile chooses a collision-safe destination and moves through the
// already-open root, which keeps both endpoints below the storage root.
func (m *Manager) placeLibraryFile(root *os.Root, srcRel, dstRel string) (string, error) {
	srcInfo, err := root.Stat(filepath.FromSlash(srcRel))
	if err != nil {
		return "", fmt.Errorf("library: stat %s: %w", srcRel, err)
	}
	if err := root.MkdirAll(filepath.Dir(filepath.FromSlash(dstRel)), 0o755); err != nil {
		return "", fmt.Errorf("library: create %s: %w", path.Dir(dstRel), err)
	}
	finalRel, same, err := uniqueRootDest(root, dstRel, srcInfo)
	if err != nil {
		return "", err
	}
	if same {
		return finalRel, nil
	}
	if err := rootRename(root, filepath.FromSlash(srcRel), filepath.FromSlash(finalRel)); err != nil {
		// Two libraries may sit on two filesystems, and a rename across them
		// cannot work. Copy-then-replace consumes the source exactly as the
		// rename would have, which is what the organizer already falls back to.
		if copyErr := copyThenReplaceRoot(root, m.abs(srcRel), srcRel, finalRel, consumeSource); copyErr != nil {
			return "", fmt.Errorf("library: move %s to %s: %w",
				srcRel, finalRel, errors.Join(err, copyErr))
		}
	}
	return finalRel, nil
}

// rootRename is the move primitive placeLibraryFile tries first. It is a
// variable so a test can force the cross-device failure a single-filesystem
// test machine cannot produce.
var rootRename = func(root *os.Root, from, to string) error { return root.Rename(from, to) }

func uniqueRootDest(root *os.Root, dst string, src fs.FileInfo) (string, bool, error) {
	ext := path.Ext(dst)
	stem := strings.TrimSuffix(dst, ext)
	for i := 0; i <= maxCollisionSuffix; i++ {
		candidate := dst
		if i > 0 {
			candidate = fmt.Sprintf("%s (%d)%s", stem, i, ext)
		}
		info, err := root.Stat(filepath.FromSlash(candidate))
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return candidate, false, nil
		case err != nil:
			return "", false, fmt.Errorf("library: stat %s: %w", candidate, err)
		case os.SameFile(info, src):
			return candidate, true, nil
		}
	}
	return "", false, fmt.Errorf("library: no free filename for %s after %d attempts", dst, maxCollisionSuffix)
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
		dst := path.Join(newDir, name)
		if !fileExists(m.abs(src)) && !m.hasJournaledMove(src, dst) {
			continue
		}
		finalRel, err := m.moveItemFile(core.MediaFile{Path: src}, oldDir, newDir)
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
	root, err := os.OpenRoot(m.root)
	if err != nil {
		return
	}
	defer root.Close()
	itemDir, err := root.Open(filepath.FromSlash(dir))
	if err != nil {
		return
	}
	entries, err := itemDir.ReadDir(-1)
	if closeErr := itemDir.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			_ = root.Remove(filepath.Join(filepath.FromSlash(dir), entry.Name()))
		}
	}
	_ = root.Remove(filepath.FromSlash(dir))
}
