package api

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/watzon/caravan/internal/indexer/packs"
	"github.com/watzon/caravan/internal/store"
)

const (
	backupContentType          = "application/vnd.sqlite3"
	portableBackupContentType  = "application/vnd.caravan.portable+zip"
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
	if s.definitionPacks != nil {
		// The on-disk footprint bounds the snapshot size without taking a
		// full extra in-memory backup just to measure it.
		limit := s.restoreUploadLimit()
		if minimum := s.databaseFootprint() + definitionPackMultipartReserve; limit < minimum {
			limit = minimum
		}
		var portable bytes.Buffer
		if err := s.definitionPacks.CreatePortable(r.Context(), &portable, packs.PortableOptions{MaxBytes: limit}); err != nil {
			s.log.Error("create portable backup", "error", err)
			writeError(w, http.StatusInternalServerError, "could not create database backup")
			return
		}
		w.Header().Set("Cache-Control", "no-store, private")
		filename := "caravan-portable-backup-" + time.Now().UTC().Format("20060102T150405Z") + ".zip"
		w.Header().Set("Content-Type", portableBackupContentType)
		w.Header().Set("Content-Disposition", "attachment; filename="+strconv.Quote(filename))
		if _, err := w.Write(portable.Bytes()); err != nil {
			if clientGone(r) {
				return
			}
			s.log.Error("write portable database backup", "error", err)
		}
		return
	}
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
	contentType := ""
	if raw := r.Header.Get("Content-Type"); raw != "" {
		var parseErr error
		contentType, _, parseErr = mime.ParseMediaType(raw)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "restore upload is invalid")
			return
		}
	}
	body := io.Reader(r.Body)
	switch contentType {
	case portableBackupContentType, backupContentType:
	case "", "application/octet-stream", "application/zip":
		// Generic uploads (curl, scripts, browsers picking their own type)
		// are routed by payload magic: portable backups are ZIP archives.
		buffered := bufio.NewReader(r.Body)
		magic, _ := buffered.Peek(4)
		if bytes.HasPrefix(magic, []byte("PK\x03\x04")) {
			contentType = portableBackupContentType
		} else {
			contentType = backupContentType
		}
		body = buffered
	default:
		writeError(w, http.StatusBadRequest, "restore upload is invalid")
		return
	}
	if contentType == portableBackupContentType {
		if s.definitionPacks == nil {
			writeError(w, http.StatusServiceUnavailable, "definition pack service is not configured")
			return
		}
		limited := http.MaxBytesReader(w, io.NopCloser(body), limit)
		if err := s.definitionPacks.RestorePortable(r.Context(), limited, packs.PortableOptions{MaxBytes: limit}); err != nil {
			var maxBytesErr *http.MaxBytesError
			if errors.As(err, &maxBytesErr) || errors.Is(err, packs.ErrPortableTooLarge) {
				writeError(w, http.StatusRequestEntityTooLarge, restoreUploadLimitMessage(limit))
				return
			}
			s.log.Warn("stage portable database restore rejected", "error", err)
			writeError(w, http.StatusBadRequest, "restore upload is not a compatible Caravan portable backup")
			return
		}
		writeJSON(w, http.StatusAccepted, restoreResponse{RestartRequired: true})
		return
	}
	if err := s.st.StageRestore(r.Context(), body, limit); err != nil {
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

// databaseFootprint reports the on-disk bytes of the live database and its
// WAL sidecar, or 0 when they cannot be measured.
func (s *server) databaseFootprint() int64 {
	if s.runtime == nil || s.runtime.DatabasePath == "" {
		return 0
	}
	var total int64
	for _, path := range []string{s.runtime.DatabasePath, s.runtime.DatabasePath + "-wal"} {
		if info, err := os.Stat(path); err == nil {
			total += info.Size()
		}
	}
	return total
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
