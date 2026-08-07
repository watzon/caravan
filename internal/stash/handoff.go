package stash

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// Job kinds the handoff turns library changes into (SPEC §7).
//
// Two kinds rather than one because they coalesce differently. A burst of
// scene imports owes *one* scan — the payload is constant, so HasOpenJob
// collapses the burst — but it owes one identity push *per scene*, and those
// pushes must survive a restart individually. Folding them together would mean
// either re-pushing every scene the scan ever touched or losing the ones that
// were still queued.
const (
	// ScanJobKind is the scoped metadataScan of the adult library root.
	ScanJobKind = "stash_scan"
	// IdentifyJobKind is one scene's identity push.
	IdentifyJobKind = "stash_identify"
)

// EventCategory tags the handoff's activity-feed entries.
const EventCategory = "stash"

// DefaultCoalesceWindow is how long a queued scan waits before it runs. It is a
// debounce, not a delay for its own sake: a download carrying a dozen scenes
// fires one notification, but a burst of manual imports fires one each, and
// every one inside this window collapses into the job already waiting.
const DefaultCoalesceWindow = 20 * time.Second

// DefaultIdentifyDelay is how much of a head start the scan gets over the first
// identity push. Stash indexes in the background, so a push issued the instant
// the scan is accepted would find nothing and burn a retry on a file that was
// always going to arrive. It is a head start rather than a guarantee — the
// retry path below is what actually handles a slow scan.
const DefaultIdentifyDelay = 45 * time.Second

// DefaultRetryWindow is the wall clock a scan or an identity push keeps asking
// across before it gives up and says so in the feed.
//
// It exists because both things this handoff waits on are slower than the job
// queue's own budget. store.JobMaxAttempts failures of one job are spent inside
// nine minutes (store.RetryDelay doubling from thirty seconds), while a
// metadataScan of a household adult library routinely runs longer than that —
// as does a Stash host being rebooted or upgraded. A job that died on the
// queue's budget is a scene that is in Caravan's library, absent from Stash,
// and has nothing left to re-trigger it: exactly what PLAN phase 11's second
// and third acceptance criteria promise cannot happen.
//
// So the handoff owns its own schedule. A run that cannot finish *yet* re-arms
// itself with a fresh RunAfter instead of failing, and this ceiling is what
// keeps "not yet" from being forever.
const DefaultRetryWindow = 2 * time.Hour

// Config is the Stash half of the settings table.
type Config struct {
	URL     string `json:"url"`
	APIKey  string `json:"api_key"`
	Enabled bool   `json:"enabled"`
}

// Ready reports whether a handoff can actually be attempted. An enabled
// integration with no URL is a half-finished settings form, not an error.
func (c Config) Ready() bool { return c.Enabled && c.URL != "" }

// Health is the handoff's last known verdict on whether the Stash server can be
// *reached*, which is what the status banner renders. A zero Health means
// "nothing has failed", which is also what a fresh process reports: the handoff
// does not probe, it remembers.
//
// Reachability only, deliberately. A server that answers "no scene at that
// path", "more than one scene at that path", or "your API key is wrong" is a
// server that is up, and raising an outage banner over it tells the user to go
// looking for a network problem that does not exist. Those answers are activity
// feed entries; this is the banner, and the banner means the server did not
// answer at all.
//
// It is the same model an unreachable download client gets (SPEC §13): the work
// is queued and delivers when the server comes back, and the banner is how the
// user learns why their scenes have not appeared yet. It never blocks an
// import.
type Health struct {
	// Error is why the last attempt could not reach the server, empty while the
	// handoff is healthy.
	Error string
	// Since is when the current failure started — not when it was last seen —
	// so a banner can say "unreachable for twenty minutes".
	Since time.Time
}

// Unreachable reports whether the last attempt failed.
func (h Health) Unreachable() bool { return h.Error != "" }

