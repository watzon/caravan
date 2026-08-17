package cardigann

import "testing"

func TestDescribeProviderQuarantinesMalformedDocument(t *testing.T) {
	descriptors, err := DescribeProvider(staticProvider{
		source: "user",
		items:  []SourceDocument{{Path: "broken.yml", Data: []byte("id: broken\nname: [\n")}},
	})
	if err != nil {
		t.Fatalf("DescribeProvider: %v", err)
	}
	if len(descriptors) != 1 || descriptors[0].State != DefinitionStateQuarantined || descriptors[0].BlockedReason == "" || descriptors[0].Provider.Kind != SourceKindUser {
		t.Fatalf("descriptors = %+v, want a quarantined user document", descriptors)
	}
}
