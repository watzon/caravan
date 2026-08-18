package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

func TestIndexerCRUD(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	if _, err := st.GetIndexer(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetIndexer(absent) = %v, want ErrNotFound", err)
	}
	list, err := st.ListIndexers(ctx)
	if err != nil {
		t.Fatalf("ListIndexers: %v", err)
	}
	if len(list) != 0 {
		t.Errorf("ListIndexers on a fresh db = %v, want empty", list)
	}

	idx := core.IndexerConfig{
		Name:         "Nyaa",
		DefinitionID: "fixture",
		Settings:     map[string]string{"username": "member", "password": "secret"},
		URL:          "https://example.invalid/torznab",
		APIKey:       "test-api-key",
		Type:         core.IndexerTypeTorznab,
		Categories:   []int{2000, 5000},
		Priority:     20,
		Enabled:      true,
	}
	if err := st.UpsertIndexer(ctx, &idx); err != nil {
		t.Fatalf("UpsertIndexer: %v", err)
	}
	if idx.ID == 0 {
		t.Fatal("UpsertIndexer did not write back an ID")
	}

	got, err := st.GetIndexer(ctx, idx.ID)
	if err != nil {
		t.Fatalf("GetIndexer: %v", err)
	}
	if !reflect.DeepEqual(*got, idx) {
		t.Errorf("GetIndexer = %+v, want %+v", *got, idx)
	}

	// Update in place: same id, no second row.
	idx.Name = "Nyaa (renamed)"
	idx.Categories = []int{5070}
	idx.Enabled = false
	if err := st.UpsertIndexer(ctx, &idx); err != nil {
		t.Fatalf("UpsertIndexer update: %v", err)
	}
	list, err = st.ListIndexers(ctx)
	if err != nil {
		t.Fatalf("ListIndexers: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListIndexers after update = %d rows, want 1", len(list))
	}
	if !reflect.DeepEqual(list[0], idx) {
		t.Errorf("ListIndexers[0] = %+v, want %+v", list[0], idx)
	}

	// A disabled indexer keeps its config but is skipped by search fan-out.
	enabled, err := st.ListEnabledIndexers(ctx)
	if err != nil {
		t.Fatalf("ListEnabledIndexers: %v", err)
	}
	if len(enabled) != 0 {
		t.Errorf("ListEnabledIndexers with only a disabled indexer = %v, want empty", enabled)
	}

	if err := st.DeleteIndexer(ctx, idx.ID); err != nil {
		t.Fatalf("DeleteIndexer: %v", err)
	}
	if _, err := st.GetIndexer(ctx, idx.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetIndexer after delete = %v, want ErrNotFound", err)
	}
	// Deleting twice is not an error.
	if err := st.DeleteIndexer(ctx, idx.ID); err != nil {
		t.Errorf("DeleteIndexer twice: %v", err)
	}
}

func TestRecordIndexerHealthFlagsThenDisables(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)
	idx := core.IndexerConfig{Name: "dead", URL: "https://example.invalid", Type: core.IndexerTypeTorznab, Enabled: true}
	if err := st.UpsertIndexer(ctx, &idx); err != nil {
		t.Fatalf("UpsertIndexer: %v", err)
	}

	if err := st.RecordIndexerHealth(ctx, idx.ID, errors.New("dial timeout")); err != nil {
		t.Fatalf("RecordIndexerHealth: %v", err)
	}
	got, err := st.GetIndexer(ctx, idx.ID)
	if err != nil {
		t.Fatalf("GetIndexer: %v", err)
	}
	if got.HealthError == "" || !got.Enabled || got.Searchable() {
		t.Fatalf("after first failure = %+v, want flagged but still enabled", got)
	}
	if got.ConsecutiveFailures != 1 {
		t.Fatalf("consecutive = %d, want 1", got.ConsecutiveFailures)
	}

	for i := 2; i <= core.IndexerHealthDisableAfter; i++ {
		if err := st.RecordIndexerHealth(ctx, idx.ID, errors.New("dial timeout")); err != nil {
			t.Fatalf("RecordIndexerHealth #%d: %v", i, err)
		}
	}
	got, err = st.GetIndexer(ctx, idx.ID)
	if err != nil {
		t.Fatalf("GetIndexer after disable: %v", err)
	}
	if got.Enabled {
		t.Fatal("indexer still enabled after the disable threshold")
	}

	if err := st.RecordIndexerHealth(ctx, idx.ID, nil); err != nil {
		t.Fatalf("RecordIndexerHealth success: %v", err)
	}
	got, err = st.GetIndexer(ctx, idx.ID)
	if err != nil {
		t.Fatalf("GetIndexer after recover: %v", err)
	}
	if got.HealthError != "" || got.ConsecutiveFailures != 0 {
		t.Fatalf("after recover = %+v, want a clear streak", got)
	}
}

