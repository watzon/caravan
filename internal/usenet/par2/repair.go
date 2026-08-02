package par2

import (
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Repair reconstructs everything rep says is damaged or missing.
//
// rep must be the result of [Set.Verify] against the same directory and an
// unchanged set of files; passing a stale report repairs against stale
// information. Callers that do not want to hold a report should use
// [Set.VerifyAndRepair].
//
// Repair is all-or-nothing. Reconstructed data is written to temporary files,
// every one of them is checked against the MD5 the set declared before
// anything is renamed, and the whole directory is verified again afterwards.
// A failure at any point leaves the directory exactly as it was found, and
// returns an error rather than a partially repaired release. Damage beyond the
// set's recovery budget returns an [*InsufficientError] carrying the deficit.
//
// Repairing a directory that is already complete is a no-op.
func (s *Set) Repair(ctx context.Context, dir string, rep *Report) error {
	if err := s.checkReport(dir, rep); err != nil {
		return err
	}
	if rep.Complete() {
		return nil
	}

	missing := missingSliceIndices(s, rep)
	if len(missing) > len(s.recovery) {
		return &InsufficientError{Needed: len(missing), Available: len(s.recovery)}
	}
	if err := checkRepairMemory(len(missing), s.SliceSize); err != nil {
		return err
	}

	rebuilt, err := s.solve(ctx, dir, rep, missing)
	if err != nil {
		return err
	}
	if err := s.writeRepaired(ctx, dir, rep, rebuilt); err != nil {
		return err
	}

	// Read everything back from disk. The MD5 check during writing already
	// proves the arithmetic; this proves the bytes actually landed.
	after, err := s.Verify(ctx, dir)
	if err != nil {
		return err
	}
	if !after.Complete() {
		return fmt.Errorf("%w: %d of %d slices still bad after repair",
			ErrRepairFailed, after.MissingSlices, after.TotalSlices)
	}
	return nil
}

// VerifyAndRepair verifies dir and repairs it when repair is both needed and
// possible.
//
// The returned report always describes the directory *before* any repair, so
// a caller can report what was wrong ("rebuilt 5 of 25 blocks") whether or not
// the repair succeeded. A nil error means the directory now matches the set.
func (s *Set) VerifyAndRepair(ctx context.Context, dir string) (*Report, error) {
	rep, err := s.Verify(ctx, dir)
	if err != nil {
		return nil, err
	}
	if rep.Complete() {
		return rep, nil
	}
	return rep, s.Repair(ctx, dir, rep)
}

// checkReport rejects a report that did not come from verifying this set
// against this directory. Repairing against someone else's report would
// reconstruct the wrong slices and overwrite files with them, so the mismatch
// has to be caught before any arithmetic happens rather than trusted to the
// final MD5 check.
func (s *Set) checkReport(dir string, rep *Report) error {
	switch {
	case rep == nil:
		return fmt.Errorf("par2: Repair called without a report")
	case rep.Dir != dir:
		return fmt.Errorf("par2: report was produced for %q, not %q", rep.Dir, dir)
	case rep.SliceSize != s.SliceSize:
		return fmt.Errorf("par2: report has slice size %d, set has %d", rep.SliceSize, s.SliceSize)
	case rep.TotalSlices != s.TotalSlices:
		return fmt.Errorf("par2: report describes %d slices, set describes %d", rep.TotalSlices, s.TotalSlices)
	case len(rep.Files) != len(s.Files):
		return fmt.Errorf("par2: report describes %d files, set describes %d", len(rep.Files), len(s.Files))
	}
	for i := range s.Files {
		if rep.Files[i].Name != s.Files[i].Name {
			return fmt.Errorf("par2: report file %d is %q, set file %d is %q",
				i, rep.Files[i].Name, i, s.Files[i].Name)
		}
		if n := s.Files[i].Slices(); len(rep.Files[i].valid) != n {
			return fmt.Errorf("par2: report for %s covers %d slices, set says %d",
				s.Files[i].Name, len(rep.Files[i].valid), n)
		}
	}
	return nil
}

// checkRepairMemory refuses a repair whose linear system would not fit in
// MaxRepairMemory, before a byte of it is allocated.
//
// solve holds two full sets of m slice buffers at the same time — the
// right-hand side and the solved slices — and nothing chunks the slice
// dimension, so the product is unbounded in the set's own terms. Saying so is
// the honest answer: the alternative is an allocation the kernel kills in the
// middle of a repair, which leaves the user with a failed download and no
// explanation.
func checkRepairMemory(missing int, sliceSize uint64) error {
	const buffers = 2
	need := uint64(missing) * sliceSize * buffers
	// uint64 overflow needs a slice size no real set carries, but a par2 file
	// is attacker-controlled data and the check is one comparison.
	if sliceSize != 0 && need/sliceSize/buffers != uint64(missing) {
		need = ^uint64(0)
	}
	if need <= maxRepairMemory {
		return nil
	}
	return fmt.Errorf("%w: rebuilding %d slices of %d bytes needs about %d bytes, the limit is %d",
		ErrRepairTooLarge, missing, sliceSize, need, maxRepairMemory)
}

// missingSliceIndices lists, in ascending order, the global index of every
// slice that has to be reconstructed.
func missingSliceIndices(s *Set, rep *Report) []int {
	var out []int
	for i := range s.Files {
		f := &s.Files[i]
		st := &rep.Files[i]
		for j := 0; j < f.Slices(); j++ {
			if j >= len(st.valid) || !st.valid[j] {
				out = append(out, f.FirstSlice+j)
			}
		}
	}
	return out
}

// solve reconstructs the missing slices, keyed by global slice index.
//
// The identity is: every recovery slice with exponent e is the field sum over
// all input slices of constant_i^e times slice_i. Moving the surviving slices
// to the other side turns the recovery slices we picked into the right-hand
// side of a square linear system whose unknowns are the missing slices, and
// whose matrix is constant_missing_j ^ e_r.
func (s *Set) solve(ctx context.Context, dir string, rep *Report, missing []int) (map[int][]byte, error) {
	m := len(missing)
	if m == 0 {
		return nil, nil
	}

	// Lowest exponents first, which is the order par2cmdline consumes recovery
	// blocks in; s.recovery is sorted at parse time.
	chosen := s.recovery[:m]

	constants := make([]uint16, m)
	for j, idx := range missing {
		c, ok := sliceConstant(idx)
		if !ok {
			return nil, fmt.Errorf("%w: slice index %d has no field constant", ErrMalformed, idx)
		}
		constants[j] = c
	}

	matrix := make([]uint16, m*m)
	for r := 0; r < m; r++ {
		for j := 0; j < m; j++ {
			matrix[r*m+j] = gfPow(constants[j], chosen[r].Exponent)
		}
	}
	inv, err := invertMatrix(matrix, m)
	if err != nil {
		return nil, err
	}

	// Right-hand side: start from the recovery slices themselves.
	rhs := make([][]byte, m)
	for r := range chosen {
		buf := make([]byte, s.SliceSize)
		if err := readAt(chosen[r].Path, chosen[r].Offset, buf); err != nil {
			return nil, err
		}
		rhs[r] = buf
	}

	// Subtract (which in this field is add, which is XOR) the contribution of
	// every slice that survived.
	if err := s.foldPresentSlices(ctx, dir, rep, chosen, rhs); err != nil {
		return nil, err
	}

	out := make(map[int][]byte, m)
	for j := 0; j < m; j++ {
		acc := make([]byte, s.SliceSize)
		for r := 0; r < m; r++ {
			mulAddSlice(acc, rhs[r], inv[j*m+r])
		}
		out[missing[j]] = acc
	}
	return out, nil
}

// foldPresentSlices removes every surviving slice's contribution from rhs.
// This is the expensive half of a repair: one field multiply-add per
// (surviving slice, missing slice) pair.
func (s *Set) foldPresentSlices(ctx context.Context, dir string, rep *Report, chosen []recoverySlice, rhs [][]byte) error {
	buf := make([]byte, s.SliceSize)

	for i := range s.Files {
		f := &s.Files[i]
		st := &rep.Files[i]
		if st.State == FileMissing || f.Slices() == 0 {
			continue
		}
		anyValid := false
		for _, v := range st.valid {
			if v {
				anyValid = true
				break
			}
		}
		if !anyValid {
			continue
		}

		fh, err := os.Open(f.path(dir))
		if err != nil {
			return fmt.Errorf("par2: open %s: %w", f.Name, err)
		}
		err = func() error {
			defer fh.Close()
			for j := 0; j < f.Slices(); j++ {
				if !st.valid[j] {
					continue
				}
				if err := ctx.Err(); err != nil {
					return err
				}
				if err := readSliceAt(fh, f.Name, j, s.SliceSize, buf); err != nil {
					return err
				}
				c, ok := sliceConstant(f.FirstSlice + j)
				if !ok {
					return fmt.Errorf("%w: slice index %d has no field constant", ErrMalformed, f.FirstSlice+j)
				}
				for r := range chosen {
					mulAddSlice(rhs[r], buf, gfPow(c, chosen[r].Exponent))
				}
			}
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return nil
}

// writeRepaired rebuilds every file that is not already complete, into
// temporary files beside their targets, and renames them into place only once
// all of them have matched their declared MD5.
func (s *Set) writeRepaired(ctx context.Context, dir string, rep *Report, rebuilt map[int][]byte) error {
	type pending struct {
		temp   string
		target string
		mode   os.FileMode
	}
	var writes []pending

	cleanup := func() {
		for _, w := range writes {
			os.Remove(w.temp)
		}
	}

	for i := range s.Files {
		f := &s.Files[i]
		st := &rep.Files[i]
		if st.State == FileComplete {
			continue
		}
		if err := ctx.Err(); err != nil {
			cleanup()
			return err
		}

		target := f.path(dir)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			cleanup()
			return fmt.Errorf("par2: create directory for %s: %w", f.Name, err)
		}

		mode := os.FileMode(0o644)
		if info, err := os.Stat(target); err == nil {
			mode = info.Mode().Perm()
		}

		temp, err := s.rebuildFile(ctx, dir, f, st, rebuilt)
		if err != nil {
			cleanup()
			return err
		}
		writes = append(writes, pending{temp: temp, target: target, mode: mode})
	}

	for _, w := range writes {
		if err := os.Chmod(w.temp, w.mode); err != nil {
			cleanup()
			return fmt.Errorf("par2: chmod %s: %w", w.target, err)
		}
	}
	for i, w := range writes {
		if err := os.Rename(w.temp, w.target); err != nil {
			// Everything renamed so far stays; there is nothing safe to roll
			// back to, since the originals were the damaged files. Say so
			// loudly rather than pretend.
			for _, rest := range writes[i:] {
				os.Remove(rest.temp)
			}
			return fmt.Errorf("par2: rename repaired %s: %w", w.target, err)
		}
	}
	return nil
}

// rebuildFile writes one file's full contents to a temporary file next to its
// target, taking each slice from disk when it survived and from the solved
// slices when it did not, and returns the temporary file's path. The file is
// hashed as it is written and the temporary file is removed if the hash does
// not match what the set declared — so a wrong answer never reaches the name
// a media player would open.
func (s *Set) rebuildFile(ctx context.Context, dir string, f *File, st *FileStatus, rebuilt map[int][]byte) (string, error) {
	target := f.path(dir)
	tmp, err := os.CreateTemp(filepath.Dir(target), "."+filepath.Base(target)+".par2repair-*")
	if err != nil {
		return "", fmt.Errorf("par2: create temp for %s: %w", f.Name, err)
	}
	tmpName := tmp.Name()

	fail := func(err error) (string, error) {
		tmp.Close()
		os.Remove(tmpName)
		return "", err
	}

	var src *os.File
	if st.State == FileDamaged {
		src, err = os.Open(target)
		if err != nil {
			return fail(fmt.Errorf("par2: open %s: %w", f.Name, err))
		}
		defer src.Close()
	}

	sum := md5.New()
	buf := make([]byte, s.SliceSize)
	written := uint64(0)

	for j := 0; j < f.Slices(); j++ {
		if err := ctx.Err(); err != nil {
			return fail(err)
		}
		if st.valid[j] {
			if src == nil {
				return fail(fmt.Errorf("par2: %s: slice %d marked valid but the file is missing", f.Name, j))
			}
			if err := readSliceAt(src, f.Name, j, s.SliceSize, buf); err != nil {
				return fail(err)
			}
		} else {
			data, ok := rebuilt[f.FirstSlice+j]
			if !ok {
				return fail(fmt.Errorf("par2: %s: slice %d was not reconstructed", f.Name, j))
			}
			copy(buf, data)
		}

		// The final slice is padded; only the declared length is written.
		n := uint64(len(buf))
		if written+n > f.Length {
			n = f.Length - written
		}
		if _, err := tmp.Write(buf[:n]); err != nil {
			return fail(fmt.Errorf("par2: write %s: %w", f.Name, err))
		}
		sum.Write(buf[:n])
		written += n
	}

	if written != f.Length {
		return fail(fmt.Errorf("%w: %s: wrote %d bytes, expected %d", ErrRepairFailed, f.Name, written, f.Length))
	}
	var got [16]byte
	copy(got[:], sum.Sum(nil))
	if got != f.MD5 {
		return fail(&ChecksumError{Name: f.Name, Expected: f.MD5, Actual: got})
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return "", fmt.Errorf("par2: close repaired %s: %w", f.Name, err)
	}
	return tmpName, nil
}

// readSliceAt fills buf with slice index j of fh, zero-padding past the end of
// the file exactly as the slice checksums assume.
func readSliceAt(fh *os.File, name string, j int, sliceSize uint64, buf []byte) error {
	off := int64(j) * int64(sliceSize)
	n, err := fh.ReadAt(buf, off)
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("par2: read %s slice %d: %w", name, j, err)
	}
	for k := n; k < len(buf); k++ {
		buf[k] = 0
	}
	return nil
}

// readAt fills buf from path at off. Recovery slice payloads are left on disk
// at parse time and pulled in here, one at a time, only when a repair needs
// them.
func readAt(path string, off int64, buf []byte) error {
	fh, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("par2: open %s: %w", path, err)
	}
	defer fh.Close()
	if _, err := io.ReadFull(io.NewSectionReader(fh, off, int64(len(buf))), buf); err != nil {
		return fmt.Errorf("par2: read recovery slice from %s: %w", path, err)
	}
	return nil
}
