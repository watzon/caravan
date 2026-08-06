package library

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// videoExts is the extension allowlist. Anything else in the library — NFOs,
// posters, subtitles, stray archives — is not a media file and is ignored.
var videoExts = map[string]bool{
	".mkv":  true,
	".mp4":  true,
	".m4v":  true,
	".avi":  true,
	".mov":  true,
	".wmv":  true,
	".mpg":  true,
	".mpeg": true,
	".m2ts": true,
	".ts":   true,
	".flv":  true,
	".webm": true,
	".divx": true,
	".ogv":  true,
}

// isVideo reports whether a bare filename is a media file Caravan handles.
//
// Dotfiles are excluded even when they carry a video extension: macOS
// AppleDouble sidecars ("._Movie.mkv") look like video and are not.
func isVideo(name string) bool {
	return !strings.HasPrefix(name, ".") && videoExts[strings.ToLower(filepath.Ext(name))]
}

// Park reasons, surfaced verbatim in the scan-review screen (SPEC §10.1).
const (
	reasonNoProvider   = "no metadata provider configured"
	reasonLowParse     = "low parser confidence"
	reasonNoTitle      = "no title in filename"
	reasonNoEpisodeNum = "under TV/ but no season/episode in filename"
	reasonNoMatch      = "no metadata match"
	reasonProviderErr  = "metadata provider error"
	// Adult park reasons (PLAN phase 9 task 4). They name the scene vocabulary
	// rather than reusing the episode one, because "no season/episode in
	// filename" would be advice nobody can act on for a file that is supposed
	// to be identified by its date.
	reasonNoSceneDate    = "under Adult/ but no release date in filename"
	reasonNoSceneMatch   = "no scene released on that date"
	reasonAmbiguousScene = "several scenes released on that date"
	reasonSceneNotInGrab = "file is a different scene than the one grabbed"
	// reasonNoLibrary parks a file loose under library/ that no library root
	// claims. Before 0022 such a file was silently read as a movie; with
	// several libraries there is no defensible default, and a visible park
	// beats a silent misfile.
	reasonNoLibrary = "not under any library root"
)

// ScanResult summarizes one library scan.
type ScanResult struct {
	// Scanned is the number of video files walked.
	Scanned int
	// Added and Updated count media_files rows created and refreshed.
	Added   int
	Updated int
	// Removed counts media_files rows dropped because the file is gone.
	Removed int
	// Unmatched counts files parked in the scan-review queue.
	Unmatched int
	// Errors holds non-fatal problems: a file that could not be organized, a
	// poster that would not download. The scan continues past all of them.
	Errors []string
}

