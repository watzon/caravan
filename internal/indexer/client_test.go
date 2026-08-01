package indexer

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// testAPIKey is the key every stubbed client sends; tests assert it never
// escapes into an error message.
const testAPIKey = "test-key-do-not-log"

// response is one canned HTTP reply.
type response struct {
	status int
	body   []byte
}

// stub is a fake indexer. Each `t` mode maps to a response; anything not in
// the map is answered with the Torznab "no such function" error document, the
// same as a real indexer would.
type stub struct {
	mu       sync.Mutex
	modes    map[string]response
	requests []url.Values
}

func (s *stub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()

	q := r.URL.Query()
	s.requests = append(s.requests, q)

	if r.URL.Path != "/api" {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	resp, ok := s.modes[q.Get("t")]
	if !ok {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(readFixture(nil, "error_no_such_function.xml"))
		return
	}

	w.Header().Set("Content-Type", "application/xml")
	w.WriteHeader(resp.status)
	_, _ = w.Write(resp.body)
}

func (s *stub) seen() []url.Values {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]url.Values(nil), s.requests...)
}

// newStub returns a client pointed at a fake indexer serving modes.
func newStub(t *testing.T, cfg core.IndexerConfig, modes map[string]response) (*Client, *stub) {
	t.Helper()

	s := &stub{modes: modes}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)

	cfg.URL = srv.URL
	if cfg.APIKey == "" {
		cfg.APIKey = testAPIKey
	}
	return New(cfg, srv.Client()), s
}

// torznabCfg and newznabCfg are the two flavors under test.
func torznabCfg() core.IndexerConfig {
	return core.IndexerConfig{ID: 7, Name: "Example Tracker", Type: core.IndexerTypeTorznab, Enabled: true}
}

func newznabCfg() core.IndexerConfig {
	return core.IndexerConfig{ID: 9, Name: "Example Usenet", Type: core.IndexerTypeNewznab, Enabled: true}
}

// ok serves a fixture with 200.
func ok(t *testing.T, name string) response {
	t.Helper()
	return response{status: http.StatusOK, body: readFixture(t, name)}
}

