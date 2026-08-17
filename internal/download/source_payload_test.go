package download

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestEmbeddedTorrentSpecUsesResolvedPayloadWithoutRefetchingIndexerURL(t *testing.T) {
	metainfo, _, _ := buildTorrent(t, "payload.bin", 1024)
	var payload bytes.Buffer
	if err := metainfo.Write(&payload); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		http.Error(w, "must not refetch", http.StatusBadGateway)
	}))
	defer server.Close()
	embedded := &Embedded{http: server.Client()}

	spec, err := embedded.torrentSpec(context.Background(), core.Release{
		Title:          "Payload Torrent",
		Protocol:       core.ProtocolTorrent,
		DownloadURL:    server.URL + "/private.torrent",
		TorrentPayload: payload.Bytes(),
	})
	if err != nil {
		t.Fatalf("torrentSpec: %v", err)
	}
	if got, want := spec.InfoHash, metainfo.HashInfoBytes(); got != want {
		t.Fatalf("info hash = %s, want %s", got, want)
	}
	if requests != 0 {
		t.Fatalf("indexer URL requests = %d, want 0", requests)
	}
}

func TestEmbeddedTorrentSpecRejectsReportedHashMismatch(t *testing.T) {
	metainfo, _, _ := buildTorrent(t, "mismatch.bin", 1024)
	var payload bytes.Buffer
	if err := metainfo.Write(&payload); err != nil {
		t.Fatalf("write metainfo: %v", err)
	}
	embedded := &Embedded{}
	_, err := embedded.torrentSpec(context.Background(), core.Release{
		Title:          "Mismatched Torrent",
		Protocol:       core.ProtocolTorrent,
		TorrentPayload: payload.Bytes(),
		InfoHash:       "ffffffffffffffffffffffffffffffffffffffff",
	})
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("torrentSpec error = %v, want hash mismatch", err)
	}
}

func TestEmbeddedTorrentSpecRejectsEmptyTorrentInfoDictionary(t *testing.T) {
	embedded := &Embedded{}
	_, err := embedded.torrentSpec(context.Background(), core.Release{
		Title:          "Empty Info Torrent",
		Protocol:       core.ProtocolTorrent,
		TorrentPayload: []byte("d4:infodee"),
	})
	if err == nil || !strings.Contains(err.Error(), "torrent payload") {
		t.Fatalf("torrentSpec error = %v, want invalid-info rejection", err)
	}
}
