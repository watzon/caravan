package api

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strconv"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/parse"
	"github.com/watzon/caravan/internal/store"
)

// The library DTOs are declared here rather than reusing the core types
// directly so the wire format is an explicit, stable contract: snake_case
// keys, timestamps as RFC3339 strings (empty when unset), and no accidental
// exposure of a field added to core for internal reasons.

type movieJSON struct {
	ID               int64  `json:"id"`
	TMDBID           int64  `json:"tmdb_id"`
	IMDBID           string `json:"imdb_id"`
	Title            string `json:"title"`
	SortTitle        string `json:"sort_title"`
	Year             int    `json:"year"`
	Overview         string `json:"overview"`
	Path             string `json:"path"`
	PosterPath       string `json:"poster_path"`
	PosterURL        string `json:"poster_url"`
	Monitored        bool   `json:"monitored"`
	QualityProfileID int64  `json:"quality_profile_id"`
	ReleaseDate      string `json:"release_date"`
	AddedAt          string `json:"added_at"`
	UpdatedAt        string `json:"updated_at"`
	// File is the imported file, null while the movie is only wanted. A movie
	// has at most one current file in v1 (upgrades replace it, SPEC §9), so
	// this is a single object rather than a list.
	File *mediaFileJSON `json:"file"`
}

type seriesJSON struct {
	ID               int64  `json:"id"`
	TMDBID           int64  `json:"tmdb_id"`
	TVDBID           int64  `json:"tvdb_id"`
	IMDBID           string `json:"imdb_id"`
	Title            string `json:"title"`
	SortTitle        string `json:"sort_title"`
	Year             int    `json:"year"`
	Overview         string `json:"overview"`
	Status           string `json:"status"`
	Path             string `json:"path"`
	PosterPath       string `json:"poster_path"`
	PosterURL        string `json:"poster_url"`
	Monitored        bool   `json:"monitored"`
	QualityProfileID int64  `json:"quality_profile_id"`
	FirstAired       string `json:"first_aired"`
	AddedAt          string `json:"added_at"`
	UpdatedAt        string `json:"updated_at"`
	// EpisodeCount and EpisodeFileCount are what "12 / 24" and the
	// downloaded/incomplete status are computed from, so the list endpoint
	// carries them and the client never has to fetch every season to render a
	// poster grid.
	EpisodeCount     int `json:"episode_count"`
	EpisodeFileCount int `json:"episode_file_count"`
}

type seriesDetailJSON struct {
	seriesJSON
	Seasons []seasonJSON `json:"seasons"`
}

type seasonJSON struct {
	ID           int64         `json:"id"`
	SeriesID     int64         `json:"series_id"`
	SeasonNumber int           `json:"season_number"`
	Title        string        `json:"title"`
	Overview     string        `json:"overview"`
	PosterPath   string        `json:"poster_path"`
	AirDate      string        `json:"air_date"`
	Monitored    bool          `json:"monitored"`
	Episodes     []episodeJSON `json:"episodes"`
}

type episodeJSON struct {
	ID            int64  `json:"id"`
	SeriesID      int64  `json:"series_id"`
	SeasonNumber  int    `json:"season_number"`
	EpisodeNumber int    `json:"episode_number"`
	TMDBID        int64  `json:"tmdb_id"`
	Title         string `json:"title"`
	Overview      string `json:"overview"`
	AirDate       string `json:"air_date"`
	Monitored     bool   `json:"monitored"`
	// File is the imported file, null when the episode is missing. A
	// multi-episode file (S01E01E02) appears on each episode it covers.
	File *mediaFileJSON `json:"file"`
}

type mediaFileJSON struct {
	ID           int64  `json:"id"`
	Path         string `json:"path"`
	Size         int64  `json:"size"`
	Quality      string `json:"quality"`
	Source       string `json:"source"`
	Codec        string `json:"codec"`
	Audio        string `json:"audio"`
	ReleaseGroup string `json:"release_group"`
	AddedAt      string `json:"added_at"`
	ModifiedAt   string `json:"modified_at"`
	// Compatibility is the active TV profile's verdict on this file (SPEC §8).
	// The row carries no bit depth — that is a probe's answer, not a
	// filename's — so a 10-bit file imported from an untagged name reads as
	// unstated rather than as 8-bit.
	Compatibility compatibilityJSON `json:"compatibility"`
}