// Service owns the Stash handoff: reading its configuration, queueing a scoped
// scan and the identity pushes that follow it, and running both.
type Service struct {
	st  *store.Store
	hc  *http.Client
	log *slog.Logger

	// window and identifyDelay are DefaultCoalesceWindow and
	// DefaultIdentifyDelay, as fields so tests can queue work that is claimable
	// immediately instead of sleeping through the debounce.
	window        time.Duration
	identifyDelay time.Duration

	// retryWindow and retryDelay are DefaultRetryWindow and store.RetryDelay,
	// as fields for the same reason: a test can drive the whole re-arm and
	// give-up path without waiting out a real backoff.
	retryWindow time.Duration
	retryDelay  func(attempt int) time.Duration

	mu     sync.Mutex
	health Health
}

// Option configures a Service at construction, in the idiom library.Option and
// automation.Option use.
type Option func(*Service)

// WithSchedule overrides the two delays that space a handoff out: how long a
// queued scan debounces, and how much of a head start it gets over the first
// identity push.
//
// It exists so a caller that owns the whole cycle — a test driving a real
// import through a real automation.Runner, for the acceptance criterion that
// says one adult import fires exactly one scoped scan — can run that cycle
// without waiting out a debounce measured in real seconds. The defaults are the
// only values production uses.
func WithSchedule(coalesce, identify time.Duration) Option {
	return func(s *Service) {
		s.window = coalesce
		s.identifyDelay = identify
	}
}

