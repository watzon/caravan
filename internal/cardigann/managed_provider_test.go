package cardigann

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestInspectManagedSnapshotVerifiesListingAndBuildsDeterministicProvider(t *testing.T) {
	documents := map[string][]byte{
		"alpha.yml": []byte("id: alpha\nname: Alpha\nlinks: [https://alpha.example]\ncaps: {modes: {search: [q]}}\nsearch: {paths: [{path: /search}], rows: {selector: article}, fields: {title: {selector: h2}, download: {selector: a, attribute: href}}}\n"),
		"beta.yml":  []byte("id: beta\nname: Beta\nlinks: [https://beta.example]\ncaps: {modes: {search: [q]}}\nsearch: {paths: [{path: /search}], rows: {selector: article}, fields: {title: {selector: h2}, download: {selector: a, attribute: href}}}\n"),
	}
	listing := managedListingBytes(t, documents)
	archive := managedArchiveBytes(t, documents)

	snapshot, err := InspectManagedSnapshot(listing, archive)
	if err != nil {
		t.Fatalf("InspectManagedSnapshot: %v", err)
	}
	if snapshot.Revision == "" || len(snapshot.Revision) != 64 {
		t.Fatalf("revision = %q", snapshot.Revision)
	}
	provider := snapshot.Provider()
	if provider.Source() != ManagedSource || provider.Descriptor().Kind != SourceKindManaged || provider.Descriptor().Revision != snapshot.Revision {
		t.Fatalf("provider descriptor = %+v", provider.Descriptor())
	}
	got, err := provider.Documents()
	if err != nil {
		t.Fatalf("Documents: %v", err)
	}
	if len(got) != 2 || got[0].Path != "alpha.yml" || got[1].Path != "beta.yml" {
		t.Fatalf("documents = %#v", got)
	}
	registry, manifests, err := LoadProviders(provider)
	if err != nil || len(manifests) != 2 {
		t.Fatalf("LoadProviders = %d manifests, %v", len(manifests), err)
	}
	if _, ok := registry.GetExact("managed:alpha", ManagedSource, snapshot.Revision, manifests[0].Digest); !ok {
		compiled, _ := registry.Get("managed:alpha")
		t.Fatalf("managed definition did not resolve through its complete immutable pin: want_revision=%s want_digest=%s got_revision=%s got_digest=%s", snapshot.Revision, manifests[0].Digest, compiled.sourceRevision, compiled.sourceDigest)
	}
	if _, ok := registry.GetExact("managed:alpha", ManagedSource, "mutable", manifests[0].Digest); ok {
		t.Fatal("managed definition resolved through a wrong revision")
	}

	again, err := InspectManagedSnapshot(listing, archive)
	if err != nil || again.Revision != snapshot.Revision {
		t.Fatalf("deterministic revision = %q, %v", again.Revision, err)
	}
}

func TestInspectManagedSnapshotRejectsUnverifiedOrUnsafeArchives(t *testing.T) {
	document := []byte("id: alpha\n")
	validListing := managedListingBytes(t, map[string][]byte{"alpha.yml": document})
	tests := []struct {
		name    string
		listing []byte
		archive []byte
	}{
		{name: "digest mismatch", listing: validListing, archive: managedArchiveBytes(t, map[string][]byte{"alpha.yml": []byte("id: changed\n")})},
		{name: "unexpected entry", listing: validListing, archive: managedArchiveBytes(t, map[string][]byte{"alpha.yml": document, "extra.yml": document})},
		{name: "path traversal", listing: validListing, archive: managedArchiveBytes(t, map[string][]byte{"../alpha.yml": document})},
		{name: "missing entry", listing: validListing, archive: managedArchiveBytes(t, map[string][]byte{})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := InspectManagedSnapshot(tt.listing, tt.archive); err == nil {
				t.Fatal("InspectManagedSnapshot unexpectedly succeeded")
			}
		})
	}
}

func TestInspectManagedSnapshotAcceptsCaseFoldedListingIdentity(t *testing.T) {
	documents := map[string][]byte{
		"Bittorrentfiles.yml": []byte("id: bittorrentfiles\nname: BitTorrentFiles\nlinks: [https://tracker.example]\ncaps: {modes: {search: [q]}}\nsearch: {paths: [{path: /search}], rows: {selector: article}, fields: {title: {selector: h2}, download: {selector: a, attribute: href}}}\n"),
	}
	if _, err := InspectManagedSnapshot(managedListingBytes(t, documents), managedArchiveBytes(t, documents)); err != nil {
		t.Fatalf("case-folded managed identity: %v", err)
	}
}

