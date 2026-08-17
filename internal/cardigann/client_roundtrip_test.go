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

func TestRuTorDefinitionSupportsDirectClientAndTorznabRoundTrip(t *testing.T) {
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search/0/0/100/0/debian" && r.URL.Path != "/search/0/0/100/0/" {
			t.Fatalf("search path = %q", r.URL.Path)
		}
		fmt.Fprint(w, `<table><tr class='gai'>
			<td class='date'>14 Aug 2026</td>
			<td class='release'><a href='/download/42'>file</a><a href='magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567'>magnet</a><a href='/torrent/42/debian'>Debian 13 netinst</a></td>
			<td class='size'>690 MiB</td><td class='peers'><span class='green'>17</span><span class='red'>2</span></td>
		</tr></table>`)
	}))
	defer tracker.Close()

	registry, err := cardigann.LoadBuiltins()
	if err != nil {
		t.Fatalf("LoadBuiltins: %v", err)
	}
	if _, ok := registry.Get("rutor"); !ok {
		t.Fatal("rutor definition is missing")
	}

	direct := cardigann.NewClient(registry, core.IndexerConfig{
		ID:           41,
		Name:         "RuTor fixture",
		DefinitionID: "rutor",
		URL:          tracker.URL,
	}, tracker.Client())
	releases, err := direct.Search(context.Background(), "debian", nil)
	if err != nil {
		t.Fatalf("direct Search: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("direct releases = %d, want 1", len(releases))
	}
	if got := releases[0]; got.Title != "Debian 13 netinst" || got.IndexerID != 41 || got.Seeders != 17 || got.Leechers != 2 {
		t.Fatalf("direct release = %+v", got)
	}

	feed := httptest.NewServer(cardigann.NewClientTorznabHandler("RuTor fixture", direct))
	defer feed.Close()
	viaTorznab := indexer.New(core.IndexerConfig{ID: 42, Name: "RuTor through feed", URL: feed.URL, Type: core.IndexerTypeTorznab}, feed.Client())
	if err := viaTorznab.Test(context.Background()); err != nil {
		t.Fatalf("Torznab Test: %v", err)
	}
	releases, err = viaTorznab.Search(context.Background(), "debian", nil)
	if err != nil {
		t.Fatalf("Torznab Search: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("Torznab releases = %d, want 1", len(releases))
	}
	if got := releases[0]; got.Title != "Debian 13 netinst" || got.IndexerID != 42 || !strings.EqualFold(got.InfoHash, "0123456789abcdef0123456789abcdef01234567") {
		t.Fatalf("Torznab release = %+v", got)
	}
}

func TestNyaaDefinitionSupportsDirectClientAndTorznabRoundTrip(t *testing.T) {
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" || r.URL.Query().Get("f") != "0" || r.URL.Query().Get("c") != "0_0" {
			t.Fatalf("search URL = %s", r.URL.String())
		}
		fmt.Fprint(w, `<table class='torrent-list'><tr><th>header</th></tr><tr class='default'>
			<td><a href='/?c=1_0'>category</a></td>
			<td colspan='2'><a href='/view/9'>Example subtitle pack</a></td>
			<td><a href='/download/9.torrent'>file</a><a href='magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01'>magnet</a></td>
			<td>1.25 GiB</td><td>2026-08-14</td><td>29</td><td>4</td>
		</tr></table>`)
	}))
	defer tracker.Close()

	registry, err := cardigann.LoadBuiltins()
	if err != nil {
		t.Fatalf("LoadBuiltins: %v", err)
	}
	if _, ok := registry.Get("nyaa"); !ok {
		t.Fatal("nyaa definition is missing")
	}

	direct := cardigann.NewClient(registry, core.IndexerConfig{ID: 51, Name: "Nyaa fixture", DefinitionID: "nyaa", URL: tracker.URL}, tracker.Client())
	releases, err := direct.Search(context.Background(), "subtitle pack", nil)
	if err != nil {
		t.Fatalf("direct Search: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("direct releases = %d, want 1", len(releases))
	}
	if got := releases[0]; got.Title != "Example subtitle pack" || got.IndexerID != 51 || got.Size != 1342177280 || got.Seeders != 29 || got.Leechers != 4 {
		t.Fatalf("direct release = %+v", got)
	}

	feed := httptest.NewServer(cardigann.NewClientTorznabHandler("Nyaa fixture", direct))
	defer feed.Close()
	viaTorznab := indexer.New(core.IndexerConfig{ID: 52, Name: "Nyaa through feed", URL: feed.URL, Type: core.IndexerTypeTorznab}, feed.Client())
	if err := viaTorznab.Test(context.Background()); err != nil {
		t.Fatalf("Torznab Test: %v", err)
	}
	releases, err = viaTorznab.Search(context.Background(), "subtitle pack", nil)
	if err != nil {
		t.Fatalf("Torznab Search: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("Torznab releases = %d, want 1", len(releases))
	}
	if got := releases[0]; got.Title != "Example subtitle pack" || got.IndexerID != 52 || !strings.EqualFold(got.InfoHash, "abcdef0123456789abcdef0123456789abcdef01") {
		t.Fatalf("Torznab release = %+v", got)
	}
}

func TestTokyoToshoDefinitionSupportsDirectClientAndTorznabRoundTrip(t *testing.T) {
	tracker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/search.php" || r.URL.Query().Get("cat") != "0" {
			t.Fatalf("search URL = %s", r.URL.String())
		}
		fmt.Fprint(w, `<table class='listing'><tr class='shade'>
			<td><a href='?cat=2'>category</a></td>
			<td class='desc-top'><a href='magnet:?xt=urn:btih:fedcba9876543210fedcba9876543210fedcba98'>magnet</a><a href='/files/77.torrent'>Example concert recording</a></td>
			<td class='web'><a href='details.php?id=77'>details</a></td>
		</tr></table>`)
	}))
	defer tracker.Close()

	registry, err := cardigann.LoadBuiltins()
	if err != nil {
		t.Fatalf("LoadBuiltins: %v", err)
	}
	if _, ok := registry.Get("tokyotosho"); !ok {
		t.Fatal("tokyotosho definition is missing")
	}

	direct := cardigann.NewClient(registry, core.IndexerConfig{ID: 61, Name: "TokyoTosho fixture", DefinitionID: "tokyotosho", URL: tracker.URL}, tracker.Client())
	releases, err := direct.Search(context.Background(), "concert recording", nil)
	if err != nil {
		t.Fatalf("direct Search: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("direct releases = %d, want 1", len(releases))
	}
	if got := releases[0]; got.Title != "Example concert recording" || got.IndexerID != 61 || got.GUID != tracker.URL+"/details.php?id=77" {
		t.Fatalf("direct release = %+v", got)
	}

	feed := httptest.NewServer(cardigann.NewClientTorznabHandler("TokyoTosho fixture", direct))
	defer feed.Close()
	viaTorznab := indexer.New(core.IndexerConfig{ID: 62, Name: "TokyoTosho through feed", URL: feed.URL, Type: core.IndexerTypeTorznab}, feed.Client())
	if err := viaTorznab.Test(context.Background()); err != nil {
		t.Fatalf("Torznab Test: %v", err)
	}
	releases, err = viaTorznab.Search(context.Background(), "concert recording", nil)
	if err != nil {
		t.Fatalf("Torznab Search: %v", err)
	}
	if len(releases) != 1 {
		t.Fatalf("Torznab releases = %d, want 1", len(releases))
	}
	if got := releases[0]; got.Title != "Example concert recording" || got.IndexerID != 62 || !strings.EqualFold(got.InfoHash, "fedcba9876543210fedcba9876543210fedcba98") {
		t.Fatalf("Torznab release = %+v", got)
	}
}
