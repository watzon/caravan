package searchql

import (
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

func TestUpstreamQueries(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  []string
	}{
		{"plain text goes out unchanged", "dune part two", []string{"dune part two"}},
		{"a phrase is the same text to an indexer", `"dune part two"`, []string{"dune part two"}},
		{"keywords keep their written order", `1080p "dune part two"`, []string{"1080p dune part two"}},
		{"title and year fan out like a movie search", `title:"Dune" year:2021`, []string{"Dune 2021", "Dune"}},
		{"title alone", `title:"Dune"`, []string{"Dune"}},
		{"season and episode fan out like a series search", `title:"Some Show" season:1 episode:2`, []string{"Some Show S01E02", "Some Show S01"}},
		{"season alone", `title:"Some Show" season:1`, []string{"Some Show S01"}},
		{"specials are season zero, not no season", `title:"Some Show" season:0`, []string{"Some Show S00"}},
		{"an episode with no season cannot be spelled", `title:"Some Show" episode:2`, []string{"Some Show"}},
		{"site and date make the scene form", `site:"Creampie Thais" date:2026-01-19`, []string{"Creampie Thais 26.01.19"}},
		{"a scene date may be written the way release names write it", `site:"Creampie Thais" date:26.01.19`, []string{"Creampie Thais 26.01.19"}},
		{"keywords join the title", `title:"Dune" imax`, []string{"imax Dune"}},
		{"OR becomes separate searches", "dune OR arrakis", []string{"dune", "arrakis"}},
		{"a group distributes over the branches", "(dune OR arrakis) 2021", []string{"dune 2021", "arrakis 2021"}},
		{"negated terms stay local", "dune -cam -quality:480p", []string{"dune"}},
		{"a negated group sends nothing of its own", "dune NOT (cam OR ts)", []string{"dune"}},
		{"result-only filters send nothing", "quality:1080p is:proper", nil},
		{"filters ride along with text", "dune quality:1080p indexer:geek", []string{"dune"}},
		{"an unknown field is searched as text", "Re:Zero", []string{"Re:Zero"}},
		{"punctuation alone is not worth sending", `dune "..."`, []string{"dune"}},
		{"duplicate branches collapse", "dune OR dune", []string{"dune"}},
		{"the first value of a field wins", `title:"Dune" title:"Arrakis"`, []string{"Dune"}},
		{"an unreadable value is dropped from the search", `title:"Dune" year:soon`, []string{"Dune"}},
		{"branches are capped at four", "a OR b OR c OR d OR e OR f", []string{"a", "b", "c", "d"}},
		{
			"queries are capped at six",
			`title:"A" year:2000 OR title:"B" year:2001 OR title:"C" year:2002 OR title:"D" year:2003`,
			[]string{"A 2000", "A", "B 2001", "B", "C 2002", "C"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			q := mustParse(t, tc.input)
			if got := q.UpstreamQueries(); !equalStrings(got, tc.want) {
				t.Fatalf("UpstreamQueries(%q) = %v, want %v", tc.input, got, tc.want)
			}
			if got, want := q.HasUpstreamText(), len(tc.want) > 0; got != want {
				t.Fatalf("HasUpstreamText(%q) = %v, want %v", tc.input, got, want)
			}
		})
	}
}

