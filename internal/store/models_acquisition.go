package store

import (
	"database/sql"
	"encoding/json"

	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

type downloadModel struct {
	bun.BaseModel `bun:"table:downloads,alias:download"`

	ID          int64 `bun:",pk,autoincrement"`
	GrabID      int64
	ClientID    int64
	Engine      string
	EngineID    string
	Title       string
	State       string
	Progress    float64
	OutputPath  string
	Error       string
	Size        int64
	BytesDone   int64
	MaxDownRate int64
	MaxUpRate   int64
	CreatedAt   string
	UpdatedAt   string
}

func downloadModelFromCore(d *core.Download) downloadModel {
	return downloadModel{
		ID: d.ID, GrabID: d.GrabID, Engine: d.Engine, EngineID: string(d.EngineID),
		Title: d.Title, State: string(d.State), Progress: d.Progress, OutputPath: d.SavePath,
		Error: d.Error, Size: d.Size, BytesDone: d.BytesDone, MaxDownRate: d.MaxDownRate,
		MaxUpRate: d.MaxUpRate, CreatedAt: formatTime(d.CreatedAt), UpdatedAt: formatTime(d.UpdatedAt),
	}
}

func (m downloadModel) core() core.Download {
	return core.Download{
		ID: m.ID, GrabID: m.GrabID, Engine: m.Engine, EngineID: core.DownloadID(m.EngineID),
		Title: m.Title, State: core.DownloadState(m.State), Progress: m.Progress,
		SavePath: m.OutputPath, Error: m.Error, Size: m.Size, BytesDone: m.BytesDone,
		MaxDownRate: m.MaxDownRate, MaxUpRate: m.MaxUpRate,
		CreatedAt: parseTime(m.CreatedAt), UpdatedAt: parseTime(m.UpdatedAt),
	}
}

type grabModel struct {
	bun.BaseModel `bun:"table:grabs,alias:grab"`

	ID           int64 `bun:",pk,autoincrement"`
	ReleaseID    int64
	MovieID      int64
	SeriesID     int64
	SeasonNumber int
	EpisodeIDs   string `bun:"episode_ids"`
	ReleaseTitle string
	Reason       string
	Status       string
	CreatedAt    string
	LibraryID    sql.NullInt64
}

func grabModelFromCore(g *core.Grab, episodeIDs string) grabModel {
	return grabModel{
		ID: g.GrabID, ReleaseID: g.ReleaseID, MovieID: g.MovieID, SeriesID: g.SeriesID,
		SeasonNumber: g.SeasonNum, EpisodeIDs: episodeIDs, ReleaseTitle: g.ReleaseTitle,
		Reason: g.Reason, Status: g.Status, CreatedAt: formatTime(g.CreatedAt),
		LibraryID: sql.NullInt64{Int64: g.LibraryID, Valid: g.LibraryID != 0},
	}
}

func (m grabModel) core() (core.Grab, error) {
	g := core.Grab{
		GrabInfo: core.GrabInfo{
			GrabID: m.ID, MovieID: m.MovieID, SeriesID: m.SeriesID, SeasonNum: m.SeasonNumber,
			ReleaseTitle: m.ReleaseTitle, LibraryID: m.LibraryID.Int64,
		},
		ReleaseID: m.ReleaseID, Reason: m.Reason, Status: m.Status, CreatedAt: parseTime(m.CreatedAt),
	}
	if m.EpisodeIDs != "" {
		if err := json.Unmarshal([]byte(m.EpisodeIDs), &g.EpisodeIDs); err != nil {
			return core.Grab{}, err
		}
	}
	return g, nil
}
