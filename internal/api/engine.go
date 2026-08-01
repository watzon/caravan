package api

import "net/http"

// handleDownloadInsight returns per-download detail (peers, trackers,
// availability) for the queue detail drawer (PLAN phase 3, task 10). The
// engine-controls slice replaces this stub with the real handler.
func (s *server) handleDownloadInsight(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "download insight not implemented yet")
}

// handleSetDownloadLimits applies per-torrent rate limits (PLAN phase 3,
// task 10). The engine-controls slice replaces this stub.
func (s *server) handleSetDownloadLimits(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "download limits not implemented yet")
}
