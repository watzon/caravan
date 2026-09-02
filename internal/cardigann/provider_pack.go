package cardigann

import (
	"archive/zip"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/jsonpolicy"
	"gopkg.in/yaml.v3"
)

const (
	CurrentPackFormatVersion                = 1
	CurrentCardigannPackSchemaVersion       = 1
	MaxPackDefinitionFiles                  = 4096
	MaxPackArchiveBytes               int64 = 80 << 20
	MaxPackDefinitionAggregateBytes   int64 = 64 << 20
	maxPackManifestBytes              int64 = 4 << 20
	maxPackLicenseBytes               int64 = 1 << 20
	maxPackProvenanceLength                 = 4096
	maxPackRevisionLength                   = 256
	maxPackSignerKeyIDLength                = 128
)

// PackTrustStore resolves an explicitly trusted signer. A pack can never carry
// the public key that makes its own signature trusted.
type PackTrustStore interface {
	LookupPackSigner(keyID string) (ed25519.PublicKey, bool)
}

// StaticPackTrustStore is useful for an owner-managed or application-managed
// immutable keyring. Returned keys are copied before verification.
type StaticPackTrustStore map[string]ed25519.PublicKey

func (s StaticPackTrustStore) LookupPackSigner(keyID string) (ed25519.PublicKey, bool) {
	key, ok := s[keyID]
	if !ok || len(key) != ed25519.PublicKeySize {
		return nil, false
	}
	return append(ed25519.PublicKey(nil), key...), true
}

type signedPackManifest struct {
	FormatVersion          int                    `json:"format_version"`
	CardigannSchemaVersion int                    `json:"cardigann_schema_version"`
	Source                 string                 `json:"source"`
	Revision               string                 `json:"revision"`
	SPDXLicenseExpression  string                 `json:"spdx_license_expression"`
	Provenance             string                 `json:"provenance"`
	SignerKeyID            string                 `json:"signer_key_id"`
	MinimumCaravanVersion  string                 `json:"minimum_caravan_version"`
	TotalFiles             int                    `json:"total_files"`
	TotalUncompressedBytes int64                  `json:"total_uncompressed_bytes"`
	License                signedPackFile         `json:"license"`
	Notice                 *signedPackFile        `json:"notice,omitempty"`
	Definitions            []signedPackDefinition `json:"definitions"`
}

type signedPackFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
}

type signedPackDefinition struct {
	ID              string   `json:"id"`
	MetadataID      string   `json:"metadata_id"`
	Path            string   `json:"path"`
	SHA256          string   `json:"sha256"`
	ApprovedOrigins []string `json:"approved_origins"`
}

// PackCandidate is a verified, immutable classification result. It deliberately
// does not implement Provider: installation, persistence, activation and
// executable publication require revision pins and last-known-good state that
// are outside this tranche.
type PackCandidate struct {
	descriptor  ProviderDescriptor
	definitions []DefinitionDescriptor
	acceptance  PackLicenseAcceptanceRequirements
	receipt     packReceiptMetadata
	license     []byte
	notice      []byte
}

type PackLegalMaterial struct {
	License []byte
	Notice  []byte
}

// LegalMaterial returns the exact, bounded license and optional notice bytes
// that were digest-checked while verifying this candidate.
func (c *PackCandidate) LegalMaterial() PackLegalMaterial {
	if c == nil {
		return PackLegalMaterial{}
	}
	return PackLegalMaterial{License: append([]byte(nil), c.license...), Notice: append([]byte(nil), c.notice...)}
}

type packReceiptMetadata struct {
	LicensePath           string
	LicenseDigest         string
	NoticePath            string
	NoticeDigest          string
	MinimumCaravanVersion string
	SignerKeyFingerprint  string
	SignerPublicKey       []byte
}

// PackLicenseAcceptanceRequirements identifies the exact licensed signed
// material an owner must review before an archive may be installed.
type PackLicenseAcceptanceRequirements struct {
	ManifestDigest       string
	LicenseDigest        string
	SignerKeyFingerprint string
}

