package cardigann

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type staticProvider struct {
	source string
	items  []SourceDocument
}

func (p staticProvider) Source() string                       { return p.source }
func (p staticProvider) Documents() ([]SourceDocument, error) { return p.items, nil }

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks(temp dir): %v", err)
	}
	return dir
}

func TestLoadProvidersRejectsDuplicateNamespacedReferences(t *testing.T) {
	doc := SourceDocument{Path: "fixture.yml", Data: []byte(`
id: fixture
name: Fixture
links: [https://tracker.example]
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search}]
  rows: {selector: tr}
  fields: {title: {selector: .title}, download: {selector: .download}}
`)}
	_, _, err := LoadProviders(staticProvider{source: "local", items: []SourceDocument{doc, doc}})
	if err == nil || fmt.Sprint(err) != `duplicate definition reference "local:fixture"` {
		t.Fatalf("LoadProviders error = %v", err)
	}
}

func TestLoadProvidersRejectsDistinctProvidersClaimingSameSource(t *testing.T) {
	document := func(id string) SourceDocument {
		return SourceDocument{Path: id + ".yml", Data: []byte(fmt.Sprintf(`
id: %s
name: %s
links: [https://tracker.example]
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search}]
  rows: {selector: tr}
  fields: {title: {selector: .title}, download: {selector: .download}}
`, id, id))}
	}
	_, _, err := LoadProviders(
		staticProvider{source: "user", items: []SourceDocument{document("a")}},
		staticProvider{source: "user", items: []SourceDocument{document("b")}},
	)
	if err == nil || fmt.Sprint(err) != `definition provider source "user" is already registered` {
		t.Fatalf("LoadProviders error = %v", err)
	}
}

func TestLoadProvidersCompilesOnlySupportedT1Definition(t *testing.T) {
	doc := SourceDocument{Path: "fixture.yml", Data: []byte(`
id: fixture
name: Fixture
type: public
links: [https://tracker.example]
caps: {modes: {search: [q]}}
settings: []
search:
  paths:
    - path: /search
      method: post
      inputs: {q: "{{ .Keywords }}"}
      headers: {Accept: application/xml}
      response: {type: xml}
  rows: {selector: rss.channel.item}
  fields:
    title: {selector: title}
    download: {selector: link}
`)}
	registry, manifests, err := LoadProviders(staticProvider{source: "user", items: []SourceDocument{doc}})
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	if len(manifests) != 1 || !manifests[0].Runnable {
		t.Fatalf("manifests = %+v, want one runnable supported T1 definition", manifests)
	}
	if _, ok := registry.Get("user:fixture"); !ok {
		t.Fatal("supported namespaced T1 definition was absent from executable registry")
	}
	if _, ok := registry.Get("fixture"); ok {
		t.Fatal("bare lookup incorrectly resolved a user definition instead of builtin only")
	}
}

func TestDirectoryProviderReadsOnlySortedRegularYAMLFiles(t *testing.T) {
	dir := canonicalTempDir(t)
	for name, data := range map[string]string{
		"b.yml":    "id: b\n",
		"a.yml":    "id: a\n",
		"note.txt": "not YAML\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	provider, err := NewDirectoryProvider("local", dir)
	if err != nil {
		t.Fatalf("NewDirectoryProvider: %v", err)
	}
	documents, err := provider.Documents()
	if err != nil {
		t.Fatalf("Documents: %v", err)
	}
	paths := make([]string, len(documents))
	for i, document := range documents {
		paths[i] = document.Path
	}
	if !slices.Equal(paths, []string{"a.yml", "b.yml"}) {
		t.Fatalf("document order = %v", paths)
	}
}

func TestDirectoryProviderRejectsSymlinkedYAML(t *testing.T) {
	dir := canonicalTempDir(t)
	target := filepath.Join(dir, "target.yml")
	if err := os.WriteFile(target, []byte("id: target\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "linked.yml")); err != nil {
		t.Fatal(err)
	}
	provider, err := NewDirectoryProvider("local", dir)
	if err != nil {
		t.Fatalf("NewDirectoryProvider: %v", err)
	}
	if _, err := provider.Documents(); err == nil {
		t.Fatal("Documents accepted a symlinked YAML definition")
	}
}

func TestDirectoryProviderRejectsTraversalComponentBeforeCleaning(t *testing.T) {
	dir := canonicalTempDir(t)
	traversing := dir + string(filepath.Separator) + "missing" + string(filepath.Separator) + ".."
	if _, err := NewDirectoryProvider("local", traversing); err == nil {
		t.Fatal("NewDirectoryProvider accepted a path containing a traversal component")
	}
}

func TestDirectoryProviderRejectsSymlinkedAncestor(t *testing.T) {
	actual := canonicalTempDir(t)
	definitions := filepath.Join(actual, "definitions")
	if err := os.Mkdir(definitions, 0o700); err != nil {
		t.Fatal(err)
	}
	aliasParent := canonicalTempDir(t)
	alias := filepath.Join(aliasParent, "alias")
	if err := os.Symlink(actual, alias); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDirectoryProvider("local", filepath.Join(alias, "definitions")); err == nil {
		t.Fatal("NewDirectoryProvider accepted a symlinked ancestor")
	}
}

func TestDirectoryProviderRejectsRootReplacedAfterConstruction(t *testing.T) {
	parent := canonicalTempDir(t)
	dir := filepath.Join(parent, "definitions")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	provider, err := NewDirectoryProvider("local", dir)
	if err != nil {
		t.Fatal(err)
	}
	moved := filepath.Join(parent, "moved-definitions")
	if err := os.Rename(dir, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, dir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if _, err := provider.Documents(); err == nil {
		t.Fatal("Documents accepted a root replaced by a symlink after construction")
	}
}

func TestBuiltinProviderUsesBuiltinNamespace(t *testing.T) {
	provider := BuiltinProvider{}
	if provider.Source() != BuiltinSource {
		t.Fatalf("source = %q", provider.Source())
	}
	documents, err := provider.Documents()
	if err != nil {
		t.Fatalf("Documents: %v", err)
	}
	if len(documents) != 4 || documents[0].Path != "definitions/nyaa.yml" {
		t.Fatalf("builtin documents = %#v", documents)
	}
}
