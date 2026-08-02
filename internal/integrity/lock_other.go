//go:build !unix && !windows

package integrity

import "os"

// lockFile has no implementation on platforms with no advisory locking Caravan
// can reach from the standard library. It reports success: refusing to start on
// a platform that cannot be checked would be worse than the single-instance
// guard being absent there, and no supported deployment target lands here.
func lockFile(*os.File) (bool, error) { return true, nil }
