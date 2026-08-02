package pipeline

import "sync"

// Progress is what a download has achieved so far.
//
// The byte counts are on-the-wire (encoded) sizes, taken from the NZB's
// segment sizes, because that is the number the user recognises as "how much
// of this download have I paid for" and the only total that is known before
// the first article is decoded. BytesWritten is the decoded total on disk,
// which is smaller and is not known ahead of time.
//
// Failed segments never count towards Bytes: a download that finishes with
// holes stops short of its total on purpose, and the engine reports the
// failures rather than rounding them up to done.
type Progress struct {
	// Files is the number of files being downloaded.
	Files int
	// FilesComplete is how many of them have every segment on disk.
	FilesComplete int
	// Segments is the total number of segments across every file.
	Segments int
	// SegmentsDone is how many segments were decoded and written, resumed
	// ones included.
	SegmentsDone int
	// SegmentsFailed is how many segments this run gave up on. They are
	// holes for par2, not a reason to stop.
	SegmentsFailed int
	// Bytes is the on-the-wire size of the completed segments.
	Bytes int64
	// TotalBytes is the on-the-wire size of every segment.
	TotalBytes int64
	// BytesWritten is the decoded size of the completed segments: what is
	// actually on disk.
	BytesWritten int64
}

// Fraction is Bytes over TotalBytes, clamped to [0,1]. It falls back to the
// segment counts for NZBs whose segments carry no bytes attribute, and is 0
// when there is nothing to measure.
func (p Progress) Fraction() float64 {
	if p.TotalBytes > 0 {
		f := float64(p.Bytes) / float64(p.TotalBytes)
		if f > 1 {
			return 1
		}
		return f
	}
	if p.Segments > 0 {
		return float64(p.SegmentsDone) / float64(p.Segments)
	}
	return 0
}

// Tracker is the live progress of a download, safe to read from another
// goroutine while Download runs.
//
// It is a poll surface rather than a callback because the engine above it
// already polls (internal/download/engine.go's sample loop): handing it a
// callback would mean a lock inside a hot per-segment path and a chance to
// stall the scheduler from outside. Snapshot copies, so a caller can hold the
// value as long as it likes.
//
// The zero Tracker is usable and reports an empty Progress.
type Tracker struct {
	mu sync.Mutex
	p  Progress
}

// NewTracker returns a Tracker to hand to Download in Options.Progress.
func NewTracker() *Tracker { return &Tracker{} }

// Snapshot is the progress right now.
func (t *Tracker) Snapshot() Progress {
	if t == nil {
		return Progress{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.p
}

// reset installs the totals and clears the counters, so a Tracker reused
// across a restart reports this run's plan rather than the last one's.
func (t *Tracker) reset(files, segments int, total int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p = Progress{Files: files, Segments: segments, TotalBytes: total}
}

func (t *Tracker) segmentDone(wire, written int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p.SegmentsDone++
	t.p.Bytes += wire
	t.p.BytesWritten += written
}

func (t *Tracker) segmentFailed() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p.SegmentsFailed++
}

func (t *Tracker) fileComplete() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p.FilesComplete++
}
