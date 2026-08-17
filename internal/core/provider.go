package core

import "strings"

// Metadata provider ids (stored in `libraries.provider`). A provider id names
// a client implementation compiled into the binary; the registry below is the
// single statement of which ids exist, which library kinds each may be chained
// on, and which item vocabularies each can look a ref up in.
// Instantiation stays in cmd/caravan — this package only answers "is this id
// valid for that kind" and "can this id resolve that ref", so the store and the
// API can validate a library row without knowing how a client is built or
// credentialed.
//
// An id may carry an INSTANCE: `<base>:<slug>` names one configured endpoint of
// a protocol that can be configured more than once. The base is the compiled
// client; the slug is a row in a table. Only the base is a fact about the
// binary, which is why every question this file answers about a kind is
// answered on the base (ProviderServes) while the whole string stays the value
// stored on an item or a chain.
const (
	ProviderTMDB     = "tmdb"
	ProviderStashbox = "stashbox"
	ProviderAniList  = "anilist"
	ProviderTVmaze   = "tvmaze"
	ProviderTheTVDB  = "thetvdb"
)

// ProviderDescriptor describes one compiled-in metadata provider.
type ProviderDescriptor struct {
	// ID is the value `libraries.provider` stores.
	ID string
	// Name is the user-facing label.
	Name string
	// Kinds lists the LibraryKind* values the provider may be CHAINED on. A
	// provider that speaks movies and television is not thereby qualified to
	// serve an adult library: the vocabularies (TMDB ids vs stash ids) do not
	// overlap.
	//
	// It is chain eligibility and nothing else. "Can this provider look one
	// item up" is Lookups' question, and the two are separate because a
	// provider that can answer GetMovie is not thereby a provider an owner
	// should be able to file an anime shelf against: the shelf's chain decides
	// how EVERY file under a root is identified, and a rung whose catalogue
	// numbers episodes differently from the shelf's own is a wrong answer
	// rather than a missing one.
	Kinds []string
	// Lookups lists the item vocabularies this provider can be asked to
	// resolve one ref in: MediaTypeMovie, MediaTypeSeries, MediaTypeScene.
	//
	// It answers a different question from Kinds, and the difference is the
	// whole reason it exists. Kinds governs the CHAIN — which shelves an owner
	// may point at this provider. Lookups governs the REF — a user pasting a
	// search hit and saying "this is the title", where the only thing that can
	// be wrong is asking a catalogue for a record it does not file. A provider
	// may be looked up on a vocabulary it serves no library kind for: AniList
	// identifies an anime film by id perfectly well, and refusing that ref
	// because AniList chains onto no movie library would turn the chain into a
	// second allow-list nobody asked for (see api.itemRefFrom).
	Lookups []string
	// CredentialSetting is the settings-table key holding this provider's API
	// key. Empty means keyless, and keyless is the default: a provider that
	// needs nothing entered is Ready the moment it is compiled in, and the
	// server has no verdict to reach about it.
	//
	// It is a bare literal rather than a store.Setting* reference because core
	// cannot import store — store validates library rows against this package,
	// and the dependency the other way round would be a cycle. The two spellings
	// are held together by TestProviderCredentialSettingsMatchStoreKeys in
	// internal/api, which is the one place that may import both.
	CredentialSetting string
}

