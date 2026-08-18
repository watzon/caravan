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

// ErrAdultDisabled reports that the adult library this operation would act on
// is switched off — for a sweep that names none, that no adult library is on at
// all.
//
// It is a distinct error rather than a silent no-op wherever a caller ASKED for
// something adult — AddSite, a scene import — because a request that names a
// site is not something to quietly ignore. The automatic sweeps do treat it as
// a no-op, which is the "zero stash-box traffic when disabled" promise: nothing
// scheduled may reach the endpoint, and nothing scheduled may log an error
// about a shelf the owner turned off either.
var ErrAdultDisabled = errors.New("library: the adult module is disabled")

// scenePageSize is how many scenes one provider round trip asks for. A site's
// whole catalogue is walked on every refresh, so the page is the provider's
// maximum rather than a UI-sized number.
const scenePageSize = 100

// maxScenePages bounds a catalogue walk. A site with more scenes than this is
// not a site, it is a provider paging bug answering the same page forever, and
// a refresh that never terminates is worse than one that stops early.
const maxScenePages = 200

// adultSeriesDir returns a site's folder under its adult library's root.
//
// There is no year in the name, unlike a television series: a site is not a
// production with a first-air year, it is a publisher that has been releasing
// since it opened. "library/Adult/Site Name" is the whole layout, and it is
// under an adult root rather than under TV so that excluding adult content
// from a prepared drive or a DLNA tree is a path-prefix check per adult
// library (PLAN phase 9 task 6).
func adultSeriesDir(lib *core.Library, title string) string {
	return path.Join(lib.RootPath, sanitize(title))
}

// adultReady answers whether this Manager may talk to the adult provider AT
// ALL right now. It is the gate for the paths that name no library — the
// sweeps, and the jobs that have not yet read the row they are about to act on.
//
// Both halves matter and they fail differently. Not one adult library being
// switched on is a decision the owner made, and it is what guarantees the
// endpoint is never reached; no provider configured is a setup step nobody has
// done yet.
//
// A path that HOLDS the library it is about to act on asks adultReadyIn
// instead: with the switch living on the rows, "some adult library is on" is
// the weaker question, and answering it for a caller that named a dormant one
// would reach the endpoint on that library's behalf.
func (m *Manager) adultReady(ctx context.Context) error {
	on, err := m.store.AnyActiveLibraryOfKind(ctx, core.LibraryKindAdult)
	if err != nil {
		return err
	}
	if !on {
		return ErrAdultDisabled
	}
	if m.adult == nil {
		return core.ErrNoAdultProvider
	}
	return nil
}

// adultReadyIn is adultReady for a caller that already holds its target
// library: the same two failures, with the master switch read off the row
// rather than off the kind.
func (m *Manager) adultReadyIn(lib *core.Library) error {
	if lib == nil || !lib.Active {
		return ErrAdultDisabled
	}
	if m.adult == nil {
		return core.ErrNoAdultProvider
	}
	return nil
}

// AddSite adds a site to the library by stash-box id, as a series of kind
// adult, WITHOUT walking its catalogue.
//
// It is AddSeries' counterpart in everything except that: nothing is written to
// disk, and adding a site that is already there refreshes its metadata while
// keeping the owner's monitored flag and profile assignment. What it does not
// do is file the site's scenes, because a big site is hundreds of provider
// round trips and a request that waits for them is a request people give up on
// and repeat. The caller is expected to queue core.JobSyncSite (see SyncSite);
// the site page's empty state says so while the job is open.
//
// A caller that genuinely needs the scenes to exist the moment this returns —
// the member-request approval, whose whole point is that the asked-for scene
// becomes a wanted episode — wants AddSiteAndWait instead.
//
// monitored follows monitoredOrDefault: nil means unmonitored, and it decides
// a new row only.
//
// ref names the stash-box INSTANCE the id was read from as well as the id, for
// AddMovie's reason: a UUID means nothing without the box that minted it, and
// two boxes hold the same UUID under different sites. An empty provider is the
// legacy instance (adultRef), which is what a client written before instances
// sends and what a single-box install resolves to anyway.
func (m *Manager) AddSite(ctx context.Context, ref core.ItemRef, monitored *bool, libraryID int64) (*core.Series, error) {
	return m.addSite(ctx, ref, monitored, libraryID, false)
}

