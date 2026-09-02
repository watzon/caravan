package api

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/store"
)

// newRoutingServer builds a server whose engine is the real protocol router
// over stubs, so the grab endpoints are exercised through the same dispatch the
// serving process uses.
//
// usenet is optional: passing nil is the configuration a stock Caravan has,
// where usenet has no engine at all.
func newRoutingServer(t *testing.T, usenet *stubEngine) (http.Handler, *store.Store, *stubEngine, *stubEngine) {
	t.Helper()
	torrent := &stubEngine{}
	routes := []download.Route{
		{Name: download.EngineName, Protocol: core.ProtocolTorrent, Engine: torrent},
	}
	if usenet != nil {
		routes = append(routes, download.Route{
			Name: core.DownloadClientSABnzbd, Protocol: core.ProtocolUsenet, Engine: usenet,
		})
	}
	router := download.NewRouter(func(context.Context) ([]download.Route, error) { return routes, nil })
	h, st, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: router}))
	return h, st, torrent, usenet
}

// cacheUsenetRelease stores the release a Newznab indexer produces.
func cacheUsenetRelease(t *testing.T, st *store.Store, title string) core.Release {
	t.Helper()
	rel := core.Release{
		Title:       title,
		GUID:        "guid-" + title,
		DownloadURL: "https://news.example/getnzb/" + title + ".nzb",
		Protocol:    core.ProtocolUsenet,
		Size:        4 << 30,
		PublishedAt: time.Now().Add(-48 * time.Hour),
		Parsed:      core.ParsedRelease{Title: title, Quality: core.Quality1080p},
		IndexerID:   7,
		Indexer:     "alpha",
	}
	if err := st.UpsertRelease(context.Background(), &rel); err != nil {
		t.Fatalf("UpsertRelease: %v", err)
	}
	return rel
}

// The acceptance criterion at the HTTP boundary: Torznab and Newznab results
// route to the correct engine by protocol, with no per-grab choice anywhere in
// the request.
func TestGrabRoutesByReleaseProtocol(t *testing.T) {
	ctx := context.Background()
	usenetEngine := &stubEngine{}
	h, st, torrentEngine, _ := newRoutingServer(t, usenetEngine)
	m := addMovie(t, st, "Big Buck Bunny", 2008)

	torrentRel := cacheRelease(t, st, "BBB.2008.1080p")
	usenetRel := cacheUsenetRelease(t, st, "BBB.2008.1080p.WEB")

	for _, tt := range []struct {
		name    string
		release core.Release
		engine  *stubEngine
		other   *stubEngine
		want    string
	}{
		{"torznab result", torrentRel, torrentEngine, usenetEngine, download.EngineName},
		{"newznab result", usenetRel, usenetEngine, torrentEngine, core.DownloadClientSABnzbd},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := len(tt.other.addCalls())
			rec := do(t, h, http.MethodPost, "/api/v1/library/movies/"+itoa(m.ID)+"/grab",
				`{"release_id":`+itoa(tt.release.ID)+`}`)
			wantStatus(t, rec, http.StatusCreated)
			var body grabResponse
			decodeBody(t, rec, &body)

			adds := tt.engine.addCalls()
			if len(adds) == 0 || adds[len(adds)-1].release.Title != tt.release.Title {
				t.Fatalf("%s did not reach its engine: %+v", tt.name, adds)
			}
			if got := len(tt.other.addCalls()); got != before {
				t.Fatalf("%s also reached the other engine", tt.name)
			}

			// The download row has to record the engine that actually holds
			// it: that column is what addresses the download afterwards, and
			// the router's own name would address nothing.
			dl, err := st.GetDownloadByEngineID(ctx, core.DownloadID(body.DownloadID))
			if err != nil {
				t.Fatalf("GetDownloadByEngineID: %v", err)
			}
			if dl.Engine != tt.want {
				t.Fatalf("download engine = %q, want %q", dl.Engine, tt.want)
			}
		})
	}
}

