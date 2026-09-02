package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

// stubDiscoverProvider is a canned core.DiscoverProvider. It embeds
// stubProvider so it satisfies the search half of the seam too, which is what
// s.mgr.Metadata() hands back.
type stubDiscoverProvider struct {
	stubProvider

	trending       []core.DiscoverItem
	popularMovies  []core.DiscoverItem
	popularSeries  []core.DiscoverItem
	upcomingMovies []core.DiscoverItem
	nowPlaying     []core.DiscoverItem
	upcomingSeries []core.DiscoverItem
	airingSeries   []core.DiscoverItem
	upcomingErr    error
	page           *core.DiscoverPage
	movieDetail    *core.TitleDetail
	seriesDetail   *core.TitleDetail
	err            error

	// browseCalls records what the browse endpoint forwarded.
	browseCalls []browseCall

	// The filtered scopes and what they forwarded. The filters are recorded
	// rather than acted on: what the provider does with them is internal/tmdb's
	// business, and what the API must get right is that the query string became
	// exactly this struct.
	movieFilters  []core.MovieFilter
	seriesFilters []core.SeriesFilter

	people    []core.DiscoverPerson
	companies []core.DiscoverCompany
	keywords  []core.DiscoverKeyword
	genres    map[string][]core.DiscoverGenre
	// typeaheadQueries and genreCalls record what each passthrough was asked.
	typeaheadQueries []string
	genreCalls       []string
}

type browseCall struct {
	kind string
	id   int64
	page int
}

func (p *stubDiscoverProvider) TrendingWeek(context.Context) ([]core.DiscoverItem, error) {
	return p.trending, p.err
}

func (p *stubDiscoverProvider) PopularMovies(context.Context) ([]core.DiscoverItem, error) {
	return p.popularMovies, p.err
}

func (p *stubDiscoverProvider) PopularSeries(context.Context) ([]core.DiscoverItem, error) {
	return p.popularSeries, p.err
}

func (p *stubDiscoverProvider) UpcomingMovies(context.Context) ([]core.DiscoverItem, error) {
	if p.upcomingErr != nil {
		return nil, p.upcomingErr
	}
	return p.upcomingMovies, nil
}

func (p *stubDiscoverProvider) NowPlayingMovies(context.Context) ([]core.DiscoverItem, error) {
	return p.nowPlaying, nil
}

func (p *stubDiscoverProvider) UpcomingSeries(context.Context) ([]core.DiscoverItem, error) {
	return p.upcomingSeries, nil
}

func (p *stubDiscoverProvider) AiringSeries(context.Context) ([]core.DiscoverItem, error) {
	return p.airingSeries, nil
}

func (p *stubDiscoverProvider) MoviesByCompany(_ context.Context, companyID int64, page int) (*core.DiscoverPage, error) {
	p.browseCalls = append(p.browseCalls, browseCall{kind: SourceStudio, id: companyID, page: page})
	return p.page, p.err
}

func (p *stubDiscoverProvider) SeriesByNetwork(_ context.Context, networkID int64, page int) (*core.DiscoverPage, error) {
	p.browseCalls = append(p.browseCalls, browseCall{kind: SourceNetwork, id: networkID, page: page})
	return p.page, p.err
}

func (p *stubDiscoverProvider) DiscoverMovies(_ context.Context, f core.MovieFilter) (*core.DiscoverPage, error) {
	p.movieFilters = append(p.movieFilters, f)
	if p.err != nil {
		return nil, p.err
	}
	return p.page, nil
}

func (p *stubDiscoverProvider) DiscoverSeries(_ context.Context, f core.SeriesFilter) (*core.DiscoverPage, error) {
	p.seriesFilters = append(p.seriesFilters, f)
	if p.err != nil {
		return nil, p.err
	}
	return p.page, nil
}

func (p *stubDiscoverProvider) SearchPeople(_ context.Context, query string) ([]core.DiscoverPerson, error) {
	p.typeaheadQueries = append(p.typeaheadQueries, query)
	return p.people, p.err
}

func (p *stubDiscoverProvider) SearchCompanies(_ context.Context, query string) ([]core.DiscoverCompany, error) {
	p.typeaheadQueries = append(p.typeaheadQueries, query)
	return p.companies, p.err
}

func (p *stubDiscoverProvider) SearchKeywords(_ context.Context, query string) ([]core.DiscoverKeyword, error) {
	p.typeaheadQueries = append(p.typeaheadQueries, query)
	return p.keywords, p.err
}

