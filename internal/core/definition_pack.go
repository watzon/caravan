package core

import "time"

const (
	DefinitionPackInstalling = "installing"
	DefinitionPackInstalled  = "installed"
	DefinitionPackFailed     = "failed"

	DefinitionPackEntryUnsupported        = "unsupported"
	DefinitionPackEntryRunnableUnverified = "runnable-unverified"
)

// DefinitionPackRevision is one accepted immutable pack revision. It contains
// only public provenance and lifecycle data; tracker credentials never belong
// in pack records.
type DefinitionPackRevision struct {
	Source               string
	Revision             string
	ManifestDigest       string
	ArchiveDigest        string
	ArchiveRelPath       string
	LicenseExpression    string
	LicensePath          string
	LicenseDigest        string
	NoticePath           string
	NoticeDigest         string
	Provenance           string
	SignerKeyID          string
	SignerKeyFingerprint string
	// SignerPublicKey is the exact accepted Ed25519 key material, bound to this
	// source/key ID/fingerprint and required for every startup verification.
	SignerPublicKey       []byte
	MinimumCaravanVersion string
	InstallState          string
	Pending               bool
	Active                bool
	LastKnownGood         bool
	ValidationError       string
	DefinitionCount       int
	RunnableCount         int
	AcceptedAt            time.Time
	AcceptedByUserID      int64
	InstalledAt           time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// DefinitionPackEntry pins one classified definition to exact signed bytes.
type DefinitionPackEntry struct {
	Source          string
	Revision        string
	DefinitionRef   string
	MetadataID      string
	Path            string
	Digest          string
	State           string
	Unsupported     []string
	ApprovedOrigins []string
}
