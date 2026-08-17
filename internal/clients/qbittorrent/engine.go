package qbittorrent

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/torrentmeta"
)

// EngineName is what downloads handed to qBittorrent record in their
// `downloads.engine` column, so a library outlives the client that fetched it.
const EngineName = core.DownloadClientQBittorrent

// Tag is the qBittorrent tag Caravan marks its own torrents with.
//
// A tag rather than a category, because the category is the user's: it is a
// configurable field they may already sort their client by, and it may be
// shared with whatever else feeds that client. The tag is Caravan's, is added
// on top of whatever category is configured, and is what makes "the queue"
// mean "the torrents Caravan added" instead of "everything in qBittorrent".
//
// A torrent the user untags leaves Caravan's queue and keeps seeding — which
// is the right escape hatch, and the reason removal is never implicit.
const Tag = "caravan"

// Add has to wait when a release carries no info hash — a .torrent URL rather
// than a magnet link — because qBittorrent's add endpoint answers "queued",
// not "added", and cannot say what it produced. These bound that wait: a
// couple of seconds in total, which is well inside a grab's patience and far
// past a local client's parse time.
const (
	defaultAddPollInterval = 100 * time.Millisecond
	defaultAddPollAttempts = 20
)

// Engine drives one configured qBittorrent as a core.Engine.
//
// It holds no download state of its own (see the package comment): every
// method is a question asked of qBittorrent, so two Engines over the same
// client agree by construction and a restart loses nothing.
type Engine struct {
	c   *Client
	cfg core.DownloadClientConfig

	addPollInterval time.Duration
	addPollAttempts int
}

var _ core.Engine = (*Engine)(nil)

// NewEngine returns an engine for cfg. A nil hc gets a client with a default
// timeout. It does not talk to qBittorrent: construction must succeed before
// the storage root or the network is available, exactly like the embedded
// engine's provider expects.
func NewEngine(cfg core.DownloadClientConfig, hc *http.Client) (*Engine, error) {
	c, err := New(cfg, hc)
	if err != nil {
		return nil, err
	}
	return &Engine{
		c:               c,
		cfg:             cfg,
		addPollInterval: defaultAddPollInterval,
		addPollAttempts: defaultAddPollAttempts,
	}, nil
}

// Config returns the client configuration this engine was built for.
func (e *Engine) Config() core.DownloadClientConfig { return e.cfg }

// Client exposes the underlying WebUI client for calls that are not part of
// core.Engine.
func (e *Engine) Client() *Client { return e.c }

// Add hands a torrent release to qBittorrent and returns its info hash.
//
// opts is not used. Its Category is Caravan's internal routing label
// ("movies", "tv"); the qBittorrent category is the one on the client's own
// configuration, because that is the label the user chose to sort their client
// by and inventing two more would be Caravan reorganising someone else's
// client. The rest of opts is recorded by the caller in `grabs`.
//
// The save path is likewise left alone: where qBittorrent writes is
// qBittorrent's configuration (SPEC §5.1).
func (e *Engine) Add(ctx context.Context, r core.Release, opts core.AddOpts) (core.DownloadID, error) {
	if r.Protocol == core.ProtocolUsenet {
		return "", fmt.Errorf("qbittorrent: release %q is usenet: qBittorrent only handles torrents", r.Title)
	}
	link := strings.TrimSpace(r.DownloadURL)
	if link == "" && len(r.TorrentPayload) == 0 {
		return "", fmt.Errorf("qbittorrent: release %q has no download url", r.Title)
	}
	payloadHash := ""
	if len(r.TorrentPayload) > 0 {
		if len(r.TorrentPayload) > core.MaxTorrentPayloadBytes {
			return "", fmt.Errorf("qbittorrent: torrent payload exceeds size limit")
		}
		mi, _, err := torrentmeta.Parse(r.TorrentPayload)
		if err != nil {
			return "", fmt.Errorf("qbittorrent: read torrent payload: %w", err)
		}
		payloadHash = fmt.Sprintf("%x", mi.HashInfoBytes())
		if reported := normalizeHash(r.InfoHash); reported != "" && reported != payloadHash {
			return "", fmt.Errorf("qbittorrent: torrent payload info hash does not match release")
		}
	}

	// The info hash is the download id, and it is knowable up front for a
	// magnet link or an indexer that reported one. When it is not — a
	// .torrent URL qBittorrent has to fetch and parse — the only honest answer
	// is to watch our tag for the torrent that appears.
	want := payloadHash
	if want == "" {
		want = normalizeHash(r.InfoHash)
	}
	if want == "" {
		want = magnetHash(link)
	}
	var before map[string]struct{}
	if want == "" {
		var err error
		if before, err = e.tagged(ctx); err != nil {
			return "", err
		}
	}
	requestURL := link
	if len(r.TorrentPayload) > 0 {
		requestURL = ""
	}

	if err := e.c.Add(ctx, AddRequest{
		URL: requestURL, Payload: r.TorrentPayload, Category: e.cfg.Category, Tags: []string{Tag},
		Paused: opts.Paused,
	}); err != nil {
		return "", err
	}
	if want != "" {
		return core.DownloadID(want), nil
	}
	return e.discover(ctx, before, r.Title)
}

