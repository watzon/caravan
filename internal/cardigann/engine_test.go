package cardigann

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"golang.org/x/text/encoding/charmap"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestEngineSearchScrapesDefinitionIntoRelease(t *testing.T) {
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><table class="results">
			<tr class="release">
				<td><a class="title" href="/torrent/42">Ubuntu 26.04 x64</a></td>
				<td class="category">1</td>
				<td class="size">1.5 GB</td>
				<td class="seeders">120</td>
				<td class="leechers">7</td>
				<td class="date">2026-08-14T20:00:00Z</td>
				<td><a class="download" href="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567">download</a></td>
			</tr>
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
    - {id: 1, cat: Movies, desc: Movies}
  modes:
    search: [q]
search:
  paths:
    - path: "search/{{ .Keywords }}/1/"
  rows:
    selector: "table.results tr.release"
  fields:
    title:
      selector: "a.title"
    details:
      selector: "a.title"
      attribute: href
    download:
      selector: "a.download"
      attribute: href
    category:
      selector: "td.category"
    size:
      selector: "td.size"
    seeders:
      selector: "td.seeders"
    leechers:
      selector: "td.leechers"
    date:
      selector: "td.date"
`, upstream.URL))

	def, err := ParseDefinition(src)
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(def, Config{BaseURL: upstream.URL}, upstream.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	results, err := engine.Search(context.Background(), Query{Keywords: "ubuntu linux"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotPath != "/search/ubuntu%20linux/1/" {
		t.Fatalf("path = %q, want escaped search path", gotPath)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	got := results[0]
	if got.Title != "Ubuntu 26.04 x64" {
		t.Fatalf("title = %q", got.Title)
	}
	if got.GUID != upstream.URL+"/torrent/42" {
		t.Fatalf("guid = %q", got.GUID)
	}
	if !strings.HasPrefix(got.DownloadURL, "magnet:?xt=urn:btih:") {
		t.Fatalf("download URL = %q", got.DownloadURL)
	}
	if got.InfoHash != "0123456789ABCDEF0123456789ABCDEF01234567" {
		t.Fatalf("info hash = %q", got.InfoHash)
	}
	if got.Size != 1610612736 || got.Seeders != 120 || got.Leechers != 7 {
		t.Fatalf("size/swarm = %d/%d/%d", got.Size, got.Seeders, got.Leechers)
	}
	if !got.PublishedAt.Equal(time.Date(2026, 8, 14, 20, 0, 0, 0, time.UTC)) {
		t.Fatalf("published = %s", got.PublishedAt)
	}
	if len(got.Categories) != 1 || got.Categories[0] != 2000 {
		t.Fatalf("categories = %v, want mapped Movies/2000", got.Categories)
	}
}

func TestEngineAcceptsMagnetResultFieldAsDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<table><tr class="result"><td class="title">Magnet Only</td><td><a class="magnet" href="magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa">download</a></td></tr></table>`)
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: magnet-result-fixture
name: Magnet Result Fixture
links: [%s]
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search}]
  rows: {selector: tr.result}
  fields:
    title: {selector: .title}
    magnet: {selector: a.magnet, attribute: href}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, err := engine.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || !strings.HasPrefix(results[0].DownloadURL, "magnet:?xt=urn:btih:") {
		t.Fatalf("results = %+v", results)
	}
}

func TestEngineSearchDecodesDeclaredDefinitionEncoding(t *testing.T) {
	body, err := charmap.Windows1251.NewEncoder().Bytes([]byte(`<article><h2>Привет мир</h2><a href="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567">download</a></article>`))
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write(body)
	}))
	defer upstream.Close()

	def, err := ParseDefinition([]byte(fmt.Sprintf(`
id: encoded-fixture
name: Encoded Fixture
encoding: windows-1251
links: [%s]
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search}]
  rows: {selector: article}
  fields:
    title: {selector: h2}
    download: {selector: a, attribute: href}
`, upstream.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(def, Config{}, upstream.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	results, err := engine.Search(context.Background(), Query{Keywords: "fixture"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 || results[0].Title != "Привет мир" {
		t.Fatalf("results = %+v", results)
	}
}

func TestEngineUsesResultAndConfigTemplatesForOptionalFieldDefaults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<article><span class="fallback">Fallback</span><a href="/file.torrent">download</a></article>`)
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: fixture
name: Fixture
links: [%s]
settings:
  - {name: suffix, type: text, default: Release}
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search}]
  rows: {selector: article}
  fields:
    fallback: {selector: .fallback}
    title:
      selector: h2
      optional: true
      default: "{{ .Result.fallback }} {{ .Config.suffix }}"
    download: {selector: a, attribute: href}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{BaseURL: server.URL}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, err := engine.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "Fallback Release" {
		t.Fatalf("results = %+v", results)
	}
}

func TestEngineMergesFollowingRowsBeforeExtractingFieldsAndMatching(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<table>
			<tr class="main"><td><h2>Ubuntu <span class="badge">trusted</span> Linux</h2></td></tr>
			<tr class="details"><td><a href="magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa">download</a></td></tr>
			<tr class="main"><td><h2>Windows Release</h2></td></tr>
			<tr class="details"><td><a href="magnet:?xt=urn:btih:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb">download</a></td></tr>
		</table>`)
	}))
	defer server.Close()

	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: row-filter-fixture
name: Row Filter Fixture
links: [%s]
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search}]
  rows:
    selector: tr.main, tr.details
    after: 1
    filters: [{name: andmatch}]
  fields:
    title: {selector: h2, remove: .badge}
    download: {selector: a, attribute: href}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, err := engine.Search(context.Background(), Query{Keywords: "ubuntu linux"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "Ubuntu  Linux" {
		t.Fatalf("results = %+v, want one matching release without badge text", results)
	}
}

func TestEngineExtractsDateFromNearestPrecedingDateHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<section><div class="date-header"><a class="date" href="?date=2026-08-15">date</a></div><div class="row"><span class="title">Dated</span><span class="download">magnet:?xt=urn:btih:dddddddddddddddddddddddddddddddddddddddd</span></div></section>`)
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: row-date-header
name: Row Date Header
links: [%s]
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search}]
  rows:
    selector: .row
    dateheaders:
      selector: a.date
      attribute: href
      filters:
        - {name: querystring, args: date}
        - {name: dateparse, args: yyyy-MM-dd}
  fields:
    title: {selector: .title}
    download: {selector: .download}
`, server.URL)))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, err := engine.Search(context.Background(), Query{})
	want := time.Date(2026, time.August, 15, 0, 0, 0, 0, time.UTC)
	if err != nil || len(results) != 1 || !results[0].PublishedAt.Equal(want) {
		t.Fatalf("results = %+v, %v, want date %s", results, err, want)
	}
}

