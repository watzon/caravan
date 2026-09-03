package api

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/usenet"
)

// storeDownload persists a download row the way a grab would.
func storeDownload(t *testing.T, st *store.Store, engineID core.DownloadID, title string) core.Download {
	t.Helper()
	d := core.Download{
		GrabID:   42,
		Engine:   "stub",
		EngineID: engineID,
		Title:    title,
		State:    core.DownloadQueued,
		Size:     100,
	}
	if err := st.UpsertDownload(context.Background(), &d); err != nil {
		t.Fatalf("UpsertDownload: %v", err)
	}
	return d
}

func storeSeriesDownload(t *testing.T, st *store.Store, engineID core.DownloadID, title string, seriesID int64) core.Download {
	t.Helper()
	ctx := context.Background()
	grab := core.Grab{
		GrabInfo: core.GrabInfo{SeriesID: seriesID, ReleaseTitle: title},
		Status:   core.GrabStatusGrabbed,
	}
	if err := st.InsertGrab(ctx, &grab); err != nil {
		t.Fatalf("InsertGrab: %v", err)
	}
	download := core.Download{
		GrabID:   grab.GrabID,
		Engine:   "stub",
		EngineID: engineID,
		Title:    title,
		State:    core.DownloadQueued,
		Size:     100,
	}
	if err := st.UpsertDownload(ctx, &download); err != nil {
		t.Fatalf("UpsertDownload: %v", err)
	}
	return download
}

type routeCursorStubEngine struct {
	*stubEngine
	pageLimits []int
}

func (e *routeCursorStubEngine) ListPage(_ context.Context, limit int, before string) ([]core.DownloadStatus, string, bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.listErr != nil {
		return nil, "", true, e.listErr
	}
	e.pageLimits = append(e.pageLimits, limit)

	statuses := append([]core.DownloadStatus(nil), e.statuses...)
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID })
	beforeID := ""
	if before != "" {
		route, id, ok := strings.Cut(before, "\x00")
		if !ok || route != "stub" || id == "" {
			return nil, "", true, errors.New("stub engine: invalid page cursor")
		}
		beforeID = id
	}

	start := 0
	for start < len(statuses) && beforeID != "" && string(statuses[start].ID) <= beforeID {
		start++
	}
	if start == len(statuses) {
		return []core.DownloadStatus{}, "", true, nil
	}
	end := min(start+limit, len(statuses))
	next := ""
	if end < len(statuses) {
		next = "stub\x00" + string(statuses[end-1].ID)
	}
	return statuses[start:end], next, true, nil
}

func (e *routeCursorStubEngine) recordedPageLimits() []int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]int(nil), e.pageLimits...)
}

func newRouteCursorDownloadServer(t *testing.T) (http.Handler, *store.Store, *routeCursorStubEngine) {
	t.Helper()
	engine := &routeCursorStubEngine{stubEngine: &stubEngine{}}
	h, st, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: engine}))
	return h, st, engine
}

func TestListDownloadsMergesEngineAndStore(t *testing.T) {
	h, st, engine, _ := newAcquisitionServer(t)
	storeDownload(t, st, "abc", "Big Buck Bunny")
	engine.statuses = []core.DownloadStatus{
		{
			ID: "abc", State: core.DownloadDownloading, Name: "Big Buck Bunny",
			Progress: 0.5, BytesDone: 50, Size: 100, DownRate: 1000, UpRate: 10,
			ETASeconds: 30, Ratio: 0.1, SavePath: "incomplete/bbb",
		},
		{ID: "orphan", State: core.DownloadSeeding, Name: "Added out of band", Progress: 1, ETASeconds: -1},
	}

	rec := do(t, h, http.MethodGet, "/api/v1/downloads", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Downloads []downloadJSON `json:"downloads"`
	}
	decodeBody(t, rec, &body)

	if len(body.Downloads) != 2 {
		t.Fatalf("downloads = %+v, want the stored row and the engine's orphan", body.Downloads)
	}

	// The engine is authoritative for live numbers; the row keeps what the
	// engine has no opinion about.
	got := body.Downloads[0]
	if got.ID != "abc" || got.State != string(core.DownloadDownloading) || got.Progress != 0.5 ||
		got.BytesDone != 50 || got.DownRate != 1000 || got.ETASeconds != 30 || got.SavePath != "incomplete/bbb" {
		t.Fatalf("download = %+v, want the engine's live view", got)
	}
	if got.GrabID != 42 || got.Engine != "stub" || got.CreatedAt == "" {
		t.Fatalf("download = %+v, want the persisted fields kept", got)
	}

	// A download the engine knows about and Caravan does not is still shown.
	if body.Downloads[1].ID != "orphan" || body.Downloads[1].GrabID != 0 || body.Downloads[1].Engine != "stub" {
		t.Fatalf("orphan = %+v, want it surfaced", body.Downloads[1])
	}
}

