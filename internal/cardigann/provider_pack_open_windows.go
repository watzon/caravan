//go:build windows

package cardigann

import "os"

// Windows named pipes are not regular filesystem entries. The post-open fstat,
// lstat, and SameFile checks in OpenSignedPackArchive retain the same identity
// invariant where O_NOFOLLOW is unavailable.
func openPackArchiveNoFollow(path string) (*os.File, error) {
	return os.Open(path)
}
