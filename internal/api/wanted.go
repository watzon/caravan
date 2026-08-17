package api

import (
	"net/http"

	"github.com/watzon/caravan/internal/wanted"
)

// wantedMovieJSON is one row of the wanted movie list: the movie plus why it
// is wanted and what the library already holds.
type wantedMovieJSON struct {
	ID          int64  `json:"id"`
	Title       string `json:"title"`
	Year        int    `json:"year"`
	PosterPath  string `json:"poster_path"`
	PosterURL   string `json:"poster_url"`
	Reason      string `json:"reason"`
	FileQuality string `json:"file_quality"`
}

// wantedEpisodeJSON is one row of the wanted episode list. The poster is the
// series' — episodes have no artwork of their own.
type wantedEpisodeJSON struct {
	ID          int64  `json:"id"`
	SeriesID    int64  `json:"series_id"`
	SeriesTitle string `json:"series_title"`
	// SeriesKind is the series' core.SeriesKind* value. The wanted screen
	// routes and labels from it: a scene is not a television episode, and a
	// link that pretends it is opens the picker on the wrong seed.
	SeriesKind    string `json:"series_kind"`
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
	Title         string `json:"title"`
	AirDate       string `json:"air_date"`
	PosterPath    string `json:"poster_path"`
	PosterURL     string `json:"poster_url"`
	Reason        string `json:"reason"`
	FileQuality   string `json:"file_quality"`
}

// handleWanted returns the wanted list: monitored movies and episodes that
// are missing or below their profile's cutoff (PLAN phase 3, task 2).
func (s *server) handleWanted(w http.ResponseWriter, r *http.Request) {
	lists, err := wanted.Compute(r.Context(), s.st)
	if err != nil {
		s.writeStoreError(w, "compute wanted list", err)
		return
	}

	movies := make([]wantedMovieJSON, 0, len(lists.Movies))
	for _, m := range lists.Movies {
		movies = append(movies, wantedMovieJSON{
			ID:          m.ID,
			Title:       m.Title,
			Year:        m.Year,
			PosterPath:  m.PosterPath,
			PosterURL:   m.PosterURL,
			Reason:      m.Reason,
			FileQuality: m.FileQuality,
		})
	}
	episodes := make([]wantedEpisodeJSON, 0, len(lists.Episodes))
	for _, e := range lists.Episodes {
		episodes = append(episodes, wantedEpisodeJSON{
			ID:            e.ID,
			SeriesID:      e.SeriesID,
			SeriesTitle:   e.SeriesTitle,
			SeriesKind:    e.SeriesKind,
			SeasonNumber:  e.SeasonNumber,
			EpisodeNumber: e.EpisodeNumber,
			Title:         e.Title,
			AirDate:       jsonDate(e.AirDate),
			PosterPath:    e.SeriesPosterPath,
			PosterURL:     e.SeriesPosterURL,
			Reason:        e.Reason,
			FileQuality:   e.FileQuality,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"movies": movies, "episodes": episodes})
}
