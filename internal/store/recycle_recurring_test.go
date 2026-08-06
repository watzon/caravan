package store

import (
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestRecycleCleanupIsRecurringDaily(t *testing.T) {
	interval, ok := RecurringIntervalFor(core.JobRecycleCleanup)
	if !ok {
		t.Fatal("recycle cleanup is not recurring")
	}
	if interval.Key != SettingRecycleCleanupIntervalMinutes || interval.DefaultMinutes != DefaultRecycleCleanupIntervalMinutes {
		t.Fatalf("recycle interval = %+v", interval)
	}
	if interval.DefaultMinutes != 24*60 {
		t.Fatalf("recycle interval = %d, want daily", interval.DefaultMinutes)
	}
}
