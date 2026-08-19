package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/wanted"
)

// The recurring cadences live in internal/store (RecurringIntervalFor), not
// here: the Tasks screen reports the same numbers this scheduler runs on, and
// a second copy of them is a screen that lies.
const (
	searchTimeout       = 30 * time.Second
	engineWaitTimeout   = 5 * time.Second
	highTitleConfidence = 0.9
)

type movieSearcher interface {
	SearchMovie(ctx context.Context, q string, cats []int) ([]core.Release, error)
}

type tvSearcher interface {
	SearchTV(ctx context.Context, q string, season, episode int, cats []int) ([]core.Release, error)
}

func (r *Runner) handleRSSSync(ctx context.Context, st *store.Store, payload json.RawMessage) error {
	if err := emptyPayload(payload); err != nil {
		return err
	}
	lists, err := wanted.Compute(ctx, st)
	if err != nil {
		return fmt.Errorf("compute wanted releases: %w", err)
	}
	feeds, err := rssFeeds(ctx, st)
	if err != nil {
		return err
	}
	if r.indexers == nil {
		return fmt.Errorf("no indexer client configured")
	}

	for _, feed := range feeds {
		searchCtx, cancel := context.WithTimeout(ctx, searchTimeout)
		releases, searchErr := r.indexers(feed.cfg).Search(searchCtx, "", feed.cfg.Categories)
		cancel()
		if searchErr != nil {
			continue
		}
		for _, release := range releases {
			release.IndexerID = feed.cfg.ID
			release.Indexer = feed.cfg.Name
			if err := st.UpsertRelease(ctx, &release); err != nil {
				return fmt.Errorf("store: cache rss release: %w", err)
			}
			if err := r.matchRSSRelease(ctx, st, release, lists, feed.libsFor(release)); err != nil {
				return err
			}
		}
	}
	return r.scheduleRecurring(ctx, core.JobRSSSync)
}

// rssFeed is one indexer's share of an RSS cycle: a single fetch carrying the
// union of every enabling library's categories, and the libraries that fetch is
// allowed to satisfy.
type rssFeed struct {
	cfg core.IndexerConfig
	// subscribers are the libraries that enabled this indexer, each with the
	// categories it asked this indexer for. The fetch is shared; the decision
	// is not.
	subscribers []rssSubscriber
	// unfiltered records that some library asks this indexer for everything,
	// which is a superset of any category list another library asked for.
	unfiltered bool
	categories map[int]bool
}

// rssSubscriber is one library's subscription to a shared feed.
type rssSubscriber struct {
	// libraryID is the subscribing library's row id — the id, not its kind,
	// because two libraries of one kind may subscribe with different
	// categories and the decision below is per library.
	libraryID int64
	// categories are the categories this library asked this indexer for, empty
	// when it asked unfiltered.
	categories []int
}

// libsFor is the per-library half of a shared fetch: the libraries that may
// act on this release.
//
// The union that made one fetch out of many is exactly what makes this
// necessary. A library that narrowed an indexer to its own categories still
// receives every other library's categories in the shared response, and
// offering those to its wanted items would grab releases the interactive and
// backlog searches for the same item would never have seen (PLAN phase 8 task
// 5).
func (f rssFeed) libsFor(release core.Release) map[int64]bool {
	libs := map[int64]bool{}
	for _, sub := range f.subscribers {
		if release.InCategories(sub.categories) {
			libs[sub.libraryID] = true
		}
	}
	return libs
}

// rssFeeds groups the libraries' resolved indexer sets by indexer.
//
// The grouping is the point: a feed is a firehose of everything new, so asking
// the same indexer once per library would fetch the same document twice, and
// indexers rate-limit. One fetch per indexer per cycle answers for every
// library that enabled it, and the per-library decision moves to matching
// (PLAN phase 8 task 5).
//
// A library that is switched off contributes nothing. Deactivating deliberately
// does not delete the row (store.SetLibraryActive), so without this a dormant
// library's categories stay in the union forever and every RSS poll keeps
// asking each indexer for them, once per sync interval, on an install whose
// owner turned that shelf off. Nothing would be grabbed — wanted.Compute drops
// an inactive library's items — but the request itself is a durable trace of a
// shelf that is meant to be absent, visible in the indexer's own request log,
// and it is a wider fetch than the active libraries asked for. Scan skips
// inactive roots and refreshSites no-ops for the same reason.
func rssFeeds(ctx context.Context, st *store.Store) ([]rssFeed, error) {
	libraries, err := st.ListLibraries(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: list libraries: %w", err)
	}

	feeds := []*rssFeed{}
	byIndexer := map[int64]*rssFeed{}
	for _, library := range libraries {
		if !library.Active {
			continue
		}
		settings, err := st.ResolveLibrarySettings(ctx, library.ID)
		if err != nil {
			return nil, fmt.Errorf("store: resolve settings of library %d: %w", library.ID, err)
		}
		for _, cfg := range settings.Indexers {
			if !cfg.Searchable() {
				continue
			}
			feed, ok := byIndexer[cfg.ID]
			if !ok {
				feed = &rssFeed{cfg: cfg, categories: map[int]bool{}}
				byIndexer[cfg.ID] = feed
				feeds = append(feeds, feed)
			}
			feed.subscribers = append(feed.subscribers, rssSubscriber{
				libraryID: library.ID, categories: cfg.Categories,
			})
			if len(cfg.Categories) == 0 {
				feed.unfiltered = true
				continue
			}
			for _, category := range cfg.Categories {
				feed.categories[category] = true
			}
		}
	}

	out := make([]rssFeed, 0, len(feeds))
	for _, feed := range feeds {
		// The client's own config is what it falls back to when the cat list is
		// empty, so the union is written to both and the two can never disagree.
		feed.cfg.Categories = unionCategories(feed)
		out = append(out, *feed)
	}
	return out, nil
}

