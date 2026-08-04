package stashbox

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/stashbox/stashboxtest"
)

func TestSearchScenes(t *testing.T) {
	c, s := newStub(t, map[string][]stashboxtest.Response{
		opSearchScenes: {okFixture(t, "query_scenes.json")},
	})

	got, err := c.SearchScenes(context.Background(), core.SceneQuery{
		SiteStashID: "f1f1f1f1-1111-4111-8111-111111111111",
		Page:        2,
		PerPage:     40,
	})
	if err != nil {
		t.Fatalf("SearchScenes: %v", err)
	}

	want := &core.ScenePage{
		Page:    2,
		PerPage: 40,
		Total:   412,
		Scenes: []core.SceneMeta{
			{
				StashID:     "5c5c5c5c-1111-4111-8111-aaaaaaaaaaaa",
				SiteStashID: "f1f1f1f1-1111-4111-8111-111111111111",
				SiteName:    "Tushy",
				Title:       "The Long Way Home",
				Overview:    "A scene description as the box stores it.",
				Date:        time.Date(2023, 11, 4, 0, 0, 0, 0, time.UTC),
				Code:        "TU-0412",
				Duration:    2712,
				Performers: []core.ScenePerformer{
					{StashID: "b1b1b1b1-1111-4111-8111-bbbbbbbbbbbb", Name: "Ava Rivers", As: "Ava R."},
					{StashID: "b2b2b2b2-2222-4222-8222-cccccccccccc", Name: "Mick Stone"},
				},
				URL:      "https://www.tushy.com/scene/the-long-way-home",
				ImageURL: "https://cdn.example.test/scene-412-cover.jpg",
			},
			{
				// Nulls everywhere and a year-only date: a thin record still
				// files under season 2019 rather than failing the page.
				StashID: "5d5d5d5d-2222-4222-8222-dddddddddddd",
				Date:    time.Date(2019, 1, 1, 0, 0, 0, 0, time.UTC),
			},
		},
	}

	if !reflect.DeepEqual(got, want) {
		t.Errorf("SearchScenes:\n got %+v\nwant %+v", got, want)
	}

	input := s.Requests()[0].Variables["input"].(map[string]any)
	if input["page"] != float64(2) || input["per_page"] != float64(40) {
		t.Errorf("input paging = (%v, %v), want (2, 40)", input["page"], input["per_page"])
	}
	// Date order, newest first: a site's scenes become episodes of seasons that
	// are release years, so relevance order would only have to be re-sorted.
	if input["sort"] != "DATE" || input["direction"] != "DESC" {
		t.Errorf("input sort = (%v, %v), want (DATE, DESC)", input["sort"], input["direction"])
	}
	studios, ok := input["studios"].(map[string]any)
	if !ok {
		t.Fatalf("input.studios = %v, want a MultiIDCriterionInput object", input["studios"])
	}
	// INCLUDES, not EQUALS: EQUALS asks for a scene whose studio set is exactly
	// this one, which excludes scenes filed under a sub-studio.
	if studios["modifier"] != "INCLUDES" {
		t.Errorf("input.studios.modifier = %v, want INCLUDES", studios["modifier"])
	}
	value, ok := studios["value"].([]any)
	if !ok || len(value) != 1 || value[0] != "f1f1f1f1-1111-4111-8111-111111111111" {
		t.Errorf("input.studios.value = %v, want the one requested site id", studios["value"])
	}
	if _, present := input["text"]; present {
		t.Errorf("input.text = %v, want it omitted when no text was asked for", input["text"])
	}
}

func TestSearchScenesSendsTrimmedText(t *testing.T) {
	c, s := newStub(t, map[string][]stashboxtest.Response{
		opSearchScenes: {okFixture(t, "query_scenes.json")},
	})

	if _, err := c.SearchScenes(context.Background(), core.SceneQuery{Text: "  the long way home  "}); err != nil {
		t.Fatalf("SearchScenes: %v", err)
	}

	input := s.Requests()[0].Variables["input"].(map[string]any)
	if input["text"] != "the long way home" {
		t.Errorf("input.text = %v, want the trimmed query", input["text"])
	}
	if _, present := input["studios"]; present {
		t.Errorf("input.studios = %v, want it omitted with no site filter", input["studios"])
	}
}