func TestRecordIndexerHealthRedactsStoredSecrets(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)
	apiSecret := strings.Repeat("api-key-", 2)
	idx := core.IndexerConfig{
		Name:     "secret fixture",
		URL:      "https://tracker.example/api?token=url-secret-marker",
		Settings: map[string]string{"password": "setting-secret-marker"},
		Type:     core.IndexerTypeTorznab,
		Enabled:  true,
	}
	idx.APIKey = apiSecret
	if err := st.UpsertIndexer(ctx, &idx); err != nil {
		t.Fatalf("UpsertIndexer: %v", err)
	}
	probeErr := errors.New("request " + idx.URL + " failed with " + idx.APIKey + " and " + idx.Settings["password"])
	if err := st.RecordIndexerHealth(ctx, idx.ID, probeErr); err != nil {
		t.Fatalf("RecordIndexerHealth: %v", err)
	}
	got, err := st.GetIndexer(ctx, idx.ID)
	if err != nil {
		t.Fatalf("GetIndexer: %v", err)
	}
	for _, secret := range []string{"url-secret-marker", apiSecret, "setting-secret-marker"} {
		if strings.Contains(got.HealthError, secret) {
			t.Fatalf("HealthError exposed %q: %q", secret, got.HealthError)
		}
	}
	if !strings.Contains(got.HealthError, "[REDACTED]") {
		t.Fatalf("HealthError = %q, want redaction marker", got.HealthError)
	}
}

func TestIndexerHealthRedactionHandlesOverlappingSecretsLongestFirst(t *testing.T) {
	indexer := core.IndexerConfig{
		APIKey:   "abc",
		Settings: map[string]string{"token": "abcdef"},
	}
	message := redactIndexerHealthError(indexer, errors.New("tracker returned abcdef"))
	if strings.Contains(message, "abc") || strings.Contains(message, "def") {
		t.Fatalf("overlapping health secret was only partially redacted: %q", message)
	}
}

func TestListIndexersUsesPriorityThenName(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)
	for _, idx := range []core.IndexerConfig{
		{Name: "Zulu", Type: core.IndexerTypeTorznab, Priority: 25, Enabled: true},
		{Name: "Alpha", Type: core.IndexerTypeTorznab, Priority: 25, Enabled: true},
		{Name: "First", Type: core.IndexerTypeNewznab, Priority: 5, Enabled: true},
	} {
		if err := st.UpsertIndexer(ctx, &idx); err != nil {
			t.Fatalf("UpsertIndexer(%s): %v", idx.Name, err)
		}
	}

	got, err := st.ListEnabledIndexers(ctx)
	if err != nil {
		t.Fatalf("ListEnabledIndexers: %v", err)
	}
	if len(got) != 3 || got[0].Name != "First" || got[1].Name != "Alpha" || got[2].Name != "Zulu" {
		t.Fatalf("indexer order = %+v, want First, Alpha, Zulu", got)
	}
}

func TestUpsertIndexerUnknownIDIsNotFound(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	idx := core.IndexerConfig{ID: 404, Name: "ghost", Type: core.IndexerTypeNewznab}
	if err := st.UpsertIndexer(ctx, &idx); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpsertIndexer with an unknown id = %v, want ErrNotFound", err)
	}
}

