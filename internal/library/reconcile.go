package library

import (
	"context"
	"errors"
	"fmt"
	"path"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// warnf records a non-fatal problem. One unreachable image host or one
// unwritable NFO must not abort a scan (SPEC §13), so the paths that can
// degrade take a warnf instead of returning an error.
type warnf func(format string, args ...any)

// importMovie organizes a movie file and reconciles the database against it.
// rel is the file's current storage-root-relative path; the returned path is
// where it ended up, which differs from rel whenever the file was not already
// correctly named. disp says whether rel survives the move (see
// sourceDisposition): a scan consumes its source, an import keeps it.
func (m *Manager) importMovie(ctx context.Context, meta *core.MovieMeta, rel string, size int64, p core.ParsedRelease, warn warnf, disp sourceDisposition, targetLibraryID int64) (string, int64, error) {
	// The library is decided before the file is placed: the movie's own row
	// when it has one, else the library whose root holds rel, else the movie
	// default (libraries.go). Only then is a destination path built from it.
	lib, err := m.movieLibrary(ctx, meta.Ref(), rel, targetLibraryID)
	if err != nil {
		return "", 0, err
	}
	dir := m.movieDir(lib, meta.Title, meta.Year)
	dst := path.Join(dir, m.movieFileName(meta.Title, meta.Year, p.Edition, path.Ext(rel)))

	finalRel, err := m.placeFile(rel, dst, disp)
	if err != nil {
		return "", 0, err
	}

	if err := m.writeMovieNFO(dir, meta); err != nil {
		warn("%v", err)
	}
	posterRel, err := m.ensurePoster(ctx, dir, meta.PosterURL)
	if err != nil {
		warn("%v", err)
	}

	mv, created, err := m.upsertMovieRow(ctx, meta, dir, posterRel, "", nil, lib.ID)
	if err != nil {
		return "", 0, err
	}
	// Requests stay TMDB-keyed, so a non-TMDB add absorbs nothing.
	if created && meta.TMDBID != 0 {
		m.absorbRequests(ctx, core.MediaTypeMovie, meta.TMDBID, warn)
	}

	file := mediaFileFrom(finalRel, size, mv.ID, p)
	if err := m.preserveKnownTags(ctx, file); err != nil {
		return "", 0, err
	}
	if err := m.store.UpsertMediaFile(ctx, file); err != nil {
		return "", 0, err
	}
	if err := m.forgetOldPath(ctx, rel, finalRel); err != nil {
		return "", 0, err
	}
	return finalRel, mv.ID, nil
}

// importEpisode organizes an episode file and reconciles the database against
// it, including the series' full season/episode tree so the library view and
// the calendar have something to show for episodes that are not on disk yet.
func (m *Manager) importEpisode(ctx context.Context, meta *core.SeriesMeta, rel string, size int64, p core.ParsedRelease, warn warnf, disp sourceDisposition, targetLibraryID int64) (string, int64, error) {
	if len(p.Episodes) == 0 {
		return "", 0, fmt.Errorf("library: %s has no episode number", rel)
	}

	lib, err := m.seriesLibrary(ctx, meta.Ref(), rel, targetLibraryID)
	if err != nil {
		return "", 0, err
	}
	dir := m.seriesDir(lib, meta.Title, meta.Year)
	dst := path.Join(dir, m.seasonFolderName(p.Season),
		m.episodeFileName(meta.Title, meta.Year, p.Season, p.Episodes, episodeTitles(meta, p), path.Ext(rel)))

	finalRel, err := m.placeFile(rel, dst, disp)
	if err != nil {
		return "", 0, err
	}

	if err := m.writeTVShowNFO(dir, meta); err != nil {
		warn("%v", err)
	}
	posterRel, err := m.ensurePoster(ctx, dir, meta.PosterURL)
	if err != nil {
		warn("%v", err)
	}

	sr, created, err := m.upsertSeriesRow(ctx, meta, dir, posterRel, nil, lib.ID)
	if err != nil {
		return "", 0, err
	}
	if err := m.upsertSeriesTree(ctx, sr, meta); err != nil {
		return "", 0, err
	}
	// TMDB-keyed, exactly as importMovie's is.
	if created && meta.TMDBID != 0 {
		m.absorbRequests(ctx, core.MediaTypeSeries, meta.TMDBID, warn)
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

// linkImportedFile is the database tail every episode-shaped import shares:
// write the media_files row, link it to each episode the file covers, and drop
// the rows that described where the file used to be.
//
// It is a function rather than repeated code because the adult import path
// (importScene) differs from importEpisode only in where the file goes and
// where the series row came from. Everything after placement is identical, and
// a second copy of it is exactly how the two would drift.
func (m *Manager) linkImportedFile(ctx context.Context, rel, finalRel string, size int64, p core.ParsedRelease, episodeIDs []int64) error {
	file := mediaFileFrom(finalRel, size, 0, p)
	if err := m.preserveKnownTags(ctx, file); err != nil {
		return err
	}
	if err := m.store.UpsertMediaFile(ctx, file); err != nil {
		return err
	}
	for _, id := range episodeIDs {
		if err := m.store.LinkEpisodeFile(ctx, id, file.ID); err != nil {
			return err
		}
	}
	return m.forgetOldPath(ctx, rel, finalRel)
}

// forgetOldPath drops the database rows that referred to a file's pre-organize
// location. Without this a renamed file would keep a phantom media_files row
// that the next scan reports as removed.
func (m *Manager) forgetOldPath(ctx context.Context, oldRel, newRel string) error {
	if err := m.store.DeleteUnmatchedFileByPath(ctx, oldRel); err != nil {
		return err
	}
	if oldRel == newRel {
		return nil
	}
	return m.store.DeleteMediaFileByPath(ctx, oldRel)
}

// preserveKnownTags keeps the release tags an existing row already carries
// when the current parse could not recover them.
//
// This matters because the organized Jellyfin filename (SPEC §6) carries no
// quality, codec, or group tag: once a file is renamed, re-parsing it yields
// less than the original release name did. Without this, every rescan would
// erase the tags phase-3 upgrade decisions read.
func (m *Manager) preserveKnownTags(ctx context.Context, f *core.MediaFile) error {
	existing, err := m.store.GetMediaFileByPath(ctx, f.Path)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	f.ID = existing.ID
	f.AddedAt = existing.AddedAt
	if f.Quality == core.QualityUnknown {
		f.Quality = existing.Quality
	}
	if f.Source == core.SourceUnknown {
		f.Source = existing.Source
	}
	if f.Codec == "" {
		f.Codec = existing.Codec
	}
	if f.Audio == "" {
		f.Audio = existing.Audio
	}
	if f.ReleaseGroup == "" {
		f.ReleaseGroup = existing.ReleaseGroup
	}
	return nil
}

// absorbRequests marks a pending request approved because its title has just
// entered the library through the scan/import path.
//
// A request is granted by the title arriving, not by the route it took: the
// HTTP add endpoints do the same thing (internal/api/library.go). Without this,
// a title requested and then simply copied into the storage root would leave
// its request asking forever, with an Approve button that re-adds something
// already tracked.
//
// It is a warning rather than an error for the same reason the poster and NFO
// steps are: the file is imported and the library is correct, and a request row
// still reading "pending" must not abort a scan (SPEC §13).
func (m *Manager) absorbRequests(ctx context.Context, mediaType string, tmdbID int64, warn warnf) {
	if tmdbID == 0 {
		return
	}
	if _, err := m.store.ApproveRequestsFor(ctx, mediaType, tmdbID); err != nil {
		warn("library: absorb requests for %s %d: %v", mediaType, tmdbID, err)
	}
}

// monitoredOrDefault renders an add's monitored choice.
//
// Nil is "no opinion", which is unmonitored. Starting automation requires an
// explicit opt-in; scans, imports, old clients, and omitted JSON fields must
// not invent that intent. A non-nil pointer applies verbatim.
//
// It only ever decides a NEW row. Both upserts below overwrite it from the
// existing row when there is one, because a re-add is a metadata refresh and
// the monitored flag is the owner's, not the new caller's.
func monitoredOrDefault(monitored *bool) bool {
	return monitored != nil && *monitored
}

// existingMovieRow finds the row a provider answer is an update of, or nil
// when there is none. It is the read side of store.UpsertMovie's rung order:
// the pinned ref first, the TMDB id as the compatibility alias behind it.
//
// The TMDB rung is reachable only for a TMDB-shaped (or ref-less) answer. An
// AniList answer that happened to carry a TMDB id must NOT collapse onto the
// row TMDB identified: two providers' descriptions of a title are two rows
// until somebody says otherwise, and silently merging them is the drift the
// pinned ref exists to prevent.
func (m *Manager) existingMovieRow(ctx context.Context, ref core.ItemRef, tmdbID int64) (*core.Movie, error) {
	if ref.Valid() {
		existing, err := m.store.GetMovieByProviderRef(ctx, ref.Provider, ref.Ref)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	if tmdbID == 0 || (ref.Provider != "" && ref.Provider != core.ProviderTMDB) {
		return nil, nil
	}
	existing, err := m.store.GetMovieByTMDBID(ctx, tmdbID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return existing, nil
}

// existingSeriesRow is existingMovieRow's series twin, with the same rung
// order and the same refusal to cross providers.
func (m *Manager) existingSeriesRow(ctx context.Context, ref core.ItemRef, tmdbID int64) (*core.Series, error) {
	if ref.Valid() {
		existing, err := m.store.GetSeriesByProviderRef(ctx, ref.Provider, ref.Ref)
		if err == nil {
			return existing, nil
		}
		if !errors.Is(err, store.ErrNotFound) {
			return nil, err
		}
	}
	if tmdbID == 0 || (ref.Provider != "" && ref.Provider != core.ProviderTMDB) {
		return nil, nil
	}
	existing, err := m.store.GetSeriesByTMDBID(ctx, tmdbID)
	if errors.Is(err, store.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return existing, nil
}

// upsertMovieRow refreshes provider metadata onto the movies row, creating it
// when the provider ref is new, and reports whether the row was created.
//
// The row carries the identity of the provider that answered (Provider and
// ProviderRef straight off the meta): pinning happens at the write door, so
// every later refresh of this item asks the provider that actually knows it.
//
// A rescan must not overwrite user intent, so the monitored flag, the quality
// profile assignment, the minimum availability, and the original add time
// survive from any existing row. minAvailability, when non-empty, is fresh
// user intent and overrides; the scan and import paths pass "". monitored is
// the add's own choice for a new row and is ignored for an existing one — see
// monitoredOrDefault.
//
// libraryID is the resolved owning library. Like monitored it decides a new
// row only: an existing row keeps its own library (a refresh never moves an
// item), except that a zero — a row from before 0022, or one whose library
// vanished — is healed to the resolved value.
func (m *Manager) upsertMovieRow(ctx context.Context, meta *core.MovieMeta, dir, posterRel, minAvailability string, monitored *bool, libraryID int64) (*core.Movie, bool, error) {
	mv := &core.Movie{
		Provider:        meta.Provider,
		ProviderRef:     meta.ProviderRef,
		TMDBID:          meta.TMDBID,
		IMDBID:          meta.IMDBID,
		Title:           meta.Title,
		SortTitle:       sortTitle(meta.Title),
		Year:            meta.Year,
		Overview:        meta.Overview,
		Path:            dir,
		PosterPath:      posterRel,
		PosterURL:       meta.PosterURL,
		Monitored:       monitoredOrDefault(monitored),
		LibraryID:       libraryID,
		ReleaseDate:     meta.ReleaseDate,
		DigitalRelease:  meta.DigitalRelease,
		PhysicalRelease: meta.PhysicalRelease,
		MinAvailability: minAvailability,
	}

	existing, err := m.existingMovieRow(ctx, meta.Ref(), meta.TMDBID)
	if err != nil {
		return nil, false, err
	}
	created := existing == nil
	if existing != nil {
		mv.ID = existing.ID
		mv.Monitored = existing.Monitored
		mv.QualityProfileID = existing.QualityProfileID
		mv.AddedAt = existing.AddedAt
		if existing.LibraryID != 0 {
			mv.LibraryID = existing.LibraryID
		}
		if posterRel == "" {
			mv.PosterPath = existing.PosterPath
		}
		if minAvailability == "" {
			mv.MinAvailability = existing.MinAvailability
		}
	}

	if err := m.store.UpsertMovie(ctx, mv); err != nil {
		return nil, false, err
	}
	return mv, created, nil
}

// upsertSeriesRow is upsertMovieRow's series twin, with the same
// preserve-user-intent rule (including libraryID's decide-new-heal-zero rule).
func (m *Manager) upsertSeriesRow(ctx context.Context, meta *core.SeriesMeta, dir, posterRel string, monitored *bool, libraryID int64) (*core.Series, bool, error) {
	sr := &core.Series{
		Provider:    meta.Provider,
		ProviderRef: meta.ProviderRef,
		TMDBID:      meta.TMDBID,
		TVDBID:      meta.TVDBID,
		IMDBID:      meta.IMDBID,
		Title:       meta.Title,
		SortTitle:   sortTitle(meta.Title),
		Year:        meta.Year,
		Overview:    meta.Overview,
		Status:      meta.Status,
		Path:        dir,
		PosterPath:  posterRel,
		PosterURL:   meta.PosterURL,
		Monitored:   monitoredOrDefault(monitored),
		LibraryID:   libraryID,
		FirstAired:  meta.FirstAirDate,
	}

	existing, err := m.existingSeriesRow(ctx, meta.Ref(), meta.TMDBID)
	if err != nil {
		return nil, false, err
	}
	created := existing == nil
	if existing != nil {
		sr.ID = existing.ID
		sr.Monitored = existing.Monitored
		sr.QualityProfileID = existing.QualityProfileID
		sr.AddedAt = existing.AddedAt
		if existing.LibraryID != 0 {
			sr.LibraryID = existing.LibraryID
		}
		if posterRel == "" {
			sr.PosterPath = existing.PosterPath
		}
	}

	if err := m.store.UpsertSeries(ctx, sr); err != nil {
		return nil, false, err
	}
	return sr, created, nil
}

// upsertSeriesTree writes the provider's seasons and episodes for a series.
// The whole tree lands, not just the episodes on disk: the library UI needs to
// show what is missing, which is the entire point of a wanted list.
//
// A row the tree writes for the first time inherits the SERIES' monitored flag.
// That is the same rule PATCH /library/series/{id} enforces from the other
// direction (store.CascadeSeriesMonitored, SPEC §7), and it is load-bearing
// rather than tidy: the wanted list is computed from episodes.monitored, not
// from the series', so a series added unmonitored whose episodes landed
// monitored would be followed by the backlog sweep exactly as if it had been
// added monitored. Rows that already exist keep their own flag, so a season
// somebody toggled by hand survives every refresh.
func (m *Manager) upsertSeriesTree(ctx context.Context, sr *core.Series, meta *core.SeriesMeta) error {
	seriesID := sr.ID
	existingSeasons, err := m.store.ListSeasons(ctx, seriesID)
	if err != nil {
		return err
	}
	seasonMonitored := make(map[int]bool, len(existingSeasons))
	for _, s := range existingSeasons {
		seasonMonitored[s.Number] = s.Monitored
	}

	existingEpisodes, err := m.store.ListEpisodes(ctx, seriesID)
	if err != nil {
		return err
	}
	type key struct{ season, episode int }
	episodeMonitored := make(map[key]bool, len(existingEpisodes))
	for _, e := range existingEpisodes {
		episodeMonitored[key{e.SeasonNumber, e.EpisodeNumber}] = e.Monitored
	}

	for _, sm := range meta.Seasons {
		monitored, ok := seasonMonitored[sm.Number]
		if !ok {
			// Specials (season 0) start unmonitored: they are typically promo
			// shorts and recaps nobody wants automation hunting for. The user
			// opts in per season or episode; existing flags are preserved above.
			monitored = sr.Monitored && sm.Number != 0
		}
		season := &core.Season{
			SeriesID:  seriesID,
			Number:    sm.Number,
			Title:     sm.Title,
			Overview:  sm.Overview,
			AirDate:   sm.AirDate,
			Monitored: monitored,
		}
		if err := m.store.UpsertSeason(ctx, season); err != nil {
			return err
		}

		for _, em := range sm.Episodes {
			monitored, ok := episodeMonitored[key{em.Season, em.Number}]
			if !ok {
				monitored = sr.Monitored && em.Season != 0
			}
			episode := &core.Episode{
				SeriesID:       seriesID,
				SeasonNumber:   em.Season,
				EpisodeNumber:  em.Number,
				TMDBID:         em.TMDBID,
				Title:          em.Title,
				Overview:       em.Overview,
				AirDate:        em.AirDate,
				Monitored:      monitored,
				AbsoluteNumber: em.Absolute,
			}
			if err := m.store.UpsertEpisode(ctx, episode); err != nil {
				return err
			}
		}
	}
	return nil
}

// ensureEpisodes returns the row ids for the given episode numbers, creating
// placeholder rows for any the provider did not list. A file on disk is
// evidence the episode exists, whatever the provider thinks. New placeholders
// inherit the parent series' explicit monitoring state.
func (m *Manager) ensureEpisodes(ctx context.Context, seriesID int64, season int, numbers []int, monitored bool) ([]int64, error) {
	ids := make([]int64, 0, len(numbers))
	for _, n := range numbers {
		existing, err := m.store.GetEpisodeByNumber(ctx, seriesID, season, n)
		switch {
		case err == nil:
			ids = append(ids, existing.ID)
			continue
		case !errors.Is(err, store.ErrNotFound):
			return nil, err
		}

		episode := &core.Episode{
			SeriesID:      seriesID,
			SeasonNumber:  season,
			EpisodeNumber: n,
			Monitored:     monitored,
		}
		if err := m.store.UpsertEpisode(ctx, episode); err != nil {
			return nil, err
		}
		ids = append(ids, episode.ID)
	}
	return ids, nil
}

// episodeTitles joins the titles of every episode a file covers, which is what
// a multi-episode filename renders after the SxxExx tag.
func episodeTitles(meta *core.SeriesMeta, p core.ParsedRelease) string {
	byNumber := map[int]string{}
	for _, sm := range meta.Seasons {
		if sm.Number != p.Season {
			continue
		}
		for _, em := range sm.Episodes {
			byNumber[em.Number] = em.Title
		}
	}

	titles := make([]string, 0, len(p.Episodes))
	for _, n := range p.Episodes {
		if t := byNumber[n]; t != "" {
			titles = append(titles, t)
		}
	}
	return strings.Join(titles, " + ")
}

// mediaFileFrom builds the media_files row for an organized file. Unparseable
// quality and source become the explicit "unknown" rung rather than an empty
// string, so ranking never has to special-case a blank (core.QualityRank).
func mediaFileFrom(rel string, size, movieID int64, p core.ParsedRelease) *core.MediaFile {
	quality := p.Quality
	if quality == "" {
		quality = core.QualityUnknown
	}
	source := p.Source
	if source == "" {
		source = core.SourceUnknown
	}
	return &core.MediaFile{
		Path:         rel,
		Size:         size,
		MovieID:      movieID,
		Quality:      quality,
		Source:       source,
		Codec:        p.Codec,
		Audio:        p.Audio,
		ReleaseGroup: p.Group,
	}
}
