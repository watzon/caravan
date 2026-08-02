//go:build !darwin && !freebsd && !linux && !windows

package prepare

// filesystemName has no implementation on this platform. prepare then skips the
// exFAT check silently: the check only ever adds a warning, so a platform that
// cannot answer it is not a platform that cannot prepare a drive.
func filesystemName(string) (string, error) { return "", nil }