// readFixture reads a recorded indexer response. t may be nil when called off
// the test goroutine, where a missing file must panic rather than silently
// serve nothing.
func readFixture(t *testing.T, name string) []byte {
	if t != nil {
		t.Helper()
	}
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		if t == nil {
			panic(err)
		}
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestSearchTorznabParsesItems(t *testing.T) {
	c, _ := newStub(t, torznabCfg(), map[string]response{"search": ok(t, "torznab_search.xml")})

	rels, err := c.Search(context.Background(), "blade runner", []int{2000})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rels) != 3 {
		t.Fatalf("got %d releases, want 3", len(rels))
	}

	got := rels[0]
	if got.IndexerID != 7 || got.Indexer != "Example Tracker" {
		t.Errorf("indexer identity = %d/%q, want 7/%q", got.IndexerID, got.Indexer, "Example Tracker")
	}
	if want := "Blade Runner 2049 2017 1080p BluRay x264-SPARKS"; got.Title != want {
		t.Errorf("Title = %q, want %q", got.Title, want)
	}
	if want := "https://tracker.example/details/9f1c2b"; got.GUID != want {
		t.Errorf("GUID = %q, want %q", got.GUID, want)
	}
	if !strings.HasPrefix(got.DownloadURL, "https://tracker.example/download/9f1c2b.torrent") {
		t.Errorf("DownloadURL = %q, want the enclosure url", got.DownloadURL)
	}
	if want := "7a1b2c3d4e5f60718293a4b5c6d7e8f901234567"; got.InfoHash != want {
		t.Errorf("InfoHash = %q, want %q (lowercased)", got.InfoHash, want)
	}
	if got.Protocol != core.ProtocolTorrent {
		t.Errorf("Protocol = %q, want %q", got.Protocol, core.ProtocolTorrent)
	}
	if got.Size != 12884901888 {
		t.Errorf("Size = %d, want 12884901888", got.Size)
	}
	if got.Seeders != 42 {
		t.Errorf("Seeders = %d, want 42", got.Seeders)
	}
	// peers is the whole swarm, so leechers is peers - seeders.
	if got.Leechers != 13 {
		t.Errorf("Leechers = %d, want 13", got.Leechers)
	}
	if want := time.Date(2017, 1, 2, 15, 4, 5, 0, time.FixedZone("", -7*3600)); !got.PublishedAt.Equal(want) {
		t.Errorf("PublishedAt = %v, want %v", got.PublishedAt, want)
	}
	if got.Parsed.Title != "Blade Runner 2049" || got.Parsed.Year != 2017 {
		t.Errorf("Parsed title/year = %q/%d, want %q/2017", got.Parsed.Title, got.Parsed.Year, "Blade Runner 2049")
	}
	if got.Parsed.Quality != core.Quality1080p || got.Parsed.Source != core.SourceBluray {
		t.Errorf("Parsed quality/source = %q/%q, want %q/%q", got.Parsed.Quality, got.Parsed.Source, core.Quality1080p, core.SourceBluray)
	}

	// The second item has no enclosure and no <size>: everything comes from
	// the link and the torznab attrs.
	got = rels[1]
	if !strings.HasPrefix(got.DownloadURL, "magnet:?xt=urn:btih:aaaabbbb") {
		t.Errorf("DownloadURL = %q, want the magnet link", got.DownloadURL)
	}
	if got.GUID != "expanse-s04e01-2160p" {
		t.Errorf("GUID = %q, want the non-permalink guid", got.GUID)
	}
	if got.Size != 5368709120 {
		t.Errorf("Size = %d, want the size attr 5368709120", got.Size)
	}
	if got.Seeders != 7 || got.Leechers != 3 {
		t.Errorf("Seeders/Leechers = %d/%d, want 7/3 from explicit attrs", got.Seeders, got.Leechers)
	}
	if got.Protocol != core.ProtocolTorrent {
		t.Errorf("Protocol = %q, want %q (magneturl attr)", got.Protocol, core.ProtocolTorrent)
	}
	if got.Parsed.Season != 4 || len(got.Parsed.Episodes) != 1 || got.Parsed.Episodes[0] != 1 {
		t.Errorf("Parsed season/episodes = %d/%v, want 4/[1]", got.Parsed.Season, got.Parsed.Episodes)
	}

	// The third item is magnet-only: no enclosure, no link, no guid.
	got = rels[2]
	wantMagnet := "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567&dn=Dune.Part.Two.2024"
	if got.DownloadURL != wantMagnet {
		t.Errorf("DownloadURL = %q, want the magneturl attr %q", got.DownloadURL, wantMagnet)
	}
	if got.GUID != wantMagnet {
		t.Errorf("GUID = %q, want the download url as the identity of a guid-less item", got.GUID)
	}
	if got.Seeders != 300 || got.Leechers != 0 {
		t.Errorf("Seeders/Leechers = %d/%d, want 300/0 when peers equals seeders", got.Seeders, got.Leechers)
	}
	if got.Size != 0 {
		t.Errorf("Size = %d, want 0 when the indexer published no size", got.Size)
	}
}

func TestSearchNewznabParsesItems(t *testing.T) {
	c, _ := newStub(t, newznabCfg(), map[string]response{"search": ok(t, "newznab_search.xml")})

	rels, err := c.Search(context.Background(), "arrival", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rels) != 3 {
		t.Fatalf("got %d releases, want 3", len(rels))
	}

	got := rels[0]
	if got.Protocol != core.ProtocolUsenet {
		t.Errorf("Protocol = %q, want %q", got.Protocol, core.ProtocolUsenet)
	}
	if got.InfoHash != "" {
		t.Errorf("InfoHash = %q, want empty for usenet", got.InfoHash)
	}
	if got.Seeders != 0 || got.Leechers != 0 {
		t.Errorf("Seeders/Leechers = %d/%d, want 0/0 for usenet", got.Seeders, got.Leechers)
	}
	if got.Size != 9663676416 {
		t.Errorf("Size = %d, want 9663676416", got.Size)
	}
	if !strings.HasPrefix(got.DownloadURL, "https://usenet.example/getnzb/3f8ac1.nzb") {
		t.Errorf("DownloadURL = %q, want the nzb enclosure url", got.DownloadURL)
	}
	if got.Parsed.Year != 2016 || got.Parsed.Group != "DRONES" {
		t.Errorf("Parsed year/group = %d/%q, want 2016/DRONES", got.Parsed.Year, got.Parsed.Group)
	}

	// The second item's pubDate is junk; usenetdate is the fallback.
	got = rels[1]
	want := time.Date(2019, 5, 13, 2, 0, 0, 0, time.UTC)
	if !got.PublishedAt.Equal(want) {
		t.Errorf("PublishedAt = %v, want %v from usenetdate", got.PublishedAt, want)
	}
	if got.Protocol != core.ProtocolUsenet {
		t.Errorf("Protocol = %q, want %q", got.Protocol, core.ProtocolUsenet)
	}

	// The third item carries no enclosure and no torrent attrs, so only the
	// configured indexer type says which engine it would route to.
	got = rels[2]
	if got.Protocol != core.ProtocolUsenet {
		t.Errorf("Protocol = %q, want %q from the newznab indexer type", got.Protocol, core.ProtocolUsenet)
	}
	if got.Size != 2147483648 {
		t.Errorf("Size = %d, want 2147483648 from the <size> element", got.Size)
	}
}

