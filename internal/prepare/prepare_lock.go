package prepare

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// PrepareLockFile is a persistent advisory-lock sidecar at the drive root.
// Keeping one inode across runs avoids stale-lock and inode-replacement races;
// closing the handle releases ownership even after a crash.
const PrepareLockFile = ".caravan.prepare.lock"

// ErrPrepareLocked means another prepare process is currently mutating the
// selected drive.
var ErrPrepareLocked = errors.New("prepare: another preparation is already running on this drive")

func acquirePrepareLock(root *os.Root) (*os.File, error) {
	file, err := root.OpenFile(filepath.FromSlash(PrepareLockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("prepare: open preparation lock: %w", err)
	}
	locked, err := lockPrepareFile(file)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("prepare: lock drive: %w", err)
	}
	if !locked {
		file.Close()
		return nil, ErrPrepareLocked
	}
	return file, nil
}