func (r *ScanResult) addErr(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

// Scan walks the library, matches every video file against the metadata
// provider, and reconciles the database against what is actually on disk.
//
// It is the operation that makes the database disposable (SPEC §1.2 pillar 2):
// deleting every row and scanning again converges on the same library, because
// nothing is derived from state Scan itself wrote.
//
// Individual failures degrade rather than abort: a file that cannot be parsed,
// matched, or organized parks in the unmatched queue or is reported in
// Errors. Only a failure to read the library root or to talk to the database
// aborts the scan.
func (m *Manager) Scan(ctx context.Context) (*ScanResult, error) {
	res := &ScanResult{}
	m.syncedSites = map[int64]bool{}

	// The library set is snapshotted once per scan, like syncedSites: a walk
	// over thousands of files must not re-query the table per file, and a
	// library created mid-scan is the next scan's business.
	libs, err := m.store.ListLibraries(ctx)
	if err != nil {
		return nil, err
	}
	m.scanLibs = libs

	libAbs := m.abs(LibraryDir)
	info, err := os.Stat(libAbs)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// No library directory on disk. That is the normal first-run state,
		// but it is also the state after the library tree was removed, so the
		// walk is skipped while the removal/artwork reconciliation below
		// still runs: rows pointing at vanished files must not linger.
		info = nil
	case err != nil:
		return nil, fmt.Errorf("library: stat %s: %w", libAbs, err)
	case !info.IsDir():
		return nil, fmt.Errorf("library: %s is not a directory", libAbs)
	}

	// A disabled adult module is not walked at all, which is stronger than
	// walking it and refusing to match: a scene filename parked in the review
	// queue is a UI trace of a module the owner turned off, and this phase
	// promises there are none (PLAN phase 9 task 5). It is also the cheapest
	// possible guarantee that a scan makes no stash-box request.
	adultEnabled, err := m.store.AdultEnabled(ctx)
	if err != nil {
		return nil, err
	}
	// Every adult-kind library root is skipped, not just the seed one: a
	// second adult library missed here would park scene filenames in the
	// review queue with the module off, the exact trace this promises against.
	adultAbs := map[string]bool{}
	for _, lib := range libs {
		if lib.Kind == core.LibraryKindAdult {
			adultAbs[m.abs(lib.RootPath)] = true
		}
	}

	// The pre-scan snapshots serve two purposes: they tell an added file from
	// an updated one, and they are the removal candidates once the walk knows
	// what is actually on disk.
	before, err := m.store.ListMediaFiles(ctx)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(before))
	for _, f := range before {
		known[f.Path] = true
	}
	beforeUnmatched, err := m.store.ListUnmatchedFiles(ctx)
	if err != nil {
		return nil, err
	}

	seenFiles := map[string]bool{}
	seenUnmatched := map[string]bool{}

	var walkErr error
	if info != nil {
		walkErr = filepath.WalkDir(libAbs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				res.addErr("walk %s: %v", p, err)
				if d != nil && d.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if err := ctx.Err(); err != nil {
				return err
			}

			name := d.Name()
			if d.IsDir() {
				// Hidden directories are tooling, not media: .Trashes, .Spotlight,
				// @eaDir-style sidecars all start with a dot.
				if p != libAbs && strings.HasPrefix(name, ".") {
					return fs.SkipDir
				}
				if !adultEnabled && adultAbs[p] {
					return fs.SkipDir
				}
				return nil
			}
			if !isVideo(name) {
				return nil
			}

			relOS, err := filepath.Rel(libAbs, p)
			if err != nil {
				res.addErr("relative path for %s: %v", p, err)
				return nil
			}
			rel := path.Join(LibraryDir, filepath.ToSlash(relOS))

			fi, err := d.Info()
			if err != nil {
				res.addErr("stat %s: %v", rel, err)
				return nil
			}

			res.Scanned++
			m.scanFile(ctx, rel, fi.Size(), res, known, seenFiles, seenUnmatched)
			return nil
		})
	}
	if walkErr != nil {
		return nil, fmt.Errorf("library: scan %s: %w", libAbs, walkErr)
	}

	if err := m.reconcileRemovals(ctx, res, before, beforeUnmatched, seenFiles, seenUnmatched, adultEnabled); err != nil {
		return nil, err
	}
	if err := m.healStaleArtwork(ctx, res); err != nil {
		return nil, err
	}
	return res, nil
}

// healStaleArtwork clears poster references whose files are no longer on
// disk. The UI prefers a local poster over the provider URL it would
// otherwise show, so a poster_path that 404s is strictly worse than none:
// clearing it lets the interface fall back to the provider artwork until a
// future import writes a real file. It never touches artwork that exists.
func (m *Manager) healStaleArtwork(ctx context.Context, res *ScanResult) error {
	movies, err := m.store.ListMovies(ctx)
	if err != nil {
		return err
	}
	for _, mv := range movies {
		if mv.PosterPath == "" || fileExists(m.abs(mv.PosterPath)) {
			continue
		}
		mv.PosterPath = ""
		if err := m.store.UpsertMovie(ctx, &mv); err != nil {
			res.addErr("clear stale poster of %s: %v", mv.Title, err)
		}
	}

	series, err := m.store.ListSeries(ctx)
	if err != nil {
		return err
	}
	for _, sr := range series {
		if sr.PosterPath == "" || fileExists(m.abs(sr.PosterPath)) {
			continue
		}
		sr.PosterPath = ""
		if err := m.store.UpsertSeries(ctx, &sr); err != nil {
			res.addErr("clear stale poster of %s: %v", sr.Title, err)
		}
	}
	return nil
}

