package api

import (
	"context"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

type perIndexerReleaseClient struct {
	release core.Release
}

func (c perIndexerReleaseClient) Search(context.Context, string, []int) ([]core.Release, error) {
	return []core.Release{c.release}, nil
}

func (perIndexerReleaseClient) Test(context.Context) error { return nil }

func (perIndexerReleaseClient) Categories(context.Context) ([]core.IndexerCategory, error) {
	return nil, nil
}

func TestInteractiveSearchDoesNotCollapseSameGUIDAcrossIndexers(t *testing.T) {
	indexers := []core.IndexerConfig{
		{ID: 41, Name: "first", URL: "https://first.invalid", Enabled: true},
		{ID: 42, Name: "second", URL: "https://second.invalid", Enabled: true},
	}
	factory := func(cfg core.IndexerConfig) IndexerClient {
		return perIndexerReleaseClient{release: core.Release{
			GUID:        "provider-local-guid",
			Title:       cfg.Name + " result",
			DownloadURL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
			Protocol:    core.ProtocolTorrent,
		}}
	}

	releases, failures := searchIndexers(context.Background(), factory, indexers, []string{"query"})
	if len(failures) != 0 {
		t.Fatalf("failures = %+v", failures)
	}
	if len(releases) != 2 {
		t.Fatalf("releases = %+v, want one result from each configured indexer", releases)
	}
	if releases[0].IndexerID != 41 || releases[1].IndexerID != 42 {
		t.Fatalf("indexer ids = %d, %d, want 41, 42", releases[0].IndexerID, releases[1].IndexerID)
	}
}
