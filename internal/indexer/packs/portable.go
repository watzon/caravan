package packs

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/watzon/caravan/internal/cardigann"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

const (
	portableFormat       = "caravan-portable-bundle"
	portableVersion      = 1
	portableMaxMembers   = 4098
	portableMaxManifest  = 1 << 20
	portableManifestName = "manifest.json"
	portableDatabaseName = "database.sqlite"
)

// ErrPortableTooLarge identifies a portable outer bundle that exceeded the
// caller's byte limit. API callers map this stable classification to HTTP 413.
var ErrPortableTooLarge = errors.New("portable bundle exceeds size limit")

// PortableOptions supplies the hard outer ZIP byte limit. Both create and
// restore read/write at most MaxBytes+1 before rejecting oversized input.
type PortableOptions struct{ MaxBytes int64 }

type portableManifest struct {
	Format   string                 `json:"format"`
	Version  int                    `json:"version"`
	Database portableBundleMember   `json:"database"`
	Archives []portableBundleMember `json:"archives"`
}

type portableBundleMember struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	Digest string `json:"digest"`
}

// CreatePortable writes a deterministic ZIP snapshot containing the SQLite
// backup and every nonfailed signed-pack archive it references. Archive
// ownership remains with the cardigann publisher; this is only a transport
// container.
func (s *Service) CreatePortable(ctx context.Context, dst io.Writer, options PortableOptions) error {
	if s == nil || s.Store == nil || strings.TrimSpace(s.DataDir) == "" || options.MaxBytes <= 0 {
		return fmt.Errorf("portable bundle requires store, data directory, and positive size limit")
	}
	var database bytes.Buffer
	inventory, err := s.Store.BackupAndInspect(ctx, &database, options.MaxBytes)
	if err != nil {
		return fmt.Errorf("create portable database snapshot: %w", err)
	}
	archives := make([]portableArchive, 0, len(inventory.Revisions))
	for _, revision := range inventory.Revisions {
		if revision.InstallState == core.DefinitionPackFailed {
			continue // exact missing-key tombstones are audit-only.
		}
		entries := inventory.Entries[store.DefinitionPackSnapshotKey(revision.Source, revision.Revision)]
		if len(entries) == 0 {
			return fmt.Errorf("portable snapshot has no entries for revision %s:%s", revision.Source, revision.Revision)
		}
		_, memberName, err := portableArchivePath(s.DataDir, revision)
		if err != nil {
			return err
		}
		data, err := cardigann.ReadInstalledPackArchive(s.DataDir, revision.ArchiveRelPath, revision.ArchiveDigest)
		if err != nil {
			return fmt.Errorf("read portable archive: %w", err)
		}
		if projectedPortableBytes(database.Len(), archives, memberName, len(data)) > options.MaxBytes {
			return fmt.Errorf("portable bundle exceeds size limit")
		}
		stage, err := cardigann.WritePackStagingFile(s.DataDir, "portable-staging", data)
		if err != nil {
			return fmt.Errorf("stage portable archive verification: %w", err)
		}
		err = cardigann.VerifyInstalledPackArchive(stage, s.Version, revision, entries)
		os.Remove(stage)
		if err != nil {
			return fmt.Errorf("verify portable archive %s:%s: %w", revision.Source, revision.Revision, err)
		}
		archives = append(archives, portableArchive{portableBundleMember: portableBundleMember{Path: memberName, Size: int64(len(data)), Digest: digestBytes(data)}, data: data})
	}
	sort.Slice(archives, func(i, j int) bool { return archives[i].Path < archives[j].Path })
	manifest := portableManifest{Format: portableFormat, Version: portableVersion, Database: portableBundleMember{Path: portableDatabaseName, Size: int64(database.Len()), Digest: digestBytes(database.Bytes())}, Archives: make([]portableBundleMember, len(archives))}
	for i := range archives {
		manifest.Archives[i] = archives[i].portableBundleMember
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return fmt.Errorf("encode portable manifest: %w", err)
	}
	if len(manifestBytes) > portableMaxManifest {
		return fmt.Errorf("portable manifest exceeds limit")
	}

	// Build in a private bounded buffer: callers never receive a partial bundle.
	var output bytes.Buffer
	limited := &portableLimitedWriter{dst: &output, limit: options.MaxBytes + 1}
	zw := zip.NewWriter(limited)
	if err := writePortableZipMember(zw, portableManifestName, manifestBytes); err != nil {
		return err
	}
	if err := writePortableZipMember(zw, portableDatabaseName, database.Bytes()); err != nil {
		return err
	}
	for _, archive := range archives {
		if err := writePortableZipMember(zw, archive.Path, archive.data); err != nil {
			return err
		}
	}
	if err := zw.Close(); err != nil {
		return fmt.Errorf("write portable ZIP: %w", err)
	}
	if limited.tooLarge || limited.written > options.MaxBytes {
		return fmt.Errorf("portable bundle exceeds size limit")
	}
	n, err := dst.Write(output.Bytes())
	if err != nil {
		return fmt.Errorf("write portable bundle: %w", err)
	}
	if n != output.Len() {
		return io.ErrShortWrite
	}
	return nil
}

