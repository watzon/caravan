package cardigann

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/watzon/caravan/internal/core"
)

const packArchiveRoot = "indexer-packs"

// Publication is serialized in-process because Caravan owns a data directory
// exclusively. Unlike a filesystem lock, this state disappears on a crash; any
// staged or final orphan is recovered by digest verification on the next call.
var packPublicationLocks sync.Map // map[string]*sync.Mutex

var (
	packImportCopy          = io.Copy
	packImportRename        = func(root *os.Root, oldName, newName string) error { return root.Rename(oldName, newName) }
	packImportFileSync      = func(file *os.File) error { return file.Sync() }
	packImportDirectorySync = func(dir *os.File) error { return dir.Sync() }
)

func packPublicationMutex(name string) *sync.Mutex {
	value, _ := packPublicationLocks.LoadOrStore(name, &sync.Mutex{})
	return value.(*sync.Mutex)
}

// PackImportRequest is the explicit owner-controlled boundary for importing a
// separately licensed signed archive. Trust is supplied by the owner; Caravan
// does not carry or discover signer keys.
type PackImportRequest struct {
	DataDir               string
	ArchivePath           string
	CurrentCaravanVersion string
	Trust                 PackTrustStore
	Acceptance            *PackLicenseAcceptance
}

// PackLicenseAcceptance is the owner's affirmative acceptance of the precise
// signed manifest, license bytes, and signer key that will be installed.
type PackLicenseAcceptance struct {
	ManifestDigest       string
	LicenseDigest        string
	SignerKeyFingerprint string
	AcceptedAt           time.Time
	AcceptedByUserID     int64
}

// PackImportResult identifies the immutable, content-addressed bytes that were
// fully verified before publication. Candidate remains inert until a lifecycle
// service records and activates the revision.
type PackImportResult struct {
	Candidate      *PackCandidate
	ArchiveDigest  string
	ArchiveRelPath string
}

// VerifiedPackInstallReceipt is constructible only from a verified candidate
// and published archive. It deliberately owns the signed identity fields and
// always returns fresh copies of its core representations.
type VerifiedPackInstallReceipt struct {
	revision core.DefinitionPackRevision
	entries  []core.DefinitionPackEntry
}

func (r VerifiedPackInstallReceipt) Revision() core.DefinitionPackRevision { return r.revision }
func (r VerifiedPackInstallReceipt) Entries() []core.DefinitionPackEntry {
	entries := append([]core.DefinitionPackEntry(nil), r.entries...)
	for i := range entries {
		entries[i].Unsupported = append([]string(nil), entries[i].Unsupported...)
		entries[i].ApprovedOrigins = append([]string(nil), entries[i].ApprovedOrigins...)
	}
	return entries
}

// InstallReceipt converts only verified signed metadata plus the durable
// content-addressed archive into the immutable store receipt. Lifecycle flags
// are intentionally fixed to a fresh installed state.
func (r *PackImportResult) InstallReceipt(acceptedAt time.Time, acceptedByUserID int64) (VerifiedPackInstallReceipt, error) {
	if r == nil || r.Candidate == nil || acceptedAt.IsZero() || r.ArchiveDigest == "" || r.ArchiveRelPath == "" {
		return VerifiedPackInstallReceipt{}, fmt.Errorf("verified pack install receipt requires published archive and server acceptance")
	}
	candidate := r.Candidate
	provider := candidate.Descriptor()
	metadata := candidate.receipt
	if provider.Kind != SourceKindPack || provider.Source == "" || provider.Revision == "" || provider.ManifestDigest == "" || metadata.LicensePath == "" || metadata.LicenseDigest == "" || metadata.SignerKeyFingerprint == "" {
		return VerifiedPackInstallReceipt{}, fmt.Errorf("verified pack install receipt has incomplete signed identity")
	}
	definitions := candidate.Definitions()
	entries := make([]core.DefinitionPackEntry, 0, len(definitions))
	runnable := 0
	for _, definition := range definitions {
		state := core.DefinitionPackEntryUnsupported
		if definition.State == DefinitionStateRunnableUnverified {
			state, runnable = core.DefinitionPackEntryRunnableUnverified, runnable+1
		}
		unsupported := make([]string, len(definition.Unsupported))
		for i := range definition.Unsupported {
			unsupported[i] = string(definition.Unsupported[i])
		}
		entries = append(entries, core.DefinitionPackEntry{Source: provider.Source, Revision: provider.Revision, DefinitionRef: definition.Ref.String(), MetadataID: definition.MetadataID, Path: definition.Path, Digest: definition.Digest, State: state, Unsupported: unsupported, ApprovedOrigins: append([]string(nil), definition.ApprovedOrigins...)})
	}
	return VerifiedPackInstallReceipt{revision: core.DefinitionPackRevision{
		Source: provider.Source, Revision: provider.Revision, ManifestDigest: provider.ManifestDigest, ArchiveDigest: r.ArchiveDigest, ArchiveRelPath: r.ArchiveRelPath,
		LicenseExpression: provider.License, LicensePath: metadata.LicensePath, LicenseDigest: metadata.LicenseDigest, NoticePath: metadata.NoticePath, NoticeDigest: metadata.NoticeDigest, Provenance: provider.Provenance,
		SignerKeyID: provider.SignerKeyID, SignerKeyFingerprint: metadata.SignerKeyFingerprint, SignerPublicKey: append([]byte(nil), metadata.SignerPublicKey...), MinimumCaravanVersion: metadata.MinimumCaravanVersion,
		InstallState: core.DefinitionPackInstalled, DefinitionCount: len(entries), RunnableCount: runnable, AcceptedAt: acceptedAt.UTC(), AcceptedByUserID: acceptedByUserID, InstalledAt: acceptedAt.UTC(),
	}, entries: entries}, nil
}