// fileExists reports whether p is present on disk. Any stat error other than
// a clear not-exist counts as present: an unreadable filesystem is not
// evidence the file is gone.
func fileExists(p string) bool {
	_, err := os.Stat(p)
	return !errors.Is(err, fs.ErrNotExist)
}

// scanFile matches and reconciles one video file. It never returns an error:
// everything it can go wrong about is either a park reason or a scan error.
func (m *Manager) scanFile(ctx context.Context, rel string, size int64, res *ScanResult, known, seenFiles, seenUnmatched map[string]bool) {
	// Which parser reads the name is decided by where the file is, not by what
	// the name looks like: a date under a tv root is a daily episode and a
	// date under an adult root is a scene, and the same string means both.
	// "Where the file is" is the library whose root holds it (libraries.go).
	lib := libraryForPath(m.scanLibs, rel)
	isScene := lib != nil && lib.Kind == core.LibraryKindAdult
	parse := m.parse
	if isScene {
		parse = m.parseScene
	}
	p := parse(path.Base(rel))

	park := func(reason string) {
		u := &core.UnmatchedFile{Path: rel, Size: size, Parsed: p, Reason: reason}
		if err := m.store.UpsertUnmatchedFile(ctx, u); err != nil {
			res.addErr("park %s: %v", rel, err)
			return
		}
		// A file that used to match and no longer does must not keep its
		// media_files row: the queue is now the truth about it.
		if err := m.store.DeleteMediaFileByPath(ctx, rel); err != nil {
			res.addErr("clear media file %s: %v", rel, err)
		}
		seenUnmatched[rel] = true
		res.Unmatched++
	}

	if lib == nil {
		park(reasonNoLibrary)
		return
	}

	isEpisode := p.IsEpisode()
	switch {
	case isScene && !p.IsScene():
		park(reasonNoSceneDate)
		return
	case !isScene && !isEpisode && lib.Kind == core.LibraryKindTV:
		park(reasonNoEpisodeNum)
		return
	}
	switch {
	case strings.TrimSpace(p.Title) == "":
		park(reasonNoTitle)
		return
	case p.Confidence < m.minConfidence:
		park(reasonLowParse)
		return
	case isScene && m.adultFor(ctx, lib) == nil:
		park(reasonNoProvider)
		return
	case !isScene && m.metadataFor(ctx, lib) == nil:
		park(reasonNoProvider)
		return
	}

	var (
		finalRel string
		err      error
	)
	switch {
	case isScene:
		finalRel, err = m.matchAndImportScene(ctx, lib, rel, size, p, res, park)
	case isEpisode:
		finalRel, err = m.matchAndImportEpisode(ctx, lib, rel, size, p, res, park)
	default:
		finalRel, err = m.matchAndImportMovie(ctx, lib, rel, size, p, res, park)
	}
	if err != nil {
		res.addErr("import %s: %v", rel, err)
		// An organize or database failure is not the user's ambiguity to
		// resolve, so it is reported rather than parked — but the file must
		// still count as seen or the next step would delete its row.
		seenFiles[rel] = true
		return
	}
	if finalRel == "" {
		return // parked
	}

	seenFiles[finalRel] = true
	if known[finalRel] {
		res.Updated++
	} else {
		res.Added++
	}
}

// matchAndImportMovie searches the file's library's provider for rel's parsed
// title and imports the winner. A provider failure or a weak match parks the
// file rather than failing the scan.
func (m *Manager) matchAndImportMovie(ctx context.Context, lib *core.Library, rel string, size int64, p core.ParsedRelease, res *ScanResult, park func(string)) (string, error) {
	results, err := m.metadataFor(ctx, lib).SearchMovies(ctx, p.Title)
	if err != nil {
		res.addErr("search movies for %s: %v", rel, err)
		park(reasonProviderErr)
		return "", nil
	}

	cands := make([]candidate, len(results))
	for i, r := range results {
		cands[i] = candidate{title: r.Title, originalTitle: r.OriginalTitle, year: r.Year}
	}
	idx := bestMatch(cands, p.Title, p.Year)
	if idx < 0 {
		park(reasonNoMatch)
		return "", nil
	}

	meta := results[idx]
	finalRel, _, err := m.importMovie(ctx, &meta, rel, size, p, res.addErr, consumeSource, lib.ID)
	return finalRel, err
}