type portableArchive struct {
	portableBundleMember
	data []byte
}

type portableRestoreArchive struct {
	revision core.DefinitionPackRevision
	data     []byte
}

func projectedPortableBytes(databaseBytes int, archives []portableArchive, nextName string, nextBytes int) int64 {
	// Stored ZIP members use a local header, central header, and one EOCD. Keep
	// a full manifest allowance before retaining another archive in memory.
	members := 2 + len(archives) + 1
	total := int64(portableMaxManifest + databaseBytes + nextBytes + 22)
	names := []string{portableManifestName, portableDatabaseName, nextName}
	for _, archive := range archives {
		total += int64(len(archive.data))
		names = append(names, archive.Path)
	}
	for _, name := range names {
		total += int64(30 + len(name) + 46 + len(name))
	}
	if members > portableMaxMembers {
		return 1<<63 - 1
	}
	return total
}

// RestorePortable validates every ZIP member, its database snapshot, and every
// referenced signed archive before publishing archives idempotently. Database
// replacement is deliberately last, via Store.StageRestore.
func (s *Service) RestorePortable(ctx context.Context, src io.Reader, options PortableOptions) error {
	if s == nil || s.Store == nil || strings.TrimSpace(s.DataDir) == "" || options.MaxBytes <= 0 {
		return fmt.Errorf("portable bundle requires store, data directory, and positive size limit")
	}
	bundle, err := io.ReadAll(io.LimitReader(src, options.MaxBytes+1))
	if err != nil {
		return fmt.Errorf("read portable bundle: %w", err)
	}
	if int64(len(bundle)) > options.MaxBytes {
		return ErrPortableTooLarge
	}
	zr, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		return fmt.Errorf("read portable ZIP: %w", err)
	}
	members, err := validatePortableMembers(zr.File)
	if err != nil {
		return err
	}
	manifestData, err := readPortableMember(members[portableManifestName], portableMaxManifest)
	if err != nil {
		return err
	}
	manifest, err := parsePortableManifest(manifestData)
	if err != nil {
		return err
	}
	database, err := readPortableMember(members[portableDatabaseName], options.MaxBytes)
	if err != nil {
		return err
	}
	if err := verifyPortableMember(manifest.Database, portableDatabaseName, database); err != nil {
		return err
	}
	if len(manifest.Archives)+2 != len(members) {
		return fmt.Errorf("portable ZIP inventory has unexpected members")
	}
	for _, archive := range manifest.Archives {
		file, ok := members[archive.Path]
		if !ok {
			return fmt.Errorf("portable ZIP is missing archive")
		}
		data, err := readPortableMember(file, cardigann.MaxPackArchiveBytes)
		if err != nil {
			return err
		}
		if err := verifyPortableMember(archive, archive.Path, data); err != nil {
			return err
		}
	}

	stageDB, err := cardigann.WritePackStagingFile(s.DataDir, "portable-staging", database)
	if err != nil {
		return fmt.Errorf("stage portable database: %w", err)
	}
	defer os.Remove(stageDB)
	inventory, err := store.InspectDefinitionPackSnapshot(ctx, stageDB)
	if err != nil {
		return fmt.Errorf("validate portable database: %w", err)
	}
	want := map[string]portableBundleMember{}
	for _, revision := range inventory.Revisions {
		if revision.InstallState == core.DefinitionPackFailed {
			continue
		}
		_, member, err := portableArchivePath(s.DataDir, revision)
		if err != nil {
			return err
		}
		want[member] = portableBundleMember{Path: member, Digest: revision.ArchiveDigest}
	}
	if len(want) != len(manifest.Archives) {
		return fmt.Errorf("portable manifest archive inventory does not match database")
	}
	for _, archive := range manifest.Archives {
		if expected, ok := want[archive.Path]; !ok || expected.Digest != archive.Digest {
			return fmt.Errorf("portable manifest archive inventory does not match database")
		}
	}
	validated := make([]portableRestoreArchive, 0, len(inventory.Revisions))
	for _, revision := range inventory.Revisions {
		if revision.InstallState == core.DefinitionPackFailed {
			continue
		}
		_, member, _ := portableArchivePath(s.DataDir, revision)
		data, err := readPortableMember(members[member], cardigann.MaxPackArchiveBytes)
		if err != nil {
			return err
		}
		archivePath, err := cardigann.WritePackStagingFile(s.DataDir, "portable-staging", data)
		if err != nil {
			return fmt.Errorf("stage portable archive: %w", err)
		}
		entries := inventory.Entries[store.DefinitionPackSnapshotKey(revision.Source, revision.Revision)]
		err = cardigann.VerifyInstalledPackArchive(archivePath, s.Version, revision, entries)
		os.Remove(archivePath)
		if err != nil {
			return fmt.Errorf("verify portable signed archive: %w", err)
		}
		validated = append(validated, portableRestoreArchive{revision: revision, data: data})
	}
	// Nothing under the destination archive root is published until every DB and
	// archive member above has passed verification.
	for _, archive := range validated {
		revision := archive.revision
		archivePath, err := cardigann.WritePackStagingFile(s.DataDir, "portable-staging", archive.data)
		if err != nil {
			return fmt.Errorf("stage portable archive publication: %w", err)
		}
		_, err = cardigann.ImportSignedPackArchive(cardigann.PackImportRequest{DataDir: s.DataDir, ArchivePath: archivePath, CurrentCaravanVersion: s.Version, Trust: cardigann.StaticPackTrustStore{revision.SignerKeyID: revision.SignerPublicKey}, Acceptance: &cardigann.PackLicenseAcceptance{ManifestDigest: revision.ManifestDigest, LicenseDigest: revision.LicenseDigest, SignerKeyFingerprint: revision.SignerKeyFingerprint, AcceptedAt: revision.AcceptedAt}})
		os.Remove(archivePath)
		if err != nil {
			return fmt.Errorf("publish portable signed archive: %w", err)
		}
	}
	if err := s.Store.StageRestore(ctx, bytes.NewReader(database), int64(len(database))); err != nil {
		return fmt.Errorf("stage portable database restore: %w", err)
	}
	return nil
}

