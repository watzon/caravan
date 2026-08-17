package cardigann

import (
	"archive/zip"
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestOpenSignedPackArchiveClassifiesWithoutBecomingExecutable(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	archive := writeSignedPackArchive(t, privateKey, nil)

	candidate, err := OpenSignedPackArchive(archive, "1.0.0", StaticPackTrustStore{"test-key": publicKey})
	if err != nil {
		t.Fatalf("OpenSignedPackArchive: %v", err)
	}
	provider := candidate.Descriptor()
	if provider.Source != "community" || provider.Kind != SourceKindPack || provider.Revision != "2026.08.14" || provider.SignerKeyID != "test-key" || provider.ManifestDigest == "" {
		t.Fatalf("provider descriptor = %+v", provider)
	}
	descriptors := candidate.Definitions()
	if len(descriptors) != 2 {
		t.Fatalf("definitions = %d, want 2", len(descriptors))
	}
	if got := []string{descriptors[0].Ref.String(), descriptors[1].Ref.String()}; !slices.Equal(got, []string{"community:first", "community:second"}) {
		t.Fatalf("definition order = %v", got)
	}
	if descriptors[0].MetadataID != "first-site" || descriptors[0].State != DefinitionStateRunnableUnverified || descriptors[1].State != DefinitionStateUnsupported {
		t.Fatalf("descriptors = %+v", descriptors)
	}
	// A validated archive is classification input, not an executable Provider.
	if _, ok := any(candidate).(Provider); ok {
		t.Fatal("signed pack candidate implements Provider and can bypass activation")
	}
}

func TestOpenSignedPackArchiveQuarantinesCompilerInvalidDefinition(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	compilerInvalid := []byte(`id: first
name: First
links: [https://tracker.example]
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search}]
  rows: {selector: article}
  fields:
    title: {selector: h2, filters: [{name: dateparse}]}
    download: {selector: a, attribute: href}
`)
	archive := writeSignedPackArchive(t, privateKey, func(f *packFixture) {
		f.documents = f.documents[:1]
		f.manifest.Definitions = f.manifest.Definitions[:1]
		f.documents[0] = compilerInvalid
		delete(f.files, "definitions/second.yml")
	})

	candidate, err := OpenSignedPackArchive(archive, "1.0.0", StaticPackTrustStore{"test-key": publicKey})
	if err != nil {
		t.Fatalf("OpenSignedPackArchive compiler-invalid definition: %v", err)
	}
	descriptors := candidate.Definitions()
	if len(descriptors) != 1 || descriptors[0].State != DefinitionStateQuarantined || !slices.Contains(descriptors[0].Unsupported, CapabilityCode("compiler.invalid")) {
		t.Fatalf("descriptors = %+v, want one compiler.invalid quarantined definition", descriptors)
	}
	if _, err := ParseDefinition(compilerInvalid); err == nil {
		t.Fatal("compiler-invalid fixture unexpectedly passes strict execution ParseDefinition")
	}
}

func TestOpenSignedPackArchiveQuarantinesJSONSlashEscapeCompilerIncompatibility(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	compilerInvalid := []byte("id: first\nname: First\ndescription: \"x\\/y\"\nlinks: [https://tracker.example]\nlogin: {path: /login}\n")
	archive := writeSignedPackArchive(t, privateKey, func(f *packFixture) {
		f.documents = f.documents[:1]
		f.manifest.Definitions = f.manifest.Definitions[:1]
		f.documents[0] = compilerInvalid
		delete(f.files, "definitions/second.yml")
	})

	candidate, err := OpenSignedPackArchive(archive, "1.0.0", StaticPackTrustStore{"test-key": publicKey})
	if err != nil {
		t.Fatalf("OpenSignedPackArchive JSON-slash compiler incompatibility: %v", err)
	}
	descriptors := candidate.Definitions()
	if len(descriptors) != 1 || descriptors[0].State != DefinitionStateQuarantined || !slices.Contains(descriptors[0].Unsupported, CapabilityCode("compiler.invalid")) {
		t.Fatalf("descriptors = %+v, want one compiler.invalid quarantined definition", descriptors)
	}
	if _, err := ParseDefinition(compilerInvalid); err == nil {
		t.Fatal("JSON-slash fixture unexpectedly passes strict execution ParseDefinition")
	}
}

func TestOpenSignedPackArchiveRejectsSyntaxInvalidAndAmbiguousYAMLAtomically(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := StaticPackTrustStore{"test-key": publicKey}
	for name, document := range map[string][]byte{
		"malformed":     []byte("id: first\nname: [\nlinks: [https://tracker.example]\n"),
		"duplicate key": []byte("id: first\nid: second\nname: First\nlinks: [https://tracker.example]\n"),
	} {
		t.Run(name, func(t *testing.T) {
			archive := writeSignedPackArchive(t, privateKey, func(f *packFixture) {
				f.documents[0] = document
			})
			if candidate, err := OpenSignedPackArchive(archive, "1.0.0", trust); err == nil || candidate != nil {
				t.Fatalf("invalid YAML accepted atomically: candidate=%#v err=%v", candidate, err)
			}
		})
	}
}

func TestOpenSignedPackArchiveRejectsIdentityAndOriginPolicyFailuresAtomically(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := StaticPackTrustStore{"test-key": publicKey}
	for name, mutate := range map[string]func(*packFixture){
		"id mismatch": func(f *packFixture) {
			f.documents[0] = []byte(runnablePackDefinition("different", "https://tracker.example"))
		},
		"missing approved origins": func(f *packFixture) {
			f.manifest.Definitions[0].ApprovedOrigins = nil
		},
		"invalid approved origin": func(f *packFixture) {
			f.manifest.Definitions[0].ApprovedOrigins = []string{"https://tracker.example/path"}
		},
	} {
		t.Run(name, func(t *testing.T) {
			archive := writeSignedPackArchive(t, privateKey, mutate)
			if candidate, err := OpenSignedPackArchive(archive, "1.0.0", trust); err == nil || candidate != nil {
				t.Fatalf("invalid signed sibling accepted atomically: candidate=%#v err=%v", candidate, err)
			}
		})
	}
}

func TestImportSignedPackArchivePublishesOnlyTheAuthenticatedBytes(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	archive := writeSignedPackArchive(t, privateKey, nil)
	dataDir := t.TempDir()
	candidate, err := OpenSignedPackArchive(archive, "1.0.0", StaticPackTrustStore{"test-key": publicKey})
	if err != nil {
		t.Fatal(err)
	}
	requirements := candidate.LicenseAcceptanceRequirements()

	imported, err := ImportSignedPackArchive(PackImportRequest{
		DataDir:               dataDir,
		ArchivePath:           archive,
		CurrentCaravanVersion: "1.0.0",
		Trust:                 StaticPackTrustStore{"test-key": publicKey},
		Acceptance: &PackLicenseAcceptance{
			ManifestDigest: requirements.ManifestDigest, LicenseDigest: requirements.LicenseDigest,
			SignerKeyFingerprint: requirements.SignerKeyFingerprint, AcceptedAt: time.Unix(1, 0),
		},
	})
	if err != nil {
		t.Fatalf("ImportSignedPackArchive: %v", err)
	}
	if imported.ArchiveDigest == "" || imported.ArchiveRelPath == "" || imported.Candidate == nil {
		t.Fatalf("import result = %+v", imported)
	}
	published, err := os.ReadFile(filepath.Join(dataDir, "indexer-packs", imported.ArchiveRelPath))
	if err != nil {
		t.Fatalf("read published archive: %v", err)
	}
	original, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(published, original) {
		t.Fatal("published archive does not contain the exact authenticated input bytes")
	}
	if got := "sha256:" + digestHex(published); got != imported.ArchiveDigest {
		t.Fatalf("published archive digest = %q, want %q", got, imported.ArchiveDigest)
	}
}

func TestImportSignedPackArchiveRecoversFromStalePublicationLockAndSerializesSameDigest(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	archive := writeSignedPackArchive(t, privateKey, nil)
	dataDir := t.TempDir()
	candidate, err := OpenSignedPackArchive(archive, "1.0.0", StaticPackTrustStore{"test-key": publicKey})
	if err != nil {
		t.Fatal(err)
	}
	requirements := candidate.LicenseAcceptanceRequirements()
	request := PackImportRequest{DataDir: dataDir, ArchivePath: archive, CurrentCaravanVersion: "1.0.0", Trust: StaticPackTrustStore{"test-key": publicKey}, Acceptance: &PackLicenseAcceptance{ManifestDigest: requirements.ManifestDigest, LicenseDigest: requirements.LicenseDigest, SignerKeyFingerprint: requirements.SignerKeyFingerprint, AcceptedAt: time.Unix(1, 0)}}

	archiveBytes, err := os.ReadFile(archive)
	if err != nil {
		t.Fatal(err)
	}
	// The old protocol left this permanent file after a crash. A retry must not
	// wait for or trust it.
	stale := filepath.Join(dataDir, packArchiveRoot, "archives", "sha256", digestHex(archiveBytes)+".zip.lock")
	if err := os.MkdirAll(filepath.Dir(stale), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stale, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}

	const importers = 8
	results := make(chan *PackImportResult, importers)
	errs := make(chan error, importers)
	for range importers {
		go func() {
			result, err := ImportSignedPackArchive(request)
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}
	for range importers {
		select {
		case err := <-errs:
			t.Fatalf("concurrent import: %v", err)
		case result := <-results:
			if result.ArchiveDigest == "" {
				t.Fatal("concurrent import returned before publication")
			}
		}
	}
	if _, err := os.Stat(stale); err != nil {
		t.Fatalf("unrelated stale lock was changed: %v", err)
	}
}

func TestImportSignedPackArchiveFailsClosedOnPublicationFailureAndRetrySucceeds(t *testing.T) {
	for _, test := range []struct {
		name   string
		inject func()
	}{
		{name: "copy", inject: func() {
			packImportCopy = func(io.Writer, io.Reader) (int64, error) { return 0, errors.New("injected copy failure") }
		}},
		{name: "rename", inject: func() {
			packImportRename = func(*os.Root, string, string) error { return errors.New("injected rename failure") }
		}},
		{name: "file sync", inject: func() { packImportFileSync = func(*os.File) error { return errors.New("injected file sync failure") } }},
		{name: "directory sync", inject: func() {
			packImportDirectorySync = func(*os.File) error { return errors.New("injected directory sync failure") }
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			archive := writeSignedPackArchive(t, privateKey, nil)
			candidate, err := OpenSignedPackArchive(archive, "1.0.0", StaticPackTrustStore{"test-key": publicKey})
			if err != nil {
				t.Fatal(err)
			}
			requirements := candidate.LicenseAcceptanceRequirements()
			request := PackImportRequest{DataDir: t.TempDir(), ArchivePath: archive, CurrentCaravanVersion: "1.0.0", Trust: StaticPackTrustStore{"test-key": publicKey}, Acceptance: &PackLicenseAcceptance{ManifestDigest: requirements.ManifestDigest, LicenseDigest: requirements.LicenseDigest, SignerKeyFingerprint: requirements.SignerKeyFingerprint, AcceptedAt: time.Unix(1, 0)}}
			copyFn, renameFn, fileSync, dirSync := packImportCopy, packImportRename, packImportFileSync, packImportDirectorySync
			t.Cleanup(func() {
				packImportCopy, packImportRename, packImportFileSync, packImportDirectorySync = copyFn, renameFn, fileSync, dirSync
			})
			test.inject()
			if result, err := ImportSignedPackArchive(request); err == nil || result != nil {
				t.Fatalf("injected %s failure returned result=%#v err=%v", test.name, result, err)
			}
			packImportCopy, packImportRename, packImportFileSync, packImportDirectorySync = copyFn, renameFn, fileSync, dirSync
			if result, err := ImportSignedPackArchive(request); err != nil || result == nil {
				t.Fatalf("retry after %s failure result=%#v err=%v", test.name, result, err)
			}
		})
	}
}

func TestPackImportResultCreatesCompleteVerifiedInstallReceipt(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	archive := writeSignedPackArchive(t, privateKey, nil)
	candidate, err := OpenSignedPackArchive(archive, "1.0.0", StaticPackTrustStore{"test-key": publicKey})
	if err != nil {
		t.Fatal(err)
	}
	requirements := candidate.LicenseAcceptanceRequirements()
	imported, err := ImportSignedPackArchive(PackImportRequest{
		DataDir: t.TempDir(), ArchivePath: archive, CurrentCaravanVersion: "1.0.0", Trust: StaticPackTrustStore{"test-key": publicKey},
		Acceptance: &PackLicenseAcceptance{ManifestDigest: requirements.ManifestDigest, LicenseDigest: requirements.LicenseDigest, SignerKeyFingerprint: requirements.SignerKeyFingerprint, AcceptedAt: time.Unix(1, 0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := imported.InstallReceipt(time.Unix(2, 0), 42)
	if err != nil {
		t.Fatal(err)
	}
	revision, entries := receipt.Revision(), receipt.Entries()
	if revision.Source != "community" || revision.ArchiveDigest != imported.ArchiveDigest || revision.ManifestDigest != requirements.ManifestDigest || revision.LicenseDigest != requirements.LicenseDigest || revision.SignerKeyID != "test-key" || revision.AcceptedByUserID != 42 || len(entries) != 2 {
		t.Fatalf("verified receipt was incomplete: %+v %#v", revision, entries)
	}
	if revision.Pending || revision.Active || revision.LastKnownGood || revision.ValidationError != "" || revision.InstallState != "installed" {
		t.Fatalf("verified receipt exposed caller lifecycle state: %+v", revision)
	}
}

func TestImportSignedPackArchiveRequiresExplicitOwnerLicenseAcceptance(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	archive := writeSignedPackArchive(t, privateKey, nil)
	_, err = ImportSignedPackArchive(PackImportRequest{
		DataDir: t.TempDir(), ArchivePath: archive, CurrentCaravanVersion: "1.0.0",
		Trust: StaticPackTrustStore{"test-key": publicKey},
	})
	if err == nil || !strings.Contains(err.Error(), "license acceptance") {
		t.Fatalf("import without owner license acceptance error = %v", err)
	}
}

func TestInstalledPackProviderLoadsOnlyExactPinnedDefinitions(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	archive := writeSignedPackArchive(t, privateKey, nil)
	candidate, err := OpenSignedPackArchive(archive, "1.0.0", StaticPackTrustStore{"test-key": publicKey})
	if err != nil {
		t.Fatal(err)
	}
	requirements := candidate.LicenseAcceptanceRequirements()
	dataDir := t.TempDir()
	imported, err := ImportSignedPackArchive(PackImportRequest{
		DataDir: dataDir, ArchivePath: archive, CurrentCaravanVersion: "1.0.0", Trust: StaticPackTrustStore{"test-key": publicKey},
		Acceptance: &PackLicenseAcceptance{ManifestDigest: requirements.ManifestDigest, LicenseDigest: requirements.LicenseDigest, SignerKeyFingerprint: requirements.SignerKeyFingerprint, AcceptedAt: time.Unix(1, 0)},
	})
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]PackRuntimeEntry, 0)
	for _, descriptor := range imported.Candidate.Definitions() {
		entries = append(entries, PackRuntimeEntry{DefinitionRef: descriptor.Ref.String(), Path: descriptor.Path, Digest: descriptor.Digest, State: descriptor.State, ApprovedOrigins: descriptor.ApprovedOrigins})
	}
	provider, err := OpenInstalledPackProvider(filepath.Join(dataDir, packArchiveRoot, imported.ArchiveRelPath), imported.ArchiveDigest, "community", "2026.08.14", entries)
	if err != nil {
		t.Fatalf("OpenInstalledPackProvider: %v", err)
	}
	registry, _, err := LoadProviders(provider)
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	if _, ok := registry.GetExactPack("community:first", "community", "2026.08.14", entries[0].Digest); !ok {
		t.Fatal("exact stored pin did not resolve")
	}
	if _, ok := registry.GetExactPack("community:first", "community", "2026.08.14", "sha256:"+strings.Repeat("0", 64)); ok {
		t.Fatal("wrong stored digest resolved through fallback")
	}
	if _, ok := registry.Get("community:second"); ok {
		t.Fatal("unsupported definition became executable")
	}
	entries[0].Digest = "sha256:" + strings.Repeat("0", 64)
	if invalid, err := OpenInstalledPackProvider(filepath.Join(dataDir, packArchiveRoot, imported.ArchiveRelPath), imported.ArchiveDigest, "community", "2026.08.14", entries); err == nil || invalid != nil {
		t.Fatalf("mismatched persisted entry digest accepted: %v", err)
	}
}

func TestOpenSignedPackArchiveRejectsUnknownSignerAndBadSignature(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	archive := writeSignedPackArchive(t, privateKey, nil)
	if candidate, err := OpenSignedPackArchive(archive, "1.0.0", StaticPackTrustStore{}); err == nil || candidate != nil {
		t.Fatalf("unknown signer accepted: %#v, %v", candidate, err)
	}

	otherPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if candidate, err := OpenSignedPackArchive(archive, "1.0.0", StaticPackTrustStore{"test-key": otherPublic}); err == nil || candidate != nil {
		t.Fatalf("bad signature accepted: %#v, %v", candidate, err)
	}

	if len(publicKey) != ed25519.PublicKeySize {
		t.Fatal("invalid generated public key")
	}
}

func TestOpenSignedPackArchiveRejectsOriginOutsideSignedAllowlist(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	archive := writeSignedPackArchive(t, privateKey, func(f *packFixture) {
		f.documents[0] = []byte(runnablePackDefinition("first", "https://evil.example"))
	})
	if candidate, err := OpenSignedPackArchive(archive, "1.0.0", StaticPackTrustStore{"test-key": publicKey}); err == nil || candidate != nil {
		t.Fatalf("unapproved definition origin accepted: %#v, %v", candidate, err)
	}
}

func TestOpenSignedPackArchiveRejectsDigestMismatchAndMalformedSiblingAtomically(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := StaticPackTrustStore{"test-key": publicKey}
	tests := map[string]func(*packFixture){
		"digest mismatch": func(f *packFixture) {
			f.documents[0] = nil
			f.files["definitions/first.yml"] = []byte("different bytes")
		},
		"malformed sibling": func(f *packFixture) {
			f.documents[1] = []byte("id: second\nname: [\n")
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			archive := writeSignedPackArchive(t, privateKey, mutate)
			if candidate, err := OpenSignedPackArchive(archive, "1.0.0", trust); err == nil || candidate != nil {
				t.Fatalf("invalid sibling accepted: %#v, %v", candidate, err)
			}
		})
	}
}

func TestOpenSignedPackArchiveRejectsExtraMissingAndTraversalMembers(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := StaticPackTrustStore{"test-key": publicKey}
	tests := map[string]func(*packFixture){
		"extra":                      func(f *packFixture) { f.extra["unexpected.txt"] = []byte("extra") },
		"missing":                    func(f *packFixture) { delete(f.files, "LICENSE") },
		"traversal":                  func(f *packFixture) { f.extra["../escape.yml"] = []byte("escape") },
		"duplicate case-folded path": func(f *packFixture) { f.extra["Definitions/first.yml"] = f.documents[0] },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			archive := writeSignedPackArchive(t, privateKey, mutate)
			if candidate, err := OpenSignedPackArchive(archive, "1.0.0", trust); err == nil || candidate != nil {
				t.Fatalf("invalid archive accepted: %#v, %v", candidate, err)
			}
		})
	}
}

func TestOpenSignedPackArchiveMapsDevelopmentVersionToZeroAndRejectsNewerRequirement(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	archive := writeSignedPackArchive(t, privateKey, func(f *packFixture) {
		f.manifest.MinimumCaravanVersion = "0.0.1"
	})
	candidate, err := OpenSignedPackArchive(archive, "dev", StaticPackTrustStore{"test-key": publicKey})
	if err == nil || candidate != nil || !strings.Contains(err.Error(), "requires Caravan 0.0.1") {
		t.Fatalf("development build accepted newer requirement: %#v, %v", candidate, err)
	}
}

func TestOpenSignedPackArchiveRejectsReservedSourceAndNewerCaravanRequirement(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := StaticPackTrustStore{"test-key": publicKey}
	reserved := writeSignedPackArchive(t, privateKey, func(f *packFixture) { f.manifest.Source = BuiltinSource })
	if candidate, err := OpenSignedPackArchive(reserved, "1.0.0", trust); err == nil || candidate != nil {
		t.Fatalf("reserved source accepted: %#v, %v", candidate, err)
	}
	newer := writeSignedPackArchive(t, privateKey, func(f *packFixture) { f.manifest.MinimumCaravanVersion = "2.0.0" })
	if candidate, err := OpenSignedPackArchive(newer, "1.0.0", trust); err == nil || candidate != nil {
		t.Fatalf("newer Caravan requirement accepted: %#v, %v", candidate, err)
	}
}

func TestOpenSignedPackArchiveRejectsDuplicateManifestKeys(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	trust := StaticPackTrustStore{"test-key": publicKey}
	tests := map[string]struct {
		old string
		new string
	}{
		"top level": {old: `"source":"community"`, new: `"source":"community","source":"evil"`},
		"nested":    {old: `"path":"LICENSE"`, new: `"path":"LICENSE","path":"other"`},
	}
	for name, replacement := range tests {
		t.Run(name, func(t *testing.T) {
			archive := writeSignedPackArchive(t, privateKey, func(f *packFixture) {
				f.rewriteManifest = func(data []byte) []byte {
					return bytes.Replace(data, []byte(replacement.old), []byte(replacement.new), 1)
				}
			})
			if candidate, err := OpenSignedPackArchive(archive, "1.0.0", trust); err == nil || candidate != nil {
				t.Fatalf("duplicate manifest key accepted: %#v, %v", candidate, err)
			}
		})
	}
}

func TestOpenSignedPackArchiveRejectsSymlinkPath(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	archive := writeSignedPackArchive(t, privateKey, nil)
	link := filepath.Join(t.TempDir(), "pack-link")
	if err := os.Symlink(archive, link); err != nil {
		t.Fatal(err)
	}
	if candidate, err := OpenSignedPackArchive(link, "1.0.0", StaticPackTrustStore{"test-key": publicKey}); err == nil || candidate != nil {
		t.Fatalf("symlink archive accepted: %#v, %v", candidate, err)
	}
}

func TestSignedPackOriginPolicyRejectsConfiguredBaseOutsideAllowlist(t *testing.T) {
	definition := &Definition{
		ID: "fixture", Name: "Fixture", Type: "public", Links: []string{"https://tracker.example"},
		approvedOrigins: []string{"https://tracker.example"},
		Search:          searchBlock{Paths: []pathBlock{{Path: "/search"}}, Rows: rowsBlock{Selector: "article"}, Fields: map[string]fieldBlock{"title": {Selector: "h2"}, "download": {Selector: "a", Attribute: "href"}}},
	}
	if _, err := New(definition, Config{BaseURL: "https://other.example"}, nil); err == nil || strings.Contains(err.Error(), "signed") || !strings.Contains(err.Error(), "supported tracker URLs") {
		t.Fatalf("unapproved configured base error = %v", err)
	}
}

func TestOwnerPackArchiveCurrentCompilerOptIn(t *testing.T) {
	archive := os.Getenv("CARAVAN_OWNER_PACK_ARCHIVE")
	publicKeyPath := os.Getenv("CARAVAN_OWNER_PACK_PUBLIC_KEY")
	if archive == "" || publicKeyPath == "" {
		t.Skip("set CARAVAN_OWNER_PACK_ARCHIVE and CARAVAN_OWNER_PACK_PUBLIC_KEY to run owner-pack acceptance")
	}
	encoded, err := os.ReadFile(publicKeyPath)
	if err != nil {
		t.Fatalf("read owner public key: %v", err)
	}
	publicKey, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(encoded)))
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		t.Fatalf("decode owner public key: length=%d err=%v", len(publicKey), err)
	}
	candidate, err := OpenSignedPackArchive(archive, "dev", StaticPackTrustStore{"owner-local-v11-20260815": ed25519.PublicKey(publicKey)})
	if err != nil {
		t.Fatalf("OpenSignedPackArchive owner pack: %v", err)
	}
	descriptors := candidate.Definitions()
	runnable := 0
	for _, descriptor := range descriptors {
		if descriptor.State == DefinitionStateRunnableUnverified {
			runnable++
		}
	}
	if len(descriptors) != 542 || runnable != 0 {
		t.Fatalf("owner pack descriptors/runnable = %d/%d, want 542/0", len(descriptors), runnable)
	}
}

