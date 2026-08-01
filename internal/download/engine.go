// Package download holds Caravan's built-in download engines. In v1 that is
// one engine: the embedded BitTorrent client (SPEC §5.1), which is why a
// stock Caravan needs no external download client at all.
//
// The package deliberately does not import internal/store. Persistence is a
// narrow callback interface the caller supplies, so the engine can be
// exercised without a database and the store schema can move without dragging
// the engine with it.
package download

import (
	"context"
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

	"golang.org/x/time/rate"

	"github.com/anacrolix/torrent"

	"github.com/watzon/caravan/internal/core"
)

// ErrNotFound is returned for a download id the engine does not know. It is a
// sentinel so the API layer can map it to a 404 rather than a 500.
var ErrNotFound = errors.New("download: download not found")

// ErrClosed is returned once Close has run. Restarting means a new engine.
var ErrClosed = errors.New("download: engine is closed")

// IncompleteDir is the sub-directory of the storage root that in-progress
// data lives in. Keeping it under the same root is what makes the finished
// import a hardlink or a rename rather than a copy (SPEC §1.2 pillar 3).
const IncompleteDir = "incomplete"

// EngineName is what downloads started by this engine record in their
// `downloads.engine` column.
const EngineName = "embedded"

// metaDir holds one .torrent per added download, inside the incomplete
// directory. A persisted download only remembers its info hash, and an info
// hash alone cannot be resumed without peers to re-fetch the metadata from —
// so the metainfo (info dict *and* trackers) is kept beside the data, and a
// restart re-adds a torrent knowing everything the original grab knew.
const metaDir = ".caravan"

// partFileSuffix is what anacrolix's file storage appends to a file it has not
// finished writing. Deleting a download's data means deleting these too.
const partFileSuffix = ".part"

// defaultPollInterval is how often the engine samples the client. It is the
// resolution of the transfer rates it reports and of the progress it persists;
// a couple of seconds is under a UI refresh and far above sqlite's cost.
const defaultPollInterval = 2 * time.Second

// maxSeedDays keeps conversion to time.Duration safe in the polling loop.
const maxSeedDays = int((1<<63 - 1) / (24 * time.Hour))

// Persistence is the seam through which the engine remembers its downloads
// across restarts. The store implements it; the engine only knows this much.
//
// Every method must be idempotent: the engine calls Save on each state change
// and cannot know which of those survived a crash.
type Persistence interface {
	// Save records the current state of one download.
	Save(ctx context.Context, d core.Download) error
	// Load returns every download previously saved, so the engine can re-add
	// them on startup.
	Load(ctx context.Context) ([]core.Download, error)
	// Delete forgets one download. It never deletes downloaded data.
	Delete(ctx context.Context, id core.DownloadID) error
}

// EmbeddedOpts configures the embedded engine.
type EmbeddedOpts struct {
	// ListenPort is the TCP/uTP port the client binds. Zero picks one.
	ListenPort int
	// MaxConnections caps established peers per torrent. Zero uses anacrolix's
	// default. It is a construction-time setting.
	MaxConnections int
	// MaxDownKBps and MaxUpKBps are global client limits in KB/s. Zero is
	// unlimited and can be changed while the engine is running.
	MaxDownKBps int64
	MaxUpKBps   int64
	// SeedRatio and SeedDays stop a completed torrent once either non-zero
	// target is met.
	SeedRatio float64
	SeedDays  int
	// DisableDHT and DisablePEX turn off peer discovery. SPEC §12 makes both
	// user-configurable rather than assumed.
	DisableDHT bool
	DisablePEX bool
	// DisableTrackers stops the client announcing to a torrent's trackers. It
	// completes the set of ways a download can find peers, so a test (or a user
	// who wants a strictly private engine) can turn all of them off.
	DisableTrackers bool
	// Paused starts every restored download paused. Portable mode defaults to
	// this so a freshly plugged-in drive does not start seeding (SPEC §2.3).
	Paused bool
	// Store persists the queue across restarts. A nil Store means the engine
	// keeps nothing: downloads do not survive a restart.
	Store Persistence
	// PollInterval overrides defaultPollInterval.
	PollInterval time.Duration
	// HTTPClient fetches .torrent files. Nil uses a client with a bounded
	// timeout — an indexer that never answers must not wedge a grab.
	HTTPClient *http.Client
	// Logger receives the torrent client's own logging and the engine's
	// background failures, which have no caller to return an error to. Nil
	// discards both rather than writing to stderr behind the caller's back.
	Logger *slog.Logger
}

