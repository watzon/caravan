package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/stash"
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

	ownership, err := s.ownershipFilter(r)
	if err != nil {
		s.writeStoreError(w, "read library access", err)
		return
	}
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

// libraryOwnershipFilter is the shared Queue and History ownership policy for
// one request: which LIBRARY a row belongs to, and whether the caller has it.
//
// The owner of a row is resolved through the movie or series it names, then
// cached, because a page commonly holds several events or downloads for the
// same item. An item whose linked row is gone is an orphan, not evidence of
// anything — ownership that cannot be established is not ownership — so it is
// preserved. Unexpected store failures abort the response rather than leaking a
// row whose owner could not be checked.
type libraryOwnershipFilter struct {
	server *server
	gate   *libraryGate
	// seesAll short-circuits every lookup below when nothing is hidden from
	// this caller, which is what keeps the queue and history pages costing
	// exactly what they cost before libraries could be somebody else's.
	seesAll bool
	// seesAdult answers for rows whose provenance is adult without naming an
	// item at all (see eventVisible).
	seesAdult bool

	seriesVisible map[int64]bool
	movieVisible  map[int64]bool
}

// ownershipFilter resolves the two per-request answers the filter is built on.
func (s *server) ownershipFilter(r *http.Request) (libraryOwnershipFilter, error) {
	gate := s.gate(r)
	seesAll, err := gate.seesAll(r.Context())
	if err != nil {
		return libraryOwnershipFilter{}, err
	}
	seesAdult, err := gate.seesAdult(r.Context())
	if err != nil {
		return libraryOwnershipFilter{}, err
	}
	return libraryOwnershipFilter{server: s, gate: gate, seesAll: seesAll, seesAdult: seesAdult}, nil
}

// libraryVisibleTo reports whether a row owned only by a LIBRARY — an untied
// universal-search grab, or the file it parked — may be shown.
func (f *libraryOwnershipFilter) libraryVisibleTo(ctx context.Context, libraryID int64) (bool, error) {
	if f.seesAll {
		return true, nil
	}
	return f.gate.visible(ctx, libraryID)
}

// ownerVisible resolves the library behind a row's item and asks the gate for
// it. BOTH ids are followed: a movie library is as restrictable as any other,
// so "a movie cannot be adult" is no longer a reason to wave one through.
func (f *libraryOwnershipFilter) ownerVisible(ctx context.Context, movieID, seriesID int64) (bool, error) {
	if f.seesAll {
		return true, nil
	}
	if seriesID != 0 {
		return f.seriesVisibleTo(ctx, seriesID)
	}
	if movieID != 0 {
		return f.movieVisibleTo(ctx, movieID)
	}
	// No ownership ids at all: the row names nothing to check.
	return true, nil
}

func (f *libraryOwnershipFilter) seriesVisibleTo(ctx context.Context, seriesID int64) (bool, error) {
	if visible, ok := f.seriesVisible[seriesID]; ok {
		return visible, nil
	}
	visible := true
	sr, err := f.server.st.GetSeries(ctx, seriesID)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return false, err
	default:
		visible, err = f.gate.visible(ctx, sr.LibraryID)
		if err != nil {
			return false, err
		}
	}
	if f.seriesVisible == nil {
		f.seriesVisible = make(map[int64]bool)
	}
	f.seriesVisible[seriesID] = visible
	return visible, nil
}

func (f *libraryOwnershipFilter) movieVisibleTo(ctx context.Context, movieID int64) (bool, error) {
	if visible, ok := f.movieVisible[movieID]; ok {
		return visible, nil
	}
	visible := true
	m, err := f.server.st.GetMovie(ctx, movieID)
	switch {
	case errors.Is(err, store.ErrNotFound):
	case err != nil:
		return false, err
	default:
		visible, err = f.gate.visible(ctx, m.LibraryID)
		if err != nil {
			return false, err
		}
	}
	if f.movieVisible == nil {
		f.movieVisible = make(map[int64]bool)
	}
	f.movieVisible[movieID] = visible
	return visible, nil
}

// eventVisible applies intrinsic event provenance before ownership IDs. Adult-
// only and Stash rows remain adult even without IDs; their detail may contain
// scene paths or handoff failures whose episode can no longer be resolved.
func (f *libraryOwnershipFilter) eventVisible(ctx context.Context, event core.Event) (bool, error) {
	if !f.seesAdult &&
		(event.Category == core.EventCategoryAdultOnly || event.Category == stash.EventCategory) {
		return false, nil
	}
	return f.ownerVisible(ctx, event.MovieID, event.SeriesID)
}

// listEvents scans store pages until it has limit visible rows. Filtering only
// after a single page would let hidden adult rows consume the limit and make
// older, visible history disappear.
func (f *libraryOwnershipFilter) listEvents(ctx context.Context, limit int, beforeID int64) ([]core.Event, int64, error) {
	out := make([]core.Event, 0, limit)
	nextID := beforeID
	for len(out) < limit {
		events, next, err := f.server.st.ListEventsPage(ctx, int64(limit-len(out)), nextID)
		if err != nil {
			return nil, 0, err
		}
		for _, event := range events {
			visible, err := f.eventVisible(ctx, event)
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
