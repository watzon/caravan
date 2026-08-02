//go:build linux

package prepare

import "golang.org/x/sys/unix"

// linuxFilesystems maps the statfs magic numbers worth naming to their
// filesystem names. It is deliberately short: the only question prepare asks is
// "is this exFAT?", and every other entry exists so the warning can say what
// the drive *is* instead of printing a hex number at the user.
var linuxFilesystems = map[int64]string{
	unix.EXFAT_SUPER_MAGIC: "exfat",
	unix.MSDOS_SUPER_MAGIC: "vfat",
	// NTFS_SB_MAGIC, spelled out because x/sys/unix does not export it.
	0x5346544e:                 "ntfs",
	unix.EXT4_SUPER_MAGIC:      "ext",
	unix.BTRFS_SUPER_MAGIC:     "btrfs",
	unix.XFS_SUPER_MAGIC:       "xfs",
	unix.F2FS_SUPER_MAGIC:      "f2fs",
	unix.TMPFS_MAGIC:           "tmpfs",
	unix.FUSE_SUPER_MAGIC:      "fuse",
	unix.OVERLAYFS_SUPER_MAGIC: "overlay",
}

// filesystemName reports the filesystem type mounted at path, lowercased. An
// unrecognized magic number returns "" with no error: prepare then says nothing
// rather than guessing, which is the correct behaviour for a warning-only check.
func filesystemName(path string) (string, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return "", err
	}
	return linuxFilesystems[int64(st.Type)], nil
}
