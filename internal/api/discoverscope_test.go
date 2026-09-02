package api

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/store"
)

func day(y int, m time.Month, d int) time.Time {
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// TestDiscoverMoviesMapsEveryQueryParam is the movie half of the param-mapping
// proof: one URL carrying every filter, one exact core.MovieFilter.
func TestDiscoverMoviesMapsEveryQueryParam(t *testing.T) {
	p := &stubDiscoverProvider{page: &core.DiscoverPage{Page: 3, TotalPages: 12}}
	h, _ := discoverServer(t, p)

	target := "/api/v1/discover/movies?genres=28,878&companies=41077&keywords=9715,4565" +
		"&cast=3&crew=525,488&people=1245" +
		"&from=1980-01-01&to=1989-12-31&runtime_min=90&runtime_max=150" +
		"&rating_min=7.5&votes_min=200&language=ja&sort=rating&order=asc&page=3"
	rec := do(t, h, http.MethodGet, target, "")
	wantStatus(t, rec, http.StatusOK)

	want := core.MovieFilter{
		DiscoverFilter: core.DiscoverFilter{
			Genres:         []int64{28, 878},
			Companies:      []int64{41077},
			Keywords:       []int64{9715, 4565},
			ReleasedFrom:   day(1980, time.January, 1),
			ReleasedTo:     day(1989, time.December, 31),
			RuntimeMin:     90,
			RuntimeMax:     150,
			VoteAverageMin: 7.5,
			VoteCountMin:   200,
			Language:       "ja",
			Sort:           core.SortRating,
			Order:          core.OrderAsc,
			Page:           3,
		},
		Cast:   []int64{3},
		Crew:   []int64{525, 488},
		People: []int64{1245},
	}
	if len(p.movieFilters) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(p.movieFilters))
	}
	if !reflect.DeepEqual(p.movieFilters[0], want) {
		t.Errorf("filter:\n got %+v\nwant %+v", p.movieFilters[0], want)
	}

	var body discoverScopeResponse
	decodeBody(t, rec, &body)
	if body.MediaType != MediaTypeMovie || body.Page != 3 || body.TotalPages != 12 {
		t.Errorf("body = %+v, want movie page 3 of 12", body)
	}
	// An empty page is still an array.
	if body.Items == nil {
		t.Error("items = null, want an empty array")
	}
}

// TestDiscoverSeriesMapsEveryQueryParam is the series half. Networks are the
// only extra it accepts; there is no person filter in the list.
func TestDiscoverSeriesMapsEveryQueryParam(t *testing.T) {
	p := &stubDiscoverProvider{page: &core.DiscoverPage{Page: 2, TotalPages: 5}}
	h, _ := discoverServer(t, p)

	target := "/api/v1/discover/series?genres=10765&companies=2&keywords=4565&networks=213,49" +
		"&from=2015-06-01&to=2020-06-01&runtime_min=20&runtime_max=45" +
		"&rating_min=8&votes_min=50&language=en&sort=release_date&order=desc&page=2"
	rec := do(t, h, http.MethodGet, target, "")
	wantStatus(t, rec, http.StatusOK)

	want := core.SeriesFilter{
		DiscoverFilter: core.DiscoverFilter{
			Genres:         []int64{10765},
			Companies:      []int64{2},
			Keywords:       []int64{4565},
			ReleasedFrom:   day(2015, time.June, 1),
			ReleasedTo:     day(2020, time.June, 1),
			RuntimeMin:     20,
			RuntimeMax:     45,
			VoteAverageMin: 8,
			VoteCountMin:   50,
			Language:       "en",
			Sort:           core.SortReleaseDate,
			Order:          core.OrderDesc,
			Page:           2,
		},
		Networks: []int64{213, 49},
	}
	if len(p.seriesFilters) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(p.seriesFilters))
	}
	if !reflect.DeepEqual(p.seriesFilters[0], want) {
		t.Errorf("filter:\n got %+v\nwant %+v", p.seriesFilters[0], want)
	}

	var body discoverScopeResponse
	decodeBody(t, rec, &body)
	if body.MediaType != MediaTypeSeries {
		t.Errorf("media_type = %q, want %q", body.MediaType, MediaTypeSeries)
	}
}

