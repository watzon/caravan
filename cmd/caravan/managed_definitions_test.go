package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"

	"github.com/watzon/caravan/internal/cardigann"
)

func init() {
	// runServe tests exercise the full process and must never call the public
	// network. Fetch verification itself is covered with httptest in cardigann.
	managedDefinitionSourceURL = ""
}

func TestIndexerRuntimeLoadsManagedSnapshotFromCache(t *testing.T) {
	dataDir := t.TempDir()
	definition := []byte(`
id: shanaproject
name: Shana Project
type: public
links: [https://tracker.example]
caps: {modes: {search: [q]}}
settings:
  - {name: token, label: API token, type: password}
search:
  paths:
    - path: /search
  rows: {selector: article}
  fields:
    title: {selector: h2}
    download: {selector: a, attribute: href}
`)
	listing, archive := managedRuntimeFixture(t, "shanaproject", definition)
	snapshot, err := cardigann.InspectManagedSnapshot(listing, archive)
	if err != nil {
		t.Fatalf("InspectManagedSnapshot: %v", err)
	}
	if err := cardigann.InstallManagedSnapshot(dataDir, snapshot); err != nil {
		t.Fatalf("InstallManagedSnapshot: %v", err)
	}

	runtime, err := newIndexerRuntime(dataDir, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	if err != nil {
		t.Fatalf("newIndexerRuntime: %v", err)
	}
	manifest, err := cardigann.ParseManifest(cardigann.ManagedSource, definition)
	if err != nil {
		t.Fatal(err)
	}
	schema, ok := runtime.definitions("managed:shanaproject")
	if !ok || len(schema.BaseURLs) != 1 || schema.BaseURLs[0] != "https://tracker.example" || len(schema.Fields) != 1 || schema.Fields[0].Name != "token" {
		t.Fatalf("managed exact schema = %+v, %v", schema, ok)
	}
	if len(runtime.managedStatuses) != 1 {
		t.Fatalf("managed statuses = %+v", runtime.managedStatuses)
	}
	status := runtime.managedStatuses[0]
	if !status.Addable || status.Source != cardigann.ManagedSource || status.Revision != snapshot.Revision || status.Digest != manifest.Digest || status.DefinitionID != "managed:shanaproject" {
		t.Fatalf("managed status = %+v", status)
	}
}

func managedRuntimeFixture(t *testing.T, id string, definition []byte) ([]byte, []byte) {
	t.Helper()
	hash := sha1.New()
	_, _ = fmt.Fprintf(hash, "blob %d%c", len(definition), byte(0))
	_, _ = hash.Write(definition)
	listing, err := json.Marshal([]map[string]string{{"id": id, "file": id, "sha": hex.EncodeToString(hash.Sum(nil))}})
	if err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	member, err := zw.Create(id + ".yml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := member.Write(definition); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return listing, archive.Bytes()
}