// discover waits for a torrent that was not in before to appear under our tag.
func (e *Engine) discover(ctx context.Context, before map[string]struct{}, title string) (core.DownloadID, error) {
	for attempt := 0; ; attempt++ {
		now, err := e.tagged(ctx)
		if err != nil {
			return "", err
		}
		for hash := range now {
			if _, seen := before[hash]; !seen {
				return core.DownloadID(hash), nil
			}
		}
		if attempt+1 >= e.addPollAttempts {
			// qBittorrent accepted the request and then produced nothing:
			// almost always a .torrent URL it could not fetch. Saying so beats
			// returning an id that names no torrent.
			return "", fmt.Errorf("qbittorrent: added %q but it did not appear in the queue", title)
		}
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(e.addPollInterval):
		}
	}
}

// tagged returns the info hashes currently carrying Caravan's tag.
func (e *Engine) tagged(ctx context.Context) (map[string]struct{}, error) {
	torrents, err := e.ours(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]struct{}, len(torrents))
	for _, t := range torrents {
		out[t.Hash] = struct{}{}
	}
	return out, nil
}

// ours returns the torrents Caravan added.
//
// The tag filter is re-applied here rather than trusted, because servers older
// than WebAPI 2.8.3 ignore the `tag` query parameter and answer with the whole
// queue. Without the second pass, an old qBittorrent would put every torrent
// the user has ever added into Caravan's queue.
func (e *Engine) ours(ctx context.Context) ([]Torrent, error) {
	torrents, err := e.c.Info(ctx, InfoQuery{Tag: Tag})
	if err != nil {
		return nil, err
	}
	out := make([]Torrent, 0, len(torrents))
	for _, t := range torrents {
		if hasTag(t.Tags, Tag) {
			out = append(out, t)
		}
	}
	return out, nil
}

// Status returns a live snapshot of one download. See core.Engine.
//
// It deliberately does not check the tag: a download Caravan is asking about
// by id is one it already knows, and a torrent whose tag a user removed should
// still report progress rather than vanish mid-transfer.
func (e *Engine) Status(ctx context.Context, id core.DownloadID) (*core.DownloadStatus, error) {
	torrents, err := e.c.Info(ctx, InfoQuery{Hashes: []string{string(id)}})
	if err != nil {
		return nil, err
	}
	// The hash is re-checked rather than assumed: qBittorrent treats an empty
	// `hashes` parameter as "no filter" and answers with the whole queue, and
	// reporting some unrelated torrent's progress as this download's would be
	// worse than reporting nothing.
	for _, t := range torrents {
		if strings.EqualFold(t.Hash, string(id)) {
			s := status(t)
			return &s, nil
		}
	}
	return nil, fmt.Errorf("qbittorrent: %s: %w", id, ErrNotFound)
}

// List returns every download Caravan added to this qBittorrent. See
// core.Engine.
func (e *Engine) List(ctx context.Context) ([]core.DownloadStatus, error) {
	torrents, err := e.ours(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]core.DownloadStatus, 0, len(torrents))
	for _, t := range torrents {
		out = append(out, status(t))
	}
	return out, nil
}

