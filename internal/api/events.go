package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
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
	query := r.URL.Query()
	_, hasLimit := query["limit"]
	rawCursor := query.Get("cursor")
	_, hasCursor := query["cursor"]
	cursorMode := hasLimit || hasCursor

	limit := defaultEventLimit
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, maxEventLimit)
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

	adultVisible, err := s.adultVisible(r)
	if err != nil {
		s.writeStoreError(w, "read adult settings", err)
		return
	}
	ownership := adultOwnershipFilter{server: s, adultVisible: adultVisible}
	events, nextID, err := ownership.listEvents(r.Context(), limit, beforeID)
	if err != nil {
		s.writeStoreError(w, "resolve event ownership", err)
		return
	}

	response := map[string]any{"events": eventJSONs(events)}
	if cursorMode {
		response["next_cursor"] = cursorString(nextID)
	}
	writeJSON(w, http.StatusOK, response)
}

// adultOwnershipFilter is the shared Queue and History ownership policy for one
// request. Adult visibility is resolved once by adultVisible; series kind is
// then cached because a page commonly contains several events or downloads for
// the same series.
//
// MovieID establishes ordinary movie ownership. SeriesID needs a store lookup,
// because television and adult sites deliberately share the series table. A
// missing linked row is an orphan, not evidence that the row is adult, so it is
// preserved. Unexpected store failures abort the response rather than leaking a
// row whose ownership could not be checked.
type adultOwnershipFilter struct {
	server       *server
	adultVisible bool
	seriesAdult  map[int64]bool
}

func (f *adultOwnershipFilter) ownerVisible(ctx context.Context, movieID, seriesID int64) (bool, error) {
	if f.adultVisible {
		return true, nil
	}
	if seriesID == 0 {
		// A MovieID, or no ownership IDs at all, cannot identify an adult site.
		return true, nil
	}
	if adult, ok := f.seriesAdult[seriesID]; ok {
		return !adult, nil
	}
	series, err := f.server.st.GetSeries(ctx, seriesID)
	if errors.Is(err, store.ErrNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	adult := series.Kind == core.SeriesKindAdult
	if f.seriesAdult == nil {
		f.seriesAdult = make(map[int64]bool)
	}
	f.seriesAdult[seriesID] = adult
	return !adult, nil
}

// listEvents scans store pages until it has limit visible rows. Filtering only
// after a single page would let hidden adult rows consume the limit and make
// older, visible history disappear.
func (f *adultOwnershipFilter) listEvents(ctx context.Context, limit int, beforeID int64) ([]core.Event, int64, error) {
	out := make([]core.Event, 0, limit)
	nextID := beforeID
	for len(out) < limit {
		events, next, err := f.server.st.ListEventsPage(ctx, int64(limit-len(out)), nextID)
		if err != nil {
			return nil, 0, err
		}
		for _, event := range events {
			visible, err := f.ownerVisible(ctx, event.MovieID, event.SeriesID)
			if err != nil {
				return nil, 0, err
			}
			if visible {
				out = append(out, event)
			}
		}
		if next == 0 {
			return out, 0, nil
		}
		nextID = next
	}
	return out, nextID, nil
}

func eventJSONs(events []core.Event) []eventJSON {
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
	return out
}

func cursorString(id int64) string {
	if id == 0 {
		return ""
	}
	return strconv.FormatInt(id, 10)
}
