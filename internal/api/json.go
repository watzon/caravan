package api

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/watzon/caravan/internal/store"
)

const contentTypeJSON = "application/json; charset=utf-8"

// maxBodyBytes caps request bodies. Every phase-1 request body is a handful of
// fields; anything larger is a client bug or an attack.
const maxBodyBytes = 1 << 20

// errorResponse is the failure envelope every error reply uses (SPEC §11).
type errorResponse struct {
	Error string `json:"error"`
	// Code is a stable machine-readable tag for the failures a client has to
	// branch on rather than merely display, today, the credential states in
	// credentials.go. It is omitted from every other failure: an absent code
	// means "render the message", which is what the SPA has always done and
	// must keep doing for errors nobody has had a reason to name.
	Code string `json:"code,omitempty"`
}

// writeJSON sends v with the given status. An encoding failure is logged as
// part of the response being truncated; the status line is already gone by
// then, so there is nothing better to do.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	if v == nil {
		return
	}
	_ = json.NewEncoder(w).Encode(v)
}

// writeError sends the error envelope.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, errorResponse{Error: msg})
}

// writeCodedError sends the error envelope with a machine-readable code beside
// the message, for the failures a client changes its screen over instead of
// showing a toast.
func writeCodedError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, errorResponse{Error: msg, Code: code})
}

// writeStoreError maps a store failure to a status: a missing row is a 404,
// anything else is a 500 whose detail stays in the log, not in the response.
func (s *server) writeStoreError(w http.ResponseWriter, msg string, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.log.Error(msg, "error", err)
	writeError(w, http.StatusInternalServerError, msg)
}

// decodeJSON reads a JSON request body into dst. It writes a 400 and returns
// false when the body is missing or malformed.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	// See formContentTypes: a body sent under an HTML-form encoding is never a
	// legitimate API call and is how a cross-site form smuggles JSON in.
	if formEncoded(r) {
		writeError(w, http.StatusUnsupportedMediaType, "request body must be JSON")
		return false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err := dec.Decode(dst); err != nil {
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "request body is required")
		} else {
			writeError(w, http.StatusBadRequest, "invalid JSON body")
		}
		return false
	}
	return true
}

// pathID reads the {id} path value. It writes a 400 and returns false when the
// segment is not a positive integer.
func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}
	return id, true
}

// jsonDate renders just the day of a timestamp, for air dates and release
// dates where the time of day is noise. The zero time becomes the empty
// string.
func jsonDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format("2006-01-02")
}

// jsonTime renders a timestamp for the API. The zero time becomes the empty
// string, matching how the store distinguishes "unset" from "the epoch".
func jsonTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
