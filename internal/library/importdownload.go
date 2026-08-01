package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// ReasonImport is the park reason for a finished download's file the import
// pipeline could not reconcile with the grab that fetched it (SPEC §5.1: a
// visible stuck-import queue, never a silent drop).
//
// It is a bare token where the scan reasons are sentences, because the API
// filters the stuck-import queue from the scan-review queue by this exact
// value. The human explanation and the grab it belongs to go into the activity
// feed alongside it.
const ReasonImport = "import"

// EventCategoryImport groups import events in the activity feed (SPEC §7).
const EventCategoryImport = "import"

// ImportDownload imports a finished download into the library (SPEC §5.1,
// PLAN phase 2 task 5).
//
// grab says what the download was fetched for; the parsed filename is only a
// sanity check on top of it. That order is the point: re-deriving the target
// from a scene name is how an import lands the wrong episode, and the grab is
// the one piece of evidence that cannot be wrong about intent.
//
// A file that contradicts the grab parks in the unmatched queue with reason
// ReasonImport instead of being imported, and parking is a *successful*
// outcome: returning an error would make the job queue retry a decision that
// is never going to come out differently. Only I/O, provider, and database
// failures are errors.
//
// Library files are never touched by a failure: everything that can reject a
// file happens before the first byte moves, and the download's own data is
// hardlinked rather than moved (see sourceDisposition), so an import costs the
// engine nothing.
//
// It is idempotent, as every job handler must be (SPEC §7). A download whose
// grab is already marked imported is a no-op, and re-running an unmarked
// import re-resolves onto the same destinations, which placeFile recognizes as
// hardlinks of the source and leaves alone.
func (m *Manager) ImportDownload(ctx context.Context, dl core.DownloadStatus, grab core.GrabInfo) error {
	done, err := m.alreadyImported(ctx, grab)
	if err != nil || done {
		return err
	}

	files, err := m.downloadFiles(dl.SavePath)
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("library: download %q has no video file under %s", dl.Name, dl.SavePath)
	}

	var imported, parked int
	switch {
	case grab.MovieID > 0:
		imported, parked, err = m.importDownloadedMovie(ctx, files, grab)
	case grab.SeriesID > 0:
		imported, parked, err = m.importDownloadedEpisodes(ctx, files, grab)
	default:
		return fmt.Errorf("library: grab %d targets neither a movie nor a series", grab.GrabID)
	}
	if err != nil {
		return err
	}
	return m.recordGrabOutcome(ctx, grab, imported, parked)
}

// alreadyImported reports whether this grab has already been imported. It is
// the cheap half of ImportDownload's idempotency: a redelivered job costs one
// query rather than a filesystem walk.
func (m *Manager) alreadyImported(ctx context.Context, grab core.GrabInfo) (bool, error) {
	if grab.GrabID == 0 {
		return false, nil
	}
	g, err := m.store.GetGrab(ctx, grab.GrabID)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return g.Status == core.GrabStatusImported, nil
}

// recordGrabOutcome closes the grab out in the history. A grab that imported
// nothing and parked something failed and says so; anything that landed at
// least one file counts as imported, because a season pack carrying one
// unrecognized extra is not a failed grab.
func (m *Manager) recordGrabOutcome(ctx context.Context, grab core.GrabInfo, imported, parked int) error {
	if grab.GrabID == 0 {
		return nil
	}

	status := core.GrabStatusImported
	reason := fmt.Sprintf("imported %d file(s)", imported)
	if imported == 0 {
		status = core.GrabStatusFailed
		reason = fmt.Sprintf("no file matched the grab; %d parked for manual match", parked)
	}

	err := m.store.SetGrabStatus(ctx, grab.GrabID, status, reason)
	if errors.Is(err, store.ErrNotFound) {
		// The grab row is gone. The files are imported either way, and history
		// is the thing SPEC §7 allows to be lost.
		return nil
	}
	return err
}