func TestPreflightPackArchiveAggregateRejectsInflationBeforeReading(t *testing.T) {
	files := map[string]*zip.File{
		"first":  {FileHeader: zip.FileHeader{UncompressedSize64: uint64(MaxPackDefinitionAggregateBytes)}},
		"second": {FileHeader: zip.FileHeader{UncompressedSize64: 1}},
	}
	if err := preflightPackArchiveAggregate(files); err == nil {
		t.Fatal("aggregate larger than fixed limit accepted")
	}
	delete(files, "second")
	if err := preflightPackArchiveAggregate(files); err != nil {
		t.Fatalf("aggregate at fixed limit rejected: %v", err)
	}
}

type packFixture struct {
	manifest        signedPackManifest
	documents       [][]byte
	files           map[string][]byte
	extra           map[string][]byte
	rewriteManifest func([]byte) []byte
}

func writeSignedPackArchive(t *testing.T, privateKey ed25519.PrivateKey, mutate func(*packFixture)) string {
	t.Helper()
	documents := [][]byte{
		[]byte(runnablePackDefinition("first", "https://tracker.example")),
		[]byte("id: second\nname: Second\nlinks: [https://tracker.example]\nlogin: {path: /login}\n"),
	}
	license := []byte("Synthetic test license\n")
	fixture := packFixture{
		manifest: signedPackManifest{
			FormatVersion:          1,
			CardigannSchemaVersion: 1,
			Source:                 "community",
			Revision:               "2026.08.14",
			SPDXLicenseExpression:  "MIT",
			Provenance:             "synthetic test pack",
			SignerKeyID:            "test-key",
			MinimumCaravanVersion:  "0.1.0",
			License:                signedPackFile{Path: "LICENSE", SHA256: digestHex(license)},
			Definitions: []signedPackDefinition{
				{ID: "first", MetadataID: "first-site", Path: "definitions/first.yml", SHA256: digestHex(documents[0]), ApprovedOrigins: []string{"https://tracker.example"}},
				{ID: "second", MetadataID: "second-site", Path: "definitions/second.yml", SHA256: digestHex(documents[1]), ApprovedOrigins: []string{"https://tracker.example"}},
			},
		},
		documents: documents,
		files: map[string][]byte{
			"LICENSE":                license,
			"definitions/first.yml":  documents[0],
			"definitions/second.yml": documents[1],
		},
		extra: map[string][]byte{},
	}
	if mutate != nil {
		mutate(&fixture)
	}
	// Keep manifest digests synchronized with any fixture document mutation.
	for i := range fixture.manifest.Definitions {
		path := fixture.manifest.Definitions[i].Path
		if i < len(fixture.documents) && fixture.documents[i] != nil {
			fixture.files[path] = fixture.documents[i]
			fixture.manifest.Definitions[i].SHA256 = digestHex(fixture.documents[i])
		}
	}
	fixture.manifest.TotalFiles = len(fixture.files)
	for _, data := range fixture.files {
		fixture.manifest.TotalUncompressedBytes += int64(len(data))
	}
	manifestBytes, err := json.Marshal(fixture.manifest)
	if err != nil {
		t.Fatal(err)
	}
	if fixture.rewriteManifest != nil {
		manifestBytes = fixture.rewriteManifest(manifestBytes)
	}
	signature := ed25519.Sign(privateKey, manifestBytes)

	archivePath := filepath.Join(t.TempDir(), "fixture.caravan-indexer-pack")
	file, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	entries := map[string][]byte{"manifest.json": manifestBytes, "manifest.sig": signature}
	for name, data := range fixture.files {
		entries[name] = data
	}
	for name, data := range fixture.extra {
		entries[name] = data
	}
	for name, data := range entries {
		header := &zip.FileHeader{Name: name, Method: zip.Store}
		header.SetMode(0o600)
		part, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return archivePath
}

func runnablePackDefinition(id, origin string) string {
	return "id: " + id + "\nname: " + id + "\nlinks: [" + origin + "]\ncaps: {modes: {search: [q]}}\nsearch:\n  paths: [{path: /search}]\n  rows: {selector: article}\n  fields:\n    title: {selector: h2}\n    download: {selector: a, attribute: href}\n"
}

func digestHex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
