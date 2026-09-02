package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/watzon/caravan/internal/core"
	"golang.org/x/sync/errgroup"
)

// Values accepted by GET /discover/browse?type=.
const (
	SourceNetwork = "network"
	SourceStudio  = "studio"
)

// discoverLogoBaseURL is the TMDB image prefix for curated shelf logos.
// w185 is the logo-sized sibling of the poster prefix (w500): sharp on a
// tile, small enough that a full row of them is not a download.
const discoverLogoBaseURL = "https://image.tmdb.org/t/p/w185"

// discoverSource is one curated browse destination: a TV network or a film
// studio, named by its TMDB id.
//
// The lists are server-side constants rather than a table because they are
// editorial, not configuration: they are the shelves the discover screen
// offers, and a user who wants something else searches for it. No title counts
// are carried, TMDB's are the whole catalogue, not what Caravan could get, and
// a number nobody can act on is worse than no number.
type discoverSource struct {
	ID   int64
	Name string
	Type string
	// LogoPath is the TMDB image path ("/abc.png"), empty when the shelf
	// has no artwork. It is rendered as LogoURL on the wire.
	LogoPath string
	// Lockup is true when the mark is a filled badge plus lettering. Flat
	// marks silhouette to ink; a lockup keeps the tri-tone so the letters
	// stay visible against the badge.
	Lockup bool
}

var discoverNetworks = []discoverSource{
	{ID: 213, Name: "Netflix", Type: SourceNetwork, LogoPath: "/wwemzKWzjKYJFfCeiB57q3r4Bcm.png"},
	{ID: 49, Name: "HBO", Type: SourceNetwork, LogoPath: "/tuomPhY2UtuPTqqFnKMVHvSb724.png"},
	{ID: 2552, Name: "Apple TV+", Type: SourceNetwork, LogoPath: "/bngHRFi794mnMq34gfVcm9nDxN1.png"},
	{ID: 2739, Name: "Disney+", Type: SourceNetwork, LogoPath: "/1edZOYAfoyZyZ3rklNSiUpXX30Q.png"},
	{ID: 1024, Name: "Prime Video", Type: SourceNetwork, LogoPath: "/w7HfLNm9CWwRmAMU58udl2L7We7.png"},
	{ID: 453, Name: "Hulu", Type: SourceNetwork, LogoPath: "/pqUTCleNUiTLAVlelGxUgWn1ELh.png"},
	{ID: 3186, Name: "HBO Max", Type: SourceNetwork, LogoPath: "/nmU0UMDJB3dRRQSTUqawzF2Od1a.png"},
	{ID: 4330, Name: "Paramount+", Type: SourceNetwork, LogoPath: "/fi83B1oztoS47xxcemFdPMhIzK.png"},
	{ID: 3353, Name: "Peacock", Type: SourceNetwork, LogoPath: "/gIAcGTjKKr0KOHL5s4O36roJ8p7.png"},
	{ID: 88, Name: "FX", Type: SourceNetwork, LogoPath: "/aexGjtcs42DgRtZh7zOxayiry4J.png"},
	{ID: 174, Name: "AMC", Type: SourceNetwork, LogoPath: "/pmvRmATOCaDykE6JrVoeYxlFHw3.png"},
	{ID: 67, Name: "Showtime", Type: SourceNetwork, LogoPath: "/Allse9kbjiP6ExaQrnSpIhkurEi.png"},
	{ID: 1112, Name: "Crunchyroll", Type: SourceNetwork, LogoPath: "/qqyXcZlJQKlRmAD1TCKV7mGLQlt.png"},
	{ID: 80, Name: "Adult Swim", Type: SourceNetwork, LogoPath: "/tHZPHOLc6iF27G34cAZGPsMtMSy.png"},
	{ID: 4, Name: "BBC One", Type: SourceNetwork, LogoPath: "/uJjcCg3O4DMEjM0xtno9OWFciRP.png"},
	{ID: 71, Name: "The CW", Type: SourceNetwork, LogoPath: "/hEpcdJ4O6eitG9ADSnDXNUrlovS.png"},
	{ID: 56, Name: "Cartoon Network", Type: SourceNetwork, LogoPath: "/c5OC6oVCg6QP4eqzW6XIq17CQjI.png"},
	{ID: 318, Name: "STARZ", Type: SourceNetwork, LogoPath: "/qx3Y9LCaK4mq1ykFuDIfjshlo3U.png"},
}

