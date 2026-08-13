package store

import (
	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

type eventModel struct {
	bun.BaseModel `bun:"table:events,alias:event"`

	ID        int64 `bun:",pk,autoincrement"`
	Level     string
	Category  string
	Message   string
	Detail    string
	MovieID   int64
	SeriesID  int64
	CreatedAt string
}

func eventModelFromCore(e *core.Event) eventModel {
	return eventModel{
		ID: e.ID, Level: e.Level, Category: e.Category, Message: e.Message,
		Detail: e.Detail, MovieID: e.MovieID, SeriesID: e.SeriesID,
		CreatedAt: formatTime(e.CreatedAt),
	}
}

func (m eventModel) core() core.Event {
	return core.Event{
		ID: m.ID, Level: m.Level, Category: m.Category, Message: m.Message,
		Detail: m.Detail, MovieID: m.MovieID, SeriesID: m.SeriesID,
		CreatedAt: parseTime(m.CreatedAt),
	}
}

type conversionModel struct {
	bun.BaseModel `bun:"table:conversions,alias:conversion"`

	ID          int64 `bun:",pk,autoincrement"`
	MediaFileID int64
	SourcePath  string
	OutputPath  string
	Strategy    string
	ProfileID   string
	Status      string
	Error       string
	CreatedAt   string
	UpdatedAt   string
}

func conversionModelFromCore(c *core.Conversion) conversionModel {
	return conversionModel{
		ID: c.ID, MediaFileID: c.MediaFileID, SourcePath: c.SourcePath,
		OutputPath: c.OutputPath, Strategy: c.Strategy, ProfileID: c.ProfileID,
		Status: c.Status, Error: c.Error, CreatedAt: formatTime(c.CreatedAt),
		UpdatedAt: formatTime(c.UpdatedAt),
	}
}

func (m conversionModel) core() core.Conversion {
	return core.Conversion{
		ID: m.ID, MediaFileID: m.MediaFileID, SourcePath: m.SourcePath,
		OutputPath: m.OutputPath, Strategy: m.Strategy, ProfileID: m.ProfileID,
		Status: m.Status, Error: m.Error, CreatedAt: parseTime(m.CreatedAt),
		UpdatedAt: parseTime(m.UpdatedAt),
	}
}

type storageMigrationModel struct {
	bun.BaseModel `bun:"table:storage_migrations,alias:storage_migration"`

	ID         int64 `bun:",pk,autoincrement"`
	SourceRoot string
	TargetRoot string
	Status     string
	FilesTotal int64
	FilesDone  int64
	BytesTotal int64
	BytesDone  int64
	Error      string
	CreatedAt  string
	UpdatedAt  string
}

func storageMigrationModelFromCore(m *core.StorageMigration) storageMigrationModel {
	return storageMigrationModel{
		ID: m.ID, SourceRoot: m.SourceRoot, TargetRoot: m.TargetRoot, Status: m.Status,
		FilesTotal: m.FilesTotal, FilesDone: m.FilesDone, BytesTotal: m.BytesTotal,
		BytesDone: m.BytesDone, Error: m.Error, CreatedAt: formatTime(m.CreatedAt),
		UpdatedAt: formatTime(m.UpdatedAt),
	}
}

func (m storageMigrationModel) core() core.StorageMigration {
	return core.StorageMigration{
		ID: m.ID, SourceRoot: m.SourceRoot, TargetRoot: m.TargetRoot, Status: m.Status,
		FilesTotal: m.FilesTotal, FilesDone: m.FilesDone, BytesTotal: m.BytesTotal,
		BytesDone: m.BytesDone, Error: m.Error, CreatedAt: parseTime(m.CreatedAt),
		UpdatedAt: parseTime(m.UpdatedAt),
	}
}
