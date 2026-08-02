package prepare

import "strings"

// normalizeFilesystem folds the spellings the different platforms report onto
// one lowercase name, so the exFAT comparison and the warning text do not have
// to know which kernel answered. Windows says "exFAT", macOS says "exfat",
// Linux answers with a magic number this package has already named.
func normalizeFilesystem(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}