// ImportSignedPackArchive copies a no-follow archive into application storage,
// validates that copied snapshot, and publishes it atomically by its SHA-256.
// No caller-controlled archive pathname is retained after this operation.
func ImportSignedPackArchive(request PackImportRequest) (*PackImportResult, error) {
	if strings.TrimSpace(request.DataDir) == "" || strings.TrimSpace(request.ArchivePath) == "" || request.Trust == nil {
		return nil, fmt.Errorf("signed pack import requires data directory, archive path, and trust store")
	}
	root, err := openPackStorageRoot(request.DataDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	if err := ensurePackStorageDirectories(root); err != nil {
		return nil, err
	}

	stagedName, digest, err := copyPackArchiveToStaging(root, request.ArchivePath)
	if err != nil {
		return nil, err
	}
	defer root.Remove(stagedName)

	stagedPath := filepath.Join(root.Name(), stagedName)
	candidate, err := OpenSignedPackArchive(stagedPath, request.CurrentCaravanVersion, request.Trust)
	if err != nil {
		return nil, fmt.Errorf("validate copied signed pack: %w", err)
	}
	if err := validatePackLicenseAcceptance(request.Acceptance, candidate.LicenseAcceptanceRequirements()); err != nil {
		return nil, err
	}
	archiveRelPath := "archives/sha256/" + strings.TrimPrefix(digest, "sha256:") + ".zip"
	if err := publishPackArchive(root, stagedName, archiveRelPath, digest); err != nil {
		return nil, err
	}
	return &PackImportResult{Candidate: candidate, ArchiveDigest: digest, ArchiveRelPath: archiveRelPath}, nil
}

func validatePackLicenseAcceptance(acceptance *PackLicenseAcceptance, requirements PackLicenseAcceptanceRequirements) error {
	if acceptance == nil || acceptance.AcceptedAt.IsZero() ||
		acceptance.ManifestDigest != requirements.ManifestDigest ||
		acceptance.LicenseDigest != requirements.LicenseDigest ||
		acceptance.SignerKeyFingerprint != requirements.SignerKeyFingerprint {
		return fmt.Errorf("signed pack import requires explicit exact owner license acceptance")
	}
	return nil
}

func openPackStorageRoot(dataDir string) (*os.Root, error) {
	absolute, err := filepath.Abs(strings.TrimSpace(dataDir))
	if err != nil {
		return nil, fmt.Errorf("resolve pack data directory: %w", err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, fmt.Errorf("stat pack data directory: %w", err)
	}
	if info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return nil, fmt.Errorf("pack data directory must be a real directory")
	}
	root, err := os.OpenRoot(absolute)
	if err != nil {
		return nil, fmt.Errorf("open pack data directory: %w", err)
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(info, opened) {
		root.Close()
		return nil, fmt.Errorf("pack data directory changed while opening")
	}
	return root, nil
}

func ensurePackStorageDirectories(root *os.Root) error {
	for _, dir := range []string{packArchiveRoot, packArchiveRoot + "/staging", packArchiveRoot + "/archives", packArchiveRoot + "/archives/sha256"} {
		if err := ensurePackStorageDirectory(root, dir); err != nil {
			return err
		}
	}
	return nil
}

func ensurePackStorageDirectory(root *os.Root, dir string) error {
	if err := root.Mkdir(dir, 0o700); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("create pack storage directory %q: %w", dir, err)
	}
	info, err := root.Lstat(dir)
	if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("pack storage directory %q must be a real directory", dir)
	}
	return nil
}

