package usenet

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/usenet/nntp"
	"github.com/watzon/caravan/internal/usenet/nzb"
	"github.com/watzon/caravan/internal/usenet/pipeline"
)

// EngineName is what downloads started by this engine record in their
// `downloads.engine` column.
//
// It is distinct from internal/download's "embedded" because the two are
// different backends holding different downloads, and the column is what a
// restart uses to decide which engine to hand a row back to.
const EngineName = "embedded-usenet"

// nzbTimeout bounds fetching one .nzb. Indexers stall; grabs must not. It
// matches the torrent side's metainfo timeout for the same reason.
const nzbTimeout = 30 * time.Second

// metaDir holds one .nzb per added download, inside the incomplete directory.
//
// The NZB is the download's whole plan — every file, every segment, every
// message-id — and none of it is in the database (the database is a disposable
// cache, SPEC §1.2). Keeping the document beside the data is what lets a
// restart resume a half-finished release instead of re-grabbing it, exactly as
// the torrent engine keeps a metainfo sidecar.
const metaDir = ".caravan"

// defaultPollInterval is how often the engine samples its downloads: the
// resolution of the reported transfer rate and of the progress it persists.
const defaultPollInterval = 2 * time.Second

// handlePrefix marks this engine's download handles.
//
// Handles are stored bare in `downloads.engine_id` and the router probes every
// un-namespaced engine for one, so a handle has to say which engine it belongs
// to by itself. An info hash is 40 hex characters; prefixing these with a
// letter that hex cannot produce means a Usenet handle can never be mistaken
// for a torrent's, without the router's namespacing machinery (which exists for
// external clients, whose small integer handles genuinely do collide).
const handlePrefix = "u"

// admissionRegistrar is the half of the concurrency coordinator this engine
// needs beyond core.Admitter: a way to be told a slot has freed somewhere, so
// it can re-ask for the downloads it is holding back.
type admissionRegistrar interface {
	Register(method string, wake func())
}

// EngineOpts configures the embedded Usenet engine.
type EngineOpts struct {
	// Servers are the news servers to fetch from, in priority order. An empty
	// list is a legal starting state — the engine builds, reports its
	// downloads and refuses new ones with nntp.ErrNoServers until SetServers
	// supplies one.
	Servers []nntp.ServerConfig
	// NNTP tunes the transport (timeouts, retry policy, TLS roots).
	NNTP nntp.Options
	// Concurrency is how many segments are fetched at once per download. Zero
	// uses pipeline.DefaultConcurrency.
	Concurrency int
	// Store persists the queue across restarts. It is the same seam the
	// torrent engine uses, so one implementation serves both. A nil Store
	// means downloads do not survive a restart.
	Store download.Persistence
	// PollInterval overrides defaultPollInterval.
	PollInterval time.Duration
	// HTTPClient fetches .nzb files. Nil uses a client with a bounded timeout.
	HTTPClient *http.Client
	// Logger receives the engine's background failures, which have no caller
	// to return an error to. Nil discards them.
	Logger *slog.Logger
	// Paused starts every restored download paused, for portable mode
	// (SPEC §2.3).
	Paused bool
	// FreeSpace measures free bytes for the disk preflight. Nil uses the
	// platform implementation.
	FreeSpace func(path string) (int64, error)
	// SkipSpaceCheck turns the preflight off. Tests that stage kilobyte
	// releases on a full CI disk want it; production does not.
	SkipSpaceCheck bool
	// Admitter decides whether a download may run, so a ceiling across every
	// engine can exist. Nil is unlimited and is the path that predates
	// concurrency caps: nothing is asked and no download is held back.
	//
	// It matters more here than for torrents. Parallel NZBs share one pool of
	// connections to the same news servers, so two at once do not go twice as
	// fast — they halve each other and both take twice as long to become
	// importable.
	Admitter core.Admitter
}

