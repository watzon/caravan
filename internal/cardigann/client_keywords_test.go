package cardigann

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSanitizeSearchKeywords(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "Marvel's Agents of S.H.I.E.L.D.", want: "Marvels Agents of SHIELD"},
		{in: "Fast & Furious", want: "Fast and Furious"},
		{in: "Blade Runner 2049: The Final Cut", want: "Blade Runner 2049 The Final Cut"},
		{in: "Ubuntu 24.04", want: "Ubuntu 24.04"},
		{in: "Место встречи изменить нельзя", want: "Место встречи изменить нельзя"},
		{in: "Whats Love Got to Do with It?", want: "Whats Love Got to Do with It"},
		{in: "  spaced   out  ", want: "spaced out"},
	}
	for _, test := range tests {
		if got := sanitizeSearchKeywords(test.in); got != test.want {
			t.Fatalf("sanitizeSearchKeywords(%q) = %q, want %q", test.in, got, test.want)
		}
	}
}

// Regression: the client must hand the engine sanitized keywords, scraped sites
// match literal text, so "Marvel's S.H.I.E.L.D." style metadata titles would
// otherwise return nothing.
func TestClientSearchSanitizesKeywordsForScrapedSites(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
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
caps:
  categorymappings:
    - {id: 1, cat: Movies, desc: Movies}
  modes:
    search: [q]
search:
  paths:
    - path: "search/{{ .Keywords }}/"
  rows:
    selector: "table.results tr.release"
  fields:
    title:
      selector: "a.title"
    download:
      selector: "a.title"
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
	client := &Client{engine: engine}
	if _, err := client.Search(context.Background(), "Marvel's Agents of S.H.I.E.L.D.", nil); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotPath != "/search/Marvels Agents of SHIELD/" {
		t.Fatalf("path = %q, want sanitized keywords", gotPath)
	}
}
