package core

import (
	"context"
	"time"
)

// DownloadID is an engine-native handle for one download: an info hash for the
// embedded torrent engine, an nzo_id for SABnzbd, and so on. It is a string
// rather than an integer precisely because every engine names its downloads
// differently.
type DownloadID string

// DownloadState is the lifecycle state of a download.
type DownloadState string

// Download states. These are the vocabulary the queue UI colors by, so they
// are deliberately few: engine-specific sub-states collapse into these.
const (
	DownloadQueued      DownloadState = "queued"
	DownloadDownloading DownloadState = "downloading"
	DownloadSeeding     DownloadState = "seeding"
	DownloadCompleted   DownloadState = "completed"
	DownloadFailed      DownloadState = "failed"
	DownloadPaused      DownloadState = "paused"
)

// DownloadPhase is the sub-step a download is in while its state is
// DownloadDownloading.
//
// It exists because a Usenet download is three jobs, not one: fetching the
// articles, repairing the holes with par2, and unpacking the archives
// (SPEC §5.1, PLAN phase 7). All three are "downloading" as far as the queue's
// state machine is concerned — the user is still waiting and the item is not
// importable — but "repairing" and "extracting" are the difference between a
// stalled download and one that is nearly done, so the queue says which.
//
// A torrent has no sub-steps and reports no phase.
type DownloadPhase string

// Download phases. The empty phase means "no sub-step", which is what every
// engine but the embedded Usenet one reports.
const (
	PhaseDownloading DownloadPhase = "downloading"
	PhaseRepairing   DownloadPhase = "repairing"
	PhaseExtracting  DownloadPhase = "extracting"
)

// DownloadStatus is a live snapshot of one download, as the engine sees it
// right now. It is not persisted as-is: rates and ETA are meaningless a second
// later. The durable half lives in Download.
type DownloadStatus struct {
	ID    DownloadID
	State DownloadState
	// Phase is the sub-step of a multi-stage download, and "" for an engine
	// that has none. It is live-only: like the transfer rates, it describes
	// what is happening right now and is meaningless once read back from a
	// restart, so it is deliberately absent from Download.
	Phase DownloadPhase
	// Name is the download's display name (the torrent name, the nzb name).
	Name string
	// Progress is completion in [0,1].
	Progress float64
	// BytesDone is how much of Size has been written.
	BytesDone int64
	// Size is the total download size in bytes, 0 when not yet known (a magnet
	// link has no size until its metadata arrives).
	Size int64
	// DownRate and UpRate are current transfer rates in bytes per second.
	DownRate int64
	UpRate   int64
	// ETASeconds is the estimated time to completion, -1 when unknown.
	ETASeconds int64
	// Ratio is uploaded over downloaded, for seeding limits.
	Ratio float64
	// SavePath is where the engine is writing.
	//
	// The embedded engine reports it relative to the storage root (SPEC §1.2
	// pillar 3). An external download client reports its own absolute path
	// instead, because the directory it writes into is its configuration, not
	// Caravan's (PLAN phase 6). Such a path is live download state only: it is
	// read to locate a finished payload and is never stored in `media_files`,
	// `unmatched_files`, or `downloads.output_path`.
	SavePath string
	// Engine names the backend holding this download ("embedded",
	// "qbittorrent", …). It is set by the router, which is the only thing that
	// knows which of several engines answered; a single engine leaves it empty
	// and the caller falls back to the provider's name.
	Engine string
	// Error is the engine's failure message; empty unless State is
	// DownloadFailed.
	Error string
}

