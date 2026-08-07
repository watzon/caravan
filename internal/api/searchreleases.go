package api

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// Universal indexer search (plan part B): a free-text query fanned out over
// the enabled indexers, Prowlarr-style. It exists for the releases the
// derived per-item queries miss — a naming pattern the builders never try —
// and for content that is no library item at all.
//
// It reuses the per-item picker's whole machinery: the same fan-out
// (searchIndexers), the same cache (UpsertRelease is what makes the grab a
// lookup by id), and the same row DTO, so one frontend table renders both.

// searchReleaseLimits bound the response, not the cache: every row is cached
// BEFORE the cut, so a truncated result is still grabbable after a narrower
// re-search finds it again.
const (
	searchReleaseDefaultLimit = 200
	searchReleaseMaxLimit     = 500
	// searchQueryMaxLen keeps a pathological query out of every outbound URL.
	searchQueryMaxLen = 200
)

func (s *server) handleSearchReleases(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "q is required")
		return
	}
	if len(q) > searchQueryMaxLen {
		writeError(w, http.StatusBadRequest, "q is too long")
		return
	}
	rawCats, ok := intListParam(w, r, "cats")
	if !ok {
		return
	}
	cats := make([]int, 0, len(rawCats))
	for _, id := range rawCats {
		cats = append(cats, int(id))
	}
	indexerIDs, ok := intListParam(w, r, "indexer_ids")
	if !ok {
		return
	}
	limit := searchReleaseDefaultLimit
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = min(parsed, searchReleaseMaxLimit)
	}

	adult, err := s.gate(r).seesAdult(ctx)
	if err != nil {
		s.writeStoreError(w, "read library access", err)
		return
	}
	// The adult gate on the way OUT: requested adult categories are stripped
	// for a caller the module is absent to, and a request that named ONLY
	// adult categories short-circuits to an empty answer — indistinguishable
	// from a search that matched nothing, which is the module's promise of
	// absence. It must never fall through to an empty list, because an empty
	// list means "search unfiltered" and would return everything.
	if !adult {
		kept := cats[:0]
		for _, id := range cats {
			if !core.IsAdultCategory(id) {
				kept = append(kept, id)
			}
		}
		if len(cats) > 0 && len(kept) == 0 {
			writeJSON(w, http.StatusOK, releasesResponse{
				Query: q, Queries: []string{q},
				Releases: []releaseJSON{}, Errors: []indexerErrorJSON{},
			})
			return
		}
		cats = kept
	}

	// The profile every row is scored against: the chosen library's, or the
	// store-wide default. Never nil — the scorer and its DTO dereference it —
	// which is why an absent library_id still resolves a profile.
	var libraryID int64
	profile := (*core.QualityProfile)(nil)
	if raw := r.URL.Query().Get("library_id"); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, "invalid library_id")
			return
		}
		lib, ok := s.visibleLibrary(w, r, id)
		if !ok {
			return
		}
		libraryID = lib.ID
		profile, err = s.st.ResolveItemQualityProfileByLibrary(ctx, lib.ID, lib.Kind, 0)
		if err != nil {
			s.writeStoreError(w, "resolve library quality profile", err)
			return
		}
	} else {
		profile, err = s.st.ResolveQualityProfile(ctx, 0)
		if err != nil {
			s.writeStoreError(w, "resolve quality profile", err)
			return
		}
	}

	newClient, ok := s.requireIndexerClients(w)
	if !ok {
		return
	}
	indexers, err := s.st.ListEnabledIndexers(ctx)
	if err != nil {
		s.writeStoreError(w, "list indexers", err)
		return
	}
	chosen := make([]core.IndexerConfig, 0, len(indexers))
	for _, cfg := range indexers {
		// Unknown or disabled ids are silently dropped: they are a stale
		// client cache, not an error. An explicit list that selects nothing
		// is a search over nothing, answered with zero releases.
		if len(indexerIDs) > 0 && !containsInt64(indexerIDs, cfg.ID) {
			continue
		}
		// Exactly the requested categories, overwriting the indexer's own
		// list: the client falls back to its configured categories when
		// handed none (indexer.Client.search), and a Prowlarr-style search
		// with no categories means genuinely unfiltered.
		cfg.Categories = cats
		chosen = append(chosen, cfg)
	}

	searchCtx, cancel := context.WithTimeout(ctx, releaseSearchTimeout)
	defer cancel()
	releases, failures := searchIndexers(searchCtx, newClient, chosen, []string{q})

	tvProfile := s.activeTVProfile(ctx)
	out := releasesResponse{
		Query: q, Queries: []string{q}, LibraryID: libraryID,
		Releases: make([]releaseJSON, 0, len(releases)),
		Errors:   failures,
	}
	for _, rel := range releases {
		// The belt behind the stripped request: an indexer that files XXX
		// into an unfiltered answer must not show it to a caller the module
		// is absent to — and must not CACHE it under this search either, or
		// the row id would be grabbable.
		if !adult && core.HasAdultCategory(rel.Categories) {
			continue
		}
		if err := s.st.UpsertRelease(ctx, &rel); err != nil {
			s.writeStoreError(w, "cache release", err)
			return
		}
		out.Releases = append(out.Releases, releaseDTO(rel, commonReleaseFlags(rel), tvProfile, profile))
	}
	sortReleases(out.Releases)
	if len(out.Releases) > limit {
		out.Releases = out.Releases[:limit]
		out.Truncated = true
	}
	writeJSON(w, http.StatusOK, out)
}

