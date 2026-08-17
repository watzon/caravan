package cardigann_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/cardigann"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/indexer"
)

func TestTorznabHandlerIsConsumableByCaravanClient(t *testing.T) {
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<table><tr class="release">
			<td><a class="title" href="/details/1">Example.Movie.2026.1080p</a></td>
			<td><a class="download" href="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567">magnet</a></td>
			<td class="category">1</td><td class="size">2 GB</td>
			<td class="seeders">20</td><td class="leechers">3</td>
		</tr></table>`)
	}))
	defer tracker.Close()

	def, err := cardigann.ParseDefinition([]byte(fmt.Sprintf(`
id: fixture
name: Fixture Tracker
links: [%s]
caps:
  categorymappings: [{id: 1, cat: Movies}]
  modes: {search: [q]}
search:
  paths: [{path: /search}]
  rows: {selector: tr.release}
  fields:
    title: {selector: a.title}
    details: {selector: a.title, attribute: href}
    download: {selector: a.download, attribute: href}
    category: {selector: td.category}
    size: {selector: td.size}
    seeders: {selector: td.seeders}
    leechers: {selector: td.leechers}
`, tracker.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := cardigann.New(def, cardigann.Config{BaseURL: tracker.URL}, tracker.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	proxy := httptest.NewServer(cardigann.NewTorznabHandler(engine))
	defer proxy.Close()

	client := indexer.New(core.IndexerConfig{
		ID:   42,
		Name: "Local Fixture",
		URL:  proxy.URL,
		Type: core.IndexerTypeTorznab,
	}, proxy.Client())
	if err := client.Test(context.Background()); err != nil {
		t.Fatalf("Test through local proxy: %v", err)
	}
	categories, err := client.Categories(context.Background())
	if err != nil {
		t.Fatalf("Categories: %v", err)
	}
	if len(categories) != 1 || categories[0].ID != 2000 {
		t.Fatalf("categories = %+v", categories)
	}
	releases, err := client.Search(context.Background(), "example", nil)
	if err != nil {
		t.Fatalf("Search through local proxy: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("releases = %d, want 1", len(releases))
	}
	got := releases[0]
	if got.IndexerID != 42 || got.Indexer != "Local Fixture" || got.Title != "Example.Movie.2026.1080p" {
		t.Fatalf("release = %+v", got)
	}
	if got.Size != 2<<30 || got.Seeders != 20 || got.Leechers != 3 {
		t.Fatalf("release size/swarm = %d/%d/%d", got.Size, got.Seeders, got.Leechers)
	}
}

type searchOnlySource struct{ called bool }

func (s *searchOnlySource) Search(context.Context, string, []int) ([]core.Release, error) {
	s.called = true
	return nil, nil
}

func (s *searchOnlySource) Categories(context.Context) ([]core.IndexerCategory, error) {
	return []core.IndexerCategory{}, nil
}

func (s *searchOnlySource) Modes() map[string]bool { return map[string]bool{"search": true} }

func TestTorznabHandlerRejectsUnadvertisedSearchMode(t *testing.T) {
	source := &searchOnlySource{}
	handler := cardigann.NewClientTorznabHandler("search only", source)
	request := httptest.NewRequest(http.MethodGet, "/?t=tvsearch&q=example", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `code="202"`) {
		t.Fatalf("response = %d %s, want Torznab unsupported-function error", response.Code, response.Body.String())
	}
	if source.called {
		t.Fatal("unadvertised search mode reached the source")
	}
}
