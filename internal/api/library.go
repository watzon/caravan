package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"

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
	LibraryID        int64  `json:"library_id"`
	ReleaseDate      string `json:"release_date"`
	// MinAvailability is the release stage the movie's automatic search waits
	// for: announced, in_cinemas or released.
	MinAvailability string `json:"min_availability"`
	AddedAt         string `json:"added_at"`
	UpdatedAt       string `json:"updated_at"`
	// File is the imported file, null while the movie is only wanted. A movie
	// has at most one current file in v1 (upgrades replace it, SPEC §9), so
	// this is a single object rather than a list.
	File *mediaFileJSON `json:"file"`
	// Downloading is true while an in-flight grab is fetching this movie.
	Downloading bool `json:"downloading"`
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
	LibraryID        int64  `json:"library_id"`
	// Kind is core.SeriesKindTV, core.SeriesKindAnime or core.SeriesKindAdult.
	// The picker seeds from it: an adult series is a site, and a television
	// seed for one writes season/episode into the box until the search lands.
	// It is also which screen the row belongs to, GET /library/series lists one
	// kind at a time.
	Kind       string `json:"kind"`
	FirstAired string `json:"first_aired"`
	AddedAt    string `json:"added_at"`
	UpdatedAt  string `json:"updated_at"`
	// EpisodeCount and EpisodeFileCount are what "12 / 24" and the
	// downloaded/incomplete status are computed from, so the list endpoint
	// carries them and the client never has to fetch every season to render a
	// poster grid.
	EpisodeCount     int `json:"episode_count"`
	EpisodeFileCount int `json:"episode_file_count"`
	// Downloading is true while an in-flight grab is fetching any episode.
	Downloading bool `json:"downloading"`
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
	// Downloading is true while an in-flight grab is fetching this episode.
	Downloading bool `json:"downloading"`
}