// unionCategories renders the merged category filter. A library that asked for
// everything wins outright: narrowing the fetch to another library's categories
// would drop releases that library is entitled to see.
func unionCategories(feed *rssFeed) []int {
	if feed.unfiltered {
		return nil
	}
	cats := make([]int, 0, len(feed.categories))
	for category := range feed.categories {
		cats = append(cats, category)
	}
	sort.Ints(cats)
	return cats
}

// matchRSSRelease offers one release to the wanted items of every LIBRARY the
// feed it came from answers for with this release. A library that disabled
// the indexer, or that narrowed it to categories this release is not in, is
// not among them — so its items never see the release even though the fetch
// was shared. Every item names its library outright since migration 0011, so
// the subscription is matched on that id alone.
func (r *Runner) matchRSSRelease(ctx context.Context, st *store.Store, release core.Release, lists *wanted.Lists, libs map[int64]bool) error {
	for _, target := range lists.Movies {
		if !libs[target.Movie.LibraryID] || !matchesMovie(release, target.Movie) {
			continue
		}
		profile, err := st.ResolveItemQualityProfileByLibrary(ctx, target.Movie.LibraryID, core.LibraryKindMovie, target.QualityProfileID)
		if err != nil {
			return fmt.Errorf("store: resolve movie profile: %w", err)
		}
		score, reject := wanted.ScoreRelease(release, profile)
		if reject != "" || (target.Reason == wanted.ReasonBelowCutoff && !wanted.IsUpgrade(release.Parsed.Quality, target.FileQuality)) {
			continue
		}
		if err := r.grabMovie(ctx, st, target.Movie, release, score, "automatic rss"); err != nil {
			return err
		}
	}
	scenes := []wanted.Episode{}
	for _, target := range lists.Episodes {
		// The library an episode belongs to is its series' library, so a scene
		// is only offered releases from a feed its OWN adult library
		// subscribed to with categories this release is in. Another library
		// that shares the indexer — of the other kind or of the same one —
		// sees the same fetch and never these releases.
		if !libs[target.SeriesLibraryID] {
			continue
		}
		if target.SeriesKind == core.SeriesKindAdult {
			scenes = append(scenes, target)
			continue
		}
		if !matchesRSSEpisode(release, target) {
			continue
		}
		if err := r.grabRSSEpisode(ctx, st, target, release); err != nil {
			return err
		}
	}
	if target := bestRSSScene(release, scenes); target != nil {
		return r.grabRSSEpisode(ctx, st, *target, release)
	}
	return nil
}

func (r *Runner) grabRSSEpisode(ctx context.Context, st *store.Store, target wanted.Episode, release core.Release) error {
	kind := core.LibraryKindForSeries(target.SeriesKind)
	series, err := st.GetSeries(ctx, target.SeriesID)
	if err != nil {
		return fmt.Errorf("store: get series for rss episode: %w", err)
	}
	profile, err := st.ResolveItemQualityProfileByLibrary(ctx, series.LibraryID, kind, series.QualityProfileID)
	if err != nil {
		return fmt.Errorf("store: resolve episode profile: %w", err)
	}
	score, reject := wanted.ScoreRelease(release, profile)
	if reject != "" || (target.Reason == wanted.ReasonBelowCutoff && !wanted.IsUpgrade(release.Parsed.Quality, target.FileQuality)) {
		return nil
	}
	return r.grabEpisode(ctx, st, series.LibraryID, kind, target.Episode, release, score, "automatic rss")
}

func (r *Runner) handleBacklogSweep(ctx context.Context, st *store.Store, payload json.RawMessage) error {
	if err := emptyPayload(payload); err != nil {
		return err
	}
	lists, err := wanted.ComputeSearchable(ctx, st)
	if err != nil {
		return fmt.Errorf("compute wanted releases: %w", err)
	}
	for _, movie := range lists.Movies {
		payload, err := json.Marshal(core.JobSearchMoviePayload{MovieID: movie.ID})
		if err != nil {
			return fmt.Errorf("encode movie search payload: %w", err)
		}
		if err := enqueueIfMissing(ctx, st, core.JobSearchMovie, string(payload)); err != nil {
			return err
		}
	}
	for _, episode := range lists.Episodes {
		payload, err := json.Marshal(core.JobSearchEpisodePayload{EpisodeID: episode.ID})
		if err != nil {
			return fmt.Errorf("encode episode search payload: %w", err)
		}
		if err := enqueueIfMissing(ctx, st, core.JobSearchEpisode, string(payload)); err != nil {
			return err
		}
	}
	return r.scheduleRecurring(ctx, core.JobBacklogSweep)
}

func (r *Runner) handleSearchMovie(ctx context.Context, st *store.Store, payload json.RawMessage) error {
	var input core.JobSearchMoviePayload
	if err := json.Unmarshal(payload, &input); err != nil || input.MovieID <= 0 {
		return fmt.Errorf("decode search_movie payload")
	}
	movie, err := st.GetMovie(ctx, input.MovieID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: get movie: %w", err)
	}
	missing, err := movieMissing(ctx, st, movie.ID)
	if err != nil {
		return err
	}
	if !missing {
		return nil
	}
	// handleSearchEpisode's rule, for the other item kind: a job outlives the
	// switch, so the library is re-read rather than trusted from when the job
	// was queued.
	libraries, err := st.ListLibraries(ctx)
	if err != nil {
		return fmt.Errorf("store: list libraries: %w", err)
	}
	if !core.NewLibrarySet(libraries).Active(movie.LibraryID) {
		return nil
	}
	profile, err := st.ResolveItemQualityProfileByLibrary(ctx, movie.LibraryID, core.LibraryKindMovie, movie.QualityProfileID)
	if err != nil {
		return fmt.Errorf("store: resolve movie profile: %w", err)
	}
	if _, active, err := st.ActiveGrabForMovie(ctx, movie.ID); err != nil {
		return fmt.Errorf("store: find active movie grab: %w", err)
	} else if active {
		return nil
	}

	query := movie.Title
	if movie.Year > 0 {
		query = fmt.Sprintf("%s %d", movie.Title, movie.Year)
	}
	candidates, err := r.searchMovies(ctx, st, movie.LibraryID, query)
	if err != nil {
		return err
	}
	best, rejected := wanted.SelectBest(candidates, profile)
	if err := recordRejections(ctx, st, rejected, profile, core.GrabInfo{MovieID: movie.ID}); err != nil {
		return err
	}
	if best == nil {
		return recordNoRelease(ctx, st, movie.Title, len(candidates), movie.ID, 0)
	}
	score, _ := wanted.ScoreRelease(*best, profile)
	return r.grabSearchedMovie(ctx, st, *movie, *best, score)
}