func testRelease() core.Release {
	return core.Release{
		IndexerID:   7,
		Indexer:     "Nyaa",
		Title:       "Big.Buck.Bunny.2008.1080p.BluRay.x264-GROUP",
		GUID:        "guid-1",
		DownloadURL: "https://example.invalid/d/1.torrent",
		InfoHash:    "0123456789abcdef0123456789abcdef01234567",
		Protocol:    core.ProtocolTorrent,
		Size:        4 << 30,
		Seeders:     42,
		Leechers:    3,
		PublishedAt: time.Date(2008, 5, 20, 12, 0, 0, 0, time.UTC),
		Parsed: core.ParsedRelease{
			Title:      "Big Buck Bunny",
			Year:       2008,
			Quality:    core.Quality1080p,
			Source:     core.SourceBluray,
			Codec:      "x264",
			Group:      "GROUP",
			Confidence: 0.9,
		},
	}
}

func TestReleaseSeenCache(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	if _, err := st.GetRelease(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRelease(absent) = %v, want ErrNotFound", err)
	}
	if _, err := st.GetReleaseByGUID(ctx, 7, "guid-1"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetReleaseByGUID(absent) = %v, want ErrNotFound", err)
	}

	r := testRelease()
	r.TorrentPayload = []byte("ephemeral torrent payload")
	r.Attributes = []core.ReleaseAttribute{
		{Name: "genre", Value: "drama"},
		{Name: "downloadvolumefactor", Value: "0"},
	}
	if err := st.UpsertRelease(ctx, &r); err != nil {
		t.Fatalf("UpsertRelease: %v", err)
	}
	if r.ID == 0 {
		t.Fatal("UpsertRelease did not write back an ID")
	}

	got, err := st.GetRelease(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRelease: %v", err)
	}
	want := r
	want.TorrentPayload = nil
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("GetRelease = %+v, want %+v", *got, want)
	}
	if len(got.TorrentPayload) != 0 {
		t.Fatalf("persisted transient torrent payload = %q", got.TorrentPayload)
	}

	// Seeing the same GUID again refreshes the row instead of duplicating it:
	// that is the whole point of the cache.
	second := testRelease()
	second.Seeders = 99
	if err := st.UpsertRelease(ctx, &second); err != nil {
		t.Fatalf("UpsertRelease again: %v", err)
	}
	if second.ID != r.ID {
		t.Errorf("re-seen release got id %d, want the existing %d", second.ID, r.ID)
	}
	byGUID, err := st.GetReleaseByGUID(ctx, r.IndexerID, r.GUID)
	if err != nil {
		t.Fatalf("GetReleaseByGUID: %v", err)
	}
	if byGUID.Seeders != 99 {
		t.Errorf("Seeders after re-upsert = %d, want 99", byGUID.Seeders)
	}

	// The same GUID from a different indexer is a different release.
	other := testRelease()
	other.IndexerID = 8
	if err := st.UpsertRelease(ctx, &other); err != nil {
		t.Fatalf("UpsertRelease on another indexer: %v", err)
	}
	if other.ID == r.ID {
		t.Errorf("release on indexer 8 collapsed onto indexer 7's row %d", r.ID)
	}
}

func TestReleaseSurvivesIndexerDeletion(t *testing.T) {
	// The indexer reference is soft (0001's schema note): deleting an indexer
	// must not erase where a cached result came from.
	ctx := context.Background()
	st, _ := openTemp(t)

	idx := core.IndexerConfig{Name: "Nyaa", Type: core.IndexerTypeTorznab, Enabled: true}
	if err := st.UpsertIndexer(ctx, &idx); err != nil {
		t.Fatalf("UpsertIndexer: %v", err)
	}
	r := testRelease()
	r.IndexerID = idx.ID
	if err := st.UpsertRelease(ctx, &r); err != nil {
		t.Fatalf("UpsertRelease: %v", err)
	}
	if err := st.DeleteIndexer(ctx, idx.ID); err != nil {
		t.Fatalf("DeleteIndexer: %v", err)
	}

	got, err := st.GetRelease(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRelease after indexer deletion: %v", err)
	}
	if got.Indexer != "Nyaa" {
		t.Errorf("Indexer = %q, want %q", got.Indexer, "Nyaa")
	}
}

