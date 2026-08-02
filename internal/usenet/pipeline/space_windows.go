//go:build windows

package pipeline

import "golang.org/x/sys/windows"

// freeSpace reports the bytes available to the calling user on the volume
// holding path. GetDiskFreeSpaceEx's first output honours per-user quotas,
// which is the number a download is actually allowed to consume.
func freeSpace(path string) (int64, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	var availableToCaller, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(p, &availableToCaller, &totalBytes, &totalFree); err != nil {
		return 0, err
	}
	return int64(availableToCaller), nil
}
