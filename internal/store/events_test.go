package store

import (
	"context"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestListEventsPageUsesStrictIDBoundaries(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)
	for i := range 5 {
		if err := st.InsertEvent(ctx, &core.Event{Category: "test", Message: string(rune('a' + i))}); err != nil {
			t.Fatalf("InsertEvent: %v", err)
		}
	}

	first, next, err := st.ListEventsPage(ctx, 2, 0)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first) != 2 || next != first[1].ID {
		t.Fatalf("first page = %d rows, next %d, want 2 rows and cursor %d", len(first), next, first[1].ID)
	}
	second, final, err := st.ListEventsPage(ctx, 2, next)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second) != 2 || final != second[1].ID {
		t.Fatalf("second page = %d rows, final %d, want 2 rows and cursor %d", len(second), final, second[1].ID)
	}
	third, done, err := st.ListEventsPage(ctx, 2, final)
	if err != nil {
		t.Fatalf("final page: %v", err)
	}
	if len(third) != 1 || done != 0 {
		t.Fatalf("final page = %d rows, cursor %d, want one row and empty cursor", len(third), done)
	}
	if first[1].ID <= second[0].ID || second[1].ID <= third[0].ID {
		t.Fatalf("page boundary IDs are not strictly descending: %d, %d, %d", first[1].ID, second[0].ID, third[0].ID)
	}
}
