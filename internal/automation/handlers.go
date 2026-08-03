package automation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/watzon/caravan/internal/api"
	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/wanted"
)

const (
	defaultRSSSyncInterval = 15
	defaultBacklogInterval = 360
	// defaultRefreshInterval is twelve hours, Radarr and Sonarr's cadence: a
	// release date or a series status changes on the scale of days, and every
	// sweep is one provider round trip per movie plus one per season.
	defaultRefreshInterval = 720
	searchTimeout          = 30 * time.Second
	engineWaitTimeout      = 5 * time.Second
	highTitleConfidence    = 0.9
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
			if err := r.matchRSSRelease(ctx, st, release, lists, feed.kindsFor(release)); err != nil {
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
	// kind is one of the core.LibraryKind* constants.
	kind string
	// categories are the categories this library asked this indexer for, empty
	// when it asked unfiltered.
	categories []int
}

// kindsFor is the per-library half of a shared fetch: the library kinds that
// may act on this release.
//
// The union that made one fetch out of many is exactly what makes this
// necessary. A library that narrowed an indexer to its own categories still
// receives every other library's categories in the shared response, and
// offering those to its wanted items would grab releases the interactive and
// backlog searches for the same item would never have seen (PLAN phase 8 task
// 5).
func (f rssFeed) kindsFor(release core.Release) map[string]bool {
	kinds := map[string]bool{}
	for _, sub := range f.subscribers {
		if release.InCategories(sub.categories) {
			kinds[sub.kind] = true
		}
	}
	return kinds
}

// rssFeeds groups the libraries' resolved indexer sets by indexer.
//
// The grouping is the point: a feed is a firehose of everything new, so asking
// the same indexer once per library would fetch the same document twice, and
// indexers rate-limit. One fetch per indexer per cycle answers for every
// library that enabled it, and the per-library decision moves to matching
// (PLAN phase 8 task 5).
func rssFeeds(ctx context.Context, st *store.Store) ([]rssFeed, error) {
	libraries, err := st.ListLibraries(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: list libraries: %w", err)
	}

	feeds := []*rssFeed{}
	byIndexer := map[int64]*rssFeed{}
	for _, library := range libraries {
		settings, err := st.ResolveLibrarySettings(ctx, library.ID)
		if err != nil {
			return nil, fmt.Errorf("store: resolve settings of library %d: %w", library.ID, err)
		}
		for _, cfg := range settings.Indexers {
			feed, ok := byIndexer[cfg.ID]
			if !ok {
				feed = &rssFeed{cfg: cfg, categories: map[int]bool{}}
				byIndexer[cfg.ID] = feed
				feeds = append(feeds, feed)
			}
			feed.subscribers = append(feed.subscribers, rssSubscriber{
				kind: library.Kind, categories: cfg.Categories,
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

// matchRSSRelease offers one release to the wanted items of every library kind
// the feed it came from answers for with this release. A library that disabled
// the indexer, or that narrowed it to categories this release is not in, is not
// among them — so its items never see the release even though the fetch was
// shared.
func (r *Runner) matchRSSRelease(ctx context.Context, st *store.Store, release core.Release, lists *wanted.Lists, kinds map[string]bool) error {
	movies, episodes := lists.Movies, lists.Episodes
	if !kinds[core.LibraryKindMovie] {
		movies = nil
	}
	if !kinds[core.LibraryKindTV] {
		episodes = nil
	}
	for _, target := range movies {
		if !matchesMovie(release, target.Movie) {
			continue
		}
		profile, err := st.ResolveItemQualityProfile(ctx, core.LibraryKindMovie, target.QualityProfileID)
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
	for _, target := range episodes {
		if !matchesRSSEpisode(release, target) || len(release.Parsed.Episodes) != 1 {
			continue
		}
		series, err := st.GetSeries(ctx, target.SeriesID)
		if err != nil {
			return fmt.Errorf("store: get series for rss episode: %w", err)
		}
		profile, err := st.ResolveItemQualityProfile(ctx, core.LibraryKindTV, series.QualityProfileID)
		if err != nil {
			return fmt.Errorf("store: resolve episode profile: %w", err)
		}
		score, reject := wanted.ScoreRelease(release, profile)
		if reject != "" || (target.Reason == wanted.ReasonBelowCutoff && !wanted.IsUpgrade(release.Parsed.Quality, target.FileQuality)) {
			continue
		}
		if err := r.grabEpisode(ctx, st, target.Episode, release, score, "automatic rss"); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runner) handleBacklogSweep(ctx context.Context, st *store.Store, payload json.RawMessage) error {
	if err := emptyPayload(payload); err != nil {
		return err
	}
	lists, err := wanted.Compute(ctx, st)
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
	profile, err := st.ResolveItemQualityProfile(ctx, core.LibraryKindMovie, movie.QualityProfileID)
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
	candidates, err := r.searchMovies(ctx, st, query)
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
	return r.grabMovie(ctx, st, *movie, *best, score, "automatic search")
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
	series, err := st.GetSeries(ctx, episode.SeriesID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: get series: %w", err)
	}
	profile, err := st.ResolveItemQualityProfile(ctx, core.LibraryKindTV, series.QualityProfileID)
	if err != nil {
		return fmt.Errorf("store: resolve episode profile: %w", err)
	}
	if _, active, err := st.ActiveGrabForEpisode(ctx, episode.ID); err != nil {
		return fmt.Errorf("store: find active episode grab: %w", err)
	} else if active {
		return nil
	}

	candidates, err := r.searchEpisodes(ctx, st, series.Title, episode.SeasonNumber, episode.EpisodeNumber)
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
	return r.grabEpisode(ctx, st, *episode, *best, score, "automatic search")
}

func (r *Runner) searchMovies(ctx context.Context, st *store.Store, query string) ([]core.Release, error) {
	return r.searchIndexers(ctx, st, core.LibraryKindMovie, func(ctx context.Context, client api.IndexerClient, cfg core.IndexerConfig) ([]core.Release, error) {
		searcher, ok := client.(movieSearcher)
		if !ok {
			return nil, fmt.Errorf("indexer %q does not support movie search", cfg.Name)
		}
		return searcher.SearchMovie(ctx, query, cfg.Categories)
	})
}

func (r *Runner) searchEpisodes(ctx context.Context, st *store.Store, title string, season, episode int) ([]core.Release, error) {
	return r.searchIndexers(ctx, st, core.LibraryKindTV, func(ctx context.Context, client api.IndexerClient, cfg core.IndexerConfig) ([]core.Release, error) {
		searcher, ok := client.(tvSearcher)
		if !ok {
			return nil, fmt.Errorf("indexer %q does not support tv search", cfg.Name)
		}
		return searcher.SearchTV(ctx, title, season, episode, cfg.Categories)
	})
}

type indexerSearch func(context.Context, api.IndexerClient, core.IndexerConfig) ([]core.Release, error)

type indexerResult struct {
	cfg      core.IndexerConfig
	releases []core.Release
}

// searchIndexers fans one search out over the indexers the library of the given
// core.LibraryKind* searches, each already carrying the categories that search
// must send (PLAN phase 8 task 4).
func (r *Runner) searchIndexers(ctx context.Context, st *store.Store, kind string, search indexerSearch) ([]core.Release, error) {
	settings, err := st.ResolveLibrarySettingsByKind(ctx, kind)
	if err != nil {
		return nil, fmt.Errorf("store: resolve %s library settings: %w", kind, err)
	}
	indexers := settings.Indexers
	if r.indexers == nil {
		return nil, fmt.Errorf("no indexer client configured")
	}

	results := make(chan indexerResult, len(indexers))
	for _, cfg := range indexers {
		go func() {
			searchCtx, cancel := context.WithTimeout(ctx, searchTimeout)
			releases, err := search(searchCtx, r.indexers(cfg), cfg)
			cancel()
			if err == nil {
				results <- indexerResult{cfg: cfg, releases: releases}
				return
			}
			results <- indexerResult{cfg: cfg}
		}()
	}

	candidates := []core.Release{}
	for range indexers {
		result := <-results
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

func (r *Runner) grabMovie(ctx context.Context, st *store.Store, movie core.Movie, release core.Release, score int, source string) error {
	if _, active, err := st.ActiveGrabForMovie(ctx, movie.ID); err != nil {
		return fmt.Errorf("store: find active movie grab: %w", err)
	} else if active {
		return nil
	}
	return r.grab(ctx, st, core.LibraryKindMovie, release, score, source, core.GrabInfo{MovieID: movie.ID}, core.AddOpts{
		Category: "movies", MovieID: movie.ID,
	})
}

func (r *Runner) grabEpisode(ctx context.Context, st *store.Store, episode core.Episode, release core.Release, score int, source string) error {
	if _, active, err := st.ActiveGrabForEpisode(ctx, episode.ID); err != nil {
		return fmt.Errorf("store: find active episode grab: %w", err)
	} else if active {
		return nil
	}
	info := core.GrabInfo{SeriesID: episode.SeriesID, SeasonNum: episode.SeasonNumber, EpisodeIDs: []int64{episode.ID}}
	return r.grab(ctx, st, core.LibraryKindTV, release, score, source, info, core.AddOpts{
		Category: "tv", SeriesID: episode.SeriesID, SeasonNum: episode.SeasonNumber, EpisodeIDs: []int64{episode.ID},
	})
}

// grab hands one release to the engine the library of kind routes its
// downloads to, and records the attempt either way.
func (r *Runner) grab(ctx context.Context, st *store.Store, kind string, release core.Release, score int, source string, info core.GrabInfo, opts core.AddOpts) error {
	if r.engine == nil {
		return fmt.Errorf("download engine unavailable")
	}
	engineCtx, cancel := context.WithTimeout(ctx, engineWaitTimeout)
	defer cancel()
	engine := r.engine(engineCtx, kind)
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
	if _, err := engine.Add(ctx, release, opts); err != nil {
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

func (r *Runner) scheduleRecurring(ctx context.Context, kind string) error {
	var (
		key            string
		defaultMinutes int
	)
	switch kind {
	case core.JobRSSSync:
		key, defaultMinutes = store.SettingRSSSyncIntervalMinutes, defaultRSSSyncInterval
	case core.JobBacklogSweep:
		key, defaultMinutes = store.SettingBacklogIntervalMinutes, defaultBacklogInterval
	case core.JobRefreshMetadata:
		key, defaultMinutes = store.SettingRefreshIntervalMinutes, defaultRefreshInterval
	default:
		return fmt.Errorf("unsupported recurring job kind %q", kind)
	}
	minutes := settingMinutes(ctx, r.st, key, defaultMinutes)
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

func recordNoRelease(ctx context.Context, st *store.Store, title string, candidates int, movieID, seriesID int64) error {
	if err := st.InsertEvent(ctx, &core.Event{
		Category: "grab",
		Message:  fmt.Sprintf("no acceptable release found for %s (%d candidates)", title, candidates),
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

func settingMinutes(ctx context.Context, st *store.Store, key string, fallback int) int {
	value, err := st.GetSetting(ctx, key)
	if err != nil {
		return fallback
	}
	minutes, err := strconv.Atoi(value)
	if err != nil || minutes <= 0 || int64(minutes) > int64(^uint64(0)>>1)/int64(time.Minute) {
		return fallback
	}
	return minutes
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

func matchesRSSEpisode(release core.Release, episode wanted.Episode) bool {
	return normalizeTitle(release.Parsed.Title) == normalizeTitle(episode.SeriesTitle) &&
		release.Parsed.Season == episode.SeasonNumber &&
		contains(release.Parsed.Episodes, episode.EpisodeNumber)
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