func TestSearchDefaultsProtocolToTorrentForTorznab(t *testing.T) {
	// The malformed fixture's surviving items have no enclosure type and no
	// torrent attrs, which leaves the indexer type as the only signal.
	c, _ := newStub(t, torznabCfg(), map[string]response{"search": ok(t, "torznab_search_malformed.xml")})

	rels, err := c.Search(context.Background(), "anything", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	for _, r := range rels {
		if r.Protocol != core.ProtocolTorrent {
			t.Errorf("%q protocol = %q, want %q from the torznab indexer type", r.Title, r.Protocol, core.ProtocolTorrent)
		}
	}
}

func TestSearchSkipsMalformedItems(t *testing.T) {
	c, _ := newStub(t, torznabCfg(), map[string]response{"search": ok(t, "torznab_search_malformed.xml")})

	rels, err := c.Search(context.Background(), "anything", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("got %d releases (%v), want 2: titleless and linkless items are dropped, the rest survive", len(rels), titles(rels))
	}

	junk := rels[0]
	if !strings.HasPrefix(junk.Title, "Junk.Numbers") {
		t.Fatalf("first surviving release = %q, want the junk-numbers item", junk.Title)
	}
	if junk.Size != 0 {
		t.Errorf("Size = %d, want 0 for an unparseable size", junk.Size)
	}
	if junk.Seeders != 0 {
		t.Errorf("Seeders = %d, want 0 for an unparseable seeders attr", junk.Seeders)
	}
	if junk.Leechers != 0 {
		t.Errorf("Leechers = %d, want 0 for a negative peers attr", junk.Leechers)
	}
	if !junk.PublishedAt.IsZero() {
		t.Errorf("PublishedAt = %v, want the zero time for an unparseable date", junk.PublishedAt)
	}
	if junk.InfoHash != "" {
		t.Errorf("InfoHash = %q, want empty", junk.InfoHash)
	}
	if junk.DownloadURL != "https://sloppy.example/download/4.torrent" {
		t.Errorf("DownloadURL = %q, want the link (the enclosure url is empty)", junk.DownloadURL)
	}

	good := rels[1]
	if good.Seeders != 11 || good.Leechers != 9 || good.Size != 3221225472 {
		t.Errorf("good release = %d seeders / %d leechers / %d bytes, want 11/9/3221225472", good.Seeders, good.Leechers, good.Size)
	}
}

func titles(rels []core.Release) []string {
	out := make([]string, len(rels))
	for i, r := range rels {
		out[i] = r.Title
	}
	return out
}

func TestSearchRejectsNonFeedBody(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"html login page", "<!DOCTYPE html><html><body>Please log in</body></html>"},
		{"truncated xml", `<?xml version="1.0"?><rss><channel><item><title>Half`},
		{"empty body", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newStub(t, torznabCfg(), map[string]response{"search": {status: http.StatusOK, body: []byte(tc.body)}})

			_, err := c.Search(context.Background(), "q", nil)
			if !errors.Is(err, ErrBadResponse) {
				t.Fatalf("err = %v, want ErrBadResponse", err)
			}
		})
	}
}

