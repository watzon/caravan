package store

import (
	"context"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

// Qualified column lists for the joined queries here: the plain movieColumns
// and episodeColumns constants are ambiguous next to media_files.id and
// series.id.
const (
	movieStateColumns = `m.id, m.tmdb_id, m.imdb_id, m.title, m.sort_title, m.year, m.overview,
		m.path, m.poster_path, m.poster_url, m.monitored, m.quality_profile_id, m.release_date,
		m.added_at, m.updated_at`
	episodeStateColumns = `e.id, e.series_id, e.season_number, e.episode_number, e.tmdb_id,
		e.title, e.overview, e.air_date, e.monitored`
)

// MovieFileState is a monitored movie plus the quality of the best file the
// library holds for it. It is the raw material of the wanted computation:
// the store reports what exists, internal/wanted decides what it means.
type MovieFileState struct {
	Movie core.Movie
	// HasFile reports whether any media file is linked to the movie.
	HasFile bool
	// FileQuality is the best (lowest-rank) quality across the movie's files,
	// empty when HasFile is false.
	FileQuality string
}

// MovieFileStates returns every monitored movie with its best file quality.
// Unmonitored movies are excluded here rather than by the caller: nothing
// downstream of this query ever wants them.
func (s *Store) MovieFileStates(ctx context.Context) ([]MovieFileState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+movieStateColumns+`, COALESCE(mf.quality, '')
		FROM movies m
		LEFT JOIN media_files mf ON mf.movie_id = m.id
		WHERE m.monitored = 1
		ORDER BY m.sort_title, m.title`)
	if err != nil {
		return nil, fmt.Errorf("store: movie file states: %w", err)
	}
	defer rows.Close()

	// A movie with several files (an old file awaiting replacement, say) scans
	// as several rows; keep the best quality across them. QualityRank compares
	// on the global ladder, and store may not import wanted, so the comparison
	// is spelled out here the same way EpisodeCountsBySeries spells out its
	// EXISTS.
	byID := map[int64]*MovieFileState{}
	order := []int64{}
	for rows.Next() {
		var quality string
		m, err := scanMovieWith(rows, &quality)
		if err != nil {
			return nil, fmt.Errorf("store: scan movie file state: %w", err)
		}
		st, ok := byID[m.ID]
		if !ok {
			st = &MovieFileState{Movie: *m}
			byID[m.ID] = st
			order = append(order, m.ID)
		}
		if quality == "" {
			continue
		}
		if !st.HasFile || core.QualityRank(quality) < core.QualityRank(st.FileQuality) {
			st.HasFile = true
			st.FileQuality = quality
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: movie file states: %w", err)
	}

	out := make([]MovieFileState, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

// EpisodeFileState is a monitored episode plus the quality of the best file
// the library holds for it. See MovieFileState.
type EpisodeFileState struct {
	Episode     core.Episode
	SeriesTitle string
	// SeriesProfileID is the series' quality_profile_id: episodes carry no
	// profile of their own, so the wanted computation resolves the series'.
	SeriesProfileID int64
	HasFile         bool
	FileQuality     string
}

// EpisodeFileStates returns every monitored episode with its best file
// quality. Per SPEC §7 the cascade is a bulk update, not a lock, so the
// episode's own flag is the whole monitored test: an unmonitored series has
// already pushed its flag down to every episode.
func (s *Store) EpisodeFileStates(ctx context.Context) ([]EpisodeFileState, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+episodeStateColumns+`, s.title, s.quality_profile_id, COALESCE(mf.quality, '')
		FROM episodes e
		JOIN series s ON s.id = e.series_id
		LEFT JOIN episode_files ef ON ef.episode_id = e.id
		LEFT JOIN media_files mf ON mf.id = ef.media_file_id
		WHERE e.monitored = 1
		ORDER BY s.sort_title, s.title, e.season_number, e.episode_number`)
	if err != nil {
		return nil, fmt.Errorf("store: episode file states: %w", err)
	}
	defer rows.Close()

	byID := map[int64]*EpisodeFileState{}
	order := []int64{}
	for rows.Next() {
		var (
			seriesTitle, quality string
			profileID            int64
		)
		e, err := scanEpisodeWith(rows, &seriesTitle, &profileID, &quality)
		if err != nil {
			return nil, fmt.Errorf("store: scan episode file state: %w", err)
		}
		st, ok := byID[e.ID]
		if !ok {
			st = &EpisodeFileState{Episode: *e, SeriesTitle: seriesTitle, SeriesProfileID: profileID}
			byID[e.ID] = st
			order = append(order, e.ID)
		}
		if quality == "" {
			continue
		}
		if !st.HasFile || core.QualityRank(quality) < core.QualityRank(st.FileQuality) {
			st.HasFile = true
			st.FileQuality = quality
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: episode file states: %w", err)
	}

	out := make([]EpisodeFileState, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out, nil
}

// scanMovieWith is scanMovie plus trailing extra columns, for the joined
// queries in this file. It exists because movieColumns is shared with queries
// that carry no extras.
func scanMovieWith(sc scanner, extra ...any) (*core.Movie, error) {
	var (
		m           core.Movie
		releaseDate string
		addedAt     string
		updatedAt   string
	)
	dest := []any{&m.ID, &m.TMDBID, &m.IMDBID, &m.Title, &m.SortTitle, &m.Year, &m.Overview,
		&m.Path, &m.PosterPath, &m.PosterURL, &m.Monitored, &m.QualityProfileID, &releaseDate,
		&addedAt, &updatedAt}
	if err := sc.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	m.ReleaseDate = parseTime(releaseDate)
	m.AddedAt = parseTime(addedAt)
	m.UpdatedAt = parseTime(updatedAt)
	return &m, nil
}

// scanEpisodeWith is scanMovieWith's episode twin.
func scanEpisodeWith(sc scanner, extra ...any) (*core.Episode, error) {
	var (
		e       core.Episode
		airDate string
	)
	dest := []any{&e.ID, &e.SeriesID, &e.SeasonNumber, &e.EpisodeNumber, &e.TMDBID, &e.Title,
		&e.Overview, &airDate, &e.Monitored}
	if err := sc.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	e.AirDate = parseTime(airDate)
	return &e, nil
}
