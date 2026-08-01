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
	"github.com/watzon/caravan/internal/store"
	"github.com/watzon/caravan/internal/wanted"
)

const (
	defaultRSSSyncInterval = 15
	defaultBacklogInterval = 360
	searchTimeout          = 30 * time.Second
	engineWaitTimeout      = 5 * time.Second
	highTitleConfidence    = 0.9
)

type moviePayload struct {
	MovieID int64 `json:"movie_id"`
}

type episodePayload struct {
	EpisodeID int64 `json:"episode_id"`
}

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
	indexers, err := st.ListEnabledIndexers(ctx)
	if err != nil {
		return fmt.Errorf("store: list enabled indexers: %w", err)
	}
	if r.indexers == nil {
		return fmt.Errorf("no indexer client configured")
	}

	for _, cfg := range indexers {
		searchCtx, cancel := context.WithTimeout(ctx, searchTimeout)
		releases, searchErr := r.indexers(cfg).Search(searchCtx, "", cfg.Categories)
		cancel()
		if searchErr != nil {
			continue
		}
		for _, release := range releases {
			release.IndexerID = cfg.ID
			release.Indexer = cfg.Name
			if err := st.UpsertRelease(ctx, &release); err != nil {
				return fmt.Errorf("store: cache rss release: %w", err)
			}
			if err := r.matchRSSRelease(ctx, st, release, lists); err != nil {
				return err
			}
		}
	}
	return r.scheduleRecurring(ctx, jobRSSSync)
}

func (r *Runner) matchRSSRelease(ctx context.Context, st *store.Store, release core.Release, lists *wanted.Lists) error {
	for _, target := range lists.Movies {
		if !matchesMovie(release, target.Movie) {
			continue
		}
		profile, err := st.ResolveQualityProfile(ctx, target.QualityProfileID)
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
	for _, target := range lists.Episodes {
		if !matchesRSSEpisode(release, target) || len(release.Parsed.Episodes) != 1 {
			continue
		}
		series, err := st.GetSeries(ctx, target.SeriesID)
		if err != nil {
			return fmt.Errorf("store: get series for rss episode: %w", err)
		}
		profile, err := st.ResolveQualityProfile(ctx, series.QualityProfileID)
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
		payload, err := json.Marshal(moviePayload{MovieID: movie.ID})
		if err != nil {
			return fmt.Errorf("encode movie search payload: %w", err)
		}
		if err := enqueueIfMissing(ctx, st, jobSearchMovie, string(payload)); err != nil {
			return err
		}
	}
	for _, episode := range lists.Episodes {
		payload, err := json.Marshal(episodePayload{EpisodeID: episode.ID})
		if err != nil {
			return fmt.Errorf("encode episode search payload: %w", err)
		}
		if err := enqueueIfMissing(ctx, st, jobSearchEpisode, string(payload)); err != nil {
			return err
		}
	}
	return r.scheduleRecurring(ctx, jobBacklogSweep)
}

func (r *Runner) handleSearchMovie(ctx context.Context, st *store.Store, payload json.RawMessage) error {
	var input moviePayload
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
	profile, err := st.ResolveQualityProfile(ctx, movie.QualityProfileID)
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
	var input episodePayload
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
	profile, err := st.ResolveQualityProfile(ctx, series.QualityProfileID)
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
	return r.searchIndexers(ctx, st, func(ctx context.Context, client api.IndexerClient, cfg core.IndexerConfig) ([]core.Release, error) {
		searcher, ok := client.(movieSearcher)
		if !ok {
			return nil, fmt.Errorf("indexer %q does not support movie search", cfg.Name)
		}
		return searcher.SearchMovie(ctx, query, cfg.Categories)
	})
}

func (r *Runner) searchEpisodes(ctx context.Context, st *store.Store, title string, season, episode int) ([]core.Release, error) {
	return r.searchIndexers(ctx, st, func(ctx context.Context, client api.IndexerClient, cfg core.IndexerConfig) ([]core.Release, error) {
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

func (r *Runner) searchIndexers(ctx context.Context, st *store.Store, search indexerSearch) ([]core.Release, error) {
	indexers, err := st.ListEnabledIndexers(ctx)
	if err != nil {
		return nil, fmt.Errorf("store: list enabled indexers: %w", err)
	}
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
	return r.grab(ctx, st, release, score, source, core.GrabInfo{MovieID: movie.ID}, core.AddOpts{
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
	return r.grab(ctx, st, release, score, source, info, core.AddOpts{
		Category: "tv", SeriesID: episode.SeriesID, SeasonNum: episode.SeasonNumber, EpisodeIDs: []int64{episode.ID},
	})
}

func (r *Runner) grab(ctx context.Context, st *store.Store, release core.Release, score int, source string, info core.GrabInfo, opts core.AddOpts) error {
	if r.engine == nil {
		return fmt.Errorf("download engine unavailable")
	}
	engineCtx, cancel := context.WithTimeout(ctx, engineWaitTimeout)
	defer cancel()
	engine := r.engine(engineCtx)
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
	case jobRSSSync:
		key, defaultMinutes = store.SettingRSSSyncIntervalMinutes, defaultRSSSyncInterval
	case jobBacklogSweep:
		key, defaultMinutes = store.SettingBacklogIntervalMinutes, defaultBacklogInterval
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