// AddSiteAndWait is AddSite with the catalogue walk done before it returns.
//
// It exists for exactly one caller shape: one that must see the site's episode
// rows immediately, because it is about to act on one of them. Approving a
// scene request is that caller — the scene it granted has to be a wanted
// episode by the time the approval answers, or the request is closed against
// nothing.
func (m *Manager) AddSiteAndWait(ctx context.Context, ref core.ItemRef, monitored *bool, libraryID int64) (*core.Series, error) {
	return m.addSite(ctx, ref, monitored, libraryID, true)
}

func (m *Manager) addSite(ctx context.Context, ref core.ItemRef, monitored *bool, libraryID int64, walk bool) (*core.Series, error) {
	ref = adultRef(ref)
	if ref.Ref == "" {
		return nil, fmt.Errorf("library: empty stash id")
	}

	// The TARGET library is resolved before the gate rather than after it,
	// because the gate is that library's own master switch: an add into a
	// dormant shelf must be refused even while a sibling adult library is on.
	// Nothing between here and the gate reaches a provider — siteLibrary is
	// store reads — so the zero-traffic order is unchanged.
	lib, err := m.siteLibrary(ctx, ref, "", libraryID)
	if errors.Is(err, store.ErrNotFound) {
		// No adult library on the install at all is OFF, not open: a shelf that
		// does not exist reaches no endpoint and shows nobody anything. That is
		// the same reading core.LibrarySet gives a row whose kind has no library.
		return nil, ErrAdultDisabled
	}
	if err != nil {
		return nil, err
	}
	if err := m.adultReadyIn(lib); err != nil {
		return nil, err
	}

	// The instance the REF names, not the library's chain head: the id is
	// written in one box's vocabulary and the ref is the only thing that says
	// which (see adultByID).
	provider := m.adultByID(ctx, ref.Provider)
	if provider == nil {
		return nil, core.ErrNoAdultProvider
	}
	meta, err := provider.GetSite(ctx, ref.Ref)
	if err != nil {
		return nil, fmt.Errorf("library: get site %s/%s: %w", ref.Provider, ref.Ref, err)
	}
	if meta == nil {
		return nil, fmt.Errorf("library: site %s/%s not found", ref.Provider, ref.Ref)
	}

	sr, _, err := m.upsertSiteRow(ctx, ref.Provider, meta, adultSeriesDir(lib, meta.Name), "", monitored, lib.ID)
	if err != nil {
		return nil, err
	}
	if walk {
		if err := m.syncSiteScenes(ctx, sr); err != nil {
			return nil, err
		}
	}
	return sr, nil
}