// searchGrabRequest is POST /search/grab's body: the cached release, the
// library the payload belongs to, and — optionally — the item it is tied to.
//
// "Add on the fly" is deliberately NOT here: the client adds through the
// ordinary add endpoint first and then ties to the new item, so there is one
// add path and one metadata-search UI. A failed grab after a successful add
// leaves a visible library item, which is honest and recoverable.
type searchGrabRequest struct {
	ReleaseID int64 `json:"release_id"`
	LibraryID int64 `json:"library_id"`
	// Tie names an existing library item. Absent means untied: the finished
	// download parks in scan review scoped to LibraryID for a manual match.
	Tie *searchGrabTie `json:"tie"`
}

// searchGrabTie scopes a tied grab exactly as the per-item endpoints scope
// theirs: a movie by id; a series by id with an optional season and episode
// narrowing (absent = the whole series).
type searchGrabTie struct {
	MediaType string `json:"media_type"`
	MediaID   int64  `json:"media_id"`
	Season    *int   `json:"season"`
	Episode   *int   `json:"episode"`
}

func (s *server) handleSearchGrab(w http.ResponseWriter, r *http.Request) {
	var body searchGrabRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ReleaseID <= 0 {
		writeError(w, http.StatusBadRequest, "release_id is required")
		return
	}
	if body.LibraryID <= 0 {
		writeError(w, http.StatusBadRequest, "library_id is required")
		return
	}
	ctx := r.Context()
	lib, ok := s.visibleLibrary(w, r, body.LibraryID)
	if !ok {
		return
	}

	// The grab-side adult gate, and what releases.categories exists for: a
	// cached adult release id — cached by an admin's own search — must be
	// "not found" to a caller the module is absent to, not a grabbable
	// download. 404 rather than 403 for the module's usual reason.
	rel, err := s.st.GetRelease(ctx, body.ReleaseID)
	if err != nil {
		s.writeStoreError(w, "get release", err)
		return
	}
	if core.HasAdultCategory(rel.Categories) {
		adult, err := s.gate(r).seesAdult(ctx)
		if err != nil {
			s.writeStoreError(w, "read library access", err)
			return
		}
		if !adult {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
	}

	if body.Tie == nil {
		// Untied: the library IS the whole target. The import pipeline sees a
		// grab with no movie and no series but a library, and parks the
		// payload in scan review scoped to it.
		s.grabRelease(w, r, lib.ID, lib.Kind,
			body.ReleaseID,
			core.GrabInfo{LibraryID: lib.ID},
			core.AddOpts{Category: engineCategoryFor(lib.Kind), LibraryID: lib.ID})
		return
	}

	switch body.Tie.MediaType {
	case core.MediaTypeMovie:
		m, err := s.st.GetMovie(ctx, body.Tie.MediaID)
		if err != nil {
			s.writeStoreError(w, "get movie", err)
			return
		}
		if !itemInLibrary(m.LibraryID, core.LibraryKindMovie, lib) {
			writeError(w, http.StatusBadRequest, "movie does not belong to that library")
			return
		}
		s.grabRelease(w, r, lib.ID, core.LibraryKindMovie, body.ReleaseID,
			core.GrabInfo{MovieID: m.ID, LibraryID: lib.ID},
			core.AddOpts{Category: engineCategoryMovies, MovieID: m.ID, LibraryID: lib.ID})
	case core.MediaTypeSeries:
		sr, ok := s.getVisibleSeries(w, r, body.Tie.MediaID)
		if !ok {
			return
		}
		if !itemInLibrary(sr.LibraryID, core.LibraryKindForSeries(sr.Kind), lib) {
			writeError(w, http.StatusBadRequest, "series does not belong to that library")
			return
		}
		season, episode := -1, 0
		if body.Tie.Season != nil {
			season = *body.Tie.Season
		}
		if body.Tie.Episode != nil {
			episode = *body.Tie.Episode
		}
		episodes, err := s.st.ListEpisodes(ctx, sr.ID)
		if err != nil {
			s.writeStoreError(w, "list episodes", err)
			return
		}
		episodeIDs, seasonNum := seriesGrabScope(episodes, season, episode)
		if (season >= 0 || episode > 0) && len(episodeIDs) == 0 {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		info, opts := seriesGrabTarget(*sr, seasonNum, episodeIDs)
		s.grabRelease(w, r, sr.LibraryID, core.LibraryKindForSeries(sr.Kind), body.ReleaseID, info, opts)
	default:
		writeError(w, http.StatusBadRequest, "tie media_type must be movie or series")
	}
}

// itemInLibrary reports whether an item belongs to the chosen library. An
// item whose library_id is still 0 belongs to its kind's default library,
// which is what the check honours rather than refusing every pre-0022 row.
func itemInLibrary(itemLibraryID int64, itemKind string, lib *core.Library) bool {
	if itemLibraryID != 0 {
		return itemLibraryID == lib.ID
	}
	return lib.IsDefault && lib.Kind == itemKind
}

// engineCategoryFor labels an untied grab by its library's kind, the same
// three labels every tied grab uses. Per-library labels would churn every
// existing user's download client for a cosmetic field, so kind stays the
// vocabulary here; a per-library label would be a settings field, not this.
func engineCategoryFor(kind string) string {
	switch kind {
	case core.LibraryKindMovie:
		return engineCategoryMovies
	case core.LibraryKindAdult:
		return engineCategoryAdult
	}
	return engineCategoryTV
}

// intListParam parses a comma-separated list of positive ints, writing the
// 400 itself. Absent is an empty list.
func intListParam(w http.ResponseWriter, r *http.Request, name string) ([]int64, bool) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return nil, true
	}
	parts := strings.Split(raw, ",")
	out := make([]int64, 0, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			writeError(w, http.StatusBadRequest, name+" must be positive integers")
			return nil, false
		}
		out = append(out, id)
	}
	return out, true
}

func containsInt64(list []int64, v int64) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
