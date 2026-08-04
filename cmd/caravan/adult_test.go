package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/library"
	"github.com/watzon/caravan/internal/stashbox"
	"github.com/watzon/caravan/internal/stashbox/stashboxtest"
	"github.com/watzon/caravan/internal/store"
)

// noMetadata is a core.MetadataProvider that answers nothing. RefreshLibrary
// refuses to run at all without one, so the adult half of the sweep would never
// be reached on a first-run install — and "the adult sweep did nothing because
// the whole sweep did nothing" is not the property being tested.
type noMetadata struct{}

func (noMetadata) SearchMovies(context.Context, string) ([]core.MovieMeta, error) { return nil, nil }
func (noMetadata) SearchSeries(context.Context, string) ([]core.SeriesMeta, error) {
	return nil, nil
}
func (noMetadata) GetMovie(context.Context, int64) (*core.MovieMeta, error)   { return nil, nil }
func (noMetadata) GetSeries(context.Context, int64) (*core.SeriesMeta, error) { return nil, nil }

// seedSite puts an adult series and one scene in the store, the state an owner
// who used the module and then switched it off leaves behind.
func seedSite(t *testing.T, ctx context.Context, st *store.Store) {
	t.Helper()
	series := core.Series{
		StashID: "site-1", Title: "Brazzers", SortTitle: "brazzers",
		Kind: core.SeriesKindAdult, Monitored: true, Path: store.AdultLibraryRoot + "/Brazzers",
	}
	if err := st.UpsertSeries(ctx, &series); err != nil {
		t.Fatalf("upsert site: %v", err)
	}
	episode := core.Episode{
		SeriesID: series.ID, SeasonNumber: 2022, EpisodeNumber: 1, StashID: "scene-1",
		Title: "Deep Impact", AirDate: time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC),
		Monitored: true,
	}
	if err := st.UpsertEpisode(ctx, &episode); err != nil {
		t.Fatalf("upsert scene: %v", err)
	}
}

// The composition root's own guard: with the module off, no client for the
// endpoint is built at all. This is the outer of the two independent reasons
// the endpoint cannot be reached — library.adultReady is the inner one.
func TestAdultProviderIsNilUntilTheModuleIsOnAndCredentialed(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	fake := stashboxtest.New(stashboxtest.Options{})
	t.Cleanup(fake.Close)

	if err := st.SetSetting(ctx, store.SettingStashboxEndpoint, fake.URL()); err != nil {
		t.Fatalf("set endpoint: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingStashboxAPIKey, "secret"); err != nil {
		t.Fatalf("set api key: %v", err)
	}

	// Credential present, module off.
	if p := adapter.adultMetadata(ctx); p != nil {
		t.Errorf("adultMetadata = %v with the module off, want nil", p)
	}

	// Module on, credential removed.
	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingStashboxAPIKey, ""); err != nil {
		t.Fatalf("clear api key: %v", err)
	}
	if p := adapter.adultMetadata(ctx); p != nil {
		t.Errorf("adultMetadata = %v with no credential, want nil", p)
	}

	// Both.
	if err := st.SetSetting(ctx, store.SettingStashboxAPIKey, "secret"); err != nil {
		t.Fatalf("set api key: %v", err)
	}
	if p := adapter.adultMetadata(ctx); p == nil {
		t.Error("adultMetadata = nil with the module on and a credential set, want a provider")
	}

	// Nothing above made a request: building a client is not using one.
	if n := fake.Count(); n != 0 {
		t.Errorf("resolving the provider made %d stash-box requests, want 0", n)
	}
}