type mediaFileJSON struct {
	ID            int64  `json:"id"`
	Path          string `json:"path"`
	Size          int64  `json:"size"`
	MovieID       int64  `json:"movie_id"`
	SeriesID      int64  `json:"series_id,omitempty"`
	SeriesKind    string `json:"series_kind,omitempty"`
	SeasonNumber  int    `json:"season_number,omitempty"`
	EpisodeNumber int    `json:"episode_number,omitempty"`
	Quality       string `json:"quality"`
	Source        string `json:"source"`
	Codec         string `json:"codec"`
	Audio         string `json:"audio"`
	ReleaseGroup  string `json:"release_group"`
	AddedAt       string `json:"added_at"`
	ModifiedAt    string `json:"modified_at"`
	// Compatibility is the owning item's playback-target verdict on this file.
	// The row carries no bit depth (that is a probe's answer, not a filename's)
	// so a 10-bit file imported from an untagged name reads as unstated rather
	// than as 8-bit.
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
		LibraryID:        m.LibraryID,
		ReleaseDate:      jsonTime(m.ReleaseDate),
		MinAvailability:  m.MinAvailability,
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
		LibraryID:        sr.LibraryID,
		Kind:             sr.Kind,
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
		MovieID:      f.MovieID,
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
// behaviour, add and wait for the next backlog sweep.
type addRequest struct {
	TMDBID int64 `json:"tmdb_id"`
	// Provider and ProviderRef are the general spelling of "which title": the
	// provider that identified it and its id in that provider's own numbering,
	// straight off a search hit. They travel as a pair; see itemRefFrom.
	Provider    string `json:"provider"`
	ProviderRef string `json:"provider_ref"`
	// QualityProfileID is the optional item override. Zero, including an
	// omitted field, leaves the new item to inherit its library or system
	// default.
	QualityProfileID int64 `json:"quality_profile_id"`
	// Monitored is the dialog's "Add and monitor" checkbox.
	//
	// It is a pointer so an explicit opt-in can be distinguished from an
	// omitted field. Absent means unmonitored: old clients and implicit add
	// paths must not start automation. A title already in the library keeps
	// the flag its owner set because a re-add is a metadata refresh.
	Monitored *bool `json:"monitored"`
	// SearchNow queues the new movie's automatic search straight after the
	// add. A movie that was just added has no file, so there is nothing to
	// check it against the wanted list for.
	SearchNow bool `json:"search_now"`
	// SearchMissing queues a search for every wanted episode of the new
	// series.
	SearchMissing bool `json:"search_missing"`
	// Seasons names the seasons of a new series this add is going after.
	// Omitting it adds the whole series, which is the historical behaviour;
	// naming a subset leaves every other season unmonitored, and scopes what a
	// pending request for this title counts as granted (see absorbSeriesRequests).
	Seasons []int `json:"seasons"`
	// MinAvailability is the release stage a new movie's automatic search
	// waits for: announced, in_cinemas or released. Movie-only, like Seasons
	// is series-only. Omitting it defaults a new movie to released and leaves
	// a re-added movie's choice alone.
	MinAvailability string `json:"min_availability"`
	// LibraryID is the library a new item lands in. Zero targets the kind's
	// default, and that is a wire convention rather than a stored one: a body
	// that names no shelf means "wherever this kind goes", and the row it
	// creates always names its library outright. A re-added title stays in the
	// library it already lives in whatever this says: a move is an explicit
	// operation, never a side effect of an add.
	LibraryID int64 `json:"library_id"`
}

// itemRefFrom resolves the two spellings of "which title" a body may carry into
// one core.ItemRef, writing the refusal itself. mediaType is the item
// vocabulary the endpoint is about: MediaTypeMovie for the movie routes,
// MediaTypeSeries for the series ones, MediaTypeScene for a scene match.
//
// tmdb_id is the compatibility spelling, and it is what every client written
// before providers were plural still sends; provider + provider_ref is the
// general one, and it is what a search hit from any chain hands back.
//
// The pair travels together or not at all. Half a pair has named a provider
// with nothing to look up, or a ref written in a vocabulary nobody named, and
// there is no safe guess: a ref read in the wrong vocabulary is a different
// title, not a failed lookup, so the item would be pinned to something real and
// wrong.
//
// The provider is validated against the endpoint's vocabulary
// (core.ProviderLooksUp) and not against the target library's chain, nor
// against the library kinds the provider may be chained on. The chain governs
// identification (which providers are asked when Caravan has to work out what a
// file is) and this is the user telling it the answer outright. A ref pasted
// from a provider that is not on the chain is still a true ref, and refusing it
// would quietly turn the chain into a second allow-list nobody asked for.
//
// Asking the registry's chain kinds instead is the mistake the two-function
// split exists to prevent: a provider whose catalogue files films would have to
// claim a movie library kind to have its films added, and the claim would then
// offer it in every Movies library's chain editor.
//
// existence is a different question from membership, and it is asked: a ref
// naming a stash-box instance this install does not hold is a ref nothing can
// ever be refreshed against, so it is refused here rather than pinned to a row
// and discovered on the next refresh (see knownProviderInstance). That is why
// this takes a context and hangs off the server.
func (s *server) itemRefFrom(ctx context.Context, w http.ResponseWriter, provider, providerRef string, tmdbID int64, mediaType string) (core.ItemRef, bool) {
	provider = strings.TrimSpace(provider)
	providerRef = strings.TrimSpace(providerRef)

	switch {
	case provider != "" && providerRef != "":
		if !core.ProviderLooksUp(provider, mediaType) {
			writeError(w, http.StatusBadRequest, "provider does not serve this kind of item")
			return core.ItemRef{}, false
		}
		known, err := s.knownProviderInstance(ctx, provider)
		if err != nil {
			s.writeStoreError(w, "get stash-box instance", err)
			return core.ItemRef{}, false
		}
		if !known {
			writeError(w, http.StatusBadRequest, "no stash-box instance named "+provider+" is configured")
			return core.ItemRef{}, false
		}
		return core.ItemRef{Provider: provider, Ref: providerRef}, true
	case provider != "" || providerRef != "":
		writeError(w, http.StatusBadRequest, "provider and provider_ref must be sent together")
		return core.ItemRef{}, false
	case tmdbID > 0:
		return core.TMDBRef(tmdbID), true
	}
	writeError(w, http.StatusBadRequest, "tmdb_id or provider/provider_ref is required")
	return core.ItemRef{}, false
}

// moveRequest is the body of the two move endpoints: the target library.
type moveRequest struct {
	LibraryID int64 `json:"library_id"`
}

// handleMoveMovie queues a movie's move into another library. 202 rather than
// 200: the transfer is a durable job, because a move is file I/O the request
// must not own. The validation happens now, while the user is watching. The job
// re-checks, but a target of the wrong kind should be a 400 today, not a failed
// job tomorrow.
func (s *server) handleMoveMovie(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body moveRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	if _, err := s.st.GetMovie(r.Context(), id); err != nil {
		s.writeStoreError(w, "get movie", err)
		return
	}
	if !s.validMoveTarget(w, r, body.LibraryID, core.LibraryKindMovie) {
		return
	}
	s.enqueueMove(w, r, core.MediaTypeMovie, id, body.LibraryID)
}

// handleMoveSeries is handleMoveMovie's series twin, covering adult sites
// too: the series must be visible to the caller, and the target must speak
// the series' kind.
func (s *server) handleMoveSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body moveRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	sr, ok := s.getVisibleSeries(w, r, id)
	if !ok {
		return
	}
	if !s.validMoveTarget(w, r, body.LibraryID, core.LibraryKindForSeries(sr.Kind)) {
		return
	}
	s.enqueueMove(w, r, core.MediaTypeSeries, id, body.LibraryID)
}

