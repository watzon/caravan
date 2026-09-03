package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/usenet"
)

// SweepIncomplete removes completed, imported data left behind by an
// interrupted cleanup. It runs before the download engines restore, so no
// writer can appear between the database snapshot and the removals.
//
// A parked file protects its whole download directory because the user still
// needs the source to resolve the manual match. Unknown directories are kept:
// an external client can write below incomplete/ without storing a relative
// path in Caravan's database.
func (m *Manager) SweepIncomplete(ctx context.Context) ([]string, error) {
	downloads, err := m.store.ListDownloads(ctx)
	if err != nil {
		return nil, err
	}
	unmatched, err := m.store.ListUnmatchedFiles(ctx)
	if err != nil {
		return nil, err
	}

	protected := make(map[string]bool)
	for _, file := range unmatched {
		if entry, ok := incompleteEntry(file.Path); ok {
			protected[entry] = true
		}
	}

	settled := make(map[core.DownloadID]string)
	for _, item := range downloads {
		entry, ok := incompleteEntry(item.SavePath)
		if !ok || !ownedIncompleteEngine(item.Engine) {
			continue
		}
		if item.State == core.DownloadCompleted && !protected[entry] {
			grab, grabErr := m.store.GetGrabByDownloadID(ctx, item.EngineID)
			switch {
			case grabErr == nil && reclaimableGrab(grab.Status):
				settled[item.EngineID] = entry
				continue
			case grabErr != nil && !errors.Is(grabErr, store.ErrNotFound):
				return nil, grabErr
			}
		}
		protected[entry] = true
	}

	root, err := os.OpenRoot(m.root)
	if err != nil {
		return nil, fmt.Errorf("library: open storage root: %w", err)
	}
	defer root.Close()

	removed := []string{}
	removedEntries := make(map[string]bool)
	for _, entry := range settled {
		if protected[entry] || removedEntries[entry] {
			continue
		}
		rel := path.Join(download.IncompleteDir, entry)
		if err := root.RemoveAll(filepath.FromSlash(rel)); err != nil {
			return removed, fmt.Errorf("library: remove stale download data %s: %w", rel, err)
		}
		removed = append(removed, rel)
		removedEntries[entry] = true
	}

	for id, entry := range settled {
		if protected[entry] {
			continue
		}
		item, err := m.store.GetDownloadByEngineID(ctx, id)
		if err != nil {
			return removed, err
		}
		for _, sidecar := range incompleteSidecars(*item) {
			if err := root.Remove(filepath.FromSlash(sidecar)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return removed, fmt.Errorf("library: remove stale download sidecar %s: %w", sidecar, err)
			}
		}
		if err := m.store.DeleteDownloadByEngineID(ctx, id); err != nil {
			return removed, err
		}
	}
	sort.Strings(removed)
	return removed, nil
}

// reclaimableGrab reports an import outcome that makes completed source data
// disposable. A failed grab is not enough proof because the user can dismiss
// its Scan Review row without deleting or importing the source file.
func reclaimableGrab(status string) bool {
	return status == core.GrabStatusImported
}

func ownedIncompleteEngine(engine string) bool {
	return engine == "" || engine == download.EngineName || engine == usenet.EngineName
}

// incompleteSidecars lists the built-in engine metadata that belongs to one
// settled download. The engine IDs are generated names, but invalid values are
// ignored here so a damaged database cannot make cleanup address another path.
func incompleteSidecars(item core.Download) []string {
	id := string(item.EngineID)
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\`) {
		return nil
	}
	extensions := []string{}
	switch item.Engine {
	case download.EngineName:
		extensions = []string{".torrent"}
	case usenet.EngineName:
		extensions = []string{".nzb", ".done"}
	case "":
		// Early download rows did not always record the engine. IDs remain
		// unique, so probing all built-in extensions is safe.
		extensions = []string{".torrent", ".nzb", ".done"}
	}
	paths := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		paths = append(paths, path.Join(download.IncompleteDir, ".caravan", id+extension))
	}
	return paths
}

// incompleteEntry returns the first path component below incomplete/. That is
// the ownership unit the engines remove, whether the download is one file or a
// directory tree.
func incompleteEntry(rel string) (string, bool) {
	if filepath.IsAbs(rel) {
		return "", false
	}
	slash := filepath.ToSlash(rel)
	for _, part := range strings.Split(slash, "/") {
		if part == "." || part == ".." {
			return "", false
		}
	}
	clean := path.Clean(slash)
	rest, ok := strings.CutPrefix(clean, download.IncompleteDir+"/")
	if !ok || rest == "" || rest == "." || rest == ".." {
		return "", false
	}
	entry, _, _ := strings.Cut(rest, "/")
	if entry == "" || entry == "." || entry == ".." || entry == ".caravan" {
		return "", false
	}
	return entry, true
}
