package store

import (
	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

type mediaFileModel struct {
	bun.BaseModel `bun:"table:media_files,alias:media_file"`

	ID           int64 `bun:",pk,autoincrement"`
	Path         string
	Size         int64
	MovieID      int64
	Quality      string
	Source       string
	Codec        string
	Audio        string
	ReleaseGroup string
	AddedAt      string
	ModifiedAt   string
}

func mediaFileModelFromCore(f *core.MediaFile) mediaFileModel {
	return mediaFileModel{
		ID: f.ID, Path: f.Path, Size: f.Size, MovieID: f.MovieID, Quality: f.Quality,
		Source: f.Source, Codec: f.Codec, Audio: f.Audio, ReleaseGroup: f.ReleaseGroup,
		AddedAt: formatTime(f.AddedAt), ModifiedAt: formatTime(f.ModifiedAt),
	}
}

func (m mediaFileModel) core() core.MediaFile {
	return core.MediaFile{
		ID: m.ID, Path: m.Path, Size: m.Size, MovieID: m.MovieID, Quality: m.Quality,
		Source: m.Source, Codec: m.Codec, Audio: m.Audio, ReleaseGroup: m.ReleaseGroup,
		AddedAt: parseTime(m.AddedAt), ModifiedAt: parseTime(m.ModifiedAt),
	}
}

type episodeFileModel struct {
	bun.BaseModel `bun:"table:episode_files,alias:episode_file"`

	EpisodeID   int64 `bun:",pk"`
	MediaFileID int64 `bun:",pk"`
}
