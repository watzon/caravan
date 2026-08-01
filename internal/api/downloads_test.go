package api

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
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