func (c *PackCandidate) LicenseAcceptanceRequirements() PackLicenseAcceptanceRequirements {
	if c == nil {
		return PackLicenseAcceptanceRequirements{}
	}
	return c.acceptance
}

func (c *PackCandidate) Descriptor() ProviderDescriptor {
	if c == nil {
		return ProviderDescriptor{}
	}
	return c.descriptor
}

func (c *PackCandidate) Definitions() []DefinitionDescriptor {
	if c == nil {
		return []DefinitionDescriptor{}
	}
	out := make([]DefinitionDescriptor, len(c.definitions))
	for i, descriptor := range c.definitions {
		out[i] = cloneDefinitionDescriptor(descriptor)
	}
	return out
}

// OpenSignedPackArchive validates and classifies a complete ZIP pack without
// persisting, activating, or exposing its definitions to Registry.
func OpenSignedPackArchive(archivePath, currentCaravanVersion string, trust PackTrustStore) (*PackCandidate, error) {
	archivePath = strings.TrimSpace(archivePath)
	if archivePath == "" || trust == nil {
		return nil, fmt.Errorf("signed pack requires an archive path and trust store")
	}
	archive, err := openPackArchiveNoFollow(archivePath)
	if err != nil {
		return nil, fmt.Errorf("open signed pack: %w", err)
	}
	defer archive.Close()
	openedInfo, err := archive.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat opened signed pack: %w", err)
	}
	pathInfo, err := os.Lstat(archivePath)
	if err != nil {
		return nil, fmt.Errorf("stat signed pack path: %w", err)
	}
	if !openedInfo.Mode().IsRegular() || !pathInfo.Mode().IsRegular() || !os.SameFile(openedInfo, pathInfo) || openedInfo.Size() > MaxPackArchiveBytes {
		return nil, fmt.Errorf("signed pack must be a regular file no larger than %d bytes", MaxPackArchiveBytes)
	}
	reader, err := zip.NewReader(archive, openedInfo.Size())
	if err != nil {
		return nil, fmt.Errorf("read signed pack ZIP: %w", err)
	}

	files, err := indexPackArchive(reader.File)
	if err != nil {
		return nil, err
	}
	if err := preflightPackArchiveAggregate(files); err != nil {
		return nil, err
	}
	manifestFile, ok := files["manifest.json"]
	if !ok {
		return nil, fmt.Errorf("signed pack is missing manifest.json")
	}
	signatureFile, ok := files["manifest.sig"]
	if !ok {
		return nil, fmt.Errorf("signed pack is missing manifest.sig")
	}
	manifestBytes, err := readPackArchiveFile(manifestFile, maxPackManifestBytes)
	if err != nil {
		return nil, fmt.Errorf("read signed pack manifest: %w", err)
	}
	if err := jsonpolicy.ValidateNoDuplicateKeys(manifestBytes); err != nil {
		return nil, fmt.Errorf("validate signed pack manifest JSON: %w", err)
	}
	signature, err := readPackArchiveFile(signatureFile, ed25519.SignatureSize)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return nil, fmt.Errorf("signed pack signature must be exactly %d bytes", ed25519.SignatureSize)
	}

	var manifest signedPackManifest
	decoder := json.NewDecoder(strings.NewReader(string(manifestBytes)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode signed pack manifest: %w", err)
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return nil, fmt.Errorf("decode signed pack manifest: %w", err)
	}
	manifest.SignerKeyID = strings.TrimSpace(manifest.SignerKeyID)
	if manifest.SignerKeyID == "" || len(manifest.SignerKeyID) > maxPackSignerKeyIDLength {
		return nil, fmt.Errorf("signed pack requires a bounded signer key id")
	}
	publicKey, ok := trust.LookupPackSigner(manifest.SignerKeyID)
	if !ok {
		return nil, fmt.Errorf("signed pack signer is not trusted")
	}
	if !ed25519.Verify(publicKey, manifestBytes, signature) {
		return nil, fmt.Errorf("signed pack signature is invalid")
	}

	provider, err := validateSignedPackManifest(manifest, manifestBytes, currentCaravanVersion)
	if err != nil {
		return nil, err
	}
	descriptors, err := classifySignedPack(files, manifest, provider)
	if err != nil {
		return nil, err
	}
	licenseBytes, err := readPackArchiveFile(files[manifest.License.Path], maxPackLicenseBytes)
	if err != nil {
		return nil, fmt.Errorf("read signed pack license: %w", err)
	}
	var noticeBytes []byte
	if manifest.Notice != nil {
		noticeBytes, err = readPackArchiveFile(files[manifest.Notice.Path], maxPackLicenseBytes)
		if err != nil {
			return nil, fmt.Errorf("read signed pack notice: %w", err)
		}
	}
	licenseDigest, err := normalizedSHA256(manifest.License.SHA256)
	if err != nil {
		return nil, fmt.Errorf("signed pack license digest: %w", err)
	}
	signerFingerprint := sha256.Sum256(publicKey)
	receipt := packReceiptMetadata{
		LicensePath: manifest.License.Path, LicenseDigest: licenseDigest,
		MinimumCaravanVersion: manifest.MinimumCaravanVersion,
		SignerKeyFingerprint:  "sha256:" + hex.EncodeToString(signerFingerprint[:]),
		SignerPublicKey:       append([]byte(nil), publicKey...),
	}
	if manifest.Notice != nil {
		noticeDigest, err := normalizedSHA256(manifest.Notice.SHA256)
		if err != nil {
			return nil, fmt.Errorf("signed pack notice digest: %w", err)
		}
		receipt.NoticePath, receipt.NoticeDigest = manifest.Notice.Path, noticeDigest
	}
	return &PackCandidate{
		descriptor: provider, definitions: descriptors,
		acceptance: PackLicenseAcceptanceRequirements{
			ManifestDigest: provider.ManifestDigest, LicenseDigest: licenseDigest,
			SignerKeyFingerprint: receipt.SignerKeyFingerprint,
		},
		receipt: receipt, license: licenseBytes, notice: noticeBytes,
	}, nil
}