// NewService builds the service. A nil hc gets a client with DefaultTimeout.
func NewService(st *store.Store, hc *http.Client, log *slog.Logger, opts ...Option) *Service {
	if log == nil {
		log = slog.Default()
	}
	s := &Service{
		st:            st,
		hc:            hc,
		log:           log,
		window:        DefaultCoalesceWindow,
		identifyDelay: DefaultIdentifyDelay,
		retryWindow:   DefaultRetryWindow,
		// The same curve the queue would have used, reused rather than
		// reinvented: thirty seconds doubling to a thirty-minute cap. What
		// changes is who owns the ceiling, not how the waiting is spaced.
		retryDelay: store.RetryDelay,
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// Config reads the current configuration. A key that was never set reads as its
// zero value, so an unconfigured Caravan reports "disabled" rather than failing.
func (s *Service) Config(ctx context.Context) (Config, error) {
	values, err := s.st.AllSettings(ctx)
	if err != nil {
		return Config{}, err
	}
	enabled, _ := strconv.ParseBool(strings.TrimSpace(values[store.SettingStashEnabled]))
	return Config{
		URL:     strings.TrimSpace(values[store.SettingStashURL]),
		APIKey:  strings.TrimSpace(values[store.SettingStashAPIKey]),
		Enabled: enabled,
	}, nil
}

// active resolves the configuration a handoff would run with right now, and
// reports whether it may run at all.
//
// The adult module's own switch is checked first and independently of the
// handoff's. Stash is an adult-module feature: with the module off there is no
// adult library to hand over, and a Caravan that still talked to a Stash server
// would be making a request the user believes they turned off (PLAN phase 9's
// zero-traffic rule, applied to the other end of the pipe). Both switches are
// read at run time rather than carried in a job payload, so a handoff switched
// off between the import and the job is simply not made.
func (s *Service) active(ctx context.Context) (Config, bool, error) {
	on, err := s.st.AdultEnabled(ctx)
	if err != nil {
		return Config{}, false, err
	}
	if !on {
		return Config{}, false, nil
	}
	cfg, err := s.Config(ctx)
	if err != nil {
		return Config{}, false, err
	}
	return cfg, cfg.Ready(), nil
}

// Health is the last known verdict on the Stash server.
func (s *Service) Health() Health {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.health
}

// ResetHealth forgets the last verdict.
//
// It is what the settings screen calls when the user changes the answer the
// verdict was about: a successful test-connection, a new URL, a handoff switched
// off. Health is remembered rather than probed, so without this a banner raised
// by a server the user has since fixed, replaced or disabled would survive until
// the next handoff happened to run — and, before the handoff re-armed itself,
// for the life of the process.
func (s *Service) ResetHealth() { s.markReachable() }

// markUnreachable records a failed attempt, keeping the first failure's
// timestamp so "since" means since the outage started. It reports whether this
// is the start of an outage rather than another attempt inside one, so a
// handoff that re-arms itself for two hours writes one feed entry and not a
// dozen.
func (s *Service) markUnreachable(reason string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	fresh := s.health.Error == ""
	if fresh {
		s.health.Since = time.Now()
	}
	s.health.Error = reason
	return fresh
}

// markReachable clears the failure, and reports whether it was set — so the
// caller can log a recovery once rather than on every success.
func (s *Service) markReachable() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	recovered := s.health.Error != ""
	s.health = Health{}
	return recovered
}

// retryState is the half of both payloads that carries the handoff's own retry
// schedule: which attempt this is, and the wall clock it stops at.
//
// It lives in the payload because a handler is handed its payload and nothing
// else — not its own row, not its attempt count — so a run that wants to re-arm
// itself has to write down what the next run needs to know. Both fields are
// omitted when zero, which keeps a *fresh* job's payload the string it has
// always been and keeps a queued job readable in the activity feed.
type retryState struct {
	Attempt int       `json:"attempt,omitempty"`
	Until   time.Time `json:"retry_until,omitzero"`
}

// next is the state one attempt later. The ceiling is started on the first
// re-arm rather than at enqueue time, so the window measures how long the
// handoff has been *unable* to finish, not how long the job sat in a debounce.
func (r retryState) next(now time.Time, window time.Duration) retryState {
	out := retryState{Attempt: r.Attempt + 1, Until: r.Until}
	if out.Until.IsZero() {
		out.Until = now.Add(window)
	}
	return out
}

// spent reports whether the ceiling has passed.
func (r retryState) spent(now time.Time) bool {
	return !r.Until.IsZero() && !now.Before(r.Until)
}

// scanPayload is ScanJobKind's payload. "Rescan the adult root" has no
// arguments, so it carries nothing but the retry state — which means a fresh
// scan still encodes as `{}`, and a re-armed one differs only in the
// bookkeeping its successor needs.
type scanPayload struct {
	retryState
}

// identifyPayload is IdentifyJobKind's payload: which scene to push.
//
// It carries an id and the retry state, and nothing else. Everything the push
// needs — the title, the stash-box id, the studio, the performers, the file's
// path — is re-read from the database when the job runs, so a scene renamed or
// superseded between the import and the push is pushed as it is now rather than
// as it was.
type identifyPayload struct {
	EpisodeID int64 `json:"episode_id"`
	retryState
}

// AdultLibraryChanged satisfies library.AdultNotifier: it is called after scenes
// land in the adult library, and its whole job is to record that a scan and a
// set of identity pushes are owed.
//
// It never talks to Stash. The HTTP calls belong to the handlers, behind the job
// queue, so an import cannot be slowed down by a media server that is asleep and
// cannot be failed by one that is gone — and a Caravan killed between the import
// and the scan still owes both when it comes back (SPEC §7).
func (s *Service) AdultLibraryChanged(ctx context.Context, episodeIDs []int64) error {
	_, ok, err := s.active(ctx)
	if err != nil || !ok {
		return err
	}

	if err := s.queueScan(ctx, scanPayload{}, s.window); err != nil {
		return err
	}
	// The pushes wait for the scan as well as for the debounce: Stash has to
	// have seen the file before there is anything to update.
	for _, id := range episodeIDs {
		if id <= 0 {
			continue
		}
		if err := s.queueIdentify(ctx, identifyPayload{EpisodeID: id}, s.window+s.identifyDelay); err != nil {
			return err
		}
	}
	return nil
}

// queueScan enqueues a scan unless one is already *waiting* to run.
//
// Pending rather than open, and that distinction is the whole comment. A scan
// that has already been claimed sent Stash its path list before the file that
// just landed existed, so counting it as covering that file is how a scene ends
// up in Caravan's library and permanently absent from Stash — the identity push
// then finds nothing, and nothing else will ever ask Stash to look again. A
// redundant scoped scan costs Stash one walk of one directory; a skipped one
// costs a scene.
func (s *Service) queueScan(ctx context.Context, p scanPayload, delay time.Duration) error {
	pending, err := s.pendingJobs(ctx, ScanJobKind)
	if err != nil {
		return err
	}
	if len(pending) > 0 {
		return nil
	}
	return s.enqueue(ctx, ScanJobKind, p, delay)
}

// queueIdentify enqueues one scene's push unless that scene already has one
// waiting.
//
// The subject is the episode id rather than the whole payload string, which is
// why this reads jobs instead of asking HasOpenJob. A re-armed push carries its
// retry state beside the id, so an exact-payload match would answer "no job"
// for precisely the jobs it exists to collapse and stack a second push behind
// the first. Same reasoning, and the same reader, as core.JobSyncSitePayload's.
func (s *Service) queueIdentify(ctx context.Context, p identifyPayload, delay time.Duration) error {
	pending, err := s.pendingJobs(ctx, IdentifyJobKind)
	if err != nil {
		return err
	}
	for _, job := range pending {
		var queued identifyPayload
		if err := json.Unmarshal([]byte(job.Payload), &queued); err != nil {
			// A payload this process cannot read is not evidence that the scene
			// is covered, so it is not allowed to suppress a push.
			continue
		}
		if queued.EpisodeID == p.EpisodeID {
			return nil
		}
	}
	return s.enqueue(ctx, IdentifyJobKind, p, delay)
}

// pendingJobs returns the jobs of one kind that are queued but not yet claimed.
func (s *Service) pendingJobs(ctx context.Context, kind string) ([]core.Job, error) {
	jobs, err := s.st.OpenJobsByKind(ctx, kind)
	if err != nil {
		return nil, err
	}
	out := jobs[:0]
	for _, job := range jobs {
		if job.State == core.JobStatePending {
			out = append(out, job)
		}
	}
	return out, nil
}

// enqueue puts one job on the queue with its payload encoded.
func (s *Service) enqueue(ctx context.Context, kind string, payload any, delay time.Duration) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("stash: encode %s payload: %w", kind, err)
	}
	return s.st.EnqueueJob(ctx, &core.Job{
		Kind:     kind,
		Payload:  string(raw),
		RunAfter: time.Now().Add(delay),
	})
}

