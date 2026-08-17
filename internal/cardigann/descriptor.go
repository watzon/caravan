package cardigann

import (
	"fmt"
	"sort"
	"strings"
)

// SourceKind tells catalog consumers where inert or executable definition bytes
// came from. It intentionally does not imply that a definition is executable.
type SourceKind string

const (
	SourceKindBuiltin SourceKind = "builtin"
	SourceKindUser    SourceKind = "user"
	SourceKindManaged SourceKind = "managed"
	SourceKindPack    SourceKind = "pack"
	SourceKindUnknown SourceKind = "unknown"
)

// DefinitionState is a fail-closed lifecycle state for display and diagnostics.
// Only runnable-unverified definitions can be handed to the executable registry.
type DefinitionState string

const (
	DefinitionStateMetadataOnly       DefinitionState = "metadata-only"
	DefinitionStateSourceNotInstalled DefinitionState = "source-not-installed"
	DefinitionStateUnsupported        DefinitionState = "unsupported"
	DefinitionStateQuarantined        DefinitionState = "quarantined"
	DefinitionStateRunnableUnverified DefinitionState = "runnable-unverified"
)

// ProviderDescriptor identifies a source independently of each definition.
type ProviderDescriptor struct {
	Source         string
	Kind           SourceKind
	Revision       string
	License        string
	Provenance     string
	SignerKeyID    string
	ManifestDigest string
}

// DefinitionDescriptor is display-only classification metadata. It is never a
// capability to execute a definition; callers must use Registry.Get instead.
type DefinitionDescriptor struct {
	Ref             DefinitionRef
	MetadataID      string
	Path            string
	Revision        string
	Digest          string
	State           DefinitionState
	Unsupported     []CapabilityCode
	BlockedReason   string
	ApprovedOrigins []string
	Provider        ProviderDescriptor
}

// DescriptorProvider supplies source provenance in addition to inert bytes.
type DescriptorProvider interface {
	Provider
	Descriptor() ProviderDescriptor
}

func providerDescriptor(provider Provider) ProviderDescriptor {
	if described, ok := provider.(DescriptorProvider); ok {
		descriptor := described.Descriptor()
		descriptor.Source = strings.TrimSpace(descriptor.Source)
		if descriptor.Source == "" {
			descriptor.Source = provider.Source()
		}
		if descriptor.Kind == "" {
			descriptor.Kind = SourceKindUnknown
		}
		return descriptor
	}
	kind := SourceKindUnknown
	switch provider.Source() {
	case BuiltinSource:
		kind = SourceKindBuiltin
	case "user":
		kind = SourceKindUser
	}
	return ProviderDescriptor{Source: provider.Source(), Kind: kind}
}

// DescribeProvider classifies a source without registering any executable
// definition. Bad documents are kept as quarantined descriptors where their
// identity can be recovered; valid unsupported documents remain inert.
func DescribeProvider(provider Provider) ([]DefinitionDescriptor, error) {
	if provider == nil {
		return nil, fmt.Errorf("definition provider is nil")
	}
	documents, err := provider.Documents()
	if err != nil {
		return nil, fmt.Errorf("load definition source %q: %w", provider.Source(), err)
	}
	providerInfo := providerDescriptor(provider)
	descriptors := make([]DefinitionDescriptor, 0, len(documents))
	for _, document := range documents {
		manifest, parseErr := ParseManifest(provider.Source(), document.Data)
		if parseErr != nil {
			descriptors = append(descriptors, DefinitionDescriptor{
				Path:          document.Path,
				State:         DefinitionStateQuarantined,
				BlockedReason: parseErr.Error(),
				Provider:      providerInfo,
			})
			continue
		}
		state := DefinitionStateUnsupported
		blockedReason := ""
		if manifest.Runnable {
			state = DefinitionStateRunnableUnverified
		} else if containsCapability(manifest.Unsupported, "syntax.invalid") || containsCapability(manifest.Unsupported, "compiler.invalid") {
			state = DefinitionStateQuarantined
			blockedReason = "definition failed strict syntax or compiler validation"
		}
		descriptors = append(descriptors, DefinitionDescriptor{
			Ref:             manifest.Ref,
			MetadataID:      manifest.Ref.ID,
			Path:            document.Path,
			Revision:        firstNonEmpty(document.Revision, manifest.Revision),
			Digest:          firstNonEmpty(document.Digest, manifest.Digest),
			State:           state,
			Unsupported:     append([]CapabilityCode(nil), manifest.Unsupported...),
			BlockedReason:   blockedReason,
			ApprovedOrigins: append([]string(nil), document.ApprovedOrigins...),
			Provider:        providerInfo,
		})
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].Path < descriptors[j].Path })
	return descriptors, nil
}

func containsCapability(capabilities []CapabilityCode, target CapabilityCode) bool {
	for _, capability := range capabilities {
		if capability == target {
			return true
		}
	}
	return false
}
