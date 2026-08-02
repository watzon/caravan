//go:build darwin || linux || freebsd || netbsd || openbsd

package pipeline

import "golang.org/x/sys/unix"

// freeSpace reports the bytes available to an unprivileged caller (Bavail,
// not Bfree) on the filesystem holding path, because the blocks reserved for
// root are not blocks a download can use.
func freeSpace(path string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