// Engine is the built-in Usenet engine: NZB in, imported media out, with no
// external download client anywhere (SPEC §5.1, PLAN phase 7).
//
// It is the composition of the four packages below it — nntp fetches, yenc and
// pipeline assemble, par2 repairs, extract unpacks — behind the same
// core.Engine interface the torrent engine implements, so the router, the
// queue API and the import watcher treat a Usenet download exactly like any
// other.
//
// Like internal/download it does not import internal/store: persistence is the
// narrow callback seam in EngineOpts.Store.
type Engine struct {
	// incomplete is the absolute path of the directory in-progress data lives
	// under. Everything reported is relative to its parent, the storage root
	// (SPEC §1.2 pillar 3).
	incomplete string
	opts       EngineOpts
	http       *http.Client
	logger     *slog.Logger
	fetch      *fetcher

	// ctx is cancelled by Close and bounds the poller and every worker.
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	// workers is every running download goroutine, so Close can wait for the
	// sidecars to be flushed rather than racing them.
	workers sync.WaitGroup

	mu     sync.Mutex
	items  map[core.DownloadID]*item
	closed bool
	// servers fingerprints the configuration the current pool was built from,
	// so a settings save that changed nothing does not churn connections.
	servers string
}

// item is one download: its durable record, the plan it is working through,
// and the live state of whichever stage is running.
type item struct {
	rec core.Download
	// doc is the parsed NZB. It is the plan for every stage, so it is held
	// rather than re-read; a restore that cannot read the sidecar has no
	// download to resume.
	doc *nzb.NZB
	// dir is the absolute directory this download assembles into.
	dir string

	phase   core.DownloadPhase
	paused  bool
	failure string
	// admitted is whether the concurrency coordinator has given this download
	// a slot. Without one no worker starts, which is already the engine's
	// "queued" — see statusLocked.
	admitted bool
	// finished marks a download that has been through every stage. It is
	// separate from the record's state so a completed download that the user
	// pauses (they cannot, but Remove races) does not restart its worker.
	finished bool
	// repaired records that par2 has already run for this download, so a
	// failed extraction does not spend the recovery volumes a second time on
	// damage they have already been asked about.
	repaired bool

	// track is the running download stage's progress, nil once that stage is
	// over. Repair and extraction have no byte counter of their own, so the
	// last download snapshot is frozen into bytesDone/size and shown for the
	// rest of the run — progress must not rewind while par2 works.
	track     *pipeline.Tracker
	bytesDone int64
	size      int64
	// files is the per-file breakdown frozen out of the download stage, so the
	// queue drawer keeps showing which files arrived while par2 and the
	// unpacker run with no tracker of their own. Nil until that stage has
	// finished at least once this process, and the NZB answers instead.
	files []core.UsenetFileInsight
	// What verification found wrong, which is what the repairing phase is
	// working on. par2 reports no live progress, so this is the whole of what
	// can honestly be said about that stage.
	damagedSegments int
	damagedFiles    []string

	// cancel stops the running worker; stopped closes when it has returned.
	// Both are nil when no worker is running.
	cancel  context.CancelFunc
	stopped chan struct{}

	// Rate sampling. The pipeline exposes counters, not rates.
	sampledAt time.Time
	lastBytes int64
	downRate  int64
}

// Engine implements the interface every download backend speaks, plus two
// optional extensions: insight, because a Usenet download's detail is its file
// list and its repair state where a torrent's is peers and trackers, and retry,
// because it is several stages and a failure belongs to one of them.
var (
	_ core.Engine        = (*Engine)(nil)
	_ core.EngineInsight = (*Engine)(nil)
	_ core.EngineRetry   = (*Engine)(nil)
)

