package relocate

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// The invariant every test in this file asserts, directly or through the step
// hook: at every observable point of a migration, every planned file exists at
// exactly one of the two roots, and the storage_root setting names a root that
// has the files.

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

type harness struct {
	t   *testing.T
	st  *store.Store
	svc *Service
	src string
	dst string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	dir := t.TempDir()
	st, err := store.Open(filepath.Join(dir, "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	h := &harness{
		t:   t,
		st:  st,
		src: filepath.Join(dir, "old-root"),
		dst: filepath.Join(dir, "new-root"),
	}
	for _, root := range []string{h.src, h.dst} {
		if err := os.MkdirAll(root, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", root, err)
		}
	}
	if err := st.SetSetting(context.Background(), store.SettingStorageRoot, h.src); err != nil {
		t.Fatalf("seed storage root: %v", err)
	}
	h.svc = New(st, nil, discardLogger())
	return h
}

// write creates a file under root at a storage-root-relative slash path.
func (h *harness) write(root, rel, content string) {
	h.t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		h.t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		h.t.Fatalf("write %s: %v", rel, err)
	}
}

// seed lays down a small but representative library plus an in-flight download.
func (h *harness) seed() map[string]string {
	h.t.Helper()
	files := map[string]string{
		"library/Movies/Arrival (2016)/Arrival (2016).mkv": "arrival-video-bytes",
		"library/Movies/Arrival (2016)/poster.jpg":         "arrival-poster",
		"library/Movies/Zed (2001)/Zed (2001).mkv":         "zed-video",
		"library/TV/Show (2019)/Season 01/S01E01.mkv":      "s01e01",
		"incomplete/.caravan/abc123.torrent":               "metainfo",
		"incomplete/Pending Movie/Pending.mkv":             "half-downloaded",
	}
	for rel, content := range files {
		h.write(h.src, rel, content)
	}
	return files
}

// queue creates the migration row the handler works from.
func (h *harness) queue(source, target string) *core.StorageMigration {
	h.t.Helper()
	m := &core.StorageMigration{SourceRoot: source, TargetRoot: target}
	if err := h.st.CreateStorageMigration(context.Background(), m); err != nil {
		h.t.Fatalf("CreateStorageMigration: %v", err)
	}
	return m
}

func (h *harness) run(m *core.StorageMigration) {
	h.t.Helper()
	payload, err := json.Marshal(Payload{MigrationID: m.ID})
	if err != nil {
		h.t.Fatalf("marshal payload: %v", err)
	}
	if err := h.svc.Handle(context.Background(), nil, json.RawMessage(payload)); err != nil {
		h.t.Fatalf("Handle returned an error; the row is meant to carry the failure: %v", err)
	}
}

func (h *harness) reload(id int64) *core.StorageMigration {
	h.t.Helper()
	m, err := h.st.GetStorageMigration(context.Background(), id)
	if err != nil {
		h.t.Fatalf("GetStorageMigration: %v", err)
	}
	return m
}

func (h *harness) storageRoot() string {
	h.t.Helper()
	root, err := h.st.GetSetting(context.Background(), store.SettingStorageRoot)
	if err != nil {
		h.t.Fatalf("read storage root: %v", err)
	}
	return root
}

// wantOnlyAt asserts the file is readable with the expected content under
// `here` and absent under `notHere` — the single-location half of the invariant.
func wantOnlyAt(t *testing.T, here, notHere, rel, content string) {
	t.Helper()
	got, err := os.ReadFile(filepath.Join(here, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("%s is missing from %s: %v", rel, here, err)
	}
	if string(got) != content {
		t.Fatalf("%s = %q, want %q", rel, got, content)
	}
	if _, err := os.Lstat(filepath.Join(notHere, filepath.FromSlash(rel))); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("%s is still present under %s (err=%v); a moved file must exist at exactly one root", rel, notHere, err)
	}
}

