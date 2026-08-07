package stash

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/stash/stashtest"
	"github.com/watzon/caravan/internal/store"
)

// storageRoot is the absolute root every fixture hangs off. It is a fixed
// POSIX-shaped string rather than a temp dir because what is being asserted is
// the path Caravan *sends*, and a path that changes per run cannot be asserted
// against literally.
var storageRoot = filepath.Join(string(filepath.Separator), "srv", "media")

// wantAdultRoot is the one path a scan may ever name.
var wantAdultRoot = filepath.Join(storageRoot, filepath.FromSlash(store.AdultLibraryRoot))

// newTestService builds a service over a real sqlite store with both delays
// collapsed, so a queued job is claimable immediately instead of a minute from
// now.
func newTestService(t *testing.T, hc *http.Client) (*Service, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	if err := st.SetSetting(context.Background(), store.SettingStorageRoot, storageRoot); err != nil {
		t.Fatalf("set storage root: %v", err)
	}

	svc := NewService(st, hc, discardLogger())
	svc.window = 0
	svc.identifyDelay = 0
	// The re-arm curve is collapsed so a successor job is claimable at once; the
	// ceiling stays generous, and the tests that want the give-up path shorten it
	// themselves.
	//
	// Negative rather than zero, and deliberately a whole minute of it: the
	// queue compares run_after as a string (store.timeFormat is RFC3339Nano,
	// whose fractional part is variable-length), so two stamps inside the same
	// second can order wrongly. Production never lands there — every real delay
	// is tens of seconds — but a zero-delay successor claimed microseconds later
	// does, and a flake here would look like the handoff failing to re-arm.
	svc.retryDelay = func(int) time.Duration { return -time.Minute }
	svc.window = -time.Minute
	svc.identifyDelay = 0
	return svc, st
}

