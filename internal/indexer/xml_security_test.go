package indexer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestGenericTorznabDecoderRejectsDTD(t *testing.T) {
	var document feedDoc
	err := decodeDoc([]byte(`<!DOCTYPE rss SYSTEM "https://attacker.invalid/evil.dtd"><rss><channel/></rss>`), "rss", &document)
	if err == nil || !strings.Contains(err.Error(), "DTD") {
		t.Fatalf("decodeDoc error = %v, want DTD rejection", err)
	}
}

func TestGenericTorznabDecoderRejectsTrailingXMLDocument(t *testing.T) {
	var document feedDoc
	err := decodeDoc([]byte(`<rss><channel/></rss><rss><channel/></rss>`), "rss", &document)
	if err == nil || !strings.Contains(err.Error(), "trailing") {
		t.Fatalf("decodeDoc error = %v, want trailing-document rejection", err)
	}
}

func TestGenericTorznabClientRejectsResponseOverMaxBody(t *testing.T) {
	body := `<rss><channel></channel></rss>` + strings.Repeat(" ", maxBody)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	client := New(core.IndexerConfig{Name: "oversize", URL: server.URL, Type: core.IndexerTypeTorznab}, server.Client())
	_, err := client.Search(context.Background(), "fixture", nil)
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("Search error = %v, want response-size rejection", err)
	}
}
