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

// DownloadStatus is a live snapshot of one download, as the engine sees it
// right now. It is not persisted as-is: rates and ETA are meaningless a second
// later. The durable half lives in Download.
type DownloadStatus struct {
	ID    DownloadID
	State DownloadState
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
	// SavePath is where the engine is writing, relative to the storage root
	// (SPEC §1.2 pillar 3).
	SavePath string
	// Error is the engine's failure message; empty unless State is
	// DownloadFailed.
	Error string
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
}

// Grab statuses.
const (
	GrabStatusGrabbed  = "grabbed"
	GrabStatusImported = "imported"
	GrabStatusFailed   = "failed"
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

// Job states for the durable queue (SPEC §7).
const (
	JobStatePending = "pending"
	JobStateRunning = "running"
	JobStateDone    = "done"
	JobStateFailed  = "failed"
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
