package packs

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

const (
	testPackSource = "example.test"
	testPackKeyID  = "example-test-key"
)

type testSignedPackOptions struct {
	Source, Revision, KeyID string
	Definition              []byte
	License                 []byte
	PublicKey               ed25519.PublicKey
	PrivateKey              ed25519.PrivateKey
}

type testSignedPack struct {
	Archive                 []byte
	PublicKey               ed25519.PublicKey
	PrivateKey              ed25519.PrivateKey
	Source, Revision, KeyID string
}

type testSignedPackManifest struct {
	FormatVersion          int                        `json:"format_version"`
	CardigannSchemaVersion int                        `json:"cardigann_schema_version"`
	Source                 string                     `json:"source"`
	Revision               string                     `json:"revision"`
	SPDXLicenseExpression  string                     `json:"spdx_license_expression"`
	Provenance             string                     `json:"provenance"`
	SignerKeyID            string                     `json:"signer_key_id"`
	MinimumCaravanVersion  string                     `json:"minimum_caravan_version"`
	TotalFiles             int                        `json:"total_files"`
	TotalUncompressedBytes int64                      `json:"total_uncompressed_bytes"`
	License                testSignedPackFile         `json:"license"`
	Definitions            []testSignedPackDefinition `json:"definitions"`
}

type testSignedPackFile struct {
	Path, SHA256 string
}

type testSignedPackDefinition struct {
	ID, MetadataID, Path, SHA256 string
	ApprovedOrigins              []string
}

func (f testSignedPackDefinition) MarshalJSON() ([]byte, error) {
	type wire struct {
		ID              string   `json:"id"`
		MetadataID      string   `json:"metadata_id"`
		Path            string   `json:"path"`
		SHA256          string   `json:"sha256"`
		ApprovedOrigins []string `json:"approved_origins"`
	}
	return json.Marshal(wire{f.ID, f.MetadataID, f.Path, f.SHA256, f.ApprovedOrigins})
}

