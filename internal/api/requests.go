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
	// RequestedByUsername is who asked, empty when the row records no account:
	// one made before accounts existed, one made while the server ran open, or
	// one whose asker has since been deleted. The screen renders all three the
	// same way, because from the row's side they are the same thing.
	RequestedByUsername string `json:"requested_by_username"`
	CreatedAt           string `json:"created_at"`
	UpdatedAt           string `json:"updated_at"`
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
//
// The row records the calling account, which is zero on an open server or for
// the API key. A merge keeps the first asker, so the row the answer describes
// can be somebody else's — which is why the name on it is filtered by
// visibleRequester rather than reported flat.
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
		RequestedBy:     currentUser(r).ID,
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
	names, err := s.requesterNames(ctx, []core.Request{*stored})
	if err != nil {
		s.writeStoreError(w, "read usernames", err)
		return
	}
	writeJSON(w, http.StatusCreated, s.requestDTO(*stored, s.visibleRequester(r, *stored, names)))
}

// visibleRequester is the asker's name as this caller may know it.
//
// A member sees only their own rows, and a merge is the one place that rule
// could be walked around: POST /requests answers with the row that now exists,
// which on a merge is a housemate's. Handing the name back would turn the
// endpoint into a lookup from a provider id to whoever asked for it — the
// roster of accounts a failed login goes out of its way not to confirm.
func (s *server) visibleRequester(r *http.Request, row core.Request, names map[int64]string) string {
	if user := currentUser(r); user.Role == core.RoleMember && row.RequestedBy != user.ID {
		return ""
	}
	return names[row.RequestedBy]
}

// handleListRequests lists requests newest first, optionally filtered by
// status.
//
// A member sees only their own rows, in every status: this is their screen for
// watching a wish go from pending to approved, not a window onto the household.
// An admin sees everybody's, which is what makes the screen a queue to work
// through, and each row names who asked.
func (s *server) handleListRequests(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	switch status {
	case "", core.RequestPending, core.RequestApproved, core.RequestDismissed:
	default:
		writeError(w, http.StatusBadRequest, "status must be pending, approved or dismissed")
		return
	}

	ctx := r.Context()
	var (
		rows []core.Request
		err  error
	)
	if user := currentUser(r); user.Role == core.RoleMember {
		rows, err = s.st.ListRequestsBy(ctx, user.ID, status)
	} else {
		rows, err = s.st.ListRequests(ctx, status)
	}
	if err != nil {
		s.writeStoreError(w, "list requests", err)
		return
	}
	names, err := s.requesterNames(ctx, rows)
	if err != nil {
		s.writeStoreError(w, "read usernames", err)
		return
	}

	out := make([]requestJSON, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.requestDTO(row, names[row.RequestedBy]))
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": out})
}

// handleApproveRequest grants a request by adding its title to the library —
// the same path POST /library/movies and POST /library/series take, so an
// approved title is indistinguishable from a directly added one. The request
// is marked approved as a side effect of the add absorbing it.
//
// Admin-only, and enforced at the gate rather than here (memberAllowed): a
// member who could approve their own request would be an admin wearing a
// smaller badge.
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
	req, ok := s.loadRequest(ctx, w, id)
	if !ok || !insistPending(w, req) {
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
	names, err := s.requesterNames(ctx, []core.Request{*approved})
	if err != nil {
		s.writeStoreError(w, "read usernames", err)
		return
	}
	// Admin-only at the gate, so visibleRequester never withholds anything here
	// — it is used so that every single-row answer goes through one rule rather
	// than two that have to be kept in step.
	out["request"] = s.requestDTO(*approved, s.visibleRequester(r, *approved, names))
	writeJSON(w, http.StatusOK, out)
}

// handleDismissRequest turns a request down. The row stays as history with a
// dismissed status, which is also what unblocks a fresh request for the same
// title later.
//
// For an admin that is a decision about somebody else's wish; for a member it
// is cancelling their own, and it is the only row they may touch. To a member,
// a housemate's row and no row at all are the same 404 with the same body:
// ownership is checked before the pending check so "already decided" cannot
// confirm the row exists, and a distinct 403 here would let anyone with a
// member login walk the id space and map how much the household asks for.
func (s *server) handleDismissRequest(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	req, ok := s.loadRequest(ctx, w, id)
	if !ok {
		return
	}
	if user := currentUser(r); user.Role == core.RoleMember && req.RequestedBy != user.ID {
		// Byte-identical to loadRequest's answer for an id that was never
		// issued, deliberately.
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if !insistPending(w, req) {
		return
	}
	if err := s.st.SetRequestStatus(ctx, id, core.RequestDismissed); err != nil {
		s.writeStoreError(w, "dismiss request", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// loadRequest reads one request, writing the failure itself. An absent id is
// the store's ErrNotFound, which writeStoreError turns into a 404.
func (s *server) loadRequest(ctx context.Context, w http.ResponseWriter, id int64) (*core.Request, bool) {
	req, err := s.st.GetRequest(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get request", err)
		return nil, false
	}
	return req, true
}

// insistPending refuses a request that has already been decided. Approving or
// dismissing one twice is a stale client, and answering 409 tells it to reload
// instead of silently re-adding a title.
func insistPending(w http.ResponseWriter, req *core.Request) bool {
	if req.Status != core.RequestPending {
		writeError(w, http.StatusConflict, "request is not pending")
		return false
	}
	return true
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

// requestDTO renders one row. username is the asker's name, which the caller
// resolves with requesterNames — passing it in rather than looking it up here
// is what keeps a list of rows from becoming a query per row.
func (s *server) requestDTO(r core.Request, username string) requestJSON {
	return requestJSON{
		ID:                  r.ID,
		MediaType:           r.MediaType,
		TMDBID:              r.TMDBID,
		Title:               r.Title,
		Year:                r.Year,
		PosterPath:          r.PosterPath,
		PosterURL:           s.providerPosterURL(r.PosterPath),
		Seasons:             r.Seasons,
		MinAvailability:     r.MinAvailability,
		Status:              r.Status,
		RequestedByUsername: username,
		CreatedAt:           jsonTime(r.CreatedAt),
		UpdatedAt:           jsonTime(r.UpdatedAt),
	}
}

// requesterNames resolves the asker of every row in one query. Rows that record
// no account contribute no id, so a screen full of pre-accounts rows costs
// nothing at all.
func (s *server) requesterNames(ctx context.Context, rows []core.Request) (map[int64]string, error) {
	ids := make([]int64, 0, len(rows))
	for _, r := range rows {
		if r.RequestedBy != 0 {
			ids = append(ids, r.RequestedBy)
		}
	}
	return s.st.UsernamesByID(ctx, ids)
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