// NewEngine starts the embedded Usenet engine, writing in-progress data under
// root/download.IncompleteDir, and re-adds whatever opts.Store remembers.
func NewEngine(root string, opts EngineOpts) (*Engine, error) {
	if strings.TrimSpace(root) == "" {
		return nil, errors.New("usenet: storage root must not be empty")
	}
	if opts.Concurrency < 0 {
		return nil, errors.New("usenet: concurrency must not be negative")
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("usenet: resolve storage root %s: %w", root, err)
	}
	incomplete := filepath.Join(abs, download.IncompleteDir)
	if err := os.MkdirAll(filepath.Join(incomplete, metaDir), 0o755); err != nil {
		return nil, fmt.Errorf("usenet: create %s: %w", incomplete, err)
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: nzbTimeout}
	}

	// No enabled server is a configuration state, not a failure: the engine
	// still has to list, pause and remove the downloads it already holds, and
	// the user is told to add a server when they try to grab (Track 1 note 5).
	pool, err := newPool(opts.Servers, opts.NNTP)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{
		incomplete: incomplete,
		opts:       opts,
		http:       httpClient,
		logger:     logger,
		fetch:      newFetcher(pool),
		ctx:        ctx,
		cancel:     cancel,
		done:       make(chan struct{}),
		items:      map[core.DownloadID]*item{},
		servers:    fingerprintServers(opts.Servers),
	}

	// The coordinator has to be able to reach back when a slot frees anywhere,
	// including in the torrent engine. A nil Admitter fails the assertion,
	// which is the uncapped path.
	if reg, ok := opts.Admitter.(admissionRegistrar); ok {
		reg.Register(EngineName, e.wake)
	}

	if err := e.restore(ctx); err != nil {
		cancel()
		e.fetch.close()
		return nil, err
	}

	interval := opts.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	go e.poll(ctx, interval)
	return e, nil
}

// newPool builds the transport, treating "nothing configured" as a nil pool
// rather than an error. Every other failure — an unreachable-looking config, a
// server that does not validate — is real and stops construction.
func newPool(servers []nntp.ServerConfig, opts nntp.Options) (*nntp.MultiPool, error) {
	pool, err := nntp.NewMultiPool(servers, opts)
	if errors.Is(err, nntp.ErrNoServers) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("usenet: open news servers: %w", err)
	}
	return pool, nil
}

// SetServers re-points the engine at a new set of news servers.
//
// It is how a settings change reaches a running engine without a restart, and
// it is a no-op when nothing about the configuration changed — the fingerprint
// covers credentials too, so an edited password rebuilds the pool while a
// save that only touched an unrelated setting does not drop a single
// connection. Downloads in flight keep fetching; see fetcher.swap.
func (e *Engine) SetServers(servers []nntp.ServerConfig) error {
	fingerprint := fingerprintServers(servers)

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return download.ErrClosed
	}
	if fingerprint == e.servers {
		e.mu.Unlock()
		return nil
	}
	e.mu.Unlock()

	pool, err := newPool(servers, e.opts.NNTP)
	if err != nil {
		return err
	}

	e.mu.Lock()
	e.servers = fingerprint
	e.mu.Unlock()
	e.fetch.swap(pool)
	return nil
}

