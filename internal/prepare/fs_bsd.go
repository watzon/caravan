//go:build darwin || freebsd

package prepare

import (
	"bytes"

	"golang.org/x/sys/unix"
)

// filesystemName reports the filesystem type mounted at path, lowercased
// ("exfat", "apfs", "hfs", "msdos", ...). The BSD statfs carries the name
// directly, so no magic-number table is needed here.
func filesystemName(path string) (string, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return "", err
	}
	name := st.Fstypename[:]
	if i := bytes.IndexByte(name, 0); i >= 0 {
		name = name[:i]
	}
	return normalizeFilesystem(string(name)), nil
}
