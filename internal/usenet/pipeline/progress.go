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

// FileProgress is one file's share of a download, live.
//
// The aggregate counters in Progress answer "how far along is this download";
// these answer "which files are whole", which is what a Usenet queue drawer
// shows in place of a torrent's peer list. They are carried alongside Progress
// rather than inside it because the aggregate is read on every engine poll and
// this slice is read only when someone has the drawer open.
type FileProgress struct {
	// Name is the file's name inside the download directory: the same name
	// FileResult reports once the download is over.
	Name string
	// Segments is how many segments the file was posted in.
	Segments int
	// SegmentsDone is how many of them are on disk, resumed ones included.
	SegmentsDone int
	// SegmentsFailed is how many this run gave up on.
	SegmentsFailed int
	// IsPar2 marks a recovery volume rather than payload.
	IsPar2 bool
}

// Complete reports whether every segment of this file is on disk.
func (f FileProgress) Complete() bool { return f.Segments > 0 && f.SegmentsDone == f.Segments }

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
	mu    sync.Mutex
	p     Progress
	files []FileProgress
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

// Files is the per-file progress right now, in NZB order. Like Snapshot it
// copies, so a caller can hold the value as long as it likes.
func (t *Tracker) Files() []FileProgress {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.files) == 0 {
		return nil
	}
	out := make([]FileProgress, len(t.files))
	copy(out, t.files)
	return out
}

// reset installs the plan and clears the counters, so a Tracker reused across a
// restart reports this run's plan rather than the last one's. files is one
// entry per file with only the totals filled in.
func (t *Tracker) reset(files []FileProgress, segments int, total int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p = Progress{Files: len(files), Segments: segments, TotalBytes: total}
	t.files = make([]FileProgress, len(files))
	copy(t.files, files)
}

// file resolves one entry, or nil when the index is outside this run's plan.
// The counters are fed from the fetch workers, and a tracker handed to a second
// DownloadFiles call (the par2 pass) has a different plan; a bounds check here
// is cheaper than a panic in a worker goroutine.
func (t *Tracker) file(i int) *FileProgress {
	if i < 0 || i >= len(t.files) {
		return nil
	}
	return &t.files[i]
}

func (t *Tracker) segmentDone(file int, wire, written int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p.SegmentsDone++
	t.p.Bytes += wire
	t.p.BytesWritten += written
	if f := t.file(file); f != nil {
		f.SegmentsDone++
	}
}

func (t *Tracker) segmentFailed(file int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p.SegmentsFailed++
	if f := t.file(file); f != nil {
		f.SegmentsFailed++
	}
}

// fileComplete counts one whole file. It takes no index: whether a particular
// file is whole is already derivable from its own counters (FileProgress.
// Complete), and a second flag for the same fact is a second thing to keep true.
func (t *Tracker) fileComplete() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.p.FilesComplete++
}