// configure writes the Stash card and the adult module switch, which are the
// two independent conditions every handoff path checks.
func configure(t *testing.T, st *store.Store, url, key string, enabled, adult bool) {
	t.Helper()
	ctx := context.Background()
	if err := st.SetSettings(ctx, map[string]string{
		store.SettingStashURL:     url,
		store.SettingStashAPIKey:  key,
		store.SettingStashEnabled: strconv.FormatBool(enabled),
	}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	if err := st.SetAdultEnabled(ctx, adult); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
}

// runOneJob claims the next eligible job of one kind and runs it through the
// handler under the automation runner's contract — complete on nil, fail on an
// error — reporting whether there was anything to claim.
//
// It exists so the retry assertions go through the queue instead of around it.
// A handler called directly in a loop proves the handler returns what the loop
// expects; only a claim proves the job the handoff left behind was still there
// to be claimed.
func runOneJob(t *testing.T, st *store.Store, kind string,
	handler func(context.Context, *store.Store, json.RawMessage) error,
) bool {
	t.Helper()
	ctx := context.Background()
	job, err := st.ClaimJob(ctx, []string{kind}, time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob: %v", err)
	}
	if job == nil {
		return false
	}
	if err := handler(ctx, st, json.RawMessage(job.Payload)); err != nil {
		if failErr := st.FailJob(ctx, job.ID, err.Error()); failErr != nil {
			t.Fatalf("FailJob: %v", failErr)
		}
		return true
	}
	if err := st.CompleteJob(ctx, job.ID); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	return true
}

// pendingOnly is the subset of jobs still waiting to run.
func pendingOnly(jobs []core.Job) []core.Job {
	out := []core.Job{}
	for _, j := range jobs {
		if j.State == core.JobStatePending {
			out = append(out, j)
		}
	}
	return out
}

func jobsOfKind(t *testing.T, st *store.Store, kind string) []core.Job {
	t.Helper()
	jobs, err := st.ListJobs(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	out := []core.Job{}
	for _, j := range jobs {
		if j.Kind == kind {
			out = append(out, j)
		}
	}
	return out
}

// seedScene inserts one adult site with one scene and one imported file, which
// is the state an import leaves behind and the identity push reads. The site is
// pinned to the legacy instance, which is what a single-box install and every
// row written before 0026 carry; seedSceneOn pins it elsewhere.
func seedScene(t *testing.T, st *store.Store) core.Episode {
	t.Helper()
	return seedSceneOn(t, st, core.ProviderStashbox)
}

func seedSceneOn(t *testing.T, st *store.Store, providerID string) core.Episode {
	t.Helper()
	ctx := context.Background()

	series := &core.Series{
		Provider: providerID, ProviderRef: "site-1",
		StashID: "site-1", Title: "Brazzers", Kind: core.SeriesKindAdult, Monitored: true,
	}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	season := &core.Season{SeriesID: series.ID, Number: 2022, Monitored: true}
	if err := st.UpsertSeason(ctx, season); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	episode := &core.Episode{
		SeriesID: series.ID, SeasonNumber: 2022, EpisodeNumber: 1,
		StashID: "scene-a", Title: "Deep Impact",
		AirDate:   time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC),
		Monitored: true,
		Scene: &core.SceneInfo{
			Studio:     "Brazzers",
			Performers: []string{"Abella Danger", "Jane Doe"},
			URL:        "https://example.test/scene-a",
		},
	}
	if err := st.UpsertEpisode(ctx, episode); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	file := &core.MediaFile{
		Path: "library/Adult/Brazzers/Season 2022/Brazzers - 2022-03-14 - Deep Impact.mp4",
		Size: 1024,
	}
	if err := st.UpsertMediaFile(ctx, file); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	if err := st.LinkEpisodeFile(ctx, episode.ID, file.ID); err != nil {
		t.Fatalf("LinkEpisodeFile: %v", err)
	}
	return *episode
}

// seedInstance configures one stash-box endpoint. The endpoint is what a push
// carries beside every UUID, so a fixture with no instance is a fixture whose
// ids have no issuer — see TestIdentityPushOmitsIdsWhenTheInstanceIsGone.
func seedInstance(t *testing.T, st *store.Store, providerID, name, endpoint string) {
	t.Helper()
	in := &core.StashboxInstance{ProviderID: providerID, Name: name, Endpoint: endpoint}
	if err := st.UpsertStashboxInstance(context.Background(), in); err != nil {
		t.Fatalf("UpsertStashboxInstance(%s): %v", providerID, err)
	}
}

func TestConfigOnAFreshDatabaseIsDisabled(t *testing.T) {
	svc, _ := newTestService(t, nil)

	cfg, err := svc.Config(context.Background())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if cfg.Enabled || cfg.URL != "" || cfg.APIKey != "" {
		t.Fatalf("Config = %+v, want zero", cfg)
	}
	if cfg.Ready() {
		t.Fatal("an unconfigured handoff must not be Ready")
	}
}

// One scan and one push per scene, from one notification.
func TestAdultLibraryChangedQueuesOneScanAndOnePushPerScene(t *testing.T) {
	svc, st := newTestService(t, nil)
	configure(t, st, "http://stash.lan:9999", "secret", true, true)

	if err := svc.AdultLibraryChanged(context.Background(), []int64{7, 9}); err != nil {
		t.Fatalf("AdultLibraryChanged: %v", err)
	}

	scans := jobsOfKind(t, st, ScanJobKind)
	if len(scans) != 1 {
		t.Fatalf("scan jobs = %d, want 1", len(scans))
	}
	// A fresh scan carries no arguments and no retry bookkeeping, so its payload
	// is the empty object — the shape the activity feed renders and the shape a
	// re-armed successor is distinguishable from.
	if scans[0].Payload != "{}" {
		t.Errorf("scan payload = %q, want %q", scans[0].Payload, "{}")
	}

	pushes := jobsOfKind(t, st, IdentifyJobKind)
	if len(pushes) != 2 {
		t.Fatalf("identify jobs = %d, want 2", len(pushes))
	}
	want := map[string]bool{`{"episode_id":7}`: true, `{"episode_id":9}`: true}
	for _, p := range pushes {
		if !want[p.Payload] {
			t.Errorf("identify payload = %q, want one of %v", p.Payload, want)
		}
	}
}

// The debounce: a burst of imports owes one scan, not one per notification, and
// one push per scene however many times that scene is announced.
func TestAdultLibraryChangedCoalescesABurst(t *testing.T) {
	svc, st := newTestService(t, nil)
	configure(t, st, "http://stash.lan:9999", "secret", true, true)

	for range 5 {
		if err := svc.AdultLibraryChanged(context.Background(), []int64{7}); err != nil {
			t.Fatalf("AdultLibraryChanged: %v", err)
		}
	}
	if got := len(jobsOfKind(t, st, ScanJobKind)); got != 1 {
		t.Errorf("scan jobs = %d, want 1 after a burst of 5", got)
	}
	if got := len(jobsOfKind(t, st, IdentifyJobKind)); got != 1 {
		t.Errorf("identify jobs = %d, want 1 after a burst of 5", got)
	}
}

// The module gate. With adult content off there is no adult library to hand
// over, so a stored Stash address is not a reason to queue anything — and, at
// the other end, not a reason to make a request.
func TestModuleOffQueuesNothingAndTalksToNobody(t *testing.T) {
	srv := stashtest.New(stashtest.Options{})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, false)
	episode := seedScene(t, st)

	ctx := context.Background()
	if err := svc.AdultLibraryChanged(ctx, []int64{episode.ID}); err != nil {
		t.Fatalf("AdultLibraryChanged: %v", err)
	}
	if got := len(jobsOfKind(t, st, ScanJobKind)) + len(jobsOfKind(t, st, IdentifyJobKind)); got != 0 {
		t.Fatalf("jobs = %d, want 0 while the adult module is off", got)
	}

	// And a job that somehow survived the module being switched off — queued
	// while it was on, run after it was not — must still make no request.
	if err := svc.HandleScan(ctx, st, nil); err != nil {
		t.Fatalf("HandleScan: %v", err)
	}
	if err := svc.HandleIdentify(ctx, st, identifyPayloadFor(t, episode.ID)); err != nil {
		t.Fatalf("HandleIdentify: %v", err)
	}
	if n := srv.Count(); n != 0 {
		t.Fatalf("requests to Stash = %d, want 0 while the adult module is off", n)
	}
}

func TestQueuesNothingWhenTheHandoffIsDisabled(t *testing.T) {
	svc, st := newTestService(t, nil)
	configure(t, st, "http://stash.lan:9999", "secret", false, true)

	if err := svc.AdultLibraryChanged(context.Background(), []int64{7}); err != nil {
		t.Fatalf("AdultLibraryChanged: %v", err)
	}
	if got := len(jobsOfKind(t, st, ScanJobKind)); got != 0 {
		t.Errorf("scan jobs = %d, want 0 while the handoff is off", got)
	}
}

