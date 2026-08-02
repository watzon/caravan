package core

import "time"

// Moving the storage root (SPEC §10). Two operations share one concept and
// almost nothing else.
//
// Re-pointing changes where Caravan looks. Every stored path is relative to the
// root, so it is a single settings update and it is instant; it has no row here
// because there is no progress to report and nothing to roll back.
//
// Migrating moves the bytes. That is hours of copying, it can fail halfway, and
// it is the only thing in Caravan that can lose media if it gets it wrong — so
// it is a durable job with a durable row, and the root setting is the last
// thing it touches rather than the first.

// Storage-migration statuses. Queued and running belong to the queue; the rest
// are terminal.
const (
	StorageMigrationQueued  = "queued"
	StorageMigrationRunning = "running"
	StorageMigrationDone    = "done"
	// StorageMigrationFailed is a migration that broke *and could not put the
	// files back*. It is the one status that needs a human: the roots named on
	// the row both hold part of the library.
	StorageMigrationFailed = "failed"
	// StorageMigrationRolledBack is a migration that broke and undid itself.
	// The old root holds everything and the root setting never moved, so the
	// only cost was time.
	StorageMigrationRolledBack = "rolled_back"
)

// StorageMigration is one move of the storage root's trees from one absolute
// path to another.
type StorageMigration struct {
	ID int64
	// SourceRoot is the root the files are moving from: the storage root in
	// force when the migration was queued.
	SourceRoot string
	// TargetRoot is the root they are moving to, and the value the storage_root
	// setting takes once — and only once — every file has arrived and verified.
	TargetRoot string
	// Status is one of the StorageMigration* constants.
	Status string
	// FilesTotal and BytesTotal describe the whole move, counted once at the
	// start from the union of both roots (a resumed migration has files at each).
	FilesTotal int64
	BytesTotal int64
	// FilesDone and BytesDone are how much of that has arrived. They only ever
	// grow, including across a resume, because "arrived" is a fact about the
	// target rather than about this attempt.
	FilesDone int64
	BytesDone int64
	// Error is why it stopped, empty otherwise.
	Error string

	CreatedAt time.Time
	UpdatedAt time.Time
}

// StorageMigrationOpen reports whether a status still belongs to the queue.
func StorageMigrationOpen(status string) bool {
	return status == StorageMigrationQueued || status == StorageMigrationRunning
}
