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

// StashboxInstance is one configured stash-box endpoint.
//
// "stash-box" is a protocol, so a single endpoint setting could only ever
// describe one of the catalogues speaking it. Each configured endpoint is its
// own provider: it has its own account, its own capabilities, and its own
// UUIDs, and the public boxes are forks of one another that mint identical
// UUIDs for different rows of different catalogues.
type StashboxInstance struct {
	ID int64
	// ProviderID is the id stored in `series.provider` and in a library's
	// provider chain. It is the instance's identity — 'stashbox' for the
	// endpoint configured before instances existed, 'stashbox:<slug>' for every
	// one minted since — and it is immutable: renaming an instance must never
	// re-point the rows pinned to it.
	ProviderID string
	// Name is the user-facing label, unique across instances.
	Name string
	// Endpoint is the box's GraphQL URL, always absolute. There is no "" means
	// the preset here: with several instances that sentinel would let two of
	// them be silently the same box. The preset belongs to the picker that
	// offers it, not to the stored value.
	//
	// It is immutable after creation for the reason the provider id is: every
	// item pinned to this instance carries a UUID that only this box minted, and
	// re-pointing it at another box would have the next refresh overwrite those
	// rows with whatever the new box happens to hold under the same ids. Moving
	// to another box is adding an instance.
	Endpoint string
	// APIKey is the credential. It lives in the database, never in the bootstrap
	// YAML and never in logs (SPEC §12). Empty is legitimate: a box that serves
	// anonymous reads needs none.
	APIKey    string
	CreatedAt time.Time
	UpdatedAt time.Time
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
	// SearchPerformers and SearchTags back the scene filter rail's typeaheads
	// (PLAN phase 12 task 2). They return the provider's first page only: a
	// typeahead is a shortlist, and anyone who has to page one has not typed
	// enough.
	//
	// They are on this interface rather than an optional one because a
	// provider that can answer SearchScenes can answer these — every dialect
	// that has scenes has the performers and tags those scenes are filed
	// under.
	SearchPerformers(ctx context.Context, query string) ([]ScenePerformerMeta, error)
	SearchTags(ctx context.Context, query string) ([]SceneFilterRef, error)
}

// ErrSceneFilterUnsupported reports that a SceneQuery asked for a filter the
// configured endpoint cannot express. It is a REFUSAL, not a degradation: a
// provider that quietly dropped the filter would answer a wider question than
// the caller asked, which on this surface means showing scenes the filter was
// there to exclude.
//
// Use errors.As with *SceneFilterUnsupportedError to learn which filter.
var ErrSceneFilterUnsupported = errors.New("core: scene filter not supported by this metadata endpoint")

// SceneFilterUnsupportedError names the filter that could not be served.
// Filter is a short caller-facing phrase ("year", "any-of performers"), never
// the value asked for: a scene filter's VALUE is the one string on this
// surface that must not reach a log or an error body.
type SceneFilterUnsupportedError struct {
	Filter string
}

func (e *SceneFilterUnsupportedError) Error() string {
	return "core: the metadata endpoint cannot filter scenes by " + e.Filter
}

func (e *SceneFilterUnsupportedError) Unwrap() error { return ErrSceneFilterUnsupported }

// SceneFilterSupport says which SceneQuery fields the configured endpoint can
// actually express. Every field is the ANSWER AHEAD OF TIME to a refusal the
// provider would otherwise make, which is why it exists: a refusal arrives as a
// 400 after the fact and blanks the grid, and PLAN phase 12's first acceptance
// criterion is that "nothing renders a control the provider cannot answer".
// The rail reads this to decide which pills to draw.
//
// It is a positive list — true is "this works" — so the zero value is the
// safest thing a caller can be handed rather than the most permissive: a
// provider that says nothing offers nothing.
//
// Fields absent from it are the ones every dialect serves (free text, a site,
// a performer or tag, all-of, date order), so there is nothing to advertise.
type SceneFilterSupport struct {
	// Year is a release-year filter.
	Year bool
	// Duration is a runtime filter.
	Duration bool
	// SiteScope is widening a site to its parent studio or whole network.
	SiteScope bool
	// DateOp is a date comparison other than "on this exact day".
	DateOp bool
	// SortDuration and SortRelevance are the two orderings not every dialect
	// offers.
	SortDuration  bool
	SortRelevance bool
	// AnyOf is the ANY half of the any/all switch with two or more ids. A
	// dialect that can only say "carries all of these" still answers a single
	// id either way, so this is only about the second chip onwards.
	AnyOf bool
}

