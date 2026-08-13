package store

import (
	"fmt"
	"os"
)

// acquireDatabaseInitLock serializes schema classification and migration across
// independently opened Store handles and processes. The persistent lock
// sidecar avoids inode replacement races when a staged restore swaps the main
// database file. Closing the returned file releases the advisory lock.
func acquireDatabaseInitLock(path string) (*os.File, error) {
	lockPath := path + ".init.lock"
	file, err := os.OpenFile(lockPath, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("store: open database initialization lock %s: %w", lockPath, err)
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return nil, fmt.Errorf("store: chmod database initialization lock %s: %w", lockPath, err)
	}
	if err := lockDatabaseInitFile(file); err != nil {
		file.Close()
		return nil, fmt.Errorf("store: lock database initialization %s: %w", lockPath, err)
	}
	return file, nil
}
