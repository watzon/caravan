package cardigann

import (
	"embed"
	"sort"
	"strings"
)

//go:embed definitions/*.yml
var definitionFiles embed.FS

// Registry is the set of definitions this Caravan build can execute. Only
// definitions that parse and pass the engine's compatibility checks belong in
// the add-indexer catalog.
type Registry struct {
	definitions map[DefinitionRef]*Definition
	seen        map[DefinitionRef]struct{}
	sources     map[string]struct{}
}

func newRegistry() *Registry {
	return &Registry{
		definitions: make(map[DefinitionRef]*Definition),
		seen:        make(map[DefinitionRef]struct{}),
		sources:     make(map[string]struct{}),
	}
}

// LoadBuiltins compiles the definitions bundled with Caravan.
func LoadBuiltins() (*Registry, error) {
	registry, _, err := LoadProviders(BuiltinProvider{})
	return registry, err
}

// Get returns a compiled definition by stable id.
func (r *Registry) Get(id string) (*Definition, bool) {
	if r == nil {
		return nil, false
	}
	ref, err := ParseDefinitionRef(strings.TrimSpace(id))
	if err != nil {
		return nil, false
	}
	definition, ok := r.definitions[ref]
	return definition, ok
}

// GetExact returns a definition only when every persisted immutable source pin
// matches the compiled bytes. It intentionally has no fallback.
func (r *Registry) GetExact(id, source, revision, digest string) (*Definition, bool) {
	definition, ok := r.Get(id)
	if !ok || source == "" || revision == "" || digest == "" {
		return nil, false
	}
	ref, err := ParseDefinitionRef(id)
	if err != nil || ref.Source != source || definition.sourceRevision != revision || definition.sourceDigest != digest {
		return nil, false
	}
	return definition, true
}

// GetExactPack is retained for signed-pack lifecycle callers. Exact runtime
// identity itself is source-agnostic and shared with managed definitions.
func (r *Registry) GetExactPack(id, source, revision, digest string) (*Definition, bool) {
	return r.GetExact(id, source, revision, digest)
}

// All returns every compiled definition sorted by display name.
func (r *Registry) All() []*Definition {
	if r == nil {
		return []*Definition{}
	}
	out := make([]*Definition, 0, len(r.definitions))
	for _, definition := range r.definitions {
		out = append(out, definition)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// SettingNames returns the declared write-only setting keys for one executable
// definition. Unsupported manifests are absent by construction.
func (r *Registry) SettingNames(id string) ([]string, bool) {
	definition, ok := r.Get(id)
	if !ok {
		return nil, false
	}
	names := make([]string, 0, len(definition.Settings))
	for _, setting := range definition.SettingSchemas() {
		if setting.Editable && setting.Name != "" {
			names = append(names, setting.Name)
		}
	}
	sort.Strings(names)
	return names, true
}

// SettingSchemas returns the complete non-secret Add Indexer field contract.
func (r *Registry) SettingSchemas(id string) ([]SettingSchema, bool) {
	definition, ok := r.Get(id)
	if !ok {
		return nil, false
	}
	return definition.SettingSchemas(), true
}

// BaseURLs returns the request-authorizing links from the compiled definition.
func (r *Registry) BaseURLs(id string) ([]string, bool) {
	definition, ok := r.Get(id)
	if !ok {
		return nil, false
	}
	return append([]string(nil), definition.Links...), true
}

// SettingNamesExact returns settings only for the complete immutable pack pin.
// It intentionally has no short-ID or newer-revision fallback.
func (r *Registry) SettingNamesExact(id, source, revision, digest string) ([]string, bool) {
	definition, ok := r.GetExact(id, source, revision, digest)
	if !ok {
		return nil, false
	}
	names := make([]string, 0, len(definition.Settings))
	for _, setting := range definition.SettingSchemas() {
		if setting.Editable && setting.Name != "" {
			names = append(names, setting.Name)
		}
	}
	sort.Strings(names)
	return names, true
}

// SettingSchemasExact is SettingSchemas with the complete immutable identity.
func (r *Registry) SettingSchemasExact(id, source, revision, digest string) ([]SettingSchema, bool) {
	definition, ok := r.GetExact(id, source, revision, digest)
	if !ok {
		return nil, false
	}
	return definition.SettingSchemas(), true
}

// BaseURLsExact is BaseURLs with the complete immutable definition identity.
func (r *Registry) BaseURLsExact(id, source, revision, digest string) ([]string, bool) {
	definition, ok := r.GetExact(id, source, revision, digest)
	if !ok {
		return nil, false
	}
	return append([]string(nil), definition.Links...), true
}
