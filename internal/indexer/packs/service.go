// Package packs is the internal owner-facing signed-pack lifecycle service.
// It deliberately exposes no HTTP types and accepts upload bytes, never paths.
package packs

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/watzon/caravan/internal/cardigann"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

type Store interface {
	InstallDefinitionPackRevision(context.Context, *core.DefinitionPackRevision, []core.DefinitionPackEntry) error
	MarkDefinitionPackPending(context.Context, string, string) error
	RollbackPendingDefinitionPack(context.Context, string, string, string) error
	GetDefinitionPackRevision(context.Context, string, string) (*core.DefinitionPackRevision, error)
	ListDefinitionPackRevisions(context.Context) ([]core.DefinitionPackRevision, error)
	ListDefinitionPackEntries(context.Context, string, string) ([]core.DefinitionPackEntry, error)
	Backup(context.Context, io.Writer) error
	BackupAndInspect(context.Context, io.Writer, int64) (store.DefinitionPackSnapshot, error)
	StageRestore(context.Context, io.Reader, int64) error
}

type Service struct {
	DataDir    string
	Version    string
	Store      Store
	Now        func() time.Time
	PreviewTTL time.Duration
	mu         sync.Mutex
	tokens     map[string]previewToken
}

type previewToken struct {
	digest, source, revision, manifest, license, signer, keyID string
	publicKey                                                  []byte
	actor                                                      int64
	expires                                                    time.Time
	used                                                       bool
}
type Preview struct {
	Source, Revision, ArchiveDigest, ManifestDigest, LicenseDigest, SignerKeyFingerprint, Token string
	License, Notice                                                                             []byte
	ExpiresAt                                                                                   time.Time
}
type Status struct {
	Source, Revision, State, ValidationCode                                          string
	ArchiveDigest, ManifestDigest, LicenseDigest, NoticeDigest, SignerKeyFingerprint string
	LicenseExpression, Provenance, MinimumCaravanVersion                             string
	Pending, Active, LastKnownGood                                                   bool
	DefinitionCount, RunnableCount                                                   int
	AcceptedAt, InstalledAt                                                          time.Time
	AcceptedByUserID                                                                 int64
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now().UTC()
	}
	return time.Now().UTC()
}
func (s *Service) ttl() time.Duration {
	if s.PreviewTTL > 0 {
		return s.PreviewTTL
	}
	return 5 * time.Minute
}

const maxPreviewTokens = 1024

func (s *Service) Preview(ctx context.Context, actor int64, signerKeyID string, publicKey ed25519.PublicKey, upload []byte) (Preview, error) {
	if actor <= 0 {
		return Preview{}, fmt.Errorf("pack actor is invalid")
	}
	candidate, digest, err := s.verifyUpload(upload, signerKeyID, publicKey)
	if err != nil {
		return Preview{}, fmt.Errorf("pack.upload.invalid")
	}
	p := candidate.Descriptor()
	req := candidate.LicenseAcceptanceRequirements()
	legal := candidate.LegalMaterial()
	token, err := randomToken()
	if err != nil {
		return Preview{}, err
	}
	expires := s.now().Add(s.ttl())
	s.mu.Lock()
	if s.tokens == nil {
		s.tokens = map[string]previewToken{}
	}
	s.pruneTokensLocked(s.now())
	if len(s.tokens) >= maxPreviewTokens {
		s.mu.Unlock()
		return Preview{}, fmt.Errorf("pack preview capacity is exhausted")
	}
	s.tokens[token] = previewToken{digest: digest, source: p.Source, revision: p.Revision, manifest: req.ManifestDigest, license: req.LicenseDigest, signer: req.SignerKeyFingerprint, keyID: signerKeyID, publicKey: append([]byte(nil), publicKey...), actor: actor, expires: expires}
	s.mu.Unlock()
	return Preview{Source: p.Source, Revision: p.Revision, ArchiveDigest: digest, ManifestDigest: req.ManifestDigest, LicenseDigest: req.LicenseDigest, SignerKeyFingerprint: req.SignerKeyFingerprint, License: legal.License, Notice: legal.Notice, Token: token, ExpiresAt: expires}, nil
}

