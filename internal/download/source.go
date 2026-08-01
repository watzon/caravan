package download

import (
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
		if err != nil {
			return nil, fmt.Errorf("download: fetch torrent for %q: %w", r.Title, err)
		}
		spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
		if err != nil {
			return nil, fmt.Errorf("download: read torrent for %q: %w", r.Title, err)
		}
		return spec, nil
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

// fetchMetainfo downloads and parses a .torrent file.
func (e *Embedded) fetchMetainfo(ctx context.Context, url string) (*metainfo.MetaInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := e.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s returned %s", url, resp.Status)
	}
	mi, err := metainfo.Load(io.LimitReader(resp.Body, maxMetainfoBytes))
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
