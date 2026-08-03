package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/download"
	"github.com/watzon/caravan/internal/parse"
)

// releaseSearchTimeout bounds one interactive fan-out. A user is waiting on
// this request, and one unreachable indexer must not hold the picker open
// until the client gives up.
const releaseSearchTimeout = 60 * time.Second

// grabReasonInteractive is recorded on grabs the user picked themselves. Phase
// 3's automatic decisioning writes a scoring explanation here instead.
const grabReasonInteractive = "interactive pick"

// Rejection-style flags on a release row (SPEC §9 step 4). They are advisory:
// the picker greys a flagged release rather than hiding it, because the user
// asked to see what the indexers actually have. Profile scoring — the part
// that turns a flag into a refusal — is phase 3.
const (
	flagWrongYear    = "wrong-year"
	flagWrongSeason  = "wrong-season"
	flagWrongEpisode = "wrong-episode"
	flagSeasonPack   = "season-pack"
	flagNoSeeders    = "no-seeders"
)

// releaseJSON is one row of the interactive picker: what the indexer said,
// what the parser made of it, and what Caravan can already tell is off about
// it relative to the item being searched for.
type releaseJSON struct {
	// ID is the cached `releases` row, and what POST .../grab is given.
	ID          int64  `json:"id"`
	IndexerID   int64  `json:"indexer_id"`
	Indexer     string `json:"indexer"`
	Title       string `json:"title"`
	GUID        string `json:"guid"`
	Protocol    string `json:"protocol"`
	Size        int64  `json:"size"`
	Seeders     int    `json:"seeders"`
	Leechers    int    `json:"leechers"`
	PublishedAt string `json:"published_at"`
	// AgeDays is how long ago the indexer published the release, -1 when it did
	// not say. The picker sorts and colors by age, and computing it here keeps
	// every client from re-deriving the same number.
	AgeDays int        `json:"age_days"`
	Parsed  parsedJSON `json:"parsed"`
	Flags   []string   `json:"flags"`
	// Compatibility is the active TV profile's verdict on the parsed tags
	// (SPEC §8). It is advisory: a flagged release is still grabbable, and
	// nothing here changes the ordering.
	Compatibility compatibilityJSON `json:"compatibility"`
}

// indexerErrorJSON reports an indexer that failed during a fan-out. Partial
// results are returned rather than failing the whole search (SPEC §13: a
// failure is visible, never silent), so the picker can say "3 of 4 indexers
// answered".
type indexerErrorJSON struct {
	IndexerID int64  `json:"indexer_id"`
	Indexer   string `json:"indexer"`
	Error     string `json:"error"`
}

type releasesResponse struct {
	// Query is what was actually sent to the indexers, so a picker that comes
	// back empty can show the user why.
	Query    string             `json:"query"`
	Releases []releaseJSON      `json:"releases"`
	Errors   []indexerErrorJSON `json:"errors"`
}

// grabRequest is the body of the grab endpoints: the release the user picked,
// by its cached id.
type grabRequest struct {
	ReleaseID int64 `json:"release_id"`
}

type grabResponse struct {
	GrabID       int64  `json:"grab_id"`
	DownloadID   string `json:"download_id"`
	ReleaseTitle string `json:"release_title"`
}

func (s *server) handleMovieReleases(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	m, err := s.st.GetMovie(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get movie", err)
		return
	}

	query := m.Title
	if m.Year > 0 {
		query = fmt.Sprintf("%s %d", m.Title, m.Year)
	}
	s.serveReleases(w, r, core.LibraryKindMovie, query, func(rel core.Release) []string {
		return movieReleaseFlags(rel, *m)
	})
}

func (s *server) handleSeriesReleases(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	season, episode, ok := seasonEpisodeParams(w, r)
	if !ok {
		return
	}
	sr, err := s.st.GetSeries(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get series", err)
		return
	}

	// Indexers index episodes by SxxEyy and season packs by Sxx, so the query
	// is narrowed exactly as far as the caller narrowed the request.
	query := sr.Title
	switch {
	case episode > 0:
		query = fmt.Sprintf("%s S%02dE%02d", sr.Title, season, episode)
	case season >= 0:
		query = fmt.Sprintf("%s S%02d", sr.Title, season)
	}
	s.serveReleases(w, r, core.LibraryKindTV, query, func(rel core.Release) []string {
		return seriesReleaseFlags(rel, season, episode)
	})
}