func validateSignedPackManifest(manifest signedPackManifest, manifestBytes []byte, currentCaravanVersion string) (ProviderDescriptor, error) {
	if manifest.FormatVersion != CurrentPackFormatVersion {
		return ProviderDescriptor{}, fmt.Errorf("unsupported signed pack format version %d", manifest.FormatVersion)
	}
	if manifest.CardigannSchemaVersion != CurrentCardigannPackSchemaVersion {
		return ProviderDescriptor{}, fmt.Errorf("unsupported Cardigann pack schema version %d", manifest.CardigannSchemaVersion)
	}
	manifest.Source = strings.TrimSpace(manifest.Source)
	if manifest.Source == BuiltinSource || manifest.Source == "user" {
		return ProviderDescriptor{}, fmt.Errorf("signed pack source %q is reserved", manifest.Source)
	}
	if _, err := ParseDefinitionRef(manifest.Source + ":source-check"); err != nil {
		return ProviderDescriptor{}, fmt.Errorf("signed pack source: %w", err)
	}
	manifest.Revision = strings.TrimSpace(manifest.Revision)
	if manifest.Revision == "" || len(manifest.Revision) > maxPackRevisionLength {
		return ProviderDescriptor{}, fmt.Errorf("signed pack requires a bounded revision")
	}
	manifest.SPDXLicenseExpression = strings.TrimSpace(manifest.SPDXLicenseExpression)
	manifest.Provenance = strings.TrimSpace(manifest.Provenance)
	if manifest.SPDXLicenseExpression == "" || len(manifest.SPDXLicenseExpression) > 256 || manifest.Provenance == "" || len(manifest.Provenance) > maxPackProvenanceLength {
		return ProviderDescriptor{}, fmt.Errorf("signed pack requires bounded license and provenance metadata")
	}
	if err := requireCompatibleVersion(currentCaravanVersion, manifest.MinimumCaravanVersion); err != nil {
		return ProviderDescriptor{}, err
	}
	if len(manifest.Definitions) == 0 || len(manifest.Definitions) > MaxPackDefinitionFiles {
		return ProviderDescriptor{}, fmt.Errorf("signed pack definitions must contain 1 through %d entries", MaxPackDefinitionFiles)
	}
	if manifest.TotalFiles <= 0 || manifest.TotalFiles > MaxPackDefinitionFiles+2 || manifest.TotalUncompressedBytes <= 0 || manifest.TotalUncompressedBytes > MaxPackDefinitionAggregateBytes {
		return ProviderDescriptor{}, fmt.Errorf("signed pack declared totals are outside fixed limits")
	}
	digest := sha256.Sum256(manifestBytes)
	return ProviderDescriptor{
		Source:         manifest.Source,
		Kind:           SourceKindPack,
		Revision:       manifest.Revision,
		License:        manifest.SPDXLicenseExpression,
		Provenance:     manifest.Provenance,
		SignerKeyID:    manifest.SignerKeyID,
		ManifestDigest: "sha256:" + hex.EncodeToString(digest[:]),
	}, nil
}