func TestListDownloadsIncludesGrabTarget(t *testing.T) {
	h, st, engine, _ := newAcquisitionServer(t)
	ctx := context.Background()

	movie := &core.Movie{TMDBID: 7, Title: "Arrival", SortTitle: "arrival"}
	if err := st.UpsertMovie(ctx, movie); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	movieGrab := core.Grab{
		GrabInfo: core.GrabInfo{MovieID: movie.ID, ReleaseTitle: "Arrival.2016"},
		Status:   core.GrabStatusGrabbed,
	}
	if err := st.InsertGrab(ctx, &movieGrab); err != nil {
		t.Fatalf("InsertGrab movie: %v", err)
	}
	if err := st.UpsertDownload(ctx, &core.Download{
		GrabID: movieGrab.GrabID, Engine: "stub", EngineID: "movie-dl",
		Title: "Arrival.2016", State: core.DownloadQueued,
	}); err != nil {
		t.Fatalf("UpsertDownload movie: %v", err)
	}

	series := &core.Series{TMDBID: 3, Title: "Severance", SortTitle: "severance"}
	if err := st.UpsertSeries(ctx, series); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	episode := &core.Episode{SeriesID: series.ID, SeasonNumber: 1, EpisodeNumber: 2, Title: "Half Loop"}
	if err := st.UpsertEpisode(ctx, episode); err != nil {
		t.Fatalf("UpsertEpisode: %v", err)
	}
	adultLib := enableAdultLibrary(t, st)
	site := &core.Series{
		StashID: "site-transfixed", Title: "Transfixed", SortTitle: "transfixed",
		Kind: core.SeriesKindAdult, LibraryID: adultLib.ID,
	}
	if err := st.UpsertSeries(ctx, site); err != nil {
		t.Fatalf("UpsertSeries adult: %v", err)
	}
	scene := &core.Episode{SeriesID: site.ID, SeasonNumber: 2026, EpisodeNumber: 24, Title: "A Lesson"}
	if err := st.UpsertEpisode(ctx, scene); err != nil {
		t.Fatalf("UpsertEpisode scene: %v", err)
	}

	storeSeriesDownload(t, st, "series-dl", "Severance.S01E02", series.ID)
	sceneGrab := core.Grab{
		GrabInfo: core.GrabInfo{
			SeriesID: site.ID, EpisodeIDs: []int64{scene.ID}, ReleaseTitle: "Transfixed.24",
		},
		Status: core.GrabStatusGrabbed,
	}
	if err := st.InsertGrab(ctx, &sceneGrab); err != nil {
		t.Fatalf("InsertGrab scene: %v", err)
	}
	if err := st.UpsertDownload(ctx, &core.Download{
		GrabID: sceneGrab.GrabID, Engine: "stub", EngineID: "scene-dl",
		Title: "Transfixed.24", State: core.DownloadQueued,
	}); err != nil {
		t.Fatalf("UpsertDownload scene: %v", err)
	}

	engine.statuses = []core.DownloadStatus{
		{ID: "movie-dl", State: core.DownloadDownloading, Name: "Arrival.2016", Engine: "stub"},
		{ID: "series-dl", State: core.DownloadDownloading, Name: "Severance.S01E02", Engine: "stub"},
		{ID: "scene-dl", State: core.DownloadDownloading, Name: "Transfixed.24", Engine: "stub"},
	}

	rec := do(t, h, http.MethodGet, "/api/v1/downloads", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Downloads []downloadJSON `json:"downloads"`
	}
	decodeBody(t, rec, &body)

	byID := map[string]downloadJSON{}
	for _, row := range body.Downloads {
		byID[row.ID] = row
	}
	if got := byID["movie-dl"]; got.MovieID != movie.ID || got.SeriesID != 0 {
		t.Fatalf("movie download = %+v, want movie %d", got, movie.ID)
	}
	if got := byID["series-dl"]; got.SeriesID != series.ID || got.SeriesKind != core.SeriesKindTV || got.MovieID != 0 {
		t.Fatalf("series download = %+v, want series %d", got, series.ID)
	}
	if got := byID["scene-dl"]; got.SeriesID != site.ID || got.SeriesKind != core.SeriesKindAdult ||
		len(got.EpisodeIDs) != 1 || got.EpisodeIDs[0] != scene.ID ||
		got.SeasonNumber != 2026 || got.EpisodeNumber != 24 {
		t.Fatalf("scene download = %+v, want site %d 2026 · #024", got, site.ID)
	}
}

