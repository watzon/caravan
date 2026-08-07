package main

import (
	"context"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
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

// TVmaze needs no credential either, so its entry has to resolve on a store
// where nothing has been configured at all — a library chained to it works on a
// fresh install, and a test that seeded a setting first would not prove it.
func TestProviderRegistryResolvesTVmazeWithoutSettings(t *testing.T) {
	adapter, _ := testAdapter(t)
	reg := providerRegistry{adapter}

	if got := reg.Metadata(context.Background(), core.ProviderTVmaze); got == nil {
		t.Fatal("Metadata(tvmaze) = nil, want a client")
	}
}

// The client is built once and reused, because the throttle it carries is the
// process-wide budget TVmaze enforces. A second call that built a second client
// would hand the caller a fresh, empty budget.
func TestTVmazeClientIsReused(t *testing.T) {
	adapter, _ := testAdapter(t)

	first := adapter.tvmazeClient()
	if second := adapter.tvmazeClient(); second != first {
		t.Error("tvmazeClient built a second client; the throttle would be reset")
	}
}

// TheTVDB is the other way round: it cannot be resolved until a key exists, and
// the nil it answers with until then must be a GENUINE untyped nil — callers
// test the interface value, and a typed nil *thetvdb.Client would pass that test
// and then try to log in with no credential.
func TestProviderRegistryWithholdsTheTVDBWithoutAKey(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	reg := providerRegistry{adapter}

	if got := reg.Metadata(ctx, core.ProviderTheTVDB); got != nil {
		t.Fatalf("Metadata(thetvdb) with no key = %#v, want an untyped nil", got)
	}

	if err := st.SetSetting(ctx, store.SettingTheTVDBAPIKey, "licensed-key"); err != nil {
		t.Fatalf("set thetvdb key: %v", err)
	}
	if got := reg.Metadata(ctx, core.ProviderTheTVDB); got == nil {
		t.Fatal("Metadata(thetvdb) with a key = nil, want a client")
	}
}

// The client is reused while both settings are unchanged, and that cache is
// load-bearing: the client holds the bearer token it logged in for, so a fresh
// one per call would log in before every lookup.
//
// A PIN change still rebuilds. The PIN is half of what /login consumes, so a
// token obtained with the old pair says nothing about the new one.
func TestTheTVDBClientCachesOnKeyAndPIN(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	reg := providerRegistry{adapter}

	if err := st.SetSetting(ctx, store.SettingTheTVDBAPIKey, "supporter-key"); err != nil {
		t.Fatalf("set thetvdb key: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingTheTVDBPIN, "1234"); err != nil {
		t.Fatalf("set thetvdb pin: %v", err)
	}

	first := reg.Metadata(ctx, core.ProviderTheTVDB)
	if again := reg.Metadata(ctx, core.ProviderTheTVDB); again != first {
		t.Error("thetvdbClient built a second client for the same pair; the token would be thrown away")
	}

	if err := st.SetSetting(ctx, store.SettingTheTVDBPIN, "4321"); err != nil {
		t.Fatalf("set thetvdb pin: %v", err)
	}
	if after := reg.Metadata(ctx, core.ProviderTheTVDB); after == first {
		t.Error("a PIN edit reused the client built for the old pair")
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
