//go:build darwin || linux || freebsd || netbsd || openbsd

package api

import "golang.org/x/sys/unix"

// diskUsage reports the free and total bytes of the filesystem holding path.
// Free is the space available to an unprivileged caller (Bavail), because
// that is what a download can actually use.
func diskUsage(path string) (free, total int64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := int64(st.Bsize)
	return int64(st.Bavail) * bsize, int64(st.Blocks) * bsize, nil
}