func TestListDownloadsUsesStableCursorPages(t *testing.T) {
	h, st, engine, _ := newAcquisitionServer(t)
	for _, id := range []core.DownloadID{"a", "b", "c"} {
		storeDownload(t, st, id, string(id))
		engine.statuses = append(engine.statuses, core.DownloadStatus{ID: id, Name: string(id), State: core.DownloadDownloading, Engine: "stub"})
	}
	engine.statuses = append(engine.statuses, core.DownloadStatus{ID: "orphan", Name: "orphan", Engine: "stub"})

	var all []string
	cursor := ""
	for page := 0; page < 3; page++ {
		path := "/api/v1/downloads?limit=2"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		rec := do(t, h, http.MethodGet, path, "")
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Downloads []downloadJSON `json:"downloads"`
			Next      string         `json:"next_cursor"`
		}
		decodeBody(t, rec, &body)
		if len(body.Downloads) == 0 || len(body.Downloads) > 2 {
			t.Fatalf("page %d downloads = %+v, want one or two rows", page, body.Downloads)
		}
		for _, row := range body.Downloads {
			all = append(all, row.ID)
		}
		cursor = body.Next
		if cursor == "" {
			break
		}
	}
	if len(all) != 4 {
		t.Fatalf("paged downloads = %v, want three stored rows and one orphan", all)
	}
	seen := map[string]bool{}
	for _, id := range all {
		if seen[id] {
			t.Fatalf("paged downloads repeated %q: %v", id, all)
		}
		seen[id] = true
	}
	for _, id := range []string{"a", "b", "c", "orphan"} {
		if !seen[id] {
			t.Errorf("paged downloads omitted %q: %v", id, all)
		}
	}
}

func TestListDownloadsCursorDrainsStoredAndOrphanPages(t *testing.T) {
	h, st, engine := newRouteCursorDownloadServer(t)
	for _, id := range []core.DownloadID{"stored-a", "stored-b", "stored-c"} {
		storeDownload(t, st, id, string(id))
	}
	engine.statuses = []core.DownloadStatus{
		{ID: "orphan-a", Name: "orphan-a", Engine: "stub"},
		{ID: "orphan-b", Name: "orphan-b", Engine: "stub"},
		{ID: "orphan-c", Name: "orphan-c", Engine: "stub"},
		{ID: "stored-a", Name: "stored-a", Engine: "stub"},
		{ID: "stored-b", Name: "stored-b", Engine: "stub"},
		{ID: "stored-c", Name: "stored-c", Engine: "stub"},
	}

	want := map[string]bool{
		"stored-a": true, "stored-b": true, "stored-c": true,
		"orphan-a": true, "orphan-b": true, "orphan-c": true,
	}
	seen := map[string]bool{}
	seenCursors := map[string]bool{}
	cursor := ""
	terminated := false
	for page := range 8 {
		path := "/api/v1/downloads?limit=2"
		if cursor != "" {
			if seenCursors[cursor] {
				t.Fatalf("pagination repeated cursor %q", cursor)
			}
			seenCursors[cursor] = true
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		rec := do(t, h, http.MethodGet, path, "")
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Downloads []downloadJSON `json:"downloads"`
			Next      string         `json:"next_cursor"`
		}
		decodeBody(t, rec, &body)
		if len(body.Downloads) == 0 {
			if body.Next != "" {
				t.Fatalf("empty page %d next_cursor = %q, want exhaustion", page, body.Next)
			}
			terminated = true
			break
		}
		if len(body.Downloads) > 2 {
			t.Fatalf("page %d downloads = %+v, want at most two rows", page, body.Downloads)
		}
		for _, row := range body.Downloads {
			if seen[row.ID] {
				t.Fatalf("pagination repeated download %q", row.ID)
			}
			seen[row.ID] = true
		}
		if body.Next == "" {
			terminated = true
			break
		}
		cursor = body.Next
	}
	if !terminated {
		t.Fatal("pagination did not terminate")
	}
	if len(seen) != len(want) {
		t.Fatalf("downloads = %v, want %v", seen, want)
	}
	for id := range want {
		if !seen[id] {
			t.Errorf("pagination omitted %q", id)
		}
	}
}

