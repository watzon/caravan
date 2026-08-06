package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/watzon/caravan/internal/store"
)

const (
	backupContentType          = "application/vnd.sqlite3"
	fallbackRestoreUploadSize  = 256 << 20
	restoreUploadSafetyReserve = 64 << 20
	minimumRestoreUploadSize   = 1
)

// restoreDiskUsage lets tests set a precise filesystem capacity.
var restoreDiskUsage = diskUsage

type restoreResponse struct {
	RestartRequired bool `json:"restart_required"`
}

// handleBackup streams a SQLite-consistent snapshot. The Store creates the
// snapshot before it writes any bytes, so this endpoint never copies a live WAL
// main file whose committed pages may still live in its sidecar.
func (s *server) handleBackup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, private")
	filename := "caravan-backup-" + time.Now().UTC().Format("20060102T150405Z") + ".sqlite"
	w.Header().Set("Content-Type", backupContentType)
	w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(filename))
	if err := s.st.Backup(r.Context(), w); err != nil {
		if clientGone(r) {
			return
		}
		s.log.Error("create database backup", "error", err)
		writeError(w, http.StatusInternalServerError, "could not create database backup")
	}
}

// handleRestore validates and stages a raw SQLite upload. Applying it is a
// startup operation: replacing an open database would be unsafe, so Open does
// the atomic cutover only after Caravan restarts.
func (s *server) handleRestore(w http.ResponseWriter, r *http.Request) {
	limit := s.restoreUploadLimit()
	if r.ContentLength > limit {
		writeError(w, http.StatusRequestEntityTooLarge, restoreUploadLimitMessage(limit))
		return
	}
	if err := s.st.StageRestore(r.Context(), r.Body, limit); err != nil {
		switch {
		case errors.Is(err, store.ErrRestoreTooLarge):
			writeError(w, http.StatusRequestEntityTooLarge, restoreUploadLimitMessage(limit))
		case errors.Is(err, store.ErrInvalidRestore):
			writeError(w, http.StatusBadRequest, "restore upload is not a compatible Caravan database")
		default:
			s.log.Error("stage database restore", "error", err)
			writeError(w, http.StatusInternalServerError, "could not stage database restore")
		}
		return
	}
	writeJSON(w, http.StatusAccepted, restoreResponse{RestartRequired: true})
}

func (s *server) restoreUploadLimit() int64 {
	if s.runtime == nil || s.runtime.DatabasePath == "" {
		return fallbackRestoreUploadSize
	}
	free, total, err := restoreDiskUsage(s.runtime.DatabasePath)
	return restoreUploadLimitFromUsage(free, total, err)
}

func restoreUploadLimitFromUsage(free, total int64, err error) int64 {
	if err != nil || total <= 0 {
		return fallbackRestoreUploadSize
	}
	if free <= restoreUploadSafetyReserve {
		return minimumRestoreUploadSize
	}
	return free - restoreUploadSafetyReserve
}

func restoreUploadLimitMessage(limit int64) string {
	return fmt.Sprintf("restore upload exceeds the %d byte limit", limit)
}