// An empty query is the unfiltered scope, not a 400: it is how the scope opens
// before anyone touches the rail.
func TestDiscoverScopesAcceptAnEmptyFilter(t *testing.T) {
	p := &stubDiscoverProvider{page: &core.DiscoverPage{Page: 1, TotalPages: 1}}
	h, _ := discoverServer(t, p)

	wantStatus(t, do(t, h, http.MethodGet, "/api/v1/discover/movies", ""), http.StatusOK)
	wantStatus(t, do(t, h, http.MethodGet, "/api/v1/discover/series", ""), http.StatusOK)

	if len(p.movieFilters) != 1 || !reflect.DeepEqual(p.movieFilters[0], core.MovieFilter{}) {
		t.Errorf("movie filter = %+v, want the zero filter", p.movieFilters)
	}
	if len(p.seriesFilters) != 1 || !reflect.DeepEqual(p.seriesFilters[0], core.SeriesFilter{}) {
		t.Errorf("series filter = %+v, want the zero filter", p.seriesFilters)
	}
}

// the seam, enforced at the edge: TMDB's TV discover endpoint has no person
// parameter and ignores one if sent, so a person filter on the series scope is
// refused rather than dropped. A caller that asked a narrower question than it
// got is the failure this prevents.
func TestDiscoverSeriesRefusesPersonFilters(t *testing.T) {
	p := &stubDiscoverProvider{page: &core.DiscoverPage{}}
	h, _ := discoverServer(t, p)

	for _, param := range []string{"cast", "crew", "people"} {
		t.Run(param, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, "/api/v1/discover/series?"+param+"=3", "")
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
		})
	}
	// And the mirror: a network is a series-only filter, so it is refused on
	// the movie scope for the same reason.
	rec := do(t, h, http.MethodGet, "/api/v1/discover/movies?networks=213", "")
	wantStatus(t, rec, http.StatusBadRequest)
	wantErrorBody(t, rec)

	if len(p.seriesFilters) != 0 || len(p.movieFilters) != 0 {
		t.Errorf("provider was called: movies %+v, series %+v; want neither",
			p.movieFilters, p.seriesFilters)
	}
}

func TestDiscoverScopeRejectsMalformedFilters(t *testing.T) {
	p := &stubDiscoverProvider{page: &core.DiscoverPage{}}
	h, _ := discoverServer(t, p)

	tests := []struct {
		name  string
		query string
	}{
		{"non-numeric ids", "genres=action"},
		{"zero id", "genres=0"},
		{"negative id", "companies=-3"},
		{"trailing comma", "keywords=9715,"},
		{"bad date", "from=yesterday"},
		{"american date", "to=12/31/1989"},
		{"negative runtime", "runtime_min=-1"},
		{"non-numeric runtime", "runtime_max=long"},
		{"rating above ten", "rating_min=11"},
		{"negative votes", "votes_min=-5"},
		{"unknown sort", "sort=revenue"},
		{"unknown order", "order=sideways"},
		{"negative page", "page=-2"},
		{"a filter that does not exist", "mood=sad"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodGet, "/api/v1/discover/movies?"+tt.query, "")
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
		})
	}
	if len(p.movieFilters) != 0 {
		t.Errorf("provider calls = %+v, want none: nothing malformed should reach it", p.movieFilters)
	}
}

// A filtered row carries the same two-part state every other discover row
// does, from the same three queries.
func TestDiscoverScopeDecoratesLibraryAndRequests(t *testing.T) {
	ctx := context.Background()
	p := &stubDiscoverProvider{page: &core.DiscoverPage{
		Page: 1, TotalPages: 1,
		Items: []core.DiscoverItem{
			movieItem(78, "Blade Runner"),
			movieItem(335984, "Blade Runner 2049"),
			movieItem(603, "The Matrix"),
		},
	}}
	h, st := discoverServer(t, p)

	owned := core.Movie{TMDBID: 78, Title: "Blade Runner", SortTitle: "blade runner"}
	if err := st.UpsertMovie(ctx, &owned); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	req := core.Request{MediaType: MediaTypeMovie, TMDBID: 335984, Title: "Blade Runner 2049"}
	if err := st.CreateRequest(ctx, &req); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/discover/movies?genres=878", "")
	wantStatus(t, rec, http.StatusOK)

	var body discoverScopeResponse
	decodeBody(t, rec, &body)
	if len(body.Items) != 3 {
		t.Fatalf("items = %d, want 3", len(body.Items))
	}
	if !body.Items[0].InLibrary || body.Items[0].LibraryID != owned.ID || body.Items[0].Requested {
		t.Errorf("items[0] = %+v, want in_library %d and not requested", body.Items[0], owned.ID)
	}
	if body.Items[1].InLibrary || !body.Items[1].Requested {
		t.Errorf("items[1] = %+v, want requested and not in_library", body.Items[1])
	}
	if body.Items[2].InLibrary || body.Items[2].Requested {
		t.Errorf("items[2] = %+v, want neither flag", body.Items[2])
	}
	if body.Items[0].Date != "1982-06-25" {
		t.Errorf("date = %q, want 1982-06-25", body.Items[0].Date)
	}
}

