package library

// Site-as-series (PLAN phase 9 tasks 3 and 4).
//
// A stash-box site is stored as a series of kind "adult", its release years as
// seasons, and its scenes as episodes. That is the whole design: the wanted
// list, the backlog sweep, RSS matching, the calendar and the import pipeline
// are the ones that already exist, because a site modelled as a series is a
// thing they already know how to handle. What lives in this file is only the
// part that genuinely differs — which provider answers for a title, which
// directory its files organize into, and how a scene's release date becomes an
// episode number.

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// AdultDir is the adult library's directory inside the library root. Together
// with LibraryDir it is store.AdultLibraryRoot, and adultRootMatchesStore in
// the tests is what keeps the two from drifting apart.
const AdultDir = "Adult"

// ErrAdultDisabled reports that the adult module is switched off server-wide.
//
// It is a distinct error rather than a silent no-op wherever a caller ASKED for
// something adult — AddSite, a scene import — because a request that names a
// site is not something to quietly ignore. The automatic sweeps do treat it as
// a no-op, which is the "zero stash-box traffic when disabled" promise: nothing
// scheduled may reach the endpoint, and nothing scheduled may log an error
// about a module the owner turned off either.
var ErrAdultDisabled = errors.New("library: the adult module is disabled")

// scenePageSize is how many scenes one provider round trip asks for. A site's
// whole catalogue is walked on every refresh, so the page is the provider's
// maximum rather than a UI-sized number.
const scenePageSize = 100

// maxScenePages bounds a catalogue walk. A site with more scenes than this is
// not a site, it is a provider paging bug answering the same page forever, and
// a refresh that never terminates is worse than one that stops early.
const maxScenePages = 200

// adultSeriesDir returns a site's folder, storage-root-relative.
//
// There is no year in the name, unlike a television series: a site is not a
// production with a first-air year, it is a publisher that has been releasing
// since it opened. "library/Adult/Site Name" is the whole layout, and it is
// under the adult root rather than under TV so that excluding adult content
// from a prepared drive or a DLNA tree is one path prefix (PLAN phase 9 task 6).
func adultSeriesDir(title string) string {
	return path.Join(LibraryDir, AdultDir, sanitize(title))
}

// adultReady answers whether this Manager may talk to the adult provider right
// now, and is the single gate every adult path in this package goes through.
//
// Both halves matter and they fail differently. The module being off is a
// decision the owner made, and it is what guarantees the endpoint is never
// reached; no provider configured is a setup step nobody has done yet.
func (m *Manager) adultReady(ctx context.Context) error {
	enabled, err := m.store.AdultEnabled(ctx)
	if err != nil {
		return err
	}
	if !enabled {
		return ErrAdultDisabled
	}
	if m.adult == nil {
		return core.ErrNoAdultProvider
	}
	return nil
}

// AddSite adds a site to the library by stash-box id, as a series of kind
// adult, and files every scene the provider knows about as an episode.
//
// It is AddSeries' counterpart and behaves the same way: nothing is written to
// disk, the whole catalogue lands as rows so the site view can show what is
// missing, and adding a site that is already there refreshes it while keeping
// the owner's monitored flag and profile assignment.
func (m *Manager) AddSite(ctx context.Context, stashID string) (*core.Series, error) {
	if err := m.adultReady(ctx); err != nil {
		return nil, err
	}
	if stashID == "" {
		return nil, fmt.Errorf("library: empty stash id")
	}

	meta, err := m.adult.GetSite(ctx, stashID)
	if err != nil {
		return nil, fmt.Errorf("library: get site %s: %w", stashID, err)
	}
	if meta == nil {
		return nil, fmt.Errorf("library: site %s not found", stashID)
	}

	sr, _, err := m.upsertSiteRow(ctx, meta, adultSeriesDir(meta.Name), "")
	if err != nil {
		return nil, err
	}
	if err := m.syncSiteScenes(ctx, sr); err != nil {
		return nil, err
	}
	return sr, nil
}

