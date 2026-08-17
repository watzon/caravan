package automation

import (
	"bytes"
	"context"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/core"
)

type payloadIndexerClient struct {
	resolved string
	payload  []byte
}

func (payloadIndexerClient) Search(context.Context, string, []int) ([]core.Release, error) {
	return nil, nil
}

func (payloadIndexerClient) Test(context.Context) error { return nil }

func (payloadIndexerClient) Categories(context.Context) ([]core.IndexerCategory, error) {
	return nil, nil
}

func (c payloadIndexerClient) ResolveDownload(context.Context, string) (string, error) {
	return c.resolved, nil
}

func (c payloadIndexerClient) FetchDownload(_ context.Context, raw string) ([]byte, error) {
	if raw != c.resolved {
		return nil, context.Canceled
	}
	return append([]byte(nil), c.payload...), nil
}

func TestAutomationResolvesAuthenticatedDownloadPayload(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	indexer := core.IndexerConfig{
		Name: "payload indexer", URL: "https://tracker.example", Type: core.IndexerTypeTorznab, Enabled: true,
	}
	if err := st.UpsertIndexer(ctx, &indexer); err != nil {
		t.Fatalf("UpsertIndexer: %v", err)
	}
	payload := automationTorrentPayload(t)
	client := payloadIndexerClient{
		resolved: "https://tracker.example/download/9.torrent",
		payload:  payload,
	}
	runner := NewRunner(st, func(core.IndexerConfig) api.IndexerClient { return client }, nil)
	release := core.Release{
		IndexerID:   indexer.ID,
		DownloadURL: "https://tracker.example/details/9",
		Protocol:    core.ProtocolTorrent,
	}

	if err := runner.resolveReleaseDownload(ctx, st, &release); err != nil {
		t.Fatalf("resolveReleaseDownload: %v", err)
	}
	if release.DownloadURL != "" {
		t.Fatalf("download URL = %q, want an explicit payload-only handoff", release.DownloadURL)
	}
	if !bytes.Equal(release.TorrentPayload, payload) {
		t.Fatalf("torrent payload = %q", release.TorrentPayload)
	}
}

func automationTorrentPayload(t *testing.T) []byte {
	t.Helper()
	infoBytes, err := bencode.Marshal(metainfo.Info{
		Name: "automated.bin", Length: 1, PieceLength: 1, Pieces: make([]byte, metainfo.HashSize),
	})
	if err != nil {
		t.Fatalf("marshal torrent info: %v", err)
	}
	var payload bytes.Buffer
	if err := (&metainfo.MetaInfo{InfoBytes: infoBytes}).Write(&payload); err != nil {
		t.Fatalf("write torrent payload: %v", err)
	}
	return payload.Bytes()
}