func (s *server) validMoveTarget(w http.ResponseWriter, r *http.Request, libraryID int64, kind string) bool {
	if libraryID <= 0 {
		writeError(w, http.StatusBadRequest, "library_id is required")
		return false
	}
	lib, ok := s.visibleLibrary(w, r, libraryID)
	if !ok {
		return false
	}
	// core.LibraryKindAccepts rather than equality: an anime library holds films
	// and television series alike, and a television library takes a row back off
	// the anime shelf. The job re-checks with the same rule (library.MoveSeries),
	// which is where `series.kind` is rewritten to match the destination.
	if !core.LibraryKindAccepts(lib.Kind, kind) {
		writeError(w, http.StatusBadRequest, "library holds a different kind of item")
		return false
	}
	return true
}

func (s *server) enqueueMove(w http.ResponseWriter, r *http.Request, itemType string, itemID, libraryID int64) {
	if _, err := s.enqueueSearchJob(r.Context(), core.JobMoveItem, core.JobMoveItemPayload{
		ItemType: itemType, ItemID: itemID, LibraryID: libraryID,
	}); err != nil {
		s.writeStoreError(w, "queue move", err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "moving"})
}

// validAddLibraryID validates an add's library target, writing the refusal
// itself. Zero names the kind's default and is always fine (the request
// convention, see addRequest.LibraryID) while a real id must exist, be visible
// to this caller, and hold items of the endpoint's kind.
func (s *server) validAddLibraryID(w http.ResponseWriter, r *http.Request, libraryID int64, kind string) bool {
	if libraryID < 0 {
		writeError(w, http.StatusBadRequest, "invalid library_id")
		return false
	}
	if libraryID == 0 {
		return true
	}
	lib, ok := s.visibleLibrary(w, r, libraryID)
	if !ok {
		return false
	}
	// The acceptance rule, not equality: a movie add may target a movie or an
	// anime library, a series add a tv or an anime one (core.LibraryKindAccepts).
	if !core.LibraryKindAccepts(lib.Kind, kind) {
		writeError(w, http.StatusBadRequest, "library holds a different kind of item")
		return false
	}
	return true
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

	// The Movies screen's first visibility filter. It changes nothing while
	// every movie library is on and open to everybody, which is the point: the
	// shelf a movie sits on is now allowed to be somebody else's, and this list
	// is where that has to be true before it can be true anywhere.
	gate := s.gate(r)
	out := make([]movieJSON, 0, len(movies))
	for _, m := range movies {
		visible, err := gate.visible(ctx, m.LibraryID)
		if err != nil {
			s.writeStoreError(w, "read library access", err)
			return
		}
		if !visible {
			continue
		}
		profile, err := s.st.ResolveItemPlaybackTargetByLibrary(
			ctx,
			m.LibraryID,
			core.LibraryKindMovie,
			m.QualityProfileID,
		)
		if err != nil {
			s.writeStoreError(w, "resolve movie playback target", err)
			return
		}
		dto := movieDTO(m)
		dto.File = firstFileDTO(byMovie[m.ID], profile)
		out = append(out, dto)
	}
	if err := s.markDownloadingMovies(ctx, out); err != nil {
		s.writeStoreError(w, "read grab history", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"movies": out})
}

func (s *server) handleAddMovie(w http.ResponseWriter, r *http.Request) {
	var body addRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	ref, ok := s.itemRefFrom(r.Context(), w, body.Provider, body.ProviderRef, body.TMDBID, MediaTypeMovie)
	if !ok {
		return
	}
	if len(body.Seasons) > 0 {
		writeError(w, http.StatusBadRequest, "seasons are only valid for a series")
		return
	}
	if !validAvailability(w, body.MinAvailability) {
		return
	}
	if !s.validQualityProfileID(w, r, body.QualityProfileID) {
		return
	}
	if !s.validAddLibraryID(w, r, body.LibraryID, core.LibraryKindMovie) {
		return
	}

	m, err := s.addMovieToLibrary(r.Context(), ref, body.SearchNow, body.MinAvailability, body.Monitored, body.QualityProfileID, body.LibraryID)
	if err != nil {
		s.writeManagerError(w, ref.Provider, "add movie", err)
		return
	}
	writeJSON(w, http.StatusCreated, movieDTO(*m))
}

// validAvailability rejects an availability that names no known stage, writing
// the failure itself. Empty is fine, it means "no opinion" everywhere the field
// appears.
func validAvailability(w http.ResponseWriter, s string) bool {
	if s != "" && !core.ValidAvailability(s) {
		writeError(w, http.StatusBadRequest, "min_availability must be announced, in_cinemas or released")
		return false
	}
	return true
}

// validQualityProfileID validates the item override used by add and PATCH
// requests. Zero deliberately names no override, so the item inherits the
// library or system default.
func (s *server) validQualityProfileID(w http.ResponseWriter, r *http.Request, profileID int64) bool {
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
	return true
}

