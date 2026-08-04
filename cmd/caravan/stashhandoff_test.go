package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/automation"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/stash"
	"github.com/watzon/caravan/internal/stash/stashtest"
	"github.com/watzon/caravan/internal/store"
)

// PLAN phase 11 acceptance criterion 1, end to end: "an adult import fires
// exactly one scoped Stash scan; non-adult imports fire none."
//
// The two tests below are the only place the whole path is joined up. Everywhere
// else it is proved in halves that share no code — internal/library asserts a
// stub notifier is called once, internal/stash asserts the handler sends the
// right path when it is invoked by hand — and neither half notices if the two
// are wired to each other wrongly, or not at all. Here the composition root
// builds the Manager, a real import runs through it, a real automation.Runner
// drains the queue, and the assertion is on what a fake Stash actually received.

// stashHarness is one Caravan wired the way serve.go wires it, pointed at a fake
// Stash.
type stashHarness struct {
	t      *testing.T
	st     *store.Store
	root   string
	srv    *stashtest.Server
	svc    *stash.Service
	runner *automation.Runner
}

func newStashHarness(t *testing.T, adult bool) *stashHarness {
	t.Helper()
	ctx := context.Background()

	st, err := store.Open(filepath.Join(t.TempDir(), "caravan.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	root := t.TempDir()

	srv := stashtest.New(stashtest.Options{
		Operations: map[string][]stashtest.Response{
			"MetadataScan":        {stashtest.Data(`{"metadataScan":"job-1"}`)},
			"FindSceneByPath":     {stashtest.Data(`{"findScenes":{"count":1,"scenes":[{"id":"42"}]}}`)},
			"FindStudioByName":    {stashtest.Data(`{"findStudios":{"studios":[{"id":"9"}]}}`)},
			"FindPerformerByName": {stashtest.Data(`{"findPerformers":{"performers":[{"id":"3"}]}}`)},
			"SceneUpdate":         {stashtest.Data(`{"sceneUpdate":{"id":"42"}}`)},
		},
	})
	t.Cleanup(srv.Close)

	if err := st.SetSettings(ctx, map[string]string{
		store.SettingStorageRoot:  root,
		store.SettingStashURL:     srv.URL(),
		store.SettingStashAPIKey:  "secret",
		store.SettingStashEnabled: "true",
	}); err != nil {
		t.Fatalf("SetSettings: %v", err)
	}
	if err := st.SetAdultEnabled(ctx, adult); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	// A television import resolves its series through the metadata provider, so
	// the control half of this proof needs one that answers. The adult half does
	// not use it: scenes resolve out of the library the site walk already filled.
	redirectTMDB(t, startFakeTV(t))
	if err := st.SetSetting(ctx, store.SettingTMDBAPIKey, "test-key"); err != nil {
		t.Fatalf("set tmdb key: %v", err)
	}

	// The debounce is wound back rather than collapsed to zero: the queue orders
	// run_after as a string, so a job queued for "now" and claimed microseconds
	// later can compare wrongly. Production never lands there — the real window
	// is twenty seconds — and a minute in the past is unambiguously claimable.
	svc := stash.NewService(st, srv.Client(), discardLog(), stash.WithSchedule(-time.Minute, 0))

	return &stashHarness{
		t: t, st: st, root: root, srv: srv, svc: svc,
		runner: automation.NewRunner(st, nil, nil,
			automation.WithHandler(stash.ScanJobKind, svc.HandleScan),
			automation.WithHandler(stash.IdentifyJobKind, svc.HandleIdentify)),
	}
}

// manager builds the library.Manager through the composition root, which is the
// seam under test: newLibraryAdapter is where WithAdultNotifier is attached.
func (h *stashHarness) manager() interface {
	ImportDownload(context.Context, core.DownloadStatus, core.GrabInfo) error
} {
	h.t.Helper()
	adapter := newLibraryAdapter(h.st, h.root, discardLog(), nil, h.svc)
	mgr, err := adapter.current(context.Background())
	if err != nil {
		h.t.Fatalf("build manager: %v", err)
	}
	return mgr
}

// drain runs the queue to a standstill, which is what a Caravan does between an
// import and the moment Stash hears about it.
func (h *stashHarness) drain() {
	h.t.Helper()
	ctx := context.Background()
	for range 50 {
		worked, err := h.runner.ProcessOne(ctx)
		if err != nil {
			h.t.Fatalf("ProcessOne: %v", err)
		}
		if !worked {
			return
		}
	}
	h.t.Fatal("the job queue never went quiet")
}

// grab records a grab the way the acquisition path does, so ImportDownload has
// history to close out.
func (h *stashHarness) grab(info core.GrabInfo) core.GrabInfo {
	h.t.Helper()
	g := &core.Grab{GrabInfo: info}
	if err := h.st.InsertGrab(context.Background(), g); err != nil {
		h.t.Fatalf("InsertGrab: %v", err)
	}
	return g.GrabInfo
}

// requireImported fails unless the episode ended up with a file, so a test that
// counts requests is counting them after a real import rather than after one
// that parked everything.
func (h *stashHarness) requireImported(episodeID int64) {
	h.t.Helper()
	files, err := h.st.ListMediaFilesForEpisode(context.Background(), episodeID)
	if err != nil {
		h.t.Fatalf("ListMediaFilesForEpisode: %v", err)
	}
	if len(files) == 0 {
		h.t.Fatalf("episode %d has no file; the import did not land", episodeID)
	}
}

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// writeFile drops one file with its parents created.
func writeFile(t *testing.T, abs, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", abs, err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", abs, err)
	}
}