// Embedded is the built-in BitTorrent engine.
type Embedded struct {
	client *torrent.Client
	// incomplete is the absolute path of the directory all in-progress data
	// lives under. Everything the engine reports is relative to its parent, the
	// storage root (SPEC §1.2 pillar 3).
	incomplete string
	opts       EmbeddedOpts
	http       *http.Client
	logger     *slog.Logger
	// The client reads these limiter instances directly, so changing their
	// limits updates the live global transfer budget without rebuilding peers.
	downLimiter *rate.Limiter
	upLimiter   *rate.Limiter
	// maxConnections is the configured per-torrent ceiling. It is fixed for a
	// client lifetime because anacrolix exposes it through ClientConfig only.
	maxConnections int
	seedRatio      float64
	seedDays       int

	// ctx is cancelled by Close. It bounds the poller and the per-download
	// goroutines that wait for metadata.
	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}

	mu     sync.Mutex
	items  map[core.DownloadID]*item
	closed bool
}

// item is one download: the live torrent, the durable record that mirrors it,
// and the sampling state the rates are derived from.
type item struct {
	t   *torrent.Torrent
	rec core.Download

	// paused is tracked here because anacrolix has no paused state of its own:
	// pausing is "stop the transfer", and only the engine knows it was asked
	// for rather than merely happening.
	paused bool
	// maxConns is the connection limit to restore on resume.
	maxConns int
	// failure is the last write error. anacrolix reports a failed chunk write
	// and keeps going; for a download manager that is a dead download.
	failure string

	// Rate sampling. anacrolix exposes counters, not rates, so a rate is the
	// delta between two polls.
	sampledAt   time.Time
	lastRead    int64
	lastWritten int64
	downRate    int64
	upRate      int64
	// seedingStarted is the first poll that observed this torrent seeding. It
	// deliberately lives with the running item: the engine's transfer ratio is
	// session-scoped too, so neither target can be faithfully continued after a
	// restart.
	seedingStarted time.Time
}

// Embedded implements the engine interface every download backend speaks.
var _ core.Engine = (*Embedded)(nil)
var _ core.EngineInsight = (*Embedded)(nil)
var _ core.EngineRateLimits = (*Embedded)(nil)

