package store

import (
	"context"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestListCalendarGrabsFiltersByCalendarIDs(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	selectedMovie := insertCalendarGrab(t, st, core.Grab{
		GrabInfo: core.GrabInfo{MovieID: 11, ReleaseTitle: "selected movie"},
		Status:   core.GrabStatusGrabbed,
	})
	insertCalendarGrab(t, st, core.Grab{
		GrabInfo: core.GrabInfo{MovieID: 22, ReleaseTitle: "unrelated movie"},
		Status:   core.GrabStatusGrabbed,
	})
	selectedSeason := insertCalendarGrab(t, st, core.Grab{
		GrabInfo: core.GrabInfo{
			SeriesID:     3,
			EpisodeIDs:   []int64{101, 102},
			ReleaseTitle: "selected season",
		},
		Status: core.GrabStatusGrabbed,
	})
	insertCalendarGrab(t, st, core.Grab{
		GrabInfo: core.GrabInfo{
			SeriesID:     4,
			EpisodeIDs:   []int64{201},
			ReleaseTitle: "unrelated episode",
		},
		Status: core.GrabStatusGrabbed,
	})
	insertCalendarGrab(t, st, core.Grab{
		GrabInfo: core.GrabInfo{MovieID: 11, ReleaseTitle: "failed selected"},
		Status:   core.GrabStatusFailed,
	})
	insertCalendarGrab(t, st, core.Grab{
		GrabInfo: core.GrabInfo{MovieID: 11, ReleaseTitle: "rejected selected"},
		Status:   core.GrabStatusRejected,
	})

	empty, err := st.ListCalendarGrabs(ctx, nil, nil)
	if err != nil {
		t.Fatalf("ListCalendarGrabs empty: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("ListCalendarGrabs empty = %#v, want non-nil empty slice", empty)
	}

	grabs, err := st.ListCalendarGrabs(ctx, []int64{11}, []int64{102})
	if err != nil {
		t.Fatalf("ListCalendarGrabs: %v", err)
	}
	if len(grabs) != 2 {
		t.Fatalf("ListCalendarGrabs returned %d rows, want 2: %#v", len(grabs), grabs)
	}
	if grabs[0].GrabID != selectedSeason.GrabID || grabs[1].GrabID != selectedMovie.GrabID {
		t.Fatalf("ListCalendarGrabs IDs = %d, %d, want %d, %d", grabs[0].GrabID, grabs[1].GrabID, selectedSeason.GrabID, selectedMovie.GrabID)
	}
}

func TestListDownloadsForGrabsFiltersAndOrders(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	selected := insertCalendarGrab(t, st, core.Grab{
		GrabInfo: core.GrabInfo{MovieID: 11, ReleaseTitle: "selected"},
		Status:   core.GrabStatusGrabbed,
	})
	other := insertCalendarGrab(t, st, core.Grab{
		GrabInfo: core.GrabInfo{MovieID: 22, ReleaseTitle: "other"},
		Status:   core.GrabStatusGrabbed,
	})
	selectedFailed := insertCalendarGrab(t, st, core.Grab{
		GrabInfo: core.GrabInfo{MovieID: 33, ReleaseTitle: "selected failed"},
		Status:   core.GrabStatusGrabbed,
	})

	first := insertCalendarDownload(t, st, core.Download{GrabID: selected.GrabID, EngineID: "selected-active", State: core.DownloadQueued})
	second := insertCalendarDownload(t, st, core.Download{GrabID: selectedFailed.GrabID, EngineID: "selected-failed", State: core.DownloadFailed})
	insertCalendarDownload(t, st, core.Download{GrabID: other.GrabID, EngineID: "other-active", State: core.DownloadQueued})
	insertCalendarDownload(t, st, core.Download{GrabID: 0, EngineID: "orphan", State: core.DownloadQueued})

	empty, err := st.ListDownloadsForGrabs(ctx, nil)
	if err != nil {
		t.Fatalf("ListDownloadsForGrabs empty: %v", err)
	}
	if empty == nil || len(empty) != 0 {
		t.Fatalf("ListDownloadsForGrabs empty = %#v, want non-nil empty slice", empty)
	}

	downloads, err := st.ListDownloadsForGrabs(ctx, []int64{selected.GrabID, selectedFailed.GrabID})
	if err != nil {
		t.Fatalf("ListDownloadsForGrabs: %v", err)
	}
	if len(downloads) != 2 {
		t.Fatalf("ListDownloadsForGrabs returned %d rows, want 2: %#v", len(downloads), downloads)
	}
	if downloads[0].ID != second.ID || downloads[1].ID != first.ID {
		t.Fatalf("download IDs = %d, %d, want %d, %d", downloads[0].ID, downloads[1].ID, second.ID, first.ID)
	}
}

func insertCalendarGrab(t *testing.T, st *Store, grab core.Grab) core.Grab {
	t.Helper()
	if err := st.InsertGrab(context.Background(), &grab); err != nil {
		t.Fatalf("InsertGrab %q: %v", grab.ReleaseTitle, err)
	}
	return grab
}

func insertCalendarDownload(t *testing.T, st *Store, download core.Download) core.Download {
	t.Helper()
	if err := st.UpsertDownload(context.Background(), &download); err != nil {
		t.Fatalf("UpsertDownload %q: %v", download.EngineID, err)
	}
	return download
}