// HandleScan runs one queued scan. It matches automation.Handler; the store
// argument is ignored because the service holds its own handle.
//
// Idempotency is free here: the request is "rescan this path", so running it
// twice costs Stash a redundant walk and nothing else.
func (s *Service) HandleScan(ctx context.Context, _ *store.Store, payload json.RawMessage) error {
	var p scanPayload
	if len(payload) > 0 {
		if err := json.Unmarshal(payload, &p); err != nil {
			s.log.Warn("stash: unreadable scan payload", "error", err)
			return nil
		}
	}

	cfg, ok, err := s.active(ctx)
	if err != nil {
		return err
	}
	if !ok {
		// A handoff that is switched off cannot be unreachable. This is the one
		// moment the service learns the user's answer to a banner was "turn it
		// off", and clearing here is what stops that banner outliving the thing
		// it was about — health is remembered, not probed.
		s.markReachable()
		return nil
	}
	roots, err := s.adultRoots(ctx)
	if err != nil {
		return err
	}
	if len(roots) == 0 {
		// No storage root means no library on disk to point at. There is
		// nothing to retry into, so this is a log line rather than a failed job.
		s.log.Warn("stash: no storage root configured, skipping scan")
		return nil
	}

	if _, err := NewClient(cfg.URL, cfg.APIKey, s.hc).Scan(ctx, roots); err != nil {
		if !s.note(ctx, "Stash library scan could not be triggered", cfg.URL, err) {
			// A server that answered and refused will refuse the same request
			// again. Asking every thirty seconds for two hours would fill the
			// feed with one mistake.
			return nil
		}
		next := p.next(time.Now(), s.retryWindow)
		return s.rearm(ctx, ScanJobKind, scanPayload{retryState: next}, next,
			"Stash library scan gave up", err.Error())
	}
	s.recovered(cfg.URL)
	// Deliberately a log line and not an event: every import already writes an
	// "Imported X" entry, and a successful handoff that doubles the feed is
	// noise rather than news.
	s.log.Info("stash: scoped library scan triggered", "url", redactURL(cfg.URL), "paths", strings.Join(roots, ", "))
	return nil
}

