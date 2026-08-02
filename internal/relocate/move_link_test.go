package relocate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// An import hardlinks incomplete/<x> into library/<y>: one inode, two names,
// one copy of the bytes (internal/library.organize). A move to another drive
// cannot rename, so it falls through to a byte-for-byte copy per name — and
// before this was fixed that copy had no inode bookkeeping, so a 500 GB library
// that was still seeding needed 1 TB at the target and permanently occupied
// twice the space it had before.
//
// The copy path is reached here the way a resumed migration reaches it: a
// previous attempt left a wrong-sized file at the target, so os.Rename is
// skipped and copyThenRemove runs — the same branch a cross-filesystem move
// takes for every file.
func TestHardlinkedPairStaysOneInodeThroughTheCopyPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("linkKey has no implementation on windows; the migration copies each name there")
	}

	dir := t.TempDir()
	from := filepath.Join(dir, "from")
	to := filepath.Join(dir, "to")

	const rel1 = "incomplete/Arrival.mkv"
	const rel2 = "library/Movies/Arrival (2016)/Arrival (2016).mkv"
	const body = "arrival-video-bytes"

	first := filepath.Join(from, filepath.FromSlash(rel1))
	if err := os.MkdirAll(filepath.Dir(first), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(first, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	second := filepath.Join(from, filepath.FromSlash(rel2))
	if err := os.MkdirAll(filepath.Dir(second), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Link(first, second); err != nil {
		t.Skipf("this filesystem has no hardlinks: %v", err)
	}

	// Stale partial copies from an earlier attempt: they are the wrong size, so
	// every entry takes the copy branch rather than the rename fast path.
	for _, rel := range []string{rel1, rel2} {
		p := filepath.Join(to, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	entries, err := plan(from, to)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	mv := &mover{from: from, to: to, log: discardLogger()}
	if err := mv.move(context.Background(), entries, nil, false); err != nil {
		t.Fatalf("move: %v", err)
	}

	a, err := os.Stat(filepath.Join(to, filepath.FromSlash(rel1)))
	if err != nil {
		t.Fatalf("stat %s at the target: %v", rel1, err)
	}
	b, err := os.Stat(filepath.Join(to, filepath.FromSlash(rel2)))
	if err != nil {
		t.Fatalf("stat %s at the target: %v", rel2, err)
	}
	if !os.SameFile(a, b) {
		t.Fatalf("the hardlinked pair arrived as two independent inodes: "+
			"the move copied %d bytes twice and the library now occupies double the space", a.Size())
	}
	if got := string(mustRead(t, filepath.Join(to, filepath.FromSlash(rel2)))); got != body {
		t.Fatalf("content at the target = %q, want %q", got, body)
	}

	// The never-lost invariant still holds: nothing is left at the old root.
	for _, rel := range []string{rel1, rel2} {
		if _, err := os.Lstat(filepath.Join(from, filepath.FromSlash(rel))); !os.IsNotExist(err) {
			t.Fatalf("%s is still at the old root", rel)
		}
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}
