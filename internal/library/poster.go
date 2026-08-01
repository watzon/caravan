package library

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
)

// PosterName is the poster's filename inside an item folder (SPEC §6).
const PosterName = "poster.jpg"

// maxPosterBytes caps a poster download. Posters are a few hundred kilobytes;
// anything past this is a misconfigured URL, and the library scan must not
// stream an unbounded body into the media folder.
const maxPosterBytes = 16 << 20

// ensurePoster downloads posterURL into dir/poster.jpg and returns the
// storage-root-relative path it wrote.
//
// It returns an empty path with no error in the two "nothing to do" cases: an
// empty URL (the provider had no image), and a poster already on disk. Posters
// never change for a given item, so re-fetching one on every rescan would be
// pure network cost.
func (m *Manager) ensurePoster(ctx context.Context, dir, posterURL string) (string, error) {
	rel := path.Join(dir, PosterName)
	if posterURL == "" {
		return "", nil
	}
	if _, err := os.Stat(m.abs(rel)); err == nil {
		return rel, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, posterURL, nil)
	if err != nil {
		return "", fmt.Errorf("library: poster request %s: %w", posterURL, err)
	}
	resp, err := m.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("library: fetch poster %s: %w", posterURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("library: fetch poster %s: unexpected status %s", posterURL, resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPosterBytes))
	if err != nil {
		return "", fmt.Errorf("library: read poster %s: %w", posterURL, err)
	}
	if len(body) == 0 {
		return "", fmt.Errorf("library: poster %s is empty", posterURL)
	}
	if err := m.writeFileAtomic(rel, body); err != nil {
		return "", err
	}
	return rel, nil
}
