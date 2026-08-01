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

// ListMediaFiles returns every media file ordered by path.
func (s *Store) ListMediaFiles(ctx context.Context) ([]core.MediaFile, error) {
	return s.queryMediaFiles(ctx, "SELECT "+mediaFileColumns+" FROM media_files ORDER BY path")
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