// NewEmbedded starts the embedded torrent client, writing in-progress data
// under dataDir/IncompleteDir, and re-adds whatever opts.Store remembers.
func NewEmbedded(dataDir string, opts EmbeddedOpts) (*Embedded, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("download: storage root must not be empty")
	}
	if opts.ListenPort < 0 || opts.ListenPort > 65535 {
		return nil, errors.New("download: listen port must be between 0 and 65535")
	}
	if opts.MaxConnections < 0 || opts.MaxDownKBps < 0 || opts.MaxUpKBps < 0 ||
		opts.MaxDownKBps > (1<<63-1)/1024 || opts.MaxUpKBps > (1<<63-1)/1024 ||
		opts.SeedRatio < 0 || opts.SeedDays < 0 || opts.SeedDays > maxSeedDays {
		return nil, errors.New("download: engine limits must not be negative or overflow")
	}
	root, err := filepath.Abs(dataDir)
	if err != nil {
		return nil, fmt.Errorf("download: resolve storage root %s: %w", dataDir, err)
	}
	incomplete := filepath.Join(root, IncompleteDir)
	if err := os.MkdirAll(filepath.Join(incomplete, metaDir), 0o755); err != nil {
		return nil, fmt.Errorf("download: create %s: %w", incomplete, err)
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: metainfoTimeout}
	}

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = incomplete
	cfg.ListenPort = opts.ListenPort
	cfg.NoDHT = opts.DisableDHT
	cfg.DisablePEX = opts.DisablePEX
	cfg.DisableTrackers = opts.DisableTrackers
	// Keep concrete limiter instances even when unlimited. anacrolix retains
	// these pointers in the client, letting SetLimit update the live budget.
	downLimiter := rate.NewLimiter(kbpsLimit(opts.MaxDownKBps), 1<<20)
	upLimiter := rate.NewLimiter(kbpsLimit(opts.MaxUpKBps), 1<<20)
	cfg.DownloadRateLimiter = downLimiter
	cfg.UploadRateLimiter = upLimiter
	if opts.MaxConnections > 0 {
		cfg.EstablishedConnsPerTorrent = opts.MaxConnections
	}
	// Seeding is free here: an imported file is a hardlink to this data
	// (SPEC §5.1), so the copy exists either way until the user removes it.
	cfg.Seed = true
	// No UPnP. SPEC §12 promises the engine binds the configured port and
	// nothing more; opening a router port is the user's decision, not ours.
	cfg.NoDefaultPortForwarding = true
	cfg.Slogger = logger.With("component", "torrent")

	client, err := torrent.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("download: start torrent client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	e := &Embedded{
		client:         client,
		incomplete:     incomplete,
		opts:           opts,
		http:           httpClient,
		logger:         logger,
		downLimiter:    downLimiter,
		upLimiter:      upLimiter,
		maxConnections: cfg.EstablishedConnsPerTorrent,
		seedRatio:      opts.SeedRatio,
		seedDays:       opts.SeedDays,
		ctx:            ctx,
		cancel:         cancel,
		done:           make(chan struct{}),
		items:          make(map[core.DownloadID]*item),
	}

	if err := e.restore(ctx); err != nil {
		cancel()
		client.Close()
		return nil, err
	}

	interval := opts.PollInterval
	if interval <= 0 {
		interval = defaultPollInterval
	}
	go e.poll(ctx, interval)
	return e, nil
}

// restore re-adds every download the store remembers. A row the engine cannot
// make sense of is skipped rather than fatal: one unreadable download must not
// keep Caravan from starting (SPEC §13).
func (e *Embedded) restore(ctx context.Context) error {
	if e.opts.Store == nil {
		return nil
	}
	recs, err := e.opts.Store.Load(ctx)
	if err != nil {
		return fmt.Errorf("download: load downloads: %w", err)
	}
	for _, rec := range recs {
		if rec.Engine != "" && rec.Engine != EngineName {
			continue
		}
		spec, err := e.restoreSpec(rec)
		if err != nil {
			e.logger.Warn("skipping unrestorable download", "download", rec.EngineID, "err", err)
			continue
		}
		paused := e.opts.Paused || rec.State == core.DownloadPaused
		if _, err := e.add(ctx, spec, rec, paused); err != nil {
			e.logger.Warn("skipping unrestorable download", "download", rec.EngineID, "err", err)
		}
	}
	return nil
}

// Add starts downloading r. See core.Engine.
//
// opts is not used by the embedded engine: it has no categories, and the
// routing it carries is recorded by the caller in `grabs` — the engine's job
// is to fetch bytes, not to remember what they were for.
func (e *Embedded) Add(ctx context.Context, r core.Release, opts core.AddOpts) (core.DownloadID, error) {
	if r.Protocol == core.ProtocolUsenet {
		return "", fmt.Errorf("download: release %q is usenet: the embedded engine only handles torrents", r.Title)
	}
	spec, err := e.torrentSpec(ctx, r)
	if err != nil {
		return "", err
	}
	return e.add(ctx, spec, core.Download{Title: r.Title}, false)
}

