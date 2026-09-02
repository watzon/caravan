// Package integrity owns Caravan's clean-shutdown marker: the one fact about a
// session that has to outlive both the process and the database (SPEC §2.3,
// §13).
//
// A portable install lives on a drive somebody can pull mid-write. Noticing
// that on the next start needs a record kept outside sqlite, for two reasons.
// The database is a disposable cache (SPEC §7): "delete caravan.db, rescan, the
// library comes back" has to keep working, and a flag inside the file that was
// just deleted would go with it. And the check happens before the database is
// opened at all, which is the moment the answer matters.
//
// So the marker is a sidecar file next to the database holding one word: it
// says "running" for as long as a serving process owns that directory, and
// "clean" once one has shut down in an orderly way. A process that dies without
// running its shutdown path leaves "running" behind, and that is exactly what
// the next start reads as a dirty eject.
package integrity

import (
	"errors"
	"fmt"
	"os"
	"strings"
)

// The two states a marker file can hold.
const (
	// StateRunning means a serving process claimed the directory and has not
	// given it back. Reading it at startup means the previous session died.
	StateRunning = "running"
	// StateClean means the last process to own the directory shut down in an
	// orderly way: engines flushed, WAL checkpointed, database closed.
	StateClean = "clean"
)

// ErrLocked says another live process already owns this storage root. It is a
// reason to refuse to start: a second server would open the same database, and
// its own shutdown, including the one that follows immediately when it cannot
// bind the listener, would write "clean" over a marker the first process is
// still relying on, disarming dirty-eject detection for the session that is
// actually running.
var ErrLocked = errors.New("integrity: another Caravan process is already using this storage root")

// Marker is the clean-shutdown marker file at a fixed path.
//
// It is not safe for concurrent use, and does not need to be: exactly one
// goroutine per process touches it, once on the way in and once on the way out.
type Marker struct {
	path string
	// lock is the open handle holding the exclusive advisory lock on the
	// marker, kept for the life of the process. Nil means this process never
	// took ownership, and must not write "clean".
	lock *os.File
}

// NewMarker returns the marker stored at path. Nothing is read or written
// until Begin.
func NewMarker(path string) *Marker { return &Marker{path: path} }

// Path is where the marker lives, for logs and error messages.
func (m *Marker) Path() string { return m.path }

// State reports what the marker currently says.
//
// A missing file is StateClean: a first run, or a directory whose marker was
// deleted along with the database, is not evidence of a crash and must not send
// the user into a recovery flow they do not need.
//
// Anything that is not exactly "clean", a truncated write, a half-flushed page
// of zeros, the word "running", reads as StateRunning. That is the conservative
// direction: the cost of a false dirty is one verify-and-rescan, the cost of a
// false clean is resuming writes onto a filesystem nobody checked.
func (m *Marker) State() (string, error) {
	data, err := os.ReadFile(m.path)
	switch {
	case err == nil:
	case errors.Is(err, os.ErrNotExist):
		return StateClean, nil
	default:
		return StateRunning, fmt.Errorf("integrity: read marker %s: %w", m.path, err)
	}
	if strings.TrimSpace(string(data)) == StateClean {
		return StateClean, nil
	}
	return StateRunning, nil
}

// Begin claims the directory for this process and reports whether the previous
// session left without releasing it.
//
// The read happens before the lock file is opened, so the answer describes the
// session that came before this one and a first run, where opening the file is
// what creates it, is not mistaken for a crash. On a read failure it reports
// dirty alongside the error: a marker that cannot be read cannot vouch for
// anything.
//
// Claiming is an exclusive advisory lock held for the life of the process. It
// is what makes ownership decidable: a marker saying "running" belongs to a
// live process if and only if that lock is held, which is the one thing a PID
// or a token in the file cannot tell a second instance from a crashed one. A
// caller that gets ErrLocked must not serve.
func (m *Marker) Begin() (bool, error) {
	state, readErr := m.State()
	dirty := state != StateClean

	f, err := os.OpenFile(m.path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return true, errors.Join(readErr, fmt.Errorf("integrity: open marker %s: %w", m.path, err))
	}
	held, err := lockFile(f)
	if err != nil {
		f.Close()
		return true, errors.Join(readErr, fmt.Errorf("integrity: lock marker %s: %w", m.path, err))
	}
	if !held {
		f.Close()
		// Deliberately leaves the marker exactly as the owning process left
		// it: this process is not going to serve, so it has nothing to record.
		return true, errors.Join(readErr, ErrLocked)
	}
	m.lock = f

	if err := m.write(StateRunning); err != nil {
		return true, errors.Join(readErr, err)
	}
	return dirty, readErr
}

// Finish records that this process shut down cleanly. It is the last thing a
// serving process does: everything it vouches for, flushed engines, a
// checkpointed WAL, a closed database, has to have already happened.
//
// A process that never took the lock refuses rather than writing: the marker it
// would overwrite belongs to whoever is still running.
func (m *Marker) Finish() error {
	if m.lock == nil {
		return fmt.Errorf("integrity: %s is not owned by this process", m.path)
	}
	err := m.write(StateClean)
	if closeErr := m.lock.Close(); err == nil {
		err = closeErr
	}
	m.lock = nil
	return err
}

// write replaces the marker's contents and fsyncs them.
//
// The sync is the point of the whole mechanism: on a removable drive an
// unsynced "clean" sitting in the page cache is indistinguishable from no
// write at all, which is precisely the case being detected.
func (m *Marker) write(state string) error {
	f, err := os.OpenFile(m.path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("integrity: write marker %s: %w", m.path, err)
	}
	if _, err := f.WriteString(state + "\n"); err != nil {
		f.Close()
		return fmt.Errorf("integrity: write marker %s: %w", m.path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("integrity: sync marker %s: %w", m.path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("integrity: close marker %s: %w", m.path, err)
	}
	return nil
}
