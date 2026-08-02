//go:build windows

package integrity

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// lockFile is the Windows half of the single-instance guard: an exclusive byte
// range lock on the marker, released when the handle closes or the process
// dies. See the unix build for why that lifetime is the point.
//
// LockFileEx lives in x/sys/windows rather than the standard syscall package,
// which is why this file imports it; x/sys is already a direct dependency.
func lockFile(f *os.File) (bool, error) {
	var overlapped windows.Overlapped
	err := windows.LockFileEx(windows.Handle(f.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0, 1, 0, &overlapped)
	if err == nil {
		return true, nil
	}
	// What a lock already held by another process looks like when
	// LOCKFILE_FAIL_IMMEDIATELY is set.
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) || errors.Is(err, windows.ERROR_IO_PENDING) {
		return false, nil
	}
	return false, err
}
