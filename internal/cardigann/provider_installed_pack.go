package cardigann

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// PackRuntimeEntry is the stored immutable pin required before a signed pack
// member may enter Registry. Unsupported entries are retained for diagnostics
// but never become provider documents.
type PackRuntimeEntry struct {
	DefinitionRef   string
	Path            string
	Digest          string
	State           DefinitionState
	ApprovedOrigins []string
}

// InstalledPackProvider is a verified snapshot of one persisted pack revision.
// It implements Provider only after the archive and every persisted runnable
// entry have been rechecked against their exact stored identities.
type InstalledPackProvider struct {
	source    string
	documents []SourceDocument
}

func (p *InstalledPackProvider) Source() string {
	if p == nil {
		return ""
	}
	return p.source
}

func (p *InstalledPackProvider) Documents() ([]SourceDocument, error) {
	if p == nil {
		return nil, fmt.Errorf("installed pack provider is nil")
	}
	documents := make([]SourceDocument, len(p.documents))
	for i, document := range p.documents {
		documents[i] = SourceDocument{
			Path: document.Path, Data: append([]byte(nil), document.Data...),
			ApprovedOrigins: append([]string(nil), document.ApprovedOrigins...),
			Revision:        document.Revision, Digest: document.Digest,
		}
	}
	return documents, nil
}

// VerifyInstalledPackArchive re-runs the complete signed archive and immutable
// receipt verification. It deliberately permits a valid all-unsupported pack:
// that archive is inert data, not an activation candidate.
func VerifyInstalledPackArchive(archivePath, currentVersion string, revision core.DefinitionPackRevision, entries []core.DefinitionPackEntry) error {
	_, err := verifiedInstalledPackEntries(archivePath, currentVersion, revision, entries)
	return err
}

// OpenVerifiedInstalledPackProvider returns a safe Provider even for a verified
// inert pack. Activation policy still rejects revisions with runnable_count=0.
func OpenVerifiedInstalledPackProvider(archivePath, currentVersion string, revision core.DefinitionPackRevision, entries []core.DefinitionPackEntry) (*InstalledPackProvider, error) {
	runtimeEntries, err := verifiedInstalledPackEntries(archivePath, currentVersion, revision, entries)
	if err != nil {
		return nil, err
	}
	if revision.RunnableCount == 0 {
		return &InstalledPackProvider{source: revision.Source}, nil
	}
	return OpenInstalledPackProvider(archivePath, revision.ArchiveDigest, revision.Source, revision.Revision, runtimeEntries)
}

func verifiedInstalledPackEntries(archivePath, currentVersion string, revision core.DefinitionPackRevision, entries []core.DefinitionPackEntry) ([]PackRuntimeEntry, error) {
	if len(revision.SignerPublicKey) != 32 {
		return nil, fmt.Errorf("installed pack has no exact persisted Ed25519 public key")
	}
	candidate, err := OpenSignedPackArchive(archivePath, currentVersion, StaticPackTrustStore{revision.SignerKeyID: revision.SignerPublicKey})
	if err != nil {
		return nil, fmt.Errorf("verify signed installed pack: %w", err)
	}
	provider := candidate.Descriptor()
	metadata := candidate.receipt
	if provider.Source != revision.Source || provider.Revision != revision.Revision || provider.ManifestDigest != revision.ManifestDigest ||
		provider.License != revision.LicenseExpression || provider.Provenance != revision.Provenance || provider.SignerKeyID != revision.SignerKeyID ||
		metadata.LicensePath != revision.LicensePath || metadata.LicenseDigest != revision.LicenseDigest || metadata.NoticePath != revision.NoticePath || metadata.NoticeDigest != revision.NoticeDigest ||
		metadata.MinimumCaravanVersion != revision.MinimumCaravanVersion || metadata.SignerKeyFingerprint != revision.SignerKeyFingerprint || !bytes.Equal(metadata.SignerPublicKey, revision.SignerPublicKey) {
		return nil, fmt.Errorf("signed installed pack does not match persisted immutable receipt")
	}
	definitions := candidate.Definitions()
	if len(definitions) != len(entries) {
		return nil, fmt.Errorf("signed installed pack definition count does not match persisted receipt")
	}
	runtimeEntries := make([]PackRuntimeEntry, len(entries))
	persisted := make(map[string]core.DefinitionPackEntry, len(entries))
	for _, entry := range entries {
		if entry.Source != revision.Source || entry.Revision != revision.Revision {
			return nil, fmt.Errorf("persisted installed pack entry belongs to another revision")
		}
		persisted[entry.DefinitionRef] = entry
	}
	for i, definition := range definitions {
		entry, ok := persisted[definition.Ref.String()]
		expectedState := core.DefinitionPackEntryUnsupported
		if definition.State == DefinitionStateRunnableUnverified {
			expectedState = core.DefinitionPackEntryRunnableUnverified
		}
		if !ok || entry.MetadataID != definition.MetadataID || entry.Path != definition.Path || entry.Digest != definition.Digest || entry.State != expectedState || !slices.Equal(entry.ApprovedOrigins, definition.ApprovedOrigins) {
			return nil, fmt.Errorf("signed installed pack definition %q does not match persisted receipt", definition.Ref)
		}
		runtimeEntries[i] = PackRuntimeEntry{DefinitionRef: entry.DefinitionRef, Path: entry.Path, Digest: entry.Digest, State: DefinitionState(entry.State), ApprovedOrigins: append([]string(nil), entry.ApprovedOrigins...)}
	}
	return runtimeEntries, nil
}

