package core

import (
	"context"
	"errors"
	"time"
)

// ErrNoAdultProvider reports that an operation needed adult metadata and no
// provider is configured — in practice, no stash-box endpoint and API key have
// been entered yet.
//
// It mirrors ErrNoMetadataProvider rather than reusing it because the two are
// answered differently: a missing TMDB key degrades a scan to parse-only, while
// a missing stash-box key is only ever reachable once the adult module has been
// deliberately enabled (SPEC/PLAN phase 9 task 5), and the settings screen that
// turned it on is the place that has to say so.
var ErrNoAdultProvider = errors.New("core: no adult metadata provider configured")

// AdultVisible is the whole access rule for the adult module (PLAN phase 9
// task 5), in one pure function so there is exactly one truth table to read and
// exactly one to test.
//
// Two switches, and both must be on:
//
//   - enabled is the server-wide `adult_enabled` setting. Nobody bypasses it,
//     including an admin: it is the switch that makes the module absent rather
//     than merely locked, and an admin who can see adult routes on a server
//     they turned the module off on is a trace this phase promises not to
//     leave. It is also what guarantees zero stash-box traffic when off.
//   - granted is the per-account `adult_access` flag. An admin is implicitly
//     granted — the person who can flip the global switch and hand out the
//     grants gains nothing from being made to grant themselves — so the flag
//     is only ever consulted for a member.
//
// The open server (no accounts at all) authenticates as an implicit admin, so
// it reaches this with role RoleAdmin and needs only the global switch, which
// is the same trusted-LAN default the rest of Caravan has.
func AdultVisible(enabled bool, role string, granted bool) bool {
	if !enabled {
		return false
	}
	if role == RoleAdmin {
		return true
	}
	return granted
}

// AdultMetadataProvider is the metadata source behind the adult library
// (stash-box in phase 9, TPDB by default). It is the seam that keeps the
// library, automation and API layers testable without network access, exactly
// as MetadataProvider does for TMDB.
//
// It is a separate interface rather than an extension of MetadataProvider
// because the two describe different worlds and pretending otherwise would cost
// more than it saves:
//
//   - stash-box ids are UUID strings, not the int64s TMDB hands out, so every
//     method here would have to lie about its parameter type;
//   - a site has no "seasons" to fetch — the season a scene lands in is derived
//     from its release date by Caravan, not supplied by the provider — so
//     GetSite cannot return a populated SeriesMeta the way GetSeries does;
//   - scene search is paged and filtered by site, because a refresh walks every
//     scene a site ever released; TMDB search is a single relevance list.
//
// The mapping from these types onto series/season/episode rows (site → series,
// release year → season, scene → episode) is the library layer's job, which is
// what lets the wanted list, backlog search, RSS matching and the import
// pipeline be reused rather than forked.
type AdultMetadataProvider interface {
	// SearchSites returns site candidates for a free-text query, best match
	// first. A site becomes a series of kind "adult".
	SearchSites(ctx context.Context, q string) ([]SiteMeta, error)
	// GetSite returns one site by provider id. It returns an error wrapping
	// ErrNotFound — not a nil SiteMeta — when the provider has no such record.
	GetSite(ctx context.Context, stashID string) (*SiteMeta, error)
	// SearchScenes returns one page of scenes matching q. It is the query a
	// refresh pages through to discover a site's scenes, so paging is part of
	// the interface rather than a client detail.
	SearchScenes(ctx context.Context, q SceneQuery) (*ScenePage, error)
	// GetScene returns full details for one scene by provider id, with the
	// same ErrNotFound contract as GetSite.
	GetScene(ctx context.Context, stashID string) (*SceneMeta, error)
}

// SiteMeta is provider-side site metadata, before it becomes a library Series
// of kind "adult". stash-box calls these studios; Caravan calls them sites,
// because that is the word on the release names the parser has to match.
type SiteMeta struct {
	// StashID is the provider's own id, a UUID string. It is what lands in
	// series.stash_id.
	StashID string
	Name    string
	// Aliases are the other names the provider knows this site by. They matter
	// to release matching: a scene file is named for whichever alias the
	// packager used. Nil when the provider supplied none.
	Aliases []string
	// ParentStashID and ParentName describe the network above this site
	// ("Vixen Media Group" over "Blacked"), both empty when it is top-level.
	ParentStashID string
	ParentName    string
	// URL is the site's own home page, empty when the provider has none.
	URL string
	// ImageURL is an absolute provider URL for the site's artwork, not a
	// storage-root-relative path: it is what the organizer downloads from, not
	// what it writes. Empty means the provider has no artwork (see
	// MovieMeta.PosterURL for the same convention).
	ImageURL string
}

// SceneMeta is provider-side scene metadata, before it becomes an episode.
type SceneMeta struct {
	// StashID is the provider's own id, a UUID string, destined for
	// episodes.stash_id.
	StashID string
	// SiteStashID and SiteName identify the site that released the scene —
	// the series the episode belongs to.
	SiteStashID string
	SiteName    string
	Title       string
	// Overview is the provider's long description, named to match MovieMeta
	// and SeriesMeta rather than stash-box's own "details".
	Overview string
	// Date is the scene's release date, zero when the provider has none. It is
	// the episode's air date, and its year is the season the episode lands in,
	// so a zero date is the one field a caller must handle rather than pass on.
	Date time.Time
	// Code is the studio's own scene identifier, empty when unknown. It is a
	// hint for ordering scenes released on the same day, never the episode
	// number: the episode number is the scene's sequence within its year, which
	// only the library layer can compute because it needs every scene at once.
	Code string
	// Duration is the runtime in seconds, zero when unknown.
	Duration int
	// Performers are credited in the provider's own order, which is the
	// billing order the UI renders.
	Performers []ScenePerformer
	// URL is the scene's page on the site, empty when unknown.
	URL string
	// ImageURL is an absolute provider URL for the scene's cover art (see
	// SiteMeta.ImageURL).
	ImageURL string
}

// ScenePerformer is one credited performer on a scene.
type ScenePerformer struct {
	// StashID is the provider's performer id, a UUID string.
	StashID string
	// Name is the performer's canonical name.
	Name string
	// As is the alias this particular scene credits them under, empty when
	// they are credited by Name. Release names use whichever of the two the
	// packager saw, so both are kept.
	As string
}

// SceneQuery selects scenes for AdultMetadataProvider.SearchScenes. A zero
// SceneQuery is legal and asks for the provider's first page of everything.
type SceneQuery struct {
	// Text is a free-text match over titles and performers. Empty matches
	// everything the other fields allow.
	Text string
	// SiteStashID restricts results to one site. It is the field a refresh
	// uses: a site's scenes are its series' episodes, so a refresh pages this
	// query rather than searching by title.
	SiteStashID string
	// Page is 1-based. Anything lower is clamped to 1 rather than rejected, so
	// a caller that forgot to set it gets the first page instead of an error.
	Page int
	// PerPage is the page size. Zero takes the provider's default; providers
	// cap it at whatever their endpoint allows.
	PerPage int
}

// ScenePage is one page of SearchScenes results.
type ScenePage struct {
	// Page and PerPage are the values actually used, after clamping — a caller
	// paging through a site walks these rather than its own request values.
	Page    int
	PerPage int
	// Total is the provider's count of matching scenes across every page. It is
	// how a refresh knows when it has seen them all.
	Total  int
	Scenes []SceneMeta
}
