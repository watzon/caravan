package core

// Library kinds (SPEC §7 `libraries.kind`, PLAN phase 8). The kind names the
// item vocabulary a library speaks — movie rows, television series, adult
// sites. Since migration 0022 an install may hold several libraries of one
// kind; the one flagged is_default absorbs every lookup that still asks by
// kind, and items name their own library through `library_id`.
const (
	LibraryKindMovie = "movie"
	LibraryKindTV    = "tv"
	// LibraryKindAdult is the adult module's library (PLAN phase 9). Unlike
	// the other two it is not seeded by a migration: an install has one only
	// once somebody creates it, because a library row is a shelf in the UI and
	// a container in the DLNA tree, and neither may exist on an install that
	// never asked for adult content. Creating one IS turning the module on.
	LibraryKindAdult = "adult"
)

// LibraryKindForSeries maps a series kind onto the library it belongs to.
//
// The two vocabularies stay separate — `series.kind` says what a row IS,
// `libraries.kind` says which shelf answers for it — but for series they line
// up one-to-one, and this is the single place that says so. Everything that
// resolves a quality profile, an indexer set or a download route for an
// episode goes through it, so an adult episode cannot silently be graded
// against the television library's settings.
func LibraryKindForSeries(seriesKind string) string {
	if seriesKind == SeriesKindAdult {
		return LibraryKindAdult
	}
	return LibraryKindTV
}

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
	// Kind is one of the LibraryKind* constants and is not editable: it says
	// which item vocabulary the library speaks (and so which tables its items
	// live in), not which shelf it is. Several libraries may share a kind.
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
	// Provider is the chain's head, kept in sync by the store — one of the
	// Provider* constants, validated against Kind by ProviderServes at the
	// edge. It survives beside Providers so every reader written against 0022
	// keeps answering exactly as it did.
	Provider string
	// Providers is the ordered list of providers this library identifies new
	// items through: the first one that answers wins, and the rest are the
	// fallback. Empty on a row written before migration 0024, which is what
	// ProviderChain exists to smooth over.
	Providers []string
	// IsDefault marks the one library per kind that answers legacy by-kind
	// lookups and receives items added without an explicit target. Exactly
	// one default exists per kind (partial unique index, migration 0022).
	IsDefault bool
	// Active is the library's master switch. False makes it dormant for
	// EVERYONE — an admin included — and deletes nothing: the rows, the files
	// and the grants all wait for it to come back on. It is how a library is
	// hidden without being destroyed, and it is the general form of the switch
	// the adult module used to own alone.
	Active bool
	// Restricted narrows the library to the accounts named in `library_access`
	// (plus admins, see LibraryVisible). False is every account.
	//
	// It is a flag rather than an inference from an empty roster because
	// "restricted to nobody yet" is not "open to everybody": a library that
	// unlocked itself the moment its last grantee was removed would do the
	// opposite of what removing them asked for.
	Restricted bool
}

// LibraryVisible is the whole per-library access rule, in one pure function so
// there is exactly one truth table to read and exactly one to test. It is
// AdultVisible generalized: the module switch became lib.Active and the
// per-account grant became a per-library one.
//
// Two rules, applied in this order and no other:
//
//   - lib.Active binds everyone. An inactive library is dormant, not merely
//     locked, and an admin is no exception — deactivating a library is how an
//     owner hides one from THEMSELVES, and a switch the person holding it
//     cannot feel is not a switch. It is also what guarantees no provider
//     traffic, no scans and no DLNA container for a library that is off.
//   - lib.Restricted binds members only. Admins bypass it, and that is
//     load-bearing rather than a convenience: the API-key credential and the
//     open install (no accounts at all) both authenticate as an admin with
//     user id 0, and user 0 can never hold a library_access row. Binding
//     admins to restriction would lock both of them out of every restricted
//     library instantly, with no door left to grant themselves through. Anyone
//     tempted by a future "restrict admins too" is looking at that failure.
//
// granted is whether this account holds a grant on this library. It is only
// ever consulted for a member, and an unrecognised role is not an admin.
func LibraryVisible(lib Library, role string, granted bool) bool {
	if !lib.Active {
		return false
	}
	if role == RoleAdmin {
		return true
	}
	return !lib.Restricted || granted
}

// LibrarySet indexes a whole library list for the one question background work
// asks of it: given a row's library_id and the kind it belongs to, is the
// library that owns it switched on?
//
// It exists so that a sweep with no caller and a request with one resolve
// ownership the same way. The rules are api.libraryGate's, restated for callers
// that have no identity to gate on — divergence between them would mean an item
// the wanted list searches for and the screens refuse to show, or the reverse:
//
//   - a zero library_id resolves to the kind's DEFAULT library, exactly as a
//     by-kind lookup would (pre-0022 rows, and every import that named no
//     target, carry a zero);
//   - a kind with no library at all is NOT active: a row belonging to a shelf
//     that does not exist belongs nowhere, and nowhere is off. That is what
//     keeps an adult series row on an install that never enabled the module
//     from being wanted;
//   - an id no row answers to stays active: ownership that cannot be
//     established is not evidence of ownership.
type LibrarySet struct {
	byID     map[int64]Library
	defaults map[string]int64
}

// NewLibrarySet indexes the list once, for callers that will ask about many
// rows.
func NewLibrarySet(libs []Library) LibrarySet {
	s := LibrarySet{byID: make(map[int64]Library, len(libs)), defaults: map[string]int64{}}
	for _, l := range libs {
		s.byID[l.ID] = l
		// store.GetLibraryByKind's ordering, reproduced: is_default first, then
		// id. Resolving a by-kind lookup differently here would answer about a
		// different library than the one the rest of the server files rows under.
		if cur, ok := s.defaults[l.Kind]; !ok || (l.IsDefault && !s.byID[cur].IsDefault) {
			s.defaults[l.Kind] = l.ID
		}
	}
	return s
}

// Active reports whether the library owning a row is switched on.
func (s LibrarySet) Active(libraryID int64, kind string) bool {
	if libraryID == 0 {
		id, ok := s.defaults[kind]
		if !ok {
			return false
		}
		libraryID = id
	}
	lib, ok := s.byID[libraryID]
	if !ok {
		return true
	}
	return lib.Active
}

// ProviderChain is the ordered provider list to walk when identifying an item
// in this library.
//
// It is the one place that reconciles the two columns, so no caller has to
// know that Provider is the head of Providers: a row from 0024 onward answers
// from the list, a row that only ever had a head answers with a chain of one,
// and a library nobody assigned a provider answers with nothing to walk.
func (l Library) ProviderChain() []string {
	if len(l.Providers) > 0 {
		return l.Providers
	}
	if l.Provider != "" {
		return []string{l.Provider}
	}
	return nil
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
