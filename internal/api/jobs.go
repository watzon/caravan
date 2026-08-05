package api

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/watzon/caravan/internal/core"
)

const (
	defaultJobLimit = 100
	maxJobLimit     = 1000
)

type jobJSON struct {
	ID             int64           `json:"id"`
	Kind           string          `json:"kind"`
	Payload        json.RawMessage `json:"payload"`
	State          string          `json:"state"`
	Attempts       int             `json:"attempts"`
	RunAfter       string          `json:"run_after"`
	LeaseExpiresAt string          `json:"lease_expires_at"`
	LastError      string          `json:"last_error"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
}

// handleListJobs returns the durable job activity feed, newest first.
func (s *server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	_, hasLimit := query["limit"]
	rawCursor := query.Get("cursor")
	_, hasCursor := query["cursor"]
	cursorMode := hasLimit || hasCursor

	limit := defaultJobLimit
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, maxJobLimit)
	}

	if !cursorMode {
		jobs, err := s.st.ListJobs(r.Context(), limit)
		if err != nil {
			s.writeStoreError(w, "list jobs", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"jobs": jobJSONs(jobs)})
		return
	}

	var beforeID int64
	if rawCursor != "" {
		parsed, err := strconv.ParseInt(rawCursor, 10, 64)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "cursor must be a positive integer")
			return
		}
		beforeID = parsed
	}
	jobs, nextID, err := s.st.ListJobsPage(r.Context(), limit, beforeID)
	if err != nil {
		s.writeStoreError(w, "list job page", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":        jobJSONs(jobs),
		"next_cursor": cursorString(nextID),
	})
}

func jobJSONs(jobs []core.Job) []jobJSON {
	out := make([]jobJSON, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, jobJSON{
			ID:             job.ID,
			Kind:           job.Kind,
			Payload:        json.RawMessage(job.Payload),
			State:          job.State,
			Attempts:       job.Attempts,
			RunAfter:       jsonTime(job.RunAfter),
			LeaseExpiresAt: jsonTime(job.LeaseExpiresAt),
			LastError:      job.LastError,
			CreatedAt:      jsonTime(job.CreatedAt),
			UpdatedAt:      jsonTime(job.UpdatedAt),
		})
	}
	return out
}