// SceneFilterReporter is implemented by an AdultMetadataProvider that knows
// which of SceneQuery's fields its endpoint can express.
//
// Optional, and deliberately so. SearchScenes already refuses what it cannot
// serve, so a provider that does not implement this is CORRECT, merely
// unhelpful — the filter still fails honestly, just after the request instead
// of before it. Callers treat a provider without it as "assume it can, and let
// the refusal explain", which is exactly the behaviour that existed before.
type SceneFilterReporter interface {
	SceneFilterSupport() SceneFilterSupport
}

// EverySceneFilter is what a caller assumes about a provider that does not
// implement SceneFilterReporter: it might serve anything, and SearchScenes will
// say otherwise if it cannot.
func EverySceneFilter() SceneFilterSupport {
	return SceneFilterSupport{
		Year:          true,
		Duration:      true,
		SiteScope:     true,
		DateOp:        true,
		SortDuration:  true,
		SortRelevance: true,
		AnyOf:         true,
	}
}

// SceneFiltersOf reports what provider can serve, falling back to
// EverySceneFilter for one that does not say.
func SceneFiltersOf(provider AdultMetadataProvider) SceneFilterSupport {
	if reporter, ok := provider.(SceneFilterReporter); ok {
		return reporter.SceneFilterSupport()
	}
	return EverySceneFilter()
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

// SceneFilterRef is one performer or tag as a scene filter names it.
//
// It carries BOTH ids because the two dialects disagree about which one
// identifies a performer: TPDB's REST scene index filters on its own numeric
// id (the `performers[84060]=Mia Malkova` map), while a generic stash-box
// filters on the uuid its GraphQL calls `id`. A ref comes out of the same
// provider's typeahead that consumes it, so in practice whichever id that
// dialect needs is the one that is set; a provider handed a ref with only the
// other dialect's id says so rather than filtering on nothing.
//
// Name is what the provider's own wire format wants alongside the id (TPDB's
// filter is a map of id to name) and what a chip renders. It is not matched
// on: an id that names nothing simply selects nothing.
type SceneFilterRef struct {
	// ID is the provider's numeric id, zero when the dialect has none.
	ID int64
	// StashID is the provider's uuid, empty when the dialect has none.
	StashID string
	Name    string
}

// ScenePerformerMeta is one performer in a typeahead: a ref plus the picture
// that tells two people with similar names apart.
//
// It is distinct from ScenePerformer, which is a CREDIT on one scene and
// carries the alias that scene billed them under. A filter has no alias.
type ScenePerformerMeta struct {
	SceneFilterRef
	// ImageURL is an absolute provider URL for the performer's photo, empty
	// when the provider has none (see SiteMeta.ImageURL).
	ImageURL string
}

// SceneSiteScope is how wide a SiteStashID filter reaches. It exists because
// "this site" and "everything this network puts out" are different questions
// and a household browsing for scenes asks both.
type SceneSiteScope string

const (
	// SceneSiteOnly is the default and the narrow reading: scenes filed under
	// exactly this site. It is what a catalogue walk uses, because a site is
	// one series and its scenes are that series' episodes.
	SceneSiteOnly SceneSiteScope = "site"
	// SceneSiteParent widens to the site's parent, so a sub-site's siblings
	// come with it.
	SceneSiteParent SceneSiteScope = "parent"
	// SceneSiteNetwork is the widest: the whole network above the site.
	SceneSiteNetwork SceneSiteScope = "network"
)

// ParseSceneSiteScope resolves a caller-supplied scope, reporting false for
// anything else so an unknown value is refused rather than quietly becoming
// the narrow default.
func ParseSceneSiteScope(s string) (SceneSiteScope, bool) {
	switch SceneSiteScope(s) {
	case SceneSiteOnly, SceneSiteParent, SceneSiteNetwork:
		return SceneSiteScope(s), true
	}
	return "", false
}

// SceneDateOp is how SceneQuery.Date is compared. The provider serves one date
// and an operator rather than a range, so this is the provider's shape rather
// than the from/to pair the movie and series scopes use — a control Caravan
// cannot serve as a range must not be offered as one.
type SceneDateOp string

const (
	// SceneDateOn is the default when a Date is set: that exact release day.
	SceneDateOn         SceneDateOp = "on"
	SceneDateBefore     SceneDateOp = "before"
	SceneDateOnOrBefore SceneDateOp = "on_or_before"
	SceneDateAfter      SceneDateOp = "after"
	SceneDateOnOrAfter  SceneDateOp = "on_or_after"
)

// ParseSceneDateOp resolves a caller-supplied comparison, reporting false for
// anything else.
func ParseSceneDateOp(s string) (SceneDateOp, bool) {
	switch SceneDateOp(s) {
	case SceneDateOn, SceneDateBefore, SceneDateOnOrBefore, SceneDateAfter, SceneDateOnOrAfter:
		return SceneDateOp(s), true
	}
	return "", false
}

// SceneSort names an ordering of scene results. The values are Caravan's, not
// any dialect's: TPDB bakes the direction into one `orderBy` enum
// (recently_released, former_released) and stash-box splits it into a sort and
// a direction, and which shape to send is the client's problem rather than the
// caller's. The direction is DiscoverOrder, the same asc/desc vocabulary the
// movie and series scopes use.
type SceneSort string

const (
	// SceneSortReleased is release date, and the default: a site's scenes are
	// episodes filed by release year, so date order is the order the library
	// and the browse screen both want.
	SceneSortReleased SceneSort = "released"
	// SceneSortCreated and SceneSortUpdated are when the PROVIDER first filed
	// the record and last edited it — "what is new on the endpoint", which is
	// not the same question as "what came out recently".
	SceneSortCreated SceneSort = "created"
	SceneSortUpdated SceneSort = "updated"
	// SceneSortDuration is runtime.
	SceneSortDuration SceneSort = "duration"
	// SceneSortRelevance is the provider's own match ranking. It only means
	// anything alongside Text, and it has no direction — a provider sorting by
	// relevance ignores Order.
	SceneSortRelevance SceneSort = "relevance"
)

// ParseSceneSort resolves a caller-supplied sort name, reporting false for
// anything else.
func ParseSceneSort(s string) (SceneSort, bool) {
	switch SceneSort(s) {
	case SceneSortReleased, SceneSortCreated, SceneSortUpdated, SceneSortDuration, SceneSortRelevance:
		return SceneSort(s), true
	}
	return "", false
}

// SceneQuery selects scenes for AdultMetadataProvider.SearchScenes. A zero
// SceneQuery is legal and asks for the provider's first page of everything.
//
// Not every field is expressible on every dialect. A provider that cannot
// serve one returns a *SceneFilterUnsupportedError naming it rather than
// answering the wider question — see ErrSceneFilterUnsupported.
type SceneQuery struct {
	// Text is a free-text match over titles and performers. Empty matches
	// everything the other fields allow.
	Text string
	// SiteStashID restricts results to one site. It is the field a refresh
	// uses: a site's scenes are its series' episodes, so a refresh pages this
	// query rather than searching by title.
	SiteStashID string
	// SiteScope widens SiteStashID from the site itself to its parent or its
	// whole network. Empty is SceneSiteOnly. It means nothing without a
	// SiteStashID, and a provider handed one without the other says so.
	SiteScope SceneSiteScope
	// Performers and Tags narrow to scenes carrying those ids.
	//
	// PerformersAll and TagsAll are the any/all switch: false asks for a scene
	// carrying ANY of the ids, true for one carrying ALL of them. With fewer
	// than two ids the two readings are the same question.
	Performers    []SceneFilterRef
	PerformersAll bool
	Tags          []SceneFilterRef
	TagsAll       bool
	// Year is a release year, zero for any. It is a filter of its own rather
	// than a Date range because the provider serves it as one.
	Year int
	// Date is a release date compared with DateOp, zero for any. An empty
	// DateOp alongside a set Date is SceneDateOn.
	Date   time.Time
	DateOp SceneDateOp
	// Duration is a runtime in seconds, zero for any. It is a single value and
	// not a range because that is what the provider serves — see SceneDateOp
	// for the same rule.
	Duration int
	// Sort and Order default to SceneSortReleased and OrderDesc when empty,
	// which is the newest-first order a site's page and the refresh both read.
	Sort  SceneSort
	Order DiscoverOrder
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
