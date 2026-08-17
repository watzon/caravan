package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

// UpsertSeason inserts or updates a season and writes back the assigned ID.
// Identity is (SeriesID, Number), which is what a provider refresh and a disk
// scan both have in hand.
func (s *Store) UpsertSeason(ctx context.Context, se *core.Season) error {
	model := catalogSeasonModelFromCore(se)
	_, err := s.db.NewInsert().Model(&model).
		On("CONFLICT (series_id, season_number) DO UPDATE").
		Set("title = EXCLUDED.title").
		Set("overview = EXCLUDED.overview").
		Set("poster_path = EXCLUDED.poster_path").
		Set("air_date = EXCLUDED.air_date").
		Set("monitored = EXCLUDED.monitored").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: upsert season %d of series %d: %w", se.Number, se.SeriesID, err)
	}
	if se.ID != 0 {
		return nil
	}
	if err := s.db.NewSelect().Model((*catalogSeasonModel)(nil)).Column("id").
		Where("series_id = ?", se.SeriesID).Where("season_number = ?", se.Number).
		Scan(ctx, &se.ID); err != nil {
		return fmt.Errorf("store: upsert season %d of series %d: %w", se.Number, se.SeriesID, err)
	}
	return nil
}

// GetSeason returns the season with the given id, or ErrNotFound.
func (s *Store) GetSeason(ctx context.Context, id int64) (*core.Season, error) {
	var model catalogSeasonModel
	if err := s.db.NewSelect().Model(&model).Where("id = ?", id).Scan(ctx); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("store: season %d: %w", id, ErrNotFound)
		}
		return nil, fmt.Errorf("store: get season %d: %w", id, err)
	}
	season := model.core()
	return &season, nil
}

// ListSeasons returns a series' seasons ordered by season number, specials
// (season 0) first.
func (s *Store) ListSeasons(ctx context.Context, seriesID int64) ([]core.Season, error) {
	var models []catalogSeasonModel
	if err := s.db.NewSelect().Model(&models).Where("series_id = ?", seriesID).
		OrderExpr("season_number").Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list seasons of series %d: %w", seriesID, err)
	}
	out := make([]core.Season, len(models))
	for i := range models {
		out[i] = models[i].core()
	}
	return out, nil
}

// DeleteSeason removes the season row. Episodes hang off the series, not the
// season row, so they survive; a rescan reconciles.
func (s *Store) DeleteSeason(ctx context.Context, id int64) error {
	if _, err := s.db.NewDelete().Model((*catalogSeasonModel)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("store: delete season %d: %w", id, err)
	}
	return nil
}

// UpsertEpisode inserts or updates an episode and writes back the assigned ID.
// Identity is (SeriesID, SeasonNumber, EpisodeNumber) — for a scene, that is
// (site, release year, sequence within the year), which is the whole
// site-as-series mapping expressed in the key the table already had.
func (s *Store) UpsertEpisode(ctx context.Context, e *core.Episode) error {
	model, err := catalogEpisodeModelFromCore(e)
	if err != nil {
		return fmt.Errorf("store: upsert episode S%02dE%02d of series %d: %w",
			e.SeasonNumber, e.EpisodeNumber, e.SeriesID, err)
	}
	_, err = s.db.NewInsert().Model(&model).
		On("CONFLICT (series_id, season_number, episode_number) DO UPDATE").
		Set("tmdb_id = EXCLUDED.tmdb_id").Set("stash_id = EXCLUDED.stash_id").
		Set("title = EXCLUDED.title").Set("overview = EXCLUDED.overview").
		Set("air_date = EXCLUDED.air_date").Set("monitored = EXCLUDED.monitored").
		Set("scene = EXCLUDED.scene").
		Set("absolute_number = CASE WHEN EXCLUDED.absolute_number != 0 THEN EXCLUDED.absolute_number ELSE episode.absolute_number END").
		Exec(ctx)
	if err != nil {
		return fmt.Errorf("store: upsert episode S%02dE%02d of series %d: %w",
			e.SeasonNumber, e.EpisodeNumber, e.SeriesID, err)
	}
	if e.ID != 0 {
		s.note("library")
		return nil
	}
	if err := s.db.NewSelect().Model((*catalogEpisodeModel)(nil)).Column("id").
		Where("series_id = ?", e.SeriesID).Where("season_number = ?", e.SeasonNumber).
		Where("episode_number = ?", e.EpisodeNumber).Scan(ctx, &e.ID); err != nil {
		return fmt.Errorf("store: upsert episode S%02dE%02d of series %d: %w",
			e.SeasonNumber, e.EpisodeNumber, e.SeriesID, err)
	}
	s.note("library")
	return nil
}

func (s *Store) getEpisode(ctx context.Context, description string, query func(*catalogEpisodeModel) error) (*core.Episode, error) {
	var model catalogEpisodeModel
	if err := query(&model); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("store: %s: %w", description, ErrNotFound)
		}
		return nil, fmt.Errorf("store: get %s: %w", description, err)
	}
	episode, err := model.core()
	if err != nil {
		return nil, fmt.Errorf("store: decode %s: %w", description, err)
	}
	return &episode, nil
}

// GetEpisode returns the episode with the given id, or ErrNotFound.
func (s *Store) GetEpisode(ctx context.Context, id int64) (*core.Episode, error) {
	return s.getEpisode(ctx, fmt.Sprintf("episode %d", id), func(model *catalogEpisodeModel) error {
		return s.db.NewSelect().Model(model).Where("id = ?", id).Scan(ctx)
	})
}

