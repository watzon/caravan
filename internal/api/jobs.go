package api

import (
	"encoding/json"
	"net/http"
	"strconv"
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
	limit := defaultJobLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, maxJobLimit)
	}

	jobs, err := s.st.ListJobs(r.Context(), limit)
	if err != nil {
		s.writeStoreError(w, "list jobs", err)
		return
	}

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
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}