// providers is a literal, not a mutable registry: what is compiled in is the
// only truth worth having, and a second, runtime-writable source of that truth
// is how the two drift apart. Adding a provider is one entry here plus a
// client implementation wired up in cmd/caravan.
//
// It partitions STRICTLY by library kind: every kind names the one
// catalogue whose numbering that shelf is filed under, and no provider claims a
// kind it is not the natural answer for. A shelf's chain decides how every file
// beneath its root is identified, so a rung from another catalogue is a
// different show's episode ordering rather than a fallback — the anime kind is
// the clearest case, where AniList's flat records and TMDB's seasons disagree
// about what "episode 13" names.
//
// Lookups is where breadth lives instead. A provider is asked for a ref in any
// vocabulary its catalogue actually files, whatever shelves it may be chained
// onto, which is what keeps "paste a search hit" working across the whole set.
var providers = []ProviderDescriptor{
	{ID: ProviderTMDB, Name: "TMDB",
		Kinds:             []string{LibraryKindMovie, LibraryKindTV},
		Lookups:           []string{MediaTypeMovie, MediaTypeSeries},
		CredentialSetting: "tmdb_api_key"},
	// Stash-box is credentialed and yet carries no CredentialSetting: the adult
	// half keeps its own door. Its endpoint and key are proved together by
	// ValidateAdultCredential, guarded on the way in by the enable gate and by
	// guardAdultCredentialEdit, and a credential that is only meaningful beside
	// the endpoint that issued it is not the same object as a bare metadata API
	// key. Listing it here would put it in the metadata credential map and in
	// the two loops that read that map, where neither of those guards runs.
	//
	// It is also the one INSTANCED provider: "stash-box" is a protocol, and
	// StashDB, FansDB, PMV-Stash and ThePornDB are separate catalogues speaking
	// it, each with its own account and its own UUIDs. Each configured endpoint
	// is therefore its own provider id.
	//
	// The BARE id is the legacy instance — the endpoint configured before
	// instances existed — and it stays valid forever. Rewriting it would mean
	// rewriting the provider column of every adult row already on disk, and a
	// pre-existing endpoint is a real first-class instance rather than a
	// migration artefact. New instances are `stashbox:<slug>`.
	{ID: ProviderStashbox, Name: "Stash-box",
		Kinds:   []string{LibraryKindAdult},
		Lookups: []string{MediaTypeScene}},
	// AniList is the anime kind's provider outright, and the only one. An anime
	// library holds films and series together, and both halves come out of one
	// catalogue: an anime film is a MEDIA entry of format MOVIE beside the
	// series records, so internal/anilist answers GetMovie by reading the same
	// document GetSeries does and refusing every format but MOVIE. A shelf whose
	// films and episodes are numbered by one catalogue is the point of the
	// unified kind — a second rung from a catalogue that seasons what AniList
	// files flat would renumber episodes rather than fill a gap.
	//
	// Its Lookups cover films and series for the same reason its chain does not
	// reach the movie kind: a film pasted from an AniList search hit is a true
	// ref whatever shelf it is destined for, and refusing it because AniList
	// chains onto no Movies library would make the chain a second allow-list.
	{ID: ProviderAniList, Name: "AniList",
		Kinds:   []string{LibraryKindAnime},
		Lookups: []string{MediaTypeMovie, MediaTypeSeries}},
	// TVmaze catalogues television and nothing else — there is no film half of
	// its catalogue to look up — and internal/tvmaze answers GetMovie with
	// ErrProviderKindUnsupported.
	{ID: ProviderTVmaze, Name: "TVmaze",
		Kinds:   []string{LibraryKindTV},
		Lookups: []string{MediaTypeSeries}},
	// TheTVDB looks up no MOVIE, and that is a deliberate omission rather than a
	// gap in the catalogue: TheTVDB does hold films.
	//
	// MovieMeta.DigitalRelease is what gates minimum availability
	// (internal/wanted/list.go:199), and TheTVDB's movie record carries no typed
	// release list to fill it from. A movie mapped through here would arrive with
	// a zero digital release, which reads as "released long ago" and starts
	// grabbing a film that is still in cinemas — a wrong automation decision, not
	// a missing field on a detail page. Admitting films therefore waits on a
	// release-date mapping design, and until then internal/thetvdb answers
	// GetMovie with ErrProviderKindUnsupported so the two cannot disagree.
	{ID: ProviderTheTVDB, Name: "TheTVDB",
		Kinds:             []string{LibraryKindTV},
		Lookups:           []string{MediaTypeSeries},
		CredentialSetting: "thetvdb_api_key"},
}

// Providers returns the compiled-in provider descriptors. The result is a
// fresh copy: callers get a list to render, not a handle on the registry.
func Providers() []ProviderDescriptor {
	out := make([]ProviderDescriptor, len(providers))
	copy(out, providers)
	return out
}

// ProviderBase returns the compiled-in provider an id names, discarding any
// instance slug: "stashbox:stashdb" is "stashbox", and "tmdb" is "tmdb".
//
// Everything after the FIRST colon is the slug, so "a:b:c" is base "a" with the
// (invalid) slug "b:c". One colon separates the two halves and a slug may not
// contain another; parsing more than one would invent a hierarchy no id has.
//
// The function is total on purpose. "" answers "" and "stashbox:" answers
// "stashbox" even though neither is a usable id — validity is
// ValidProviderInstanceID's question, and a parser that refused malformed input
// would leave every caller to ask both questions in the right order.
func ProviderBase(id string) string {
	base, _, _ := strings.Cut(id, ":")
	return base
}

