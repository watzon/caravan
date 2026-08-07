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
}

// providers is a literal, not a mutable registry: what is compiled in is the
// only truth worth having, and a second, runtime-writable source of that truth
// is how the two drift apart. Adding a provider is one entry here plus a
// client implementation wired up in cmd/caravan.
var providers = []ProviderDescriptor{
	{ID: ProviderTMDB, Name: "TMDB", Kinds: []string{LibraryKindMovie, LibraryKindTV}},
	{ID: ProviderStashbox, Name: "Stash-box", Kinds: []string{LibraryKindAdult}},
	// AniList serves television only. Anime films exist, but AniList's own
	// vocabulary files them as a MEDIA entry of format MOVIE under the same
	// anime catalogue, and internal/anilist answers GetMovie with
	// ErrProviderKindUnsupported rather than pretend otherwise. Claiming the
	// movie kind here would let a movie library be created against a provider
	// that refuses every movie lookup.
	{ID: ProviderAniList, Name: "AniList", Kinds: []string{LibraryKindTV}},
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