var discoverStudios = []discoverSource{
	{ID: 41077, Name: "A24", Type: SourceStudio, LogoPath: "/1ZXsGaFPgrgS6ZZGS37AqD5uU12.png"},
	{ID: 174, Name: "Warner Bros.", Type: SourceStudio, LogoPath: "/zhD3hhtKB5qyv7ZeL4uLpNxgMVU.png"},
	{ID: 33, Name: "Universal", Type: SourceStudio, LogoPath: "/8lvHyhjr8oUKOOy2dKXoALWKdp0.png"},
	{ID: 10342, Name: "Studio Ghibli", Type: SourceStudio, LogoPath: "/uFuxPEZRUcBTEiYIxjHJq62Vr77.png"},
	{ID: 4, Name: "Paramount", Type: SourceStudio, LogoPath: "/jay6WcMgagAklUt7i9Euwj1pzTF.png"},
	{ID: 923, Name: "Legendary", Type: SourceStudio, LogoPath: "/5UQsZrfbfG2dYJbx8DxfoTr2Bvu.png"},
	{ID: 420, Name: "Marvel", Type: SourceStudio, LogoPath: "/hUzeosd33nzE5MCNsZxCGEKTXaQ.png", Lockup: true},
	{ID: 3, Name: "Pixar", Type: SourceStudio, LogoPath: "/1TjvGVDMYsj6JBxOAkUHpPEwLf7.png"},
	{ID: 2, Name: "Disney", Type: SourceStudio, LogoPath: "/wdrCwmRnLFJhEoH8GSfymY85KHT.png"},
	{ID: 5, Name: "Columbia", Type: SourceStudio, LogoPath: "/71BqEFAF4V3qjjMPCpLuyJFB9A.png"},
	{ID: 25, Name: "20th Century", Type: SourceStudio, LogoPath: "/nM2MfoMqzJQRiSynsDabOtFKetD.png"},
	{ID: 35, Name: "Lionsgate", Type: SourceStudio, LogoPath: "/wxrHa3nZ1K4zo65p56991INkGo6.png"},
	{ID: 12, Name: "New Line", Type: SourceStudio, LogoPath: "/2ycs64eqV5rqKYHyQK0GVoKGvfX.png"},
	{ID: 21, Name: "MGM", Type: SourceStudio, LogoPath: "/usUnaYV6hQnlVAXP6r4HwrlLFPG.png"},
	{ID: 7, Name: "DreamWorks", Type: SourceStudio, LogoPath: "/zcKhWbxFJ4CohZ9dLBMxmOArTVn.png"},
	{ID: 1, Name: "Lucasfilm", Type: SourceStudio, LogoPath: "/tlVSws0RvvtPBwViUyOFAO0vcQS.png"},
	{ID: 3172, Name: "Blumhouse", Type: SourceStudio, LogoPath: "/rzKluDcRkIwHZK2pHsiT667A2Kw.png"},
	{ID: 6704, Name: "Illumination", Type: SourceStudio, LogoPath: "/fOG2oY4m1YuYTQh4bMqqZkmgOAI.png"},
}

// discoverItemJSON is one provider title decorated with what Caravan knows
// about it. PosterPath is the provider's raw path, kept alongside the rendered
// URL because it is what POST /requests stores.
type discoverItemJSON struct {
	MediaType   string  `json:"media_type"`
	TMDBID      int64   `json:"tmdb_id"`
	Title       string  `json:"title"`
	Year        int     `json:"year"`
	Overview    string  `json:"overview"`
	PosterPath  string  `json:"poster_path"`
	PosterURL   string  `json:"poster_url"`
	BackdropURL string  `json:"backdrop_url"`
	VoteAverage float64 `json:"vote_average"`
	VoteCount   int     `json:"vote_count"`
	// Date is the release date for a movie and the first air date for a
	// series, empty when the provider has none.
	Date string `json:"date"`
	// InLibrary and LibraryID say whether Caravan already tracks this title;
	// LibraryID is 0 when it does not.
	InLibrary bool  `json:"in_library"`
	LibraryID int64 `json:"library_id"`
	// Requested says a pending request names this title.
	Requested bool `json:"requested"`
}