func TestManagedAdvertisedBaseURLPassesOriginPolicy(t *testing.T) {
	documents := map[string][]byte{
		"xxxtor.yml": []byte("id: xxxtor\nname: xxxtor\nlinks: [https://xxxtor.com/]\ncaps: {modes: {search: [q]}}\nsearch: {paths: [{path: /search}], rows: {selector: article}, fields: {title: {selector: h2}, download: {selector: a, attribute: href}}}\n"),
	}
	snapshot, err := InspectManagedSnapshot(managedListingBytes(t, documents), managedArchiveBytes(t, documents))
	if err != nil {
		t.Fatalf("InspectManagedSnapshot: %v", err)
	}
	registry, _, err := LoadProviders(snapshot.Provider())
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	definition, ok := registry.Get("managed:xxxtor")
	if !ok {
		t.Fatal("managed xxxtor definition is unavailable")
	}
	if _, err := New(definition, Config{BaseURL: "https://xxxtor.com"}, nil); err != nil {
		t.Fatalf("New rejected its advertised managed base URL: %v", err)
	}
}

func TestFetchInstallAndRecoverManagedSnapshot(t *testing.T) {
	firstDocuments := map[string][]byte{"alpha.yml": []byte("id: alpha\n")}
	firstListing := managedListingBytes(t, firstDocuments)
	firstArchive := managedArchiveBytes(t, firstDocuments)
	var sawUserAgent bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawUserAgent = strings.Contains(r.UserAgent(), "Caravan")
		switch r.URL.Path {
		case "/11":
			_, _ = w.Write(firstListing)
		case "/11/package.zip":
			_, _ = w.Write(firstArchive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := &http.Client{Timeout: time.Second}
	snapshot, err := FetchManagedSnapshot(t.Context(), client, server.URL+"/11")
	if err != nil {
		t.Fatalf("FetchManagedSnapshot: %v", err)
	}
	if !sawUserAgent {
		t.Fatal("managed source request did not identify Caravan")
	}
	dataDir := t.TempDir()
	if err := InstallManagedSnapshot(dataDir, snapshot); err != nil {
		t.Fatalf("InstallManagedSnapshot first: %v", err)
	}
	loaded, err := LoadManagedSnapshot(dataDir)
	if err != nil || loaded.Revision != snapshot.Revision {
		t.Fatalf("LoadManagedSnapshot first = %v, %v", loaded, err)
	}

	secondDocuments := map[string][]byte{"beta.yml": []byte("id: beta\n")}
	second, err := InspectManagedSnapshot(managedListingBytes(t, secondDocuments), managedArchiveBytes(t, secondDocuments))
	if err != nil {
		t.Fatal(err)
	}
	if err := InstallManagedSnapshot(dataDir, second); err != nil {
		t.Fatalf("InstallManagedSnapshot second: %v", err)
	}
	currentArchive := filepath.Join(dataDir, managedStorageRoot, "snapshots", second.Revision+".zip")
	if err := os.WriteFile(currentArchive, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	recovered, err := LoadManagedSnapshot(dataDir)
	if err != nil || recovered.Revision != snapshot.Revision {
		t.Fatalf("LoadManagedSnapshot fallback revision = %v, %v; want %s", recovered, err, snapshot.Revision)
	}
}

func managedListingBytes(t *testing.T, documents map[string][]byte) []byte {
	t.Helper()
	entries := make([]map[string]string, 0, len(documents))
	for path, data := range documents {
		file := path
		if len(file) > 4 && file[len(file)-4:] == ".yml" {
			file = file[:len(file)-4]
		}
		entries = append(entries, map[string]string{"id": file, "file": file, "sha": managedGitBlobSHA(data)})
	}
	data, err := json.Marshal(entries)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func managedArchiveBytes(t *testing.T, documents map[string][]byte) []byte {
	t.Helper()
	var out bytes.Buffer
	archive := zip.NewWriter(&out)
	for path, data := range documents {
		entry, err := archive.Create(path)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(data); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func managedGitBlobSHA(data []byte) string {
	digest := sha1.Sum(append([]byte(fmt.Sprintf("blob %d\x00", len(data))), data...))
	return hex.EncodeToString(digest[:])
}
