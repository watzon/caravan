package catalog

import "testing"

func TestMetadataInventoryContainsExactly542NonAddableRows(t *testing.T) {
	inventory, err := Inventory(nil)
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	if len(inventory) != 542 {
		t.Fatalf("inventory = %d, want 542", len(inventory))
	}
	seen := make(map[string]struct{}, len(inventory))
	for _, entry := range inventory {
		if _, exists := seen[entry.ID]; exists {
			t.Fatalf("duplicate metadata id %q", entry.ID)
		}
		seen[entry.ID] = struct{}{}
		if entry.Addable || entry.DefinitionID != "" || entry.State != InventoryStateMetadataOnly {
			t.Fatalf("metadata-only entry is operational: %+v", entry)
		}
		if entry.MetadataURLs == nil || entry.Content == nil || entry.Definitions == nil {
			t.Fatalf("metadata entry has nil slices: %+v", entry)
		}
	}
	entry, ok := inventoryByID(inventory, "1337x")
	if !ok || entry.Addable || entry.DefinitionID != "" || entry.InfoURL == "" || len(entry.MetadataURLs) == 0 {
		t.Fatalf("1337x inventory entry = %+v", entry)
	}
}

func TestInventoryReconcilesOnlyExplicitExecutableStatus(t *testing.T) {
	inventory, err := Inventory([]ExecutionStatus{
		{
			MetadataID:   "thepiratebay",
			DefinitionID: "builtin:thepiratebay",
			State:        InventoryStateVerified,
			Source:       "builtin",
			BaseURLs:     []string{"https://thepiratebay.org"},
			Addable:      true,
		},
		{
			MetadataID:   "1337x",
			DefinitionID: "pack:1337x",
			State:        InventoryStateUnsupported,
			Source:       "pack",
			Revision:     "r1",
			BlockedCode:  "detail-download-required",
			Addable:      false,
		},
	})
	if err != nil {
		t.Fatalf("Inventory: %v", err)
	}
	tpb, _ := inventoryByID(inventory, "thepiratebay")
	if !tpb.Addable || tpb.DefinitionID != "builtin:thepiratebay" || tpb.State != InventoryStateVerified {
		t.Fatalf("Pirate Bay entry = %+v", tpb)
	}
	x, _ := inventoryByID(inventory, "1337x")
	if x.Addable || x.DefinitionID != "" || x.State != InventoryStateUnsupported || len(x.Definitions) != 1 || x.Definitions[0].BlockedCode == "" {
		t.Fatalf("1337x entry = %+v", x)
	}
}

func inventoryByID(inventory []InventoryEntry, id string) (InventoryEntry, bool) {
	for _, entry := range inventory {
		if entry.ID == id {
			return entry, true
		}
	}
	return InventoryEntry{}, false
}