func (r *Runner) handleSearchEpisode(ctx context.Context, st *store.Store, payload json.RawMessage) error {
	var input core.JobSearchEpisodePayload
	if err := json.Unmarshal(payload, &input); err != nil || input.EpisodeID <= 0 {
		return fmt.Errorf("decode search_episode payload")
	}
	episode, err := st.GetEpisode(ctx, input.EpisodeID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: get episode: %w", err)
	}
	missing, err := episodeMissing(ctx, st, episode.ID)
	if err != nil {
		return err
	}
	if !missing {
		return nil
	}
	series, err := st.GetSeries(ctx, episode.SeriesID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: get series: %w", err)
	}
	kind := core.LibraryKindForSeries(series.Kind)
	// A job queued before the series' library was switched off is the one path
	// that can reach an indexer on a dormant library's behalf, so the switch is
	// re-read here rather than trusted from when the job was made. Dropping the
	// job is the right answer: the item is not wanted any more (wanted.Compute
	// agrees), so there is nothing to retry.
	libraries, err := st.ListLibraries(ctx)
	if err != nil {
		return fmt.Errorf("store: list libraries: %w", err)
	}
	if !core.NewLibrarySet(libraries).Active(series.LibraryID) {
		return nil
	}
	profile, err := st.ResolveItemQualityProfileByLibrary(ctx, series.LibraryID, kind, series.QualityProfileID)
	if err != nil {
		return fmt.Errorf("store: resolve episode profile: %w", err)
	}
	if _, active, err := st.ActiveGrabForEpisode(ctx, episode.ID); err != nil {
		return fmt.Errorf("store: find active episode grab: %w", err)
	} else if active {
		return nil
	}

	if series.Kind == core.SeriesKindAdult {
		return r.searchScene(ctx, st, *series, *episode, profile)
	}

	candidates, err := r.searchEpisodes(ctx, st, series.LibraryID, series.Title, episode.SeasonNumber, episode.EpisodeNumber)
	if err != nil {
		return err
	}
	automatic := make([]core.Release, 0, len(candidates))
	seasonPacks := []wanted.Decision{}
	for _, candidate := range candidates {
		if candidate.Parsed.Season != episode.SeasonNumber {
			continue
		}
		if len(candidate.Parsed.Episodes) == 0 {
			// A pack can match the season, but automatic episode grabs must
			// remain one-episode downloads until season-pack importing owns the
			// complete multi-file contract.
			seasonPacks = append(seasonPacks, wanted.Decision{
				Release: candidate,
				Reject:  "season packs require interactive selection",
			})
			continue
		}
		if !contains(candidate.Parsed.Episodes, episode.EpisodeNumber) {
			continue
		}
		automatic = append(automatic, candidate)
	}
	best, rejected := wanted.SelectBest(automatic, profile)
	rejected = append(rejected, seasonPacks...)
	if err := recordRejections(ctx, st, rejected, profile, core.GrabInfo{
		SeriesID: series.ID, SeasonNum: episode.SeasonNumber, EpisodeIDs: []int64{episode.ID},
	}); err != nil {
		return err
	}
	if best == nil {
		return recordNoRelease(ctx, st, episode.Title, len(automatic), 0, series.ID)
	}
	score, _ := wanted.ScoreRelease(*best, profile)
	return r.grabSearchedEpisode(ctx, st, series.LibraryID, kind, *episode, *best, score)
}

