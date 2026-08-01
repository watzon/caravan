package api

import "net/http"

// handleListJobs is the activity-feed half of the job queue (PLAN phase 3,
// task 8). The scheduler slice replaces this stub with the real handler.
func (s *server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "jobs feed not implemented yet")
}
