package store

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"path"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

// InstallDefinitionPackRevision records one exact, already accepted immutable
// revision and all of its classified entries. Installation is idempotent only
// when the source owner, revision identity, and every entry are identical.
func (s *Store) InstallDefinitionPackRevision(ctx context.Context, revision *core.DefinitionPackRevision, entries []core.DefinitionPackEntry) error {
	if revision == nil {
		return fmt.Errorf("store: install definition pack: revision is nil")
	}
	if revision.Pending || revision.Active || revision.LastKnownGood || revision.ValidationError != "" {
		return fmt.Errorf("store: install definition pack lifecycle is server-controlled")
	}
	if err := validateDefinitionPackRevision(*revision, entries); err != nil {
		return err
	}
	createdAt := revision.CreatedAt
	if createdAt.IsZero() {
		createdAt = now()
	}
	updatedAt := revision.UpdatedAt
	if updatedAt.IsZero() {
		updatedAt = createdAt
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin definition pack install: %w", err)
	}
	defer tx.Rollback()

	owner, err := getDefinitionPackSource(ctx, tx, revision.Source)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if !validDefinitionPackPublicKey(revision.SignerPublicKey, revision.SignerKeyFingerprint) {
			return fmt.Errorf("store: source claim requires an exact accepted Ed25519 public key")
		}
		owner = definitionPackSourceModel{
			Source: revision.Source, OwnerSignerKeyID: revision.SignerKeyID,
			OwnerSignerKeyFingerprint: revision.SignerKeyFingerprint,
			OwnerSignerPublicKey:      append([]byte(nil), revision.SignerPublicKey...),
			CreatedAt:                 formatTime(createdAt), UpdatedAt: formatTime(updatedAt),
		}
		if _, err := tx.NewInsert().Model(&owner).Exec(ctx); err != nil {
			return fmt.Errorf("store: claim definition pack source %q: %w", revision.Source, definitionPackConstraintError(err))
		}
	case err != nil:
		return fmt.Errorf("store: read definition pack source %q: %w", revision.Source, err)
	case owner.OwnerSignerKeyID != revision.SignerKeyID || owner.OwnerSignerKeyFingerprint != revision.SignerKeyFingerprint:
		return fmt.Errorf("store: definition pack source %q belongs to another signer: %w", revision.Source, ErrConflict)
	case len(owner.OwnerSignerPublicKey) == 0:
		// 0008 left pre-existing sources keyless. The stored fingerprint
		// still identifies the original owner, so only the key hashing to
		// it may reclaim the namespace.
		if !validDefinitionPackPublicKey(revision.SignerPublicKey, owner.OwnerSignerKeyFingerprint) {
			return fmt.Errorf("store: definition pack source %q belongs to another signer: %w", revision.Source, ErrConflict)
		}
		if _, err := tx.NewUpdate().Model((*definitionPackSourceModel)(nil)).
			Set("owner_signer_public_key = ?", append([]byte(nil), revision.SignerPublicKey...)).
			Set("updated_at = ?", formatTime(updatedAt)).
			Where("source = ?", revision.Source).
			Exec(ctx); err != nil {
			return fmt.Errorf("store: reclaim definition pack source %q: %w", revision.Source, definitionPackConstraintError(err))
		}
	case !bytes.Equal(owner.OwnerSignerPublicKey, revision.SignerPublicKey):
		return fmt.Errorf("store: definition pack source %q belongs to another signer: %w", revision.Source, ErrConflict)
	}

	var existing definitionPackRevisionModel
	err = tx.NewSelect().Model(&existing).
		Where("source = ? AND revision = ?", revision.Source, revision.Revision).
		Scan(ctx)
	if err == nil {
		if !sameDefinitionPackRevision(existing, *revision) {
			return fmt.Errorf("store: definition pack revision %s:%s has different immutable content: %w", revision.Source, revision.Revision, ErrConflict)
		}
		if err := sameDefinitionPackEntries(ctx, tx, entries); err != nil {
			return err
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: inspect definition pack revision %s:%s: %w", revision.Source, revision.Revision, err)
	}

	model := definitionPackRevisionModelFromCore(*revision, createdAt, updatedAt)
	if _, err := tx.NewInsert().Model(&model).Exec(ctx); err != nil {
		return fmt.Errorf("store: insert definition pack revision %s:%s: %w", revision.Source, revision.Revision, definitionPackConstraintError(err))
	}
	for _, entry := range entries {
		model, err := definitionPackEntryModelFromCore(entry)
		if err != nil {
			return err
		}
		if _, err := tx.NewInsert().Model(&model).Exec(ctx); err != nil {
			return fmt.Errorf("store: insert definition pack entry %q: %w", entry.DefinitionRef, definitionPackConstraintError(err))
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit definition pack install: %w", err)
	}
	revision.CreatedAt = createdAt
	revision.UpdatedAt = updatedAt
	return nil
}

func (s *Store) ListDefinitionPackRevisions(ctx context.Context) ([]core.DefinitionPackRevision, error) {
	models := make([]definitionPackRevisionModel, 0)
	if err := s.db.NewSelect().Model(&models).OrderExpr("source ASC, revision ASC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list definition pack revisions: %w", err)
	}
	result := make([]core.DefinitionPackRevision, 0, len(models))
	for _, model := range models {
		revision, err := model.core()
		if err != nil {
			return nil, err
		}
		if err := s.attachDefinitionPackPublicKey(ctx, &revision); err != nil {
			return nil, err
		}
		result = append(result, revision)
	}
	return result, nil
}

