package download

import (
	"bytes"
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/watzon/caravan/internal/core"
)

// metainfoTimeout bounds fetching one .torrent file. Indexers stall; grabs
// must not.
const metainfoTimeout = 30 * time.Second

// maxMetainfoBytes caps a .torrent download. Real ones are kilobytes; a
// multi-terabyte torrent with tiny pieces is still under a megabyte.
const maxMetainfoBytes = 4 << 20

// infoHashLength is the hex length of a BitTorrent v1 info hash.
const infoHashLength = 2 * metainfo.HashSize

// torrentSpec turns a release into something the client can add. Indexers hand
// out three shapes of the same thing — a magnet link, a URL to a .torrent, or
// a bare info hash — so all three are accepted, in that order of preference:
// a magnet and a .torrent carry trackers, a bare hash carries nothing.
func (e *Embedded) torrentSpec(ctx context.Context, r core.Release) (*torrent.TorrentSpec, error) {
	url := strings.TrimSpace(r.DownloadURL)
	lower := strings.ToLower(url)

	switch {
	case strings.HasPrefix(lower, "magnet:"):
		spec, err := torrent.TorrentSpecFromMagnetUri(url)
		if err != nil {
			return nil, fmt.Errorf("download: parse magnet for %q: %w", r.Title, err)
		}
		return spec, nil

	case strings.HasPrefix(lower, "http://"), strings.HasPrefix(lower, "https://"):
		mi, err := e.fetchMetainfo(ctx, url)
		if err == nil {
			spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
			if err != nil {
				return nil, fmt.Errorf("download: read torrent for %q: %w", r.Title, err)
			}
			return spec, nil
		}
		// A dead or lying .torrent URL (an indexer serving an HTML error page
		// with a 200, a stale storage link) does not have to kill the grab
		// when the release also names its info hash: the swarm can supply the
		// metadata. Slower start, but a download instead of an error.
		if hash, herr := parseInfoHash(r.InfoHash); herr == nil {
			e.logger.Warn("torrent fetch failed, falling back to the info hash",
				"title", r.Title, "url", url, "error", err)
			return &torrent.TorrentSpec{
				AddTorrentOpts: torrent.AddTorrentOpts{InfoHash: hash},
				DisplayName:    r.Title,
			}, nil
		}
		return nil, fmt.Errorf("download: fetch torrent for %q: %w", r.Title, err)
	}

	// Neither: the only thing left that identifies a torrent is an info hash,
	// which some indexers publish in its own field and some put in the
	// download link.
	raw := r.InfoHash
	if strings.TrimSpace(raw) == "" {
		raw = url
	}
	hash, err := parseInfoHash(raw)
	if err != nil {
		return nil, fmt.Errorf("download: %q has no usable torrent source: %w", r.Title, err)
	}
	return &torrent.TorrentSpec{AddTorrentOpts: torrent.AddTorrentOpts{InfoHash: hash}}, nil
}

// restoreSpec rebuilds the spec for a persisted download. The metainfo sidecar
// is preferred because it restores the trackers and the info dict along with
// the hash; without it the download resumes as a bare hash and has to find
// peers through DHT or PEX again.
func (e *Embedded) restoreSpec(rec core.Download) (*torrent.TorrentSpec, error) {
	if mi, err := metainfo.LoadFromFile(e.metainfoPath(rec.EngineID)); err == nil {
		if spec, err := torrent.TorrentSpecFromMetaInfoErr(mi); err == nil {
			return spec, nil
		}
	} else if !os.IsNotExist(err) {
		e.logger.Warn("reading metainfo sidecar", "download", rec.EngineID, "err", err)
	}

	hash, err := parseInfoHash(string(rec.EngineID))
	if err != nil {
		return nil, err
	}
	return &torrent.TorrentSpec{
		AddTorrentOpts: torrent.AddTorrentOpts{InfoHash: hash},
		DisplayName:    rec.Title,
	}, nil
}

// FetchPayload GETs url and returns at most max bytes of the response body.
//
// It is the one place a release's payload is pulled off an indexer, shared by
// the two embedded engines: a .torrent here and a .nzb in internal/usenet.
// Both face the same indexer behaviour — a link that 404s, one that serves an
// HTML error page with a 200, one that would stream forever — so the status
// check and the size cap belong together rather than once per engine.
//
// The cap truncates rather than erroring; the caller's parser is what decides
// whether what came back is usable, and every one of them already refuses a
// document it could not read to the end.
func FetchPayload(ctx context.Context, hc *http.Client, url string, max int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	return io.ReadAll(io.LimitReader(resp.Body, max))
}

// fetchMetainfo downloads and parses a .torrent file.
func (e *Embedded) fetchMetainfo(ctx context.Context, url string) (*metainfo.MetaInfo, error) {
	body, err := FetchPayload(ctx, e.http, url, maxMetainfoBytes)
	if err != nil {
		return nil, err
	}
	mi, err := metainfo.Load(bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	return mi, nil
}

// writeMetainfo saves a torrent's metainfo beside its data, so the next start
// can resume it without re-fetching metadata from peers.
//
// It writes through a temporary file: a half-written sidecar read after a
// crash would be worse than a missing one, which is merely a slower resume.
func (e *Embedded) writeMetainfo(id core.DownloadID, t *torrent.Torrent) error {
	final := e.metainfoPath(id)
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(final), "."+string(id)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())

	mi := t.Metainfo()
	if err := mi.Write(f); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), final)
}

// metainfoPath is where one download's sidecar lives. The id is a hex info
// hash, so it is always a safe filename.
func (e *Embedded) metainfoPath(id core.DownloadID) string {
	return filepath.Join(e.incomplete, metaDir, string(id)+".torrent")
}

// parseInfoHash reads a v1 info hash. Only hex is accepted: base32 hashes
// exist, but they arrive inside magnet links, which are parsed as magnets.
func parseInfoHash(s string) (metainfo.Hash, error) {
	var hash metainfo.Hash
	s = strings.TrimSpace(s)
	if len(s) != infoHashLength {
		return hash, fmt.Errorf("%q is not a %d-character info hash", s, infoHashLength)
	}
	b, err := hex.DecodeString(s)
	if err != nil {
		return hash, fmt.Errorf("%q is not a hex info hash: %w", s, err)
	}
	copy(hash[:], b)
	return hash, nil
}
