package api

import (
	"context"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/library"
)

// Manager is the slice of the library manager (internal/library) the HTTP
// layer needs: everything that touches the filesystem or a metadata provider.
// Read-only endpoints go straight to the store instead.
//
// It is declared here, as an interface, rather than taking *library.Manager
// directly so this package compiles and tests without the filesystem half of
// the application. *library.Manager is expected to satisfy it; where a
// signature differs, the wiring in cmd/caravan adapts.
type Manager interface {
	// Scan reconciles the database with the storage root. The API triggers it
	// in the background and reports progress through events, so the scan's
	// own result value is deliberately not part of this interface.
	Scan(ctx context.Context) error

	// AddMovie adds a movie to the library by provider ref, fetching its
	// metadata, and returns the stored movie. minAvailability is the release
	// stage its automatic search waits for; an empty string keeps an existing
	// row's choice and defaults a new one.
	//
	// The ref carries the provider that identified the title as well as its id,
	// because the id alone does not say which vocabulary it is written in. The
	// HTTP bodies are still TMDB-shaped and the handlers build a TMDB ref from
	// them; a ref-accepting body is a later phase.
	//
	// monitored is the add dialog's "Add and monitor" checkbox. Nil means
	// unmonitored, so a new row starts automation only after an explicit opt-in.
	// It applies to a NEW row only; re-adding something already in the library
	// keeps the owner's flag.
	AddMovie(ctx context.Context, ref core.ItemRef, minAvailability string, monitored *bool, libraryID int64) (*core.Movie, error)

	// AddSeries adds a series (with its seasons and episodes) by provider ref.
	// monitored is the series-level flag and reads exactly as AddMovie's; the
	// season and episode rows keep their own monitored semantics.
	AddSeries(ctx context.Context, ref core.ItemRef, monitored *bool, libraryID int64) (*core.Series, error)

	// RemoveMovie stops tracking a movie. With deleteFiles set it deletes the
	// movie's files from disk first; without it, only the rows go and a rescan
	// re-adds the movie. It reports store.ErrNotFound for an unknown id.
	RemoveMovie(ctx context.Context, id int64, deleteFiles bool) error

	// RemoveSeries is RemoveMovie's series twin, deleting every episode file
	// of the series when deleteFiles is set.
	RemoveSeries(ctx context.Context, id int64, deleteFiles bool) error

	// MatchUnmatched resolves a file parked in the scan-review queue against a
	// provider ref and imports it. mediaType is MediaTypeMovie,
	// MediaTypeSeries, or MediaTypeScene. Series numbering comes from the
	// parked parse; a scene ref names the exact provider scene.
	MatchUnmatched(ctx context.Context, unmatchedID int64, mediaType string, ref core.ItemRef) error

	// AddSite adds an adult site by stash-box id, as a series of kind adult
	// with its scenes as episodes. It is AddSeries' counterpart, and it is a
	// separate method for the reason core.AdultMetadataProvider is a separate
	// interface: a stash-box id is a UUID string, not the int64 TMDB hands out.
	//
	// It reports library.ErrAdultDisabled when the module is switched off. That
	// path is not reachable through the HTTP layer — every route that calls
	// this sits behind requireAdult — but the manager is the thing that owns
	// the invariant, and a second caller is one refactor away.
	//
	// It does NOT walk the site's scene catalogue: that is hundreds of provider
	// round trips for a large site, and it is a core.JobSyncSite the caller
	// queues (see handleAddSite). monitored reads as AddMovie's does.
	//
	// ref names the stash-box INSTANCE the id was read from beside the id, for
	// AddMovie's reason: a UUID is only an identity together with the box that
	// minted it, and two boxes hold the same UUID under different sites. An
	// empty provider means the legacy instance, which is what a client written
	// before instances sends.
	AddSite(ctx context.Context, ref core.ItemRef, monitored *bool, libraryID int64) (*core.Series, error)

	// AddSiteAndWait is AddSite with the catalogue walked before it returns.
	//
	// Approving a scene request is the one caller that needs it, and needs it
	// for a reason the split makes easy to lose: the approval grants a specific
	// scene, and a scene is an episode row that the walk is the only thing that
	// creates. Queueing the walk instead would answer "approved" for a request
	// that has, at that moment, made nothing wanted.
	AddSiteAndWait(ctx context.Context, ref core.ItemRef, monitored *bool, libraryID int64) (*core.Series, error)

	// SearchLibrary identifies a title through one library's whole provider
	// chain, merging what every provider on it offered. libraryID 0 means the
	// kind's default library for mediaType, which is the shelf an add made
	// without choosing one would land on anyway.
	//
	// It is the search half of the ref-based world: the hits carry the provider
	// that offered them, so the add that follows can name what the user picked
	// rather than assume TMDB.
	//
	// A provider that failed while others answered is a SearchHits.Failure and
	// the call still succeeds — one provider being down must not hide the rest
	// of the chain's hits. An error means the whole chain failed (its first
	// failure's error, so core.ErrMetadataUnauthorized survives) or that
	// nothing on the chain is configured at all (core.ErrNoMetadataProvider).
	SearchLibrary(ctx context.Context, libraryID int64, mediaType, q string) (*library.SearchHits, error)

	// Metadata returns the configured metadata provider, or nil when none is
	// configured (no TMDB API key yet). The search endpoint reports that as a
	// 503 rather than pretending there are no results.
	Metadata() core.MetadataProvider

	// ValidateMetadataKey proves apiKey against one metadata provider with a
	// single live call, reporting nil when the provider accepted it and an error
	// wrapping core.ErrMetadataUnauthorized when it rejected it.
	//
	// providerID is explicit because "the metadata provider" stopped being
	// singular: a library chains several, more than one of them can want a key,
	// and a key is only meaningful against the provider it was issued by. An id
	// that names no credentialed provider is an error rather than a default,
	// since defaulting would prove some other provider's key and report the
	// answer as this one's.
	//
	// It takes the key rather than reading the settings table because the two
	// callers that matter test a key that is not stored yet: the first-run
	// wizard, which proves the credential before writing it, and the settings
	// Test button, which proves what is in the field rather than what was last
	// saved.
	//
	// pin is TheTVDB's subscriber PIN. Empty means "use the stored one", which
	// is what the settings Test button needs. First run sends the unsaved PIN
	// so a user-supported key can be proved before either half is written.
	// TMDB ignores it.
	ValidateMetadataKey(ctx context.Context, providerID, apiKey, pin string) error

	// ValidateAdultCredential is ValidateMetadataKey's stash-box twin. The
	// endpoint travels with the key because a stash-box credential is only
	// meaningful against the endpoint it was issued by; a blank endpoint means
	// the TPDB preset, exactly as it does in the settings table.
	//
	// It is what the instance routes run before they write a row, so an
	// endpoint that has never answered cannot end up on a screen looking as
	// though it were configured.
	ValidateAdultCredential(ctx context.Context, endpoint, apiKey string) error

	// AdultMetadataFor returns the provider for ONE configured stash-box
	// instance, or nil.
	//
	// Nil is the answer when the module is switched off, when no instance
	// answers to that id, and when the one that does has no credential. The
	// first is load-bearing: it is the wiring's own guard, independent of
	// requireAdult and of library.adultReady, so the "zero stash-box traffic
	// when disabled" acceptance survives any one of the three being got wrong.
	// The nil must be a genuine untyped nil rather than a typed nil pointer,
	// because callers test the interface value (see Metadata).
	//
	// There is deliberately no fallback for an id nothing answers to. The refs
	// on a pinned item were minted by one catalogue, and asking a different one
	// about them does not fail — it answers about something else.
	AdultMetadataFor(ctx context.Context, providerID string) core.AdultMetadataProvider

	// DefaultAdultMetadata is the provider a surface that names no instance
	// answers from, together with the id it resolved to — so the answer can say
	// which box it came from, which is the whole point of the `provider` field
	// on the site and scene DTOs.
	//
	// The choice is the default adult library's chain head, or the oldest
	// instance when it names none. Both halves are nil/"" when the module is off.
	DefaultAdultMetadata(ctx context.Context) (core.AdultMetadataProvider, string)
}

// Media types accepted by POST /import/queue/{id}/match and POST /requests,
// and reported by GET /search and the discover endpoints. They alias the core
// constants because the same strings are stored in requests.media_type: one
// spelling, one place to change it.
//
// MediaTypeScene is only ever accepted from a caller the adult gate would let
// through; see handleCreateRequest for why an ungranted one is answered as
// though the value did not exist at all.
const (
	MediaTypeMovie  = core.MediaTypeMovie
	MediaTypeSeries = core.MediaTypeSeries
	MediaTypeScene  = core.MediaTypeScene
)
