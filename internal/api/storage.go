package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/relocate"
	"github.com/watzon/caravan/internal/store"
)

// Moving the storage root (SPEC §10, PLAN phase 5 task 4).
//
// Two endpoints because they are two different promises.
//
// Re-point is instant and answers synchronously: it validates the folder,
// writes one setting, and every consumer that resolves the root per call — the
// library manager, the media server, the convert queue — is already looking at
// the new one by the time the response lands. It never touches media.
//
// Migrate moves the files, which is hours of work that has to survive the
// browser being closed. It answers 202 with a row id and the settings screen
// polls the row, exactly as the convert queue does.

// storageMigrationJSON is one storage migration as the settings screen sees it.
type storageMigrationJSON struct {
	ID         int64  `json:"id"`
	SourceRoot string `json:"source_root"`
	TargetRoot string `json:"target_root"`
	// Status is "queued", "running", "done", "rolled_back" or "failed".
	// "rolled_back" means the move broke and undid itself: the old root still
	// has everything and nothing was lost. "failed" is the one that needs a
	// human — part of the library is under each root.
	Status     string `json:"status"`
	FilesTotal int64  `json:"files_total"`
	FilesDone  int64  `json:"files_done"`
	BytesTotal int64  `json:"bytes_total"`
	BytesDone  int64  `json:"bytes_done"`
	Error      string `json:"error"`
	CreatedAt  string `json:"created_at"`
	UpdatedAt  string `json:"updated_at"`
}

func storageMigrationDTO(m core.StorageMigration) storageMigrationJSON {
	return storageMigrationJSON{
		ID:         m.ID,
		SourceRoot: m.SourceRoot,
		TargetRoot: m.TargetRoot,
		Status:     m.Status,
		FilesTotal: m.FilesTotal,
		FilesDone:  m.FilesDone,
		BytesTotal: m.BytesTotal,
		BytesDone:  m.BytesDone,
		Error:      m.Error,
		CreatedAt:  jsonTime(m.CreatedAt),
		UpdatedAt:  jsonTime(m.UpdatedAt),
	}
}

// storageRootRequest is the body of both storage-root operations.
type storageRootRequest struct {
	Root string `json:"root"`
}

// repointResponse is what POST /system/storage-root/repoint answers with.
type repointResponse struct {
	Root string `json:"root"`
	// Warnings are things worth knowing that are not reasons to refuse — most
	// often "this folder has no library in it", which is what re-pointing at a
	// fresh drive looks like and must not be blocked.
	Warnings []string `json:"warnings"`
	// RestartRequired says the download engine is still writing under the old
	// root. It captured that root when it was built and cannot be re-pointed
	// underneath a running process, so the queue follows on the next start.
	RestartRequired bool `json:"restart_required"`
}

// handleRepointStorageRoot changes where Caravan looks, without moving a byte.
func (s *server) handleRepointStorageRoot(w http.ResponseWriter, r *http.Request) {
	var body storageRootRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	root := strings.TrimSpace(body.Root)

	warnings, err := relocate.ValidateRoot(root)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// A migration in flight owns both roots. Re-pointing under it would leave
	// the mover copying into a root nothing reads and the library pointing at a
	// tree that is being emptied.
	if !s.storageRootFree(w, r) {
		return
	}

	previous, err := s.st.GetSetting(r.Context(), store.SettingStorageRoot)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeStoreError(w, "read storage root", err)
		return
	}
	if err := s.st.SetSetting(r.Context(), store.SettingStorageRoot, root); err != nil {
		s.writeStoreError(w, "write storage root", err)
		return
	}

	// The media server holds settings of its own and re-reads them on request,
	// the same way a saved DLNA toggle reaches it. Everything else — the library
	// manager, the convert queue — resolves the root per call and needs nothing.
	if s.dlna != nil {
		s.dlna.Reload(r.Context())
	}

	detail := "Files were not moved; every stored path is relative to the root."
	if previous != "" {
		detail = "Previously " + previous + ". " + detail
	}
	s.logEvent(r.Context(), &core.Event{
		Level:    core.EventLevelInfo,
		Category: relocate.EventCategory,
		Message:  "Storage root re-pointed to " + root,
		Detail:   detail,
	})

	writeJSON(w, http.StatusOK, repointResponse{
		Root:            root,
		Warnings:        warnings,
		RestartRequired: s.engineHealth() == "ok",
	})
}

