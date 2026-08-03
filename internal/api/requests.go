package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// requestJSON is one row of the requests screen. PosterPath is the provider
// path the row stores; PosterURL is it rendered, and is empty when no metadata
// provider is configured — listing requests must not depend on one.
type requestJSON struct {
	ID         int64  `json:"id"`
	MediaType  string `json:"media_type"`
	TMDBID     int64  `json:"tmdb_id"`
	Title      string `json:"title"`
	Year       int    `json:"year"`
	PosterPath string `json:"poster_path"`
	PosterURL  string `json:"poster_url"`
	// Seasons are the requested season numbers. Null means the whole title:
	// every movie request, and a series request for all seasons.
	Seasons []int `json:"seasons"`
	// MinAvailability is the release stage the asker wants a movie held for,
	// empty when unspecified (and always for a series).
	MinAvailability string `json:"min_availability"`
	Status          string `json:"status"`
	CreatedAt       string `json:"created_at"`
	UpdatedAt       string `json:"updated_at"`
}

// requestCreateRequest is the body of POST /requests. Seasons is optional and
// series-only; omitting it asks for the whole title.
type requestCreateRequest struct {
	MediaType  string `json:"media_type"`
	TMDBID     int64  `json:"tmdb_id"`
	Title      string `json:"title"`
	Year       int    `json:"year"`
	PosterPath string `json:"poster_path"`
	Seasons    []int  `json:"seasons"`
	// MinAvailability is movie-only, the way Seasons is series-only: the
	// release stage the asker wants the movie held for. Optional.
	MinAvailability string `json:"min_availability"`
}

// approveRequestRequest is the body of POST /requests/{id}/approve. It mirrors
// the add endpoints, which take a search flag and a season selection and
// nothing else: a quality profile and a root folder are not part of the add
// contract today, so this endpoint does not pretend to accept them.
type approveRequestRequest struct {
	SearchNow bool `json:"search_now"`
	// Seasons narrows a series approval to those seasons, exactly as it does on
	// POST /library/series. A request that asked for more than was granted
	// stays pending for the remainder rather than being closed.
	Seasons []int `json:"seasons"`
	// MinAvailability overrides the release stage the request asked a movie to
	// be held for. Empty honours the request's own choice.
	MinAvailability string `json:"min_availability"`
}

// handleCreateRequest records a wish for a title that is not in the library.
//
// A second request for the same title merges into the pending one, unioning
// the season lists — see store.CreateRequest. Requesting something already in
// the library is refused rather than merged: nothing would ever absorb the row,
// and it would sit pending forever.
func (s *server) handleCreateRequest(w http.ResponseWriter, r *http.Request) {
	var body requestCreateRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.MediaType != MediaTypeMovie && body.MediaType != MediaTypeSeries {
		writeError(w, http.StatusBadRequest, "media_type must be movie or series")
		return
	}
	if body.TMDBID <= 0 {
		writeError(w, http.StatusBadRequest, "tmdb_id is required")
		return
	}
	title := strings.TrimSpace(body.Title)
	if title == "" {
		writeError(w, http.StatusBadRequest, "title is required")
		return
	}
	if len(body.Seasons) > 0 && body.MediaType != MediaTypeSeries {
		writeError(w, http.StatusBadRequest, "seasons are only valid for a series")
		return
	}
	if !validSeasonNumbers(w, body.Seasons) {
		return
	}
	if body.MinAvailability != "" && body.MediaType != MediaTypeMovie {
		writeError(w, http.StatusBadRequest, "min_availability is only valid for a movie")
		return
	}
	if !validAvailability(w, body.MinAvailability) {
		return
	}

	ctx := r.Context()
	inLibrary, err := s.inLibrary(ctx, body.MediaType, body.TMDBID)
	if err != nil {
		s.writeStoreError(w, "read library state", err)
		return
	}
	if inLibrary {
		writeError(w, http.StatusConflict, "already in the library")
		return
	}

	req := core.Request{
		MediaType:       body.MediaType,
		TMDBID:          body.TMDBID,
		Title:           title,
		Year:            body.Year,
		PosterPath:      body.PosterPath,
		Seasons:         body.Seasons,
		MinAvailability: body.MinAvailability,
	}
	if err := s.st.CreateRequest(ctx, &req); err != nil {
		s.writeStoreError(w, "create request", err)
		return
	}

	// Re-read so a merge answers with the merged row rather than what was
	// posted.
	stored, err := s.st.GetRequest(ctx, req.ID)
	if err != nil {
		s.writeStoreError(w, "get request", err)
		return
	}
	writeJSON(w, http.StatusCreated, s.requestDTO(*stored))
}

// handleListRequests lists requests newest first, optionally filtered by
// status.
func (s *server) handleListRequests(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	switch status {
	case "", core.RequestPending, core.RequestApproved, core.RequestDismissed:
	default:
		writeError(w, http.StatusBadRequest, "status must be pending, approved or dismissed")
		return
	}

	rows, err := s.st.ListRequests(r.Context(), status)
	if err != nil {
		s.writeStoreError(w, "list requests", err)
		return
	}

	out := make([]requestJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.requestDTO(row))
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": out})
}

