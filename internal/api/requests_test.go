package api

import (
	"context"
	"net/http"
	"reflect"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

type requestListBody struct {
	Requests []requestJSON `json:"requests"`
}

type approveBody struct {
	Request requestJSON `json:"request"`
	Movie   *movieJSON  `json:"movie"`
	Series  *seriesJSON `json:"series"`
}

func TestCreateRequestMovie(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{})

	rec := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner","year":1982,"poster_path":"/p.jpg"}`)
	wantStatus(t, rec, http.StatusCreated)

	var body requestJSON
	decodeBody(t, rec, &body)
	if body.ID == 0 {
		t.Fatal("response has no id")
	}
	if body.Status != core.RequestPending {
		t.Errorf("status = %q, want %q", body.Status, core.RequestPending)
	}
	if body.PosterPath != "/p.jpg" {
		t.Errorf("poster_path = %q, want /p.jpg", body.PosterPath)
	}
	// The stored path is rendered by the provider that knows the CDN prefix.
	if body.PosterURL != "https://images.test/w500/p.jpg" {
		t.Errorf("poster_url = %q, want the provider-rendered URL", body.PosterURL)
	}
	if body.Seasons != nil {
		t.Errorf("seasons = %v, want null for a movie", body.Seasons)
	}
}

func TestCreateRequestMergesSeasons(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{})

	first := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"series","tmdb_id":1396,"title":"Breaking Bad","seasons":[2,1]}`)
	wantStatus(t, first, http.StatusCreated)
	var firstBody requestJSON
	decodeBody(t, first, &firstBody)

	second := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"series","tmdb_id":1396,"title":"Breaking Bad","seasons":[3]}`)
	wantStatus(t, second, http.StatusCreated)
	var secondBody requestJSON
	decodeBody(t, second, &secondBody)

	if secondBody.ID != firstBody.ID {
		t.Errorf("second id = %d, want it merged into %d", secondBody.ID, firstBody.ID)
	}
	if want := []int{1, 2, 3}; !reflect.DeepEqual(secondBody.Seasons, want) {
		t.Errorf("seasons = %v, want %v", secondBody.Seasons, want)
	}

	list := do(t, h, http.MethodGet, "/api/v1/requests", "")
	wantStatus(t, list, http.StatusOK)
	var listBody requestListBody
	decodeBody(t, list, &listBody)
	if len(listBody.Requests) != 1 {
		t.Errorf("requests = %d, want the merge to leave 1", len(listBody.Requests))
	}
}

func TestCreateRequestValidation(t *testing.T) {
	ctx := context.Background()
	h, st := discoverServer(t, &stubDiscoverProvider{})

	owned := core.Movie{TMDBID: 78, Title: "Blade Runner", SortTitle: "blade runner"}
	if err := st.UpsertMovie(ctx, &owned); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "bad media type", body: `{"media_type":"person","tmdb_id":1,"title":"x"}`, want: http.StatusBadRequest},
		{name: "missing tmdb id", body: `{"media_type":"movie","title":"x"}`, want: http.StatusBadRequest},
		{name: "blank title", body: `{"media_type":"movie","tmdb_id":1,"title":"  "}`, want: http.StatusBadRequest},
		{name: "seasons on a movie", body: `{"media_type":"movie","tmdb_id":1,"title":"x","seasons":[1]}`, want: http.StatusBadRequest},
		{name: "negative season", body: `{"media_type":"series","tmdb_id":1,"title":"x","seasons":[-1]}`, want: http.StatusBadRequest},
		{name: "already in the library", body: `{"media_type":"movie","tmdb_id":78,"title":"Blade Runner"}`, want: http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(t, h, http.MethodPost, "/api/v1/requests", tt.body)
			wantStatus(t, rec, tt.want)
			wantErrorBody(t, rec)
		})
	}
}

func TestListRequestsFiltersByStatus(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{})

	create := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner"}`)
	wantStatus(t, create, http.StatusCreated)
	var created requestJSON
	decodeBody(t, create, &created)

	pending := do(t, h, http.MethodGet, "/api/v1/requests?status=pending", "")
	wantStatus(t, pending, http.StatusOK)
	var body requestListBody
	decodeBody(t, pending, &body)
	if len(body.Requests) != 1 || body.Requests[0].ID != created.ID {
		t.Errorf("pending = %+v, want the one request", body.Requests)
	}

	dismissed := do(t, h, http.MethodGet, "/api/v1/requests?status=dismissed", "")
	wantStatus(t, dismissed, http.StatusOK)
	decodeBody(t, dismissed, &body)
	if len(body.Requests) != 0 {
		t.Errorf("dismissed = %+v, want none", body.Requests)
	}

	bad := do(t, h, http.MethodGet, "/api/v1/requests?status=maybe", "")
	wantStatus(t, bad, http.StatusBadRequest)
	wantErrorBody(t, bad)
}