// watchInvariant installs the step hook so every planned file is checked for
// existence at one root or the other after every file the mover touches.
func (h *harness) watchInvariant(files map[string]string) {
	h.t.Helper()
	h.svc.step = func(string) {
		for rel := range files {
			_, srcErr := os.Lstat(filepath.Join(h.src, filepath.FromSlash(rel)))
			_, dstErr := os.Lstat(filepath.Join(h.dst, filepath.FromSlash(rel)))
			if srcErr != nil && dstErr != nil {
				h.t.Errorf("%s exists at neither root mid-move (src=%v, dst=%v)", rel, srcErr, dstErr)
			}
		}
	}
}

func TestMigrateMovesEveryFileAndFlipsTheRootLast(t *testing.T) {
	h := newHarness(t)
	files := h.seed()
	h.watchInvariant(files)

	// A library row, to prove the database needs no rewriting: its path is
	// relative, so the same row describes the file at the new root.
	movie := &core.Movie{Title: "Arrival", Year: 2016, TMDBID: 329865}
	if err := h.st.UpsertMovie(context.Background(), movie); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	const rel = "library/Movies/Arrival (2016)/Arrival (2016).mkv"
	file := &core.MediaFile{MovieID: movie.ID, Path: rel, Size: 19}
	if err := h.st.UpsertMediaFile(context.Background(), file); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}

	m := h.queue(h.src, h.dst)
	h.run(m)

	got := h.reload(m.ID)
	if got.Status != core.StorageMigrationDone {
		t.Fatalf("status = %q (%s), want done", got.Status, got.Error)
	}
	if got.FilesDone != int64(len(files)) || got.FilesTotal != int64(len(files)) {
		t.Fatalf("progress = %d/%d, want %d/%d", got.FilesDone, got.FilesTotal, len(files), len(files))
	}
	for rel, content := range files {
		wantOnlyAt(t, h.dst, h.src, rel, content)
	}
	if root := h.storageRoot(); root != h.dst {
		t.Fatalf("storage root = %q, want %q", root, h.dst)
	}

	// The database is untouched: relative paths are what make a migration a
	// file operation rather than a schema operation.
	stored, err := h.st.GetMediaFile(context.Background(), file.ID)
	if err != nil {
		t.Fatalf("GetMediaFile: %v", err)
	}
	if stored.Path != rel {
		t.Fatalf("media file path = %q, want it unchanged at %q", stored.Path, rel)
	}

	// History survives: the completion is in the activity feed.
	events, err := h.st.ListEvents(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	var found bool
	for _, e := range events {
		if e.Category == EventCategory && strings.Contains(e.Message, h.dst) {
			found = true
		}
	}
	if !found {
		t.Fatalf("no %q event naming the new root; events = %v", EventCategory, events)
	}

	// The old root keeps no empty skeleton behind.
	for _, tree := range trees {
		if _, err := os.Stat(filepath.Join(h.src, tree)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s survived at the old root (err=%v)", tree, err)
		}
	}
}

func TestMigrateRollsBackWhenAFileCannotLand(t *testing.T) {
	h := newHarness(t)
	files := h.seed()
	h.watchInvariant(files)

	// A real filesystem failure rather than an injected one: a directory
	// standing where a file has to go. Nothing can rename or copy over it, and
	// it fails partway through — the incomplete tree and the earlier library
	// files have already moved by then.
	blocked := filepath.Join(h.dst, filepath.FromSlash("library/Movies/Zed (2001)/Zed (2001).mkv"))
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("place the blocking directory: %v", err)
	}

	m := h.queue(h.src, h.dst)
	h.run(m)

	got := h.reload(m.ID)
	if got.Status != core.StorageMigrationRolledBack {
		t.Fatalf("status = %q (%s), want rolled_back", got.Status, got.Error)
	}
	if got.Error == "" {
		t.Fatal("a rolled-back migration must record why")
	}
	// Everything is back where it started, byte for byte.
	for rel, content := range files {
		wantOnlyAt(t, h.src, h.dst, rel, content)
	}
	// And the root never moved, so the library the user reloads is intact.
	if root := h.storageRoot(); root != h.src {
		t.Fatalf("storage root = %q, want it still at %q", root, h.src)
	}
}