// Enabled with a blank URL is a half-finished settings form. Queueing a job that
// can only ever fail would fill the activity feed with the user's typo.
func TestQueuesNothingWithoutAURL(t *testing.T) {
	svc, st := newTestService(t, nil)
	configure(t, st, "", "secret", true, true)

	if err := svc.AdultLibraryChanged(context.Background(), []int64{7}); err != nil {
		t.Fatalf("AdultLibraryChanged: %v", err)
	}
	if got := len(jobsOfKind(t, st, ScanJobKind)); got != 0 {
		t.Errorf("scan jobs = %d, want 0 without a URL", got)
	}
}

// The scan is scoped to the adult root and to nothing else. This is the
// exposure assertion: a scan that named the storage root would have Stash walk
// the television and film libraries.
func TestHandleScanIsScopedToTheAdultRoot(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"MetadataScan": {stashtest.Data(`{"metadataScan":"job-1"}`)},
		},
	})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, true)

	if err := svc.HandleScan(context.Background(), st, nil); err != nil {
		t.Fatalf("HandleScan: %v", err)
	}

	reqs := srv.Operations("MetadataScan")
	if len(reqs) != 1 {
		t.Fatalf("MetadataScan requests = %d, want 1", len(reqs))
	}
	input, _ := reqs[0].Variables["input"].(map[string]any)
	if !reflect.DeepEqual(input["paths"], []any{wantAdultRoot}) {
		t.Errorf("scan paths = %v, want [%s]", input["paths"], wantAdultRoot)
	}

	// A successful handoff is a log line, not feed noise: the import that
	// caused it already wrote an entry.
	if events := listEvents(t, st); len(events) != 0 {
		t.Errorf("events = %+v, want none on success", events)
	}
	if svc.Health().Unreachable() {
		t.Errorf("health = %+v, want healthy after a successful scan", svc.Health())
	}
}

// Stash being down is a banner and a retry, never a failed import. The import
// already completed; what this proves is the other half — the work re-arms
// itself on the queue and the outage is visible.
func TestStashDownSurfacesHealthAndRetries(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		Fallback: &stashtest.Response{Status: http.StatusBadGateway, Body: []byte("<html>down</html>")},
	})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, true)

	// The run does not fail: it re-arms. Failing would spend one of the queue's
	// five attempts on an outage that routinely outlasts all five.
	if err := svc.HandleScan(context.Background(), st, nil); err != nil {
		t.Fatalf("HandleScan against a dead Stash = %v, want it re-armed rather than failed", err)
	}
	successor := jobsOfKind(t, st, ScanJobKind)
	if len(successor) != 1 {
		t.Fatalf("queued scans after an unreachable Stash = %d, want the successor", len(successor))
	}

	health := svc.Health()
	if !health.Unreachable() {
		t.Fatalf("health = %+v, want unreachable", health)
	}
	if health.Since.IsZero() {
		t.Error("health.Since is zero, want when the outage started")
	}

	events := listEvents(t, st)
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1", len(events))
	}
	if events[0].Level != core.EventLevelWarn || events[0].Category != EventCategory {
		t.Errorf("event = %+v, want a warn in the %q category", events[0], EventCategory)
	}

	// A second failure keeps the original start time — "unreachable since" must
	// mean since the outage began — and writes no second entry: one unplugged
	// cable is one piece of news however many times it is retried.
	first := health.Since
	if err := svc.HandleScan(context.Background(), st, json.RawMessage(successor[0].Payload)); err != nil {
		t.Fatalf("second HandleScan = %v, want it re-armed", err)
	}
	if got := svc.Health().Since; !got.Equal(first) {
		t.Errorf("health.Since moved from %v to %v across attempts", first, got)
	}
	if got := len(listEvents(t, st)); got != 1 {
		t.Errorf("events after a second attempt = %d, want the outage reported once", got)
	}

	// And it clears when Stash comes back, which is what makes the banner go
	// away without a restart.
	srv.SetOperation("MetadataScan", stashtest.Data(`{"metadataScan":"job-1"}`))
	if err := svc.HandleScan(context.Background(), st, nil); err != nil {
		t.Fatalf("HandleScan after recovery: %v", err)
	}
	if svc.Health().Unreachable() {
		t.Errorf("health = %+v, want healthy once Stash answers again", svc.Health())
	}
}

// Acceptance criterion 3's third clause, driven through the real queue rather
// than by hand: with Stash down for longer than the queue's own five-attempt
// budget, the scan still delivers when Stash comes back.
//
// The old shape of this proof called HandleScan in a loop in-process, which
// showed the handler returns an error and nothing about whether the *job*
// survived to make a later call. Here the store owns the job and the runner's
// contract owns the outcome, so a regression that put the handoff back on the
// queue's budget shows up as a job in JobStateFailed and no MetadataScan.
func TestAQueuedScanSurvivesAnOutageLongerThanTheQueueBudget(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		Fallback: &stashtest.Response{Status: http.StatusBadGateway, Body: []byte("<html>down</html>")},
	})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, true)
	ctx := context.Background()

	if err := svc.AdultLibraryChanged(ctx, nil); err != nil {
		t.Fatalf("AdultLibraryChanged: %v", err)
	}

	// More rounds than store.JobMaxAttempts: the point is that the work outlives
	// the budget that used to end it.
	rounds := store.JobMaxAttempts + 3
	for range rounds {
		if !runOneJob(t, st, ScanJobKind, svc.HandleScan) {
			t.Fatal("no scan job was claimable; the handoff stopped re-arming mid-outage")
		}
	}
	if got := len(srv.Operations("MetadataScan")); got != rounds {
		t.Fatalf("MetadataScan attempts during the outage = %d, want %d", got, rounds)
	}

	// Stash comes back. The next claim is the one that delivers.
	srv.SetOperation("MetadataScan", stashtest.Data(`{"metadataScan":"job-1"}`))
	if !runOneJob(t, st, ScanJobKind, svc.HandleScan) {
		t.Fatal("nothing left to claim once Stash returned; the queued scan was lost")
	}
	if got := len(srv.Operations("MetadataScan")); got != rounds+1 {
		t.Fatalf("MetadataScan requests = %d, want the delivery after recovery", got)
	}
	if svc.Health().Unreachable() {
		t.Errorf("health = %+v, want the banner gone once the scan landed", svc.Health())
	}
	// Delivered means done: nothing is left waiting to ask again.
	if got := jobsOfKind(t, st, ScanJobKind); len(pendingOnly(got)) != 0 {
		t.Errorf("pending scans after delivery = %+v, want none", pendingOnly(got))
	}
}