// AcceptAndInstall consumes a preview token, derives acceptance time and actor
// on the server, re-verifies supplied bytes, and stores only its receipt.
func (s *Service) AcceptAndInstall(ctx context.Context, actor int64, sourceClaim, token, signerKeyID string, publicKey ed25519.PublicKey, upload []byte) (Status, error) {
	if actor <= 0 {
		return Status{}, fmt.Errorf("pack actor is invalid")
	}
	s.mu.Lock()
	s.pruneTokensLocked(s.now())
	record, ok := s.tokens[token]
	s.mu.Unlock()
	if !ok || record.used || record.actor != actor || record.keyID != signerKeyID || !bytes.Equal(record.publicKey, publicKey) {
		return s.idempotentInstallStatus(ctx, actor, sourceClaim, signerKeyID, publicKey, upload)
	}
	candidate, digest, err := s.verifyUpload(upload, signerKeyID, publicKey)
	if err != nil {
		return Status{}, fmt.Errorf("pack.upload.invalid")
	}
	p := candidate.Descriptor()
	req := candidate.LicenseAcceptanceRequirements()
	if record.digest != digest || record.source != p.Source || record.revision != p.Revision || record.manifest != req.ManifestDigest || record.license != req.LicenseDigest || record.signer != req.SignerKeyFingerprint {
		return Status{}, fmt.Errorf("pack preview token is invalid, expired, or already used")
	}
	// This claim check deliberately precedes the one-way token burn.
	if sourceClaim != p.Source {
		return Status{}, fmt.Errorf("pack source claim does not match signed source")
	}
	s.mu.Lock()
	s.pruneTokensLocked(s.now())
	record, ok = s.tokens[token]
	alreadyUsed := !ok || record.used
	if !alreadyUsed {
		record.used = true
		s.tokens[token] = record
	}
	s.mu.Unlock()
	if alreadyUsed {
		return s.idempotentInstallStatus(ctx, actor, sourceClaim, signerKeyID, publicKey, upload)
	}
	imported, err := s.importUpload(upload, req, signerKeyID, publicKey)
	if err != nil {
		return Status{}, fmt.Errorf("pack.install.failed")
	}
	receipt, err := imported.InstallReceipt(s.now(), actor)
	if err != nil {
		return Status{}, err
	}
	revision, entries := receipt.Revision(), receipt.Entries()
	if err := s.Store.InstallDefinitionPackRevision(ctx, &revision, entries); err != nil {
		if existing, retryErr := s.idempotentReceiptStatus(ctx, actor, revision, entries); retryErr == nil {
			return existing, nil
		}
		return Status{}, err
	}
	return status(revision), nil
}
func (s *Service) pruneTokensLocked(now time.Time) {
	for token, record := range s.tokens {
		if record.used || !now.Before(record.expires) {
			delete(s.tokens, token)
		}
	}
}

func (s *Service) idempotentInstallStatus(ctx context.Context, actor int64, sourceClaim, signerKeyID string, publicKey ed25519.PublicKey, upload []byte) (Status, error) {
	candidate, digest, err := s.verifyUpload(upload, signerKeyID, publicKey)
	if err != nil {
		return Status{}, fmt.Errorf("pack preview token is invalid, expired, or already used")
	}
	provider := candidate.Descriptor()
	if sourceClaim != provider.Source {
		return Status{}, fmt.Errorf("pack source claim does not match signed source")
	}
	revision, err := s.Store.GetDefinitionPackRevision(ctx, provider.Source, provider.Revision)
	if err != nil || revision.AcceptedByUserID != actor || revision.ArchiveDigest != digest || revision.ManifestDigest != provider.ManifestDigest || revision.SignerKeyID != signerKeyID || !bytes.Equal(revision.SignerPublicKey, publicKey) {
		return Status{}, fmt.Errorf("pack preview token is invalid, expired, or already used")
	}
	return status(*revision), nil
}

func (s *Service) idempotentReceiptStatus(ctx context.Context, actor int64, receipt core.DefinitionPackRevision, entries []core.DefinitionPackEntry) (Status, error) {
	existing, err := s.Store.GetDefinitionPackRevision(ctx, receipt.Source, receipt.Revision)
	if err != nil || existing.AcceptedByUserID != actor || existing.ArchiveDigest != receipt.ArchiveDigest || existing.ManifestDigest != receipt.ManifestDigest || existing.LicenseDigest != receipt.LicenseDigest || existing.SignerKeyID != receipt.SignerKeyID || existing.SignerKeyFingerprint != receipt.SignerKeyFingerprint || !bytes.Equal(existing.SignerPublicKey, receipt.SignerPublicKey) {
		return Status{}, fmt.Errorf("pack install is not an idempotent receipt")
	}
	persisted, err := s.Store.ListDefinitionPackEntries(ctx, receipt.Source, receipt.Revision)
	if err != nil || len(persisted) != len(entries) {
		return Status{}, fmt.Errorf("pack install is not an idempotent receipt")
	}
	byRef := make(map[string]core.DefinitionPackEntry, len(entries))
	for _, entry := range entries {
		byRef[entry.DefinitionRef] = entry
	}
	for _, entry := range persisted {
		want, ok := byRef[entry.DefinitionRef]
		if !ok || entry.MetadataID != want.MetadataID || entry.Path != want.Path || entry.Digest != want.Digest || entry.State != want.State || !slices.Equal(entry.Unsupported, want.Unsupported) || !slices.Equal(entry.ApprovedOrigins, want.ApprovedOrigins) {
			return Status{}, fmt.Errorf("pack install is not an idempotent receipt")
		}
	}
	return status(*existing), nil
}
func (s *Service) RequestActivation(ctx context.Context, source, revision string) error {
	return s.Store.MarkDefinitionPackPending(ctx, source, revision)
}
func (s *Service) Rollback(ctx context.Context, source, revision string) error {
	return s.Store.RollbackPendingDefinitionPack(ctx, source, revision, "pack.owner.rollback")
}
func (s *Service) List(ctx context.Context) ([]Status, error) {
	revisions, err := s.Store.ListDefinitionPackRevisions(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]Status, len(revisions))
	for i := range revisions {
		result[i] = status(revisions[i])
	}
	return result, nil
}

