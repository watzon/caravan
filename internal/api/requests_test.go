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
	if body.Request.Status != core.RequestApproved {
		t.Errorf("request status = %q, want %q", body.Request.Status, core.RequestApproved)
	}

	// The add went through the same path POST /library/movies takes, so the
	// movie is really in the library.
	if _, err := st.GetMovieByTMDBID(ctx, 78); err != nil {
		t.Errorf("GetMovieByTMDBID after approve: %v", err)
	}
}

func TestApproveRequestAddsSeries(t *testing.T) {
	h, _ := discoverServer(t, &stubDiscoverProvider{})

	create := do(t, h, http.MethodPost, "/api/v1/requests",
		`{"media_type":"series","tmdb_id":1396,"title":"Breaking Bad","seasons":[1]}`)
	wantStatus(t, create, http.StatusCreated)
	var created requestJSON
	decodeBody(t, create, &created)

	rec := do(t, h, http.MethodPost, "/api/v1/requests/"+itoa(created.ID)+"/approve", `{}`)
	wantStatus(t, rec, http.StatusOK)

	var body approveBody
	decodeBody(t, rec, &body)
	if body.Series == nil || body.Series.TMDBID != 1396 {
		t.Fatalf("series = %+v, want the added series 1396", body.Series)
	}
	if body.Movie != nil {
		t.Errorf("movie = %+v, want it absent on a series approval", body.Movie)
	}
	if body.Request.Status != core.RequestApproved {
		t.Errorf("request status = %q, want %q", body.Request.Status, core.RequestApproved)
	}
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

	add := do(t, h, http.MethodPost, "/api/v1/library/series", `{"tmdb_id":1396,"seasons":[1]}`)
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