func TestSearchSendsModeCategoriesAndKey(t *testing.T) {
	cfg := torznabCfg()
	cfg.Categories = []int{2000, 2040}
	c, s := newStub(t, cfg, map[string]response{
		"search":   ok(t, "torznab_search.xml"),
		"movie":    ok(t, "torznab_search.xml"),
		"tvsearch": ok(t, "torznab_search.xml"),
	})

	ctx := context.Background()
	if _, err := c.Search(ctx, "  blade runner  ", []int{5040, 0, -1}); err != nil {
		t.Fatalf("Search: %v", err)
	}
	if _, err := c.SearchMovie(ctx, "arrival", nil); err != nil {
		t.Fatalf("SearchMovie: %v", err)
	}
	if _, err := c.SearchTV(ctx, "the expanse", 4, 1, nil); err != nil {
		t.Fatalf("SearchTV: %v", err)
	}
	if _, err := c.SearchTV(ctx, "the expanse", 4, 0, nil); err != nil {
		t.Fatalf("SearchTV season only: %v", err)
	}

	reqs := s.seen()
	if len(reqs) != 4 {
		t.Fatalf("got %d requests, want 4", len(reqs))
	}
	for i, r := range reqs {
		if r.Get("apikey") != testAPIKey {
			t.Errorf("request %d apikey = %q, want the configured key", i, r.Get("apikey"))
		}
		if r.Get("extended") != "1" {
			t.Errorf("request %d extended = %q, want 1", i, r.Get("extended"))
		}
	}

	if got := reqs[0]; got.Get("t") != "search" || got.Get("q") != "blade runner" || got.Get("cat") != "5040" {
		t.Errorf("search request = t=%q q=%q cat=%q, want t=search q=trimmed cat=5040 (non-positive ids dropped)", got.Get("t"), got.Get("q"), got.Get("cat"))
	}
	if got := reqs[1]; got.Get("t") != "movie" || got.Get("cat") != "2000,2040" {
		t.Errorf("movie request = t=%q cat=%q, want t=movie cat=2000,2040 from the config", got.Get("t"), got.Get("cat"))
	}
	if got := reqs[2]; got.Get("t") != "tvsearch" || got.Get("season") != "4" || got.Get("ep") != "1" {
		t.Errorf("tvsearch request = t=%q season=%q ep=%q, want t=tvsearch season=4 ep=1", got.Get("t"), got.Get("season"), got.Get("ep"))
	}
	if got := reqs[3]; got.Get("season") != "4" || got.Has("ep") {
		t.Errorf("season-only request = season=%q ep=%q, want season=4 and no ep", got.Get("season"), got.Get("ep"))
	}
}

func TestSearchOmitsEmptyQueryAndCategories(t *testing.T) {
	c, s := newStub(t, torznabCfg(), map[string]response{"search": ok(t, "torznab_search.xml")})

	if _, err := c.Search(context.Background(), "   ", nil); err != nil {
		t.Fatalf("Search: %v", err)
	}

	got := s.seen()[0]
	if got.Has("q") || got.Has("cat") {
		t.Errorf("request = q=%q cat=%q, want neither parameter sent", got.Get("q"), got.Get("cat"))
	}
}

func TestErrorDocumentSurfacesAsAPIError(t *testing.T) {
	for _, tc := range []struct {
		name         string
		fixture      string
		status       int
		wantCode     int
		unauthorized bool
	}{
		{"bad credentials over 200", "error_credentials.xml", http.StatusOK, 100, true},
		{"bad credentials over 401", "error_credentials.xml", http.StatusUnauthorized, 100, true},
		{"unsupported function", "error_no_such_function.xml", http.StatusOK, 202, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newStub(t, torznabCfg(), map[string]response{"search": {status: tc.status, body: readFixture(t, tc.fixture)}})

			_, err := c.Search(context.Background(), "q", nil)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v, want *APIError", err)
			}
			if apiErr.Code != tc.wantCode {
				t.Errorf("Code = %d, want %d", apiErr.Code, tc.wantCode)
			}
			if apiErr.Indexer != "Example Tracker" {
				t.Errorf("Indexer = %q, want the configured name", apiErr.Indexer)
			}
			if got := errors.Is(err, ErrUnauthorized); got != tc.unauthorized {
				t.Errorf("errors.Is(err, ErrUnauthorized) = %v, want %v (%v)", got, tc.unauthorized, err)
			}
		})
	}
}

func TestNon2xxWithoutErrorDocument(t *testing.T) {
	c, _ := newStub(t, torznabCfg(), map[string]response{"search": {status: http.StatusBadGateway, body: []byte("upstream is down")}})

	_, err := c.Search(context.Background(), "q", nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v, want *APIError", err)
	}
	if apiErr.StatusCode != http.StatusBadGateway {
		t.Errorf("StatusCode = %d, want 502", apiErr.StatusCode)
	}
	if errors.Is(err, ErrUnauthorized) {
		t.Errorf("502 must not read as unauthorized: %v", err)
	}
}

