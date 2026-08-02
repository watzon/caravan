package core

import "time"

// The convert-for-TV queue (SPEC §8). A conversion is one file's trip through
// ffmpeg: a container swap when only the container is wrong, an explicit
// re-encode when a stream is.
//
// It is history rather than cache. A rescan can rebuild media_files from disk,
// but it cannot rebuild "this file was transcoded on Tuesday and here is what
// ffmpeg said when it failed", so the row survives independently of the file
// it describes (same reasoning as grabs and events).

// Conversion statuses. Queued and running are the queue's business; done,
// failed and cancelled are terminal as far as the queue is concerned, though a
// failed conversion can be retried into a fresh queued one.
const (
	ConversionQueued    = "queued"
	ConversionRunning   = "running"
	ConversionDone      = "done"
	ConversionFailed    = "failed"
	ConversionCancelled = "cancelled"
)

// Conversion strategies, decided per file by probing it.
const (
	// ConvertStrategyNone means the file already plays: nothing ran.
	ConvertStrategyNone = "none"
	// ConvertStrategyRemux is a stream copy into an accepted container. It
	// takes seconds and loses nothing.
	ConvertStrategyRemux = "remux"
	// ConvertStrategyTranscode re-encodes at least the video. It is slow and
	// lossy, which is why it is the explicit fallback and never the first try.
	ConvertStrategyTranscode = "transcode"
)

// Conversion is one queued or completed convert-for-TV job, as the UI sees it.
type Conversion struct {
	ID int64
	// MediaFileID is the library file being converted. It is a loose id, not a
	// foreign key: the record of a conversion outlives the row it converted.
	MediaFileID int64
	// SourcePath is the storage-root-relative path as it was when the
	// conversion was queued.
	SourcePath string
	// OutputPath is the storage-root-relative path the library now points at.
	// Empty until the conversion succeeds.
	OutputPath string
	// Strategy is one of the ConvertStrategy* constants, empty until the file
	// has been probed.
	Strategy string
	// ProfileID is the TVProfile the conversion targeted, recorded because the
	// active profile can change between queueing and running.
	ProfileID string
	// Status is one of the Conversion* status constants.
	Status string
	// Error is the last failure, empty otherwise.
	Error string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// ConversionOpen reports whether a status still belongs to the queue. Exactly
// one open conversion per file is allowed, which is what stops a double-click
// on Convert from starting two ffmpeg runs over the same output.
func ConversionOpen(status string) bool {
	return status == ConversionQueued || status == ConversionRunning
}
