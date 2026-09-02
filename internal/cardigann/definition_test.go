package cardigann

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestDefinitionSettingsExposeAddIndexerFields(t *testing.T) {
	src := []byte(`
id: settings-fixture
name: Settings Fixture
links: [https://example.com]
settings:
  - {name: username, type: text, label: Username}
  - {name: password, type: password, label: Password}
  - {name: freeleech, type: checkbox, label: Freeleech only, default: false}
  - name: sort
    type: select
    label: Sort requested from site
    default: added
    options: {added: Created, seeders: Seeders}
  - {name: info_tpp, type: info, label: Results per page, default: Use 100 results per page.}
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search}]
  rows: {selector: article}
  fields:
    title: {selector: h2}
    download: {selector: a, attribute: href}
`)
	definition, err := ParseDefinition(src)
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	want := []SettingSchema{
		{Name: "username", Label: "Username", Type: "text", Editable: true},
		{Name: "password", Label: "Password", Type: "password", Secret: true, Editable: true},
		{Name: "freeleech", Label: "Freeleech only", Type: "checkbox", Default: "false", Editable: true},
		{Name: "sort", Label: "Sort requested from site", Type: "select", Default: "added", Editable: true, Options: []SettingOption{{Value: "added", Label: "Created"}, {Value: "seeders", Label: "Seeders"}}},
		{Name: "info_tpp", Label: "Results per page", Type: "info", Default: "Use 100 results per page."},
	}
	if got := definition.SettingSchemas(); !reflect.DeepEqual(got, want) {
		t.Fatalf("SettingSchemas = %#v, want %#v", got, want)
	}
}

func TestDefinitionAcceptsDescriptiveV11RootFieldsAndEncoding(t *testing.T) {
	def, err := ParseDefinition([]byte(`
id: descriptive
name: Descriptive Fixture
description: Synthetic tracker fixture
language: ru-RU
type: private
encoding: windows-1251
links: [https://tracker.example]
legacylinks: [https://old.tracker.example]
replaces: [old-descriptive]
testlinktorrent: true
caps:
  categories: {Other: Other}
  allowrawsearch: true
  allowtvsearchimdb: true
  modes: {search: [q]}
search: {paths: [{path: /search}], rows: {selector: article}, fields: {title: {selector: h2}, download: {selector: a, attribute: href}}}
`))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	if def.Encoding != "windows-1251" || !reflect.DeepEqual(def.LegacyLinks, []string{"https://old.tracker.example"}) || !reflect.DeepEqual(def.Replaces, []string{"old-descriptive"}) || !def.TestLinkTorrent || !def.Caps.AllowRawSearch || !def.Caps.AllowTVSearchIMDb || def.Caps.Categories["Other"] != "Other" {
		t.Fatalf("descriptive fields = %+v", def)
	}
}

func TestDefinitionAcceptsSharedSearchIOAndSinglePath(t *testing.T) {
	def, err := ParseDefinition([]byte(`
id: shared-io
name: Shared IO Fixture
links: [https://tracker.example]
caps: {modes: {search: [q]}}
search:
  path: /search
  inputs: {q: "{{ .Keywords }}"}
  headers: {Accept: application/json}
  allowEmptyInputs: true
  rows: {selector: article}
  fields: {title: {selector: h2}, download: {selector: a, attribute: href}}
`))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	if len(def.Search.Paths) != 1 || def.Search.Paths[0].Path != "/search" || def.Search.Inputs["q"] == "" || def.Search.Headers["Accept"] != "application/json" || !def.Search.AllowEmptyInputs {
		t.Fatalf("shared search contract = %+v", def.Search)
	}
}

