package store

import "github.com/uptrace/bun"

type definitionPackSourceModel struct {
	bun.BaseModel `bun:"table:definition_pack_sources,alias:definition_pack_source"`

	Source                    string `bun:",pk"`
	OwnerSignerKeyID          string `bun:"owner_signer_key_id,notnull"`
	OwnerSignerKeyFingerprint string `bun:"owner_signer_key_fingerprint,notnull"`
	OwnerSignerPublicKey      []byte `bun:"owner_signer_public_key,notnull"`
	CreatedAt                 string `bun:"created_at,notnull"`
	UpdatedAt                 string `bun:"updated_at,notnull"`
}

type definitionPackRevisionModel struct {
	bun.BaseModel `bun:"table:definition_pack_revisions,alias:definition_pack_revision"`

	Source                string `bun:",pk"`
	Revision              string `bun:",pk"`
	ManifestDigest        string `bun:"manifest_digest,notnull"`
	ArchiveDigest         string `bun:"archive_digest,notnull"`
	ArchiveRelPath        string `bun:"archive_relpath,notnull"`
	LicenseExpression     string `bun:"license_expression,notnull"`
	LicensePath           string `bun:"license_path,notnull"`
	LicenseDigest         string `bun:"license_digest,notnull"`
	NoticePath            string `bun:"notice_path,notnull"`
	NoticeDigest          string `bun:"notice_digest,notnull"`
	Provenance            string `bun:",notnull"`
	SignerKeyID           string `bun:"signer_key_id,notnull"`
	SignerKeyFingerprint  string `bun:"signer_key_fingerprint,notnull"`
	MinimumCaravanVersion string `bun:"minimum_caravan_version,notnull"`
	InstallState          string `bun:"install_state,notnull"`
	Pending               bool   `bun:"is_pending,notnull"`
	Active                bool   `bun:"is_active,notnull"`
	LastKnownGood         bool   `bun:"is_last_known_good,notnull"`
	ValidationError       string `bun:"validation_error,notnull"`
	DefinitionCount       int    `bun:"definition_count,notnull"`
	RunnableCount         int    `bun:"runnable_count,notnull"`
	AcceptedAt            string `bun:"accepted_at,notnull"`
	AcceptedByUserID      int64  `bun:"accepted_by_user_id,notnull"`
	InstalledAt           string `bun:"installed_at,notnull"`
	CreatedAt             string `bun:"created_at,notnull"`
	UpdatedAt             string `bun:"updated_at,notnull"`
}

type definitionPackEntryModel struct {
	bun.BaseModel `bun:"table:definition_pack_entries,alias:definition_pack_entry"`

	Source              string `bun:",pk"`
	Revision            string `bun:",pk"`
	DefinitionRef       string `bun:"definition_ref,pk"`
	MetadataID          string `bun:"metadata_id,notnull"`
	Path                string `bun:",notnull"`
	Digest              string `bun:",notnull"`
	State               string `bun:",notnull"`
	UnsupportedJSON     string `bun:"unsupported_json,notnull"`
	ApprovedOriginsJSON string `bun:"approved_origins_json,notnull"`
}

type indexerDefinitionPinModel struct {
	bun.BaseModel `bun:"table:indexer_definition_pins,alias:indexer_definition_pin"`

	IndexerID     int64  `bun:"indexer_id,pk"`
	Source        string `bun:",notnull"`
	Revision      string `bun:",notnull"`
	DefinitionRef string `bun:"definition_ref,notnull"`
	Digest        string `bun:",notnull"`
}