func TestApproveRequestAddsMovieAndMarksApproved(t *testing.T) {
	ctx := context.Background()
	h, st := discoverServer(t, &stubDiscoverProvider{})

	create := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner"}`)
	wantStatus(t, create, http.StatusCreated)
	var created requestJSON
	decodeBody(t, create, &created)

	rec := do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve", `{}`)
	wantStatus(t, rec, http.StatusOK)

	var body approveBody
	decodeBody(t, rec, &body)
	if body.Movie == nil || body.Movie.TMDBID != 78 {
		t.Fatalf("movie = %+v, want the added movie 78", body.Movie)
	}
	if body.Movie.Monitored {
		t.Error("omitted monitored value should leave the approved movie unmonitored")
	}
	if body.Request.Status != core.RequestApproved {
		t.Errorf("request status = %q, want %q", body.Request.Status, core.RequestApproved)
	}

	// The add went through the same path POST /library/movies takes, so the
	// movie is really in the library.
	if _, err := st.GetMovieByTMDBID(ctx, 78); err != nil {
		t.Errorf("GetMovieByTMDBID after approve: %v", err)
	}
}

func TestApproveMovieQualityProfileBeforeSearch(t *testing.T) {
	t.Run("chosen profile is stored before the queued search", func(t *testing.T) {
		ctx := context.Background()
		h, st := discoverServer(t, &stubDiscoverProvider{})
		p := seedSearchQualityProfile(t, st)
		req := &core.Request{MediaType: MediaTypeMovie, TMDBID: 81, Title: "Profiled Movie"}
		if err := st.CreateRequest(ctx, req); err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}

		rec := do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(req.ID)+"/approve",
			`{"search_now":true,"quality_profile_id":`+itoa(p.ID)+`}`)
		wantStatus(t, rec, http.StatusOK)
		movie, err := st.GetMovieByTMDBID(ctx, req.TMDBID)
		if err != nil {
			t.Fatalf("GetMovieByTMDBID: %v", err)
		}
		if movie.QualityProfileID != p.ID {
			t.Fatalf("stored profile = %d, want %d", movie.QualityProfileID, p.ID)
		}
		wantEffectiveQualityProfile(t, st, core.LibraryKindMovie, movie.QualityProfileID, p.ID)
		jobs := openJobs(t, st, core.JobSearchMovie)
		if len(jobs) != 1 || jobs[0].Payload != `{"movie_id":`+itoa(movie.ID)+`}` {
			t.Fatalf("search_movie jobs = %+v, want one for movie %d", jobs, movie.ID)
		}
	})

	t.Run("omitted profile inherits the default", func(t *testing.T) {
		ctx := context.Background()
		h, st := discoverServer(t, &stubDiscoverProvider{})
		def, err := st.ResolveQualityProfile(ctx, 0)
		if err != nil {
			t.Fatalf("ResolveQualityProfile: %v", err)
		}
		req := &core.Request{MediaType: MediaTypeMovie, TMDBID: 82, Title: "Default Movie"}
		if err := st.CreateRequest(ctx, req); err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}

		rec := do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(req.ID)+"/approve",
			`{"search_now":true}`)
		wantStatus(t, rec, http.StatusOK)
		movie, err := st.GetMovieByTMDBID(ctx, req.TMDBID)
		if err != nil {
			t.Fatalf("GetMovieByTMDBID: %v", err)
		}
		if movie.QualityProfileID != 0 {
			t.Fatalf("stored profile = %d, want inherited default", movie.QualityProfileID)
		}
		wantEffectiveQualityProfile(t, st, core.LibraryKindMovie, movie.QualityProfileID, def.ID)
	})

	t.Run("unknown profile leaves the request pending and queues no search", func(t *testing.T) {
		ctx := context.Background()
		h, st := discoverServer(t, &stubDiscoverProvider{})
		req := &core.Request{MediaType: MediaTypeMovie, TMDBID: 83, Title: "Invalid Movie"}
		if err := st.CreateRequest(ctx, req); err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}

		rec := do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(req.ID)+"/approve",
			`{"search_now":true,"quality_profile_id":999}`)
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
		stored, err := st.GetRequest(ctx, req.ID)
		if err != nil {
			t.Fatalf("GetRequest: %v", err)
		}
		if stored.Status != core.RequestPending {
			t.Fatalf("request status = %q, want pending", stored.Status)
		}
		if jobs := openJobs(t, st, core.JobSearchMovie); len(jobs) != 0 {
			t.Fatalf("search_movie jobs = %d, want none", len(jobs))
		}
	})
}

