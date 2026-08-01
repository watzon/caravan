//go:build !darwin && !linux && !freebsd && !netbsd && !openbsd && !windows

package api

import "errors"

// diskUsage has no implementation on this platform; the status endpoint
// reports unknown (zeros) rather than failing.
func diskUsage(string) (free, total int64, err error) {
	return 0, 0, errors.New("disk usage not supported on this platform")
}
