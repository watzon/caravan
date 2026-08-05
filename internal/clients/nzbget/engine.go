package nzbget

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/watzon/caravan/internal/clients"
	"github.com/watzon/caravan/internal/core"
)

// EngineName is what downloads handed to NZBGet record in their
// `downloads.engine` column, so a library outlives the client that fetched it.
const EngineName = core.DownloadClientNZBGet

// maxNZBBytes bounds the NZB fetched from an indexer before it is handed to
// NZBGet. A season pack's NZB is a few megabytes of XML; past this is an
// indexer serving something that is not an NZB.
const maxNZBBytes = 32 << 20

// Engine drives one configured NZBGet as a core.Engine.
//
// Like the other external backends it holds no download state of its own:
// every method is a question asked of NZBGet, so two Engines over the same
// client agree by construction and a restart loses nothing.
type Engine struct {
	c   *Client
	cfg core.DownloadClientConfig
	// hc fetches NZBs from indexers. It is the caller's client so a fan-out
	// reuses connections, and it is separate from the RPC path because the
	// requests go to a different host with different credentials.
	hc *http.Client
}

var _ core.Engine = (*Engine)(nil)

// NewEngine returns an engine for cfg. A nil hc gets a client with a default
// timeout. It does not talk to NZBGet: construction must succeed before the
// storage root or the network is available.
func NewEngine(cfg core.DownloadClientConfig, hc *http.Client) (*Engine, error) {
	c, err := New(cfg, hc)
	if err != nil {
		return nil, err
	}
	if hc == nil {
		hc = &http.Client{Timeout: defaultTimeout}
	}
	return &Engine{c: c, cfg: cfg, hc: hc}, nil
}

// Config returns the client configuration this engine was built for.
func (e *Engine) Config() core.DownloadClientConfig { return e.cfg }

// Client exposes the underlying RPC client for calls that are not part of
// core.Engine.
func (e *Engine) Client() *Client { return e.c }

// Add fetches the release's NZB and uploads it to NZBGet, returning the NZBID.
//
// The NZB is fetched here rather than handed to NZBGet as a link, which is the
// one place this backend does more work than the others. NZBGet's `append`
// takes a URL, but it files it as a placeholder with its own id and mints a
// *different* id once the NZB has been fetched — so the handle returned to the
// caller would name nothing a minute later, and the download would vanish from
// the queue. Uploading the bytes gets the real id back straight away. It also
// keeps the indexer's API key, which is in that URL, out of NZBGet's queue,
// its web UI and its logs (SPEC §12).
//
// opts is not used. Its Category is Caravan's internal routing label
// ("movies", "tv"); the NZBGet category is the one on the client's own
// configuration, because that is the label the user sorts their client by and
// it decides where NZBGet writes. The rest of opts is recorded by the caller
// in `grabs`.
func (e *Engine) Add(ctx context.Context, r core.Release, opts core.AddOpts) (core.DownloadID, error) {
	if r.Protocol == core.ProtocolTorrent {
		return "", fmt.Errorf("nzbget: release %q is a torrent: NZBGet only handles usenet", r.Title)
	}
	link := strings.TrimSpace(r.DownloadURL)
	if link == "" {
		return "", fmt.Errorf("nzbget: release %q has no download url", r.Title)
	}

	content, err := e.fetchNZB(ctx, link)
	if err != nil {
		return "", err
	}
	nzbID, err := e.c.Append(ctx, AppendRequest{
		Filename: nzbFilename(r.Title),
		Content:  content,
		Category: e.cfg.Category,
		// Over the concurrency cap: NZBGet takes the NZB and does nothing with
		// it until Caravan resumes it.
		Paused: opts.Paused,
	})
	if err != nil {
		return "", err
	}
	return id(nzbID), nil
}

