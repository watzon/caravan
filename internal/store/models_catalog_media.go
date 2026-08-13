package store

import (
	"github.com/uptrace/bun"
	"github.com/watzon/caravan/internal/core"
)

// catalogMovieModel is the database representation of a movie. Timestamps
// remain strings so reads and writes retain the store's RFC3339Nano/zero-time
// behavior without coupling core.Movie to Bun.
type catalogMovieModel struct {
	bun.BaseModel `bun:"table:movies,alias:movie"`

	ID               int64 `bun:",pk,autoincrement"`
	Provider         string
	ProviderRef      string
	TMDBID           int64  `bun:"tmdb_id"`
	IMDBID           string `bun:"imdb_id"`
	Title            string
	SortTitle        string
	Year             int
	Overview         string
	Path             string
	PosterPath       string
	PosterURL        string
	Monitored        bool
	QualityProfileID int64
	ReleaseDate      string
	DigitalRelease   string
	PhysicalRelease  string
	MinAvailability  string
	AddedAt          string
	UpdatedAt        string
	LibraryID        int64
}

func catalogMovieModelFromCore(m *core.Movie) catalogMovieModel {
	return catalogMovieModel{
		ID: m.ID, Provider: m.Provider, ProviderRef: m.ProviderRef, TMDBID: m.TMDBID,
		IMDBID: m.IMDBID, Title: m.Title, SortTitle: m.SortTitle, Year: m.Year,
		Overview: m.Overview, Path: m.Path, PosterPath: m.PosterPath, PosterURL: m.PosterURL,
		Monitored: m.Monitored, QualityProfileID: m.QualityProfileID,
		ReleaseDate: formatTime(m.ReleaseDate), DigitalRelease: formatTime(m.DigitalRelease),
		PhysicalRelease: formatTime(m.PhysicalRelease), MinAvailability: m.MinAvailability,
		AddedAt: formatTime(m.AddedAt), UpdatedAt: formatTime(m.UpdatedAt), LibraryID: m.LibraryID,
	}
}

func (m catalogMovieModel) core() core.Movie {
	return core.Movie{
		ID: m.ID, Provider: m.Provider, ProviderRef: m.ProviderRef, TMDBID: m.TMDBID,
		IMDBID: m.IMDBID, Title: m.Title, SortTitle: m.SortTitle, Year: m.Year,
		Overview: m.Overview, Path: m.Path, PosterPath: m.PosterPath, PosterURL: m.PosterURL,
		Monitored: m.Monitored, QualityProfileID: m.QualityProfileID,
		ReleaseDate: parseTime(m.ReleaseDate), DigitalRelease: parseTime(m.DigitalRelease),
		PhysicalRelease: parseTime(m.PhysicalRelease), MinAvailability: m.MinAvailability,
		AddedAt: parseTime(m.AddedAt), UpdatedAt: parseTime(m.UpdatedAt), LibraryID: m.LibraryID,
	}
}

// catalogSeriesModel is the database representation of a series. As with
// movies, text timestamps are converted explicitly at the persistence edge.
type catalogSeriesModel struct {
	bun.BaseModel `bun:"table:series,alias:series"`

	ID               int64 `bun:",pk,autoincrement"`
	Kind             string
	Provider         string
	ProviderRef      string
	TMDBID           int64 `bun:"tmdb_id"`
	StashID          string
	TVDBID           int64  `bun:"tvdb_id"`
	IMDBID           string `bun:"imdb_id"`
	Title            string
	SortTitle        string
	Year             int
	Overview         string
	Status           string
	Path             string
	PosterPath       string
	PosterURL        string
	Monitored        bool
	QualityProfileID int64
	FirstAired       string
	AddedAt          string
	UpdatedAt        string
	LibraryID        int64
}

