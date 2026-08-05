package api

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"sort"
	"testing"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/store"
)

type insightEngine struct {
	*stubEngine
	insight    core.DownloadInsight
	insightErr error
}

func (e *insightEngine) Insight(ctx context.Context, id core.DownloadID) (*core.DownloadInsight, error) {
	if e.insightErr != nil {
		return nil, e.insightErr
	}
	return &e.insight, nil
}

type rateEngine struct {
	*stubEngine
	downKbps int64
	upKbps   int64
	err      error
}

func (e *rateEngine) SetGlobalRates(ctx context.Context, downKbps, upKbps int64) error {
	return e.err
}

func (e *rateEngine) SetDownloadRates(ctx context.Context, id core.DownloadID, downKbps, upKbps int64) error {
	if e.err != nil {
		return e.err
	}
	e.downKbps = downKbps
	e.upKbps = upKbps
	return nil
}

type settingsEngineProvider struct {
	*stubEngineProvider
	settings map[string]string
}

func (p *settingsEngineProvider) ApplyEngineSettings(ctx context.Context, settings map[string]string) error {
	p.settings = make(map[string]string, len(settings))
	for key, value := range settings {
		p.settings[key] = value
	}
	return nil
}

func TestDownloadInsight(t *testing.T) {
	t.Run("returns insight shape", func(t *testing.T) {
		engine := &insightEngine{
			stubEngine: &stubEngine{},
			insight: core.DownloadInsight{
				Peers:        []core.PeerInsight{{Addr: "127.0.0.1:51413", Client: "Test", Progress: 0.5, DownRate: 12, UpRate: 3}},
				Trackers:     []core.TrackerInsight{{URL: "https://tracker.example/announce", Status: "working"}},
				Availability: 1.25,
			},
		}
		h, _, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: engine}))

		rec := do(t, h, http.MethodGet, "/api/v1/downloads/hash/insight", "")
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Insight core.DownloadInsight `json:"insight"`
		}
		decodeBody(t, rec, &body)
		if len(body.Insight.Peers) != 1 || body.Insight.Peers[0].Addr != "127.0.0.1:51413" {
			t.Fatalf("peers = %#v, want insight peer", body.Insight.Peers)
		}
		if len(body.Insight.Trackers) != 1 || body.Insight.Trackers[0].Status != "working" {
			t.Fatalf("trackers = %#v, want working tracker", body.Insight.Trackers)
		}
		if body.Insight.Availability != 1.25 {
			t.Fatalf("availability = %v, want 1.25", body.Insight.Availability)
		}
	})

	// The Usenet half of DownloadInsight is omitempty for exactly this reason:
	// a torrent's insight body has to be what it always was, key for key, so
	// the drawer's torrent path is untouched by the Usenet one existing.
	t.Run("carries no usenet keys for a torrent", func(t *testing.T) {
		engine := &insightEngine{
			stubEngine: &stubEngine{},
			insight: core.DownloadInsight{
				Peers:        []core.PeerInsight{{Addr: "127.0.0.1:51413"}},
				Trackers:     []core.TrackerInsight{{URL: "https://tracker.example/announce"}},
				Availability: 1.25,
			},
		}
		h, _, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: engine}))

		rec := do(t, h, http.MethodGet, "/api/v1/downloads/hash/insight", "")
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Insight map[string]any `json:"insight"`
		}
		decodeBody(t, rec, &body)

		keys := make([]string, 0, len(body.Insight))
		for k := range body.Insight {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		want := []string{"availability", "peers", "trackers"}
		if !reflect.DeepEqual(keys, want) {
			t.Fatalf("insight keys = %v, want exactly %v", keys, want)
		}
	})

	// And a Usenet engine's insight carries the file half and no peer chatter.
	t.Run("carries the file breakdown for a usenet download", func(t *testing.T) {
		engine := &insightEngine{
			stubEngine: &stubEngine{},
			insight: core.DownloadInsight{
				Peers:    []core.PeerInsight{},
				Trackers: []core.TrackerInsight{},
				Files: []core.UsenetFileInsight{
					{Name: "movie.mkv", Segments: 10, SegmentsDone: 4},
					{Name: "movie.nfo", Segments: 1, SegmentsDone: 1, Complete: true},
				},
				FilesComplete:   1,
				Segments:        11,
				SegmentsDone:    5,
				DamagedSegments: 2,
				DamagedFiles:    []string{"movie.mkv"},
			},
		}
		h, _, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: engine}))

		rec := do(t, h, http.MethodGet, "/api/v1/downloads/nzo/insight", "")
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Insight core.DownloadInsight `json:"insight"`
		}
		decodeBody(t, rec, &body)

		if len(body.Insight.Files) != 2 || body.Insight.Files[0].Name != "movie.mkv" ||
			body.Insight.Files[0].SegmentsDone != 4 {
			t.Fatalf("files = %#v, want the per-file segment counts", body.Insight.Files)
		}
		if body.Insight.SegmentsDone != 5 || body.Insight.FilesComplete != 1 {
			t.Fatalf("insight = %#v, want the aggregate counts", body.Insight)
		}
		if body.Insight.DamagedSegments != 2 || len(body.Insight.DamagedFiles) != 1 {
			t.Fatalf("insight = %#v, want the repair detail", body.Insight)
		}
	})

	t.Run("returns 503 without engine", func(t *testing.T) {
		h, _, _ := newTestServer(t)
		rec := do(t, h, http.MethodGet, "/api/v1/downloads/hash/insight", "")
		wantStatus(t, rec, http.StatusServiceUnavailable)
		wantErrorBody(t, rec)
	})

	t.Run("returns 404 for unknown engine download", func(t *testing.T) {
		engine := &insightEngine{stubEngine: &stubEngine{}, insightErr: download.ErrNotFound}
		h, _, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: engine}))
		rec := do(t, h, http.MethodGet, "/api/v1/downloads/missing/insight", "")
		wantStatus(t, rec, http.StatusNotFound)
		wantErrorBody(t, rec)
	})
}

