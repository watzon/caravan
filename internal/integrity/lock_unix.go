//go:build unix

package integrity

import (
	"os"
	"syscall"
)

// lockFile takes an exclusive advisory lock on f without blocking, reporting
// false when another process already holds it.
//
// flock is associated with the open file description, so the lock is released
// by closing the handle and, crucially for a drive that was pulled, by the
// kernel when the process dies. There is no stale lock to clean up, which is
// exactly what a PID file could not promise.
func lockFile(f *os.File) (bool, error) {
	err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	switch err {
	case nil:
		return true, nil
	case syscall.EWOULDBLOCK:
		return false, nil
	}
	return false, err
}
