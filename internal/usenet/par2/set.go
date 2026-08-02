package par2

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// File is one source file described by a par2 set.
type File struct {
	// Name is the file's name as the set records it, always with '/'
	// separators. It may contain directory components.
	Name string
	// Length is the file's exact size in bytes.
	Length uint64
	// MD5 is the hash of the whole file.
	MD5 [16]byte
	// MD5_16k is the hash of the file's first 16 KiB, which par2 uses to
	// recognise a renamed file. This package records it but does not rename.
	MD5_16k [16]byte
	// ID is the file's par2 identifier.
	ID [16]byte
	// FirstSlice is the file's first slice's index in the set's global slice
	// numbering, which is what assigns each slice its Reed-Solomon constant.
	FirstSlice int

	hashes []sliceHash
}

// Slices is how many slices the file occupies. A zero-length file occupies
// none; every other file occupies one more than its last full slice.
func (f *File) Slices() int { return len(f.hashes) }

// Set is a parsed par2 recovery set: the description of some files plus the
// recovery slices that can rebuild them.
//
// A Set holds no open file handles. Recovery slice payloads stay on disk and
// are read only when a repair actually needs them, so a parsed set costs
// almost nothing to keep around no matter how large the recovery volumes are.
// Parsing does read every byte of them once, because a recovery slice whose
// packet MD5 has not been checked cannot honestly be counted towards "you have
// N recovery blocks".
//
// A Set is immutable once returned and is safe for concurrent use.
type Set struct {
	// ID is the recovery set ID every packet in the set carries.
	ID [16]byte
	// SliceSize is the size every slice is padded to. Always a multiple of 4.
	SliceSize uint64
	// Files are the recovery set's source files in main-packet order, which
	// is the order the global slice numbering follows.
	Files []File
	// Sources are the par2 files that contributed packets, in the order they
	// were read.
	Sources []string
	// Creator is the identification string from the creator packet, empty if
	// the set has none. Useful in bug reports; nothing depends on it.
	Creator string

	// TotalSlices is the number of source slices across all files.
	TotalSlices int

	recovery []recoverySlice
}

// recoverySlice locates one recovery slice's payload without holding it.
type recoverySlice struct {
	Exponent uint32
	Path     string
	Offset   int64
	Length   int64
}

// RecoverySlices is how many recovery slices the set has, which is the
// maximum number of damaged or missing source slices it can rebuild.
func (s *Set) RecoverySlices() int { return len(s.recovery) }

// Open parses the par2 set that indexPath belongs to. Every other .par2 file
// in the same directory that carries the same recovery set ID is pulled in
// too, so a caller that knows only the index file still gets the whole
// recovery budget.
//
// Sibling volumes are matched by recovery set ID rather than by name, which
// means a renamed volume still counts and a same-named volume from a different
// set does not.
func Open(indexPath string) (*Set, error) {
	dir := filepath.Dir(indexPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("par2: read %s: %w", dir, err)
	}

	paths := []string{indexPath}
	var siblings []string
	for _, e := range entries {
		if e.IsDir() || !strings.EqualFold(filepath.Ext(e.Name()), ".par2") {
			continue
		}
		p := filepath.Join(dir, e.Name())
		if p == indexPath {
			continue
		}
		siblings = append(siblings, p)
	}
	sort.Strings(siblings)
	return OpenFiles(append(paths, siblings...)...)
}

// OpenFiles parses an explicit list of .par2 files as one set. The set's
// identity comes from the first main packet found; files carrying a different
// recovery set ID contribute nothing and are not an error, because a download
// directory legitimately holds several sets side by side.
func OpenFiles(paths ...string) (*Set, error) {
	if len(paths) == 0 {
		return nil, fmt.Errorf("%w: no par2 files given", ErrMalformed)
	}

	l := &loader{sets: map[[16]byte]*accum{}}
	for _, p := range paths {
		if err := l.load(p); err != nil {
			return nil, err
		}
	}
	if !l.haveID {
		// Nothing here is a set. If some bucket did fail to parse, that is a
		// better answer than "no main packet": it names the packet that broke.
		if l.firstErr != nil {
			return nil, l.firstErr
		}
		return nil, fmt.Errorf("%w: %w in %s", ErrMalformed, errNoMainPacket, strings.Join(paths, ", "))
	}
	// A parse failure only counts against the set that was asked for. A
	// download directory legitimately holds several sets side by side, and one
	// foreign or oddly-generated volume must not take a healthy release's
	// recovery budget down with it.
	if a := l.sets[l.id]; a.err != nil {
		return nil, a.err
	}
	return l.sets[l.id].build(l.id)
}

// loader reads every given file once, bucketing packets by recovery set ID.
// Which bucket is "the" set is only known once a main packet turns up, and a
// main packet can legitimately appear in the last file read, so bucketing is
// what keeps this to a single pass over what may be gigabytes of recovery
// volumes. The buckets stay small: recovery slice payloads are left on disk
// and only their locations are recorded.
type loader struct {
	sets map[[16]byte]*accum
	id   [16]byte
	// firstErr is the first parse failure in any bucket, in read order, kept
	// only so a run that never finds a main packet can say what actually went
	// wrong instead of reporting the absence.
	firstErr error
	haveID   bool
}

// accum is the packets seen so far for one recovery set ID.
type accum struct {
	main   *mainPacket
	descs  map[[16]byte]*fileDesc
	slices map[[16]byte][]sliceHash
	exps   map[uint32]bool
	rec    []recoverySlice
	// err is the first packet in this bucket that would not parse. It is held
	// rather than returned because which bucket the caller asked for is not
	// known until a main packet turns up, which may be in the last file read.
	err     error
	creator string
	sources []string
}

