package library

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestMapRemotePathUsesLongestComponentMatch(t *testing.T) {
	mappings := []core.RemotePathMapping{
		{RemotePath: `/downloads`, LocalPath: `/mnt/downloads`},
		{RemotePath: `/downloads/complete`, LocalPath: `/srv/complete`},
	}

	got, ok := mapRemotePath(`/downloads/complete/Movie/file.mkv`, mappings)
	want := filepath.Join(`/srv/complete`, `Movie`, `file.mkv`)
	if !ok || got != want {
		t.Fatalf("mapRemotePath = %q, %t; want %q, true", got, ok, want)
	}

	if got, ok := mapRemotePath(`/downloads-old/file.mkv`, mappings); ok || got != `/downloads-old/file.mkv` {
		t.Fatalf("component-prefix mismatch mapped to %q, %t", got, ok)
	}
}

func TestMapRemotePathAcceptsWindowsClientPaths(t *testing.T) {
	mappings := []core.RemotePathMapping{{
		RemotePath: `D:\\Downloads`,
		LocalPath:  `/mnt/downloads`,
	}}

	got, ok := mapRemotePath(`d:\\downloads\\Movie\\file.mkv`, mappings)
	want := filepath.Join(`/mnt/downloads`, `Movie`, `file.mkv`)
	if !ok || got != want {
		t.Fatalf("mapRemotePath = %q, %t; want %q, true", got, ok, want)
	}
}

func TestImportDownloadAppliesRemotePathMapping(t *testing.T) {
	h := newHarness(t)
	movie := addMovieItem(h)

	localRoot := t.TempDir()
	folder := "Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP"
	file := folder + ".mkv"
	localFolder := filepath.Join(localRoot, folder)
	if err := os.MkdirAll(localFolder, 0o755); err != nil {
		t.Fatalf("mkdir remote-path fixture: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localFolder, file), []byte("mapped movie bytes"), 0o644); err != nil {
		t.Fatalf("write remote-path fixture: %v", err)
	}

	mapping := &core.RemotePathMapping{RemotePath: `/client/downloads`, LocalPath: localRoot}
	if err := h.st.CreateRemotePathMapping(context.Background(), mapping); err != nil {
		t.Fatalf("CreateRemotePathMapping: %v", err)
	}
	grab := h.grabFor(core.GrabInfo{
		MovieID:      movie.ID,
		ReleaseTitle: "Big.Buck.Bunny.2008.1080p.BluRay.x264-GRP",
	})
	download := core.DownloadStatus{
		ID:       "external-download",
		State:    core.DownloadCompleted,
		Name:     folder,
		Progress: 1,
		SavePath: `/client/downloads/` + folder,
	}

	if err := h.mgr.ImportDownload(context.Background(), download, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	if got := h.read(organizedRel); got != "mapped movie bytes" {
		t.Fatalf("imported content = %q, want mapped movie bytes", got)
	}
	recorded, err := h.st.GetRemotePathMapping(context.Background(), mapping.ID)
	if err != nil {
		t.Fatalf("GetRemotePathMapping: %v", err)
	}
	if recorded.MatchCount != 1 || recorded.LastMatchedAt.IsZero() {
		t.Fatalf("mapping diagnostics = count %d, last %v; want one observed match",
			recorded.MatchCount, recorded.LastMatchedAt)
	}
}
