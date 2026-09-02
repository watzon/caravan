package store

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// The two primitives the portable safe-shutdown path is built on (SPEC §2.3):
// the WAL is folded into the database file before the handle closes, and the
// recovery flow can ask sqlite whether the file survived the eject.

// Checkpoint must actually empty the write-ahead log, not just succeed. An
// ejected drive whose recent writes are still in a -wal file is the failure
// this exists to prevent.
func TestCheckpointTruncatesTheWriteAheadLog(t *testing.T) {
	st, path := openTemp(t)
	ctx := context.Background()

	// Enough writes that the WAL is definitely non-empty.
	for i := range 200 {
		if err := st.InsertEvent(ctx, &core.Event{
			Category: "scan",
			Message:  strings.Repeat("x", 256),
			Detail:   string(rune('a' + i%26)),
		}); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	wal := path + "-wal"
	before, err := os.Stat(wal)
	if err != nil {
		t.Fatalf("stat %s: %v", wal, err)
	}
	if before.Size() == 0 {
		t.Fatalf("%s is empty before the checkpoint; the test proves nothing", wal)
	}

	if err := st.Checkpoint(); err != nil {
		t.Fatalf("Checkpoint: %v", err)
	}

	after, err := os.Stat(wal)
	if err != nil {
		t.Fatalf("stat %s after checkpoint: %v", wal, err)
	}
	if after.Size() != 0 {
		t.Fatalf("%s is %d bytes after the checkpoint, want 0", wal, after.Size())
	}

	// The data is still there. A checkpoint moves writes into the database
	// file, it does not discard them.
	events, err := st.ListEvents(ctx, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 200 {
		t.Fatalf("ListEvents returned %d events after the checkpoint, want 200", len(events))
	}
}

// A healthy database passes the check the dirty-eject recovery gates on.
func TestIntegrityCheckPassesOnAHealthyDatabase(t *testing.T) {
	st, _ := openTemp(t)
	if err := st.IntegrityCheck(context.Background()); err != nil {
		t.Fatalf("IntegrityCheck on a freshly migrated database: %v", err)
	}
}

// And it reports rather than swallows a database that can no longer be read.
func TestIntegrityCheckFailsOnAClosedDatabase(t *testing.T) {
	st, _ := openTemp(t)
	if err := st.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := st.IntegrityCheck(context.Background()); err == nil {
		t.Fatal("IntegrityCheck on a closed database returned no error")
	}
}
