package par2

import (
	"bufio"
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"os"
)

// FileState is how a source file compares to what the par2 set says it should
// be.
type FileState int

const (
	// FileComplete means the file exists, is the right length, and every one
	// of its slices matches.
	FileComplete FileState = iota
	// FileDamaged means the file exists but does not match: bad slices, the
	// wrong length, or both.
	FileDamaged
	// FileMissing means the file is not there at all.
	FileMissing
)

func (s FileState) String() string {
	switch s {
	case FileComplete:
		return "complete"
	case FileDamaged:
		return "damaged"
	case FileMissing:
		return "missing"
	default:
		return fmt.Sprintf("FileState(%d)", int(s))
	}
}

// FileStatus is one file's verification result.
type FileStatus struct {
	// Name is the file as the par2 set names it.
	Name string
	// State is the verdict.
	State FileState
	// Length is the length the set declares.
	Length uint64
	// ActualLength is the length on disk, or -1 when the file is missing.
	ActualLength int64
	// GoodSlices and BadSlices partition the file's slices. Their sum is the
	// number of slices the set says the file has.
	GoodSlices int
	BadSlices  int

	// valid[i] reports whether slice i of this file is usable as-is. Repair
	// reads it to decide which slices to copy and which to rebuild.
	valid []bool
}

// Report is the result of verifying a whole set against a directory.
type Report struct {
	// Dir is the directory that was verified.
	Dir string
	// SliceSize is the set's slice size, repeated here so a caller holding
	// only a report can turn block counts into bytes.
	SliceSize uint64
	// Files are the per-file verdicts, in the set's file order.
	Files []FileStatus
	// TotalSlices is how many source slices the set describes.
	TotalSlices int
	// GoodSlices is how many of them survived; MissingSlices is the rest, and
	// is exactly how many recovery slices a repair would consume.
	GoodSlices    int
	MissingSlices int
	// RecoverySlices is how many recovery slices the set actually has.
	RecoverySlices int
}

// Complete reports whether the directory already matches the set, in which
// case there is nothing to repair.
func (r *Report) Complete() bool {
	for i := range r.Files {
		if r.Files[i].State != FileComplete {
			return false
		}
	}
	return true
}

// Repairable reports whether a repair would succeed. A complete set is not
// "repairable", there is nothing to repair, so callers branch on Complete
// first.
func (r *Report) Repairable() bool {
	return !r.Complete() && r.MissingSlices <= r.RecoverySlices
}

// Deficit is how many more recovery blocks would be needed, zero when the
// damage is inside the set's budget. It is the number the UI shows.
func (r *Report) Deficit() int {
	if d := r.MissingSlices - r.RecoverySlices; d > 0 {
		return d
	}
	return 0
}

// Verify classifies every file the set describes against dir, slice by slice.
//
// Verification is positional: slice i of a file is checked against the bytes
// at offset i*SliceSize. par2cmdline additionally scans for correct blocks
// that have been *moved* within a file, which recovers from an insertion or
// deletion; that is a different failure mode from the one the Usenet pipeline
// produces (a missing article leaves a hole exactly where the article was), so
// this package does not do it. The consequence is conservative in the safe
// direction: a shifted file is reported as more damaged than par2cmdline would
// call it, never less.
func (s *Set) Verify(ctx context.Context, dir string) (*Report, error) {
	rep := &Report{
		Dir:            dir,
		SliceSize:      s.SliceSize,
		TotalSlices:    s.TotalSlices,
		RecoverySlices: len(s.recovery),
	}

	for i := range s.Files {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		st, err := s.verifyFile(ctx, dir, &s.Files[i])
		if err != nil {
			return nil, err
		}
		rep.Files = append(rep.Files, st)
		rep.GoodSlices += st.GoodSlices
		rep.MissingSlices += st.BadSlices
	}
	return rep, nil
}

func (s *Set) verifyFile(ctx context.Context, dir string, f *File) (FileStatus, error) {
	n := f.Slices()
	st := FileStatus{
		Name:         f.Name,
		Length:       f.Length,
		ActualLength: -1,
		valid:        make([]bool, n),
	}

	fh, err := os.Open(f.path(dir))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			st.State = FileMissing
			st.BadSlices = n
			return st, nil
		}
		return st, fmt.Errorf("par2: open %s: %w", f.Name, err)
	}
	defer fh.Close()

	info, err := fh.Stat()
	if err != nil {
		return st, fmt.Errorf("par2: stat %s: %w", f.Name, err)
	}
	st.ActualLength = info.Size()

	r := bufio.NewReaderSize(fh, 1<<20)
	buf := make([]byte, s.SliceSize)
	fileHash := md5.New()

	for i := 0; i < n; i++ {
		if i%64 == 0 {
			if err := ctx.Err(); err != nil {
				return st, err
			}
		}

		read, err := io.ReadFull(r, buf)
		switch {
		case err == nil, errors.Is(err, io.ErrUnexpectedEOF):
		case errors.Is(err, io.EOF):
			read = 0
		default:
			return st, fmt.Errorf("par2: read %s: %w", f.Name, err)
		}
		// Everything past the end of the file is a zero, exactly as the
		// checksums were computed over the padded slice.
		for j := read; j < len(buf); j++ {
			buf[j] = 0
		}

		// The whole-file MD5 only covers the declared length, so a file that
		// is longer than declared does not poison it.
		start := uint64(i) * s.SliceSize
		if start < f.Length {
			take := f.Length - start
			if take > uint64(read) {
				take = uint64(read)
			}
			fileHash.Write(buf[:take])
		}

		want := f.hashes[i]
		if crc32.ChecksumIEEE(buf) == want.CRC && md5.Sum(buf) == want.MD5 {
			st.valid[i] = true
			st.GoodSlices++
		} else {
			st.BadSlices++
		}
	}

	switch {
	case st.BadSlices > 0:
		st.State = FileDamaged
	case uint64(st.ActualLength) != f.Length:
		// Every slice matched but the file is the wrong size: trailing garbage,
		// or a shorter file whose missing tail happens to be inside the padding
		// of its final slice. It still has to be rewritten, but it needs no
		// recovery blocks to do it.
		st.State = FileDamaged
	case !bytes.Equal(fileHash.Sum(nil), f.MD5[:]):
		// Unreachable for a well-formed set: the slice checksums cover every
		// byte. If it ever fires, the set contradicts itself and we would
		// rather say so than declare a mismatched file complete.
		st.State = FileDamaged
	default:
		st.State = FileComplete
	}
	return st, nil
}