func TestGrabHistory(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	if _, err := st.GetGrab(ctx, 1); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetGrab(absent) = %v, want ErrNotFound", err)
	}

	g := core.Grab{
		GrabInfo: core.GrabInfo{
			SeriesID:     3,
			SeasonNum:    1,
			EpisodeIDs:   []int64{11, 12},
			ReleaseTitle: "Show.S01E01E02.1080p",
		},
		ReleaseID: 5,
		Reason:    "manual pick",
	}
	if err := st.InsertGrab(ctx, &g); err != nil {
		t.Fatalf("InsertGrab: %v", err)
	}
	if g.GrabID == 0 {
		t.Fatal("InsertGrab did not write back a GrabID")
	}
	if g.Status != core.GrabStatusGrabbed {
		t.Errorf("Status = %q, want the default %q", g.Status, core.GrabStatusGrabbed)
	}

	got, err := st.GetGrab(ctx, g.GrabID)
	if err != nil {
		t.Fatalf("GetGrab: %v", err)
	}
	if !reflect.DeepEqual(got.GrabInfo, g.GrabInfo) {
		t.Errorf("GrabInfo = %+v, want %+v", got.GrabInfo, g.GrabInfo)
	}
	if got.ReleaseID != g.ReleaseID || got.Reason != g.Reason {
		t.Errorf("GetGrab = %+v, want release %d reason %q", *got, g.ReleaseID, g.Reason)
	}

	movieGrab := core.Grab{GrabInfo: core.GrabInfo{MovieID: 9, ReleaseTitle: "Movie.2008.1080p"}}
	if err := st.InsertGrab(ctx, &movieGrab); err != nil {
		t.Fatalf("InsertGrab movie: %v", err)
	}
	if movieGrab.EpisodeIDs != nil {
		t.Errorf("EpisodeIDs on a movie grab = %v, want nil", movieGrab.EpisodeIDs)
	}

	all, err := st.ListGrabs(ctx, 0)
	if err != nil {
		t.Fatalf("ListGrabs: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListGrabs = %d rows, want 2", len(all))
	}
	if all[0].GrabID != movieGrab.GrabID {
		t.Errorf("ListGrabs[0] = grab %d, want the newest %d", all[0].GrabID, movieGrab.GrabID)
	}
	limited, err := st.ListGrabs(ctx, 1)
	if err != nil {
		t.Fatalf("ListGrabs(1): %v", err)
	}
	if len(limited) != 1 {
		t.Errorf("ListGrabs(1) = %d rows, want 1", len(limited))
	}

	byRelease, err := st.ListGrabsForReleaseIDs(ctx, []int64{5})
	if err != nil {
		t.Fatalf("ListGrabsForReleaseIDs: %v", err)
	}
	if len(byRelease) != 1 || byRelease[0].GrabID != g.GrabID {
		t.Fatalf("ListGrabsForReleaseIDs(5) = %+v, want grab %d", byRelease, g.GrabID)
	}
	none, err := st.ListGrabsForReleaseIDs(ctx, nil)
	if err != nil || len(none) != 0 {
		t.Fatalf("ListGrabsForReleaseIDs(nil) = %v %v, want empty", none, err)
	}

	if err := st.SetGrabStatus(ctx, g.GrabID, core.GrabStatusImported, "imported"); err != nil {
		t.Fatalf("SetGrabStatus: %v", err)
	}
	got, err = st.GetGrab(ctx, g.GrabID)
	if err != nil {
		t.Fatalf("GetGrab after status change: %v", err)
	}
	if got.Status != core.GrabStatusImported {
		t.Errorf("Status = %q, want %q", got.Status, core.GrabStatusImported)
	}
	if err := st.SetGrabStatus(ctx, 404, core.GrabStatusFailed, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("SetGrabStatus(absent) = %v, want ErrNotFound", err)
	}
}