func TestApproveRequestAddsSeries(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{})

	create := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"series","tmdb_id":1396,"title":"Breaking Bad","seasons":[1]}`)
	wantStatus(t, create, http.StatusCreated)
	var created requestJSON
	decodeBody(t, create, &created)

	rec := do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve", `{"monitored":false}`)
	wantStatus(t, rec, http.StatusOK)

	var body approveBody
	decodeBody(t, rec, &body)
	if body.Series == nil || body.Series.TMDBID != 1396 {
		t.Fatalf("series = %+v, want the added series 1396", body.Series)
	}
	if body.Series.Monitored {
		t.Error("series monitored = true, want explicit false from approval")
	}
	if body.Movie != nil {
		t.Errorf("movie = %+v, want it absent on a series approval", body.Movie)
	}
	if body.Request.Status != core.RequestApproved {
		t.Errorf("request status = %q, want %q", body.Request.Status, core.RequestApproved)
	}
}

func TestApproveSeriesQualityProfileBeforeSearch(t *testing.T) {
	t.Run("chosen profile is stored before the queued search", func(t *testing.T) {
		ctx := context.Background()
		h, st, mgr := newTestServer(t)
		mgr.addSeriesEpisodes = 1
		p := seedSearchQualityProfile(t, st)
		req := &core.Request{MediaType: MediaTypeSeries, TMDBID: 1401, Title: "Profiled Series"}
		if err := st.CreateRequest(ctx, req); err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}

		rec := do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(req.ID)+"/approve",
			`{"monitored":true,"search_now":true,"quality_profile_id":`+itoa(p.ID)+`}`)
		wantStatus(t, rec, http.StatusOK)
		series, err := st.GetSeriesByTMDBID(ctx, req.TMDBID)
		if err != nil {
			t.Fatalf("GetSeriesByTMDBID: %v", err)
		}
		if series.QualityProfileID != p.ID {
			t.Fatalf("stored profile = %d, want %d", series.QualityProfileID, p.ID)
		}
		wantEffectiveQualityProfile(t, st, core.LibraryKindTV, series.QualityProfileID, p.ID)
		episodes, err := st.ListEpisodes(ctx, series.ID)
		if err != nil {
			t.Fatalf("ListEpisodes: %v", err)
		}
		jobs := openJobs(t, st, core.JobSearchEpisode)
		if len(episodes) != 1 || len(jobs) != 1 || jobs[0].Payload != `{"episode_id":`+itoa(episodes[0].ID)+`}` {
			t.Fatalf("search_episode jobs = %+v, want one for series %d", jobs, series.ID)
		}
	})

	t.Run("omitted profile inherits the default", func(t *testing.T) {
		ctx := context.Background()
		h, st, mgr := newTestServer(t)
		mgr.addSeriesEpisodes = 1
		def, err := st.ResolveQualityProfile(ctx, 0)
		if err != nil {
			t.Fatalf("ResolveQualityProfile: %v", err)
		}
		req := &core.Request{MediaType: MediaTypeSeries, TMDBID: 1402, Title: "Default Series"}
		if err := st.CreateRequest(ctx, req); err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}

		rec := do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(req.ID)+"/approve",
			`{"search_now":true}`)
		wantStatus(t, rec, http.StatusOK)
		series, err := st.GetSeriesByTMDBID(ctx, req.TMDBID)
		if err != nil {
			t.Fatalf("GetSeriesByTMDBID: %v", err)
		}
		if series.QualityProfileID != 0 {
			t.Fatalf("stored profile = %d, want inherited default", series.QualityProfileID)
		}
		wantEffectiveQualityProfile(t, st, core.LibraryKindTV, series.QualityProfileID, def.ID)
	})

	t.Run("unknown profile leaves the request pending and queues no search", func(t *testing.T) {
		ctx := context.Background()
		h, st, _ := newTestServer(t)
		req := &core.Request{MediaType: MediaTypeSeries, TMDBID: 1403, Title: "Invalid Series"}
		if err := st.CreateRequest(ctx, req); err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}

		rec := do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(req.ID)+"/approve",
			`{"search_now":true,"quality_profile_id":999}`)
		wantStatus(t, rec, http.StatusBadRequest)
		wantErrorBody(t, rec)
		stored, err := st.GetRequest(ctx, req.ID)
		if err != nil {
			t.Fatalf("GetRequest: %v", err)
		}
		if stored.Status != core.RequestPending {
			t.Fatalf("request status = %q, want pending", stored.Status)
		}
		if jobs := openJobs(t, st, core.JobSearchEpisode); len(jobs) != 0 {
			t.Fatalf("search_episode jobs = %d, want none", len(jobs))
		}
	})
}

func TestApproveRequestTwiceIsConflict(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{})

	create := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner"}`)
	var created requestJSON
	decodeBody(t, create, &created)

	first := do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve", `{}`)
	wantStatus(t, first, http.StatusOK)

	second := do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve", `{}`)
	wantStatus(t, second, http.StatusConflict)
	wantErrorBody(t, second)
}

func TestApproveAbsentRequestIsNotFound(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{})

	rec := do(t, h, http.MethodPost, "/api/v1/requests/999/approve", `{}`)
	wantStatus(t, rec, http.StatusNotFound)
	wantErrorBody(t, rec)
}

func TestDismissRequest(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{})

	create := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner"}`)
	var created requestJSON
	decodeBody(t, create, &created)

	rec := do(t, h, http.MethodDelete, "/api/v1/requests/"+itoa(created.ID), "")
	wantStatus(t, rec, http.StatusNoContent)

	list := do(t, h, http.MethodGet, "/api/v1/requests?status=dismissed", "")
	var body requestListBody
	decodeBody(t, list, &body)
	if len(body.Requests) != 1 || body.Requests[0].ID != created.ID {
		t.Fatalf("dismissed = %+v, want the request", body.Requests)
	}

	// A dismissed title can be requested again; the old row stays as history.
	again := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner"}`)
	wantStatus(t, again, http.StatusCreated)
	var reborn requestJSON
	decodeBody(t, again, &reborn)
	if reborn.ID == created.ID {
		t.Error("the dismissed request was reused, want a new row")
	}

	// Dismissing it twice is a stale client, not a second dismissal.
	repeat := do(t, h, http.MethodDelete, "/api/v1/requests/"+itoa(created.ID), "")
	wantStatus(t, repeat, http.StatusConflict)
	wantErrorBody(t, repeat)
}

