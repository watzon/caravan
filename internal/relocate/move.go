package relocate

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/library"
)

// The directories a migration moves, named by the packages that own them so
// this cannot drift from where the files actually go.
//
// Nothing else under the storage root moves. The database is a disposable cache
// that the new root rebuilds by rescanning (SPEC §7), and anything else a user
// keeps beside their library is theirs.
const (
	libraryTree    = library.LibraryDir
	incompleteTree = download.IncompleteDir
)

var trees = []string{libraryTree, incompleteTree}

// movePrefix marks a copy that has not been verified yet. The dot keeps it out
// of the library scanner's way, and the fixed prefix means a crash leaves
// something a later attempt recognises rather than something that looks like
// media.
const movePrefix = ".caravan-move-"

// entry is one file the migration is responsible for, identified by its
// storage-root-relative path in the slash form the database uses.
type entry struct {
	rel  string
	size int64
}

// plan is every file the migration owns: the union of what is still at `from`
// and what has already reached `to`.
//
// The union is what makes a resumed migration honest. After a crash the files
// are split across both roots, and a plan built from the source alone would
// report a total that shrinks every time the job restarts.
//
// A non-regular entry is a hard stop, and it is a hard stop *here*, before
// anything has moved: a socket or a device node under the library is not
// something Caravan can copy, and finding that out halfway through is a
// rollback the user did not need to sit through.
func plan(from, to string) ([]entry, error) {
	sizes := make(map[string]int64)
	// `to` first so a file present at both is measured at the source, which is
	// the copy that still has to move.
	for _, root := range []string{to, from} {
		for _, tree := range trees {
			if err := collect(root, tree, sizes); err != nil {
				return nil, err
			}
		}
	}

	out := make([]entry, 0, len(sizes))
	for rel, size := range sizes {
		out = append(out, entry{rel: rel, size: size})
	}
	// Sorted so a resumed or retried migration does the same work in the same
	// order, which is what makes a failure reproducible.
	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

func collect(root, tree string, into map[string]int64) error {
	base := filepath.Join(root, tree)
	if _, err := os.Stat(base); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if !d.Type().IsRegular() {
			return fmt.Errorf("%s is not a regular file; move it out of the storage root before migrating", p)
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		relOS, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		into[filepath.ToSlash(relOS)] = info.Size()
		return nil
	})
}

// mover moves the planned files between two roots. Rollback is the same type
// with the roots swapped, so the undo path is the do path and cannot rot
// separately from it.
type mover struct {
	from string
	to   string
	log  *slog.Logger
	// step, when set, runs after every entry has been resolved. It exists so
	// tests can assert the never-lost invariant — every file is readable at the
	// source or the target at every observable point — from inside the move.
	// Production never sets it.
	step func(rel string)
	// links maps a source inode to the path at the target that already holds
	// it. It is what keeps an import's hardlink between incomplete/ and
	// library/ from becoming two independent copies on the other side of a
	// cross-filesystem move — a seeding library would otherwise need twice the
	// space at the target and keep occupying twice the space afterwards.
	links map[string]string
}

// move relocates every entry. done is called once per file that has arrived at
// the target, including files a previous attempt already put there.
//
// bestEffort is the rollback's mode: a file that cannot go back is logged and
// the rest still go, because stopping at the first problem would strand more of
// the library at the target than continuing does.
func (m *mover) move(ctx context.Context, entries []entry, done func(entry), bestEffort bool) error {
	m.links = make(map[string]string, len(entries))
	m.renameTrees()
	for _, e := range entries {
		// A cancelled context stops a migration but never a rollback: the
		// process shutting down mid-move is precisely when the files most need
		// to end up back where the root setting still points.
		if !bestEffort {
			if err := ctx.Err(); err != nil {
				return err
			}
		}
		if err := m.one(e); err != nil {
			if !bestEffort {
				return err
			}
			m.log.Error("storage migration: a file could not be put back",
				"path", e.rel, "from", m.from, "to", m.to, "error", err)
			continue
		}
		if done != nil {
			done(e)
		}
		if m.step != nil {
			m.step(e.rel)
		}
	}
	return nil
}

// renameTrees is the same-filesystem fast path: a whole tree becomes one
// metadata operation instead of a byte-for-byte copy of every file in it.
//
// Failure is not reported. A rename across filesystems fails, which is the
// common case this is an optimisation for, and every file it did not move is
// moved by the per-file pass immediately after. Anything genuinely broken
// surfaces there with an error that says what it was.
func (m *mover) renameTrees() {
	for _, tree := range trees {
		src := filepath.Join(m.from, tree)
		if _, err := os.Stat(src); err != nil {
			continue
		}
		dst := filepath.Join(m.to, tree)
		switch _, err := os.Stat(dst); {
		case err == nil:
			// Only an empty directory may be renamed over. A populated one is a
			// resumed migration, which merges file by file.
			if os.Remove(dst) != nil {
				continue
			}
		case !errors.Is(err, fs.ErrNotExist):
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			continue
		}
		if err := os.Rename(src, dst); err != nil {
			m.log.Debug("storage migration: moving file by file",
				"tree", tree, "reason", err)
		}
	}
}

