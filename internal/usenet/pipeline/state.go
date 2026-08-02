package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// StateFile is the resume sidecar's name inside a download's directory.
//
// It lives with the data rather than in the database on purpose. Caravan's
// database is a disposable cache — delete it, rescan, the library comes back —
// but a half-finished download is not something a rescan can rebuild, and
// refetching thirty gigabytes because a cache was thrown away is exactly the
// kind of expensive surprise a paid Usenet account should never spring. Keeping
// it beside the parts also means the state moves when the download does.
const StateFile = ".caravan-segments.json"

// stateVersion is bumped when the sidecar's shape changes. A sidecar from a
// different version is discarded, not migrated: the cost of getting it wrong is
// a silently skipped segment, and the cost of throwing it away is a refetch.
const stateVersion = 1

// flushEvery is how many newly completed segments trigger a sidecar write. It
// is a variable so a test can force a write per segment without a sleep, and
// nothing outside this package changes it.
var flushEvery = 32

// flushInterval bounds how long a completed segment can sit unrecorded when
// segments are arriving slower than flushEvery. Rewriting the whole sidecar per
// segment would cost, on a release with fifteen thousand of them, gigabytes of
// pointless writes; a crash between flushes costs a few refetched articles.
const flushInterval = 2 * time.Second

// state is the sidecar's contents.
//
// It is keyed by file rather than by the run that produced it, because a
// download happens in more than one pass: the content files first, then
// whichever par2 volumes verification turned out to need, into the same
// directory. A sidecar that only understood one pass would throw away the
// other's progress the moment the second one started.
type state struct {
	Version int         `json:"version"`
	Files   []fileState `json:"files"`

	index map[string]int
	dirty int
	last  time.Time
}

// fileState is one target's progress.
type fileState struct {
	Name string `json:"name"`
	// Fingerprint is the file's name and segment list hashed, so an entry
	// left by a different release — or by the same release re-grabbed with
	// different segments — is discarded instead of being used to skip
	// articles that were never fetched.
	Fingerprint string         `json:"fingerprint"`
	Size        int64          `json:"size,omitempty"`
	End         int64          `json:"end,omitempty"`
	FileCRC32   *uint32        `json:"file_crc32,omitempty"`
	Segments    []segmentState `json:"segments"`
}

// segmentState is one segment that is verified on disk. Only completed
// segments are recorded: a segment that failed is retried on the next run,
// because an article a backup server had all along should not be written off
// forever by one bad afternoon.
type segmentState struct {
	Number int   `json:"number"`
	Begin  int64 `json:"begin"`
	Bytes  int64 `json:"bytes"`
}

// loadState reads a download directory's sidecar, returning an empty one when
// it is absent, unreadable, or written by another version of this package.
// Every one of those is a reason to refetch, never a reason to fail the
// download; per-file trust is decided later, by attach.
func loadState(dir string) *state {
	s := &state{Version: stateVersion, last: time.Now()}

	data, err := os.ReadFile(filepath.Join(dir, StateFile))
	if err == nil {
		var stored state
		if json.Unmarshal(data, &stored) == nil && stored.Version == stateVersion {
			s.Files = stored.Files
		}
	}
	s.index = make(map[string]int, len(s.Files))
	for i, f := range s.Files {
		s.index[f.Name] = i
	}
	return s
}

