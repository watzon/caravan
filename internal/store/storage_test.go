package store

import (
	"context"
	"errors"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestStorageMigrationAllowsOnlyOneOpenAtATime(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	first := &core.StorageMigration{SourceRoot: "/old", TargetRoot: "/new"}
	if err := st.CreateStorageMigration(ctx, first); err != nil {
		t.Fatalf("CreateStorageMigration: %v", err)
	}

	// The guard is the schema's, not the handler's: two movers over the same
	// trees would each read the other's half-finished work as "already moved".
	second := &core.StorageMigration{SourceRoot: "/old", TargetRoot: "/other"}
	if err := st.CreateStorageMigration(ctx, second); !errors.Is(err, ErrStorageMigrationOpen) {
		t.Fatalf("second create = %v, want ErrStorageMigrationOpen", err)
	}

	// Running is still open.
	first.Status = core.StorageMigrationRunning
	if err := st.UpdateStorageMigration(ctx, first); err != nil {
		t.Fatalf("UpdateStorageMigration: %v", err)
	}
	if err := st.CreateStorageMigration(ctx, second); !errors.Is(err, ErrStorageMigrationOpen) {
		t.Fatalf("create while running = %v, want ErrStorageMigrationOpen", err)
	}
	if open, err := st.OpenStorageMigration(ctx); err != nil || open.ID != first.ID {
		t.Fatalf("OpenStorageMigration = %v, %v, want migration %d", open, err, first.ID)
	}

	// A terminal status releases the slot, so a rolled-back move can be retried.
	first.Status = core.StorageMigrationRolledBack
	first.Error = "target went away"
	if err := st.UpdateStorageMigration(ctx, first); err != nil {
		t.Fatalf("UpdateStorageMigration: %v", err)
	}
	if _, err := st.OpenStorageMigration(ctx); !errors.Is(err, ErrNotFound) {
		t.Fatalf("OpenStorageMigration after rollback = %v, want ErrNotFound", err)
	}
	if err := st.CreateStorageMigration(ctx, second); err != nil {
		t.Fatalf("create after rollback: %v", err)
	}

	// The settings screen polls the newest row whatever its status, so a
	// finished move stays readable.
	latest, err := st.LatestStorageMigration(ctx)
	if err != nil {
		t.Fatalf("LatestStorageMigration: %v", err)
	}
	if latest.ID != second.ID {
		t.Fatalf("latest = %d, want %d", latest.ID, second.ID)
	}

	stored, err := st.GetStorageMigration(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetStorageMigration: %v", err)
	}
	if stored.Status != core.StorageMigrationRolledBack || stored.Error != "target went away" {
		t.Fatalf("stored = %+v, want the rolled-back status and its reason", stored)
	}
}

func TestLatestStorageMigrationIsNotFoundOnAFreshDatabase(t *testing.T) {
	st, _ := openTemp(t)
	if _, err := st.LatestStorageMigration(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("LatestStorageMigration = %v, want ErrNotFound", err)
	}
}
