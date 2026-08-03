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

	"github.com/watzon/caravan/internal/core"
)

// EventCategoryLibrary groups library-maintenance events in the activity feed.
const EventCategoryLibrary = "library"

// ErrOutsideLibrary is what a path that does not resolve inside one of the
// library's section directories fails with. It is a refusal, not a warning:
// nothing at such a path is deleted.
var ErrOutsideLibrary = errors.New("path outside the library sections")

// RemoveMovie stops tracking a movie and, when deleteFiles is set, deletes its
// files from disk first.
//
// With deleteFiles false this is the untrack the API has always done: rows go,
// the filesystem is untouched, and a rescan re-adds the movie (SPEC §1.2 — the
// filesystem is the source of truth). With it set, the movie's media files and
// the poster and NFO Caravan wrote beside them go too, and the folders that
// held them are pruned while they are empty.
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
	prune := make([]string, 0, len(files)+1)
	for _, f := range files {
		abs, err := m.insideLibrary(f.Path)
		if err != nil {
			// A media_files row is data, not a command. One naming somewhere
			// outside the library is a database Caravan cannot trust to steer
			// os.Remove, so it is reported and skipped — the untrack still
			// happens, which is what the caller asked for.
			m.refuseRemoval(ctx, f.Path, err)
			continue
		}
		if err := os.Remove(abs); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("library: delete %s: %w", f.Path, err)
		}
		// The file is gone either way — a row that outlived its file is stale,
		// not a reason to keep describing something that is not there.
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
