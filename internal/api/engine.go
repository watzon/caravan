package api

import (
	"errors"
	"math"
	"net/http"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/store"
)

// handleDownloadInsight returns the torrent-specific information the queue
// detail drawer displays. It is optional because phase-6 external engines may
// not expose peer or tracker information.
func (s *server) handleDownloadInsight(w http.ResponseWriter, r *http.Request) {
	id, ok := downloadID(w, r)
	if !ok {
		return
	}
	engine, ok := s.requireEngine(w)
	if !ok {
		return
	}
	insighter, ok := engine.(core.EngineInsight)
	if !ok {
		writeError(w, http.StatusBadRequest, "download engine does not provide insight")
		return
	}
	insight, err := insighter.Insight(r.Context(), id)
	if err != nil {
		s.writeDownloadEngineError(w, "get download insight", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"insight": insight})
}

// handleSetDownloadLimits stores byte-per-second overrides and applies their
// KB/s equivalents to an engine that supports rate controls.
func (s *server) handleSetDownloadLimits(w http.ResponseWriter, r *http.Request) {
	id, ok := downloadID(w, r)
	if !ok {
		return
	}
	var body struct {
		MaxDownKBps int64 `json:"max_down_kbps"`
		MaxUpKBps   int64 `json:"max_up_kbps"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.MaxDownKBps < 0 || body.MaxUpKBps < 0 ||
		body.MaxDownKBps > math.MaxInt64/1024 || body.MaxUpKBps > math.MaxInt64/1024 {
		writeError(w, http.StatusBadRequest, "rate limits must be non-negative KB/s values")
		return
	}
	engine, ok := s.requireEngine(w)
	if !ok {
		return
	}
	limiter, ok := engine.(core.EngineRateLimits)
	if !ok {
		writeError(w, http.StatusBadRequest, "download engine does not support per-download rate limits")
		return
	}
	row, err := s.st.GetDownloadByEngineID(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		s.writeStoreError(w, "get download", err)
		return
	}
	if err := limiter.SetDownloadRates(r.Context(), id, body.MaxDownKBps, body.MaxUpKBps); err != nil {
		s.writeDownloadEngineError(w, "set download limits", err)
		return
	}
	row.MaxDownRate = body.MaxDownKBps * 1024
	row.MaxUpRate = body.MaxUpKBps * 1024
	if err := s.st.UpsertDownload(r.Context(), row); err != nil {
		s.writeStoreError(w, "set download limits", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) writeDownloadEngineError(w http.ResponseWriter, msg string, err error) {
	if errors.Is(err, download.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	// A router satisfies the optional extensions on behalf of engines that may
	// not, so "this download's engine cannot do that" arrives as an error
	// rather than a failed type assertion. It is the same 400 either way.
	if errors.Is(err, download.ErrUnsupported) {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// "This download has nothing to retry" is a different mistake: the engine
	// can do it, the caller acted on state it had misread, and the answer is
	// the conflict the queue's own next poll will explain.
	if errors.Is(err, download.ErrNotRetryable) {
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	s.writeEngineError(w, msg, err)
}