// A usenet release with no usenet engine configured is a recorded rejection and
// a 4xx that names what to configure, never a silent drop, and never a misroute
// into the embedded torrent engine.
func TestUsenetGrabWithoutAUsenetEngineIsRejected(t *testing.T) {
	ctx := context.Background()
	h, st, torrentEngine, _ := newRoutingServer(t, nil)
	m := addMovie(t, st, "Big Buck Bunny", 2008)
	rel := cacheUsenetRelease(t, st, "BBB.2008.1080p.WEB")

	rec := do(t, h, http.MethodPost, "/api/v1/library/movies/"+itoa(m.ID)+"/grab",
		`{"release_id":`+itoa(rel.ID)+`}`)
	// A 4xx, not the 502 an engine that tried and failed gets: nothing broke,
	// the user has not finished configuring Caravan.
	wantStatus(t, rec, http.StatusConflict)
	var failure errorResponse
	decodeBody(t, rec, &failure)
	// What to configure is a storage root and a news server: the built-in
	// Usenet engine needs no external download client, and telling the user
	// otherwise would present one as required.
	if !strings.Contains(failure.Error, "storage root") || !strings.Contains(failure.Error, "Usenet servers") {
		t.Fatalf("error = %q, want it to name what to configure", failure.Error)
	}
	if strings.Contains(failure.Error, "SABnzbd") || strings.Contains(failure.Error, "NZBGet") {
		t.Fatalf("error = %q, want it not to present an external client as required", failure.Error)
	}

	if adds := torrentEngine.addCalls(); len(adds) != 0 {
		t.Fatalf("the usenet release reached the torrent engine: %+v", adds)
	}

	grabs, err := st.ListGrabs(ctx, 0)
	if err != nil {
		t.Fatalf("ListGrabs: %v", err)
	}
	if len(grabs) != 1 || grabs[0].Status != core.GrabStatusRejected {
		t.Fatalf("grabs = %+v, want one rejected grab", grabs)
	}
	if !strings.Contains(grabs[0].Reason, "Usenet servers") {
		t.Fatalf("recorded reason = %q, want the reason the user can act on", grabs[0].Reason)
	}

	downloads, err := st.ListDownloads(ctx)
	if err != nil {
		t.Fatalf("ListDownloads: %v", err)
	}
	if len(downloads) != 0 {
		t.Fatalf("downloads = %+v, want none for a rejected grab", downloads)
	}

	// The activity feed is where a user looks for this. It is a warning: there
	// is nothing broken to report.
	events, err := st.ListEvents(ctx, 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) != 1 || events[0].Level != core.EventLevelWarn || events[0].Category != "grab" {
		t.Fatalf("events = %+v, want one warning grab event", events)
	}
	if !strings.Contains(events[0].Detail, "Usenet servers") {
		t.Fatalf("event detail = %q, want it to name what to configure", events[0].Detail)
	}
}

// The routing settings are the whole routing configuration, so a value that
// would not route has to be refused where the user can see it rather than
// stored and silently ignored at grab time.
func TestRouteSettingsAreValidated(t *testing.T) {
	h, st, _, _ := newRoutingServer(t, nil)
	ctx := context.Background()

	sab := core.DownloadClientConfig{
		Type: core.DownloadClientSABnzbd, Name: "sab", URL: "http://sab.example",
		APIKey: "secret", Priority: 25, Enabled: true,
	}
	if err := st.UpsertDownloadClient(ctx, &sab); err != nil {
		t.Fatalf("UpsertDownloadClient: %v", err)
	}
	id := itoa(sab.ID)

	for _, tt := range []struct {
		name string
		body string
		want int
	}{
		{"embedded is the torrent default", `{"route_torrent":"embedded"}`, http.StatusOK},
		{"empty clears a route", `{"route_usenet":""}`, http.StatusOK},
		{"a usenet client takes usenet", `{"route_usenet":"` + id + `"}`, http.StatusOK},
		{"the embedded engine cannot take usenet", `{"route_usenet":"embedded"}`, http.StatusBadRequest},
		{"a usenet client cannot take torrents", `{"route_torrent":"` + id + `"}`, http.StatusBadRequest},
		{"an id nothing owns", `{"route_usenet":"9999"}`, http.StatusBadRequest},
		{"a value that is not an id", `{"route_torrent":"qbittorrent"}`, http.StatusBadRequest},
		{"a negative id", `{"route_usenet":"-1"}`, http.StatusBadRequest},
	} {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPut, "/api/v1/settings", tt.body)
			wantStatus(t, rec, tt.want)
			if tt.want != http.StatusOK {
				wantErrorBody(t, rec)
			}
		})
	}

	// Nothing a rejected save proposed may have landed.
	settings, err := st.AllSettings(ctx)
	if err != nil {
		t.Fatalf("AllSettings: %v", err)
	}
	if got := settings[store.SettingRouteTorrent]; got != store.RouteEmbedded {
		t.Fatalf("%s = %q, want the last accepted value", store.SettingRouteTorrent, got)
	}
	if got := settings[store.SettingRouteUsenet]; got != id {
		t.Fatalf("%s = %q, want the last accepted value", store.SettingRouteUsenet, got)
	}
}
