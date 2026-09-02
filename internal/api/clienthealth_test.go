package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
)

// healthyEngineProvider is a stubEngineProvider that also reports external
// download-client health, the way the serving process's provider does.
type healthEngineProvider struct {
	stubEngineProvider
	unhealthy []core.DownloadClientHealth
}

func (p *healthEngineProvider) UnhealthyDownloadClients() []core.DownloadClientHealth {
	return p.unhealthy
}

var _ DownloadClientHealthReporter = (*healthEngineProvider)(nil)

// The banner's input: an unreachable client is named on GET /system/status,
// with the poll's reason, and nothing else about the system changes.
func TestSystemStatusNamesUnreachableDownloadClients(t *testing.T) {
	down := time.Date(2026, 8, 1, 9, 30, 0, 0, time.UTC)
	provider := &healthEngineProvider{
		stubEngineProvider: stubEngineProvider{engine: &stubEngine{}},
		unhealthy: []core.DownloadClientHealth{{
			ID: 3, Name: "Seedbox", Type: core.DownloadClientQBittorrent,
			Error: "connection refused", Since: down,
		}},
	}
	h, _, _ := newTestServer(t, WithEngine(provider))

	rec := do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)

	var got statusResponse
	decodeBody(t, rec, &got)
	if got.EngineHealth != "ok" {
		t.Fatalf("engine_health = %q, want the embedded engine unaffected", got.EngineHealth)
	}
	if len(got.UnhealthyDownloadClients) != 1 {
		t.Fatalf("unhealthy_download_clients = %+v, want one entry", got.UnhealthyDownloadClients)
	}
	want := unhealthyClientJSON{
		ID: 3, Name: "Seedbox", Type: core.DownloadClientQBittorrent,
		Error: "connection refused", Since: jsonTime(down),
	}
	if got.UnhealthyDownloadClients[0] != want {
		t.Fatalf("unhealthy_download_clients[0] = %+v, want %+v", got.UnhealthyDownloadClients[0], want)
	}
}

// A healthy system reports an empty list, not null: the UI iterates it.
func TestSystemStatusReportsNoUnhealthyClientsByDefault(t *testing.T) {
	h, _, _, _ := newAcquisitionServer(t)

	rec := do(t, h, http.MethodGet, "/api/v1/system/status", "")
	wantStatus(t, rec, http.StatusOK)
	if !strings.Contains(rec.Body.String(), `"unhealthy_download_clients":[]`) {
		t.Fatalf("body = %q, want an empty array", rec.Body.String())
	}
}

// A grab routed to a client that has stopped answering fails immediately, with
// the poll's own reason rather than a generic "add download", and the grab is
// recorded as failed so the queue does not show a download nobody holds.
func TestGrabToAnUnreachableClientFailsWithItsReason(t *testing.T) {
	ctx := context.Background()
	torrent := &stubEngine{}
	router := download.NewRouter(func(context.Context) ([]download.Route, error) {
		return []download.Route{{
			Name: core.DownloadClientQBittorrent, Protocol: core.ProtocolTorrent,
			Engine: torrent, Unhealthy: "connection refused",
		}}, nil
	})
	h, st, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: router}))
	m := addMovie(t, st, "Big Buck Bunny", 2008)
	rel := cacheRelease(t, st, "BBB.2008.1080p")

	rec := do(t, h, http.MethodPost, "/api/v1/library/movies/"+itoa(m.ID)+"/grab",
		`{"release_id":`+itoa(rel.ID)+`}`)
	wantStatus(t, rec, http.StatusBadGateway)
	var failure errorResponse
	decodeBody(t, rec, &failure)
	if !strings.Contains(failure.Error, "connection refused") {
		t.Fatalf("error = %q, want the poll's own reason", failure.Error)
	}

	if adds := torrent.addCalls(); len(adds) != 0 {
		t.Fatalf("the release was handed to the unreachable client: %+v", adds)
	}
	grabs, err := st.ListGrabs(ctx, 0)
	if err != nil {
		t.Fatalf("ListGrabs: %v", err)
	}
	if len(grabs) != 1 || grabs[0].Status != core.GrabStatusFailed {
		t.Fatalf("grabs = %+v, want one failed grab", grabs)
	}
	downloads, err := st.ListDownloads(ctx)
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if len(downloads) != 0 {
		t.Fatalf("downloads = %+v, want none for a grab that was never accepted", downloads)
	}
}

// The queue row says which backend holds a download, because a live status
// from a router names it. That is what puts "qbittorrent" on the row instead
// of the provider's own name.
func TestListDownloadsNamesTheBackendFromTheLiveStatus(t *testing.T) {
	engine := &stubEngine{statuses: []core.DownloadStatus{{
		ID: "nzo_1", State: core.DownloadDownloading, Name: "Movie.2020",
		Engine: core.DownloadClientSABnzbd, ETASeconds: -1,
	}}}
	h, _, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: engine}))

	rec := do(t, h, http.MethodGet, "/api/v1/downloads", "")
	wantStatus(t, rec, http.StatusOK)
	var body struct {
		Downloads []downloadJSON `json:"downloads"`
	}
	decodeBody(t, rec, &body)
	if len(body.Downloads) != 1 {
		t.Fatalf("downloads = %+v, want one", body.Downloads)
	}
	if body.Downloads[0].Engine != core.DownloadClientSABnzbd {
		t.Fatalf("engine = %q, want %q", body.Downloads[0].Engine, core.DownloadClientSABnzbd)
	}
}
