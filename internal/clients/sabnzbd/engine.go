package sabnzbd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
)

// EngineName is what downloads handed to SABnzbd record in their
// `downloads.engine` column, so a library outlives the client that fetched it.
const EngineName = core.DownloadClientSABnzbd

// Engine drives one configured SABnzbd as a core.Engine.
//
// Like the qBittorrent engine it holds no download state of its own: every
// method is a question asked of SABnzbd, so two Engines over the same client
// agree by construction and a restart loses nothing.
type Engine struct {
	c   *Client
	cfg core.DownloadClientConfig
}

var _ core.Engine = (*Engine)(nil)

// NewEngine returns an engine for cfg. A nil hc gets a client with a default
// timeout. It does not talk to SABnzbd: construction must succeed before the
// storage root or the network is available.
func NewEngine(cfg core.DownloadClientConfig, hc *http.Client) (*Engine, error) {
	c, err := New(cfg, hc)
	if err != nil {
		return nil, err
	}
	return &Engine{c: c, cfg: cfg}, nil
}

// Config returns the client configuration this engine was built for.
func (e *Engine) Config() core.DownloadClientConfig { return e.cfg }

// Client exposes the underlying API client for calls that are not part of
// core.Engine.
func (e *Engine) Client() *Client { return e.c }

// Add hands an NZB link to SABnzbd and returns its nzo_id.
//
// The link is handed over rather than fetched: SABnzbd downloads NZBs itself,
// keeps the same nzo_id from the moment it accepts the link to the moment the
// job lands in its history, and does a better job of retrying a flaky indexer
// than a single grab would.
//
// opts is not used. Its Category is Caravan's internal routing label
// ("movies", "tv"); the SABnzbd category is the one on the client's own
// configuration, because that is the label the user sorts their client by and
// it decides where SABnzbd writes. The rest of opts is recorded by the caller
// in `grabs`.
func (e *Engine) Add(ctx context.Context, r core.Release, opts core.AddOpts) (core.DownloadID, error) {
	if r.Protocol == core.ProtocolTorrent {
		return "", fmt.Errorf("sabnzbd: release %q is a torrent: SABnzbd only handles usenet", r.Title)
	}
	link := strings.TrimSpace(r.DownloadURL)
	if link == "" {
		return "", fmt.Errorf("sabnzbd: release %q has no download url", r.Title)
	}

	// The release title is sent as the job name so SABnzbd's queue, its
	// directory names and Caravan's grab all say the same thing — an indexer's
	// download link is usually named after a numeric id.
	id, err := e.c.AddURL(ctx, AddRequest{
		URL: link, Name: r.Title, Category: e.cfg.Category,
		// Over the concurrency cap: SABnzbd holds the job at paused priority
		// and fetches nothing until Caravan resumes it.
		Paused: opts.Paused,
	})
	if err != nil {
		return "", err
	}
	return core.DownloadID(id), nil
}

// Status returns a live snapshot of one download. See core.Engine.
//
// Both lists are asked because a job crosses from one to the other the instant
// its transfer ends, and the caller polling it must not see it disappear for a
// tick.
func (e *Engine) Status(ctx context.Context, id core.DownloadID) (*core.DownloadStatus, error) {
	if strings.TrimSpace(string(id)) == "" {
		// SABnzbd reads an empty nzo_ids filter as "no filter" and answers
		// with everything; reporting a stranger's job as this download would
		// be worse than reporting nothing.
		return nil, fmt.Errorf("sabnzbd: %w", ErrNotFound)
	}
	q := Query{NZOIDs: []string{string(id)}}

	queue, err := e.c.Queue(ctx, q)
	if err != nil {
		return nil, err
	}
	rate := int64(queue.KBPerSec.Float() * kibibyte)
	for _, slot := range queue.Slots {
		if slot.NZOID == string(id) {
			s := queueStatus(slot, rate)
			return &s, nil
		}
	}

	history, err := e.c.History(ctx, q)
	if err != nil {
		return nil, err
	}
	for _, slot := range history {
		if slot.NZOID == string(id) {
			s := historyStatus(slot)
			return &s, nil
		}
	}
	return nil, fmt.Errorf("sabnzbd: %s: %w", id, ErrNotFound)
}