// PLAN phase 9 acceptance: "the fake stash-box server logs zero requests across
// a full job cycle" with the module disabled.
//
// The Manager here is built with a REAL stashbox.Client pointed at the fake, so
// the test is not proving that a nil provider makes no calls — it is proving
// that the library's own gate stops a fully wired, reachable endpoint from ever
// being asked. That is the guard that survives somebody making adultMetadata
// unconditional.
func TestFullJobCycleMakesNoStashboxRequestWhenAdultIsDisabled(t *testing.T) {
	ctx := context.Background()
	_, st := testAdapter(t)
	root := t.TempDir()

	fake := stashboxtest.New(stashboxtest.Options{})
	t.Cleanup(fake.Close)

	// Enable, seed the library the way a user of the module would leave it,
	// then switch the module back off. Disabling deletes nothing, so the rows
	// the sweeps walk are all still there.
	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	seedSite(t, ctx, st)
	if err := st.SetAdultEnabled(ctx, false); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	fake.Reset()

	client := stashbox.New("secret", fake.URL(), fake.Client())
	mgr := library.NewManager(st, noMetadata{}, root,
		library.WithAdultProvider(client))

	// The recurring metadata refresh — the sweep that would otherwise walk
	// every site's catalogue on a schedule.
	if _, err := mgr.RefreshLibrary(ctx); err != nil {
		t.Fatalf("RefreshLibrary: %v", err)
	}
	// A library scan, which is the other periodic thing that can reach a
	// provider.
	if err := os.MkdirAll(filepath.Join(root, "library", "Adult", "Brazzers"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := mgr.Scan(ctx); err != nil {
		t.Fatalf("Scan: %v", err)
	}

	if n := fake.Count(); n != 0 {
		t.Fatalf("a full job cycle with adult disabled made %d stash-box requests:\n%+v", n, fake.Requests())
	}

	// Control: the fake IS reachable, and the client IS wired. Without this the
	// test above would pass just as well against a broken URL.
	if _, err := client.SearchSites(ctx, "anything"); err == nil {
		t.Log("fake answered the control request")
	}
	if n := fake.Count(); n != 1 {
		t.Fatalf("control request logged %d, want 1 — the fake is not actually reachable", n)
	}
}

// The long-lived watcher Manager carries no adult provider, so the one Manager
// that outlives a settings change cannot hold a stale client for the endpoint.
func TestWatcherManagerCarriesNoAdultProvider(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	fake := stashboxtest.New(stashboxtest.Options{})
	t.Cleanup(fake.Close)

	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingStashboxEndpoint, fake.URL()); err != nil {
		t.Fatalf("set endpoint: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingStashboxAPIKey, "secret"); err != nil {
		t.Fatalf("set api key: %v", err)
	}
	seedSite(t, ctx, st)

	mgr := adapter.watcherManager(t.TempDir())
	if _, err := mgr.Scan(ctx); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if n := fake.Count(); n != 0 {
		t.Fatalf("the watcher Manager made %d stash-box requests, want 0", n)
	}
}

// The exported seam the HTTP layer reads the provider through (api.Manager).
// It is a second door onto the same decision, so it gets its own assertion: a
// delegation that stopped delegating would leave the API able to search a
// module the owner turned off, while every test of the unexported half stayed
// green.
func TestAdultMetadataSeamIsNilWhileTheModuleIsOff(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	fake := stashboxtest.New(stashboxtest.Options{})
	t.Cleanup(fake.Close)

	if err := st.SetSetting(ctx, store.SettingStashboxEndpoint, fake.URL()); err != nil {
		t.Fatalf("set endpoint: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingStashboxAPIKey, "secret"); err != nil {
		t.Fatalf("set api key: %v", err)
	}

	if p := adapter.AdultMetadata(); p != nil {
		t.Errorf("AdultMetadata = %v with the module off, want nil", p)
	}
	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	if p := adapter.AdultMetadata(); p == nil {
		t.Error("AdultMetadata = nil with the module on and a credential set")
	}
	// Switching back off closes the door again without a restart, which is what
	// reading the settings table per call buys.
	if err := st.SetAdultEnabled(ctx, false); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	if p := adapter.AdultMetadata(); p != nil {
		t.Errorf("AdultMetadata = %v after the module was switched off, want nil", p)
	}
	if n := fake.Count(); n != 0 {
		t.Errorf("resolving the seam made %d stash-box requests, want 0", n)
	}
}

// AddSite goes through the manager the API holds, so a route that reached it
// with the module off would still be refused by the library layer. The HTTP
// gate is the first wall; this is the one behind it.
func TestAdapterAddSiteRefusesWhileTheModuleIsOff(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	fake := stashboxtest.New(stashboxtest.Options{})
	t.Cleanup(fake.Close)

	if err := st.SetSetting(ctx, store.SettingStashboxEndpoint, fake.URL()); err != nil {
		t.Fatalf("set endpoint: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingStashboxAPIKey, "secret"); err != nil {
		t.Fatalf("set api key: %v", err)
	}

	if _, err := adapter.AddSite(ctx, "site-1", nil); err == nil {
		t.Fatal("AddSite succeeded with the module off")
	}
	if n := fake.Count(); n != 0 {
		t.Errorf("a refused AddSite made %d stash-box requests, want 0", n)
	}
}

// queryStudiosAttempts counts the requests that asked for queryStudios. That is
// the query TPDB does not implement, so on a TPDB-shaped endpoint every one of
// these is a request that can only fail.
func queryStudiosAttempts(s *stashboxtest.Server) int {
	n := 0
	for _, r := range s.Requests() {
		if strings.Contains(r.Query, "queryStudios") {
			n++
		}
	}
	return n
}

// tpdbFake is a fake endpoint shaped like TPDB: no queryStudios, but the
// scene-derived search the client falls back to answers normally.
func tpdbFake(t *testing.T) *stashboxtest.Server {
	t.Helper()
	s := stashboxtest.New(stashboxtest.Options{
		WithoutQueryStudios: true,
		Operations: map[string][]stashboxtest.Response{
			"SearchSitesByScene": {stashboxtest.Data([]byte(`{"searchScene":[]}`))},
		},
	})
	t.Cleanup(s.Close)
	return s
}

// The client memoizes "this endpoint has no queryStudios" per instance, so the
// composition root has to hand out the same instance twice — otherwise the memo
// is thrown away between HTTP requests and a typeahead search box sends one
// doomed query per keystroke.
func TestAdultProviderIsReusedAcrossCallsSoTheProbeRunsOnce(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	fake := tpdbFake(t)

	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingStashboxEndpoint, fake.URL()); err != nil {
		t.Fatalf("set endpoint: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingStashboxAPIKey, "secret"); err != nil {
		t.Fatalf("set api key: %v", err)
	}

	// Two acquisitions, as two HTTP requests would make.
	for i := 0; i < 2; i++ {
		provider := adapter.adultMetadata(ctx)
		if provider == nil {
			t.Fatalf("adultMetadata = nil on call %d", i+1)
		}
		if _, err := provider.SearchSites(ctx, "brazzers"); err != nil {
			t.Fatalf("SearchSites on call %d: %v", i+1, err)
		}
	}
	if n := queryStudiosAttempts(fake); n != 1 {
		t.Errorf("queryStudios attempts = %d, want 1: the capability memo must survive between acquisitions", n)
	}

	// Both searches still ran — a memo that worked by not searching would be
	// worse than the bug.
	if n := fake.Count(); n != 3 {
		t.Errorf("requests = %d, want 3 (one probe + two scene-derived searches):\n%+v", n, fake.Requests())
	}
}

// The cache is keyed on the settings, not held forever: pasting a new key or
// pointing at a different endpoint has to build a fresh client, because what was
// true of the old endpoint is not evidence about the new one.
func TestAdultProviderIsRebuiltWhenTheSettingsChange(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	fake := tpdbFake(t)
	other := tpdbFake(t)

	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingStashboxEndpoint, fake.URL()); err != nil {
		t.Fatalf("set endpoint: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingStashboxAPIKey, "secret"); err != nil {
		t.Fatalf("set api key: %v", err)
	}

	search := func(what string) {
		t.Helper()
		provider := adapter.adultMetadata(ctx)
		if provider == nil {
			t.Fatalf("adultMetadata = nil %s", what)
		}
		if _, err := provider.SearchSites(ctx, "brazzers"); err != nil {
			t.Fatalf("SearchSites %s: %v", what, err)
		}
	}

	search("on the first endpoint")

	// A new credential: same endpoint, different client.
	if err := st.SetSetting(ctx, store.SettingStashboxAPIKey, "rotated"); err != nil {
		t.Fatalf("rotate api key: %v", err)
	}
	search("after the key was rotated")
	if n := queryStudiosAttempts(fake); n != 2 {
		t.Errorf("queryStudios attempts = %d after a key change, want 2: a new credential must get a fresh client", n)
	}

	// A new endpoint: a different box entirely, which may well have the query.
	if err := st.SetSetting(ctx, store.SettingStashboxEndpoint, other.URL()); err != nil {
		t.Fatalf("repoint endpoint: %v", err)
	}
	search("after the endpoint moved")
	if n := queryStudiosAttempts(other); n != 1 {
		t.Errorf("queryStudios attempts on the new endpoint = %d, want 1: it must be probed on its own terms", n)
	}
	if n := queryStudiosAttempts(fake); n != 2 {
		t.Errorf("the old endpoint saw %d probes, want 2: it must not be touched after the move", n)
	}
}

// adultMetadata is called from concurrent HTTP handlers, so the cache it now
// reads and replaces has to be safe under -race — and has to hand every caller
// the same client, since one client per goroutine would lose the memo exactly
// the way one client per call did.
func TestAdultProviderIsSafeForConcurrentCallers(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	fake := tpdbFake(t)

	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingStashboxEndpoint, fake.URL()); err != nil {
		t.Fatalf("set endpoint: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingStashboxAPIKey, "secret"); err != nil {
		t.Fatalf("set api key: %v", err)
	}

	const callers = 8
	got := make([]core.AdultMetadataProvider, callers)
	var wg sync.WaitGroup
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			got[i] = adapter.adultMetadata(ctx)
		}(i)
	}
	wg.Wait()

	for i, provider := range got {
		if provider == nil {
			t.Fatalf("adultMetadata = nil on caller %d", i)
		}
		if provider != got[0] {
			t.Errorf("caller %d got a different client; every caller must share one so the capability memo is shared too", i)
		}
	}
}

// A candidate credential is exactly what the client cache must not hold.
//
// The enable gate validates a pair BEFORE deciding whether to commit it, so
// routing that validation through the cache installed an unproven pair: a
// typo'd key in the enable modal evicted the working client of a module that
// was already on, and the next search paid the endpoint-dialect probe again —
// the per-search round trip the cache exists to prevent.
func TestValidatingACredentialDoesNotEvictTheWorkingClient(t *testing.T) {
	ctx := context.Background()
	adapter, st := testAdapter(t)
	// tpdbFake plus the blank-query operation, which is what a credential test
	// runs (stashbox.Client.Test searches for nothing).
	fake := stashboxtest.New(stashboxtest.Options{
		WithoutQueryStudios: true,
		Operations: map[string][]stashboxtest.Response{
			"SearchSitesByScene": {stashboxtest.Data([]byte(`{"searchScene":[]}`))},
			"RecentSitesByScene": {stashboxtest.Data([]byte(`{"queryScenes":{"scenes":[]}}`))},
		},
	})
	t.Cleanup(fake.Close)

	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingStashboxEndpoint, fake.URL()); err != nil {
		t.Fatalf("set endpoint: %v", err)
	}
	if err := st.SetSetting(ctx, store.SettingStashboxAPIKey, "secret"); err != nil {
		t.Fatalf("set api key: %v", err)
	}

	search := func(what string) {
		t.Helper()
		provider := adapter.adultMetadata(ctx)
		if provider == nil {
			t.Fatalf("adultMetadata = nil %s", what)
		}
		if _, err := provider.SearchSites(ctx, "brazzers"); err != nil {
			t.Fatalf("SearchSites %s: %v", what, err)
		}
	}

	// One search warms the cache and learns the endpoint's dialect.
	search("before the validation")
	if n := queryStudiosAttempts(fake); n != 1 {
		t.Fatalf("queryStudios attempts = %d after the first search, want 1", n)
	}

	// The enable modal, opened again on a live module and submitted with a
	// typo. Nothing is committed — the settings still name the working key.
	if err := adapter.ValidateAdultCredential(ctx, fake.URL(), "typo"); err != nil {
		t.Fatalf("ValidateAdultCredential: %v", err)
	}

	search("after the validation")

	// Two probes: the warmed client's, and the throwaway validation client's.
	// A third would mean the validation replaced the cached client and the
	// search had to rebuild — the memo thrown away by a credential that was
	// never stored.
	if n := queryStudiosAttempts(fake); n != 2 {
		t.Errorf("queryStudios attempts = %d, want 2: the validation must not evict the cached client", n)
	}
}