// Ownership is recorded from the session, and an admin's list names who asked.
// A row nobody owns — made while the server ran open, or before accounts
// existed — names nobody rather than guessing.
func TestRequestsRecordTheAskerAndNameThemToAdmins(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newTestServer(t)

	// Made while the server still ran open, so it belongs to nobody.
	if err := st.CreateRequest(ctx, &core.Request{
		MediaType: MediaTypeMovie, TMDBID: 80, Title: "Nobody's",
	}); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	setPassword(t, st, testPassword)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	theirs := login(t, h, testMember, testPassword)
	admin := login(t, h, testAdmin, testPassword)

	create := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner"}`, withCookie(theirs))
	wantStatus(t, create, http.StatusCreated)
	var created requestJSON
	decodeBody(t, create, &created)
	if created.RequestedByUsername != testMember {
		t.Errorf("requested_by_username = %q, want %q", created.RequestedByUsername, testMember)
	}

	stored, err := st.GetRequest(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if stored.RequestedBy != member.ID {
		t.Errorf("requested_by = %d, want the session's account %d", stored.RequestedBy, member.ID)
	}

	list := doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(admin))
	wantStatus(t, list, http.StatusOK)
	var body requestListBody
	decodeBody(t, list, &body)
	if len(body.Requests) != 2 {
		t.Fatalf("admin sees %d requests, want both", len(body.Requests))
	}
	names := map[int64]string{}
	for _, row := range body.Requests {
		names[row.TMDBID] = row.RequestedByUsername
	}
	if names[78] != testMember {
		t.Errorf("request 78 requested_by_username = %q, want %q", names[78], testMember)
	}
	if names[80] != "" {
		t.Errorf("ownerless request requested_by_username = %q, want empty", names[80])
	}
}

// A merge is somebody queueing behind the first asker, not taking their request
// over, so the row keeps naming whoever asked first.
func TestMergedRequestKeepsTheFirstAsker(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	createUser(t, st, testMember, testPassword, core.RoleMember)
	first := login(t, h, testMember, testPassword)
	second := login(t, h, testAdmin, testPassword)

	create := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"series","tmdb_id":1396,"title":"Breaking Bad","seasons":[1]}`, withCookie(first))
	wantStatus(t, create, http.StatusCreated)

	merge := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"series","tmdb_id":1396,"title":"Breaking Bad","seasons":[2]}`, withCookie(second))
	wantStatus(t, merge, http.StatusCreated)
	var merged requestJSON
	decodeBody(t, merge, &merged)
	if merged.RequestedByUsername != testMember {
		t.Errorf("requested_by_username = %q, want the first asker %q",
			merged.RequestedByUsername, testMember)
	}
}

// A member sees their own rows and no others, and POST /requests is not a hole
// in that: a merge answers with a row that may belong to somebody else, so the
// name comes off it. Otherwise a member could walk the provider ids and read
// back which housemate asked for what.
func TestMergedRequestDoesNotNameAnotherMemberToAMember(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	createUser(t, st, "alice", testPassword, core.RoleMember)
	createUser(t, st, "bob", testPassword, core.RoleMember)
	alice := login(t, h, "alice", testPassword)
	bob := login(t, h, "bob", testPassword)

	create := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":603,"title":"The Matrix"}`, withCookie(alice))
	wantStatus(t, create, http.StatusCreated)

	merge := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":603,"title":"The Matrix"}`, withCookie(bob))
	wantStatus(t, merge, http.StatusCreated)
	var merged requestJSON
	decodeBody(t, merge, &merged)
	if merged.RequestedByUsername != "" {
		t.Errorf("requested_by_username = %q, want it withheld from a member who does not own the row",
			merged.RequestedByUsername)
	}

	// The row itself still records alice, so the admin's queue names her.
	admin := login(t, h, testAdmin, testPassword)
	list := doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(admin))
	wantStatus(t, list, http.StatusOK)
	var body requestListBody
	decodeBody(t, list, &body)
	if len(body.Requests) != 1 || body.Requests[0].RequestedByUsername != "alice" {
		t.Fatalf("admin's queue = %+v, want the one row still naming alice", body.Requests)
	}

	// A member's own row is still named to them: the rule is ownership, not
	// "members never see a name".
	own := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner"}`, withCookie(bob))
	wantStatus(t, own, http.StatusCreated)
	var mine requestJSON
	decodeBody(t, own, &mine)
	if mine.RequestedByUsername != "bob" {
		t.Errorf("own requested_by_username = %q, want %q", mine.RequestedByUsername, "bob")
	}
}