// WritePackStagingFile writes private bytes below the verified pack root. The
// purpose directory is fixed by this package, so callers cannot direct writes
// outside dataDir or through a symlinked indexer-packs component.
func WritePackStagingFile(dataDir, purpose string, data []byte) (string, error) {
	if purpose != "service-staging" && purpose != "portable-staging" {
		return "", fmt.Errorf("invalid pack staging purpose")
	}
	root, err := openPackStorageRoot(dataDir)
	if err != nil {
		return "", err
	}
	defer root.Close()
	if err := ensurePackStorageDirectory(root, packArchiveRoot); err != nil {
		return "", err
	}
	dir := packArchiveRoot + "/" + purpose
	if err := ensurePackStorageDirectory(root, dir); err != nil {
		return "", err
	}
	name, err := newPackStagingName()
	if err != nil {
		return "", err
	}
	name = dir + "/" + name
	file, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create pack staging file: %w", err)
	}
	written, writeErr := file.Write(data)
	syncErr := packImportFileSync(file)
	closeErr := file.Close()
	if writeErr != nil || syncErr != nil || closeErr != nil || written != len(data) {
		root.Remove(name)
		if written != len(data) && writeErr == nil {
			writeErr = io.ErrShortWrite
		}
		return "", fmt.Errorf("write pack staging file: %w", errors.Join(writeErr, syncErr, closeErr))
	}
	return filepath.Join(root.Name(), name), nil
}

// ReadInstalledPackArchive reads one canonical content-addressed object using
// the same no-symlink root and identity checks as pack publication.
func ReadInstalledPackArchive(dataDir, archiveRelPath, digest string) ([]byte, error) {
	if !strings.HasPrefix(archiveRelPath, "archives/sha256/") || !strings.HasSuffix(archiveRelPath, ".zip") {
		return nil, fmt.Errorf("installed pack archive path is not canonical")
	}
	root, err := openPackStorageRoot(dataDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	name := packArchiveRoot + "/" + archiveRelPath
	info, err := root.Lstat(name)
	if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Size() > MaxPackArchiveBytes {
		return nil, fmt.Errorf("installed pack archive has unsafe identity")
	}
	file, err := root.Open(name)
	if err != nil {
		return nil, fmt.Errorf("open installed pack archive: %w", err)
	}
	opened, statErr := file.Stat()
	data, readErr := io.ReadAll(io.LimitReader(file, MaxPackArchiveBytes+1))
	closeErr := file.Close()
	current, currentErr := root.Lstat(name)
	if statErr != nil || readErr != nil || closeErr != nil || currentErr != nil || !os.SameFile(info, opened) || !current.Mode().IsRegular() || !os.SameFile(opened, current) || int64(len(data)) > MaxPackArchiveBytes {
		return nil, fmt.Errorf("read installed pack archive safely: %w", errors.Join(statErr, readErr, closeErr, currentErr))
	}
	actual := sha256.Sum256(data)
	if "sha256:"+hex.EncodeToString(actual[:]) != digest {
		return nil, fmt.Errorf("installed pack archive digest does not match content address")
	}
	return data, nil
}

func copyPackArchiveToStaging(root *os.Root, archivePath string) (string, string, error) {
	input, err := openPackArchiveNoFollow(archivePath)
	if err != nil {
		return "", "", fmt.Errorf("open import archive: %w", err)
	}
	defer input.Close()
	opened, err := input.Stat()
	if err != nil || !opened.Mode().IsRegular() || opened.Size() > MaxPackArchiveBytes {
		return "", "", fmt.Errorf("import archive must be a regular file no larger than %d bytes", MaxPackArchiveBytes)
	}
	pathInfo, err := os.Lstat(archivePath)
	if err != nil || !pathInfo.Mode().IsRegular() || !os.SameFile(opened, pathInfo) {
		return "", "", fmt.Errorf("import archive path changed while opening")
	}

	stagedName, err := newPackStagingName()
	if err != nil {
		return "", "", err
	}
	stagedName = packArchiveRoot + "/staging/" + stagedName
	output, err := root.OpenFile(stagedName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", "", fmt.Errorf("create pack staging file: %w", err)
	}
	hash := sha256.New()
	written, copyErr := packImportCopy(io.MultiWriter(output, hash), io.LimitReader(input, MaxPackArchiveBytes+1))
	syncErr := packImportFileSync(output)
	closeErr := output.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil || written > MaxPackArchiveBytes {
		root.Remove(stagedName)
		return "", "", fmt.Errorf("copy import archive: %w", errors.Join(copyErr, syncErr, closeErr))
	}
	current, err := os.Lstat(archivePath)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(opened, current) {
		root.Remove(stagedName)
		return "", "", fmt.Errorf("import archive path changed during copy")
	}
	return stagedName, "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func newPackStagingName() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate pack staging name: %w", err)
	}
	return ".import-" + hex.EncodeToString(bytes), nil
}