func classifySignedPack(files map[string]*zip.File, manifest signedPackManifest, provider ProviderDescriptor) ([]DefinitionDescriptor, error) {
	expected := map[string]struct{}{"manifest.json": {}, "manifest.sig": {}}
	seenPaths := make(map[string]struct{}, len(manifest.Definitions)+2)
	seenRefs := make(map[DefinitionRef]struct{}, len(manifest.Definitions))
	seenMetadata := make(map[string]struct{}, len(manifest.Definitions))
	var aggregate int64
	payloadFiles := 0

	validatePayload := func(entry signedPackFile, limit int64) ([]byte, error) {
		name, err := normalizeSignedPackPath(entry.Path)
		if err != nil {
			return nil, err
		}
		if name == "manifest.json" || name == "manifest.sig" {
			return nil, fmt.Errorf("signed payload path %q is reserved", name)
		}
		key := strings.ToLower(name)
		if _, exists := seenPaths[key]; exists {
			return nil, fmt.Errorf("signed pack repeats normalized path %q", name)
		}
		seenPaths[key] = struct{}{}
		expected[name] = struct{}{}
		file, ok := files[name]
		if !ok {
			return nil, fmt.Errorf("signed pack is missing %q", name)
		}
		data, err := readPackArchiveFile(file, limit)
		if err != nil {
			return nil, fmt.Errorf("read signed pack member %q: %w", name, err)
		}
		digest, err := normalizedSHA256(entry.SHA256)
		if err != nil {
			return nil, fmt.Errorf("signed pack member %q digest: %w", name, err)
		}
		actual := fmt.Sprintf("sha256:%x", sha256.Sum256(data))
		if actual != digest {
			return nil, fmt.Errorf("signed pack member %q digest mismatch", name)
		}
		aggregate += int64(len(data))
		if aggregate > MaxPackDefinitionAggregateBytes {
			return nil, fmt.Errorf("signed pack payload exceeds aggregate byte limit")
		}
		payloadFiles++
		return data, nil
	}

	if _, err := validatePayload(manifest.License, maxPackLicenseBytes); err != nil {
		return nil, fmt.Errorf("validate signed pack license: %w", err)
	}
	if manifest.Notice != nil {
		if _, err := validatePayload(*manifest.Notice, maxPackLicenseBytes); err != nil {
			return nil, fmt.Errorf("validate signed pack notice: %w", err)
		}
	}

	descriptors := make([]DefinitionDescriptor, 0, len(manifest.Definitions))
	for _, entry := range manifest.Definitions {
		entry.ID = strings.TrimSpace(entry.ID)
		ref, err := ParseDefinitionRef(manifest.Source + ":" + entry.ID)
		if err != nil {
			return nil, fmt.Errorf("signed pack definition id %q: %w", entry.ID, err)
		}
		if _, exists := seenRefs[ref]; exists {
			return nil, fmt.Errorf("signed pack repeats definition reference %q", ref)
		}
		seenRefs[ref] = struct{}{}
		metadataRef, err := ParseDefinitionRef("metadata:" + strings.TrimSpace(entry.MetadataID))
		if err != nil {
			return nil, fmt.Errorf("signed pack definition %q metadata id: %w", ref, err)
		}
		metadataID := metadataRef.ID
		if _, exists := seenMetadata[metadataID]; exists {
			return nil, fmt.Errorf("signed pack repeats metadata id %q", metadataID)
		}
		seenMetadata[metadataID] = struct{}{}
		if !strings.HasPrefix(entry.Path, "definitions/") || path.Ext(entry.Path) != ".yml" {
			return nil, fmt.Errorf("signed pack definition %q path must be definitions/<name>.yml", ref)
		}
		data, err := validatePayload(signedPackFile{Path: entry.Path, SHA256: entry.SHA256}, MaxDefinitionFileBytes)
		if err != nil {
			return nil, fmt.Errorf("validate signed pack definition %q: %w", ref, err)
		}
		manifestData := data
		manifestView, err := ParseManifest(manifest.Source, manifestData)
		if err != nil {
			return nil, fmt.Errorf("classify signed pack definition %q: %w", ref, err)
		}
		if containsCapability(manifestView.Unsupported, "syntax.invalid") {
			compatibleData, ok := normalizeSignedPackJSONSlashEscapes(data)
			if !ok {
				return nil, fmt.Errorf("signed pack definition %q failed strict syntax validation", ref)
			}
			manifestData = compatibleData
			manifestView, err = ParseManifest(manifest.Source, manifestData)
			if err != nil || containsCapability(manifestView.Unsupported, "syntax.invalid") {
				return nil, fmt.Errorf("signed pack definition %q failed strict syntax validation", ref)
			}
			manifestView.Unsupported = sortedCodes(append(manifestView.Unsupported, CapabilityCode("compiler.invalid")))
			manifestView.Runnable = false
		}
		if manifestView.Ref != ref {
			return nil, fmt.Errorf("signed pack definition %q declares %q", ref, manifestView.Ref)
		}
		origins, err := normalizedOrigins(entry.ApprovedOrigins)
		if err != nil || len(origins) == 0 {
			return nil, fmt.Errorf("signed pack definition %q requires valid approved origins", ref)
		}
		declaredOrigins, err := manifestOrigins(manifestData)
		if err != nil {
			return nil, fmt.Errorf("read signed pack definition %q origins: %w", ref, err)
		}
		for _, origin := range declaredOrigins {
			if !containsString(origins, origin) {
				return nil, fmt.Errorf("signed pack definition %q origin %q is not approved", ref, origin)
			}
		}
		if manifestView.Runnable {
			if _, err := ParseDefinition(data); err != nil {
				return nil, fmt.Errorf("compile signed pack definition %q: %w", ref, err)
			}
		}
		state := DefinitionStateUnsupported
		switch {
		case manifestView.Runnable:
			state = DefinitionStateRunnableUnverified
		case containsCapability(manifestView.Unsupported, "compiler.invalid"):
			state = DefinitionStateQuarantined
		}
		digest, _ := normalizedSHA256(entry.SHA256)
		descriptors = append(descriptors, DefinitionDescriptor{
			Ref:             ref,
			MetadataID:      metadataID,
			Path:            entry.Path,
			Revision:        manifest.Revision,
			Digest:          digest,
			State:           state,
			Unsupported:     append([]CapabilityCode(nil), manifestView.Unsupported...),
			ApprovedOrigins: origins,
			Provider:        provider,
		})
	}
	if payloadFiles != manifest.TotalFiles || aggregate != manifest.TotalUncompressedBytes {
		return nil, fmt.Errorf("signed pack declared totals do not match verified payload")
	}
	if len(files) != len(expected) {
		return nil, fmt.Errorf("signed pack contains undeclared archive members")
	}
	for name := range files {
		if _, ok := expected[name]; !ok {
			return nil, fmt.Errorf("signed pack contains undeclared archive member %q", name)
		}
	}
	sort.Slice(descriptors, func(i, j int) bool { return descriptors[i].Ref.String() < descriptors[j].Ref.String() })
	return descriptors, nil
}

