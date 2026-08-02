//go:build unix

package relocate

import (
	"fmt"
	"io/fs"
	"syscall"
)

// linkKey identifies the inode behind a file, and reports separately whether
// that inode still has more than one name.
//
// Caravan's imports hardlink incomplete/<x> into library/<y> (see
// internal/library.organize): one inode, two names, one copy of the bytes.
//
// The two answers are separate because the link count falls as the move
// proceeds. The first name of a pair is seen with two links and is worth
// remembering; by the time the second name is reached the first has been
// released, so it has one link and is worth nothing to remember — but it still
// has to be recognised as the same inode. Recording only the multi-link case
// keeps the mover's map to the size of the library's hardlinks rather than the
// size of the library.
func linkKey(info fs.FileInfo) (key string, hardlinked bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return "", false
	}
	return fmt.Sprintf("%v:%v", st.Dev, st.Ino), uint64(st.Nlink) >= 2
}
