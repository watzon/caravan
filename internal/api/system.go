package api

import (
	"net/http"

	"github.com/watzon/caravan/internal/core"
)

// The portable-drive integrity flow (SPEC §2.3, §13, PLAN phase 5 task 3).
//
// A portable install lives on a drive that can be pulled at any moment, so it
// gets two things a server install does not need: a way to be shut down from
// the UI that ends with the drive safe to eject, and a way to be told on the
// next start that the last one did not get that far.
//
// Neither belongs to this package alone. The marker that survives the process
// is written by the serving process (internal/integrity); this package is
// handed the verdict with WithDirtyStart and the stop trigger with
// WithShutdown, so a server built without either behaves exactly as it did
// before phase 5.

// EventCategorySystem groups process-lifecycle events in the activity feed:
// the dirty start itself, and the verification that cleared it.
const EventCategorySystem = "system"

// WithDirtyStart tells the API that this session followed an unclean shutdown.
//
// It drives the dirty flag on GET /system/status, which is what puts the
// recovery banner in front of the user, and it blocks download resumes until
// POST /system/verify has cleared it. The serving process only sets it in
// portable mode: a server install's downloads are not on a drive somebody
// unplugged, so nagging there would train the user to dismiss the nag.
func WithDirtyStart(dirty bool) Option {
	return func(s *server) { s.dirty.Store(dirty) }
}

// WithShutdown supplies the orderly-stop trigger POST /system/shutdown pulls.
//
// The function must be the same one the signal handler uses, so that a shutdown
// from the UI and a Ctrl-C run the identical teardown — flush the engines,
// checkpoint the WAL, close the database, write the clean marker. A server
// built without it answers 503: a process that cannot stop itself must say so
// rather than pretend the drive is safe.
func WithShutdown(stop func()) Option {
	return func(s *server) { s.shutdown = stop }
}

// handleShutdown stops the process (SPEC §11).
//
// The reply goes out first and the teardown starts after, because the caller is
// a browser that needs to be told "you may eject now" and the connection it
// asked over is one of the things being torn down. 202 rather than 200: the
// work this describes has been accepted, not finished.
func (s *server) handleShutdown(w http.ResponseWriter, r *http.Request) {
	if s.shutdown == nil {
		writeError(w, http.StatusServiceUnavailable, "this process cannot shut itself down")
		return
	}

	s.logEvent(r.Context(), &core.Event{
		Level:    core.EventLevelInfo,
		Category: EventCategorySystem,
		Message:  "Shutting down at the user's request",
		Detail:   "Flushing downloads, checkpointing the database and releasing the storage root.",
	})

	writeJSON(w, http.StatusAccepted, map[string]string{"status": "shutting down"})
	// Best-effort push before the teardown starts. What actually guarantees the
	// reply is the graceful drain: http.Server.Shutdown waits for in-flight
	// requests, and this is one — which is also why the trigger has to be
	// pulled from a goroutine rather than inline.
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
	go s.shutdown()
}

// verifyResponse is the payload of POST /system/verify.
type verifyResponse struct {
	// Integrity is sqlite's verdict on its own file: "ok", or the endpoint
	// failed and this never reaches the client.
	Integrity string `json:"integrity"`
	// Dirty is what GET /system/status reports from here on.
	Dirty bool `json:"dirty"`
	// Scanning reports whether a library scan is now running. False means one
	// was already in flight, which serves the same purpose.
	Scanning bool `json:"scanning"`
}

// handleVerify is the recovery action offered after a dirty start (SPEC §13):
// check the database, rescan the library, and only then let downloads resume.
//
// Caravan never runs fsck itself — that is the operating system's job on an
// unmounted filesystem, and a tool that repaired the drive it was running from
// would be its own worst failure mode. The UI prints the per-OS command; this
// endpoint verifies what Caravan owns.
func (s *server) handleVerify(w http.ResponseWriter, r *http.Request) {
	// A crash mid-migration is a dirty start whose files are half-moved, so
	// this banner and a running migration turn up together. Rescanning then
	// would reconcile the library against a root the mover is emptying and
	// delete the rows for every file it has already taken (see
	// scanBlockedByMigration). The migration resumes on its own; verification
	// waits for it.
	if !s.noOpenMigration(w, r, scanBlockedByMigration) {
		return
	}
	if err := s.st.IntegrityCheck(r.Context()); err != nil {
		// The dirty flag deliberately stays set. A database that failed its own
		// consistency check is precisely the case downloads must not resume on,
		// and the honest recovery from here is "delete caravan.db and rescan"
		// (SPEC §7), not "carry on".
		s.log.Error("database integrity check", "error", err)
		s.logEvent(r.Context(), &core.Event{
			Level:    core.EventLevelError,
			Category: EventCategorySystem,
			Message:  "Database integrity check failed",
			Detail:   err.Error(),
		})
		writeError(w, http.StatusInternalServerError, "database integrity check failed")
		return
	}

	// The rescan is the other half of the verification: sqlite can only vouch
	// for its own pages, and the files those pages describe live on the drive
	// that was pulled.
	scanning := s.startScan()

	s.dirty.Store(false)
	s.logEvent(r.Context(), &core.Event{
		Level:    core.EventLevelInfo,
		Category: EventCategorySystem,
		Message:  "Database verified after an unclean shutdown",
		Detail:   "Downloads can be resumed.",
	})

	writeJSON(w, http.StatusOK, verifyResponse{Integrity: "ok", Dirty: false, Scanning: scanning})
}
