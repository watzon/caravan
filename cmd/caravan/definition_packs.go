package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/watzon/caravan/internal/cardigann"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// definitionPackRuntimeStore is the internal lifecycle seam for an owner-facing
// pack management service. No HTTP/API contract is implied by this interface.
type definitionPackRuntimeStore interface {
	GetActiveDefinitionPackRevisions(context.Context) ([]core.DefinitionPackRevision, error)
	GetPendingDefinitionPackRevisions(context.Context) ([]core.DefinitionPackRevision, error)
	ListDefinitionPackEntries(context.Context, string, string) ([]core.DefinitionPackEntry, error)
	PromotePendingDefinitionPack(context.Context, string, string) error
	RollbackPendingDefinitionPack(context.Context, string, string, string) error
	QuarantineDefinitionPack(context.Context, string, string, string) error
}

type installedPackOpener func(string, core.DefinitionPackRevision, []core.DefinitionPackEntry) (cardigann.Provider, error)

func storeInstalledPackOpener(dataDir, currentVersion string) installedPackOpener {
	return func(_ string, revision core.DefinitionPackRevision, entries []core.DefinitionPackEntry) (cardigann.Provider, error) {
		return cardigann.OpenVerifiedInstalledPackProvider(filepath.Join(dataDir, "indexer-packs", revision.ArchiveRelPath), currentVersion, revision, entries)
	}
}

// loadPersistedDefinitionPacks performs the startup trust boundary. Pending
// revisions self-test in an isolated Registry before promotion. Active packs are
// then loaded by exact archive/revision/entry pins only; no source:id fallback
// path exists for persisted pins.
func loadPersistedDefinitionPacks(ctx context.Context, packs definitionPackRuntimeStore, dataDir string, registry *cardigann.Registry, opener installedPackOpener) error {
	if packs == nil || registry == nil || opener == nil {
		return fmt.Errorf("definition pack runtime requires store, registry, and opener")
	}
	loaded := make(map[string]struct{})
	identity := func(revision core.DefinitionPackRevision) string { return revision.Source + "\x00" + revision.Revision }
	pending, err := packs.GetPendingDefinitionPackRevisions(ctx)
	if err != nil {
		return err
	}
	for _, revision := range pending {
		entries, entryErr := packs.ListDefinitionPackEntries(ctx, revision.Source, revision.Revision)
		provider, openErr := opener(dataDir, revision, entries)
		var prepared *cardigann.PreparedProvider
		if entryErr == nil && openErr == nil {
			var problems []error
			prepared, _, problems = registry.PrepareProvider(provider)
			if len(problems) > 0 {
				openErr = problems[0]
			}
		}
		if entryErr != nil || openErr != nil {
			if err := packs.RollbackPendingDefinitionPack(ctx, revision.Source, revision.Revision, "pack.registry.resolution_failed"); err != nil {
				return fmt.Errorf("rollback failed definition pack %s:%s: %w", revision.Source, revision.Revision, err)
			}
			continue
		}
		if err := packs.PromotePendingDefinitionPack(ctx, revision.Source, revision.Revision); err != nil {
			return fmt.Errorf("promote validated definition pack %s:%s: %w", revision.Source, revision.Revision, err)
		}
		prepared.Commit()
		loaded[identity(revision)] = struct{}{}
	}
	active, err := packs.GetActiveDefinitionPackRevisions(ctx)
	if err != nil {
		return err
	}
	for _, revision := range active {
		if _, alreadyLoaded := loaded[identity(revision)]; alreadyLoaded {
			continue
		}
		entries, entryErr := packs.ListDefinitionPackEntries(ctx, revision.Source, revision.Revision)
		provider, openErr := opener(dataDir, revision, entries)
		var prepared *cardigann.PreparedProvider
		if entryErr == nil && openErr == nil {
			var problems []error
			prepared, _, problems = registry.PrepareProvider(provider)
			if len(problems) > 0 {
				openErr = problems[0]
			}
		}
		if entryErr != nil || openErr != nil {
			if err := packs.QuarantineDefinitionPack(ctx, revision.Source, revision.Revision, "pack.registry.resolution_failed"); err != nil {
				return fmt.Errorf("quarantine invalid active definition pack %s:%s: %w", revision.Source, revision.Revision, err)
			}
			continue
		}
		prepared.Commit()
	}
	return nil
}

var _ definitionPackRuntimeStore = (*store.Store)(nil)