// ValidProviderInstanceID reports whether id is a well-formed provider id: a
// bare compiled-in id, or `<base>:<slug>` naming one instance of a provider
// that can be configured more than once.
//
// A slug is `^[a-z0-9][a-z0-9-]{0,31}$` — lowercase, at most 32 characters, no
// leading dash. It is part of a value stored on rows and in chain JSON and it
// is immutable once minted, so the alphabet is narrow deliberately: an id that
// can differ only by case is an id two rows can disagree about.
//
// Stash-box is the only base that admits a slug. That is a deliberate assertion
// rather than a gap: nothing mints tmdb instances, so `tmdb:anything` can only
// be a mistake or a hand-edited chain, and admitting it would put an id in a
// library's chain that the registry can never resolve to a client. When a
// second protocol becomes instanced this grows a descriptor property; until
// then the narrower rule is the honest one.
func ValidProviderInstanceID(id string) bool {
	base, slug, instanced := strings.Cut(id, ":")
	if !providerExists(base) {
		return false
	}
	if !instanced {
		return true
	}
	if base != ProviderStashbox {
		return false
	}
	return validProviderSlug(slug)
}

func providerExists(id string) bool {
	for _, p := range providers {
		if p.ID == id {
			return true
		}
	}
	return false
}

func validProviderSlug(slug string) bool {
	if slug == "" || len(slug) > 32 {
		return false
	}
	for i := 0; i < len(slug); i++ {
		c := slug[i]
		switch {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '-' && i > 0:
		default:
			return false
		}
	}
	return true
}

// ProviderSlug derives an instance slug from a user-supplied display name:
// lowercased, every character outside the slug alphabet folded to '-', runs of
// '-' collapsed, the ends trimmed, and the result capped at 32 characters.
//
// It is a suggestion, not a guarantee. A name made entirely of characters the
// alphabet has no room for ("日本") yields "", and the caller minting the
// instance has to supply an id of its own rather than store an empty slug.
func ProviderSlug(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		default:
			// Collapse on the way in, so the cap below counts real characters
			// rather than a run of separators.
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	slug := b.String()
	if len(slug) > 32 {
		slug = slug[:32]
	}
	// The cap can land mid-separator, and trailing dashes were legal until the
	// cut made them final.
	return strings.TrimRight(slug, "-")
}

// ProviderServes reports whether the provider named by id may be chained on a
// library of the given kind. An unknown id serves nothing — like
// ValidSeriesKind, a caller mistake is rejected at the edge rather than
// defaulted.
//
// This is the CHAIN question only. "Can this provider resolve a ref of that
// vocabulary" is ProviderLooksUp, and no caller may substitute one for the
// other: doing so is what widens a registry entry to admit an add.
//
// Instance ids are answered on their base: which kinds a provider serves is a
// property of the compiled client, and every instance of stash-box speaks
// stash-box. A malformed instance id serves nothing, which is what keeps
// `tmdb:anything` out of a chain that only ever checks this function.
func ProviderServes(id, kind string) bool {
	if !ValidProviderInstanceID(id) {
		return false
	}
	base := ProviderBase(id)
	for _, p := range providers {
		if p.ID != base {
			continue
		}
		for _, k := range p.Kinds {
			if k == kind {
				return true
			}
		}
	}
	return false
}

// ProviderLooksUp reports whether the provider named by id can be asked to
// resolve a ref in the given item vocabulary — MediaTypeMovie, MediaTypeSeries
// or MediaTypeScene.
//
// It is ProviderServes' twin for the OTHER question, and the pair is deliberate:
// a ref is validated against what the catalogue files, a chain against what the
// shelf is filed under. Keeping one function for both is what made the registry
// widen its chain kinds to admit an add, which is a chain nobody chose.
//
// Unknown ids and instance ids follow ProviderServes exactly: an unknown id
// looks nothing up, and an instance is answered on its base, because which
// vocabularies a catalogue files is a property of the compiled client.
func ProviderLooksUp(id, mediaType string) bool {
	if !ValidProviderInstanceID(id) {
		return false
	}
	base := ProviderBase(id)
	for _, p := range providers {
		if p.ID != base {
			continue
		}
		for _, m := range p.Lookups {
			if m == mediaType {
				return true
			}
		}
	}
	return false
}

// ProviderCredentialSetting returns the settings key holding the provider's API
// key, or "" when the provider needs none.
//
// An unknown id gets the same "" for the same reason ProviderServes gives an
// unknown id nothing: there is no credential of this kind to read either way,
// and a caller that reads the answer as "keyless" is right on both counts.
func ProviderCredentialSetting(id string) string {
	for _, p := range providers {
		if p.ID == id {
			return p.CredentialSetting
		}
	}
	return ""
}

// DefaultProviderForKind returns the provider a library of the given kind gets
// when nobody chose one: the id the baseline seeds and the create form
// preselects. An unknown kind gets "" so the
// mistake surfaces as a validation failure, not as a working library wired to
// the wrong provider.
func DefaultProviderForKind(kind string) string {
	switch kind {
	case LibraryKindMovie, LibraryKindTV:
		return ProviderTMDB
	case LibraryKindAnime:
		return ProviderAniList
	case LibraryKindAdult:
		return ProviderStashbox
	}
	return ""
}
