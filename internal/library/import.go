package library

import (
	"context"
	"fmt"
	"os"

	"github.com/watzon/caravan/internal/core"
)

// ImportResult describes a resolved manual match.
type ImportResult struct {
	// Path is the file's storage-root-relative path after organizing.
	Path string
	// MovieID is set for a movie import, SeriesID for an episode import.
	MovieID  int64
	SeriesID int64
	// Warnings holds non-fatal problems (an NFO or poster that would not
	// write). The file itself is imported regardless.
	Warnings []string
}

// ImportUnmatched resolves a parked file into the library against a
// user-chosen provider id (SPEC §10.1, §13: the scan-review screen's "this is
// actually X" action).
//
// mediaType is MediaTypeMovie or MediaTypeSeries. The parked file's parsed
// season and episode numbers are reused as-is — the user is correcting *what*
// the file is, not which episode of it — so a series import needs a filename
// the parser found an episode number in.
//
// On success the file is organized, its metadata rows are written, and the
// unmatched entry is cleared.
func (m *Manager) ImportUnmatched(ctx context.Context, unmatchedID, tmdbID int64, mediaType string) (*ImportResult, error) {
	if m.provider == nil {
		return nil, core.ErrNoMetadataProvider
	}
	if tmdbID <= 0 {
		return nil, fmt.Errorf("library: invalid tmdb id %d", tmdbID)
	}

	u, err := m.store.GetUnmatchedFile(ctx, unmatchedID)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(m.abs(u.Path))
	if err != nil {
		return nil, fmt.Errorf("library: unmatched file %q: %w", u.Path, err)
	}

	res := &ImportResult{}
	warn := func(format string, args ...any) {
		res.Warnings = append(res.Warnings, fmt.Sprintf(format, args...))
	}

	switch mediaType {
	case MediaTypeMovie:
		meta, err := m.provider.GetMovie(ctx, tmdbID)
		if err != nil {
			return nil, fmt.Errorf("library: get movie %d: %w", tmdbID, err)
		}
		if meta == nil {
			return nil, fmt.Errorf("library: movie %d not found", tmdbID)
		}
		res.Path, res.MovieID, err = m.importMovie(ctx, meta, u.Path, info.Size(), u.Parsed, warn)
		if err != nil {
			return nil, err
		}

	case MediaTypeSeries:
		if !u.Parsed.IsEpisode() {
			return nil, fmt.Errorf("library: %q has no season/episode number to import as an episode", u.Path)
		}
		meta, err := m.provider.GetSeries(ctx, tmdbID)
		if err != nil {
			return nil, fmt.Errorf("library: get series %d: %w", tmdbID, err)
		}
		if meta == nil {
			return nil, fmt.Errorf("library: series %d not found", tmdbID)
		}
		res.Path, res.SeriesID, err = m.importEpisode(ctx, meta, u.Path, info.Size(), u.Parsed, warn)
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("library: unknown media type %q", mediaType)
	}

	return res, nil
}
