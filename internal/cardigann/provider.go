package cardigann

import (
	"errors"
	"fmt"
)

// SourceDocument is raw, inert definition input supplied by one named source.
// Path is diagnostic-only and is never interpreted as an include or template.
type SourceDocument struct {
	Path            string
	Data            []byte
	ApprovedOrigins []string
	// Revision and Digest are optional immutable runtime pins supplied only by
	// an installed signed-pack provider; loose and built-in providers leave them empty.
	Revision string
	Digest   string
}

// Provider supplies definition bytes for one explicit source. Providers must
// return a deterministic document order; LoadProviders preserves that order for
// diagnostics while registry lookups remain namespaced by Source().
type Provider interface {
	Source() string
	Documents() ([]SourceDocument, error)
}

// PreparedProvider is an isolated, fully classified provider addition. Commit
// is deliberately a no-fail map swap for serialized startup callers: until it
// is committed, the target registry has not observed the provider at all.
type PreparedProvider struct {
	target    *Registry
	prepared  *Registry
	committed bool
}

// PrepareProvider validates one provider against a clone of this registry.
// Unlike LoadProvider it is all-or-nothing: a Documents, manifest, duplicate,
// compiler, or immutable-digest problem leaves target maps untouched. Loose
// user definitions intentionally continue to use LoadProvider's best-effort
// sibling behavior.
func (r *Registry) PrepareProvider(provider Provider) (*PreparedProvider, []Manifest, []error) {
	if r == nil {
		return nil, nil, []error{fmt.Errorf("definition registry is nil")}
	}
	clone := r.clone()
	manifests, problems := clone.LoadProvider(provider)
	if len(problems) != 0 {
		return nil, manifests, problems
	}
	return &PreparedProvider{target: r, prepared: clone}, manifests, nil
}

// Commit publishes the prepared maps exactly once. It has no validation or I/O
// path by design; callers must complete durable lifecycle transitions before
// publishing a pending pack to the process registry.
func (p *PreparedProvider) Commit() {
	if p == nil || p.committed || p.target == nil || p.prepared == nil {
		return
	}
	p.target.definitions = p.prepared.definitions
	p.target.seen = p.prepared.seen
	p.target.sources = p.prepared.sources
	p.committed = true
}

func (r *Registry) clone() *Registry {
	clone := newRegistry()
	for ref, definition := range r.definitions {
		clone.definitions[ref] = definition
	}
	for ref := range r.seen {
		clone.seen[ref] = struct{}{}
	}
	for source := range r.sources {
		clone.sources[source] = struct{}{}
	}
	return clone
}

// LoadProviders classifies all supplied documents and adds only compiler-safe
// definitions to the executable registry. Unsupported manifests remain inert
// metadata and are intentionally absent from the registry.
func LoadProviders(providers ...Provider) (*Registry, []Manifest, error) {
	registry := newRegistry()
	sources := make(map[string]struct{}, len(providers))
	for _, provider := range providers {
		if provider == nil {
			return registry, nil, fmt.Errorf("definition provider is nil")
		}
		source := provider.Source()
		if _, err := ParseDefinitionRef(source + ":source-check"); err != nil {
			return registry, nil, fmt.Errorf("definition provider source: %w", err)
		}
		if _, exists := sources[source]; exists {
			return registry, nil, fmt.Errorf("definition provider source %q is already registered", source)
		}
		sources[source] = struct{}{}
	}
	manifests := make([]Manifest, 0)
	for _, provider := range providers {
		loaded, problems := registry.LoadProvider(provider)
		manifests = append(manifests, loaded...)
		if len(problems) > 0 {
			return registry, manifests, errors.Join(problems...)
		}
	}
	return registry, manifests, nil
}

// LoadProvider classifies one source into an existing registry. Invalid files,
// duplicate references and compile failures are returned individually while
// valid siblings remain loaded. The first seen reference always wins.
func (r *Registry) LoadProvider(provider Provider) ([]Manifest, []error) {
	if r == nil {
		return nil, []error{fmt.Errorf("definition registry is nil")}
	}
	if provider == nil {
		return nil, []error{fmt.Errorf("definition provider is nil")}
	}
	if r.definitions == nil {
		r.definitions = make(map[DefinitionRef]*Definition)
	}
	if r.seen == nil {
		r.seen = make(map[DefinitionRef]struct{})
	}
	if r.sources == nil {
		r.sources = make(map[string]struct{})
	}
	source := provider.Source()
	if _, err := ParseDefinitionRef(source + ":source-check"); err != nil {
		return nil, []error{fmt.Errorf("definition provider source: %w", err)}
	}
	if _, exists := r.sources[source]; exists {
		return nil, []error{fmt.Errorf("definition provider source %q is already registered", source)}
	}
	r.sources[source] = struct{}{}
	documents, err := provider.Documents()
	if err != nil {
		return nil, []error{fmt.Errorf("load definition source %q: %w", source, err)}
	}
	manifests := make([]Manifest, 0, len(documents))
	problems := make([]error, 0)
	for _, document := range documents {
		manifest, err := ParseManifest(source, document.Data)
		if err != nil {
			problems = append(problems, fmt.Errorf("classify %s from %q: %w", document.Path, source, err))
			continue
		}
		manifests = append(manifests, manifest)
		if _, exists := r.seen[manifest.Ref]; exists {
			problems = append(problems, fmt.Errorf("duplicate definition reference %q", manifest.Ref))
			continue
		}
		r.seen[manifest.Ref] = struct{}{}
		if !manifest.Runnable {
			continue
		}
		definition, err := ParseDefinition(document.Data)
		if err != nil {
			problems = append(problems, fmt.Errorf("compile %s from %q: %w", document.Path, source, err))
			continue
		}
		if document.Digest != "" && document.Digest != manifest.Digest {
			problems = append(problems, fmt.Errorf("compile %s from %q: immutable digest does not match bytes", document.Path, source))
			continue
		}
		definition.approvedOrigins = append([]string(nil), document.ApprovedOrigins...)
		definition.sourceRevision = document.Revision
		definition.sourceDigest = document.Digest
		r.definitions[manifest.Ref] = definition
	}
	return manifests, problems
}