func TestMigrateResumesAfterACrash(t *testing.T) {
	h := newHarness(t)
	files := h.seed()

	// The state a crash leaves: some files at the target, some still at the
	// source, one copied but not yet released, and the row stuck at running.
	moved := "library/Movies/Arrival (2016)/Arrival (2016).mkv"
	h.write(h.dst, moved, files[moved])
	if err := os.Remove(filepath.Join(h.src, filepath.FromSlash(moved))); err != nil {
		t.Fatalf("simulate a completed move: %v", err)
	}
	duplicated := "library/TV/Show (2019)/Season 01/S01E01.mkv"
	h.write(h.dst, duplicated, files[duplicated])

	h.watchInvariant(files)

	m := h.queue(h.src, h.dst)
	m.Status = core.StorageMigrationRunning
	if err := h.st.UpdateStorageMigration(context.Background(), m); err != nil {
		t.Fatalf("UpdateStorageMigration: %v", err)
	}

	h.run(m)

	got := h.reload(m.ID)
	if got.Status != core.StorageMigrationDone {
		t.Fatalf("status = %q (%s), want done", got.Status, got.Error)
	}
	if got.FilesTotal != int64(len(files)) {
		t.Fatalf("files_total = %d, want %d: the plan is the union of both roots, "+
			"so a resume must not report a shrinking total", got.FilesTotal, len(files))
	}
	for rel, content := range files {
		wantOnlyAt(t, h.dst, h.src, rel, content)
	}
	if root := h.storageRoot(); root != h.dst {
		t.Fatalf("storage root = %q, want %q", root, h.dst)
	}
}

func TestMigrateReplacesAShortPartialAtTheTarget(t *testing.T) {
	h := newHarness(t)
	files := h.seed()

	// The cross-filesystem path: a target file that exists but is the wrong
	// size is a copy that died halfway, and must be redone rather than trusted.
	partial := "library/Movies/Arrival (2016)/Arrival (2016).mkv"
	h.write(h.dst, partial, "arri")

	h.watchInvariant(files)
	m := h.queue(h.src, h.dst)
	h.run(m)

	if got := h.reload(m.ID); got.Status != core.StorageMigrationDone {
		t.Fatalf("status = %q (%s), want done", got.Status, got.Error)
	}
	for rel, content := range files {
		wantOnlyAt(t, h.dst, h.src, rel, content)
	}
	// No temporary file is left describing a move that finished.
	dir := filepath.Join(h.dst, filepath.FromSlash("library/Movies/Arrival (2016)"))
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), movePrefix) {
			t.Fatalf("leftover partial copy %s", e.Name())
		}
	}
}

func TestMigrateRefusesNestedRootsWithoutMovingAnything(t *testing.T) {
	h := newHarness(t)
	files := h.seed()
	nested := filepath.Join(h.src, "inner")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// Queued directly, bypassing the API's validation: this asserts the job
	// re-checks rather than trusting what it was handed hours earlier.
	m := h.queue(h.src, nested)
	h.run(m)

	got := h.reload(m.ID)
	if got.Status != core.StorageMigrationRolledBack {
		t.Fatalf("status = %q, want rolled_back", got.Status)
	}
	if !strings.Contains(got.Error, "inside") {
		t.Fatalf("error = %q, want it to name the nesting", got.Error)
	}
	for rel, content := range files {
		wantOnlyAt(t, h.src, nested, rel, content)
	}
	if root := h.storageRoot(); root != h.src {
		t.Fatalf("storage root = %q, want it still at %q", root, h.src)
	}
}

