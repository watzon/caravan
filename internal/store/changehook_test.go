package store

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// The API installs the change hook after the automation runner and the
// download engines have started writing through the same handle, so the
// install must not race the readers. Meaningful under -race, where the old
// plain field write was reported by CI on TestPortableDirtyEjectDetectionAndRecovery.
func TestSetChangeHookIsSafeWhileWritersNote(t *testing.T) {
	st, _ := openTemp(t)
	ctx := context.Background()

	var fired atomic.Int32
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			err := st.EnqueueJob(ctx, &core.Job{Kind: core.JobRSSSync, Payload: "{}"})
			if err != nil {
				t.Errorf("EnqueueJob: %v", err)
				return
			}
		}
	}()

	for range 200 {
		st.SetChangeHook(func(string) { fired.Add(1) })
		st.SetChangeHook(nil)
	}
	<-done

	// Every enqueue that observed a hook must have reached it; every one that
	// observed nil must have been skipped. Either way the count is bounded by
	// the writes, which is what a torn read of the hook could not promise.
	if n := fired.Load(); n > 200 {
		t.Fatalf("hook fired %d times for 200 writes", n)
	}
}
