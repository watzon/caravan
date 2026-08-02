package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/relocate"
	"github.com/watzon/caravan/internal/store"
)

// storageRoots builds a populated source root and an empty target root, and
// points the settings table at the source.
func storageRoots(t *testing.T, st *store.Store) (source, target string) {
	t.Helper()
	dir := t.TempDir()
	source = filepath.Join(dir, "old")
	target = filepath.Join(dir, "new")
	film := filepath.Join(source, "library", "Movies", "Arrival (2016)")
	if err := os.MkdirAll(film, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(film, "Arrival (2016).mkv"), []byte("bytes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := st.SetSetting(context.Background(), store.SettingStorageRoot, source); err != nil {
		t.Fatalf("seed storage root: %v", err)
	}
	return source, target
}

func TestRepointStorageRootValidation(t *testing.T) {
	h, st, _ := newTestServer(t)
	source, target := storageRoots(t, st)

	notADir := filepath.Join(t.TempDir(), "a-file")
	if err := os.WriteFile(notADir, []byte("x"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	refusals := []struct {
		name string
		body string
	}{
		{"empty", `{"root":""}`},
		{"whitespace", `{"root":"   "}`},
		{"relative", `{"root":"relative/path"}`},
		{"missing", `{"root":"` + jsonPath(filepath.Join(target, "nope")) + `"}`},
		{"not a folder", `{"root":"` + jsonPath(notADir) + `"}`},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/v1/system/storage-root/repoint", tc.body)
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
			if got := currentRoot(t, st); got != source {
				t.Fatalf("storage root = %q, want it unchanged at %q", got, source)
			}
		})
	}
}

func TestRepointStorageRootWarnsButDoesNotBlockOnAnEmptyRoot(t *testing.T) {
	h, st, _ := newTestServer(t)
	_, target := storageRoots(t, st)

	rec := do(t, h, http.MethodPost, "/api/v1/system/storage-root/repoint",
		`{"root":"`+jsonPath(target)+`"}`)
	wantStatus(t, rec, http.StatusOK)

	var body repointResponse
	decodeBody(t, rec, &body)
	if body.Root != target {
		t.Fatalf("root = %q, want %q", body.Root, target)
	}
	if len(body.Warnings) == 0 {
		t.Fatal("re-pointing at a root with no library must warn")
	}
	// The point of the operation: the setting moved and no file did.
	if got := currentRoot(t, st); got != target {
		t.Fatalf("storage root = %q, want %q", got, target)
	}
	if _, err := os.Stat(filepath.Join(target, "library")); err == nil {
		t.Fatal("re-pointing must not touch the filesystem")
	}

	events, err := st.ListEvents(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) == 0 || events[0].Category != relocate.EventCategory {
		t.Fatalf("no %q event recorded; events = %v", relocate.EventCategory, events)
	}
}

func TestStorageRootOperationsRefuseWhileAMigrationIsOpen(t *testing.T) {
	h, st, _ := newTestServer(t)
	source, target := storageRoots(t, st)

	m := &core.StorageMigration{SourceRoot: source, TargetRoot: target}
	if err := st.CreateStorageMigration(context.Background(), m); err != nil {
		t.Fatalf("CreateStorageMigration: %v", err)
	}

	for _, path := range []string{"repoint", "migrate"} {
		rec := do(t, h, http.MethodPost, "/api/v1/system/storage-root/"+path,
			`{"root":"`+jsonPath(target)+`"}`)
		wantStatus(t, rec, http.StatusConflict)
		wantErrorBody(t, rec)
	}
	if got := currentRoot(t, st); got != source {
		t.Fatalf("storage root = %q, want it unchanged at %q", got, source)
	}
}

func TestMigrateStorageRootValidation(t *testing.T) {
	h, st, _ := newTestServer(t)
	source, _ := storageRoots(t, st)

	occupied := filepath.Join(t.TempDir(), "occupied")
	if err := os.MkdirAll(filepath.Join(occupied, "library", "Movies"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	nested := filepath.Join(source, "inner")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	refusals := []struct {
		name string
		root string
		want string
	}{
		{"same root", source, "current one"},
		{"target inside source", nested, "inside"},
		{"source inside target", filepath.Dir(source), "inside"},
		{"target already holds a library", occupied, "non-empty"},
	}
	for _, tc := range refusals {
		t.Run(tc.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/v1/system/storage-root/migrate",
				`{"root":"`+jsonPath(tc.root)+`"}`)
			wantStatus(t, rec, http.StatusBadRequest)
			var body errorResponse
			decodeBody(t, rec, &body)
			if !strings.Contains(body.Error, tc.want) {
				t.Fatalf("error = %q, want it to mention %q", body.Error, tc.want)
			}
			if _, err := st.LatestStorageMigration(context.Background()); err == nil {
				t.Fatal("a refused migration must not leave a row behind")
			}
		})
	}
}

// A first run has no root to move from, and "migrate" is not how one is set.
func TestMigrateStorageRootRefusesWithoutACurrentRoot(t *testing.T) {
	h, _, _ := newTestServer(t)

	rec := do(t, h, http.MethodPost, "/api/v1/system/storage-root/migrate",
		`{"root":"`+jsonPath(t.TempDir())+`"}`)
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}

func TestMigrateStorageRootQueuesAJobAndReportsProgress(t *testing.T) {
	h, st, _ := newTestServer(t)
	source, target := storageRoots(t, st)

	// Nothing has ever run: the poll endpoint says so rather than 404ing.
	rec := do(t, h, http.MethodGet, "/api/v1/system/storage-root/migration", "")
	wantStatus(t, rec, http.StatusOK)
	var empty storageMigrationResponse
	decodeBody(t, rec, &empty)
	if empty.Migration != nil {
		t.Fatalf("migration = %+v, want null", empty.Migration)
	}

	rec = do(t, h, http.MethodPost, "/api/v1/system/storage-root/migrate",
		`{"root":"`+jsonPath(target)+`"}`)
	wantStatus(t, rec, http.StatusAccepted)
	var queued storageMigrationJSON
	decodeBody(t, rec, &queued)
	if queued.SourceRoot != source || queued.TargetRoot != target {
		t.Fatalf("migration = %+v, want %s -> %s", queued, source, target)
	}
	if queued.Status != core.StorageMigrationQueued {
		t.Fatalf("status = %q, want queued", queued.Status)
	}

	// The storage root must not move until the files have.
	if got := currentRoot(t, st); got != source {
		t.Fatalf("storage root = %q, want it still at %q", got, source)
	}

	// A durable job carries the work, and it names the row rather than the paths.
	jobs, err := st.ListJobs(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	var payload relocate.Payload
	var found bool
	for _, j := range jobs {
		if j.Kind != relocate.JobKind {
			continue
		}
		found = true
		if err := json.Unmarshal([]byte(j.Payload), &payload); err != nil {
			t.Fatalf("decode payload %q: %v", j.Payload, err)
		}
	}
	if !found {
		t.Fatalf("no %q job queued; jobs = %v", relocate.JobKind, jobs)
	}
	if payload.MigrationID != queued.ID {
		t.Fatalf("payload migration_id = %d, want %d", payload.MigrationID, queued.ID)
	}

	// A second request while the first is open is a conflict, not a second mover.
	rec = do(t, h, http.MethodPost, "/api/v1/system/storage-root/migrate",
		`{"root":"`+jsonPath(target)+`"}`)
	wantStatus(t, rec, http.StatusConflict)
	wantErrorBody(t, rec)

	rec = do(t, h, http.MethodGet, "/api/v1/system/storage-root/migration", "")
	wantStatus(t, rec, http.StatusOK)
	var polled storageMigrationResponse
	decodeBody(t, rec, &polled)
	if polled.Migration == nil || polled.Migration.ID != queued.ID {
		t.Fatalf("polled migration = %+v, want id %d", polled.Migration, queued.ID)
	}
}

func currentRoot(t *testing.T, st *store.Store) string {
	t.Helper()
	root, err := st.GetSetting(context.Background(), store.SettingStorageRoot)
	if err != nil {
		return ""
	}
	return root
}

// jsonPath escapes a filesystem path for embedding in a JSON string literal,
// which matters on Windows where separators are backslashes.
func jsonPath(p string) string {
	encoded, err := json.Marshal(p)
	if err != nil {
		return p
	}
	return strings.Trim(string(encoded), `"`)
}
