// Package library owns the on-disk media library: scanning it, matching what
// it finds against a metadata provider, reconciling the database against the
// filesystem, and organizing files into the Jellyfin layout (SPEC §5.1, §6).
//
// The import pipeline lives here too (ImportDownload, RunWatcher) rather than
// in a package of its own, because importing a finished download *is* the
// organize path with a grab in front of it: everything the pipeline does is a
// store call or one of the reconcile helpers below. It reaches the download
// engine only through core.Engine, so it never depends on which engine ran.
//
// Path model (SPEC §1.2 pillar 3): every path this package stores is relative
// to the storage root and uses forward slashes, so a database written on Linux
// resolves on Windows. Absolute paths exist only inside this package, at the
// filesystem boundary. The library lives under "library/" within the storage
// root (SPEC §2.3, §6), so a stored path reads
// "library/Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv".
//
// Disposability (SPEC §1.2 pillar 2): the filesystem is the source of truth.
// Scan is idempotent and additive against the metadata provider, so deleting
// every row and rescanning converges on the same library.
package library

import (
	"context"
	"net/http"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/parse"
	"github.com/watzon/caravan/internal/store"
)

// Layout constants. These are the fixed folder names SPEC §6 promises to
// players; naming *templates* are a later phase, the layout is not.
const (
	// LibraryDir is the library's directory inside the storage root.
	LibraryDir = "library"
	// MoviesDir and TVDir are the two library sections.
	MoviesDir = "Movies"
	TVDir     = "TV"
)

// Media types accepted by ImportUnmatched.
const (
	MediaTypeMovie  = "movie"
	MediaTypeSeries = "series"
)

// defaultMinConfidence is the parser confidence below which a file parks in
// the unmatched queue rather than being matched against the provider. A shaky
// parse produces a shaky search query, and a wrong match is worse than a
// visible question (SPEC §10.1, §13).
const defaultMinConfidence = 0.5

// posterTimeout bounds a single poster download. A slow image host must not
// stall a library scan.
const posterTimeout = 30 * time.Second

// Manager scans, matches, and organizes the media library.
type Manager struct {
	store    *store.Store
	provider core.MetadataProvider
	// adult answers for series of kind adult, and is nil when no stash-box
	// credential is configured. It is a second provider rather than a second
	// implementation of the first because the two describe different worlds —
	// see core.AdultMetadataProvider for why. Nothing here reaches it without
	// going through adultReady, which is what makes "zero stash-box traffic
	// when the module is off" a property of one function rather than of every
	// call site.
	adult core.AdultMetadataProvider
	root  string

	// parse turns a filename into a ParsedRelease. It is a field rather than a
	// direct call so tests can drive matching and reconciliation with
	// deterministic input instead of tracking the parser's heuristics.
	parse func(name string) core.ParsedRelease
	// parseScene is parse's date-based counterpart, used for files under the
	// adult library root. Which parser reads a name is decided by where the
	// file is, never by what the name looks like (see parse.Scene).
	parseScene func(name string) core.ParsedRelease
	// link is os.Link. Overridable so the no-hardlink fallback (exFAT,
	// cross-device) is testable without a second filesystem.
	link func(oldname, newname string) error
	// hc fetches posters.
	hc *http.Client
	// minConfidence is the parking threshold described on defaultMinConfidence.
	minConfidence float64
	// syncedSites remembers which sites a single Scan has already walked the
	// stash-box catalogue for. Without it, every scene file whose date the
	// library does not know would walk the same catalogue again — see
	// matchAndImportScene. It is scan-scoped state and Scan is the only writer.
	syncedSites map[int64]bool
	// notify is the playback handoff, or nil when none is configured.
	notify Notifier
	// notifyAdult is the adult library's handoff (Stash), or nil. It is a
	// separate field from notify because the two are told about disjoint sets of
	// imports; see AdultNotifier.
	notifyAdult AdultNotifier
}

// Notifier is told after an import puts new files in the library, so playback
// handoff can react (SPEC §5.2: an import triggers a Jellyfin library scan).
//
// It is an interface here rather than the handoff itself so the import pipeline
// neither imports nor waits on it. The contract is that LibraryChanged records
// intent and returns promptly — it must not make the network call itself —
// because an import that a sleeping media server can slow down or fail is worse
// than a handoff that arrives a moment late.
type Notifier interface {
	LibraryChanged(ctx context.Context) error
}

