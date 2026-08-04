package api

import (
	"context"

	"github.com/watzon/caravan/internal/core"
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

	// AddMovie adds a movie to the library by provider id, fetching its
	// metadata, and returns the stored movie. minAvailability is the release
	// stage its automatic search waits for; an empty string keeps an existing
	// row's choice and defaults a new one.
	//
	// monitored is the add dialog's "Add and monitor" checkbox. Nil is the
	// historical behaviour — monitored — and is what every caller that has no
	// opinion passes, including request approval. It applies to a NEW row only;
	// re-adding something already in the library keeps the owner's flag.
	AddMovie(ctx context.Context, tmdbID int64, minAvailability string, monitored *bool) (*core.Movie, error)

	// AddSeries adds a series (with its seasons and episodes) by provider id.
	// monitored is the series-level flag and reads exactly as AddMovie's; the
	// season and episode rows keep their own monitored semantics.
	AddSeries(ctx context.Context, tmdbID int64, monitored *bool) (*core.Series, error)

	// RemoveMovie stops tracking a movie. With deleteFiles set it deletes the
	// movie's files from disk first; without it, only the rows go and a rescan
	// re-adds the movie. It reports store.ErrNotFound for an unknown id.
	RemoveMovie(ctx context.Context, id int64, deleteFiles bool) error

	// RemoveSeries is RemoveMovie's series twin, deleting every episode file
	// of the series when deleteFiles is set.
	RemoveSeries(ctx context.Context, id int64, deleteFiles bool) error

	// MatchUnmatched resolves a file parked in the scan-review queue against a
	// provider id and imports it. mediaType is MediaTypeMovie or
	// MediaTypeSeries; for a series, the season and episode numbers come from
	// the parked file's parsed guess.
	MatchUnmatched(ctx context.Context, unmatchedID int64, mediaType string, tmdbID int64) error

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
	AddSite(ctx context.Context, stashID string, monitored *bool) (*core.Series, error)

	// AddSiteAndWait is AddSite with the catalogue walked before it returns.
	//
	// Approving a scene request is the one caller that needs it, and needs it
	// for a reason the split makes easy to lose: the approval grants a specific
	// scene, and a scene is an episode row that the walk is the only thing that
	// creates. Queueing the walk instead would answer "approved" for a request
	// that has, at that moment, made nothing wanted.
	AddSiteAndWait(ctx context.Context, stashID string, monitored *bool) (*core.Series, error)

	// Metadata returns the configured metadata provider, or nil when none is
	// configured (no TMDB API key yet). The search endpoint reports that as a
	// 503 rather than pretending there are no results.
	Metadata() core.MetadataProvider

	// AdultMetadata returns the configured adult metadata provider, or nil.
	//
	// Nil is the answer both when no stash-box credential has been entered and
	// when the module is switched off, and the second half is load-bearing: it
	// is the wiring's own guard, independent of requireAdult and of
	// library.adultReady, so the "zero stash-box traffic when disabled"
	// acceptance survives any one of the three being got wrong. The nil must be
	// a genuine untyped nil rather than a typed nil pointer, because callers
	// test the interface value (see Metadata).
	AdultMetadata() core.AdultMetadataProvider
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
