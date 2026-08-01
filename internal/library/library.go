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
	root     string

	// parse turns a filename into a ParsedRelease. It is a field rather than a
	// direct call so tests can drive matching and reconciliation with
	// deterministic input instead of tracking the parser's heuristics.
	parse func(name string) core.ParsedRelease
	// link is os.Link. Overridable so the no-hardlink fallback (exFAT,
	// cross-device) is testable without a second filesystem.
	link func(oldname, newname string) error
	// hc fetches posters.
	hc *http.Client
	// minConfidence is the parking threshold described on defaultMinConfidence.
	minConfidence float64
}

// NewManager returns a Manager rooted at the storage root.
//
// mp may be nil: without a metadata provider every scanned file parks in the
// unmatched queue with the parser's guess instead of the scan failing
// (SPEC §13 — import match failures are visible, never fatal).
func NewManager(st *store.Store, mp core.MetadataProvider, root string) *Manager {
	return &Manager{
		store:         st,
		provider:      mp,
		root:          cleanRoot(root),
		parse:         parse.Parse,
		link:          osLink,
		hc:            &http.Client{Timeout: posterTimeout},
		minConfidence: defaultMinConfidence,
	}
}