// fetchNZB downloads the release's NZB.
//
// The answer is checked for shape because indexers answer a rate limit, an
// expired key or a missing release with a 200 and an HTML page far more often
// than with a status code. Base64-ing that into NZBGet would produce a
// download that fails minutes later for no stated reason; failing here names
// the problem while the user is looking at it.
//
// No part of the URL reaches an error message: it carries the indexer's API
// key (SPEC §12).
func (e *Engine) fetchNZB(ctx context.Context, link string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, link, nil)
	if err != nil {
		return nil, fmt.Errorf("nzbget: fetch nzb: %w", clients.Scrub(err))
	}
	resp, err := e.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("nzbget: fetch nzb: %w", clients.Scrub(err))
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxNZBBytes))
	if err != nil {
		return nil, fmt.Errorf("nzbget: fetch nzb: %w", clients.Scrub(err))
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("nzbget: fetch nzb: the indexer answered http %d: %s", resp.StatusCode, clients.Snippet(body))
	}
	if err := looksLikeNZB(body); err != nil {
		return nil, fmt.Errorf("nzbget: fetch nzb: %w", err)
	}
	return body, nil
}

// rootScan is how far into a body the NZB root element is looked for. It sits
// behind an XML declaration and at most a DOCTYPE, which is a few hundred
// bytes; the rest of the allowance is slack.
const rootScan = 8 << 10

// looksLikeNZB reports why a fetched body cannot be an NZB, or nil when it
// could be. It is a shape check, not a parse: NZBGet is the one that has to
// understand the file.
//
// The root element is what is looked for rather than a leading '<', because an
// indexer's rate-limit or expired-key page is HTML and starts with one too \u2014
// and that page is the failure this check exists to catch.
func looksLikeNZB(body []byte) error {
	// The trim set includes a UTF-8 byte-order mark, which some indexers put
	// in front of the XML declaration.
	trimmed := bytes.TrimLeft(body, " \t\r\n\uFEFF")
	if len(trimmed) == 0 {
		return errors.New("the indexer returned an empty body")
	}
	if bytes.HasPrefix(trimmed, []byte{0x1f, 0x8b}) {
		// Answered with a .nzb.gz rather than with Content-Encoding, which
		// net/http would have unwrapped.
		return errors.New("the indexer returned a gzip-compressed body, not an NZB")
	}
	head := trimmed
	if len(head) > rootScan {
		head = head[:rootScan]
	}
	if !bytes.Contains(bytes.ToLower(head), []byte("<nzb")) {
		return fmt.Errorf("the indexer did not return an NZB: %s", clients.Snippet(trimmed))
	}
	return nil
}

// nzbFilename names the job NZBGet files the NZB under. NZBGet reads the
// extension to decide how to handle the content, and uses the stem for the
// download's directory, so the release title goes in unchanged.
func nzbFilename(title string) string {
	title = strings.TrimSpace(title)
	if title == "" {
		title = "caravan"
	}
	if strings.HasSuffix(strings.ToLower(title), ".nzb") {
		return title
	}
	return title + ".nzb"
}

// Status returns a live snapshot of one download. See core.Engine.
//
// Both lists are asked because a download crosses from one to the other the
// instant NZBGet is finished with it, and the caller polling it must not see it
// disappear for a tick.
func (e *Engine) Status(ctx context.Context, downloadID core.DownloadID) (*core.DownloadStatus, error) {
	nzbID, ok := parseID(downloadID)
	if !ok {
		return nil, fmt.Errorf("nzbget: %q: %w", downloadID, ErrNotFound)
	}

	groups, err := e.c.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	for _, g := range groups {
		if g.NZBID == nzbID {
			rate, err := e.rate(ctx)
			if err != nil {
				return nil, err
			}
			s := groupStatus(g, rate)
			return &s, nil
		}
	}

	history, err := e.c.History(ctx)
	if err != nil {
		return nil, err
	}
	for _, h := range history {
		if h.NZBID == nzbID {
			s := historyStatus(h)
			return &s, nil
		}
	}
	return nil, fmt.Errorf("nzbget: %s: %w", downloadID, ErrNotFound)
}

