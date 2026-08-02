//go:build !darwin && !linux && !freebsd && !netbsd && !openbsd && !windows

package pipeline

// freeSpace has no implementation on this platform, so the preflight is
// skipped rather than failed: a check that cannot run must not stop a download
// that would have worked.
func freeSpace(string) (int64, error) { return 0, errSpaceUnsupported }
