package api

import "net/http"

type healthResponse struct {
	Status string `json:"status"`
}

// handleHealth reports whether the HTTP server can use its migrated SQLite
// database. It is unauthenticated so container health checks keep working
// after an administrator sets a password.
func (s *server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.st == nil {
		writeError(w, http.StatusServiceUnavailable, "database is not ready")
		return
	}
	if _, err := s.st.CountUsers(r.Context()); err != nil {
		s.log.Error("health check failed", "error", err)
		writeError(w, http.StatusServiceUnavailable, "database is not ready")
		return
	}

	writeJSON(w, http.StatusOK, healthResponse{Status: "ok"})
}
