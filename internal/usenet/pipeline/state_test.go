package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/usenet/nzb"
)

// testTargets builds targets pointed at real paths under dir. The path
// matters: attach checks the sidecar against the file it claims to describe,
// so a target with no file behind it is a target with nothing to resume.
func testTargets(dir string, names ...string) []*target {
	out := make([]*target, 0, len(names))
	for i, n := range names {
		out = append(out, &target{index: i, name: n, path: filepath.Join(dir, n), fp: "fp-" + n, total: 2})
	}
	return out
}

// onDisk gives a target's file the length an interrupted run would have left
// it, which is what makes its sidecar entry trustworthy.
func onDisk(t *testing.T, tg *target, size int64) {
	t.Helper()
	f, err := os.Create(tg.path)
	if err != nil {
		t.Fatalf("create %s: %v", tg.path, err)
	}
	if err := f.Truncate(size); err != nil {
		t.Fatalf("size %s: %v", tg.path, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close %s: %v", tg.path, err)
	}
}

func TestStateRoundTripsThroughTheSidecar(t *testing.T) {
	dir := t.TempDir()
	targets := testTargets(dir, "a.rar", "b.rar")
	st := loadState(dir)
	st.attach(targets)

	targets[0].size, targets[0].end = 4096, 4096
	targets[0].crc, targets[0].hasCRC = 0xdeadbeef, true
	st.mark(targets[0], 2, 2048, 2048)
	st.mark(targets[0], 1, 0, 2048)
	onDisk(t, targets[0], 4096)
	if err := st.save(dir); err != nil {
		t.Fatalf("save: %v", err)
	}

	fresh := testTargets(dir, "a.rar", "b.rar")
	done := loadState(dir).attach(fresh)
	if len(done[0]) != 2 || len(done[1]) != 0 {
		t.Fatalf("resumed %d and %d segments, want 2 and 0", len(done[0]), len(done[1]))
	}
	if got := done[0][2]; got.Begin != 2048 || got.Bytes != 2048 {
		t.Fatalf("segment 2 = %+v", got)
	}
	if fresh[0].size != 4096 || fresh[0].end != 4096 || !fresh[0].sized {
		t.Fatalf("target did not recover its size: %+v", fresh[0])
	}
	if !fresh[0].hasCRC || fresh[0].crc != 0xdeadbeef {
		t.Fatalf("target did not recover its file crc: %x", fresh[0].crc)
	}

	// Segments are written in order regardless of the order they landed in,
	// because the sidecar is read by people debugging stuck downloads.
	raw := readSidecar(t, dir)
	if raw.Files[0].Segments[0].Number != 1 || raw.Files[0].Segments[1].Number != 2 {
		t.Fatalf("segments were not sorted: %+v", raw.Files[0].Segments)
	}
}

func readSidecar(t *testing.T, dir string) state {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, StateFile))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	var st state
	if err := json.Unmarshal(data, &st); err != nil {
		t.Fatalf("decode sidecar: %v", err)
	}
	return st
}