// retryEngine is an engine that can retry, so the capability-gated route has
// something to reach.
type retryEngine struct {
	*stubEngine
	retried []core.DownloadID
	err     error
}

func (e *retryEngine) Retry(ctx context.Context, id core.DownloadID) error {
	if e.err != nil {
		return e.err
	}
	e.retried = append(e.retried, id)
	return nil
}

// POST /downloads/{id}/retry, and the three ways it says no. The status codes
// are the contract the queue reads: 400 is "this engine cannot", 409 is "this
// download has nothing to retry", and they mean different things to a user.
func TestRetryDownload(t *testing.T) {
	t.Run("retries through the engine", func(t *testing.T) {
		engine := &retryEngine{stubEngine: &stubEngine{}}
		h, _, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: engine}))

		rec := do(t, h, http.MethodPost, "/api/v1/downloads/u-abc/retry", "")
		wantStatus(t, rec, http.StatusNoContent)
		if len(engine.retried) != 1 || engine.retried[0] != "u-abc" {
			t.Fatalf("retried = %v, want [u-abc]", engine.retried)
		}
	})

	// The embedded torrent engine is exactly this case: its failures are about
	// the swarm and Resume already covers them, so it does not implement the
	// capability and the route says so rather than pretending.
	t.Run("400 when the engine cannot retry", func(t *testing.T) {
		h, _, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: &stubEngine{}}))
		rec := do(t, h, http.MethodPost, "/api/v1/downloads/hash/retry", "")
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
	})

	t.Run("409 when the download has not failed", func(t *testing.T) {
		engine := &retryEngine{stubEngine: &stubEngine{}, err: download.ErrNotRetryable}
		h, _, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: engine}))
		rec := do(t, h, http.MethodPost, "/api/v1/downloads/u-abc/retry", "")
		wantStatus(t, rec, http.StatusConflict)
		wantErrorBody(t, rec)
	})

	t.Run("404 for an unknown download", func(t *testing.T) {
		engine := &retryEngine{stubEngine: &stubEngine{}, err: download.ErrNotFound}
		h, _, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: engine}))
		rec := do(t, h, http.MethodPost, "/api/v1/downloads/missing/retry", "")
		wantStatus(t, rec, http.StatusNotFound)
		wantErrorBody(t, rec)
	})

	t.Run("503 without an engine", func(t *testing.T) {
		h, _, _ := newTestServer(t)
		rec := do(t, h, http.MethodPost, "/api/v1/downloads/u-abc/retry", "")
		wantStatus(t, rec, http.StatusServiceUnavailable)
		wantErrorBody(t, rec)
	})
}

