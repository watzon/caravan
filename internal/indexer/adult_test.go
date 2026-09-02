package indexer

import (
	"context"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// The category an indexer files a result under decides how its name is read.
// This is the selector, and it is asserted on results that come back through
// the real feed decoder rather than on a call to the parser directly, because
// the wiring is the part that can silently regress.
func TestAdultCategoriesSelectTheSceneParser(t *testing.T) {
	c, _ := newStub(t, torznabCfg(), map[string]response{"search": ok(t, "torznab_search_adult.xml")})

	rels, err := c.Search(context.Background(), "", nil)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(rels) != 3 {
		t.Fatalf("got %d releases, want 3", len(rels))
	}

	scene := rels[0]
	if !scene.Parsed.IsScene() {
		t.Fatalf("a 6000-category release parsed as %+v, want a scene date", scene.Parsed)
	}
	if got := scene.Parsed.SceneDate.UTC().Format("2006-01-02"); got != "2022-03-14" {
		t.Errorf("scene date = %s, want 2022-03-14", got)
	}
	if scene.Parsed.Title != "Brazzers" {
		t.Errorf("scene title = %q, want the site name Brazzers", scene.Parsed.Title)
	}
	if scene.Parsed.Quality != core.Quality1080p || scene.Parsed.Group != "KTR" {
		t.Errorf("scene tags = %+v, want the shared quality/group rules to have run", scene.Parsed)
	}

	// An adult category is not a promise the name is date-shaped, so the scene
	// parser falls through and the release parses as what it is.
	misfiled := rels[1]
	if misfiled.Parsed.IsScene() {
		t.Errorf("a television name in an XXX category gained a scene date: %+v", misfiled.Parsed)
	}
	if misfiled.Parsed.Season != 1 || len(misfiled.Parsed.Episodes) != 1 || misfiled.Parsed.Episodes[0] != 2 {
		t.Errorf("misfiled television release = %+v, want S01E02", misfiled.Parsed)
	}

	// And the converse, which is the reason the selector is the category and
	// not the shape of the name: a dated television release is a daily episode
	// and must not be read as a scene.
	daily := rels[2]
	if daily.Parsed.IsScene() {
		t.Errorf("a dated television release was read as a scene: %+v", daily.Parsed)
	}
}

func TestIsAdultCategory(t *testing.T) {
	for _, id := range []int{6000, 6010, 6040, 6090, 6999} {
		if !core.IsAdultCategory(id) {
			t.Errorf("IsAdultCategory(%d) = false, want true", id)
		}
	}
	for _, id := range []int{0, 2000, 5000, 5999, 7000, 6000000} {
		if core.IsAdultCategory(id) {
			t.Errorf("IsAdultCategory(%d) = true, want false", id)
		}
	}
	if core.HasAdultCategory([]int{5000, 2000}) {
		t.Error("HasAdultCategory found an adult id where there is none")
	}
	if !core.HasAdultCategory([]int{5000, 6040}) {
		t.Error("HasAdultCategory missed an adult id among others")
	}
	if core.HasAdultCategory(nil) {
		t.Error("HasAdultCategory(nil) = true")
	}
}