// TestSeedExpressionsMatchTheEstablishedSearches is the invariant the whole
// package exists to hold: a search started from an item page and the seed
// expression it puts in the box ask the indexers exactly the same questions.
func TestSeedExpressionsMatchTheEstablishedSearches(t *testing.T) {
	movies := []struct {
		title string
		year  int
		want  string
	}{
		{"Dune", 2021, `title:"Dune" year:2021`},
		{"Big Buck Bunny", 2008, `title:"Big Buck Bunny" year:2008`},
		{"Amélie", 0, `title:"Amélie"`},
		{`The "Great" Movie`, 1999, `title:"The \"Great\" Movie" year:1999`},
	}
	for _, tc := range movies {
		seed := MovieExpression(tc.title, tc.year)
		if seed != tc.want {
			t.Fatalf("MovieExpression(%q, %d) = %q, want %q", tc.title, tc.year, seed, tc.want)
		}
		got := mustParse(t, seed).UpstreamQueries()
		if want := core.MovieSearches(tc.title, tc.year); !equalStrings(got, want) {
			t.Fatalf("MovieExpression(%q, %d) searches %v, want %v", tc.title, tc.year, got, want)
		}
	}

	series := []struct {
		title           string
		season, episode int
		want            string
	}{
		{"Some Show", 1, 2, `title:"Some Show" season:1 episode:2`},
		{"Some Show", 1, 0, `title:"Some Show" season:1`},
		{"Some Show", -1, 0, `title:"Some Show"`},
		{"Specials Show", 0, 1, `title:"Specials Show" season:0 episode:1`},
	}
	for _, tc := range series {
		seed := SeriesExpression(tc.title, tc.season, tc.episode)
		if seed != tc.want {
			t.Fatalf("SeriesExpression(%q, %d, %d) = %q, want %q", tc.title, tc.season, tc.episode, seed, tc.want)
		}
		got := mustParse(t, seed).UpstreamQueries()
		if want := core.SeriesSearches(tc.title, tc.season, tc.episode); !equalStrings(got, want) {
			t.Fatalf("SeriesExpression(%q, %d, %d) searches %v, want %v", tc.title, tc.season, tc.episode, got, want)
		}
	}

	// The scene seed must be the whole truth of the fan-out: every variant
	// core.SceneSearches would run appears in the expression, and parsing the
	// seed reproduces those exact query strings.
	site, date := "Creampie Thais", time.Date(2026, 6, 14, 0, 0, 0, 0, time.UTC)
	sceneCases := []struct {
		date  time.Time
		title string
		want  string
	}{
		{date: date, title: "Moie", want: `(site:"Creampie Thais" date:2026-06-14) OR "Creampie Thais Moie"`},
		{date: date, title: "", want: `site:"Creampie Thais" date:2026-06-14`},
		{date: time.Time{}, title: "Moie", want: `"Creampie Thais Moie"`},
		{date: time.Time{}, title: "", want: `site:"Creampie Thais"`},
	}
	for _, tc := range sceneCases {
		seed := SceneExpression(site, tc.date, tc.title)
		if seed != tc.want {
			t.Fatalf("SceneExpression(%v, %q) = %q, want %q", tc.date, tc.title, seed, tc.want)
		}
		want := make([]string, 0, 2)
		for _, search := range core.SceneSearches(site, tc.date, tc.title) {
			want = append(want, search.Query)
		}
		if len(want) == 0 {
			// Neither a date nor a title: the page's fallback searches the
			// site name alone, and so must the seed.
			want = []string{site}
		}
		if got := mustParse(t, seed).UpstreamQueries(); !equalStrings(got, want) {
			t.Fatalf("SceneExpression(%v, %q) searches %v, want %v", tc.date, tc.title, got, want)
		}
	}
}

// TestSeedExpressionsSurviveTheirOwnPunctuation guards the round trip through
// the search box: whatever a title contains, the seed must parse back to the
// same searches.
func TestSeedExpressionsSurviveTheirOwnPunctuation(t *testing.T) {
	for _, title := range []string{
		`Re:Zero`, `WALL-E`, `9 1/2 Weeks`, `Marvel's Agents of S.H.I.E.L.D.`,
		`AC/DC: Live`, `The "Burbs"`, `-30-`, `A (Very) Long Title`,
	} {
		seed := MovieExpression(title, 2001)
		got := mustParse(t, seed).UpstreamQueries()
		if want := core.MovieSearches(title, 2001); !equalStrings(got, want) {
			t.Fatalf("MovieExpression(%q) = %q searches %v, want %v", title, seed, got, want)
		}
	}
}