func TestSearchScenesClampsPagingAndReportsWhatItUsed(t *testing.T) {
	// A caller paging through a site walks the returned values, so what the
	// page reports has to be what was actually asked for.
	tests := []struct {
		name        string
		query       core.SceneQuery
		wantPage    int
		wantPerPage int
	}{
		{name: "zero query takes the defaults", query: core.SceneQuery{}, wantPage: 1, wantPerPage: defaultPerPage},
		{name: "page below one is clamped", query: core.SceneQuery{Page: -3}, wantPage: 1, wantPerPage: defaultPerPage},
		{name: "per page above the cap is clamped", query: core.SceneQuery{Page: 5, PerPage: 5000}, wantPage: 5, wantPerPage: maxPerPage},
		{name: "in-range values are kept", query: core.SceneQuery{Page: 3, PerPage: 10}, wantPage: 3, wantPerPage: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, s := newStub(t, map[string][]stashboxtest.Response{
				opSearchScenes: {okFixture(t, "query_scenes.json")},
			})

			got, err := c.SearchScenes(context.Background(), tt.query)
			if err != nil {
				t.Fatalf("SearchScenes: %v", err)
			}
			if got.Page != tt.wantPage || got.PerPage != tt.wantPerPage {
				t.Errorf("page reported (%d, %d), want (%d, %d)", got.Page, got.PerPage, tt.wantPage, tt.wantPerPage)
			}

			input := s.Requests()[0].Variables["input"].(map[string]any)
			if input["page"] != float64(tt.wantPage) || input["per_page"] != float64(tt.wantPerPage) {
				t.Errorf("input paging = (%v, %v), want (%d, %d)",
					input["page"], input["per_page"], tt.wantPage, tt.wantPerPage)
			}
		})
	}
}

func TestSearchScenesEmptyResultIsNotAnError(t *testing.T) {
	c, _ := newStub(t, map[string][]stashboxtest.Response{
		opSearchScenes: {stashboxtest.Raw([]byte(`{"data":{"queryScenes":{"count":0,"scenes":[]}}}`))},
	})

	got, err := c.SearchScenes(context.Background(), core.SceneQuery{SiteStashID: "x"})
	if err != nil {
		t.Fatalf("SearchScenes: %v", err)
	}
	if got.Total != 0 || len(got.Scenes) != 0 {
		t.Errorf("page = %+v, want an empty page", got)
	}
	if got.Scenes == nil {
		t.Error("Scenes = nil, want an empty slice: a site with no scenes is a normal answer")
	}
}

func TestGetScene(t *testing.T) {
	c, _ := newStub(t, map[string][]stashboxtest.Response{
		opFindScene: {okFixture(t, "find_scene.json")},
	})

	got, err := c.GetScene(context.Background(), "5c5c5c5c-1111-4111-8111-aaaaaaaaaaaa")
	if err != nil {
		t.Fatalf("GetScene: %v", err)
	}

	want := core.SceneMeta{
		StashID:     "5c5c5c5c-1111-4111-8111-aaaaaaaaaaaa",
		SiteStashID: "f1f1f1f1-1111-4111-8111-111111111111",
		SiteName:    "Tushy",
		Title:       "The Long Way Home",
		Overview:    "A scene description as the box stores it.",
		Date:        time.Date(2023, 11, 4, 0, 0, 0, 0, time.UTC),
		Code:        "TU-0412",
		Duration:    2712,
		Performers: []core.ScenePerformer{
			{StashID: "b1b1b1b1-1111-4111-8111-bbbbbbbbbbbb", Name: "Ava Rivers", As: "Ava R."},
			{StashID: "b2b2b2b2-2222-4222-8222-cccccccccccc", Name: "Mick Stone"},
		},
		// The canonical page, not the mirror listed after it.
		URL:      "https://www.tushy.com/scene/the-long-way-home",
		ImageURL: "https://cdn.example.test/scene-412-cover.jpg",
	}
	if !reflect.DeepEqual(*got, want) {
		t.Errorf("GetScene:\n got %+v\nwant %+v", *got, want)
	}
}

func TestGetSceneNotFound(t *testing.T) {
	c, _ := newStub(t, map[string][]stashboxtest.Response{
		opFindScene: {okFixture(t, "find_scene_null.json")},
	})

	got, err := c.GetScene(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if got != nil {
		t.Errorf("scene = %+v, want nil alongside the error", got)
	}
}

func TestGetSceneBlankIDIsNotFoundWithoutTraffic(t *testing.T) {
	c, s := newStub(t, map[string][]stashboxtest.Response{})

	if _, err := c.GetScene(context.Background(), ""); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if n := s.Count(); n != 0 {
		t.Errorf("requests = %d, want 0: a blank id must not reach the endpoint", n)
	}
}

func TestSceneMetaSkipsCreditsWithNoPerformer(t *testing.T) {
	// A credit with nothing behind it is a broken record, not a nameless
	// performer: writing an empty name into an episode's metadata is worse than
	// dropping the row.
	c, _ := newStub(t, map[string][]stashboxtest.Response{
		opFindScene: {stashboxtest.Raw([]byte(`{"data":{"findScene":{
			"id":"s1",
			"performers":[
				{"as":"ghost","performer":{"id":"","name":""}},
				{"as":null,"performer":{"id":"p1","name":"Real Person"}}
			]
		}}}`))},
	})

	got, err := c.GetScene(context.Background(), "s1")
	if err != nil {
		t.Fatalf("GetScene: %v", err)
	}
	want := []core.ScenePerformer{{StashID: "p1", Name: "Real Person"}}
	if !reflect.DeepEqual(got.Performers, want) {
		t.Errorf("Performers = %+v, want %+v", got.Performers, want)
	}
}