// addMovieToLibrary is the single path a movie enters the library through from
// the HTTP layer: the add button and approving a request both come here. That
// is what makes "a pending request is absorbed when its title arrives" true
// however the title arrived, and it is the one place a permission check goes
// when Caravan grows more than one kind of user.
//
// ref is the provider identity to add. Requests are still TMDB-keyed, so the
// absorb step below reads the ref's TMDB id back out of it.
func (s *server) addMovieToLibrary(ctx context.Context, ref core.ItemRef, searchNow bool, minAvailability string, monitored *bool, qualityProfileID, libraryID int64) (*core.Movie, error) {
	m, err := s.mgr.AddMovie(ctx, ref, minAvailability, monitored, libraryID)
	if err != nil {
		return nil, err
	}
	if qualityProfileID > 0 {
		if err := s.st.SetMovieQualityProfile(ctx, m.ID, qualityProfileID); err != nil {
			return nil, err
		}
		m.QualityProfileID = qualityProfileID
	}
	s.absorbRequests(ctx, MediaTypeMovie, ref.TMDBID())
	if searchNow {
		if _, err := s.enqueueMovieSearch(ctx, m.ID); err != nil {
			// The movie is in the library; failing the request now would tell
			// the client the opposite. The add stands and the missed search is
			// logged. The next backlog sweep queues it anyway.
			s.log.Error("queue search for added movie", "movie_id", m.ID, "error", err)
		}
	}
	return m, nil
}

// addSeriesToLibrary is addMovieToLibrary's series twin.
//
// seasons, when non-nil, narrows the add to those season numbers: everything
// else the provider knows about lands unmonitored. It is the same season
// selection the add dialog shows, and it is what makes a partial add absorb
// only the part of a pending request it actually granted.
func (s *server) addSeriesToLibrary(ctx context.Context, ref core.ItemRef, searchMissing bool, seasons []int, monitored *bool, qualityProfileID, libraryID int64) (*core.Series, error) {
	sr, err := s.mgr.AddSeries(ctx, ref, monitored, libraryID)
	if err != nil {
		return nil, err
	}
	if qualityProfileID > 0 {
		if err := s.st.SetSeriesQualityProfile(ctx, sr.ID, qualityProfileID); err != nil {
			return nil, err
		}
		sr.QualityProfileID = qualityProfileID
	}
	ungranted, err := s.applySeasonSelection(ctx, sr.ID, seasons)
	if err != nil {
		return nil, err
	}
	s.absorbSeriesRequests(ctx, ref.TMDBID(), ungranted)
	if searchMissing {
		// Episode rows exist by the time AddSeries returns, so the wanted list
		// already names them. See addMovieToLibrary for why a failure here does
		// not fail the add.
		if _, err := s.queueSeriesSearch(ctx, sr.ID); err != nil {
			s.log.Error("queue search for added series", "series_id", sr.ID, "error", err)
		}
	}
	return sr, nil
}

// absorbRequests marks any pending request for a title approved, because the
// title has just reached the library and there is nothing left to ask for.
//
// It never fails the caller: the add already happened, and a request row left
// saying "pending" is a cosmetic wrong, not a reason to tell the client the
// add did not work.
//
// A title added by a non-TMDB ref has no TMDB id, and requests are still
// TMDB-keyed, so there is nothing it could have granted. Asking the store about
// id 0 would be a query for rows that cannot exist and, worse, one that any
// malformed request row carrying a zero would answer.
func (s *server) absorbRequests(ctx context.Context, mediaType string, tmdbID int64) {
	if tmdbID == 0 {
		return
	}
	n, err := s.st.ApproveRequestsFor(ctx, mediaType, tmdbID)
	if err != nil {
		s.log.Error("absorb requests", "media_type", mediaType, "tmdb_id", tmdbID, "error", err)
		return
	}
	if n > 0 {
		s.log.Info("requests absorbed by library add",
			"media_type", mediaType, "tmdb_id", tmdbID, "requests", n)
	}
}

// absorbSeriesRequests is absorbRequests for a series add, told which seasons
// the add left behind.
//
// A request the add covered in full is approved like any other. One that also
// asked for a season nobody went after is narrowed to that remainder and stays
// pending: closing it would throw away the ask with no record that only part of
// it was granted.
func (s *server) absorbSeriesRequests(ctx context.Context, tmdbID int64, ungranted []int) {
	// See absorbRequests: no TMDB id, nothing a request could have asked for.
	if tmdbID == 0 {
		return
	}
	if len(ungranted) == 0 {
		s.absorbRequests(ctx, MediaTypeSeries, tmdbID)
		return
	}
	n, err := s.st.GrantRequestSeasons(ctx, tmdbID, ungranted)
	if err != nil {
		s.log.Error("absorb requests", "media_type", MediaTypeSeries, "tmdb_id", tmdbID, "error", err)
		return
	}
	if n > 0 {
		s.log.Info("requests absorbed by library add",
			"media_type", MediaTypeSeries, "tmdb_id", tmdbID, "requests", n)
	}
}

