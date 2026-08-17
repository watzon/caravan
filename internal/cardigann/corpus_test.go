package cardigann

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"testing"
)

// This optional compatibility gate uses a local, read-only functional corpus.
// Normal package tests never depend on it or on network access.
func TestExternalV11CorpusClassifiesInertly(t *testing.T) {
	root := os.Getenv("CARAVAN_CARDIGANN_CORPUS")
	if root == "" {
		t.Skip("set CARAVAN_CARDIGANN_CORPUS to run the external corpus gate")
	}
	paths, err := filepath.Glob(filepath.Join(root, "*.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) != 542 {
		t.Fatalf("YAML files = %d, want 542", len(paths))
	}
	codes := map[CapabilityCode]struct{}{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		manifest, err := ParseManifest("external", data)
		if err != nil {
			t.Fatalf("ParseManifest(%s): %v", filepath.Base(path), err)
		}
		if manifest.Runnable {
			t.Fatalf("classification made %s executable", filepath.Base(path))
		}
		if manifest.Revision == "" || manifest.Revision != manifest.Digest {
			t.Fatalf("manifest %s revision/digest = %q/%q", filepath.Base(path), manifest.Revision, manifest.Digest)
		}
		if len(manifest.Unsupported) == 0 || !slices.IsSorted(manifest.Unsupported) {
			t.Fatalf("manifest %s capability codes are empty or unsorted: %v", filepath.Base(path), manifest.Unsupported)
		}
		for i, code := range manifest.Unsupported {
			if i > 0 && code == manifest.Unsupported[i-1] {
				t.Fatalf("manifest %s repeats capability code %q", filepath.Base(path), code)
			}
			codes[code] = struct{}{}
		}
		again, err := ParseManifest("external", data)
		if err != nil || !reflect.DeepEqual(manifest, again) {
			t.Fatalf("ParseManifest(%s) is not deterministic: %v", filepath.Base(path), err)
		}
	}
	t.Logf("classified %d inert definitions across %d capability codes", len(paths), len(codes))
}
