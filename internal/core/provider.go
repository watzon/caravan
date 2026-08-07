package core

// Metadata provider ids (stored in `libraries.provider`). A provider id names
// a client implementation compiled into the binary; the registry below is the
// single statement of which ids exist and which library kinds each can serve.
// Instantiation stays in cmd/caravan — this package only answers "is this id
// valid for that kind", so the store and the API can validate a library row
// without knowing how a client is built or credentialed.
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
	// Kinds lists the LibraryKind* values the provider can serve. A provider
	// that speaks movies and television is not thereby qualified to serve an
	// adult library: the vocabularies (TMDB ids vs stash ids) do not overlap.
	Kinds []string
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
var providers = []ProviderDescriptor{
	{ID: ProviderTMDB, Name: "TMDB", Kinds: []string{LibraryKindMovie, LibraryKindTV},
		CredentialSetting: "tmdb_api_key"},
	// Stash-box is credentialed and yet carries no CredentialSetting: the adult
	// half keeps its own door. Its endpoint and key are proved together by
	// ValidateAdultCredential, guarded on the way in by the enable gate and by
	// guardAdultCredentialEdit, and a credential that is only meaningful beside
	// the endpoint that issued it is not the same object as a bare metadata API
	// key. Listing it here would put it in the metadata credential map and in
	// the two loops that read that map, where neither of those guards runs.
	{ID: ProviderStashbox, Name: "Stash-box", Kinds: []string{LibraryKindAdult}},
	// AniList serves television only. Anime films exist, but AniList's own
	// vocabulary files them as a MEDIA entry of format MOVIE under the same
	// anime catalogue, and internal/anilist answers GetMovie with
	// ErrProviderKindUnsupported rather than pretend otherwise. Claiming the
	// movie kind here would let a movie library be created against a provider
	// that refuses every movie lookup.
	{ID: ProviderAniList, Name: "AniList", Kinds: []string{LibraryKindTV}},
	// TVmaze catalogues television and nothing else — there is no film half of
	// its catalogue to claim — and internal/tvmaze answers GetMovie with
	// ErrProviderKindUnsupported. Claiming the movie kind here would let a
	// movie library be created against a provider that refuses every movie
	// lookup.
	{ID: ProviderTVmaze, Name: "TVmaze", Kinds: []string{LibraryKindTV}},
	// TheTVDB is television-only HERE, and the movie half is a deliberate
	// omission rather than a gap in the catalogue: TheTVDB does hold films.
	//
	// MovieMeta.DigitalRelease is what gates minimum availability
	// (internal/wanted/list.go:199), and TheTVDB's movie record carries no typed
	// release list to fill it from. A movie mapped through here would arrive with
	// a zero digital release, which reads as "released long ago" and starts
	// grabbing a film that is still in cinemas — a wrong automation decision, not
	// a missing field on a detail page. Claiming the movie kind therefore waits
	// on a release-date mapping design, and until then internal/thetvdb answers
	// GetMovie with ErrProviderKindUnsupported so the two cannot disagree.
	{ID: ProviderTheTVDB, Name: "TheTVDB", Kinds: []string{LibraryKindTV},
		CredentialSetting: "thetvdb_api_key"},
}

// Providers returns the compiled-in provider descriptors. The result is a
// fresh copy: callers get a list to render, not a handle on the registry.
func Providers() []ProviderDescriptor {
	out := make([]ProviderDescriptor, len(providers))
	copy(out, providers)
	return out
}

// ProviderServes reports whether the provider named by id can serve a library
// of the given kind. An unknown id serves nothing — like ValidSeriesKind, a
// caller mistake is rejected at the edge rather than defaulted.
func ProviderServes(id, kind string) bool {
	for _, p := range providers {
		if p.ID != id {
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
// when nobody chose one: the id migration 0022 backfills onto pre-existing
// rows, and the id the create form preselects. An unknown kind gets "" so the
// mistake surfaces as a validation failure, not as a working library wired to
// the wrong provider.
func DefaultProviderForKind(kind string) string {
	switch kind {
	case LibraryKindMovie, LibraryKindTV:
		return ProviderTMDB
	case LibraryKindAdult:
		return ProviderStashbox
	}
	return ""
}