type discoverSourceJSON struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
	// LogoURL is the absolute TMDB image URL, empty when the shelf has no mark.
	LogoURL string `json:"logo_url"`
	Lockup  bool   `json:"lockup"`
}

type discoverHomeResponse struct {
	Trending       []discoverItemJSON   `json:"trending"`
	PopularMovies  []discoverItemJSON   `json:"popular_movies"`
	UpcomingMovies []discoverItemJSON   `json:"upcoming_movies"`
	NowPlaying     []discoverItemJSON   `json:"now_playing"`
	PopularSeries  []discoverItemJSON   `json:"popular_series"`
	UpcomingSeries []discoverItemJSON   `json:"upcoming_series"`
	AiringSeries   []discoverItemJSON   `json:"airing_series"`
	MovieGenres    []discoverNamedJSON  `json:"movie_genres"`
	SeriesGenres   []discoverNamedJSON  `json:"series_genres"`
	Networks       []discoverSourceJSON `json:"networks"`
	Studios        []discoverSourceJSON `json:"studios"`
}

type discoverBrowseResponse struct {
	Source     discoverSourceJSON `json:"source"`
	Page       int                `json:"page"`
	TotalPages int                `json:"total_pages"`
	Items      []discoverItemJSON `json:"items"`
}

type castMemberJSON struct {
	TMDBID     int64  `json:"tmdb_id"`
	Name       string `json:"name"`
	Character  string `json:"character"`
	ProfileURL string `json:"profile_url"`
}

// discoverSeasonJSON is one season of a series being browsed, with the same
// two-part state the title itself carries: is it ours, and has anyone asked.
type discoverSeasonJSON struct {
	SeasonNumber int    `json:"season_number"`
	Title        string `json:"title"`
	Overview     string `json:"overview"`
	PosterURL    string `json:"poster_url"`
	AirDate      string `json:"air_date"`
	EpisodeCount int    `json:"episode_count"`
	InLibrary    bool   `json:"in_library"`
	Requested    bool   `json:"requested"`
}

type discoverTitleResponse struct {
	discoverItemJSON
	Status  string `json:"status"`
	Runtime int    `json:"runtime"`
	// Network is the originating network of a series and the lead production
	// company of a movie; the client labels it accordingly.
	Network string `json:"network"`
	// LastAired is a series' most recent air date, empty for a movie and for a
	// series that has not aired.
	LastAired string `json:"last_aired"`
	// Language is the ISO 639-1 original-language code, empty when unknown.
	Language        string               `json:"language"`
	Genres          []string             `json:"genres"`
	IMDBID          string               `json:"imdb_id"`
	TVDBID          int64                `json:"tvdb_id"`
	Cast            []castMemberJSON     `json:"cast"`
	Recommendations []discoverItemJSON   `json:"recommendations"`
	Seasons         []discoverSeasonJSON `json:"seasons"`
}