func movieDTO(m core.Movie) movieJSON {
	return movieJSON{
		ID:               m.ID,
		TMDBID:           m.TMDBID,
		IMDBID:           m.IMDBID,
		Title:            m.Title,
		SortTitle:        m.SortTitle,
		Year:             m.Year,
		Overview:         m.Overview,
		Path:             m.Path,
		PosterPath:       m.PosterPath,
		PosterURL:        m.PosterURL,
		Monitored:        m.Monitored,
		QualityProfileID: m.QualityProfileID,
		ReleaseDate:      jsonTime(m.ReleaseDate),
		AddedAt:          jsonTime(m.AddedAt),
		UpdatedAt:        jsonTime(m.UpdatedAt),
	}
}

func seriesDTO(sr core.Series) seriesJSON {
	return seriesJSON{
		ID:               sr.ID,
		TMDBID:           sr.TMDBID,
		TVDBID:           sr.TVDBID,
		IMDBID:           sr.IMDBID,
		Title:            sr.Title,
		SortTitle:        sr.SortTitle,
		Year:             sr.Year,
		Overview:         sr.Overview,
		Status:           sr.Status,
		Path:             sr.Path,
		PosterPath:       sr.PosterPath,
		PosterURL:        sr.PosterURL,
		Monitored:        sr.Monitored,
		QualityProfileID: sr.QualityProfileID,
		FirstAired:       jsonTime(sr.FirstAired),
		AddedAt:          jsonTime(sr.AddedAt),
		UpdatedAt:        jsonTime(sr.UpdatedAt),
	}
}

func mediaFileDTO(f core.MediaFile, profile core.TVProfile) mediaFileJSON {
	return mediaFileJSON{
		ID:           f.ID,
		Path:         f.Path,
		Size:         f.Size,
		Quality:      f.Quality,
		Source:       f.Source,
		Codec:        f.Codec,
		Audio:        f.Audio,
		ReleaseGroup: f.ReleaseGroup,
		AddedAt:      jsonTime(f.AddedAt),
		ModifiedAt:   jsonTime(f.ModifiedAt),

		Compatibility: compatibilityDTO(profile.Check(core.MediaTags{
			Codec: f.Codec,
			Audio: f.Audio,
			// The container is the file's own extension, which is the one
			// technical fact about an imported file that needs no parser.
			Container: parse.Container(f.Path),
			Quality:   f.Quality,
		})),
	}
}

// firstFileDTO renders the current file for an item, or nil when it has none.
// Several rows for one item can only mean a half-reconciled database, and the
// first by path is a deterministic choice rather than an arbitrary one.
func firstFileDTO(files []core.MediaFile, profile core.TVProfile) *mediaFileJSON {
	if len(files) == 0 {
		return nil
	}
	dto := mediaFileDTO(files[0], profile)
	return &dto
}

// addRequest is the body of POST /library/movies and POST /library/series.
//
// The two search flags are optional and endpoint-specific: movies read
// SearchNow, series read SearchMissing. Omitting them is the historical
// behaviour — add and wait for the next backlog sweep.
type addRequest struct {
	TMDBID int64 `json:"tmdb_id"`
	// SearchNow queues the new movie's automatic search straight after the
	// add. A movie that was just added has no file, so there is nothing to
	// check it against the wanted list for.
	SearchNow bool `json:"search_now"`
	// SearchMissing queues a search for every wanted episode of the new
	// series.
	SearchMissing bool `json:"search_missing"`
}

