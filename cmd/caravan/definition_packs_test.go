package main

import (
	"context"
	"errors"
	"testing"

	"github.com/watzon/caravan/internal/cardigann"
	"github.com/watzon/caravan/internal/core"
)

type fakeDefinitionPackStore struct {
	active     []core.DefinitionPackRevision
	pending    []core.DefinitionPackRevision
	entries    map[string][]core.DefinitionPackEntry
	promoted   []string
	rolled     []string
	promoteErr error
}

func (s *fakeDefinitionPackStore) GetActiveDefinitionPackRevisions(context.Context) ([]core.DefinitionPackRevision, error) {
	return s.active, nil
}
func (s *fakeDefinitionPackStore) GetPendingDefinitionPackRevisions(context.Context) ([]core.DefinitionPackRevision, error) {
	return s.pending, nil
}
func (s *fakeDefinitionPackStore) ListDefinitionPackEntries(_ context.Context, source, revision string) ([]core.DefinitionPackEntry, error) {
	return s.entries[source+":"+revision], nil
}
func (s *fakeDefinitionPackStore) PromotePendingDefinitionPack(_ context.Context, source, revision string) error {
	if s.promoteErr != nil {
		return s.promoteErr
	}
	s.promoted = append(s.promoted, source+":"+revision)
	for i, pending := range s.pending {
		if pending.Source == source && pending.Revision == revision {
			pending.Pending = false
			pending.Active = true
			pending.LastKnownGood = true
			s.active = append(s.active, pending)
			s.pending = append(s.pending[:i], s.pending[i+1:]...)
			break
		}
	}
	return nil
}
func (s *fakeDefinitionPackStore) RollbackPendingDefinitionPack(_ context.Context, source, revision, reason string) error {
	s.rolled = append(s.rolled, source+":"+revision+":"+reason)
	return nil
}

func (s *fakeDefinitionPackStore) QuarantineDefinitionPack(_ context.Context, source, revision, reason string) error {
	s.rolled = append(s.rolled, source+":"+revision+":"+reason)
	return nil
}

type runtimeTestProvider struct{ source string }

func (p runtimeTestProvider) Source() string { return p.source }
func (p runtimeTestProvider) Documents() ([]cardigann.SourceDocument, error) {
	return []cardigann.SourceDocument{{Path: "definitions/first.yml", Data: []byte("id: first\nname: First\nlinks: [https://tracker.example]\ncaps: {modes: {search: [q]}}\nsearch: {paths: [{path: /search}], rows: {selector: article}, fields: {title: {selector: h2}, download: {selector: a, attribute: href}}}\n"), ApprovedOrigins: []string{"https://tracker.example"}}}, nil
}

func TestLoadPersistedDefinitionPacksPromotesOnlyExactPendingRegistry(t *testing.T) {
	pending := core.DefinitionPackRevision{Source: "community", Revision: "r2", ArchiveDigest: "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ArchiveRelPath: "archives/sha256/a.zip"}
	st := &fakeDefinitionPackStore{pending: []core.DefinitionPackRevision{pending}, entries: map[string][]core.DefinitionPackEntry{"community:r2": {{Source: "community", Revision: "r2", DefinitionRef: "community:first", Path: "definitions/first.yml", Digest: "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", State: core.DefinitionPackEntryRunnableUnverified, ApprovedOrigins: []string{"https://tracker.example"}}}}}
	registry, err := cardigann.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	err = loadPersistedDefinitionPacks(context.Background(), st, "/unused", registry, func(string, core.DefinitionPackRevision, []core.DefinitionPackEntry) (cardigann.Provider, error) {
		return runtimeTestProvider{source: "community"}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.promoted) != 1 || st.promoted[0] != "community:r2" {
		t.Fatalf("promoted = %v", st.promoted)
	}
	if _, ok := registry.Get("community:first"); !ok {
		t.Fatal("promoted exact provider is missing from registry")
	}
}

func TestLoadPersistedDefinitionPacksRollsBackInvalidPendingRevision(t *testing.T) {
	pending := core.DefinitionPackRevision{Source: "community", Revision: "r2"}
	st := &fakeDefinitionPackStore{pending: []core.DefinitionPackRevision{pending}, entries: map[string][]core.DefinitionPackEntry{"community:r2": {}}}
	registry, err := cardigann.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	err = loadPersistedDefinitionPacks(context.Background(), st, "/unused", registry, func(string, core.DefinitionPackRevision, []core.DefinitionPackEntry) (cardigann.Provider, error) {
		return nil, errors.New("digest mismatch")
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.rolled) != 1 || st.rolled[0] != "community:r2:pack.registry.resolution_failed" {
		t.Fatalf("rolled = %v", st.rolled)
	}
}

func TestLoadPersistedDefinitionPacksDoesNotCommitWhenPromotionFails(t *testing.T) {
	pending := core.DefinitionPackRevision{Source: "community", Revision: "r2"}
	st := &fakeDefinitionPackStore{pending: []core.DefinitionPackRevision{pending}, entries: map[string][]core.DefinitionPackEntry{"community:r2": {}}, promoteErr: errors.New("db unavailable")}
	registry, err := cardigann.LoadBuiltins()
	if err != nil {
		t.Fatal(err)
	}
	err = loadPersistedDefinitionPacks(context.Background(), st, "/unused", registry, func(string, core.DefinitionPackRevision, []core.DefinitionPackEntry) (cardigann.Provider, error) {
		return runtimeTestProvider{source: "community"}, nil
	})
	if err == nil {
		t.Fatal("startup promotion error = nil")
	}
	if _, ok := registry.Get("community:first"); ok {
		t.Fatal("failed promotion leaked definition into registry")
	}
}