// handleDiscoverHome serves the discover landing page: this week's trending
// titles, the movie and series editorial shelves, the two genre vocabularies,
// and the curated network/studio tiles the browse endpoint takes ids from.
//
// The required popularity lists fail the page; extras (upcoming, now playing,
// currently airing, genres) degrade to empty so one missing TMDB list does not
// blank Featured. A limit of 4 keeps the burst inside TMDB's usual 40/10s
// window. Each shelf is two pages, and racing every call at once is how a key
// gets a 429 on first paint.
func (s *server) handleDiscoverHome(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.discovery(w, r)
	if !ok {
		return
	}
	ctx := r.Context()

	var (
		trending       []core.DiscoverItem
		movies         []core.DiscoverItem
		series         []core.DiscoverItem
		upcomingMovies []core.DiscoverItem
		nowPlaying     []core.DiscoverItem
		upcomingSeries []core.DiscoverItem
		airing         []core.DiscoverItem
		movieGenres    []core.DiscoverGenre
		seriesGenres   []core.DiscoverGenre
	)

	g, gctx := errgroup.WithContext(ctx)
	g.SetLimit(4)

	g.Go(func() error {
		var err error
		trending, err = provider.TrendingWeek(gctx)
		if err != nil {
			return fmtDiscoverShelfError("trending", err)
		}
		return nil
	})
	g.Go(func() error {
		var err error
		movies, err = provider.PopularMovies(gctx)
		if err != nil {
			return fmtDiscoverShelfError("popular movies", err)
		}
		return nil
	})
	g.Go(func() error {
		var err error
		series, err = provider.PopularSeries(gctx)
		if err != nil {
			return fmtDiscoverShelfError("popular series", err)
		}
		return nil
	})
	g.Go(func() error {
		items, err := provider.UpcomingMovies(gctx)
		if err != nil {
			s.log.Error("discover extra shelf failed", "shelf", "upcoming movies", "error", err)
			return nil
		}
		upcomingMovies = items
		return nil
	})
	g.Go(func() error {
		items, err := provider.NowPlayingMovies(gctx)
		if err != nil {
			s.log.Error("discover extra shelf failed", "shelf", "now playing", "error", err)
			return nil
		}
		nowPlaying = items
		return nil
	})
	g.Go(func() error {
		items, err := provider.UpcomingSeries(gctx)
		if err != nil {
			s.log.Error("discover extra shelf failed", "shelf", "upcoming series", "error", err)
			return nil
		}
		upcomingSeries = items
		return nil
	})
	g.Go(func() error {
		items, err := provider.AiringSeries(gctx)
		if err != nil {
			s.log.Error("discover extra shelf failed", "shelf", "airing series", "error", err)
			return nil
		}
		airing = items
		return nil
	})
	g.Go(func() error {
		items, err := provider.Genres(gctx, MediaTypeMovie)
		if err != nil {
			s.log.Error("discover extra shelf failed", "shelf", "movie genres", "error", err)
			return nil
		}
		movieGenres = items
		return nil
	})
	g.Go(func() error {
		items, err := provider.Genres(gctx, MediaTypeSeries)
		if err != nil {
			s.log.Error("discover extra shelf failed", "shelf", "series genres", "error", err)
			return nil
		}
		seriesGenres = items
		return nil
	})

	if err := g.Wait(); err != nil {
		s.writeDiscoverError(w, r, discoverShelfName(err), err)
		return
	}

	state, err := s.libraryStateFor(ctx, trending, movies, series, upcomingMovies, nowPlaying, upcomingSeries, airing)
	if err != nil {
		s.writeStoreError(w, "read library state", err)
		return
	}
	writeJSON(w, http.StatusOK, discoverHomeResponse{
		Trending:       state.decorateAll(trending),
		PopularMovies:  state.decorateAll(movies),
		UpcomingMovies: state.decorateAll(upcomingMovies),
		NowPlaying:     state.decorateAll(nowPlaying),
		PopularSeries:  state.decorateAll(series),
		UpcomingSeries: state.decorateAll(upcomingSeries),
		AiringSeries:   state.decorateAll(airing),
		MovieGenres:    namedDTOs(movieGenres),
		SeriesGenres:   namedDTOs(seriesGenres),
		Networks:       sourceDTOs(discoverNetworks),
		Studios:        sourceDTOs(discoverStudios),
	})
}

// shelfError names which required shelf failed so writeDiscoverError can
// still log "trending" rather than a generic "home".
type shelfError struct {
	shelf string
	err   error
}

func (e *shelfError) Error() string { return e.err.Error() }
func (e *shelfError) Unwrap() error { return e.err }

