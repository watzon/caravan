//go:build unix

package store

import (
	"os"
	"syscall"
)

func lockDatabaseInitFile(file *os.File) error {
	return syscall.Flock(int(file.Fd()), syscall.LOCK_EX)
}