// List returns every download Caravan can claim in this SABnzbd. See
// core.Engine.
//
// "Caravan's" means the configured category when there is one, and everything
// otherwise. SABnzbd has no tags — the qBittorrent backend's way of marking
// its own downloads — so the category is the only marker available, and it is
// the field these clients are conventionally partitioned by. With no category
// configured the queue shows the user's other Usenet downloads too; they are
// surfaced as grab-less rows rather than hidden, and the fix is to set a
// category.
func (e *Engine) List(ctx context.Context) ([]core.DownloadStatus, error) {
	q := Query{Category: e.cfg.Category}

	queue, err := e.c.Queue(ctx, q)
	if err != nil {
		return nil, err
	}
	history, err := e.c.History(ctx, q)
	if err != nil {
		return nil, err
	}

	rate := int64(queue.KBPerSec.Float() * kibibyte)
	out := make([]core.DownloadStatus, 0, len(queue.Slots)+len(history))
	for _, slot := range queue.Slots {
		out = append(out, queueStatus(slot, rate))
	}
	for _, slot := range history {
		out = append(out, historyStatus(slot))
	}
	return out, nil
}

// Pause stops transferring one job without discarding progress. See
// core.Engine.
//
// SABnzbd answers 200 for an nzo_id it does not know, so this cannot report
// ErrNotFound without a second round trip on every call. The queue only pauses
// rows it just listed, and the next poll shows the truth either way.
func (e *Engine) Pause(ctx context.Context, id core.DownloadID) error {
	return e.c.PauseJob(ctx, string(id))
}

// Resume restarts a paused job. See core.Engine and the note on Pause.
func (e *Engine) Resume(ctx context.Context, id core.DownloadID) error {
	return e.c.ResumeJob(ctx, string(id))
}

// Remove drops a download, and its data when deleteData is set. See
// core.Engine.
//
// Both lists are cleared, because "remove this download" means the same thing
// whether SABnzbd currently files it under queue or history, and a caller
// polling every few seconds cannot know which it is. Deleting from the list a
// job is not in is a no-op in SABnzbd, so this is one honest operation rather
// than a lookup and a race.
//
// An imported file is a hardlink or a move away from the download data, so
// deleting here must not cost media (SPEC §13) — the import track's contract
// to keep, not something this call can check.
func (e *Engine) Remove(ctx context.Context, id core.DownloadID, deleteData bool) error {
	queueErr := e.c.DeleteQueue(ctx, string(id), deleteData)
	historyErr := e.c.DeleteHistory(ctx, string(id), deleteData)
	return errors.Join(queueErr, historyErr)
}

// Close releases nothing: SABnzbd holds the queue and keeps downloading
// whether Caravan is running or not.
func (e *Engine) Close() error { return e.c.Close() }

// TestConnection is the registry probe.
//
// It asks two questions because they fail differently: `version` proves
// something that talks like SABnzbd is at the URL, and `queue` proves the API
// key is accepted — SABnzbd answers `version` without checking the key, so
// stopping there would call a wrong key reachable.
//
// The error it returns is shown to the user, so it names what SABnzbd
// complained about and never the key that was refused (SPEC §12).
func TestConnection(ctx context.Context, cfg core.DownloadClientConfig) error {
	c, err := New(cfg, nil)
	if err != nil {
		return err
	}
	defer func() { _ = c.Close() }()

	version, err := c.Version(ctx)
	if err != nil {
		return err
	}
	if version == "" {
		// A 200 with no version is something that is not SABnzbd — usually a
		// reverse proxy or a router's login page on the same port.
		return errors.New("sabnzbd: the URL answered but did not report a version")
	}
	// One row is enough to prove the key: this is a probe, not a poll.
	if _, err := c.Queue(ctx, Query{Limit: 1}); err != nil {
		return err
	}
	return nil
}

// Register installs the SABnzbd probe in a client registry. The serving
// process calls it once at startup; a failure is a wiring mistake.
func Register(r *clients.Registry) error {
	return r.Register(core.DownloadClientSABnzbd, TestConnection)
}
