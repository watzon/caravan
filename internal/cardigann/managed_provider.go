package cardigann

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	ManagedSource                   = "managed"
	maxManagedListingBytes          = 8 << 20
	maxManagedArchiveBytes          = 8 << 20
	maxManagedArchiveEntries        = 1024
	maxManagedDefinitionBytes       = 4 << 20
	maxManagedUncompressedTotal     = 16 << 20
	managedDefinitionProvenance     = "https://indexers.prowlarr.com/master/11"
	managedDefinitionLicenseSummary = "Source repository does not publish a standalone definition license"
)

var managedNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

type managedListingEntry struct {
	ID   string `json:"id"`
	File string `json:"file"`
	SHA  string `json:"sha"`
}

// ManagedSnapshot is an authenticated-by-content snapshot from Caravan's
// automatically managed definition source. The upstream listing's Git blob SHA
// authenticates each exact YAML byte sequence; Revision pins the complete set.
type ManagedSnapshot struct {
	Revision  string
	documents []SourceDocument
	listing   []byte
	archive   []byte
}

func (s *ManagedSnapshot) Provider() DescriptorProvider {
	documents := cloneSourceDocuments(s.documents)
	return &managedProvider{revision: s.Revision, documents: documents}
}

type managedProvider struct {
	revision  string
	documents []SourceDocument
}

func (p *managedProvider) Source() string { return ManagedSource }

func (p *managedProvider) Descriptor() ProviderDescriptor {
	return ProviderDescriptor{
		Source:     ManagedSource,
		Kind:       SourceKindManaged,
		Revision:   p.revision,
		License:    managedDefinitionLicenseSummary,
		Provenance: managedDefinitionProvenance,
	}
}

func (p *managedProvider) Documents() ([]SourceDocument, error) {
	return cloneSourceDocuments(p.documents), nil
}

