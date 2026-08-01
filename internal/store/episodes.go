package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const seasonColumns = `id, series_id, season_number, title, overview, poster_path, air_date, monitored`

// UpsertSeason inserts or updates a season and writes back the assigned ID.
// Identity is (SeriesID, Number), which is what a provider refresh and a disk
// scan both have in hand.
func (s *Store) UpsertSeason(ctx context.Context, se *core.Season) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO seasons (series_id, season_number, title, overview, poster_path, air_date, monitored)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (series_id, season_number) DO UPDATE SET
			title = excluded.title, overview = excluded.overview,
			poster_path = excluded.poster_path, air_date = excluded.air_date,
			monitored = excluded.monitored`,
		se.SeriesID, se.Number, se.Title, se.Overview, se.PosterPath, formatTime(se.AirDate), se.Monitored)
	if err != nil {
		return fmt.Errorf("store: upsert season %d of series %d: %w", se.Number, se.SeriesID, err)
	}
	if se.ID != 0 {
		return nil
	}
	// LastInsertId is not meaningful after a DO UPDATE, so read the id back.
	if err := s.db.QueryRowContext(ctx,
		"SELECT id FROM seasons WHERE series_id = ? AND season_number = ?",
		se.SeriesID, se.Number).Scan(&se.ID); err != nil {
		return fmt.Errorf("store: upsert season %d of series %d: %w", se.Number, se.SeriesID, err)
	}
	return nil
}

// GetSeason returns the season with the given id, or ErrNotFound.
func (s *Store) GetSeason(ctx context.Context, id int64) (*core.Season, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+seasonColumns+" FROM seasons WHERE id = ?", id)
	se, err := scanSeason(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: season %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get season %d: %w", id, err)
	}
	return se, nil
}

// ListSeasons returns a series' seasons ordered by season number, specials
// (season 0) first.
func (s *Store) ListSeasons(ctx context.Context, seriesID int64) ([]core.Season, error) {
	rows, err := s.db.QueryContext(ctx,
		"SELECT "+seasonColumns+" FROM seasons WHERE series_id = ? ORDER BY season_number", seriesID)
	if err != nil {
		return nil, fmt.Errorf("store: list seasons of series %d: %w", seriesID, err)
	}
	defer rows.Close()

	out := []core.Season{}
	for rows.Next() {
		se, err := scanSeason(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan season: %w", err)
		}
		out = append(out, *se)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list seasons of series %d: %w", seriesID, err)
	}
	return out, nil
}

// DeleteSeason removes the season row. Episodes hang off the series, not the
// season row, so they survive; a rescan reconciles.
func (s *Store) DeleteSeason(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM seasons WHERE id = ?", id); err != nil {
		return fmt.Errorf("store: delete season %d: %w", id, err)
	}
	return nil
}

func scanSeason(sc scanner) (*core.Season, error) {
	var (
		se      core.Season
		airDate string
	)
	err := sc.Scan(&se.ID, &se.SeriesID, &se.Number, &se.Title, &se.Overview, &se.PosterPath,
		&airDate, &se.Monitored)
	if err != nil {
		return nil, err
	}
	se.AirDate = parseTime(airDate)
	return &se, nil
}

const episodeColumns = `id, series_id, season_number, episode_number, tmdb_id, title, overview, air_date, monitored`

// UpsertEpisode inserts or updates an episode and writes back the assigned ID.
// Identity is (SeriesID, SeasonNumber, EpisodeNumber).
func (s *Store) UpsertEpisode(ctx context.Context, e *core.Episode) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO episodes (series_id, season_number, episode_number, tmdb_id, title, overview, air_date, monitored)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (series_id, season_number, episode_number) DO UPDATE SET
			tmdb_id = excluded.tmdb_id, title = excluded.title, overview = excluded.overview,
			air_date = excluded.air_date, monitored = excluded.monitored`,
		e.SeriesID, e.SeasonNumber, e.EpisodeNumber, e.TMDBID, e.Title, e.Overview,
		formatTime(e.AirDate), e.Monitored)
	if err != nil {
		return fmt.Errorf("store: upsert episode S%02dE%02d of series %d: %w",
			e.SeasonNumber, e.EpisodeNumber, e.SeriesID, err)
	}
	if e.ID != 0 {
		return nil
	}
	if err := s.db.QueryRowContext(ctx,
		"SELECT id FROM episodes WHERE series_id = ? AND season_number = ? AND episode_number = ?",
		e.SeriesID, e.SeasonNumber, e.EpisodeNumber).Scan(&e.ID); err != nil {
		return fmt.Errorf("store: upsert episode S%02dE%02d of series %d: %w",
			e.SeasonNumber, e.EpisodeNumber, e.SeriesID, err)
	}
	return nil
}

