package store

import "github.com/uptrace/bun"

// libraryStoreModel preserves nullable routing/profile overrides and the
// providers JSON text until the row crosses the store boundary.
type libraryStoreModel struct {
	bun.BaseModel `bun:"table:libraries,alias:library"`

	ID               int64 `bun:",pk,autoincrement"`
	Kind             string
	Name             string
	Icon             string
	RootPath         string
	DLNAVisible      bool `bun:"dlna_visible"`
	RouteTorrent     *string
	RouteUsenet      *string
	QualityProfileID *int64
	Provider         string
	Providers        string
	IsDefault        bool
	Active           bool
	Restricted       bool
}

// libraryIndexerStoreModel keeps categories as nullable JSON text: nil means
// inherit, while a pointer to "[]" means an explicit unfiltered override.
type libraryIndexerStoreModel struct {
	bun.BaseModel `bun:"table:library_indexers,alias:library_indexer"`

	LibraryID  int64 `bun:",pk"`
	IndexerID  int64 `bun:",pk"`
	Enabled    bool
	Categories *string
}

type libraryAccessStoreModel struct {
	bun.BaseModel `bun:"table:library_access,alias:library_access"`

	LibraryID int64 `bun:",pk"`
	UserID    int64 `bun:",pk"`
}

// qualityProfileStoreModel keeps JSON and timestamps in their established TEXT
// representation so decoding errors and malformed-time fallback stay at the
// persistence boundary.
type qualityProfileStoreModel struct {
	bun.BaseModel `bun:"table:quality_profiles,alias:quality_profile"`

	ID                     int64 `bun:",pk,autoincrement"`
	Name                   string
	Cutoff                 string
	Items                  string
	UpgradeAllowed         bool
	PreferredSources       string
	ProperRepackPreference string
	MinSeeders             int
	MinSizeMB              int64
	MaxSizeMB              int64
	CustomFormats          string
	TVProfile              string `bun:"tv_profile"`
	TVCompatibilityPolicy  string
	CreatedAt              string
	UpdatedAt              string
}

// These narrow models let profile assignments use Bun builders without
// coupling the full movie, series, and library persistence representations.
type movieProfileAssignmentStoreModel struct {
	bun.BaseModel `bun:"table:movies,alias:movie"`

	ID               int64 `bun:",pk"`
	QualityProfileID int64
}

type seriesProfileAssignmentStoreModel struct {
	bun.BaseModel `bun:"table:series,alias:series"`

	ID               int64 `bun:",pk"`
	QualityProfileID int64
}

type libraryProfileAssignmentStoreModel struct {
	bun.BaseModel `bun:"table:libraries,alias:library"`

	ID               int64 `bun:",pk"`
	QualityProfileID *int64
}

// requestStoreModel preserves SQL NULL for optional poster/seasons fields and
// stores timestamps as text for the existing formatTime/parseTime contract.
type requestStoreModel struct {
	bun.BaseModel `bun:"table:requests,alias:request"`

	ID              int64 `bun:",pk,autoincrement"`
	MediaType       string
	TMDBID          int64 `bun:"tmdb_id"`
	StashID         string
	Title           string
	Year            int
	PosterPath      *string
	Seasons         *string
	MinAvailability string
	Status          string
	RequestedBy     int64
	CreatedAt       string
	UpdatedAt       string
}