// searchScene is handleSearchEpisode's adult branch (PLAN phase 9 task 3).
//
// Two things differ from a television search and nothing else does. The queries
// are built from the site and the scene rather than from a SxxEyy, because a
// scene has no season/episode an indexer could filter on — Caravan's own are a
// mapping (release year, sequence within that year) that no indexer has heard
// of. And the candidate filter matches on the release DATE, or on a much
// stricter title test for the fallback query. The scoring, the rejection record
// and the grab are the shared ones.
//
// The exact date is asked first. When it yields nothing grabbable, the day
// before and the day after follow — a timezone split names the file with
// those digits, and an exact-date query will not see it. "Site Scene Title"
// is last: the releases named after their title or their performers, which
// no date query can see. That last query is where Whisparr stops (its issue
// #115 asks for exactly this), and it is only safe because what comes back
// is held to matchesSceneTitle rather than to the date.
//
// The fan-out itself is the adult library's: searchIndexers resolves that
// library's indexers and the categories it asked each of them for, so a scene
// search sends 6000-series categories and nothing else (PLAN phase 8 task 4).
func (r *Runner) searchScene(ctx context.Context, st *store.Store, series core.Series, episode core.Episode, profile *core.QualityProfile) error {
	searches := core.NearbySceneSearches(series.Title, episode.AirDate, episode.Title)
	if len(searches) == 0 {
		// No date and no title is no query to make and no candidate to
		// recognize. Nothing is wrong; there is simply nothing to search for.
		return nil
	}

	siblings, err := siblingSceneDates(ctx, st, series.ID, episode.ID)
	if err != nil {
		return err
	}

	info := core.GrabInfo{
		SeriesID: series.ID, SeasonNum: episode.SeasonNumber, EpisodeIDs: []int64{episode.ID},
	}
	// Deduped across variants within each configured indexer: GUIDs are
	// provider-local, so the same value from two indexers remains two candidates.
	// A release returned by both query variants is still one candidate and, when
	// it loses, one rejection record rather than two.
	seen := map[string]bool{}
	tried := make([]string, 0, 2)
	triedSeen := map[string]bool{}
	matched := 0

	for _, search := range searches {
		candidates, err := r.searchIndexers(ctx, st, series.LibraryID, core.LibraryKindAdult,
			func(ctx context.Context, client api.IndexerClient, cfg core.IndexerConfig) ([]core.Release, error) {
				return client.Search(ctx, search.Query, cfg.Categories)
			})
		if err != nil {
			return err
		}
		if variant := string(search.Variant); !triedSeen[variant] {
			triedSeen[variant] = true
			tried = append(tried, variant)
		}

		automatic := make([]core.Release, 0, len(candidates))
		for _, candidate := range candidates {
			key := fmt.Sprintf("%d\x00%s", candidate.IndexerID, candidate.GUID)
			if candidate.GUID != "" && seen[key] {
				continue
			}
			if !matchesScene(candidate, search.Variant, series, episode, siblings) {
				continue
			}
			if candidate.GUID != "" {
				seen[key] = true
			}
			automatic = append(automatic, candidate)
		}
		matched += len(automatic)

		best, rejected := selectSceneRelease(automatic, episode.AirDate, profile)
		if err := recordRejections(ctx, st, rejected, profile, info); err != nil {
			return err
		}
		if best != nil {
			score, _ := wanted.ScoreRelease(*best, profile)
			return r.grabSearchedEpisode(ctx, st, series.LibraryID, core.LibraryKindAdult, episode, *best, score)
		}
	}
	return recordNoRelease(ctx, st, episode.Title, matched, 0, series.ID, tried...)
}

// matchesScene reports whether a candidate is the scene that was searched for.
//
// The date query's answers are matched on the date and nothing else: a scene
// release named the standard way carries it. The day may be one off when no
// sibling scene owns that nearby day (timezone split). The title query's
// answers cannot rely on the date — a release named after its title has none
// to compare — so they go through matchesSceneTitle, which is strict on
// purpose.
func matchesScene(release core.Release, variant core.SceneSearchVariant, series core.Series, episode core.Episode, siblings []time.Time) bool {
	if variant == core.SceneSearchByTitle {
		return matchesSceneTitle(release, series, episode, siblings)
	}
	return sceneDateFits(release.Parsed.SceneDate, episode.AirDate, siblings)
}

// matchesSceneTitle is the conservative test a title-named release has to pass.
//
// The rule it is written to: a false grab is worse than a miss. A wrong scene
// downloaded under a right scene's name is a file somebody has to find and
// delete, and the library will believe it is complete; a miss just leaves the
// scene wanted, and the interactive picker is one click away.
//
// So all of the following must hold:
//
//   - The release does not CONTRADICT the date. A name that carries a scene
//     date is a date-named release, and if that date is not the scene's then
//     the release is a different scene however well its words line up.
//   - The site's name appears in the release name. Compared with the
//     separators removed, because release names weld words together
//     ("RealityKings.Deep.Impact"), which no token comparison would match.
//   - The scene's title appears the same way, and is substantial enough to
//     mean something: two words or more, or one long word plus a performer
//     also named in the release. A one-word title is the case that would
//     otherwise match half a site's catalogue.
//
// The known miss is a sub-studio whose releases are named after the network
// above it ("Brazzers.…" for a Brazzers Exxtra scene). Loosening the site test
// to a partial match would take it, and would also take every other scene the
// network released that day, so it stays strict.
func matchesSceneTitle(release core.Release, series core.Series, episode core.Episode, siblings []time.Time) bool {
	if !release.Parsed.SceneDate.IsZero() && !sceneDateFits(release.Parsed.SceneDate, episode.AirDate, siblings) {
		return false
	}

	name := compactName(release.Title)
	if name == "" {
		return false
	}
	site := compactName(series.Title)
	if site == "" || !strings.Contains(name, site) {
		return false
	}

	title := compactName(episode.Title)
	if title == "" || !strings.Contains(name, title) {
		return false
	}
	if len(significantWords(episode.Title)) >= minSceneTitleWords {
		return true
	}
	// One word carries too little on its own: "Impact" matches anything with
	// that word in it. A performer named in the release is the second signal
	// that makes it a scene rather than a coincidence.
	return len(title) >= minSceneTitleRunes && hasScenePerformer(name, episode)
}

// The thresholds matchesSceneTitle uses. Both are deliberately blunt: they are
// there to refuse a match, not to grade one.
const (
	// minSceneTitleWords is how many words a title needs before it can carry a
	// match on its own.
	minSceneTitleWords = 2
	// minSceneTitleRunes is how long a single-word title must be to be worth
	// testing at all, even with a performer beside it.
	minSceneTitleRunes = 5
)

// hasScenePerformer reports whether any performer credited on the scene is
// named in the release, compared the same welded way the title is.
func hasScenePerformer(compactRelease string, episode core.Episode) bool {
	if episode.Scene == nil {
		return false
	}
	for _, performer := range episode.Scene.Performers {
		name := compactName(performer)
		// A one-name credit ("Anna") is too short to be evidence of anything.
		if len(name) < minPerformerRunes {
			continue
		}
		if strings.Contains(compactRelease, name) {
			return true
		}
	}
	return false
}

// minPerformerRunes is the shortest performer name worth testing. A stage name
// of four letters or fewer appears inside too many other words.
const minPerformerRunes = 6

