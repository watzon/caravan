//go:build windows

package prepare

import "golang.org/x/sys/windows"

// filesystemName reports the filesystem type of the volume holding path,
// lowercased ("exfat", "ntfs", "fat32", ...).
//
// GetVolumeInformation wants the volume's own root — "D:\", not
// "D:\Media\Caravan" — so the mount point is resolved first.
func filesystemName(path string) (string, error) {
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	root := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumePathName(p, &root[0], uint32(len(root))); err != nil {
		return "", err
	}

	name := make([]uint16, windows.MAX_PATH+1)
	if err := windows.GetVolumeInformation(&root[0], nil, 0, nil, nil, nil, &name[0], uint32(len(name))); err != nil {
		return "", err
	}
	return normalizeFilesystem(windows.UTF16ToString(name)), nil
}
