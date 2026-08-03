package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/wanted"
)

// searchQueuedResponse is the reply from the on-demand search endpoints: how
// many search jobs the request actually added to the queue.
//
// Zero is a normal, successful answer, not a failure. It is what an item that
// already meets its cutoff reports, and what a second click reports while the
// first search is still queued — so the UI can say "nothing to search" rather
// than pretending work started.
type searchQueuedResponse struct {
	Queued int `json:"queued"`
}

// handleSearchMovieNow queues the automatic search for one movie (SPEC §9).
//
// Only a movie the wanted list actually names is queued. The search_movie
// handler has no guard of its own against re-grabbing a file that already
// meets the cutoff, so the decision of whether there is anything to look for
// has to be made here rather than deferred to the worker.
func (s *server) handleSearchMovieNow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, err := s.st.GetMovie(ctx, id); err != nil {
		s.writeStoreError(w, "get movie", err)
		return
	}

	lists, err := wanted.Compute(ctx, s.st)
	if err != nil {
		s.writeStoreError(w, "compute wanted list", err)
		return
	}
	queued := 0
	for _, m := range lists.Movies {
		if m.ID != id {
			continue
		}
		added, err := s.enqueueMovieSearch(ctx, id)
		if err != nil {
			s.writeStoreError(w, "queue movie search", err)
			return
		}
		if added {
			queued++
		}
		break
	}
	writeJSON(w, http.StatusAccepted, searchQueuedResponse{Queued: queued})
}

// handleSearchSeriesNow queues an automatic search for every wanted episode of
// one series.
func (s *server) handleSearchSeriesNow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	if _, err := s.st.GetSeries(ctx, id); err != nil {
		s.writeStoreError(w, "get series", err)
		return
	}

	queued, err := s.queueSeriesSearch(ctx, id)
	if err != nil {
		s.writeStoreError(w, "queue series search", err)
		return
	}
	writeJSON(w, http.StatusAccepted, searchQueuedResponse{Queued: queued})
}

// handleSearchWanted queues an automatic search for the whole wanted list. It
// is the backlog sweep on demand, and shares its dedupe: an item the sweep has
// already queued is not queued twice.
func (s *server) handleSearchWanted(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	lists, err := wanted.Compute(ctx, s.st)
	if err != nil {
		s.writeStoreError(w, "compute wanted list", err)
		return
	}

	queued := 0
	for _, m := range lists.Movies {
		added, err := s.enqueueMovieSearch(ctx, m.ID)
		if err != nil {
			s.writeStoreError(w, "queue movie search", err)
			return
		}
		if added {
			queued++
		}
	}
	added, err := s.enqueueEpisodeSearches(ctx, lists.Episodes)
	if err != nil {
		s.writeStoreError(w, "queue episode search", err)
		return
	}
	writeJSON(w, http.StatusAccepted, searchQueuedResponse{Queued: queued + added})
}

// queueSeriesSearch queues a search for every wanted episode of one series and
// reports how many jobs it added. Shared by POST /library/series/{id}/search
// and the search_missing flag on add, so both agree on what "missing" means:
// the wanted list, which excludes episodes that have not aired yet. Searching
// for an unaired episode can only ever record "no acceptable release found".
func (s *server) queueSeriesSearch(ctx context.Context, seriesID int64) (int, error) {
	lists, err := wanted.Compute(ctx, s.st)
	if err != nil {
		return 0, err
	}
	mine := make([]wanted.Episode, 0, len(lists.Episodes))
	for _, e := range lists.Episodes {
		if e.SeriesID == seriesID {
			mine = append(mine, e)
		}
	}
	return s.enqueueEpisodeSearches(ctx, mine)
}

func (s *server) enqueueEpisodeSearches(ctx context.Context, episodes []wanted.Episode) (int, error) {
	queued := 0
	for _, e := range episodes {
		added, err := s.enqueueSearchJob(ctx, core.JobSearchEpisode, core.JobSearchEpisodePayload{EpisodeID: e.ID})
		if err != nil {
			return queued, err
		}
		if added {
			queued++
		}
	}
	return queued, nil
}

func (s *server) enqueueMovieSearch(ctx context.Context, movieID int64) (bool, error) {
	return s.enqueueSearchJob(ctx, core.JobSearchMovie, core.JobSearchMoviePayload{MovieID: movieID})
}

// enqueueSearchJob adds one search job unless an identical one is already
// pending or running, and reports whether it wrote a row. The dedupe is the
// same HasOpenJob check the backlog sweep uses (PLAN phase 3, task 5): two
// clicks on Search now must not become two searches.
func (s *server) enqueueSearchJob(ctx context.Context, kind string, payload any) (bool, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("encode %s payload: %w", kind, err)
	}
	open, err := s.st.HasOpenJob(ctx, kind, string(encoded))
	if err != nil {
		return false, err
	}
	if open {
		return false, nil
	}
	if err := s.st.EnqueueJob(ctx, &core.Job{Kind: kind, Payload: string(encoded)}); err != nil {
		return false, err
	}
	return true, nil
}
