package extract

import (
	"encoding/binary"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
)

// volumeSet resolves the volume the decoder asks for onto the file that
// actually holds it.
//
// rardecode walks a multi-volume set by *name*: it takes the name of the volume
// it is reading, increments the number in it preserving that number's width,
// and opens the result. Two things posted on Usenet break that, and both are
// common enough that a release hitting either is not exotic:
//
//   - Inconsistent width. A poster's set runs part01…part09 and then part010…
//     part030, so after part09 the successor rardecode infers ("part10.rar")
//     does not exist under that spelling at all.
//
//   - Scrambled names. The name a volume was posted under is not always the
//     volume it holds. A release seen in the wild had part06 holding volume 7
//     and part07 holding volume 6, with the same swap again at 15/16. Opening
//     them in name order gets a file that exists, is a valid rar volume, and is
//     the wrong one — which surfaces as rardecode's "bad volume number" a few
//     megabytes into the payload.
//
// The archive is the authority on which volume it is: a RAR5 volume records its
// own number in its main header, and that number is what this maps on. The
// filename is only a fallback, for the volumes that carry no such record —
// volume one of a RAR5 set, and every RAR4 volume, which has no field for it.
//
// Nothing here renames or moves anything on disk. The set is read through this
// mapping and the files keep the names the poster gave them, because those
// names are also what [cleanup] deletes and what a human looking in the
// directory sees.
type volumeSet struct {
	// dir is the download directory the volumes live in.
	dir string
	// byNum maps a 1-based volume number onto a filename inside dir.
	byNum map[int]string
	// nominal is the first volume by filename, the fallback when nothing in
	// the set could be numbered.
	nominal string
}

// resolveVolumes reads each volume's own idea of its number and builds the
// mapping. volumes are filenames inside dir, in the order [Detect] found them.
func resolveVolumes(dir string, volumes []string) volumeSet {
	vs := volumeSet{dir: dir, byNum: make(map[int]string, len(volumes))}
	if len(volumes) > 0 {
		vs.nominal = volumes[0]
	}

	named := make(map[string]int, len(volumes))
	var unnumbered []string
	for _, name := range volumes {
		n, ok := partNumber(name)
		if !ok {
			// Legacy .rar/.r00/.r01 naming carries no part number to key on,
			// and RAR4 records no volume number either, so such a set is left
			// entirely to rardecode's own walk.
			continue
		}
		named[name] = n
		num, err := rarVolumeNumber(filepath.Join(dir, name))
		if err != nil {
			unnumbered = append(unnumbered, name)
			continue
		}
		// The archive's own answer wins, and wins over any name.
		vs.byNum[num] = name
	}

	// Then the volumes that could not say: they keep the number their name
	// claims, but only where a numbered volume has not already taken the slot.
	for _, name := range unnumbered {
		if _, taken := vs.byNum[named[name]]; !taken {
			vs.byNum[named[name]] = name
		}
	}
	return vs
}

// first is the absolute path of the volume the set starts at, which is the one
// handed to the decoder.
func (v volumeSet) first() string {
	if name, ok := v.byNum[1]; ok {
		return filepath.Join(v.dir, name)
	}
	return filepath.Join(v.dir, v.nominal)
}