// POST /requests is member-allowed and its body is free text, so a merge must
// not become a way to edit a housemate's pending request.
func TestMergedRequestKeepsTheOwnersDescription(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	createUser(t, st, "alice", testPassword, core.RoleMember)
	createUser(t, st, "bob", testPassword, core.RoleMember)
	alice := login(t, h, "alice", testPassword)
	bob := login(t, h, "bob", testPassword)

	create := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"series","tmdb_id":1399,"title":"Game of Thrones","year":2011,"seasons":[1]}`,
		withCookie(alice))
	wantStatus(t, create, http.StatusCreated)

	merge := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"series","tmdb_id":1399,"title":"BOB OWNS THIS NOW","year":1999,"seasons":[2]}`,
		withCookie(bob))
	wantStatus(t, merge, http.StatusCreated)

	list := doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(alice))
	wantStatus(t, list, http.StatusOK)
	var body requestListBody
	decodeBody(t, list, &body)
	if len(body.Requests) != 1 {
		t.Fatalf("alice sees %d requests, want her one row", len(body.Requests))
	}
	row := body.Requests[0]
	if row.Title != "Game of Thrones" || row.Year != 2011 {
		t.Errorf("alice's row = %q (%d), want her own words", row.Title, row.Year)
	}
}

// A member's requests screen is their own wishes in every status, not a window
// onto the household.
func TestMembersSeeOnlyTheirOwnRequests(t *testing.T) {
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	createUser(t, st, testMember, testPassword, core.RoleMember)
	theirs := login(t, h, testMember, testPassword)
	admin := login(t, h, testAdmin, testPassword)

	mine := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner"}`, withCookie(theirs))
	wantStatus(t, mine, http.StatusCreated)
	var created requestJSON
	decodeBody(t, mine, &created)

	somebodyElses := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":79,"title":"Theirs"}`, withCookie(admin))
	wantStatus(t, somebodyElses, http.StatusCreated)

	list := doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(theirs))
	wantStatus(t, list, http.StatusOK)
	var body requestListBody
	decodeBody(t, list, &body)
	if len(body.Requests) != 1 || body.Requests[0].ID != created.ID {
		t.Fatalf("member sees %+v, want only their own request %d", body.Requests, created.ID)
	}

	// Dismissed is still theirs to see: watching a wish get decided is the point
	// of the screen.
	dismiss := doAuth(t, h, http.MethodDelete, "/api/v1/requests/"+itoa(created.ID), "", withCookie(theirs))
	wantStatus(t, dismiss, http.StatusNoContent)

	list = doAuth(t, h, http.MethodGet, "/api/v1/requests?status=dismissed", "", withCookie(theirs))
	decodeBody(t, list, &body)
	if len(body.Requests) != 1 || body.Requests[0].ID != created.ID {
		t.Errorf("member's dismissed list = %+v, want their own row", body.Requests)
	}

	// The admin still sees both.
	list = doAuth(t, h, http.MethodGet, "/api/v1/requests", "", withCookie(admin))
	decodeBody(t, list, &body)
	if len(body.Requests) != 2 {
		t.Errorf("admin sees %d requests, want both", len(body.Requests))
	}
}