// importDownloadedMovie imports the feature out of a completed movie download.
//
// A movie download routinely holds more than one video file — a sample, a
// featurette, the odd trailer — and the feature is the biggest of them.
// Choosing by size rather than by name means there is no "sample" heuristic to
// keep in sync with what release groups actually name things.
func (m *Manager) importDownloadedMovie(ctx context.Context, files []downloadedFile, grab core.GrabInfo) (int, int, error) {
	meta, unresolvable, err := m.movieMeta(ctx, grab.MovieID)
	if err != nil {
		return 0, 0, err
	}

	file := largestFile(files)
	p := m.parse(path.Base(file.rel))

	reason := unresolvable
	if reason == "" {
		reason = movieMismatch(meta, p)
	}
	if reason != "" {
		if err := m.parkImport(ctx, file, p, grab, reason); err != nil {
			return 0, 0, err
		}
		return 0, 1, nil
	}

	var warnings []string
	warn := func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}
	rel, movieID, err := m.importMovie(ctx, meta, file.rel, file.size, p, warn, keepSource)
	if err != nil {
		return 0, 0, err
	}
	if err := m.recordImport(ctx, rel, grab, movieID, 0, warnings); err != nil {
		return 0, 0, err
	}
	return 1, 0, nil
}

// importDownloadedEpisodes imports every episode file in a completed download.
// One download legitimately carries a whole season (PLAN phase 3 task 7 builds
// on this), so each file is reconciled and imported on its own: an extra that
// does not belong parks without taking the episodes down with it.
func (m *Manager) importDownloadedEpisodes(ctx context.Context, files []downloadedFile, grab core.GrabInfo) (int, int, error) {
	meta, unresolvable, err := m.seriesMeta(ctx, grab.SeriesID)
	if err != nil {
		return 0, 0, err
	}
	wanted, err := m.wantedEpisodes(ctx, grab)
	if err != nil {
		return 0, 0, err
	}

	var imported, parked int
	for _, file := range files {
		p := m.parse(path.Base(file.rel))

		reason := unresolvable
		if reason == "" {
			reason = episodeMismatch(meta, p, grab, wanted)
		}
		if reason != "" {
			if err := m.parkImport(ctx, file, p, grab, reason); err != nil {
				return imported, parked, err
			}
			parked++
			continue
		}

		var warnings []string
		warn := func(format string, args ...any) {
			warnings = append(warnings, fmt.Sprintf(format, args...))
		}
		rel, seriesID, err := m.importEpisode(ctx, meta, file.rel, file.size, p, warn, keepSource)
		if err != nil {
			return imported, parked, err
		}
		if err := m.recordImport(ctx, rel, grab, 0, seriesID, warnings); err != nil {
			return imported, parked, err
		}
		imported++
	}
	return imported, parked, nil
}

// movieMeta resolves the provider metadata for a grabbed movie.
//
// The second return is a park reason: a non-empty string means the grab's
// target cannot be resolved at all — the library row is gone, or it never had
// a provider id — which is a decision, not a failure, so it comes back with a
// nil error for the caller to park on. A provider that is merely unreachable
// *is* an error, because retrying it will work.
func (m *Manager) movieMeta(ctx context.Context, movieID int64) (*core.MovieMeta, string, error) {
	if m.provider == nil {
		return nil, "", core.ErrNoMetadataProvider
	}

	mv, err := m.store.GetMovie(ctx, movieID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Sprintf("movie %d is no longer in the library", movieID), nil
	}
	if err != nil {
		return nil, "", err
	}
	if mv.TMDBID <= 0 {
		return nil, fmt.Sprintf("movie %q has no TMDB id to import against", mv.Title), nil
	}

	meta, err := m.provider.GetMovie(ctx, mv.TMDBID)
	if err != nil {
		return nil, "", fmt.Errorf("library: get movie %d: %w", mv.TMDBID, err)
	}
	if meta == nil {
		return nil, fmt.Sprintf("movie %d is not in the metadata provider", mv.TMDBID), nil
	}
	return meta, "", nil
}

