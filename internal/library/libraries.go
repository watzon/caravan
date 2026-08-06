package library

import (
	"context"
	"errors"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// This file is where an operation decides WHICH library a path or an item
// belongs to, now that there can be several libraries of one kind (migration
// 0022). The rule, applied in order, is:
//
//  1. the item's own row wins — an item already in a library stays there,
//     whatever query or file location brought us here; a move is an explicit
//     operation, never a side effect of a refresh or a rescan;
//  2. the caller's target (an add's library choice) answers for a new item;
//  3. the library whose root contains the file answers for a scanned file;
//  4. the kind's default library answers when nothing above did.
//
// Every step is kind-checked: a fallback that names a library of the wrong
// kind is skipped rather than honored, because filing a movie under a tv root
// (or an adult series anywhere else) is exactly the drift UpsertSeries refuses.

// libraryForPath returns the library whose root contains rel, or nil when no
// library root does. Longest prefix wins, so if a root were ever nested inside
// another the inner library would answer, not the outer.
func libraryForPath(libs []core.Library, rel string) *core.Library {
	var best *core.Library
	bestLen := -1
	for i := range libs {
		root := libs[i].RootPath
		if root == "" || len(root) <= bestLen {
			continue
		}
		if strings.HasPrefix(rel, root+"/") {
			best = &libs[i]
			bestLen = len(root)
		}
	}
	return best
}

// libraryByIDOrDefault resolves id to a library of the given kind, falling
// back to the kind's default when id is zero, names a vanished row, or names a
// library of another kind.
func (m *Manager) libraryByIDOrDefault(ctx context.Context, id int64, kind string) (*core.Library, error) {
	if id != 0 {
		lib, err := m.store.GetLibrary(ctx, id)
		if err == nil && lib.Kind == kind {
			return lib, nil
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	return m.store.GetDefaultLibrary(ctx, kind)
}

// resolveLibrary applies the file-location and default steps shared by the
// per-kind resolvers below: rel's containing library when it speaks the kind,
// the kind's default otherwise.
func (m *Manager) resolveLibrary(ctx context.Context, kind, rel string, targetID int64) (*core.Library, error) {
	if targetID != 0 {
		lib, err := m.store.GetLibrary(ctx, targetID)
		if err == nil && lib.Kind == kind {
			return lib, nil
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	if rel != "" {
		libs, err := m.store.ListLibraries(ctx)
		if err != nil {
			return nil, err
		}
		if lib := libraryForPath(libs, rel); lib != nil && lib.Kind == kind {
			return lib, nil
		}
	}
	return m.store.GetDefaultLibrary(ctx, kind)
}

// movieLibrary resolves the library a movie import or add lands in. rel is the
// file being imported ("" for a file-less add); targetID is the add's explicit
// choice (0 for none).
func (m *Manager) movieLibrary(ctx context.Context, tmdbID int64, rel string, targetID int64) (*core.Library, error) {
	if tmdbID != 0 {
		existing, err := m.store.GetMovieByTMDBID(ctx, tmdbID)
		if err == nil && existing.LibraryID != 0 {
			return m.libraryByIDOrDefault(ctx, existing.LibraryID, core.LibraryKindMovie)
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	return m.resolveLibrary(ctx, core.LibraryKindMovie, rel, targetID)
}

// seriesLibrary is movieLibrary's television twin.
func (m *Manager) seriesLibrary(ctx context.Context, tmdbID int64, rel string, targetID int64) (*core.Library, error) {
	if tmdbID != 0 {
		existing, err := m.store.GetSeriesByTMDBID(ctx, tmdbID)
		if err == nil && existing.LibraryID != 0 {
			return m.libraryByIDOrDefault(ctx, existing.LibraryID, core.LibraryKindTV)
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	return m.resolveLibrary(ctx, core.LibraryKindTV, rel, targetID)
}

// siteLibrary is movieLibrary's adult twin, keyed by stash id.
func (m *Manager) siteLibrary(ctx context.Context, stashID, rel string, targetID int64) (*core.Library, error) {
	if stashID != "" {
		existing, err := m.store.GetSeriesByStashID(ctx, stashID)
		if err == nil && existing.LibraryID != 0 {
			return m.libraryByIDOrDefault(ctx, existing.LibraryID, core.LibraryKindAdult)
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	return m.resolveLibrary(ctx, core.LibraryKindAdult, rel, targetID)
}

// seriesLibraryOf resolves the library an existing series row belongs to,
// healing a zero LibraryID through the kind's default. It is what the paths
// that already hold a row (scene imports, site refreshes) use instead of the
// id-based resolvers above.
func (m *Manager) seriesLibraryOf(ctx context.Context, sr *core.Series) (*core.Library, error) {
	return m.libraryByIDOrDefault(ctx, sr.LibraryID, core.LibraryKindForSeries(sr.Kind))
}

// metadataFor resolves the metadata provider that answers for one library:
// the library's own choice through the registry when both exist, the
// Manager-level provider otherwise.
//
// The fallback runs even with a registry wired, because the two are built
// from the same settings in the full wiring — a registry that answers nil for
// a library's id means "not configured", which is exactly what a nil
// Manager-level provider means, and every caller already degrades on nil
// (park the file, refuse the add).
func (m *Manager) metadataFor(ctx context.Context, lib *core.Library) core.MetadataProvider {
	if m.providers != nil && lib != nil && lib.Provider != "" {
		if p := m.providers.Metadata(ctx, lib.Provider); p != nil {
			return p
		}
	}
	return m.provider
}

// adultFor is metadataFor's adult twin. It does NOT replace adultReady: the
// module switch is a global gate and stays one.
func (m *Manager) adultFor(ctx context.Context, lib *core.Library) core.AdultMetadataProvider {
	if m.providers != nil && lib != nil && lib.Provider != "" {
		if p := m.providers.Adult(ctx, lib.Provider); p != nil {
			return p
		}
	}
	return m.adult
}