func indexPackArchive(entries []*zip.File) (map[string]*zip.File, error) {
	if len(entries) > MaxPackDefinitionFiles+4 {
		return nil, fmt.Errorf("signed pack contains too many archive members")
	}
	files := make(map[string]*zip.File, len(entries))
	caseFolded := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		name, err := normalizeSignedPackPath(entry.Name)
		if err != nil {
			return nil, fmt.Errorf("invalid signed pack archive path %q: %w", entry.Name, err)
		}
		if entry.NonUTF8 || !entry.Mode().IsRegular() {
			return nil, fmt.Errorf("signed pack member %q must be a regular UTF-8 file", name)
		}
		key := strings.ToLower(name)
		if _, exists := caseFolded[key]; exists {
			return nil, fmt.Errorf("signed pack repeats normalized archive path %q", name)
		}
		caseFolded[key] = struct{}{}
		files[name] = entry
	}
	return files, nil
}

func preflightPackArchiveAggregate(files map[string]*zip.File) error {
	var total uint64
	limit := uint64(MaxPackDefinitionAggregateBytes)
	for name, file := range files {
		if name == "manifest.json" || name == "manifest.sig" {
			continue
		}
		if file.UncompressedSize64 > limit-total {
			return fmt.Errorf("signed pack payload exceeds aggregate byte limit")
		}
		total += file.UncompressedSize64
	}
	return nil
}