func TestDownloadCRUD(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	if _, err := st.GetDownloadByEngineID(ctx, "nope"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetDownloadByEngineID(absent) = %v, want ErrNotFound", err)
	}
	if err := st.UpsertDownload(ctx, &core.Download{Title: "no handle"}); err == nil {
		t.Error("UpsertDownload with an empty engine id = nil error, want error")
	}

	d := core.Download{
		GrabID:    4,
		Engine:    "embedded",
		EngineID:  "0123456789abcdef",
		Title:     "Big Buck Bunny 2008 1080p",
		State:     core.DownloadDownloading,
		Progress:  0.25,
		BytesDone: 1 << 20,
		Size:      4 << 20,
		SavePath:  "incomplete/Big Buck Bunny 2008 1080p",
	}
	if err := st.UpsertDownload(ctx, &d); err != nil {
		t.Fatalf("UpsertDownload: %v", err)
	}
	if d.ID == 0 {
		t.Fatal("UpsertDownload did not write back an ID")
	}

	got, err := st.GetDownloadByEngineID(ctx, d.EngineID)
	if err != nil {
		t.Fatalf("GetDownloadByEngineID: %v", err)
	}
	if got.ID != d.ID || got.GrabID != d.GrabID || got.State != d.State ||
		got.BytesDone != d.BytesDone || got.Size != d.Size || got.SavePath != d.SavePath {
		t.Errorf("GetDownloadByEngineID = %+v, want %+v", *got, d)
	}

	// Progress updates upsert onto the same row, keyed by the engine handle.
	update := core.Download{
		Engine:    "embedded",
		EngineID:  d.EngineID,
		GrabID:    d.GrabID,
		Title:     d.Title,
		State:     core.DownloadSeeding,
		Progress:  1,
		BytesDone: 4 << 20,
		Size:      4 << 20,
	}
	if err := st.UpsertDownload(ctx, &update); err != nil {
		t.Fatalf("UpsertDownload update: %v", err)
	}
	if update.ID != d.ID {
		t.Errorf("upsert by engine id created row %d, want the existing %d", update.ID, d.ID)
	}
	if !update.CreatedAt.Equal(d.CreatedAt) {
		t.Errorf("CreatedAt = %v, want the original %v", update.CreatedAt, d.CreatedAt)
	}

	list, err := st.ListDownloads(ctx)
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("ListDownloads = %d rows, want 1", len(list))
	}
	if list[0].State != core.DownloadSeeding {
		t.Errorf("State = %q, want %q", list[0].State, core.DownloadSeeding)
	}

	if err := st.DeleteDownloadByEngineID(ctx, d.EngineID); err != nil {
		t.Fatalf("DeleteDownloadByEngineID: %v", err)
	}
	if _, err := st.GetDownloadByEngineID(ctx, d.EngineID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetDownloadByEngineID after delete = %v, want ErrNotFound", err)
	}
	// Deleting twice is not an error.
	if err := st.DeleteDownloadByEngineID(ctx, d.EngineID); err != nil {
		t.Errorf("DeleteDownloadByEngineID twice: %v", err)
	}
}

func TestGetUnlinkedGrabbedByReleaseTitle(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	if _, err := st.GetUnlinkedGrabbedByReleaseTitle(ctx, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty title = %v, want ErrNotFound", err)
	}
	if _, err := st.GetUnlinkedGrabbedByReleaseTitle(ctx, "Missing.Release"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown title = %v, want ErrNotFound", err)
	}

	g := core.Grab{GrabInfo: core.GrabInfo{SeriesID: 10, ReleaseTitle: "Site.26.05.20.Scene.XXX.2160p"}}
	if err := st.InsertGrab(ctx, &g); err != nil {
		t.Fatalf("InsertGrab: %v", err)
	}
	got, err := st.GetUnlinkedGrabbedByReleaseTitle(ctx, "Site.26.05.20.Scene.XXX.2160p")
	if err != nil {
		t.Fatalf("unlinked grab: %v", err)
	}
	if got.GrabID != g.GrabID {
		t.Errorf("unlinked grab = %d, want %d", got.GrabID, g.GrabID)
	}

	if err := st.UpsertDownload(ctx, &core.Download{
		GrabID: g.GrabID, Engine: "embedded-usenet", EngineID: "u1", Title: g.ReleaseTitle,
	}); err != nil {
		t.Fatalf("UpsertDownload: %v", err)
	}
	if _, err := st.GetUnlinkedGrabbedByReleaseTitle(ctx, g.ReleaseTitle); !errors.Is(err, ErrNotFound) {
		t.Errorf("already-linked grab = %v, want ErrNotFound", err)
	}

	finished := core.Grab{
		GrabInfo: core.GrabInfo{SeriesID: 10, ReleaseTitle: "Site.26.05.20.Scene.XXX.1080p"},
		Status:   core.GrabStatusImported,
	}
	if err := st.InsertGrab(ctx, &finished); err != nil {
		t.Fatalf("InsertGrab finished: %v", err)
	}
	if _, err := st.GetUnlinkedGrabbedByReleaseTitle(ctx, finished.ReleaseTitle); !errors.Is(err, ErrNotFound) {
		t.Errorf("imported grab = %v, want ErrNotFound", err)
	}
}