// Cancelling is "cancel mine". Somebody else's request is not a member's to
// turn down, and the refusal must not double as a way to probe for rows.
func TestMemberCannotDismissAnotherPersonsRequest(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	createUser(t, st, testMember, testPassword, core.RoleMember)
	theirs := login(t, h, testMember, testPassword)
	admin := login(t, h, testAdmin, testPassword)

	somebodyElses := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":79,"title":"Theirs"}`, withCookie(admin))
	wantStatus(t, somebodyElses, http.StatusCreated)
	var other requestJSON
	decodeBody(t, somebodyElses, &other)

	rec := doAuth(t, h, http.MethodDelete, "/api/v1/requests/"+itoa(other.ID), "", withCookie(theirs))
	wantStatus(t, rec, http.StatusNotFound)
	wantErrorBody(t, rec)

	// The refusal is byte-identical to an id that was never issued, so the
	// endpoint cannot be used to map which ids exist.
	absent := doAuth(t, h, http.MethodDelete, "/api/v1/requests/999999", "", withCookie(theirs))
	wantStatus(t, absent, http.StatusNotFound)
	if rec.Body.String() != absent.Body.String() {
		t.Errorf("existing-but-foreign body %q != absent body %q; the difference is an oracle",
			rec.Body.String(), absent.Body.String())
	}

	stored, err := st.GetRequest(ctx, other.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if stored.Status != core.RequestPending {
		t.Fatalf("status = %q, want it left %q", stored.Status, core.RequestPending)
	}

	// An admin dismissing the same row is the normal case and still works.
	wantStatus(t, doAuth(t, h, http.MethodDelete, "/api/v1/requests/"+itoa(other.ID), "", withCookie(admin)),
		http.StatusNoContent)
}

// Approving is the admin's decision. A member who could approve their own
// request would be an admin wearing a smaller badge, so the gate turns them
// away before the handler runs.
func TestApprovingIsAdminOnly(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newTestServer(t)
	setPassword(t, st, testPassword)
	createUser(t, st, testMember, testPassword, core.RoleMember)
	theirs := login(t, h, testMember, testPassword)

	create := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner"}`, withCookie(theirs))
	wantStatus(t, create, http.StatusCreated)
	var created requestJSON
	decodeBody(t, create, &created)

	rec := doAuth(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve", `{}`,
		withCookie(theirs))
	wantStatus(t, rec, http.StatusForbidden)
	wantErrorBody(t, rec)

	// Nothing landed: the request is still asking and the title is not in the
	// library.
	stored, err := st.GetRequest(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if stored.Status != core.RequestPending {
		t.Errorf("status = %q, want it left %q", stored.Status, core.RequestPending)
	}
	if _, err := st.GetMovieByTMDBID(ctx, 78); err == nil {
		t.Error("the member's approval added the movie anyway")
	}
}

// The absorb rule: however a title reaches the library, a pending request for
// it stops asking.
func TestAddingToLibraryAbsorbsPendingRequest(t *testing.T) {
	tests := []struct {
		name      string
		mediaType string
		create    string
		addPath   string
		addBody   string
	}{
		{
			name:      "movie",
			mediaType: MediaTypeMovie,
			create:    `{"media_type":"movie","tmdb_id":78,"title":"Blade Runner"}`,
			addPath:   "/api/v1/library/movies",
			addBody:   `{"tmdb_id":78}`,
		},
		{
			name:      "series",
			mediaType: MediaTypeSeries,
			create:    `{"media_type":"series","tmdb_id":1396,"title":"Breaking Bad","seasons":[1]}`,
			addPath:   "/api/v1/library/series",
			addBody:   `{"tmdb_id":1396}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, _ := discoverServer(t, &stubDiscoverProvider{})

			create := do(t, h, http.MethodPost, "/api/v1/requests", tt.create)
			wantStatus(t, create, http.StatusCreated)
			var created requestJSON
			decodeBody(t, create, &created)

			add := do(t, h, http.MethodPost, tt.addPath, tt.addBody)
			wantStatus(t, add, http.StatusCreated)

			list := do(t, h, http.MethodGet, "/api/v1/requests", "")
			var body requestListBody
			decodeBody(t, list, &body)
			if len(body.Requests) != 1 {
				t.Fatalf("requests = %+v, want the one row", body.Requests)
			}
			if body.Requests[0].Status != core.RequestApproved {
				t.Errorf("status = %q, want %q after the title was added directly",
					body.Requests[0].Status, core.RequestApproved)
			}
		})
	}
}

func TestMatchingUnmatchedFileAbsorbsPendingRequest(t *testing.T) {
	ctx := context.Background()
	h, st := discoverServer(t, &stubDiscoverProvider{})

	create := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner"}`)
	wantStatus(t, create, http.StatusCreated)

	parked := core.UnmatchedFile{Path: "Movies/blade.runner.1982.mkv", Reason: "no match"}
	if err := st.UpsertUnmatchedFile(ctx, &parked); err != nil {
		t.Fatalf("UpsertUnmatchedFile: %v", err)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/import/queue/"+itoa(parked.ID)+"/match",
		`{"type":"movie","tmdb_id":78}`)
	wantStatus(t, rec, http.StatusOK)

	list := do(t, h, http.MethodGet, "/api/v1/requests", "")
	var body requestListBody
	decodeBody(t, list, &body)
	if len(body.Requests) != 1 || body.Requests[0].Status != core.RequestApproved {
		t.Errorf("requests = %+v, want the request approved by the match", body.Requests)
	}
}

func TestRequestsListWithoutProviderOmitsPosterURL(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newTestServer(t)

	req := core.Request{MediaType: MediaTypeMovie, TMDBID: 78, Title: "Blade Runner", PosterPath: "/p.jpg"}
	if err := st.CreateRequest(ctx, &req); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	rec := do(t, h, http.MethodGet, "/api/v1/requests", "")
	wantStatus(t, rec, http.StatusOK)

	var body requestListBody
	decodeBody(t, rec, &body)
	if len(body.Requests) != 1 {
		t.Fatalf("requests = %+v, want one", body.Requests)
	}
	if body.Requests[0].PosterPath != "/p.jpg" {
		t.Errorf("poster_path = %q, want it kept", body.Requests[0].PosterPath)
	}
	if body.Requests[0].PosterURL != "" {
		t.Errorf("poster_url = %q, want empty without a provider", body.Requests[0].PosterURL)
	}
}

// A partial series add grants only what it went after. Closing the whole
// request would throw the rest of the ask away with no record that it was only
// part granted.
func TestPartialSeriesAddNarrowsPendingRequestInsteadOfApprovingIt(t *testing.T) {
	ctx := context.Background()
	h, st, mgr := newTestServer(t)
	mgr.provider = &stubDiscoverProvider{}
	mgr.addSeriesSeasons = 3

	create := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"series","tmdb_id":1396,"title":"Breaking Bad","seasons":[1,2,3]}`)
	wantStatus(t, create, http.StatusCreated)
	var created requestJSON
	decodeBody(t, create, &created)

	add := do(t, h, http.MethodPost, "/api/v1/library/series", `{"tmdb_id":1396,"monitored":true,"seasons":[1]}`)
	wantStatus(t, add, http.StatusCreated)
	var added seriesJSON
	decodeBody(t, add, &added)

	pending, err := st.GetRequest(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if pending.Status != core.RequestPending {
		t.Errorf("status = %q, want %q — seasons 2 and 3 were never acquired",
			pending.Status, core.RequestPending)
	}
	if want := []int{2, 3}; !reflect.DeepEqual(pending.Seasons, want) {
		t.Errorf("seasons = %v, want %v", pending.Seasons, want)
	}

	// The seasons nobody went after are unmonitored, which is what "do not go
	// get this one" means in the library.
	seasons, err := st.ListSeasons(ctx, added.ID)
	if err != nil {
		t.Fatalf("ListSeasons: %v", err)
	}
	got := map[int]bool{}
	for _, se := range seasons {
		got[se.Number] = se.Monitored
	}
	if want := map[int]bool{1: true, 2: false, 3: false}; !reflect.DeepEqual(got, want) {
		t.Errorf("monitored by season = %v, want %v", got, want)
	}
}

// Naming every season is a whole-title add, which absorbs the request outright.
func TestSeriesAddCoveringEverySeasonAbsorbsTheRequest(t *testing.T) {
	ctx := context.Background()
	h, st, mgr := newTestServer(t)
	mgr.provider = &stubDiscoverProvider{}
	mgr.addSeriesSeasons = 3

	create := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"series","tmdb_id":1396,"title":"Breaking Bad","seasons":[1,2]}`)
	wantStatus(t, create, http.StatusCreated)
	var created requestJSON
	decodeBody(t, create, &created)

	add := do(t, h, http.MethodPost, "/api/v1/library/series", `{"tmdb_id":1396,"seasons":[1,2,3]}`)
	wantStatus(t, add, http.StatusCreated)

	granted, err := st.GetRequest(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if granted.Status != core.RequestApproved {
		t.Errorf("status = %q, want %q", granted.Status, core.RequestApproved)
	}
}

// Approving is an add, so it grants the same way: an approver who hands over
// less than was asked for leaves the remainder asking.
func TestApprovingWithFewerSeasonsLeavesTheRemainderPending(t *testing.T) {
	ctx := context.Background()
	h, st, mgr := newTestServer(t)
	mgr.provider = &stubDiscoverProvider{}
	mgr.addSeriesSeasons = 2

	create := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"series","tmdb_id":1396,"title":"Breaking Bad","seasons":[1,2]}`)
	wantStatus(t, create, http.StatusCreated)
	var created requestJSON
	decodeBody(t, create, &created)

	rec := do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve",
		`{"seasons":[1]}`)
	wantStatus(t, rec, http.StatusOK)

	var body approveBody
	decodeBody(t, rec, &body)
	if body.Request.Status != core.RequestPending {
		t.Errorf("request status = %q, want %q", body.Request.Status, core.RequestPending)
	}
	if want := []int{2}; !reflect.DeepEqual(body.Request.Seasons, want) {
		t.Errorf("request seasons = %v, want %v", body.Request.Seasons, want)
	}

	stored, err := st.GetRequest(ctx, created.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if stored.Status != core.RequestPending {
		t.Errorf("stored status = %q, want %q", stored.Status, core.RequestPending)
	}
}

func TestSeasonsRejectedOnTheMovieEndpoints(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{})

	add := do(t, h, http.MethodPost, "/api/v1/library/movies", `{"tmdb_id":78,"seasons":[1]}`)
	wantStatus(t, add, http.StatusBadRequest)
	wantErrorBody(t, add)

	create := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner"}`)
	wantStatus(t, create, http.StatusCreated)
	var created requestJSON
	decodeBody(t, create, &created)

	approve := do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve",
		`{"seasons":[1]}`)
	wantStatus(t, approve, http.StatusBadRequest)
	wantErrorBody(t, approve)
}