func TestParseDefinitionAcceptsSingleValueHeaderLists(t *testing.T) {
	definition, err := ParseDefinition([]byte(`
id: header-list-fixture
name: Header List Fixture
links: [https://tracker.example]
settings: [{name: apikey, type: password}]
caps: {modes: {search: [q]}}
search:
  headers:
    Authorization: ["Bearer {{ .Config.apikey }}"]
  paths: [{path: /search}]
  rows: {selector: article}
  fields:
    title: {text: title}
    download: {text: "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
`))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	if got := definition.Search.Headers["Authorization"]; got != "Bearer {{ .Config.apikey }}" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestDefinitionSuppliesSafeGuidanceForSpecializedInfoSettings(t *testing.T) {
	def, err := ParseDefinition([]byte(`
id: info-fixture
name: Info Fixture
links: [https://tracker.example]
settings:
  - {name: info_category_8000, type: info_category_8000}
caps: {modes: {search: [q]}}
search: {paths: [{path: /search}], rows: {selector: article}, fields: {title: {selector: h2}, download: {selector: a, attribute: href}}}
`))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	fields := def.SettingSchemas()
	if len(fields) != 1 || fields[0].Type != "info" || fields[0].Editable || fields[0].Label == "" || fields[0].Default == "" {
		t.Fatalf("specialized info schema = %#v", fields)
	}
}

func TestParseDefinitionRejectsLoginWithUnknownMethod(t *testing.T) {
	src := []byte(`
id: unsupported-login
name: Unsupported Login
links: [https://example.com]
login:
  path: /login
  method: oneurl
caps:
  categorymappings: [{id: 1, cat: Movies}]
  modes: {search: [q]}
search:
  paths: [{path: /search}]
  rows: {selector: tr}
  fields:
    title: {selector: .title}
    download: {selector: .download}
`)

	_, err := ParseDefinition(src)
	if err == nil {
		t.Fatal("ParseDefinition accepted unsupported login block")
	}
	if !strings.Contains(err.Error(), "unsupported method") {
		t.Fatalf("ParseDefinition error = %q", err)
	}
}

func TestParseDefinitionRejectsFeaturesEngineCannotExecute(t *testing.T) {
	base := `
id: fixture
name: Fixture
links: [https://example.com]
caps:
  categorymappings: [{id: 1, cat: Movies}]
  modes: {search: [q]}
search:
  paths:
    - path: /search
%s
  rows: {selector: tr}
  fields:
    title:
      selector: .title
%s
    download: {selector: .download}
`
	tests := []struct {
		name     string
		root     string
		path     string
		field    string
		wantText string
	}{

		{name: "noncanonical header", path: "      headers: {\" Accept \": application/json}\n", wantText: "header name"},
		{name: "duplicate header", path: "      headers: {Accept: application/json, accept: application/xml}\n", wantText: "duplicate header"},

		{name: "malformed supported filter", field: "      filters: [{name: dateparse}]\n", wantText: "dateparse format must be a string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			src := []byte(tt.root + fmt.Sprintf(base, tt.path, tt.field))
			_, err := ParseDefinition(src)
			if err == nil || !strings.Contains(err.Error(), tt.wantText) {
				t.Fatalf("ParseDefinition error = %q, want %q", err, tt.wantText)
			}
		})
	}
}

func TestParseDefinitionRejectsRowSemanticsOnUnsupportedResponseTypes(t *testing.T) {
	tests := []struct {
		name     string
		response string
		rows     string
		want     string
	}{
		{name: "after on JSON", response: "json", rows: "after: 1", want: "row after requires HTML"},
		{name: "date headers on JSON", response: "json", rows: "dateheaders: {selector: .date}", want: "row date headers require HTML"},
		{name: "count on HTML", response: "html", rows: "count: {selector: total}", want: "row count requires JSON"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			document := fmt.Sprintf(`
id: fixture
name: Fixture
links: [https://example.com]
search:
  paths:
    - path: /search
      response: {type: %s}
  rows:
    selector: items
    %s
  fields:
    title: {selector: title}
    download: {selector: download}
`, tt.response, tt.rows)
			_, err := ParseDefinition([]byte(document))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("ParseDefinition error = %q, want %q", err, tt.want)
			}
		})
	}
}

func TestParseDefinitionRejectsAdditionalYAMLDocuments(t *testing.T) {
	src := []byte(`
id: first
name: First
links: [https://example.com]
caps:
  categorymappings: [{id: 1, cat: Movies}]
  modes: {search: [q]}
search:
  paths: [{path: /search}]
  rows: {selector: tr}
  fields:
    title: {selector: .title}
    download: {selector: .download}
---
login:
  path: /login
`)
	_, err := ParseDefinition(src)
	if err == nil || !strings.Contains(err.Error(), "one YAML document") {
		t.Fatalf("ParseDefinition error = %q, want additional-document rejection", err)
	}
}

func TestDefinitionParsingRejectsYAMLAliasesAndCustomTags(t *testing.T) {
	base := `
id: fixture
name: %s
description: %s
type: public
links: [https://tracker.example]
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search}]
  rows: {selector: article}
  fields:
    title: {selector: h2}
    download: {selector: a, attribute: href}
`
	tests := map[string]string{
		"alias":      fmt.Sprintf(base, "&shared Fixture", "*shared"),
		"custom-tag": fmt.Sprintf(base, "!unsafe Fixture", "plain"),
	}
	for name, document := range tests {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseManifest("user", []byte(document)); err == nil {
				t.Fatal("ParseManifest accepted unsupported YAML graph feature")
			}
			if _, err := ParseDefinition([]byte(document)); err == nil {
				t.Fatal("ParseDefinition accepted unsupported YAML graph feature")
			}
		})
	}
}

func TestParseDefinitionRejectsExcessiveSearchPaths(t *testing.T) {
	var paths strings.Builder
	for i := 0; i < 17; i++ {
		fmt.Fprintf(&paths, "    - {path: /search/%d}\n", i)
	}
	document := fmt.Sprintf(`
id: fixture
name: Fixture
type: public
links: [https://tracker.example]
caps: {modes: {search: [q]}}
search:
  paths:
%s  rows: {selector: article}
  fields:
    title: {selector: h2}
    download: {selector: a, attribute: href}
`, paths.String())
	_, err := ParseDefinition([]byte(document))
	if err == nil || !strings.Contains(err.Error(), "search paths") {
		t.Fatalf("ParseDefinition error = %v, want path-count rejection", err)
	}
}