// The identity push's whole point: the scene arrives in Stash already
// identified. Title and stash-box id always; studio and performers when they
// can be resolved.
func TestIdentityPushPayload(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"FindSceneByPath":  {stashtest.Data(`{"findScenes":{"count":1,"scenes":[{"id":"42"}]}}`)},
			"FindStudioByName": {stashtest.Data(`{"findStudios":{"studios":[{"id":"9"}]}}`)},
			"FindPerformerByName": {
				stashtest.Data(`{"findPerformers":{"performers":[{"id":"3"}]}}`),
				stashtest.Data(`{"findPerformers":{"performers":[{"id":"4"}]}}`),
			},
			"SceneUpdate": {stashtest.Data(`{"sceneUpdate":{"id":"42"}}`)},
		},
	})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, true)
	seedInstance(t, st, core.ProviderStashbox, "ThePornDB", "https://tpdb.test/graphql")
	episode := seedScene(t, st)

	if err := svc.HandleIdentify(context.Background(), st, identifyPayloadFor(t, episode.ID)); err != nil {
		t.Fatalf("HandleIdentify: %v", err)
	}

	// The lookup addresses the file by its absolute path, which is the one
	// address Caravan and Stash share.
	lookups := srv.Operations("FindSceneByPath")
	if len(lookups) != 1 {
		t.Fatalf("FindSceneByPath requests = %d, want 1", len(lookups))
	}
	wantPath := filepath.Join(wantAdultRoot, "Brazzers", "Season 2022", "Brazzers - 2022-03-14 - Deep Impact.mp4")
	if got := lookups[0].Variables["path"]; got != wantPath {
		t.Errorf("lookup path = %v, want %s", got, wantPath)
	}

	updates := srv.Operations("SceneUpdate")
	if len(updates) != 1 {
		t.Fatalf("SceneUpdate requests = %d, want 1", len(updates))
	}
	input, _ := updates[0].Variables["input"].(map[string]any)
	want := map[string]any{
		"id":            "42",
		"title":         "Deep Impact",
		"studio_id":     "9",
		"performer_ids": []any{"3", "4"},
		"urls":          []any{"https://example.test/scene-a"},
		"date":          "2022-03-14",
		"stash_ids": []any{map[string]any{
			"endpoint": "https://tpdb.test/graphql",
			"stash_id": "scene-a",
		}},
	}
	if !reflect.DeepEqual(input, want) {
		got, _ := json.Marshal(input)
		t.Errorf("SceneUpdateInput = %s,\nwant %+v", got, want)
	}
}

// A site whose stash-box instance has been deleted is pushed WITHOUT stash ids
// — not with a guess at which box issued them.
//
// The file still arrives, titled and dated and with its performers, because
// those are facts Caravan owns whatever happened to the endpoint. What is
// withheld is the attribution: a StashIDInput naming the wrong box writes into
// the user's own Stash a claim that box never made, Stash's identify step then
// trusts it, and undoing it means finding every scene the guess touched. An
// absent id leaves the scene merely unidentified, which the next push repairs
// the moment the instance is added back.
func TestIdentityPushOmitsIdsWhenTheInstanceIsGone(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"FindSceneByPath":     {stashtest.Data(`{"findScenes":{"count":1,"scenes":[{"id":"42"}]}}`)},
			"FindStudioByName":    {stashtest.Data(`{"findStudios":{"studios":[{"id":"9"}]}}`)},
			"FindPerformerByName": {stashtest.Data(`{"findPerformers":{"performers":[{"id":"3"}]}}`)},
			"SceneUpdate":         {stashtest.Data(`{"sceneUpdate":{"id":"42"}}`)},
		},
	})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, true)
	episode := seedScene(t, st)

	if err := svc.HandleIdentify(context.Background(), st, identifyPayloadFor(t, episode.ID)); err != nil {
		t.Fatalf("HandleIdentify: %v", err)
	}
	updates := srv.Operations("SceneUpdate")
	if len(updates) != 1 {
		t.Fatalf("SceneUpdate requests = %d, want the file pushed anyway", len(updates))
	}
	input, _ := updates[0].Variables["input"].(map[string]any)
	if _, ok := input["stash_ids"]; ok {
		t.Errorf("SceneUpdateInput carried stash_ids %v with no instance to attribute them to",
			input["stash_ids"])
	}
	if input["title"] != "Deep Impact" {
		t.Errorf("title = %v, want the push to have happened regardless", input["title"])
	}
}