func TestEngineTreatsZeroJSONCountAsNoResults(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"total":0,"items":[{"title":"stale","download":"magnet:?xt=urn:btih:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"}]}`)
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: json-zero-count
name: JSON Zero Count
links: [%s]
search:
  paths:
    - path: /search
      response: {type: json}
  rows:
    selector: items
    count: {selector: total}
  fields:
    title: {selector: title}
    download: {selector: download}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	results, err := engine.Search(context.Background(), Query{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 0 {
		t.Fatalf("results = %+v, want no results for count zero", results)
	}
}

func TestEngineResolvesDownloadSelectorOnlyAtGrabTime(t *testing.T) {
	var detailRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/details/1" {
			detailRequests++
			_, _ = io.WriteString(w, `<a class="magnet" href="magnet:?xt=urn:btih:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee">Magnet</a>`)
			return
		}
		http.NotFound(w, r)
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: download-resolver
name: Download Resolver
links: [%s]
caps: {modes: {search: [q]}}
download:
  selectors:
    - {selector: a.magnet, attribute: href}
search:
  paths: [{path: /search}]
  rows: {selector: .row}
  fields:
    title: {selector: .title}
    download: {selector: .details, attribute: href}
`, server.URL)))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.ResolveDownload(context.Background(), server.URL+"/details/1")
	if err != nil || resolved != "magnet:?xt=urn:btih:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" || detailRequests != 1 {
		t.Fatalf("ResolveDownload = %q, %v requests=%d", resolved, err, detailRequests)
	}
}

func TestEngineKeepsDirectMagnetWhenDefinitionAlsoHasDownloadSelectors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unexpected request", http.StatusInternalServerError)
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: direct-magnet-with-download-block
name: Direct Magnet With Download Block
links: [%s]
download:
  selectors:
    - {selector: a.download, attribute: href}
search:
  paths: [{path: /search}]
  rows: {selector: .row}
  fields:
    title: {selector: .title}
    download: {selector: .download}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	magnet := "magnet:?xt=urn:btih:eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee&dn=direct"
	resolved, err := engine.ResolveDownload(context.Background(), magnet)
	if err != nil || resolved != magnet {
		t.Fatalf("ResolveDownload = %q, %v", resolved, err)
	}
}

func TestEngineRunsPreDownloadRequestBeforeResolvingDetailLink(t *testing.T) {
	var events []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/thanks":
			if r.Method != http.MethodPost {
				t.Fatalf("before method = %s, want POST", r.Method)
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if got := r.Form.Get("torrentid"); got != "9" {
				t.Fatalf("torrentid = %q, want 9", got)
			}
			events = append(events, "before")
			_, _ = io.WriteString(w, "ok")
		case "/details":
			events = append(events, "detail")
			_, _ = io.WriteString(w, `<a class="torrent" href="/download/9.torrent">Torrent</a>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: download-before
name: Download Before
links: [%s]
caps: {modes: {search: [q]}}
download:
  before:
    path: /thanks
    method: post
    inputs:
      torrentid: "{{ .DownloadUri.Query.id }}"
  selectors:
    - {selector: a.torrent, attribute: href}
search:
  paths: [{path: /search}]
  rows: {selector: .row}
  fields:
    title: {selector: .title}
    download: {selector: .details, attribute: href}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := engine.ResolveDownload(context.Background(), server.URL+"/details?id=9")
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if resolved != server.URL+"/download/9.torrent" {
		t.Fatalf("resolved = %q", resolved)
	}
	if got, want := strings.Join(events, ","), "before,detail"; got != want {
		t.Fatalf("request order = %q, want %q", got, want)
	}
}

func TestEngineResolvesPreDownloadPathSelectorFromBeforeResponse(t *testing.T) {
	const magnet = "magnet:?xt=urn:btih:abcdef0123456789abcdef0123456789abcdef01"
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		switch r.URL.Path {
		case "/details/9":
			_, _ = io.WriteString(w, `<a class="prepare" href="/prepare/9">prepare</a>`)
		case "/prepare/9":
			_, _ = fmt.Fprintf(w, `<div id="magnet">%s</div>`, magnet)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: pre-download-selector-fixture
name: Pre-download Selector Fixture
links: [%s]
download:
  before:
    pathselector: {selector: a.prepare, attribute: href}
  selectors:
    - {selector: "#magnet", usebeforeresponse: true}
search:
  paths: [{path: search}]
  rows: {selector: tr}
  fields:
    title: {text: fixture}
    download: {text: "%s/details/9"}
`, server.URL, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolved, err := engine.ResolveDownload(context.Background(), server.URL+"/details/9")
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if resolved != magnet {
		t.Fatalf("resolved = %q", resolved)
	}
	if got, want := strings.Join(paths, ","), "/details/9,/prepare/9"; got != want {
		t.Fatalf("request paths = %q, want %q", got, want)
	}
}

func TestEngineResolveDownloadRedactsConfiguredValuesFromTrackerErrors(t *testing.T) {
	const secret = "download-owner-secret"
	definition, err := ParseDefinition([]byte(`
id: download-redaction-fixture
name: Download Redaction Fixture
links: [https://tracker.example]
settings:
  - {name: password, type: password, label: Password}
download:
  before: {path: before}
search:
  paths: [{path: search}]
  rows: {selector: tr}
  fields:
    title: {text: fixture}
    download: {text: "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
`))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial failed for %s", secret)
	})}
	engine, err := New(definition, Config{Settings: map[string]string{"password": secret}}, client)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = engine.ResolveDownload(context.Background(), "https://tracker.example/details/9")
	if err == nil {
		t.Fatal("ResolveDownload succeeded, want tracker error")
	}
	if strings.Contains(err.Error(), secret) || !strings.Contains(err.Error(), "[REDACTED]") {
		t.Fatalf("ResolveDownload error = %q, want redacted configured value", err)
	}
}

func TestEngineFetchesTorrentPayloadWithDownloadMethodHeadersAndSession(t *testing.T) {
	payload := cardigannTorrentPayload(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/download/9" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s, want POST", r.Method)
		}
		if got := r.Header.Get("Referer"); got != "https://tracker.example/torrents.php" {
			t.Fatalf("Referer = %q", got)
		}
		cookie, err := r.Cookie("session")
		if err != nil || cookie.Value != "owner" {
			t.Fatalf("session cookie = %v, %v", cookie, err)
		}
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: download-payload
name: Download Payload
links: [%s]
settings:
  - {name: cookie, type: text}
login:
  method: cookie
  inputs: {cookie: "{{ .Config.cookie }}"}
download:
  method: post
  headers:
    Referer: https://tracker.example/torrents.php
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search}]
  rows: {selector: .row}
  fields:
    title: {selector: .title}
    download: {selector: .download, attribute: href}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{Settings: map[string]string{"cookie": "session=owner"}}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	got, err := engine.FetchDownload(context.Background(), server.URL+"/download/9")
	if err != nil {
		t.Fatalf("FetchDownload: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("payload = %q", got)
	}
}

func cardigannTorrentPayload(t *testing.T) []byte {
	t.Helper()
	infoBytes, err := bencode.Marshal(metainfo.Info{
		Name: "payload.bin", Length: 1, PieceLength: 1, Pieces: make([]byte, 20),
	})
	if err != nil {
		t.Fatalf("marshal torrent info: %v", err)
	}
	mi := metainfo.MetaInfo{InfoBytes: infoBytes}
	var payload bytes.Buffer
	if err := mi.Write(&payload); err != nil {
		t.Fatalf("write torrent payload: %v", err)
	}
	return payload.Bytes()
}

func TestEngineFetchDownloadRejectsNonTorrentResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<html><body>please log in</body></html>`)
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: invalid-download-payload-fixture
name: Invalid Download Payload Fixture
links: [%s]
download: {headers: {Referer: ["{{ .Config.sitelink }}"]}}
search:
  paths: [{path: search}]
  rows: {selector: tr}
  fields:
    title: {text: fixture}
    download: {text: "%s/file.torrent"}
`, server.URL, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = engine.FetchDownload(context.Background(), server.URL+"/file.torrent")
	if err == nil || !strings.Contains(err.Error(), "torrent payload") {
		t.Fatalf("FetchDownload error = %v, want invalid torrent payload", err)
	}
}