// OpenInstalledPackProvider verifies an archive already published under the
// application's pack root against the exact persisted revision and entries.
// It does not trust names, discovery, or a newer revision with the same id.
func OpenInstalledPackProvider(archivePath, archiveDigest, source, revision string, entries []PackRuntimeEntry) (*InstalledPackProvider, error) {
	if strings.TrimSpace(archivePath) == "" || strings.TrimSpace(source) == "" || strings.TrimSpace(revision) == "" || len(entries) == 0 {
		return nil, fmt.Errorf("installed pack requires archive, source, revision, and entries")
	}
	if _, err := ParseDefinitionRef(source + ":source-check"); err != nil || source == BuiltinSource || source == "user" {
		return nil, fmt.Errorf("installed pack source is invalid")
	}
	archive, err := openPackArchiveNoFollow(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open installed pack archive: %w", err)
	}
	defer archive.Close()
	opened, err := archive.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() > MaxPackArchiveBytes {
		return nil, fmt.Errorf("installed pack archive must be a bounded regular file")
	}
	pathInfo, err := os.Lstat(archivePath)
	if err != nil || !pathInfo.Mode().IsRegular() || !os.SameFile(opened, pathInfo) {
		return nil, fmt.Errorf("installed pack archive path changed while opening")
	}
	bytes, err := io.ReadAll(io.LimitReader(archive, MaxPackArchiveBytes+1))
	if err != nil || int64(len(bytes)) > MaxPackArchiveBytes {
		return nil, fmt.Errorf("read installed pack archive: %w", err)
	}
	current, err := os.Lstat(archivePath)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		return nil, fmt.Errorf("installed pack archive path changed during read")
	}
	actualArchive := sha256.Sum256(bytes)
	if "sha256:"+fmt.Sprintf("%x", actualArchive) != archiveDigest {
		return nil, fmt.Errorf("installed pack archive digest does not match persisted pin")
	}
	reader, err := zip.NewReader(strings.NewReader(string(bytes)), int64(len(bytes)))
	if err != nil {
		return nil, fmt.Errorf("read installed pack ZIP: %w", err)
	}
	files, err := indexPackArchive(reader.File)
	if err != nil {
		return nil, err
	}
	documents := make([]SourceDocument, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		if entry.State != DefinitionStateRunnableUnverified {
			continue
		}
		if _, exists := seen[entry.DefinitionRef]; exists {
			return nil, fmt.Errorf("installed pack repeats persisted definition %q", entry.DefinitionRef)
		}
		seen[entry.DefinitionRef] = struct{}{}
		ref, err := ParseDefinitionRef(entry.DefinitionRef)
		if err != nil || ref.Source != source || !strings.HasPrefix(entry.Path, "definitions/") {
			return nil, fmt.Errorf("installed pack entry identity is invalid")
		}
		file, ok := files[entry.Path]
		if !ok {
			return nil, fmt.Errorf("installed pack is missing persisted definition %q", entry.DefinitionRef)
		}
		data, err := readPackArchiveFile(file, MaxDefinitionFileBytes)
		if err != nil {
			return nil, fmt.Errorf("read installed pack definition %q: %w", entry.DefinitionRef, err)
		}
		actual := sha256.Sum256(data)
		if "sha256:"+fmt.Sprintf("%x", actual) != entry.Digest {
			return nil, fmt.Errorf("installed pack definition %q digest does not match persisted pin", entry.DefinitionRef)
		}
		manifest, err := ParseManifest(source, data)
		if err != nil || !manifest.Runnable || manifest.Ref != ref {
			return nil, fmt.Errorf("installed pack definition %q does not match persisted runnable revision", entry.DefinitionRef)
		}
		origins, err := normalizedOrigins(entry.ApprovedOrigins)
		if err != nil || len(origins) == 0 {
			return nil, fmt.Errorf("installed pack definition %q has invalid persisted origins", entry.DefinitionRef)
		}
		declaredOrigins, err := manifestOrigins(data)
		if err != nil {
			return nil, fmt.Errorf("read installed pack definition %q origins: %w", entry.DefinitionRef, err)
		}
		for _, origin := range declaredOrigins {
			if !containsString(origins, origin) {
				return nil, fmt.Errorf("installed pack definition %q has an origin outside its persisted allowlist", entry.DefinitionRef)
			}
		}
		documents = append(documents, SourceDocument{
			Path: entry.Path, Data: data, ApprovedOrigins: origins,
			Revision: revision, Digest: entry.Digest,
		})
	}
	if len(documents) == 0 {
		return nil, fmt.Errorf("installed pack has no runnable persisted definitions")
	}
	sort.Slice(documents, func(i, j int) bool { return documents[i].Path < documents[j].Path })
	return &InstalledPackProvider{source: source, documents: documents}, nil
}

var _ Provider = (*InstalledPackProvider)(nil)
