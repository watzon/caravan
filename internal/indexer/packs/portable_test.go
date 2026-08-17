package packs

import (
	"archive/zip"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/cardigann"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

type portableShortWriter struct{ bytes.Buffer }

func (w *portableShortWriter) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	_, _ = w.Buffer.Write(p[:len(p)-1])
	return len(p) - 1, nil
}

func TestCreatePortableWithholdsOutputWhenBackupExceedsLimit(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var dst bytes.Buffer
	svc := Service{Store: st, DataDir: t.TempDir(), Version: "1.0.0"}
	if err := svc.CreatePortable(context.Background(), &dst, PortableOptions{MaxBytes: 1}); err == nil {
		t.Fatal("CreatePortable accepted an oversized backup")
	}
	if dst.Len() != 0 {
		t.Fatalf("destination received %d partial bytes", dst.Len())
	}
}

func TestCreatePortableReturnsShortWriteWithoutPartialZipEmission(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	w := &portableShortWriter{}
	svc := Service{Store: st, DataDir: t.TempDir(), Version: "1.0.0"}
	err = svc.CreatePortable(context.Background(), w, PortableOptions{MaxBytes: 8 << 20})
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("CreatePortable error = %v, want io.ErrShortWrite", err)
	}
}

func installedPortableFixture(t *testing.T, definition []byte, activate bool) (*Service, *store.Store, testSignedPack, string, string) {
	t.Helper()
	root := t.TempDir()
	dataDir := filepath.Join(root, "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(root, "caravan.sqlite")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	now := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	svc := &Service{Store: st, DataDir: dataDir, Version: "1.0.0", Now: func() time.Time { return now }}
	fixture := makeTestSignedPack(t, testSignedPackOptions{Definition: definition})
	previewAndInstall(t, svc, 101, fixture)
	if activate {
		if err := st.MarkDefinitionPackPending(context.Background(), fixture.Source, fixture.Revision); err != nil {
			t.Fatalf("MarkDefinitionPackPending: %v", err)
		}
		if err := st.PromotePendingDefinitionPack(context.Background(), fixture.Source, fixture.Revision); err != nil {
			t.Fatalf("PromotePendingDefinitionPack: %v", err)
		}
	}
	return svc, st, fixture, dataDir, dbPath
}

func TestPortableRunnablePackRoundTripRestoresExactExecutablePinInDifferentDataDir(t *testing.T) {
	source, _, fixture, sourceDataDir, _ := installedPortableFixture(t, runnableTestPackDefinition(), true)
	var bundle bytes.Buffer
	if err := source.CreatePortable(context.Background(), &bundle, PortableOptions{MaxBytes: 16 << 20}); err != nil {
		t.Fatalf("CreatePortable: %v", err)
	}

	destinationRoot := t.TempDir()
	destinationDataDir := filepath.Join(destinationRoot, "different-data")
	if destinationDataDir == sourceDataDir {
		t.Fatal("portable destination unexpectedly reused source data directory")
	}
	if err := os.Mkdir(destinationDataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	destinationDB := filepath.Join(destinationRoot, "different-store.sqlite")
	destination, err := store.Open(destinationDB)
	if err != nil {
		t.Fatal(err)
	}
	restoreService := &Service{Store: destination, DataDir: destinationDataDir, Version: "1.0.0"}
	if err := restoreService.RestorePortable(context.Background(), bytes.NewReader(bundle.Bytes()), PortableOptions{MaxBytes: 16 << 20}); err != nil {
		destination.Close()
		t.Fatalf("RestorePortable: %v", err)
	}
	if err := destination.Close(); err != nil {
		t.Fatalf("close destination before applying restore: %v", err)
	}
	destination, err = store.Open(destinationDB)
	if err != nil {
		t.Fatalf("reopen destination to apply restore: %v", err)
	}
	defer destination.Close()
	active, err := destination.GetActiveDefinitionPackRevisions(context.Background())
	if err != nil || len(active) != 1 {
		t.Fatalf("active revisions=%d err=%v, want one", len(active), err)
	}
	revision := active[0]
	entries, err := destination.ListDefinitionPackEntries(context.Background(), revision.Source, revision.Revision)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%d err=%v, want one", len(entries), err)
	}
	archivePath := filepath.Join(destinationDataDir, "indexer-packs", revision.ArchiveRelPath)
	provider, err := cardigann.OpenVerifiedInstalledPackProvider(archivePath, "1.0.0", revision, entries)
	if err != nil {
		t.Fatalf("OpenVerifiedInstalledPackProvider: %v", err)
	}
	registry, _, err := cardigann.LoadProviders(provider)
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	entry := entries[0]
	if _, ok := registry.GetExactPack(entry.DefinitionRef, fixture.Source, fixture.Revision, entry.Digest); !ok {
		t.Fatal("restored exact runnable pin did not resolve")
	}
	for _, test := range []struct{ name, source, revision, digest string }{
		{"wrong digest", fixture.Source, fixture.Revision, "sha256:" + strings.Repeat("0", 64)},
		{"wrong revision", fixture.Source, "other", entry.Digest},
		{"wrong source", "other.test", fixture.Revision, entry.Digest},
		{"bare fallback", fixture.Source, fixture.Revision, entry.Digest},
	} {
		id := entry.DefinitionRef
		if test.name == "bare fallback" {
			id = "fixture"
		}
		if _, ok := registry.GetExactPack(id, test.source, test.revision, test.digest); ok {
			t.Fatalf("%s resolved through fallback", test.name)
		}
	}
}

func TestPortableInertCompilerInvalidPackRoundTripRemainsNonExecutableInDifferentDataDir(t *testing.T) {
	source, _, fixture, _, _ := installedPortableFixture(t, inertCompilerInvalidTestPackDefinition(), false)
	var bundle bytes.Buffer
	if err := source.CreatePortable(context.Background(), &bundle, PortableOptions{MaxBytes: 16 << 20}); err != nil {
		t.Fatalf("CreatePortable: %v", err)
	}
	destinationRoot := t.TempDir()
	destinationDataDir := filepath.Join(destinationRoot, "different-data")
	if err := os.Mkdir(destinationDataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	destinationDB := filepath.Join(destinationRoot, "different-store.sqlite")
	destination, err := store.Open(destinationDB)
	if err != nil {
		t.Fatal(err)
	}
	restoreService := &Service{Store: destination, DataDir: destinationDataDir, Version: "1.0.0"}
	if err := restoreService.RestorePortable(context.Background(), bytes.NewReader(bundle.Bytes()), PortableOptions{MaxBytes: 16 << 20}); err != nil {
		destination.Close()
		t.Fatalf("RestorePortable: %v", err)
	}
	if err := destination.Close(); err != nil {
		t.Fatal(err)
	}
	destination, err = store.Open(destinationDB)
	if err != nil {
		t.Fatalf("reopen destination to apply inert restore: %v", err)
	}
	defer destination.Close()
	revision, err := destination.GetDefinitionPackRevision(context.Background(), fixture.Source, fixture.Revision)
	if err != nil {
		t.Fatal(err)
	}
	if revision.RunnableCount != 0 {
		t.Fatalf("restored inert runnable count = %d, want 0", revision.RunnableCount)
	}
	entries, err := destination.ListDefinitionPackEntries(context.Background(), revision.Source, revision.Revision)
	if err != nil || len(entries) != 1 {
		t.Fatalf("entries=%d err=%v", len(entries), err)
	}
	archivePath := filepath.Join(destinationDataDir, "indexer-packs", revision.ArchiveRelPath)
	if err := cardigann.VerifyInstalledPackArchive(archivePath, "1.0.0", *revision, entries); err != nil {
		t.Fatalf("VerifyInstalledPackArchive: %v", err)
	}
	provider, err := cardigann.OpenVerifiedInstalledPackProvider(archivePath, "1.0.0", *revision, entries)
	if err != nil {
		t.Fatalf("OpenVerifiedInstalledPackProvider: %v", err)
	}
	documents, err := provider.Documents()
	if err != nil || len(documents) != 0 {
		t.Fatalf("provider documents=%d err=%v, want zero", len(documents), err)
	}
	if err := destination.MarkDefinitionPackPending(context.Background(), fixture.Source, fixture.Revision); err == nil {
		t.Fatal("inert restored revision became pending")
	}
	registry, _, err := cardigann.LoadProviders(provider)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.Get(entries[0].DefinitionRef); ok {
		t.Fatal("inert restored definition entered executable registry")
	}
}

type snapshotMutationStore struct {
	Store
	mutate    func() error
	mu        sync.Mutex
	listCalls int
}

func (s *snapshotMutationStore) BackupAndInspect(ctx context.Context, dst io.Writer, maxBytes int64) (store.DefinitionPackSnapshot, error) {
	snapshot, err := s.Store.BackupAndInspect(ctx, dst, maxBytes)
	if err == nil && s.mutate != nil {
		err = s.mutate()
	}
	return snapshot, err
}

func (s *snapshotMutationStore) ListDefinitionPackRevisions(ctx context.Context) ([]core.DefinitionPackRevision, error) {
	s.mu.Lock()
	s.listCalls++
	s.mu.Unlock()
	return s.Store.ListDefinitionPackRevisions(ctx)
}

func (s *snapshotMutationStore) ListDefinitionPackEntries(ctx context.Context, source, revision string) ([]core.DefinitionPackEntry, error) {
	s.mu.Lock()
	s.listCalls++
	s.mu.Unlock()
	return s.Store.ListDefinitionPackEntries(ctx, source, revision)
}

func TestCreatePortableUsesOnlyBackupSnapshotInventoryAcrossConcurrentMutation(t *testing.T) {
	source, st, fixture, dataDir, _ := installedPortableFixture(t, runnableTestPackDefinition(), false)
	wrapped := &snapshotMutationStore{Store: st}
	wrapped.mutate = func() error {
		return st.QuarantineDefinitionPack(context.Background(), fixture.Source, fixture.Revision, "test.concurrent_mutation")
	}
	source.Store = wrapped
	var bundle bytes.Buffer
	if err := source.CreatePortable(context.Background(), &bundle, PortableOptions{MaxBytes: 16 << 20}); err != nil {
		t.Fatalf("CreatePortable after live mutation: %v", err)
	}
	wrapped.mu.Lock()
	calls := wrapped.listCalls
	wrapped.mu.Unlock()
	if calls != 0 {
		t.Fatalf("CreatePortable made %d later live List calls", calls)
	}
	live, err := st.GetDefinitionPackRevision(context.Background(), fixture.Source, fixture.Revision)
	if err != nil || live.InstallState != core.DefinitionPackFailed {
		t.Fatalf("live mutation did not occur: revision=%+v err=%v", live, err)
	}

	destinationRoot := t.TempDir()
	destinationDataDir := filepath.Join(destinationRoot, "data")
	if err := os.Mkdir(destinationDataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	destination, err := store.Open(filepath.Join(destinationRoot, "caravan.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	if err := (&Service{Store: destination, DataDir: destinationDataDir, Version: "1.0.0"}).RestorePortable(context.Background(), bytes.NewReader(bundle.Bytes()), PortableOptions{MaxBytes: 16 << 20}); err != nil {
		t.Fatalf("snapshot-consistent bundle failed restore: %v (source data %s)", err, dataDir)
	}
}

func portableMembers(t *testing.T, bundle []byte) (portableManifest, map[string][]byte) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(bundle), int64(len(bundle)))
	if err != nil {
		t.Fatal(err)
	}
	members := make(map[string][]byte, len(zr.File))
	for _, file := range zr.File {
		r, err := file.Open()
		if err != nil {
			t.Fatal(err)
		}
		members[file.Name], err = io.ReadAll(r)
		r.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	var manifest portableManifest
	if err := json.Unmarshal(members[portableManifestName], &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest, members
}

func rebuildPortableBundleWithAllMembers(t *testing.T, manifest portableManifest, members map[string][]byte) []byte {
	t.Helper()
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	if err := writePortableZipMember(zw, portableManifestName, manifestBytes); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(members))
	for name := range members {
		if name != portableManifestName {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		if err := writePortableZipMember(zw, name, members[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func rebuildPortableBundle(t *testing.T, manifest portableManifest, members map[string][]byte) []byte {
	t.Helper()
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	if err := writePortableZipMember(zw, portableManifestName, manifestBytes); err != nil {
		t.Fatal(err)
	}
	if err := writePortableZipMember(zw, portableDatabaseName, members[portableDatabaseName]); err != nil {
		t.Fatal(err)
	}
	archives := append([]portableBundleMember(nil), manifest.Archives...)
	sort.Slice(archives, func(i, j int) bool { return archives[i].Path < archives[j].Path })
	for _, archive := range archives {
		if data, ok := members[archive.Path]; ok {
			if err := writePortableZipMember(zw, archive.Path, data); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func replacePortableDatabase(t *testing.T, bundle, database []byte) []byte {
	t.Helper()
	manifest, members := portableMembers(t, bundle)
	members[portableDatabaseName] = database
	manifest.Database.Size = int64(len(database))
	manifest.Database.Digest = digestBytes(database)
	return rebuildPortableBundle(t, manifest, members)
}

func mutateSQLiteSnapshot(t *testing.T, database []byte, statement string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "snapshot.sqlite")
	if err := os.WriteFile(path, database, 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(statement); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func TestRestorePortableRejectsNoncurrentOrMalformedDatabaseBeforePublishingArchives(t *testing.T) {
	source, _, _, _, _ := installedPortableFixture(t, runnableTestPackDefinition(), false)
	var valid bytes.Buffer
	if err := source.CreatePortable(context.Background(), &valid, PortableOptions{MaxBytes: 16 << 20}); err != nil {
		t.Fatal(err)
	}
	manifest, members := portableMembers(t, valid.Bytes())
	currentDB := members[portableDatabaseName]
	cases := map[string][]byte{
		"old schema":   mutateSQLiteSnapshot(t, currentDB, "DELETE FROM caravan_schema_migrations WHERE version_id = (SELECT MAX(version_id) FROM caravan_schema_migrations)"),
		"newer schema": mutateSQLiteSnapshot(t, currentDB, "INSERT INTO caravan_schema_migrations(version_id, is_applied) VALUES (99999999999999, 1)"),
		"malformed":    []byte("not a sqlite database"),
	}
	for name, database := range cases {
		t.Run(name, func(t *testing.T) {
			destinationRoot := t.TempDir()
			dataDir := filepath.Join(destinationRoot, "data")
			if err := os.Mkdir(dataDir, 0o700); err != nil {
				t.Fatal(err)
			}
			dbPath := filepath.Join(destinationRoot, "caravan.sqlite")
			destination, err := store.Open(dbPath)
			if err != nil {
				t.Fatal(err)
			}
			defer destination.Close()
			if err := destination.SetSetting(context.Background(), "portable-sentinel", "current"); err != nil {
				t.Fatal(err)
			}
			bundle := replacePortableDatabase(t, valid.Bytes(), database)
			err = (&Service{Store: destination, DataDir: dataDir, Version: "1.0.0"}).RestorePortable(context.Background(), bytes.NewReader(bundle), PortableOptions{MaxBytes: 16 << 20})
			if err == nil {
				t.Fatalf("RestorePortable accepted %s database", name)
			}
			got, getErr := destination.GetSetting(context.Background(), "portable-sentinel")
			if getErr != nil || got != "current" {
				t.Fatalf("current DB changed after rejection: got=%q err=%v", got, getErr)
			}
			if _, statErr := os.Stat(dbPath + ".restore-pending"); !os.IsNotExist(statErr) {
				t.Fatalf("rejected DB left staged restore: %v", statErr)
			}
			for _, archive := range manifest.Archives {
				if _, statErr := os.Stat(filepath.Join(dataDir, archive.Path)); !os.IsNotExist(statErr) {
					t.Fatalf("rejected DB published archive %s: %v", archive.Path, statErr)
				}
			}
		})
	}
}

type portableHeaderFixture struct {
	name   string
	method uint16
	mode   os.FileMode
	data   []byte
}

func portableZipFiles(t *testing.T, entries []portableHeaderFixture) []*zip.File {
	t.Helper()
	var buffer bytes.Buffer
	zw := zip.NewWriter(&buffer)
	for _, entry := range entries {
		method := entry.method
		header := &zip.FileHeader{Name: entry.name, Method: method}
		if entry.mode != 0 {
			header.SetMode(entry.mode)
		} else {
			header.SetMode(0o600)
		}
		part, err := zw.CreateHeader(header)
		if err != nil {
			t.Fatalf("CreateHeader(%q): %v", entry.name, err)
		}
		if _, err := part.Write(entry.data); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	zr, err := zip.NewReader(bytes.NewReader(buffer.Bytes()), int64(buffer.Len()))
	if err != nil {
		t.Fatal(err)
	}
	return zr.File
}

func TestRestorePortableRejectsMissingExtraAndMismatchedArchiveInventory(t *testing.T) {
	source, _, _, _, _ := installedPortableFixture(t, runnableTestPackDefinition(), false)
	var valid bytes.Buffer
	if err := source.CreatePortable(context.Background(), &valid, PortableOptions{MaxBytes: 16 << 20}); err != nil {
		t.Fatal(err)
	}
	baseManifest, baseMembers := portableMembers(t, valid.Bytes())
	if len(baseManifest.Archives) != 1 {
		t.Fatalf("fixture archive count = %d, want 1", len(baseManifest.Archives))
	}
	extraPath := "indexer-packs/archives/sha256/" + strings.Repeat("1", 64) + ".zip"
	for _, name := range []string{"missing declared archive", "extra actual archive", "extra manifest archive"} {
		t.Run(name, func(t *testing.T) {
			manifest := baseManifest
			manifest.Archives = append([]portableBundleMember(nil), baseManifest.Archives...)
			members := make(map[string][]byte, len(baseMembers)+1)
			for path, data := range baseMembers {
				members[path] = append([]byte(nil), data...)
			}
			switch name {
			case "missing declared archive":
				delete(members, manifest.Archives[0].Path)
			case "extra actual archive":
				members[extraPath] = append([]byte(nil), members[manifest.Archives[0].Path]...)
			case "extra manifest archive":
				data := append([]byte(nil), members[manifest.Archives[0].Path]...)
				members[extraPath] = data
				manifest.Archives = append(manifest.Archives, portableBundleMember{Path: extraPath, Size: int64(len(data)), Digest: digestBytes(data)})
				sort.Slice(manifest.Archives, func(i, j int) bool { return manifest.Archives[i].Path < manifest.Archives[j].Path })
			}
			bundle := rebuildPortableBundleWithAllMembers(t, manifest, members)
			root := t.TempDir()
			dataDir := filepath.Join(root, "data")
			if err := os.Mkdir(dataDir, 0o700); err != nil {
				t.Fatal(err)
			}
			destination, err := store.Open(filepath.Join(root, "caravan.sqlite"))
			if err != nil {
				t.Fatal(err)
			}
			defer destination.Close()
			if err := (&Service{Store: destination, DataDir: dataDir, Version: "1.0.0"}).RestorePortable(context.Background(), bytes.NewReader(bundle), PortableOptions{MaxBytes: 16 << 20}); err == nil {
				t.Fatalf("RestorePortable accepted %s", name)
			}
		})
	}
}

func TestPortableZIPMemberTableRejectsUnsafeAndIncompleteInventories(t *testing.T) {
	baseManifest := portableHeaderFixture{name: portableManifestName, method: zip.Store, data: []byte(`{}`)}
	baseDatabase := portableHeaderFixture{name: portableDatabaseName, method: zip.Store, data: []byte("db")}
	tests := map[string][]portableHeaderFixture{
		"duplicate":        {baseManifest, baseDatabase, baseManifest},
		"case duplicate":   {baseManifest, baseDatabase, {name: "Manifest.json", method: zip.Store}},
		"traversal":        {baseManifest, baseDatabase, {name: "../escape", method: zip.Store}},
		"backslash":        {baseManifest, baseDatabase, {name: `indexer-packs\\escape`, method: zip.Store}},
		"NUL":              {baseManifest, baseDatabase, {name: "bad\x00name", method: zip.Store}},
		"symlink":          {baseManifest, baseDatabase, {name: "extra", method: zip.Store, mode: os.ModeSymlink | 0o777}},
		"compressed":       {{name: portableManifestName, method: zip.Deflate, data: []byte(`{}`)}, baseDatabase},
		"unexpected extra": {baseManifest, baseDatabase, {name: "extra", method: zip.Store}},
		"missing manifest": {baseDatabase, {name: portableDatabaseName + ".copy", method: zip.Store}},
		"missing database": {baseManifest, {name: portableManifestName + ".copy", method: zip.Store}},
	}
	for name, entries := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := validatePortableMembers(portableZipFiles(t, entries)); err == nil {
				t.Fatalf("unsafe portable ZIP inventory %s accepted", name)
			}
		})
	}
	files := portableZipFiles(t, []portableHeaderFixture{baseManifest, baseDatabase})
	files[0].Flags |= 1
	if _, err := validatePortableMembers(files); err == nil {
		t.Fatal("encrypted portable ZIP member accepted")
	}
	files = portableZipFiles(t, []portableHeaderFixture{baseManifest, baseDatabase})
	files[0].CompressedSize64++
	if _, err := validatePortableMembers(files); err == nil {
		t.Fatal("compressed/uncompressed size mismatch accepted")
	}
}

func TestPortableManifestAndMemberBoundariesRejectWrongIdentityAndLimits(t *testing.T) {
	data := []byte("member")
	valid := portableBundleMember{Path: portableDatabaseName, Size: int64(len(data)), Digest: digestBytes(data)}
	for name, member := range map[string]portableBundleMember{
		"path":   {Path: "other", Size: valid.Size, Digest: valid.Digest},
		"size":   {Path: valid.Path, Size: valid.Size + 1, Digest: valid.Digest},
		"digest": {Path: valid.Path, Size: valid.Size, Digest: digestBytes([]byte("other"))},
	} {
		t.Run(name, func(t *testing.T) {
			if err := verifyPortableMember(member, portableDatabaseName, data); err == nil {
				t.Fatalf("wrong portable member %s accepted", name)
			}
		})
	}
	tooMany := make([]*zip.File, portableMaxMembers+1)
	for i := range tooMany {
		tooMany[i] = &zip.File{FileHeader: zip.FileHeader{Name: fmt.Sprintf("member-%d", i), Method: zip.Store}}
	}
	if _, err := validatePortableMembers(tooMany); err == nil {
		t.Fatal("portable ZIP member count limit was not enforced")
	}
	for name, file := range map[string]*zip.File{
		"manifest": {FileHeader: zip.FileHeader{Name: portableManifestName, Method: zip.Store, UncompressedSize64: portableMaxManifest + 1, CompressedSize64: portableMaxManifest + 1}},
		"archive":  {FileHeader: zip.FileHeader{Name: "indexer-packs/archives/sha256/" + strings.Repeat("0", 64) + ".zip", Method: zip.Store, UncompressedSize64: uint64(cardigann.MaxPackArchiveBytes + 1), CompressedSize64: uint64(cardigann.MaxPackArchiveBytes + 1)}},
	} {
		t.Run(name+" limit", func(t *testing.T) {
			files := []*zip.File{{FileHeader: zip.FileHeader{Name: portableManifestName, Method: zip.Store}}, {FileHeader: zip.FileHeader{Name: portableDatabaseName, Method: zip.Store}}, file}
			if name == "manifest" {
				files[0] = file
				files = files[:2]
			}
			if _, err := validatePortableMembers(files); err == nil {
				t.Fatalf("portable %s limit was not enforced", name)
			}
		})
	}
}

func TestRestorePortableRejectsOuterAndAggregateSizeLimit(t *testing.T) {
	source, _, _, _, _ := installedPortableFixture(t, runnableTestPackDefinition(), false)
	var bundle bytes.Buffer
	if err := source.CreatePortable(context.Background(), &bundle, PortableOptions{MaxBytes: 16 << 20}); err != nil {
		t.Fatal(err)
	}
	destinationRoot := t.TempDir()
	dataDir := filepath.Join(destinationRoot, "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	destination, err := store.Open(filepath.Join(destinationRoot, "caravan.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer destination.Close()
	err = (&Service{Store: destination, DataDir: dataDir, Version: "1.0.0"}).RestorePortable(context.Background(), bytes.NewReader(bundle.Bytes()), PortableOptions{MaxBytes: int64(bundle.Len() - 1)})
	if !errors.Is(err, ErrPortableTooLarge) {
		t.Fatalf("portable outer/aggregate byte limit error = %v, want ErrPortableTooLarge", err)
	}
}

func TestServiceAndPortableStagingRejectSymlinksWithoutOutsideWrites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink staging semantics are Unix-only")
	}
	svc, _, fixture, dataDir, _ := installedPortableFixture(t, runnableTestPackDefinition(), false)
	outside := t.TempDir()
	for _, purpose := range []string{"service-staging", "portable-staging"} {
		path := filepath.Join(dataDir, "indexer-packs", purpose)
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(outside, path); err != nil {
			t.Fatal(err)
		}
		if purpose == "service-staging" {
			if _, err := svc.Preview(context.Background(), 55, fixture.KeyID, fixture.PublicKey, fixture.Archive); err == nil {
				t.Fatal("service staging accepted symlink")
			}
		} else {
			var bundle bytes.Buffer
			if err := svc.CreatePortable(context.Background(), &bundle, PortableOptions{MaxBytes: 16 << 20}); err == nil {
				t.Fatal("portable staging accepted symlink")
			}
		}
		entries, err := os.ReadDir(outside)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 0 {
			t.Fatalf("%s symlink caused %d outside writes", purpose, len(entries))
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}