// A scene under a site pinned to the SECOND instance carries that instance's
// endpoint, not the first one's. The endpoint is half the identity, and the
// public boxes mint identical UUIDs — so the wrong one attributes the scene to
// a record on a box that never held it.
func TestIdentityPushCarriesThePinnedInstancesEndpoint(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"FindSceneByPath":     {stashtest.Data(`{"findScenes":{"count":1,"scenes":[{"id":"42"}]}}`)},
			"FindStudioByName":    {stashtest.Data(`{"findStudios":{"studios":[{"id":"9"}]}}`)},
			"FindPerformerByName": {stashtest.Data(`{"findPerformers":{"performers":[{"id":"3"}]}}`)},
			"SceneUpdate":         {stashtest.Data(`{"sceneUpdate":{"id":"42"}}`)},
		},
	})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, true)
	seedInstance(t, st, core.ProviderStashbox, "ThePornDB", "https://tpdb.test/graphql")
	seedInstance(t, st, core.ProviderStashbox+":stashdb", "StashDB", "https://stashdb.test/graphql")
	episode := seedSceneOn(t, st, core.ProviderStashbox+":stashdb")

	if err := svc.HandleIdentify(context.Background(), st, identifyPayloadFor(t, episode.ID)); err != nil {
		t.Fatalf("HandleIdentify: %v", err)
	}
	input, _ := srv.Operations("SceneUpdate")[0].Variables["input"].(map[string]any)
	want := []any{map[string]any{
		"endpoint": "https://stashdb.test/graphql",
		"stash_id": "scene-a",
	}}
	if !reflect.DeepEqual(input["stash_ids"], want) {
		t.Errorf("stash_ids = %v, want the pinned instance's endpoint", input["stash_ids"])
	}
}

// Retry-then-succeed: a scan that has not indexed the file yet answers with no
// match, the job is failed so the queue backs off, and the next attempt lands.
// Nothing about that is a health problem — Stash answered.
func TestIdentityPushRetriesUntilTheScanCatchesUp(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"FindSceneByPath": {
				stashtest.Data(`{"findScenes":{"count":0,"scenes":[]}}`),
				stashtest.Data(`{"findScenes":{"count":0,"scenes":[]}}`),
				stashtest.Data(`{"findScenes":{"count":1,"scenes":[{"id":"42"}]}}`),
			},
			"FindStudioByName":    {stashtest.Data(`{"findStudios":{"studios":[{"id":"9"}]}}`)},
			"FindPerformerByName": {stashtest.Data(`{"findPerformers":{"performers":[{"id":"3"}]}}`)},
			"SceneUpdate":         {stashtest.Data(`{"sceneUpdate":{"id":"42"}}`)},
		},
	})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, true)
	episode := seedScene(t, st)
	ctx := context.Background()

	if err := svc.AdultLibraryChanged(ctx, []int64{episode.ID}); err != nil {
		t.Fatalf("AdultLibraryChanged: %v", err)
	}

	// Two claims find nothing and re-arm; the third finds the scene the scan has
	// now indexed. Driven through the queue, so what is proved is that the job
	// is still there to be claimed each time.
	for attempt := 1; attempt <= 2; attempt++ {
		if !runOneJob(t, st, IdentifyJobKind, svc.HandleIdentify) {
			t.Fatalf("attempt %d had nothing to claim; the push stopped re-arming", attempt)
		}
		if svc.Health().Unreachable() {
			t.Fatalf("attempt %d marked Stash unreachable; a scan still running is not an outage", attempt)
		}
		if events := listEvents(t, st); len(events) != 0 {
			t.Fatalf("attempt %d wrote %+v; a scan still running is not feed news", attempt, events)
		}
	}

	if !runOneJob(t, st, IdentifyJobKind, svc.HandleIdentify) {
		t.Fatal("nothing to claim on the attempt that should have found the scene")
	}
	if got := len(srv.Operations("SceneUpdate")); got != 1 {
		t.Fatalf("SceneUpdate requests = %d, want exactly 1 across three attempts", got)
	}
	if got := pendingOnly(jobsOfKind(t, st, IdentifyJobKind)); len(got) != 0 {
		t.Errorf("pending pushes after the identity landed = %+v, want none", got)
	}
}

// The retry is bounded, and by this package: the queue's five attempts are spent
// in nine minutes, which is shorter than a real metadataScan, so the handoff
// keeps its own wall clock and says so in the feed when it runs out.
//
// Every assertion here is about traffic the fake actually received and jobs the
// store actually held, driven claim-by-claim. An earlier version of this test
// looped over store.FailJob without ever invoking the handler, so it would have
// passed just as happily against a HandleIdentify that swallowed the
// not-indexed-yet answer and never asked Stash anything at all.
func TestIdentityPushRetryIsBoundedByItsOwnWindow(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"FindSceneByPath": {stashtest.Data(`{"findScenes":{"count":0,"scenes":[]}}`)},
		},
	})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	// A ceiling short enough to reach inside a test, reached by elapsed wall
	// clock exactly as the two-hour one is.
	svc.retryWindow = 40 * time.Millisecond
	configure(t, st, srv.URL(), "secret", true, true)
	episode := seedScene(t, st)
	ctx := context.Background()

	if err := svc.AdultLibraryChanged(ctx, []int64{episode.ID}); err != nil {
		t.Fatalf("AdultLibraryChanged: %v", err)
	}

	attempts := 0
	for runOneJob(t, st, IdentifyJobKind, svc.HandleIdentify) {
		attempts++
		if attempts > 1000 {
			t.Fatal("the identity push never gave up; the window is not bounding it")
		}
	}
	if attempts == 0 {
		t.Fatal("the queued push was never claimed")
	}
	// Every claim asked Stash, and asking stopped when the window closed.
	if got := len(srv.Operations("FindSceneByPath")); got != attempts {
		t.Errorf("FindSceneByPath requests = %d, want one per attempt (%d)", got, attempts)
	}
	if got := pendingOnly(jobsOfKind(t, st, IdentifyJobKind)); len(got) != 0 {
		t.Errorf("pending pushes after the window closed = %+v, want none", got)
	}

	// Giving up is news. The old shape of this failure was silent: the job
	// parked in JobStateFailed, the scene stayed untitled in Stash, and nothing
	// said so anywhere a user looks.
	events := listEvents(t, st)
	if len(events) != 1 {
		t.Fatalf("events = %+v, want exactly one 'gave up' warning", events)
	}
	if events[0].Level != core.EventLevelWarn || events[0].Category != EventCategory {
		t.Errorf("event = %+v, want a warn in the %q category", events[0], EventCategory)
	}
	// And it is not an outage: Stash answered every one of those lookups.
	if svc.Health().Unreachable() {
		t.Errorf("health = %+v, want healthy — Stash answered every attempt", svc.Health())
	}
}