// compactName lowercases a name and drops everything that is not a letter or a
// digit, so "Reality Kings" and "RealityKings" and "Reality.Kings" are one
// string. It is the comparison release names force: they weld words together
// as often as they separate them.
func compactName(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// significantWords are a title's words with the small connecting ones dropped:
// release names leave them out as often as they keep them, so they say nothing
// about whether two names are the same scene.
func significantWords(value string) []string {
	out := []string{}
	for _, word := range strings.Fields(normalizeTitle(value)) {
		if sceneTitleStopWords[word] {
			continue
		}
		out = append(out, word)
	}
	return out
}

var sceneTitleStopWords = map[string]bool{
	"a": true, "an": true, "the": true, "and": true, "or": true, "of": true,
	"in": true, "on": true, "at": true, "to": true, "for": true, "with": true,
	"my": true, "your": true, "his": true, "her": true, "part": true, "vol": true,
}

// sceneDateFits reports whether a parsed release date is this scene's date.
//
// The exact stored day always fits. A date one day away also fits when no
// other scene from the same site owns that day: that is the timezone split
// (studio local time vs a UTC calendar day) that would otherwise leave a
// monitored scene wanted forever. A sibling on the adjacent day means the
// release is that other scene, so it is refused.
func sceneDateFits(parsed, air time.Time, siblings []time.Time) bool {
	days, ok := core.SceneDayDelta(parsed, air)
	if !ok {
		return false
	}
	if days == 0 {
		return true
	}
	if days < 0 {
		days = -days
	}
	if days > core.SceneDateSlackDays {
		return false
	}
	for _, sib := range siblings {
		if core.SameSceneDay(parsed, sib) {
			return false
		}
	}
	return true
}

// selectSceneRelease prefers a release named with the scene's exact day.
// Nearby-day candidates are only considered when nothing on the exact day
// came back: falling through would grab yesterday's 2160p instead of today's
// below-cutoff original.
func selectSceneRelease(automatic []core.Release, air time.Time, profile *core.QualityProfile) (*core.Release, []wanted.Decision) {
	exact := make([]core.Release, 0, len(automatic))
	for _, rel := range automatic {
		if core.SameSceneDay(rel.Parsed.SceneDate, air) {
			exact = append(exact, rel)
		}
	}
	if len(exact) > 0 {
		return wanted.SelectBest(exact, profile)
	}
	return wanted.SelectBest(automatic, profile)
}

func siblingSceneDates(ctx context.Context, st *store.Store, seriesID, skipID int64) ([]time.Time, error) {
	episodes, err := st.ListEpisodes(ctx, seriesID)
	if err != nil {
		return nil, fmt.Errorf("store: list scenes of series %d: %w", seriesID, err)
	}
	out := make([]time.Time, 0, len(episodes))
	for _, ep := range episodes {
		if ep.ID == skipID || ep.AirDate.IsZero() {
			continue
		}
		out = append(out, ep.AirDate)
	}
	return out, nil
}

func (r *Runner) searchMovies(ctx context.Context, st *store.Store, libraryID int64, query string) ([]core.Release, error) {
	return r.searchIndexers(ctx, st, libraryID, core.LibraryKindMovie, func(ctx context.Context, client api.IndexerClient, cfg core.IndexerConfig) ([]core.Release, error) {
		searcher, ok := client.(movieSearcher)
		if !ok {
			return nil, fmt.Errorf("indexer %q does not support movie search", cfg.Name)
		}
		return searcher.SearchMovie(ctx, query, cfg.Categories)
	})
}

func (r *Runner) searchEpisodes(ctx context.Context, st *store.Store, libraryID int64, title string, season, episode int) ([]core.Release, error) {
	return r.searchIndexers(ctx, st, libraryID, core.LibraryKindTV, func(ctx context.Context, client api.IndexerClient, cfg core.IndexerConfig) ([]core.Release, error) {
		searcher, ok := client.(tvSearcher)
		if !ok {
			return nil, fmt.Errorf("indexer %q does not support tv search", cfg.Name)
		}
		return searcher.SearchTV(ctx, title, season, episode, cfg.Categories)
	})
}

type indexerSearch func(context.Context, api.IndexerClient, core.IndexerConfig) ([]core.Release, error)

type indexerResult struct {
	index    int
	cfg      core.IndexerConfig
	releases []core.Release
}

// searchIndexers fans one search out over the indexers the ITEM'S library
// searches, each already carrying the categories that search must send (PLAN
// phase 8 task 4). libraryID 0 resolves through the kind's default library,
// which is the fallback for a library that has vanished — every item names one.
func (r *Runner) searchIndexers(ctx context.Context, st *store.Store, libraryID int64, kind string, search indexerSearch) ([]core.Release, error) {
	settings, err := st.ResolveLibrarySettingsForItem(ctx, libraryID, kind)
	if err != nil {
		return nil, fmt.Errorf("store: resolve %s library settings: %w", kind, err)
	}
	indexers := settings.Indexers
	if r.indexers == nil {
		return nil, fmt.Errorf("no indexer client configured")
	}

	results := make(chan indexerResult, len(indexers))
	launched := 0
	for index, cfg := range indexers {
		if !cfg.Searchable() {
			continue
		}
		launched++
		go func(index int, cfg core.IndexerConfig) {
			searchCtx, cancel := context.WithTimeout(ctx, searchTimeout)
			releases, err := search(searchCtx, r.indexers(cfg), cfg)
			cancel()
			if err == nil {
				_ = st.RecordIndexerHealth(ctx, cfg.ID, nil)
				results <- indexerResult{index: index, cfg: cfg, releases: releases}
				return
			}
			_ = st.RecordIndexerHealth(ctx, cfg.ID, err)
			results <- indexerResult{index: index, cfg: cfg}
		}(index, cfg)
	}

	indexerResults := make([]indexerResult, len(indexers))
	for range launched {
		result := <-results
		indexerResults[result.index] = result
	}

	candidates := []core.Release{}
	for _, result := range indexerResults {
		for _, release := range result.releases {
			release.IndexerID = result.cfg.ID
			release.Indexer = result.cfg.Name
			if err := st.UpsertRelease(ctx, &release); err != nil {
				return nil, fmt.Errorf("store: cache search release: %w", err)
			}
			candidates = append(candidates, release)
		}
	}
	return candidates, nil
}

func movieMissing(ctx context.Context, st *store.Store, movieID int64) (bool, error) {
	files, err := st.ListMediaFilesForMovie(ctx, movieID)
	if err != nil {
		return false, fmt.Errorf("store: list movie files: %w", err)
	}
	return len(files) == 0, nil
}

func episodeMissing(ctx context.Context, st *store.Store, episodeID int64) (bool, error) {
	files, err := st.ListMediaFilesForEpisode(ctx, episodeID)
	if err != nil {
		return false, fmt.Errorf("store: list episode files: %w", err)
	}
	return len(files) == 0, nil
}

// grabSearchedMovie checks again after the indexer request. An import can
// finish while that request is running, and the completed file wins that race.
func (r *Runner) grabSearchedMovie(ctx context.Context, st *store.Store, movie core.Movie, release core.Release, score int) error {
	dropped, err := searchJobDropped(ctx, st, core.JobSearchMovie, core.JobSearchMoviePayload{MovieID: movie.ID})
	if err != nil {
		return err
	}
	if dropped {
		return nil
	}
	missing, err := movieMissing(ctx, st, movie.ID)
	if err != nil {
		return err
	}
	if !missing {
		return nil
	}
	return r.grabMovie(ctx, st, movie, release, score, "automatic search")
}

// grabSearchedEpisode is the episode and adult scene form of
// grabSearchedMovie.
func (r *Runner) grabSearchedEpisode(ctx context.Context, st *store.Store, libraryID int64, kind string, episode core.Episode, release core.Release, score int) error {
	dropped, err := searchJobDropped(ctx, st, core.JobSearchEpisode, core.JobSearchEpisodePayload{EpisodeID: episode.ID})
	if err != nil {
		return err
	}
	if dropped {
		return nil
	}
	missing, err := episodeMissing(ctx, st, episode.ID)
	if err != nil {
		return err
	}
	if !missing {
		return nil
	}
	return r.grabEpisode(ctx, st, libraryID, kind, episode, release, score, "automatic search")
}

// searchJobDropped reports that an automatic search was cancelled (or already
// finished) while the indexer request was in flight. The grab must not start.
func searchJobDropped(ctx context.Context, st *store.Store, kind string, payload any) (bool, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("encode %s payload: %w", kind, err)
	}
	cancelled, err := st.HasJobInState(ctx, kind, string(encoded), core.JobStateCancelled)
	if err != nil {
		return false, err
	}
	return cancelled, nil
}