type portableLimitedWriter struct {
	dst            io.Writer
	limit, written int64
	tooLarge       bool
}

func (w *portableLimitedWriter) Write(p []byte) (int, error) {
	if w.written+int64(len(p)) > w.limit {
		w.tooLarge = true
		return 0, fmt.Errorf("portable bundle exceeds size limit")
	}
	n, err := w.dst.Write(p)
	w.written += int64(n)
	return n, err
}

func writePortableZipMember(zw *zip.Writer, name string, data []byte) error {
	h := &zip.FileHeader{Name: name, Method: zip.Store}
	w, err := zw.CreateHeader(h)
	if err != nil {
		return fmt.Errorf("create portable ZIP member: %w", err)
	}
	n, err := w.Write(data)
	if err != nil {
		return fmt.Errorf("write portable ZIP member: %w", err)
	}
	if n != len(data) {
		return io.ErrShortWrite
	}
	return nil
}

func validatePortableMembers(files []*zip.File) (map[string]*zip.File, error) {
	if len(files) < 2 || len(files) > portableMaxMembers {
		return nil, fmt.Errorf("portable ZIP member count is invalid")
	}
	members := make(map[string]*zip.File, len(files))
	folded := make(map[string]struct{}, len(files))
	for _, file := range files {
		name := file.Name
		if name == "" || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "/") || strings.Contains(name, "..") || strings.HasSuffix(name, "/") || file.FileInfo().Mode()&os.ModeSymlink != 0 || file.Method != zip.Store || file.CompressedSize64 != file.UncompressedSize64 || file.Flags&1 != 0 {
			return nil, fmt.Errorf("portable ZIP member is unsafe")
		}
		if _, exists := members[name]; exists {
			return nil, fmt.Errorf("portable ZIP repeats member")
		}
		fold := strings.ToLower(name)
		if _, exists := folded[fold]; exists {
			return nil, fmt.Errorf("portable ZIP has case-fold collision")
		}
		folded[fold] = struct{}{}
		if name != portableManifestName && name != portableDatabaseName && !portableArchiveMember(name) {
			return nil, fmt.Errorf("portable ZIP has unexpected member")
		}
		if name == portableManifestName && file.UncompressedSize64 > portableMaxManifest {
			return nil, fmt.Errorf("portable manifest exceeds limit")
		}
		if portableArchiveMember(name) && file.UncompressedSize64 > uint64(cardigann.MaxPackArchiveBytes) {
			return nil, fmt.Errorf("portable archive exceeds limit")
		}
		members[name] = file
	}
	if _, ok := members[portableManifestName]; !ok {
		return nil, fmt.Errorf("portable ZIP is missing manifest")
	}
	if _, ok := members[portableDatabaseName]; !ok {
		return nil, fmt.Errorf("portable ZIP is missing database")
	}
	return members, nil
}