// add is the one path into the client, shared by Add and restore. rec carries
// whatever the caller already knows about the download — for a restored row
// that includes its grab link and creation time, which must survive the
// round trip.
func (e *Embedded) add(ctx context.Context, spec *torrent.TorrentSpec, rec core.Download, paused bool) (core.DownloadID, error) {
	if spec.DisplayName == "" {
		spec.DisplayName = rec.Title
	}

	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return "", ErrClosed
	}
	t, _, err := e.client.AddTorrentSpec(spec)
	if err != nil {
		e.mu.Unlock()
		return "", fmt.Errorf("download: add %q: %w", spec.DisplayName, err)
	}

	id := core.DownloadID(t.InfoHash().HexString())
	it, existing := e.items[id]
	if !existing {
		rec.Engine = EngineName
		rec.EngineID = id
		if rec.Title == "" {
			rec.Title = t.Name()
		}
		if rec.CreatedAt.IsZero() {
			// Queue order. The store stamps its own created_at, but the engine
			// has to order downloads it has not persisted yet.
			rec.CreatedAt = time.Now()
		}
		it = &item{t: t, rec: rec, maxConns: e.connectionLimit(rec)}
		t.SetMaxEstablishedConns(it.maxConns)
		e.items[id] = it
		t.SetOnWriteChunkError(func(err error) { e.fail(id, err) })
	}
	if paused {
		pauseItem(it)
	} else {
		resumeItem(it)
	}
	snapshot := e.refreshLocked(it)
	e.mu.Unlock()

	if !existing {
		go e.watchInfo(id, t)
	}
	if err := e.save(ctx, snapshot); err != nil {
		// Nothing persisted means nothing resumes, and a download the engine
		// holds but no one remembers is worse than a failed grab. Undo it —
		// but leave the data alone: a failed write to the database is no reason
		// to delete bytes that may be a previous attempt's progress.
		if !existing {
			if derr := e.drop(id, false); derr != nil {
				e.logger.Warn("rolling back unsaved download", "download", id, "err", derr)
			}
		}
		return "", err
	}
	return id, nil
}

// watchInfo waits for a torrent's metadata, then starts the transfer and
// writes the metainfo sidecar the next restart resumes from. A magnet has no
// info dict until a peer sends one, so everything that needs the file list has
// to wait here.
func (e *Embedded) watchInfo(id core.DownloadID, t *torrent.Torrent) {
	select {
	case <-t.GotInfo():
	case <-t.Closed():
		return
	case <-e.ctx.Done():
		return
	}

	e.mu.Lock()
	it, ok := e.items[id]
	if !ok || it.t != t {
		e.mu.Unlock()
		return
	}
	if !it.paused {
		t.DownloadAll()
	}
	it.rec.Title = t.Name()
	snapshot := e.refreshLocked(it)
	e.mu.Unlock()

	if err := e.writeMetainfo(id, t); err != nil {
		e.logger.Warn("writing metainfo sidecar", "download", id, "err", err)
	}
	if err := e.save(e.ctx, snapshot); err != nil {
		e.logger.Warn("persisting download", "download", id, "err", err)
	}
}

// Status returns a live snapshot of one download. See core.Engine.
func (e *Embedded) Status(ctx context.Context, id core.DownloadID) (*core.DownloadStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	it, ok := e.items[id]
	if !ok {
		return nil, fmt.Errorf("download: status %q: %w", id, ErrNotFound)
	}
	st := e.statusLocked(it)
	return &st, nil
}