// Open implements fs.FS for rardecode.
//
// The name it is given is one rardecode composed by incrementing the previous
// volume's, so the part number in it is the volume number wanted — that is the
// only thing read out of it. A number this set does not have is reported as
// not existing rather than as whatever file happens to bear the name, because
// "the volume after this one is missing" is an answer rardecode already knows
// how to end an archive on, and handing it the wrong volume instead is how a
// misnamed set produces a corrupt file rather than an error.
func (v volumeSet) Open(name string) (fs.File, error) {
	if n, ok := partNumber(filepath.Base(name)); ok && len(v.byNum) > 0 {
		if real, hit := v.byNum[n]; hit {
			return os.Open(filepath.Join(v.dir, real))
		}
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return os.Open(name)
}

// partNumber is the volume number a modern multi-volume filename claims.
func partNumber(name string) (int, bool) {
	m := partRE.FindStringSubmatch(name)
	if m == nil {
		return 0, false
	}
	n, err := strconv.Atoi(m[2])
	if err != nil || n < 1 {
		return 0, false
	}
	return n, true
}

// errNoVolumeNumber means the file does not record which volume it is: it is
// not a RAR5 archive, or it is the first volume of one, which omits the field.
var errNoVolumeNumber = errors.New("extract: archive records no volume number")

// rar5Signature is the marker block a RAR5 archive starts with. RAR4's is one
// byte shorter and ends 0x00; the difference in the final bytes is the version.
var rar5Signature = []byte{'R', 'a', 'r', '!', 0x1a, 0x07, 0x01, 0x00}

// volumeHeaderBytes is how much of a volume is read to find its number. The
// main archive header follows the signature immediately and is a few dozen
// bytes; this is slack, not a guess at a maximum.
const volumeHeaderBytes = 512

// rarVolumeNumber reads the 1-based volume number a RAR5 archive records in its
// main header.
//
// Only the main header is parsed, and only far enough to reach the number: the
// signature, one block header, and the archive block's flags. Anything it does
// not understand is errNoVolumeNumber, never a decoding attempt — this runs
// over attacker-supplied files, and the caller's fallback is the behaviour that
// was there before it.
func rarVolumeNumber(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	buf := make([]byte, volumeHeaderBytes)
	n, err := io.ReadFull(f, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return 0, err
	}
	buf = buf[:n]

	if len(buf) < len(rar5Signature) {
		return 0, errNoVolumeNumber
	}
	for i, b := range rar5Signature {
		if buf[i] != b {
			return 0, errNoVolumeNumber
		}
	}
	r := &varintReader{b: buf[len(rar5Signature):]}

	// Block header: a CRC32 this does not check (rardecode checks it when it
	// reads the archive for real; a mismatch here would only mean falling back
	// to the name, which is what a bad header should do anyway), the size of
	// the block body, then the body.
	if !r.skip(4) {
		return 0, errNoVolumeNumber
	}
	size, ok := r.uvarint()
	if !ok {
		return 0, errNoVolumeNumber
	}
	body := &varintReader{b: r.rest()}
	if uint64(len(body.b)) < size {
		return 0, errNoVolumeNumber
	}
	body.b = body.b[:size]

	blockType, ok := body.uvarint()
	if !ok || blockType != rar5BlockMain {
		// An encryption block ahead of the main one means the archive is
		// password protected, which extraction refuses further down anyway.
		return 0, errNoVolumeNumber
	}
	flags, ok := body.uvarint()
	if !ok {
		return 0, errNoVolumeNumber
	}
	if flags&rar5BlockHasExtra != 0 {
		if _, ok := body.uvarint(); !ok {
			return 0, errNoVolumeNumber
		}
	}
	if flags&rar5BlockHasData != 0 {
		if _, ok := body.uvarint(); !ok {
			return 0, errNoVolumeNumber
		}
	}
	archiveFlags, ok := body.uvarint()
	if !ok || archiveFlags&rar5ArchiveHasVolumeNumber == 0 {
		// No number recorded. The first volume of a set is written this way,
		// so this is the ordinary answer for one file in every set.
		return 0, errNoVolumeNumber
	}
	num, ok := body.uvarint()
	if !ok || num > 1<<20 {
		return 0, errNoVolumeNumber
	}
	// Recorded 0-based; every other number in this package is the 1-based one
	// the filenames use.
	return int(num) + 1, nil
}

// RAR5 block and archive-header flags, named for the fields this reads. They
// mirror archive50.go in github.com/nwaples/rardecode/v2.
const (
	rar5BlockMain              = 1
	rar5BlockHasExtra          = 0x0001
	rar5BlockHasData           = 0x0002
	rar5ArchiveHasVolumeNumber = 0x0002
)

// varintReader walks the little-endian base-128 varints RAR5 headers are built
// from, reporting rather than panicking when it runs out of bytes.
type varintReader struct{ b []byte }

func (r *varintReader) skip(n int) bool {
	if len(r.b) < n {
		return false
	}
	r.b = r.b[n:]
	return true
}

func (r *varintReader) uvarint() (uint64, bool) {
	v, n := binary.Uvarint(r.b)
	if n <= 0 {
		return 0, false
	}
	r.b = r.b[n:]
	return v, true
}

func (r *varintReader) rest() []byte { return r.b }