// attach binds each target to its entry in the sidecar and reports, per
// target, which segment numbers are already on disk.
//
// An entry whose fingerprint does not match the file being downloaded is
// replaced rather than trusted. Entries for files this run is not touching are
// left exactly as they are: that is what lets the repair stage fetch par2
// volumes into a directory whose content files are already half done.
func (s *state) attach(targets []*target) []map[int]segmentState {
	done := make([]map[int]segmentState, len(targets))
	for i, t := range targets {
		slot, known := s.index[t.name]
		if !known {
			slot = len(s.Files)
			s.Files = append(s.Files, fileState{Name: t.name, Fingerprint: t.fp})
			s.index[t.name] = slot
		} else if s.Files[slot].Fingerprint != t.fp {
			s.Files[slot] = fileState{Name: t.name, Fingerprint: t.fp}
		}
		t.slot = slot

		// The sidecar is a claim about a file on disk, and the file is the
		// authority. Nothing else checks: open() creates without truncating and
		// finish() only ever grows, so a target that was deleted or truncated
		// between runs would come back as a full-length hole of zeros that every
		// recorded segment claims to have filled — a download that reports
		// itself complete with no failures, which is exactly the state that
		// makes the engine skip par2 and hand a zero-filled file to the import.
		s.Files[slot] = trimToDisk(t.path, s.Files[slot])

		fs := s.Files[slot]
		done[i] = make(map[int]segmentState, len(fs.Segments))
		for _, seg := range fs.Segments {
			done[i][seg.Number] = seg
		}
		t.size, t.end = fs.Size, fs.End
		t.sized = fs.Size > 0
		if fs.FileCRC32 != nil {
			t.crc, t.hasCRC = *fs.FileCRC32, true
		}
	}
	return done
}

// trimToDisk drops the part of a sidecar entry the file on disk cannot back
// up: every segment whose bytes would sit past the file's actual end, and the
// whole entry when the file is gone or is not a regular file.
//
// It is deliberately per-segment rather than all-or-nothing. A crash between
// recording a segment and its WriteAt landing leaves the file a few bytes
// shorter than End, and refetching a whole thirty-gigabyte release over that
// is the bill the sidecar exists to prevent; refetching the segments the file
// cannot account for costs a handful of articles.
func trimToDisk(path string, fs fileState) fileState {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return fileState{Name: fs.Name, Fingerprint: fs.Fingerprint}
	}

	size := info.Size()
	kept := make([]segmentState, 0, len(fs.Segments))
	for _, seg := range fs.Segments {
		if seg.Begin+seg.Bytes <= size {
			kept = append(kept, seg)
		}
	}
	if len(kept) == len(fs.Segments) {
		return fs
	}
	fs.Segments = kept
	if fs.End > size {
		fs.End = size
	}
	return fs
}

// mark records a completed segment. It does not write: save decides that.
func (s *state) mark(t *target, number int, begin, bytes int64) {
	fs := &s.Files[t.slot]
	fs.Segments = append(fs.Segments, segmentState{Number: number, Begin: begin, Bytes: bytes})

	t.mu.Lock()
	fs.Size, fs.End = t.size, t.end
	if t.hasCRC {
		crc := t.crc
		fs.FileCRC32 = &crc
	}
	t.mu.Unlock()

	s.dirty++
}

// due reports whether enough has changed, or enough time passed, to be worth
// rewriting the sidecar.
func (s *state) due(now time.Time) bool {
	return s.dirty > 0 && (s.dirty >= flushEvery || now.Sub(s.last) >= flushInterval)
}

// save rewrites the sidecar through a temporary file in the same directory, so
// a crash mid-write leaves the previous sidecar rather than a truncated one.
// Segments are sorted first: the file is read by humans debugging a stuck
// download at least as often as by this package.
func (s *state) save(dir string) error {
	for i := range s.Files {
		segs := s.Files[i].Segments
		sort.Slice(segs, func(a, b int) bool { return segs[a].Number < segs[b].Number })
	}
	data, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("pipeline: encode resume state: %w", err)
	}

	tmp, err := os.CreateTemp(dir, StateFile+".*")
	if err != nil {
		return fmt.Errorf("pipeline: create resume state: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("pipeline: write resume state: %w", err)
	}
	// The point of the sidecar is surviving a crash, so the bytes have to be
	// on the platter before the rename publishes them.
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("pipeline: sync resume state: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("pipeline: close resume state: %w", err)
	}
	if err := os.Chmod(tmpName, 0o644); err != nil {
		return fmt.Errorf("pipeline: chmod resume state: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(dir, StateFile)); err != nil {
		return fmt.Errorf("pipeline: publish resume state: %w", err)
	}

	s.dirty = 0
	s.last = time.Now()
	return nil
}
