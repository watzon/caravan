package pipeline

import (
	"errors"
	"fmt"
)

// ErrInsufficientSpace is the sentinel a disk-space preflight failure unwraps
// to. It is a distinct error because it is the one download failure with an
// obvious user action attached, free some space, and the queue should say so
// rather than reporting a generic write error thirty gigabytes in.
var ErrInsufficientSpace = errors.New("pipeline: not enough free disk space")

// errSpaceUnsupported means this platform has no way to ask how much room is
// left. The preflight then does not run: a check that cannot be performed must
// not block a download that would have worked.
var errSpaceUnsupported = errors.New("pipeline: free space is not measurable on this platform")

// SpaceError is a download refused before it started because the filesystem
// holding its directory does not have room for it.
type SpaceError struct {
	// Path is the directory that was measured.
	Path string
	// Need is the number of bytes the download wants, headroom included.
	Need int64
	// Free is what the filesystem had.
	Free int64
}

func (e *SpaceError) Error() string {
	return fmt.Sprintf("pipeline: %s has %d bytes free, the download needs %d", e.Path, e.Free, e.Need)
}

// Unwrap is ErrInsufficientSpace.
func (e *SpaceError) Unwrap() error { return ErrInsufficientSpace }

// FreeSpace reports the bytes available to an unprivileged caller on the
// filesystem holding path: space the process can actually use, quotas and root
// reservations already subtracted.
//
// It is exported so that the engine can reuse the same measurement the
// preflight uses, and so Options.FreeSpace has an obvious default to name.
// Platforms with no implementation return an error wrapping
// errSpaceUnsupported, which the preflight treats as "do not check".
func FreeSpace(path string) (int64, error) { return freeSpace(path) }

// checkSpace is the preflight. It returns nil when there is room, when the
// platform cannot say, or when the caller turned the check off.
func checkSpace(dir string, need int64, free func(string) (int64, error)) error {
	if need <= 0 {
		return nil
	}
	avail, err := free(dir)
	if err != nil {
		// A filesystem that will not answer statfs is not a filesystem that
		// cannot hold the download. Proceed and let a real write fail.
		return nil
	}
	if avail >= need {
		return nil
	}
	return &SpaceError{Path: dir, Need: need, Free: avail}
}