// The availability choice flows request → approve → movie row, the approver's
// explicit override beating the asker's choice.
func TestRequestMinAvailabilityFlowsToApprove(t *testing.T) {
	ctx := context.Background()
	h, st := discoverServer(t, &stubDiscoverProvider{})

	create := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner","min_availability":"in_cinemas"}`)
	wantStatus(t, create, http.StatusCreated)
	var created requestJSON
	decodeBody(t, create, &created)
	if created.MinAvailability != core.AvailabilityInCinemas {
		t.Fatalf("min_availability = %q, want %q echoed", created.MinAvailability, core.AvailabilityInCinemas)
	}

	rec := do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve", `{}`)
	wantStatus(t, rec, http.StatusOK)
	m, err := st.GetMovieByTMDBID(ctx, 78)
	if err != nil {
		t.Fatalf("GetMovieByTMDBID: %v", err)
	}
	if m.MinAvailability != core.AvailabilityInCinemas {
		t.Errorf("movie min_availability = %q, want the request's %q", m.MinAvailability, core.AvailabilityInCinemas)
	}

	// A second title, approved with an explicit override.
	create = do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":79,"title":"Other","min_availability":"released"}`)
	wantStatus(t, create, http.StatusCreated)
	decodeBody(t, create, &created)

	rec = do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve",
		`{"min_availability":"announced"}`)
	wantStatus(t, rec, http.StatusOK)
	m, err = st.GetMovieByTMDBID(ctx, 79)
	if err != nil {
		t.Fatalf("GetMovieByTMDBID: %v", err)
	}
	if m.MinAvailability != core.AvailabilityAnnounced {
		t.Errorf("movie min_availability = %q, want the approver's %q", m.MinAvailability, core.AvailabilityAnnounced)
	}
}