// List returns every download Caravan can claim in this NZBGet. See
// core.Engine.
//
// "Caravan's" means the configured category when there is one, and everything
// otherwise. NZBGet has no tags — the qBittorrent backend's way of marking its
// own downloads — so the category is the only marker available, and it is the
// field these clients are conventionally partitioned by. With no category
// configured the queue shows the user's other Usenet downloads too; they are
// surfaced as grab-less rows rather than hidden, and the fix is to set a
// category.
func (e *Engine) List(ctx context.Context) ([]core.DownloadStatus, error) {
	groups, err := e.c.ListGroups(ctx)
	if err != nil {
		return nil, err
	}
	history, err := e.c.History(ctx)
	if err != nil {
		return nil, err
	}
	rate, err := e.rate(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]core.DownloadStatus, 0, len(groups)+len(history))
	for _, g := range groups {
		if !e.ours(g.Category) {
			continue
		}
		out = append(out, groupStatus(g, rate))
	}
	for _, h := range history {
		if !e.ours(h.Category) {
			continue
		}
		out = append(out, historyStatus(h))
	}
	return out, nil
}

// ours reports whether a download in this category belongs to Caravan's queue.
func (e *Engine) ours(category string) bool {
	return e.cfg.Category == "" || e.cfg.Category == category
}

// rate asks for the server-wide download rate, which is the rate of whichever
// download is currently transferring. NZBGet publishes no per-group speed, so
// this is the only rate there is.
func (e *Engine) rate(ctx context.Context) (int64, error) {
	status, err := e.c.Status(ctx)
	if err != nil {
		return 0, err
	}
	return max(status.DownloadRate, 0), nil
}

// Pause stops transferring one download without discarding progress. See
// core.Engine.
//
// A download NZBGet does not have queued is not reported as an error: it has
// almost certainly just finished into the history, where pausing means
// nothing, and the next poll shows the truth either way.
func (e *Engine) Pause(ctx context.Context, downloadID core.DownloadID) error {
	return e.edit(ctx, EditGroupPause, downloadID)
}

// Resume restarts a paused download. See core.Engine and the note on Pause.
func (e *Engine) Resume(ctx context.Context, downloadID core.DownloadID) error {
	return e.edit(ctx, EditGroupResume, downloadID)
}

func (e *Engine) edit(ctx context.Context, command string, downloadID core.DownloadID) error {
	nzbID, ok := parseID(downloadID)
	if !ok {
		return fmt.Errorf("nzbget: %q: %w", downloadID, ErrNotFound)
	}
	_, err := e.c.EditQueue(ctx, command, nzbID)
	return err
}

// Remove drops a download from whichever list holds it. See core.Engine.
//
// NZBGet answers an edit aimed at the wrong list with a plain false, which is
// what picks the second call: a queued download is deleted from the queue, and
// only a download the queue did not know is looked for in the history. Neither
// finding it is success, not an error — removal is idempotent, and a download
// that is already gone is the state the caller asked for.
//
// deleteData is honoured as far as NZBGet allows, which is not all the way.
// NZBGet always removes a download's partial and failed data, and it has no
// API at all for deleting a *completed* download's payload — that stays on
// disk for the user's own retention settings to deal with. Nothing here
// touches the library either way: an imported file is a hardlink or a move
// away from the download data, and removing a download must not cost media
// (SPEC §13).
func (e *Engine) Remove(ctx context.Context, downloadID core.DownloadID, deleteData bool) error {
	nzbID, ok := parseID(downloadID)
	if !ok {
		return nil
	}
	removed, err := e.c.EditQueue(ctx, EditGroupFinalDelete, nzbID)
	if err != nil {
		return err
	}
	if removed {
		return nil
	}
	_, err = e.c.EditQueue(ctx, EditHistoryFinalDelete, nzbID)
	return err
}

// Close releases nothing: NZBGet holds the queue and keeps downloading whether
// Caravan is running or not.
func (e *Engine) Close() error { return e.c.Close() }

// TestConnection is the registry probe: it asks for the version, which is the
// cheapest call that proves both the URL and the control login — NZBGet
// answers every RPC with a 401 until the credentials are right.
//
// The error it returns is shown to the user, so it names what NZBGet
// complained about and never the credential that was refused (SPEC §12).
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
		// A well-formed answer with an empty version is something that is not
		// NZBGet — usually a reverse proxy on the same port.
		return errors.New("nzbget: the URL answered but did not report a version")
	}
	return nil
}

// Register installs the NZBGet probe in a client registry. The serving process
// calls it once at startup; a failure is a wiring mistake.
func Register(r *clients.Registry) error {
	return r.Register(core.DownloadClientNZBGet, TestConnection)
}