func (s *server) handleListMovies(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	movies, err := s.st.ListMovies(ctx)
	if err != nil {
		s.writeStoreError(w, "list movies", err)
		return
	}
	// One query for every file, bucketed in memory, rather than one query per
	// movie: the list endpoint is the library's front page.
	files, err := s.st.ListMediaFiles(ctx)
	if err != nil {
		s.writeStoreError(w, "list media files", err)
		return
	}
	byMovie := make(map[int64][]core.MediaFile, len(movies))
	for _, f := range files {
		if f.MovieID != 0 {
			byMovie[f.MovieID] = append(byMovie[f.MovieID], f)
		}
	}

	profile := s.activeTVProfile(ctx)
	out := make([]movieJSON, 0, len(movies))
	for _, m := range movies {
		dto := movieDTO(m)
		dto.File = firstFileDTO(byMovie[m.ID], profile)
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, map[string]any{"movies": out})
}

func (s *server) handleAddMovie(w http.ResponseWriter, r *http.Request) {
	var body addRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.TMDBID <= 0 {
		writeError(w, http.StatusBadRequest, "tmdb_id is required")
		return
	}

	m, err := s.mgr.AddMovie(r.Context(), body.TMDBID)
	if err != nil {
		s.writeManagerError(w, "add movie", err)
		return
	}
	if body.SearchNow {
		if _, err := s.enqueueMovieSearch(r.Context(), m.ID); err != nil {
			// The movie is in the library; failing the request now would tell
			// the client the opposite. The add stands and the missed search is
			// logged — the next backlog sweep queues it anyway.
			s.log.Error("queue search for added movie", "movie_id", m.ID, "error", err)
		}
	}
	writeJSON(w, http.StatusCreated, movieDTO(*m))
}

