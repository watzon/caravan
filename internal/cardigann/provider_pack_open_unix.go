//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package cardigann

import (
	"os"

	"golang.org/x/sys/unix"
)

func openPackArchiveNoFollow(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