func (r *Runner) grabMovie(ctx context.Context, st *store.Store, movie core.Movie, release core.Release, score int, source string) error {
	if _, active, err := st.ActiveGrabForMovie(ctx, movie.ID); err != nil {
		return fmt.Errorf("store: find active movie grab: %w", err)
	} else if active {
		return nil
	}
	// The shelf the film sits on decides the download route and the client-side
	// label, exactly as grabEpisode's does — and a film is the case where the
	// two can differ, because a movie row carries no kind of its own. An anime
	// library's film sorts under "anime" beside that library's episodes rather
	// than into the Movies library's download folder.
	//
	// The movie kind is the one library kind whose label is not its own name:
	// "movies" is the folder every download client has always sorted, so it is
	// spelled out where every other kind is already its own label.
	kind := core.LibraryKindMovie
	if lib, err := st.GetLibrary(ctx, movie.LibraryID); err == nil {
		kind = lib.Kind
	}
	category := "movies"
	if kind != core.LibraryKindMovie {
		category = kind
	}
	return r.grab(ctx, st, movie.LibraryID, kind, release, score, source, core.GrabInfo{MovieID: movie.ID}, core.AddOpts{
		Category: category, MovieID: movie.ID,
	})
}

// grabEpisode hands a release to the engine the episode's own library routes
// to. libraryID is the series' library and kind is one of the
// core.LibraryKind* constants — the television kind for a television episode,
// the adult kind for a scene — deciding the download route and the
// client-side category, so a scene never lands in the television library's
// download folder.
func (r *Runner) grabEpisode(ctx context.Context, st *store.Store, libraryID int64, kind string, episode core.Episode, release core.Release, score int, source string) error {
	if _, active, err := st.ActiveGrabForEpisode(ctx, episode.ID); err != nil {
		return fmt.Errorf("store: find active episode grab: %w", err)
	} else if active {
		return nil
	}
	info := core.GrabInfo{SeriesID: episode.SeriesID, SeasonNum: episode.SeasonNumber, EpisodeIDs: []int64{episode.ID}}
	return r.grab(ctx, st, libraryID, kind, release, score, source, info, core.AddOpts{
		Category: kind, SeriesID: episode.SeriesID, SeasonNum: episode.SeasonNumber, EpisodeIDs: []int64{episode.ID},
	})
}