// HandleIdentify runs one queued identity push.
//
// The push is what makes this more than a scan trigger: Caravan already knows
// the scene's stash-box id, title, studio and performers, because phase 9 read
// them from a stash-box — the same vocabulary Stash's own identify step uses. So
// the scene arrives identified instead of as an untagged file.
//
// The expected case is that the scan has not indexed the file yet, which Stash
// answers with ErrSceneNotFound. That is not a failure of this job — it is "not
// yet" — so the run re-arms itself with a fresh RunAfter instead of spending one
// of the queue's five attempts on it. DefaultRetryWindow is what bounds the
// asking; a scan that never indexes the path ends in a warn event rather than
// in silence.
func (s *Service) HandleIdentify(ctx context.Context, _ *store.Store, payload json.RawMessage) error {
	var p identifyPayload
	if err := json.Unmarshal(payload, &p); err != nil {
		// A payload this process cannot read will not become readable on a
		// retry; failing the job forever would be noise.
		s.log.Warn("stash: unreadable identify payload", "error", err)
		return nil
	}

	cfg, ok, err := s.active(ctx)
	if err != nil {
		return err
	}
	if !ok {
		s.markReachable() // see HandleScan
		return nil
	}

	push, ok, err := s.scenePush(ctx, p.EpisodeID)
	if err != nil || !ok {
		return err
	}

	client := NewClient(cfg.URL, cfg.APIKey, s.hc)
	scene, err := client.SceneByPath(ctx, push.path)
	if errors.Is(err, ErrSceneNotFound) {
		// The ordinary "the scan is still running" answer, and proof the server
		// is up: a round trip that ends in "no such scene" clears an outage as
		// convincingly as one that ends in a scene.
		s.recovered(cfg.URL)
		s.log.Info("stash: scene not indexed yet, will retry", "path", push.path)
		next := p.next(time.Now(), s.retryWindow)
		return s.rearm(ctx, IdentifyJobKind,
			identifyPayload{EpisodeID: p.EpisodeID, retryState: next}, next,
			"Stash never indexed an imported scene",
			"no scene at "+push.path+"; the identity push gave up waiting for the scan")
	}
	if errors.Is(err, ErrAmbiguousScene) {
		// Two scenes share the path, so there is no scene to push onto. Asking
		// again cannot change the answer, and guessing is what SceneByPath
		// refuses to do — so this is one feed entry and no retry, and emphatically
		// not a banner: the server answered.
		s.recovered(cfg.URL)
		return s.refuse(ctx, "Stash scene lookup was ambiguous", cfg.URL, err)
	}
	if err != nil {
		if !s.note(ctx, "Stash scene lookup failed", cfg.URL, err) {
			return nil
		}
		next := p.next(time.Now(), s.retryWindow)
		return s.rearm(ctx, IdentifyJobKind,
			identifyPayload{EpisodeID: p.EpisodeID, retryState: next}, next,
			"Stash scene lookup gave up", err.Error())
	}

	update := SceneUpdate{
		ID:       scene.ID,
		Title:    push.title,
		StashIDs: push.stashIDs,
		URLs:     push.urls,
		Date:     push.date,
	}
	// Studio and performers are best effort, and deliberately so: the title and
	// the stash-box id are facts Caravan owns, while a studio or performer row
	// is a thing Stash owns and may already have under a name Caravan cannot
	// predict. A lookup that fails is logged and skipped, leaving whatever Stash
	// already had, rather than failing a push whose important half would have
	// succeeded.
	update.StudioID = s.resolveStudio(ctx, client, push)
	update.PerformerIDs = s.resolvePerformers(ctx, client, push.performers)

	if err := client.UpdateScene(ctx, update); err != nil {
		if !s.note(ctx, "Stash scene identity could not be pushed", cfg.URL, err) {
			return nil
		}
		next := p.next(time.Now(), s.retryWindow)
		return s.rearm(ctx, IdentifyJobKind,
			identifyPayload{EpisodeID: p.EpisodeID, retryState: next}, next,
			"Stash scene identity push gave up", err.Error())
	}
	s.recovered(cfg.URL)
	s.log.Info("stash: scene identity pushed",
		"scene", scene.ID, "title", push.title, "performers", len(update.PerformerIDs))
	return nil
}