// fail records a parse failure against one bucket and against the loader,
// without stopping the scan: the packet belongs to whichever set carries its
// ID, and every other set in the directory is still perfectly readable.
func (l *loader) fail(a *accum, err error) {
	if a.err == nil {
		a.err = err
	}
	if l.firstErr == nil {
		l.firstErr = err
	}
}

func (l *loader) bucket(id [16]byte) *accum {
	a := l.sets[id]
	if a == nil {
		a = &accum{
			descs:  map[[16]byte]*fileDesc{},
			slices: map[[16]byte][]sliceHash{},
			exps:   map[uint32]bool{},
		}
		l.sets[id] = a
	}
	return a
}

func (l *loader) load(path string) error {
	return scanPackets(path, func(p *rawPacket) error {
		a := l.bucket(p.SetID)

		switch p.Type {
		case typeMain:
			m, err := parseMain(p.Body)
			if err != nil {
				l.fail(a, fmt.Errorf("par2: %s: %w", path, err))
				return nil
			}
			if a.main == nil {
				a.main = m
			}
			if !l.haveID {
				l.id = p.SetID
				l.haveID = true
			}

		case typeFileDesc:
			d, err := parseFileDesc(p.Body)
			if err != nil {
				l.fail(a, fmt.Errorf("par2: %s: %w", path, err))
				return nil
			}
			if _, seen := a.descs[d.ID]; !seen {
				a.descs[d.ID] = d
			}

		case typeIFSC:
			id, hashes, err := parseIFSC(p.Body)
			if err != nil {
				l.fail(a, fmt.Errorf("par2: %s: %w", path, err))
				return nil
			}
			if _, seen := a.slices[id]; !seen {
				a.slices[id] = hashes
			}

		case typeRecovery:
			if a.exps[p.Exponent] {
				return nil // the same recovery slice arrived twice
			}
			a.exps[p.Exponent] = true
			a.rec = append(a.rec, recoverySlice{
				Exponent: p.Exponent,
				Path:     p.Path,
				Offset:   p.DataOffset,
				Length:   p.DataLength,
			})

		case typeCreator:
			if a.creator == "" {
				a.creator = strings.TrimRight(string(p.Body), "\x00")
			}

		default:
			return nil // unknown packet type: skip it, per the spec
		}

		if n := len(a.sources); n == 0 || a.sources[n-1] != path {
			a.sources = append(a.sources, path)
		}
		return nil
	})
}

func (a *accum) build(id [16]byte) (*Set, error) {
	s := &Set{
		ID:        id,
		SliceSize: a.main.SliceSize,
		Sources:   a.sources,
		Creator:   a.creator,
		recovery:  a.rec,
	}

	next := 0
	for _, fid := range a.main.Recovery {
		d, ok := a.descs[fid]
		if !ok {
			return nil, fmt.Errorf("%w: no file description for recovery set file %x", ErrMalformed, fid)
		}
		name, err := safeName(d.Name)
		if err != nil {
			return nil, err
		}

		want := int((d.Length + s.SliceSize - 1) / s.SliceSize)
		hashes := a.slices[fid]
		if len(hashes) != want {
			return nil, fmt.Errorf("%w: %s declares %d bytes (%d slices) but carries %d slice checksums",
				ErrMalformed, d.Name, d.Length, want, len(hashes))
		}

		s.Files = append(s.Files, File{
			Name:       name,
			Length:     d.Length,
			MD5:        d.MD5,
			MD5_16k:    d.MD5_16,
			ID:         d.ID,
			FirstSlice: next,
			hashes:     hashes,
		})
		next += want
	}
	s.TotalSlices = next

	if s.TotalSlices > maxInputSlices {
		return nil, fmt.Errorf("%w: %d slices exceeds the %d the field can address",
			ErrMalformed, s.TotalSlices, maxInputSlices)
	}

	// Recovery slices must all be exactly one slice long; anything else means
	// the volume was truncated in a way the packet MD5 somehow survived, and
	// using it would silently corrupt the solve.
	kept := s.recovery[:0]
	for _, r := range s.recovery {
		if uint64(r.Length) == s.SliceSize {
			kept = append(kept, r)
		}
	}
	s.recovery = kept

	// Lowest exponent first. par2cmdline consumes recovery blocks in this
	// order, so matching it means we build the same matrix it does for the
	// same damage.
	sort.Slice(s.recovery, func(i, j int) bool { return s.recovery[i].Exponent < s.recovery[j].Exponent })

	return s, nil
}

// safeName normalises a name from a par2 set and rejects anything that would
// escape the target directory. A par2 set is attacker-controlled data from a
// public news server, so "../../.ssh/authorized_keys" has to be a hard error
// rather than something the repair writes.
func safeName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: empty name", ErrUnsafeName)
	}
	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: %q contains a NUL", ErrUnsafeName, name)
	}
	// par2 uses '/' as the separator; a backslash is a literal character on
	// POSIX but a separator on Windows, so treat it as a separator too rather
	// than let the same set mean different things on different platforms.
	clean := strings.ReplaceAll(name, "\\", "/")
	if strings.HasPrefix(clean, "/") || filepath.IsAbs(clean) || filepath.VolumeName(clean) != "" {
		return "", fmt.Errorf("%w: %q is absolute", ErrUnsafeName, name)
	}
	for _, part := range strings.Split(clean, "/") {
		if part == ".." {
			return "", fmt.Errorf("%w: %q escapes the target directory", ErrUnsafeName, name)
		}
	}
	return clean, nil
}

// path is where a file lives under dir.
func (f *File) path(dir string) string {
	return filepath.Join(dir, filepath.FromSlash(f.Name))
}
