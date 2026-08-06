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
	"github.com/watzon/caravan/internal/wanted"
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

// ReasonManualGrab marks a file parked by an UNTIED universal-search grab: the
// user chose a library and nothing else, so parking for a manual match is the
// grab doing exactly what it was asked. A bare token for ReasonImport's
// reason — the review screen labels it — and a distinct one because "needs a
// manual match" (something went wrong) and "awaiting your match" (working as
// designed) deserve different framing.
const ReasonManualGrab = "manual-grab"

// EventCategoryImport groups import events in the activity feed (SPEC §7).
const EventCategoryImport = "import"

// ImportPathConstraint is the remediation shown when an external download
// client reports a directory Caravan cannot read. A remote path mapping can
// translate client-local paths; without one, both processes must see the same
// absolute path.
const ImportPathConstraint = "configure a remote path mapping or expose the download at the same path"

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
	mappedPath, err := m.resolveDownloadPath(ctx, dl.SavePath)
	if err != nil {
		return fmt.Errorf("library: resolve remote download path: %w", err)
	}
	dl.SavePath = mappedPath

	files, err := m.downloadFiles(dl.SavePath)
	if err != nil {
		// A payload on an external client's own filesystem that Caravan cannot
		// open is the v1 constraint being broken, and no number of retries will
		// open it. It is reported once and the grab is closed out, exactly like
		// a file that contradicts its grab.
		//
		// Inside the storage root the same error means something else — a race
		// with the engine, a disk that went away — and that *is* worth
		// retrying, so it stays an error there.
		if foreignPath(dl.SavePath) && (errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission)) {
			return m.failUnreadableDownload(ctx, dl, grab)
		}
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
	case grab.LibraryID > 0:
		// An untied universal-search grab: the user chose a library and no
		// item, so every payload file parks in scan review scoped to it. The
		// default below survives on purpose — a grab with no target AND no
		// library is still a bug that must fail loudly.
		imported, parked, err = m.parkUntiedDownload(ctx, files, grab)
	default:
		return fmt.Errorf("library: grab %d targets neither a movie nor a series", grab.GrabID)
	}
	if err != nil {
		return err
	}
	if err := m.recordGrabOutcome(ctx, grab, imported, parked); err != nil {
		return err
	}
	// One notification per download, not per file: a season pack is a single
	// batch and Jellyfin only needs telling once. A download that landed
	// nothing changed no files, so there is nothing for a player to rescan.
	if imported > 0 {
		m.libraryChanged(ctx)
	}
	return nil
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
	switch {
	case grab.MovieID == 0 && grab.SeriesID == 0 && grab.LibraryID > 0:
		// An untied grab that parked its payload did exactly what it was
		// asked. The status is NOT cosmetic: alreadyImported keys on
		// GrabStatusImported, and it is what keeps a redelivered job from
		// parking the same files twice under an expired lease.
		reason = fmt.Sprintf("parked %d file(s) for manual match", parked)
	case imported == 0:
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
	p := m.parse(filepath.Base(file.rel))

	reason := unresolvable
	if reason == "" {
		reason = movieMismatch(meta, p)
		if reason != "" && m.noClaim(p) {
			if rp, ok := m.grabTitleParse(grab); ok && movieMismatch(meta, rp) == "" {
				p, reason = rp, ""
			}
		}
	}
	if reason != "" {
		if err := m.parkImport(ctx, file, p, grab, reason); err != nil {
			return 0, 0, err
		}
		return 0, 1, nil
	}

	existing, err := m.store.ListMediaFilesForMovie(ctx, grab.MovieID)
	if err != nil {
		return 0, 0, err
	}

	var warnings []string
	warn := func(format string, args ...any) {
		warnings = append(warnings, fmt.Sprintf(format, args...))
	}
	rel, movieID, err := m.importMovie(ctx, meta, file.rel, file.size, p, warn, keepSource, grab.LibraryID)
	if err != nil {
		return 0, 0, err
	}
	newFile, err := m.verifyMovieImport(ctx, rel, file.size, movieID)
	if err != nil {
		return 0, 0, err
	}
	if err := m.replaceMovieFiles(ctx, meta, file.rel, existing, newFile, grab, movieID); err != nil {
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
	// A grab against a site is the same download shape with a different
	// identity model behind it, so the branch is here rather than one level up:
	// everything before this point — idempotency, the file walk, the outcome
	// record — is shared.
	sr, err := m.store.GetSeries(ctx, grab.SeriesID)
	switch {
	case err == nil && sr.Kind == core.SeriesKindAdult:
		return m.importDownloadedScenes(ctx, files, grab, sr)
	case err != nil && !errors.Is(err, store.ErrNotFound):
		return 0, 0, err
	}

	meta, unresolvable, err := m.seriesMeta(ctx, grab.SeriesID)
	if err != nil {
		return 0, 0, err
	}
	wanted, err := m.wantedEpisodes(ctx, grab)
	if err != nil {
		return 0, 0, err
	}

	var imported, parked int
	largest := largestFile(files)
	for _, file := range files {
		p := m.parse(filepath.Base(file.rel))

		reason := unresolvable
		if reason == "" {
			reason = episodeMismatch(meta, p, grab, wanted)
			// The release title can vouch for one file only — the
			// feature-sized one — or an obfuscated sample would import as the
			// same episode and supersede the real payload.
			if reason != "" && m.noClaim(p) && file.rel == largest.rel {
				if rp, ok := m.grabTitleParse(grab); ok && episodeMismatch(meta, rp, grab, wanted) == "" {
					p, reason = rp, ""
				}
			}
		}
		if reason != "" {
			if err := m.parkImport(ctx, file, p, grab, reason); err != nil {
				return imported, parked, err
			}
			parked++
			continue
		}

		existing, err := m.existingEpisodeFiles(ctx, grab, p)
		if err != nil {
			return imported, parked, err
		}

		var warnings []string
		warn := func(format string, args ...any) {
			warnings = append(warnings, fmt.Sprintf(format, args...))
		}
		rel, seriesID, err := m.importEpisode(ctx, meta, file.rel, file.size, p, warn, keepSource, grab.LibraryID)
		if err != nil {
			return imported, parked, err
		}
		newFile, err := m.verifyEpisodeImport(ctx, rel, file.size, seriesID, p)
		if err != nil {
			return imported, parked, err
		}
		if err := m.replaceEpisodeFiles(ctx, meta.Title, file.rel, existing, newFile, grab, seriesID, p); err != nil {
			return imported, parked, err
		}
		if err := m.recordImport(ctx, rel, grab, 0, seriesID, warnings); err != nil {
			return imported, parked, err
		}
		imported++
	}
	return imported, parked, nil
}

// existingEpisodeFiles returns the files currently linked to the exact
// episodes a parsed release covers. The lookup happens before placement so an
// import can replace those files only after its own row and links exist.
func (m *Manager) existingEpisodeFiles(ctx context.Context, grab core.GrabInfo, p core.ParsedRelease) ([]core.MediaFile, error) {
	filesByID := make(map[int64]core.MediaFile)
	for _, id := range grab.EpisodeIDs {
		episode, err := m.store.GetEpisode(ctx, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if episode.SeasonNumber != p.Season || !containsEpisode(p.Episodes, episode.EpisodeNumber) {
			continue
		}
		files, err := m.store.ListMediaFilesForEpisode(ctx, id)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			filesByID[file.ID] = file
		}
	}

	out := make([]core.MediaFile, 0, len(filesByID))
	for _, file := range filesByID {
		out = append(out, file)
	}
	return out, nil
}

func containsEpisode(episodes []int, want int) bool {
	for _, episode := range episodes {
		if episode == want {
			return true
		}
	}
	return false
}

// verifyImportedFile confirms that the filesystem and media_files row agree
// before an import removes any earlier file. The store is a cache, but it must
// describe the verified destination before it can safely supersede an old row.
func (m *Manager) verifyImportedFile(ctx context.Context, rel string, expectedSize int64) (*core.MediaFile, error) {
	info, err := os.Stat(m.abs(rel))
	if err != nil {
		return nil, fmt.Errorf("library: verify imported file %s: %w", rel, err)
	}
	if info.Size() != expectedSize {
		return nil, fmt.Errorf("library: verify imported file %s: size %d, want %d", rel, info.Size(), expectedSize)
	}
	file, err := m.store.GetMediaFileByPath(ctx, rel)
	if err != nil {
		return nil, err
	}
	if file.Size != expectedSize {
		return nil, fmt.Errorf("library: verify media file %s: size %d, want %d", rel, file.Size, expectedSize)
	}
	return file, nil
}

// verifyMovieImport confirms the verified media row belongs to the grabbed
// movie, not merely to the path that happened to receive the file.
func (m *Manager) verifyMovieImport(ctx context.Context, rel string, expectedSize, movieID int64) (*core.MediaFile, error) {
	file, err := m.verifyImportedFile(ctx, rel, expectedSize)
	if err != nil {
		return nil, err
	}
	if file.MovieID != movieID {
		return nil, fmt.Errorf("library: verify media file %s belongs to movie %d, want %d", rel, file.MovieID, movieID)
	}
	return file, nil
}

// verifyEpisodeImport also confirms every covered episode points to the new
// media row. A multi-episode file is one row with several links, so checking
// only the path would permit a partially linked season-pack import.
func (m *Manager) verifyEpisodeImport(ctx context.Context, rel string, expectedSize, seriesID int64, p core.ParsedRelease) (*core.MediaFile, error) {
	file, err := m.verifyImportedFile(ctx, rel, expectedSize)
	if err != nil {
		return nil, err
	}
	for _, number := range p.Episodes {
		episode, err := m.store.GetEpisodeByNumber(ctx, seriesID, p.Season, number)
		if err != nil {
			return nil, err
		}
		files, err := m.store.ListMediaFilesForEpisode(ctx, episode.ID)
		if err != nil {
			return nil, err
		}
		if !containsMediaFile(files, file.ID) {
			return nil, fmt.Errorf("library: verify episode S%02dE%02d links to %s", p.Season, number, rel)
		}
	}
	return file, nil
}

func containsMediaFile(files []core.MediaFile, want int64) bool {
	for _, file := range files {
		if file.ID == want {
			return true
		}
	}
	return false
}

// replaceMovieFiles removes each prior movie file only after the replacement
// is verified, and writes an explicit history record for the quality decision.
func (m *Manager) replaceMovieFiles(ctx context.Context, meta *core.MovieMeta, sourceRel string, existing []core.MediaFile, newFile *core.MediaFile, grab core.GrabInfo, movieID int64) error {
	label := meta.Title
	if meta.Year > 0 {
		label = fmt.Sprintf("%s (%d)", meta.Title, meta.Year)
	}
	return m.removeSupersededFiles(ctx, sourceRel, existing, newFile, func(old core.MediaFile) *core.Event {
		return replacementEvent(label, old.Quality, newFile.Quality, grab, movieID, 0)
	})
}

// replaceEpisodeFiles is the episode counterpart of replaceMovieFiles. Every
// file covered by a multi-episode release is handled once, even though it can
// have appeared in the old-file lookup for several episode links.
// It takes the series' title rather than its provider metadata because that is
// all it needs, and because a scene import has a series row but no SeriesMeta.
func (m *Manager) replaceEpisodeFiles(ctx context.Context, title, sourceRel string, existing []core.MediaFile, newFile *core.MediaFile, grab core.GrabInfo, seriesID int64, p core.ParsedRelease) error {
	label := fmt.Sprintf("%s %s", title, episodeTag(p.Season, p.Episodes))
	return m.removeSupersededFiles(ctx, sourceRel, existing, newFile, func(old core.MediaFile) *core.Event {
		return replacementEvent("file for "+label, old.Quality, newFile.Quality, grab, 0, seriesID)
	})
}

// removeSupersededFiles makes replacement idempotent. In particular, a
// redelivered import sees its source hardlinked to the existing library file
// and must not delete the row that describes that file.
func (m *Manager) removeSupersededFiles(ctx context.Context, sourceRel string, existing []core.MediaFile, newFile *core.MediaFile, eventFor func(core.MediaFile) *core.Event) error {
	seen := make(map[int64]bool)
	for _, old := range existing {
		if seen[old.ID] {
			continue
		}
		seen[old.ID] = true
		same, err := m.sameImportedFile(sourceRel, old, *newFile)
		if err != nil {
			return err
		}
		if same {
			continue
		}
		if err := os.Remove(m.abs(old.Path)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("library: remove superseded file %s: %w", old.Path, err)
		}
		if err := m.store.DeleteMediaFileByPath(ctx, old.Path); err != nil {
			return err
		}
		if err := m.store.InsertEvent(ctx, eventFor(old)); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) sameImportedFile(sourceRel string, old, new core.MediaFile) (bool, error) {
	if old.ID == new.ID || old.Path == new.Path {
		return true, nil
	}
	source, err := os.Stat(m.abs(sourceRel))
	if err != nil {
		return false, fmt.Errorf("library: stat import source %s: %w", sourceRel, err)
	}
	oldInfo, err := os.Stat(m.abs(old.Path))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("library: stat existing library file %s: %w", old.Path, err)
	}
	return os.SameFile(source, oldInfo), nil
}

func replacementEvent(target, oldQuality, newQuality string, grab core.GrabInfo, movieID, seriesID int64) *core.Event {
	message := fmt.Sprintf("Replaced %s with %s (was %s; same or lower quality)", target, newQuality, oldQuality)
	if wanted.IsUpgrade(newQuality, oldQuality) {
		message = fmt.Sprintf("Upgraded %s to %s (was %s)", target, newQuality, oldQuality)
	}
	return &core.Event{
		Level:    core.EventLevelInfo,
		Category: EventCategoryImport,
		Message:  message,
		Detail:   grabLabel(grab),
		MovieID:  movieID,
		SeriesID: seriesID,
	}
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

	lib, err := m.libraryByIDOrDefault(ctx, mv.LibraryID, core.LibraryKindMovie)
	if err != nil {
		return nil, "", err
	}
	meta, err := m.metadataFor(ctx, lib).GetMovie(ctx, mv.TMDBID)
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

	lib, err := m.seriesLibraryOf(ctx, sr)
	if err != nil {
		return nil, "", err
	}
	meta, err := m.metadataFor(ctx, lib).GetSeries(ctx, sr.TMDBID)
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

// noClaim reports whether a parse is noise rather than a positive claim about
// what a file is: no episode numbers and below the confidence that would let a
// scan import it. Only such a file may borrow its grab's release title — a
// file that positively claims to be something else parks, never relabels
// (the movieMismatch principle, applied to the fallback itself).
func (m *Manager) noClaim(p core.ParsedRelease) bool {
	return !p.IsEpisode() && p.Confidence < m.minConfidence
}

// grabTitleParse parses the release title the grab recorded — the name the
// user actually grabbed. Usenet posts routinely obfuscate the payload's file
// name, so when that name is noise the release title is the best remaining
// description of the payload, and the grab it hangs off is the evidence of
// intent that lets the import trust it.
func (m *Manager) grabTitleParse(grab core.GrabInfo) (core.ParsedRelease, bool) {
	title := strings.TrimSpace(grab.ReleaseTitle)
	if title == "" {
		return core.ParsedRelease{}, false
	}
	return m.parse(title), true
}

// releaseLabel renders a parse for a human: what the file says it is.
func releaseLabel(p core.ParsedRelease) string {
	if p.Year > 0 {
		return fmt.Sprintf("%s (%d)", p.Title, p.Year)
	}
	return p.Title
}

// failUnreadableDownload closes out a download whose payload Caravan cannot
// read, with the v1 constraint spelled out (PLAN phase 6 task 2).
//
// It is a *successful* outcome for the import job, like parking is: the
// unreadable path is a configuration problem, and a job that retries it every
// five minutes forever would bury the one message that explains it. Nothing on
// disk is touched — the download stays in the client, so fixing the mount and
// re-grabbing (or re-queueing the import) still works.
func (m *Manager) failUnreadableDownload(ctx context.Context, dl core.DownloadStatus, grab core.GrabInfo) error {
	reason := fmt.Sprintf("%s is not readable: %s", dl.SavePath, ImportPathConstraint)
	if err := m.recordGrabFailure(ctx, grab, reason); err != nil {
		return err
	}
	return m.store.InsertEvent(ctx, &core.Event{
		Level:    core.EventLevelWarn,
		Category: EventCategoryImport,
		Message:  fmt.Sprintf("Import of %s could not read the downloaded data", dl.Name),
		Detail:   fmt.Sprintf("%s: %s", grabLabel(grab), reason),
		MovieID:  grab.MovieID,
		SeriesID: grab.SeriesID,
	})
}

// recordGrabFailure marks a grab failed with reason, tolerating a grab row
// that is already gone (history is the thing SPEC §7 allows to be lost).
func (m *Manager) recordGrabFailure(ctx context.Context, grab core.GrabInfo, reason string) error {
	if grab.GrabID == 0 {
		return nil
	}
	err := m.store.SetGrabStatus(ctx, grab.GrabID, core.GrabStatusFailed, reason)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	return err
}

// parkImport puts one file in the stuck-import queue and says why in the
// activity feed (SPEC §5.1, §13). The file itself is not touched: it stays
// where the download engine put it until the user resolves it, and resolving
// it goes through ImportUnmatched like any other parked file.
//
// A file an external client wrote outside the storage root gets the feed entry
// but no queue row: the queue is addressed by root-relative path, and putting
// a foreign absolute path in `unmatched_files` would give the library a path
// it does not own and cannot resolve after the root moves (SPEC §1.2 pillar
// 3). Pointing the client's completed directory inside the storage root — what
// docs/external-clients.md recommends anyway, since it is also what makes
// imports hardlink — gets the queue row back.
func (m *Manager) parkImport(ctx context.Context, f downloadedFile, p core.ParsedRelease, grab core.GrabInfo, reason string) error {
	return m.parkFile(ctx, f, p, grab, ReasonImport, EventCategoryImport,
		fmt.Sprintf("Import of %s needs a manual match", filepath.Base(f.rel)), reason)
}

// parkFile is the queue-row-plus-event tail parkImport and the untied grab
// path share: the queue reason and the event framing differ, everything else
// must not drift.
func (m *Manager) parkFile(ctx context.Context, f downloadedFile, p core.ParsedRelease, grab core.GrabInfo, queueReason, eventCategory, message, detail string) error {
	if !foreignPath(f.rel) {
		u := &core.UnmatchedFile{
			Path: f.rel, Size: f.size, Parsed: p,
			Reason: queueReason, LibraryID: grab.LibraryID,
		}
		if err := m.store.UpsertUnmatchedFile(ctx, u); err != nil {
			return err
		}
	}
	return m.store.InsertEvent(ctx, &core.Event{
		Level:    core.EventLevelWarn,
		Category: eventCategory,
		Message:  message,
		Detail:   fmt.Sprintf("%s: %s", grabLabel(grab), detail),
		MovieID:  grab.MovieID,
		SeriesID: grab.SeriesID,
	})
}

// parkUntiedDownload parks every video file of an untied universal-search
// grab. Every file, honestly: a manual grab can be a season pack, an album,
// an ISO — Caravan has no idea how many items it holds, and the user asked to
// decide by hand. Which parser reads each name follows the chosen library's
// kind, the same "where it is decides" rule the scanner applies.
func (m *Manager) parkUntiedDownload(ctx context.Context, files []downloadedFile, grab core.GrabInfo) (int, int, error) {
	lib, err := m.store.GetLibrary(ctx, grab.LibraryID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return 0, 0, err
	}
	parseName := m.parse
	eventCategory := EventCategoryImport
	if lib != nil && lib.Kind == core.LibraryKindAdult {
		parseName = m.parseScene
		// An untied grab into an adult library must stay as invisible to
		// ungranted callers as the library itself; the adult-only category is
		// what the event feed's gate reads.
		eventCategory = core.EventCategoryAdultOnly
	}
	parked := 0
	for _, f := range files {
		if err := m.parkFile(ctx, f, parseName(filepath.Base(f.rel)), grab,
			ReasonManualGrab, eventCategory,
			fmt.Sprintf("%s is ready for a manual match", filepath.Base(f.rel)),
			"grabbed without a library item"); err != nil {
			return 0, parked, err
		}
		parked++
	}
	return 0, parked, nil
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
// (everything else), and it may be an external client's own absolute path
// (PLAN phase 6). Non-video files — NFOs, artwork, par2 leftovers, the
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
		// Through downloadPath for the same reason the walk below is: an
		// external client reports a single-file torrent by the file's own
		// absolute path (qBittorrent's content_path), and one that landed inside
		// the storage root has to come back root-relative or every later
		// foreignPath test reads it as a file the library does not own — which
		// would cost a mismatched single-file download its stuck-import row.
		return []downloadedFile{{rel: m.downloadPath(root), size: info.Size()}}, nil
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
		out = append(out, downloadedFile{rel: m.downloadPath(p), size: fi.Size()})
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
