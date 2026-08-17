package cardigann

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEngineRejectsAggregateResultsAcrossSearchPaths(t *testing.T) {
	var payload strings.Builder
	payload.WriteByte('[')
	for i := 0; i < 600; i++ {
		if i > 0 {
			payload.WriteByte(',')
		}
		fmt.Fprintf(&payload, `{"title":"release-%d"}`, i)
	}
	payload.WriteByte(']')
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(payload.String()))
	}))
	defer server.Close()

	definition := &Definition{
		ID: "fixture", Name: "Fixture", Type: "public", Encoding: "UTF-8", Links: []string{server.URL},
		Caps: capabilitiesBlock{Modes: map[string][]string{"search": {"q"}}},
		Search: searchBlock{
			Paths: []pathBlock{
				{Path: "/one", Response: responseBlock{Type: "json"}},
				{Path: "/two", Response: responseBlock{Type: "json"}},
			},
			Rows: rowsBlock{Selector: "$"},
			Fields: map[string]fieldBlock{
				"title":    {Selector: "title"},
				"download": {Text: server.URL + "/file"},
			},
		},
	}
	engine, err := New(definition, Config{BaseURL: server.URL}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Search(context.Background(), Query{}); err == nil || !strings.Contains(err.Error(), "too many results") {
		t.Fatalf("Search error = %v, want aggregate-result rejection", err)
	}
}

func TestEngineRejectsExcessiveHTMLRows(t *testing.T) {
	var page strings.Builder
	for i := 0; i < 1001; i++ {
		fmt.Fprintf(&page, `<article><h2>release-%d</h2><a href="/file/%d">download</a></article>`, i, i)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(page.String()))
	}))
	defer server.Close()
	definition := &Definition{
		ID: "fixture", Name: "Fixture", Type: "public", Encoding: "UTF-8", Links: []string{server.URL},
		Caps: capabilitiesBlock{Modes: map[string][]string{"search": {"q"}}},
		Search: searchBlock{
			Paths: []pathBlock{{Path: "/search"}}, Rows: rowsBlock{Selector: "article"},
			Fields: map[string]fieldBlock{
				"title": {Selector: "h2"}, "download": {Selector: "a", Attribute: "href"},
			},
		},
	}
	engine, err := New(definition, Config{BaseURL: server.URL}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Search(context.Background(), Query{}); err == nil || !strings.Contains(err.Error(), "too many rows") {
		t.Fatalf("Search error = %v, want HTML row rejection", err)
	}
}

func TestJSONFieldRejectsCompositeValues(t *testing.T) {
	engine := &Engine{def: &Definition{Search: searchBlock{
		Rows: rowsBlock{Selector: "$"},
		Fields: map[string]fieldBlock{
			"title": {Selector: "nested"}, "download": {Text: "https://tracker.example/file"},
		},
	}}}
	// A composite value must never be stringified into a release; the row
	// carrying it is dropped like any row with a failed required field.
	releases, err := engine.jsonRows(strings.NewReader(`[{"nested":{"secret":"value"}}]`))
	if err != nil {
		t.Fatalf("jsonRows: %v", err)
	}
	if len(releases) != 0 {
		t.Fatalf("releases = %d, want composite-value row dropped", len(releases))
	}
}

func TestTorznabWriterRejectsOversizedOutput(t *testing.T) {
	recorder := httptest.NewRecorder()
	writeXML(recorder, http.StatusOK, rssXML{
		Version: "2.0",
		Channel: rssChannelXML{Items: []rssItemXML{{Title: strings.Repeat("x", (8<<20)+1)}}},
	})
	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusBadGateway)
	}
	if recorder.Body.Len() > 8<<20 {
		t.Fatalf("oversized response body = %d bytes", recorder.Body.Len())
	}
}
