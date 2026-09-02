package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// requestJSON is one row of the requests screen. PosterPath is the provider
// path the row stores; PosterURL is it rendered, and is empty when no metadata
// provider is configured, listing requests must not depend on one.
type requestJSON struct {
	ID        int64  `json:"id"`
	MediaType string `json:"media_type"`
	TMDBID    int64  `json:"tmdb_id"`
	// StashID identifies a scene request and is empty on the other two kinds;
	// TMDBID is zero on a scene one. See core.Request for why they are two
	// fields rather than one "provider id".
	StashID    string `json:"stash_id"`
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
	MediaType string `json:"media_type"`
	TMDBID    int64  `json:"tmdb_id"`
	// StashID names a scene, and is the only id a scene request carries.
	StashID string `json:"stash_id"`
	// Provider names the stash-box instance the scene was browsed on. It is
	// accepted and validated, and then deliberately dropped: requests carry no
	// provider column, so there is nowhere truthful to put it and approval reads
	// the default instance (see approveScene). It is here so a client can start
	// sending what it knows before the column exists.
	Provider   string `json:"provider"`
	Title      string `json:"title"`
	Year       int    `json:"year"`
	PosterPath string `json:"poster_path"`
	Seasons    []int  `json:"seasons"`
	// MinAvailability is movie-only, the way Seasons is series-only: the
	// release stage the asker wants the movie held for. Optional.
	MinAvailability string `json:"min_availability"`
	// Approve is admin-only: the request is recorded and granted in the one
	// call, so an admin asking for something themselves never visits the
	// requests queue for their own wish. The answer is the approve endpoint's
	// shape, and the grant uses the request's own seasons and availability with
	// server defaults. No profile override, no immediate search.
	Approve bool `json:"approve"`
}

// approveRequestRequest is the body of POST /requests/{id}/approve. It mirrors
// the direct add endpoints: zero quality_profile_id leaves the item to inherit
// its library or system default.
//
// Monitored and SearchNow mean something for every kind. Monitoring defaults
// false for all additions. A scene approval still monitors the one asked-for
// episode while its new parent site stays unmonitored (see approveScene);
// search_now queues a search for that episode rather than for a whole title.
type approveRequestRequest struct {
	SearchNow        bool  `json:"search_now"`
	Monitored        *bool `json:"monitored"`
	QualityProfileID int64 `json:"quality_profile_id"`
	// POST /library/series. A request that asked for more than was granted
	// stays pending for the remainder rather than being closed.
	Seasons []int `json:"seasons"`
	// MinAvailability overrides the release stage the request asked a movie to
	// be held for. Empty honours the request's own choice.
	MinAvailability string `json:"min_availability"`
}