func TestListDownloadsInterleavedOrphansAppearExactlyOnce(t *testing.T) {
	h, st, engine := newRouteCursorDownloadServer(t)
	for _, id := range []core.DownloadID{"b-persisted", "e-persisted"} {
		storeDownload(t, st, id, string(id))
	}
	engine.statuses = []core.DownloadStatus{
		{ID: "a-orphan", Name: "a-orphan", Engine: "stub"},
		{ID: "b-persisted", Name: "b-persisted", Engine: "stub"},
		{ID: "c-orphan", Name: "c-orphan", Engine: "stub"},
		{ID: "d-orphan", Name: "d-orphan", Engine: "stub"},
		{ID: "e-persisted", Name: "e-persisted", Engine: "stub"},
		{ID: "f-orphan", Name: "f-orphan", Engine: "stub"},
	}

	orphanCounts := map[string]int{}
	cursor := ""
	for page := range 4 {
		path := "/api/v1/downloads?limit=2"
		if cursor != "" {
			path += "&cursor=" + url.QueryEscape(cursor)
		}
		rec := do(t, h, http.MethodGet, path, "")
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Downloads []downloadJSON `json:"downloads"`
			Next      string         `json:"next_cursor"`
		}
		decodeBody(t, rec, &body)
		if len(body.Downloads) > 2 {
			t.Fatalf("page %d downloads = %+v, want at most two rows", page, body.Downloads)
		}
		for _, row := range body.Downloads {
			if strings.HasSuffix(row.ID, "-orphan") {
				orphanCounts[row.ID]++
			}
		}
		cursor = body.Next
		if cursor == "" {
			break
		}
	}
	if cursor != "" {
		t.Fatalf("pagination did not drain; final cursor = %q", cursor)
	}
	for _, id := range []string{"a-orphan", "c-orphan", "d-orphan", "f-orphan"} {
		if orphanCounts[id] != 1 {
			t.Errorf("orphan %q appeared %d times, want exactly once; all counts = %v", id, orphanCounts[id], orphanCounts)
		}
	}
	if len(orphanCounts) != 4 {
		t.Fatalf("orphan counts = %v, want exactly four distinct engine-only rows", orphanCounts)
	}
	if limits := engine.recordedPageLimits(); !slices.Equal(limits, []int{2, 1, 2, 1}) {
		t.Fatalf("native page limits = %v, want remaining capacities [2 1 2 1]", limits)
	}
}