// upsertSiteRow is upsertSeriesRow's adult twin: same preserve-user-intent
// rule, matched on the stash id rather than the TMDB id.
//
// A site has no release year, so Year stays zero and the folder name carries
// none. Status stays empty for the same reason: stash-box has no notion of a
// site having ended, and inventing "Continuing" would put a claim in the UI
// that no provider made.
func (m *Manager) upsertSiteRow(ctx context.Context, meta *core.SiteMeta, dir, posterRel string) (*core.Series, bool, error) {
	sr := &core.Series{
		StashID:    meta.StashID,
		Title:      meta.Name,
		SortTitle:  sortTitle(meta.Name),
		Kind:       core.SeriesKindAdult,
		Path:       dir,
		PosterPath: posterRel,
		PosterURL:  meta.ImageURL,
		Monitored:  true,
	}

	created := true
	if meta.StashID != "" {
		existing, err := m.store.GetSeriesByStashID(ctx, meta.StashID)
		switch {
		case err == nil:
			created = false
			sr.ID = existing.ID
			sr.Monitored = existing.Monitored
			sr.QualityProfileID = existing.QualityProfileID
			sr.AddedAt = existing.AddedAt
			// The folder on disk is ground truth, exactly as it is for a movie
			// refresh: a site renamed upstream must not point the row at a
			// directory that does not exist.
			if existing.Path != "" {
				sr.Path = existing.Path
			}
			if posterRel == "" {
				sr.PosterPath = existing.PosterPath
			}
		case !errors.Is(err, store.ErrNotFound):
			return nil, false, err
		}
	}

	if err := m.store.UpsertSeries(ctx, sr); err != nil {
		return nil, false, err
	}
	return sr, created, nil
}

// syncSiteScenes walks a site's whole catalogue and writes it as seasons and
// episodes.
//
// The walk is complete rather than incremental because episode numbering needs
// every scene at once: a scene's number is its sequence within its release
// year, which is not in any single scene's metadata.
func (m *Manager) syncSiteScenes(ctx context.Context, sr *core.Series) error {
	scenes, err := m.siteScenes(ctx, sr.StashID)
	if err != nil {
		return err
	}
	return m.writeScenes(ctx, sr, scenes)
}

// siteScenes pages the provider for every scene a site has released.
func (m *Manager) siteScenes(ctx context.Context, siteStashID string) ([]core.SceneMeta, error) {
	var out []core.SceneMeta
	seen := map[string]bool{}

	for page := 1; page <= maxScenePages; page++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		result, err := m.adult.SearchScenes(ctx, core.SceneQuery{
			SiteStashID: siteStashID,
			Page:        page,
			PerPage:     scenePageSize,
		})
		if err != nil {
			return nil, fmt.Errorf("library: list scenes of site %s: %w", siteStashID, err)
		}
		if result == nil || len(result.Scenes) == 0 {
			return out, nil
		}
		for _, scene := range result.Scenes {
			// A provider that answers the same page twice would otherwise
			// duplicate its catalogue into the numbering; the id is the only
			// thing that says two results are the same scene.
			if scene.StashID == "" || seen[scene.StashID] {
				continue
			}
			seen[scene.StashID] = true
			out = append(out, scene)
		}
		if result.Total > 0 && len(seen) >= result.Total {
			return out, nil
		}
		if len(result.Scenes) < result.PerPage {
			return out, nil
		}
	}
	return out, nil
}