func (p *stubDiscoverProvider) Genres(_ context.Context, mediaType string) ([]core.DiscoverGenre, error) {
	p.genreCalls = append(p.genreCalls, mediaType)
	return p.genres[mediaType], p.err
}

func (p *stubDiscoverProvider) MovieDetail(context.Context, int64) (*core.TitleDetail, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.movieDetail, nil
}

func (p *stubDiscoverProvider) SeriesDetail(context.Context, int64) (*core.TitleDetail, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.seriesDetail, nil
}

func (p *stubDiscoverProvider) PosterURL(path string) string {
	if path == "" {
		return ""
	}
	return "https://images.test/w500" + path
}

// discoverServer builds a server whose manager reports a browse-capable
// provider.
func discoverServer(t *testing.T, p *stubDiscoverProvider) (http.Handler, *store.Store) {
	t.Helper()
	h, st, mgr := newTestServer(t)
	mgr.provider = p
	return h, st
}

func movieItem(tmdbID int64, title string) core.DiscoverItem {
	return core.DiscoverItem{
		MediaType: MediaTypeMovie, TMDBID: tmdbID, Title: title, Year: 1982,
		PosterPath: "/p.jpg", PosterURL: "https://images.test/w500/p.jpg",
		Date: time.Date(1982, 6, 25, 0, 0, 0, 0, time.UTC),
	}
}

func seriesItem(tmdbID int64, title string) core.DiscoverItem {
	return core.DiscoverItem{
		MediaType: MediaTypeSeries, TMDBID: tmdbID, Title: title, Year: 2008,
		Date: time.Date(2008, 1, 20, 0, 0, 0, 0, time.UTC),
	}
}