// GetEpisode returns the episode with the given id, or ErrNotFound.
func (s *Store) GetEpisode(ctx context.Context, id int64) (*core.Episode, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+episodeColumns+" FROM episodes WHERE id = ?", id)
	e, err := scanEpisode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: episode %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get episode %d: %w", id, err)
	}
	return e, nil
}

// GetEpisodeByNumber returns one episode of a series by season and episode
// number, or ErrNotFound. This is the lookup a parsed filename needs.
func (s *Store) GetEpisodeByNumber(ctx context.Context, seriesID int64, season, episode int) (*core.Episode, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+episodeColumns+
		" FROM episodes WHERE series_id = ? AND season_number = ? AND episode_number = ?",
		seriesID, season, episode)
	e, err := scanEpisode(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: series %d S%02dE%02d: %w", seriesID, season, episode, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get series %d S%02dE%02d: %w", seriesID, season, episode, err)
	}
	return e, nil
}

// ListEpisodes returns a series' episodes ordered by season then episode.
func (s *Store) ListEpisodes(ctx context.Context, seriesID int64) ([]core.Episode, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT "+episodeColumns+
		" FROM episodes WHERE series_id = ? ORDER BY season_number, episode_number", seriesID)
	if err != nil {
		return nil, fmt.Errorf("store: list episodes of series %d: %w", seriesID, err)
	}
	defer rows.Close()

	out := []core.Episode{}
	for rows.Next() {
		e, err := scanEpisode(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan episode: %w", err)
		}
		out = append(out, *e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list episodes of series %d: %w", seriesID, err)
	}
	return out, nil
}

// EpisodeCounts is a series' episode tally: how many episodes are known and
// how many of them have a file on disk. It is what the library list renders as
// "12 / 24" and what the status vocabulary turns into downloaded / incomplete.
type EpisodeCounts struct {
	Total    int
	WithFile int
}

// EpisodeCountsBySeries returns the tally for every series in one query,
// keyed by series id. A series with no episode rows is absent from the map,
// which reads as the zero EpisodeCounts.
//
// The file check is an EXISTS subquery rather than a join because a
// multi-episode file links to the same episode row once per episode it covers;
// joining would count those episodes twice (SPEC §7).
func (s *Store) EpisodeCountsBySeries(ctx context.Context) (map[int64]EpisodeCounts, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT series_id, COUNT(*), SUM(has_file)
		FROM (
			SELECT
				e.series_id AS series_id,
				EXISTS (SELECT 1 FROM episode_files ef WHERE ef.episode_id = e.id) AS has_file
			FROM episodes e
		)
		GROUP BY series_id`)
	if err != nil {
		return nil, fmt.Errorf("store: count episodes: %w", err)
	}
	defer rows.Close()

	out := make(map[int64]EpisodeCounts)
	for rows.Next() {
		var (
			seriesID int64
			counts   EpisodeCounts
		)
		if err := rows.Scan(&seriesID, &counts.Total, &counts.WithFile); err != nil {
			return nil, fmt.Errorf("store: scan episode counts: %w", err)
		}
		out[seriesID] = counts
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: count episodes: %w", err)
	}
	return out, nil
}

// DeleteEpisode removes the episode and, by cascade, its episode-file links.
func (s *Store) DeleteEpisode(ctx context.Context, id int64) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM episodes WHERE id = ?", id); err != nil {
		return fmt.Errorf("store: delete episode %d: %w", id, err)
	}
	return nil
}

func scanEpisode(sc scanner) (*core.Episode, error) {
	var (
		e       core.Episode
		airDate string
	)
	err := sc.Scan(&e.ID, &e.SeriesID, &e.SeasonNumber, &e.EpisodeNumber, &e.TMDBID, &e.Title,
		&e.Overview, &airDate, &e.Monitored)
	if err != nil {
		return nil, err
	}
	e.AirDate = parseTime(airDate)
	return &e, nil
}