func TestEngineSearchParsesJSONAndBuildsMagnetFromInfoHash(t *testing.T) {
	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"id":"9","name":"EXAMPLE MOVIE 2026","info_hash":"abcdef0123456789abcdef0123456789abcdef01","category":"201","added":"1786737600","size":"2147483648","seeders":"44","leechers":"5"}]`)
	}))
	defer upstream.Close()

	def, err := ParseDefinition([]byte(fmt.Sprintf(`
id: json-fixture
name: JSON Fixture
links: [%s]
settings:
  - {name: apiurl, type: text, default: %q}
caps:
  categorymappings: [{id: 201, cat: Movies}]
  modes: {search: [q]}
search:
  paths:
    - path: "{{ .Config.apiurl }}/q.php?q={{ urlquery .Keywords }}"
      response: {type: json}
  keywordsfilters:
    - {name: tolower}
  rows: {selector: "$"}
  fields:
    id: {selector: id}
    title:
      selector: name
      filters: [{name: replace, args: ["EXAMPLE", "Example"]}]
    category: {selector: category}
    infohash: {selector: info_hash}
    date: {selector: added}
    size: {selector: size}
    seeders: {selector: seeders}
    leechers: {selector: leechers}
`, upstream.URL, upstream.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(def, Config{}, upstream.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	results, err := engine.Search(context.Background(), Query{Keywords: "EXAMPLE Query"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if gotQuery != "example query" {
		t.Fatalf("query = %q, want filtered query", gotQuery)
	}
	if len(results) != 1 {
		t.Fatalf("results = %d, want 1", len(results))
	}
	got := results[0]
	if got.Title != "Example MOVIE 2026" {
		t.Fatalf("title = %q", got.Title)
	}
	if got.InfoHash != "ABCDEF0123456789ABCDEF0123456789ABCDEF01" {
		t.Fatalf("info hash = %q", got.InfoHash)
	}
	if !strings.HasPrefix(got.DownloadURL, "magnet:?xt=urn:btih:ABCDEF0123456789ABCDEF0123456789ABCDEF01") {
		t.Fatalf("download URL = %q", got.DownloadURL)
	}
	if got.Size != 2<<30 || got.Seeders != 44 || got.Leechers != 5 {
		t.Fatalf("size/swarm = %d/%d/%d", got.Size, got.Seeders, got.Leechers)
	}
	if got.PublishedAt.Unix() != 1786737600 {
		t.Fatalf("published = %s", got.PublishedAt)
	}
}

func TestEngineJSONRendersFilterArgumentsAndValidatesArrayValues(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"title":"Example [ML]","tags":["Drama","Noise"],"hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]`)
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: json-filter-fixture
name: JSON Filter Fixture
links: [%s]
settings:
  - {name: multilanguage, type: checkbox, default: false}
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search, response: {type: json}}]
  rows: {selector: "$"}
  fields:
    title:
      selector: title
      filters:
        - {name: replace, args: [" [ML]", "{{ if .Config.multilanguage }} [ML]{{ else }}{{ end }}"]}
    genre:
      selector: tags
      filters:
        - {name: validate, args: "Drama, Comedy"}
    infohash: {selector: hash}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, err := engine.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "Example" {
		t.Fatalf("results = %+v", results)
	}
	attributes := map[string]string{}
	for _, attribute := range results[0].Attributes {
		attributes[attribute.Name] = attribute.Value
	}
	if attributes["genre"] != "drama" {
		t.Fatalf("genre = %q", attributes["genre"])
	}
}

func TestEngineJSONFlattensNestedRowsAndRetainsParentFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":{"movies":[{"title":"Example","year":2026,"torrents":[{"quality":"1080p","hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]}]}}`)
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: nested-json-fixture
name: Nested JSON Fixture
links: [%s]
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search, response: {type: json}}]
  rows:
    selector: data.movies
    attribute: torrents
    multiple: true
    missingAttributeEqualsNoResults: true
  fields:
    parent_title: {selector: ..title}
    year: {selector: ..year}
    quality: {selector: quality}
    title: {text: "{{ .Result.parent_title }} ({{ .Result.year }}) {{ .Result.quality }}"}
    infohash: {selector: hash}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, err := engine.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "Example (2026) 1080p" {
		t.Fatalf("results = %+v", results)
	}
}

func TestEngineOrdersResultDependentFilterArguments(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `[{"name":"Example","year":"2026","hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}]`)
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: result-filter-fixture
name: Result Filter Fixture
links: [%s]
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search, response: {type: json}}]
  rows: {selector: "$"}
  fields:
    title:
      selector: name
      filters:
        - {name: append, args: " ({{ .Result.year }})"}
    year: {selector: year}
    infohash: {selector: hash}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, err := engine.Search(context.Background(), Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Title != "Example (2026)" {
		t.Fatalf("results = %+v", results)
	}
}

func TestDefinitionTemplatesExposeCardigannQueryAliases(t *testing.T) {
	engine := &Engine{}
	got, err := engine.renderTemplate(`{{ .Query.IMDBID }}|{{ .Query.IMDBIDShort }}|{{ .Query.Ep }}`, Query{IMDbID: "tt1234567", Episode: 4})
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	if got != "tt1234567|1234567|4" {
		t.Fatalf("query aliases = %q", got)
	}
}

func TestDefinitionTemplatesExposeTodayAndCompleteDownloadURI(t *testing.T) {
	engine := &Engine{}
	target, err := url.Parse("https://tracker.example/details/9?token=owner")
	if err != nil {
		t.Fatal(err)
	}
	year := time.Now().Year()
	got, err := engine.renderTemplateWithDownloadURI(`{{ .DownloadUri.AbsolutePath }}|{{ .DownloadUri.PathAndQuery }}|{{ .Today.Year }}`, Query{}, target)
	if err != nil {
		t.Fatalf("renderTemplateWithDownloadURI: %v", err)
	}
	want := fmt.Sprintf("/details/9|/details/9?token=owner|%d", year)
	if got != want {
		t.Fatalf("download URI aliases = %q, want %q", got, want)
	}
}

func TestDefinitionCapabilitiesAreUsableWithoutCallingTracker(t *testing.T) {
	def, err := ParseDefinition([]byte(`
id: fixture
name: Fixture Tracker
links: [https://tracker.example/]
caps:
  categorymappings:
    - {id: 1, cat: Movies}
    - {id: 2, cat: TV/HD}
  modes:
    search: [q]
    tv-search: [q, season, ep]
search:
  paths: [{path: /search}]
  rows: {selector: tr}
  fields: {title: {text: title}, download: {text: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"}}
`))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	caps := def.Capabilities()
	if len(caps.Categories) != 2 || caps.Categories[0].ID != 2000 || caps.Categories[1].ID != 5040 {
		t.Fatalf("categories = %+v", caps.Categories)
	}
	if !caps.Modes["search"] || !caps.Modes["tvsearch"] {
		t.Fatalf("modes = %v", caps.Modes)
	}
}

