package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/watzon/caravan/internal/core"
)

const mediaFileColumns = `id, path, size, movie_id, quality, source, codec, audio,
	release_group, added_at, modified_at`

// mediaFileColumnsQualified is mediaFileColumns for queries that join another
// table; only `id` is actually ambiguous, but qualifying all of them keeps the
// two lists diffable.
const mediaFileColumnsQualified = `mf.id, mf.path, mf.size, mf.movie_id, mf.quality, mf.source,
	mf.codec, mf.audio, mf.release_group, mf.added_at, mf.modified_at`

// UpsertMediaFile inserts or updates a media file and writes back the assigned
// ID. Identity is the storage-root-relative Path: the file on disk is the
// source of truth, so its path is what a rescan can re-derive.
func (s *Store) UpsertMediaFile(ctx context.Context, f *core.MediaFile) error {
	ts := now()
	if f.AddedAt.IsZero() {
		f.AddedAt = ts
	}
	if f.ModifiedAt.IsZero() {
		f.ModifiedAt = ts
	}

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO media_files (path, size, movie_id, quality, source, codec, audio,
			release_group, added_at, modified_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (path) DO UPDATE SET
			size = excluded.size, movie_id = excluded.movie_id, quality = excluded.quality,
			source = excluded.source, codec = excluded.codec, audio = excluded.audio,
			release_group = excluded.release_group, modified_at = excluded.modified_at`,
		f.Path, f.Size, f.MovieID, f.Quality, f.Source, f.Codec, f.Audio, f.ReleaseGroup,
		formatTime(f.AddedAt), formatTime(f.ModifiedAt))
	if err != nil {
		return fmt.Errorf("store: upsert media file %q: %w", f.Path, err)
	}
	if f.ID != 0 {
		return nil
	}
	if err := s.db.QueryRowContext(ctx, "SELECT id FROM media_files WHERE path = ?", f.Path).Scan(&f.ID); err != nil {
		return fmt.Errorf("store: upsert media file %q: %w", f.Path, err)
	}
	return nil
}

// GetMediaFileByPath returns the media file at the given relative path, or
// ErrNotFound.
func (s *Store) GetMediaFileByPath(ctx context.Context, path string) (*core.MediaFile, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+mediaFileColumns+" FROM media_files WHERE path = ?", path)
	f, err := scanMediaFile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: media file %q: %w", path, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get media file %q: %w", path, err)
	}
	return f, nil
}

// GetMediaFile returns the media file with the given id, or ErrNotFound.
// The convert queue addresses files by id rather than by path, because the
// path is exactly what a conversion changes.
func (s *Store) GetMediaFile(ctx context.Context, id int64) (*core.MediaFile, error) {
	row := s.db.QueryRowContext(ctx, "SELECT "+mediaFileColumns+" FROM media_files WHERE id = ?", id)
	f, err := scanMediaFile(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("store: media file %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("store: get media file %d: %w", id, err)
	}
	return f, nil
}

// GetMediaFileLibrary resolves the one library that owns a media file: its
// kind, and the `libraries` row the owning movie or series names. A zero
// library id is the usual "names none", which callers resolve through the
// kind's default library (see core.Movie.LibraryID) — the DLNA tree needs both
// halves, because with several libraries per kind the kind alone no longer
// says whose dlna_visible flag applies.
//
// Files with no owner, or with conflicting movie/episode owners, or with
// episode owners across two libraries, return ErrNotFound so callers fail
// closed instead of choosing one link.
func (s *Store) GetMediaFileLibrary(ctx context.Context, id int64) (int64, string, error) {
	fail := func(err error) (int64, string, error) { return 0, "", err }
	rows, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT mf.movie_id, m.library_id, s.kind, s.library_id
		FROM media_files mf
		LEFT JOIN movies m ON m.id = mf.movie_id
		LEFT JOIN episode_files ef ON ef.media_file_id = mf.id
		LEFT JOIN episodes e ON e.id = ef.episode_id
		LEFT JOIN series s ON s.id = e.series_id
		WHERE mf.id = ?`, id)
	if err != nil {
		return fail(fmt.Errorf("store: get media file %d library: %w", id, err))
	}
	defer rows.Close()

	found := false
	movieOwned := false
	movieLibrary := int64(0)
	episodeKind := ""
	episodeLibrary := int64(0)
	for rows.Next() {
		found = true
		var (
			movieID     int64
			movieLibID  sql.NullInt64
			seriesKind  sql.NullString
			seriesLibID sql.NullInt64
		)
		if err := rows.Scan(&movieID, &movieLibID, &seriesKind, &seriesLibID); err != nil {
			return fail(fmt.Errorf("store: scan media file %d library: %w", id, err))
		}
		movieOwned = movieID != 0
		movieLibrary = movieLibID.Int64
		if !seriesKind.Valid {
			continue
		}
		kind := core.LibraryKindForSeries(seriesKind.String)
		if episodeKind != "" && (episodeKind != kind || episodeLibrary != seriesLibID.Int64) {
			return fail(fmt.Errorf("store: media file %d library: %w", id, ErrNotFound))
		}
		episodeKind, episodeLibrary = kind, seriesLibID.Int64
	}
	if err := rows.Err(); err != nil {
		return fail(fmt.Errorf("store: get media file %d library: %w", id, err))
	}
	if !found || (movieOwned && episodeKind != "") || (!movieOwned && episodeKind == "") {
		return fail(fmt.Errorf("store: media file %d library: %w", id, ErrNotFound))
	}
	if movieOwned {
		return movieLibrary, core.LibraryKindMovie, nil
	}
	return episodeLibrary, episodeKind, nil
}