func TestDiscoverTypeaheads(t *testing.T) {
	p := &stubDiscoverProvider{
		people: []core.DiscoverPerson{
			{TMDBID: 3, Name: "Harrison Ford", Department: "Acting", ProfileURL: "https://images.test/w500/f.jpg"},
		},
		companies: []core.DiscoverCompany{
			{TMDBID: 41077, Name: "A24", Country: "US", LogoURL: "https://images.test/w500/a24.png"},
		},
		keywords: []core.DiscoverKeyword{{TMDBID: 9715, Name: "superhero"}},
	}
	h, _ := discoverServer(t, p)

	rec := do(t, h, http.MethodGet, "/api/v1/discover/people?q=harrison", "")
	wantStatus(t, rec, http.StatusOK)
	var people discoverPeopleResponse
	decodeBody(t, rec, &people)
	want := []discoverPersonJSON{{TMDBID: 3, Name: "Harrison Ford", Department: "Acting",
		ProfileURL: "https://images.test/w500/f.jpg"}}
	if !reflect.DeepEqual(people.People, want) {
		t.Errorf("people = %+v, want %+v", people.People, want)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/discover/companies?q=a24", "")
	wantStatus(t, rec, http.StatusOK)
	var companies discoverCompaniesResponse
	decodeBody(t, rec, &companies)
	if len(companies.Companies) != 1 || companies.Companies[0].Country != "US" {
		t.Errorf("companies = %+v, want A24 (US)", companies.Companies)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/discover/keywords?q=hero", "")
	wantStatus(t, rec, http.StatusOK)
	var keywords discoverKeywordsResponse
	decodeBody(t, rec, &keywords)
	if len(keywords.Keywords) != 1 || keywords.Keywords[0].Name != "superhero" {
		t.Errorf("keywords = %+v, want superhero", keywords.Keywords)
	}

	// The query reaches the provider trimmed, once per call.
	if got := []string{"harrison", "a24", "hero"}; !reflect.DeepEqual(p.typeaheadQueries, got) {
		t.Errorf("queries = %v, want %v", p.typeaheadQueries, got)
	}
}

func TestDiscoverTypeaheadsRequireAQuery(t *testing.T) {
	p := &stubDiscoverProvider{}
	h, _ := discoverServer(t, p)

	for _, path := range []string{"people", "companies", "keywords"} {
		for _, query := range []string{"", "?q=", "?q=%20%20"} {
			rec := do(t, h, http.MethodGet, "/api/v1/discover/"+path+query, "")
			wantStatus(t, rec, http.StatusBadRequest)
			wantErrorBody(t, rec)
		}
	}
	if len(p.typeaheadQueries) != 0 {
		t.Errorf("queries = %v, want none", p.typeaheadQueries)
	}
}

// Empty typeahead results are arrays, never null: the rail renders "no
// matches", not a crash.
func TestDiscoverTypeaheadsReturnArrays(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{})

	rec := do(t, h, http.MethodGet, "/api/v1/discover/people?q=zzz", "")
	var people discoverPeopleResponse
	decodeBody(t, rec, &people)
	if people.People == nil {
		t.Error("people = null, want an empty array")
	}

	rec = do(t, h, http.MethodGet, "/api/v1/discover/keywords?q=zzz", "")
	var keywords discoverKeywordsResponse
	decodeBody(t, rec, &keywords)
	if keywords.Keywords == nil {
		t.Error("keywords = null, want an empty array")
	}
}

func TestDiscoverGenres(t *testing.T) {
	p := &stubDiscoverProvider{genres: map[string][]core.DiscoverGenre{
		MediaTypeMovie:  {{TMDBID: 28, Name: "Action"}, {TMDBID: 878, Name: "Science Fiction"}},
		MediaTypeSeries: {{TMDBID: 10765, Name: "Sci-Fi & Fantasy"}},
	}}
	h, _ := discoverServer(t, p)

	rec := do(t, h, http.MethodGet, "/api/v1/discover/genres?type=movie", "")
	wantStatus(t, rec, http.StatusOK)
	var body discoverGenresResponse
	decodeBody(t, rec, &body)
	if body.MediaType != MediaTypeMovie {
		t.Errorf("media_type = %q, want %q", body.MediaType, MediaTypeMovie)
	}
	want := []discoverNamedJSON{{TMDBID: 28, Name: "Action"}, {TMDBID: 878, Name: "Science Fiction"}}
	if !reflect.DeepEqual(body.Genres, want) {
		t.Errorf("genres = %+v, want %+v", body.Genres, want)
	}

	rec = do(t, h, http.MethodGet, "/api/v1/discover/genres?type=series", "")
	wantStatus(t, rec, http.StatusOK)
	decodeBody(t, rec, &body)
	if len(body.Genres) != 1 || body.Genres[0].Name != "Sci-Fi & Fantasy" {
		t.Errorf("genres = %+v, want the TV vocabulary", body.Genres)
	}

	if !reflect.DeepEqual(p.genreCalls, []string{MediaTypeMovie, MediaTypeSeries}) {
		t.Errorf("genre calls = %v, want [movie series]", p.genreCalls)
	}
}

// The two vocabularies differ, so the media type is required: defaulting it
// would render movie genres over a series scope, offering filters that match
// nothing.
func TestDiscoverGenresRequireAMediaType(t *testing.T) {
	p := &stubDiscoverProvider{}
	h, _ := discoverServer(t, p)

	for _, query := range []string{"", "?type=", "?type=scene", "?type=all"} {
		rec := do(t, h, http.MethodGet, "/api/v1/discover/genres"+query, "")
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
	}
	if len(p.genreCalls) != 0 {
		t.Errorf("genre calls = %v, want none", p.genreCalls)
	}
}

// A plain upstream failure (not a credential problem) stays a 502 on every new
// route, rather than sending anyone to the Settings screen.
func TestDiscoverScopeProviderFailureIsBadGateway(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{
		err: errors.New("tmdb: get /discover/movie: connection refused"),
	})

	for _, path := range discoverScopePaths {
		rec := do(t, h, http.MethodGet, path, "")
		wantStatus(t, rec, http.StatusBadGateway)
		wantErrorBody(t, rec)
	}
}