// fingerprintServers hashes everything a pool is built from, so a changed
// configuration is detected without holding the credentials it changed in a
// comparable field (SPEC §12).
func fingerprintServers(servers []nntp.ServerConfig) string {
	h := sha256.New()
	for _, s := range servers {
		fmt.Fprintf(h, "%d\x00%s\x00%s\x00%d\x00%t\x00%s\x00%s\x00%d\x00%d\x00%t\x00",
			s.ID, s.Name, s.Host, s.Port, s.TLS, s.Username, s.Password,
			s.MaxConnections, s.Priority, s.Enabled)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// restore re-adds every download the store remembers.
//
// A row this engine cannot make sense of is skipped rather than fatal: one
// unreadable download must not keep Caravan from starting (SPEC §13). The NZB
// comes from the sidecar, so a download whose sidecar is gone is dropped from
// the engine's view — its row and its data stay, and the user can re-grab.
func (e *Engine) restore(ctx context.Context) error {
	if e.opts.Store == nil {
		return nil
	}
	recs, err := e.opts.Store.Load(ctx)
	if err != nil {
		return fmt.Errorf("usenet: load downloads: %w", err)
	}
	// Oldest first, so a restart hands out the slots the caps allow in the same
	// order the queue would have: the store returns rows in no useful order.
	sort.Slice(recs, func(i, j int) bool {
		if a, b := recs[i].CreatedAt, recs[j].CreatedAt; !a.Equal(b) {
			return a.Before(b)
		}
		return recs[i].EngineID < recs[j].EngineID
	})
	for _, rec := range recs {
		if rec.Engine != EngineName {
			continue
		}
		doc, err := e.readNZB(rec.EngineID)
		if err != nil {
			e.logger.Warn("skipping unrestorable download", "download", rec.EngineID, "err", err)
			continue
		}
		it := &item{
			rec:       rec,
			doc:       doc,
			dir:       e.dirFor(rec),
			bytesDone: rec.BytesDone,
			size:      rec.Size,
		}
		// A download that had finished every stage stays finished: its data is
		// on disk and the import watcher may not have got to it yet.
		it.finished = rec.State == core.DownloadCompleted
		it.paused = e.opts.Paused || rec.State == core.DownloadPaused
		if rec.State == core.DownloadFailed {
			it.failure = rec.Error
			if it.failure == "" {
				it.failure = "the download failed before the last restart"
			}
		}
		e.mu.Lock()
		e.items[rec.EngineID] = it
		// start decides for itself: a row that came back paused, finished or
		// failed stays that way until the user says otherwise.
		e.start(it)
		e.mu.Unlock()
	}
	return nil
}

// Add fetches the release's .nzb and starts downloading it. See core.Engine.
func (e *Engine) Add(ctx context.Context, r core.Release, opts core.AddOpts) (core.DownloadID, error) {
	if r.Protocol != "" && r.Protocol != core.ProtocolUsenet {
		return "", fmt.Errorf("usenet: release %q is %s: the embedded Usenet engine only handles NZBs", r.Title, r.Protocol)
	}
	// Refuse before spending a request on the indexer. A grab that downloads
	// an NZB and then discovers there is nowhere to fetch its articles from
	// has burned an indexer hit for nothing.
	if !e.fetch.configured() {
		return "", fmt.Errorf("usenet: %w: add one under Settings → Usenet servers", nntp.ErrNoServers)
	}

	url := strings.TrimSpace(r.DownloadURL)
	if url == "" {
		return "", fmt.Errorf("usenet: release %q has no NZB URL", r.Title)
	}
	raw, err := download.FetchPayload(ctx, e.http, url, nzb.MaxDocumentBytes)
	if err != nil {
		return "", fmt.Errorf("usenet: fetch nzb for %q: %w", r.Title, err)
	}
	doc, err := nzb.Parse(bytes.NewReader(raw))
	if err != nil {
		return "", fmt.Errorf("usenet: read nzb for %q: %w", r.Title, err)
	}
	if len(doc.ContentFiles()) == 0 {
		return "", fmt.Errorf("usenet: nzb for %q has no content files, only par2", r.Title)
	}

	// The handle is the NZB's own digest, which gives Usenet the property an
	// info hash gives BitTorrent: grabbing the same release twice re-attaches
	// to the download already running instead of starting a second copy over
	// the same directory.
	id := handle(raw)

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return "", download.ErrClosed
	}
	if it, existing := e.items[id]; existing {
		// Already here. Make sure it is actually going: deliberately grabbing
		// a release that is sitting paused, or that failed, is a request to
		// have it — the same thing Resume means, and the only other way to ask.
		it.paused = false
		it.failure = ""
		e.start(it)
		snapshot := e.refreshLocked(it)
		e.mu.Unlock()
		return id, e.save(ctx, snapshot)
	}

	title := strings.TrimSpace(r.Title)
	if title == "" {
		title = doc.Meta["name"]
	}
	if title == "" {
		title = string(id)
	}
	it := &item{
		rec: core.Download{
			Engine:    EngineName,
			EngineID:  id,
			Title:     title,
			CreatedAt: time.Now(),
		},
		doc:   doc,
		phase: core.PhaseDownloading,
	}
	it.rec.SavePath = path.Join(download.IncompleteDir, e.dirNameLocked(title, id))
	it.dir = e.dirFor(it.rec)
	e.items[id] = it
	e.mu.Unlock()

	// The sidecar before the worker: a download whose plan is not on disk
	// cannot be resumed, and a restart right after Add is exactly when that
	// matters.
	if err := e.writeNZB(id, raw); err != nil {
		e.drop(id)
		return "", err
	}

	e.mu.Lock()
	snapshot := e.refreshLocked(it)
	e.mu.Unlock()

	if err := e.save(ctx, snapshot); err != nil {
		// Nothing persisted means nothing resumes. Undo the registration, but
		// leave any bytes alone: a failed database write is no reason to
		// delete a previous attempt's progress.
		e.drop(id)
		return "", err
	}

	e.mu.Lock()
	e.start(it)
	e.mu.Unlock()
	return id, nil
}

// handle is the download id for an NZB document.
func handle(raw []byte) core.DownloadID {
	sum := sha256.Sum256(raw)
	return core.DownloadID(handlePrefix + hex.EncodeToString(sum[:20]))
}

// Status returns a live snapshot of one download. See core.Engine.
func (e *Engine) Status(ctx context.Context, id core.DownloadID) (*core.DownloadStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	it, ok := e.items[id]
	if !ok {
		return nil, fmt.Errorf("usenet: status %q: %w", id, download.ErrNotFound)
	}
	st := e.statusLocked(it)
	return &st, nil
}

// List returns a live snapshot of every download, oldest first. See core.Engine.
func (e *Engine) List(ctx context.Context) ([]core.DownloadStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]core.DownloadStatus, 0, len(e.items))
	order := make(map[core.DownloadID]time.Time, len(e.items))
	for id, it := range e.items {
		out = append(out, e.statusLocked(it))
		order[id] = it.rec.CreatedAt
	}
	sort.Slice(out, func(i, j int) bool {
		if a, b := order[out[i].ID], order[out[j].ID]; !a.Equal(b) {
			return a.Before(b)
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// ListPage returns a deterministic page for the optional cursor seam. The
// legacy List order remains oldest first for queue consumers.
func (e *Engine) ListPage(ctx context.Context, limit int, before core.DownloadID) ([]core.DownloadStatus, core.DownloadID, error) {
	statuses, err := e.List(ctx)
	if err != nil {
		return nil, "", err
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].ID < statuses[j].ID })
	start := 0
	for start < len(statuses) && before != "" && statuses[start].ID <= before {
		start++
	}
	if start == len(statuses) || limit <= 0 {
		return []core.DownloadStatus{}, "", nil
	}
	end := min(start+limit, len(statuses))
	next := core.DownloadID("")
	if end < len(statuses) {
		next = statuses[end-1].ID
	}
	return statuses[start:end], next, nil
}

// Insight returns the Usenet-shaped detail the queue drawer shows in place of
// a torrent's peers and trackers: which files the NZB indexes, how much of each
// one is on disk, and what the repair stage is working on when it is running.
// See core.EngineInsight.
//
// The peer and tracker halves stay empty rather than absent: a Usenet download
// has neither, and an empty list says so where a null would only look like a
// bug.
func (e *Engine) Insight(ctx context.Context, id core.DownloadID) (*core.DownloadInsight, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	it, ok := e.items[id]
	if !ok {
		return nil, fmt.Errorf("usenet: insight %q: %w", id, download.ErrNotFound)
	}

	ins := &core.DownloadInsight{
		Peers:           []core.PeerInsight{},
		Trackers:        []core.TrackerInsight{},
		Files:           e.filesLocked(it),
		DamagedSegments: it.damagedSegments,
		DamagedFiles:    append([]string(nil), it.damagedFiles...),
	}
	for _, f := range ins.Files {
		ins.Segments += f.Segments
		ins.SegmentsDone += f.SegmentsDone
		ins.SegmentsFailed += f.SegmentsFailed
		if f.Complete {
			ins.FilesComplete++
		}
	}
	return ins, nil
}

// filesLocked is the per-file breakdown from the best source available, which
// changes as a download moves through its stages. It must be called with e.mu
// held.
//
// The live tracker while articles are being fetched; the snapshot frozen out of
// it once that stage is over and par2 or the unpacker owns the download; and
// failing both — a download restored from a previous process, which has no
// counters at all — the NZB itself, where the only honest per-file answer is
// "all of it" for a finished download and "none of it" for one that has not
// started.
func (e *Engine) filesLocked(it *item) []core.UsenetFileInsight {
	if live := it.track.Files(); len(live) > 0 {
		out := make([]core.UsenetFileInsight, 0, len(live))
		for _, f := range live {
			out = append(out, core.UsenetFileInsight{
				Name:           f.Name,
				Segments:       f.Segments,
				SegmentsDone:   f.SegmentsDone,
				SegmentsFailed: f.SegmentsFailed,
				Complete:       f.Complete(),
				Par2:           f.IsPar2,
			})
		}
		return out
	}
	if len(it.files) > 0 {
		return append([]core.UsenetFileInsight(nil), it.files...)
	}
	if it.doc == nil {
		return nil
	}
	content := it.doc.ContentFiles()
	out := make([]core.UsenetFileInsight, 0, len(content))
	for _, f := range content {
		segments := len(f.Segments)
		done := 0
		if it.finished {
			done = segments
		}
		out = append(out, core.UsenetFileInsight{
			Name:         f.Filename(),
			Segments:     segments,
			SegmentsDone: done,
			Complete:     it.finished,
		})
	}
	return out
}

// Pause stops transferring without discarding progress. See core.Engine.
//
// Pausing cancels the running stage's context. The pipeline flushes its resume
// sidecar on the way out, so the articles already on disk stay on disk and the
// next Resume picks up from there rather than refetching them.
func (e *Engine) Pause(ctx context.Context, id core.DownloadID) error {
	e.mu.Lock()
	it, ok := e.items[id]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("usenet: pause %q: %w", id, download.ErrNotFound)
	}
	it.paused = true
	it.downRate = 0
	stop := it.cancel
	snapshot := e.refreshLocked(it)
	e.mu.Unlock()

	if stop != nil {
		stop()
	}
	return e.save(ctx, snapshot)
}

