package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/watzon/caravan/internal/core"
)

const (
	defaultJobLimit = 100
	maxJobLimit     = 1000
)

type jobJSON struct {
	ID             int64           `json:"id"`
	Kind           string          `json:"kind"`
	Payload        json.RawMessage `json:"payload"`
	State          string          `json:"state"`
	Attempts       int             `json:"attempts"`
	RunAfter       string          `json:"run_after"`
	LeaseExpiresAt string          `json:"lease_expires_at"`
	LastError      string          `json:"last_error"`
	CreatedAt      string          `json:"created_at"`
	UpdatedAt      string          `json:"updated_at"`
	// Subject is the library title this job is about — a movie name, a
	// series name, or an adult site. Empty on kinds that have no title, or
	// when the row is gone. The footer groups live searches by this.
	Subject string `json:"subject,omitempty"`
	// SubjectKind is "movie", "series", or "site". Empty when Subject is.
	SubjectKind string `json:"subject_kind,omitempty"`
	// SubjectID is the library id the footer should open: a movie, a series,
	// or an adult site. Zero when there is no row to link to.
	SubjectID int64 `json:"subject_id,omitempty"`
}

// handleListJobs returns the durable job activity feed, newest first.
func (s *server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	_, hasLimit := query["limit"]
	rawCursor := query.Get("cursor")
	_, hasCursor := query["cursor"]
	cursorMode := hasLimit || hasCursor

	limit := defaultJobLimit
	if raw := query.Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = min(n, maxJobLimit)
	}

	if !cursorMode {
		jobs, err := s.st.ListJobs(r.Context(), limit)
		if err != nil {
			s.writeStoreError(w, "list jobs", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"jobs": s.jobJSONs(r.Context(), jobs)})
		return
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
	jobs, nextID, err := s.st.ListJobsPage(r.Context(), limit, beforeID)
	if err != nil {
		s.writeStoreError(w, "list job page", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"jobs":        s.jobJSONs(r.Context(), jobs),
		"next_cursor": cursorString(nextID),
	})
}

func (s *server) jobJSONs(ctx context.Context, jobs []core.Job) []jobJSON {
	out := make([]jobJSON, 0, len(jobs))
	for _, job := range jobs {
		out = append(out, jobJSON{
			ID:             job.ID,
			Kind:           job.Kind,
			Payload:        json.RawMessage(job.Payload),
			State:          job.State,
			Attempts:       job.Attempts,
			RunAfter:       jsonTime(job.RunAfter),
			LeaseExpiresAt: jsonTime(job.LeaseExpiresAt),
			LastError:      job.LastError,
			CreatedAt:      jsonTime(job.CreatedAt),
			UpdatedAt:      jsonTime(job.UpdatedAt),
		})
	}
	s.decorateJobSubjects(ctx, out)
	return out
}

// decorateJobSubjects fills Subject for live search and catalogue jobs. A
// missing library row is not an error: the footer then falls back to an
// un-named count.
func (s *server) decorateJobSubjects(ctx context.Context, jobs []jobJSON) {
	movies := map[int64]*core.Movie{}
	series := map[int64]*core.Series{}
	for i := range jobs {
		job := &jobs[i]
		if job.State != core.JobStatePending && job.State != core.JobStateRunning {
			continue
		}
		switch job.Kind {
		case core.JobSearchMovie:
			var payload core.JobSearchMoviePayload
			if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.MovieID <= 0 {
				continue
			}
			movie := loadMovie(ctx, s, movies, payload.MovieID)
			if movie == nil {
				continue
			}
			job.Subject = movie.Title
			job.SubjectKind = "movie"
			job.SubjectID = movie.ID
		case core.JobSearchEpisode:
			var payload core.JobSearchEpisodePayload
			if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.EpisodeID <= 0 {
				continue
			}
			episode, err := s.st.GetEpisode(ctx, payload.EpisodeID)
			if err != nil {
				continue
			}
			show := loadSeries(ctx, s, series, episode.SeriesID)
			if show == nil {
				continue
			}
			job.Subject = show.Title
			job.SubjectID = show.ID
			if show.Kind == core.SeriesKindAdult {
				job.SubjectKind = "site"
			} else {
				job.SubjectKind = "series"
			}
		case core.JobSyncSite:
			var payload core.JobSyncSitePayload
			if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.SeriesID <= 0 {
				continue
			}
			show := loadSeries(ctx, s, series, payload.SeriesID)
			if show == nil {
				continue
			}
			job.Subject = show.Title
			job.SubjectKind = "site"
			job.SubjectID = show.ID
		case core.JobMoveItem:
			var payload core.JobMoveItemPayload
			if err := json.Unmarshal(job.Payload, &payload); err != nil || payload.ItemID <= 0 {
				continue
			}
			if payload.ItemType == core.MediaTypeMovie {
				movie := loadMovie(ctx, s, movies, payload.ItemID)
				if movie == nil {
					continue
				}
				job.Subject = movie.Title
				job.SubjectKind = "movie"
				job.SubjectID = movie.ID
				continue
			}
			show := loadSeries(ctx, s, series, payload.ItemID)
			if show == nil {
				continue
			}
			job.Subject = show.Title
			job.SubjectID = show.ID
			if show.Kind == core.SeriesKindAdult {
				job.SubjectKind = "site"
			} else {
				job.SubjectKind = "series"
			}
		}
	}
}

func loadMovie(ctx context.Context, s *server, cache map[int64]*core.Movie, id int64) *core.Movie {
	if movie, ok := cache[id]; ok {
		return movie
	}
	movie, err := s.st.GetMovie(ctx, id)
	if err != nil {
		cache[id] = nil
		return nil
	}
	cache[id] = movie
	return movie
}

func loadSeries(ctx context.Context, s *server, cache map[int64]*core.Series, id int64) *core.Series {
	if show, ok := cache[id]; ok {
		return show
	}
	show, err := s.st.GetSeries(ctx, id)
	if err != nil {
		cache[id] = nil
		return nil
	}
	cache[id] = show
	return show
}
