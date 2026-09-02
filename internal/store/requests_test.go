package store

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestChangeHookFiresOnRequestWrite(t *testing.T) {
	st, _ := openTemp(t)
	var got []string
	st.SetChangeHook(func(resource string) {
		got = append(got, resource)
	})

	r := core.Request{
		MediaType: core.MediaTypeMovie,
		TMDBID:    78,
		Title:     "Blade Runner",
		Year:      1982,
	}
	if err := st.CreateRequest(context.Background(), &r); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if len(got) != 1 || got[0] != "requests" {
		t.Fatalf("hook = %v, want [requests]", got)
	}
}

func TestCreateRequestMovie(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	r := core.Request{
		MediaType:  core.MediaTypeMovie,
		TMDBID:     78,
		Title:      "Blade Runner",
		Year:       1982,
		PosterPath: "/63N9uy8nd9j7Eog2axPQ8lbr3Wj.jpg",
	}
	if err := st.CreateRequest(ctx, &r); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if r.ID == 0 {
		t.Fatal("CreateRequest did not write back an ID")
	}
	if r.Status != core.RequestPending {
		t.Errorf("Status = %q, want %q", r.Status, core.RequestPending)
	}

	got, err := st.GetRequest(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Title != "Blade Runner" || got.Year != 1982 || got.TMDBID != 78 {
		t.Errorf("GetRequest = %+v, want Blade Runner (1982) tmdb 78", *got)
	}
	if got.PosterPath != r.PosterPath {
		t.Errorf("PosterPath = %q, want %q", got.PosterPath, r.PosterPath)
	}
	if got.Seasons != nil {
		t.Errorf("Seasons = %v, want nil for a movie", got.Seasons)
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Errorf("timestamps = %v/%v, want both set", got.CreatedAt, got.UpdatedAt)
	}
}

func TestGetRequestAbsentIsNotFound(t *testing.T) {
	st, _ := openTemp(t)
	if _, err := st.GetRequest(context.Background(), 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetRequest(absent) = %v, want ErrNotFound", err)
	}
}

func TestCreateRequestPosterPathIsOptional(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	r := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 5, Title: "No Art"}
	if err := st.CreateRequest(ctx, &r); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	got, err := st.GetRequest(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.PosterPath != "" {
		t.Errorf("PosterPath = %q, want empty", got.PosterPath)
	}
}

func TestCreateRequestMergesPendingSeasons(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	first := core.Request{
		MediaType: core.MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad",
		Year: 2008, Seasons: []int{2, 1},
	}
	if err := st.CreateRequest(ctx, &first); err != nil {
		t.Fatalf("CreateRequest(first): %v", err)
	}
	// Sorted and deduplicated on the way in, so the stored order never depends
	// on what the client sent.
	if want := []int{1, 2}; !reflect.DeepEqual(first.Seasons, want) {
		t.Errorf("Seasons = %v, want %v", first.Seasons, want)
	}

	second := core.Request{
		MediaType: core.MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad",
		Year: 2008, Seasons: []int{3, 2},
	}
	if err := st.CreateRequest(ctx, &second); err != nil {
		t.Fatalf("CreateRequest(second): %v", err)
	}
	if second.ID != first.ID {
		t.Errorf("second request id = %d, want it merged into %d", second.ID, first.ID)
	}
	if want := []int{1, 2, 3}; !reflect.DeepEqual(second.Seasons, want) {
		t.Errorf("merged Seasons = %v, want %v", second.Seasons, want)
	}

	all, err := st.ListRequests(ctx, "")
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("ListRequests returned %d rows, want the merge to leave 1", len(all))
	}
	if want := []int{1, 2, 3}; !reflect.DeepEqual(all[0].Seasons, want) {
		t.Errorf("stored Seasons = %v, want %v", all[0].Seasons, want)
	}
}