func TestTestAcceptsBothCapsFlavors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		cfg     core.IndexerConfig
		fixture string
	}{
		{"torznab", torznabCfg(), "torznab_caps.xml"},
		{"newznab", newznabCfg(), "newznab_caps.xml"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, s := newStub(t, tc.cfg, map[string]response{"caps": ok(t, tc.fixture)})

			if err := c.Test(context.Background()); err != nil {
				t.Fatalf("Test: %v", err)
			}
			if got := s.seen()[0]; got.Get("t") != "caps" || got.Get("apikey") != testAPIKey {
				t.Errorf("caps request = t=%q apikey=%q, want t=caps with the configured key", got.Get("t"), got.Get("apikey"))
			}
		})
	}
}

func TestTestRejectsUnusableCaps(t *testing.T) {
	for _, tc := range []struct {
		name   string
		resp   response
		wantIs error
	}{
		{"no search modes", response{status: http.StatusOK, body: readFixture(nil, "caps_no_modes.xml")}, ErrBadResponse},
		{"html login page", response{status: http.StatusOK, body: []byte("<html><body>login</body></html>")}, ErrBadResponse},
		{"bad credentials", response{status: http.StatusOK, body: readFixture(nil, "error_credentials.xml")}, ErrUnauthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := newStub(t, torznabCfg(), map[string]response{"caps": tc.resp})

			err := c.Test(context.Background())
			if !errors.Is(err, tc.wantIs) {
				t.Fatalf("err = %v, want %v", err, tc.wantIs)
			}
		})
	}
}

func TestUnsupportedModeReportsIndexerError(t *testing.T) {
	// The stub answers anything it was not given with "no such function",
	// which is how real indexers refuse t=movie.
	c, _ := newStub(t, torznabCfg(), map[string]response{"search": ok(t, "torznab_search.xml")})

	if _, err := c.SearchMovie(context.Background(), "arrival", nil); err == nil {
		t.Fatal("SearchMovie on an indexer without movie search must fail")
	}
	if _, err := c.Search(context.Background(), "arrival", nil); err != nil {
		t.Fatalf("plain search must still work: %v", err)
	}
}

func TestAPIKeyStaysOutOfTransportErrors(t *testing.T) {
	srv := httptest.NewServer(&stub{})
	srv.Close() // nothing is listening: every request is a transport error

	cfg := torznabCfg()
	cfg.URL = srv.URL
	cfg.APIKey = testAPIKey
	c := New(cfg, &http.Client{Timeout: time.Second})

	_, err := c.Search(context.Background(), "q", nil)
	if err == nil {
		t.Fatal("Search against a closed server must fail")
	}
	if strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("error leaks the api key: %v", err)
	}
	if err := c.Test(context.Background()); err == nil || strings.Contains(err.Error(), testAPIKey) {
		t.Fatalf("Test error = %v, want a failure that does not leak the api key", err)
	}
}

func TestAPIURLAcceptsBaseOrEndpoint(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"https://x.example", "https://x.example/api"},
		{"https://x.example/", "https://x.example/api"},
		{"  https://x.example//  ", "https://x.example/api"},
		{"https://x.example/api", "https://x.example/api"},
		{"https://x.example/api/", "https://x.example/api"},
		{"https://x.example/torznab/indexer", "https://x.example/torznab/indexer/api"},
	} {
		if got := apiURL(tc.in); got != tc.want {
			t.Errorf("apiURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestNewWithoutHTTPClientStillWorks(t *testing.T) {
	s := &stub{modes: map[string]response{"caps": ok(t, "torznab_caps.xml")}}
	srv := httptest.NewServer(s)
	t.Cleanup(srv.Close)

	cfg := torznabCfg()
	cfg.URL = srv.URL
	if err := New(cfg, nil).Test(context.Background()); err != nil {
		t.Fatalf("Test with the default http client: %v", err)
	}
}

func TestSearchHonorsContextCancellation(t *testing.T) {
	c, _ := newStub(t, torznabCfg(), map[string]response{"search": ok(t, "torznab_search.xml")})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.Search(ctx, "q", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestConfigRoundTrips(t *testing.T) {
	cfg := torznabCfg()
	c := New(cfg, nil)
	if got := c.Config(); got.ID != cfg.ID || got.Name != cfg.Name || got.Type != cfg.Type {
		t.Errorf("Config() = %+v, want the configuration it was built with", got)
	}
}
