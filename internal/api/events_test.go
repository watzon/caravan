package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

func TestEventsRespectAdultVisibility(t *testing.T) {
	h, st, _ := newTestServer(t)
	ctx := context.Background()
	setPassword(t, st, testPassword)
	cookie := login(t, h, testAdmin, testPassword)

	site := &core.Series{
		Kind: core.SeriesKindAdult, StashID: "history-site", Title: "Adult Site", SortTitle: "adult site",
	}
	if err := st.UpsertSeries(ctx, site); err != nil {
		t.Fatalf("UpsertSeries(adult): %v", err)
	}
	show := &core.Series{
		Kind: core.SeriesKindTV, TMDBID: 202, Title: "Family Show", SortTitle: "family show",
	}
	if err := st.UpsertSeries(ctx, show); err != nil {
		t.Fatalf("UpsertSeries(tv): %v", err)
	}
	movie := &core.Movie{TMDBID: 303, Title: "Family Movie", SortTitle: "family movie"}
	if err := st.UpsertMovie(ctx, movie); err != nil {
		t.Fatalf("UpsertMovie: %v", err)
	}

	for _, event := range []*core.Event{
		{Category: "system", Message: "Unrelated Event"},
		{Category: "import", Message: "Family Movie Event", MovieID: movie.ID},
		{Category: "import", Message: "Orphan Event", SeriesID: 999999},
		{Category: "import", Message: "Family Show Event", SeriesID: show.ID},
		{Category: "grab", Message: "Explicit Adult Event", SeriesID: site.ID},
	} {
		if err := st.InsertEvent(ctx, event); err != nil {
			t.Fatalf("InsertEvent(%q): %v", event.Message, err)
		}
	}

	read := func(path string) struct {
		Events []eventJSON `json:"events"`
		Next   string      `json:"next_cursor"`
	} {
		t.Helper()
		rec := doAuth(t, h, http.MethodGet, path, "", withCookie(cookie))
		wantStatus(t, rec, http.StatusOK)
		var body struct {
			Events []eventJSON `json:"events"`
			Next   string      `json:"next_cursor"`
		}
		decodeBody(t, rec, &body)
		return body
	}
	messages := func(events []eventJSON) map[string]bool {
		got := make(map[string]bool, len(events))
		for _, event := range events {
			got[event.Message] = true
		}
		return got
	}

	legacy := messages(read("/api/v1/events").Events)
	if legacy["Explicit Adult Event"] {
		t.Fatalf("legacy history with adult disabled exposed the adult event: %v", legacy)
	}
	for _, want := range []string{"Family Show Event", "Family Movie Event", "Orphan Event", "Unrelated Event"} {
		if !legacy[want] {
			t.Errorf("legacy history with adult disabled omitted %q: %v", want, legacy)
		}
	}

	first := read("/api/v1/events?limit=2")
	if len(first.Events) != 2 {
		t.Fatalf("first visible history page = %+v, want two rows", first.Events)
	}
	paged := messages(first.Events)
	if paged["Explicit Adult Event"] {
		t.Fatalf("paginated history with adult disabled exposed the adult event: %v", paged)
	}
	if first.Next == "" {
		t.Fatal("first visible history page has no cursor, want remaining visible rows")
	}
	second := read("/api/v1/events?limit=2&cursor=" + first.Next)
	for message := range messages(second.Events) {
		paged[message] = true
	}
	if paged["Explicit Adult Event"] {
		t.Fatalf("paginated history with adult disabled exposed the adult event: %v", paged)
	}
	for _, want := range []string{"Family Show Event", "Family Movie Event", "Orphan Event", "Unrelated Event"} {
		if !paged[want] {
			t.Errorf("paginated history with adult disabled omitted %q: %v", want, paged)
		}
	}

	if err := st.SetAdultEnabled(ctx, true); err != nil {
		t.Fatalf("SetAdultEnabled: %v", err)
	}
	for _, path := range []string{"/api/v1/events", "/api/v1/events?limit=10"} {
		if got := messages(read(path).Events); !got["Explicit Adult Event"] {
			t.Errorf("GET %s with adult enabled omitted the adult event: %v", path, got)
		}
	}
}