// A push whose episode is gone, or whose file has been removed, is moot rather
// than an error: retrying it would never come out differently.
func TestIdentityPushIsANoOpForAMissingScene(t *testing.T) {
	srv := stashtest.New(stashtest.Options{})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, true)

	if err := svc.HandleIdentify(context.Background(), st, identifyPayloadFor(t, 999)); err != nil {
		t.Fatalf("HandleIdentify for an absent episode = %v, want nil", err)
	}
	if n := srv.Count(); n != 0 {
		t.Errorf("requests = %d, want 0 for an episode that no longer exists", n)
	}
}

// A push aimed at a television episode is a bug upstream. Carrying it out would
// tell Stash about the television library, which is the one thing this
// integration must never do.
func TestIdentityPushRefusesANonAdultEpisode(t *testing.T) {
	srv := stashtest.New(stashtest.Options{})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, true)

	ctx := context.Background()
	series := &core.Series{TMDBID: 1, Title: "Planet Earth II", Kind: core.SeriesKindTV, Monitored: true}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := st.UpsertSeason(ctx, &core.Season{SeriesID: series.ID, Number: 1}); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	episode := &core.Episode{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 1, Title: "Islands"}
	if err := st.UpsertEpisode(ctx, episode); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	file := &core.MediaFile{Path: "library/TV/Planet Earth II (2016)/Season 01/x.mkv", Size: 1}
	if err := st.UpsertMediaFile(ctx, file); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}
	if err := st.LinkEpisodeFile(ctx, episode.ID, file.ID); err != nil {
		t.Fatalf("LinkEpisodeFile: %v", err)
	}

	if err := svc.HandleIdentify(ctx, st, identifyPayloadFor(t, episode.ID)); err != nil {
		t.Fatalf("HandleIdentify for a television episode = %v, want nil", err)
	}
	if n := srv.Count(); n != 0 {
		t.Fatalf("requests = %d, want 0 — a television episode must never reach Stash", n)
	}
}

// Studio and performers are best effort. A Stash that cannot answer for them
// must still get the title and the stash-box id, which are the facts Caravan
// owns.
func TestIdentityPushSurvivesStudioAndPerformerFailures(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"FindSceneByPath":     {stashtest.Data(`{"findScenes":{"count":1,"scenes":[{"id":"42"}]}}`)},
			"FindStudioByName":    {stashtest.GraphQLError("studio subsystem is unhappy")},
			"FindPerformerByName": {stashtest.GraphQLError("performer subsystem is unhappy")},
			"SceneUpdate":         {stashtest.Data(`{"sceneUpdate":{"id":"42"}}`)},
		},
	})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, true)
	// The ids need an issuer to be pushed at all now (see
	// TestIdentityPushOmitsIdsWhenTheInstanceIsGone), so the fixture configures
	// one. What is under test here is still the best-effort rule.
	seedInstance(t, st, core.ProviderStashbox, "ThePornDB", "https://tpdb.test/graphql")
	episode := seedScene(t, st)

	if err := svc.HandleIdentify(context.Background(), st, identifyPayloadFor(t, episode.ID)); err != nil {
		t.Fatalf("HandleIdentify: %v", err)
	}
	input, _ := srv.Operations("SceneUpdate")[0].Variables["input"].(map[string]any)
	if input["title"] != "Deep Impact" {
		t.Errorf("title = %v, want the push to carry it regardless", input["title"])
	}
	if _, ok := input["stash_ids"]; !ok {
		t.Error("stash_ids missing; the identity must land even when the extras do not")
	}
	// Absent rather than empty: an empty studio_id would clear a studio the
	// user set by hand.
	if _, ok := input["studio_id"]; ok {
		t.Errorf("studio_id = %v, want it omitted when it could not be resolved", input["studio_id"])
	}
	if _, ok := input["performer_ids"]; ok {
		t.Errorf("performer_ids = %v, want it omitted when none resolved", input["performer_ids"])
	}
}

func identifyPayloadFor(t *testing.T, episodeID int64) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(identifyPayload{EpisodeID: episodeID})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	return raw
}