// scenePush is everything one identity push needs, read out of the database at
// run time.
type scenePush struct {
	// path is the file's absolute path — the one address Stash and Caravan
	// share.
	path       string
	title      string
	stashIDs   []StashID
	studio     string
	studioIDs  []StashID
	performers []string
	urls       []string
	date       string
}

// scenePush assembles the push for one episode, reporting ok=false when there is
// nothing to push. A scene that was deleted, or whose file is gone, is not an
// error: the push is simply moot.
func (s *Service) scenePush(ctx context.Context, episodeID int64) (scenePush, bool, error) {
	episode, err := s.st.GetEpisode(ctx, episodeID)
	if errors.Is(err, store.ErrNotFound) {
		return scenePush{}, false, nil
	}
	if err != nil {
		return scenePush{}, false, err
	}

	series, err := s.st.GetSeries(ctx, episode.SeriesID)
	if errors.Is(err, store.ErrNotFound) {
		return scenePush{}, false, nil
	}
	if err != nil {
		return scenePush{}, false, err
	}
	if series.Kind != core.SeriesKindAdult {
		// A queued push for a television episode is a bug upstream, not
		// something to carry out: Stash must never be told about the television
		// library (SPEC §1.2, the exposure rule).
		s.log.Warn("stash: refusing to push a non-adult episode", "episode", episodeID)
		return scenePush{}, false, nil
	}

	files, err := s.st.ListMediaFilesForEpisode(ctx, episodeID)
	if err != nil {
		return scenePush{}, false, err
	}
	if len(files) == 0 {
		return scenePush{}, false, nil
	}
	root, err := s.storageRoot(ctx)
	if err != nil {
		return scenePush{}, false, err
	}
	if root == "" {
		s.log.Warn("stash: no storage root configured, skipping identity push")
		return scenePush{}, false, nil
	}

	endpoint, err := s.endpointForSeries(ctx, series)
	if err != nil {
		return scenePush{}, false, err
	}

	push := scenePush{
		path:  filepath.Join(root, filepath.FromSlash(files[0].Path)),
		title: episode.Title,
	}
	// No endpoint means the instance these ids were minted by is gone, and the
	// file is pushed with NO stash ids at all — not with a guess.
	//
	// A StashIDInput carrying the wrong endpoint is worse than none: it writes
	// into the user's own Stash a claim that box X issued a UUID it never
	// issued, Stash's identify step then trusts it, and undoing it means
	// finding every scene the guess touched. An absent id leaves the scene
	// merely unidentified, which the next push repairs the moment the instance
	// is added back.
	if endpoint != "" {
		if episode.StashID != "" {
			push.stashIDs = []StashID{{Endpoint: endpoint, StashID: episode.StashID}}
		}
		if series.StashID != "" {
			push.studioIDs = []StashID{{Endpoint: endpoint, StashID: series.StashID}}
		}
	}
	if !episode.AirDate.IsZero() {
		push.date = episode.AirDate.Format("2006-01-02")
	}
	if episode.Scene != nil {
		push.studio = episode.Scene.Studio
		push.performers = episode.Scene.Performers
		if episode.Scene.URL != "" {
			push.urls = []string{episode.Scene.URL}
		}
	}
	if push.studio == "" {
		// The site's own title is the studio when the scene row denormalized
		// nothing — which is what a scene imported before the studio field
		// existed looks like.
		push.studio = series.Title
	}
	return push, true, nil
}