// List returns a live snapshot of every download. See core.Engine.
//
// Order is oldest first, so the queue reads as a queue.
func (e *Embedded) List(ctx context.Context) ([]core.DownloadStatus, error) {
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

// Insight reports the information anacrolix exposes without tracker scraping.
// Tracker counts therefore stay zero, and availability is the number of peer
// piece copies divided by the torrent's piece count.
func (e *Embedded) Insight(ctx context.Context, id core.DownloadID) (*core.DownloadInsight, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	it, ok := e.items[id]
	if !ok {
		return nil, fmt.Errorf("download: insight %q: %w", id, ErrNotFound)
	}

	peerConns := it.t.PeerConns()
	insight := &core.DownloadInsight{
		Peers:    make([]core.PeerInsight, 0, len(peerConns)),
		Trackers: []core.TrackerInsight{},
	}
	totalPieces := 0
	if info := it.t.Info(); info != nil {
		totalPieces = info.NumPieces()
	}
	var availablePieces uint64
	for _, peer := range peerConns {
		stats := peer.Stats()
		client, _ := peer.PeerClientName.Load().(string)
		progress := 0.0
		pieces := peer.PeerPieces().GetCardinality()
		if totalPieces > 0 {
			progress = float64(pieces) / float64(totalPieces)
			availablePieces += pieces
		}
		insight.Peers = append(insight.Peers, core.PeerInsight{
			Addr:     peer.RemoteAddr.String(),
			Client:   client,
			Progress: progress,
			DownRate: int64(stats.DownloadRate),
			UpRate:   int64(stats.LastWriteUploadRate),
		})
	}
	sort.Slice(insight.Peers, func(i, j int) bool {
		return insight.Peers[i].Addr < insight.Peers[j].Addr
	})
	if totalPieces > 0 {
		insight.Availability = float64(availablePieces) / float64(totalPieces)
	}

	meta := it.t.Metainfo()
	status := "unknown"
	if len(peerConns) > 0 || it.t.Info() != nil {
		status = "working"
	}
	for _, url := range meta.UpvertedAnnounceList().DistinctValues() {
		insight.Trackers = append(insight.Trackers, core.TrackerInsight{
			URL:    url,
			Status: status,
		})
	}
	return insight, nil
}

// SetGlobalRates updates the token budgets shared by every connected peer.
// anacrolix v1.61.0 retains ClientConfig limiter pointers after construction,
// so mutating the existing limiters changes live traffic without reconnecting.
func (e *Embedded) SetGlobalRates(ctx context.Context, downKbps, upKbps int64) error {
	if downKbps < 0 || upKbps < 0 ||
		downKbps > (1<<63-1)/1024 || upKbps > (1<<63-1)/1024 {
		return errors.New("download: global rates must not be negative or overflow")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	now := time.Now()
	e.downLimiter.SetLimitAt(now, kbpsLimit(downKbps))
	e.upLimiter.SetLimitAt(now, kbpsLimit(upKbps))
	return nil
}

// SetDownloadRates stores one torrent's requested limits. anacrolix v1.61.0
// has client-wide limiters only, not torrent-level limiters. A non-zero
// override therefore reduces this torrent to one connection, which constrains
// its opportunity to consume bandwidth but cannot guarantee a byte rate.
func (e *Embedded) SetDownloadRates(ctx context.Context, id core.DownloadID, downKbps, upKbps int64) error {
	if downKbps < 0 || upKbps < 0 ||
		downKbps > (1<<63-1)/1024 || upKbps > (1<<63-1)/1024 {
		return errors.New("download: per-download rates must not be negative or overflow")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	it, ok := e.items[id]
	if !ok {
		return fmt.Errorf("download: set rates %q: %w", id, ErrNotFound)
	}
	it.rec.MaxDownRate = downKbps * 1024
	it.rec.MaxUpRate = upKbps * 1024
	it.maxConns = e.connectionLimit(it.rec)
	if !it.paused {
		it.t.SetMaxEstablishedConns(it.maxConns)
	}
	return nil
}

// SetSeedingTargets applies global stopping targets to future poll samples.
func (e *Embedded) SetSeedingTargets(ratio float64, days int) error {
	if ratio < 0 || days < 0 || days > maxSeedDays {
		return errors.New("download: seeding targets must not be negative or overflow")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return ErrClosed
	}
	e.seedRatio = ratio
	e.seedDays = days
	return nil
}

func kbpsLimit(kbps int64) rate.Limit {
	if kbps == 0 {
		return rate.Inf
	}
	return rate.Limit(kbps * 1024)
}

func (e *Embedded) connectionLimit(rec core.Download) int {
	if rec.MaxDownRate != 0 || rec.MaxUpRate != 0 {
		return 1
	}
	return e.maxConnections
}

// Pause stops transferring without discarding progress. See core.Engine.
func (e *Embedded) Pause(ctx context.Context, id core.DownloadID) error {
	return e.setPaused(ctx, id, true)
}

// Resume restarts a paused download. See core.Engine.
func (e *Embedded) Resume(ctx context.Context, id core.DownloadID) error {
	return e.setPaused(ctx, id, false)
}

func (e *Embedded) setPaused(ctx context.Context, id core.DownloadID, paused bool) error {
	verb := "resume"
	if paused {
		verb = "pause"
	}

	e.mu.Lock()
	it, ok := e.items[id]
	if !ok {
		e.mu.Unlock()
		return fmt.Errorf("download: %s %q: %w", verb, id, ErrNotFound)
	}
	if paused {
		pauseItem(it)
	} else {
		resumeItem(it)
	}
	snapshot := e.refreshLocked(it)
	e.mu.Unlock()

	return e.save(ctx, snapshot)
}

// pauseItem stops a download without unregistering it.
//
// The mechanism is deliberate. Dropping the torrent (Torrent.Drop) would also
// stop the transfer, but it forgets the torrent: the queue entry disappears
// from the client, the metadata a magnet spent time fetching is lost, and
// resuming re-hashes every piece on disk. Capping connections at zero and
// disallowing data instead leaves the torrent registered and verified, so
// pause/resume is instant and Status keeps answering for it. The old
// connection cap is remembered so resume restores the configured value rather
// than a hardcoded one.
func pauseItem(it *item) {
	if it.paused {
		return
	}
	it.paused = true
	it.maxConns = it.t.SetMaxEstablishedConns(0)
	it.t.DisallowDataDownload()
	it.t.DisallowDataUpload()
	// Rates are a delta between samples; a paused download's rate is zero now,
	// not two seconds from now.
	it.downRate, it.upRate = 0, 0
}

// resumeItem undoes pauseItem, and makes sure the transfer is actually wanted:
// a torrent whose metadata arrived while it was paused was never told to
// download anything.
func resumeItem(it *item) {
	if it.paused {
		it.paused = false
		if it.maxConns > 0 {
			it.t.SetMaxEstablishedConns(it.maxConns)
		}
		it.t.AllowDataDownload()
		it.t.AllowDataUpload()
	}
	if it.t.Info() != nil {
		it.t.DownloadAll()
	}
}

// Remove drops the download, and its data when deleteData is set. It never
// touches the library. See core.Engine.
func (e *Embedded) Remove(ctx context.Context, id core.DownloadID, deleteData bool) error {
	e.mu.Lock()
	_, ok := e.items[id]
	e.mu.Unlock()
	if !ok {
		return fmt.Errorf("download: remove %q: %w", id, ErrNotFound)
	}

	if err := e.drop(id, deleteData); err != nil {
		return err
	}
	if e.opts.Store == nil {
		return nil
	}
	if err := e.opts.Store.Delete(ctx, id); err != nil {
		return fmt.Errorf("download: remove %q: %w", id, err)
	}
	return nil
}

// drop unregisters a download from the client and optionally deletes its data.
// It is the half of Remove that does not touch persistence, so a failed Add can
// undo itself.
func (e *Embedded) drop(id core.DownloadID, deleteData bool) error {
	e.mu.Lock()
	it, ok := e.items[id]
	if !ok {
		e.mu.Unlock()
		return nil
	}
	delete(e.items, id)
	// The data name comes from the info dict, never from a display name: a
	// display name is attacker-supplied text from a magnet link, and the info
	// dict is what the storage layer actually wrote under. No info means
	// nothing was written, so there is nothing to delete.
	var dataName string
	if info := it.t.Info(); info != nil {
		dataName = info.BestName()
	}
	e.mu.Unlock()

	it.t.Drop()
	if err := os.Remove(e.metainfoPath(id)); err != nil && !os.IsNotExist(err) {
		e.logger.Warn("removing metainfo sidecar", "download", id, "err", err)
	}
	if !deleteData || dataName == "" {
		return nil
	}

	target, err := e.dataPath(dataName)
	if err != nil {
		return fmt.Errorf("download: remove %q: %w", id, err)
	}
	// Two paths, because the storage layer writes an unfinished file as
	// "<name>.part" and renames it on completion. A multi-file torrent keeps
	// its part files inside its own directory, which the first removal covers;
	// a single-file torrent's part file sits beside where the finished file
	// will go, and would otherwise be left behind.
	for _, p := range []string{target, target + partFileSuffix} {
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("download: remove %q data: %w", id, err)
		}
	}
	return nil
}

// dataPath resolves a torrent's own directory inside the incomplete directory,
// and refuses anything that would land outside it.
//
// This is the guard on SPEC §13's promise that removing a download never costs
// media: the name comes from a torrent's info dict, which is a stranger's
// bytes, and "../../library" is a perfectly legal string to put there.
func (e *Embedded) dataPath(name string) (string, error) {
	target := filepath.Join(e.incomplete, name)
	rel, err := filepath.Rel(e.incomplete, target)
	if err != nil {
		return "", fmt.Errorf("torrent name %q does not resolve under %s", name, e.incomplete)
	}
	if rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("torrent name %q escapes %s", name, e.incomplete)
	}
	return target, nil
}

// Close shuts the client down cleanly. See core.Engine.
func (e *Embedded) Close() error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	e.closed = true
	e.mu.Unlock()

	e.cancel()
	<-e.done

	// A final flush, with a fresh context: e.ctx is cancelled by now, and the
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

	return errors.Join(e.client.Close()...)
}