func TestListDownloadsExactStoredPageFindsLaterOrphan(t *testing.T) {
	h, st, engine := newRouteCursorDownloadServer(t)
	for _, id := range []core.DownloadID{"persisted-a", "persisted-b"} {
		storeDownload(t, st, id, string(id))
	}
	engine.statuses = []core.DownloadStatus{
		{ID: "persisted-a", Name: "persisted-a", Engine: "stub"},
		{ID: "persisted-b", Name: "persisted-b", Engine: "stub"},
		{ID: "z-live-only", Name: "z-live-only", Engine: "stub"},
	}

	rec := do(t, h, http.MethodGet, "/api/v1/downloads?limit=2", "")
	wantStatus(t, rec, http.StatusOK)
	var first struct {
		Downloads []downloadJSON `json:"downloads"`
		Next      string         `json:"next_cursor"`
	}
	decodeBody(t, rec, &first)
	if len(first.Downloads) != 2 {
		t.Fatalf("first page downloads = %+v, want two persisted rows", first.Downloads)
	}
	if first.Next == "" {
		t.Fatal("first page omitted continuation for later live-only download")
	}
	if calls := engine.recordedPageLimits(); len(calls) != 0 {
		t.Fatalf("stored-to-orphan boundary pager calls = %v, want none", calls)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/downloads?limit=2&cursor="+url.QueryEscape(first.Next), "")
	wantStatus(t, rec, http.StatusOK)
	var second struct {
		Downloads []downloadJSON `json:"downloads"`
		Next      string         `json:"next_cursor"`
	}
	decodeBody(t, rec, &second)
	if len(second.Downloads) != 1 || second.Downloads[0].ID != "z-live-only" {
		t.Fatalf("second page downloads = %+v, want the later live-only download", second.Downloads)
	}
	if second.Next != "" {
		t.Fatalf("second page next_cursor = %q, want exhaustion", second.Next)
	}
	for _, limit := range engine.recordedPageLimits() {
		if limit != 2 {
			t.Fatalf("orphan-stage pager limit = %d, want 2", limit)
		}
	}
}

func TestListDownloadsRejectsInvalidCursorParameters(t *testing.T) {
	h, _, _, _ := newAcquisitionServer(t)
	for _, path := range []string{
		"/api/v1/downloads?limit=0",
		"/api/v1/downloads?limit=nope",
		"/api/v1/downloads?cursor=stored:nope",
		"/api/v1/downloads?cursor=orphan:bad",
	} {
		rec := do(t, h, http.MethodGet, path, "")
		wantStatus(t, rec, http.StatusBadRequest)
	}
}

func TestListDownloadsRespectsAdultVisibility(t *testing.T) {
	h, st, engine, _ := newAcquisitionServer(t)
	ctx := context.Background()
	setPassword(t, st, testPassword)
	cookie := login(t, h, testAdmin, testPassword)

	site := &core.Series{
		Kind: core.SeriesKindAdult, StashID: "queue-site", Title: "Adult Site", SortTitle: "adult site",
		LibraryID: defaultLibraryID(t, st, core.LibraryKindAdult),
	}
	if err := st.UpsertSeries(ctx, site); err != nil {
		t.Fatalf("UpsertSeries(adult): %v", err)
	}
	show := &core.Series{
		Kind: core.SeriesKindTV, TMDBID: 101, Title: "Family Show", SortTitle: "family show",
	}
	if err := st.UpsertSeries(ctx, show); err != nil {
		t.Fatalf("UpsertSeries(tv): %v", err)
	}

	adult := storeSeriesDownload(t, st, "adult-download", "Explicit Adult Release", site.ID)
	ordinary := storeSeriesDownload(t, st, "tv-download", "Family Show S01E01", show.ID)
	orphan := storeDownload(t, st, "orphaned-grab", "Download With Missing Grab")
	engine.statuses = []core.DownloadStatus{
		{ID: adult.EngineID, Engine: adult.Engine, Name: adult.Title, State: core.DownloadDownloading},
		{ID: ordinary.EngineID, Engine: ordinary.Engine, Name: ordinary.Title, State: core.DownloadDownloading},
		{ID: orphan.EngineID, Engine: orphan.Engine, Name: orphan.Title, State: core.DownloadDownloading},
		{ID: "engine-only", Engine: "stub", Name: "Engine-only Download", State: core.DownloadDownloading},
	}

	names := func(path string) map[string]bool {
		t.Helper()
		rec := doAuth(t, h, http.MethodGet, path, "", withCookie(cookie))
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Downloads []downloadJSON `json:"downloads"`
		}
		decodeBody(t, rec, &body)
		got := make(map[string]bool, len(body.Downloads))
		for _, row := range body.Downloads {
			got[row.Name] = true
		}
		return got
	}

	for _, path := range []string{"/api/v1/downloads", "/api/v1/downloads?limit=10"} {
		got := names(path)
		if got[adult.Title] {
			t.Errorf("GET %s with adult disabled exposed %q: %v", path, adult.Title, got)
		}
		for _, want := range []string{ordinary.Title, orphan.Title, "Engine-only Download"} {
			if !got[want] {
				t.Errorf("GET %s with adult disabled omitted %q: %v", path, want, got)
			}
		}
	}

	enableAdultLibrary(t, st)
	for _, path := range []string{"/api/v1/downloads", "/api/v1/downloads?limit=10"} {
		if got := names(path); !got[adult.Title] {
			t.Errorf("GET %s with adult enabled omitted %q: %v", path, adult.Title, got)
		}
	}
}