// one resolves a single file to "present at the target, absent at the source".
//
// Every branch keeps the invariant the whole feature rests on: at no observable
// moment does the file exist at neither root. The copy lands under a temporary
// name, is measured, and only then replaces the target and releases the source.
func (m *mover) one(e entry) (err error) {
	src := filepath.Join(m.from, filepath.FromSlash(e.rel))
	dst := filepath.Join(m.to, filepath.FromSlash(e.rel))

	srcInfo, srcErr := os.Lstat(src)
	if srcErr != nil && !errors.Is(srcErr, fs.ErrNotExist) {
		return srcErr
	}
	dstInfo, dstErr := os.Lstat(dst)
	if dstErr != nil && !errors.Is(dstErr, fs.ErrNotExist) {
		return dstErr
	}
	srcPresent, dstPresent := srcErr == nil, dstErr == nil

	// A file that shares its inode with one this migration has already placed
	// is a second name for bytes that are already at the target. Linking to
	// them costs nothing; copying them again costs the file's size twice, at
	// the target and for the rest of the library's life.
	if key, hardlinked := "", false; srcPresent {
		if key, hardlinked = linkKey(srcInfo); key != "" {
			if primary, seen := m.links[key]; seen && primary != dst {
				if relink(primary, dst) == nil {
					return os.Remove(src)
				}
				// A target filesystem with no hardlinks (exFAT) falls through
				// to the copy, which is exactly what happened before.
			}
			if hardlinked {
				defer func() {
					if err == nil {
						m.links[key] = dst
					}
				}()
			}
		}
	}

	switch {
	case !srcPresent && dstPresent:
		// Already arrived: a rename of the whole tree, or a previous attempt.
		return nil
	case !srcPresent && !dstPresent:
		// Deleted under us between the plan and now. Not an error — the user
		// removing a file mid-migration is allowed to win.
		m.log.Warn("storage migration: a planned file has gone", "path", e.rel)
		return nil
	}

	if dstPresent && dstInfo.Mode().IsRegular() && dstInfo.Size() == srcInfo.Size() {
		// A previous attempt copied it and died before releasing the source.
		return os.Remove(src)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if !dstPresent {
		if err := os.Rename(src, dst); err == nil {
			return nil
		}
	}
	return copyThenRemove(src, dst, srcInfo)
}

// relink gives dst the same inode primary already has at the target, which is
// how a hardlinked pair survives a move to another filesystem as a pair.
//
// The never-lost invariant holds throughout: the only thing removed is the
// target's own copy, and the source is still there until the link succeeds.
func relink(primary, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := os.Remove(dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.Link(primary, dst)
}

// copyThenRemove is the cross-filesystem move: copy, verify, replace, release.
func copyThenRemove(src, dst string, info fs.FileInfo) error {
	tmp := filepath.Join(filepath.Dir(dst), movePrefix+filepath.Base(dst))
	if err := os.Remove(tmp); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("clear a leftover partial copy of %s: %w", filepath.Base(dst), err)
	}
	if err := copyFile(src, tmp, info); err != nil {
		_ = os.Remove(tmp)
		return err
	}

	// Size is the verification. A short file is what a full disk, a pulled
	// cable and a killed process all produce, and it is the one failure a
	// rename over the top would turn into silent corruption.
	out, err := os.Stat(tmp)
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if out.Size() != info.Size() {
		_ = os.Remove(tmp)
		return fmt.Errorf("copy of %s is %d bytes, expected %d", filepath.Base(dst), out.Size(), info.Size())
	}

	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Best effort: the scanner compares modification times, so preserving them
	// keeps a migration from looking like every file changed. A filesystem that
	// refuses is not a reason to fail a verified move.
	_ = os.Chtimes(dst, time.Now(), info.ModTime())
	// Only now, with a verified copy at the target, is the original safe to drop.
	return os.Remove(src)
}

func copyFile(src, dst string, info fs.FileInfo) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	// Flushed before the rename that publishes it, so a power cut cannot leave
	// a full-length file whose contents never reached the platter.
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// verify is the last gate before the storage-root setting moves: every planned
// file is at the target at the size it was planned with, and none is still at
// the source.
func verify(from, to string, entries []entry) error {
	for _, e := range entries {
		dst := filepath.Join(to, filepath.FromSlash(e.rel))
		info, err := os.Stat(dst)
		if err != nil {
			return fmt.Errorf("%s did not arrive: %w", e.rel, err)
		}
		if info.Size() != e.size {
			return fmt.Errorf("%s arrived as %d bytes, expected %d", e.rel, info.Size(), e.size)
		}
		switch _, err := os.Lstat(filepath.Join(from, filepath.FromSlash(e.rel))); {
		case err == nil:
			return fmt.Errorf("%s is still at the old root", e.rel)
		case !errors.Is(err, fs.ErrNotExist):
			return fmt.Errorf("%s at the old root cannot be read: %w", e.rel, err)
		}
	}
	return nil
}

// leftAt reports how many files remain under root's trees. It is what tells a
// rollback that put everything back apart from a rollback that could not.
func leftAt(root string) int {
	sizes := make(map[string]int64)
	for _, tree := range trees {
		if err := collect(root, tree, sizes); err != nil {
			// Unreadable counts as occupied: the honest answer to "is anything
			// still there" that cannot be checked is "assume yes".
			return 1
		}
	}
	return len(sizes)
}

// pruneEmptyDirs removes the directory skeleton a completed move left at the
// old root, deepest first. It stops at anything that is not empty, so a file
// Caravan does not own keeps its folder.
func pruneEmptyDirs(root string) {
	for _, tree := range trees {
		base := filepath.Join(root, tree)
		var dirs []string
		err := filepath.WalkDir(base, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				dirs = append(dirs, p)
			}
			return nil
		})
		if err != nil {
			continue
		}
		for i := len(dirs) - 1; i >= 0; i-- {
			_ = os.Remove(dirs[i])
		}
	}
}