// stubEngine is the download queue as the migration sees it. The embedded nil
// interface supplies the methods this package never calls, so a future call to
// one is a panic in a test rather than a silent no-op.
type stubEngine struct {
	core.Engine
	list       []core.DownloadStatus
	listErr    error
	pauseErr   map[core.DownloadID]error
	paused     []core.DownloadID
	resumed    []core.DownloadID
	quiesceErr error
	resumeErr  error
	quiesced   bool
}

func (e *stubEngine) List(context.Context) ([]core.DownloadStatus, error) { return e.list, e.listErr }

func (e *stubEngine) Pause(_ context.Context, id core.DownloadID) error {
	if err := e.pauseErr[id]; err != nil {
		return err
	}
	e.paused = append(e.paused, id)
	return nil
}

func (e *stubEngine) Resume(_ context.Context, id core.DownloadID) error {
	e.resumed = append(e.resumed, id)
	return nil
}

func (e *stubEngine) Quiesce(context.Context) error {
	if e.quiesceErr != nil {
		return e.quiesceErr
	}
	e.quiesced = true
	return nil
}

func (e *stubEngine) ResumeQuiesced(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if e.resumeErr != nil {
		return e.resumeErr
	}
	e.quiesced = false
	return nil
}

func TestMigrateQuiescesTheQueueAndReleasesItOnSuccess(t *testing.T) {
	h := newHarness(t)
	h.seed()
	engine := &stubEngine{list: []core.DownloadStatus{
		{ID: "active", State: core.DownloadDownloading},
		{ID: "seeding", State: core.DownloadSeeding},
		{ID: "already-paused", State: core.DownloadPaused},
		{ID: "finished", State: core.DownloadCompleted},
	}}
	h.svc = New(h.st, func() core.Engine { return engine }, discardLogger())

	m := h.queue(h.src, h.dst)
	h.run(m)

	if engine.quiesced {
		t.Fatal("queue is still quiesced after a completed migration")
	}
}

func TestMigrateResumesTheQueueAfterARollback(t *testing.T) {
	h := newHarness(t)
	h.seed()
	engine := &stubEngine{list: []core.DownloadStatus{{ID: "active", State: core.DownloadDownloading}}}
	h.svc = New(h.st, func() core.Engine { return engine }, discardLogger())

	blocked := filepath.Join(h.dst, filepath.FromSlash("library/Movies/Zed (2001)/Zed (2001).mkv"))
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("place the blocking directory: %v", err)
	}

	m := h.queue(h.src, h.dst)
	h.run(m)

	if got := h.reload(m.ID); got.Status != core.StorageMigrationRolledBack {
		t.Fatalf("status = %q, want rolled_back", got.Status)
	}
	if engine.quiesced {
		t.Fatal("queue is still quiesced after rollback")
	}
}

func TestMigrateAbortsWhenTheQueueCannotQuiesce(t *testing.T) {
	h := newHarness(t)
	files := h.seed()
	engine := &stubEngine{quiesceErr: errors.New("engine is unavailable")}
	h.svc = New(h.st, func() core.Engine { return engine }, discardLogger())

	m := h.queue(h.src, h.dst)
	err := h.svc.Handle(context.Background(), nil, mustPayload(t, m.ID))
	if err == nil || !strings.Contains(err.Error(), "quiesce the download queue") {
		t.Fatalf("Handle error = %v, want queue-quiesce failure", err)
	}
	for rel, content := range files {
		wantOnlyAt(t, h.src, h.dst, rel, content)
	}
}

func TestMigrateAbortsWhenTheQueueBarrierRejectsANewTransfer(t *testing.T) {
	h := newHarness(t)
	files := h.seed()
	engine := &stubEngine{quiesceErr: errors.New("new transfer attempted while quiescing")}
	h.svc = New(h.st, func() core.Engine { return engine }, discardLogger())

	m := h.queue(h.src, h.dst)
	err := h.svc.Handle(context.Background(), nil, mustPayload(t, m.ID))
	if err == nil || !strings.Contains(err.Error(), "new transfer attempted") {
		t.Fatalf("Handle error = %v, want the barrier to reject the new transfer", err)
	}
	for rel, content := range files {
		wantOnlyAt(t, h.src, h.dst, rel, content)
	}
}