// seedAdultScene puts one site and one scene in the library, the state adding a
// site and walking its catalogue leaves behind.
func seedAdultScene(t *testing.T, st *store.Store) core.Episode {
	t.Helper()
	ctx := context.Background()

	series := &core.Series{
		StashID: "site-1", Title: "Brazzers", SortTitle: "brazzers",
		Kind: core.SeriesKindAdult, Monitored: true,
	}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := st.UpsertSeason(ctx, &core.Season{SeriesID: series.ID, Number: 2022, Monitored: true}); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	episode := &core.Episode{
		SeriesID: series.ID, SeasonNumber: 2022, EpisodeNumber: 1,
		StashID: "scene-a", Title: "Deep Impact",
		AirDate:   time.Date(2022, time.March, 14, 0, 0, 0, 0, time.UTC),
		Monitored: true,
		Scene:     &core.SceneInfo{Studio: "Brazzers", Performers: []string{"Jane Doe"}},
	}
	if err := st.UpsertEpisode(ctx, episode); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	return *episode
}

// tvTMDBID is the one series the fake below knows about.
const tvTMDBID = 1399

// startFakeTV serves the two endpoints a series import reads: the series detail
// and its one season. It is deliberately minimal — nothing here is asserted,
// it only has to let a television import actually succeed so the "no Stash
// traffic" claim is about a completed import rather than an aborted one.
func startFakeTV(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case fmt.Sprintf("/3/tv/%d", tvTMDBID):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"id": tvTMDBID, "name": "Planet Earth II", "first_air_date": "2016-11-06",
				"seasons": []map[string]any{{"season_number": 1}},
			})
		case fmt.Sprintf("/3/tv/%d/season/1", tvTMDBID):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"season_number": 1, "name": "Season 1",
				"episodes": []map[string]any{
					{"id": 1, "episode_number": 1, "season_number": 1,
						"name": "Islands", "air_date": "2016-11-06"},
				},
			})
		default:
			http.Error(w, "not found", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// seedTelevisionEpisode is the control's library: an ordinary series, imported
// through the same ImportDownload.
func seedTelevisionEpisode(t *testing.T, st *store.Store) core.Episode {
	t.Helper()
	ctx := context.Background()

	series := &core.Series{
		TMDBID: tvTMDBID, Title: "Planet Earth II", SortTitle: "planet earth ii",
		Kind: core.SeriesKindTV, Monitored: true,
	}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	if err := st.UpsertSeason(ctx, &core.Season{SeriesID: series.ID, Number: 1, Monitored: true}); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	episode := &core.Episode{
		SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 1,
		Title: "Islands", Monitored: true,
	}
	if err := st.UpsertEpisode(ctx, episode); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	return *episode
}

// One adult import, one scoped scan, and the scan names the adult root and
// nothing else. This is the assertion the exposure rule rests on: a scan that
// named the storage root would have Stash walk the television shelf.
func TestAdultImportFiresExactlyOneScopedStashScan(t *testing.T) {
	h := newStashHarness(t, true)
	episode := seedAdultScene(t, h.st)
	ctx := context.Background()

	const dir = "incomplete/Brazzers.22.03.14.Deep.Impact.XXX.1080p.MP4-KTR"
	writeFile(t, filepath.Join(h.root, filepath.FromSlash(dir),
		"Brazzers.22.03.14.Deep.Impact.XXX.1080p.MP4-KTR.mp4"), "scene payload")

	grab := h.grab(core.GrabInfo{SeriesID: episode.SeriesID, EpisodeIDs: []int64{episode.ID}})
	dl := core.DownloadStatus{ID: "scene-1", State: core.DownloadCompleted, SavePath: dir}
	if err := h.manager().ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	h.requireImported(episode.ID)

	// Nothing yet: the handoff records intent and the jobs do the talking, so an
	// import is never slowed down or failed by a media server.
	if n := h.srv.Count(); n != 0 {
		t.Fatalf("requests during the import = %d, want 0 — the import must not wait on Stash", n)
	}

	h.drain()

	scans := h.srv.Operations("MetadataScan")
	if len(scans) != 1 {
		t.Fatalf("MetadataScan requests = %d, want exactly 1", len(scans))
	}
	wantRoot := filepath.Join(h.root, filepath.FromSlash(store.AdultLibraryRoot))
	input, _ := scans[0].Variables["input"].(map[string]any)
	if !reflect.DeepEqual(input["paths"], []any{wantRoot}) {
		t.Errorf("scan paths = %v, want [%s]", input["paths"], wantRoot)
	}

	// And the identity push followed it, which is what makes the scene arrive
	// identified rather than as an untagged file (acceptance criterion 2).
	if got := len(h.srv.Operations("SceneUpdate")); got != 1 {
		t.Errorf("SceneUpdate requests = %d, want 1", got)
	}
}

// The control, and the exposure assertion stated the other way round: a
// television import runs through the same ImportDownload and the same drained
// queue, and the fake Stash sees nothing at all.
func TestTelevisionImportTalksToStashNotAtAll(t *testing.T) {
	h := newStashHarness(t, true)
	episode := seedTelevisionEpisode(t, h.st)
	ctx := context.Background()

	const dir = "incomplete/Planet.Earth.II.S01E01.1080p.BluRay.x264-GRP"
	writeFile(t, filepath.Join(h.root, filepath.FromSlash(dir),
		"Planet.Earth.II.S01E01.1080p.BluRay.x264-GRP.mkv"), "episode payload")

	grab := h.grab(core.GrabInfo{SeriesID: episode.SeriesID, EpisodeIDs: []int64{episode.ID}})
	dl := core.DownloadStatus{ID: "tv-1", State: core.DownloadCompleted, SavePath: dir}
	if err := h.manager().ImportDownload(ctx, dl, grab); err != nil {
		t.Fatalf("ImportDownload: %v", err)
	}
	// The file really landed: "no Stash traffic" from an import that parked
	// everything would prove nothing.
	h.requireImported(episode.ID)
	h.drain()

	if n := h.srv.Count(); n != 0 {
		t.Fatalf("a television import made %d requests to Stash:\n%+v", n, h.srv.Requests())
	}

	// Control: the wiring is live, so the zero above is a decision and not a
	// broken address. The same harness, given an adult import, does talk.
	adult := seedAdultScene(t, h.st)
	const sceneDir = "incomplete/Brazzers.22.03.14.Deep.Impact.XXX.1080p.MP4-KTR"
	writeFile(t, filepath.Join(h.root, filepath.FromSlash(sceneDir),
		"Brazzers.22.03.14.Deep.Impact.XXX.1080p.MP4-KTR.mp4"), "scene payload")
	sceneGrab := h.grab(core.GrabInfo{SeriesID: adult.SeriesID, EpisodeIDs: []int64{adult.ID}})
	if err := h.manager().ImportDownload(ctx,
		core.DownloadStatus{ID: "scene-1", State: core.DownloadCompleted, SavePath: sceneDir},
		sceneGrab); err != nil {
		t.Fatalf("ImportDownload (control): %v", err)
	}
	h.drain()
	if got := len(h.srv.Operations("MetadataScan")); got != 1 {
		t.Fatalf("control MetadataScan requests = %d, want 1 — the handoff is not actually wired", got)
	}
}

// The module switch is the outer gate: with adult content off there is no adult
// library to hand over, and a stored Stash address is not a reason to make a
// request.
func TestAdultImportTalksToStashNotAtAllWhileTheModuleIsOff(t *testing.T) {
	h := newStashHarness(t, false)
	episode := seedAdultScene(t, h.st)

	const dir = "incomplete/Brazzers.22.03.14.Deep.Impact.XXX.1080p.MP4-KTR"
	writeFile(t, filepath.Join(h.root, filepath.FromSlash(dir),
		"Brazzers.22.03.14.Deep.Impact.XXX.1080p.MP4-KTR.mp4"), "scene payload")

	grab := h.grab(core.GrabInfo{SeriesID: episode.SeriesID, EpisodeIDs: []int64{episode.ID}})
	dl := core.DownloadStatus{ID: "scene-1", State: core.DownloadCompleted, SavePath: dir}
	// The import itself may refuse an adult grab while the module is off; what
	// this test is about is the traffic either way.
	_ = h.manager().ImportDownload(context.Background(), dl, grab)
	h.drain()

	if n := h.srv.Count(); n != 0 {
		t.Fatalf("requests with the adult module off = %d, want 0:\n%+v", n, h.srv.Requests())
	}
}