// serveReleases runs one interactive search and writes the picker payload:
// fan out, merge, cache, flag, sort. kind is the core.LibraryKind* the searched
// item belongs to, which decides which indexers answer and with which
// categories (PLAN phase 8 task 4).
func (s *server) serveReleases(w http.ResponseWriter, r *http.Request, kind, query string, flags func(core.Release) []string) {
	newClient, ok := s.requireIndexerClients(w)
	if !ok {
		return
	}

	settings, err := s.st.ResolveLibrarySettingsByKind(r.Context(), kind)
	if err != nil {
		s.writeStoreError(w, "resolve library settings", err)
		return
	}
	indexers := settings.Indexers

	// Resolved once for the whole fan-out so every row in one table is judged
	// against the same profile.
	profile := s.activeTVProfile(r.Context())

	ctx, cancel := context.WithTimeout(r.Context(), releaseSearchTimeout)
	defer cancel()
	releases, failures := searchIndexers(ctx, newClient, indexers, query)

	out := releasesResponse{Query: query, Releases: make([]releaseJSON, 0, len(releases)), Errors: failures}
	for _, rel := range releases {
		// Caching every result is what makes the grab endpoint a lookup by id
		// rather than a second search, and it is the same table RSS sync
		// deduplicates against in phase 3. The search's deadline deliberately
		// does not cover it: the cache write is not part of the fan-out.
		if err := s.st.UpsertRelease(r.Context(), &rel); err != nil {
			s.writeStoreError(w, "cache release", err)
			return
		}
		out.Releases = append(out.Releases, releaseDTO(rel, flags(rel), profile))
	}
	sortReleases(out.Releases)
	writeJSON(w, http.StatusOK, out)
}

// indexerSearch is one indexer's answer, carried back over a channel.
type indexerSearch struct {
	cfg      core.IndexerConfig
	releases []core.Release
	err      error
}

// searchIndexers queries every indexer concurrently and merges the answers.
//
// Indexers are independent network calls of wildly different latency, so they
// run in parallel; the results are collected in the configured order rather
// than the order they arrive, so the same set of answers always merges the same
// way. A failing indexer costs its own results and nothing else.
func searchIndexers(ctx context.Context, newClient IndexerFactory, indexers []core.IndexerConfig, query string) ([]core.Release, []indexerErrorJSON) {
	results := make(chan indexerSearch, len(indexers))
	for _, cfg := range indexers {
		go func() {
			// Exactly the resolved categories, nothing inferred: an empty
			// list searches the indexer unfiltered. Guessing a
			// per-media-type default here silently returned nothing from
			// indexers that do not expand parent categories.
			releases, err := newClient(cfg).Search(ctx, query, cfg.Categories)
			results <- indexerSearch{cfg: cfg, releases: releases, err: err}
		}()
	}

	byIndexer := make(map[int64]indexerSearch, len(indexers))
	for range indexers {
		res := <-results
		byIndexer[res.cfg.ID] = res
	}

	merged := []core.Release{}
	failures := []indexerErrorJSON{}
	seen := map[string]bool{}
	for _, cfg := range indexers {
		res := byIndexer[cfg.ID]
		if res.err != nil {
			failures = append(failures, indexerErrorJSON{
				IndexerID: cfg.ID,
				Indexer:   cfg.Name,
				Error:     res.err.Error(),
			})
			continue
		}
		for _, rel := range res.releases {
			// The indexer's identity is taken from the configuration that was
			// searched, not from the client's answer: the store keys the release
			// cache on (indexer_id, guid), and a client that left them unset
			// would collapse every result onto one row.
			rel.IndexerID = cfg.ID
			rel.Indexer = cfg.Name
			key := fmt.Sprintf("%d\x00%s", rel.IndexerID, rel.GUID)
			if seen[key] {
				continue
			}
			seen[key] = true
			if rel.Parsed.Title == "" {
				rel.Parsed = parse.Parse(rel.Title)
			}
			merged = append(merged, rel)
		}
	}
	return merged, failures
}