func TestResumeQueueIgnoresTheJobCancellation(t *testing.T) {
	h := newHarness(t)
	engine := &stubEngine{quiesced: true}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := h.svc.resumeQueue(ctx, engine); err != nil {
		t.Fatalf("resumeQueue: %v", err)
	}
	if engine.quiesced {
		t.Fatal("queue is still quiesced after resuming from a cancelled job")
	}
}

func TestHandleRetriesWhenQueueReleaseFails(t *testing.T) {
	h := newHarness(t)
	h.seed()
	engine := &stubEngine{resumeErr: errors.New("client is unavailable")}
	h.svc = New(h.st, func() core.Engine { return engine }, discardLogger())
	m := h.queue(h.src, h.dst)
	payload := mustPayload(t, m.ID)

	if err := h.svc.Handle(context.Background(), nil, payload); err == nil {
		t.Fatal("Handle returned nil, want retryable queue-release failure")
	}
	if got := h.reload(m.ID); got.Status != core.StorageMigrationRunning {
		t.Fatalf("status = %q, want running until the queue is released", got.Status)
	}
	engine.resumeErr = nil
	if err := h.svc.Handle(context.Background(), nil, payload); err != nil {
		t.Fatalf("retry Handle: %v", err)
	}
	if got := h.reload(m.ID); got.Status != core.StorageMigrationDone {
		t.Fatalf("status = %q, want done after queue release", got.Status)
	}
}

// failingStore wraps the real store so these tests fail one durable operation
// without hiding the row and filesystem state that a later delivery must read.
type failingStore struct {
	relocationStore
	getErr    error
	updateErr func(*core.StorageMigration) error
}

func (s *failingStore) GetStorageMigration(ctx context.Context, id int64) (*core.StorageMigration, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	return s.relocationStore.GetStorageMigration(ctx, id)
}

func (s *failingStore) UpdateStorageMigration(ctx context.Context, m *core.StorageMigration) error {
	if s.updateErr != nil {
		if err := s.updateErr(m); err != nil {
			return err
		}
	}
	return s.relocationStore.UpdateStorageMigration(ctx, m)
}

func mustPayload(t *testing.T, id int64) json.RawMessage {
	t.Helper()
	payload, err := json.Marshal(Payload{MigrationID: id})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return payload
}

func TestHandleRetriesWhenReadingOrClaimingTheMigrationFails(t *testing.T) {
	for name, configure := range map[string]func(*failingStore){
		"read": func(s *failingStore) { s.getErr = errors.New("database is unavailable") },
		"claim": func(s *failingStore) {
			s.updateErr = func(*core.StorageMigration) error { return errors.New("database is unavailable") }
		},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			files := h.seed()
			m := h.queue(h.src, h.dst)
			failing := &failingStore{relocationStore: h.st}
			configure(failing)
			h.svc.st = failing

			if err := h.svc.Handle(context.Background(), nil, mustPayload(t, m.ID)); err == nil {
				t.Fatal("Handle returned nil, want retryable store failure")
			}
			for rel, content := range files {
				wantOnlyAt(t, h.src, h.dst, rel, content)
			}
		})
	}
}

