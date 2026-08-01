package api

import "net/http"

// handleCalendar is the combined movie/episode calendar (PLAN phase 3,
// task 9). The calendar slice replaces this stub with the real handler.
func (s *server) handleCalendar(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "calendar not implemented yet")
}

// handleCalendarICS is the iCal feed for external calendar apps (PLAN phase 3,
// task 9). The calendar slice replaces this stub with the real handler.
func (s *server) handleCalendarICS(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "calendar feed not implemented yet")
}

// handleGenerateAPIKey (re)generates the API key the iCal feed authenticates
// with (PLAN phase 3, task 9). The calendar slice replaces this stub.
func (s *server) handleGenerateAPIKey(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "api key generation not implemented yet")
}