func TestEngineSearchFiltersRequestedCategories(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[
			{"name":"Example Movie","info_hash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","category":"201"},
			{"name":"Example Episode","info_hash":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","category":"205"}
		]`)
	}))
	defer upstream.Close()

	def, err := ParseDefinition([]byte(fmt.Sprintf(`
id: category-fixture
name: Category Fixture
links: [%s]
caps:
  categorymappings:
    - {id: 201, cat: Movies}
    - {id: 205, cat: TV}
  modes: {search: [q]}
search:
  paths:
    - path: /search
      response: {type: json}
  rows: {selector: "$"}
  fields:
    title: {selector: name}
    category: {selector: category}
    infohash: {selector: info_hash}
`, upstream.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(def, Config{BaseURL: upstream.URL}, upstream.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	releases, err := engine.Search(context.Background(), Query{Categories: []int{5000}})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 1 || releases[0].Title != "Example Episode" {
		t.Fatalf("releases = %+v, want only TV result", releases)
	}
}

func TestEngineSearchDeduplicatesAcrossPaths(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `[{"name":"Duplicate Release","info_hash":"cccccccccccccccccccccccccccccccccccccccc"}]`)
	}))
	defer upstream.Close()

	def, err := ParseDefinition([]byte(fmt.Sprintf(`
id: dedupe-fixture
name: Dedupe Fixture
links: [%s]
caps:
  categorymappings: [{id: 1, cat: Movies}]
  modes: {search: [q]}
search:
  paths:
    - {path: /first, response: {type: json}}
    - {path: /second, response: {type: json}}
  rows: {selector: "$"}
  fields:
    title: {selector: name}
    infohash: {selector: info_hash}
`, upstream.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(def, Config{BaseURL: upstream.URL}, upstream.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	releases, err := engine.Search(context.Background(), Query{})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 1 || releases[0].Title != "Duplicate Release" {
		t.Fatalf("releases = %+v, want one first-path result", releases)
	}
}

func TestExtractJSONFieldFollowsNestedDotPath(t *testing.T) {
	value, found, err := extractJSONField(map[string]any{
		"torrent": map[string]any{"name": "Nested title"},
	}, fieldBlock{Selector: "torrent.name"})
	if err != nil || !found || value != "Nested title" {
		t.Fatalf("extractJSONField = %q, %v, %v", value, found, err)
	}
}

func TestXMLRowsAndFieldsFollowBoundedDotPaths(t *testing.T) {
	root, err := parseXMLDocument([]byte(`<feed><items><item><torrent><title>XML title</title><link href="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567"/></torrent></item></items></feed>`))
	if err != nil {
		t.Fatalf("parseXMLDocument: %v", err)
	}
	rows := xmlRows(root, "feed.items.item")
	if len(rows) != 1 {
		t.Fatalf("xmlRows = %d", len(rows))
	}
	title, found, err := extractXMLField(rows[0], fieldBlock{Selector: "torrent.title"})
	if err != nil || !found || title != "XML title" {
		t.Fatalf("title = %q, %v, %v", title, found, err)
	}
	download, found, err := extractXMLField(rows[0], fieldBlock{Selector: "torrent.link", Attribute: "href"})
	if err != nil || !found || !strings.HasPrefix(download, "magnet:") {
		t.Fatalf("download = %q, %v, %v", download, found, err)
	}
}

func TestEngineXMLRowsSupportChildPathsAndOrderedFieldCases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = io.WriteString(w, `<rss><channel><item><raw_title>Example Show 720p</raw_title><link>magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa</link></item></channel></rss>`)
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: xml-case-fixture
name: XML Case Fixture
links: [%s]
caps:
  categorymappings:
    - {id: 1, cat: TV/SD}
    - {id: 2, cat: TV/HD}
  modes: {search: [q]}
search:
  paths: [{path: /feed, response: {type: xml}}]
  rows:
    selector: rss > channel > item
    filters: [{name: andmatch}]
  fields:
    category:
      selector: raw_title
      case:
        ':contains("720p")': 2
        '*': 1
    title: {selector: raw_title}
    download: {selector: link}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, err := engine.Search(context.Background(), Query{Keywords: "example show"})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || len(results[0].Categories) != 1 || results[0].Categories[0] != 5040 {
		t.Fatalf("results = %+v, want one TV/HD release", results)
	}
}

func TestBuildSearchRequestMergesSharedIOWithPathOverrides(t *testing.T) {
	base, err := url.Parse("https://tracker.example/base/")
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{
		base:     base,
		settings: map[string]string{"token": "abc"},
		def: &Definition{Search: searchBlock{
			Inputs:  map[string]string{"q": "{{ .Keywords }}", "token": "{{ .Config.token }}", "empty": "{{ .Genre }}"},
			Headers: map[string]headerTemplate{"Accept": "application/json", "User-Agent": "Shared Agent"},
		}},
	}
	req, err := engine.buildSearchRequest(context.Background(), pathBlock{
		Path:    "/find",
		Inputs:  map[string]string{"q": "path {{ .Keywords }}"},
		Headers: map[string]headerTemplate{"Accept": "text/html"},
	}, Query{Keywords: "fixture"})
	if err != nil {
		t.Fatalf("buildSearchRequest: %v", err)
	}
	if req.URL.Query().Get("q") != "path fixture" || req.URL.Query().Get("token") != "abc" || req.URL.Query().Has("empty") {
		t.Fatalf("query = %q", req.URL.RawQuery)
	}
	if req.Header.Get("Accept") != "text/html" || req.Header.Get("User-Agent") != "Shared Agent" {
		t.Fatalf("headers = %#v", req.Header)
	}

	engine.def.Search.AllowEmptyInputs = true
	req, err = engine.buildSearchRequest(context.Background(), pathBlock{Path: "/find"}, Query{})
	if err != nil {
		t.Fatalf("buildSearchRequest allow empty: %v", err)
	}
	if !req.URL.Query().Has("empty") {
		t.Fatalf("allowEmptyInputs query = %q", req.URL.RawQuery)
	}
}

func TestBuildSearchRequestUsesTypedFormPOSTAndSafeHeaders(t *testing.T) {
	base, err := url.Parse("https://tracker.example/base/")
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{base: base, settings: map[string]string{"token": "abc"}}
	req, err := engine.buildSearchRequest(context.Background(), pathBlock{
		Path:    "/find",
		Method:  "post",
		Inputs:  map[string]string{"q": "{{ .Keywords }}", "token": "{{ .Config.token }}"},
		Headers: map[string]headerTemplate{"Accept": "application/json", "Referer": "https://tracker.example/"},
	}, Query{Keywords: "a & b"})
	if err != nil {
		t.Fatalf("buildSearchRequest: %v", err)
	}
	if req.Method != http.MethodPost || req.URL.String() != "https://tracker.example/base/find" {
		t.Fatalf("request = %s %s", req.Method, req.URL)
	}
	body, _ := io.ReadAll(req.Body)
	if string(body) != "q=a+%26+b&token=abc" || req.Header.Get("Content-Type") != "application/x-www-form-urlencoded" {
		t.Fatalf("form request = %q %#v", body, req.Header)
	}
	if req.Header.Get("Accept") != "application/json" || req.Header.Get("Referer") != "https://tracker.example/" {
		t.Fatalf("headers = %#v", req.Header)
	}
}

func TestBuildSearchRequestRendersBoundedDynamicMethod(t *testing.T) {
	base, err := url.Parse("https://tracker.example/")
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{base: base, def: &Definition{Search: searchBlock{Inputs: map[string]string{"query": "{{ .Keywords }}"}}}}
	path := pathBlock{Path: "/search", Method: "{{ if .Keywords }}post{{ else }}get{{ end }}"}
	withoutQuery, err := engine.buildSearchRequest(context.Background(), path, Query{})
	if err != nil {
		t.Fatalf("buildSearchRequest without query: %v", err)
	}
	if withoutQuery.Method != http.MethodGet {
		t.Fatalf("empty-query method = %s", withoutQuery.Method)
	}
	withQuery, err := engine.buildSearchRequest(context.Background(), path, Query{Keywords: "example"})
	if err != nil {
		t.Fatalf("buildSearchRequest with query: %v", err)
	}
	if withQuery.Method != http.MethodPost {
		t.Fatalf("query method = %s", withQuery.Method)
	}
}

func TestEngineRoutesCanonicalCategoriesThroughSiteSpecificPath(t *testing.T) {
	var seen []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+"?"+r.URL.RawQuery)
		_, _ = io.WriteString(w, `<div class="row"><span class="title">Category</span><span class="download">magnet:?xt=urn:btih:ffffffffffffffffffffffffffffffffffffffff</span></div>`)
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: category-path
name: Category Path
links: [%s]
caps:
  categorymappings:
    - {id: anime, cat: TV/Anime}
    - {id: manga, cat: Books/Comics}
  modes: {search: [q]}
search:
  paths:
    - {path: /anime, categories: [anime]}
    - {path: /manga, categories: [manga]}
  inputs: {cat: '{{ join .Categories "," }}'}
  rows: {selector: .row}
  fields:
    title: {selector: .title}
    category: {text: anime}
    download: {selector: .download}
`, server.URL)))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, err := engine.Search(context.Background(), Query{Categories: []int{5000}})
	if err != nil || len(results) != 1 || len(seen) != 1 || seen[0] != "/anime?cat=anime" {
		t.Fatalf("results=%+v err=%v seen=%v", results, err, seen)
	}
}