// UpdateMediaFileConverted repoints a media file at the file ffmpeg produced
// (PLAN phase 4, task 4).
//
// It updates in place instead of insert-plus-delete because the row id is what
// episode_files links against: a multi-episode file that lost its id on
// conversion would silently detach from every episode it covers.
//
// Quality travels with the rest: a conversion may downscale, and a row still
// claiming the source's resolution is one the TV compatibility check keeps
// condemning after the file it describes has been made compatible.
func (s *Store) UpdateMediaFileConverted(ctx context.Context, id int64, path string, size int64, quality, codec, audio string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE media_files
		SET path = ?, size = ?, quality = ?, codec = ?, audio = ?, modified_at = ?
		WHERE id = ?`, path, size, quality, codec, audio, formatTime(now()), id)
	if err != nil {
		return fmt.Errorf("store: update converted media file %d: %w", id, err)
	}
	return affectedOne(res, "update converted media file", id)
}

// UpdateMediaFilePath repoints one media file row at the location a move put
// its file. The row id survives on purpose — episode links reference it, and
// a delete-and-reinsert would orphan them (the same reason
// UpdateMediaFileConverted updates in place).
func (s *Store) UpdateMediaFilePath(ctx context.Context, id int64, path string) error {
	res, err := s.db.ExecContext(ctx, `
		UPDATE media_files SET path = ?, modified_at = ? WHERE id = ?`,
		path, formatTime(now()), id)
	if err != nil {
		return fmt.Errorf("store: update media file %d path: %w", id, err)
	}
	return affectedOne(res, "update media file path", id)
}

// ListMediaFiles returns every media file ordered by path.
func (s *Store) ListMediaFiles(ctx context.Context) ([]core.MediaFile, error) {
	return s.queryMediaFiles(ctx, "SELECT "+mediaFileColumns+" FROM media_files ORDER BY path")
}

// ConversionCandidate is a current library file with no queued or running
// conversion. LibraryKind lets shared API surfaces apply the adult visibility
// rule without an ownership query per file.
type ConversionCandidate struct {
	File        core.MediaFile
	LibraryKind string
}

// ListConversionCandidates returns owned media files that are free to queue,
// ordered by path. Compatibility is profile-dependent and belongs to the API;
// this query only resolves ownership and excludes open conversion rows.
//
// Ownership fails closed, matching GetMediaFileLibrary: unowned files,
// files attached to both a movie and an episode, and files attached across TV
// and adult series do not become shared-surface candidates.
func (s *Store) ListConversionCandidates(ctx context.Context) ([]ConversionCandidate, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT `+mediaFileColumnsQualified+`,
			COUNT(ef.episode_id),
			COUNT(DISTINCT CASE WHEN s.kind = ? THEN ? ELSE ? END),
			MAX(CASE WHEN s.kind = ? THEN 1 ELSE 0 END)
		FROM media_files mf
		LEFT JOIN episode_files ef ON ef.media_file_id = mf.id
		LEFT JOIN episodes e ON e.id = ef.episode_id
		LEFT JOIN series s ON s.id = e.series_id
		WHERE NOT EXISTS (
			SELECT 1
			FROM conversions c
			WHERE c.media_file_id = mf.id AND c.status IN (?, ?)
		)
		GROUP BY mf.id
		ORDER BY mf.path`,
		core.SeriesKindAdult, core.LibraryKindAdult, core.LibraryKindTV,
		core.SeriesKindAdult, core.ConversionQueued, core.ConversionRunning)
	if err != nil {
		return nil, fmt.Errorf("store: list conversion candidates: %w", err)
	}
	defer rows.Close()

	out := []ConversionCandidate{}
	for rows.Next() {
		var (
			candidate        ConversionCandidate
			addedAt          string
			modifiedAt       string
			episodeCount     int
			libraryKindCount int
			hasAdult         int
		)
		err := rows.Scan(
			&candidate.File.ID, &candidate.File.Path, &candidate.File.Size,
			&candidate.File.MovieID, &candidate.File.Quality, &candidate.File.Source,
			&candidate.File.Codec, &candidate.File.Audio, &candidate.File.ReleaseGroup,
			&addedAt, &modifiedAt, &episodeCount, &libraryKindCount, &hasAdult,
		)
		if err != nil {
			return nil, fmt.Errorf("store: scan conversion candidate: %w", err)
		}
		switch {
		case candidate.File.MovieID != 0 && episodeCount == 0:
			candidate.LibraryKind = core.LibraryKindMovie
		case candidate.File.MovieID == 0 && episodeCount > 0 && libraryKindCount == 1:
			candidate.LibraryKind = core.LibraryKindTV
			if hasAdult != 0 {
				candidate.LibraryKind = core.LibraryKindAdult
			}
		default:
			continue
		}
		candidate.File.AddedAt = parseTime(addedAt)
		candidate.File.ModifiedAt = parseTime(modifiedAt)
		out = append(out, candidate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list conversion candidates: %w", err)
	}
	return out, nil
}