// Every queue row says which protocol it is, because the detail drawer is built
// from it: a torrent has peers, trackers, a ratio and an upload limit, and a
// Usenet download has a file list and repair stages instead. Before this the UI
// showed torrent chrome (and a Limits tab the embedded Usenet engine answers
// 400 for) on every download whatever fetched it.
func TestListDownloadsTagsEachRowWithItsProtocol(t *testing.T) {
	h, st, engine, _ := newAcquisitionServer(t)

	for _, row := range []struct {
		id     core.DownloadID
		engine string
		want   string
	}{
		{"torrent-embedded", clients.EmbeddedTorrentEngine, core.ProtocolTorrent},
		{"torrent-external", core.DownloadClientQBittorrent, core.ProtocolTorrent},
		{"usenet-embedded", clients.EmbeddedUsenetEngine, core.ProtocolUsenet},
		{"usenet-sabnzbd", core.DownloadClientSABnzbd, core.ProtocolUsenet},
		{"usenet-nzbget", core.DownloadClientNZBGet, core.ProtocolUsenet},
	} {
		d := core.Download{Engine: row.engine, EngineID: row.id, Title: string(row.id), State: core.DownloadQueued}
		if err := st.UpsertDownload(context.Background(), &d); err != nil {
			t.Fatalf("UpsertDownload: %v", err)
		}
		// The engine is authoritative for the backend name too, so the live
		// overlay has to be what the protocol follows from.
		engine.statuses = append(engine.statuses, core.DownloadStatus{
			ID: row.id, State: core.DownloadDownloading, Name: string(row.id), Engine: row.engine,
		})
	}

	rec := do(t, h, http.MethodGet, "/api/v1/downloads", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Downloads []downloadJSON `json:"downloads"`
	}
	decodeBody(t, rec, &body)

	got := map[string]string{}
	for _, d := range body.Downloads {
		got[d.ID] = d.Protocol
	}
	want := map[string]string{
		"torrent-embedded": core.ProtocolTorrent,
		"torrent-external": core.ProtocolTorrent,
		"usenet-embedded":  core.ProtocolUsenet,
		"usenet-sabnzbd":   core.ProtocolUsenet,
		"usenet-nzbget":    core.ProtocolUsenet,
	}
	for id, wantProtocol := range want {
		if got[id] != wantProtocol {
			t.Errorf("download %s protocol = %q, want %q", id, got[id], wantProtocol)
		}
	}
}

// The engine name constants are duplicated in internal/clients, which cannot
// import the engines themselves without closing a cycle. This is the one place
// all three packages are visible, so it is where the copy is pinned.
func TestEmbeddedEngineNamesMatchTheEngines(t *testing.T) {
	if clients.EmbeddedTorrentEngine != download.EngineName {
		t.Errorf("clients.EmbeddedTorrentEngine = %q, want %q", clients.EmbeddedTorrentEngine, download.EngineName)
	}
	if clients.EmbeddedUsenetEngine != usenet.EngineName {
		t.Errorf("clients.EmbeddedUsenetEngine = %q, want %q", clients.EmbeddedUsenetEngine, usenet.EngineName)
	}
}