// Resume restarts a paused download. See core.Engine.
func (e *Engine) Resume(ctx context.Context, id core.DownloadID) error {
	e.mu.Lock()
	it, ok := e.items[id]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("usenet: resume %q: %w", id, download.ErrNotFound)
	}
	it.paused = false
	// Resuming is also the retry: a download that failed because a provider
	// was down or a disk was full is worth another go, and there is no other
	// button for it.
	it.failure = ""
	if !it.finished {
		e.start(it)
	}
	snapshot := e.refreshLocked(it)
	e.mu.Unlock()

	return e.save(ctx, snapshot)
}

// Retry puts a failed download back to work. See core.EngineRetry.
//
// It re-enters the stage machine from the top, which is what makes it pick up
// where the failure left it rather than start over: every stage is written to
// recognise work that is already done. The articles already fetched are in the
// pipeline's resume sidecar and are not asked for a second time; a release
// whose files are all on disk goes straight past the download stage; and one
// that got as far as unpacking goes straight to unpacking, because that is the
// first stage with anything left to do (see Engine.stages).
//
// The repair budget is the one thing deliberately reset. Within a single run a
// download spends its recovery volumes once, so a failed extraction does not
// ask par2 about damage it has already been asked about; a user pressing Retry
// is asking for a genuinely fresh attempt, and the volumes are on disk by then
// so the second pass costs cpu rather than a provider's quota.
func (e *Engine) Retry(ctx context.Context, id core.DownloadID) error {
	e.mu.Lock()
	it, ok := e.items[id]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("usenet: retry %q: %w", id, download.ErrNotFound)
	}
	// Only a failure has something to retry. A running, paused or finished
	// download reaching here means the caller acted on state it had misread,
	// and quietly restarting one would be a worse answer than saying so.
	if it.failure == "" {
		e.mu.Unlock()
		return fmt.Errorf("usenet: retry %q: %w", id, download.ErrNotRetryable)
	}
	it.failure = ""
	it.paused = false
	it.repaired = false
	e.start(it)
	snapshot := e.refreshLocked(it)
	e.mu.Unlock()

	return e.save(ctx, snapshot)
}