func TestEngineEnforcesDefinitionRequestDelay(t *testing.T) {
	definition, err := ParseDefinition([]byte(`
id: delayed-fixture
name: Delayed Fixture
links: [https://tracker.example]
requestDelay: 2.5
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search}]
  rows: {selector: article}
  fields:
    title: {text: title}
    download: {text: "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
`))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	base := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	engine := &Engine{def: definition, lastRequest: base.Add(-500 * time.Millisecond)}
	var waited time.Duration
	err = engine.waitRequestDelayWith(context.Background(), func() time.Time { return base }, func(_ context.Context, delay time.Duration) error {
		waited = delay
		return nil
	})
	if err != nil {
		t.Fatalf("waitRequestDelayWith: %v", err)
	}
	if waited != 2*time.Second {
		t.Fatalf("waited = %s, want 2s", waited)
	}
}

func TestEngineUsesUserSuppliedCookieLoginBeforeSearch(t *testing.T) {
	var testRequests, searchRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "session=user-provided" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/account":
			testRequests++
			_, _ = io.WriteString(w, `<a class="logout">Logout</a>`)
		case "/search":
			searchRequests++
			_, _ = io.WriteString(w, `<table><tr><td class="title">Cookie Result</td><td class="download">magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa</td></tr></table>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: cookie-login-fixture
name: Cookie Login Fixture
links: [%s]
caps: {modes: {search: [q]}}
settings:
  - {name: cookie, type: password, label: Cookie}
login:
  method: cookie
  inputs: {cookie: "{{ .Config.cookie }}"}
  test: {path: account, selector: a.logout}
search:
  paths: [{path: search}]
  rows: {selector: tr}
  fields:
    title: {selector: .title}
    download: {selector: .download}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{Settings: map[string]string{"cookie": "session=user-provided"}}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		results, searchErr := engine.Search(context.Background(), Query{})
		if searchErr != nil || len(results) != 1 {
			t.Fatalf("Search %d = %+v, %v", i, results, searchErr)
		}
	}
	if testRequests != 1 || searchRequests != 2 {
		t.Fatalf("requests test=%d search=%d, want 1/2", testRequests, searchRequests)
	}
}

func TestEngineDoesNotSendConfiguredCookieToSecondaryApprovedOrigin(t *testing.T) {
	base := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer base.Close()
	var secondaryCookie string
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCookie = r.Header.Get("Cookie")
		_, _ = io.WriteString(w, `<a class="download" href="/file.torrent">download</a>`)
	}))
	defer secondary.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: cookie-origin-fixture
name: Cookie Origin Fixture
links: [%s, %s]
settings:
  - {name: cookie, type: password, label: Cookie}
login:
  method: cookie
  inputs: {cookie: "{{ .Config.cookie }}"}
download:
  selectors:
    - {selector: a.download, attribute: href}
search:
  paths: [{path: search}]
  rows: {selector: tr}
  fields:
    title: {text: fixture}
    download: {text: "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
`, base.URL, secondary.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{BaseURL: base.URL, Settings: map[string]string{"cookie": "session=user-provided"}}, secondary.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resolved, err := engine.ResolveDownload(context.Background(), secondary.URL+"/details/9")
	if err != nil {
		t.Fatalf("ResolveDownload: %v", err)
	}
	if resolved != secondary.URL+"/file.torrent" {
		t.Fatalf("resolved = %q", resolved)
	}
	if secondaryCookie != "" {
		t.Fatalf("secondary origin received configured cookie %q", secondaryCookie)
	}
}

func TestEngineStripsConfiguredCookieOnApprovedCrossOriginRedirect(t *testing.T) {
	var redirectedCookie string
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedCookie = r.Header.Get("Cookie")
		_, _ = io.WriteString(w, `<div class="row"><span class="title">Redirected</span><span class="download">magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa</span></div>`)
	}))
	defer secondary.Close()
	base := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, secondary.URL+"/results", http.StatusFound)
	}))
	defer base.Close()

	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: cookie-redirect-origin-fixture
name: Cookie Redirect Origin Fixture
links: [%s, %s]
settings:
  - {name: cookie, type: password, label: Cookie}
login:
  method: cookie
  inputs: {cookie: "{{ .Config.cookie }}"}
search:
  paths: [{path: /search}]
  rows: {selector: .row}
  fields:
    title: {selector: .title}
    download: {selector: .download}