type discoverHomeBody struct {
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

func TestDiscoverHomeDecoratesLibraryAndRequests(t *testing.T) {
	ctx := context.Background()
	trendingMovie := movieItem(78, "Blade Runner")
	trendingMovie.VoteAverage = 8.1
	trendingMovie.VoteCount = 9_834
	trendingSeries := seriesItem(1396, "Breaking Bad")
	trendingSeries.VoteAverage = 8.9
	trendingSeries.VoteCount = 15_672
	popularMovie := movieItem(335984, "Blade Runner 2049")
	popularMovie.VoteAverage = 7.6
	popularSeries := seriesItem(66732, "Stranger Things")
	popularSeries.VoteAverage = 8.5
	p := &stubDiscoverProvider{
		trending:      []core.DiscoverItem{trendingMovie, trendingSeries},
		popularMovies: []core.DiscoverItem{popularMovie},
		popularSeries: []core.DiscoverItem{popularSeries},
	}
	h, st := discoverServer(t, p)

	// One title is already in the library, another has a pending request, and
	// the rest are neither.
	owned := core.Movie{TMDBID: 78, Title: "Blade Runner", SortTitle: "blade runner"}
	if err := st.UpsertMovie(ctx, &owned); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	req := core.Request{MediaType: MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad"}
	if err := st.CreateRequest(ctx, &req); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/discover", "")
	wantStatus(t, rec, http.StatusOK)
	rawBody := rec.Body.String()

	var body discoverHomeBody
	decodeBody(t, rec, &body)

	if len(body.Trending) != 2 {
		t.Fatalf("trending = %d items, want 2", len(body.Trending))
	}
	if !body.Trending[0].InLibrary || body.Trending[0].LibraryID != owned.ID {
		t.Errorf("trending[0] = %+v, want in_library with library_id %d", body.Trending[0], owned.ID)
	}
	if body.Trending[0].Requested {
		t.Error("trending[0] is requested, want false: it is already in the library")
	}
	if body.Trending[1].InLibrary {
		t.Errorf("trending[1] = %+v, want in_library false", body.Trending[1])
	}
	if !body.Trending[1].Requested {
		t.Errorf("trending[1] = %+v, want requested true", body.Trending[1])
	}
	if body.Trending[0].Date != "1982-06-25" {
		t.Errorf("date = %q, want 1982-06-25", body.Trending[0].Date)
	}
	if body.Trending[0].VoteAverage != 8.1 || body.Trending[0].VoteCount != 9_834 {
		t.Errorf("trending movie rating = %g from %d votes, want 8.1 from 9834", body.Trending[0].VoteAverage, body.Trending[0].VoteCount)
	}
	if body.Trending[1].VoteAverage != 8.9 || body.Trending[1].VoteCount != 15_672 {
		t.Errorf("trending series rating = %g from %d votes, want 8.9 from 15672", body.Trending[1].VoteAverage, body.Trending[1].VoteCount)
	}
	if body.PopularMovies[0].InLibrary || body.PopularMovies[0].Requested {
		t.Errorf("popular movie = %+v, want neither flag", body.PopularMovies[0])
	}
	if body.PopularSeries[0].TMDBID != 66732 {
		t.Errorf("popular series = %+v, want tmdb 66732", body.PopularSeries[0])
	}
	if body.PopularMovies[0].VoteAverage != 7.6 || body.PopularMovies[0].VoteCount != 0 {
		t.Errorf("popular movie rating = %g from %d votes, want 7.6 from 0", body.PopularMovies[0].VoteAverage, body.PopularMovies[0].VoteCount)
	}
	if body.PopularSeries[0].VoteAverage != 8.5 || body.PopularSeries[0].VoteCount != 0 {
		t.Errorf("popular series rating = %g from %d votes, want 8.5 from 0", body.PopularSeries[0].VoteAverage, body.PopularSeries[0].VoteCount)
	}
	if got := strings.Count(rawBody, `"vote_count":0`); got != 2 {
		t.Fatalf("zero vote_count fields = %d, want 2 in %s", got, rawBody)
	}
}

func TestDiscoverHomeServesCuratedShelves(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{})

	rec := do(t, h, http.MethodGet, "/api/v1/discover", "")
	wantStatus(t, rec, http.StatusOK)

	var body discoverHomeBody
	decodeBody(t, rec, &body)

	if len(body.Networks) != len(discoverNetworks) {
		t.Fatalf("networks = %d, want %d", len(body.Networks), len(discoverNetworks))
	}
	if body.Networks[0].ID != 213 || body.Networks[0].Name != "Netflix" {
		t.Errorf("networks[0] = %+v, want Netflix 213", body.Networks[0])
	}
	if body.Networks[0].Type != SourceNetwork {
		t.Errorf("networks[0].Type = %q, want %q", body.Networks[0].Type, SourceNetwork)
	}
	if body.Networks[0].LogoURL != discoverLogoBaseURL+"/wwemzKWzjKYJFfCeiB57q3r4Bcm.png" {
		t.Errorf("networks[0].LogoURL = %q, want the Netflix mark", body.Networks[0].LogoURL)
	}
	if len(body.Studios) != len(discoverStudios) {
		t.Fatalf("studios = %d, want %d", len(body.Studios), len(discoverStudios))
	}
	if body.Studios[0].ID != 41077 || body.Studios[0].Name != "A24" {
		t.Errorf("studios[0] = %+v, want A24 41077", body.Studios[0])
	}
	if body.Studios[0].LogoURL != discoverLogoBaseURL+"/1ZXsGaFPgrgS6ZZGS37AqD5uU12.png" {
		t.Errorf("studios[0].LogoURL = %q, want the A24 mark", body.Studios[0].LogoURL)
	}
	if body.Networks[0].Lockup {
		t.Errorf("networks[0].Lockup = true, want a flat Netflix mark")
	}
	// Empty lists must still be arrays, never null.
	if body.Trending == nil || body.PopularMovies == nil || body.PopularSeries == nil ||
		body.UpcomingMovies == nil || body.NowPlaying == nil ||
		body.UpcomingSeries == nil || body.AiringSeries == nil ||
		body.MovieGenres == nil || body.SeriesGenres == nil {
		t.Errorf("lists = %+v, want empty arrays rather than null", body)
	}
}

func TestDiscoverHomeServesExtraShelvesAndGenres(t *testing.T) {
	upcoming := movieItem(335984, "Blade Runner 2049")
	playing := movieItem(78, "Blade Runner")
	premiering := seriesItem(66732, "Stranger Things")
	airing := seriesItem(1396, "Breaking Bad")
	p := &stubDiscoverProvider{
		upcomingMovies: []core.DiscoverItem{upcoming},
		nowPlaying:     []core.DiscoverItem{playing},
		upcomingSeries: []core.DiscoverItem{premiering},
		airingSeries:   []core.DiscoverItem{airing},
		genres: map[string][]core.DiscoverGenre{
			MediaTypeMovie:  {{TMDBID: 878, Name: "Science Fiction"}},
			MediaTypeSeries: {{TMDBID: 18, Name: "Drama"}},
		},
	}
	h, _ := discoverServer(t, p)

	rec := do(t, h, http.MethodGet, "/api/v1/discover", "")
	wantStatus(t, rec, http.StatusOK)

	var body discoverHomeBody
	decodeBody(t, rec, &body)
	if len(body.UpcomingMovies) != 1 || body.UpcomingMovies[0].TMDBID != 335984 {
		t.Errorf("upcoming movies = %+v, want Blade Runner 2049", body.UpcomingMovies)
	}
	if len(body.NowPlaying) != 1 || body.NowPlaying[0].TMDBID != 78 {
		t.Errorf("now playing = %+v, want Blade Runner", body.NowPlaying)
	}
	if len(body.UpcomingSeries) != 1 || body.UpcomingSeries[0].TMDBID != 66732 {
		t.Errorf("upcoming series = %+v, want Stranger Things", body.UpcomingSeries)
	}
	if len(body.AiringSeries) != 1 || body.AiringSeries[0].TMDBID != 1396 {
		t.Errorf("airing series = %+v, want Breaking Bad", body.AiringSeries)
	}
	if len(body.MovieGenres) != 1 || body.MovieGenres[0].Name != "Science Fiction" {
		t.Errorf("movie genres = %+v, want Science Fiction", body.MovieGenres)
	}
	if len(body.SeriesGenres) != 1 || body.SeriesGenres[0].Name != "Drama" {
		t.Errorf("series genres = %+v, want Drama", body.SeriesGenres)
	}
}

func TestDiscoverHomeKeepsGoingWhenAnExtraShelfFails(t *testing.T) {
	p := &stubDiscoverProvider{
		popularMovies: []core.DiscoverItem{movieItem(78, "Blade Runner")},
		upcomingErr:   errors.New("tmdb upcoming down"),
	}
	h, _ := discoverServer(t, p)

	rec := do(t, h, http.MethodGet, "/api/v1/discover", "")
	wantStatus(t, rec, http.StatusOK)

	var body discoverHomeBody
	decodeBody(t, rec, &body)
	if len(body.PopularMovies) != 1 {
		t.Errorf("popular movies = %+v, want the required shelf intact", body.PopularMovies)
	}
	if body.UpcomingMovies == nil || len(body.UpcomingMovies) != 0 {
		t.Errorf("upcoming movies = %+v, want an empty array after the extra failed", body.UpcomingMovies)
	}
}

func TestDiscoverWithoutProviderIsUnavailable(t *testing.T) {
	h, _, _ := newTestServer(t)

	for _, path := range []string{"/api/v1/discover", "/api/v1/discover/browse?type=network&id=213", "/api/v1/discover/movie/78"} {
		rec := do(t, h, http.MethodGet, path, "")
		wantStatus(t, rec, http.StatusServiceUnavailable)
		wantErrorBody(t, rec)
	}
}

func TestDiscoverProviderFailureIsBadGateway(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{err: errors.New("tmdb down")})

	rec := do(t, h, http.MethodGet, "/api/v1/discover", "")
	wantStatus(t, rec, http.StatusBadGateway)
	wantErrorBody(t, rec)
}