// Remove drops the download, and its data when deleteData is set. It never
// touches the library. See core.Engine.
func (e *Engine) Remove(ctx context.Context, id core.DownloadID, deleteData bool) error {
	e.mu.Lock()
	it, ok := e.items[id]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("usenet: remove %q: %w", id, download.ErrNotFound)
	}
	dir, stop, stopped := it.dir, it.cancel, it.stopped
	delete(e.items, id)
	// It is leaving entirely, so its slot goes back now rather than when the
	// worker finishes unwinding.
	e.release(it)
	e.mu.Unlock()

	// Wait for the worker before touching the directory. Deleting files a
	// running pipeline is still writing into leaves a half-removed download
	// and a confusing error; this is bounded by one article's fetch.
	if stop != nil {
		stop()
		<-stopped
	}

	if err := os.Remove(e.nzbPath(id)); err != nil && !os.IsNotExist(err) {
		e.logger.Warn("removing nzb sidecar", "download", id, "err", err)
	}
	if err := os.Remove(e.donePath(id)); err != nil && !os.IsNotExist(err) {
		e.logger.Warn("removing the assembled marker", "download", id, "err", err)
	}
	if deleteData && dir != "" {
		if err := e.removeData(dir); err != nil {
			return fmt.Errorf("usenet: remove %q data: %w", id, err)
		}
	}
	if e.opts.Store == nil {
		return nil
	}
	if err := e.opts.Store.Delete(ctx, id); err != nil {
		return fmt.Errorf("usenet: remove %q: %w", id, err)
	}
	return nil
}