// poll samples every download: it turns anacrolix's counters into rates and
// writes the durable half of any download whose state moved. Completion is
// noticed here rather than subscribed to, so one loop covers "it finished",
// "it made progress" and "how fast is it going".
func (e *Embedded) poll(ctx context.Context, interval time.Duration) {
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

// sample updates every item's rates and returns the records whose durable
// fields changed. Saving happens outside the lock: the store is a database.
func (e *Embedded) sample() []core.Download {
	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now()
	var changed []core.Download
	for _, it := range e.items {
		it.sample(now)
		before := it.rec
		after := e.refreshLocked(it)
		if after.State == core.DownloadSeeding {
			if it.seedingStarted.IsZero() {
				it.seedingStarted = now
			}
			if shouldStopSeeding(e.statusLocked(it).Ratio, it.seedingStarted, now, e.seedRatio, e.seedDays) {
				pauseItem(it)
				after = e.refreshLocked(it)
			}
		} else if after.State != core.DownloadPaused {
			// The seeding clock survives a pause (target-met or user-requested),
			// so a resume continues timing rather than restarting it.
			it.seedingStarted = time.Time{}
		}
		if durableChanged(before, after) {
			changed = append(changed, after)
		}
	}
	return changed
}

// sample recomputes transfer rates from the counter deltas since the last call.
func (it *item) sample(now time.Time) {
	stats := it.t.Stats()
	read, written := stats.BytesReadData.Int64(), stats.BytesWrittenData.Int64()

	if elapsed := now.Sub(it.sampledAt).Seconds(); !it.sampledAt.IsZero() && elapsed > 0 {
		it.downRate = int64(float64(read-it.lastRead) / elapsed)
		it.upRate = int64(float64(written-it.lastWritten) / elapsed)
	}
	it.sampledAt, it.lastRead, it.lastWritten = now, read, written
	if it.paused {
		it.downRate, it.upRate = 0, 0
	}
}

// shouldStopSeeding applies the global stop policy to a torrent already in the
// seeding state. Either configured target is sufficient to stop uploading.
func shouldStopSeeding(ratio float64, started, now time.Time, targetRatio float64, targetDays int) bool {
	if targetRatio > 0 && ratio >= targetRatio {
		return true
	}
	return targetDays > 0 && !started.IsZero() && now.Sub(started) >= time.Duration(targetDays)*24*time.Hour
}

// durableChanged reports whether anything worth a write changed. Rates and ETA
// are excluded on purpose: they change every sample and are meaningless by the
// time they are read back.
func durableChanged(a, b core.Download) bool {
	return a.State != b.State ||
		a.Progress != b.Progress ||
		a.BytesDone != b.BytesDone ||
		a.Size != b.Size ||
		a.Title != b.Title ||
		a.SavePath != b.SavePath ||
		a.Error != b.Error
}

// fail marks a download dead. anacrolix reports a failed chunk write and keeps
// running; for a download manager a disk that will not take bytes is the end of
// that download, and the user needs to see why.
func (e *Embedded) fail(id core.DownloadID, cause error) {
	e.mu.Lock()
	it, ok := e.items[id]
	if !ok {
		e.mu.Unlock()
		return
	}
	it.failure = cause.Error()
	pauseItem(it)
	snapshot := e.refreshLocked(it)
	e.mu.Unlock()

	e.logger.Error("download failed", "download", id, "err", cause)
	if err := e.save(e.ctx, snapshot); err != nil {
		e.logger.Warn("persisting download", "download", id, "err", err)
	}
}

// refreshLocked syncs an item's durable record with its live status and
// returns a copy of the record.
func (e *Embedded) refreshLocked(it *item) core.Download {
	st := e.statusLocked(it)
	it.rec.State = st.State
	it.rec.Progress = st.Progress
	it.rec.BytesDone = st.BytesDone
	it.rec.Size = st.Size
	it.rec.SavePath = st.SavePath
	it.rec.Error = st.Error
	if st.Name != "" {
		it.rec.Title = st.Name
	}
	return it.rec
}

// statusLocked maps anacrolix's view of a torrent onto core.DownloadStatus.
func (e *Embedded) statusLocked(it *item) core.DownloadStatus {
	t := it.t
	info := t.Info()
	complete := t.Complete().Bool()

	st := core.DownloadStatus{
		ID:         it.rec.EngineID,
		Name:       t.Name(),
		BytesDone:  t.BytesCompleted(),
		DownRate:   it.downRate,
		UpRate:     it.upRate,
		ETASeconds: -1,
		Error:      it.failure,
	}
	if info != nil {
		st.Size = t.Length()
		st.SavePath = path.Join(IncompleteDir, info.BestName())
	}
	if st.Size > 0 {
		st.Progress = float64(st.BytesDone) / float64(st.Size)
		if st.Progress > 1 {
			st.Progress = 1
		}
	}

	// Ratio is per-session: anacrolix counts bytes for as long as a torrent is
	// added, and a restart starts those counters over.
	stats := t.Stats()
	if read := stats.BytesReadData.Int64(); read > 0 {
		st.Ratio = float64(stats.BytesWrittenData.Int64()) / float64(read)
	}

	switch {
	case st.Error != "":
		// A failure outranks everything else: a download that died mid-transfer
		// must not read as merely paused.
		st.State = core.DownloadFailed
	case it.paused:
		// Paused is always resumable, seeding included: a paused seeder must
		// not collapse into "completed", a state the queue cannot unpause
		// (it reads as terminal). Complete or not, the user asked it to stop
		// and can ask it to start again.
		st.State = core.DownloadPaused
	case complete:
		// A complete torrent keeps uploading (cfg.Seed), so "seeding" is the
		// honest state.
		st.State = core.DownloadSeeding
		st.ETASeconds = 0
	case info == nil:
		// No metadata yet: nothing can be requested, so nothing is downloading.
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
func (e *Embedded) save(ctx context.Context, rec core.Download) error {
	if e.opts.Store == nil {
		return nil
	}
	if err := e.opts.Store.Save(ctx, rec); err != nil {
		return fmt.Errorf("download: persist %q: %w", rec.EngineID, err)
	}
	return nil
}