`, base.URL, secondary.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{BaseURL: base.URL, Settings: map[string]string{"cookie": "session=user-provided"}}, base.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	results, err := engine.Search(context.Background(), Query{})
	if err != nil || len(results) != 1 {
		t.Fatalf("Search = %+v, %v", results, err)
	}
	if redirectedCookie != "" {
		t.Fatalf("redirected approved origin received configured cookie %q", redirectedCookie)
	}
}

func TestEngineStripsConfiguredCookieOnApprovedRedirectVariants(t *testing.T) {
	tests := []struct {
		name      string
		base      string
		secondary string
	}{
		{name: "subdomain", base: "http://tracker.example", secondary: "http://cdn.tracker.example"},
		{name: "HTTPS downgrade", base: "https://tracker.example", secondary: "http://tracker.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var redirectedCookie string
			client := &http.Client{Transport: roundTripperFunc(func(request *http.Request) (*http.Response, error) {
				if request.URL.String() == tt.base+"/search" {
					return &http.Response{
						StatusCode: http.StatusFound,
						Header:     http.Header{"Location": []string{tt.secondary + "/results"}},
						Body:       io.NopCloser(strings.NewReader("")),
						Request:    request,
					}, nil
				}
				redirectedCookie = request.Header.Get("Cookie")
				return &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/html"}},
					Body: io.NopCloser(strings.NewReader(
						`<div class="row"><span class="title">Redirected</span><span class="download">magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa</span></div>`,
					)),
					Request: request,
				}, nil
			})}
			definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: cookie-redirect-variant-fixture
name: Cookie Redirect Variant Fixture
links: [%s, %s]
settings:
  - {name: cookie, type: password, label: Cookie}
login:
  method: cookie
  inputs: {cookie: "{{ .Config.cookie }}"}
search:
  paths: [{path: /search}]
  rows: {selector: .row}
  fields:
    title: {selector: .title}
    download: {selector: .download}
`, tt.base, tt.secondary)))
			if err != nil {
				t.Fatalf("ParseDefinition: %v", err)
			}
			engine, err := New(definition, Config{BaseURL: tt.base, Settings: map[string]string{"cookie": "session=user-provided"}}, client)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			results, err := engine.Search(context.Background(), Query{})
			if err != nil || len(results) != 1 {
				t.Fatalf("Search = %+v, %v", results, err)
			}
			if redirectedCookie != "" {
				t.Fatalf("redirected approved origin received configured cookie %q", redirectedCookie)
			}
		})
	}
}

func TestEngineDoesNotSendSeededLoginCookieToSecondaryApprovedOrigin(t *testing.T) {
	base := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer base.Close()
	var secondaryCookie string
	secondary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		secondaryCookie = r.Header.Get("Cookie")
		_, _ = io.WriteString(w, `<div class="row"><span class="title">Secondary</span><span class="download">magnet:?xt=urn:btih:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb</span></div>`)
	}))
	defer secondary.Close()

	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: seeded-cookie-origin-fixture
name: Seeded Cookie Origin Fixture
links: [%s, %s]
login:
  method: get
  path: /login
  cookies: [seed=definition-provided]
search:
  paths: [{path: %s/results}]
  rows: {selector: .row}
  fields:
    title: {selector: .title}
    download: {selector: .download}
`, base.URL, secondary.URL, secondary.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{BaseURL: base.URL}, base.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	results, err := engine.Search(context.Background(), Query{})
	if err != nil || len(results) != 1 {
		t.Fatalf("Search = %+v, %v", results, err)
	}
	if secondaryCookie != "" {
		t.Fatalf("secondary origin received seeded login cookie %q", secondaryCookie)
	}
}

func TestEnginePOSTLoginRetainsIsolatedSessionCookie(t *testing.T) {
	var loginRequests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			loginRequests++
			if err := r.ParseForm(); err != nil || r.Form.Get("username") != "alice" || r.Form.Get("password") != "correct horse" {
				http.Error(w, `<div class="error">bad credentials</div>`, http.StatusBadRequest)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "authenticated", Path: "/", HttpOnly: true})
			_, _ = io.WriteString(w, `<div class="welcome">Welcome</div>`)
		case "/account":
			if cookie, err := r.Cookie("sid"); err != nil || cookie.Value != "authenticated" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `<a class="logout">Logout</a>`)
		case "/search":
			if cookie, err := r.Cookie("sid"); err != nil || cookie.Value != "authenticated" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `<table><tr><td class="title">Session Result</td><td class="download">magnet:?xt=urn:btih:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb</td></tr></table>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: post-login-fixture
name: POST Login Fixture
links: [%s]
caps: {modes: {search: [q]}}
settings:
  - {name: username, type: text, label: Username}
  - {name: password, type: password, label: Password}
login:
  path: login
  method: post
  inputs:
    username: "{{ .Config.username }}"
    password: "{{ .Config.password }}"
  error:
    - {selector: .error, message: {selector: .error}}
  test: {path: account, selector: a.logout}
search:
  paths: [{path: search}]
  rows: {selector: tr}
  fields:
    title: {selector: .title}
    download: {selector: .download}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{Settings: map[string]string{"username": "alice", "password": "correct horse"}}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		results, searchErr := engine.Search(context.Background(), Query{})
		if searchErr != nil || len(results) != 1 || results[0].Title != "Session Result" {
			t.Fatalf("Search %d = %+v, %v", i, results, searchErr)
		}
	}
	if loginRequests != 1 {
		t.Fatalf("login requests = %d, want 1", loginRequests)
	}
}

func TestEngineFormLoginPreservesHiddenInputsAndPreloginCookie(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			java, _ := r.Cookie("JAVA")
			if java == nil || java.Value != "OK" {
				http.Error(w, "missing pre-login cookie", http.StatusBadRequest)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "preauth", Value: "issued", Path: "/", HttpOnly: true})
			_, _ = io.WriteString(w, `<meta name="csrf" content="token-123"><form id="login" action="/wrong" method="post"><input name="username"><input name="password"><input type="checkbox" name="remember" value="yes" checked></form>`)
		case "/session":
			preauth, _ := r.Cookie("preauth")
			java, _ := r.Cookie("JAVA")
			_ = r.ParseForm()
			if preauth == nil || preauth.Value != "issued" || java == nil || java.Value != "OK" || r.Form.Get("csrf") != "token-123" || r.Form.Get("username") != "alice" || r.Form.Get("password") != "secret" || r.Form.Get("remember") != "yes" {
				http.Error(w, `<div class="error">invalid form</div>`, http.StatusBadRequest)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "form-authenticated", Path: "/", HttpOnly: true})
			_, _ = io.WriteString(w, `<div class="ok">ok</div>`)
		case "/account":
			cookie, _ := r.Cookie("sid")
			if cookie == nil || cookie.Value != "form-authenticated" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `<a class="logout">Logout</a>`)
		case "/search":
			cookie, _ := r.Cookie("sid")
			if cookie == nil || cookie.Value != "form-authenticated" {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(w, `<table><tr><td class="title">Form Result</td><td class="download">magnet:?xt=urn:btih:cccccccccccccccccccccccccccccccccccccccc</td></tr></table>`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: form-login-fixture
name: Form Login Fixture
links: [%s]
caps: {modes: {search: [q]}}
settings:
  - {name: username, type: text, label: Username}
  - {name: password, type: password, label: Password}
login:
  path: login
  method: form
  form: form#login
  submitpath: /session
  cookies: [JAVA=OK]
  selectors: true
  selectorinputs:
    csrf: {selector: 'meta[name="csrf"]', attribute: content}
  inputs:
    'input[name="username"]': "{{ .Config.username }}"
    'input[name="password"]': "{{ .Config.password }}"
  error:
    - {selector: .error, message: {selector: .error}}
  test: {path: account, selector: a.logout}
search:
  paths: [{path: search}]
  rows: {selector: tr}
  fields:
    title: {selector: .title}
    download: {selector: .download}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{Settings: map[string]string{"username": "alice", "password": "secret"}}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	results, err := engine.Search(context.Background(), Query{})
	if err != nil || len(results) != 1 || results[0].Title != "Form Result" {
		t.Fatalf("Search = %+v, %v", results, err)
	}
}

func TestBuildSearchRequestRejectsHeaderInjection(t *testing.T) {
	base, err := url.Parse("https://tracker.example/")
	if err != nil {
		t.Fatal(err)
	}
	engine := &Engine{base: base, settings: map[string]string{}}
	_, err = engine.buildSearchRequest(context.Background(), pathBlock{
		Path:    "/search",
		Headers: map[string]headerTemplate{"Accept": "application/xml\r\nX-Evil: injected"},
	}, Query{})
	if err == nil || !strings.Contains(err.Error(), "invalid header value") {
		t.Fatalf("buildSearchRequest error = %v, want CR/LF rejection", err)
	}
}

func TestEngineRejectsUndeclaredAbsoluteRequestOrigin(t *testing.T) {
	called := false
	undeclared := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer undeclared.Close()
	declared := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer declared.Close()

	definition := &Definition{
		ID: "fixture", Name: "Fixture", Type: "public", Links: []string{declared.URL},
		Caps: capabilitiesBlock{Modes: map[string][]string{"search": {"q"}}},
		Search: searchBlock{
			Paths:  []pathBlock{{Path: undeclared.URL, Response: responseBlock{Type: "html"}}},
			Rows:   rowsBlock{Selector: "article"},
			Fields: map[string]fieldBlock{"title": {Selector: "h2"}, "download": {Selector: "a", Attribute: "href"}},
		},
	}
	engine, err := New(definition, Config{BaseURL: declared.URL}, undeclared.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = engine.Search(context.Background(), Query{Keywords: "fixture"})
	if err == nil || !strings.Contains(err.Error(), "unapproved origin") {
		t.Fatalf("Search error = %v, want unapproved-origin rejection", err)
	}
	if called {
		t.Fatal("undeclared origin received a request")
	}
}

func TestEngineFetchDownloadRejectsEmptyTorrentInfoDictionary(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-bittorrent")
		_, _ = w.Write([]byte("d4:infodee"))
	}))
	defer server.Close()
	definition, err := ParseDefinition([]byte(fmt.Sprintf(`
id: invalid-info-payload
name: Invalid Info Payload
links: [%s]
search:
  paths: [{path: /search}]
  rows: {selector: .row}
  fields:
    title: {selector: .title}
    download: {selector: .download}
`, server.URL)))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{}, server.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := engine.FetchDownload(context.Background(), server.URL+"/empty.torrent"); err == nil {
		t.Fatal("FetchDownload accepted an empty torrent info dictionary")
	}
}

