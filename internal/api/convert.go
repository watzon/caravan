package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/watzon/caravan/internal/convert"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

const (
	defaultConversionLimit = 100
	maxConversionLimit     = 1000
)

// Converter is the slice of internal/convert the HTTP layer needs: whether
// ffmpeg is installed at all. Everything else a conversion needs is a store
// write plus a durable job, both of which this package already does.
//
// It is an interface for the same reason EngineProvider is — a server built
// without one still serves the rest of the API, and answers /convert with a
// 503 the UI turns into "install ffmpeg" rather than an error (SPEC §8:
// ffmpeg missing degrades, it does not break).
type Converter interface {
	// Available reports whether ffmpeg and ffprobe were both found.
	Available() bool
}

// WithConverter supplies the convert-for-TV queue's ffmpeg availability.
func WithConverter(c Converter) Option {
	return func(s *server) { s.converter = c }
}

// conversionJSON is one row of the convert queue.
type conversionJSON struct {
	ID          int64  `json:"id"`
	MediaFileID int64  `json:"media_file_id"`
	SourcePath  string `json:"source_path"`
	OutputPath  string `json:"output_path"`
	// Strategy is "", "none", "remux" or "transcode" — empty until the file
	// has been probed, because the choice is the probe's, not the queue's.
	Strategy string `json:"strategy"`
	// ProfileID is the TV profile this conversion targets, recorded at queue
	// time so a later profile change does not rewrite history.
	ProfileID string `json:"profile_id"`
	Status    string `json:"status"`
	Error     string `json:"error"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

func conversionDTO(c core.Conversion) conversionJSON {
	return conversionJSON{
		ID:          c.ID,
		MediaFileID: c.MediaFileID,
		SourcePath:  c.SourcePath,
		OutputPath:  c.OutputPath,
		Strategy:    c.Strategy,
		ProfileID:   c.ProfileID,
		Status:      c.Status,
		Error:       c.Error,
		CreatedAt:   jsonTime(c.CreatedAt),
		UpdatedAt:   jsonTime(c.UpdatedAt),
	}
}

// ffmpegAvailable is what GET /system/status reports and what every mutating
// convert endpoint gates on.
func (s *server) ffmpegAvailable() bool {
	return s.converter != nil && s.converter.Available()
}

// requireFFmpeg writes a 503 and returns false when ffmpeg is absent.
func (s *server) requireFFmpeg(w http.ResponseWriter) bool {
	if s.ffmpegAvailable() {
		return true
	}
	writeError(w, http.StatusServiceUnavailable, "ffmpeg is not installed")
	return false
}

// handleListConversions returns the convert queue, newest first.
//
// It is readable without ffmpeg on purpose: uninstalling ffmpeg must not erase
// the record of what it did while it was there.
func (s *server) handleListConversions(w http.ResponseWriter, r *http.Request) {
	limit := defaultConversionLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, maxConversionLimit)
	}

	rows, err := s.st.ListConversions(r.Context(), limit)
	if err != nil {
		s.writeStoreError(w, "list conversions", err)
		return
	}
	out := make([]conversionJSON, 0, len(rows))
	for _, c := range rows {
		out = append(out, conversionDTO(c))
	}
	writeJSON(w, http.StatusOK, map[string]any{"conversions": out})
}

// convertRequest is the body of POST /convert.
type convertRequest struct {
	MediaFileID int64 `json:"media_file_id"`
}

// handleCreateConversion queues one library file for conversion.
//
// It deliberately does not decide remux-versus-transcode here: that needs a
// probe, which needs to read the whole file header, which is not something an
// HTTP handler should do while the user waits. The job decides.
func (s *server) handleCreateConversion(w http.ResponseWriter, r *http.Request) {
	if !s.requireFFmpeg(w) {
		return
	}
	var body convertRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.MediaFileID <= 0 {
		writeError(w, http.StatusBadRequest, "media_file_id is required")
		return
	}

	file, err := s.st.GetMediaFile(r.Context(), body.MediaFileID)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if err != nil {
		s.writeStoreError(w, "get media file", err)
		return
	}

	conv := &core.Conversion{
		MediaFileID: file.ID,
		SourcePath:  file.Path,
		ProfileID:   s.activeTVProfile(r.Context()).ID,
		Status:      core.ConversionQueued,
	}
	if err := s.st.CreateConversion(r.Context(), conv); err != nil {
		if errors.Is(err, store.ErrConversionOpen) {
			writeError(w, http.StatusConflict, "this file already has a conversion in the queue")
			return
		}
		s.writeStoreError(w, "queue conversion", err)
		return
	}
	if !s.enqueueConversionJob(w, r, conv) {
		return
	}
	writeJSON(w, http.StatusCreated, conversionDTO(*conv))
}

// handleCancelConversion drops a conversion that has not started.
//
// A running conversion is not cancellable in v1: killing ffmpeg mid-write is
// safe for the library (the original is untouched until the output verifies)
// but the plumbing to reach the running process is not worth it while the
// queue is single-worker. Saying so plainly beats a button that lies.
func (s *server) handleCancelConversion(w http.ResponseWriter, r *http.Request) {
	conv, ok := s.conversionByPath(w, r)
	if !ok {
		return
	}
	switch conv.Status {
	case core.ConversionQueued:
	case core.ConversionRunning:
		writeError(w, http.StatusConflict, "this conversion is already running")
		return
	default:
		writeError(w, http.StatusConflict, "this conversion has already finished")
		return
	}

	// Conditional on the row still being queued: the worker may have claimed it
	// between the read above and this write, and a 200 that says "cancelled"
	// over a conversion that is about to rewrite the file is a lie.
	cancelled, err := s.st.TransitionConversion(r.Context(), conv.ID,
		core.ConversionCancelled, core.ConversionQueued)
	if err != nil {
		s.writeStoreError(w, "cancel conversion", err)
		return
	}
	if !cancelled {
		writeError(w, http.StatusConflict, "this conversion is already running")
		return
	}
	// Re-read rather than patch the local copy: the row carries the timestamp
	// the transition wrote, and the response must be what the queue now holds.
	conv, ok = s.conversionByPath(w, r)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, conversionDTO(*conv))
}

// handleRetryConversion re-queues a conversion that failed or was cancelled.
// The job it enqueues is a new one: the old one has exhausted its attempts,
// and reusing it would inherit that exhaustion.
func (s *server) handleRetryConversion(w http.ResponseWriter, r *http.Request) {
	if !s.requireFFmpeg(w) {
		return
	}
	conv, ok := s.conversionByPath(w, r)
	if !ok {
		return
	}
	if core.ConversionOpen(conv.Status) {
		writeError(w, http.StatusConflict, "this conversion is already in the queue")
		return
	}
	if conv.Status == core.ConversionDone {
		writeError(w, http.StatusConflict, "this conversion already succeeded")
		return
	}

	conv.Status = core.ConversionQueued
	conv.Error = ""
	conv.ProfileID = s.activeTVProfile(r.Context()).ID
	if err := s.st.UpdateConversion(r.Context(), conv); err != nil {
		if errors.Is(err, store.ErrConversionOpen) {
			writeError(w, http.StatusConflict, "this file already has a conversion in the queue")
			return
		}
		s.writeStoreError(w, "retry conversion", err)
		return
	}
	if !s.enqueueConversionJob(w, r, conv) {
		return
	}
	writeJSON(w, http.StatusOK, conversionDTO(*conv))
}

// enqueueConversionJob puts the durable job behind a queued conversion.
//
// A conversion nothing will ever run is worse than a visible failure, so an
// enqueue error marks the row failed on the way out rather than leaving it
// queued forever (SPEC §13).
func (s *server) enqueueConversionJob(w http.ResponseWriter, r *http.Request, conv *core.Conversion) bool {
	ctx := r.Context()
	payload, err := json.Marshal(convert.Payload{ConversionID: conv.ID})
	if err != nil {
		s.writeStoreError(w, "queue conversion", err)
		return false
	}
	job := &core.Job{Kind: convert.JobKind, Payload: string(payload)}
	if err := s.st.EnqueueJob(ctx, job); err != nil {
		conv.Status = core.ConversionFailed
		conv.Error = "the conversion could not be queued"
		_ = s.st.UpdateConversion(ctx, conv)
		s.writeStoreError(w, "queue conversion", err)
		return false
	}
	return true
}

// conversionByPath resolves {id}, writing the error response itself.
func (s *server) conversionByPath(w http.ResponseWriter, r *http.Request) (*core.Conversion, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	conv, err := s.st.GetConversion(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return nil, false
	}
	if err != nil {
		s.writeStoreError(w, "get conversion", err)
		return nil, false
	}
	return conv, true
}