// AdultNotifier is Notifier's adult twin, told after scenes land in the adult
// library (SPEC §1.2; PLAN phase 11: an adult import triggers a scoped Stash
// scan and an identity push).
//
// It is a second interface rather than a kind argument on Notifier because the
// two notifications go to different places for different reasons, and the split
// is what makes the exposure rule structural — in the direction that matters.
// Nothing adult reaches the Stash-specific push by accident, and nothing
// television reaches it at all: only importDownloadedScenes calls this, and the
// handoff re-checks the series kind before it pushes.
//
// The guarantee is one-directional, and the other direction is deliberate. An
// adult import still fires the generic Notifier as well (ImportDownload's
// libraryChanged runs on any import that landed a file), because "the library
// changed" is true — Caravan does not know which of the user's Jellyfin
// libraries covers which directory, and suppressing the refresh would leave a
// television-shaped Jellyfin stale after a mixed download. Keeping adult files
// out of a playback server is a matter of where that server's libraries are
// rooted, not of which rescans Caravan skips.
//
// It carries the episode ids that were imported, because unlike "the library
// changed" the identity push is per scene: Stash has to be told which scenes to
// look at. Ids rather than rows for the reason the job payload carries ids —
// the notifier records intent and the work happens later, so the values must be
// re-read then rather than captured now.
//
// The contract is Notifier's: AdultLibraryChanged records intent and returns
// promptly. It must not make the network call itself.
type AdultNotifier interface {
	AdultLibraryChanged(ctx context.Context, episodeIDs []int64) error
}

// Option configures a Manager at construction.
type Option func(*Manager)

// WithNotifier attaches a playback handoff. Without one, imports simply do not
// notify anything.
func WithNotifier(n Notifier) Option {
	return func(m *Manager) { m.notify = n }
}

// WithAdultNotifier attaches the adult library's handoff. Without one, scene
// imports simply do not notify anything.
func WithAdultNotifier(n AdultNotifier) Option {
	return func(m *Manager) { m.notifyAdult = n }
}

// WithAdultProvider attaches the stash-box metadata provider. Without one,
// every adult path reports core.ErrNoAdultProvider and the recurring sweeps
// no-op, which is what a server with the module enabled but no credential
// entered yet looks like.
func WithAdultProvider(p core.AdultMetadataProvider) Option {
	return func(m *Manager) { m.adult = p }
}

// NewManager returns a Manager rooted at the storage root.
//
// mp may be nil: without a metadata provider every scanned file parks in the
// unmatched queue with the parser's guess instead of the scan failing
// (SPEC §13 — import match failures are visible, never fatal).
func NewManager(st *store.Store, mp core.MetadataProvider, root string, opts ...Option) *Manager {
	m := &Manager{
		store:         st,
		provider:      mp,
		root:          cleanRoot(root),
		parse:         parse.Parse,
		parseScene:    parse.Scene,
		link:          osLink,
		hc:            &http.Client{Timeout: posterTimeout},
		minConfidence: defaultMinConfidence,
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

// libraryChanged tells the playback handoff that new files landed.
//
// A handoff that cannot be recorded is a warning in the feed, never an error:
// the files are in the library either way, and failing the import — which would
// make the job queue retry a completed import — because a media server could
// not be told about it inverts what matters (SPEC §13).
func (m *Manager) libraryChanged(ctx context.Context) {
	if m.notify == nil {
		return
	}
	if err := m.notify.LibraryChanged(ctx); err != nil {
		_ = m.store.InsertEvent(ctx, &core.Event{
			Level:    core.EventLevelWarn,
			Category: EventCategoryImport,
			Message:  "Playback handoff could not be notified",
			Detail:   err.Error(),
		})
	}
}

// adultLibraryChanged tells the adult handoff that new scenes landed, for the
// reasons and with the failure model libraryChanged has: the files are in the
// library either way, and failing a completed import because Stash could not be
// told about it inverts what matters (SPEC §13).
func (m *Manager) adultLibraryChanged(ctx context.Context, episodeIDs []int64) {
	if m.notifyAdult == nil || len(episodeIDs) == 0 {
		return
	}
	if err := m.notifyAdult.AdultLibraryChanged(ctx, episodeIDs); err != nil {
		_ = m.store.InsertEvent(ctx, &core.Event{
			Level:    core.EventLevelWarn,
			Category: core.EventCategoryAdultOnly,
			Message:  "Adult library handoff could not be notified",
			Detail:   err.Error(),
		})
	}
}
