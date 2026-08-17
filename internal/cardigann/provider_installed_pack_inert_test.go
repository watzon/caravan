package cardigann

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"
)

func TestVerifyInstalledPackArchiveAllowsVerifiedInertPack(t *testing.T) {
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	archive := writeSignedPackArchive(t, privateKey, func(f *packFixture) {
		f.documents[0] = []byte("id: first\nname: First\nlinks: [https://tracker.example]\nlogin: {path: /login}\n")
	})
	candidate, err := OpenSignedPackArchive(archive, "1.0.0", StaticPackTrustStore{"test-key": publicKey})
	if err != nil {
		t.Fatalf("OpenSignedPackArchive: %v", err)
	}
	request := PackImportRequest{DataDir: t.TempDir(), ArchivePath: archive, CurrentCaravanVersion: "1.0.0", Trust: StaticPackTrustStore{"test-key": publicKey}, Acceptance: &PackLicenseAcceptance{ManifestDigest: candidate.Descriptor().ManifestDigest, LicenseDigest: candidate.LicenseAcceptanceRequirements().LicenseDigest, SignerKeyFingerprint: candidate.LicenseAcceptanceRequirements().SignerKeyFingerprint, AcceptedAt: time.Unix(1, 0)}}
	imported, err := ImportSignedPackArchive(request)
	if err != nil {
		t.Fatalf("ImportSignedPackArchive: %v", err)
	}
	receipt, err := imported.InstallReceipt(time.Unix(2, 0), 1)
	if err != nil {
		t.Fatalf("InstallReceipt: %v", err)
	}
	revision, entries := receipt.Revision(), receipt.Entries()
	if revision.RunnableCount != 0 {
		t.Fatalf("runnable count = %d, want 0", revision.RunnableCount)
	}
	if err := VerifyInstalledPackArchive(archive, "1.0.0", revision, entries); err != nil {
		t.Fatalf("VerifyInstalledPackArchive: %v", err)
	}
	provider, err := OpenVerifiedInstalledPackProvider(archive, "1.0.0", revision, entries)
	if err != nil {
		t.Fatalf("OpenVerifiedInstalledPackProvider: %v", err)
	}
	documents, err := provider.Documents()
	if err != nil {
		t.Fatalf("Documents: %v", err)
	}
	if len(documents) != 0 {
		t.Fatalf("documents = %d, want 0", len(documents))
	}
}