// applySeasonSelection unmonitors every season of a freshly added series the
// caller did not ask for, and reports those season numbers.
//
// A nil selection is "the whole series": nothing is touched and nothing is
// outstanding. The monitored flag is the only lever here. The rows have to
// exist either way, because the series view shows what is missing.
func (s *server) applySeasonSelection(ctx context.Context, seriesID int64, seasons []int) ([]int, error) {
	if seasons == nil {
		return nil, nil
	}
	rows, err := s.st.ListSeasons(ctx, seriesID)
	if err != nil {
		return nil, err
	}

	var ungranted []int
	for _, season := range rows {
		if slices.Contains(seasons, season.Number) {
			continue
		}
		ungranted = append(ungranted, season.Number)
		if !season.Monitored {
			continue
		}
		season.Monitored = false
		if err := s.st.UpsertSeason(ctx, &season); err != nil {
			return nil, err
		}
		if err := s.st.CascadeSeasonMonitored(ctx, seriesID, season.Number, false); err != nil {
			return nil, err
		}
	}
	return ungranted, nil
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
	profile, err := s.st.ResolveItemPlaybackTargetByLibrary(
		ctx,
		m.LibraryID,
		core.LibraryKindMovie,
		m.QualityProfileID,
	)
	if err != nil {
		return movieJSON{}, err
	}
	dto := movieDTO(*m)
	dto.File = firstFileDTO(files, profile)
	downloading, _, err := s.downloadingCalendarItems(ctx, []int64{id}, nil)
	if err != nil {
		return movieJSON{}, err
	}
	dto.Downloading = downloading[id]
	return dto, nil
}

func (s *server) markDownloadingMovies(ctx context.Context, movies []movieJSON) error {
	if len(movies) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(movies))
	for _, movie := range movies {
		ids = append(ids, movie.ID)
	}
	downloading, _, err := s.downloadingCalendarItems(ctx, ids, nil)
	if err != nil {
		return err
	}
	for i := range movies {
		movies[i].Downloading = downloading[movies[i].ID]
	}
	return nil
}

func (s *server) markDownloadingSeries(ctx context.Context, series []seriesJSON) error {
	if len(series) == 0 {
		return nil
	}
	ids := make([]int64, 0, len(series))
	for _, sr := range series {
		ids = append(ids, sr.ID)
	}
	grabs, err := s.st.ListGrabsForSeriesIDs(ctx, ids)
	if err != nil {
		return err
	}
	_, _, bySeries, err := s.activeTargetsFromGrabs(ctx, grabs)
	if err != nil {
		return err
	}
	for i := range series {
		series[i].Downloading = bySeries[series[i].ID]
	}
	return nil
}

// itemPatchRequest is the body of the movie and series PATCH endpoints. The
// pointer fields are optional pointers so "absent" and "set to the zero value"
// are distinguishable: quality_profile_id 0 explicitly re-assigns the default
// profile. A PATCH that names no field is a client bug worth reporting.
type itemPatchRequest struct {
	Monitored        *bool  `json:"monitored"`
	QualityProfileID *int64 `json:"quality_profile_id"`
	// MinAvailability is movie-only, like it is on the add: the release stage
	// the movie's automatic search waits for. Empty means "not changing it".
	// There is no unset state to spell, the store always holds a stage.
	MinAvailability string `json:"min_availability"`
}

// applyItemPatch validates the patch against the store and reports whether it
// named at least one field. A nonexistent profile is a 400, not a 404: the
// error is in the request, not in the addressed item.
func (s *server) applyItemPatch(w http.ResponseWriter, r *http.Request, body itemPatchRequest, apply func(monitored *bool, profileID int64)) bool {
	if body.Monitored == nil && body.QualityProfileID == nil && body.MinAvailability == "" {
		writeError(w, http.StatusBadRequest, "monitored, quality_profile_id or min_availability is required")
		return false
	}
	profileID := int64(-1)
	if body.QualityProfileID != nil {
		profileID = *body.QualityProfileID
		if !s.validQualityProfileID(w, r, profileID) {
			return false
		}
	}
	apply(body.Monitored, profileID)
	return true
}