func TestDiscoverBrowseForwardsCuratedSource(t *testing.T) {
	p := &stubDiscoverProvider{
		page: &core.DiscoverPage{
			Page: 2, TotalPages: 9,
			Items: []core.DiscoverItem{seriesItem(66732, "Stranger Things")},
		},
	}
	h, _ := discoverServer(t, p)

	rec := do(t, h, http.MethodGet, "/api/v1/discover/browse?type=network&id=213&page=2", "")
	wantStatus(t, rec, http.StatusOK)

	var body discoverBrowseResponse
	decodeBody(t, rec, &body)
	if body.Source.ID != 213 || body.Source.Name != "Netflix" || body.Source.Type != SourceNetwork {
		t.Errorf("source = %+v, want Netflix", body.Source)
	}
	if body.Page != 2 || body.TotalPages != 9 {
		t.Errorf("page/total = %d/%d, want 2/9", body.Page, body.TotalPages)
	}
	if len(body.Items) != 1 || body.Items[0].TMDBID != 66732 {
		t.Errorf("items = %+v, want Stranger Things", body.Items)
	}

	want := []browseCall{{kind: SourceNetwork, id: 213, page: 2}}
	if len(p.browseCalls) != 1 || p.browseCalls[0] != want[0] {
		t.Errorf("browse calls = %+v, want %+v", p.browseCalls, want)
	}
}

