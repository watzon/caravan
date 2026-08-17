package cardigann

import (
	"errors"
	"reflect"
	"testing"
)

type registryTestProvider struct {
	source string
	docs   []SourceDocument
	err    error
}

func (p registryTestProvider) Source() string                       { return p.source }
func (p registryTestProvider) Documents() ([]SourceDocument, error) { return p.docs, p.err }

func runnableRegistryDocument(id string) SourceDocument {
	return SourceDocument{Path: "definitions/" + id + ".yml", Data: []byte("id: " + id + "\nname: " + id + "\nlinks: [https://tracker.example]\ncaps: {modes: {search: [q]}}\nsearch: {paths: [{path: /search}], rows: {selector: article}, fields: {title: {selector: h2}, download: {selector: a, attribute: href}}}\n")}
}

func TestPrepareProviderDoesNotMutateRegistryOnProblem(t *testing.T) {
	registry := newRegistry()
	provider := registryTestProvider{source: "community", docs: []SourceDocument{
		runnableRegistryDocument("valid"),
		{Path: "definitions/bad.yml", Data: []byte("not: [valid")},
	}}
	prepared, manifests, problems := registry.PrepareProvider(provider)
	if prepared != nil || len(manifests) != 1 || len(problems) == 0 {
		t.Fatalf("PrepareProvider = (%v, %d manifests, %v problems), want rejected atomic prepare", prepared, len(manifests), problems)
	}
	if _, ok := registry.Get("community:valid"); ok || len(registry.sources) != 0 || len(registry.seen) != 0 {
		t.Fatalf("failed prepare mutated registry: definitions=%d sources=%d seen=%d", len(registry.definitions), len(registry.sources), len(registry.seen))
	}
}

func TestPrepareProviderDocumentsErrorDoesNotReserveSource(t *testing.T) {
	registry := newRegistry()
	if prepared, _, problems := registry.PrepareProvider(registryTestProvider{source: "community", err: errors.New("boom")}); prepared != nil || len(problems) != 1 {
		t.Fatalf("PrepareProvider documents failure = (%v, %v)", prepared, problems)
	}
	if _, ok := registry.sources["community"]; ok {
		t.Fatal("failed documents load reserved source")
	}
	prepared, _, problems := registry.PrepareProvider(registryTestProvider{source: "community", docs: []SourceDocument{runnableRegistryDocument("valid")}})
	if len(problems) != 0 || prepared == nil {
		t.Fatalf("retry prepare = (%v, %v)", prepared, problems)
	}
	prepared.Commit()
	if _, ok := registry.Get("community:valid"); !ok {
		t.Fatal("committed provider missing")
	}
}

func TestDefinitionRefKeepsBuiltinBareIDLookupCompatible(t *testing.T) {
	registry, err := LoadBuiltins()
	if err != nil {
		t.Fatalf("LoadBuiltins: %v", err)
	}

	bare, ok := registry.Get("nyaa")
	if !ok {
		t.Fatal("Get(nyaa) = false")
	}
	namespaced, ok := registry.Get("builtin:nyaa")
	if !ok {
		t.Fatal("Get(builtin:nyaa) = false")
	}
	if bare != namespaced {
		t.Fatal("bare builtin lookup did not resolve the builtin definition")
	}
	ref, err := ParseDefinitionRef("builtin:nyaa")
	if err != nil || ref.String() != "builtin:nyaa" {
		t.Fatalf("ParseDefinitionRef = %#v, %v", ref, err)
	}
}

func TestBuiltinsContainRunnablePublicDefinitions(t *testing.T) {
	registry, err := LoadBuiltins()
	if err != nil {
		t.Fatalf("LoadBuiltins: %v", err)
	}
	for id, name := range map[string]string{
		"thepiratebay": "The Pirate Bay",
		"nyaa":         "Nyaa",
		"rutor":        "RuTor",
		"tokyotosho":   "TokyoTosho",
	} {
		definition, ok := registry.Get(id)
		if !ok {
			t.Fatalf("%s definition is missing", id)
		}
		if definition.Name != name {
			t.Fatalf("%s name = %q, want %q", id, definition.Name, name)
		}
	}
	if len(registry.All()) != 4 {
		t.Fatalf("builtins = %d, want 4", len(registry.All()))
	}
}

func TestRegistryExposesEditableSettingNamesAndFullAddIndexerSchema(t *testing.T) {
	document := runnableRegistryDocument("settings")
	document.Data = []byte(`
id: settings
name: Settings
links: [https://tracker.example]
settings:
  - {name: password, type: password, label: Password}
  - {name: info_category_8000, type: info_category_8000, label: Category help, default: Adult categories are configured separately.}
caps: {modes: {search: [q]}}
search: {paths: [{path: /search}], rows: {selector: article}, fields: {title: {selector: h2}, download: {selector: a, attribute: href}}}
`)
	registry, _, err := LoadProviders(registryTestProvider{source: "managed", docs: []SourceDocument{document}})
	if err != nil {
		t.Fatalf("LoadProviders: %v", err)
	}
	if names, ok := registry.SettingNames("managed:settings"); !ok || !reflect.DeepEqual(names, []string{"password"}) {
		t.Fatalf("SettingNames = %v, %v", names, ok)
	}
	want := []SettingSchema{
		{Name: "password", Label: "Password", Type: "password", Secret: true, Editable: true},
		{Name: "info_category_8000", Label: "Category help", Type: "info", Default: "Adult categories are configured separately."},
	}
	if fields, ok := registry.SettingSchemas("managed:settings"); !ok || !reflect.DeepEqual(fields, want) {
		t.Fatalf("SettingSchemas = %#v, %v, want %#v", fields, ok, want)
	}
}
