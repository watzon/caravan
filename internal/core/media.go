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
	ID int64
	// Provider and ProviderRef are the provider that identified this row and
	// that provider's own id — the item is PINNED to it; a refresh asks this
	// provider and no other. Empty on a row no provider has identified.
	Provider    string
	ProviderRef string
	TMDBID      int64
	IMDBID      string
	Title       string
	SortTitle   string
	Year        int
	Overview    string
	// Path is the movie's folder, relative to the storage root
	// ("Movies/Big Buck Bunny (2008)"). Empty until the movie is organized.
	Path string
	// PosterPath is the stored poster, relative to the storage root. Empty
	// when no poster has been written yet.
	PosterPath string
	// PosterURL is the provider's poster, an absolute URL. It is what the UI
	// shows while PosterPath is still empty — an added movie has no folder on
	// disk, so it cannot have a local poster yet.
	PosterURL string
	// Monitored controls whether Caravan keeps searching for this movie.
	Monitored bool
	// QualityProfileID references quality_profiles.id; 0 means "use the
	// default profile".
	QualityProfileID int64
	// LibraryID references libraries.id — which movie library owns this row.
	// Every stored row names one: migration 0011 stamped the rows that carried
	// a zero onto their kind's default, and every write path resolves a library
	// before it upserts. Readers therefore ask by id and nothing else.
	LibraryID int64
	// ReleaseDate is the theatrical release date, zero when unknown.
	ReleaseDate time.Time
	// DigitalRelease and PhysicalRelease are the home-release dates, zero when
	// the provider has not published them. Together with ReleaseDate they are
	// what MinAvailability is judged against (internal/wanted).
	DigitalRelease  time.Time
	PhysicalRelease time.Time
	// MinAvailability is the release stage the movie must reach before its
	// automatic search runs: one of the Availability* constants.
	MinAvailability string
	AddedAt         time.Time
	UpdatedAt       time.Time
}

// Minimum-availability stages, stored verbatim in movies.min_availability and
// constrained by the movies table. The ordering is temporal:
// announced happens before cinemas, cinemas before the home release.
const (
	// AvailabilityAnnounced searches as soon as the movie is added.
	AvailabilityAnnounced = "announced"
	// AvailabilityInCinemas waits for the theatrical release.
	AvailabilityInCinemas = "in_cinemas"
	// AvailabilityReleased waits for a home release (digital or physical). It
	// is the default: before that, most of what a search finds is junk.
	AvailabilityReleased = "released"
)

// ValidAvailability reports whether s names a minimum-availability stage.
func ValidAvailability(s string) bool {
	return s == AvailabilityAnnounced || s == AvailabilityInCinemas || s == AvailabilityReleased
}

// Series kinds are stored in `series.kind`. A series is a television show
// or — once the adult module is enabled — a site whose scenes are its episodes
// (PLAN phase 9 task 3).
//
// The discriminator exists so the handful of places that genuinely differ can
// ask: which metadata provider refreshes the title, which library root it
// organizes into, and whether the caller is allowed to see it at all.
// Everything else — the wanted list, the backlog sweep, RSS matching, the
// calendar, the import pipeline — is reused unchanged, which is the whole
// reason a site is modelled as a series rather than as a new table.
const (
	SeriesKindTV = "tv"
	// SeriesKindAnime is a series that lives on an anime shelf. It is
	// television in every mechanical sense — seasons, episodes, air dates, the
	// wanted list, the calendar — and the discriminator exists for the two
	// questions that differ: which shelf answers for it (an anime library, see
	// LibraryKindForSeries) and which screen lists it. Without it the /anime and
	// /series screens would have to tell the same rows apart by their library's
	// kind, which is a join the listing endpoints do not have.
	SeriesKindAnime = "anime"
	SeriesKindAdult = "adult"
)

// ValidSeriesKind reports whether s names a series kind Caravan stores. Like
// ValidRole, an unknown kind is a caller mistake rejected at the edge rather
// than defaulted: defaulting it either hides a television series or files an
// adult one where everybody can see it.
func ValidSeriesKind(s string) bool {
	return s == SeriesKindTV || s == SeriesKindAnime || s == SeriesKindAdult
}