// resolveStudio finds or creates the scene's studio, returning "" when it cannot
// be resolved. Best effort by design; see HandleIdentify.
func (s *Service) resolveStudio(ctx context.Context, client *Client, push scenePush) string {
	if push.studio == "" {
		return ""
	}
	id, err := client.StudioByName(ctx, push.studio)
	if err != nil {
		s.log.Warn("stash: studio lookup failed", "studio", push.studio, "error", err)
		return ""
	}
	if id != "" {
		return id
	}
	id, err = client.CreateStudio(ctx, push.studio, push.studioIDs)
	if err != nil {
		s.log.Warn("stash: studio create failed", "studio", push.studio, "error", err)
		return ""
	}
	return id
}

// resolvePerformers finds or creates each credited performer, skipping the ones
// that cannot be resolved. A partial cast is better than no cast, and better
// than a failed push.
func (s *Service) resolvePerformers(ctx context.Context, client *Client, names []string) []string {
	out := make([]string, 0, len(names))
	seenName := make(map[string]bool, len(names))
	seenID := make(map[string]bool, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || seenName[name] {
			continue
		}
		seenName[name] = true

		id, err := client.PerformerByName(ctx, name)
		if err != nil {
			s.log.Warn("stash: performer lookup failed", "performer", name, "error", err)
			continue
		}
		if id == "" {
			if id, err = client.CreatePerformer(ctx, name); err != nil {
				s.log.Warn("stash: performer create failed", "performer", name, "error", err)
				continue
			}
		}
		// Two credited names can resolve to one Stash performer — an alias and
		// a canonical name are exactly that case, and Caravan stores both. The
		// id list has to be a set, or the same performer is attached twice.
		if seenID[id] {
			continue
		}
		seenID[id] = true
		out = append(out, id)
	}
	return out
}

// unreachable reports whether err is evidence the server could not be reached,
// as opposed to an answer Caravan did not like.
//
// Only the first kind belongs in the status banner. A 4xx — a wrong API key, a
// request Stash rejects — and a GraphQL errors array both arrive as *APIError
// with the status the server actually sent, which is the fact that separates
// "your Stash is down" from "your Stash said no". Everything that is not an
// *APIError got no HTTP answer at all: a refused connection, a timeout, a TLS
// failure, a body that was not the document a Stash sends.
func unreachable(err error) bool {
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode >= 500
	}
	return true
}

// note records why an attempt failed and reports whether it is worth asking
// again.
//
// The event is what tells the user their handoff is broken without making them
// read a log file (SPEC §13). For an outage it is written once, at the start,
// because a handoff that re-arms itself for two hours would otherwise write the
// same warning a dozen times for one unplugged cable; the banner is what carries
// it for the duration.
func (s *Service) note(ctx context.Context, message, url string, err error) (retry bool) {
	if !unreachable(err) {
		s.refuse(ctx, message, url, err)
		return false
	}
	if s.markUnreachable(err.Error()) {
		s.event(ctx, message, err.Error())
	}
	s.log.Warn("stash: "+message, "url", redactURL(url), "error", err)
	return true
}

// refuse records an answer from a server that is up and said no. It writes the
// feed entry and returns nil: the health mark, and therefore the outage banner,
// stays out of it.
func (s *Service) refuse(ctx context.Context, message, url string, err error) error {
	s.log.Warn("stash: "+message, "url", redactURL(url), "error", err)
	s.event(ctx, message, err.Error())
	return nil
}

