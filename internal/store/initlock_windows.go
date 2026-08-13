//go:build windows

package store

import (
	"os"

	"golang.org/x/sys/windows"
)

func lockDatabaseInitFile(file *os.File) error {
	var overlapped windows.Overlapped
	return windows.LockFileEx(windows.Handle(file.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK, 0, 1, 0, &overlapped)
}
