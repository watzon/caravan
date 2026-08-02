package pipeline

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/watzon/caravan/internal/usenet/nzb"
	"github.com/watzon/caravan/internal/usenet/yenc"
)

// target is one NZB file and the file on disk it assembles into.
//
// Everything mutable on it is guarded by mu and written from several fetch
// workers at once; the *os.File itself needs no guarding, because WriteAt is a
// pwrite and is safe to call concurrently.
type target struct {
	index   int
	subject string
	name    string
	path    string
	fp      string // this file's sidecar fingerprint
	slot    int    // this file's entry in the sidecar
	isPar2  bool
	total   int // segments in the NZB
	wire    int64

	mu     sync.Mutex
	f      *os.File
	sized  bool
	size   int64 // whole-file size from =ybegin, 0 until an article says
	end    int64 // highest offset written, so an unsized file still ends right
	crc    uint32
	hasCRC bool
	done   int
	failed int
}

// plan turns the NZB's files into targets, giving each one a name on disk.
//
// Names come from the subject (nzb.File.Filename already strips path
// separators and the "." / ".." cases), and a name two files both want is
// resolved by appending the file's position in the NZB. Obfuscated releases
// really do post several files under one subject, and two files writing to one
// path would corrupt both silently. The suffix is derived from the NZB rather
// than from the order files finish in, so a resumed download picks the same
// names as the run before it.
func plan(files []nzb.File, dir string) []*target {
	used := make(map[string]struct{}, len(files))
	targets := make([]*target, 0, len(files))

	for i, f := range files {
		name := safeName(f.Filename(), i)
		if _, taken := used[name]; taken {
			name = fmt.Sprintf("%s.%d", name, i+1)
		}
		used[name] = struct{}{}

		t := &target{
			index:   i,
			subject: f.Subject,
			name:    name,
			path:    filepath.Join(dir, name),
			fp:      fingerprint(name, f),
			isPar2:  f.IsPar2(),
			total:   len(f.Segments),
			wire:    f.Bytes(),
		}
		targets = append(targets, t)
	}
	return targets
}

// safeName rejects anything that could escape the download directory. The NZB
// parser already sanitises filenames, so this is a second lock on the same
// door rather than the only one; a name that fails it becomes a positional
// placeholder, which the import stage's unmatched queue is designed to handle.
func safeName(name string, index int) string {
	name = strings.TrimSpace(name)
	switch {
	case name == "", name == ".", name == "..":
	case strings.ContainsAny(name, `/\`):
	case strings.ContainsRune(name, 0):
	default:
		return name
	}
	return fmt.Sprintf("file%03d.bin", index+1)
}

// fingerprint identifies one file inside a sidecar.
//
// It covers the on-disk name and every segment in order, which is exactly what
// "resuming this file would be safe" means: an entry written for a different
// release, or for the same release re-grabbed with different segments, must be
// discarded rather than used to skip articles that were never fetched. The
// download directory is deliberately not part of it, so moving a half-finished
// download does not throw its progress away.
func fingerprint(name string, f nzb.File) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%d\n", name, len(f.Segments))
	for _, s := range f.Segments {
		fmt.Fprintf(h, "%d\x00%d\x00%s\n", s.Number, s.Bytes, s.MessageID)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// open creates or reopens the file this target assembles into.
func (t *target) open() error {
	f, err := os.OpenFile(t.path, os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("pipeline: open %s: %w", t.name, err)
	}
	t.f = f
	return nil
}

// write puts one decoded article where it belongs.
//
// The offset is the article's own Begin and never a running total: segments
// arrive from a pool of connections in whatever order the servers answer, and
// yEnc parts carry where they go precisely so assembly does not have to buffer
// them into order.
func (t *target) write(part *yenc.Part) error {
	t.mu.Lock()
	prealloc := int64(0)
	if !t.sized && part.Size > 0 {
		t.sized, t.size, prealloc = true, part.Size, part.Size
	}
	if end := part.Begin + int64(len(part.Body)); end > t.end {
		t.end = end
	}
	if part.HasFileCRC && !t.hasCRC {
		t.crc, t.hasCRC = part.FileCRC32, true
	}
	t.mu.Unlock()

	// The first article to declare a whole-file size is the cue to reserve
	// the file. Growing only ever adds, so a concurrent WriteAt past the old
	// end cannot lose to it.
	if prealloc > 0 {
		if err := grow(t.f, prealloc); err != nil {
			return fmt.Errorf("pipeline: preallocate %s: %w", t.name, err)
		}
	}
	if _, err := t.f.WriteAt(part.Body, part.Begin); err != nil {
		return fmt.Errorf("pipeline: write %s at offset %d: %w", t.name, part.Begin, err)
	}
	return nil
}

// finish sizes the file exactly and closes it. A file with holes still gets
// its full length: par2 scans a fixed-size file, and a short one looks like a
// different file rather than a damaged one.
func (t *target) finish() error {
	if t.f == nil {
		return nil
	}
	t.mu.Lock()
	want := t.size
	if t.end > want {
		want = t.end
	}
	t.mu.Unlock()

	var errs []error
	if want > 0 {
		if info, err := t.f.Stat(); err != nil {
			errs = append(errs, fmt.Errorf("pipeline: stat %s: %w", t.name, err))
		} else if info.Size() != want {
			if err := t.f.Truncate(want); err != nil {
				errs = append(errs, fmt.Errorf("pipeline: size %s: %w", t.name, err))
			}
		}
	}
	if err := t.f.Close(); err != nil {
		errs = append(errs, fmt.Errorf("pipeline: close %s: %w", t.name, err))
	}
	t.f = nil
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

// grow extends f to at least n bytes without ever shortening it.
func grow(f *os.File, n int64) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	if info.Size() >= n {
		return nil
	}
	return f.Truncate(n)
}