func TestDiscoverSourcesCarryLogos(t *testing.T) {
	var sawMarvelLockup bool
	for _, src := range append(append([]discoverSource{}, discoverNetworks...), discoverStudios...) {
		if src.LogoPath == "" {
			t.Errorf("%s %d (%s) has no logo path", src.Name, src.ID, src.Type)
		}
		if got := sourceLogoURL(src.LogoPath); !strings.HasPrefix(got, discoverLogoBaseURL+"/") {
			t.Errorf("%s logo URL = %q, want a TMDB w185 path", src.Name, got)
		}
		if src.ID == 420 && src.Type == SourceStudio {
			sawMarvelLockup = src.Lockup
		}
		if src.Lockup && !(src.ID == 420 && src.Type == SourceStudio) {
			t.Errorf("%s is marked lockup; only Marvel's filled badge needs the tri-tone", src.Name)
		}
	}
	if !sawMarvelLockup {
		t.Error("Marvel studio is not marked lockup")
	}
}

func TestDiscoverBrowseAcceptsExtendedShelves(t *testing.T) {
	p := &stubDiscoverProvider{page: &core.DiscoverPage{Page: 1, TotalPages: 1}}
	h, _ := discoverServer(t, p)

	tests := []struct {
		name string
		path string
		kind string
		id   int64
	}{
		{name: "prime video", path: "/api/v1/discover/browse?type=network&id=1024", kind: SourceNetwork, id: 1024},
		{name: "marvel", path: "/api/v1/discover/browse?type=studio&id=420", kind: SourceStudio, id: 420},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p.browseCalls = nil
			rec := do(t, h, http.MethodGet, tt.path, "")
			wantStatus(t, rec, http.StatusOK)
			if len(p.browseCalls) != 1 || p.browseCalls[0].kind != tt.kind || p.browseCalls[0].id != tt.id {
				t.Fatalf("browse calls = %+v, want one %s %d", p.browseCalls, tt.kind, tt.id)
			}
		})
	}
}

func TestDiscoverBrowseStudioUsesCompanyEndpoint(t *testing.T) {
	p := &stubDiscoverProvider{page: &core.DiscoverPage{Page: 1, TotalPages: 1}}
	h, _ := discoverServer(t, p)

	rec := do(t, h, http.MethodGet, "/api/v1/discover/browse?type=studio&id=41077", "")
	wantStatus(t, rec, http.StatusOK)

	if len(p.browseCalls) != 1 || p.browseCalls[0].kind != SourceStudio || p.browseCalls[0].id != 41077 {
		t.Fatalf("browse calls = %+v, want one studio 41077", p.browseCalls)
	}
	// A missing page is page 1, not a 400.
	if p.browseCalls[0].page != 0 && p.browseCalls[0].page != 1 {
		t.Errorf("page = %d, want the provider's own default", p.browseCalls[0].page)
	}
}

func TestDiscoverBrowseRejectsUncuratedSources(t *testing.T) {
	p := &stubDiscoverProvider{page: &core.DiscoverPage{}}
	h, _ := discoverServer(t, p)

	tests := []struct {
		name  string
		query string
	}{
		{name: "unknown type", query: "?type=label&id=213"},
		{name: "missing type", query: "?id=213"},
		{name: "missing id", query: "?type=network"},
		{name: "id not on the shelf", query: "?type=network&id=999999"},
		{name: "studio id used as a network", query: "?type=network&id=41077"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, "/api/v1/discover/browse"+tt.query, "")
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
		})
	}
	if len(p.browseCalls) != 0 {
		t.Errorf("browse calls = %+v, want none: nothing should reach the provider", p.browseCalls)
	}
}

func TestDiscoverTitleMovie(t *testing.T) {
	p := &stubDiscoverProvider{
		movieDetail: &core.TitleDetail{
			DiscoverItem: movieItem(78, "Blade Runner"),
			Runtime:      117,
			Genres:       []string{"Drama"},
			IMDBID:       "tt0083658",
			Cast: []core.CastMember{
				{TMDBID: 3, Name: "Harrison Ford", Character: "Rick Deckard", ProfileURL: "https://images.test/w500/f.jpg"},
			},
			Recommendations: []core.DiscoverItem{movieItem(335984, "Blade Runner 2049")},
		},
	}
	h, _ := discoverServer(t, p)

	rec := do(t, h, http.MethodGet, "/api/v1/discover/movie/78", "")
	wantStatus(t, rec, http.StatusOK)

	var body discoverTitleResponse
	decodeBody(t, rec, &body)
	if body.TMDBID != 78 || body.MediaType != MediaTypeMovie {
		t.Errorf("title = %+v, want movie 78", body.discoverItemJSON)
	}
	if body.Runtime != 117 || body.IMDBID != "tt0083658" {
		t.Errorf("runtime/imdb = %d/%q, want 117/tt0083658", body.Runtime, body.IMDBID)
	}
	if len(body.Cast) != 1 || body.Cast[0].Name != "Harrison Ford" {
		t.Errorf("cast = %+v, want Harrison Ford", body.Cast)
	}
	if len(body.Recommendations) != 1 || body.Recommendations[0].TMDBID != 335984 {
		t.Errorf("recommendations = %+v, want Blade Runner 2049", body.Recommendations)
	}
	if len(body.Seasons) != 0 {
		t.Errorf("seasons = %+v, want none on a movie", body.Seasons)
	}
}

