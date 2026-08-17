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
// Every step is kind-checked through core.LibraryKindAccepts: a fallback that
// names a library which cannot hold the item is skipped rather than honored,
// because filing a movie under a tv root (or an adult series anywhere else) is
// exactly the drift UpsertSeries refuses. The check is the acceptance rule
// rather than equality because an anime library legitimately holds both films
// and series — one shelf, two vocabularies.

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

// libraryByIDOrDefault resolves id to a library that accepts the given kind,
// falling back to the kind's default when id names a vanished row or a library
// that cannot hold it.
//
// Zero takes the same fallback, and it is no longer a case with a meaning of its
// own: migration 0011 stamped every item row that carried one, so a zero here is
// a caller that had nothing to resolve rather than a row waiting to be healed.
func (m *Manager) libraryByIDOrDefault(ctx context.Context, id int64, kind string) (*core.Library, error) {
	lib, err := m.store.GetLibrary(ctx, id)
	if err == nil && core.LibraryKindAccepts(lib.Kind, kind) {
		return lib, nil
	}
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return nil, err
	}
	return m.store.GetDefaultLibrary(ctx, kind)
}

// resolveLibrary applies the file-location and default steps shared by the
// per-kind resolvers below: rel's containing library when it accepts the kind,
// the kind's default otherwise.
func (m *Manager) resolveLibrary(ctx context.Context, kind, rel string, targetID int64) (*core.Library, error) {
	if targetID != 0 {
		lib, err := m.store.GetLibrary(ctx, targetID)
		if err == nil && core.LibraryKindAccepts(lib.Kind, kind) {
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
		if lib := libraryForPath(libs, rel); lib != nil && core.LibraryKindAccepts(lib.Kind, kind) {
			return lib, nil
		}
	}
	return m.store.GetDefaultLibrary(ctx, kind)
}

// movieLibrary resolves the library a movie import or add lands in. ref is the
// provider identity of the title (see existingMovieRow for how a row is found
// from it); rel is the file being imported ("" for a file-less add); targetID
// is the add's explicit choice (0 for none).
func (m *Manager) movieLibrary(ctx context.Context, ref core.ItemRef, rel string, targetID int64) (*core.Library, error) {
	existing, err := m.existingMovieRow(ctx, ref, ref.TMDBID())
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return m.libraryByIDOrDefault(ctx, existing.LibraryID, core.LibraryKindMovie)
	}
	return m.resolveLibrary(ctx, core.LibraryKindMovie, rel, targetID)
}

// seriesLibrary is movieLibrary's television twin.
func (m *Manager) seriesLibrary(ctx context.Context, ref core.ItemRef, rel string, targetID int64) (*core.Library, error) {
	existing, err := m.existingSeriesRow(ctx, ref, ref.TMDBID())
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return m.libraryByIDOrDefault(ctx, existing.LibraryID, core.LibraryKindTV)
	}
	return m.resolveLibrary(ctx, core.LibraryKindTV, rel, targetID)
}

