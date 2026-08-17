package api

import (
	"bytes"
	"context"
	"net/http"
	"testing"

	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/watzon/caravan/internal/core"
)

type payloadResolvingIndexer struct {
	resolved string
	payload  []byte
}

func (payloadResolvingIndexer) Search(context.Context, string, []int) ([]core.Release, error) {
	return nil, nil
}

func (payloadResolvingIndexer) Test(context.Context) error { return nil }

func (payloadResolvingIndexer) Categories(context.Context) ([]core.IndexerCategory, error) {
	return nil, nil
}

func (c payloadResolvingIndexer) ResolveDownload(context.Context, string) (string, error) {
	return c.resolved, nil
}

func (c payloadResolvingIndexer) FetchDownload(_ context.Context, raw string) ([]byte, error) {
	if raw != c.resolved {
		return nil, context.Canceled
	}
	return append([]byte(nil), c.payload...), nil
}

func TestMovieGrabPassesResolvedIndexerPayloadToDownloadEngine(t *testing.T) {
	engine := &stubEngine{}
	payload := authenticatedTorrentPayload(t)
	client := payloadResolvingIndexer{
		resolved: "https://tracker.example/download/9.torrent",
		payload:  payload,
	}
	h, st, _ := newTestServer(t,
		WithEngine(&stubEngineProvider{engine: engine}),
		WithIndexerClients(func(core.IndexerConfig) IndexerClient { return client }),
	)
	movie := addMovie(t, st, "Payload Movie", 2026)
	indexer := core.IndexerConfig{
		Name: "payload indexer", URL: "https://tracker.example", Type: core.IndexerTypeTorznab, Enabled: true,
	}
	if err := st.UpsertIndexer(context.Background(), &indexer); err != nil {
		t.Fatalf("UpsertIndexer: %v", err)
	}
	release := torrentRelease("Payload.Movie.2026.1080p", "payload-guid", 10, core.ParsedRelease{Title: "Payload Movie", Quality: core.Quality1080p})
	release.IndexerID = indexer.ID
	release.Indexer = indexer.Name
	if err := st.UpsertRelease(context.Background(), &release); err != nil {
		t.Fatalf("UpsertRelease: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/library/movies/"+itoa(movie.ID)+"/grab", `{"release_id":`+itoa(release.ID)+`}`)
	wantStatus(t, rec, http.StatusCreated)
	adds := engine.addCalls()
	if len(adds) != 1 {
		t.Fatalf("engine adds = %d, want 1", len(adds))
	}
	if adds[0].release.DownloadURL != "" {
		t.Fatalf("download URL = %q, want an explicit payload-only handoff", adds[0].release.DownloadURL)
	}
	if !bytes.Equal(adds[0].release.TorrentPayload, payload) {
		t.Fatalf("torrent payload = %q", adds[0].release.TorrentPayload)
	}
}

func authenticatedTorrentPayload(t *testing.T) []byte {
	t.Helper()
	infoBytes, err := bencode.Marshal(metainfo.Info{
		Name: "authenticated.bin", Length: 1, PieceLength: 1, Pieces: make([]byte, metainfo.HashSize),
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
