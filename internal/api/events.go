package api

import (
	"net/http"
	"strconv"
)

// Bounds on GET /events?limit=. The feed is a UI convenience, not an export
// format, so it always has a ceiling.
const (
	defaultEventLimit = 100
	maxEventLimit     = 1000
)

type eventJSON struct {
	ID        int64  `json:"id"`
	Level     string `json:"level"`
	Category  string `json:"category"`
	Message   string `json:"message"`
	Detail    string `json:"detail"`
	MovieID   int64  `json:"movie_id"`
	SeriesID  int64  `json:"series_id"`
	CreatedAt string `json:"created_at"`
}

// handleEvents returns the activity feed, newest first.
func (s *server) handleEvents(w http.ResponseWriter, r *http.Request) {
	limit := defaultEventLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, maxEventLimit)
	}

	events, err := s.st.ListEvents(r.Context(), limit)
	if err != nil {
		s.writeStoreError(w, "list events", err)
		return
	}

	out := make([]eventJSON, 0, len(events))
	for _, e := range events {
		out = append(out, eventJSON{
			ID:        e.ID,
			Level:     e.Level,
			Category:  e.Category,
			Message:   e.Message,
			Detail:    e.Detail,
			MovieID:   e.MovieID,
			SeriesID:  e.SeriesID,
			CreatedAt: jsonTime(e.CreatedAt),
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": out})
}