// grab hands one release to the engine the item's library routes its
// downloads to, and records the attempt either way.
func (r *Runner) grab(ctx context.Context, st *store.Store, libraryID int64, kind string, release core.Release, score int, source string, info core.GrabInfo, opts core.AddOpts) error {
	if r.engine == nil {
		return fmt.Errorf("download engine unavailable")
	}
	engineCtx, cancel := context.WithTimeout(ctx, engineWaitTimeout)
	defer cancel()
	engine := r.engine(engineCtx, libraryID, kind)
	if engine == nil {
		return fmt.Errorf("download engine unavailable")
	}
	info.ReleaseTitle = release.Title
	grab := &core.Grab{
		GrabInfo:  info,
		ReleaseID: release.ID,
		Reason:    fmt.Sprintf("%s: score %d", source, score),
		Status:    core.GrabStatusGrabbed,
	}
	if err := st.InsertGrab(ctx, grab); err != nil {
		return fmt.Errorf("store: record grab: %w", err)
	}
	if err := r.resolveReleaseDownload(ctx, st, &release); err != nil {
		if statusErr := st.SetGrabStatus(ctx, grab.GrabID, core.GrabStatusFailed, err.Error()); statusErr != nil {
			return fmt.Errorf("resolve indexer download: %w (store: mark failed grab: %v)", err, statusErr)
		}
		return fmt.Errorf("resolve indexer download: %w", err)
	}
	downloadID, err := engine.Add(ctx, release, opts)
	if err != nil {
		// The automatic path routes by protocol exactly like the interactive
		// one (PLAN phase 6 task 3), so it meets the same wall: a usenet
		// release with no usenet client configured. That is a recorded
		// rejection, not a job failure — retrying it every sweep would never
		// succeed and would bury the real reason under transport errors — so
		// the reason is written to the grab and the job completes.
		if errors.Is(err, download.ErrNoEngine) {
			if statusErr := st.SetGrabStatus(ctx, grab.GrabID, core.GrabStatusRejected, err.Error()); statusErr != nil {
				return fmt.Errorf("store: mark rejected grab: %w", statusErr)
			}
			return st.InsertEvent(ctx, &core.Event{
				Level:    core.EventLevelWarn,
				Category: "grab",
				Message:  "Cannot grab " + release.Title,
				Detail:   err.Error(),
				MovieID:  info.MovieID,
				SeriesID: info.SeriesID,
			})
		}
		if statusErr := st.SetGrabStatus(ctx, grab.GrabID, core.GrabStatusFailed, err.Error()); statusErr != nil {
			return fmt.Errorf("engine: add download: %w (store: mark failed grab: %v)", err, statusErr)
		}
		return fmt.Errorf("engine: add download: %w", err)
	}
	// The interactive grab writes this row. Without it the engine's own
	// persistence lands first with grab_id 0, and the watcher treats the
	// finished download as unowned — it stays in incomplete and the item
	// stays wanted.
	if err := st.UpsertDownload(ctx, &core.Download{
		GrabID:   grab.GrabID,
		Engine:   engineNameFor(ctx, engine, release.Protocol),
		EngineID: downloadID,
		Title:    release.Title,
		State:    core.DownloadQueued,
		Size:     release.Size,
	}); err != nil {
		return fmt.Errorf("store: record download: %w", err)
	}
	if err := st.InsertEvent(ctx, &core.Event{
		Category: "grab",
		Message:  "Grabbed " + release.Title,
		Detail:   source + " from " + release.Indexer,
		MovieID:  info.MovieID,
		SeriesID: info.SeriesID,
	}); err != nil {
		return fmt.Errorf("store: record grab event: %w", err)
	}
	return nil
}

// engineNameFor is the backend name recorded on a download row. A routing
// engine is several backends behind one interface, so the row has to name
// the one that actually took the release — the same rule the interactive
// grab uses.
func engineNameFor(ctx context.Context, engine core.Engine, protocol string) string {
	if router, ok := engine.(core.EngineRouting); ok {
		if name := router.EngineNameFor(ctx, protocol); name != "" {
			return name
		}
	}
	return ""
}

func (r *Runner) resolveReleaseDownload(ctx context.Context, st *store.Store, release *core.Release) error {
	if release == nil || release.IndexerID <= 0 || r.indexers == nil {
		return nil
	}
	config, err := st.GetIndexer(ctx, release.IndexerID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return fmt.Errorf("load indexer configuration: %w", err)
	}
	resolver, ok := r.indexers(*config).(api.IndexerDownloadResolver)
	if !ok {
		return nil
	}
	resolved, err := resolver.ResolveDownload(ctx, release.DownloadURL)
	if err != nil {
		return err
	}
	if strings.TrimSpace(resolved) == "" {
		return fmt.Errorf("indexer returned an empty download URL")
	}
	release.DownloadURL = resolved
	lower := strings.ToLower(strings.TrimSpace(resolved))
	if strings.HasPrefix(lower, "magnet:") {
		return nil
	}
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return fmt.Errorf("indexer returned an unsupported download URL scheme")
	}
	payloadResolver, ok := resolver.(api.IndexerTorrentPayloadFetcher)
	if !ok {
		return fmt.Errorf("indexer cannot retrieve its authenticated download payload")
	}
	payload, err := payloadResolver.FetchDownload(ctx, resolved)
	if err != nil {
		return err
	}
	if len(payload) == 0 {
		return fmt.Errorf("indexer returned an empty download payload")
	}
	if len(payload) > core.MaxTorrentPayloadBytes {
		return fmt.Errorf("indexer download payload exceeds size limit")
	}
	release.TorrentPayload = append([]byte(nil), payload...)
	release.DownloadURL = ""
	return nil
}

func (r *Runner) handleIndexerHealth(ctx context.Context, st *store.Store, payload json.RawMessage) error {
	if err := emptyPayload(payload); err != nil {
		return err
	}
	if r.indexers == nil {
		return fmt.Errorf("no indexer client configured")
	}
	indexers, err := st.ListEnabledIndexers(ctx)
	if err != nil {
		return fmt.Errorf("store: list indexers: %w", err)
	}
	for _, cfg := range indexers {
		probe, cancel := context.WithTimeout(ctx, 10*time.Second)
		err := r.indexers(cfg).Test(probe)
		cancel()
		if recErr := st.RecordIndexerHealth(ctx, cfg.ID, err); recErr != nil {
			return fmt.Errorf("store: record indexer health: %w", recErr)
		}
	}
	return r.scheduleRecurring(ctx, core.JobIndexerHealth)
}