// Entries returns immutable entry receipts for inventory projection. It never
// opens an archive or returns definition bytes.
func (s *Service) Entries(ctx context.Context, source, revision string) ([]core.DefinitionPackEntry, error) {
	return s.Store.ListDefinitionPackEntries(ctx, source, revision)
}

func (s *Service) Status(ctx context.Context, source, revision string) (Status, error) {
	r, err := s.Store.GetDefinitionPackRevision(ctx, source, revision)
	if err != nil {
		return Status{}, err
	}
	return status(*r), nil
}
func status(r core.DefinitionPackRevision) Status {
	return Status{
		Source: r.Source, Revision: r.Revision, State: r.InstallState, ValidationCode: r.ValidationError,
		ArchiveDigest: r.ArchiveDigest, ManifestDigest: r.ManifestDigest, LicenseDigest: r.LicenseDigest,
		NoticeDigest: r.NoticeDigest, SignerKeyFingerprint: r.SignerKeyFingerprint,
		LicenseExpression: r.LicenseExpression, Provenance: r.Provenance, MinimumCaravanVersion: r.MinimumCaravanVersion,
		Pending: r.Pending, Active: r.Active, LastKnownGood: r.LastKnownGood,
		DefinitionCount: r.DefinitionCount, RunnableCount: r.RunnableCount,
		AcceptedAt: r.AcceptedAt, InstalledAt: r.InstalledAt, AcceptedByUserID: r.AcceptedByUserID,
	}
}
func (s *Service) verifyUpload(upload []byte, signerKeyID string, publicKey ed25519.PublicKey) (*cardigann.PackCandidate, string, error) {
	if len(upload) == 0 || int64(len(upload)) > cardigann.MaxPackArchiveBytes || signerKeyID == "" || len(publicKey) != ed25519.PublicKeySize {
		return nil, "", fmt.Errorf("pack upload, signer key id, or public key is invalid")
	}
	path, err := s.writeUpload(upload)
	if err != nil {
		return nil, "", err
	}
	defer os.Remove(path)
	c, err := cardigann.OpenSignedPackArchive(path, s.Version, cardigann.StaticPackTrustStore{signerKeyID: append(ed25519.PublicKey(nil), publicKey...)})
	if err != nil {
		return nil, "", err
	}
	sum := sha256.Sum256(upload)
	return c, "sha256:" + fmt.Sprintf("%x", sum[:]), nil
}
func (s *Service) importUpload(upload []byte, req cardigann.PackLicenseAcceptanceRequirements, signerKeyID string, publicKey ed25519.PublicKey) (*cardigann.PackImportResult, error) {
	path, err := s.writeUpload(upload)
	if err != nil {
		return nil, err
	}
	defer os.Remove(path)
	return cardigann.ImportSignedPackArchive(cardigann.PackImportRequest{DataDir: s.DataDir, ArchivePath: path, CurrentCaravanVersion: s.Version, Trust: cardigann.StaticPackTrustStore{signerKeyID: append(ed25519.PublicKey(nil), publicKey...)}, Acceptance: &cardigann.PackLicenseAcceptance{ManifestDigest: req.ManifestDigest, LicenseDigest: req.LicenseDigest, SignerKeyFingerprint: req.SignerKeyFingerprint, AcceptedAt: s.now()}})
}
func (s *Service) writeUpload(upload []byte) (string, error) {
	if s.DataDir == "" || s.Store == nil {
		return "", fmt.Errorf("pack service requires data directory and store")
	}
	return cardigann.WritePackStagingFile(s.DataDir, "service-staging", upload)
}
func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
