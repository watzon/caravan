package library

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// Multi-instance stash-box (PLAN Part 2 phase 5).
//
// One rule splits this file from adult_test.go: a REFRESH resolves the
// instance an item is pinned to, while IDENTIFICATION walks the library's
// chain. Every test here exists because both used to resolve through the
// chain head, which on a two-box install asks the wrong box — and a stash-box
// asked for another box's UUID answers with a different site rather than with
// "no".

// The second instance's id. The first keeps the bare `stashbox` forever (0026),
// so a two-box fixture is always "the legacy one plus a slugged one".
const boxB = core.ProviderStashbox + ":beta"

// twoBoxHarness is an adult library whose chain is [stashbox, stashbox:beta],
// with a stub behind each id.
//
// The legacy stub is the harness' own (adultHarness.adult), which is what
// adultByID falls back to and what adultReady checks — so the fixture has one
// stash-box client per id and no third, invisible one.
func twoBoxHarness(t *testing.T) (*adultHarness, *stubAdultProvider, *core.Library) {
	t.Helper()
	h := newAdultHarness(t, true)
	beta := &stubAdultProvider{scenes: map[string][]core.SceneMeta{}}
	h.mgr.providers = &fakeRegistry{adult: map[string]core.AdultMetadataProvider{
		core.ProviderStashbox: h.adult,
		boxB:                  beta,
	}}
	return h, beta, h.adultLibraryChained(core.ProviderStashbox, boxB)
}

// adultLibraryChained rewrites the default adult library's provider chain and
// returns the row. ensureAdultLibrary seeds it with the legacy instance alone;
// a second box is a configuration the owner makes.
func (a *adultHarness) adultLibraryChained(ids ...string) *core.Library {
	a.t.Helper()
	ctx := context.Background()
	lib, err := a.st.GetDefaultLibrary(ctx, core.LibraryKindAdult)
	if err != nil {
		a.t.Fatalf("GetDefaultLibrary(adult): %v", err)
	}
	lib.Provider, lib.Providers = ids[0], ids
	if err := a.st.UpdateLibrary(ctx, lib); err != nil {
		a.t.Fatalf("UpdateLibrary: %v", err)
	}
	return lib
}

// seedSiteRow writes a site row pinned to one instance, the way an add through
// that instance would have. It writes no scenes: what the tests below watch is
// which box a refresh talks to, not what it brings back.
func (a *adultHarness) seedSiteRow(providerID, stashID, title string) *core.Series {
	a.t.Helper()
	sr := &core.Series{
		Provider: providerID, ProviderRef: stashID, StashID: stashID,
		Title: title, SortTitle: sortTitle(title), Kind: core.SeriesKindAdult,
		Path: "library/Adult/" + title, Monitored: true,
	}
	if err := a.st.UpsertSeries(context.Background(), sr); err != nil {
		a.t.Fatalf("UpsertSeries: %v", err)
	}
	return sr
}

// THE bug this wave exists to fix. A site added through the second box carries
// that box's UUIDs; refreshing it against the library's chain HEAD asks the
// first box about an id it never minted, and the public boxes are forks of one
// another — so the likely answer is a different site, written straight over the
// row's title and catalogue.
func TestRefreshResolvesTheSitesOwnInstanceNotTheChainHead(t *testing.T) {
	h, beta, _ := twoBoxHarness(t)
	beta.sites = []core.SiteMeta{{StashID: "site-b", Name: "Beta Site"}}
	beta.scenes["site-b"] = []core.SceneMeta{
		{StashID: "scene-b1", SiteStashID: "site-b", SiteName: "Beta Site",
			Title: "Only On Beta", Date: date(2023, time.May, 4)},
	}
	sr := h.seedSiteRow(boxB, "site-b", "Beta Site")

	res := &RefreshResult{}
	if err := h.mgr.refreshSites(context.Background(), res); err != nil {
		t.Fatalf("refreshSites: %v", err)
	}
	if len(res.Errors) != 0 || res.Sites != 1 {
		t.Fatalf("result = %+v, want the one pinned site refreshed cleanly", res)
	}
	if beta.calls == 0 {
		t.Error("the pinned instance was never asked")
	}
	if h.adult.calls != 0 {
		t.Errorf("the chain head was asked %d times about a site pinned elsewhere, want 0", h.adult.calls)
	}
	// The catalogue came from the box that minted the ids, so the scene landed.
	if eps := h.episodes(sr.ID); len(eps) != 1 || eps[0].StashID != "scene-b1" {
		t.Errorf("episodes = %+v, want the pinned box's one scene", eps)
	}
	// And the row is still pinned where it was: a refresh identifies nothing.
	row, err := h.st.GetSeries(context.Background(), sr.ID)
	if err != nil {
		t.Fatalf("GetSeries: %v", err)
	}
	if row.Provider != boxB {
		t.Errorf("row provider = %q after refresh, want it left pinned to %q", row.Provider, boxB)
	}
}