func normalizeSignedPackPath(raw string) (string, error) {
	if raw == "" || strings.TrimSpace(raw) != raw || strings.ContainsAny(raw, "\\\x00") || strings.HasPrefix(raw, "/") || path.Clean(raw) != raw || hasTraversalComponent(raw) {
		return "", fmt.Errorf("path must be normalized, relative, and non-traversing")
	}
	for _, character := range raw {
		if character < 0x20 || character > 0x7e {
			return "", fmt.Errorf("path must use printable ASCII")
		}
	}
	return raw, nil
}

func readPackArchiveFile(file *zip.File, limit int64) ([]byte, error) {
	if file == nil || file.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("archive member exceeds %d bytes", limit)
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	data, readErr := io.ReadAll(io.LimitReader(reader, limit+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, readErr
	}
	if closeErr != nil {
		return nil, closeErr
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("archive member exceeds %d bytes", limit)
	}
	return data, nil
}

func normalizedSHA256(raw string) (string, error) {
	raw = strings.TrimPrefix(strings.ToLower(strings.TrimSpace(raw)), "sha256:")
	if len(raw) != sha256.Size*2 {
		return "", fmt.Errorf("must be a SHA-256 hex digest")
	}
	if _, err := hex.DecodeString(raw); err != nil {
		return "", fmt.Errorf("must be a SHA-256 hex digest")
	}
	return "sha256:" + raw, nil
}

func normalizedOrigins(raw []string) ([]string, error) {
	seen := make(map[string]struct{}, len(raw))
	origins := make([]string, 0, len(raw))
	for _, value := range raw {
		origin, err := normalizeOrigin(value)
		if err != nil {
			return nil, err
		}
		if _, exists := seen[origin]; !exists {
			seen[origin] = struct{}{}
			origins = append(origins, origin)
		}
	}
	sort.Strings(origins)
	return origins, nil
}

func normalizeOrigin(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Path != "" && parsed.Path != "/") || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("%q is not an http(s) origin", raw)
	}
	return strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host), nil
}

