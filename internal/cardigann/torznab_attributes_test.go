package cardigann

import (
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestReleaseXMLPreservesExtendedAttributesWithoutReservedOverrides(t *testing.T) {
	item := releaseXML(core.Release{
		Size:     10,
		Seeders:  2,
		Leechers: 3,
		Attributes: []core.ReleaseAttribute{
			{Name: "genre", Value: "drama"},
			{Name: "seeders", Value: "999"},
			{Name: "genre", Value: "duplicate"},
		},
	})
	got := map[string]string{}
	for _, attr := range item.Attrs {
		got[attr.Name] = attr.Value
	}
	if got["genre"] != "drama" || got["seeders"] != "2" {
		t.Fatalf("attrs = %#v", got)
	}
}
