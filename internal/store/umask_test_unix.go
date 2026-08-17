//go:build !windows

package store

import "syscall"

func setTestUmaskZero() func() {
	old := syscall.Umask(0)
	return func() { syscall.Umask(old) }
}