// normalizeSignedPackJSONSlashEscapes accepts the JSON-compatible \/ escape
// used by some upstream Cardigann YAML emitters for inert metadata parsing.
// Strict execution parsing still sees and rejects the original signed bytes.
func normalizeSignedPackJSONSlashEscapes(data []byte) ([]byte, bool) {
	out := make([]byte, 0, len(data))
	inDoubleQuoted := false
	replaced := false
	for i := 0; i < len(data); i++ {
		character := data[i]
		if !inDoubleQuoted {
			out = append(out, character)
			if character == '"' {
				inDoubleQuoted = true
			}
			continue
		}
		if character == '"' {
			out = append(out, character)
			inDoubleQuoted = false
			continue
		}
		if character == '\\' && i+1 < len(data) {
			next := data[i+1]
			if next == '/' {
				out = append(out, '/')
				i++
				replaced = true
				continue
			}
			out = append(out, character, next)
			i++
			continue
		}
		out = append(out, character)
	}
	return out, replaced
}

func manifestOrigins(data []byte) ([]string, error) {
	var raw struct {
		Links []string `yaml:"links"`
	}
	if err := decodeDefinitionMetadata(data, &raw); err != nil {
		return nil, err
	}
	return normalizedOrigins(raw.Links)
}

func decodeDefinitionMetadata(data []byte, target any) error {
	// Metadata decoding reuses the same strict single-document graph policy as
	// executable parsing and never evaluates templates or performs I/O.
	if err := validateYAMLSource(data); err != nil {
		return err
	}
	return yaml.Unmarshal(data, target)
}

func containsString(values []string, target string) bool {
	index := sort.SearchStrings(values, target)
	return index < len(values) && values[index] == target
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("manifest must contain one JSON value")
		}
		return err
	}
	return nil
}

func requireCompatibleVersion(current, minimum string) error {
	// Unreleased builds are not granted a synthetic future version. Mapping only
	// local development to 0.0.0 keeps minimum-version enforcement fail-closed.
	if strings.TrimSpace(current) == "dev" {
		current = "0.0.0"
	}
	currentVersion, err := parseStrictVersion(current)
	if err != nil {
		return fmt.Errorf("current Caravan version: %w", err)
	}
	minimumVersion, err := parseStrictVersion(minimum)
	if err != nil {
		return fmt.Errorf("minimum Caravan version: %w", err)
	}
	for i := range currentVersion {
		if currentVersion[i] > minimumVersion[i] {
			return nil
		}
		if currentVersion[i] < minimumVersion[i] {
			return fmt.Errorf("signed pack requires Caravan %s or newer", minimum)
		}
	}
	return nil
}

func parseStrictVersion(raw string) ([3]int, error) {
	var version [3]int
	raw = strings.TrimPrefix(strings.TrimSpace(raw), "v")
	parts := strings.Split(raw, ".")
	if len(parts) != len(version) {
		return version, fmt.Errorf("version %q must be major.minor.patch", raw)
	}
	for i, part := range parts {
		if part == "" || (len(part) > 1 && part[0] == '0') {
			return version, fmt.Errorf("version %q is not canonical", raw)
		}
		value, err := strconv.Atoi(part)
		if err != nil || value < 0 {
			return version, fmt.Errorf("version %q is not canonical", raw)
		}
		version[i] = value
	}
	return version, nil
}

func cloneDefinitionDescriptor(descriptor DefinitionDescriptor) DefinitionDescriptor {
	descriptor.Unsupported = append([]CapabilityCode(nil), descriptor.Unsupported...)
	descriptor.ApprovedOrigins = append([]string(nil), descriptor.ApprovedOrigins...)
	return descriptor
}