// ListPage returns a deterministic page over the provider snapshot. The
// provider has no cursor API, so this does not claim network pagination.
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

// Pause stops a torrent without discarding progress. See core.Engine.
//
// qBittorrent answers 200 for an info hash it does not know, so this cannot
// report ErrNotFound without a second round trip on every call. The queue only
// pauses rows it just listed, and the next poll shows the truth either way.
func (e *Engine) Pause(ctx context.Context, id core.DownloadID) error {
	return e.c.Stop(ctx, string(id))
}

// Resume restarts a paused torrent. See core.Engine and the note on Pause.
func (e *Engine) Resume(ctx context.Context, id core.DownloadID) error {
	return e.c.Start(ctx, string(id))
}

// Remove drops a torrent, and its data when deleteData is set. See core.Engine.
//
// An imported file is a hardlink or a move away from the download data, so
// deleting here must not cost media (SPEC §13) — which is the import track's
// contract to keep, not something this call can check.
func (e *Engine) Remove(ctx context.Context, id core.DownloadID, deleteData bool) error {
	return e.c.Delete(ctx, deleteData, string(id))
}

// Files returns one torrent's file list, for locating the payload once it
// finishes. It is not part of core.Engine: the import track calls it directly.
func (e *Engine) Files(ctx context.Context, id core.DownloadID) ([]File, error) {
	files, err := e.c.Files(ctx, string(id))
	var apiErr *APIError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil, fmt.Errorf("qbittorrent: %s: %w", id, ErrNotFound)
	}
	return files, err
}

// Close forgets the session. Nothing is flushed: qBittorrent holds the queue,
// and it keeps downloading whether Caravan is running or not.
func (e *Engine) Close() error { return e.c.Close() }

// TestConnection is the registry probe: it logs in and asks for the Web API
// version, which is the cheapest call that proves both the URL and the
// credentials.
//
// The error it returns is shown to the user, so it names what qBittorrent
// complained about and never the credential that was refused (SPEC §12).
func TestConnection(ctx context.Context, cfg core.DownloadClientConfig) error {
	c, err := New(cfg, nil)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	version, err := c.WebAPIVersion(ctx)
	if err != nil {
		return err
	}
	if version == "" {
		// A 200 with an empty body is something that is not qBittorrent —
		// usually a reverse proxy or a router's login page on the same port.
		return errors.New("qbittorrent: the URL answered but did not report a Web API version")
	}
	return nil
}

// Register installs the qBittorrent probe in a client registry. The serving
// process calls it once at startup; a failure is a wiring mistake.
func Register(r *clients.Registry) error {
	return r.Register(core.DownloadClientQBittorrent, TestConnection)
}

// hasTag reports whether a comma-concatenated qBittorrent tag list contains
// tag. qBittorrent joins tags with ", " in some versions and "," in others.
func hasTag(tags, tag string) bool {
	for _, t := range strings.Split(tags, ",") {
		if strings.TrimSpace(t) == tag {
			return true
		}
	}
	return false
}

// normalizeHash lowercases a v1 info hash, or returns "" when the value is not
// one. Anything else — a base32 magnet, a full v2 hash, junk from an indexer —
// is reported as unknown so the caller falls back to watching the queue rather
// than inventing a download id that names nothing.
func normalizeHash(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) != 40 {
		return ""
	}
	if _, err := hex.DecodeString(s); err != nil {
		return ""
	}
	return s
}

// magnetHash extracts the v1 info hash from a magnet link.
func magnetHash(link string) string {
	if !strings.HasPrefix(strings.ToLower(link), "magnet:") {
		return ""
	}
	u, err := url.Parse(link)
	if err != nil {
		return ""
	}
	for _, xt := range u.Query()["xt"] {
		if rest, ok := strings.CutPrefix(strings.ToLower(xt), "urn:btih:"); ok {
			if h := normalizeHash(rest); h != "" {
				return h
			}
		}
	}
	return ""
}