// siteLibrary is movieLibrary's adult twin, keyed by (instance, stash id).
//
// The instance is half the key rather than decoration. Since 0026 the same
// UUID legitimately names a site on two boxes — the public stash-boxes are
// forks of one another — so a lookup on the bare stash id would hand the second
// box's site the first box's library, and the two rows would organize into one
// another's folders.
func (m *Manager) siteLibrary(ctx context.Context, ref core.ItemRef, rel string, targetID int64) (*core.Library, error) {
	if ref.Valid() {
		existing, err := m.store.GetSeriesByProviderRef(ctx, ref.Provider, ref.Ref)
		if err == nil {
			return m.libraryByIDOrDefault(ctx, existing.LibraryID, core.LibraryKindAdult)
		}
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	return m.resolveLibrary(ctx, core.LibraryKindAdult, rel, targetID)
}

// seriesLibraryOf resolves the library an existing series row belongs to. It is
// what the paths that already hold a row (scene imports, site refreshes) use
// instead of the id-based resolvers above.
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

// providerBinding is one rung of a library's provider chain: the id, and the
// client that answered for it. The id travels with the client because
// everything a chain walk records afterwards — a search failure, which
// provider a scan matched through — names the provider rather than the object.
type providerBinding struct {
	ID string
	P  core.MetadataProvider
}

// metadataChain resolves a library's ordered provider list to the clients that
// are actually configured, dropping ids that answer nil. Empty result =
// nothing can identify anything for this library; callers degrade as for a nil
// provider.
//
// Order is the library's own (core.Library.ProviderChain), and it is the whole
// configuration: which provider is asked first about an anime is a decision the
// owner makes per library, not one this package can infer.
func (m *Manager) metadataChain(ctx context.Context, lib *core.Library) []providerBinding {
	if lib == nil {
		return nil
	}
	chain := lib.ProviderChain()
	out := make([]providerBinding, 0, len(chain))
	for _, id := range chain {
		if p := m.providerByID(ctx, id); p != nil {
			out = append(out, providerBinding{ID: id, P: p})
		}
	}
	return out
}

// providerByID resolves ONE provider id through the registry, with NO
// fallback. It is what an item's pinned ref is fetched through, and the
// absent fallback is the whole point: metadataFor may degrade to the
// Manager-level provider because "which provider refreshes this library" has
// a sane default, but "which provider owns id 21" does not. Asking TMDB for
// an AniList ref does not fail — it returns a different show, and writes it
// over the row.
//
// The one exception is the TMDB id itself, and it is not a fallback across
// providers: a registry that answers nil for "tmdb" is the pre-registry wiring
// (and every test's seam), where the Manager-level provider IS the TMDB
// client.
func (m *Manager) providerByID(ctx context.Context, id string) core.MetadataProvider {
	if id == "" {
		return nil
	}
	if m.providers != nil {
		if p := m.providers.Metadata(ctx, id); p != nil {
			return p
		}
	}
	if id == core.ProviderTMDB {
		return m.provider
	}
	return nil
}

// adultRef fills in the instance a ref does not name.
//
// Empty is the legacy instance, `stashbox`: it is the compatibility id older
// adult rows use and the id the first instance on a fresh install
// takes. Empty is therefore never "no box" — it is the one box that was there
// before there could be more than one.
func adultRef(ref core.ItemRef) core.ItemRef {
	if ref.Provider == "" {
		ref.Provider = core.ProviderStashbox
	}
	return ref
}

// adultByID resolves ONE stash-box instance id through the registry, with NO
// fallback. It is providerByID's rule on the adult half, and the absent
// fallback matters MORE here.
//
// Asking box A for box B's UUID does not fail. The public stash-boxes are forks
// of one another, so the same UUID frequently names a different site on the
// other box — a refresh that fell back would fetch that other site and write
// its title and its catalogue over the row. An instance the owner deleted is
// nil, and the callers report that rather than asking somebody else.
//
// The one exception is the legacy id, and it is not a fallback across
// instances: a registry that answers nil for `stashbox` is the pre-registry
// wiring (and every test's seam), where the Manager-level provider IS the
// stash-box client.
func (m *Manager) adultByID(ctx context.Context, id string) core.AdultMetadataProvider {
	if id == "" {
		id = core.ProviderStashbox
	}
	if m.providers != nil {
		if p := m.providers.Adult(ctx, id); p != nil {
			return p
		}
	}
	if id == core.ProviderStashbox {
		return m.adult
	}
	return nil
}

// adultBinding is providerBinding's adult twin: one rung of a library's chain,
// carrying the instance id beside the client. The id travels because it is what
// a new site row is PINNED to — the chain identifies, and the winner's id is
// the box every later refresh of that row asks.
type adultBinding struct {
	ID string
	P  core.AdultMetadataProvider
}

// adultChain is metadataChain's adult twin: the library's ordered instance list
// resolved to the clients that are actually configured, dropping ids that
// answer nil. An empty result means nothing can identify a site for this
// library, and callers degrade exactly as they do for a nil provider.
func (m *Manager) adultChain(ctx context.Context, lib *core.Library) []adultBinding {
	if lib == nil {
		return nil
	}
	chain := lib.ProviderChain()
	out := make([]adultBinding, 0, len(chain))
	for _, id := range chain {
		if p := m.adultByID(ctx, id); p != nil {
			out = append(out, adultBinding{ID: id, P: p})
		}
	}
	return out
}
