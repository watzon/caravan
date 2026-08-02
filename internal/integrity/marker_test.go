package integrity

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newMarker(t *testing.T) *Marker {
	t.Helper()
	return NewMarker(filepath.Join(t.TempDir(), "caravan.state"))
}

// crash releases the marker the way a killed process does: the advisory lock
// goes with the file handle the kernel closes, and nothing is written. It is
// how a test can be the *next* process without being a second live one.
func crash(t *testing.T, m *Marker) {
	t.Helper()
	if m.lock == nil {
		return
	}
	if err := m.lock.Close(); err != nil {
		t.Fatalf("release the marker lock: %v", err)
	}
	m.lock = nil
}

func wantState(t *testing.T, m *Marker, want string) {
	t.Helper()
	got, err := m.State()
	if err != nil {
		t.Fatalf("State: %v", err)
	}
	if got != want {
		t.Fatalf("State = %q, want %q", got, want)
	}
}

// A directory that has never held a marker is a first run, not a crash.
func TestFirstRunIsClean(t *testing.T) {
	m := newMarker(t)
	wantState(t, m, StateClean)

	dirty, err := m.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if dirty {
		t.Fatal("Begin reported a dirty start with no marker on disk; a first run is clean")
	}
}

// The marker says "running" for as long as the process owns the directory, so
// anything that reads it mid-session sees a dirty state.
func TestBeginMarksRunning(t *testing.T) {
	m := newMarker(t)
	if _, err := m.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	wantState(t, m, StateRunning)
}

// The full clean lifecycle: start, finish, start again.
func TestCleanShutdownIsNotDirtyOnTheNextStart(t *testing.T) {
	m := newMarker(t)
	if _, err := m.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if err := m.Finish(); err != nil {
		t.Fatalf("Finish: %v", err)
	}
	wantState(t, m, StateClean)

	dirty, err := m.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if dirty {
		t.Fatal("a session that called Finish was reported dirty on the next start")
	}
}

// The acceptance case: a process that never got to Finish (power cut, drive
// yanked, SIGKILL) is detected by the next one.
func TestSimulatedCrashIsDirtyOnTheNextStart(t *testing.T) {
	m := newMarker(t)
	if _, err := m.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	// No Finish: this is the crash.
	crash(t, m)

	next := NewMarker(m.Path())
	dirty, err := next.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if !dirty {
		t.Fatal("a session that never called Finish was not reported dirty")
	}
	// And claiming the directory again leaves it claimed, so a crash during
	// recovery is still detected by the start after that.
	wantState(t, next, StateRunning)
}

// A marker half-written by a drive that vanished mid-flush must not read as
// clean.
func TestTruncatedMarkerIsDirty(t *testing.T) {
	m := newMarker(t)
	for _, content := range []string{"", "cle", "\x00\x00\x00\x00", "clean\x00running"} {
		if err := os.WriteFile(m.Path(), []byte(content), 0o644); err != nil {
			t.Fatalf("write marker: %v", err)
		}
		dirty, err := m.Begin()
		if err != nil {
			t.Fatalf("Begin: %v", err)
		}
		if !dirty {
			t.Fatalf("marker content %q read as a clean shutdown", content)
		}
		crash(t, m)
	}
}

// The regression this exists for: a second `caravan serve` on the same drive
// used to claim the marker, and its own failed start then wrote "clean" over
// the marker the still-running first instance was relying on — so a drive
// yanked afterwards reported a clean shutdown, showed no recovery banner, and
// let downloads resume onto an unchecked filesystem.
//
// The realistic trigger is a launcher double-click: the first terminal window
// went unnoticed, so the user starts Caravan twice.
func TestSecondInstanceCannotClaimOrCleanTheMarker(t *testing.T) {
	first := newMarker(t)
	if _, err := first.Begin(); err != nil {
		t.Fatalf("Begin: %v", err)
	}

	second := NewMarker(first.Path())
	dirty, err := second.Begin()
	if !errors.Is(err, ErrLocked) {
		t.Fatalf("second Begin error = %v, want ErrLocked", err)
	}
	if !dirty {
		t.Fatal("a start that could not claim the marker reported a clean session")
	}

	// The second instance now fails — "address already in use" — and runs its
	// shutdown path. It must not be able to vouch for the first one's drive.
	if err := second.Finish(); err == nil {
		t.Fatal("a process that never owned the marker was allowed to mark it clean")
	}
	wantState(t, first, StateRunning)

	// The drive is pulled while the first instance is still serving.
	crash(t, first)
	next := NewMarker(first.Path())
	dirty, err = next.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	if !dirty {
		t.Fatal("the dirty eject went undetected: the second instance disarmed the marker")
	}
}

// Trailing whitespace is how the file is actually written, so it has to parse.
func TestCleanStateToleratesTrailingNewline(t *testing.T) {
	m := newMarker(t)
	if err := os.WriteFile(m.Path(), []byte(StateClean+"\n"), 0o644); err != nil {
		t.Fatalf("write marker: %v", err)
	}
	wantState(t, m, StateClean)
}

// An unreadable marker is reported as dirty *and* as an error: the caller gets
// to log the problem without being talked into trusting the drive.
func TestUnreadableMarkerIsDirty(t *testing.T) {
	dir := t.TempDir()
	// A directory where a file is expected: ReadFile fails with something that
	// is neither nil nor ErrNotExist, on every platform.
	path := filepath.Join(dir, "caravan.state")
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	m := NewMarker(path)
	state, err := m.State()
	if err == nil {
		t.Fatal("State on an unreadable marker returned no error")
	}
	if state != StateRunning {
		t.Fatalf("State = %q, want %q so the caller assumes the worst", state, StateRunning)
	}

	dirty, err := m.Begin()
	if err == nil {
		t.Fatal("Begin on an unwritable marker returned no error")
	}
	if !dirty {
		t.Fatal("Begin on an unreadable marker reported a clean start")
	}
}