func listEvents(t *testing.T, st *store.Store) []core.Event {
	t.Helper()
	events, err := st.ListEvents(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	return events
}

// discardLogger keeps a test's expected warnings out of the test output.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// An alias and a canonical name are both credited on a scene and can resolve to
// the same Stash performer. Attaching that performer twice is what a naive
// per-name push does.
func TestIdentityPushDeduplicatesPerformersThatResolveToOneRow(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"FindSceneByPath":     {stashtest.Data(`{"findScenes":{"count":1,"scenes":[{"id":"42"}]}}`)},
			"FindStudioByName":    {stashtest.Data(`{"findStudios":{"studios":[{"id":"9"}]}}`)},
			"FindPerformerByName": {stashtest.Data(`{"findPerformers":{"performers":[{"id":"3"}]}}`)},
			"SceneUpdate":         {stashtest.Data(`{"sceneUpdate":{"id":"42"}}`)},
		},
	})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, true)
	episode := seedScene(t, st)

	if err := svc.HandleIdentify(context.Background(), st, identifyPayloadFor(t, episode.ID)); err != nil {
		t.Fatalf("HandleIdentify: %v", err)
	}
	input, _ := srv.Operations("SceneUpdate")[0].Variables["input"].(map[string]any)
	if !reflect.DeepEqual(input["performer_ids"], []any{"3"}) {
		t.Errorf("performer_ids = %v, want the one performer once", input["performer_ids"])
	}
}

// Two scenes at one path is an answer, not an outage. Stash's path filter is a
// string match, so a release name containing a SQLite LIKE wildcard can match
// twice — and the old code raised "Stash is unreachable" over a server that was
// up and answering, then asked the same unanswerable question five times.
func TestAnAmbiguousSceneIsAnAnswerNotAnOutage(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"FindSceneByPath": {stashtest.Data(
				`{"findScenes":{"count":2,"scenes":[{"id":"42"},{"id":"43"}]}}`)},
		},
	})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, true)
	episode := seedScene(t, st)
	ctx := context.Background()

	if err := svc.AdultLibraryChanged(ctx, []int64{episode.ID}); err != nil {
		t.Fatalf("AdultLibraryChanged: %v", err)
	}
	if !runOneJob(t, st, IdentifyJobKind, svc.HandleIdentify) {
		t.Fatal("the queued push was never claimed")
	}

	if svc.Health().Unreachable() {
		t.Errorf("health = %+v, want healthy — the server answered", svc.Health())
	}
	if got := len(srv.Operations("SceneUpdate")); got != 0 {
		t.Errorf("SceneUpdate requests = %d, want 0 — identity must not be pushed onto a guess", got)
	}
	// One entry, and no re-arm: the answer cannot change, so asking again is
	// only noise.
	if events := listEvents(t, st); len(events) != 1 {
		t.Fatalf("events = %+v, want exactly one", events)
	}
	if got := pendingOnly(jobsOfKind(t, st, IdentifyJobKind)); len(got) != 0 {
		t.Errorf("pending pushes = %+v, want none for a deterministic refusal", got)
	}
	if got := len(srv.Operations("FindSceneByPath")); got != 1 {
		t.Errorf("FindSceneByPath requests = %d, want 1", got)
	}
}

// A rejected API key is a server saying no, not a server that is gone. It gets
// a feed entry and no outage banner — a user sent looking for a network problem
// will not find the settings field that is actually wrong.
func TestARejectedCredentialIsNotAnOutage(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		Fallback: &stashtest.Response{
			Status: http.StatusUnauthorized,
			Body:   []byte(`{"errors":[{"message":"unauthorized"}]}`),
		},
	})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "wrong-key", true, true)

	if err := svc.HandleScan(context.Background(), st, nil); err != nil {
		t.Fatalf("HandleScan = %v, want the refusal recorded rather than returned", err)
	}
	if svc.Health().Unreachable() {
		t.Errorf("health = %+v, want healthy — a 401 is an answer", svc.Health())
	}
	if events := listEvents(t, st); len(events) != 1 {
		t.Fatalf("events = %+v, want the refusal reported once", events)
	}
	// And no re-arm: the key will not become right on its own, and the settings
	// screen re-queues when the user fixes it.
	if got := pendingOnly(jobsOfKind(t, st, ScanJobKind)); len(got) != 0 {
		t.Errorf("pending scans = %+v, want none after a refusal", got)
	}
}

// A round trip that ends in "no such scene" is proof the server is up, so it
// clears an outage the same as one that ends in a scene. Without this a banner
// raised while Stash was down survives every subsequent successful lookup that
// happens to find nothing.
func TestASceneNotFoundClearsAStaleOutage(t *testing.T) {
	srv := stashtest.New(stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"FindSceneByPath": {stashtest.Data(`{"findScenes":{"count":0,"scenes":[]}}`)},
		},
	})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, true)
	episode := seedScene(t, st)

	svc.markUnreachable("connection refused")
	if err := svc.HandleIdentify(context.Background(), st, identifyPayloadFor(t, episode.ID)); err != nil {
		t.Fatalf("HandleIdentify: %v", err)
	}
	if svc.Health().Unreachable() {
		t.Errorf("health = %+v, want cleared — Stash answered the lookup", svc.Health())
	}
}

// Switching the handoff off answers the banner. Health is remembered rather than
// probed, so a run that finds the handoff disabled is the moment the service
// learns the outage it is reporting is about a server nobody is asking for any
// more.
func TestAHandoffThatIsOffIsNotUnreachable(t *testing.T) {
	svc, st := newTestService(t, nil)
	configure(t, st, "http://stash.lan:9999", "secret", false, true)

	svc.markUnreachable("connection refused")
	if err := svc.HandleScan(context.Background(), st, nil); err != nil {
		t.Fatalf("HandleScan: %v", err)
	}
	if svc.Health().Unreachable() {
		t.Errorf("health = %+v, want cleared once the handoff is switched off", svc.Health())
	}

	// The module switch is the other half of the same answer.
	configure(t, st, "http://stash.lan:9999", "secret", true, false)
	svc.markUnreachable("connection refused")
	if err := svc.HandleIdentify(context.Background(), st, identifyPayloadFor(t, 7)); err != nil {
		t.Fatalf("HandleIdentify: %v", err)
	}
	if svc.Health().Unreachable() {
		t.Errorf("health = %+v, want cleared once the adult module is off", svc.Health())
	}
}