func TestSetDownloadLimitsPersistsAndApplies(t *testing.T) {
	engine := &rateEngine{stubEngine: &stubEngine{}}
	h, st, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: engine}))
	row := &core.Download{Engine: "stub", EngineID: "hash", Title: "Example"}
	if err := st.UpsertDownload(context.Background(), row); err != nil {
		t.Fatalf("UpsertDownload: %v", err)
	}

	rec := do(t, h, http.MethodPut, "/api/v1/downloads/hash/limits", `{"max_down_kbps":512,"max_up_kbps":64}`)
	wantStatus(t, rec, http.StatusNoContent)
	if engine.downKbps != 512 || engine.upKbps != 64 {
		t.Fatalf("engine limits = %d/%d KB/s, want 512/64", engine.downKbps, engine.upKbps)
	}
	stored, err := st.GetDownloadByEngineID(context.Background(), "hash")
	if err != nil {
		t.Fatalf("GetDownloadByEngineID: %v", err)
	}
	if stored.MaxDownRate != 512*1024 || stored.MaxUpRate != 64*1024 {
		t.Fatalf("stored rates = %d/%d B/s, want %d/%d", stored.MaxDownRate, stored.MaxUpRate, 512*1024, 64*1024)
	}
}

func TestDownloadInsightEngineErrorIsNotNotFound(t *testing.T) {
	engine := &insightEngine{stubEngine: &stubEngine{}, insightErr: errors.New("connection failed")}
	h, _, _ := newTestServer(t, WithEngine(&stubEngineProvider{engine: engine}))
	rec := do(t, h, http.MethodGet, "/api/v1/downloads/hash/insight", "")
	wantStatus(t, rec, http.StatusBadGateway)
	wantErrorBody(t, rec)
}

// The concurrency caps round-trip like any other setting, and every one of
// them is a count: negative or non-numeric is a ceiling nothing could be under.
func TestPutSettingsConcurrencyCaps(t *testing.T) {
	t.Run("round-trips every cap", func(t *testing.T) {
		h, st, _ := newTestServer(t)
		rec := do(t, h, http.MethodPut, "/api/v1/settings",
			`{"max_concurrent_downloads":"3","embedded_torrent_max_concurrent":"2","embedded_usenet_max_concurrent":"1"}`)
		wantStatus(t, rec, http.StatusOK)

		settings, err := st.AllSettings(context.Background())
		if err != nil {
			t.Fatalf("AllSettings: %v", err)
		}
		for key, want := range map[string]string{
			store.SettingMaxConcurrentDownloads:       "3",
			store.SettingEmbeddedTorrentMaxConcurrent: "2",
			store.SettingEmbeddedUsenetMaxConcurrent:  "1",
		} {
			if settings[key] != want {
				t.Errorf("%s = %q, want %q", key, settings[key], want)
			}
		}
	})

	// Zero is unlimited and has to stay writable: it is how a user turns a cap
	// back off without the setting disappearing.
	t.Run("accepts zero", func(t *testing.T) {
		h, _, _ := newTestServer(t)
		rec := do(t, h, http.MethodPut, "/api/v1/settings", `{"max_concurrent_downloads":"0"}`)
		wantStatus(t, rec, http.StatusOK)
	})

	t.Run("rejects a cap that is not a count", func(t *testing.T) {
		for _, body := range []string{
			`{"max_concurrent_downloads":"-1"}`,
			`{"max_concurrent_downloads":"lots"}`,
			`{"embedded_torrent_max_concurrent":"-2"}`,
			`{"embedded_usenet_max_concurrent":"2.5"}`,
		} {
			h, st, _ := newTestServer(t)
			rec := do(t, h, http.MethodPut, "/api/v1/settings", body)
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)

			settings, err := st.AllSettings(context.Background())
			if err != nil {
				t.Fatalf("AllSettings: %v", err)
			}
			if len(settings) != 0 {
				t.Fatalf("%s wrote %v, want nothing", body, settings)
			}
		}
	})
}

func TestPutSettingsAppliesEngineSettings(t *testing.T) {
	provider := &settingsEngineProvider{stubEngineProvider: &stubEngineProvider{engine: &stubEngine{}}}
	h, _, _ := newTestServer(t, WithEngine(provider))

	rec := do(t, h, http.MethodPut, "/api/v1/settings", `{"engine_max_down_kbps":"2048","engine_seed_ratio":"1.25"}`)
	wantStatus(t, rec, http.StatusOK)
	if provider.settings["engine_max_down_kbps"] != "2048" || provider.settings["engine_seed_ratio"] != "1.25" {
		t.Fatalf("applied settings = %#v, want updated engine settings", provider.settings)
	}
}