// matchAndImportEpisode is matchAndImportMovie's series twin. It resolves the
// full series details after the search, because episode titles and the season
// tree only come back from GetSeries.
func (m *Manager) matchAndImportEpisode(ctx context.Context, lib *core.Library, rel string, size int64, p core.ParsedRelease, res *ScanResult, park func(string)) (string, error) {
	provider := m.metadataFor(ctx, lib)
	results, err := provider.SearchSeries(ctx, p.Title)
	if err != nil {
		res.addErr("search series for %s: %v", rel, err)
		park(reasonProviderErr)
		return "", nil
	}

	cands := make([]candidate, len(results))
	for i, r := range results {
		cands[i] = candidate{title: r.Title, originalTitle: r.OriginalTitle, year: r.Year}
	}
	idx := bestMatch(cands, p.Title, p.Year)
	if idx < 0 {
		park(reasonNoMatch)
		return "", nil
	}

	meta := results[idx]
	full, err := provider.GetSeries(ctx, meta.TMDBID)
	if err != nil {
		res.addErr("get series %d for %s: %v", meta.TMDBID, rel, err)
		park(reasonProviderErr)
		return "", nil
	}
	if full == nil {
		park(reasonNoMatch)
		return "", nil
	}

	finalRel, _, err := m.importEpisode(ctx, full, rel, size, p, res.addErr, consumeSource, lib.ID)
	return finalRel, err
}

// reconcileRemovals drops the rows for files that are no longer on disk. This
// is the "remove" half of the reconciliation, and it covers both directions of
// the matched/parked transition: a file that moved from one table to the other
// is absent from the other's seen set.
//
// Movies and series rows deliberately survive a file's disappearance: an item
// with no file is a legitimate wanted item (SPEC §9), and a rescan must never
// delete user intent.
// A tree the walk deliberately skipped is not a tree whose files are gone, so
// the adult rows survive a scan made with the module switched off. Disabling
// deletes nothing (store.SetAdultEnabled says so about the library row); a
// reconciliation that quietly dropped every adult media_files row would make
// that promise false at the first rescan.
func (m *Manager) reconcileRemovals(ctx context.Context, res *ScanResult, before []core.MediaFile, beforeUnmatched []core.UnmatchedFile, seenFiles, seenUnmatched map[string]bool, adultEnabled bool) error {
	skipped := func(rel string) bool {
		if adultEnabled {
			return false
		}
		lib := libraryForPath(m.scanLibs, rel)
		return lib != nil && lib.Kind == core.LibraryKindAdult
	}

	for _, f := range before {
		// A file that moved into the unmatched queue during this scan already
		// had its row dropped; it vanished from the library, not from disk.
		if seenFiles[f.Path] || seenUnmatched[f.Path] || skipped(f.Path) {
			continue
		}
		if err := m.store.DeleteMediaFileByPath(ctx, f.Path); err != nil {
			return err
		}
		res.Removed++
	}

	// The review queue is cleared by absence from disk rather than by absence
	// from this walk, because not every parked file is under the library: the
	// import pipeline parks finished downloads it could not reconcile, and
	// those live wherever the download engine put them (SPEC §5.1). Dropping
	// them for not turning up in a library walk would make the stuck-import
	// queue disappear on the next rescan.
	for _, u := range beforeUnmatched {
		if seenUnmatched[u.Path] || seenFiles[u.Path] || skipped(u.Path) {
			continue
		}
		if _, err := os.Stat(m.abs(u.Path)); err == nil {
			continue
		}
		if err := m.store.DeleteUnmatchedFileByPath(ctx, u.Path); err != nil {
			return err
		}
	}
	return nil
}
