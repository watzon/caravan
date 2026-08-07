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
	"github.com/watzon/caravan/internal/store"
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
	// flagWrongDate is the scene equivalent of wrong-season/wrong-episode: a
	// scene is addressed by its release date, so a release whose parsed date is
	// not the searched scene's is the wrong scene (PLAN phase 9 task 3).
	flagWrongDate = "wrong-date"
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
	// (SPEC §8). It remains grabbable, but an incompatible release sorts after
	// releases with every other verdict.
	Compatibility compatibilityJSON `json:"compatibility"`
	// ProfileDecision is the effective item, library, or system quality
	// profile's scoring explanation. It is advisory in the picker: a user can
	// still grab any displayed release.
	ProfileDecision profileDecisionJSON `json:"profile_decision"`
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
	// back empty can show the user why. When a search sends several — a scene
	// is looked for by date AND by title — this is the first of them and
	// Queries is all of them.
	Query string `json:"query"`
	// Queries is every search that was run, in the order they were run. It has
	// one entry for everything but a scene.
	Queries []string `json:"queries"`
	// Truncated reports that the universal search cut the list at its limit.
	// Every cut row is still cached, so narrowing the search re-finds it.
	Truncated bool `json:"truncated,omitempty"`
	// LibraryID echoes the library whose quality profile scored the rows, so
	// the client can confirm what the scores were measured against. 0 on the
	// per-item endpoints, whose item names its own library.
	LibraryID int64              `json:"library_id,omitempty"`
	Releases  []releaseJSON      `json:"releases"`
	Errors    []indexerErrorJSON `json:"errors"`
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
	profile, err := s.st.ResolveItemQualityProfileByLibrary(r.Context(), m.LibraryID, core.LibraryKindMovie, m.QualityProfileID)
	if err != nil {
		s.writeStoreError(w, "resolve movie quality profile", err)
		return
	}
	s.serveReleases(w, r, m.LibraryID, core.LibraryKindMovie, []string{query}, profile, func(rel core.Release) []string {
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
	sr, ok := s.getVisibleSeries(w, r, id)
	if !ok {
		return
	}

	kind := core.LibraryKindForSeries(sr.Kind)
	profile, err := s.st.ResolveItemQualityProfileByLibrary(r.Context(), sr.LibraryID, kind, sr.QualityProfileID)
	if err != nil {
		s.writeStoreError(w, "resolve series quality profile", err)
		return
	}

	// A site is a series row too, and it must not be searched like one: the
	// library kind decides which indexers answer and with which categories, so
	// a hardcoded TV kind here would fan a scene search out over the television
	// library's 5000-series categories, the exact thing searchScene exists to
	// avoid (PLAN phase 9 task 3).
	if kind == core.LibraryKindAdult {
		s.serveSceneReleases(w, r, *sr, season, episode, profile)
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
	s.serveReleases(w, r, sr.LibraryID, kind, []string{query}, profile, func(rel core.Release) []string {
		return seriesReleaseFlags(rel, season, episode)
	})
}

// serveSceneReleases is handleSeriesReleases' adult branch, and it differs from
// the television one exactly where searchScene differs from a television
// search.
//
// The query is the site and the scene's RELEASE DATE, because a scene has no
// SxxEyy an indexer could filter on — "Site YY.MM.DD" is how scene releases are
// named and therefore how they are found. Caravan's season and episode numbers
// are its own mapping (release year, sequence within that year) and no indexer
// has ever heard of them, so putting an S22E07 in the query would return
// nothing at all.
//
// A request that narrows no further than the site, or one whose scene has no
// release date, searches the site's name alone. That is deliberately weaker
// than searchScene's silent no-op: this is a human at the picker who asked to
// see what the indexers have, and a list to choose from beats an empty table.
func (s *server) serveSceneReleases(w http.ResponseWriter, r *http.Request, sr core.Series, season, episode int, profile *core.QualityProfile) {
	var airDate time.Time
	var title string
	if season >= 0 && episode > 0 {
		e, err := s.st.GetEpisodeByNumber(r.Context(), sr.ID, season, episode)
		if err != nil && !errors.Is(err, store.ErrNotFound) {
			s.writeStoreError(w, "get scene", err)
			return
		}
		if e != nil {
			airDate, title = e.AirDate, e.Title
		}
	}

	// The same two questions the automatic search asks, from the same builder:
	// a picker that searched differently would show a user candidates the
	// automatic path never sees, and hide the ones it does.
	queries := make([]string, 0, 2)
	for _, search := range core.SceneSearches(sr.Title, airDate, title) {
		queries = append(queries, search.Query)
	}
	if len(queries) == 0 {
		// A request that narrows no further than the site, or a scene with
		// neither a date nor a title, searches the site's name alone. That is
		// deliberately weaker than searchScene's silent no-op: this is a human
		// who asked to see what the indexers have, and a list to choose from
		// beats an empty table.
		queries = []string{sr.Title}
	}

	s.serveReleases(w, r, sr.LibraryID, core.LibraryKindAdult, queries, profile, func(rel core.Release) []string {
		return sceneReleaseFlags(rel, airDate)
	})
}

// serveReleases runs one interactive search and writes the picker payload:
// fan out, merge, cache, flag, score, sort. libraryID is the searched item's
// own library — which decides which indexers answer and with which categories
// (PLAN phase 8 task 4) — with kind as the fallback for an item that names
// none. profile is resolved once from the item, its library, and then the
// system default.
func (s *server) serveReleases(w http.ResponseWriter, r *http.Request, libraryID int64, kind string, queries []string, profile *core.QualityProfile, flags func(core.Release) []string) {
	newClient, ok := s.requireIndexerClients(w)
	if !ok {
		return
	}

	settings, err := s.st.ResolveLibrarySettingsForItem(r.Context(), libraryID, kind)
	if err != nil {
		s.writeStoreError(w, "resolve library settings", err)
		return
	}
	indexers := settings.Indexers

	// The TV profile is resolved once for the whole fan-out so every row's
	// compatibility check uses the same playback capability.
	tvProfile := s.activeTVProfile(r.Context())

	ctx, cancel := context.WithTimeout(r.Context(), releaseSearchTimeout)
	defer cancel()
	releases, failures := searchIndexers(ctx, newClient, indexers, queries)

	out := releasesResponse{
		Query:    queries[0],
		Queries:  queries,
		Releases: make([]releaseJSON, 0, len(releases)),
		Errors:   failures,
	}
	for _, rel := range releases {
		// Caching every result is what makes the grab endpoint a lookup by id
		// rather than a second search, and it is the same table RSS sync
		// deduplicates against in phase 3. The search's deadline deliberately
		// does not cover it: the cache write is not part of the fan-out.
		if err := s.st.UpsertRelease(r.Context(), &rel); err != nil {
			s.writeStoreError(w, "cache release", err)
			return
		}
		out.Releases = append(out.Releases, releaseDTO(rel, flags(rel), tvProfile, profile))
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

// searchIndexers runs every query against every indexer and merges the answers.
//
// Indexers are independent network calls of wildly different latency, so they
// run in parallel; the results are collected in the configured order rather
// than the order they arrive, so the same set of answers always merges the same
// way. A failing indexer costs its own results and nothing else.
//
// Several queries is how a scene is searched: by date and by title, because a
// release named either way is the same scene and the picker should show both.
// Results are deduplicated by GUID across all of them, and the queries run in
// order so the first query's answers keep their place at the top.
func searchIndexers(ctx context.Context, newClient IndexerFactory, indexers []core.IndexerConfig, queries []string) ([]core.Release, []indexerErrorJSON) {
	merged := []core.Release{}
	failures := []indexerErrorJSON{}
	seenGUID := map[string]bool{}
	// One failure per indexer however many queries it failed: the user is being
	// told an indexer is down, and being told twice is not more true.
	seenFailure := map[int64]bool{}

	for _, query := range queries {
		releases, errs := searchIndexersOnce(ctx, newClient, indexers, query)
		for _, rel := range releases {
			if rel.GUID != "" && seenGUID[rel.GUID] {
				continue
			}
			if rel.GUID != "" {
				seenGUID[rel.GUID] = true
			}
			merged = append(merged, rel)
		}
		for _, err := range errs {
			if seenFailure[err.IndexerID] {
				continue
			}
			seenFailure[err.IndexerID] = true
			failures = append(failures, err)
		}
	}
	return merged, failures
}

// searchIndexersOnce is one query against every indexer.
func searchIndexersOnce(ctx context.Context, newClient IndexerFactory, indexers []core.IndexerConfig, query string) ([]core.Release, []indexerErrorJSON) {
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

func releaseDTO(rel core.Release, flags []string, tvProfile core.TVProfile, qualityProfile *core.QualityProfile) releaseJSON {
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

		Compatibility:   compatibilityDTO(tvProfile.Check(releaseTags(rel))),
		ProfileDecision: profileDecisionDTO(rel, qualityProfile),
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

// releaseIsIncompatible reports whether rel cannot play natively on the active
// TV profile. Other verdicts retain the normal picker ordering.
func releaseIsIncompatible(rel releaseJSON) bool {
	return rel.Compatibility.Verdict == core.TVCompatIncompatible
}

// sortReleases orders the picker: incompatible releases last, then best quality,
// then the healthiest swarm. This is presentation only, scoring a release
// against a quality profile is phase 3, and this ordering must not be mistaken
// for it. The title is the final tiebreak so a fan-out that finishes in a
// different order still renders the same table.
func sortReleases(releases []releaseJSON) {
	sort.Slice(releases, func(i, j int) bool {
		a, b := releases[i], releases[j]
		if aIncompatible, bIncompatible := releaseIsIncompatible(a), releaseIsIncompatible(b); aIncompatible != bIncompatible {
			return !aIncompatible
		}
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

// sceneReleaseFlags reports what is visibly wrong with a release relative to
// the scene it was searched for.
//
// It is seriesReleaseFlags' adult twin and deliberately not a call into it: a
// scene release carries no season or episode number, so seriesReleaseFlags
// would flag every single row "wrong-season" and "season-pack" and the picker
// would grey out the whole table. The date is the comparison that exists, and
// it is the same one searchScene matches candidates on.
//
// airDate is zero when the caller did not narrow to one scene, or when the
// scene has no release date, in which case there is nothing to compare against.
func sceneReleaseFlags(rel core.Release, airDate time.Time) []string {
	flags := commonReleaseFlags(rel)
	if airDate.IsZero() {
		return flags
	}
	if !sameDay(rel.Parsed.SceneDate, airDate) {
		flags = append(flags, flagWrongDate)
	}
	return flags
}

// sameDay compares two dates by calendar day in UTC. A zero date matches
// nothing: "this release does not say when it came out" is exactly the case the
// flag is for.
func sameDay(a, b time.Time) bool {
	if a.IsZero() || b.IsZero() {
		return false
	}
	ay, am, ad := a.UTC().Date()
	by, bm, bd := b.UTC().Date()
	return ay == by && am == bm && ad == bd
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

	s.grab(w, r, m.LibraryID, core.LibraryKindMovie, core.GrabInfo{
		MovieID: m.ID, LibraryID: m.LibraryID,
	}, core.AddOpts{
		Category:  engineCategoryMovies,
		MovieID:   m.ID,
		LibraryID: m.LibraryID,
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

	sr, ok := s.getVisibleSeries(w, r, id)
	if !ok {
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
	episodeIDs, seasonNum := seriesGrabScope(episodes, season, episode)
	if (season >= 0 || episode > 0) && len(episodeIDs) == 0 {
		writeError(w, http.StatusNotFound, "not found")
		return
	}

	info, opts := seriesGrabTarget(*sr, seasonNum, episodeIDs)
	s.grab(w, r, sr.LibraryID, core.LibraryKindForSeries(sr.Kind), info, opts)
}

// seriesGrabScope resolves the episode rows a series grab of this scope must
// satisfy. A whole-series grab (season < 0) has no season to record, so it
// reports 0 — the same value season 0 (specials) has; the episode ids are
// what the import pipeline actually matches against, and those are
// unambiguous.
func seriesGrabScope(episodes []core.Episode, season, episode int) ([]int64, int) {
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
	seasonNum := season
	if seasonNum < 0 {
		seasonNum = 0
	}
	return episodeIDs, seasonNum
}

// seriesGrabTarget builds the grab record and engine options for a series
// grab, shared by the per-item picker and the universal search's tied grab.
// The library a series belongs to decides which engine the download is routed
// to and which label it lands under, so a scene picked by hand goes exactly
// where automation.grabEpisode sends one found by the sweep — never into the
// television library's download folder under category "tv", which
// importDownloadedEpisodes would then have to un-guess.
func seriesGrabTarget(sr core.Series, seasonNum int, episodeIDs []int64) (core.GrabInfo, core.AddOpts) {
	kind := core.LibraryKindForSeries(sr.Kind)
	category := engineCategoryTV
	if kind == core.LibraryKindAdult {
		category = engineCategoryAdult
	}
	return core.GrabInfo{
			SeriesID:   sr.ID,
			SeasonNum:  seasonNum,
			EpisodeIDs: episodeIDs,
			LibraryID:  sr.LibraryID,
		}, core.AddOpts{
			Category:   category,
			SeriesID:   sr.ID,
			SeasonNum:  seasonNum,
			EpisodeIDs: episodeIDs,
			LibraryID:  sr.LibraryID,
		}
}

// grab sends a picked release to the engine and records it.
//
// The grab row is written before the engine is asked, so a failed grab is still
// history: "we tried this release and it was rejected" is the answer to "why is
// nothing downloading", and SPEC §7 keeps that explanation even when the
// attempt produced no download.
func (s *server) grab(w http.ResponseWriter, r *http.Request, libraryID int64, kind string, info core.GrabInfo, opts core.AddOpts) {
	var body grabRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.ReleaseID <= 0 {
		writeError(w, http.StatusBadRequest, "release_id is required")
		return
	}
	s.grabRelease(w, r, libraryID, kind, body.ReleaseID, info, opts)
}

// grabRelease is grab with the release id already decoded, so the universal
// search's grab — whose body carries more than a release id — shares the grab
// row, engine dispatch, error mapping and history event without a second
// copy.
func (s *server) grabRelease(w http.ResponseWriter, r *http.Request, libraryID int64, kind string, releaseID int64, info core.GrabInfo, opts core.AddOpts) {
	engine, ok := s.requireEngineFor(w, libraryID, kind)
	if !ok {
		return
	}
	ctx := r.Context()

	rel, err := s.st.GetRelease(ctx, releaseID)
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