// handleMigrateStorageRoot queues the move of the library and incomplete
// folders to a new root.
//
// It validates and refuses here rather than inside the job wherever it can: a
// nested path or an occupied target is something the user can fix while they are
// still looking at the screen, and a 400 is a better answer than a rolled-back
// migration ten seconds later.
func (s *server) handleMigrateStorageRoot(w http.ResponseWriter, r *http.Request) {
	var body storageRootRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	target := strings.TrimSpace(body.Root)

	source, err := s.st.GetSetting(r.Context(), store.SettingStorageRoot)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.writeStoreError(w, "read storage root", err)
		return
	}
	if source == "" {
		writeError(w, http.StatusBadRequest, "there is no storage root to move from")
		return
	}
	if err := relocate.ValidateMove(source, target); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := relocate.ValidateFreshTarget(target); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.storageRootFree(w, r) {
		return
	}

	m := &core.StorageMigration{
		SourceRoot: source,
		TargetRoot: target,
		Status:     core.StorageMigrationQueued,
	}
	if err := s.st.CreateStorageMigration(r.Context(), m); err != nil {
		if errors.Is(err, store.ErrStorageMigrationOpen) {
			writeError(w, http.StatusConflict, "a storage migration is already running")
			return
		}
		s.writeStoreError(w, "queue storage migration", err)
		return
	}

	payload, err := json.Marshal(relocate.Payload{MigrationID: m.ID})
	if err != nil {
		s.writeStoreError(w, "queue storage migration", err)
		return
	}
	if err := s.st.EnqueueJob(r.Context(), &core.Job{Kind: relocate.JobKind, Payload: string(payload)}); err != nil {
		// A migration nothing will ever run must not sit in the queue looking
		// like it is about to start (SPEC §13).
		m.Status = core.StorageMigrationFailed
		m.Error = "the migration could not be queued"
		_ = s.st.UpdateStorageMigration(r.Context(), m)
		s.writeStoreError(w, "queue storage migration", err)
		return
	}

	s.logEvent(r.Context(), &core.Event{
		Level:    core.EventLevelInfo,
		Category: relocate.EventCategory,
		Message:  "Storage migration queued: " + source + " to " + target,
		Detail:   "Downloads are paused while the files move. The storage root changes only once every file has arrived.",
	})
	writeJSON(w, http.StatusAccepted, storageMigrationDTO(*m))
}

// storageMigrationResponse is what the settings screen polls.
type storageMigrationResponse struct {
	// Migration is the most recent one whatever its status, or null when none
	// has ever run. A finished move has to stay on screen long enough to read.
	Migration *storageMigrationJSON `json:"migration"`
	// RestartRequired mirrors the re-point answer: after a completed migration
	// the download engine is still pointed at the old root until the next start.
	RestartRequired bool `json:"restart_required"`
}

// handleStorageMigration reports the latest storage migration.
func (s *server) handleStorageMigration(w http.ResponseWriter, r *http.Request) {
	m, err := s.st.LatestStorageMigration(r.Context())
	if errors.Is(err, store.ErrNotFound) {
		writeJSON(w, http.StatusOK, storageMigrationResponse{})
		return
	}
	if err != nil {
		s.writeStoreError(w, "read storage migration", err)
		return
	}
	dto := storageMigrationDTO(*m)
	writeJSON(w, http.StatusOK, storageMigrationResponse{
		Migration:       &dto,
		RestartRequired: m.Status == core.StorageMigrationDone && s.engineHealth() == "ok",
	})
}

// scanBlockedByMigration is what POST /library/rescan and POST /system/verify
// answer while a migration is in flight.
//
// A scan reconciles the database against what is under the storage root, and it
// deletes the media_file row of every path that is no longer there — artwork
// references included. While a migration is running, "no longer there" is every
// file the mover has already taken, so a scan started mid-move empties the
// library. The dirty-eject recovery banner is the path that matters: a crash
// mid-migration sets the dirty flag, and its one button is "Verify & rescan".
const scanBlockedByMigration = "a storage migration is running; the library cannot be scanned until the files stop moving"

// noOpenMigration writes a 409 with conflict and returns false while a
// migration owns the roots. Everything that changes the storage-root setting or
// reconciles the files underneath it gates on this: they may only be done by
// one thing at a time.
func (s *server) noOpenMigration(w http.ResponseWriter, r *http.Request, conflict string) bool {
	switch _, err := s.st.OpenStorageMigration(r.Context()); {
	case err == nil:
		writeError(w, http.StatusConflict, conflict)
		return false
	case !errors.Is(err, store.ErrNotFound):
		s.writeStoreError(w, "read storage migration", err)
		return false
	}
	return true
}

// storageRootFree gates the two storage-root operations against each other.
func (s *server) storageRootFree(w http.ResponseWriter, r *http.Request) bool {
	return s.noOpenMigration(w, r, "a storage migration is already running")
}
