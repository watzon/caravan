package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/usenet"
)

func TestSweepIncompleteKeepsDataWithoutProvenOwnership(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	active := "incomplete/active/release.mkv"
	parked := "incomplete/parked/release.mkv"
	orphan := "incomplete/orphan/release.mkv"
	metadata := "incomplete/.caravan/release.meta"
	for _, rel := range []string{active, parked, orphan, metadata} {
		h.writeVideo(rel, rel)
	}
	if err := h.st.UpsertDownload(ctx, &core.Download{
		EngineID: "active",
		State:    core.DownloadDownloading,
		SavePath: "incomplete/active",
	}); err != nil {
		t.Fatalf("UpsertDownload: %v", err)
	}
	if err := h.st.UpsertUnmatchedFile(ctx, &core.UnmatchedFile{Path: parked, Size: 1}); err != nil {
		t.Fatalf("UpsertUnmatchedFile: %v", err)
	}

	removed, err := h.mgr.SweepIncomplete(ctx)
	if err != nil {
		t.Fatalf("SweepIncomplete: %v", err)
	}
	if len(removed) != 0 {
		t.Fatalf("removed = %v, want unknown data kept", removed)
	}
	for _, rel := range []string{active, parked, orphan, metadata} {
		if !h.exists(rel) {
			t.Errorf("SweepIncomplete removed protected path %s", rel)
		}
	}
}

func TestSweepIncompleteRemovesASettledDownloadBeforeRestore(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	grab := h.grabFor(core.GrabInfo{MovieID: 1, ReleaseTitle: "settled"})
	linkDownloadToGrab(h, "settled", grab)
	if err := h.st.SetGrabStatus(ctx, grab.GrabID, core.GrabStatusImported, "imported 1 file(s)"); err != nil {
		t.Fatalf("SetGrabStatus: %v", err)
	}
	if err := h.st.UpsertDownload(ctx, &core.Download{
		EngineID: "settled",
		Engine:   download.EngineName,
		State:    core.DownloadCompleted,
		SavePath: "incomplete/settled",
	}); err != nil {
		t.Fatalf("UpsertDownload completed: %v", err)
	}
	h.writeVideo("incomplete/settled/release.mkv", "settled")
	h.writeVideo("incomplete/.caravan/settled.torrent", "metainfo")

	removed, err := h.mgr.SweepIncomplete(ctx)
	if err != nil {
		t.Fatalf("SweepIncomplete: %v", err)
	}
	if !slices.Equal(removed, []string{"incomplete/settled"}) {
		t.Fatalf("removed = %v, want the settled download", removed)
	}
	if _, err := h.st.GetDownloadByEngineID(ctx, "settled"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("settled download row remains: %v", err)
	}
	if h.exists("incomplete/.caravan/settled.torrent") {
		t.Fatal("settled torrent sidecar remains")
	}
}

func TestSweepIncompleteRemovesSettledUsenetSidecars(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	grab := h.grabFor(core.GrabInfo{MovieID: 1, ReleaseTitle: "settled news"})
	linkDownloadToGrab(h, "nzb-settled", grab)
	if err := h.st.SetGrabStatus(ctx, grab.GrabID, core.GrabStatusImported, "imported 1 file(s)"); err != nil {
		t.Fatalf("SetGrabStatus: %v", err)
	}
	if err := h.st.UpsertDownload(ctx, &core.Download{
		EngineID: "nzb-settled",
		Engine:   usenet.EngineName,
		State:    core.DownloadCompleted,
		SavePath: "incomplete/settled-news",
	}); err != nil {
		t.Fatalf("UpsertDownload completed: %v", err)
	}
	h.writeVideo("incomplete/settled-news/release.mkv", "settled")
	for _, rel := range []string{
		"incomplete/.caravan/nzb-settled.nzb",
		"incomplete/.caravan/nzb-settled.done",
	} {
		h.writeVideo(rel, "metadata")
	}

	if _, err := h.mgr.SweepIncomplete(ctx); err != nil {
		t.Fatalf("SweepIncomplete: %v", err)
	}
	for _, rel := range []string{
		"incomplete/.caravan/nzb-settled.nzb",
		"incomplete/.caravan/nzb-settled.done",
	} {
		if h.exists(rel) {
			t.Errorf("settled Usenet sidecar remains: %s", rel)
		}
	}
}