// Without an engine the queue still renders from the persisted rows: history
// must not disappear because the engine did not start.
func TestListDownloadsWithoutEngine(t *testing.T) {
	h, st, _ := newTestServer(t)
	storeDownload(t, st, "abc", "Big Buck Bunny")

	rec := do(t, h, http.MethodGet, "/api/v1/downloads", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Downloads []downloadJSON `json:"downloads"`
	}
	decodeBody(t, rec, &body)

	if len(body.Downloads) != 1 {
		t.Fatalf("downloads = %+v, want the stored row", body.Downloads)
	}
	if body.Downloads[0].State != string(core.DownloadQueued) || body.Downloads[0].ETASeconds != -1 {
		t.Fatalf("download = %+v, want the persisted state and an unknown ETA", body.Downloads[0])
	}
}

func TestListDownloadsReportsEngineFailure(t *testing.T) {
	h, _, engine, _ := newAcquisitionServer(t)
	engine.listErr = errors.New("engine is not running")

	rec := do(t, h, http.MethodGet, "/api/v1/downloads", "")
	wantStatus(t, rec, http.StatusBadGateway)
	wantErrorBody(t, rec)
}

func TestPauseAndResumeDownload(t *testing.T) {
	h, st, engine, _ := newAcquisitionServer(t)
	storeDownload(t, st, "abc", "Big Buck Bunny")

	rec := do(t, h, http.MethodPost, "/api/v1/downloads/abc/pause", "")
	wantStatus(t, rec, http.StatusNoContent)
	rec = do(t, h, http.MethodPost, "/api/v1/downloads/abc/resume", "")
	wantStatus(t, rec, http.StatusNoContent)

	if len(engine.paused) != 1 || engine.paused[0] != "abc" {
		t.Fatalf("paused = %v, want [abc]", engine.paused)
	}
	if len(engine.resumed) != 1 || engine.resumed[0] != "abc" {
		t.Fatalf("resumed = %v, want [abc]", engine.resumed)
	}
}

func TestPauseDownloadFailures(t *testing.T) {
	t.Run("engine failure", func(t *testing.T) {
		h, _, engine, _ := newAcquisitionServer(t)
		engine.controlErr = errors.New("unknown download")

		rec := do(t, h, http.MethodPost, "/api/v1/downloads/abc/pause", "")
		wantStatus(t, rec, http.StatusBadGateway)
		wantErrorBody(t, rec)
	})

	t.Run("completed download cannot resume", func(t *testing.T) {
		h, _, engine, _ := newAcquisitionServer(t)
		engine.controlErr = download.ErrNotResumable

		rec := do(t, h, http.MethodPost, "/api/v1/downloads/abc/resume", "")
		wantStatus(t, rec, http.StatusConflict)
		wantErrorBody(t, rec)
	})

	t.Run("no engine configured", func(t *testing.T) {
		h, _, _ := newTestServer(t)

		rec := do(t, h, http.MethodPost, "/api/v1/downloads/abc/resume", "")
		wantStatus(t, rec, http.StatusServiceUnavailable)
		wantErrorBody(t, rec)
	})
}

func TestDeleteDownload(t *testing.T) {
	tests := []struct {
		name       string
		query      string
		deleteData bool
	}{
		{"keeps data by default", "", false},
		{"keeps data when asked", "?deleteData=false", false},
		{"deletes data when asked", "?deleteData=true", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, st, engine, _ := newAcquisitionServer(t)
			ctx := context.Background()
			storeDownload(t, st, "abc", "Big Buck Bunny")

			rec := do(t, h, http.MethodDelete, "/api/v1/downloads/abc"+tt.query, "")
			wantStatus(t, rec, http.StatusNoContent)

			if len(engine.removed) != 1 || engine.removed[0].id != "abc" || engine.removed[0].deleteData != tt.deleteData {
				t.Fatalf("removed = %+v, want deleteData=%v", engine.removed, tt.deleteData)
			}

			downloads, err := st.ListDownloads(ctx)
			if err != nil {
				t.Fatalf("ListDownloads: %v", err)
			}
			if len(downloads) != 0 {
				t.Fatalf("downloads = %+v, want the row forgotten", downloads)
			}

			events, err := st.ListEvents(ctx, 0)
			if err != nil {
				t.Fatalf("ListEvents: %v", err)
			}
			if len(events) != 1 || events[0].Category != "download" {
				t.Fatalf("events = %+v, want one download event", events)
			}
			// The event has to say whether the data went with it.
			wantDetail := "download data kept"
			if tt.deleteData {
				wantDetail = "download data deleted"
			}
			if events[0].Detail != wantDetail {
				t.Fatalf("detail = %q, want %q", events[0].Detail, wantDetail)
			}
		})
	}
}

