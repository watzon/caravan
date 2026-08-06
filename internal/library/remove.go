package library

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// EventCategoryLibrary groups library-maintenance events in the activity feed.
const EventCategoryLibrary = "library"

// ErrOutsideLibrary is what a path that does not resolve inside one of the
// library's section directories fails with.
var ErrOutsideLibrary = errors.New("path outside the library sections")

// RemoveMovie stops tracking a movie and, when deleteFiles is set, removes its
// Caravan-owned files from the live library first.
//
// With deleteFiles false the filesystem is untouched and a rescan re-adds the
// movie. With it set, media files and the poster and NFO Caravan wrote beside
// them are either deleted or moved to a recycle batch when retention is set.
func (m *Manager) RemoveMovie(ctx context.Context, id int64, deleteFiles bool) error {
	if deleteFiles {
		mv, err := m.store.GetMovie(ctx, id)
		if err != nil {
			return err
		}
		files, err := m.store.ListMediaFilesForMovie(ctx, id)
		if err != nil {
			return err
		}
		if err := m.removeItemFiles(ctx, mv.Path, MovieNFOName, files); err != nil {
			return err
		}
	}
	return m.store.DeleteMovie(ctx, id)
}

// RemoveSeries is RemoveMovie's series twin. The files it deletes are every
// episode file of the series, which is what empties the season folders and,
// with them, the series folder.
func (m *Manager) RemoveSeries(ctx context.Context, id int64, deleteFiles bool) error {
	if deleteFiles {
		sr, err := m.store.GetSeries(ctx, id)
		if err != nil {
			return err
		}
		pairs, err := m.store.ListEpisodeMediaFilesForSeries(ctx, id)
		if err != nil {
			return err
		}
		// A multi-episode file is listed once per episode it covers; deleting
		// it twice would turn the second pass into a spurious "already gone".
		seen := make(map[int64]bool, len(pairs))
		files := make([]core.MediaFile, 0, len(pairs))
		for _, pair := range pairs {
			if seen[pair.File.ID] {
				continue
			}
			seen[pair.File.ID] = true
			files = append(files, pair.File)
		}
		if err := m.removeItemFiles(ctx, sr.Path, TVShowNFOName, files); err != nil {
			return err
		}
	}
	return m.store.DeleteSeries(ctx, id)
}

// removeItemFiles deletes an item's media files, then the sidecars Caravan
// itself wrote into its folder, then prunes whatever is now empty.
//
// dir is the item's storage-root-relative folder and may be empty — an item
// added before it had any file has none. Anything in the folder Caravan did
// not write (subtitles, extras, a user's own artwork) survives, and keeps the
// folder alive with it: this deletes what Caravan put there, not the folder's
// contents.
func (m *Manager) removeItemFiles(ctx context.Context, dir, nfoName string, files []core.MediaFile) error {
	retentionDays, err := m.recycleRetentionDays(ctx)
	if err != nil {
		return err
	}
	if retentionDays > 0 {
		movedFiles, err := m.recycleItemFiles(ctx, dir, nfoName, files)
		if err != nil {
			return err
		}
		for _, path := range movedFiles {
			if err := m.store.DeleteMediaFileByPath(ctx, path); err != nil {
				return err
			}
		}
		return nil
	}

	prune := make([]string, 0, len(files)+1)
	for _, f := range files {
		abs, err := m.insideLibrary(f.Path)
		if err != nil {
			m.refuseRemoval(ctx, f.Path, err)
			continue
		}
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("library: delete %s: %w", f.Path, err)
		}
		if err := m.store.DeleteMediaFileByPath(ctx, f.Path); err != nil {
			return err
		}
		prune = append(prune, filepath.Dir(abs))
	}

	if dir != "" {
		abs, err := m.insideLibrary(dir)
		if err != nil {
			m.refuseRemoval(ctx, dir, err)
		} else {
			for _, name := range []string{PosterName, nfoName} {
				if err := os.Remove(filepath.Join(abs, name)); err != nil && !errors.Is(err, fs.ErrNotExist) {
					return fmt.Errorf("library: delete %s: %w", path.Join(dir, name), err)
				}
			}
			prune = append(prune, abs)
		}
	}

	for _, abs := range prune {
		if err := m.pruneEmpty(abs); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) recycleRetentionDays(ctx context.Context) (int, error) {
	value, err := m.store.GetSetting(ctx, store.SettingRecycleRetentionDays)
	if errors.Is(err, store.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("library: get recycle retention: %w", err)
	}
	days, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("library: parse recycle retention %q: %w", value, err)
	}
	if days < 0 || days > 3650 {
		return 0, fmt.Errorf("library: recycle retention %d is outside 0 to 3650 days", days)
	}
	return days, nil
}