// writeScenes reconciles a site's scenes into season and episode rows.
func (m *Manager) writeScenes(ctx context.Context, sr *core.Series, scenes []core.SceneMeta) error {
	existing, err := m.store.ListEpisodes(ctx, sr.ID)
	if err != nil {
		return err
	}
	scenes, err = m.dropForeignScenes(ctx, existing, scenes)
	if err != nil {
		return err
	}
	numbered := numberScenes(scenes, existing)

	seasons := map[int]bool{}
	for _, ep := range numbered {
		seasons[ep.SeasonNumber] = true
	}
	existingSeasons, err := m.store.ListSeasons(ctx, sr.ID)
	if err != nil {
		return err
	}
	seasonMonitored := make(map[int]bool, len(existingSeasons))
	for _, s := range existingSeasons {
		seasonMonitored[s.Number] = s.Monitored
	}

	years := make([]int, 0, len(seasons))
	for year := range seasons {
		years = append(years, year)
	}
	sort.Ints(years)
	for _, year := range years {
		monitored, ok := seasonMonitored[year]
		if !ok {
			// A release year has no specials equivalent, so unlike a television
			// season there is no year that starts unmonitored.
			monitored = true
		}
		if err := m.store.UpsertSeason(ctx, &core.Season{
			SeriesID:  sr.ID,
			Number:    year,
			Title:     strconv.Itoa(year),
			AirDate:   time.Date(year, time.January, 1, 0, 0, 0, 0, time.UTC),
			Monitored: monitored,
		}); err != nil {
			return err
		}
	}

	episodeMonitored := make(map[string]bool, len(existing))
	for _, e := range existing {
		if e.StashID != "" {
			episodeMonitored[e.StashID] = e.Monitored
		}
	}
	for i := range numbered {
		episode := &numbered[i]
		episode.SeriesID = sr.ID
		monitored, ok := episodeMonitored[episode.StashID]
		if !ok {
			monitored = true
		}
		episode.Monitored = monitored
		if err := m.store.UpsertEpisode(ctx, episode); err != nil {
			return err
		}
	}
	return nil
}

// dropForeignScenes removes the scenes that are already filed as an episode of
// ANOTHER site.
//
// Two sites' catalogues genuinely overlap. stash-box models sub-studios, and
// stashbox.SearchScenes asks with the INCLUDES modifier precisely so that a
// site's own scenes come back whether they are attributed to it or to one of
// its sub-studios — which means a parent site's catalogue contains its
// sub-sites' scenes, and adding both sites offers the same scene twice.
//
// episodes.stash_id carries a GLOBAL partial unique index (0013_adult.sql), so
// the second write is not a duplicate row, it is a constraint violation. And
// because numberScenes hands its output back oldest-first, that violation
// aborts the walk part-way and takes the sub-site's own, non-conflicting scenes
// with it: the site row is already written, every retry fails identically, and
// the catalogue stays empty forever. numberScenes cannot see this on its own —
// it is given only this series' episodes, so a scene owned elsewhere looks new.
//
// The scene stays with the site that filed it first. That is the same stability
// rule numberScenes obeys for its numbers, and for the same reason: the episode
// row is the address a file on disk is named after and the key its
// episode_files link hangs off, so re-homing a scene to a second site would
// rename a file and orphan a link to settle nothing more than which of two true
// answers to store.
func (m *Manager) dropForeignScenes(ctx context.Context, existing []core.Episode, scenes []core.SceneMeta) ([]core.SceneMeta, error) {
	ids := make([]string, 0, len(scenes))
	for _, scene := range scenes {
		ids = append(ids, scene.StashID)
	}
	filed, err := m.store.EpisodeIDsByStashID(ctx, ids)
	if err != nil {
		return nil, err
	}
	if len(filed) == 0 {
		return scenes, nil
	}
	mine := make(map[string]bool, len(existing))
	for _, e := range existing {
		if e.StashID != "" {
			mine[e.StashID] = true
		}
	}

	out := make([]core.SceneMeta, 0, len(scenes))
	for _, scene := range scenes {
		if filed[scene.StashID] != 0 && !mine[scene.StashID] {
			continue
		}
		out = append(out, scene)
	}
	return out, nil
}