func TestHandleRetriesWhenCompletionCannotBeStored(t *testing.T) {
	h := newHarness(t)
	files := h.seed()
	m := h.queue(h.src, h.dst)
	failed := false
	failing := &failingStore{relocationStore: h.st, updateErr: func(m *core.StorageMigration) error {
		if !failed && m.Status == core.StorageMigrationDone {
			failed = true
			return errors.New("database is unavailable")
		}
		return nil
	}}
	h.svc.st = failing

	if err := h.svc.Handle(context.Background(), nil, mustPayload(t, m.ID)); err == nil {
		t.Fatal("Handle returned nil, want retryable completion failure")
	}
	if got := h.reload(m.ID); got.Status != core.StorageMigrationRunning {
		t.Fatalf("status = %q, want running until completion is stored", got.Status)
	}
	if err := h.svc.Handle(context.Background(), nil, mustPayload(t, m.ID)); err != nil {
		t.Fatalf("retry Handle: %v", err)
	}
	if got := h.reload(m.ID); got.Status != core.StorageMigrationDone {
		t.Fatalf("status = %q, want done after retry", got.Status)
	}
	for rel, content := range files {
		wantOnlyAt(t, h.dst, h.src, rel, content)
	}
}

func TestHandleRetriesWhenRollbackCannotBeStored(t *testing.T) {
	h := newHarness(t)
	h.seed()
	blocked := filepath.Join(h.dst, filepath.FromSlash("library/Movies/Zed (2001)/Zed (2001).mkv"))
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("place the blocking directory: %v", err)
	}
	m := h.queue(h.src, h.dst)
	failed := false
	failing := &failingStore{relocationStore: h.st, updateErr: func(m *core.StorageMigration) error {
		if !failed && m.Status == core.StorageMigrationRolledBack {
			failed = true
			return errors.New("database is unavailable")
		}
		return nil
	}}
	h.svc.st = failing
	payload := mustPayload(t, m.ID)

	if err := h.svc.Handle(context.Background(), nil, payload); err == nil {
		t.Fatal("Handle returned nil, want retryable rollback failure")
	}
	if got := h.reload(m.ID); got.Status != core.StorageMigrationRunning || got.Error == "" {
		t.Fatalf("migration = %#v, want durable running rollback marker", got)
	}
	if err := h.svc.Handle(context.Background(), nil, payload); err != nil {
		t.Fatalf("retry Handle: %v", err)
	}
	if got := h.reload(m.ID); got.Status != core.StorageMigrationRolledBack {
		t.Fatalf("status = %q, want rolled_back after retry", got.Status)
	}
}

func TestHandleIsIdempotentAfterSuccessfulCompletion(t *testing.T) {
	h := newHarness(t)
	files := h.seed()
	m := h.queue(h.src, h.dst)
	payload := mustPayload(t, m.ID)

	if err := h.svc.Handle(context.Background(), nil, payload); err != nil {
		t.Fatalf("first Handle: %v", err)
	}
	if err := h.svc.Handle(context.Background(), nil, payload); err != nil {
		t.Fatalf("redelivered Handle: %v", err)
	}
	if got := h.reload(m.ID); got.Status != core.StorageMigrationDone {
		t.Fatalf("status = %q, want done", got.Status)
	}
	for rel, content := range files {
		wantOnlyAt(t, h.dst, h.src, rel, content)
	}
}

func equalIDs(got, want []core.DownloadID) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestHandleIsANoOpForFinishedAndUnknownMigrations(t *testing.T) {
	h := newHarness(t)
	files := h.seed()

	m := h.queue(h.src, h.dst)
	m.Status = core.StorageMigrationDone
	if err := h.st.UpdateStorageMigration(context.Background(), m); err != nil {
		t.Fatalf("UpdateStorageMigration: %v", err)
	}
	h.run(m)
	for rel, content := range files {
		wantOnlyAt(t, h.src, h.dst, rel, content)
	}

	// An unknown id and an undecodable payload both complete the job rather
	// than burning retries on something that will never decode.
	if err := h.svc.Handle(context.Background(), nil, json.RawMessage(`{"migration_id":9999}`)); err != nil {
		t.Fatalf("unknown migration: %v", err)
	}
	if err := h.svc.Handle(context.Background(), nil, json.RawMessage(`not json`)); err != nil {
		t.Fatalf("undecodable payload: %v", err)
	}
}