// GetEpisodeByStashID returns the episode whose scene has the given stash-box
// id, or ErrNotFound. A blank id matches nothing.
func (s *Store) GetEpisodeByStashID(ctx context.Context, stashID string) (*core.Episode, error) {
	if stashID == "" {
		return nil, fmt.Errorf("store: episode stash %q: %w", stashID, ErrNotFound)
	}
	return s.getEpisode(ctx, fmt.Sprintf("episode stash %q", stashID), func(model *catalogEpisodeModel) error {
		return s.db.NewSelect().Model(model).Where("stash_id = ?", stashID).Scan(ctx)
	})
}

// GetEpisodeByNumber returns one episode of a series by season and episode
// number, or ErrNotFound.
func (s *Store) GetEpisodeByNumber(ctx context.Context, seriesID int64, season, episode int) (*core.Episode, error) {
	description := fmt.Sprintf("series %d S%02dE%02d", seriesID, season, episode)
	return s.getEpisode(ctx, description, func(model *catalogEpisodeModel) error {
		return s.db.NewSelect().Model(model).Where("series_id = ?", seriesID).
			Where("season_number = ?", season).Where("episode_number = ?", episode).Scan(ctx)
	})
}

// GetEpisodeByAbsoluteNumber returns the first episode a series-wide number
// names. Zero and negative values never match unknown absolute numbers.
func (s *Store) GetEpisodeByAbsoluteNumber(ctx context.Context, seriesID int64, absolute int) (*core.Episode, error) {
	if absolute <= 0 {
		return nil, fmt.Errorf("store: series %d absolute %d: %w", seriesID, absolute, ErrNotFound)
	}
	description := fmt.Sprintf("series %d absolute %d", seriesID, absolute)
	return s.getEpisode(ctx, description, func(model *catalogEpisodeModel) error {
		return s.db.NewSelect().Model(model).Where("series_id = ?", seriesID).
			Where("absolute_number = ?", absolute).OrderExpr("season_number, episode_number").Limit(1).Scan(ctx)
	})
}

// ListEpisodes returns a series' episodes ordered by season then episode.
func (s *Store) ListEpisodes(ctx context.Context, seriesID int64) ([]core.Episode, error) {
	var models []catalogEpisodeModel
	if err := s.db.NewSelect().Model(&models).Where("series_id = ?", seriesID).
		OrderExpr("season_number, episode_number").Scan(ctx); err != nil {
		return nil, fmt.Errorf("store: list episodes of series %d: %w", seriesID, err)
	}
	out := make([]core.Episode, 0, len(models))
	for i := range models {
		episode, err := models[i].core()
		if err != nil {
			return nil, fmt.Errorf("store: decode episode: %w", err)
		}
		out = append(out, episode)
	}
	return out, nil
}

// EpisodeCounts is a series' episode tally: how many episodes are known and
// how many of them have a file on disk.
type EpisodeCounts struct {
	Total    int
	WithFile int
}

// EpisodeCountsBySeries returns the tally for every series in one query. This
// remains set-oriented SQL because the EXISTS aggregate is the complete query.
func (s *Store) EpisodeCountsBySeries(ctx context.Context) (map[int64]EpisodeCounts, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT series_id, COUNT(*), SUM(has_file)
		FROM (
			SELECT e.series_id,
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
		var seriesID int64
		var counts EpisodeCounts
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

// CascadeSeriesMonitored bulk-updates every season and episode of a series.
func (s *Store) CascadeSeriesMonitored(ctx context.Context, seriesID int64, monitored bool) error {
	if _, err := s.db.NewUpdate().Model((*catalogSeasonModel)(nil)).Set("monitored = ?", monitored).
		Where("series_id = ?", seriesID).Exec(ctx); err != nil {
		return fmt.Errorf("store: cascade monitored to seasons of series %d: %w", seriesID, err)
	}
	if _, err := s.db.NewUpdate().Model((*catalogEpisodeModel)(nil)).Set("monitored = ?", monitored).
		Where("series_id = ?", seriesID).Exec(ctx); err != nil {
		return fmt.Errorf("store: cascade monitored to episodes of series %d: %w", seriesID, err)
	}
	s.note("library")
	return nil
}

// CascadeSeasonMonitored bulk-updates every episode in one season.
func (s *Store) CascadeSeasonMonitored(ctx context.Context, seriesID int64, seasonNumber int, monitored bool) error {
	if _, err := s.db.NewUpdate().Model((*catalogEpisodeModel)(nil)).Set("monitored = ?", monitored).
		Where("series_id = ?", seriesID).Where("season_number = ?", seasonNumber).Exec(ctx); err != nil {
		return fmt.Errorf("store: cascade monitored to season %d of series %d: %w", seasonNumber, seriesID, err)
	}
	s.note("library")
	return nil
}

// DeleteEpisode removes the episode and, by cascade, its episode-file links.
func (s *Store) DeleteEpisode(ctx context.Context, id int64) error {
	if _, err := s.db.NewDelete().Model((*catalogEpisodeModel)(nil)).Where("id = ?", id).Exec(ctx); err != nil {
		return fmt.Errorf("store: delete episode %d: %w", id, err)
	}
	return nil
}

func encodeScene(sc *core.SceneInfo) (string, error) {
	if sc == nil {
		return "", nil
	}
	b, err := json.Marshal(sc)
	if err != nil {
		return "", fmt.Errorf("store: encode scene: %w", err)
	}
	return string(b), nil
}

func decodeScene(raw string) (*core.SceneInfo, error) {
	if raw == "" {
		return nil, nil
	}
	var sc *core.SceneInfo
	if err := json.Unmarshal([]byte(raw), &sc); err != nil {
		return nil, fmt.Errorf("store: decode scene: %w", err)
	}
	return sc, nil
}