// SyncSite walks one site's catalogue by library id. It is the core.JobSyncSite
// handler's whole body, and the deferred half of AddSite.
//
// A site that has been removed since the job was queued is not an error: there
// is nothing left to walk, and failing would retry a job that can never
// succeed. Neither is its library having been switched off in the meantime —
// that is refreshSites' rule, and it is what keeps a job queued before the
// switch from being the one path that reaches stash-box after it.
//
// The switch is asked twice, and the second question is the narrow one: the
// install-wide gate refuses before any row is read, and the site's OWN library
// refuses once the row says which shelf it belongs to. A sibling adult library
// still being on is not permission to walk a dormant one's catalogue.
func (m *Manager) SyncSite(ctx context.Context, seriesID int64) error {
	if err := m.adultReady(ctx); err != nil {
		if errors.Is(err, ErrAdultDisabled) || errors.Is(err, core.ErrNoAdultProvider) {
			return nil
		}
		return err
	}
	sr, err := m.store.GetSeries(ctx, seriesID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if sr.Kind != core.SeriesKindAdult || sr.StashID == "" {
		return nil
	}
	lib, err := m.seriesLibraryOf(ctx, sr)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !lib.Active {
		return nil
	}
	return m.syncSiteScenes(ctx, sr)
}

// upsertSiteRow is upsertSeriesRow's adult twin: same preserve-user-intent
// rule, matched on the stash id rather than the TMDB id.
//
// A site has no release year, so Year stays zero and the folder name carries
// none. Status stays empty for the same reason: stash-box has no notion of a
// site having ended, and inventing "Continuing" would put a claim in the UI
// that no provider made.
//
// providerID is the INSTANCE that answered, and it is the row's identity from
// here on: every later refresh of this site asks that box and no other. The
// bare `stashbox` is what an empty id means (adultRef) and what every row
// written before instances carries.
func (m *Manager) upsertSiteRow(ctx context.Context, providerID string, meta *core.SiteMeta, dir, posterRel string, monitored *bool, libraryID int64) (*core.Series, bool, error) {
	if providerID == "" {
		providerID = core.ProviderStashbox
	}
	sr := &core.Series{
		// A site is pinned like every other item: one instance answered, and the
		// stash id is its ref (store.normalizeSeriesProvider says the same from
		// the other side, for rows written before 0024).
		Provider:    providerID,
		ProviderRef: meta.StashID,
		StashID:     meta.StashID,
		Title:       meta.Name,
		SortTitle:   sortTitle(meta.Name),
		Kind:        core.SeriesKindAdult,
		Path:        dir,
		PosterPath:  posterRel,
		PosterURL:   meta.ImageURL,
		Monitored:   monitoredOrDefault(monitored),
		LibraryID:   libraryID,
	}

	created := true
	if meta.StashID != "" {
		// (instance, ref) rather than the bare stash id: since 0026 two boxes
		// may legitimately hold the same UUID, and matching on the id alone
		// would make the second box's site an update of the first box's row.
		existing, err := m.store.GetSeriesByProviderRef(ctx, providerID, meta.StashID)
		switch {
		case err == nil:
			created = false
			sr.ID = existing.ID
			sr.Monitored = existing.Monitored
			sr.QualityProfileID = existing.QualityProfileID
			sr.AddedAt = existing.AddedAt
			sr.LibraryID = existing.LibraryID
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
// The walk publishes as it goes, one complete release year at a time, so the
// site page fills in while the pages are still arriving rather than staying
// empty until the last one lands. walkSiteScenes owns that; everything about
// how a scene becomes an episode row is writeScenes' and numberScenes' job,
// unchanged.
//
// The catalogue comes from the site's own PINNED instance, never from the
// library's chain head. The scene UUIDs already on these episode rows were
// minted by that box; walking another one would file its scenes under this
// site's numbering and leave the row claiming a catalogue it does not have.
func (m *Manager) syncSiteScenes(ctx context.Context, sr *core.Series) error {
	provider := m.adultByID(ctx, sr.Provider)
	if provider == nil {
		return fmt.Errorf("library: %w: site %q is pinned to %s",
			core.ErrNoAdultProvider, sr.Title, adultRef(core.ItemRef{Provider: sr.Provider}).Provider)
	}
	return m.walkSiteScenes(ctx, provider, sr.StashID, func(batch []core.SceneMeta) error {
		return m.writeScenes(ctx, sr, batch)
	})
}

// sceneBatch receives one publishable group of scenes during a catalogue walk.
type sceneBatch func([]core.SceneMeta) error

// walkSiteScenes pages the provider for every scene a site has released and
// hands them to flush as it goes.
//
// It writes incrementally because a large site is two hundred provider round
// trips and the site page is open while they happen: a walk that wrote nothing
// until it finished left that page empty for minutes, with no way to tell
// "still working" from "this site has nothing".
//
// The unit it publishes is a RELEASE YEAR rather than a page, and that is the
// whole subtlety. A scene's number is its sequence within its release year,
// which no single scene's metadata carries — it can only be computed from every
// scene of that year at once. Writing page by page would number each year in
// arrival order, and the provider is asked for DATE/DESC (see
// stashbox.SearchScenes), so the NEWEST scene of a year would become episode 1
// and the site page — which orders scenes on that number — would list every
// year backwards.
//
// Holding a year until it is complete is cheap for exactly the same reason:
// under DESC ordering, once a scene from an older year has arrived no scene
// from a newer one can. Each year is then numbered from its full set, which is
// the numbering a single end-of-walk write would have produced. The years are
// published newest first, because that is the end the site page shows and the
// end somebody watching it is waiting for.
//
// A provider that answered out of order would settle a year early and append
// whatever arrived late to the end of that year's numbering. That is not a new
// failure mode: it is what a refresh already does when the provider back-fills
// an old scene, and numberScenes' stability rule is what makes it survivable
// either way. A site whose whole catalogue sits in one year publishes once, at
// the end — nothing can be numbered before its year is complete, and a
// single-year site is a short walk anyway.
func (m *Manager) walkSiteScenes(ctx context.Context, provider core.AdultMetadataProvider, siteStashID string, flush sceneBatch) error {
	seen := map[string]bool{}
	// Scenes waiting for their year to be proven complete, keyed by that year,
	// and lowest is the oldest year the walk has reached. Everything above it
	// is settled; lowest itself may still be receiving scenes.
	pending := map[int][]core.SceneMeta{}
	lowest := 0
	// Scenes the provider gave no release date. numberScenes drops them — the
	// date IS the season — so they take no part in the completeness bookkeeping
	// (their year would read as 1 and hold every real year open). They are
	// still handed over at the end, so a flush sees the provider's whole answer
	// rather than a filtered one.
	var undated []core.SceneMeta

	// publish hands over buffered years, newest first. With settledOnly it
	// keeps back the year the walk is still inside; without it, everything.
	publish := func(settledOnly bool) error {
		years := make([]int, 0, len(pending))
		for year := range pending {
			if settledOnly && year <= lowest {
				continue
			}
			years = append(years, year)
		}
		sort.Sort(sort.Reverse(sort.IntSlice(years)))
		for _, year := range years {
			if err := flush(pending[year]); err != nil {
				return err
			}
			delete(pending, year)
		}
		return nil
	}

	// drain ends the walk: the year still open, then the undated remainder.
	drain := func() error {
		if err := publish(false); err != nil {
			return err
		}
		if len(undated) == 0 {
			return nil
		}
		batch := undated
		undated = nil
		return flush(batch)
	}

	for page := 1; page <= maxScenePages; page++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		result, err := provider.SearchScenes(ctx, core.SceneQuery{
			SiteStashID: siteStashID,
			Page:        page,
			PerPage:     scenePageSize,
		})
		if err != nil {
			return fmt.Errorf("library: list scenes of site %s: %w", siteStashID, err)
		}
		if result == nil || len(result.Scenes) == 0 {
			return drain()
		}
		for _, scene := range result.Scenes {
			// A provider that answers the same page twice would otherwise
			// duplicate its catalogue into the numbering; the id is the only
			// thing that says two results are the same scene.
			if scene.StashID == "" || seen[scene.StashID] {
				continue
			}
			seen[scene.StashID] = true
			if scene.Date.IsZero() {
				undated = append(undated, scene)
				continue
			}
			year := scene.Date.Year()
			if lowest == 0 || year < lowest {
				lowest = year
			}
			pending[year] = append(pending[year], scene)
		}
		if err := publish(true); err != nil {
			return err
		}
		if result.Total > 0 && len(seen) >= result.Total {
			return drain()
		}
		if len(result.Scenes) < result.PerPage {
			return drain()
		}
	}
	return drain()
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
			// season there is no year that starts unmonitored on its own
			// account. It follows the site, for the reason upsertSeriesTree's
			// rows follow their series: the wanted list reads the EPISODE flag,
			// so a site added unmonitored whose scenes landed monitored would
			// be hunted for exactly as if it had been added monitored.
			monitored = sr.Monitored
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
			monitored = sr.Monitored
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
// It is a no-op — not an error, and not one provider call — when no adult
// library is switched on or no stash-box credential is configured. That is the
// acceptance criterion this function exists to satisfy: a full job cycle on a
// server with adult content disabled must make ZERO requests to the stash-box
// endpoint, and a refresh sweep is the recurring job that would otherwise make
// them.
//
// A site under a library that is off is skipped individually, which is the
// narrow form of the same rule: the sweep names no library, so the gate above
// can only ask whether ANY adult shelf is on, and refreshing a dormant one's
// catalogue because a sibling is on would reach the endpoint on its behalf.
//
// Each site is refreshed against the instance it is PINNED to. A site whose
// instance has been deleted is an error on the result and zero provider calls:
// the alternative — falling back to whatever box the library's chain names
// first — would ask that box for a UUID it never minted, and public stash-boxes
// answer such a question with a different site rather than with "no".
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
	libs, err := m.store.ListLibraries(ctx)
	if err != nil {
		return err
	}
	owners := core.NewLibrarySet(libs)
	for _, sr := range sites {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !sr.Monitored || sr.StashID == "" {
			continue
		}
		if !owners.Active(sr.LibraryID) {
			continue
		}
		pinned := adultRef(core.ItemRef{Provider: sr.Provider, Ref: sr.StashID})
		provider := m.adultByID(ctx, pinned.Provider)
		if provider == nil {
			res.addErr("refresh site %q: no stash-box instance %q is configured", sr.Title, pinned.Provider)
			continue
		}
		meta, err := provider.GetSite(ctx, sr.StashID)
		if err != nil {
			res.addErr("refresh site %q: %v", sr.Title, err)
			continue
		}
		if meta == nil {
			continue
		}
		row, _, err := m.upsertSiteRow(ctx, pinned.Provider, meta, sr.Path, "", nil, sr.LibraryID)
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
		lib, err := m.seriesLibraryOf(ctx, sr)
		if err != nil {
			return "", 0, err
		}
		dir = adultSeriesDir(lib, sr.Title)
	}
	dst := path.Join(dir, m.seasonFolderName(p.Season),
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

	episodeIDs, err := m.ensureEpisodes(ctx, sr.ID, p.Season, p.Episodes, sr.Monitored)
	if err != nil {
		return "", 0, err
	}
	if err := m.linkImportedFile(ctx, rel, finalRel, size, p, episodeIDs); err != nil {
		return "", 0, err
	}
	return finalRel, sr.ID, nil
}

// resolveScene turns a scene parse into an ordinary episode parse by finding
// the episode row whose air date is the date the filename claims, or the
// unique scene one day away when the filename is off by a timezone split.
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
	var exact, nearby []*core.Episode
	for i := range episodes {
		days, ok := core.SceneDayDelta(episodes[i].AirDate, p.SceneDate)
		if !ok {
			continue
		}
		abs := days
		if abs < 0 {
			abs = -abs
		}
		switch {
		case abs == 0:
			exact = append(exact, &episodes[i])
		case abs <= core.SceneDateSlackDays:
			nearby = append(nearby, &episodes[i])
		}
	}
	if len(exact) > 1 {
		// Two scenes released the same day, and the filename carries
		// nothing that tells them apart. Guessing would import a scene as
		// the wrong one and then supersede the right one's file on the next
		// grab, so it parks for a human instead.
		return nil, p, reasonAmbiguousScene, nil
	}
	var match *core.Episode
	switch {
	case len(exact) == 1:
		match = exact[0]
	case len(nearby) == 1:
		// The filename is one day off the stored air date — the usual
		// timezone split — and no other scene claims that nearby day.
		match = nearby[0]
	case len(nearby) > 1:
		return nil, p, reasonAmbiguousScene, nil
	default:
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
	// The scenes this download actually landed, for the adult handoff. It is
	// collected here rather than derived by the caller because this is the only
	// place that knows a file resolved to a scene *and* which scene: one level
	// up, ImportDownload has a count and nothing else.
	var landed []int64
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
				// Even when the title cannot be imported automatically, keep its
				// stronger parse for Scan Review instead of the obfuscated payload name.
				if rp.Confidence > p.Confidence {
					p = rp
				}
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
		landed = append(landed, episode.ID)
	}
	// One notification per download, matching ImportDownload's rule for the
	// playback handoff: a download carrying six scenes owes Stash one scoped
	// scan, not six. A download that landed nothing changed no files, and
	// adultLibraryChanged treats an empty list as nothing to say.
	//
	// It is fired here, inside the adult branch, rather than beside
	// ImportDownload's libraryChanged call — that is what makes "a television
	// import never talks to Stash" a property of the routing rather than of a
	// condition somebody has to keep correct.
	m.adultLibraryChanged(ctx, landed)
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
func (m *Manager) matchAndImportScene(ctx context.Context, lib *core.Library, rel string, size int64, p core.ParsedRelease, res *ScanResult, park func(string)) (string, error) {
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
		sr, err = m.syncSiteFor(ctx, lib, sr, p.Title, rel, res, park)
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
// provider first when the library does not have it yet. rel is the scanned
// file that prompted the sync: a brand-new site is created in the adult
// library whose root holds it. It reports nil (having already parked) when
// the site cannot be resolved at all.
//
// IDENTIFICATION walks the library's chain, which is the half that differs from
// a refresh. Nothing is pinned yet — the filename names a site and no box —
// so every configured instance is a candidate, asked in the owner's own order,
// and the FIRST one confident about the title wins. The winner's id is what the
// new row is pinned to, and from then on it is the only box this site is ever
// asked about (see syncSiteScenes).
//
// A rung that errors is recorded and the walk goes on, exactly as the metadata
// chain's does: one box being down must not park a file the next box could
// place. Only a chain where every rung errored is a provider failure.
func (m *Manager) syncSiteFor(ctx context.Context, scanLib *core.Library, sr *core.Series, title, rel string, res *ScanResult, park func(string)) (*core.Series, error) {
	if sr == nil {
		chain := m.adultChain(ctx, scanLib)
		if len(chain) == 0 {
			park(reasonNoProvider)
			return nil, nil
		}
		var (
			hit    *core.SiteMeta
			winner string
			failed int
		)
		for _, rung := range chain {
			sites, err := rung.P.SearchSites(ctx, title)
			if err != nil {
				res.addErr("search sites for %q on %q: %v", title, rung.ID, err)
				failed++
				continue
			}
			cands := make([]candidate, len(sites))
			for i, site := range sites {
				cands[i] = candidate{title: site.Name}
			}
			idx := bestMatch(cands, title, 0)
			if idx < 0 {
				continue
			}
			hit, winner = &sites[idx], rung.ID
			break
		}
		if hit == nil {
			if failed == len(chain) {
				park(reasonProviderErr)
			} else {
				park(reasonNoMatch)
			}
			return nil, nil
		}
		ref := core.ItemRef{Provider: winner, Ref: hit.StashID}
		lib, err := m.siteLibrary(ctx, ref, rel, 0)
		if err != nil {
			return nil, err
		}
		sr, _, err = m.upsertSiteRow(ctx, winner, hit, adultSeriesDir(lib, hit.Name), "", nil, lib.ID)
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
