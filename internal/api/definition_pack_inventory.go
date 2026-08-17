package api

import (
	"context"
	"sort"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/indexer/catalog"
	"github.com/watzon/caravan/internal/indexer/packs"
)

type packInventoryCandidate struct {
	status packs.Status
	entry  core.DefinitionPackEntry
}

// packCatalogExecutionStatuses projects immutable persisted receipts into the
// research inventory. A runtime registry can make only an active exact entry
// addable; pending activation deliberately remains restart-gated.
func (s *server) packCatalogExecutionStatuses(ctx context.Context) []catalog.ExecutionStatus {
	if s.definitionPacks == nil {
		return []catalog.ExecutionStatus{}
	}
	revisions, err := s.definitionPacks.List(ctx)
	if err != nil {
		s.log.Error("list definition packs for inventory", "error", err)
		return []catalog.ExecutionStatus{}
	}
	chosen := map[string]packInventoryCandidate{}
	for _, revision := range revisions {
		entries, entriesErr := s.definitionPacks.Entries(ctx, revision.Source, revision.Revision)
		if entriesErr != nil {
			s.log.Error("list definition pack entries for inventory", "error", entriesErr)
			continue
		}
		for _, entry := range entries {
			key := revision.Source + "\x00" + entry.DefinitionRef
			candidate := packInventoryCandidate{status: revision, entry: entry}
			if current, ok := chosen[key]; !ok || preferPackInventoryCandidate(candidate, current) {
				chosen[key] = candidate
			}
		}
	}
	keys := make([]string, 0, len(chosen))
	for key := range chosen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]catalog.ExecutionStatus, 0, len(keys))
	for _, key := range keys {
		candidate := chosen[key]
		// Pack manifests validate metadata ids only as identifiers, never
		// against the research catalog. catalog.Inventory hard-fails on an
		// unknown id, so an off-catalog entry must not poison the whole
		// catalog response.
		if !catalog.HasMetadataID(candidate.entry.MetadataID) {
			s.log.Warn("definition pack entry references unknown metadata id",
				"source", candidate.status.Source, "revision", candidate.status.Revision,
				"metadata_id", candidate.entry.MetadataID)
			continue
		}
		out = append(out, s.packInventoryStatus(candidate))
	}
	return out
}

func preferPackInventoryCandidate(left, right packInventoryCandidate) bool {
	rank := func(status packs.Status) int {
		switch {
		case status.Active:
			return 0
		case status.Pending:
			return 1
		case status.State == core.DefinitionPackInstalled:
			return 2
		default:
			return 3
		}
	}
	if l, r := rank(left.status), rank(right.status); l != r {
		return l < r
	}
	if !left.status.InstalledAt.Equal(right.status.InstalledAt) {
		return left.status.InstalledAt.After(right.status.InstalledAt)
	}
	return left.status.Revision > right.status.Revision
}

func (s *server) packInventoryStatus(candidate packInventoryCandidate) catalog.ExecutionStatus {
	status, entry := candidate.status, candidate.entry
	out := catalog.ExecutionStatus{
		MetadataID: entry.MetadataID, DefinitionID: entry.DefinitionRef, Source: status.Source, Revision: status.Revision,
		Digest: entry.Digest, Unsupported: append([]string(nil), entry.Unsupported...),
	}
	switch {
	case status.State == core.DefinitionPackFailed:
		out.State, out.BlockedCode = catalog.InventoryStateUnsupported, status.ValidationCode
		return out
	case entry.State == core.DefinitionPackEntryUnsupported:
		out.State = catalog.InventoryStateUnsupported
		if containsCode(entry.Unsupported, "compiler.invalid") {
			out.State = catalog.InventoryStateQuarantined
		}
		return out
	case !status.Active:
		out.State, out.BlockedCode = catalog.InventoryStateRunnableUnverified, "pack.restart_required"
		return out
	case s.exactLocalDefinitions == nil:
		out.State, out.BlockedCode = catalog.InventoryStateRunnableUnverified, "pack.runtime.unavailable"
		return out
	}
	schema, ok := s.exactLocalDefinitions(entry.DefinitionRef, status.Source, status.Revision, entry.Digest)
	if !ok {
		out.State, out.BlockedCode = catalog.InventoryStateRunnableUnverified, "pack.runtime.unavailable"
		return out
	}
	out.State, out.Addable = catalog.InventoryStateVerified, true
	out.BaseURLs = append([]string(nil), schema.BaseURLs...)
	if len(out.BaseURLs) == 0 {
		out.State, out.Addable, out.BlockedCode = catalog.InventoryStateRunnableUnverified, false, "definition.url.unavailable"
		return out
	}
	if len(schema.Fields) > 0 {
		out.Settings = append([]catalog.Setting(nil), schema.Fields...)
	} else {
		out.Settings = make([]catalog.Setting, 0, len(schema.Settings))
		for _, name := range schema.Settings {
			name = strings.TrimSpace(name)
			if name != "" {
				out.Settings = append(out.Settings, catalog.Setting{Name: name, Label: name, Type: "text", Secret: true, Editable: true})
			}
		}
	}
	return out
}

func containsCode(codes []string, want string) bool {
	for _, code := range codes {
		if code == want {
			return true
		}
	}
	return false
}
