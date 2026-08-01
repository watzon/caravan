//go:build windows

package api

import "golang.org/x/sys/windows"

// diskUsage reports the free and total bytes of the volume holding path. Free
// is the space available to the calling user (quotas respected), because that
// is what a download can actually use.
func diskUsage(path string) (free, total int64, err error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, err
	}
	var availableToCaller, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &availableToCaller, &totalBytes, &totalFree); err != nil {
		return 0, 0, err
	}
	return int64(availableToCaller), int64(totalBytes), nil
}