// handleCreateRequest records a wish for a title that is not in the library.
//
// A second request for the same title merges into the pending one, unioning the
// season lists. See store.CreateRequest. Requesting something already in the
// library is refused rather than merged: nothing would ever absorb the row, and
// it would sit pending forever.
//
// `approve` turns the call into request-and-approve (Overseerr's admin flow):
// the row is recorded and then granted in the same breath, with the ask's own
// seasons and availability and server defaults for everything the approve
// endpoint would let the approver choose. It is refused to a member for the
// reason the approve route is: approving is the admin's decision, and a create
// that carries it is the same decision through a different door.
//
// The row records the calling account, which is zero on an open server or for
// the API key. A merge keeps the first asker, so the row the answer describes
// can be somebody else's, which is why the name on it is filtered by
// visibleRequester rather than reported flat.
func (s *server) handleCreateRequest(w http.ResponseWriter, r *http.Request) {
	var body requestCreateRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Approve && currentUser(r).Role != core.RoleAdmin {
		writeError(w, http.StatusForbidden, "admins only")
		return
	}

	adult, err := s.gate(r).seesAdult(r.Context())
	if err != nil {
		s.writeStoreError(w, "read library access", err)
		return
	}
	if !validRequestMediaType(w, body.MediaType, adult) {
		return
	}

	if body.MediaType == MediaTypeScene {
		if strings.TrimSpace(body.StashID) == "" {
			writeError(w, http.StatusBadRequest, "stash_id is required")
			return
		}
		if body.TMDBID != 0 {
			writeError(w, http.StatusBadRequest, "tmdb_id is not valid for a scene")
			return
		}
		// Validated and then dropped; see requestCreateRequest.Provider. It is
		// checked rather than ignored so a client cannot start sending an id
		// nobody ever verifies and have it silently become load-bearing when
		// the column arrives.
		if strings.TrimSpace(body.Provider) != "" &&
			!s.validAdultInstance(r.Context(), w, strings.TrimSpace(body.Provider)) {
			return
		}
	} else {
		if body.Provider != "" {
			writeError(w, http.StatusBadRequest, "provider is only valid for a scene")
			return
		}
		if body.StashID != "" {
			writeError(w, http.StatusBadRequest, "stash_id is only valid for a scene")
			return
		}
		if body.TMDBID <= 0 {
			writeError(w, http.StatusBadRequest, "tmdb_id is required")
			return
		}
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
	inLibrary, err := s.inLibrary(
		ctx, body.MediaType, body.TMDBID, body.StashID, strings.TrimSpace(body.Provider),
	)
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
		StashID:         strings.TrimSpace(body.StashID),
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
	if body.Approve {
		// The grant takes the ask verbatim: the (possibly merged) season list
		// and availability on the row, and no opinion on anything else. The
		// admin choosing a profile or an immediate search is what the approve
		// endpoint and the add modal are for.
		out, ok := s.approveRequest(ctx, w, r, stored, approveRequestRequest{
			Seasons:         stored.Seasons,
			MinAvailability: stored.MinAvailability,
		})
		if !ok {
			return
		}
		writeJSON(w, http.StatusCreated, out)
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
// endpoint into a lookup from a provider id to whoever asked for it. The roster
// of accounts a failed login goes out of its way not to confirm.
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
	// The requests screen is a shared surface: one table holds every kind, so
	// it is the list that has to filter rather than the router. A caller the
	// adult module is not visible to sees the requests screen they saw before
	// it existed, including an admin on a server with the module switched off,
	// whose own approved scene requests go quiet rather than reappearing as
	// evidence of a module they turned off.
	adult, err := s.gate(r).seesAdult(ctx)
	if err != nil {
		s.writeStoreError(w, "read library access", err)
		return
	}
	if !adult {
		kept := rows[:0]
		for _, row := range rows {
			if row.MediaType != MediaTypeScene {
				kept = append(kept, row)
			}
		}
		rows = kept
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

// handleApproveRequest grants a request by adding its title to the library. The
// same path POST /library/movies and POST /library/series take, so an approved
// title is indistinguishable from a directly added one. The request is marked
// approved as a side effect of the add absorbing it.
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
	req, ok := s.loadRequest(w, r, id)
	if !ok || !insistPending(w, req) {
		return
	}
	if req.MediaType != MediaTypeSeries && len(body.Seasons) > 0 {
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
	if req.MediaType != MediaTypeScene && !s.validQualityProfileID(w, r, body.QualityProfileID) {
		return
	}

	out, ok := s.approveRequest(ctx, w, r, req, body)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// approveRequest grants a pending request: the title enters the library through
// the same path the add endpoints take, so an approved title is
// indistinguishable from a directly added one, and the add absorbs the row,
// except a scene, which has no tmdb id for the absorber to key on and is closed
// explicitly in approveScene. It writes its own failures; ok is false once it
// has, and the caller stops.
//
// Both doors to a grant come here: the approve route, and request-and-approve
// off the create route. Validation against the row's media type is the
// caller's, because the create route's row is valid by construction.
func (s *server) approveRequest(ctx context.Context, w http.ResponseWriter, r *http.Request, req *core.Request, body approveRequestRequest) (map[string]any, bool) {
	out := map[string]any{}
	switch {
	case req.MediaType == MediaTypeScene:
		sr, queued, err := s.approveScene(ctx, w, r, req, body)
		if err != nil {
			return nil, false
		}
		out["site"] = siteDTO(*sr)
		out["search_queued"] = queued
	case req.MediaType == MediaTypeMovie:
		// The approver's explicit choices beat the asker's stored availability.
		// Monitoring is safe by default: an omitted choice leaves the new title
		// unmonitored, while an explicit true starts automation.
		minAvailability := body.MinAvailability
		if minAvailability == "" {
			minAvailability = req.MinAvailability
		}
		m, err := s.addMovieToLibrary(ctx, core.TMDBRef(req.TMDBID), body.SearchNow, minAvailability, body.Monitored, body.QualityProfileID, 0)
		if err != nil {
			s.writeManagerError(w, core.ProviderTMDB, "add movie", err)
			return nil, false
		}
		out["movie"] = movieDTO(*m)
	default:
		sr, err := s.addSeriesToLibrary(ctx, core.TMDBRef(req.TMDBID), body.SearchNow, body.Seasons, body.Monitored, body.QualityProfileID, 0)
		if err != nil {
			s.writeManagerError(w, core.ProviderTMDB, "add series", err)
			return nil, false
		}
		out["series"] = seriesDTO(*sr)
	}

	approved, err := s.st.GetRequest(ctx, req.ID)
	if err != nil {
		s.writeStoreError(w, "get request", err)
		return nil, false
	}
	names, err := s.requesterNames(ctx, []core.Request{*approved})
	if err != nil {
		s.writeStoreError(w, "read usernames", err)
		return nil, false
	}
	// Admin-only at the gate, so visibleRequester never withholds anything
	// here. It is used so that every single-row answer goes through one rule
	// rather than two that have to be kept in step.
	out["request"] = s.requestDTO(*approved, s.visibleRequester(r, *approved, names))
	return out, true
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
	req, ok := s.loadRequest(w, r, id)
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
//
// A scene row is invisible to a caller the adult module is not visible to, and
// the refusal is byte-identical to the one an id that was never issued gets. It
// belongs here rather than in each caller because approve and dismiss are the
// only two doors to a row by id, and a filter one of them forgot would be a
// silent way to walk the id space for scene requests.
func (s *server) loadRequest(w http.ResponseWriter, r *http.Request, id int64) (*core.Request, bool) {
	req, err := s.st.GetRequest(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get request", err)
		return nil, false
	}
	if req.MediaType == MediaTypeScene {
		adult, err := s.gate(r).seesAdult(r.Context())
		if err != nil {
			s.writeStoreError(w, "read library access", err)
			return nil, false
		}
		if !adult {
			writeError(w, http.StatusNotFound, "not found")
			return nil, false
		}
	}
	return req, true
}

// approveScene grants a scene request by adding the site that released it.
//
// A scene is an episode, and an episode cannot exist without its series: the
// site's catalogue is what numbers the scene (release year to season, sequence
// within the year to episode number), so there is no way to add one scene on
// its own. Adding the site is therefore not an over-delivery, it is the only
// shape the request has. What would be an over-delivery is monitoring it: one
// granted scene is not a standing order for everything the studio releases, so
// the site lands unmonitored and only the asked-for scene's episode is
// monitored. The wanted list reads the episode flag (library writeScenes), so
// that one flip is what turns "approved" into "hunted for". An explicit
// monitored:true on the body keeps the old whole-site behaviour.
//
// The site is looked up through the scene rather than stored on the request:
// the row records what the asker chose, which is a scene, and deriving the site
// at approval time means a scene re-filed under a different studio upstream
// lands under the studio it actually belongs to now.
//
// It writes its own failures and returns the error only so the caller can stop.
// The bool reports whether a search was queued for the scene, so the answer can
// say rather than have the approver guess.
func (s *server) approveScene(ctx context.Context, w http.ResponseWriter, r *http.Request, req *core.Request, body approveRequestRequest) (*core.Series, bool, error) {
	// The DEFAULT instance: a request row carries no provider column, so there
	// is no instance on it to honour. Scene requests made before instances
	// existed are the reason it cannot simply grow one. The field would be
	// empty on every historical row, and empty is not a box. The approval
	// therefore reads whichever catalogue the default library identifies
	// through, which is the box the scene was almost certainly browsed on.
	provider, providerID := s.mgr.DefaultAdultMetadata(ctx)
	if provider == nil {
		// Coded, and coded as the adult credential: an uncoded 503 is read by
		// the SPA as a missing TMDB key (web/src/lib/credentials.ts), so
		// approving a scene with no stash-box provider would send the admin to
		// the wrong settings screen to fix the wrong credential.
		writeCodedError(w, http.StatusServiceUnavailable, CodeAdultCredentialAbsent,
			"no adult metadata provider configured")
		return nil, false, core.ErrNoAdultProvider
	}
	scene, err := provider.GetScene(ctx, req.StashID)
	if err != nil {
		s.writeAdultProviderError(w, r, "get scene", err)
		return nil, false, err
	}
	if scene == nil || scene.SiteStashID == "" {
		// The provider answered, but with a scene it cannot name a studio for.
		// Approving would have nowhere to put it.
		s.writeAdultProviderError(w, r, "get scene", errNoSceneSite)
		return nil, false, errNoSceneSite
	}

	// AddSiteAndWait, not AddSite: the scenes have to exist by the time this
	// returns. The ordinary add defers the catalogue walk to a durable job, and
	// an approval that answered before the walk would close the request against
	// an episode row that does not exist yet, so the granted scene would not be
	// wanted, and the next sweep would search for nothing.
	//
	// The site is pinned to the instance that answered GetScene above, which is
	// the only box whose catalogue this SiteStashID was read from.
	siteMonitored := body.Monitored != nil && *body.Monitored
	sr, err := s.mgr.AddSiteAndWait(ctx,
		core.ItemRef{Provider: providerID, Ref: scene.SiteStashID}, &siteMonitored, 0)
	if err != nil {
		s.writeManagerError(w, "", "add site", err)
		return nil, false, err
	}

	// The asked-for scene is the one thing monitored on the site the approval
	// just added. A walk that has no row for it (the provider never gave the
	// scene a release date, which is the only way a scene fails to file) means
	// the approval would close the request against nothing, and that is a
	// provider answer to refuse over, not absorb: the site add itself keeps no
	// state the next approval attempt would not refresh.
	filed, err := s.st.EpisodeIDsByStashID(ctx, []string{req.StashID})
	if err != nil {
		s.writeStoreError(w, "find scene episode", err)
		return nil, false, err
	}
	episodeID := filed[req.StashID]
	if episodeID == 0 {
		s.writeAdultProviderError(w, r, "get scene", errSceneNotFiled)
		return nil, false, errSceneNotFiled
	}
	episode, err := s.st.GetEpisode(ctx, episodeID)
	if err != nil {
		s.writeStoreError(w, "get scene episode", err)
		return nil, false, err
	}
	if !episode.Monitored {
		episode.Monitored = true
		if err := s.st.UpsertEpisode(ctx, episode); err != nil {
			s.writeStoreError(w, "monitor scene", err)
			return nil, false, err
		}
	}

	// A search the approver asked for is queued now; the wanted sweep finds
	// the scene either way, so a queue failure costs the approval nothing and
	// is logged rather than answered.
	queued := false
	if body.SearchNow {
		added, err := s.enqueueEpisodeSearch(ctx, episodeID)
		if err != nil {
			s.log.Error("queue scene search", "episode_id", episodeID, "error", err)
		} else {
			queued = added
		}
	}

	// The add absorbs a matching pending request the way every other add path
	// does, but that machinery keys on a TMDB id, and a scene request has none,
	// so this closes it explicitly.
	if err := s.st.SetRequestStatus(ctx, req.ID, core.RequestApproved); err != nil {
		s.writeStoreError(w, "approve request", err)
		return nil, false, err
	}
	return sr, queued, nil
}

// errSceneNotFiled is a walked catalogue with no row for the asked-for scene:
// the provider never gave it a release date, which is what a scene's season
// and number are computed from, so there is nothing to monitor or hunt for.
var errSceneNotFiled = errors.New("api: the provider's catalogue does not file this scene")

// errNoSceneSite is a provider record with no studio on it: a scene Caravan
// cannot file, because the site is what it would be filed under.
var errNoSceneSite = errors.New("api: the provider names no site for this scene")

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
// writing the failure itself. Season 0 is legal: that is where specials live.
func validSeasonNumbers(w http.ResponseWriter, seasons []int) bool {
	for _, n := range seasons {
		if n < 0 {
			writeError(w, http.StatusBadRequest, "season numbers must not be negative")
			return false
		}
	}
	return true
}

// inLibrary reports whether a provider id is already tracked. Exactly one of
// tmdbID and stashID is meaningful, chosen by mediaType, exactly as on the row
// the request would become.
func (s *server) inLibrary(
	ctx context.Context,
	mediaType string,
	tmdbID int64,
	stashID string,
	provider string,
) (bool, error) {
	if mediaType == MediaTypeScene {
		var (
			found map[string]int64
			err   error
		)
		if provider == "" {
			found, err = s.st.EpisodeFileIDsByStashID(ctx, []string{stashID})
		} else {
			found, err = s.st.EpisodeFileIDsByStashIDForProvider(ctx, provider, []string{stashID})
		}
		if err != nil {
			return false, err
		}
		return found[stashID] != 0, nil
	}
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

// validRequestMediaType checks the media type against what this caller may ask
// for, writing the failure itself.
//
// A caller the adult module is not visible to is refused a scene with the exact
// message and status an unrecognised media type gets, "banana" and "scene" are
// the same answer, so POST /requests cannot be used to find out whether the
// module exists on this server or whether this account was granted it. The
// message widens only for a caller who could have used it anyway, where naming
// the third value is help rather than disclosure.
func validRequestMediaType(w http.ResponseWriter, mediaType string, adult bool) bool {
	switch mediaType {
	case MediaTypeMovie, MediaTypeSeries:
		return true
	case MediaTypeScene:
		if adult {
			return true
		}
	}
	message := "media_type must be movie or series"
	if adult {
		message = "media_type must be movie, series or scene"
	}
	writeError(w, http.StatusBadRequest, message)
	return false
}

// requestDTO renders one row. username is the asker's name, which the caller
// resolves with requesterNames, passing it in rather than looking it up here is
// what keeps a list of rows from becoming a query per row.
func (s *server) requestDTO(r core.Request, username string) requestJSON {
	return requestJSON{
		ID:                  r.ID,
		MediaType:           r.MediaType,
		TMDBID:              r.TMDBID,
		StashID:             r.StashID,
		Title:               r.Title,
		Year:                r.Year,
		PosterPath:          r.PosterPath,
		PosterURL:           s.requestPosterURL(r),
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

// requestPosterURL renders one request row's artwork.
//
// A movie or series request stores a TMDB-relative path ("/abc.jpg") and needs
// the provider's image base in front of it. A scene request stores something
// else entirely: stashbox.coverURL hands out an absolute url, the SPA sends it
// as poster_path, and handleCreateRequest stores it verbatim. Running that
// through the TMDB helper concatenates the two into
// "https://image.tmdb.org/t/p/w500/https://cdn.…/scene.jpg". A dead image the
// browser still fetches from TMDB's CDN with the adult path in the request
// line. On a server with the adult module configured but no TMDB key it is
// worse still: Metadata() is nil, providerPosterURL returns "", and the row
// loses its artwork with a perfectly good url sitting in the column.
//
// The scheme is checked rather than trusted because poster_path is whatever the
// requesting account's client sent. Only http and https survive, so a member
// cannot use the field to point an admin's browser at an arbitrary URI on the
// requests screen.
func (s *server) requestPosterURL(r core.Request) string {
	if r.MediaType != MediaTypeScene {
		return s.providerPosterURL(r.PosterPath)
	}
	u, err := url.Parse(r.PosterPath)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return r.PosterPath
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