// A sidecar entry that might belong to something else is worth nothing:
// skipping a segment that was never fetched writes a hole nobody knows about.
func TestStateDiscardsAnEntryItCannotTrust(t *testing.T) {
	write := func(t *testing.T, dir string, mutate func(*state)) {
		t.Helper()
		targets := testTargets(dir, "a.rar")
		st := loadState(dir)
		st.attach(targets)
		targets[0].size, targets[0].end = 100, 100
		st.mark(targets[0], 1, 0, 100)
		onDisk(t, targets[0], 100)
		if err := st.save(dir); err != nil {
			t.Fatalf("save: %v", err)
		}
		if mutate == nil {
			return
		}
		stored := readSidecar(t, dir)
		mutate(&stored)
		out, err := json.Marshal(&stored)
		if err != nil {
			t.Fatalf("encode: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, StateFile), out, 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	cases := []struct {
		name   string
		mutate func(*state)
		load   func(dir string) []*target
		disk   func(t *testing.T, dir string)
	}{
		{
			name: "the file's segments changed",
			load: func(dir string) []*target {
				ts := testTargets(dir, "a.rar")
				ts[0].fp = "regrabbed"
				return ts
			},
		},
		{
			name:   "the sidecar is from a future version",
			mutate: func(s *state) { s.Version = stateVersion + 1 },
			load:   func(dir string) []*target { return testTargets(dir, "a.rar") },
		},
		{
			// The file the sidecar describes is gone. Trusting the entry would
			// re-create it as a hole of zeros that every recorded segment
			// claims to have filled, and report the download complete.
			name: "the file is gone",
			load: func(dir string) []*target { return testTargets(dir, "a.rar") },
			disk: func(t *testing.T, dir string) {
				if err := os.Remove(filepath.Join(dir, "a.rar")); err != nil {
					t.Fatalf("remove: %v", err)
				}
			},
		},
		{
			// Same failure, spelled with a truncation: the bytes the segment
			// claims are simply not there any more.
			name: "the file is shorter than the sidecar says",
			load: func(dir string) []*target { return testTargets(dir, "a.rar") },
			disk: func(t *testing.T, dir string) {
				if err := os.Truncate(filepath.Join(dir, "a.rar"), 40); err != nil {
					t.Fatalf("truncate: %v", err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, tc.mutate)
			if tc.disk != nil {
				tc.disk(t, dir)
			}
			done := loadState(dir).attach(tc.load(dir))
			if len(done[0]) != 0 {
				t.Fatalf("kept %d segments from an untrusted entry", len(done[0]))
			}
		})
	}

	t.Run("the sidecar is not json", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, StateFile), []byte("{not json"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
		done := loadState(dir).attach(testTargets(dir, "a.rar"))
		if len(done[0]) != 0 {
			t.Fatalf("a corrupt sidecar was not discarded")
		}
	})
}

// The second pass of a download, the par2 volumes verification asked for,
// shares a directory with the first, and must not cost the first its progress.
func TestStateKeepsFilesAnotherPassIsNotTouching(t *testing.T) {
	dir := t.TempDir()
	content := testTargets(dir, "release.part01.rar")
	st := loadState(dir)
	st.attach(content)
	content[0].size, content[0].end = 100, 100
	st.mark(content[0], 1, 0, 100)
	onDisk(t, content[0], 100)
	if err := st.save(dir); err != nil {
		t.Fatalf("save: %v", err)
	}

	// A run that only names the par2 volume.
	par2 := testTargets(dir, "release.vol000+01.par2")
	second := loadState(dir)
	second.attach(par2)
	par2[0].size, par2[0].end = 50, 50
	second.mark(par2[0], 1, 0, 50)
	onDisk(t, par2[0], 50)
	if err := second.save(dir); err != nil {
		t.Fatalf("save: %v", err)
	}

	done := loadState(dir).attach(testTargets(dir, "release.part01.rar"))
	if len(done[0]) != 1 {
		t.Fatalf("the content file's progress was lost by the par2 pass: %v", done[0])
	}
}

func TestStateFlushesOnCountOrOnTime(t *testing.T) {
	st := &state{last: time.Now()}
	if st.due(time.Now()) {
		t.Fatal("a clean state asked to be written")
	}
	st.dirty = flushEvery - 1
	if st.due(st.last) {
		t.Fatal("a state below the threshold asked to be written immediately")
	}
	if !st.due(st.last.Add(flushInterval)) {
		t.Fatal("a state that sat for the interval was not written")
	}
	st.dirty = flushEvery
	if !st.due(st.last) {
		t.Fatal("a state at the threshold was not written")
	}
}

// A download that never returns must still leave a sidecar behind, or a
// process killed mid-release refetches everything.
func TestDownloadWritesTheSidecarWhileItRuns(t *testing.T) {
	restore := flushEvery
	flushEvery = 1
	t.Cleanup(func() { flushEvery = restore })

	srv := newServer(t)
	file := stage(t, "release.mkv", payload(20, 10_000), 500)
	file.publish(srv)
	dir := t.TempDir()

	var mu sync.Mutex
	var midRun int
	served := 0
	srv.SetBodyHook(func(string) {
		mu.Lock()
		served++
		nth := served
		mu.Unlock()
		if nth != 6 {
			return
		}
		// Five articles have been answered and written before this hook runs;
		// with a per-segment flush the sidecar must already know about them.
		data, err := os.ReadFile(filepath.Join(dir, StateFile))
		if err != nil {
			return
		}
		var st state
		if json.Unmarshal(data, &st) == nil && len(st.Files) == 1 {
			mu.Lock()
			midRun = len(st.Files[0].Segments)
			mu.Unlock()
		}
	})

	if _, err := Download(context.Background(), document(t, file), dir, newPool(t, srv),
		Options{SkipSpaceCheck: true, Concurrency: 1}); err != nil {
		t.Fatalf("Download: %v", err)
	}
	mu.Lock()
	got := midRun
	mu.Unlock()
	if got == 0 {
		t.Fatal("the sidecar was empty half way through the download")
	}
}

func TestFingerprintChangesWithTheSegments(t *testing.T) {
	file := nzb.File{
		Subject:  `Rel [1/1] - "a.rar" yEnc (1/2)`,
		Segments: []nzb.Segment{{Number: 1, Bytes: 10, MessageID: "a@x"}, {Number: 2, Bytes: 10, MessageID: "b@x"}},
	}
	base := fingerprint("a.rar", file)

	if fingerprint("a.rar", file) != base {
		t.Fatal("the fingerprint is not stable")
	}
	if fingerprint("b.rar", file) == base {
		t.Fatal("a different on-disk name produced the same fingerprint")
	}

	changed := file
	changed.Segments = []nzb.Segment{{Number: 1, Bytes: 10, MessageID: "a@x"}, {Number: 2, Bytes: 10, MessageID: "c@x"}}
	if fingerprint("a.rar", changed) == base {
		t.Fatal("a different message-id produced the same fingerprint")
	}
}