// discoverScopePaths is every route phase 12 adds, with whatever query each
// needs to get past validation and reach the provider.
var discoverScopePaths = []string{
	"/api/v1/discover/movies",
	"/api/v1/discover/series",
	"/api/v1/discover/people?q=ford",
	"/api/v1/discover/companies?q=a24",
	"/api/v1/discover/keywords?q=hero",
	"/api/v1/discover/genres?type=movie",
}

// The credential guard phase 10 gave the other discover routes, extended to
// every route phase 12 adds: an absent key and a rejected one each name the
// fix with a code the SPA branches on, rather than a bare 502.
func TestDiscoverScopesAnswerTypedCredentialErrors(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		for _, path := range discoverScopePaths {
			t.Run(path, func(t *testing.T) {
				h, _, mgr := newTestServer(t)
				mgr.addErr = core.ErrNoMetadataProvider
				rec := do(t, h, http.MethodGet, path, "")
				wantStatus(t, rec, http.StatusServiceUnavailable)
				wantCode(t, rec, CodeMetadataCredentialAbsent)
			})
		}
	})

	t.Run("rejected", func(t *testing.T) {
		for _, path := range discoverScopePaths {
			t.Run(path, func(t *testing.T) {
				h, st, mgr := newTestServer(t)
				setSetting(t, st, store.SettingTMDBAPIKey, "revoked")
				mgr.provider = &stubDiscoverProvider{
					stubProvider: stubProvider{err: errKeyRejected},
					err:          errKeyRejected,
				}
				mgr.addErr = errKeyRejected

				rec := do(t, h, http.MethodGet, path, "")
				wantStatus(t, rec, http.StatusServiceUnavailable)
				wantCode(t, rec, CodeMetadataCredentialInvalid)

				if got := credentialState(t, h).MetadataCredential; got != CredentialInvalid {
					t.Fatalf("metadata_credential = %q, want %q", got, CredentialInvalid)
				}
			})
		}
	})
}