// InspectManagedSnapshot validates a bounded listing and package archive
// without publishing executable definitions or writing files.
func InspectManagedSnapshot(listingJSON, archiveBytes []byte) (*ManagedSnapshot, error) {
	if len(listingJSON) == 0 || len(listingJSON) > maxManagedListingBytes {
		return nil, fmt.Errorf("managed definition listing must contain 1-%d bytes", maxManagedListingBytes)
	}
	if len(archiveBytes) == 0 || len(archiveBytes) > maxManagedArchiveBytes {
		return nil, fmt.Errorf("managed definition archive must contain 1-%d bytes", maxManagedArchiveBytes)
	}

	var listing []managedListingEntry
	decoder := json.NewDecoder(bytes.NewReader(listingJSON))
	if err := decoder.Decode(&listing); err != nil {
		return nil, fmt.Errorf("decode managed definition listing: %w", err)
	}
	if len(listing) == 0 || len(listing) > maxManagedArchiveEntries {
		return nil, fmt.Errorf("managed definition listing must contain 1-%d entries", maxManagedArchiveEntries)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("managed definition listing contains trailing JSON")
	}

	byFile := make(map[string]managedListingEntry, len(listing))
	seenIDs := make(map[string]struct{}, len(listing))
	for _, entry := range listing {
		entry.ID = strings.TrimSpace(entry.ID)
		entry.File = strings.TrimSpace(entry.File)
		entry.SHA = strings.ToLower(strings.TrimSpace(entry.SHA))
		if !managedNamePattern.MatchString(entry.ID) || !managedNamePattern.MatchString(entry.File) || entry.ID == "." || entry.ID == ".." || entry.File == "." || entry.File == ".." {
			return nil, fmt.Errorf("managed definition listing contains an invalid id or file")
		}
		if len(entry.SHA) != sha1.Size*2 {
			return nil, fmt.Errorf("managed definition %q has an invalid Git blob SHA", entry.ID)
		}
		if _, err := hex.DecodeString(entry.SHA); err != nil {
			return nil, fmt.Errorf("managed definition %q has an invalid Git blob SHA", entry.ID)
		}
		foldedID := strings.ToLower(entry.ID)
		if _, exists := seenIDs[foldedID]; exists {
			return nil, fmt.Errorf("managed definition listing contains duplicate id %q", entry.ID)
		}
		seenIDs[foldedID] = struct{}{}
		pathName := entry.File + ".yml"
		if _, exists := byFile[pathName]; exists {
			return nil, fmt.Errorf("managed definition listing contains duplicate file %q", pathName)
		}
		byFile[pathName] = entry
	}

	archive, err := zip.NewReader(bytes.NewReader(archiveBytes), int64(len(archiveBytes)))
	if err != nil {
		return nil, fmt.Errorf("open managed definition archive: %w", err)
	}
	if len(archive.File) == 0 || len(archive.File) > maxManagedArchiveEntries {
		return nil, fmt.Errorf("managed definition archive must contain 1-%d entries", maxManagedArchiveEntries)
	}

	documents := make([]SourceDocument, 0, len(archive.File))
	seenFiles := make(map[string]struct{}, len(archive.File))
	var total uint64
	for _, file := range archive.File {
		name := file.Name
		if path.Clean(name) != name || strings.ContainsAny(name, `/\\`) || !strings.HasSuffix(name, ".yml") || !managedNamePattern.MatchString(strings.TrimSuffix(name, ".yml")) {
			return nil, fmt.Errorf("managed definition archive contains unsafe path %q", name)
		}
		if file.FileInfo().Mode().Type() != 0 {
			return nil, fmt.Errorf("managed definition archive entry %q is not a regular file", name)
		}
		if _, exists := seenFiles[name]; exists {
			return nil, fmt.Errorf("managed definition archive contains duplicate entry %q", name)
		}
		seenFiles[name] = struct{}{}
		entry, expected := byFile[name]
		if !expected {
			return nil, fmt.Errorf("managed definition archive contains unexpected entry %q", name)
		}
		if file.UncompressedSize64 > maxManagedDefinitionBytes || total+file.UncompressedSize64 > maxManagedUncompressedTotal {
			return nil, fmt.Errorf("managed definition archive exceeds extraction limits")
		}
		total += file.UncompressedSize64
		reader, err := file.Open()
		if err != nil {
			return nil, fmt.Errorf("open managed definition %q: %w", name, err)
		}
		data, readErr := io.ReadAll(io.LimitReader(reader, maxManagedDefinitionBytes+1))
		closeErr := reader.Close()
		if readErr != nil {
			return nil, fmt.Errorf("read managed definition %q: %w", name, readErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("close managed definition %q: %w", name, closeErr)
		}
		if len(data) > maxManagedDefinitionBytes || uint64(len(data)) != file.UncompressedSize64 {
			return nil, fmt.Errorf("managed definition %q exceeds size or changed during extraction", name)
		}
		if managedGitBlobDigest(data) != entry.SHA {
			return nil, fmt.Errorf("managed definition %q does not match its listed Git blob SHA", name)
		}
		documents = append(documents, SourceDocument{Path: name, Data: data})
	}
	if len(seenFiles) != len(byFile) {
		return nil, fmt.Errorf("managed definition archive is missing %d listed entries", len(byFile)-len(seenFiles))
	}

	sort.Slice(documents, func(i, j int) bool { return documents[i].Path < documents[j].Path })
	revisionHash := sha256.New()
	for _, document := range documents {
		entry := byFile[document.Path]
		_, _ = fmt.Fprintf(revisionHash, "%s:%s\n", document.Path, entry.SHA)
	}
	revision := hex.EncodeToString(revisionHash.Sum(nil))
	for i := range documents {
		documents[i].Revision = revision
		manifest, manifestErr := ParseManifest(ManagedSource, documents[i].Data)
		if manifestErr != nil {
			continue
		}
		if !strings.EqualFold(manifest.Ref.ID, byFile[documents[i].Path].ID) {
			return nil, fmt.Errorf("managed definition %q does not match listing identifier", documents[i].Path)
		}
		documents[i].Digest = manifest.Digest
		if manifest.Runnable {
			definition, err := ParseDefinition(documents[i].Data)
			if err != nil {
				return nil, fmt.Errorf("compile runnable managed definition %q: %w", documents[i].Path, err)
			}
			approvedOrigins := make([]string, 0, len(definition.Links)+len(definition.Settings))
			for _, link := range definition.Links {
				parsed, parseErr := url.Parse(strings.TrimSpace(link))
				if parseErr != nil || parsed.Host == "" {
					return nil, fmt.Errorf("compile runnable managed definition %q: invalid approved origin", documents[i].Path)
				}
				approvedOrigins = append(approvedOrigins, requestOrigin(parsed))
			}
			// URL-typed setting defaults are request targets too (for
			// example YTS searches an API host declared as a setting).
			for _, setting := range definition.Settings {
				if origin, ok := urlSettingOrigin(setting.Name, settingValue(setting.Default)); ok {
					approvedOrigins = append(approvedOrigins, origin)
				}
			}
			documents[i].ApprovedOrigins = approvedOrigins
		}
	}
	return &ManagedSnapshot{
		Revision:  revision,
		documents: documents,
		listing:   append([]byte(nil), listingJSON...),
		archive:   append([]byte(nil), archiveBytes...),
	}, nil
}

func managedGitBlobDigest(data []byte) string {
	hash := sha1.New()
	_, _ = fmt.Fprintf(hash, "blob %d\x00", len(data))
	_, _ = hash.Write(data)
	return hex.EncodeToString(hash.Sum(nil))
}

func cloneSourceDocuments(documents []SourceDocument) []SourceDocument {
	out := make([]SourceDocument, len(documents))
	for i, document := range documents {
		out[i] = SourceDocument{
			Path: document.Path, Data: append([]byte(nil), document.Data...),
			ApprovedOrigins: append([]string(nil), document.ApprovedOrigins...),
			Revision:        document.Revision, Digest: document.Digest,
		}
	}
	return out
}