// numberScenes turns provider scenes into episode rows: release year becomes
// the season, and the scene's sequence within that year becomes the episode
// number.
//
// The numbering is STABLE, and that is the whole difficulty. A number, once
// assigned, is the address a file on disk is named after and the key the
// episode_files links hang off; renumbering because a site published a scene
// out of order, or because the provider back-filled one from 2019, would
// rename every later file in that year and orphan the ones already linked. So
// a scene that already has a row keeps its number, and only genuinely new
// scenes are numbered — in date order, after the highest number that year has
// already used.
//
// Scenes with no release date are dropped: the date IS the season, and a scene
// that cannot say when it came out cannot be filed. That is a provider gap,
// not something to guess at.
func numberScenes(scenes []core.SceneMeta, existing []core.Episode) []core.Episode {
	assigned := map[string]core.Episode{}
	nextNumber := map[int]int{}
	for _, e := range existing {
		if e.StashID != "" {
			assigned[e.StashID] = e
		}
		if e.EpisodeNumber >= nextNumber[e.SeasonNumber] {
			nextNumber[e.SeasonNumber] = e.EpisodeNumber + 1
		}
	}

	fresh := make([]core.SceneMeta, 0, len(scenes))
	out := make([]core.Episode, 0, len(scenes))
	for _, scene := range scenes {
		if scene.Date.IsZero() {
			continue
		}
		if prior, ok := assigned[scene.StashID]; ok {
			out = append(out, episodeFromScene(scene, prior.SeasonNumber, prior.EpisodeNumber))
			continue
		}
		fresh = append(fresh, scene)
	}

	// Oldest first, so a site's first scene of a year is episode 1. The code
	// and the id are tie-breaks rather than orderings in their own right: they
	// only decide same-day scenes, and they make the result deterministic
	// whatever order the provider paged them back in.
	sort.SliceStable(fresh, func(i, j int) bool {
		a, b := fresh[i], fresh[j]
		if !a.Date.Equal(b.Date) {
			return a.Date.Before(b.Date)
		}
		if a.Code != b.Code {
			return a.Code < b.Code
		}
		return a.StashID < b.StashID
	})
	for _, scene := range fresh {
		year := scene.Date.Year()
		number := nextNumber[year]
		if number == 0 {
			number = 1
		}
		nextNumber[year] = number + 1
		out = append(out, episodeFromScene(scene, year, number))
	}
	return out
}

// episodeFromScene renders one scene as an episode row.
func episodeFromScene(scene core.SceneMeta, season, number int) core.Episode {
	performers := make([]string, 0, len(scene.Performers))
	for _, p := range scene.Performers {
		// The alias this scene credits somebody under when there is one: it is
		// what the scene's own page shows and what a release filename carries.
		name := p.As
		if name == "" {
			name = p.Name
		}
		if name != "" {
			performers = append(performers, name)
		}
	}
	studio := scene.SiteName
	return core.Episode{
		SeasonNumber:  season,
		EpisodeNumber: number,
		StashID:       scene.StashID,
		Title:         scene.Title,
		Overview:      scene.Overview,
		AirDate:       scene.Date,
		Scene: &core.SceneInfo{
			Studio:     studio,
			Performers: performers,
			URL:        scene.URL,
		},
	}
}

// refreshSites is the adult half of RefreshLibrary.
//
// It is a no-op — not an error, and not one provider call — when the module is
// off or no stash-box credential is configured. That is the acceptance
// criterion this function exists to satisfy: a full job cycle on a server with
// adult content disabled must make ZERO requests to the stash-box endpoint,
// and a refresh sweep is the recurring job that would otherwise make them.
func (m *Manager) refreshSites(ctx context.Context, res *RefreshResult) error {
	if err := m.adultReady(ctx); err != nil {
		if errors.Is(err, ErrAdultDisabled) || errors.Is(err, core.ErrNoAdultProvider) {
			return nil
		}
		return err
	}

	sites, err := m.store.ListSeriesByKind(ctx, core.SeriesKindAdult)
	if err != nil {
		return err
	}
	for _, sr := range sites {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !sr.Monitored || sr.StashID == "" {
			continue
		}
		meta, err := m.adult.GetSite(ctx, sr.StashID)
		if err != nil {
			res.addErr("refresh site %q: %v", sr.Title, err)
			continue
		}
		if meta == nil {
			continue
		}
		row, _, err := m.upsertSiteRow(ctx, meta, sr.Path, "")
		if err != nil {
			return err
		}
		if err := m.syncSiteScenes(ctx, row); err != nil {
			res.addErr("refresh scenes of %q: %v", sr.Title, err)
			continue
		}
		res.Sites++
	}
	return nil
}