func makeTestSignedPack(t *testing.T, options testSignedPackOptions) testSignedPack {
	t.Helper()
	if options.Source == "" {
		options.Source = testPackSource
	}
	if options.Revision == "" {
		options.Revision = "v1"
	}
	if options.KeyID == "" {
		options.KeyID = testPackKeyID
	}
	if options.Definition == nil {
		options.Definition = runnableTestPackDefinition()
	}
	if options.License == nil {
		options.License = []byte("Invented example.test fixture license\n")
	}
	if len(options.PrivateKey) == 0 {
		var err error
		options.PublicKey, options.PrivateKey, err = ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(options.PublicKey) == 0 {
		options.PublicKey = append(ed25519.PublicKey(nil), options.PrivateKey.Public().(ed25519.PublicKey)...)
	}
	definitionPath := "definitions/fixture.yml"
	manifest := testSignedPackManifest{
		FormatVersion: 1, CardigannSchemaVersion: 1, Source: options.Source, Revision: options.Revision,
		SPDXLicenseExpression: "MIT", Provenance: "invented example.test test fixture", SignerKeyID: options.KeyID,
		MinimumCaravanVersion: "0.1.0", TotalFiles: 2,
		TotalUncompressedBytes: int64(len(options.License) + len(options.Definition)),
		License:                testSignedPackFile{Path: "LICENSE", SHA256: testDigestHex(options.License)},
		Definitions: []testSignedPackDefinition{{
			ID: "fixture", MetadataID: "fixture-metadata", Path: definitionPath,
			SHA256: testDigestHex(options.Definition), ApprovedOrigins: []string{"https://tracker.example"},
		}},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	signature := ed25519.Sign(options.PrivateKey, manifestBytes)
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for _, entry := range []struct {
		name string
		data []byte
	}{{"manifest.json", manifestBytes}, {"manifest.sig", signature}, {"LICENSE", options.License}, {definitionPath, options.Definition}} {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		header.SetMode(0o600)
		part, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return testSignedPack{Archive: archive.Bytes(), PublicKey: options.PublicKey, PrivateKey: options.PrivateKey, Source: options.Source, Revision: options.Revision, KeyID: options.KeyID}
}

func runnableTestPackDefinition() []byte {
	return []byte("id: fixture\nname: Fixture\nlinks: [https://tracker.example]\ncaps: {modes: {search: [q]}}\nsearch:\n  paths: [{path: /search}]\n  rows: {selector: article}\n  fields:\n    title: {selector: h2}\n    download: {selector: a, attribute: href}\n")
}

func inertCompilerInvalidTestPackDefinition() []byte {
	return []byte("id: fixture\nname: Fixture\nlinks: [https://tracker.example]\ncaps: {modes: {search: [q]}}\nsearch:\n  paths: [{path: /search}]\n  rows: {selector: article}\n  fields:\n    title: {selector: h2, filters: [{name: dateparse}]}\n    download: {selector: a, attribute: href}\n")
}

func testDigestHex(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func newTestPackService(t *testing.T, now *time.Time) (*Service, *store.Store, string) {
	t.Helper()
	root := t.TempDir()
	st, err := store.Open(filepath.Join(root, "caravan.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	dataDir := filepath.Join(root, "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	svc := &Service{Store: st, DataDir: dataDir, Version: "1.0.0", PreviewTTL: time.Minute, Now: func() time.Time { return *now }}
	return svc, st, root
}

func previewAndInstall(t *testing.T, svc *Service, actor int64, fixture testSignedPack) (Preview, Status) {
	t.Helper()
	preview, err := svc.Preview(context.Background(), actor, fixture.KeyID, fixture.PublicKey, fixture.Archive)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	status, err := svc.AcceptAndInstall(context.Background(), actor, fixture.Source, preview.Token, fixture.KeyID, fixture.PublicKey, fixture.Archive)
	if err != nil {
		t.Fatalf("AcceptAndInstall: %v", err)
	}
	return preview, status
}

func TestPreviewRejectsNonpositiveActorBeforeReadingUpload(t *testing.T) {
	svc := Service{}
	for _, actor := range []int64{0, -1} {
		_, err := svc.Preview(context.Background(), actor, "key", make([]byte, 32), []byte("not an archive"))
		if err == nil || !strings.Contains(err.Error(), "actor") {
			t.Fatalf("Preview(%d) error = %v, want actor validation", actor, err)
		}
	}
}

func TestAcceptAndInstallRejectsNonpositiveActorBeforeTokenUse(t *testing.T) {
	svc := Service{}
	for _, actor := range []int64{0, -1} {
		_, err := svc.AcceptAndInstall(context.Background(), actor, testPackSource, "token", "key", make([]byte, 32), []byte("not an archive"))
		if err == nil || !strings.Contains(err.Error(), "actor") {
			t.Fatalf("AcceptAndInstall(%d) error = %v, want actor validation", actor, err)
		}
	}
}

func TestServiceAcceptAndInstallPersistsServerDerivedReceipt(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	svc, st, _ := newTestPackService(t, &now)
	fixture := makeTestSignedPack(t, testSignedPackOptions{})
	_, got := previewAndInstall(t, svc, 41, fixture)
	if got.Source != fixture.Source || got.Revision != fixture.Revision || got.State != core.DefinitionPackInstalled {
		t.Fatalf("status = %+v", got)
	}
	revision, err := st.GetDefinitionPackRevision(context.Background(), fixture.Source, fixture.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if revision.AcceptedByUserID != 41 || !revision.AcceptedAt.Equal(now) || !revision.InstalledAt.Equal(now) || !bytes.Equal(revision.SignerPublicKey, fixture.PublicKey) {
		t.Fatalf("persisted receipt = %+v publicKeyEqual=%v", revision, bytes.Equal(revision.SignerPublicKey, fixture.PublicKey))
	}
	if revision.DefinitionCount != 1 || revision.RunnableCount != 1 {
		t.Fatalf("persisted counts = %d/%d, want 1/1", revision.DefinitionCount, revision.RunnableCount)
	}
}

func TestServicePreviewExpiresAtExactBoundary(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	svc, st, _ := newTestPackService(t, &now)
	fixture := makeTestSignedPack(t, testSignedPackOptions{})
	preview, err := svc.Preview(context.Background(), 7, fixture.KeyID, fixture.PublicKey, fixture.Archive)
	if err != nil {
		t.Fatal(err)
	}
	now = preview.ExpiresAt
	if _, err := svc.AcceptAndInstall(context.Background(), 7, fixture.Source, preview.Token, fixture.KeyID, fixture.PublicKey, fixture.Archive); err == nil {
		t.Fatal("token was accepted at its exact expiry boundary")
	}
	if _, err := st.GetDefinitionPackRevision(context.Background(), fixture.Source, fixture.Revision); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("expired preview installed a revision: %v", err)
	}
}

func TestServiceActorAndSourceMismatchDoNotBurnPreview(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	svc, _, _ := newTestPackService(t, &now)
	fixture := makeTestSignedPack(t, testSignedPackOptions{})
	preview, err := svc.Preview(context.Background(), 7, fixture.KeyID, fixture.PublicKey, fixture.Archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcceptAndInstall(context.Background(), 8, fixture.Source, preview.Token, fixture.KeyID, fixture.PublicKey, fixture.Archive); err == nil {
		t.Fatal("actor mismatch accepted")
	}
	if _, err := svc.AcceptAndInstall(context.Background(), 7, "other.example", preview.Token, fixture.KeyID, fixture.PublicKey, fixture.Archive); err == nil {
		t.Fatal("source mismatch accepted")
	}
	if _, err := svc.AcceptAndInstall(context.Background(), 7, fixture.Source, preview.Token, fixture.KeyID, fixture.PublicKey, fixture.Archive); err != nil {
		t.Fatalf("valid retry after pre-burn mismatches: %v", err)
	}
}

func TestServicePreviewBindsUploadKeyManifestAndLicense(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	base := makeTestSignedPack(t, testSignedPackOptions{})
	manifestChanged := makeTestSignedPack(t, testSignedPackOptions{Revision: "v2", PublicKey: base.PublicKey, PrivateKey: base.PrivateKey})
	licenseChanged := makeTestSignedPack(t, testSignedPackOptions{License: []byte("Different invented license\n"), PublicKey: base.PublicKey, PrivateKey: base.PrivateKey})
	otherPublic, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for name, attempt := range map[string]func(*Service, Preview) error{
		"upload": func(s *Service, p Preview) error {
			tampered := append([]byte(nil), base.Archive...)
			tampered[len(tampered)/2] ^= 1
			_, err := s.AcceptAndInstall(context.Background(), 9, base.Source, p.Token, base.KeyID, base.PublicKey, tampered)
			return err
		},
		"public key": func(s *Service, p Preview) error {
			_, err := s.AcceptAndInstall(context.Background(), 9, base.Source, p.Token, base.KeyID, otherPublic, base.Archive)
			return err
		},
		"key id": func(s *Service, p Preview) error {
			_, err := s.AcceptAndInstall(context.Background(), 9, base.Source, p.Token, "other-key", base.PublicKey, base.Archive)
			return err
		},
		"manifest": func(s *Service, p Preview) error {
			_, err := s.AcceptAndInstall(context.Background(), 9, base.Source, p.Token, base.KeyID, base.PublicKey, manifestChanged.Archive)
			return err
		},
		"license": func(s *Service, p Preview) error {
			_, err := s.AcceptAndInstall(context.Background(), 9, base.Source, p.Token, base.KeyID, base.PublicKey, licenseChanged.Archive)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			svc, _, _ := newTestPackService(t, &now)
			preview, err := svc.Preview(context.Background(), 9, base.KeyID, base.PublicKey, base.Archive)
			if err != nil {
				t.Fatal(err)
			}
			if err := attempt(svc, preview); err == nil {
				t.Fatalf("%s mismatch accepted", name)
			}
			if _, err := svc.AcceptAndInstall(context.Background(), 9, base.Source, preview.Token, base.KeyID, base.PublicKey, base.Archive); err != nil {
				t.Fatalf("valid retry after %s mismatch: %v", name, err)
			}
		})
	}
}

func TestServiceConcurrentConsumeIsSingleReceiptAndIdempotent(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	svc, st, _ := newTestPackService(t, &now)
	fixture := makeTestSignedPack(t, testSignedPackOptions{})
	preview, err := svc.Preview(context.Background(), 12, fixture.KeyID, fixture.PublicKey, fixture.Archive)
	if err != nil {
		t.Fatal(err)
	}
	const callers = 8
	var wg sync.WaitGroup
	errs := make(chan error, callers)
	for range callers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := svc.AcceptAndInstall(context.Background(), 12, fixture.Source, preview.Token, fixture.KeyID, fixture.PublicKey, fixture.Archive)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		if !strings.Contains(err.Error(), "invalid, expired, or already used") {
			t.Fatalf("unexpected concurrent consume error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent consumers succeeded %d times, want exactly one", successes)
	}
	revisions, err := st.ListDefinitionPackRevisions(context.Background())
	if err != nil || len(revisions) != 1 {
		t.Fatalf("revisions=%d err=%v, want one immutable receipt", len(revisions), err)
	}
}

func TestServiceFreshPreviewRetriesSameReceiptButRejectsConflicts(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	svc, _, _ := newTestPackService(t, &now)
	fixture := makeTestSignedPack(t, testSignedPackOptions{})
	_, first := previewAndInstall(t, svc, 23, fixture)
	fresh, err := svc.Preview(context.Background(), 23, fixture.KeyID, fixture.PublicKey, fixture.Archive)
	if err != nil {
		t.Fatal(err)
	}
	retried, err := svc.AcceptAndInstall(context.Background(), 23, fixture.Source, fresh.Token, fixture.KeyID, fixture.PublicKey, fixture.Archive)
	if err != nil || retried != first {
		t.Fatalf("fresh-preview idempotent retry status=%+v err=%v, want %+v", retried, err, first)
	}
	otherActor, err := svc.Preview(context.Background(), 24, fixture.KeyID, fixture.PublicKey, fixture.Archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcceptAndInstall(context.Background(), 24, fixture.Source, otherActor.Token, fixture.KeyID, fixture.PublicKey, fixture.Archive); err == nil {
		t.Fatal("conflicting acceptance actor was treated as idempotent")
	}
	conflict := makeTestSignedPack(t, testSignedPackOptions{Definition: append(runnableTestPackDefinition(), []byte("description: changed\n")...), PublicKey: fixture.PublicKey, PrivateKey: fixture.PrivateKey})
	conflictingPreview, err := svc.Preview(context.Background(), 23, conflict.KeyID, conflict.PublicKey, conflict.Archive)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AcceptAndInstall(context.Background(), 23, conflict.Source, conflictingPreview.Token, conflict.KeyID, conflict.PublicKey, conflict.Archive); err == nil {
		t.Fatal("conflicting immutable receipt was treated as idempotent")
	}
}

func TestServiceErrorsDoNotExposeStagingPaths(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	svc, _, root := newTestPackService(t, &now)
	fixture := makeTestSignedPack(t, testSignedPackOptions{})
	svc.DataDir = filepath.Join(root, "secret-missing-data-dir")
	_, err := svc.Preview(context.Background(), 33, fixture.KeyID, fixture.PublicKey, fixture.Archive)
	if err == nil {
		t.Fatal("Preview unexpectedly succeeded with missing staging root")
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), svc.DataDir) {
		t.Fatalf("public service error exposed staging path: %q", err)
	}
}

func TestServicePrunesUsedAndExpiredTokensAndCapsLiveTokens(t *testing.T) {
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	svc, _, _ := newTestPackService(t, &now)
	fixture := makeTestSignedPack(t, testSignedPackOptions{})
	svc.tokens = map[string]previewToken{
		"used":    {used: true, expires: now.Add(time.Hour)},
		"expired": {expires: now},
	}
	if _, err := svc.Preview(context.Background(), 31, fixture.KeyID, fixture.PublicKey, fixture.Archive); err != nil {
		t.Fatalf("Preview after pruning used/expired tokens: %v", err)
	}
	svc.mu.Lock()
	if _, ok := svc.tokens["used"]; ok {
		t.Fatal("used token was not pruned")
	}
	if _, ok := svc.tokens["expired"]; ok {
		t.Fatal("expired token was not pruned")
	}
	svc.tokens = make(map[string]previewToken, maxPreviewTokens)
	for i := 0; i < maxPreviewTokens; i++ {
		svc.tokens[string(rune(i+1))] = previewToken{expires: now.Add(time.Hour)}
	}
	svc.mu.Unlock()
	if _, err := svc.Preview(context.Background(), 31, fixture.KeyID, fixture.PublicKey, fixture.Archive); err == nil {
		t.Fatal("Preview exceeded the 1024 live-token cap")
	}
}
