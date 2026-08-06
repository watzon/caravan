package store

import (
	"context"
	"errors"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestRemotePathMappingCRUD(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	mapping := &core.RemotePathMapping{
		RemotePath: `/downloads`,
		LocalPath:  `/mnt/media/downloads`,
	}
	if err := st.CreateRemotePathMapping(ctx, mapping); err != nil {
		t.Fatalf("CreateRemotePathMapping: %v", err)
	}
	if mapping.ID == 0 {
		t.Fatal("CreateRemotePathMapping left ID at zero")
	}

	mapping.RemotePath = `/downloads/complete`
	mapping.LocalPath = `/srv/complete`
	if err := st.UpdateRemotePathMapping(ctx, mapping); err != nil {
		t.Fatalf("UpdateRemotePathMapping: %v", err)
	}
	if err := st.RecordRemotePathMappingMatch(ctx, mapping.ID); err != nil {
		t.Fatalf("RecordRemotePathMappingMatch first: %v", err)
	}
	if err := st.RecordRemotePathMappingMatch(ctx, mapping.ID); err != nil {
		t.Fatalf("RecordRemotePathMappingMatch second: %v", err)
	}

	got, err := st.GetRemotePathMapping(ctx, mapping.ID)
	if err != nil {
		t.Fatalf("GetRemotePathMapping: %v", err)
	}
	if got.RemotePath != mapping.RemotePath || got.LocalPath != mapping.LocalPath {
		t.Fatalf("GetRemotePathMapping = %+v, want %+v", got, mapping)
	}
	if got.MatchCount != 2 || got.LastMatchedAt.IsZero() {
		t.Fatalf("mapping diagnostics = count %d, last %v; want 2 and a timestamp",
			got.MatchCount, got.LastMatchedAt)
	}

	mappings, err := st.ListRemotePathMappings(ctx)
	if err != nil {
		t.Fatalf("ListRemotePathMappings: %v", err)
	}
	if len(mappings) != 1 || mappings[0].ID != mapping.ID {
		t.Fatalf("ListRemotePathMappings = %+v, want id %d", mappings, mapping.ID)
	}

	if err := st.DeleteRemotePathMapping(ctx, mapping.ID); err != nil {
		t.Fatalf("DeleteRemotePathMapping: %v", err)
	}
	if _, err := st.GetRemotePathMapping(ctx, mapping.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRemotePathMapping after delete = %v, want ErrNotFound", err)
	}
}

func TestRemotePathMappingRemotePathIsUnique(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	first := &core.RemotePathMapping{RemotePath: `/downloads`, LocalPath: `/mnt/one`}
	if err := st.CreateRemotePathMapping(ctx, first); err != nil {
		t.Fatalf("create first mapping: %v", err)
	}
	second := &core.RemotePathMapping{RemotePath: `/downloads`, LocalPath: `/mnt/two`}
	if err := st.CreateRemotePathMapping(ctx, second); err == nil {
		t.Fatal("duplicate remote path was accepted")
	}
}