// importScene organizes one scene file into a site that is already in the
// library, and links it to the episode row the scene resolved to.
//
// It differs from importEpisode in exactly two things: the folder comes from
// the site's own row under the adult root rather than from library/TV, and the
// series row and its season/episode tree are not written here — they came from
// the provider walk, which is the only thing that can number a scene. p must
// already carry the resolved Season and Episodes (see resolveScene); everything
// after placement is linkImportedFile, shared with importEpisode.
func (m *Manager) importScene(ctx context.Context, sr *core.Series, rel string, size int64, p core.ParsedRelease, airDate time.Time, episodeTitle string, warn warnf, disp sourceDisposition) (string, int64, error) {
	if len(p.Episodes) == 0 || airDate.IsZero() {
		return "", 0, fmt.Errorf("library: %s did not resolve to a scene", rel)
	}

	dir := sr.Path
	if dir == "" {
		dir = adultSeriesDir(sr.Title)
	}
	dst := path.Join(dir, seasonFolderName(p.Season),
		sceneFileName(sr.Title, airDate, episodeTitle, path.Ext(rel)))

	finalRel, err := m.placeFile(rel, dst, disp)
	if err != nil {
		return "", 0, err
	}

	// No tvshow.nfo: the NFO writers render TMDB-shaped television metadata,
	// and a site is not that. Stash is the adult library's player-side identity
	// step and it is phase 11's job, not a file this phase invents a format for.
	posterRel, err := m.ensurePoster(ctx, dir, sr.PosterURL)
	if err != nil {
		warn("%v", err)
	}
	if sr.Path != dir || (posterRel != "" && sr.PosterPath != posterRel) {
		row := *sr
		row.Path = dir
		if posterRel != "" {
			row.PosterPath = posterRel
		}
		if err := m.store.UpsertSeries(ctx, &row); err != nil {
			return "", 0, err
		}
		*sr = row
	}

	episodeIDs, err := m.ensureEpisodes(ctx, sr.ID, p.Season, p.Episodes)
	if err != nil {
		return "", 0, err
	}
	if err := m.linkImportedFile(ctx, rel, finalRel, size, p, episodeIDs); err != nil {
		return "", 0, err
	}
	return finalRel, sr.ID, nil
}

// resolveScene turns a scene parse into an ordinary episode parse by finding
// the episode row whose air date is the date the filename claims.
//
// This is the join that lets everything downstream be reused rather than
// forked: once Season and Episodes are filled in, p is the same shape an
// episode release produces, and verifyEpisodeImport, existingEpisodeFiles and
// replaceEpisodeFiles all work on it unchanged.
//
// It reports the episode it resolved to, or a park reason. A date the site has
// no scene for is a decision (the release is for a scene the provider has not
// published, or for a different site) rather than a failure.
func (m *Manager) resolveScene(ctx context.Context, seriesID int64, p core.ParsedRelease) (*core.Episode, core.ParsedRelease, string, error) {
	if !p.IsScene() {
		return nil, p, reasonNoSceneDate, nil
	}
	episodes, err := m.store.ListEpisodes(ctx, seriesID)
	if err != nil {
		// A database failure is not the user's ambiguity to resolve, so it is
		// reported rather than parked: parking would record "no scene released
		// on that date" about a query that never ran.
		return nil, p, "", err
	}
	var match *core.Episode
	for i := range episodes {
		if !sameDay(episodes[i].AirDate, p.SceneDate) {
			continue
		}
		if match != nil {
			// Two scenes released the same day, and the filename carries
			// nothing that tells them apart. Guessing would import a scene as
			// the wrong one and then supersede the right one's file on the next
			// grab, so it parks for a human instead.
			return nil, p, reasonAmbiguousScene, nil
		}
		match = &episodes[i]
	}
	if match == nil {
		return nil, p, reasonNoSceneMatch, nil
	}
	p.Season, p.Episodes = match.SeasonNumber, []int{match.EpisodeNumber}
	return match, p, "", nil
}