func TestSweepIncompleteKeepsACompletedFailedGrabAfterReviewDismissal(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	grab := h.grabFor(core.GrabInfo{MovieID: 1, ReleaseTitle: "dismissed"})
	linkDownloadToGrab(h, "dismissed", grab)
	if err := h.st.SetGrabStatus(ctx, grab.GrabID, core.GrabStatusFailed, "parked for manual match"); err != nil {
		t.Fatalf("SetGrabStatus: %v", err)
	}
	if err := h.st.UpsertDownload(ctx, &core.Download{
		EngineID: "dismissed",
		Engine:   download.EngineName,
		State:    core.DownloadCompleted,
		SavePath: "incomplete/dismissed",
	}); err != nil {
		t.Fatalf("UpsertDownload completed: %v", err)
	}
	h.writeVideo("incomplete/dismissed/release.mkv", "keep")

	removed, err := h.mgr.SweepIncomplete(ctx)
	if err != nil {
		t.Fatalf("SweepIncomplete: %v", err)
	}
	if len(removed) != 0 || !h.exists("incomplete/dismissed/release.mkv") {
		t.Fatalf("dismissed source was removed: %v", removed)
	}
}

func TestSweepIncompleteDoesNotFollowASettledSymlink(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	outside := t.TempDir()
	outsideFile := filepath.Join(outside, "keep.mkv")
	if err := os.WriteFile(outsideFile, []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(h.root, "incomplete"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(h.root, "incomplete", "settled-link")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	grab := h.grabFor(core.GrabInfo{MovieID: 1, ReleaseTitle: "settled link"})
	linkDownloadToGrab(h, "settled-link", grab)
	if err := h.st.SetGrabStatus(ctx, grab.GrabID, core.GrabStatusImported, "imported 1 file(s)"); err != nil {
		t.Fatalf("SetGrabStatus: %v", err)
	}
	if err := h.st.UpsertDownload(ctx, &core.Download{
		EngineID: "settled-link",
		Engine:   download.EngineName,
		State:    core.DownloadCompleted,
		SavePath: "incomplete/settled-link",
	}); err != nil {
		t.Fatalf("UpsertDownload completed: %v", err)
	}

	if _, err := h.mgr.SweepIncomplete(ctx); err != nil {
		t.Fatalf("SweepIncomplete: %v", err)
	}
	if _, err := os.Lstat(link); !os.IsNotExist(err) {
		t.Fatalf("settled symlink remains: %v", err)
	}
	if got, err := os.ReadFile(outsideFile); err != nil || string(got) != "keep" {
		t.Fatalf("outside file changed: %q, %v", got, err)
	}
}

func TestIncompleteEntryRejectsPathsOutsideTheOwnedDirectory(t *testing.T) {
	tests := []struct {
		name string
		path string
		want string
		ok   bool
	}{
		{name: "download directory", path: "incomplete/release/file.mkv", want: "release", ok: true},
		{name: "single file", path: "incomplete/release.mkv", want: "release.mkv", ok: true},
		{name: "library", path: "library/Movies/release.mkv"},
		{name: "absolute", path: filepath.Join(string(filepath.Separator), "tmp", "release.mkv")},
		{name: "directory itself", path: "incomplete"},
		{name: "escape", path: "incomplete/../library/release.mkv"},
		{name: "nested traversal", path: "incomplete/first/../second/release.mkv"},
		{name: "reserved metadata", path: "incomplete/.caravan/release.meta"},
		{name: "leading whitespace", path: " incomplete/release/file.mkv"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := incompleteEntry(tt.path)
			if got != tt.want || ok != tt.ok {
				t.Fatalf("incompleteEntry(%q) = %q, %t; want %q, %t", tt.path, got, ok, tt.want, tt.ok)
			}
		})
	}
}