func fmtDiscoverShelfError(shelf string, err error) error {
	return &shelfError{shelf: shelf, err: err}
}

func discoverShelfName(err error) string {
	var se *shelfError
	if errors.As(err, &se) && se.shelf != "" {
		return se.shelf
	}
	return "home"
}

func namedDTOs(list []core.DiscoverGenre) []discoverNamedJSON {
	out := make([]discoverNamedJSON, 0, len(list))
	for _, g := range list {
		out = append(out, discoverNamedJSON{TMDBID: g.TMDBID, Name: g.Name})
	}
	return out
}

// handleDiscoverBrowse serves one page of a curated shelf. A network browses
// series, a studio browses movies: that is what the two provider endpoints
// support, so the media type follows from the shelf rather than being asked
// for separately.
func (s *server) handleDiscoverBrowse(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.discovery(w, r)
	if !ok {
		return
	}

	query := r.URL.Query()
	id, err := strconv.ParseInt(query.Get("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	source, ok := findSource(query.Get("type"), id)
	if !ok {
		writeError(w, http.StatusBadRequest, "type must be network or studio, with a curated id")
		return
	}

	// An unparseable page is page 1 rather than a 400: it is how a client that
	// has not paged yet spells "the beginning".
	page, _ := strconv.Atoi(query.Get("page"))

	ctx := r.Context()
	var result *core.DiscoverPage
	if source.Type == SourceNetwork {
		result, err = provider.SeriesByNetwork(ctx, source.ID, page)
	} else {
		result, err = provider.MoviesByCompany(ctx, source.ID, page)
	}
	if err != nil {
		s.writeDiscoverError(w, r, "browse", err)
		return
	}

	state, err := s.libraryStateFor(ctx, result.Items)
	if err != nil {
		s.writeStoreError(w, "read library state", err)
		return
	}
	writeJSON(w, http.StatusOK, discoverBrowseResponse{
		Source:     sourceDTO(source),
		Page:       result.Page,
		TotalPages: result.TotalPages,
		Items:      state.decorateAll(result.Items),
	})
}

// handleDiscoverTitle serves one title's detail screen: the provider's record
// plus, for a series, what the library holds season by season.
func (s *server) handleDiscoverTitle(w http.ResponseWriter, r *http.Request) {
	provider, ok := s.discovery(w, r)
	if !ok {
		return
	}

	mediaType := r.PathValue("type")
	if mediaType != MediaTypeMovie && mediaType != MediaTypeSeries {
		writeError(w, http.StatusBadRequest, "type must be movie or series")
		return
	}
	tmdbID, ok := pathID(w, r)
	if !ok {
		return
	}

	ctx := r.Context()
	var (
		detail *core.TitleDetail
		err    error
	)
	if mediaType == MediaTypeMovie {
		detail, err = provider.MovieDetail(ctx, tmdbID)
	} else {
		detail, err = provider.SeriesDetail(ctx, tmdbID)
	}
	if err != nil {
		s.writeDiscoverError(w, r, "title detail", err)
		return
	}

	state, err := s.libraryStateFor(ctx, []core.DiscoverItem{detail.DiscoverItem}, detail.Recommendations)
	if err != nil {
		s.writeStoreError(w, "read library state", err)
		return
	}

	out := discoverTitleResponse{
		discoverItemJSON: state.decorate(detail.DiscoverItem),
		Status:           detail.Status,
		Runtime:          detail.Runtime,
		Network:          detail.Network,
		LastAired:        jsonDate(detail.LastAired),
		Language:         detail.Language,
		Genres:           detail.Genres,
		IMDBID:           detail.IMDBID,
		TVDBID:           detail.TVDBID,
		Cast:             []castMemberJSON{},
		Recommendations:  state.decorateAll(detail.Recommendations),
		Seasons:          []discoverSeasonJSON{},
	}
	if out.Genres == nil {
		out.Genres = []string{}
	}
	for _, m := range detail.Cast {
		out.Cast = append(out.Cast, castMemberJSON{
			TMDBID:     m.TMDBID,
			Name:       m.Name,
			Character:  m.Character,
			ProfileURL: m.ProfileURL,
		})
	}

	if mediaType == MediaTypeSeries && len(detail.Seasons) > 0 {
		seasons, err := s.discoverSeasons(ctx, detail.Seasons, out.LibraryID,
			state.pending[requestKey{MediaTypeSeries, detail.TMDBID}])
		if err != nil {
			s.writeStoreError(w, "read seasons", err)
			return
		}
		out.Seasons = seasons
	}
	writeJSON(w, http.StatusOK, out)
}

// discoverSeasons decorates the provider's season list. seriesID is the library
// id, or 0 when the series is not in the library; pending is the pending
// request for this series, or nil.
func (s *server) discoverSeasons(ctx context.Context, seasons []core.DiscoverSeason, seriesID int64, pending *core.Request) ([]discoverSeasonJSON, error) {
	held := map[int]bool{}
	if seriesID != 0 {
		rows, err := s.st.ListSeasons(ctx, seriesID)
		if err != nil {
			return nil, err
		}
		for _, se := range rows {
			held[se.Number] = true
		}
	}

	out := make([]discoverSeasonJSON, 0, len(seasons))
	for _, se := range seasons {
		out = append(out, discoverSeasonJSON{
			SeasonNumber: se.Number,
			Title:        se.Title,
			Overview:     se.Overview,
			PosterURL:    se.PosterURL,
			AirDate:      jsonDate(se.AirDate),
			EpisodeCount: se.EpisodeCount,
			InLibrary:    held[se.Number],
			Requested:    requestCoversSeason(pending, se.Number),
		})
	}
	return out, nil
}

// requestCoversSeason reports whether a pending request asks for this season.
// A request with no season list asks for the whole title, so it covers every
// season there is.
func requestCoversSeason(pending *core.Request, number int) bool {
	if pending == nil {
		return false
	}
	if len(pending.Seasons) == 0 {
		return true
	}
	return slices.Contains(pending.Seasons, number)
}

// requestKey identifies a title across both media types: a movie and a series
// can share a TMDB id, so neither half is a key on its own.
type requestKey struct {
	mediaType string
	tmdbID    int64
}

// libraryState is what Caravan knows about a page of provider results: which
// titles it already tracks and which have a pending request. It is built once
// per response in three queries, rather than three per row.
type libraryState struct {
	movies  map[int64]int64
	series  map[int64]int64
	pending map[requestKey]*core.Request
}

func (s *server) libraryStateFor(ctx context.Context, lists ...[]core.DiscoverItem) (*libraryState, error) {
	var movieIDs, seriesIDs, allIDs []int64
	for _, list := range lists {
		for _, item := range list {
			allIDs = append(allIDs, item.TMDBID)
			if item.MediaType == MediaTypeSeries {
				seriesIDs = append(seriesIDs, item.TMDBID)
			} else {
				movieIDs = append(movieIDs, item.TMDBID)
			}
		}
	}

	movies, err := s.st.MovieIDsByTMDBID(ctx, movieIDs)
	if err != nil {
		return nil, err
	}
	series, err := s.st.SeriesIDsByTMDBID(ctx, seriesIDs)
	if err != nil {
		return nil, err
	}
	requests, err := s.st.ListPendingRequestsForTMDBIDs(ctx, allIDs)
	if err != nil {
		return nil, err
	}

	state := &libraryState{movies: movies, series: series, pending: map[requestKey]*core.Request{}}
	for i := range requests {
		req := &requests[i]
		state.pending[requestKey{req.MediaType, req.TMDBID}] = req
	}
	return state, nil
}

func (st *libraryState) decorate(item core.DiscoverItem) discoverItemJSON {
	libraryID := st.movies[item.TMDBID]
	if item.MediaType == MediaTypeSeries {
		libraryID = st.series[item.TMDBID]
	}
	_, requested := st.pending[requestKey{item.MediaType, item.TMDBID}]

	return discoverItemJSON{
		MediaType:   item.MediaType,
		TMDBID:      item.TMDBID,
		Title:       item.Title,
		Year:        item.Year,
		Overview:    item.Overview,
		PosterPath:  item.PosterPath,
		PosterURL:   item.PosterURL,
		BackdropURL: item.BackdropURL,
		VoteAverage: item.VoteAverage,
		VoteCount:   item.VoteCount,
		Date:        jsonDate(item.Date),
		InLibrary:   libraryID != 0,
		LibraryID:   libraryID,
		Requested:   requested,
	}
}

// decorateAll returns an empty slice, never nil, so every list in the response
// decodes as an array.
func (st *libraryState) decorateAll(items []core.DiscoverItem) []discoverItemJSON {
	out := make([]discoverItemJSON, 0, len(items))
	for _, item := range items {
		out = append(out, st.decorate(item))
	}
	return out
}

// discovery returns the metadata provider's browse half. A provider that
// cannot browse is reported exactly like no provider at all, because to the
// discover screens it is the same thing: there is nothing to show and the fix
// is configuration, not a retry.
func (s *server) discovery(w http.ResponseWriter, r *http.Request) (core.DiscoverProvider, bool) {
	// A key that is absent or already known bad is answered with the typed code
	// the discover screens turn into their directed empty state, before any
	// round trip is spent proving it again.
	metadata, ok := s.metadataProvider(w, r)
	if !ok {
		return nil, false
	}
	provider, ok := metadata.(core.DiscoverProvider)
	if !ok {
		writeCodedError(w, http.StatusServiceUnavailable, CodeMetadataCredentialAbsent,
			"no metadata provider configured")
		return nil, false
	}
	return provider, true
}

// writeDiscoverError reports a provider failure as a bad gateway, matching
// GET /search: the request was fine, the upstream was not.
func (s *server) writeDiscoverError(w http.ResponseWriter, r *http.Request, what string, err error) {
	// See writeAdultProviderError: a canceled request is the ⌘K typeahead
	// aborting, not TMDB failing.
	if clientGone(r) {
		s.log.Debug("discover request abandoned by the caller", "what", what, "error", err)
		writeError(w, statusClientClosedRequest, "client closed request")
		return
	}
	// A rejected credential is the credential model's second transition: mark
	// it and answer the code, so a key revoked since it was entered turns the
	// discover screen into the same directed empty state an absent key does.
	//
	// TMDB by name and not by chain: these shelves are TMDB list ids, resolved
	// through metadataProvider, so there is exactly one credential that could
	// have been refused here and no guess to make about which.
	if s.noteMetadataFailure(core.ProviderTMDB, err) {
		writeCodedError(w, http.StatusServiceUnavailable, CodeMetadataCredentialInvalid,
			"the TMDB API key was rejected")
		return
	}
	s.log.Error("discover request failed", "what", what, "error", err)
	writeError(w, http.StatusBadGateway, "discover request failed")
}

// findSource resolves a (type, id) pair against the curated lists. An id that
// is not on a list is refused rather than forwarded: the endpoint offers
// shelves, not an arbitrary proxy onto TMDB's company and network ids.
func findSource(sourceType string, id int64) (discoverSource, bool) {
	var list []discoverSource
	switch sourceType {
	case SourceNetwork:
		list = discoverNetworks
	case SourceStudio:
		list = discoverStudios
	default:
		return discoverSource{}, false
	}
	for _, src := range list {
		if src.ID == id {
			return src, true
		}
	}
	return discoverSource{}, false
}

func sourceDTO(src discoverSource) discoverSourceJSON {
	return discoverSourceJSON{
		ID:      src.ID,
		Name:    src.Name,
		Type:    src.Type,
		LogoURL: sourceLogoURL(src.LogoPath),
		Lockup:  src.Lockup,
	}
}

func sourceLogoURL(path string) string {
	if path == "" {
		return ""
	}
	return discoverLogoBaseURL + path
}

func sourceDTOs(list []discoverSource) []discoverSourceJSON {
	out := make([]discoverSourceJSON, 0, len(list))
	for _, src := range list {
		out = append(out, sourceDTO(src))
	}
	return out
}