// An instance the owner deleted is a refresh error naming the site and the
// missing id, and ZERO provider calls. Falling back would be the same
// wrong-box fetch the test above forbids, only with nothing on screen to say
// so.
func TestRefreshOfASiteWhoseInstanceIsGoneCallsNobody(t *testing.T) {
	h, beta, _ := twoBoxHarness(t)
	h.seedSiteRow(core.ProviderStashbox+":gone", "site-x", "Vanished Box")

	res := &RefreshResult{}
	if err := h.mgr.refreshSites(context.Background(), res); err != nil {
		t.Fatalf("refreshSites: %v", err)
	}
	if res.Sites != 0 {
		t.Errorf("refreshed %d sites, want 0", res.Sites)
	}
	if len(res.Errors) != 1 ||
		!strings.Contains(res.Errors[0], "Vanished Box") ||
		!strings.Contains(res.Errors[0], "stashbox:gone") {
		t.Errorf("errors = %v, want one naming the site and the missing instance", res.Errors)
	}
	if h.adult.calls != 0 || beta.calls != 0 {
		t.Errorf("provider calls = %d/%d, want 0 on both boxes", h.adult.calls, beta.calls)
	}
}

// Identification is the other half. Nothing is pinned yet — a filename names a
// site and no box — so every configured instance is asked in the owner's order,
// and the first one confident about the title wins AND is what the new row is
// pinned to.
func TestScanIdentifiesAlongTheChainAndPinsTheWinner(t *testing.T) {
	h, beta, _ := twoBoxHarness(t)
	// The head answers, but about a different publisher: a chain walk must go
	// on past "I do not know this one", exactly as the metadata chain does.
	h.adult.sites = []core.SiteMeta{{StashID: "site-a", Name: "Some Other Studio"}}
	beta.sites = []core.SiteMeta{{StashID: "site-b", Name: "Brazzers"}}
	beta.scenes["site-b"] = []core.SceneMeta{
		{StashID: "scene-b1", SiteStashID: "site-b", SiteName: "Brazzers",
			Title: "Deep Impact", Date: date(2022, time.March, 14)},
	}
	h.writeVideo("library/Adult/Brazzers.22.03.14.Abella.Danger.Deep.Impact.XXX.1080p.MP4-KTR.mkv", "scene payload")

	res := h.scan()
	if res.Added != 1 || res.Unmatched != 0 {
		t.Fatalf("scan result = %+v (errors %v), want the second box's match imported", res, res.Errors)
	}
	if h.adult.calls == 0 {
		t.Error("the chain head was never asked; identification must start at the head")
	}

	sites, err := h.st.ListSeriesByKind(context.Background(), core.SeriesKindAdult)
	if err != nil {
		t.Fatalf("ListSeriesByKind: %v", err)
	}
	if len(sites) != 1 {
		t.Fatalf("adult series = %+v, want exactly the matched site", sites)
	}
	if sites[0].Provider != boxB || sites[0].ProviderRef != "site-b" {
		t.Errorf("row identity = %q/%q, want the winning rung's %q/site-b",
			sites[0].Provider, sites[0].ProviderRef, boxB)
	}
}

// A chain with no configured instance parks the file as a missing provider
// rather than erroring: it is the same dead end a nil provider always was, and
// it is what a library chained to an instance the owner deleted looks like.
func TestScanParksASceneWhenTheChainHasNoConfiguredInstance(t *testing.T) {
	h := newAdultHarness(t, true)
	h.mgr.providers = &fakeRegistry{adult: map[string]core.AdultMetadataProvider{}}
	h.adultLibraryChained(core.ProviderStashbox + ":gone")
	h.writeVideo("library/Adult/Brazzers.22.03.14.Abella.Danger.Deep.Impact.XXX.1080p.MP4-KTR.mkv", "scene payload")

	res := h.scan()
	if res.Unmatched != 1 || res.Added != 0 {
		t.Fatalf("scan result = %+v, want the file parked", res)
	}
	if parked := h.unmatched(); len(parked) != 1 || parked[0].Reason != reasonNoProvider {
		t.Errorf("unmatched queue = %+v, want %q", parked, reasonNoProvider)
	}
	if h.adult.calls != 0 {
		t.Errorf("a library with no configured instance made %d provider calls, want 0", h.adult.calls)
	}
}