// importDownloadedScenes is importDownloadedEpisodes' adult twin.
//
// It is short because the mapping does the work: once a file's date has been
// resolved to the episode row it belongs to, p carries a season and an episode
// number and is indistinguishable from an ordinary episode parse — so the
// existing-file lookup, the verification and the supersede-and-record steps
// are the same functions, called with the same arguments.
//
// It makes no provider call. The site's catalogue was walked when the site was
// added and is re-walked by the refresh sweep; a download landing is not the
// moment to discover a scene, and a grab is only ever made against an episode
// row that already exists.
func (m *Manager) importDownloadedScenes(ctx context.Context, files []downloadedFile, grab core.GrabInfo, sr *core.Series) (int, int, error) {
	var imported, parked int
	largest := largestFile(files)
	for _, file := range files {
		p := m.parseScene(filepath.Base(file.rel))

		episode, resolved, reason, err := m.resolveScene(ctx, sr.ID, p)
		if err != nil {
			return imported, parked, err
		}
		if reason == "" && !grabCoversEpisode(grab, episode.ID) {
			// The file resolved to a scene this grab was not for. Importing it
			// would silently satisfy a different wanted item with a release
			// nothing graded against that item's profile.
			reason = reasonSceneNotInGrab
		}
		// The release title can vouch for one file only — the feature-sized
		// one — exactly as importDownloadedEpisodes does: usenet posts
		// routinely obfuscate the payload's name, the grab is the evidence of
		// what was fetched, and its title still has to resolve to a scene this
		// grab actually covers before it is believed.
		if reason != "" && p.SceneDate.IsZero() && file.rel == largest.rel {
			if title := strings.TrimSpace(grab.ReleaseTitle); title != "" {
				rp := m.parseScene(title)
				rescued, rresolved, rreason, rerr := m.resolveScene(ctx, sr.ID, rp)
				if rerr != nil {
					return imported, parked, rerr
				}
				if rreason == "" && grabCoversEpisode(grab, rescued.ID) {
					episode, resolved, reason, p = rescued, rresolved, "", rp
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
		p = resolved

		existing, err := m.existingEpisodeFiles(ctx, grab, p)
		if err != nil {
			return imported, parked, err
		}

		var warnings []string
		warn := func(format string, args ...any) {
			warnings = append(warnings, fmt.Sprintf(format, args...))
		}
		rel, seriesID, err := m.importScene(ctx, sr, file.rel, file.size, p, episode.AirDate, episode.Title, warn, keepSource)
		if err != nil {
			return imported, parked, err
		}
		newFile, err := m.verifyEpisodeImport(ctx, rel, file.size, seriesID, p)
		if err != nil {
			return imported, parked, err
		}
		if err := m.replaceEpisodeFiles(ctx, sr.Title, file.rel, existing, newFile, grab, seriesID, p); err != nil {
			return imported, parked, err
		}
		if err := m.recordImport(ctx, rel, grab, 0, seriesID, warnings); err != nil {
			return imported, parked, err
		}
		imported++
	}
	return imported, parked, nil
}

func grabCoversEpisode(grab core.GrabInfo, id int64) bool {
	for _, want := range grab.EpisodeIDs {
		if want == id {
			return true
		}
	}
	return false
}

// matchAndImportScene is matchAndImportEpisode's adult twin: it resolves the
// site the filename names, the scene its date names, and imports the file.
//
// The provider is a last resort rather than a first step, which is what makes a
// rescan of a large adult library affordable. A site already in the library
// answers from the database, and the stash-box catalogue is only walked when
// the site is new or when the date belongs to a scene the library has not seen
// yet — once per site per scan, because a walk that found nothing would find
// nothing again for the next file.
func (m *Manager) matchAndImportScene(ctx context.Context, rel string, size int64, p core.ParsedRelease, res *ScanResult, park func(string)) (string, error) {
	sr, err := m.adultSeriesByTitle(ctx, p.Title)
	if err != nil {
		return "", err
	}

	var episode *core.Episode
	reason := reasonNoSceneMatch
	if sr != nil {
		if episode, p, reason, err = m.resolveScene(ctx, sr.ID, p); err != nil {
			return "", err
		}
	}

	if reason != "" && (sr == nil || !m.syncedSites[sr.ID]) {
		sr, err = m.syncSiteFor(ctx, sr, p.Title, res, park)
		if err != nil || sr == nil {
			return "", err
		}
		if episode, p, reason, err = m.resolveScene(ctx, sr.ID, p); err != nil {
			return "", err
		}
	}
	if reason != "" {
		park(reason)
		return "", nil
	}

	finalRel, _, err := m.importScene(ctx, sr, rel, size, p, episode.AirDate, episode.Title, res.addErr, consumeSource)
	return finalRel, err
}

// syncSiteFor brings one site's catalogue up to date, finding it in the
// provider first when the library does not have it yet. It reports nil (having
// already parked) when the site cannot be resolved at all.
func (m *Manager) syncSiteFor(ctx context.Context, sr *core.Series, title string, res *ScanResult, park func(string)) (*core.Series, error) {
	if sr == nil {
		sites, err := m.adult.SearchSites(ctx, title)
		if err != nil {
			res.addErr("search sites for %q: %v", title, err)
			park(reasonProviderErr)
			return nil, nil
		}
		cands := make([]candidate, len(sites))
		for i, site := range sites {
			cands[i] = candidate{title: site.Name}
		}
		idx := bestMatch(cands, title, 0)
		if idx < 0 {
			park(reasonNoMatch)
			return nil, nil
		}
		sr, _, err = m.upsertSiteRow(ctx, &sites[idx], adultSeriesDir(sites[idx].Name), "")
		if err != nil {
			return nil, err
		}
	}

	if err := m.syncSiteScenes(ctx, sr); err != nil {
		res.addErr("list scenes of %q: %v", sr.Title, err)
		park(reasonProviderErr)
		return nil, nil
	}
	if m.syncedSites != nil {
		m.syncedSites[sr.ID] = true
	}
	return sr, nil
}

// adultSeriesByTitle finds the site a filename names among the sites already in
// the library, or nil. Matching is the same normalized-title rule the provider
// candidates go through, so a site found locally and a site found upstream are
// accepted on identical evidence.
func (m *Manager) adultSeriesByTitle(ctx context.Context, title string) (*core.Series, error) {
	sites, err := m.store.ListSeriesByKind(ctx, core.SeriesKindAdult)
	if err != nil {
		return nil, err
	}
	cands := make([]candidate, len(sites))
	for i, site := range sites {
		cands[i] = candidate{title: site.Title}
	}
	idx := bestMatch(cands, title, 0)
	if idx < 0 {
		return nil, nil
	}
	return &sites[idx], nil
}

// sameDay compares two instants by calendar date in UTC. Air dates are stored
// as dates and scene dates are parsed as dates, so this is an equality test
// that says so rather than one that depends on both sides having been
// truncated the same way.
func sameDay(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return false
	}
	ay, am, ad := a.UTC().Date()
	by, bm, bd := b.UTC().Date()
	return ay == by && am == bm && ad == bd
}
