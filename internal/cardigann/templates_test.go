package cardigann

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Regression: a .Config lookup passed as one argument of a template function
// must stay a single argument after normalization (Prowlarr definitions such
// as torrentdownload use `{{ re_replace .Config.sort "_" "" }}`).
func TestNormalizeDefinitionTemplateKeepsLookupUsableAsFunctionArgument(t *testing.T) {
	normalized := normalizeDefinitionTemplate(`search{{ re_replace .Config.sort "_" "" }}?q={{ .Keywords }}`)
	if want := `search{{ re_replace (index .Config "sort") "_" "" }}?q={{ .Keywords }}`; normalized != want {
		t.Fatalf("normalized = %q, want %q", normalized, want)
	}
}

func TestEngineSearchRendersConfigLookupInsideTemplateFunction(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><table class="results"></table>`)
	}))
	defer upstream.Close()

	src := []byte(fmt.Sprintf(`
id: fixture
name: Fixture Tracker
language: en-US
type: public
links:
  - %s/
settings:
  - name: sort
    type: select
    label: Sort
    default: d
    options:
      d: created
      _: seeders
caps:
  categorymappings:
    - {id: 1, cat: Movies, desc: Movies}
  modes:
    search: [q]
search:
  paths:
    - path: "search{{ re_replace .Config.sort \"_\" \"\" }}?q={{ .Keywords }}"
  rows:
    selector: "table.results tr.release"
  fields:
    title:
      selector: "a.title"
    download:
      selector: "a.download"
      attribute: href
`, upstream.URL))

	def, err := ParseDefinition(src)
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(def, Config{BaseURL: upstream.URL}, upstream.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := engine.Search(context.Background(), Query{Keywords: "ubuntu"}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotPath != "/searchd" {
		t.Fatalf("path = %q, want /searchd", gotPath)
	}
}

// Regression: a helper field whose regexp filter matches nothing (for example
// LimeTorrents' category_is_tv_show probe on a movie row) must only blank that
// field for that row, never abort the search.
func TestEngineSearchKeepsRowsWhenHelperFieldFilterDoesNotMatch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><table class="results">
			<tr class="release"><td><a class="title" href="/t/1">Some Show S01E02</a></td></tr>
			<tr class="release"><td><a class="title" href="/t/2">Some Movie 2024</a></td></tr>
		</table>`)
	}))
	defer upstream.Close()

	src := []byte(fmt.Sprintf(`
id: fixture
name: Fixture Tracker
language: en-US
type: public
links:
  - %s/
caps:
  categorymappings:
    - {id: "TV shows", cat: TV, desc: TV}
    - {id: "Other", cat: Other, desc: Other}
  modes:
    search: [q]
search:
  paths:
    - path: "search"
  rows:
    selector: "table.results tr.release"
  fields:
    title:
      selector: "a.title"
    download:
      selector: "a.title"
      attribute: href
    category_is_tv_show:
      text: "{{ .Result.title }}"
      filters:
        - name: regexp
          args: "\\b(S\\d+(?:E\\d+)?)\\b"
    category:
      text: "{{ if .Result.category_is_tv_show }}TV shows{{ else }}Other{{ end }}"
`, upstream.URL))

	def, err := ParseDefinition(src)
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(def, Config{BaseURL: upstream.URL}, upstream.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	releases, err := engine.Search(context.Background(), Query{Keywords: "some"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 2 {
		t.Fatalf("releases = %d, want both rows kept", len(releases))
	}
	if len(releases[0].Categories) != 1 || releases[0].Categories[0] != 5000 {
		t.Fatalf("TV row categories = %v, want [5000]", releases[0].Categories)
	}
	if len(releases[1].Categories) != 1 || releases[1].Categories[0] != 8000 {
		t.Fatalf("movie row categories = %v, want Other/8000", releases[1].Categories)
	}
}

// Regression: single-character setting values (select defaults such as sort=d)
// must not be substituted out of error messages (that shreds unrelated words)
// while real secret values must still be redacted.
func TestRedactErrorKeepsShortNonSecretFragmentsIntact(t *testing.T) {
	src := []byte(`
id: fixture
name: Fixture Tracker
language: en-US
type: public
links:
  - https://fixture.example/
settings:
  - name: sort
    type: select
    label: Sort
    default: d
    options:
      d: created
      _: seeders
  - name: password
    type: password
    label: Password
caps:
  categorymappings:
    - {id: 1, cat: Movies, desc: Movies}
  modes:
    search: [q]
search:
  paths:
    - path: "search"
  rows:
    selector: "tr"
  fields:
    title:
      selector: "a"
    download:
      selector: "a"
      attribute: href
`)
	def, err := ParseDefinition(src)
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(def, Config{Settings: map[string]string{"password": "hunter22"}}, &http.Client{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	redacted := engine.redactError(fmt.Errorf("render template failed for password hunter22")).Error()
	if strings.Contains(redacted, "hunter22") {
		t.Fatalf("secret survived redaction: %q", redacted)
	}
	if !strings.Contains(redacted, "render template failed") {
		t.Fatalf("non-secret words were shredded: %q", redacted)
	}
}

func TestNormalizeDefinitionTemplateDropsStrayClosingParen(t *testing.T) {
	source := `{{ if and (.Keywords) (eq .Config.disablesort .False)) }}sort-{{ else }}{{ end }}/{{ .Keywords }}`
	got := normalizeDefinitionTemplate(source)
	want := `{{ if and (.Keywords) (eq (index .Config "disablesort") .False) }}sort-{{ else }}{{ end }}/{{ .Keywords }}`
	if got != want {
		t.Fatalf("normalizeDefinitionTemplate = %q, want %q", got, want)
	}
	balanced := `{{ re_replace (index .Config "sort") "_" "" }}`
	if got := normalizeDefinitionTemplate(balanced); got != balanced {
		t.Fatalf("balanced template changed: %q", got)
	}
}