func TestCreateRequestWholeTitleAbsorbsSeasons(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	partial := core.Request{MediaType: core.MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad", Seasons: []int{1}}
	if err := st.CreateRequest(ctx, &partial); err != nil {
		t.Fatalf("CreateRequest(partial): %v", err)
	}
	whole := core.Request{MediaType: core.MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad"}
	if err := st.CreateRequest(ctx, &whole); err != nil {
		t.Fatalf("CreateRequest(whole): %v", err)
	}

	got, err := st.GetRequest(ctx, partial.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Seasons != nil {
		t.Errorf("Seasons = %v, want nil: a whole-series request is not narrowed", got.Seasons)
	}
}

// Who asked is recorded on the row, and a request made with no account behind
// it (an open server) records the zero that means "nobody in particular".
func TestCreateRequestRecordsTheAsker(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	mine := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 78, Title: "Blade Runner", RequestedBy: 7}
	if err := st.CreateRequest(ctx, &mine); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	got, err := st.GetRequest(ctx, mine.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.RequestedBy != 7 {
		t.Errorf("RequestedBy = %d, want 7", got.RequestedBy)
	}

	open := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 79, Title: "Nobody's"}
	if err := st.CreateRequest(ctx, &open); err != nil {
		t.Fatalf("CreateRequest(open): %v", err)
	}
	got, err = st.GetRequest(ctx, open.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.RequestedBy != 0 {
		t.Errorf("RequestedBy = %d, want 0 for a request made with no account", got.RequestedBy)
	}
}

// The first asker owns the row. A housemate asking for the same show queues
// behind them rather than taking the request over.
func TestCreateRequestMergeKeepsTheFirstAsker(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	first := core.Request{
		MediaType: core.MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad",
		Seasons: []int{1}, RequestedBy: 7,
	}
	if err := st.CreateRequest(ctx, &first); err != nil {
		t.Fatalf("CreateRequest(first): %v", err)
	}

	second := core.Request{
		MediaType: core.MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad",
		Seasons: []int{2}, RequestedBy: 9,
	}
	if err := st.CreateRequest(ctx, &second); err != nil {
		t.Fatalf("CreateRequest(second): %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second request id = %d, want it merged into %d", second.ID, first.ID)
	}
	// Written back, so the caller answers with the row's real owner rather than
	// with the id it sent.
	if second.RequestedBy != 7 {
		t.Errorf("written-back RequestedBy = %d, want the first asker's 7", second.RequestedBy)
	}

	got, err := st.GetRequest(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.RequestedBy != 7 {
		t.Errorf("stored RequestedBy = %d, want the first asker's 7", got.RequestedBy)
	}
}

// A merge queues behind the first asker, so it must not rewrite what their row
// says either. POST /requests is member-allowed and its body is free text: a
// merge that overwrote the description would let one housemate put words in
// another's mouth, under their name, in the admin's approval queue.
func TestCreateRequestMergeKeepsTheOwnersDescription(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	first := core.Request{
		MediaType: core.MediaTypeSeries, TMDBID: 1399, Title: "Game of Thrones",
		Year: 2011, PosterPath: "/got.jpg", Seasons: []int{1}, RequestedBy: 7,
	}
	if err := st.CreateRequest(ctx, &first); err != nil {
		t.Fatalf("CreateRequest(first): %v", err)
	}

	second := core.Request{
		MediaType: core.MediaTypeSeries, TMDBID: 1399, Title: "BOB OWNS THIS NOW",
		Year: 1999, PosterPath: "/bob.jpg", Seasons: []int{2}, RequestedBy: 9,
	}
	if err := st.CreateRequest(ctx, &second); err != nil {
		t.Fatalf("CreateRequest(second): %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("second request id = %d, want it merged into %d", second.ID, first.ID)
	}

	got, err := st.GetRequest(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Title != "Game of Thrones" || got.Year != 2011 || got.PosterPath != "/got.jpg" {
		t.Errorf("merged row = %q/%d/%q, want the first asker's description kept",
			got.Title, got.Year, got.PosterPath)
	}
	// Seasons still union: that is what a merge is for.
	if want := []int{1, 2}; !reflect.DeepEqual(got.Seasons, want) {
		t.Errorf("merged Seasons = %v, want %v", got.Seasons, want)
	}
	// Written back, so the caller answers with the row rather than with itself.
	if second.Title != "Game of Thrones" || second.Year != 2011 || second.PosterPath != "/got.jpg" {
		t.Errorf("written-back description = %q/%d/%q, want the merged row's",
			second.Title, second.Year, second.PosterPath)
	}
}

// The other half of the same rule: a merge fills a field nobody has filled,
// exactly as it does for min_availability. A first request made without artwork
// gets it from the second rather than staying blank forever.
func TestCreateRequestMergeFillsAnEmptyDescription(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	first := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 78, Title: "Blade Runner"}
	if err := st.CreateRequest(ctx, &first); err != nil {
		t.Fatalf("CreateRequest(first): %v", err)
	}

	second := core.Request{
		MediaType: core.MediaTypeMovie, TMDBID: 78, Title: "Blade Runner",
		Year: 1982, PosterPath: "/br.jpg",
	}
	if err := st.CreateRequest(ctx, &second); err != nil {
		t.Fatalf("CreateRequest(second): %v", err)
	}

	got, err := st.GetRequest(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Year != 1982 || got.PosterPath != "/br.jpg" {
		t.Errorf("merged row = %d/%q, want the blanks filled in", got.Year, got.PosterPath)
	}
}

func TestListRequestsByRequester(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	mine := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 78, Title: "Blade Runner", RequestedBy: 7}
	theirs := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 79, Title: "Theirs", RequestedBy: 9}
	nobodys := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 80, Title: "Nobody's"}
	for _, r := range []*core.Request{&mine, &theirs, &nobodys} {
		if err := st.CreateRequest(ctx, r); err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}
	}
	if err := st.SetRequestStatus(ctx, mine.ID, core.RequestDismissed); err != nil {
		t.Fatalf("SetRequestStatus: %v", err)
	}

	// Every status, because watching a wish get decided is the whole point of
	// the screen.
	got, err := st.ListRequestsBy(ctx, 7, "")
	if err != nil {
		t.Fatalf("ListRequestsBy: %v", err)
	}
	if len(got) != 1 || got[0].ID != mine.ID {
		t.Fatalf("ListRequestsBy(7) = %+v, want only request %d", got, mine.ID)
	}

	// ...and the status filter still narrows within one person's rows.
	got, err = st.ListRequestsBy(ctx, 7, core.RequestPending)
	if err != nil {
		t.Fatalf("ListRequestsBy(pending): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("ListRequestsBy(7, pending) = %+v, want none — it was dismissed", got)
	}

	// Zero is a real requester, not a wildcard: it selects the rows nobody owns.
	got, err = st.ListRequestsBy(ctx, 0, "")
	if err != nil {
		t.Fatalf("ListRequestsBy(0): %v", err)
	}
	if len(got) != 1 || got[0].ID != nobodys.ID {
		t.Errorf("ListRequestsBy(0) = %+v, want only the ownerless request %d", got, nobodys.ID)
	}
}

func TestCreateRequestSeparatesMediaTypes(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	movie := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 1396, Title: "Coincidence"}
	series := core.Request{MediaType: core.MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad"}
	if err := st.CreateRequest(ctx, &movie); err != nil {
		t.Fatalf("CreateRequest(movie): %v", err)
	}
	if err := st.CreateRequest(ctx, &series); err != nil {
		t.Fatalf("CreateRequest(series): %v", err)
	}
	if movie.ID == series.ID {
		t.Fatal("a movie and a series sharing a tmdb id merged into one request")
	}
}

func TestDismissedRequestCanBeRequestedAgain(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	first := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 78, Title: "Blade Runner"}
	if err := st.CreateRequest(ctx, &first); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	if err := st.SetRequestStatus(ctx, first.ID, core.RequestDismissed); err != nil {
		t.Fatalf("SetRequestStatus: %v", err)
	}

	second := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 78, Title: "Blade Runner"}
	if err := st.CreateRequest(ctx, &second); err != nil {
		t.Fatalf("CreateRequest(after dismiss): %v", err)
	}
	if second.ID == first.ID {
		t.Error("a dismissed request was reused; it should stay as history")
	}

	pending, err := st.ListRequests(ctx, core.RequestPending)
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != second.ID {
		t.Errorf("pending = %+v, want only the new request %d", pending, second.ID)
	}
}

func TestSetRequestStatusAbsentIsNotFound(t *testing.T) {
	st, _ := openTemp(t)
	err := st.SetRequestStatus(context.Background(), 42, core.RequestDismissed)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("SetRequestStatus(absent) = %v, want ErrNotFound", err)
	}
}