// handlePatchMovie updates the mutable fields of a movie: the monitored flag,
// the quality profile assignment and the minimum availability. Everything else
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
	if !validAvailability(w, body.MinAvailability) {
		return
	}
	ctx := r.Context()

	m, err := s.st.GetMovie(ctx, id)
	if err != nil {
		s.writeStoreError(w, "get movie", err)
		return
	}
	selectedProfileID := int64(-1)
	if !s.applyItemPatch(w, r, body, func(monitored *bool, profileID int64) {
		if monitored != nil {
			m.Monitored = *monitored
		}
		if profileID >= 0 {
			selectedProfileID = profileID
			if profileID == 0 {
				m.QualityProfileID = 0
			}
		}
	}) {
		return
	}
	if body.MinAvailability != "" {
		m.MinAvailability = body.MinAvailability
	}
	if err := s.st.UpsertMovie(ctx, m); err != nil {
		s.writeStoreError(w, "update movie", err)
		return
	}
	if selectedProfileID > 0 {
		if err := s.st.SetMovieQualityProfile(ctx, id, selectedProfileID); err != nil {
			s.writeStoreError(w, "set movie quality profile", err)
			return
		}
		m.QualityProfileID = selectedProfileID
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
	grab, active, err := s.st.ActiveGrabForMovie(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "find active grab", err)
		return
	}
	var grabs []*core.Grab
	if active {
		grabs = append(grabs, grab)
	}
	if !s.cancelGrabs(w, r.Context(), grabs) {
		return
	}
	if err := s.mgr.RemoveMovie(r.Context(), id, deleteFilesRequested(r)); err != nil {
		s.writeManagerError(w, "", "delete movie", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// cancelGrabs withdraws the in-flight downloads of an item being removed, and
// reports whether the removal may proceed.
//
// The order is the contract: cancel first, then delete, so an engine that
// cannot be reached fails the request with the library untouched, deleting the
// item while its download kept running is exactly what removal must not do. The
// downloaded data goes with the download: partial data for an item that is
// leaving the library has no future. A configured-but-absent engine is
// tolerated when it has nothing to withdraw; the rows are still cleaned up,
// since nothing can be downloading through an engine that is not there.
func (s *server) cancelGrabs(w http.ResponseWriter, ctx context.Context, grabs []*core.Grab) bool {
	var engine core.Engine
	if s.engine != nil {
		engine = s.engine.Engine()
	}
	for _, g := range grabs {
		downloads, err := s.st.ListDownloadsForGrab(ctx, g.GrabID)
		if err != nil {
			s.writeStoreError(w, "list grab downloads", err)
			return false
		}
		for _, d := range downloads {
			if engine != nil {
				if err := engine.Remove(ctx, d.EngineID, true); err != nil {
					s.writeEngineError(w, "cancel download", err)
					return false
				}
			}
			if err := s.st.DeleteDownloadByEngineID(ctx, d.EngineID); err != nil {
				s.writeStoreError(w, "delete download", err)
				return false
			}
		}
		if err := s.st.SetGrabStatus(ctx, g.GrabID, core.GrabStatusCancelled, "removed from library"); err != nil {
			s.writeStoreError(w, "cancel grab", err)
			return false
		}
	}
	return true
}

// handleListSeries answers the Series and Anime screens: one kind of series per
// request, named by ?kind= and defaulting to television.
//
// The filter is not a convenience. A site is stored as a series row, and this
// route is not an adult surface (sites have their own, behind the /adult gate)
// so an unfiltered list would put them on the television shelf for every admin,
// and on an install with the module switched off it would be a visible trace of
// a module that is supposed to be absent. The anime kind joins on the same
// terms: it is its own screen, and a television list that included anime would
// show every row twice across the two.
//
// Only `tv` and `anime` are spellable here. `adult` is refused rather than
// gated, so this route can never become a second door to the adult shelf, and
// an unknown kind is a client mistake rather than an empty list nobody can
// explain.
func (s *server) handleListSeries(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	kind := core.SeriesKindTV
	if asked := r.URL.Query().Get("kind"); asked != "" {
		if asked != core.SeriesKindTV && asked != core.SeriesKindAnime {
			writeError(w, http.StatusBadRequest, "kind must be tv or anime")
			return
		}
		kind = asked
	}

	series, err := s.st.ListSeriesByKind(ctx, kind)
	if err != nil {
		s.writeStoreError(w, "list series", err)
		return
	}
	counts, err := s.st.EpisodeCountsBySeries(ctx)
	if err != nil {
		s.writeStoreError(w, "count episodes", err)
		return
	}

	// The kind filter above says which vocabulary belongs on this shelf; the
	// gate says which shelves this caller has. Both, in that order: a site is
	// not a television series however visible its library is, and a restricted
	// television library is not this caller's however television it is.
	gate := s.gate(r)
	out := make([]seriesJSON, 0, len(series))
	for _, sr := range series {
		visible, err := gate.visible(ctx, sr.LibraryID)
		if err != nil {
			s.writeStoreError(w, "read library access", err)
			return
		}
		if !visible {
			continue
		}
		dto := seriesDTO(sr)
		dto.EpisodeCount = counts[sr.ID].Total
		dto.EpisodeFileCount = counts[sr.ID].WithFile
		out = append(out, dto)
	}
	if err := s.markDownloadingSeries(ctx, out); err != nil {
		s.writeStoreError(w, "read grab history", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"series": out})
}

func (s *server) handleAddSeries(w http.ResponseWriter, r *http.Request) {
	var body addRequest
	if !decodeJSON(w, r, &body) {
		return
	}
	ref, ok := s.itemRefFrom(r.Context(), w, body.Provider, body.ProviderRef, body.TMDBID, MediaTypeSeries)
	if !ok {
		return
	}
	if !validSeasonNumbers(w, body.Seasons) {
		return
	}
	if body.MinAvailability != "" {
		writeError(w, http.StatusBadRequest, "min_availability is only valid for a movie")
		return
	}
	if !s.validQualityProfileID(w, r, body.QualityProfileID) {
		return
	}
	if !s.validAddLibraryID(w, r, body.LibraryID, core.LibraryKindTV) {
		return
	}

	sr, err := s.addSeriesToLibrary(r.Context(), ref, body.SearchMissing, body.Seasons, body.Monitored, body.QualityProfileID, body.LibraryID)
	if err != nil {
		s.writeManagerError(w, ref.Provider, "add series", err)
		return
	}
	writeJSON(w, http.StatusCreated, seriesDTO(*sr))
}

// getVisibleSeries is GetSeries plus its library's access rule, writing the
// refusal itself.
//
// A site is stored as a series row, so every by-id series route can be handed a
// site's id. handleListSeries was narrowed to television for the reason its
// comment gives; the by-id routes need the same rule, or an install that
// enabled the module once and switched it off again still answers GET
// /library/series/{siteID} with the site's title, its root under library/Adult
// and its whole season/episode tree (scene titles and release dates) which the
// SPA then renders as an ordinary television detail page. The same now holds
// for a television library somebody restricted.
//
// The refusal is 404 rather than 403, the answer visibleLibrary and
// requireAdult both give: "this exists and you may not have it" is the worse
// leak on a shelf whose promise is absence.
func (s *server) getVisibleSeries(w http.ResponseWriter, r *http.Request, id int64) (*core.Series, bool) {
	sr, err := s.st.GetSeries(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "get series", err)
		return nil, false
	}
	// The row's own library answers. A site and a television series ask the
	// same question of the same gate: `series.kind` says what the row is, and
	// the library it names says who may see it.
	visible, err := s.gate(r).visible(r.Context(), sr.LibraryID)
	if err != nil {
		s.writeStoreError(w, "read library access", err)
		return nil, false
	}
	if !visible {
		writeError(w, http.StatusNotFound, "not found")
		return nil, false
	}
	return sr, true
}

func (s *server) handleGetSeries(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	sr, ok := s.getVisibleSeries(w, r, id)
	if !ok {
		return
	}

	dto, err := s.seriesDetail(r.Context(), *sr)
	if err != nil {
		s.writeStoreError(w, "get series", err)
		return
	}
	writeJSON(w, http.StatusOK, dto)
}

// seriesDetail assembles one series with its season/episode tree, shared by
// GET and PATCH so both answer with the identical shape.
//
// It takes the row rather than the id so that the caller has already had to go
// through getVisibleSeries to obtain one: a fetch inside here would be a second
// path to the same data with no gate on it.
func (s *server) seriesDetail(ctx context.Context, sr core.Series) (seriesDetailJSON, error) {
	seasons, err := s.seasonDetail(ctx, sr)
	if err != nil {
		return seriesDetailJSON{}, err
	}

	dto := seriesDTO(sr)
	for _, season := range seasons {
		dto.EpisodeCount += len(season.Episodes)
		for _, e := range season.Episodes {
			if e.File != nil {
				dto.EpisodeFileCount++
			}
			if e.Downloading {
				dto.Downloading = true
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
	if body.MinAvailability != "" {
		writeError(w, http.StatusBadRequest, "min_availability is only valid for a movie")
		return
	}
	ctx := r.Context()

	sr, ok := s.getVisibleSeries(w, r, id)
	if !ok {
		return
	}
	cascade := false
	selectedProfileID := int64(-1)
	if !s.applyItemPatch(w, r, body, func(monitored *bool, profileID int64) {
		if monitored != nil {
			sr.Monitored = *monitored
			cascade = true
		}
		if profileID >= 0 {
			selectedProfileID = profileID
			if profileID == 0 {
				sr.QualityProfileID = 0
			}
		}
	}) {
		return
	}
	if err := s.st.UpsertSeries(ctx, sr); err != nil {
		s.writeStoreError(w, "update series", err)
		return
	}
	if selectedProfileID > 0 {
		if err := s.st.SetSeriesQualityProfile(ctx, id, selectedProfileID); err != nil {
			s.writeStoreError(w, "set series quality profile", err)
			return
		}
		sr.QualityProfileID = selectedProfileID
	}
	// SPEC §7: the series flag cascades down to every season and episode as a
	// bulk update. Children can be toggled back individually afterwards.
	if cascade {
		if err := s.st.CascadeSeriesMonitored(ctx, id, sr.Monitored); err != nil {
			s.writeStoreError(w, "cascade monitored flag", err)
			return
		}
	}

	dto, err := s.seriesDetail(ctx, *sr)
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

	if _, ok := s.getVisibleSeries(w, r, id); !ok {
		return
	}
	episodes, err := s.st.ListEpisodes(r.Context(), id)
	if err != nil {
		s.writeStoreError(w, "list episodes", err)
		return
	}
	// A season pack's grab covers several episodes; collect each grab once.
	seen := make(map[int64]bool)
	var grabs []*core.Grab
	for _, e := range episodes {
		grab, active, err := s.st.ActiveGrabForEpisode(r.Context(), e.ID)
		if err != nil {
			s.writeStoreError(w, "find active grab", err)
			return
		}
		if !active || seen[grab.GrabID] {
			continue
		}
		seen[grab.GrabID] = true
		grabs = append(grabs, grab)
	}
	if !s.cancelGrabs(w, r.Context(), grabs) {
		return
	}
	if err := s.mgr.RemoveSeries(r.Context(), id, deleteFilesRequested(r)); err != nil {
		s.writeManagerError(w, "", "delete series", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handlePatchSeason updates one season's monitored flag and cascades it to the
// season's episodes (SPEC §7).
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

	if _, ok := s.getVisibleSeries(w, r, id); !ok {
		return
	}
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
	// An episode's visibility is its series': a scene is an episode of a site,
	// and this route takes an episode id, so without the gate it is the one way
	// left to reach an adult row by id.
	if _, ok := s.getVisibleSeries(w, r, e.SeriesID); !ok {
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
func (s *server) seasonDetail(ctx context.Context, series core.Series) ([]seasonJSON, error) {
	seasons, err := s.st.ListSeasons(ctx, series.ID)
	if err != nil {
		return nil, err
	}
	episodes, err := s.st.ListEpisodes(ctx, series.ID)
	if err != nil {
		return nil, err
	}
	filePairs, err := s.st.ListEpisodeMediaFilesForSeries(ctx, series.ID)
	if err != nil {
		return nil, err
	}
	filesByEpisode := make(map[int64][]core.MediaFile)
	for _, pair := range filePairs {
		filesByEpisode[pair.EpisodeID] = append(filesByEpisode[pair.EpisodeID], pair.File)
	}

	episodeIDs := make([]int64, 0, len(episodes))
	for _, e := range episodes {
		episodeIDs = append(episodeIDs, e.ID)
	}
	_, downloading, err := s.downloadingCalendarItems(ctx, nil, episodeIDs)
	if err != nil {
		return nil, err
	}

	profile, err := s.st.ResolveItemPlaybackTargetByLibrary(
		ctx,
		series.LibraryID,
		core.LibraryKindForSeries(series.Kind),
		series.QualityProfileID,
	)
	if err != nil {
		return nil, err
	}
	byNumber := make(map[int][]episodeJSON)
	for _, e := range episodes {
		files := filesByEpisode[e.ID]
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
			Downloading:   downloading[e.ID],
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
				SeriesID:     series.ID,
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
// It is how SPEC §10.1's second first-run step ("point Caravan at existing
// media, with a library scan queued immediately") reaches the deployments that
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
		// context. That gets cancelled the moment the handler returns.
		if err := s.mgr.Scan(context.Background()); err != nil {
			s.log.Error("library scan failed", "error", err)
		}
	}()
	return true
}

// writeManagerError maps a library-manager failure to a status. A missing
// upstream record (unknown provider id, missing queue entry) is a 404, an
// unconfigured metadata provider is a 503 the UI can turn into "add an API
// key"; everything else is reported as a 502, because the manager's remaining
// failure modes are the metadata provider and the filesystem, not this process.
//
// providerID is whose credential a rejection here would be about. An add and a
// match are pinned to the ref's own provider (see library.AddMovie), so the
// caller knows it exactly; a caller with no provider in the picture (a delete,
// an adult site) passes "" and marks nothing, which noteMetadataFailure
// explains.
func (s *server) writeManagerError(w http.ResponseWriter, providerID, msg string, err error) {
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if errors.Is(err, core.ErrNoMetadataProvider) {
		writeCodedError(w, http.StatusServiceUnavailable, CodeMetadataCredentialAbsent,
			"no metadata provider configured")
		return
	}
	// A key that exists and was refused. Without this the add-movie screen got
	// a raw 502 for the one failure it can actually tell the user how to fix,
	// and the cached credential state never learned that the key had gone bad.
	if s.noteMetadataFailure(providerID, err) {
		writeCodedError(w, http.StatusServiceUnavailable, CodeMetadataCredentialInvalid,
			credentialRejectedMessage(providerID))
		return
	}
	// The adult twin. It is reachable only from behind requireAdult, so it can
	// say plainly that the credential is missing: the caller has already been
	// shown the module exists.
	if errors.Is(err, core.ErrNoAdultProvider) {
		writeCodedError(w, http.StatusServiceUnavailable, CodeAdultCredentialAbsent,
			"no adult metadata provider configured")
		return
	}
	s.log.Error(msg, "error", err)
	writeError(w, http.StatusBadGateway, msg)
}