// drop unregisters a download without touching persistence or data. It is the
// half of Remove a failed Add uses to undo itself.
func (e *Engine) drop(id core.DownloadID) {
	e.mu.Lock()
	delete(e.items, id)
	e.mu.Unlock()
	if err := os.Remove(e.nzbPath(id)); err != nil && !os.IsNotExist(err) {
		e.logger.Warn("removing nzb sidecar", "download", id, "err", err)
	}
	if err := os.Remove(e.donePath(id)); err != nil && !os.IsNotExist(err) {
		e.logger.Warn("removing the assembled marker", "download", id, "err", err)
	}
}

// removeData deletes one download's directory, refusing anything that would
// land outside the incomplete directory.
//
// This is the guard on SPEC §13's promise that removing a download never costs
// media. The directory name is derived from a release title, which is a
// stranger's text, and "../../library" is a perfectly legal thing to put in
// one.
func (e *Engine) removeData(dir string) error {
	rel, err := filepath.Rel(e.incomplete, dir)
	if err != nil {
		return fmt.Errorf("%s does not resolve under %s", dir, e.incomplete)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%s escapes %s", dir, e.incomplete)
	}
	return os.RemoveAll(dir)
}

// Close shuts the engine down cleanly. See core.Engine.
func (e *Engine) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.mu.Unlock()

	e.cancel()
	<-e.done
	// Every worker's pipeline flushes its resume sidecar as its context is
	// cancelled; waiting here is what makes a restart resume rather than
	// refetch the last few hundred articles.
	e.workers.Wait()

	// A final flush with a fresh context: e.ctx is cancelled by now, and the
	// last state change is exactly the one worth keeping.
	ctx := context.Background()
	e.mu.Lock()
	final := make([]core.Download, 0, len(e.items))
	for _, it := range e.items {
		final = append(final, e.refreshLocked(it))
	}
	e.mu.Unlock()
	for _, rec := range final {
		if err := e.save(ctx, rec); err != nil {
			e.logger.Warn("persisting download on close", "download", rec.EngineID, "err", err)
		}
	}

	e.fetch.close()
	return nil
}

// poll samples every download, turning the pipeline's counters into rates and
// writing the durable half of anything that moved.
func (e *Engine) poll(ctx context.Context, interval time.Duration) {
	defer close(e.done)
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for _, rec := range e.sample() {
				if err := e.save(ctx, rec); err != nil {
					e.logger.Warn("persisting download", "download", rec.EngineID, "err", err)
				}
			}
		}
	}
}

// sample refreshes every item and returns the records whose durable fields
// changed. Saving happens outside the lock: the store is a database.
func (e *Engine) sample() []core.Download {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now()
	var changed []core.Download
	for _, it := range e.items {
		it.sample(now)
		before := it.rec
		after := e.refreshLocked(it)
		if durableChanged(before, after) {
			changed = append(changed, after)
		}
	}
	return changed
}

