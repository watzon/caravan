package jellyfin

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// JobKind is the durable job kind a library change turns into (SPEC §7).
const JobKind = "jellyfin_scan"

// EventCategory tags the handoff's activity-feed entries.
const EventCategory = "jellyfin"

// jobPayload is the only payload this kind ever carries. "Tell Jellyfin to look
// again" has no arguments, and a constant payload is what makes HasOpenJob a
// coalescing check rather than a per-import one.
const jobPayload = "{}"

// DefaultCoalesceWindow is how long a queued scan waits before it runs. It is a
// debounce, not a delay for its own sake: a manual match of a season's worth of
// parked files fires one notification per file, and every one of them inside
// this window collapses into the single job already waiting. Jellyfin's own
// scan is the expensive part, so trading a few seconds of latency for one scan
// instead of twenty is the right side of that trade.
const DefaultCoalesceWindow = 20 * time.Second

// Config is the Jellyfin half of the settings table.
type Config struct {
	URL     string `json:"url"`
	APIKey  string `json:"api_key"`
	Enabled bool   `json:"enabled"`
}

// Ready reports whether a handoff can actually be attempted. An enabled
// integration with no URL is a half-finished settings form, not an error.
func (c Config) Ready() bool { return c.Enabled && c.URL != "" }

// Service owns the playback handoff: reading its configuration, queueing a scan
// when the library changes, and running the queued scan.
type Service struct {
	st  *store.Store
	hc  *http.Client
	log *slog.Logger
	// window is DefaultCoalesceWindow, as a field so tests can queue work that
	// is eligible immediately instead of sleeping through the debounce.
	window time.Duration
}

// NewService builds the service. A nil hc gets a client with DefaultTimeout.
func NewService(st *store.Store, hc *http.Client, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: st, hc: hc, log: log, window: DefaultCoalesceWindow}
}

// Config reads the current configuration. A key that was never set reads as its
// zero value, so an unconfigured Caravan reports "disabled" rather than failing.
func (s *Service) Config(ctx context.Context) (Config, error) {
	url, err := s.setting(ctx, store.SettingJellyfinURL)
	if err != nil {
		return Config{}, err
	}
	key, err := s.setting(ctx, store.SettingJellyfinAPIKey)
	if err != nil {
		return Config{}, err
	}
	enabled, err := s.setting(ctx, store.SettingJellyfinEnabled)
	if err != nil {
		return Config{}, err
	}
	on, _ := strconv.ParseBool(enabled)
	return Config{URL: url, APIKey: key, Enabled: on}, nil
}

func (s *Service) setting(ctx context.Context, key string) (string, error) {
	value, err := s.st.GetSetting(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return value, err
}

// LibraryChanged satisfies library.Notifier: it is called after files land in
// the library, and its whole job is to record that a scan is owed.
//
// It never talks to Jellyfin. The HTTP call belongs to Handle, behind the job
// queue, so an import cannot be slowed down by a media server that is asleep
// and cannot be failed by one that is gone — and a Caravan that is killed
// between the import and the scan still owes the scan when it comes back
// (SPEC §7).
func (s *Service) LibraryChanged(ctx context.Context) error {
	cfg, err := s.Config(ctx)
	if err != nil {
		return err
	}
	if !cfg.Ready() {
		return nil
	}

	open, err := s.st.HasOpenJob(ctx, JobKind, jobPayload)
	if err != nil {
		return err
	}
	if open {
		// A scan is already owed. Queueing a second one would ask Jellyfin to
		// do the same work twice for the same burst of imports.
		return nil
	}
	return s.st.EnqueueJob(ctx, &core.Job{
		Kind:     JobKind,
		Payload:  jobPayload,
		RunAfter: time.Now().Add(s.window),
	})
}

// Handle runs one queued scan. It matches automation.Handler; the store
// argument is ignored because the service holds its own handle.
//
// Idempotency is free here: the request is "rescan", so running it twice costs
// Jellyfin a redundant scan and nothing else. Configuration is re-read at run
// time rather than carried in the payload, so a handoff switched off between
// the import and the job is simply not made.
func (s *Service) Handle(ctx context.Context, _ *store.Store, _ json.RawMessage) error {
	cfg, err := s.Config(ctx)
	if err != nil {
		return err
	}
	if !cfg.Ready() {
		return nil
	}

	if err := NewClient(cfg.URL, cfg.APIKey, s.hc).RefreshLibrary(ctx); err != nil {
		// The error is handed back so the queue's backoff decides when to try
		// again; the event is what tells the user their handoff is broken
		// without making them read a log file (SPEC §13).
		s.log.Warn("jellyfin: library scan trigger failed", "url", cfg.URL, "error", err)
		if evErr := s.st.InsertEvent(ctx, &core.Event{
			Level:    core.EventLevelWarn,
			Category: EventCategory,
			Message:  "Jellyfin library scan could not be triggered",
			Detail:   err.Error(),
		}); evErr != nil {
			s.log.Error("jellyfin: record scan failure", "error", evErr)
		}
		return err
	}

	// Deliberately a log line and not an event: every import already writes an
	// "Imported X" entry, and a successful handoff that doubles the feed is
	// noise rather than news.
	s.log.Info("jellyfin: library scan triggered", "url", cfg.URL)
	return nil
}