func (r *Runner) scheduleRecurring(ctx context.Context, kind string) error {
	interval, ok := store.RecurringIntervalFor(kind)
	if !ok {
		return fmt.Errorf("unsupported recurring job kind %q", kind)
	}
	minutes := r.st.IntervalMinutes(ctx, interval.Key, interval.DefaultMinutes)
	open, err := r.st.HasOpenJob(ctx, kind, "{}")
	if err != nil {
		return fmt.Errorf("store: check open %s job: %w", kind, err)
	}
	if open {
		return nil
	}
	if err := r.st.EnqueueJob(ctx, &core.Job{
		Kind: kind, Payload: "{}", RunAfter: time.Now().Add(time.Duration(minutes) * time.Minute),
	}); err != nil {
		return fmt.Errorf("store: schedule %s job: %w", kind, err)
	}
	return nil
}

func enqueueIfMissing(ctx context.Context, st *store.Store, kind, payload string) error {
	open, err := st.HasOpenJob(ctx, kind, payload)
	if err != nil {
		return fmt.Errorf("store: check open %s job: %w", kind, err)
	}
	if open {
		return nil
	}
	if err := st.EnqueueJob(ctx, &core.Job{Kind: kind, Payload: payload}); err != nil {
		return fmt.Errorf("store: enqueue %s job: %w", kind, err)
	}
	return nil
}

func recordRejections(ctx context.Context, st *store.Store, decisions []wanted.Decision, profile *core.QualityProfile, info core.GrabInfo) error {
	sort.Slice(decisions, func(i, j int) bool {
		left, _ := wanted.ScoreRelease(decisions[i].Release, profile)
		right, _ := wanted.ScoreRelease(decisions[j].Release, profile)
		return left > right
	})
	for _, decision := range decisions[:min(5, len(decisions))] {
		grab := &core.Grab{
			GrabInfo: core.GrabInfo{
				MovieID: info.MovieID, SeriesID: info.SeriesID, SeasonNum: info.SeasonNum,
				EpisodeIDs: info.EpisodeIDs, ReleaseTitle: decision.Release.Title,
			},
			ReleaseID: decision.Release.ID,
			Reason:    decision.Reject,
			Status:    core.GrabStatusRejected,
		}
		if err := st.InsertGrab(ctx, grab); err != nil {
			return fmt.Errorf("store: record rejected release: %w", err)
		}
	}
	return nil
}

// recordNoRelease writes the "searched, found nothing worth grabbing" event.
//
// tried names the search variants that were run, and is empty for the searches
// that only have one. It is recorded because a scene search now asks two
// different questions, and "no release" means something different depending on
// which of them were asked.
func recordNoRelease(ctx context.Context, st *store.Store, title string, candidates int, movieID, seriesID int64, tried ...string) error {
	message := fmt.Sprintf("no acceptable release found for %s (%d candidates)", title, candidates)
	if len(tried) > 0 {
		message = fmt.Sprintf("no acceptable release found for %s (%d candidates; tried %s)",
			title, candidates, strings.Join(tried, ", "))
	}
	if err := st.InsertEvent(ctx, &core.Event{
		Category: "grab",
		Message:  message,
		MovieID:  movieID,
		SeriesID: seriesID,
	}); err != nil {
		return fmt.Errorf("store: record no-release event: %w", err)
	}
	return nil
}

func emptyPayload(payload json.RawMessage) error {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || len(object) != 0 {
		return fmt.Errorf("payload must be an empty object")
	}
	return nil
}

func matchesMovie(release core.Release, movie core.Movie) bool {
	if normalizeTitle(release.Parsed.Title) != normalizeTitle(movie.Title) {
		return false
	}
	if release.Parsed.Year > 0 && movie.Year > 0 {
		return release.Parsed.Year == movie.Year
	}
	return release.Parsed.Confidence >= highTitleConfidence
}

// matchesRSSEpisode reports whether a feed release is the episode Caravan is
// looking for. The title test is shared; the identity test is not, because a
// scene's season and episode numbers are Caravan's own mapping (release year,
// sequence within that year) and no indexer publishes them. The release date is
// what a scene name actually carries and what identifies it. A scene date may
// be one day off; matchRSSRelease then picks the closest wanted scene so a
// daily studio does not have one release claimed by two days.
func matchesRSSEpisode(release core.Release, episode wanted.Episode) bool {
	if normalizeTitle(release.Parsed.Title) != normalizeTitle(episode.SeriesTitle) {
		return false
	}
	if episode.SeriesKind == core.SeriesKindAdult {
		return core.NearbySceneDay(release.Parsed.SceneDate, episode.AirDate)
	}
	// One episode only: a season pack matching by containment would be grabbed
	// as if it were the single episode, which is the interactive path's job.
	return len(release.Parsed.Episodes) == 1 &&
		release.Parsed.Season == episode.SeasonNumber &&
		contains(release.Parsed.Episodes, episode.EpisodeNumber)
}

// bestRSSScene picks the unique closest wanted scene for a dated release.
// An exact day wins. Two scenes the same distance away (the day before and
// the day after, with nothing on the release's own day) are left unmatched:
// guessing would grab the wrong scene.
func bestRSSScene(release core.Release, episodes []wanted.Episode) *wanted.Episode {
	var best *wanted.Episode
	bestAbs := 0
	ties := 0
	for i := range episodes {
		ep := &episodes[i]
		if !matchesRSSEpisode(release, *ep) {
			continue
		}
		days, ok := core.SceneDayDelta(release.Parsed.SceneDate, ep.AirDate)
		if !ok {
			continue
		}
		abs := days
		if abs < 0 {
			abs = -abs
		}
		if best == nil || abs < bestAbs {
			best = ep
			bestAbs = abs
			ties = 1
			continue
		}
		if abs == bestAbs && ep.ID != best.ID {
			ties++
		}
	}
	if best == nil || (ties > 1 && bestAbs > 0) {
		return nil
	}
	return best
}

func normalizeTitle(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteByte(' ')
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

func contains(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