// sample recomputes the transfer rate from the byte delta since the last call.
func (it *item) sample(now time.Time) {
	bytes := it.observedBytes()
	if elapsed := now.Sub(it.sampledAt).Seconds(); !it.sampledAt.IsZero() && elapsed > 0 {
		if delta := bytes - it.lastBytes; delta > 0 {
			it.downRate = int64(float64(delta) / elapsed)
		} else {
			it.downRate = 0
		}
	}
	it.sampledAt, it.lastBytes = now, bytes
	if it.paused || it.finished {
		it.downRate = 0
	}
}

// observedBytes is how much of this download exists right now: the live
// tracker while articles are being fetched, and the frozen total afterwards.
func (it *item) observedBytes() int64 {
	if it.track != nil {
		return it.track.Snapshot().Bytes
	}
	return it.bytesDone
}

// durableChanged reports whether anything worth a write changed. Rates and the
// phase are excluded: they change every sample and are meaningless by the time
// they are read back.
func durableChanged(a, b core.Download) bool {
	return a.State != b.State ||
		a.Progress != b.Progress ||
		a.BytesDone != b.BytesDone ||
		a.Size != b.Size ||
		a.Title != b.Title ||
		a.SavePath != b.SavePath ||
		a.Error != b.Error
}

// refreshLocked syncs an item's durable record with its live status and
// returns a copy of the record.
func (e *Engine) refreshLocked(it *item) core.Download {
	st := e.statusLocked(it)
	it.rec.State = st.State
	it.rec.Progress = st.Progress
	it.rec.BytesDone = st.BytesDone
	it.rec.Size = st.Size
	it.rec.SavePath = st.SavePath
	it.rec.Error = st.Error
	return it.rec
}

// statusLocked maps an item onto core.DownloadStatus.
func (e *Engine) statusLocked(it *item) core.DownloadStatus {
	st := core.DownloadStatus{
		ID:         it.rec.EngineID,
		Name:       it.rec.Title,
		SavePath:   it.rec.SavePath,
		DownRate:   it.downRate,
		ETASeconds: -1,
		Error:      it.failure,
		Phase:      it.phase,
	}

	// The live tracker wins, but only once it has totals. A worker that has
	// just started has an empty one for the moment it takes the pipeline to
	// read the sidecar and count what is already on disk, and reporting 0%
	// there would make every resume look like a restart.
	if p := it.track.Snapshot(); it.track != nil && (p.TotalBytes > 0 || p.Segments > 0) {
		st.BytesDone, st.Size = p.Bytes, p.TotalBytes
		st.Progress = p.Fraction()
	} else {
		st.BytesDone, st.Size = it.bytesDone, it.size
		if it.size > 0 {
			st.Progress = float64(it.bytesDone) / float64(it.size)
		}
	}
	if st.Progress > 1 {
		st.Progress = 1
	}

	switch {
	case st.Error != "":
		// A failure outranks everything: a download that died mid-repair must
		// not read as merely paused.
		st.State = core.DownloadFailed
		st.Phase = ""
	case it.finished:
		// Complete, and there is nothing to seed: a Usenet download has no
		// upload half, so "completed" is the honest end state and it is what
		// the import watcher waits for.
		st.State = core.DownloadCompleted
		st.Progress, st.Phase = 1, ""
		st.ETASeconds = 0
	case it.paused:
		st.State = core.DownloadPaused
		st.DownRate = 0
	case it.cancel == nil:
		// Registered but not running: restored and waiting, or between stages.
		st.State = core.DownloadQueued
	default:
		st.State = core.DownloadDownloading
		if st.DownRate > 0 && st.Size > st.BytesDone {
			st.ETASeconds = (st.Size - st.BytesDone) / st.DownRate
		}
	}
	return st
}

// save writes one record through the persistence seam, if there is one.
func (e *Engine) save(ctx context.Context, rec core.Download) error {
	if e.opts.Store == nil {
		return nil
	}
	if err := e.opts.Store.Save(ctx, rec); err != nil {
		return fmt.Errorf("usenet: persist %q: %w", rec.EngineID, err)
	}
	return nil
}
