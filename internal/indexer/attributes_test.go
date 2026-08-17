package indexer

import (
	"reflect"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestTorznabAttributeBagRoundTripsThroughGenericClient(t *testing.T) {
	client := &Client{cfg: core.IndexerConfig{ID: 7, Name: "fixture", Type: core.IndexerTypeTorznab}}
	release, ok := client.release(feedItem{
		Title: "Fixture Release",
		GUID:  "fixture-guid",
		Link:  "magnet:?xt=urn:btih:0123456789abcdef0123456789abcdef01234567",
		Attrs: []feedAttr{
			{Name: "genre", Value: "drama"},
			{Name: "seeders", Value: "12"},
			{Name: "genre", Value: "duplicate"},
			{Name: "category", Value: "8000"},
		},
	})
	if !ok {
		t.Fatal("generic client dropped a valid item")
	}
	want := []core.ReleaseAttribute{{Name: "genre", Value: "drama"}}
	if !reflect.DeepEqual(release.Attributes, want) {
		t.Fatalf("attributes = %+v, want %+v", release.Attributes, want)
	}
	if release.Seeders != 12 || !reflect.DeepEqual(release.Categories, []int{8000}) {
		t.Fatalf("canonical fields = seeders %d, categories %v", release.Seeders, release.Categories)
	}
}