func TestApproveRequestsForAbsorbsPending(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	r := core.Request{MediaType: core.MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad", Seasons: []int{1}}
	if err := st.CreateRequest(ctx, &r); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	n, err := st.ApproveRequestsFor(ctx, core.MediaTypeSeries, 1396)
	if err != nil {
		t.Fatalf("ApproveRequestsFor: %v", err)
	}
	if n != 1 {
		t.Errorf("absorbed %d requests, want 1", n)
	}
	got, err := st.GetRequest(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Status != core.RequestApproved {
		t.Errorf("Status = %q, want %q", got.Status, core.RequestApproved)
	}

	// Adding a title nobody asked for is the common case and must be silent.
	n, err = st.ApproveRequestsFor(ctx, core.MediaTypeMovie, 999)
	if err != nil {
		t.Fatalf("ApproveRequestsFor(unrequested): %v", err)
	}
	if n != 0 {
		t.Errorf("absorbed %d requests, want 0", n)
	}
}

func TestListPendingRequestsForTMDBIDs(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	pending := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 78, Title: "Blade Runner"}
	approved := core.Request{MediaType: core.MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad"}
	other := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 500, Title: "Elsewhere"}
	for _, r := range []*core.Request{&pending, &approved, &other} {
		if err := st.CreateRequest(ctx, r); err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}
	}
	if _, err := st.ApproveRequestsFor(ctx, core.MediaTypeSeries, 1396); err != nil {
		t.Fatalf("ApproveRequestsFor: %v", err)
	}

	got, err := st.ListPendingRequestsForTMDBIDs(ctx, []int64{78, 1396})
	if err != nil {
		t.Fatalf("ListPendingRequestsForTMDBIDs: %v", err)
	}
	if len(got) != 1 || got[0].TMDBID != 78 {
		t.Errorf("got %+v, want only the pending movie 78", got)
	}

	empty, err := st.ListPendingRequestsForTMDBIDs(ctx, nil)
	if err != nil {
		t.Fatalf("ListPendingRequestsForTMDBIDs(nil): %v", err)
	}
	if len(empty) != 0 {
		t.Errorf("got %+v, want empty for no ids", empty)
	}
}