func TestDeleteAdultDownloadRecordsOwnershipForHistory(t *testing.T) {
	h, st, _, _ := newAcquisitionServer(t)
	ctx := context.Background()
	site := &core.Series{
		Kind: core.SeriesKindAdult, StashID: "removed-site", Title: "Removed Adult Site", SortTitle: "removed adult site",
		LibraryID: defaultLibraryID(t, st, core.LibraryKindAdult),
	}
	if err := st.UpsertSeries(ctx, site); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	download := storeSeriesDownload(t, st, "adult-download", "Removed Adult Release", site.ID)

	rec := do(t, h, http.MethodDelete, "/api/v1/downloads/"+string(download.EngineID), "")
	wantStatus(t, rec, http.StatusNoContent)

	events, err := st.ListEvents(ctx, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want one removal event", events)
	}
	if events[0].Message != "Removed download "+download.Title {
		t.Fatalf("message = %q, want removed download title", events[0].Message)
	}
	if events[0].MovieID != 0 || events[0].SeriesID != site.ID {
		t.Fatalf("event ownership = movie %d series %d, want movie 0 series %d", events[0].MovieID, events[0].SeriesID, site.ID)
	}
}

// Removing a download must never reach the library: an imported file is a
// hardlink or a move away from the download data (SPEC §13).
func TestDeleteDownloadLeavesLibraryAlone(t *testing.T) {
	h, st, _, _ := newAcquisitionServer(t)
	ctx := context.Background()
	storeDownload(t, st, "abc", "Big Buck Bunny")

	m := addMovie(t, st, "Big Buck Bunny", 2008)
	file := core.MediaFile{Path: "Movies/Big Buck Bunny (2008)/Big Buck Bunny (2008).mkv", Size: 100, MovieID: m.ID}
	if err := st.UpsertMediaFile(ctx, &file); err != nil {
		t.Fatalf("UpsertMediaFile: %v", err)
	}

	rec := do(t, h, http.MethodDelete, "/api/v1/downloads/abc?deleteData=true", "")
	wantStatus(t, rec, http.StatusNoContent)

	files, err := st.ListMediaFilesForMovie(ctx, m.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForMovie: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("media files = %+v, want the library untouched", files)
	}
	if _, err := st.GetMovie(ctx, m.ID); err != nil {
		t.Fatalf("GetMovie: %v", err)
	}
}

func TestDeleteDownloadRejectsBadDeleteDataFlag(t *testing.T) {
	h, st, engine, _ := newAcquisitionServer(t)
	storeDownload(t, st, "abc", "Big Buck Bunny")

	rec := do(t, h, http.MethodDelete, "/api/v1/downloads/abc?deleteData=maybe", "")
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)

	if len(engine.removed) != 0 {
		t.Fatalf("removed = %+v, want nothing removed", engine.removed)
	}
}

// A download the store never knew about is still the engine's to remove; the
// event names it by handle rather than failing.
func TestDeleteUnknownDownload(t *testing.T) {
	h, st, engine, _ := newAcquisitionServer(t)

	rec := do(t, h, http.MethodDelete, "/api/v1/downloads/ghost", "")
	wantStatus(t, rec, http.StatusNoContent)

	if len(engine.removed) != 1 || engine.removed[0].id != "ghost" {
		t.Fatalf("removed = %+v, want the engine asked anyway", engine.removed)
	}
	events, err := st.ListEvents(context.Background(), 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Message != "Removed download ghost" {
		t.Fatalf("events = %+v, want the handle used as the name", events)
	}
}