// seriesMeta is movieMeta's series twin, with the same park-reason contract.
func (m *Manager) seriesMeta(ctx context.Context, seriesID int64) (*core.SeriesMeta, string, error) {
	if m.provider == nil {
		return nil, "", core.ErrNoMetadataProvider
	}

	sr, err := m.store.GetSeries(ctx, seriesID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, fmt.Sprintf("series %d is no longer in the library", seriesID), nil
	}
	if err != nil {
		return nil, "", err
	}
	if sr.TMDBID <= 0 {
		return nil, fmt.Sprintf("series %q has no TMDB id to import against", sr.Title), nil
	}

	meta, err := m.provider.GetSeries(ctx, sr.TMDBID)
	if err != nil {
		return nil, "", fmt.Errorf("library: get series %d: %w", sr.TMDBID, err)
	}
	if meta == nil {
		return nil, fmt.Sprintf("series %d is not in the metadata provider", sr.TMDBID), nil
	}
	return meta, "", nil
}

// wantedEpisodes resolves a grab's episode ids into the season/episode numbers
// a file in that download may claim to be.
//
// An empty result means the grab named no episodes — a season pack grabbed
// before the provider listed the season — and the season number is then the
// whole check. Episode ids that no longer resolve are skipped rather than
// failing the import: the file on disk outlives the row (SPEC §1.2 pillar 2).
func (m *Manager) wantedEpisodes(ctx context.Context, grab core.GrabInfo) (map[int]map[int]bool, error) {
	out := map[int]map[int]bool{}
	for _, id := range grab.EpisodeIDs {
		e, err := m.store.GetEpisode(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if out[e.SeasonNumber] == nil {
			out[e.SeasonNumber] = map[int]bool{}
		}
		out[e.SeasonNumber][e.EpisodeNumber] = true
	}
	return out, nil
}

// movieMismatch reports why a downloaded file contradicts the movie it was
// grabbed for, or "" when the two are consistent.
//
// The check is deliberately weak. The grab is the evidence of intent, and a
// release name inside a scene folder routinely parses into something the
// provider would never return; only a file that positively claims to be
// something else is rejected. A file the parser could not name at all is
// accepted on the grab's word.
func movieMismatch(meta *core.MovieMeta, p core.ParsedRelease) string {
	if p.IsEpisode() {
		return fmt.Sprintf("file is %s but the grab is for the movie %q",
			episodeTag(p.Season, p.Episodes), meta.Title)
	}
	if strings.TrimSpace(p.Title) == "" {
		return ""
	}
	cands := []candidate{{title: meta.Title, originalTitle: meta.OriginalTitle, year: meta.Year}}
	if bestMatch(cands, p.Title, p.Year) < 0 {
		return fmt.Sprintf("file parses as %q but the grab is for %q", releaseLabel(p), meta.Title)
	}
	return ""
}

// episodeMismatch is movieMismatch's series twin. On top of the title check it
// enforces what the grab actually asked for: a season pack must not smuggle in
// another season, and an episode grab must not import an episode nobody asked
// for.
func episodeMismatch(meta *core.SeriesMeta, p core.ParsedRelease, grab core.GrabInfo, wanted map[int]map[int]bool) string {
	if !p.IsEpisode() {
		return fmt.Sprintf("file carries no season/episode number but the grab is for %q", meta.Title)
	}
	if strings.TrimSpace(p.Title) != "" {
		cands := []candidate{{title: meta.Title, originalTitle: meta.OriginalTitle, year: meta.Year}}
		if bestMatch(cands, p.Title, p.Year) < 0 {
			return fmt.Sprintf("file parses as %q but the grab is for %q", releaseLabel(p), meta.Title)
		}
	}
	if grab.SeasonNum > 0 && p.Season != grab.SeasonNum {
		return fmt.Sprintf("file is season %d but the grab is for season %d", p.Season, grab.SeasonNum)
	}
	if len(wanted) == 0 {
		return ""
	}
	for _, n := range p.Episodes {
		if wanted[p.Season][n] {
			return ""
		}
	}
	return fmt.Sprintf("%s is not among the episodes the grab was for", episodeTag(p.Season, p.Episodes))
}

// releaseLabel renders a parse for a human: what the file says it is.
func releaseLabel(p core.ParsedRelease) string {
	if p.Year > 0 {
		return fmt.Sprintf("%s (%d)", p.Title, p.Year)
	}
	return p.Title
}

// parkImport puts one file in the stuck-import queue and says why in the
// activity feed (SPEC §5.1, §13). The file itself is not touched: it stays
// where the download engine put it until the user resolves it, and resolving
// it goes through ImportUnmatched like any other parked file.
func (m *Manager) parkImport(ctx context.Context, f downloadedFile, p core.ParsedRelease, grab core.GrabInfo, reason string) error {
	u := &core.UnmatchedFile{Path: f.rel, Size: f.size, Parsed: p, Reason: ReasonImport}
	if err := m.store.UpsertUnmatchedFile(ctx, u); err != nil {
		return err
	}
	return m.store.InsertEvent(ctx, &core.Event{
		Level:    core.EventLevelWarn,
		Category: EventCategoryImport,
		Message:  fmt.Sprintf("Import of %s needs a manual match", path.Base(f.rel)),
		Detail:   fmt.Sprintf("%s: %s", grabLabel(grab), reason),
		MovieID:  grab.MovieID,
		SeriesID: grab.SeriesID,
	})
}

// recordImport writes the activity-feed entry for one imported file.
func (m *Manager) recordImport(ctx context.Context, rel string, grab core.GrabInfo, movieID, seriesID int64, warnings []string) error {
	level := core.EventLevelInfo
	detail := grabLabel(grab)
	if len(warnings) > 0 {
		// The file is imported either way; an NFO or poster that would not
		// write is a degraded import, not a failed one (SPEC §13).
		level = core.EventLevelWarn
		detail += ": " + strings.Join(warnings, "; ")
	}
	return m.store.InsertEvent(ctx, &core.Event{
		Level:    level,
		Category: EventCategoryImport,
		Message:  fmt.Sprintf("Imported %s", path.Base(rel)),
		Detail:   detail,
		MovieID:  movieID,
		SeriesID: seriesID,
	})
}

// grabLabel names the grab an event belongs to. The release title is
// denormalized onto the grab precisely so a stuck import can still say what it
// was trying to import.
func grabLabel(grab core.GrabInfo) string {
	if grab.ReleaseTitle == "" {
		return fmt.Sprintf("grab %d", grab.GrabID)
	}
	return fmt.Sprintf("grab %d (%s)", grab.GrabID, grab.ReleaseTitle)
}

// downloadedFile is one video file inside a completed download, with the size
// the media_files row needs.
type downloadedFile struct {
	rel  string
	size int64
}

// downloadFiles lists the video files a completed download left behind, in
// path order.
//
// savePath may be a single file (a one-file torrent) or a directory
// (everything else). Non-video files — NFOs, artwork, par2 leftovers, the
// screenshot folder — are not media and are ignored rather than parked: they
// are not evidence of anything going wrong.
func (m *Manager) downloadFiles(savePath string) ([]downloadedFile, error) {
	if strings.TrimSpace(savePath) == "" {
		return nil, errors.New("library: download has no save path")
	}

	root := m.abs(savePath)
	info, err := os.Stat(root)
	if err != nil {
		return nil, fmt.Errorf("library: stat %s: %w", savePath, err)
	}
	if !info.IsDir() {
		if !isVideo(info.Name()) {
			return nil, nil
		}
		return []downloadedFile{{rel: savePath, size: info.Size()}}, nil
	}

	var out []downloadedFile
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && strings.HasPrefix(d.Name(), ".") {
				return fs.SkipDir
			}
			return nil
		}
		if !isVideo(d.Name()) {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := m.rel(p)
		if err != nil {
			return err
		}
		out = append(out, downloadedFile{rel: rel, size: fi.Size()})
		return nil
	})
	if walkErr != nil {
		return nil, fmt.Errorf("library: walk %s: %w", savePath, walkErr)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].rel < out[j].rel })
	return out, nil
}

// largestFile returns the biggest file of a non-empty set, ties broken by path
// so the choice is deterministic.
func largestFile(files []downloadedFile) downloadedFile {
	best := files[0]
	for _, f := range files[1:] {
		if f.size > best.size {
			best = f
		}
	}
	return best
}