func portableArchiveMember(name string) bool {
	const prefix = "indexer-packs/archives/sha256/"
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".zip") || len(name) != len(prefix)+64+4 {
		return false
	}
	_, err := hex.DecodeString(strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".zip"))
	return err == nil && strings.ToLower(name) == name
}
func portableArchivePath(dataDir string, revision core.DefinitionPackRevision) (string, string, error) {
	hexDigest := strings.TrimPrefix(revision.ArchiveDigest, "sha256:")
	if len(hexDigest) != 64 || strings.ToLower(hexDigest) != hexDigest {
		return "", "", fmt.Errorf("portable revision archive digest is invalid")
	}
	rel := "archives/sha256/" + hexDigest + ".zip"
	if revision.ArchiveRelPath != rel {
		return "", "", fmt.Errorf("portable revision archive path is not canonical")
	}
	return filepath.Join(dataDir, "indexer-packs", rel), "indexer-packs/" + rel, nil
}
func readBoundedFile(path string, limit int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, fmt.Errorf("file exceeds limit")
	}
	return b, nil
}
func readPortableMember(file *zip.File, limit int64) ([]byte, error) {
	if file.UncompressedSize64 > uint64(limit) {
		return nil, fmt.Errorf("portable ZIP member exceeds limit")
	}
	r, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil || int64(len(b)) > limit || uint64(len(b)) != file.UncompressedSize64 {
		return nil, fmt.Errorf("read portable ZIP member")
	}
	return b, nil
}
func digestBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}
func parsePortableManifest(data []byte) (portableManifest, error) {
	var m portableManifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return m, fmt.Errorf("parse portable manifest: %w", err)
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF || m.Format != portableFormat || m.Version != portableVersion {
		return m, fmt.Errorf("portable manifest is invalid")
	}
	if m.Database.Path != portableDatabaseName || m.Database.Size < 0 || m.Database.Digest == "" {
		return m, fmt.Errorf("portable manifest database is invalid")
	}
	prev := ""
	seen := map[string]struct{}{}
	for _, a := range m.Archives {
		if !portableArchiveMember(a.Path) || a.Size < 0 || a.Digest == "" || a.Path <= prev {
			return m, fmt.Errorf("portable manifest archive inventory is invalid")
		}
		if _, ok := seen[a.Path]; ok {
			return m, fmt.Errorf("portable manifest repeats archive")
		}
		seen[a.Path] = struct{}{}
		prev = a.Path
	}
	return m, nil
}
func verifyPortableMember(member portableBundleMember, path string, data []byte) error {
	if member.Path != path || member.Size != int64(len(data)) || member.Digest != digestBytes(data) {
		return fmt.Errorf("portable member does not match manifest")
	}
	return nil
}