func TestGetGrabByDownloadID(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	g := core.Grab{GrabInfo: core.GrabInfo{MovieID: 9, ReleaseTitle: "Movie.2008.1080p"}}
	if err := st.InsertGrab(ctx, &g); err != nil {
		t.Fatalf("InsertGrab: %v", err)
	}
	d := core.Download{GrabID: g.GrabID, Engine: "embedded", EngineID: "abc123", Title: "Movie"}
	if err := st.UpsertDownload(ctx, &d); err != nil {
		t.Fatalf("UpsertDownload: %v", err)
	}

	got, err := st.GetGrabByDownloadID(ctx, d.EngineID)
	if err != nil {
		t.Fatalf("GetGrabByDownloadID: %v", err)
	}
	if got.GrabID != g.GrabID || got.MovieID != 9 {
		t.Errorf("GetGrabByDownloadID = %+v, want grab %d for movie 9", *got, g.GrabID)
	}

	if _, err := st.GetGrabByDownloadID(ctx, "unknown"); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetGrabByDownloadID(absent download) = %v, want ErrNotFound", err)
	}

	// A download with no grab behind it must report ErrNotFound, not the
	// wrong grab.
	orphan := core.Download{Engine: "embedded", EngineID: "orphan", Title: "manual add"}
	if err := st.UpsertDownload(ctx, &orphan); err != nil {
		t.Fatalf("UpsertDownload orphan: %v", err)
	}
	if _, err := st.GetGrabByDownloadID(ctx, orphan.EngineID); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetGrabByDownloadID(grabless download) = %v, want ErrNotFound", err)
	}
}

// The download engine does not know about grabs: core.AddOpts carries no grab
// id, so every record the engine persists after the grab handler linked the row
// has GrabID zero. If that zero overwrote the link, GetGrabByDownloadID would
// stop finding the grab and the watcher would never import the finished
// download.
func TestUpsertDownloadKeepsGrabLinkWhenEngineReportsNone(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	g := core.Grab{GrabInfo: core.GrabInfo{MovieID: 9, ReleaseTitle: "Movie.2008.1080p"}}
	if err := st.InsertGrab(ctx, &g); err != nil {
		t.Fatalf("InsertGrab: %v", err)
	}
	// The grab handler links the download to the grab.
	linked := core.Download{GrabID: g.GrabID, Engine: "embedded", EngineID: "abc123", Title: "Movie"}
	if err := st.UpsertDownload(ctx, &linked); err != nil {
		t.Fatalf("UpsertDownload linked: %v", err)
	}

	// The engine's poller then reports progress, knowing nothing of grabs.
	progress := core.Download{Engine: "embedded", EngineID: "abc123", Title: "Movie",
		State: core.DownloadDownloading, Progress: 0.5, BytesDone: 512, Size: 1024}
	if err := st.UpsertDownload(ctx, &progress); err != nil {
		t.Fatalf("UpsertDownload progress: %v", err)
	}

	got, err := st.GetGrabByDownloadID(ctx, "abc123")
	if err != nil {
		t.Fatalf("GetGrabByDownloadID after engine save: %v", err)
	}
	if got.GrabID != g.GrabID {
		t.Errorf("grab after engine save = %d, want %d", got.GrabID, g.GrabID)
	}

	// The progress the engine did report must still have landed.
	d, err := st.GetDownloadByEngineID(ctx, "abc123")
	if err != nil {
		t.Fatalf("GetDownloadByEngineID: %v", err)
	}
	if d.Progress != 0.5 || d.State != core.DownloadDownloading {
		t.Errorf("download = %+v, want progress 0.5 and state downloading", *d)
	}
}