// min_availability is a movie knob: a series request or approval naming one is
// a client bug, answered like seasons on a movie.
func TestRequestMinAvailabilityValidation(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{})

	rec := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"series","tmdb_id":1396,"title":"Breaking Bad","min_availability":"released"}`)
	wantStatus(t, rec, http.StatusBadRequest)

	rec = do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner","min_availability":"whenever"}`)
	wantStatus(t, rec, http.StatusBadRequest)

	create := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"series","tmdb_id":1396,"title":"Breaking Bad"}`)
	wantStatus(t, create, http.StatusCreated)
	var created requestJSON
	decodeBody(t, create, &created)
	rec = do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve",
		`{"min_availability":"released"}`)
	wantStatus(t, rec, http.StatusBadRequest)
}

// Request-and-approve (Overseerr's admin flow): one call records the wish and
// grants it, with the ask's own seasons and availability and server defaults
// for everything the approve endpoint would let the approver choose.
func TestCreateRequestWithApproveGrantsAMovieImmediately(t *testing.T) {
	ctx := context.Background()
	h, st := discoverServer(t, &stubDiscoverProvider{})

	rec := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner","year":1982,"min_availability":"announced","approve":true}`)
	wantStatus(t, rec, http.StatusCreated)

	var body approveBody
	decodeBody(t, rec, &body)
	if body.Movie == nil || body.Movie.TMDBID != 78 {
		t.Fatalf("movie = %+v, want the added movie 78", body.Movie)
	}
	if body.Request.Status != core.RequestApproved {
		t.Errorf("request status = %q, want %q", body.Request.Status, core.RequestApproved)
	}
	m, err := st.GetMovieByTMDBID(ctx, 78)
	if err != nil {
		t.Fatalf("GetMovieByTMDBID: %v", err)
	}
	// The ask's own availability came with the grant; nothing about the add
	// was the server's to choose instead.
	if m.MinAvailability != core.AvailabilityAnnounced {
		t.Errorf("min_availability = %q, want the ask's %q", m.MinAvailability, core.AvailabilityAnnounced)
	}
}

// The series shape of the same: the ask's season list is the grant's season
// list, so a request for season 2 approves into exactly season 2 — not the
// whole title an empty approval body would mean.
func TestCreateRequestWithApproveGrantsTheAskedSeasons(t *testing.T) {
	ctx := context.Background()
	h, st := discoverServer(t, &stubDiscoverProvider{})

	rec := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"series","tmdb_id":1396,"title":"Breaking Bad","year":2008,"seasons":[2],"approve":true}`)
	wantStatus(t, rec, http.StatusCreated)

	var body approveBody
	decodeBody(t, rec, &body)
	if body.Series == nil || body.Series.TMDBID != 1396 {
		t.Fatalf("series = %+v, want the added series 1396", body.Series)
	}
	if body.Request.Status != core.RequestApproved {
		t.Errorf("request status = %q, want %q", body.Request.Status, core.RequestApproved)
	}
	if _, err := st.GetSeriesByTMDBID(ctx, 1396); err != nil {
		t.Fatalf("GetSeriesByTMDBID: %v", err)
	}
}

// Approving is the admin's decision on every door: a member who asks with
// approve set gets the approve route's answer, and no row is recorded — a
// refused create must not leave the wish behind as if it had been plain.
func TestCreateRequestWithApproveIsAdminOnly(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	createUser(t, st, testMember, testPassword, core.RoleMember)
	cookie := login(t, h, testMember, testPassword)

	rec := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner","approve":true}`, withCookie(cookie))
	wantStatus(t, rec, http.StatusForbidden)

	rows, err := st.ListRequests(ctx, "")
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("requests = %+v, want nothing recorded by the refused create", rows)
	}
}

// A request-and-approve that lands on a title already asked for grants the
// EXISTING row — the merge rule does not change because the admin's call
// carries a grant — so the asker's name and the household's one row survive.
func TestCreateRequestWithApproveMergesThenGrants(t *testing.T) {
	ctx := context.Background()
	h, st := discoverServer(t, &stubDiscoverProvider{})

	createUser(t, st, testAdmin, testPassword, core.RoleAdmin)
	member := createUser(t, st, testMember, testPassword, core.RoleMember)
	adminCookie := login(t, h, testAdmin, testPassword)
	memberCookie := login(t, h, testMember, testPassword)

	rec := doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner","year":1982}`, withCookie(memberCookie))
	wantStatus(t, rec, http.StatusCreated)
	var theirs requestJSON
	decodeBody(t, rec, &theirs)

	rec = doAuth(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"movie","tmdb_id":78,"title":"Blade Runner","year":1982,"approve":true}`, withCookie(adminCookie))
	wantStatus(t, rec, http.StatusCreated)

	var body approveBody
	decodeBody(t, rec, &body)
	if body.Request.ID != theirs.ID {
		t.Errorf("approved request id = %d, want the member's row %d", body.Request.ID, theirs.ID)
	}
	if body.Request.Status != core.RequestApproved {
		t.Errorf("request status = %q, want %q", body.Request.Status, core.RequestApproved)
	}
	rows, err := st.ListRequests(ctx, "")
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(rows) != 1 || rows[0].RequestedBy != member.ID {
		t.Errorf("requests = %+v, want the member's one row", rows)
	}
}
