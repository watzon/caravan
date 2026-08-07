package main

import (
	"context"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// AniList needs no credential, so its entry in the registry has to resolve on a
// store where nothing has been configured at all. That is the whole point of
// the provider: a library chained to it works on a fresh install, and a test
// that seeded a setting first would not prove it.
func TestProviderRegistryResolvesAniListWithoutSettings(t *testing.T) {
	adapter, _ := testAdapter(t)
	reg := providerRegistry{adapter}

	if got := reg.Metadata(context.Background(), core.ProviderAniList); got == nil {
		t.Fatal("Metadata(anilist) = nil, want a client")
	}
}

// The client is built once and reused, because the rate limiter it carries is
// the process-wide budget AniList enforces. A second call that built a second
// client would hand the caller a fresh, empty budget.
func TestAniListClientIsReused(t *testing.T) {
	adapter, _ := testAdapter(t)

	first := adapter.anilistClient()
	if second := adapter.anilistClient(); second != first {
		t.Error("anilistClient built a second client; the rate limiter would be reset")
	}
}

// An id nobody compiled in must be a GENUINE untyped nil: callers test the
// interface value, and a typed nil pointer would pass that test and then be
// called.
func TestProviderRegistryUnknownIDIsUntypedNil(t *testing.T) {
	adapter, _ := testAdapter(t)
	reg := providerRegistry{adapter}

	if got := reg.Metadata(context.Background(), "nope"); got != nil {
		t.Errorf("Metadata(nope) = %#v, want an untyped nil", got)
	}
}