func TestListRequestsFiltersByStatus(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	kept := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 78, Title: "Blade Runner"}
	dropped := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 79, Title: "Nope"}
	for _, r := range []*core.Request{&kept, &dropped} {
		if err := st.CreateRequest(ctx, r); err != nil {
			t.Fatalf("CreateRequest: %v", err)
		}
	}
	if err := st.SetRequestStatus(ctx, dropped.ID, core.RequestDismissed); err != nil {
		t.Fatalf("SetRequestStatus: %v", err)
	}

	pending, err := st.ListRequests(ctx, core.RequestPending)
	if err != nil {
		t.Fatalf("ListRequests: %v", err)
	}
	if len(pending) != 1 || pending[0].ID != kept.ID {
		t.Errorf("pending = %+v, want only %d", pending, kept.ID)
	}

	all, err := st.ListRequests(ctx, "")
	if err != nil {
		t.Fatalf("ListRequests(all): %v", err)
	}
	if len(all) != 2 {
		t.Errorf("all = %+v, want both", all)
	}
}

func TestIDsByTMDBID(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	m := core.Movie{TMDBID: 78, Title: "Blade Runner", SortTitle: "blade runner"}
	if err := st.UpsertMovie(ctx, &m); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}
	sr := core.Series{TMDBID: 1396, Title: "Breaking Bad", SortTitle: "breaking bad"}
	if err := st.UpsertSeries(ctx, &sr); err != nil {
		t.Fatalf("UpsertSeries: %v", err)
	}

	movies, err := st.MovieIDsByTMDBID(ctx, []int64{78, 335984})
	if err != nil {
		t.Fatalf("MovieIDsByTMDBID: %v", err)
	}
	if want := map[int64]int64{78: m.ID}; !reflect.DeepEqual(movies, want) {
		t.Errorf("MovieIDsByTMDBID = %v, want %v", movies, want)
	}

	series, err := st.SeriesIDsByTMDBID(ctx, []int64{1396, 66732})
	if err != nil {
		t.Fatalf("SeriesIDsByTMDBID: %v", err)
	}
	if want := map[int64]int64{1396: sr.ID}; !reflect.DeepEqual(series, want) {
		t.Errorf("SeriesIDsByTMDBID = %v, want %v", series, want)
	}

	none, err := st.MovieIDsByTMDBID(ctx, nil)
	if err != nil {
		t.Fatalf("MovieIDsByTMDBID(nil): %v", err)
	}
	if len(none) != 0 {
		t.Errorf("MovieIDsByTMDBID(nil) = %v, want empty", none)
	}
}