func (s *Store) GetDefinitionPackRevision(ctx context.Context, source, revision string) (*core.DefinitionPackRevision, error) {
	var model definitionPackRevisionModel
	err := s.db.NewSelect().Model(&model).
		Where("source = ? AND revision = ?", source, revision).
		Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: definition pack revision %s:%s: %w", source, revision, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get definition pack revision %s:%s: %w", source, revision, err)
	}
	result, err := model.core()
	if err != nil {
		return nil, err
	}
	if err := s.attachDefinitionPackPublicKey(ctx, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (s *Store) attachDefinitionPackPublicKey(ctx context.Context, revision *core.DefinitionPackRevision) error {
	if revision == nil {
		return fmt.Errorf("store: definition pack revision is nil")
	}
	var key []byte
	if err := s.db.QueryRowContext(ctx, "SELECT owner_signer_public_key FROM definition_pack_sources WHERE source = ?", revision.Source).Scan(&key); err != nil {
		return fmt.Errorf("store: load definition pack source public key: %w", err)
	}
	if len(key) == 0 && revision.InstallState == core.DefinitionPackFailed && revision.ValidationError == "pack.trust.missing_public_key" {
		return nil
	}
	if !validDefinitionPackPublicKey(key, revision.SignerKeyFingerprint) {
		return fmt.Errorf("store: definition pack source %q has missing or corrupt public key", revision.Source)
	}
	revision.SignerPublicKey = append([]byte(nil), key...)
	return nil
}

func (s *Store) ListDefinitionPackEntries(ctx context.Context, source, revision string) ([]core.DefinitionPackEntry, error) {
	models := make([]definitionPackEntryModel, 0)
	if err := s.db.NewSelect().Model(&models).
		Where("source = ? AND revision = ?", source, revision).
		Order("definition_ref ASC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list definition pack entries %s:%s: %w", source, revision, err)
	}
	entries := make([]core.DefinitionPackEntry, 0, len(models))
	for _, model := range models {
		entry, err := model.core()
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func (s *Store) MarkDefinitionPackPending(ctx context.Context, source, revision string) error {
	if !validDefinitionPackSource(source) || strings.TrimSpace(revision) == "" {
		return fmt.Errorf("store: invalid definition pack lifecycle identity")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin definition pack pending transition: %w", err)
	}
	defer tx.Rollback()
	var state string
	var active, lastKnownGood bool
	var runnableCount int
	if err := tx.QueryRowContext(ctx, "SELECT install_state, is_active, is_last_known_good, runnable_count FROM definition_pack_revisions WHERE source = ? AND revision = ?", source, revision).Scan(&state, &active, &lastKnownGood, &runnableCount); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: definition pack revision %s:%s: %w", source, revision, ErrNotFound)
	} else if err != nil {
		return fmt.Errorf("store: read definition pack pending target: %w", err)
	}
	if state != core.DefinitionPackInstalled || active || lastKnownGood {
		return fmt.Errorf("store: only inactive non-LKG installed definition packs can become pending")
	}
	if runnableCount == 0 {
		return fmt.Errorf("store: inert definition pack cannot become pending")
	}
	if _, err := tx.ExecContext(ctx, "UPDATE definition_pack_revisions SET is_pending = 0, updated_at = ? WHERE source = ?", formatTime(now()), source); err != nil {
		return fmt.Errorf("store: clear definition pack pending revision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE definition_pack_revisions SET is_pending = 1, updated_at = ? WHERE source = ? AND revision = ?", formatTime(now()), source, revision); err != nil {
		return fmt.Errorf("store: mark definition pack pending: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit definition pack pending transition: %w", err)
	}
	return nil
}

// PromotePendingDefinitionPack makes an already installed pending revision the
// active source and its last-known-good revision after a startup self-test.
func (s *Store) PromotePendingDefinitionPack(ctx context.Context, source, revision string) error {
	if !validDefinitionPackSource(source) || strings.TrimSpace(revision) == "" {
		return fmt.Errorf("store: invalid definition pack lifecycle identity")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin definition pack promotion: %w", err)
	}
	defer tx.Rollback()
	var pending bool
	if err := tx.QueryRowContext(ctx, "SELECT is_pending FROM definition_pack_revisions WHERE source = ? AND revision = ? AND install_state = ?", source, revision, core.DefinitionPackInstalled).Scan(&pending); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: pending definition pack revision %s:%s: %w", source, revision, ErrNotFound)
	} else if err != nil {
		return fmt.Errorf("store: read pending definition pack: %w", err)
	}
	if !pending {
		return fmt.Errorf("store: definition pack revision %s:%s is not pending", source, revision)
	}
	ts := formatTime(now())
	if _, err := tx.ExecContext(ctx, "UPDATE definition_pack_revisions SET is_active = 0, is_last_known_good = 0, updated_at = ? WHERE source = ?", ts, source); err != nil {
		return fmt.Errorf("store: clear active definition pack revision: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE definition_pack_revisions SET is_pending = 0, is_active = 1, is_last_known_good = 1, validation_error = '', updated_at = ? WHERE source = ? AND revision = ?", ts, source, revision); err != nil {
		return fmt.Errorf("store: promote pending definition pack revision: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit definition pack promotion: %w", err)
	}
	return nil
}

// RollbackPendingDefinitionPack records a failed pending activation and returns
// the source to its existing LKG revision without deleting immutable bytes.
func (s *Store) RollbackPendingDefinitionPack(ctx context.Context, source, revision, reason string) error {
	reason = sanitizeDefinitionPackValidationCode(reason)
	if !validDefinitionPackSource(source) || strings.TrimSpace(revision) == "" || reason == "" {
		return fmt.Errorf("store: invalid definition pack rollback")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin definition pack rollback: %w", err)
	}
	defer tx.Rollback()
	var pending bool
	if err := tx.QueryRowContext(ctx, "SELECT is_pending FROM definition_pack_revisions WHERE source = ? AND revision = ? AND install_state = ?", source, revision, core.DefinitionPackInstalled).Scan(&pending); errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("store: pending definition pack revision %s:%s: %w", source, revision, ErrNotFound)
	} else if err != nil {
		return fmt.Errorf("store: read rollback definition pack: %w", err)
	}
	if !pending {
		return fmt.Errorf("store: definition pack revision %s:%s is not pending", source, revision)
	}
	ts := formatTime(now())
	if _, err := tx.ExecContext(ctx, "UPDATE definition_pack_revisions SET install_state = ?, is_pending = 0, is_active = 0, is_last_known_good = 0, validation_error = ?, updated_at = ? WHERE source = ? AND revision = ?", core.DefinitionPackFailed, strings.TrimSpace(reason), ts, source, revision); err != nil {
		return fmt.Errorf("store: mark definition pack rollback: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "UPDATE definition_pack_revisions SET is_active = 1, updated_at = ? WHERE source = ? AND is_last_known_good = 1", ts, source); err != nil {
		return fmt.Errorf("store: restore last-known-good definition pack: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit definition pack rollback: %w", err)
	}
	return nil
}

// QuarantineDefinitionPack deactivates an unusable active revision without
// falling back an exact pin to a different source or revision.
func (s *Store) QuarantineDefinitionPack(ctx context.Context, source, revision, reason string) error {
	reason = sanitizeDefinitionPackValidationCode(reason)
	if !validDefinitionPackSource(source) || strings.TrimSpace(revision) == "" || reason == "" {
		return fmt.Errorf("store: invalid definition pack quarantine")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE definition_pack_revisions
		SET install_state = ?, is_pending = 0, is_active = 0, is_last_known_good = 0,
		    validation_error = ?, updated_at = ?
		WHERE source = ? AND revision = ? AND install_state = ?`,
		core.DefinitionPackFailed, reason, formatTime(now()), source, revision, core.DefinitionPackInstalled)
	if err != nil {
		return fmt.Errorf("store: quarantine definition pack %s:%s: %w", source, revision, err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store: inspect definition pack quarantine: %w", err)
	}
	if changed != 1 {
		return fmt.Errorf("store: definition pack revision %s:%s: %w", source, revision, ErrNotFound)
	}
	return nil
}

// GetActiveDefinitionPackRevisions returns exactly the source revisions that a
// startup runtime may load. It never selects pending or merely installed bytes.
func (s *Store) GetActiveDefinitionPackRevisions(ctx context.Context) ([]core.DefinitionPackRevision, error) {
	models := make([]definitionPackRevisionModel, 0)
	if err := s.db.NewSelect().Model(&models).Where("install_state = ? AND is_active = ?", core.DefinitionPackInstalled, true).Order("source ASC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list active definition pack revisions: %w", err)
	}
	result := make([]core.DefinitionPackRevision, 0, len(models))
	for _, model := range models {
		revision, err := model.core()
		if err != nil {
			return nil, err
		}
		if !revision.Active || !revision.LastKnownGood {
			return nil, fmt.Errorf("store: active definition pack revision %s:%s has invalid lifecycle", revision.Source, revision.Revision)
		}
		if err := s.attachDefinitionPackPublicKey(ctx, &revision); err != nil {
			return nil, err
		}
		result = append(result, revision)
	}
	return result, nil
}

// GetPendingDefinitionPackRevisions returns installed revisions awaiting the
// one-time startup self-test. Pending bytes are never runtime-active by virtue
// of this query alone.
func (s *Store) GetPendingDefinitionPackRevisions(ctx context.Context) ([]core.DefinitionPackRevision, error) {
	models := make([]definitionPackRevisionModel, 0)
	if err := s.db.NewSelect().Model(&models).Where("install_state = ? AND is_pending = ?", core.DefinitionPackInstalled, true).Order("source ASC").Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list pending definition pack revisions: %w", err)
	}
	result := make([]core.DefinitionPackRevision, 0, len(models))
	for _, model := range models {
		revision, err := model.core()
		if err != nil {
			return nil, err
		}
		if !revision.Pending || revision.Active {
			return nil, fmt.Errorf("store: pending definition pack revision %s:%s has invalid lifecycle", revision.Source, revision.Revision)
		}
		if err := s.attachDefinitionPackPublicKey(ctx, &revision); err != nil {
			return nil, err
		}
		result = append(result, revision)
	}
	return result, nil
}

func validateDefinitionPackRevision(revision core.DefinitionPackRevision, entries []core.DefinitionPackEntry) error {
	if !validDefinitionPackSource(revision.Source) || revision.Source == "builtin" || revision.Source == "user" || revision.Source == "managed" ||
		strings.TrimSpace(revision.Revision) != revision.Revision || revision.Revision == "" || len(revision.Revision) > 256 ||
		!validSHA256Digest(revision.ManifestDigest) || !validSHA256Digest(revision.ArchiveDigest) ||
		!validPackRelativePath(revision.ArchiveRelPath) || !strings.HasPrefix(revision.ArchiveRelPath, "archives/sha256/") ||
		strings.TrimSpace(revision.SignerKeyID) == "" || strings.TrimSpace(revision.SignerKeyID) != revision.SignerKeyID ||
		!validSHA256Digest(revision.SignerKeyFingerprint) || (len(revision.SignerPublicKey) != 0 && !validDefinitionPackPublicKey(revision.SignerPublicKey, revision.SignerKeyFingerprint)) || !validSHA256Digest(revision.LicenseDigest) ||
		(revision.NoticePath == "") != (revision.NoticeDigest == "") ||
		(revision.NoticeDigest != "" && !validSHA256Digest(revision.NoticeDigest)) ||
		!validPackRelativePath(revision.LicensePath) || (revision.NoticePath != "" && !validPackRelativePath(revision.NoticePath)) ||
		strings.TrimSpace(revision.LicenseExpression) == "" || strings.TrimSpace(revision.Provenance) == "" ||
		strings.TrimSpace(revision.MinimumCaravanVersion) == "" || revision.AcceptedAt.IsZero() {
		return fmt.Errorf("store: definition pack revision is missing or has invalid immutable identity or acceptance")
	}
	if revision.InstallState != core.DefinitionPackInstalling && revision.InstallState != core.DefinitionPackInstalled && revision.InstallState != core.DefinitionPackFailed {
		return fmt.Errorf("store: definition pack revision has invalid install state %q", revision.InstallState)
	}
	if revision.InstallState == core.DefinitionPackInstalled && revision.InstalledAt.IsZero() {
		return fmt.Errorf("store: installed definition pack revision requires installed_at")
	}
	if revision.InstallState == core.DefinitionPackInstalling && !revision.InstalledAt.IsZero() {
		return fmt.Errorf("store: installing definition pack revision cannot have installed_at")
	}
	if revision.Active != revision.LastKnownGood {
		return fmt.Errorf("store: definition pack active and last-known-good must match")
	}
	if revision.InstallState != core.DefinitionPackInstalled && (revision.Pending || revision.Active || revision.LastKnownGood) {
		return fmt.Errorf("store: non-installed definition pack revision cannot be pending, active, or last-known-good")
	}
	if revision.InstallState == core.DefinitionPackFailed && strings.TrimSpace(revision.ValidationError) == "" {
		return fmt.Errorf("store: failed definition pack revision requires validation error")
	}
	if revision.DefinitionCount != len(entries) || revision.RunnableCount < 0 || revision.RunnableCount > len(entries) {
		return fmt.Errorf("store: definition pack revision counts do not match entries")
	}
	seenRefs := make(map[string]struct{}, len(entries))
	seenMetadata := make(map[string]struct{}, len(entries))
	runnable := 0
	for _, entry := range entries {
		if entry.Source != revision.Source || entry.Revision != revision.Revision || !validDefinitionPackReference(entry.DefinitionRef, revision.Source) {
			return fmt.Errorf("store: definition pack entry %q does not belong to revision", entry.DefinitionRef)
		}
		if !validSHA256Digest(entry.Digest) || !validPackRelativePath(entry.Path) || !strings.HasPrefix(entry.Path, "definitions/") || path.Ext(entry.Path) != ".yml" {
			return fmt.Errorf("store: definition pack entry %q has invalid path or digest", entry.DefinitionRef)
		}
		if entry.State != core.DefinitionPackEntryUnsupported && entry.State != core.DefinitionPackEntryRunnableUnverified {
			return fmt.Errorf("store: definition pack entry %q has invalid state %q", entry.DefinitionRef, entry.State)
		}
		if len(entry.ApprovedOrigins) == 0 {
			return fmt.Errorf("store: definition pack entry %q has no approved origins", entry.DefinitionRef)
		}
		for _, origin := range entry.ApprovedOrigins {
			if !validDefinitionPackOrigin(origin) {
				return fmt.Errorf("store: definition pack entry %q has invalid approved origin", entry.DefinitionRef)
			}
		}
		if entry.State == core.DefinitionPackEntryRunnableUnverified && len(entry.Unsupported) != 0 {
			return fmt.Errorf("store: runnable definition pack entry %q has unsupported capability codes", entry.DefinitionRef)
		}
		if _, duplicate := seenRefs[entry.DefinitionRef]; duplicate {
			return fmt.Errorf("store: duplicate definition pack entry %q", entry.DefinitionRef)
		}
		seenRefs[entry.DefinitionRef] = struct{}{}
		if strings.TrimSpace(entry.MetadataID) == "" {
			return fmt.Errorf("store: definition pack entry %q has empty metadata id", entry.DefinitionRef)
		}
		if _, duplicate := seenMetadata[entry.MetadataID]; duplicate {
			return fmt.Errorf("store: duplicate definition pack metadata id %q", entry.MetadataID)
		}
		seenMetadata[entry.MetadataID] = struct{}{}
		if entry.State == core.DefinitionPackEntryRunnableUnverified {
			runnable++
		}
	}
	if runnable != revision.RunnableCount {
		return fmt.Errorf("store: definition pack runnable count does not match entries")
	}
	return nil
}

func sanitizeDefinitionPackValidationCode(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 96 {
		return ""
	}
	for _, ch := range value {
		if (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-' {
			continue
		}
		return ""
	}
	return value
}

func validDefinitionPackSource(value string) bool {
	if value == "" || len(value) > 64 || strings.ToLower(value) != value {
		return false
	}
	for i, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= '0' && character <= '9') || (i > 0 && (character == '-' || character == '_' || character == '.')) {
			continue
		}
		return false
	}
	return true
}

func validDefinitionPackReference(ref, source string) bool {
	prefix := source + ":"
	return source != "builtin" && source != "user" && validDefinitionPackSource(source) &&
		strings.HasPrefix(ref, prefix) && validDefinitionPackSource(strings.TrimPrefix(ref, prefix))
}

func validDefinitionPackPublicKey(publicKey []byte, fingerprint string) bool {
	if len(publicKey) != ed25519.PublicKeySize || !validSHA256Digest(fingerprint) {
		return false
	}
	sum := sha256.Sum256(publicKey)
	return fingerprint == fmt.Sprintf("sha256:%x", sum[:])
}

func validSHA256Digest(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, character := range value[7:] {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return false
		}
	}
	return true
}

func validPackRelativePath(value string) bool {
	return value != "" && !strings.ContainsAny(value, "\\\x00") && !strings.HasPrefix(value, "/") && path.Clean(value) == value && value != "." && !strings.Contains(value, "../")
}

func validDefinitionPackOrigin(origin string) bool {
	parsed, err := url.Parse(origin)
	return err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" && parsed.User == nil &&
		(parsed.Path == "" || parsed.Path == "/") && parsed.RawQuery == "" && parsed.Fragment == ""
}

func definitionPackConstraintError(err error) error {
	if err != nil && strings.Contains(strings.ToLower(err.Error()), "unique constraint failed: definition_pack_") {
		return fmt.Errorf("%w: %v", ErrConflict, err)
	}
	return err
}

func definitionPackRevisionModelFromCore(revision core.DefinitionPackRevision, createdAt, updatedAt time.Time) definitionPackRevisionModel {
	return definitionPackRevisionModel{
		Source: revision.Source, Revision: revision.Revision,
		ManifestDigest: revision.ManifestDigest, ArchiveDigest: revision.ArchiveDigest, ArchiveRelPath: revision.ArchiveRelPath,
		LicenseExpression: revision.LicenseExpression, LicensePath: revision.LicensePath, LicenseDigest: revision.LicenseDigest,
		NoticePath: revision.NoticePath, NoticeDigest: revision.NoticeDigest, Provenance: revision.Provenance,
		SignerKeyID: revision.SignerKeyID, SignerKeyFingerprint: revision.SignerKeyFingerprint,
		MinimumCaravanVersion: revision.MinimumCaravanVersion, InstallState: revision.InstallState,
		Pending: revision.Pending, Active: revision.Active, LastKnownGood: revision.LastKnownGood,
		ValidationError: revision.ValidationError, DefinitionCount: revision.DefinitionCount, RunnableCount: revision.RunnableCount,
		AcceptedAt: formatTime(revision.AcceptedAt), AcceptedByUserID: revision.AcceptedByUserID,
		InstalledAt: formatTime(revision.InstalledAt), CreatedAt: formatTime(createdAt), UpdatedAt: formatTime(updatedAt),
	}
}

func sameDefinitionPackRevision(model definitionPackRevisionModel, revision core.DefinitionPackRevision) bool {
	return model.ManifestDigest == revision.ManifestDigest && model.ArchiveDigest == revision.ArchiveDigest &&
		model.ArchiveRelPath == revision.ArchiveRelPath && model.LicenseExpression == revision.LicenseExpression &&
		model.LicensePath == revision.LicensePath && model.LicenseDigest == revision.LicenseDigest &&
		model.NoticePath == revision.NoticePath && model.NoticeDigest == revision.NoticeDigest &&
		model.Provenance == revision.Provenance && model.SignerKeyID == revision.SignerKeyID &&
		model.SignerKeyFingerprint == revision.SignerKeyFingerprint && model.MinimumCaravanVersion == revision.MinimumCaravanVersion &&
		model.DefinitionCount == revision.DefinitionCount && model.RunnableCount == revision.RunnableCount &&
		model.AcceptedAt == formatTime(revision.AcceptedAt) && model.AcceptedByUserID == revision.AcceptedByUserID
}

func sameDefinitionPackEntries(ctx context.Context, tx bun.Tx, entries []core.DefinitionPackEntry) error {
	models := make([]definitionPackEntryModel, 0)
	if err := tx.NewSelect().Model(&models).
		Where("source = ? AND revision = ?", entries[0].Source, entries[0].Revision).
		Order("definition_ref ASC").Scan(ctx); err != nil {
		return fmt.Errorf("store: compare existing definition pack entries: %w", err)
	}
	if len(models) != len(entries) {
		return fmt.Errorf("store: installed definition pack entry count changed: %w", ErrConflict)
	}
	sorted := append([]core.DefinitionPackEntry(nil), entries...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].DefinitionRef < sorted[j].DefinitionRef })
	for i, model := range models {
		entry, err := model.core()
		if err != nil {
			return err
		}
		if entry.DefinitionRef != sorted[i].DefinitionRef || entry.MetadataID != sorted[i].MetadataID || entry.Path != sorted[i].Path ||
			entry.Digest != sorted[i].Digest || entry.State != sorted[i].State ||
			!slices.Equal(entry.Unsupported, sorted[i].Unsupported) || !slices.Equal(entry.ApprovedOrigins, sorted[i].ApprovedOrigins) {
			return fmt.Errorf("store: installed definition pack entry %q changed: %w", entry.DefinitionRef, ErrConflict)
		}
	}
	return nil
}

func definitionPackEntryModelFromCore(entry core.DefinitionPackEntry) (definitionPackEntryModel, error) {
	unsupportedValues := entry.Unsupported
	if unsupportedValues == nil {
		unsupportedValues = []string{}
	}
	unsupported, err := json.Marshal(unsupportedValues)
	if err != nil {
		return definitionPackEntryModel{}, fmt.Errorf("store: encode unsupported codes for %q: %w", entry.DefinitionRef, err)
	}
	origins, err := json.Marshal(entry.ApprovedOrigins)
	if err != nil {
		return definitionPackEntryModel{}, fmt.Errorf("store: encode approved origins for %q: %w", entry.DefinitionRef, err)
	}
	return definitionPackEntryModel{
		Source: entry.Source, Revision: entry.Revision, DefinitionRef: entry.DefinitionRef,
		MetadataID: entry.MetadataID, Path: entry.Path, Digest: entry.Digest, State: entry.State,
		UnsupportedJSON: string(unsupported), ApprovedOriginsJSON: string(origins),
	}, nil
}

func (model definitionPackEntryModel) core() (core.DefinitionPackEntry, error) {
	entry := core.DefinitionPackEntry{
		Source: model.Source, Revision: model.Revision, DefinitionRef: model.DefinitionRef,
		MetadataID: model.MetadataID, Path: model.Path, Digest: model.Digest, State: model.State,
	}
	if !strings.HasPrefix(strings.TrimSpace(model.UnsupportedJSON), "[") {
		return core.DefinitionPackEntry{}, fmt.Errorf("store: unsupported codes for %q are not an array", model.DefinitionRef)
	}
	if err := json.Unmarshal([]byte(model.UnsupportedJSON), &entry.Unsupported); err != nil {
		return core.DefinitionPackEntry{}, fmt.Errorf("store: decode unsupported codes for %q: %w", model.DefinitionRef, err)
	}
	if !strings.HasPrefix(strings.TrimSpace(model.ApprovedOriginsJSON), "[") {
		return core.DefinitionPackEntry{}, fmt.Errorf("store: approved origins for %q are not an array", model.DefinitionRef)
	}
	if err := json.Unmarshal([]byte(model.ApprovedOriginsJSON), &entry.ApprovedOrigins); err != nil {
		return core.DefinitionPackEntry{}, fmt.Errorf("store: decode approved origins for %q: %w", model.DefinitionRef, err)
	}
	if len(entry.ApprovedOrigins) == 0 {
		return core.DefinitionPackEntry{}, fmt.Errorf("store: approved origins for %q are empty", model.DefinitionRef)
	}
	if !validDefinitionPackReference(entry.DefinitionRef, entry.Source) || !validSHA256Digest(entry.Digest) ||
		!validPackRelativePath(entry.Path) || !strings.HasPrefix(entry.Path, "definitions/") || path.Ext(entry.Path) != ".yml" ||
		strings.TrimSpace(entry.MetadataID) == "" || (entry.State != core.DefinitionPackEntryUnsupported && entry.State != core.DefinitionPackEntryRunnableUnverified) {
		return core.DefinitionPackEntry{}, fmt.Errorf("store: definition pack entry %q has corrupt identity", model.DefinitionRef)
	}
	for _, origin := range entry.ApprovedOrigins {
		if !validDefinitionPackOrigin(origin) {
			return core.DefinitionPackEntry{}, fmt.Errorf("store: definition pack entry %q has invalid approved origin", model.DefinitionRef)
		}
	}
	if entry.State == core.DefinitionPackEntryRunnableUnverified && len(entry.Unsupported) != 0 {
		return core.DefinitionPackEntry{}, fmt.Errorf("store: runnable definition pack entry %q has unsupported codes", model.DefinitionRef)
	}
	return entry, nil
}

func getDefinitionPackSource(ctx context.Context, tx bun.Tx, source string) (definitionPackSourceModel, error) {
	var model definitionPackSourceModel
	err := tx.NewSelect().Model(&model).Where("source = ?", source).Scan(ctx)
	return model, err
}

func (model definitionPackRevisionModel) core() (core.DefinitionPackRevision, error) {
	acceptedAt, err := parseRequiredDefinitionPackTime("accepted_at", model.AcceptedAt)
	if err != nil {
		return core.DefinitionPackRevision{}, err
	}
	createdAt, err := parseRequiredDefinitionPackTime("created_at", model.CreatedAt)
	if err != nil {
		return core.DefinitionPackRevision{}, err
	}
	updatedAt, err := parseRequiredDefinitionPackTime("updated_at", model.UpdatedAt)
	if err != nil {
		return core.DefinitionPackRevision{}, err
	}
	var installedAt time.Time
	if model.InstalledAt != "" {
		installedAt, err = parseRequiredDefinitionPackTime("installed_at", model.InstalledAt)
		if err != nil {
			return core.DefinitionPackRevision{}, err
		}
	}
	if model.InstallState == core.DefinitionPackInstalled && installedAt.IsZero() {
		return core.DefinitionPackRevision{}, fmt.Errorf("store: installed definition pack revision %s:%s has no installed_at", model.Source, model.Revision)
	}
	if model.InstallState == core.DefinitionPackInstalling && !installedAt.IsZero() {
		return core.DefinitionPackRevision{}, fmt.Errorf("store: installing definition pack revision %s:%s has installed_at", model.Source, model.Revision)
	}
	result := core.DefinitionPackRevision{
		Source: model.Source, Revision: model.Revision, ManifestDigest: model.ManifestDigest,
		ArchiveDigest: model.ArchiveDigest, ArchiveRelPath: model.ArchiveRelPath,
		LicenseExpression: model.LicenseExpression, LicensePath: model.LicensePath, LicenseDigest: model.LicenseDigest,
		NoticePath: model.NoticePath, NoticeDigest: model.NoticeDigest, Provenance: model.Provenance,
		SignerKeyID: model.SignerKeyID, SignerKeyFingerprint: model.SignerKeyFingerprint,
		MinimumCaravanVersion: model.MinimumCaravanVersion, InstallState: model.InstallState,
		Pending: model.Pending, Active: model.Active, LastKnownGood: model.LastKnownGood,
		ValidationError: model.ValidationError, DefinitionCount: model.DefinitionCount, RunnableCount: model.RunnableCount,
		AcceptedAt: acceptedAt, AcceptedByUserID: model.AcceptedByUserID,
		InstalledAt: installedAt, CreatedAt: createdAt, UpdatedAt: updatedAt,
	}
	if !validDefinitionPackSource(result.Source) || result.Source == "builtin" || result.Source == "user" || result.Source == "managed" ||
		strings.TrimSpace(result.Revision) != result.Revision || result.Revision == "" || len(result.Revision) > 256 ||
		!validSHA256Digest(result.ManifestDigest) || !validSHA256Digest(result.ArchiveDigest) ||
		!validSHA256Digest(result.LicenseDigest) || !validSHA256Digest(result.SignerKeyFingerprint) ||
		(result.NoticeDigest != "" && !validSHA256Digest(result.NoticeDigest)) || !validPackRelativePath(result.ArchiveRelPath) ||
		!strings.HasPrefix(result.ArchiveRelPath, "archives/sha256/") || !validPackRelativePath(result.LicensePath) ||
		(result.NoticePath != "" && !validPackRelativePath(result.NoticePath)) || (result.NoticePath == "") != (result.NoticeDigest == "") ||
		strings.TrimSpace(result.SignerKeyID) == "" || strings.TrimSpace(result.SignerKeyID) != result.SignerKeyID ||
		strings.TrimSpace(result.LicenseExpression) == "" || strings.TrimSpace(result.Provenance) == "" ||
		strings.TrimSpace(result.MinimumCaravanVersion) == "" || result.DefinitionCount < 1 || result.RunnableCount < 0 || result.RunnableCount > result.DefinitionCount {
		return core.DefinitionPackRevision{}, fmt.Errorf("store: definition pack revision %s:%s has corrupt immutable identity", model.Source, model.Revision)
	}
	if result.InstallState != core.DefinitionPackInstalling && result.InstallState != core.DefinitionPackInstalled && result.InstallState != core.DefinitionPackFailed {
		return core.DefinitionPackRevision{}, fmt.Errorf("store: definition pack revision %s:%s has invalid state", model.Source, model.Revision)
	}
	if result.Active != result.LastKnownGood {
		return core.DefinitionPackRevision{}, fmt.Errorf("store: definition pack revision %s:%s has mismatched active/LKG state", model.Source, model.Revision)
	}
	if result.InstallState != core.DefinitionPackInstalled && (result.Pending || result.Active || result.LastKnownGood) {
		return core.DefinitionPackRevision{}, fmt.Errorf("store: non-installed definition pack revision %s:%s has lifecycle flags", model.Source, model.Revision)
	}
	if result.InstallState == core.DefinitionPackFailed && strings.TrimSpace(result.ValidationError) == "" {
		return core.DefinitionPackRevision{}, fmt.Errorf("store: failed definition pack revision %s:%s has no validation error", model.Source, model.Revision)
	}
	return result, nil
}

func parseRequiredDefinitionPackTime(field, value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, fmt.Errorf("store: definition pack %s is empty", field)
	}
	parsed, err := time.Parse(timeFormat, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("store: definition pack %s is malformed: %w", field, err)
	}
	return parsed, nil
}

func upsertIndexerDefinitionPin(ctx context.Context, tx bun.Tx, indexer *core.IndexerConfig) error {
	values := []string{indexer.DefinitionSource, indexer.DefinitionRevision, indexer.DefinitionDigest}
	empty := 0
	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			empty++
		}
	}
	if empty != 0 && empty != len(values) {
		return fmt.Errorf("store: indexer definition pack pin is incomplete")
	}
	if _, err := tx.NewDelete().Model((*indexerDefinitionPinModel)(nil)).Where("indexer_id = ?", indexer.ID).Exec(ctx); err != nil {
		return fmt.Errorf("store: clear indexer %d definition pin: %w", indexer.ID, err)
	}
	if empty == len(values) {
		return nil
	}
	if !strings.HasPrefix(indexer.DefinitionID, indexer.DefinitionSource+":") {
		return fmt.Errorf("store: indexer definition %q does not match pin source %q", indexer.DefinitionID, indexer.DefinitionSource)
	}
	pin := indexerDefinitionPinModel{
		IndexerID: indexer.ID, Source: indexer.DefinitionSource, Revision: indexer.DefinitionRevision,
		DefinitionRef: indexer.DefinitionID, Digest: indexer.DefinitionDigest,
	}
	if _, err := tx.NewInsert().Model(&pin).Exec(ctx); err != nil {
		return fmt.Errorf("store: pin indexer %d to %s:%s: %w", indexer.ID, indexer.DefinitionSource, indexer.DefinitionRevision, err)
	}
	return nil
}

func (s *Store) loadIndexerDefinitionPin(ctx context.Context, indexer *core.IndexerConfig) error {
	var pin indexerDefinitionPinModel
	err := s.db.NewSelect().Model(&pin).Where("indexer_id = ?", indexer.ID).Scan(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		indexer.DefinitionSource = ""
		indexer.DefinitionRevision = ""
		indexer.DefinitionDigest = ""
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: load indexer %d definition pin: %w", indexer.ID, err)
	}
	if pin.DefinitionRef != indexer.DefinitionID {
		return fmt.Errorf("store: indexer %d definition pin reference %q does not match %q", indexer.ID, pin.DefinitionRef, indexer.DefinitionID)
	}
	indexer.DefinitionSource = pin.Source
	indexer.DefinitionRevision = pin.Revision
	indexer.DefinitionDigest = pin.Digest
	return nil
}