// A scan already in flight cannot cover a file that did not exist when it
// started. Coalescing against it would leave the scene in Caravan's library and
// permanently absent from Stash — the identity push finds nothing, and nothing
// else ever asks Stash to look again.
func TestARunningScanDoesNotSuppressTheNextOne(t *testing.T) {
	svc, st := newTestService(t, nil)
	configure(t, st, "http://stash.lan:9999", "secret", true, true)
	ctx := context.Background()

	if err := svc.AdultLibraryChanged(ctx, nil); err != nil {
		t.Fatalf("AdultLibraryChanged: %v", err)
	}
	// Claiming is what a worker does the instant before it POSTs the path list.
	job, err := st.ClaimJob(ctx, []string{ScanJobKind}, time.Minute)
	if err != nil || job == nil {
		t.Fatalf("ClaimJob = %v, %v; want the queued scan", job, err)
	}

	// An import completes inside that window.
	if err := svc.AdultLibraryChanged(ctx, nil); err != nil {
		t.Fatalf("second AdultLibraryChanged: %v", err)
	}
	if got := pendingOnly(jobsOfKind(t, st, ScanJobKind)); len(got) != 1 {
		t.Fatalf("pending scans = %d, want one queued behind the running scan", len(got))
	}

	// A scan that is merely *waiting* still coalesces: that is the debounce the
	// burst case depends on, and it must survive the fix above.
	if err := svc.AdultLibraryChanged(ctx, nil); err != nil {
		t.Fatalf("third AdultLibraryChanged: %v", err)
	}
	if got := pendingOnly(jobsOfKind(t, st, ScanJobKind)); len(got) != 1 {
		t.Errorf("pending scans = %d, want the burst still collapsed to one", len(got))
	}
}

// A re-armed push carries retry bookkeeping beside the episode id, so the
// dedupe has to key on the subject rather than on the payload string. Keying on
// the string would stack a second push behind every re-armed one.
func TestQueueingAPushDedupesOnTheSceneNotThePayload(t *testing.T) {
	svc, st := newTestService(t, nil)
	configure(t, st, "http://stash.lan:9999", "secret", true, true)
	ctx := context.Background()

	rearmed := identifyPayload{EpisodeID: 7}
	rearmed.retryState = rearmed.next(time.Now(), time.Hour)
	if err := svc.queueIdentify(ctx, rearmed, 0); err != nil {
		t.Fatalf("queueIdentify: %v", err)
	}
	if err := svc.AdultLibraryChanged(ctx, []int64{7}); err != nil {
		t.Fatalf("AdultLibraryChanged: %v", err)
	}
	if got := jobsOfKind(t, st, IdentifyJobKind); len(got) != 1 {
		t.Errorf("identify jobs = %d, want the re-armed push to cover the scene", len(got))
	}
}

// SPEC §12: the address is a credential when it carries userinfo, so nothing
// logs it verbatim.
func TestRedactURLKeepsUserinfoOutOfLogs(t *testing.T) {
	cases := map[string]string{
		"http://user:pass@stash.lan:9999": "http://stash.lan:9999",
		"https://stash.lan":               "https://stash.lan",
		"":                                "(redacted)",
		"::not a url":                     "(redacted)",
	}
	for in, want := range cases {
		if got := redactURL(in); got != want {
			t.Errorf("redactURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// The same gate, driven by the switch that survives the module setting's
// retirement: the adult LIBRARY is switched off while the setting is left on.
// The two agree today (store.SetAdultEnabled dual-writes the column), so this is
// the half that proves the handoff reads the rows rather than the setting.
func TestInactiveAdultLibraryQueuesNothingAndTalksToNobody(t *testing.T) {
	srv := stashtest.New(stashtest.Options{})
	t.Cleanup(srv.Close)
	svc, st := newTestService(t, srv.Client())
	configure(t, st, srv.URL(), "secret", true, true)
	episode := seedScene(t, st)

	ctx := context.Background()
	lib, err := st.GetLibraryByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		t.Fatalf("GetLibraryByKind: %v", err)
	}
	if err := st.SetLibraryActive(ctx, lib.ID, false); err != nil {
		t.Fatalf("SetLibraryActive: %v", err)
	}
	if on, err := st.AdultEnabled(ctx); err != nil || !on {
		t.Fatalf("adult_enabled = %t, %v — the setting was meant to stay on", on, err)
	}

	if err := svc.AdultLibraryChanged(ctx, []int64{episode.ID}); err != nil {
		t.Fatalf("AdultLibraryChanged: %v", err)
	}
	if got := len(jobsOfKind(t, st, ScanJobKind)) + len(jobsOfKind(t, st, IdentifyJobKind)); got != 0 {
		t.Fatalf("jobs = %d, want 0 while every adult library is off", got)
	}
	// And a job queued while the library was on, run after it was not, still
	// makes no request.
	if err := svc.HandleScan(ctx, st, nil); err != nil {
		t.Fatalf("HandleScan: %v", err)
	}
	if err := svc.HandleIdentify(ctx, st, identifyPayloadFor(t, episode.ID)); err != nil {
		t.Fatalf("HandleIdentify: %v", err)
	}
	if n := srv.Count(); n != 0 {
		t.Fatalf("requests to Stash = %d, want 0 while every adult library is off", n)
	}
}