func (m *Manager) recycleItemFiles(ctx context.Context, dir, nfoName string, files []core.MediaFile) ([]string, error) {
	batch := time.Now().UTC().Format("20060102T150405Z")
	prune := make([]string, 0, len(files)+1)
	movedFiles := make([]string, 0, len(files))
	moved := 0
	move := func(rel string) (bool, error) {
		abs, err := m.insideLibrary(rel)
		if err != nil {
			m.refuseRemoval(ctx, rel, err)
			return false, nil
		}
		if _, err := os.Lstat(abs); errors.Is(err, fs.ErrNotExist) {
			return false, nil
		} else if err != nil {
			return false, fmt.Errorf("library: inspect %s: %w", rel, err)
		}
		dst := filepath.Join(m.root, "recycle", batch, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return false, fmt.Errorf("library: create recycle destination for %s: %w", rel, err)
		}
		if err := os.Rename(abs, dst); err != nil {
			return false, fmt.Errorf("library: recycle %s: %w", rel, err)
		}
		prune = append(prune, filepath.Dir(abs))
		moved++
		return true, nil
	}

	for _, file := range files {
		moved, err := move(file.Path)
		if err != nil {
			return nil, err
		}
		if moved {
			movedFiles = append(movedFiles, file.Path)
		}
	}
	if dir != "" {
		for _, name := range []string{PosterName, nfoName} {
			if _, err := move(path.Join(dir, name)); err != nil {
				return nil, err
			}
		}
	}
	for _, abs := range prune {
		if err := m.pruneEmpty(abs); err != nil {
			return nil, err
		}
	}
	if moved > 0 {
		_ = m.store.InsertEvent(ctx, &core.Event{
			Level:    core.EventLevelInfo,
			Category: EventCategoryLibrary,
			Message:  "Moved deleted library files to recycle",
			Detail:   fmt.Sprintf("batch %s: %d file(s)", batch, moved),
		})
	}
	return movedFiles, nil
}

// HandleRecycleCleanup is an automation.Handler that removes expired recycle
// batches. It never follows or removes entries the user placed beside batches.
func (m *Manager) HandleRecycleCleanup(ctx context.Context, _ *store.Store, _ json.RawMessage) error {
	days, err := m.recycleRetentionDays(ctx)
	if err != nil {
		return err
	}
	root := filepath.Join(m.root, "recycle")
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("library: read recycle: %w", err)
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -days)
	removed := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		batch, err := time.Parse("20060102T150405Z", entry.Name())
		if err != nil || (days > 0 && !batch.Before(cutoff)) {
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, entry.Name())); err != nil {
			return fmt.Errorf("library: remove recycle batch %s: %w", entry.Name(), err)
		}
		removed++
	}
	if removed > 0 {
		_ = m.store.InsertEvent(ctx, &core.Event{
			Level:    core.EventLevelInfo,
			Category: EventCategoryLibrary,
			Message:  "Cleaned recycle batches",
			Detail:   fmt.Sprintf("removed %d expired batch(es)", removed),
		})
	}
	return nil
}

// insideLibrary resolves a storage-root-relative path and returns it only when
// it lands strictly inside one of the library's section directories.
//
// It is the one guard every removal goes through, and it answers two questions
// at once. A path that escapes the storage root — an absolute row written
// before the path model was enforced, or one with a ".." in it — is not under
// any section and is refused. And a path that *is* a section directory, the
// library directory, or the storage root itself is refused too: those are the
// layout SPEC §6 promises to players, and they outlive every item in them.
func (m *Manager) insideLibrary(rel string) (string, error) {
	root, err := filepath.Abs(m.root)
	if err != nil {
		return "", fmt.Errorf("library: resolve storage root %s: %w", m.root, err)
	}
	abs, err := filepath.Abs(m.abs(rel))
	if err != nil {
		return "", fmt.Errorf("library: resolve %s: %w", rel, err)
	}
	if !insideSection(root, abs) {
		return "", fmt.Errorf("library: %s: %w", rel, ErrOutsideLibrary)
	}
	return abs, nil
}

// insideSection reports whether abs is strictly below library/Movies or
// library/TV under root. Both paths are already absolute and cleaned.
func insideSection(root, abs string) bool {
	for _, section := range []string{MoviesDir, TVDir} {
		base := filepath.Join(root, LibraryDir, section) + string(filepath.Separator)
		if strings.HasPrefix(abs, base) {
			return true
		}
	}
	return false
}

// pruneEmpty removes abs and each parent that the removal emptied, stopping as
// soon as a directory still holds something or the walk reaches a section
// directory.
func (m *Manager) pruneEmpty(abs string) error {
	root, err := filepath.Abs(m.root)
	if err != nil {
		return fmt.Errorf("library: resolve storage root %s: %w", m.root, err)
	}
	for insideSection(root, abs) {
		entries, err := os.ReadDir(abs)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			// Already gone: keep walking up, the parent may still be prunable.
		case err != nil:
			return fmt.Errorf("library: read %s: %w", abs, err)
		case len(entries) > 0:
			return nil
		default:
			if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("library: remove %s: %w", abs, err)
			}
		}
		abs = filepath.Dir(abs)
	}
	return nil
}

// refuseRemoval records a path a removal would not touch. It is a warning in
// the feed rather than an error out of the removal: the item still stops being
// tracked, and a path Caravan does not own is exactly what it must not delete
// quietly.
func (m *Manager) refuseRemoval(ctx context.Context, rel string, cause error) {
	_ = m.store.InsertEvent(ctx, &core.Event{
		Level:    core.EventLevelWarn,
		Category: EventCategoryLibrary,
		Message:  "Refused to delete a file outside the library",
		Detail:   fmt.Sprintf("%s: %v", rel, cause),
	})
}