// RemotePathMapping translates an absolute path reported by an external
// download client into the path where the same files are mounted on Caravan's
// host. The importer applies the longest matching RemotePath component prefix.
type RemotePathMapping struct {
	ID            int64
	RemotePath    string
	LocalPath     string
	MatchCount    int64
	LastMatchedAt time.Time
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// AddOpts is the routing context handed to an engine along with a Release: it
// says what the grab was *for*, so the import pipeline can match the finished
// data back to a library item without re-guessing (SPEC §5.1).
type AddOpts struct {
	// Category is the engine-side category/label, for users who sort their
	// client by it.
	Category string
	// MovieID references movies.id for a movie grab; 0 otherwise.
	MovieID int64
	// SeriesID references series.id for an episode or season grab; 0 otherwise.
	SeriesID int64
	// SeasonNum is the season a season-pack grab covers.
	SeasonNum int
	// EpisodeIDs are the episodes.id values the grab is expected to satisfy.
	// A season pack lists all of them.
	EpisodeIDs []int64
	// LibraryID reads exactly as GrabInfo.LibraryID does.
	LibraryID int64
	// Paused adds the download without starting it.
	//
	// It is how a concurrency cap reaches an external download client. Caravan
	// cannot hold an NZB back and hand it over later without inventing a second
	// identity for a download that has no client-side id yet, so it hands the
	// release over immediately and tells the client not to start: the client
	// does no work, transfers nothing, and Caravan unpauses it when a slot
	// frees. The built-in engines have their own way of holding a download and
	// ignore this.
	Paused bool
}

// Engine is a download backend (SPEC §5.1). The embedded torrent engine is the
// default; qBittorrent, SABnzbd and NZBGet implement the same interface in
// phase 6, which is why nothing here is torrent-specific.
type Engine interface {
	// Add starts downloading r and returns the engine's handle for it.
	Add(ctx context.Context, r Release, opts AddOpts) (DownloadID, error)
	// Status returns a live snapshot of one download.
	Status(ctx context.Context, id DownloadID) (*DownloadStatus, error)
	// List returns a live snapshot of every download the engine knows about.
	List(ctx context.Context) ([]DownloadStatus, error)
	// Pause stops transferring without discarding progress.
	Pause(ctx context.Context, id DownloadID) error
	// Resume restarts a paused download.
	Resume(ctx context.Context, id DownloadID) error
	// Remove drops the download, and its downloaded data when deleteData is
	// set. It never touches the library: an imported file is a hardlink or a
	// move away from the download data, and removing a download must not cost
	// media (SPEC §13).
	Remove(ctx context.Context, id DownloadID, deleteData bool) error
	// Close shuts the engine down cleanly, flushing whatever state it needs to
	// resume after a restart.
	Close() error
}

// EnginePager is the optional bounded listing seam. before is the backend
// native ID of the last emitted status, and next is empty at the end.
type EnginePager interface {
	ListPage(ctx context.Context, limit int, before DownloadID) ([]DownloadStatus, DownloadID, error)
}

// EngineInsight is an optional extension for engines that can report
// torrent-specific diagnostic information. External clients in phase 6 may
// omit it without losing the core queue contract.
type EngineInsight interface {
	Insight(ctx context.Context, id DownloadID) (*DownloadInsight, error)
}

// EngineRetry is an optional extension for engines that can put a failed
// download back to work, picking up from whatever it had already achieved.
//
// It is capability-gated rather than folded into Resume because "try that
// again" only means something where a download is several stages and a failure
// can belong to one of them: a Usenet download that fetched fifteen gigabytes
// and then failed to unpack them has one stage to redo, not the whole job. A
// torrent has no such structure — its failures are about the swarm, and Resume
// is already the whole of the answer — so the embedded torrent engine
// deliberately does not implement this and the UI does not offer it.
//
// Retry is for failures only. Asking it to retry a download that has not
// failed is an error, not a no-op, because the caller has misunderstood the
// state it is acting on.
type EngineRetry interface {
	Retry(ctx context.Context, id DownloadID) error
}

// EngineRouting is an optional extension for an engine that is really several
// engines behind one interface, dispatching a release by its protocol
// (SPEC §5.1, PLAN phase 6, task 3).
//
// It exists so the caller that records a download can write the backend that
// actually holds it into `downloads.engine`, rather than the name of the
// router. A protocol nothing is configured for reports "".
type EngineRouting interface {
	EngineNameFor(ctx context.Context, protocol string) string
}

// EngineRateLimits is an optional extension for engines that support live
// global and per-download transfer limits. Values are KB/s; zero means
// unlimited globally and inherit the global limit per download.
type EngineRateLimits interface {
	SetGlobalRates(ctx context.Context, downKbps, upKbps int64) error
	SetDownloadRates(ctx context.Context, id DownloadID, downKbps, upKbps int64) error
}

// PeerInsight is the observable state of one connected peer.
type PeerInsight struct {
	Addr     string  `json:"addr"`
	Client   string  `json:"client"`
	Progress float64 `json:"progress"`
	DownRate int64   `json:"down_rate"`
	UpRate   int64   `json:"up_rate"`
}

// TrackerInsight is a configured tracker. Seeder and leecher counts are zero
// when the engine has not scraped the tracker.
type TrackerInsight struct {
	URL      string `json:"url"`
	Status   string `json:"status"`
	Seeders  int    `json:"seeders"`
	Leechers int    `json:"leechers"`
}

// UsenetFileInsight is one file inside an NZB, as the pipeline sees it right
// now: a Usenet download is a set of files assembled from segments, and which
// of them are whole is the equivalent of a torrent's piece map.
type UsenetFileInsight struct {
	// Name is the file's name inside the download directory.
	Name string `json:"name"`
	// Segments is how many segments the file was posted in, and SegmentsDone
	// how many of them are on disk. SegmentsFailed counts the ones this run
	// gave up on — holes for par2, not a reason to stop.
	Segments       int  `json:"segments"`
	SegmentsDone   int  `json:"segments_done"`
	SegmentsFailed int  `json:"segments_failed"`
	Complete       bool `json:"complete"`
	// Par2 marks a recovery volume rather than payload.
	Par2 bool `json:"par2"`
}

// DownloadInsight is the protocol-specific detail surfaced by the queue drawer.
//
// The two halves are disjoint and both optional. A torrent engine fills the
// peer half — Availability is the aggregate peer piece availability divided by
// piece count — and a Usenet engine fills the file half; every Usenet field is
// omitempty precisely so a torrent's insight JSON is exactly what it always
// was.
type DownloadInsight struct {
	Peers        []PeerInsight    `json:"peers"`
	Trackers     []TrackerInsight `json:"trackers"`
	Availability float64          `json:"availability"`

	// Files is one entry per file the NZB indexes, in NZB order.
	Files []UsenetFileInsight `json:"files,omitempty"`
	// The same counts aggregated, so the drawer does not have to sum a
	// thousand-file release on every poll.
	FilesComplete  int `json:"files_complete,omitempty"`
	Segments       int `json:"segments,omitempty"`
	SegmentsDone   int `json:"segments_done,omitempty"`
	SegmentsFailed int `json:"segments_failed,omitempty"`

	// DamagedSegments is how many articles verification found missing or
	// corrupt, and DamagedFiles names the files they belonged to. They are what
	// the repairing phase is working on. par2 reports no live progress, so
	// there is deliberately no percentage here: the UI shows an indeterminate
	// stage rather than a number nothing measures.
	DamagedSegments int      `json:"damaged_segments,omitempty"`
	DamagedFiles    []string `json:"damaged_files,omitempty"`
}

// Download is the persisted record of a download (the `downloads` table): the
// half of a download that has to survive a restart, as opposed to the live
// DownloadStatus the engine reports.
type Download struct {
	ID int64
	// GrabID references grabs.id — what this download was grabbed for. A soft
	// reference: 0 means the download was not started by a grab.
	GrabID int64
	// Engine names the backend ("embedded", "qbittorrent", …).
	Engine string
	// EngineID is the backend's handle, unique across downloads.
	EngineID DownloadID
	// Title is the download's display name.
	Title string
	// State is one of the Download* state constants.
	State DownloadState
	// Progress is completion in [0,1] as of UpdatedAt.
	Progress float64
	// BytesDone and Size mirror DownloadStatus as of UpdatedAt.
	BytesDone int64
	Size      int64
	// MaxDownRate and MaxUpRate are per-download byte-per-second overrides.
	// Zero inherits the engine-wide limits.
	MaxDownRate int64
	MaxUpRate   int64
	// SavePath is where the data landed, relative to the storage root.
	SavePath string
	// Error is the last failure message, empty when healthy.
	Error     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// GrabInfo says what a grab was for. It travels from the grab decision to the
// import pipeline so a finished download is matched to the item it was fetched
// for rather than re-guessed from its filename (SPEC §5.1).
type GrabInfo struct {
	// GrabID references grabs.id.
	GrabID int64
	// MovieID references movies.id for a movie grab; 0 otherwise.
	MovieID int64
	// SeriesID references series.id for an episode or season grab; 0 otherwise.
	SeriesID int64
	// SeasonNum is the season a season-pack grab covers.
	SeasonNum int
	// EpisodeIDs are the episodes.id values the grab is expected to satisfy.
	EpisodeIDs []int64
	// ReleaseTitle is the release name that was grabbed, kept so a stuck
	// import can tell the user what it was trying to import.
	ReleaseTitle string
	// LibraryID is the library the grab's payload belongs to. On a grab tied
	// to a movie or series it mirrors the item's own library; on a universal
	// search grab tied to nothing it is the WHOLE target — the finished
	// download parks in scan review scoped to this library.
	//
	// Zero is a grab that names no library, and unlike a movie's or a series'
	// library_id it is a real state rather than a gap: the queue, a pause and a
	// removal belong to no shelf. It is stored as SQL NULL for that reason.
	LibraryID int64
}

// Grab statuses.
const (
	GrabStatusGrabbed  = "grabbed"
	GrabStatusImported = "imported"
	GrabStatusFailed   = "failed"
	// GrabStatusCancelled marks a grab whose item left the library while the
	// download was still in flight: the download was withdrawn, nothing was
	// imported, and nothing failed.
	GrabStatusCancelled = "cancelled"
	// GrabStatusRejected marks a decision-log row rather than a grab: an
	// automatic search evaluated this release for the item and skipped it,
	// and Reason says why (PLAN phase 3, task 3). The row exists so "why was
	// this skipped" is answerable from the grabs history.
	GrabStatusRejected = "rejected"
)

// Grab is one entry in the grab history (the `grabs` table): which release was
// sent to an engine for which item, and why. Grabs are history — SPEC §7 says
// losing them costs explanation, never media.
type Grab struct {
	// GrabInfo is embedded, so GrabID is this row's id and the import pipeline
	// can be handed g.GrabInfo directly.
	GrabInfo
	// ReleaseID references releases.id; 0 when the release was not cached.
	ReleaseID int64
	// Reason records why this release won, or why one was skipped — the
	// answer to "why was this grabbed" in the UI (SPEC §5.1, phase 3).
	Reason string
	// Status is one of the GrabStatus* constants.
	Status    string
	CreatedAt time.Time
}

// Job kinds for the automation queue (SPEC §7): the two recurring roots and
// the per-item searches they fan out into.
//
// They live in core rather than in internal/automation because both sides of
// the queue name them: the automation runner registers handlers for them, and
// the API enqueues the same searches on demand. package automation imports
// package api, so the shared vocabulary cannot live in either one.
const (
	JobRSSSync       = "rss_sync"
	JobBacklogSweep  = "backlog_sweep"
	JobSearchMovie   = "search_movie"
	JobSearchEpisode = "search_episode"
	// JobRefreshMetadata re-fetches provider metadata for every monitored
	// title. It is what keeps release dates, series statuses and new seasons
	// current for titles that have no files yet — nothing else ever revisits
	// those, and the minimum-availability gate judges against their dates.
	JobRefreshMetadata = "refresh_metadata"
	// JobRecycleCleanup removes expired library recycle batches.
	JobRecycleCleanup = "recycle_cleanup"
	// JobNotificationDispatch delivers new activity events to configured webhooks.
	JobNotificationDispatch = "notification_dispatch"
	// JobIndexerHealth probes every enabled indexer and disables ones that
	// have failed IndexerHealthDisableAfter times in a row.
	JobIndexerHealth = "indexer_health"
	// JobSyncSite walks one adult site's whole scene catalogue into season and
	// episode rows.
	//
	// It is a durable job rather than part of the add because a big site is
	// hundreds of provider round trips: doing it inside POST /adult/sites made
	// the request hang long enough that people clicked Add twice. Deferring it
	// buys two things the synchronous walk never had — a half-added site
	// survives a restart, because the job outlives the request that made it,
	// and a second click dedupes on the payload like every other queued search.
	//
	// It is NOT the metadata refresh under another name. The refresh sweep only
	// visits MONITORED sites, and a site added unmonitored still needs its
	// catalogue: the rows are what the site page shows as missing.
	JobSyncSite = "sync_site"

	// JobMoveItem moves one library item's files and row into another library
	// of the same kind. Durable for the reason JobSyncSite is: a series can
	// be hundreds of files, and the HTTP request that asked must not own the
	// transfer. The handler is idempotent — a move to the library the item is
	// already in is a successful no-op — which is what at-least-once delivery
	// requires.
	JobMoveItem = "move_item"
)

// JobMoveItemPayload is JobMoveItem's payload. ItemType is MediaTypeMovie or
// MediaTypeSeries; ids rather than rows for the reason every payload carries
// ids — the values are re-read when the job runs, not captured when queued.
type JobMoveItemPayload struct {
	ItemType  string `json:"item_type"`
	ItemID    int64  `json:"item_id"`
	LibraryID int64  `json:"library_id"`
}

// JobSearchMoviePayload is the search_movie job's arguments. The encoded form
// is also the queue's dedupe key (store.HasOpenJob matches on the payload
// string), so producers must marshal this type rather than hand-rolling the
// object: a differently-ordered or differently-spelled payload is a duplicate
// search, not a deduped one.
type JobSearchMoviePayload struct {
	MovieID int64 `json:"movie_id"`
}

// JobSearchEpisodePayload is the search_episode job's arguments.
type JobSearchEpisodePayload struct {
	EpisodeID int64 `json:"episode_id"`
}

// JobSyncSitePayload is the sync_site job's arguments: the library id of the
// adult series whose catalogue is to be walked.
//
// SearchNow rides along rather than being queued beside the sync because the
// searches can only be made once the scenes exist — the wanted list is computed
// from episode rows, and before the walk there are none. Queueing the search
// first would reliably queue nothing.
type JobSyncSitePayload struct {
	SeriesID  int64 `json:"series_id"`
	SearchNow bool  `json:"search_now"`
}

// Job states for the durable queue (SPEC §7).
const (
	JobStatePending   = "pending"
	JobStateRunning   = "running"
	JobStateDone      = "done"
	JobStateFailed    = "failed"
	JobStateCancelled = "cancelled"
)

// Job is one unit of durable, at-least-once background work (SPEC §7): a
// search, an import, a conversion, a scan. Every job kind must be idempotent,
// because a crashed worker's lease expires and the job is handed out again.
type Job struct {
	ID int64
	// Kind selects the handler ("search", "import", …).
	Kind string
	// Payload is the handler's JSON arguments.
	Payload string
	// State is one of the JobState* constants.
	State string
	// Attempts counts how many times the job has been handed out and failed.
	Attempts int
	// RunAfter delays the job; zero means "eligible now".
	RunAfter time.Time
	// LeaseExpiresAt is when a running job is considered abandoned and may be
	// reclaimed. Zero unless State is JobStateRunning.
	LeaseExpiresAt time.Time
	// LastError is the most recent failure message.
	LastError string
	CreatedAt time.Time
	UpdatedAt time.Time
}
