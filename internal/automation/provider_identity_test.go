package automation

import (
	"context"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/core"
)

type configuredReleaseIndexer struct {
	release core.Release
}

func (c configuredReleaseIndexer) Search(context.Context, string, []int) ([]core.Release, error) {
	return []core.Release{c.release}, nil
}

func (c configuredReleaseIndexer) SearchMovie(context.Context, string, []int) ([]core.Release, error) {
	return []core.Release{c.release}, nil
}

func (c configuredReleaseIndexer) SearchTV(context.Context, string, int, int, []int) ([]core.Release, error) {
	return []core.Release{c.release}, nil
}

func (configuredReleaseIndexer) Test(context.Context) error { return nil }

func (configuredReleaseIndexer) Categories(context.Context) ([]core.IndexerCategory, error) {
	return nil, nil
}

func TestAutomaticSceneSearchDoesNotCollapseSameGUIDAcrossIndexers(t *testing.T) {
	ctx := context.Background()
	st := openStore(t)
	enableAdultLibrary(t, st)

	for _, cfg := range []*core.IndexerConfig{
		{Name: "first", URL: "https://first.invalid", Enabled: true},
		{Name: "second", URL: "https://second.invalid", Enabled: true},
	} {
		if err := st.UpsertIndexer(ctx, cfg); err != nil {
			t.Fatalf("UpsertIndexer(%q): %v", cfg.Name, err)
		}
	}

	released := time.Date(2026, time.August, 14, 0, 0, 0, 0, time.UTC)
	series, episode := addSite(t, ctx, st, "Example Site", released)
	profile, err := st.GetDefaultQualityProfile(ctx)
	if err != nil {
		t.Fatalf("GetDefaultQualityProfile: %v", err)
	}

	factory := func(cfg core.IndexerConfig) api.IndexerClient {
		return configuredReleaseIndexer{release: core.Release{
			GUID:        "provider-local-guid",
			Title:       cfg.Name + " release",
			DownloadURL: "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
			Protocol:    core.ProtocolTorrent,
			Seeders:     10,
			Parsed: core.ParsedRelease{
				Title:     series.Title,
				Quality:   core.Quality1080p,
				Source:    core.SourceWebDL,
				SceneDate: released,
			},
		}}
	}
	engine := &fakeEngine{}
	runner := NewRunner(st, factory, func(context.Context, int64, string) core.Engine { return engine })

	if err := runner.searchScene(ctx, st, series, episode, profile); err != nil {
		t.Fatalf("searchScene: %v", err)
	}
	grabs, err := st.ListGrabs(ctx, 0)
	if err != nil {
		t.Fatalf("ListGrabs: %v", err)
	}
	if len(grabs) != 2 {
		t.Fatalf("grab history rows = %d, want winner plus same-GUID candidate from the other indexer", len(grabs))
	}
}