// handleApproveRequest grants a request by adding its title to the library —
// the same path POST /library/movies and POST /library/series take, so an
// approved title is indistinguishable from a directly added one. The request
// is marked approved as a side effect of the add absorbing it.
func (s *server) handleApproveRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body approveRequestRequest
	if !decodeJSON(w, r, &body) {
		return
	}

	if !validSeasonNumbers(w, body.Seasons) {
		return
	}

	ctx := r.Context()
	req, ok := s.pendingRequest(ctx, w, id)
	if !ok {
		return
	}
	if req.MediaType == MediaTypeMovie && len(body.Seasons) > 0 {
		writeError(w, http.StatusBadRequest, "seasons are only valid for a series")
		return
	}
	if req.MediaType != MediaTypeMovie && body.MinAvailability != "" {
		writeError(w, http.StatusBadRequest, "min_availability is only valid for a movie")
		return
	}
	if !validAvailability(w, body.MinAvailability) {
		return
	}

	out := map[string]any{}
	if req.MediaType == MediaTypeMovie {
		// The approver's explicit choice beats the asker's, which beats the
		// default the add path fills in.
		minAvailability := body.MinAvailability
		if minAvailability == "" {
			minAvailability = req.MinAvailability
		}
		m, err := s.addMovieToLibrary(ctx, req.TMDBID, body.SearchNow, minAvailability)
		if err != nil {
			s.writeManagerError(w, "add movie", err)
			return
		}
		out["movie"] = movieDTO(*m)
	} else {
		sr, err := s.addSeriesToLibrary(ctx, req.TMDBID, body.SearchNow, body.Seasons)
		if err != nil {
			s.writeManagerError(w, "add series", err)
			return
		}
		out["series"] = seriesDTO(*sr)
	}

	approved, err := s.st.GetRequest(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get request", err)
		return
	}
	out["request"] = s.requestDTO(*approved)
	writeJSON(w, http.StatusOK, out)
}

// handleDismissRequest turns a request down. The row stays as history with a
// dismissed status, which is also what unblocks a fresh request for the same
// title later.
func (s *server) handleDismissRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, ok := s.pendingRequest(ctx, w, id); !ok {
		return
	}
	if err := s.st.SetRequestStatus(ctx, id, core.RequestDismissed); err != nil {
		s.writeStoreError(w, "dismiss request", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// pendingRequest loads a request and insists it is still pending. Approving or
// dismissing a request that has already been decided is a stale client, and
// answering 409 tells it to reload instead of silently re-adding a title.
func (s *server) pendingRequest(ctx context.Context, w http.ResponseWriter, id int64) (*core.Request, bool) {
	req, err := s.st.GetRequest(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get request", err)
		return nil, false
	}
	if req.Status != core.RequestPending {
		writeError(w, http.StatusConflict, "request is not pending")
		return nil, false
	}
	return req, true
}

// validSeasonNumbers rejects a season list containing a negative number,
// writing the failure itself. Season 0 is legal — that is where specials live.
func validSeasonNumbers(w http.ResponseWriter, seasons []int) bool {
	for _, n := range seasons {
		if n < 0 {
			writeError(w, http.StatusBadRequest, "season numbers must not be negative")
			return false
		}
	}
	return true
}

// inLibrary reports whether a provider id is already tracked.
func (s *server) inLibrary(ctx context.Context, mediaType string, tmdbID int64) (bool, error) {
	var (
		found map[int64]int64
		err   error
	)
	if mediaType == MediaTypeSeries {
		found, err = s.st.SeriesIDsByTMDBID(ctx, []int64{tmdbID})
	} else {
		found, err = s.st.MovieIDsByTMDBID(ctx, []int64{tmdbID})
	}
	if err != nil {
		return false, err
	}
	return found[tmdbID] != 0, nil
}

func (s *server) requestDTO(r core.Request) requestJSON {
	return requestJSON{
		ID:              r.ID,
		MediaType:       r.MediaType,
		TMDBID:          r.TMDBID,
		Title:           r.Title,
		Year:            r.Year,
		PosterPath:      r.PosterPath,
		PosterURL:       s.providerPosterURL(r.PosterPath),
		Seasons:         r.Seasons,
		MinAvailability: r.MinAvailability,
		Status:          r.Status,
		CreatedAt:       jsonTime(r.CreatedAt),
		UpdatedAt:       jsonTime(r.UpdatedAt),
	}
}

// providerPosterURL renders a stored provider poster path. A missing provider
// costs the artwork and nothing else: the requests screen still lists.
func (s *server) providerPosterURL(path string) string {
	if path == "" {
		return ""
	}
	provider, ok := s.mgr.Metadata().(core.DiscoverProvider)
	if !ok {
		return ""
	}
	return provider.PosterURL(path)
}