// Series is a library TV series, or — when Kind is SeriesKindAdult — a site.
type Series struct {
	ID int64
	// Provider and ProviderRef are the provider that identified this row and
	// that provider's own id — the item is PINNED to it; a refresh asks this
	// provider and no other. Empty on a row no provider has identified.
	Provider    string
	ProviderRef string
	TMDBID      int64
	// StashID is the stash-box id of the site behind an adult series, a UUID
	// string. Empty on every television series and on an adult series that has
	// not been matched to a site yet. It is unique among the rows that set it,
	// exactly as TMDBID is.
	StashID   string
	TVDBID    int64
	IMDBID    string
	Title     string
	SortTitle string
	Year      int
	Overview  string
	// Kind is SeriesKindTV or SeriesKindAdult. The zero value is the empty
	// string rather than SeriesKindTV, so a caller that builds a Series by
	// hand and forgets it is rejected by the column's CHECK rather than
	// quietly filed as television — see store.UpsertSeries, which defaults it
	// once, in one place.
	Kind string
	// Status is the provider's series status ("Continuing", "Ended", …).
	Status string
	// Path is the series folder, relative to the storage root
	// ("TV/Planet Earth II (2016)"). Empty until the series is organized.
	Path string
	// PosterPath is the stored poster, relative to the storage root.
	PosterPath string
	// PosterURL is the provider's poster, an absolute URL (see
	// Movie.PosterURL).
	PosterURL string
	// Monitored is the series-level flag. SPEC §7: seasons and episodes carry
	// their own flags; setting this one cascades as a bulk update, not a lock.
	Monitored bool
	// QualityProfileID references quality_profiles.id; 0 means "use the
	// default profile".
	QualityProfileID int64
	// LibraryID references libraries.id — which library owns this series. Its
	// library's kind always agrees with Kind (UpsertSeries asserts it), and
	// every stored row names one, exactly as Movie.LibraryID does.
	LibraryID int64
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
	// StashID is the stash-box id of the scene behind an adult episode, a UUID
	// string, empty everywhere else. Unique among the rows that set it
	// by the episode identity index.
	StashID string
	Title   string
	// Overview is the long description. On a scene it is the studio's own
	// synopsis.
	Overview string
	// AirDate is the broadcast date, zero when unknown or unaired. On a scene
	// it is the release date, and its year is the season the scene lands in.
	AirDate   time.Time
	Monitored bool
	// AbsoluteNumber is the provider's series-wide episode number — the count
	// an anime-style release name uses ("Show - 105") — and 0 when no provider
	// ever served one for this episode. Zero is "not known", not "the zeroth
	// episode", so nothing may derive it.
	AbsoluteNumber int
	// Scene is the scene-side metadata of an adult episode, nil on every
	// television episode. It rides in one JSON column because nothing queries
	// on it — it is rendered on a scene row and nowhere else.
	Scene *SceneInfo
}

// SceneInfo is what an adult episode carries that a television episode has no
// counterpart for: the studio that released the scene, who is in it, and where
// it lives on the web (`episodes.scene`).
//
// It is a stored shape, deliberately separate from the provider-side SceneMeta
// in adult.go: the database format must not move when a provider adds a field.
type SceneInfo struct {
	// Studio is the releasing studio's name. It is denormalized off the site
	// for the same reason releases.indexer_name is denormalized off the
	// indexer: a sub-studio can be retired upstream, and the scene row should
	// still be able to say who put it out.
	Studio string `json:"studio,omitempty"`
	// Performers are the names credited on this scene, in the provider's
	// billing order — the alias the scene credits somebody under when there is
	// one, their canonical name otherwise. Names rather than ids because this
	// is what the scene row renders and what a release filename contains; the
	// provider's performer ids are a metadata concern, not a library one.
	Performers []string `json:"performers,omitempty"`
	// URL is the scene's page on the site, empty when unknown.
	URL string `json:"url,omitempty"`
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

// ProperRepackPreference controls whether PROPER and REPACK tags affect a
// release score.
const (
	ProperRepackPreferencePrefer  = "prefer"
	ProperRepackPreferenceNeutral = "neutral"
)

// TVCompatibilityPolicy controls how acquisition uses a profile's playback
// target.
const (
	TVCompatibilityPolicyIgnore  = "ignore"
	TVCompatibilityPolicyPrefer  = "prefer"
	TVCompatibilityPolicyRequire = "require"
)

// CustomFormat adjusts a release score when its title matches all include
// terms and none of its exclude terms.
type CustomFormat struct {
	Name         string   `json:"name"`
	IncludeTerms []string `json:"include_terms"`
	ExcludeTerms []string `json:"exclude_terms"`
	Score        int      `json:"score"`
}

// QualityProfile is a quality ladder plus acquisition policy: Caravan keeps
// searching for an item until it owns a file at or above Cutoff (SPEC §9).
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
	// PreferredSources ranks release sources best-first. An empty list uses
	// SourceLadder and preserves legacy profile behavior.
	PreferredSources []string
	// ProperRepackPreference is ProperRepackPreferencePrefer or
	// ProperRepackPreferenceNeutral. Empty reads as "prefer" for old rows.
	ProperRepackPreference string
	// MinSeeders is the torrent-only minimum seeder count. Zero disables it.
	MinSeeders int
	// MinSizeMB and MaxSizeMB constrain known release sizes. Zero disables
	// each bound, and an unknown size is always accepted.
	MinSizeMB int64
	MaxSizeMB int64
	// CustomFormats contributes the summed score of every matching rule.
	CustomFormats []CustomFormat
	// TVProfile names the playback target. Empty reads as "safe".
	TVProfile string
	// TVCompatibilityPolicy controls whether TV compatibility is ignored,
	// preferred, or required. Empty reads as "ignore".
	TVCompatibilityPolicy string
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// EventCategoryAdultOnly marks activity whose content is intrinsically adult,
// even when no library ownership ID is available to establish that provenance.
// History suppresses this category when shared adult visibility is disabled.
const EventCategoryAdultOnly = "adult"

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

// NotificationWebhook receives selected activity events outside Caravan.
// LastEventID is the delivery cursor: events at or below it have been
// considered for this webhook, whether they matched its toggles or not.
type NotificationWebhook struct {
	ID          int64
	Name        string
	URL         string
	OnGrab      bool
	OnImport    bool
	OnHealth    bool
	Enabled     bool
	LastEventID int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
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
	// LibraryID scopes the manual match: a file parked by an untied universal
	// search grab already knows which library its user chose, and the review
	// screen pre-selects it. 0 — every scan-parked file — means unscoped.
	LibraryID int64
	SeenAt    time.Time
}