func publishPackArchive(root *os.Root, stagedName, archiveRelPath, digest string) error {
	publishedName := packArchiveRoot + "/" + archiveRelPath
	lock := packPublicationMutex(publishedName)
	lock.Lock()
	defer lock.Unlock()

	// A previous crash may have completed the rename but not its directory
	// sync. Re-verify the exact content and finish syncing before success.
	if err := verifyPublishedPackArchive(root, publishedName, digest); err == nil {
		return syncPackDirectories(root, packArchiveRoot+"/staging", packArchiveRoot+"/archives/sha256", packArchiveRoot+"/archives", packArchiveRoot, ".")
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	publishedStaging, err := newPackStagingName()
	if err != nil {
		return err
	}
	publishedStaging = packArchiveRoot + "/staging/" + publishedStaging
	source, err := root.Open(stagedName)
	if err != nil {
		return fmt.Errorf("open staged signed pack archive: %w", err)
	}
	destination, err := root.OpenFile(publishedStaging, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		source.Close()
		return fmt.Errorf("create published signed pack staging: %w", err)
	}
	_, copyErr := packImportCopy(destination, source)
	syncErr, closeDestErr, closeSourceErr := packImportFileSync(destination), destination.Close(), source.Close()
	if copyErr != nil || syncErr != nil || closeDestErr != nil || closeSourceErr != nil {
		root.Remove(publishedStaging)
		return fmt.Errorf("durably copy signed pack archive: %w", errors.Join(copyErr, syncErr, closeDestErr, closeSourceErr))
	}
	if err := packImportRename(root, publishedStaging, publishedName); err != nil {
		root.Remove(publishedStaging)
		// No external writer may race this process. A destination that appeared
		// after a failed rename is therefore a crash-recovery final and must be
		// verified, never overwritten.
		if verifyErr := verifyPublishedPackArchive(root, publishedName, digest); verifyErr == nil {
			return syncPackDirectories(root, packArchiveRoot+"/staging", packArchiveRoot+"/archives/sha256", packArchiveRoot+"/archives", packArchiveRoot, ".")
		}
		return fmt.Errorf("publish signed pack archive: %w", err)
	}
	if err := syncPackDirectories(root, packArchiveRoot+"/staging", packArchiveRoot+"/archives/sha256", packArchiveRoot+"/archives", packArchiveRoot, "."); err != nil {
		return err
	}
	return verifyPublishedPackArchive(root, publishedName, digest)
}

func syncPackDirectories(root *os.Root, paths ...string) error {
	for _, name := range paths {
		dir, err := root.Open(name)
		if err != nil {
			return fmt.Errorf("open pack storage directory for sync: %w", err)
		}
		err = packImportDirectorySync(dir)
		closeErr := dir.Close()
		if err != nil || closeErr != nil {
			// Directory sync is intentionally required. Platforms/filesystems that
			// cannot guarantee it fail closed rather than claiming a durable pack.
			return fmt.Errorf("sync pack storage directory: %w", errors.Join(err, closeErr))
		}
	}
	return nil
}

func verifyPublishedPackArchive(root *os.Root, publishedName, digest string) error {
	info, err := root.Lstat(publishedName)
	if errors.Is(err, fs.ErrNotExist) {
		return fs.ErrNotExist
	}
	if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fmt.Errorf("published signed pack archive has unsafe identity")
	}
	published, err := root.Open(publishedName)
	if err != nil {
		return fmt.Errorf("open existing signed pack archive: %w", err)
	}
	opened, statErr := published.Stat()
	data, readErr := io.ReadAll(io.LimitReader(published, MaxPackArchiveBytes+1))
	closeErr := published.Close()
	if statErr != nil || readErr != nil || closeErr != nil || int64(len(data)) > MaxPackArchiveBytes || !os.SameFile(info, opened) {
		return fmt.Errorf("read existing signed pack archive safely: %w", errors.Join(statErr, readErr, closeErr))
	}
	actual := sha256.Sum256(data)
	if "sha256:"+hex.EncodeToString(actual[:]) != digest {
		return fmt.Errorf("existing signed pack archive digest does not match content address")
	}
	return nil
}