func (s *server) handleGetMovie(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	dto, err := s.movieDetail(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get movie", err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

// movieDetail assembles one movie with its file. It is shared by GET and
// PATCH so both answer with the identical shape.
func (s *server) movieDetail(ctx context.Context, id int64) (movieJSON, error) {
	m, err := s.st.GetMovie(ctx, id)
	if err != nil {
		return movieJSON{}, err
	}
	files, err := s.st.ListMediaFilesForMovie(ctx, id)
	if err != nil {
		return movieJSON{}, err
	}
	profile := s.activeTVProfile(ctx)
	dto := movieDTO(*m)
	dto.File = firstFileDTO(files, profile)
	return dto, nil
}

// itemPatchRequest is the body of the movie and series PATCH endpoints. Both
// fields are optional pointers so "absent" and "set to the zero value" are
// distinguishable: quality_profile_id 0 explicitly re-assigns the default
// profile. A PATCH that names no field is a client bug worth reporting.
type itemPatchRequest struct {
	Monitored        *bool  `json:"monitored"`
	QualityProfileID *int64 `json:"quality_profile_id"`
}

// applyItemPatch validates the patch against the store and reports whether it
// named at least one field. A nonexistent profile is a 400, not a 404: the
// error is in the request, not in the addressed item.
func (s *server) applyItemPatch(w http.ResponseWriter, r *http.Request, body itemPatchRequest, apply func(monitored *bool, profileID int64)) bool {
	if body.Monitored == nil && body.QualityProfileID == nil {
		writeError(w, http.StatusBadRequest, "monitored or quality_profile_id is required")
		return false
	}
	profileID := int64(-1)
	if body.QualityProfileID != nil {
		profileID = *body.QualityProfileID
		if profileID < 0 {
			writeError(w, http.StatusBadRequest, "invalid quality_profile_id")
			return false
		}
		if profileID > 0 {
			if _, err := s.st.GetQualityProfile(r.Context(), profileID); err != nil {
				writeError(w, http.StatusBadRequest, "unknown quality_profile_id")
				return false
			}
		}
	}
	apply(body.Monitored, profileID)
	return true
}

// handlePatchMovie updates the mutable fields of a movie: the monitored flag
// and the quality profile assignment (PLAN phase 3, task 1). Everything else
// is provider metadata, refreshed by a scan rather than edited by hand.
func (s *server) handlePatchMovie(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body itemPatchRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()

	m, err := s.st.GetMovie(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get movie", err)
		return
	}
	if !s.applyItemPatch(w, r, body, func(monitored *bool, profileID int64) {
		if monitored != nil {
			m.Monitored = *monitored
		}
		if profileID >= 0 {
			m.QualityProfileID = profileID
		}
	}) {
		return
	}
	if err := s.st.UpsertMovie(ctx, m); err != nil {
		s.writeStoreError(w, "update movie", err)
		return
	}

	dto, err := s.movieDetail(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get movie", err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

// deleteFilesRequested reads the ?files=true switch the delete endpoints take.
//
// It is a query parameter rather than a body field because a DELETE body is
// not reliably forwarded by proxies or sent by clients, and the default has to
// be the safe one: anything but an explicit "true" leaves the files alone.
func deleteFilesRequested(r *http.Request) bool {
	return r.URL.Query().Get("files") == "true"
}

// handleDeleteMovie removes the library record, and with ?files=true the
// movie's files on disk as well.
//
// Without the switch the filesystem is untouched: it is the source of truth
// (SPEC §1.2), so deleting a movie means "stop tracking it" and a rescan would
// re-add it. Deleting the files is the way to say the other thing.
func (s *server) handleDeleteMovie(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	// Read first so a delete of something that never existed is a 404 rather
	// than a silent success.
	if _, err := s.st.GetMovie(r.Context(), id); err != nil {
		s.writeStoreError(w, "get movie", err)
		return
	}
	if err := s.mgr.RemoveMovie(r.Context(), id, deleteFilesRequested(r)); err != nil {
		s.writeManagerError(w, "delete movie", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *server) handleListSeries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	series, err := s.st.ListSeries(ctx)
	if err != nil {
		s.writeStoreError(w, "list series", err)
		return
	}
	counts, err := s.st.EpisodeCountsBySeries(ctx)
	if err != nil {
		s.writeStoreError(w, "count episodes", err)
		return
	}

	out := make([]seriesJSON, 0, len(series))
	for _, sr := range series {
		dto := seriesDTO(sr)
		dto.EpisodeCount = counts[sr.ID].Total
		dto.EpisodeFileCount = counts[sr.ID].WithFile
		out = append(out, dto)
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": out})
}

func (s *server) handleAddSeries(w http.ResponseWriter, r *http.Request) {
	var body addRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.TMDBID <= 0 {
		writeError(w, http.StatusBadRequest, "tmdb_id is required")
		return
	}

	sr, err := s.mgr.AddSeries(r.Context(), body.TMDBID)
	if err != nil {
		s.writeManagerError(w, "add series", err)
		return
	}
	if body.SearchMissing {
		// Episode rows exist by the time AddSeries returns, so the wanted list
		// already names them. See handleAddMovie for why a failure here does
		// not fail the add.
		if _, err := s.queueSeriesSearch(r.Context(), sr.ID); err != nil {
			s.log.Error("queue search for added series", "series_id", sr.ID, "error", err)
		}
	}
	writeJSON(w, http.StatusCreated, seriesDTO(*sr))
}

func (s *server) handleGetSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	dto, err := s.seriesDetail(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get series", err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

// seriesDetail assembles one series with its season/episode tree, shared by
// GET and PATCH so both answer with the identical shape.
func (s *server) seriesDetail(ctx context.Context, id int64) (seriesDetailJSON, error) {
	sr, err := s.st.GetSeries(ctx, id)
	if err != nil {
		return seriesDetailJSON{}, err
	}
	seasons, err := s.seasonDetail(ctx, id)
	if err != nil {
		return seriesDetailJSON{}, err
	}

	dto := seriesDTO(*sr)
	for _, season := range seasons {
		dto.EpisodeCount += len(season.Episodes)
		for _, e := range season.Episodes {
			if e.File != nil {
				dto.EpisodeFileCount++
			}
		}
	}
	return seriesDetailJSON{seriesJSON: dto, Seasons: seasons}, nil
}

// monitorRequest is the body of the PATCH endpoints. Monitored is a pointer so
// "absent" and "false" are distinguishable: a PATCH that names no field is a
// client bug worth reporting, not a silent no-op.
type monitorRequest struct {
	Monitored *bool `json:"monitored"`
}

func (s *server) handlePatchSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body itemPatchRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	ctx := r.Context()

	sr, err := s.st.GetSeries(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get series", err)
		return
	}
	cascade := false
	if !s.applyItemPatch(w, r, body, func(monitored *bool, profileID int64) {
		if monitored != nil {
			sr.Monitored = *monitored
			cascade = true
		}
		if profileID >= 0 {
			sr.QualityProfileID = profileID
		}
	}) {
		return
	}
	if err := s.st.UpsertSeries(ctx, sr); err != nil {
		s.writeStoreError(w, "update series", err)
		return
	}
	// SPEC §7: the series flag cascades down to every season and episode as a
	// bulk update. Children can be toggled back individually afterwards.
	if cascade {
		if err := s.st.CascadeSeriesMonitored(ctx, id, sr.Monitored); err != nil {
			s.writeStoreError(w, "cascade monitored flag", err)
			return
		}
	}

	dto, err := s.seriesDetail(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get series", err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

// handleDeleteSeries is handleDeleteMovie's twin: untrack by default, and with
// ?files=true every episode file of the series goes from disk too. Untracking
// takes the seasons, episodes and episode-file links with it by cascade.
func (s *server) handleDeleteSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}

	if _, err := s.st.GetSeries(r.Context(), id); err != nil {
		s.writeStoreError(w, "get series", err)
		return
	}
	if err := s.mgr.RemoveSeries(r.Context(), id, deleteFilesRequested(r)); err != nil {
		s.writeManagerError(w, "delete series", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePatchSeason updates one season's monitored flag and cascades it to
// the season's episodes (SPEC §7, PLAN phase 3 task 2: a bulk update, not a
// lock, so individual episodes can be toggled back afterwards).
func (s *server) handlePatchSeason(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	number, err := strconv.Atoi(r.PathValue("season"))
	if err != nil || number < 0 {
		writeError(w, http.StatusBadRequest, "invalid season number")
		return
	}
	var body monitorRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Monitored == nil {
		writeError(w, http.StatusBadRequest, "monitored is required")
		return
	}
	ctx := r.Context()

	seasons, err := s.st.ListSeasons(ctx, id)
	if err != nil {
		s.writeStoreError(w, "list seasons", err)
		return
	}
	for _, season := range seasons {
		if season.Number != number {
			continue
		}
		season.Monitored = *body.Monitored
		if err := s.st.UpsertSeason(ctx, &season); err != nil {
			s.writeStoreError(w, "update season", err)
			return
		}
		if err := s.st.CascadeSeasonMonitored(ctx, id, number, *body.Monitored); err != nil {
			s.writeStoreError(w, "cascade monitored flag", err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeError(w, http.StatusNotFound, "not found")
}

func (s *server) handlePatchEpisode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body monitorRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if body.Monitored == nil {
		writeError(w, http.StatusBadRequest, "monitored is required")
		return
	}
	ctx := r.Context()

	e, err := s.st.GetEpisode(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get episode", err)
		return
	}
	e.Monitored = *body.Monitored
	if err := s.st.UpsertEpisode(ctx, e); err != nil {
		s.writeStoreError(w, "update episode", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// seasonDetail builds the season/episode tree for a series detail response.
//
// Episodes are looked up by season number rather than by season row id (that
// is how core models them), and an episode whose season row is missing is
// surfaced under a placeholder season instead of being dropped: a partially
// reconciled database must not hide files from the user.
func (s *server) seasonDetail(ctx context.Context, seriesID int64) ([]seasonJSON, error) {
	seasons, err := s.st.ListSeasons(ctx, seriesID)
	if err != nil {
		return nil, err
	}
	episodes, err := s.st.ListEpisodes(ctx, seriesID)
	if err != nil {
		return nil, err
	}

	profile := s.activeTVProfile(ctx)
	byNumber := make(map[int][]episodeJSON)
	for _, e := range episodes {
		files, err := s.st.ListMediaFilesForEpisode(ctx, e.ID)
		if err != nil {
			return nil, err
		}
		byNumber[e.SeasonNumber] = append(byNumber[e.SeasonNumber], episodeJSON{
			ID:            e.ID,
			SeriesID:      e.SeriesID,
			SeasonNumber:  e.SeasonNumber,
			EpisodeNumber: e.EpisodeNumber,
			TMDBID:        e.TMDBID,
			Title:         e.Title,
			Overview:      e.Overview,
			AirDate:       jsonTime(e.AirDate),
			Monitored:     e.Monitored,
			File:          firstFileDTO(files, profile),
		})
	}

	out := make([]seasonJSON, 0, len(seasons))
	haveRow := make(map[int]bool, len(seasons))
	for _, se := range seasons {
		haveRow[se.Number] = true
		out = append(out, seasonJSON{
			ID:           se.ID,
			SeriesID:     se.SeriesID,
			SeasonNumber: se.Number,
			Title:        se.Title,
			Overview:     se.Overview,
			PosterPath:   se.PosterPath,
			AirDate:      jsonTime(se.AirDate),
			Monitored:    se.Monitored,
			Episodes:     episodesOf(byNumber, se.Number),
		})
	}
	for number := range byNumber {
		if !haveRow[number] {
			out = append(out, seasonJSON{
				SeriesID:     seriesID,
				SeasonNumber: number,
				Episodes:     byNumber[number],
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].SeasonNumber < out[j].SeasonNumber })
	return out, nil
}

// episodesOf returns a season's episodes, never nil, so the JSON carries an
// empty array rather than null.
func episodesOf(byNumber map[int][]episodeJSON, number int) []episodeJSON {
	if eps := byNumber[number]; eps != nil {
		return eps
	}
	return []episodeJSON{}
}

// handleRescan starts a library scan in the background and returns
// immediately: a scan walks the whole storage root and can take minutes.
// Progress is observable through GET /events and the scanning flag on
// GET /system/status.
func (s *server) handleRescan(w http.ResponseWriter, r *http.Request) {
	if !s.noOpenMigration(w, r, scanBlockedByMigration) {
		return
	}
	if !s.startScan() {
		writeError(w, http.StatusConflict, "scan already running")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "started"})
}

// WithStartupScan queues a library scan as the server is built.
//
// It is how SPEC §10.1's second first-run step — "point Caravan at existing
// media, with a library scan queued immediately" — reaches the deployments that
// never see the first-run screen. Docker sets CARAVAN_STORAGE_ROOT=/data and a
// prepared drive's config says ".", so in both the root exists before the SPA
// ever loads and it routes straight past first run. Without this, a user whose
// /data already held media landed on an empty library with nothing scanned
// until they found Settings → Storage → Rescan for themselves.
//
// The serving process sets it only on the start that first wrote the root, so
// it is a first run in the sense that matters and not a scan on every boot. It
// goes through startScan rather than the manager directly, so the single-flight
// guard and the scanning flag on GET /system/status describe it like any other
// scan.
func WithStartupScan(scan bool) Option {
	return func(s *server) { s.startupScan = scan }
}

// startScan launches a background library scan, reporting false when one was
// already running.
//
// It is the single entry point to the scanning flag: POST /library/rescan turns
// a false into a 409, while the dirty-eject recovery (POST /system/verify)
// treats it as "already being done".
func (s *server) startScan() bool {
	if !s.scanning.CompareAndSwap(false, true) {
		return false
	}
	go func() {
		defer s.scanning.Store(false)
		// The scan outlives its request, so it must not inherit the request's
		// context — that gets cancelled the moment the handler returns.
		if err := s.mgr.Scan(context.Background()); err != nil {
			s.log.Error("library scan failed", "error", err)
		}
	}()
	return true
}

// writeManagerError maps a library-manager failure to a status. A missing
// upstream record (unknown TMDB id, missing queue entry) is a 404, an
// unconfigured metadata provider is a 503 the UI can turn into "add a TMDB API
// key"; everything else is reported as a 502, because the manager's remaining
// failure modes are the metadata provider and the filesystem, not this process.
func (s *server) writeManagerError(w http.ResponseWriter, msg string, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if errors.Is(err, core.ErrNoMetadataProvider) {
		writeError(w, http.StatusServiceUnavailable, "no metadata provider configured")
		return
	}
	s.log.Error(msg, "error", err)
	writeError(w, http.StatusBadGateway, msg)
}
