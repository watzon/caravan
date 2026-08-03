package core

// Library kinds (SPEC §7 `libraries.kind`, PLAN phase 8). The kind is what
// maps an item to its library: every movie belongs to the movie library, every
// series to the tv library, and there is exactly one library per kind.
const (
	LibraryKindMovie = "movie"
	LibraryKindTV    = "tv"
)

// Library is one section of the media library — Movies, Series — as a row
// rather than a constant.
//
// A library owns a deliberately short list of settings it may answer for
// itself: download routing, DLNA visibility, a default quality profile, and
// (through LibraryIndexer) which indexers it searches with which categories.
// Everything else stays global. Unset fields are the point of the type: they
// are what makes an upgraded install behave exactly as it did before anybody
// opened the Libraries screen.
type Library struct {
	ID int64
	// Kind is one of the LibraryKind* constants, unique across libraries and
	// not editable: it is the library's identity, not a preference.
	Kind string
	// Name is the user-facing label.
	Name string
	// RootPath is the library's directory relative to the storage root, with
	// forward slashes (SPEC §1.2 pillar 3).
	RootPath string
	// DLNAVisible includes the library's container in the DLNA content tree.
	// Defaults to true, so a library is advertised unless it is told not to be.
	DLNAVisible bool
	// RouteTorrent and RouteUsenet override the global routing settings for
	// grabs made on this library's behalf, using the same values
	// (a download_clients.id in decimal, or store.RouteEmbedded). Empty means
	// no override: the global setting answers.
	RouteTorrent string
	RouteUsenet  string
	// QualityProfileID is the library's default profile, used by items that
	// name none of their own. Zero means no library default, which falls
	// through to the store's default profile.
	QualityProfileID int64
}

// LibraryIndexer is one (library, indexer) search override.
//
// Its absence is meaningful and is the common case: a library with no row for
// an indexer searches it with the indexer's own categories. Only a deviation
// is ever stored.
type LibraryIndexer struct {
	LibraryID int64
	IndexerID int64
	// Enabled false drops this indexer from that one library's search fan-out
	// without touching any other library, and without disabling the indexer.
	Enabled bool
	// Categories replaces IndexerConfig.Categories for this pair. Nil means no
	// override; a non-nil empty list is an override to "search unfiltered",
	// which is what an empty category list has always meant.
	Categories []int
}

// LibrarySettings is the effective configuration for one library: the
// library's own value where it set one, the global default everywhere else
// (PLAN phase 8 task 2).
//
// The list of fields is short on purpose and closed on purpose. Routing, DLNA
// visibility, the default quality profile and the indexer set are the only
// settings a library may answer for itself; adding a field here is a product
// decision about what "per-library" means, not a refactor.
type LibrarySettings struct {
	// LibraryID and Kind identify the library these values were resolved for.
	LibraryID int64
	Kind      string
	// RouteTorrent and RouteUsenet are the resolved routing values: the
	// library's override, or the global setting when it has none. Empty means
	// nothing is configured at either level, which is what a stock install
	// looks like.
	RouteTorrent string
	RouteUsenet  string
	// DLNAVisible is the library's own flag. There is no global counterpart to
	// fall back to — the global DLNA setting turns the whole server on or off,
	// which is a different question from which libraries it advertises.
	DLNAVisible bool
	// QualityProfileID is the library's default profile, zero when it has
	// none. Zero is deliberately passed through rather than resolved here: it
	// is exactly the value Store.ResolveQualityProfile already reads as "use
	// the default".
	QualityProfileID int64
	// Indexers is this library's search fan-out: every globally enabled
	// indexer the library has not disabled, each carrying the categories a
	// search for this library must send. Categories is already resolved — the
	// pair override when there is one, the indexer's own list otherwise — so
	// search code reads it without knowing libraries exist.
	Indexers []IndexerConfig
}