func catalogSeriesModelFromCore(sr *core.Series) catalogSeriesModel {
	return catalogSeriesModel{
		ID: sr.ID, Kind: sr.Kind, Provider: sr.Provider, ProviderRef: sr.ProviderRef,
		TMDBID: sr.TMDBID, StashID: sr.StashID, TVDBID: sr.TVDBID, IMDBID: sr.IMDBID,
		Title: sr.Title, SortTitle: sr.SortTitle, Year: sr.Year, Overview: sr.Overview,
		Status: sr.Status, Path: sr.Path, PosterPath: sr.PosterPath, PosterURL: sr.PosterURL,
		Monitored: sr.Monitored, QualityProfileID: sr.QualityProfileID,
		FirstAired: formatTime(sr.FirstAired), AddedAt: formatTime(sr.AddedAt),
		UpdatedAt: formatTime(sr.UpdatedAt), LibraryID: sr.LibraryID,
	}
}

func (m catalogSeriesModel) core() core.Series {
	return core.Series{
		ID: m.ID, Kind: m.Kind, Provider: m.Provider, ProviderRef: m.ProviderRef,
		TMDBID: m.TMDBID, StashID: m.StashID, TVDBID: m.TVDBID, IMDBID: m.IMDBID,
		Title: m.Title, SortTitle: m.SortTitle, Year: m.Year, Overview: m.Overview,
		Status: m.Status, Path: m.Path, PosterPath: m.PosterPath, PosterURL: m.PosterURL,
		Monitored: m.Monitored, QualityProfileID: m.QualityProfileID,
		FirstAired: parseTime(m.FirstAired), AddedAt: parseTime(m.AddedAt),
		UpdatedAt: parseTime(m.UpdatedAt), LibraryID: m.LibraryID,
	}
}

type catalogSeasonModel struct {
	bun.BaseModel `bun:"table:seasons,alias:season"`

	ID           int64 `bun:",pk,autoincrement"`
	SeriesID     int64
	SeasonNumber int `bun:"season_number"`
	Title        string
	Overview     string
	PosterPath   string
	AirDate      string
	Monitored    bool
}

func catalogSeasonModelFromCore(se *core.Season) catalogSeasonModel {
	return catalogSeasonModel{
		ID: se.ID, SeriesID: se.SeriesID, SeasonNumber: se.Number, Title: se.Title,
		Overview: se.Overview, PosterPath: se.PosterPath, AirDate: formatTime(se.AirDate),
		Monitored: se.Monitored,
	}
}

func (m catalogSeasonModel) core() core.Season {
	return core.Season{
		ID: m.ID, SeriesID: m.SeriesID, Number: m.SeasonNumber, Title: m.Title,
		Overview: m.Overview, PosterPath: m.PosterPath, AirDate: parseTime(m.AirDate),
		Monitored: m.Monitored,
	}
}

type catalogEpisodeModel struct {
	bun.BaseModel `bun:"table:episodes,alias:episode"`

	ID             int64 `bun:",pk,autoincrement"`
	SeriesID       int64
	SeasonNumber   int
	EpisodeNumber  int
	AbsoluteNumber int
	TMDBID         int64 `bun:"tmdb_id"`
	StashID        string
	Title          string
	Overview       string
	AirDate        string
	Monitored      bool
	Scene          string
}

func catalogEpisodeModelFromCore(e *core.Episode) (catalogEpisodeModel, error) {
	scene, err := encodeScene(e.Scene)
	if err != nil {
		return catalogEpisodeModel{}, err
	}
	return catalogEpisodeModel{
		ID: e.ID, SeriesID: e.SeriesID, SeasonNumber: e.SeasonNumber,
		EpisodeNumber: e.EpisodeNumber, AbsoluteNumber: e.AbsoluteNumber,
		TMDBID: e.TMDBID, StashID: e.StashID, Title: e.Title, Overview: e.Overview,
		AirDate: formatTime(e.AirDate), Monitored: e.Monitored, Scene: scene,
	}, nil
}

func (m catalogEpisodeModel) core() (core.Episode, error) {
	scene, err := decodeScene(m.Scene)
	if err != nil {
		return core.Episode{}, err
	}
	return core.Episode{
		ID: m.ID, SeriesID: m.SeriesID, SeasonNumber: m.SeasonNumber,
		EpisodeNumber: m.EpisodeNumber, AbsoluteNumber: m.AbsoluteNumber,
		TMDBID: m.TMDBID, StashID: m.StashID, Title: m.Title, Overview: m.Overview,
		AirDate: parseTime(m.AirDate), Monitored: m.Monitored, Scene: scene,
	}, nil
}