// releaseTags is what the TV-profile check judges a release on. The container
// comes from the release name, which usually does not carry one — an absent
// container is simply not judged (core.TVProfile.Check).
func releaseTags(rel core.Release) core.MediaTags {
	return core.MediaTags{
		Codec:     rel.Parsed.Codec,
		BitDepth:  rel.Parsed.BitDepth,
		Audio:     rel.Parsed.Audio,
		Container: parse.Container(rel.Title),
		Quality:   rel.Parsed.Quality,
	}
}

func releaseDTO(rel core.Release, flags []string, profile core.TVProfile) releaseJSON {
	if flags == nil {
		flags = []string{}
	}
	return releaseJSON{
		ID:          rel.ID,
		IndexerID:   rel.IndexerID,
		Indexer:     rel.Indexer,
		Title:       rel.Title,
		GUID:        rel.GUID,
		Protocol:    rel.Protocol,
		Size:        rel.Size,
		Seeders:     rel.Seeders,
		Leechers:    rel.Leechers,
		PublishedAt: jsonTime(rel.PublishedAt),
		AgeDays:     ageDays(rel.PublishedAt),
		Parsed:      parsedDTO(rel.Parsed),
		Flags:       flags,

		Compatibility: compatibilityDTO(profile.Check(releaseTags(rel))),
	}
}

// ageDays is how many whole days ago t was, -1 when t is unset.
func ageDays(t time.Time) int {
	if t.IsZero() {
		return -1
	}
	days := int(time.Since(t).Hours() / 24)
	if days < 0 {
		return 0
	}
	return days
}

// sortReleases orders the picker: best quality first, then the healthiest
// swarm. This is presentation only — scoring a release against a quality
// profile is phase 3, and this ordering must not be mistaken for it. The title
// is the final tiebreak so a fan-out that finishes in a different order still
// renders the same table.
func sortReleases(releases []releaseJSON) {
	sort.Slice(releases, func(i, j int) bool {
		a, b := releases[i], releases[j]
		if ra, rb := core.QualityRank(a.Parsed.Quality), core.QualityRank(b.Parsed.Quality); ra != rb {
			return ra < rb
		}
		if a.Seeders != b.Seeders {
			return a.Seeders > b.Seeders
		}
		return a.Title < b.Title
	})
}

// movieReleaseFlags reports what is visibly wrong with a release relative to
// the movie it was searched for.
func movieReleaseFlags(rel core.Release, m core.Movie) []string {
	flags := commonReleaseFlags(rel)
	if rel.Parsed.Year != 0 && m.Year != 0 && rel.Parsed.Year != m.Year {
		flags = append(flags, flagWrongYear)
	}
	return flags
}

// seriesReleaseFlags reports what is visibly wrong with a release relative to
// the season/episode it was searched for. season is -1 and episode 0 when the
// caller did not narrow the search, in which case there is nothing to compare
// against.
func seriesReleaseFlags(rel core.Release, season, episode int) []string {
	flags := commonReleaseFlags(rel)
	if season >= 0 && rel.Parsed.Season != season {
		flags = append(flags, flagWrongSeason)
	}
	if !rel.Parsed.IsEpisode() {
		// No episode numbers on a TV release means a season pack: one download
		// that satisfies many episodes, which the user should know before
		// picking it.
		flags = append(flags, flagSeasonPack)
	} else if episode > 0 && !containsInt(rel.Parsed.Episodes, episode) {
		flags = append(flags, flagWrongEpisode)
	}
	return flags
}

// commonReleaseFlags are the flags that do not depend on the target item.
func commonReleaseFlags(rel core.Release) []string {
	flags := []string{}
	if rel.Protocol == core.ProtocolTorrent && rel.Seeders == 0 {
		flags = append(flags, flagNoSeeders)
	}
	return flags
}