func TestGrantRequestSeasonsNarrowsAPartialGrant(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	r := core.Request{MediaType: core.MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad", Seasons: []int{1, 2, 3}}
	if err := st.CreateRequest(ctx, &r); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	// Only season 1 was added; seasons 2 and 3 were left unmonitored.
	n, err := st.GrantRequestSeasons(ctx, 1396, []int{2, 3})
	if err != nil {
		t.Fatalf("GrantRequestSeasons: %v", err)
	}
	if n != 0 {
		t.Errorf("approved %d requests, want 0 — the ask was only part granted", n)
	}
	got, err := st.GetRequest(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Status != core.RequestPending {
		t.Errorf("Status = %q, want %q", got.Status, core.RequestPending)
	}
	if want := []int{2, 3}; !reflect.DeepEqual(got.Seasons, want) {
		t.Errorf("Seasons = %v, want %v — what is still missing", got.Seasons, want)
	}

	// The rest arrives: nothing is outstanding, so the request is granted.
	n, err = st.GrantRequestSeasons(ctx, 1396, nil)
	if err != nil {
		t.Fatalf("GrantRequestSeasons(nothing outstanding): %v", err)
	}
	if n != 1 {
		t.Errorf("approved %d requests, want 1", n)
	}
	got, err = st.GetRequest(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Status != core.RequestApproved {
		t.Errorf("Status = %q, want %q", got.Status, core.RequestApproved)
	}
}

func TestGrantRequestSeasonsApprovesWhenTheAskIsCovered(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	// Somebody asked for season 1 only; the add went after 1 and 2 and skipped
	// season 3, which nobody wanted.
	r := core.Request{MediaType: core.MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad", Seasons: []int{1}}
	if err := st.CreateRequest(ctx, &r); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	n, err := st.GrantRequestSeasons(ctx, 1396, []int{3})
	if err != nil {
		t.Fatalf("GrantRequestSeasons: %v", err)
	}
	if n != 1 {
		t.Errorf("approved %d requests, want 1", n)
	}
	got, err := st.GetRequest(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Status != core.RequestApproved {
		t.Errorf("Status = %q, want %q", got.Status, core.RequestApproved)
	}
}

// A whole-title ask granted in part becomes an ask for the remainder: there is
// no honest way to read "all of it" as satisfied by some of it.
func TestGrantRequestSeasonsNarrowsAWholeTitleAsk(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	r := core.Request{MediaType: core.MediaTypeSeries, TMDBID: 1396, Title: "Breaking Bad"}
	if err := st.CreateRequest(ctx, &r); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}

	if _, err := st.GrantRequestSeasons(ctx, 1396, []int{4, 5}); err != nil {
		t.Fatalf("GrantRequestSeasons: %v", err)
	}
	got, err := st.GetRequest(ctx, r.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.Status != core.RequestPending {
		t.Errorf("Status = %q, want %q", got.Status, core.RequestPending)
	}
	if want := []int{4, 5}; !reflect.DeepEqual(got.Seasons, want) {
		t.Errorf("Seasons = %v, want %v", got.Seasons, want)
	}
}

func TestGrantRequestSeasonsWithNothingPendingIsSilent(t *testing.T) {
	st, _ := openTemp(t)
	n, err := st.GrantRequestSeasons(context.Background(), 1396, []int{1})
	if err != nil {
		t.Fatalf("GrantRequestSeasons: %v", err)
	}
	if n != 0 {
		t.Errorf("approved %d requests, want 0", n)
	}
}

// A movie request's availability choice round-trips, and a merge fills the
// field only when nobody has chosen yet. The first asker's choice stands.
func TestCreateRequestMinAvailability(t *testing.T) {
	ctx := context.Background()
	st, _ := openTemp(t)

	first := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 78, Title: "Blade Runner"}
	if err := st.CreateRequest(ctx, &first); err != nil {
		t.Fatalf("CreateRequest: %v", err)
	}
	got, err := st.GetRequest(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.MinAvailability != "" {
		t.Errorf("MinAvailability = %q, want empty when unspecified", got.MinAvailability)
	}

	// A merge into an undecided row adopts the newcomer's choice.
	second := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 78, Title: "Blade Runner",
		MinAvailability: core.AvailabilityInCinemas}
	if err := st.CreateRequest(ctx, &second); err != nil {
		t.Fatalf("CreateRequest (merge): %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("merge created row %d, want merge into %d", second.ID, first.ID)
	}
	got, err = st.GetRequest(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.MinAvailability != core.AvailabilityInCinemas {
		t.Errorf("MinAvailability = %q, want %q adopted on merge", got.MinAvailability, core.AvailabilityInCinemas)
	}

	// A later merge must not overwrite the standing choice.
	third := core.Request{MediaType: core.MediaTypeMovie, TMDBID: 78, Title: "Blade Runner",
		MinAvailability: core.AvailabilityAnnounced}
	if err := st.CreateRequest(ctx, &third); err != nil {
		t.Fatalf("CreateRequest (second merge): %v", err)
	}
	got, err = st.GetRequest(ctx, first.ID)
	if err != nil {
		t.Fatalf("GetRequest: %v", err)
	}
	if got.MinAvailability != core.AvailabilityInCinemas {
		t.Errorf("MinAvailability = %q, want the first choice %q kept", got.MinAvailability, core.AvailabilityInCinemas)
	}
	if third.MinAvailability != core.AvailabilityInCinemas {
		t.Errorf("written-back MinAvailability = %q, want the merged row's %q",
			third.MinAvailability, core.AvailabilityInCinemas)
	}
}
