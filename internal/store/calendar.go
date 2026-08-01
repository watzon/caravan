package store

import (
	"context"
	"fmt"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// CalendarEpisode is the date-bearing episode state needed by the combined
// calendar. Keeping the file existence join here prevents the API from making
// one media-files query for every episode.
type CalendarEpisode struct {
	Episode     core.Episode
	SeriesTitle string
	HasFile     bool
}

// CalendarMovie is the movie counterpart of CalendarEpisode. Calendar rows
// include unmonitored movies too, so this is intentionally separate from the
// monitored-only MovieFileStates query used by wanted.
type CalendarMovie struct {
	Movie   core.Movie
	HasFile bool
}

// CalendarEpisodes returns dated episodes that are either monitored or already
// represented by a library file. An unmonitored, fileless episode is neither
// actionable nor historical enough to merit a calendar entry.
func (s *Store) CalendarEpisodes(ctx context.Context, start, end time.Time) ([]CalendarEpisode, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+episodeStateColumns+`, s.title,
			EXISTS(SELECT 1 FROM episode_files ef WHERE ef.episode_id = e.id)
		FROM episodes e
		JOIN series s ON s.id = e.series_id
		WHERE e.air_date != ''
			AND date(e.air_date) BETWEEN ? AND ?
			AND (e.monitored = 1 OR EXISTS(SELECT 1 FROM episode_files ef WHERE ef.episode_id = e.id))
		ORDER BY date(e.air_date), s.sort_title, s.title, e.season_number, e.episode_number`,
		start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("store: list calendar episodes: %w", err)
	}
	defer rows.Close()

	out := []CalendarEpisode{}
	for rows.Next() {
		var entry CalendarEpisode
		episode, err := scanEpisodeWith(rows, &entry.SeriesTitle, &entry.HasFile)
		if err != nil {
			return nil, fmt.Errorf("store: scan calendar episode: %w", err)
		}
		entry.Episode = *episode
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list calendar episodes: %w", err)
	}
	return out, nil
}

// CalendarMovies returns every library movie whose known release date falls in
// the range. Unlike wanted, a calendar remains useful for an unmonitored movie
// because it records when a library item was released.
func (s *Store) CalendarMovies(ctx context.Context, start, end time.Time) ([]CalendarMovie, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+movieStateColumns+`,
			EXISTS(SELECT 1 FROM media_files mf WHERE mf.movie_id = m.id)
		FROM movies m
		WHERE m.release_date != ''
			AND date(m.release_date) BETWEEN ? AND ?
		ORDER BY date(m.release_date), m.sort_title, m.title, m.year`,
		start.UTC().Format("2006-01-02"), end.UTC().Format("2006-01-02"))
	if err != nil {
		return nil, fmt.Errorf("store: list calendar movies: %w", err)
	}
	defer rows.Close()

	out := []CalendarMovie{}
	for rows.Next() {
		var entry CalendarMovie
		movie, err := scanMovieWith(rows, &entry.HasFile)
		if err != nil {
			return nil, fmt.Errorf("store: scan calendar movie: %w", err)
		}
		entry.Movie = *movie
		out = append(out, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list calendar movies: %w", err)
	}
	return out, nil
}