func containsInt(values []int, want int) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}

func (s *server) handleMovieGrab(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	m, err := s.st.GetMovie(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get movie", err)
		return
	}

	s.grab(w, r, core.LibraryKindMovie, core.GrabInfo{MovieID: m.ID}, core.AddOpts{
		Category: engineCategoryMovies,
		MovieID:  m.ID,
	})
}

func (s *server) handleSeriesGrab(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	season, episode, ok := seasonEpisodeParams(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	sr, err := s.st.GetSeries(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get series", err)
		return
	}

	// The episodes the download is expected to satisfy are resolved now, while
	// the user's intent is known, so the import pipeline matches against them
	// instead of re-guessing from the finished filename (SPEC §5.1).
	episodes, err := s.st.ListEpisodes(ctx, sr.ID)
	if err != nil {
		s.writeStoreError(w, "list episodes", err)
		return
	}
	episodeIDs := []int64{}
	for _, e := range episodes {
		if season >= 0 && e.SeasonNumber != season {
			continue
		}
		if episode > 0 && e.EpisodeNumber != episode {
			continue
		}
		episodeIDs = append(episodeIDs, e.ID)
	}
	if (season >= 0 || episode > 0) && len(episodeIDs) == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	// A whole-series grab has no season to record, so it reports 0 — the same
	// value season 0 (specials) has. The episode ids are what the import
	// pipeline actually matches against, and those are unambiguous.
	seasonNum := season
	if seasonNum < 0 {
		seasonNum = 0
	}
	s.grab(w, r, core.LibraryKindTV, core.GrabInfo{
		SeriesID:   sr.ID,
		SeasonNum:  seasonNum,
		EpisodeIDs: episodeIDs,
	}, core.AddOpts{
		Category:   engineCategoryTV,
		SeriesID:   sr.ID,
		SeasonNum:  seasonNum,
		EpisodeIDs: episodeIDs,
	})
}

// grab sends a picked release to the engine and records it.
//
// The grab row is written before the engine is asked, so a failed grab is still
// history: "we tried this release and it was rejected" is the answer to "why is
// nothing downloading", and SPEC §7 keeps that explanation even when the
// attempt produced no download.
func (s *server) grab(w http.ResponseWriter, r *http.Request, kind string, info core.GrabInfo, opts core.AddOpts) {
	var body grabRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ReleaseID <= 0 {
		writeError(w, http.StatusBadRequest, "release_id is required")
		return
	}
	engine, ok := s.requireEngineFor(w, kind)
	if !ok {
		return
	}
	ctx := r.Context()

	rel, err := s.st.GetRelease(ctx, body.ReleaseID)
	if err != nil {
		s.writeStoreError(w, "get release", err)
		return
	}

	info.ReleaseTitle = rel.Title
	g := &core.Grab{
		GrabInfo:  info,
		ReleaseID: rel.ID,
		Reason:    grabReasonInteractive,
		Status:    core.GrabStatusGrabbed,
	}
	if err := s.st.InsertGrab(ctx, g); err != nil {
		s.writeStoreError(w, "record grab", err)
		return
	}

	downloadID, err := engine.Add(ctx, *rel, opts)
	if err != nil {
		// Nothing broke: the release's protocol has no engine behind it, which
		// is a configuration the user has not finished rather than a failure.
		// It is recorded as a rejection with the reason, and answered with a
		// 4xx that names what to configure — never a silent drop, and never a
		// misroute to an engine that does not speak this protocol.
		if errors.Is(err, download.ErrNoEngine) {
			s.rejectGrab(ctx, g, info, err)
			writeError(w, http.StatusConflict, err.Error())
			return
		}
		// The client the release routes to has stopped answering its polls.
		// That is the download client's failure, not Caravan's, so the grab
		// fails rather than being rejected — but the reason is the poll's own
		// message, because "add download" alone would leave the user hunting
		// for a machine that is simply switched off (PLAN phase 6 task 4).
		if errors.Is(err, download.ErrClientUnreachable) {
			s.failGrab(ctx, g, info, err)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		s.failGrab(ctx, g, info, err)
		s.writeEngineError(w, "add download", err)
		return
	}

	if err := s.st.UpsertDownload(ctx, &core.Download{
		GrabID:   g.GrabID,
		Engine:   s.engineNameFor(ctx, engine, rel.Protocol),
		EngineID: downloadID,
		Title:    rel.Title,
		State:    core.DownloadQueued,
		Size:     rel.Size,
	}); err != nil {
		// The engine already has the download; refusing the request now would
		// tell the user nothing happened, which is false. Report it and let the
		// queue endpoint show the engine's own view.
		s.log.Error("record download", "error", err, "engine_id", downloadID)
	}
	s.logEvent(ctx, &core.Event{
		Category: "grab",
		Message:  "Grabbed " + rel.Title,
		Detail:   "from " + rel.Indexer,
		MovieID:  info.MovieID,
		SeriesID: info.SeriesID,
	})

	writeJSON(w, http.StatusCreated, grabResponse{
		GrabID:       g.GrabID,
		DownloadID:   string(downloadID),
		ReleaseTitle: rel.Title,
	})
}

// failGrab records a rejected grab. Neither write can be allowed to replace the
// engine's error as the reason the request failed, so both are logged rather
// than returned.
func (s *server) failGrab(ctx context.Context, g *core.Grab, info core.GrabInfo, cause error) {
	if err := s.st.SetGrabStatus(ctx, g.GrabID, core.GrabStatusFailed, cause.Error()); err != nil {
		s.log.Error("record failed grab", "error", err, "grab_id", g.GrabID)
	}
	s.logEvent(ctx, &core.Event{
		Level:    core.EventLevelError,
		Category: "grab",
		Message:  "Failed to grab " + g.ReleaseTitle,
		Detail:   cause.Error(),
		MovieID:  info.MovieID,
		SeriesID: info.SeriesID,
	})
}

// rejectGrab records a grab nothing was configured to take.
//
// It is GrabStatusRejected rather than GrabStatusFailed for the same reason
// phase 3 gives the status to a release an automatic search skipped: the row
// is a decision-log entry, and "why is this not downloading" is answered by
// its reason. The event is a warning, not an error — the activity feed is
// where a user looks for this, and there is nothing broken to report.
func (s *server) rejectGrab(ctx context.Context, g *core.Grab, info core.GrabInfo, cause error) {
	if err := s.st.SetGrabStatus(ctx, g.GrabID, core.GrabStatusRejected, cause.Error()); err != nil {
		s.log.Error("record rejected grab", "error", err, "grab_id", g.GrabID)
	}
	s.logEvent(ctx, &core.Event{
		Level:    core.EventLevelWarn,
		Category: "grab",
		Message:  "Cannot grab " + g.ReleaseTitle,
		Detail:   cause.Error(),
		MovieID:  info.MovieID,
		SeriesID: info.SeriesID,
	})
}

// logEvent appends to the activity feed. A feed write that fails is logged and
// swallowed: events are history, and losing one must never fail the operation
// it describes (SPEC §7).
func (s *server) logEvent(ctx context.Context, e *core.Event) {
	if err := s.st.InsertEvent(ctx, e); err != nil {
		s.log.Error("record event", "error", err, "message", e.Message)
	}
}

// seasonEpisodeParams reads ?season= and ?episode=. Absent season is -1 and
// absent episode is 0, which is how the handlers tell "the whole series" from
// "season 0", the specials season.
func seasonEpisodeParams(w http.ResponseWriter, r *http.Request) (season, episode int, ok bool) {
	season, episode = -1, 0
	query := r.URL.Query()

	if raw := query.Get("season"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 {
			writeError(w, http.StatusBadRequest, "season must be a non-negative integer")
			return 0, 0, false
		}
		season = n
	}
	if raw := query.Get("episode"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "episode must be a positive integer")
			return 0, 0, false
		}
		if season < 0 {
			writeError(w, http.StatusBadRequest, "episode requires season")
			return 0, 0, false
		}
		episode = n
	}
	return season, episode, true
}