// ListMediaFilesForMovie returns a movie's files ordered by path.
func (s *Store) ListMediaFilesForMovie(ctx context.Context, movieID int64) ([]core.MediaFile, error) {
	return s.queryMediaFiles(ctx,
		"SELECT "+mediaFileColumns+" FROM media_files WHERE movie_id = ? ORDER BY path", movieID)
}

// ListMediaFilesForEpisode returns the files linked to an episode. An episode
// usually has one, but a multi-episode file appears here for each episode it
// covers (SPEC §7).
func (s *Store) ListMediaFilesForEpisode(ctx context.Context, episodeID int64) ([]core.MediaFile, error) {
	return s.queryMediaFiles(ctx, `
		SELECT `+mediaFileColumns+`
		FROM media_files
		JOIN episode_files ON episode_files.media_file_id = media_files.id
		WHERE episode_files.episode_id = ?
		ORDER BY media_files.path`, episodeID)
}

// EpisodeMediaFile pairs one episode with one of the files that covers it. A
// multi-episode file (S01E01E02) appears once per episode, which is what makes
// it correct to count these rows as "playable things under this season".
type EpisodeMediaFile struct {
	EpisodeID     int64
	SeasonNumber  int
	EpisodeNumber int
	File          core.MediaFile
}

// ListEpisodeMediaFilesForSeries returns every episode/file pair in a series,
// ordered by season, episode and path.
//
// One query rather than ListEpisodes plus a ListMediaFilesForEpisode per row:
// the DLNA browse needs both the per-season counts and the per-season files,
// and a per-episode round trip would turn one browse into a query per episode.
func (s *Store) ListEpisodeMediaFilesForSeries(ctx context.Context, seriesID int64) ([]EpisodeMediaFile, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT e.id, e.season_number, e.episode_number, `+mediaFileColumnsQualified+`
		FROM media_files mf
		JOIN episode_files ef ON ef.media_file_id = mf.id
		JOIN episodes e ON e.id = ef.episode_id
		WHERE e.series_id = ?
		ORDER BY e.season_number, e.episode_number, mf.path`, seriesID)
	if err != nil {
		return nil, fmt.Errorf("store: list episode media files for series %d: %w", seriesID, err)
	}
	defer rows.Close()

	out := []EpisodeMediaFile{}
	for rows.Next() {
		var (
			pair       EpisodeMediaFile
			addedAt    string
			modifiedAt string
		)
		err := rows.Scan(&pair.EpisodeID, &pair.SeasonNumber, &pair.EpisodeNumber,
			&pair.File.ID, &pair.File.Path, &pair.File.Size, &pair.File.MovieID,
			&pair.File.Quality, &pair.File.Source, &pair.File.Codec, &pair.File.Audio,
			&pair.File.ReleaseGroup, &addedAt, &modifiedAt)
		if err != nil {
			return nil, fmt.Errorf("store: scan episode media file: %w", err)
		}
		pair.File.AddedAt = parseTime(addedAt)
		pair.File.ModifiedAt = parseTime(modifiedAt)
		out = append(out, pair)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list episode media files for series %d: %w", seriesID, err)
	}
	return out, nil
}

// DeleteMediaFileByPath removes the media file row and, by cascade, its
// episode links. The file on disk is untouched: deleting media is a library
// operation, not a store one.
func (s *Store) DeleteMediaFileByPath(ctx context.Context, path string) error {
	if _, err := s.db.ExecContext(ctx, "DELETE FROM media_files WHERE path = ?", path); err != nil {
		return fmt.Errorf("store: delete media file %q: %w", path, err)
	}
	return nil
}

// LinkEpisodeFile links a media file to an episode. Linking twice is a no-op,
// which keeps rescans and re-imports idempotent.
func (s *Store) LinkEpisodeFile(ctx context.Context, episodeID, mediaFileID int64) error {
	_, err := s.db.ExecContext(ctx,
		"INSERT INTO episode_files (episode_id, media_file_id) VALUES (?, ?) ON CONFLICT DO NOTHING",
		episodeID, mediaFileID)
	if err != nil {
		return fmt.Errorf("store: link episode %d to media file %d: %w", episodeID, mediaFileID, err)
	}
	return nil
}

func (s *Store) queryMediaFiles(ctx context.Context, query string, args ...any) ([]core.MediaFile, error) {
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: list media files: %w", err)
	}
	defer rows.Close()

	out := []core.MediaFile{}
	for rows.Next() {
		f, err := scanMediaFile(rows)
		if err != nil {
			return nil, fmt.Errorf("store: scan media file: %w", err)
		}
		out = append(out, *f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: list media files: %w", err)
	}
	return out, nil
}

func scanMediaFile(sc scanner) (*core.MediaFile, error) {
	var (
		f          core.MediaFile
		addedAt    string
		modifiedAt string
	)
	err := sc.Scan(&f.ID, &f.Path, &f.Size, &f.MovieID, &f.Quality, &f.Source, &f.Codec,
		&f.Audio, &f.ReleaseGroup, &addedAt, &modifiedAt)
	if err != nil {
		return nil, err
	}
	f.AddedAt = parseTime(addedAt)
	f.ModifiedAt = parseTime(modifiedAt)
	return &f, nil
}