// The 0026 index rule, exercised through the library layer: the public boxes
// are forks of one another and mint identical UUIDs, so the same id on two
// instances is two sites — two catalogues, two sets of scenes — and must be two
// rows. Collapsing them would have each box's refresh overwrite the other's.
func TestTheSameUUIDOnTwoInstancesIsTwoSites(t *testing.T) {
	ctx := context.Background()
	h, beta, _ := twoBoxHarness(t)
	const shared = "11111111-2222-3333-4444-555555555555"
	h.adult.sites = []core.SiteMeta{{StashID: shared, Name: "Legacy Brazzers"}}
	beta.sites = []core.SiteMeta{{StashID: shared, Name: "Beta Brazzers"}}

	first, err := h.mgr.AddSite(ctx, core.ItemRef{Provider: core.ProviderStashbox, Ref: shared}, nil, 0)
	if err != nil {
		t.Fatalf("AddSite(legacy): %v", err)
	}
	second, err := h.mgr.AddSite(ctx, core.ItemRef{Provider: boxB, Ref: shared}, nil, 0)
	if err != nil {
		t.Fatalf("AddSite(beta): %v", err)
	}
	if first.ID == second.ID {
		t.Fatalf("both adds landed on row %d; one UUID collapsed two boxes' sites", first.ID)
	}
	for _, want := range []struct{ provider, title string }{
		{core.ProviderStashbox, "Legacy Brazzers"},
		{boxB, "Beta Brazzers"},
	} {
		sr, err := h.st.GetSeriesByProviderRef(ctx, want.provider, shared)
		if err != nil {
			t.Fatalf("GetSeriesByProviderRef(%s): %v", want.provider, err)
		}
		if sr.Title != want.title {
			t.Errorf("%s row title = %q, want %q — each box keeps its own answer",
				want.provider, sr.Title, want.title)
		}
	}
}

// A site pinned to an instance that is gone cannot have its catalogue walked
// either, and the refusal names the absent provider rather than reading as a
// missing credential nobody can find.
func TestSyncSiteScenesRefusesAGoneInstance(t *testing.T) {
	h, beta, _ := twoBoxHarness(t)
	sr := h.seedSiteRow(core.ProviderStashbox+":gone", "site-x", "Vanished Box")

	err := h.mgr.syncSiteScenes(context.Background(), sr)
	if !errors.Is(err, core.ErrNoAdultProvider) {
		t.Fatalf("syncSiteScenes error = %v, want ErrNoAdultProvider", err)
	}
	if !strings.Contains(err.Error(), "stashbox:gone") {
		t.Errorf("error = %v, want the missing instance named", err)
	}
	if h.adult.calls != 0 || beta.calls != 0 {
		t.Errorf("provider calls = %d/%d, want 0 on both boxes", h.adult.calls, beta.calls)
	}
}

// The zero-traffic promise is per SERVER, not per instance: with the module off
// nothing scheduled may reach ANY configured box. The switch is read before the
// instances are, which is what keeps a second endpoint from being a second way
// to leak the fact that the module was ever configured.
func TestRefreshMakesNoRequestOfEitherInstanceWhenDisabled(t *testing.T) {
	ctx := context.Background()
	h, beta, _ := twoBoxHarness(t)
	h.adult.sites = []core.SiteMeta{{StashID: "site-a", Name: "Legacy Site"}}
	beta.sites = []core.SiteMeta{{StashID: "site-b", Name: "Beta Site"}}
	h.seedSiteRow(core.ProviderStashbox, "site-a", "Legacy Site")
	h.seedSiteRow(boxB, "site-b", "Beta Site")

	if err := h.st.SetAdultEnabled(ctx, false); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	h.adult.calls, beta.calls = 0, 0

	res := &RefreshResult{}
	if err := h.mgr.refreshSites(ctx, res); err != nil {
		t.Fatalf("refreshSites: %v", err)
	}
	if h.adult.calls != 0 || beta.calls != 0 {
		t.Errorf("provider calls = %d/%d, want 0 on both boxes", h.adult.calls, beta.calls)
	}
	if res.Sites != 0 || len(res.Errors) != 0 {
		t.Errorf("result = %+v, want an untouched no-op", res)
	}
}
