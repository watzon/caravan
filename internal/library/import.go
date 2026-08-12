package library

import (
	"context"
	"fmt"
	"os"
	"strings"

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

// dispositionFor decides what happens to a parked file's original copy once it
// is organized. A file already inside the library is being moved into place and
// must not leave its old name behind for the next scan to rediscover; a file
// outside it was parked by the import pipeline and still belongs to a download
// engine that may be seeding it (SPEC §5.1, §13), so it is linked, not moved.
func dispositionFor(rel string) sourceDisposition {
	if rel == LibraryDir || strings.HasPrefix(rel, LibraryDir+"/") {
		return consumeSource
	}
	return keepSource
}

// sceneMatchLibrary resolves the parked file's adult shelf before any provider
// call. A library-scoped download must stay on that shelf; an old unscoped scan
// entry uses the default adult library.
func (m *Manager) sceneMatchLibrary(ctx context.Context, libraryID int64) (*core.Library, error) {
	var (
		lib *core.Library
		err error
	)
	if libraryID == 0 {
		lib, err = m.store.GetDefaultLibrary(ctx, core.LibraryKindAdult)
	} else {
		lib, err = m.store.GetLibrary(ctx, libraryID)
	}
	if err != nil {
		return nil, err
	}
	if lib.Kind != core.LibraryKindAdult {
		return nil, fmt.Errorf("library: library %d is %s, not adult", lib.ID, lib.Kind)
	}
	if err := m.adultReadyIn(lib); err != nil {
		return nil, err
	}
	return lib, nil
}

// ImportUnmatched resolves a parked file into the library against a
// user-chosen provider id (SPEC §10.1, §13: the scan-review screen's "this is
// actually X" action, and the stuck-import queue's).
//
// mediaType is MediaTypeMovie, MediaTypeSeries, or MediaTypeScene. Movie and
// series matches resolve a title through its metadata provider. A scene match
// resolves the exact provider scene the user selected, adds and walks its site,
// and imports against that scene row. This explicit scene id is the door out of
// same-day ambiguity: a date alone cannot choose between two releases.
//
// A series match reuses the parked file's parsed season and episode numbers
// as-is — the user is correcting *what* the file is, not which episode of it —
// so it needs a filename the parser found an episode number in. A filename
// carrying only an absolute number counts: the number is the file's own claim,
// and the series the user just named is what places it (resolveAbsolute). That
// is the door out of a reasonNoAbsoluteMatch park, and without it a file parked
// for numbering could only ever be re-parked.
//
// On success the file is organized, its metadata rows are written, and the
// unmatched entry is cleared.
//
// ref names the provider the user's choice came from and that provider's own
// id for it, and it is the only thing that decides which client is asked.
// There is deliberately no Manager-level provider gate in front of that: an
// install whose libraries identify through some other provider entirely has no
// TMDB client, and must still be able to match a parked file.

func (m *Manager) ImportUnmatched(ctx context.Context, unmatchedID int64, ref core.ItemRef, mediaType string) (*ImportResult, error) {
	if !ref.Valid() {
		return nil, fmt.Errorf("library: invalid provider ref %q/%q", ref.Provider, ref.Ref)
	}
	var provider core.MetadataProvider
	if mediaType != MediaTypeScene {
		provider = m.providerByID(ctx, ref.Provider)
		if provider == nil {
			return nil, core.ErrNoMetadataProvider
		}
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
	var adultEpisodeID int64

	switch mediaType {
	case MediaTypeMovie:
		meta, err := provider.GetMovie(ctx, ref.Ref)
		if err != nil {
			return nil, fmt.Errorf("library: get movie %s/%s: %w", ref.Provider, ref.Ref, err)
		}
		if meta == nil {
			return nil, fmt.Errorf("library: movie %s/%s not found", ref.Provider, ref.Ref)
		}
		res.Path, res.MovieID, err = m.importMovie(ctx, meta, u.Path, info.Size(), u.Parsed, warn, dispositionFor(u.Path), u.LibraryID)
		if err != nil {
			return nil, err
		}

	case MediaTypeSeries:
		if !u.Parsed.IsEpisode() && !u.Parsed.IsAbsoluteEpisode() {
			return nil, fmt.Errorf("library: %q has no season/episode number to import as an episode", u.Path)
		}
		meta, err := provider.GetSeries(ctx, ref.Ref)
		if err != nil {
			return nil, fmt.Errorf("library: get series %s/%s: %w", ref.Provider, ref.Ref, err)
		}
		if meta == nil {
			return nil, fmt.Errorf("library: series %s/%s not found", ref.Provider, ref.Ref)
		}
		// The chosen series' own tree places an absolute number, or nothing
		// does. This is a refusal rather than a park because the user is
		// standing in front of it: the answer they need is that THIS series
		// cannot place THAT number, so they can choose another one.
		p, ok := resolveAbsolute(meta, u.Parsed)
		if !ok {
			return nil, fmt.Errorf("library: %q: %s", u.Path, reasonNoAbsoluteMatch)
		}
		res.Path, res.SeriesID, err = m.importEpisode(ctx, meta, u.Path, info.Size(), p, warn, dispositionFor(u.Path), u.LibraryID)
		if err != nil {
			return nil, err
		}

	case MediaTypeScene:
		lib, err := m.sceneMatchLibrary(ctx, u.LibraryID)
		if err != nil {
			return nil, err
		}
		provider := m.adultByID(ctx, ref.Provider)
		if provider == nil {
			return nil, core.ErrNoAdultProvider
		}
		scene, err := provider.GetScene(ctx, ref.Ref)
		if err != nil {
			return nil, fmt.Errorf("library: get scene %s/%s: %w", ref.Provider, ref.Ref, err)
		}
		if scene == nil || scene.SiteStashID == "" {
			return nil, fmt.Errorf("library: scene %s/%s not found", ref.Provider, ref.Ref)
		}
		site, err := m.AddSiteAndWait(ctx,
			core.ItemRef{Provider: ref.Provider, Ref: scene.SiteStashID}, nil, lib.ID)
		if err != nil {
			return nil, err
		}
		episodes, err := m.store.ListEpisodes(ctx, site.ID)
		if err != nil {
			return nil, err
		}
		var episode *core.Episode
		for i := range episodes {
			if episodes[i].StashID == scene.StashID {
				episode = &episodes[i]
				break
			}
		}
		if episode == nil {
			return nil, fmt.Errorf("library: selected scene %s/%s is not in site %q",
				ref.Provider, ref.Ref, site.Title)
		}
		p := u.Parsed
		p.Season, p.Episodes = episode.SeasonNumber, []int{episode.EpisodeNumber}
		res.Path, res.SeriesID, err = m.importScene(ctx, site, u.Path, info.Size(), p,
			episode.AirDate, episode.Title, warn, dispositionFor(u.Path))
		if err != nil {
			return nil, err
		}
		adultEpisodeID = episode.ID

	default:
		return nil, fmt.Errorf("library: unknown media type %q", mediaType)
	}

	m.libraryChanged(ctx)
	if adultEpisodeID != 0 {
		m.adultLibraryChanged(ctx, []int64{adultEpisodeID})
	}
	return res, nil
}
