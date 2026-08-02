//go:build !unix

package relocate

import "io/fs"

// linkKey has no portable implementation off unix: os.FileInfo carries no inode
// identity there. Reporting "not hardlinked" costs nothing on the platforms
// this covers — Caravan's Windows deployments live on exFAT and NTFS drives
// where the import pipeline already falls back to copying — and a migration
// simply behaves as it did before, copying each name.
func linkKey(fs.FileInfo) (string, bool) { return "", false }
