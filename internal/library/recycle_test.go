package library

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

func TestRemoveMovieRecyclesOnlyOwnedFiles(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	mv, rel := h.addMovieWithFile("Big Buck Bunny", 2008)
	dir := movieDir("Big Buck Bunny", 2008)
	h.writeVideo(dir+"/user.srt", "subtitle")
	if err := h.st.SetSetting(ctx, store.SettingRecycleRetentionDays, "30"); err != nil {
		t.Fatal(err)
	}
	if err := h.mgr.RemoveMovie(ctx, mv.ID, true); err != nil {
		t.Fatalf("RemoveMovie: %v", err)
	}
	if !h.exists(dir + "/user.srt") {
		t.Fatal("unknown file was moved to recycle")
	}
	matches, err := filepath.Glob(filepath.Join(h.root, "recycle", "*", filepath.FromSlash(rel)))
	if err != nil || len(matches) != 1 {
		t.Fatalf("recycled media = %v, %v, want one file", matches, err)
	}
	if h.exists(rel) {
		t.Fatal("original media survived recycle")
	}
	if _, err := h.st.GetMediaFileByPath(ctx, rel); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("media_files row for %s: %v, want ErrNotFound", rel, err)
	}
	content, err := os.ReadFile(matches[0])
	if err != nil {
		t.Fatalf("read recycled media: %v", err)
	}
	if string(content) != "movie" {
		t.Fatalf("recycled media = %q, want original bytes", content)
	}
}

func TestRecycleRetentionReadFailureLeavesFilesAndRowsUntouched(t *testing.T) {
	h := newHarness(t)
	mv, rel := h.addMovieWithFile("Big Buck Bunny", 2008)
	dir := movieDir("Big Buck Bunny", 2008)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := h.mgr.removeItemFiles(ctx, dir, MovieNFOName, []core.MediaFile{{
		Path: rel, MovieID: mv.ID,
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("removeItemFiles: %v, want context canceled", err)
	}
	for _, path := range []string{rel, dir + "/" + PosterName, dir + "/" + MovieNFOName} {
		if !h.exists(path) {
			t.Fatalf("%s changed after retention read failure", path)
		}
	}
	if _, err := h.st.GetMediaFileByPath(context.Background(), rel); err != nil {
		t.Fatalf("GetMediaFileByPath: %v", err)
	}
}

func TestRemoveMovieRetentionValidationFailureLeavesLibraryUntouched(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "malformed", value: "thirty"},
		{name: "negative", value: "-1"},
		{name: "over limit", value: "3651"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			mv, rel := h.addMovieWithFile("Big Buck Bunny", 2008)
			if err := h.st.SetSetting(ctx, store.SettingRecycleRetentionDays, tc.value); err != nil {
				t.Fatalf("SetSetting: %v", err)
			}

			if err := h.mgr.RemoveMovie(ctx, mv.ID, true); err == nil {
				t.Fatal("RemoveMovie succeeded with invalid recycle retention")
			}
			if h.movieGone(mv.ID) {
				t.Fatal("movie was deleted after retention validation failure")
			}
			if !h.exists(rel) {
				t.Fatalf("%s changed after retention validation failure", rel)
			}
			if _, err := h.st.GetMediaFileByPath(ctx, rel); err != nil {
				t.Fatalf("GetMediaFileByPath: %v", err)
			}
			if h.exists("recycle") {
				t.Fatal("recycle batch was created after retention validation failure")
			}
		})
	}
}

func TestRecycleRefusesOutsideLibraryPath(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	mv, _ := h.addMovieWithFile("Sintel", 2010)
	outside := filepath.Join(t.TempDir(), "outside.mkv")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.addMediaFile(outside, mv.ID)
	if err := h.st.SetSetting(ctx, store.SettingRecycleRetentionDays, "30"); err != nil {
		t.Fatal(err)
	}
	if err := h.mgr.RemoveMovie(ctx, mv.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(outside); err != nil {
		t.Fatalf("outside file was touched: %v", err)
	}
	if entries, err := os.ReadDir(filepath.Join(h.root, "recycle")); err == nil {
		for _, entry := range entries {
			if entry.Name() == filepath.Base(outside) {
				t.Fatal("outside file was placed in recycle")
			}
		}
	}
}

func TestRecycleCleanupLeavesUnknownEntries(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.st.SetSetting(ctx, store.SettingRecycleRetentionDays, "0"); err != nil {
		t.Fatal(err)
	}
	old := "20000101T000000Z"
	if err := os.MkdirAll(filepath.Join(h.root, "recycle", old), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.root, "recycle", old, "file.mkv"), []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.root, "recycle", "notes.txt"), []byte("keep"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(h.root, "recycle", "not-a-batch"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.mgr.HandleRecycleCleanup(ctx, h.st, json.RawMessage("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.root, "recycle", old)); !os.IsNotExist(err) {
		t.Fatalf("valid batch still exists: %v", err)
	}
	for _, name := range []string{"notes.txt", "not-a-batch"} {
		if _, err := os.Stat(filepath.Join(h.root, "recycle", name)); err != nil {
			t.Errorf("unknown recycle entry %s was removed: %v", name, err)
		}
	}
}

func TestRecycleCleanupRetentionReadFailureDoesNotPurge(t *testing.T) {
	h := newHarness(t)
	batch := filepath.Join(h.root, "recycle", "20000101T000000Z")
	if err := os.MkdirAll(batch, 0o755); err != nil {
		t.Fatalf("create recycle batch: %v", err)
	}
	if err := os.WriteFile(filepath.Join(batch, "file.mkv"), []byte("old"), 0o644); err != nil {
		t.Fatalf("write recycled media: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := h.mgr.HandleRecycleCleanup(ctx, h.st, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("HandleRecycleCleanup: %v, want context canceled", err)
	}
	if _, err := os.Stat(batch); err != nil {
		t.Fatalf("recycle batch was purged after retention read failure: %v", err)
	}
}

func TestRecycleCleanupRetainsYoungBatch(t *testing.T) {
	h := newHarness(t)
	if err := h.st.SetSetting(context.Background(), store.SettingRecycleRetentionDays, "1"); err != nil {
		t.Fatal(err)
	}
	batch := time.Now().UTC().Format("20060102T150405Z")
	if err := os.MkdirAll(filepath.Join(h.root, "recycle", batch), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := h.mgr.HandleRecycleCleanup(context.Background(), h.st, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(h.root, "recycle", batch)); err != nil {
		t.Fatalf("young batch was removed: %v", err)
	}
}