func TestEngineRejectsUndeclaredAbsoluteLoginOriginBeforeSendingCredentials(t *testing.T) {
	called := false
	undeclared := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer undeclared.Close()
	declared := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<article><h2>safe</h2><a href="magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567">download</a></article>`)
	}))
	defer declared.Close()

	definition := &Definition{
		ID: "login-origin", Name: "Login Origin", Type: "private", Links: []string{declared.URL},
		Login: &loginBlock{Method: "post", Path: undeclared.URL + "/login", Inputs: map[string]string{"password": "owner-secret"}},
		Search: searchBlock{
			Paths:  []pathBlock{{Path: "/search", Response: responseBlock{Type: "html"}}},
			Rows:   rowsBlock{Selector: "article"},
			Fields: map[string]fieldBlock{"title": {Selector: "h2"}, "download": {Selector: "a", Attribute: "href"}},
		},
	}
	engine, err := New(definition, Config{BaseURL: declared.URL}, undeclared.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = engine.Search(context.Background(), Query{Keywords: "fixture"})
	if err == nil || !strings.Contains(err.Error(), "unapproved origin") {
		t.Fatalf("Search error = %v, want unapproved-origin rejection", err)
	}
	if called {
		t.Fatal("undeclared login origin received credentials")
	}
}

func TestEngineRejectsRedirectToUndeclaredOrigin(t *testing.T) {
	called := false
	undeclared := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		called = true
	}))
	defer undeclared.Close()
	declared := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Location", undeclared.URL)
		w.WriteHeader(http.StatusFound)
	}))
	defer declared.Close()

	definition := &Definition{
		ID: "fixture", Name: "Fixture", Type: "public", Links: []string{declared.URL},
		Caps: capabilitiesBlock{Modes: map[string][]string{"search": {"q"}}},
		Search: searchBlock{
			Paths:  []pathBlock{{Path: "/search", Response: responseBlock{Type: "html"}}},
			Rows:   rowsBlock{Selector: "article"},
			Fields: map[string]fieldBlock{"title": {Selector: "h2"}, "download": {Selector: "a", Attribute: "href"}},
		},
	}
	engine, err := New(definition, Config{BaseURL: declared.URL}, declared.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = engine.Search(context.Background(), Query{Keywords: "fixture"})
	if err == nil || !strings.Contains(err.Error(), "unapproved redirect origin") {
		t.Fatalf("Search error = %v, want redirect-origin rejection", err)
	}
	if called {
		t.Fatal("undeclared redirect origin received a request")
	}
}

func TestEngineRejectsUndeclaredConfiguredSettingWithoutLeakingValue(t *testing.T) {
	definition := &Definition{
		ID: "fixture", Name: "Fixture", Type: "public", Links: []string{"https://tracker.example"},
		Caps: capabilitiesBlock{Modes: map[string][]string{"search": {"q"}}},
		Search: searchBlock{
			Paths: []pathBlock{{Path: "/search"}}, Rows: rowsBlock{Selector: "article"},
			Fields: map[string]fieldBlock{"title": {Text: "title"}, "download": {Text: "https://tracker.example/file"}},
		},
	}
	_, err := New(definition, Config{
		BaseURL:  "https://tracker.example",
		Settings: map[string]string{"authorization": "do-not-leak"},
	}, http.DefaultClient)
	if err == nil {
		t.Fatal("New accepted an undeclared configured setting")
	}
	if strings.Contains(err.Error(), "do-not-leak") {
		t.Fatalf("New leaked configured setting value: %v", err)
	}
}

func TestEngineSearchRedactsConfiguredValuesFromTransportErrors(t *testing.T) {
	const secret = "do-not-leak"
	definition := &Definition{
		ID: "fixture", Name: "Fixture", Type: "public", Links: []string{"https://tracker.example"},
		Settings: []settingBlock{{Name: "apiurl", Type: "text"}},
		Caps:     capabilitiesBlock{Modes: map[string][]string{"search": {"q"}}},
		Search: searchBlock{
			Paths: []pathBlock{{Path: "{{ .Config.apiurl }}"}}, Rows: rowsBlock{Selector: "article"},
			Fields: map[string]fieldBlock{"title": {Text: "title"}, "download": {Text: "https://tracker.example/file"}},
		},
	}
	client := &http.Client{Transport: roundTripperFunc(func(*http.Request) (*http.Response, error) {
		return nil, fmt.Errorf("dial failed for %s", secret)
	})}
	engine, err := New(definition, Config{
		BaseURL:  "https://tracker.example",
		Settings: map[string]string{"apiurl": "https://tracker.example/" + secret},
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Search(context.Background(), Query{Keywords: "fixture"})
	if err == nil {
		t.Fatal("Search unexpectedly succeeded")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("Search leaked configured value: %v", err)
	}
}

func TestDefinitionTemplateRenderLimitFailsClosed(t *testing.T) {
	engine := &Engine{settings: map[string]string{}}
	_, err := engine.renderTemplate(strings.Repeat("x", (64<<10)+1), Query{})
	if err == nil || !strings.Contains(err.Error(), "rendered template exceeds") {
		t.Fatalf("renderTemplate error = %v, want size-limit rejection", err)
	}
}

func TestJSONRowsRejectsTrailingValueAndExcessiveRows(t *testing.T) {
	engine := &Engine{def: &Definition{Search: searchBlock{
		Rows: rowsBlock{Selector: "$"},
		Fields: map[string]fieldBlock{
			"title":    {Selector: "title"},
			"download": {Selector: "download"},
		},
	}}}
	if _, err := engine.jsonRows(strings.NewReader(`[] []`)); err == nil || !strings.Contains(err.Error(), "one JSON value") {
		t.Fatalf("trailing JSON error = %v", err)
	}
	rows := `[` + strings.Repeat(`{},`, 10000) + `{}` + `]`
	if _, err := engine.jsonRows(strings.NewReader(rows)); err == nil || !strings.Contains(err.Error(), "too many rows") {
		t.Fatalf("row-limit error = %v", err)
	}
}

func TestQueryExposesStructuredTorznabIdentifiersToTemplates(t *testing.T) {
	engine := &Engine{settings: map[string]string{}}
	rendered, err := engine.renderTemplate("{{ .TVDBID }}|{{ .IMDbID }}|{{ .Year }}", Query{TVDBID: 123, IMDbID: "tt123", Year: 2026})
	if err != nil || rendered != "123|tt123|2026" {
		t.Fatalf("renderTemplate = %q, %v", rendered, err)
	}
}

func TestDefinitionTemplatesJoinCategoriesAndScalarConfig(t *testing.T) {
	engine := &Engine{settings: map[string]string{"cat": "anime"}}
	rendered, err := engine.renderTemplate(`{{ join .Categories "," }}|{{ .Config.cat | join "," }}`, Query{Categories: []int{1000, 2000}})
	if err != nil || rendered != "1000,2000|anime" {
		t.Fatalf("renderTemplate join = %q, %v", rendered, err)
	}
}

func TestDefinitionTemplatesApplyBoundedRegexReplacement(t *testing.T) {
	engine := &Engine{settings: map[string]string{}}
	got, err := engine.renderTemplate(`{{ re_replace .Keywords "\b0(\d)\b" "$1" }}`, Query{Keywords: "Show 01"})
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	if got != "Show 1" {
		t.Fatalf("re_replace = %q, want Show 1", got)
	}
}

func TestDefinitionTemplatesPreserveCardigannRegexEscapes(t *testing.T) {
	engine := &Engine{}
	got, err := engine.renderTemplate(`{{ re_replace .Keywords "\b0(\d{1})\b" "$1" }}`, Query{Keywords: "Episode 05"})
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	if got != "Episode 5" {
		t.Fatalf("regex escaped template = %q", got)
	}
}

func TestDefinitionTemplatesAccessHyphenatedConfigNames(t *testing.T) {
	engine := &Engine{settings: map[string]string{"filter-id": "2", "cat-id": "1_4"}}
	got, err := engine.renderTemplate(`{{ .Config.filter-id }}|{{ .Config.cat-id }}`, Query{})
	if err != nil {
		t.Fatalf("renderTemplate: %v", err)
	}
	if got != "2|1_4" {
		t.Fatalf("hyphenated config = %q", got)
	}
}

func TestDefinitionTemplateCheckboxSettingsUseBooleanTruth(t *testing.T) {
	definition, err := ParseDefinition([]byte(`
id: checkbox-fixture
name: Checkbox Fixture
links: [https://tracker.example]
settings:
  - {name: enabled, type: checkbox, default: false}
caps: {modes: {search: [q]}}
search:
  paths: [{path: /search}]
  rows: {selector: article}
  fields:
    title: {text: title}
    download: {text: "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
`))
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(definition, Config{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err := engine.renderTemplate(`{{ if .Config.enabled }}yes{{ else }}no{{ end }}`, Query{})
	if err != nil {
		t.Fatal(err)
	}
	if got != "no" {
		t.Fatalf("false checkbox rendered as %q, want no", got)
	}

	engine, err = New(definition, Config{Settings: map[string]string{"enabled": "true"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	got, err = engine.renderTemplate(`{{ if .Config.enabled }}yes{{ else }}no{{ end }}`, Query{})
	if err != nil || got != "yes" {
		t.Fatalf("true checkbox rendered as %q, %v, want yes", got, err)
	}
}

func TestResultFieldTemplateRunsAfterSourceExtraction(t *testing.T) {
	engine := &Engine{settings: map[string]string{}}
	values, err := engine.renderResultFieldTemplates(map[string]string{"title": "Example"}, map[string]fieldBlock{
		"details": {Text: "https://tracker.example/{{ .Fields.title }}"},
	}, Query{})
	if err != nil || values["details"] != "https://tracker.example/Example" {
		t.Fatalf("renderResultFieldTemplates = %#v, %v", values, err)
	}
}

func TestEngineRejectsResponseOverMaxBytes(t *testing.T) {
	for _, responseType := range []string{"html", "json"} {
		t.Run(responseType, func(t *testing.T) {
			body := `<table><tr><td class="title">Large</td><td class="download">magnet:?xt=urn:btih:dddddddddddddddddddddddddddddddddddddddd</td></tr></table>`
			if responseType == "json" {
				body = `[{"name":"Large","info_hash":"dddddddddddddddddddddddddddddddddddddddd"}]`
			}
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.WriteString(w, body)
				_, _ = io.WriteString(w, strings.Repeat(" ", maxSearchPageBytes+1))
			}))
			defer upstream.Close()

			searchShape := `
  paths: [{path: /search}]
  rows: {selector: tr}
  fields:
    title: {selector: td.title}
    download: {selector: td.download}`
			if responseType == "json" {
				searchShape = `
  paths: [{path: /search, response: {type: json}}]
  rows: {selector: "$"}
  fields:
    title: {selector: name}
    infohash: {selector: info_hash}`
			}
			def, err := ParseDefinition([]byte(fmt.Sprintf(`
id: large-fixture
name: Large Fixture
links: [%s]
caps:
  categorymappings: [{id: 1, cat: Movies}]
  modes: {search: [q]}
search:%s
`, upstream.URL, searchShape)))
			if err != nil {
				t.Fatalf("ParseDefinition: %v", err)
			}
			engine, err := New(def, Config{BaseURL: upstream.URL}, upstream.Client())
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			_, err = engine.Search(context.Background(), Query{})
			if err == nil || !strings.Contains(err.Error(), "response exceeds") {
				t.Fatalf("Search error = %q, want response-size rejection", err)
			}
		})
	}
}

func TestConfiguredSecretValuesKeepOrdinarySettingsVisible(t *testing.T) {
	settings := map[string]string{"sort": "time", "passkey": "abcd1234efgh", "cookie": "uid=1; pass=xyz1", "sitelink": "https://tracker.example/"}
	types := map[string]string{"sort": "select", "passkey": "text", "cookie": "text"}
	secrets := configuredSecretValues("https://tracker.example/", settings, types)
	joined := strings.Join(secrets, "\n")
	for _, want := range []string{"abcd1234efgh", "uid=1; pass=xyz1", "tracker.example"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("secrets %q should include %q", secrets, want)
		}
	}
	if strings.Contains(joined, "time") {
		t.Fatalf("secrets %q should not include the sort order", secrets)
	}
}
