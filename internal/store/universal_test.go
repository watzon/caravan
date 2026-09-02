package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// The three columns 0023 adds round-trip: a grab's target library, a parked
// file's scope, and a cached release's own category filing. The field the
// untied-grab adult gate reads without re-searching.
func TestUniversalSearchColumnsRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	rel := &core.Release{
		IndexerID: 7, Indexer: "Nzbee", Title: "Some.Release", GUID: "g1",
		DownloadURL: "https://example.invalid/d/1", Protocol: core.ProtocolTorrent,
		Categories: []int{6000, 6040},
	}
	if err := st.UpsertRelease(ctx, rel); err != nil {
		t.Fatalf("UpsertRelease: %v", err)
	}
	got, err := st.GetRelease(ctx, rel.ID)
	if err != nil {
		t.Fatalf("GetRelease: %v", err)
	}
	if !reflect.DeepEqual(got.Categories, []int{6000, 6040}) {
		t.Errorf("categories = %v, want the cached filing", got.Categories)
	}

	g := &core.Grab{GrabInfo: core.GrabInfo{LibraryID: 3, ReleaseTitle: "Some.Release"}, ReleaseID: rel.ID}
	if err := st.InsertGrab(ctx, g); err != nil {
		t.Fatalf("InsertGrab: %v", err)
	}
	gotGrab, err := st.GetGrab(ctx, g.GrabID)
	if err != nil {
		t.Fatalf("GetGrab: %v", err)
	}
	if gotGrab.LibraryID != 3 {
		t.Errorf("grab library_id = %d, want 3", gotGrab.LibraryID)
	}

	u := &core.UnmatchedFile{Path: "downloads/x.mkv", Size: 1, Reason: "manual-grab", LibraryID: 3}
	if err := st.UpsertUnmatchedFile(ctx, u); err != nil {
		t.Fatalf("UpsertUnmatchedFile: %v", err)
	}
	gotU, err := st.GetUnmatchedFile(ctx, u.ID)
	if err != nil {
		t.Fatalf("GetUnmatchedFile: %v", err)
	}
	if gotU.LibraryID != 3 {
		t.Errorf("unmatched library_id = %d, want 3", gotU.LibraryID)
	}
}