func TestDiscoverTitleSeriesPerSeasonState(t *testing.T) {
	ctx := context.Background()
	p := &stubDiscoverProvider{
		seriesDetail: &core.TitleDetail{
			DiscoverItem: seriesItem(1396, "Breaking Bad"),
			Status:       "Ended",
			Seasons: []core.DiscoverSeason{
				{Number: 1, Title: "Season 1", EpisodeCount: 7},
				{Number: 2, Title: "Season 2", EpisodeCount: 13},
				{Number: 3, Title: "Season 3", EpisodeCount: 13},
			},
		},
	}
	h, st := discoverServer(t, p)

	// Season 1 is in the library; season 2 is only requested; season 3 is
	// neither.
	sr := core.Series{TMDBID: 1396, Title: "Breaking Bad", SortTitle: "breaking bad"}
	if err := st.UpsertSeries(ctx, &sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}
	season := core.Season{SeriesID: sr.ID, Number: 1, Title: "Season 1"}
	if err := st.UpsertSeason(ctx, &season); err != nil {
		t.Fatalf("UpsertSeason: %v", err)
	}
	req := core.Request{MediaType: MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad", Seasons: []int{2}}
	if err := st.CreateRequest(ctx, &req); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/discover/series/1396", "")
	wantStatus(t, rec, http.StatusOK)

	var body discoverTitleResponse
	decodeBody(t, rec, &body)
	if !body.InLibrary || body.LibraryID != sr.ID {
		t.Errorf("title = %+v, want in_library with library_id %d", body.discoverItemJSON, sr.ID)
	}
	if !body.Requested {
		t.Error("title requested = false, want true: a pending request names season 2")
	}
	if body.Status != "Ended" {
		t.Errorf("status = %q, want Ended", body.Status)
	}
	if len(body.Seasons) != 3 {
		t.Fatalf("seasons = %d, want 3", len(body.Seasons))
	}
	want := []struct {
		inLibrary bool
		requested bool
	}{
		{inLibrary: true, requested: false},
		{inLibrary: false, requested: true},
		{inLibrary: false, requested: false},
	}
	for i, w := range want {
		got := body.Seasons[i]
		if got.InLibrary != w.inLibrary || got.Requested != w.requested {
			t.Errorf("season %d = in_library %v/requested %v, want %v/%v",
				got.SeasonNumber, got.InLibrary, got.Requested, w.inLibrary, w.requested)
		}
	}
}

func TestDiscoverTitleWholeSeriesRequestCoversEverySeason(t *testing.T) {
	ctx := context.Background()
	p := &stubDiscoverProvider{
		seriesDetail: &core.TitleDetail{
			DiscoverItem: seriesItem(1396, "Breaking Bad"),
			Seasons: []core.DiscoverSeason{
				{Number: 1, Title: "Season 1"},
				{Number: 2, Title: "Season 2"},
			},
		},
	}
	h, st := discoverServer(t, p)

	req := core.Request{MediaType: MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad"}
	if err := st.CreateRequest(ctx, &req); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/discover/series/1396", "")
	wantStatus(t, rec, http.StatusOK)

	var body discoverTitleResponse
	decodeBody(t, rec, &body)
	for _, season := range body.Seasons {
		if !season.Requested {
			t.Errorf("season %d requested = false, want true under a whole-series request", season.SeasonNumber)
		}
		if season.InLibrary {
			t.Errorf("season %d in_library = true, want false", season.SeasonNumber)
		}
	}
}

func TestDiscoverTitleRejectsUnknownType(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{})

	rec := do(t, h, http.MethodGet, "/api/v1/discover/person/78", "")
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)

	rec = do(t, h, http.MethodGet, "/api/v1/discover/movie/nope", "")
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)
}