// rearm re-queues a run that could not finish yet, or gives up and says so.
//
// payload is the successor the caller built and next is the state it carries;
// the two kinds have different subjects and only the caller knows what it is
// re-arming. Returning nil is the point: "the scan has not reached this file
// yet" and "Stash is being rebooted" are reasons to ask later, not failures of
// this job, and spending the queue's five attempts on them is what used to leave
// scenes permanently unindexed with nothing in the feed to say so.
func (s *Service) rearm(ctx context.Context, kind string, payload any, next retryState, gaveUp, detail string) error {
	if next.spent(time.Now()) {
		s.log.Warn("stash: "+gaveUp, "attempts", next.Attempt, "detail", detail)
		s.event(ctx, gaveUp, detail)
		return nil
	}
	return s.enqueue(ctx, kind, payload, s.retryDelay(next.Attempt))
}

// event writes one warn entry to the activity feed.
func (s *Service) event(ctx context.Context, message, detail string) {
	if err := s.st.InsertEvent(ctx, &core.Event{
		Level:    core.EventLevelWarn,
		Category: EventCategory,
		Message:  message,
		Detail:   detail,
	}); err != nil {
		s.log.Error("stash: record failure", "error", err)
	}
}

// recovered clears the health mark, logging once when an outage ends.
func (s *Service) recovered(url string) {
	if s.markReachable() {
		s.log.Info("stash: server is reachable again", "url", redactURL(url))
	}
}

// redactURL renders a configured address safely for a log line: scheme and host,
// nothing else.
//
// The address is not obviously a credential, but it can be one. A URL a user
// pastes may carry userinfo (http://user:pass@stash.lan:9999), and Go's HTTP
// client turns that into an Authorization header — so logging the configured
// string verbatim puts a password in the log, which is exactly what the client
// avoids by sending the API key as a header rather than a query parameter
// (SPEC §12). The API layer refuses such a URL on the way in; this is the second
// wall, for one that was already stored.
func redactURL(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Host == "" {
		return "(redacted)"
	}
	return parsed.Scheme + "://" + parsed.Host
}

// adultRoots are the absolute paths of every adult library — the only
// directories a scan is ever pointed at. Plural since 0022: a second adult
// library missed here would simply never be scanned by Stash, so the roots
// come from the rows rather than the seed constant, with the constant as the
// fallback for an install whose rows predate the module.
func (s *Service) adultRoots(ctx context.Context) ([]string, error) {
	root, err := s.storageRoot(ctx)
	if err != nil || root == "" {
		return nil, err
	}
	libs, err := s.st.ListLibrariesByKind(ctx, core.LibraryKindAdult)
	if err != nil {
		return nil, err
	}
	if len(libs) == 0 {
		return []string{filepath.Join(root, filepath.FromSlash(store.AdultLibraryRoot))}, nil
	}
	out := make([]string, 0, len(libs))
	for _, lib := range libs {
		out = append(out, filepath.Join(root, filepath.FromSlash(lib.RootPath)))
	}
	return out, nil
}

func (s *Service) storageRoot(ctx context.Context) (string, error) {
	return s.setting(ctx, store.SettingStorageRoot)
}

// endpointForSeries is which stash-box the ids on this site's rows came from.
//
// It is part of the identity, which is exactly what StashIDInput.endpoint is
// for: a UUID means nothing without the box that issued it, and since 0026 the
// public boxes are forks of one another minting identical UUIDs for different
// records. The instance is read from the ITEM rather than from a setting for
// that reason — a server-wide endpoint could only ever be right for one box.
//
// An empty provider is the legacy instance (`stashbox`), which is what every
// adult row written before instances carries. An instance the owner deleted
// answers "" — see scenePush for what that suppresses.
func (s *Service) endpointForSeries(ctx context.Context, sr *core.Series) (string, error) {
	providerID := sr.Provider
	if providerID == "" {
		providerID = core.ProviderStashbox
	}
	in, err := s.st.GetStashboxInstanceByProviderID(ctx, providerID)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(in.Endpoint), nil
}

func (s *Service) setting(ctx context.Context, key string) (string, error) {
	value, err := s.st.GetSetting(ctx, key)
	if errors.Is(err, store.ErrNotFound) {
		return "", nil
	}
	return strings.TrimSpace(value), err
}
