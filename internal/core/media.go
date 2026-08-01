// Package core holds Caravan's shared domain vocabulary: the library types
// every other package speaks, the release-parser output shape, and the
// metadata-provider interface. It has no dependencies outside the standard
// library and performs no I/O.
//
// Path convention (SPEC §1.2, pillar 3): every path field on these types is
// RELATIVE to the configured storage root. Absolute paths exist only at the
// filesystem boundary, never in the database and never on these structs.
package core

import "time"

// Movie is a library movie: a wanted item, an owned item, or both.
type Movie struct {
	ID        int64
	TMDBID    int64
	IMDBID    string
	Title     string
	SortTitle string
	Year      int
	Overview  string
	// Path is the movie's folder, relative to the storage root
	// ("Movies/Big Buck Bunny (2008)"). Empty until the movie is organized.
	Path string
	// PosterPath is the stored poster, relative to the storage root. Empty
	// when no poster has been written yet.
	PosterPath string
	// Monitored controls whether Caravan keeps searching for this movie.
	Monitored bool
	// QualityProfileID references quality_profiles.id; 0 means "use the
	// default profile".
	QualityProfileID int64
	// ReleaseDate is the theatrical/digital release date, zero when unknown.
	ReleaseDate time.Time
	AddedAt     time.Time
	UpdatedAt   time.Time
}

// Series is a library TV series.
type Series struct {
	ID        int64
	TMDBID    int64
	TVDBID    int64
	IMDBID    string
	Title     string
	SortTitle string
	Year      int
	Overview  string
	// Status is the provider's series status ("Continuing", "Ended", …).
	Status string
	// Path is the series folder, relative to the storage root
	// ("TV/Planet Earth II (2016)"). Empty until the series is organized.
	Path string
	// PosterPath is the stored poster, relative to the storage root.
	PosterPath string
	// Monitored is the series-level flag. SPEC §7: seasons and episodes carry
	// their own flags; setting this one cascades as a bulk update, not a lock.
	Monitored bool
	// QualityProfileID references quality_profiles.id; 0 means "use the
	// default profile".
	QualityProfileID int64
	// FirstAired is the first air date, zero when unknown.
	FirstAired time.Time
	AddedAt    time.Time
	UpdatedAt  time.Time
}

// Season is one season of a Series. Season 0 is the specials season, following
// the TMDB/Jellyfin convention (SPEC §7).
type Season struct {
	ID       int64
	SeriesID int64
	// Number is the season number; 0 for specials.
	Number   int
	Title    string
	Overview string
	// PosterPath is the stored season poster, relative to the storage root.
	PosterPath string
	// AirDate is the season premiere date, zero when unknown.
	AirDate   time.Time
	Monitored bool
}

// Episode is one episode of a Series. Episodes are addressed by
// (SeriesID, SeasonNumber, EpisodeNumber) rather than by season row id so a
// parsed filename maps to a row without an intermediate lookup.
type Episode struct {
	ID            int64
	SeriesID      int64
	SeasonNumber  int
	EpisodeNumber int
	TMDBID        int64
	Title         string
	Overview      string
	// AirDate is the broadcast date, zero when unknown or unaired.
	AirDate   time.Time
	Monitored bool
}

// MediaFile is one imported file on disk.
//
// A file belongs either to a movie (MovieID set) or to one or more episodes
// (linked through the episode_files join table, because a single file can
// cover S01E01E02 — SPEC §7). It never belongs to both.
type MediaFile struct {
	ID int64
	// Path is the file, relative to the storage root
	// ("Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008).mp4"). Unique.
	Path string
	// Size is the file size in bytes.
	Size int64
	// MovieID references movies.id for movie files; 0 for episode files.
	MovieID int64
	// Quality is one of the Quality* constants.
	Quality string
	// Source is one of the Source* constants.
	Source string
	// Codec, Audio and ReleaseGroup mirror the parsed release tags.
	Codec        string
	Audio        string
	ReleaseGroup string
	AddedAt      time.Time
	ModifiedAt   time.Time
}

// QualityProfile is a quality ladder plus a cutoff: Caravan keeps searching
// for an item until it owns a file at or above Cutoff (SPEC §9).
type QualityProfile struct {
	ID   int64
	Name string
	// Cutoff is the quality at which upgrading stops; one of the Quality*
	// constants.
	Cutoff string
	// Items are the acceptable qualities, best-first.
	Items []string
	// UpgradeAllowed disables upgrade searches entirely when false.
	UpgradeAllowed bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// Event levels for the activity feed.
const (
	EventLevelInfo  = "info"
	EventLevelWarn  = "warn"
	EventLevelError = "error"
)

// Event is one entry in the activity/history feed (SPEC §7, `events`). Events
// are history: losing them costs the user context, never media.
type Event struct {
	ID int64
	// Level is one of the EventLevel* constants.
	Level string
	// Category groups events in the UI ("scan", "import", "grab", …).
	Category string
	// Message is the one-line human-readable summary.
	Message string
	// Detail is optional long-form context (a path, an error, a JSON blob).
	Detail string
	// MovieID and SeriesID link the event to a library item; 0 when unrelated.
	MovieID   int64
	SeriesID  int64
	CreatedAt time.Time
}

// UnmatchedFile is a media file the scanner found but could not confidently
// match. SPEC §13 requires these to be visible rather than silently dropped:
// they park here with the parser's best guess and a reason, and the user
// resolves them from the scan-review screen.
type UnmatchedFile struct {
	ID int64
	// Path is the file, relative to the storage root. Unique.
	Path string
	// Size is the file size in bytes.
	Size int64
	// Parsed is the parser's best guess at what the file is.
	Parsed ParsedRelease
	// Reason explains why it was not matched ("no metadata match",
	// "low parser confidence", …).
	Reason string
	SeenAt time.Time
}
