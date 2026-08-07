package library

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// absoluteParse is what the parser hands back for an anime-style name: a title,
// a series-wide episode number, and deliberately no season/episode pair — the
// name never said which season it is in (core.ParsedRelease.Absolute).
func absoluteParse(title string, absolute int) core.ParsedRelease {
	return core.ParsedRelease{
		Title:      title,
		Absolute:   absolute,
		Quality:    core.Quality1080p,
		Source:     core.SourceWebDL,
		Codec:      "x265",
		Group:      "Group",
		Confidence: 0.9,
	}
}

// absoluteShowTree builds a five-season series in the shape TheTVDB serves.
//
// The season lengths are uneven ON PURPOSE and the absolute numbers are the
// provider's own: 105 is S05E03 only because seasons 1-4 ran 25, 25, 25 and 27
// episodes. Nothing can arrive at that from the season and episode numbers
// alone, which is the point — the number is looked up, never counted.
//
// withAbsolutes off is the other half: the same show as a provider that
// publishes no absolute order at all describes it.
func absoluteShowTree(h *harness, withAbsolutes bool) core.SeriesMeta {
	lengths := []int{25, 25, 25, 27, 12}
	meta := core.SeriesMeta{
		TMDBID: 77, Title: "Show", Year: 2023, Status: "Continuing", PosterURL: h.posterURL,
	}
	absolute := 0
	for i, length := range lengths {
		sm := core.SeasonMeta{Number: i + 1, Title: fmt.Sprintf("Season %d", i+1)}
		for n := 1; n <= length; n++ {
			absolute++
			em := core.EpisodeMeta{Season: i + 1, Number: n, Title: fmt.Sprintf("Episode %d", absolute)}
			if withAbsolutes {
				em.Absolute = absolute
			}
			sm.Episodes = append(sm.Episodes, em)
		}
		meta.Seasons = append(meta.Seasons, sm)
	}
	h.provider.series = []core.SeriesMeta{{TMDBID: 77, Title: "Show", Year: 2023}}
	h.provider.seriesByID[77] = meta
	return meta
}

// The provider's absolute numbers have to survive the rest of the import, and
// the rest of the import writes episode rows too: a file whose episode the
// provider never listed creates a placeholder row, and a rescan walks the whole
// library again. Neither may cost the series its numbering — a number that
// vanished on the second scan is worse than one that never arrived, because
// everything downstream would have been built on it in between.
func TestScanKeepsProviderAbsoluteNumbers(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	meta := seedSeries(h)
	for i := range meta.Seasons[0].Episodes {
		meta.Seasons[0].Episodes[i].Absolute = i + 1
	}
	h.provider.seriesByID[42] = meta

	// Episode 4 is on disk and in no provider tree, which is what makes
	// ensureEpisodes write a row of its own.
	raw := "library/TV/Planet.Earth.II.S01E04.1080p.WEB-DL.x265.mkv"
	h.parser[filepath.Base(raw)] = episodeParse("Planet Earth II", 1, 4)
	h.writeVideo(raw, "episode bytes")

	absolutes := func(when string) map[int]int {
		t.Helper()
		series, err := h.st.ListSeries(ctx)
		if err != nil || len(series) != 1 {
			t.Fatalf("ListSeries %s: %v (%d rows)", when, err, len(series))
		}
		episodes, err := h.st.ListEpisodes(ctx, series[0].ID)
		if err != nil {
			t.Fatalf("ListEpisodes %s: %v", when, err)
		}
		got := map[int]int{}
		for _, e := range episodes {
			got[e.EpisodeNumber] = e.AbsoluteNumber
		}
		return got
	}

	if res := h.scan(); res.Added != 1 || res.Unmatched != 0 {
		t.Fatalf("first scan = %+v, want the file imported", res)
	}
	// 1-3 from the tree; 4 is the placeholder, and 0 is the honest answer for
	// an episode no provider has numbered.
	want := map[int]int{1: 1, 2: 2, 3: 3, 4: 0}
	for number, absolute := range want {
		if got := absolutes("after the first scan")[number]; got != absolute {
			t.Errorf("episode %d absolute = %d after the first scan, want %d", number, got, absolute)
		}
	}

	// The second scan re-imports the organized file, re-writes the tree, and
	// meets the placeholder row again.
	h.scan()
	for number, absolute := range want {
		if got := absolutes("after the second scan")[number]; got != absolute {
			t.Errorf("episode %d absolute = %d after the second scan, want %d — a rescan may not renumber",
				number, got, absolute)
		}
	}
}

// A provider that serves no absolute numbers leaves every row at 0 rather than
// at a count taken here. Counting would be an answer Caravan invented about a
// series it does not publish, and it is wrong the moment a special or a split
// cour enters the order.
func TestScanInventsNoAbsoluteNumbers(t *testing.T) {
	h := newHarness(t)
	seedSeries(h) // its episodes carry no Absolute at all
	raw := "library/TV/Planet.Earth.II.S01E01.1080p.WEB-DL.x265.mkv"
	h.parser[filepath.Base(raw)] = episodeParse("Planet Earth II", 1, 1)
	h.writeVideo(raw, "episode bytes")
	h.scan()

	ctx := context.Background()
	series, err := h.st.ListSeries(ctx)
	if err != nil || len(series) != 1 {
		t.Fatalf("ListSeries: %v (%d rows)", err, len(series))
	}
	episodes, err := h.st.ListEpisodes(ctx, series[0].ID)
	if err != nil {
		t.Fatalf("ListEpisodes: %v", err)
	}
	for _, e := range episodes {
		if e.AbsoluteNumber != 0 {
			t.Errorf("S%02dE%02d absolute = %d, want 0", e.SeasonNumber, e.EpisodeNumber, e.AbsoluteNumber)
		}
	}
	if _, err := h.st.GetEpisodeByAbsoluteNumber(ctx, series[0].ID, 1); err == nil {
		t.Error("GetEpisodeByAbsoluteNumber(1) found a row; nothing served that number")
	}
}

// epMeta and seasonMeta keep the trees below readable: what matters in each is
// which absolute numbers exist and where, not the titles.
func epMeta(season, number, absolute int) core.EpisodeMeta {
	return core.EpisodeMeta{Season: season, Number: number, Absolute: absolute}
}

func seasonMeta(number int, episodes ...core.EpisodeMeta) core.SeasonMeta {
	return core.SeasonMeta{Number: number, Episodes: episodes}
}

// resolveAbsolute answers from the provider's tree or refuses. It never
// counts, and a refusal is the park that keeps a real file off the wrong
// episode.
func TestResolveAbsolute(t *testing.T) {
	// Uneven seasons with the provider's own running count: 105 lands on
	// S05E03 and no arithmetic over the season numbers produces that.
	fiveSeasons := &core.SeriesMeta{Seasons: []core.SeasonMeta{
		seasonMeta(0, epMeta(0, 1, 0)),
		seasonMeta(1, epMeta(1, 1, 1), epMeta(1, 2, 2)),
		seasonMeta(4, epMeta(4, 26, 101), epMeta(4, 27, 102)),
		seasonMeta(5, epMeta(5, 1, 103), epMeta(5, 2, 104), epMeta(5, 3, 105)),
	}}
	// The same show from a provider that publishes no absolute order.
	noAbsolutes := &core.SeriesMeta{Seasons: []core.SeasonMeta{
		seasonMeta(1, epMeta(1, 1, 0), epMeta(1, 2, 0)),
		seasonMeta(2, epMeta(2, 1, 0), epMeta(2, 2, 0)),
	}}
	// AniList's shape: one cour as its own record, so the episode number is
	// the series-wide number for everything it describes.
	oneSeason := &core.SeriesMeta{Seasons: []core.SeasonMeta{
		seasonMeta(1, epMeta(1, 1, 0), epMeta(1, 2, 0), epMeta(1, 3, 0)),
	}}
	// A special carrying the number a real episode also carries. Specials are
	// numbered inside season 0 and hold no place in the running order.
	specialCollision := &core.SeriesMeta{Seasons: []core.SeasonMeta{
		seasonMeta(0, epMeta(0, 1, 105)),
		seasonMeta(5, epMeta(5, 3, 105)),
	}}
	specialOnly := &core.SeriesMeta{Seasons: []core.SeasonMeta{
		seasonMeta(0, epMeta(0, 1, 105)),
		seasonMeta(1, epMeta(1, 1, 1)),
	}}

	tests := []struct {
		name         string
		meta         *core.SeriesMeta
		in           core.ParsedRelease
		wantOK       bool
		wantSeason   int
		wantEpisodes []int
	}{
		{"the provider's own order", fiveSeasons, absoluteParse("Show", 105), true, 5, []int{3}},
		{"the first episode of it", fiveSeasons, absoluteParse("Show", 1), true, 1, []int{1}},
		{"across a season boundary", fiveSeasons, absoluteParse("Show", 102), true, 4, []int{27}},
		{"past the end of the order", fiveSeasons, absoluteParse("Show", 9999), false, 0, nil},
		{"specials do not answer for the running order", specialCollision,
			absoluteParse("Show", 105), true, 5, []int{3}},
		{"a special alone answers nothing", specialOnly, absoluteParse("Show", 105), false, 0, nil},
		{"one season, so the number is the number", oneSeason,
			absoluteParse("Show", 3), true, 1, []int{3}},
		{"one season that is shorter than the claim", oneSeason,
			absoluteParse("Show", 9), false, 0, nil},
		{"several seasons and no order to read", noAbsolutes,
			absoluteParse("Show", 3), false, 0, nil},
		// A caller on the episode path may run this over every parse, and a
		// name that already said S05E03 must come back untouched.
		{"already resolved", noAbsolutes, episodeParse("Show", 5, 3), true, 5, []int{3}},
		// No claim at all: nothing to place, and saying so is what keeps this
		// out of the way of the parses other machinery handles.
		{"no claim", fiveSeasons, movieParse("Show", 2023), false, 0, nil},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := resolveAbsolute(tc.meta, tc.in)
			if ok != tc.wantOK {
				t.Fatalf("resolved = %v, want %v (got %+v)", ok, tc.wantOK, got)
			}
			if !ok {
				if !reflect.DeepEqual(got, tc.in) {
					t.Errorf("refused parse = %+v, want it unchanged from %+v", got, tc.in)
				}
				return
			}
			if got.Season != tc.wantSeason || !reflect.DeepEqual(got.Episodes, tc.wantEpisodes) {
				t.Errorf("placed at S%02dE%v, want S%02dE%v",
					got.Season, got.Episodes, tc.wantSeason, tc.wantEpisodes)
			}
			if got.Absolute != tc.in.Absolute {
				t.Errorf("absolute = %d, want the name's own %d kept", got.Absolute, tc.in.Absolute)
			}
		})
	}
}

// The scan-level promise: an anime-style file under a TV root is filed against
// the episode the provider's order says it is, under the SxxEyy name the media
// servers read.
func TestScanFilesAnAbsoluteNumberedEpisode(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	absoluteShowTree(h, true)
	raw := "library/TV/[Group] Show - 105.mkv"
	h.parser[filepath.Base(raw)] = absoluteParse("Show", 105)
	h.writeVideo(raw, "episode bytes")

	res := h.scan()
	if res.Added != 1 || res.Unmatched != 0 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v (errors %v), want the file imported", res, res.Errors)
	}

	const want = "library/TV/Show (2023)/Season 05/Show (2023) - S05E03 - Episode 105.mkv"
	if got := h.read(want); got != "episode bytes" {
		t.Fatalf("organized file %q missing (content %q)", want, got)
	}
	if h.exists(raw) {
		t.Errorf("source file %s still present", raw)
	}

	series, err := h.st.ListSeries(ctx)
	if err != nil || len(series) != 1 {
		t.Fatalf("ListSeries: %v (%d rows)", err, len(series))
	}
	// The row the file is linked to is the one the provider numbered 105, and
	// it kept that number.
	episode, err := h.st.GetEpisodeByAbsoluteNumber(ctx, series[0].ID, 105)
	if err != nil {
		t.Fatalf("GetEpisodeByAbsoluteNumber(105): %v", err)
	}
	if episode.SeasonNumber != 5 || episode.EpisodeNumber != 3 {
		t.Fatalf("absolute 105 = S%02dE%02d, want S05E03", episode.SeasonNumber, episode.EpisodeNumber)
	}
	files, err := h.st.ListMediaFilesForEpisode(ctx, episode.ID)
	if err != nil {
		t.Fatalf("ListMediaFilesForEpisode: %v", err)
	}
	if len(files) != 1 || files[0].Path != want {
		t.Errorf("links = %+v, want the organized file on S05E03", files)
	}
}

// A provider that publishes no absolute order cannot place the number, and the
// file parks saying exactly that rather than "no metadata match" — the show was
// found; its numbering is what nobody could answer.
func TestScanParksAnAbsoluteNumberNoProviderCanPlace(t *testing.T) {
	h := newHarness(t)
	absoluteShowTree(h, false)
	raw := "library/TV/[Group] Show - 105.mkv"
	h.parser[filepath.Base(raw)] = absoluteParse("Show", 105)
	h.writeVideo(raw, "episode bytes")

	res := h.scan()
	if res.Unmatched != 1 || res.Added != 0 {
		t.Fatalf("result = %+v, want the file parked", res)
	}
	parked := h.unmatched()
	if len(parked) != 1 || parked[0].Reason != reasonNoAbsoluteMatch {
		t.Fatalf("unmatched queue = %+v, want %q", parked, reasonNoAbsoluteMatch)
	}
	if parked[0].Parsed.Absolute != 105 {
		t.Errorf("parked parse = %+v, want the claim it made preserved", parked[0].Parsed)
	}
	if !h.exists(raw) {
		t.Errorf("parked file %s was moved; a park organizes nothing", raw)
	}
}

// Under a movie root the same name is a movie. The dispatch asks the library
// what it holds, never the filename — a title ending in a number must not be
// read as an episode of itself.
func TestScanKeepsAbsoluteNamesOutOfTheEpisodePathUnderAMovieRoot(t *testing.T) {
	h := newHarness(t)
	seedMovie(h)
	raw := "library/Movies/Big Buck Bunny - 105.mkv"
	p := movieParse("Big Buck Bunny", 2008)
	p.Absolute = 105
	h.parser[filepath.Base(raw)] = p
	h.writeVideo(raw, "movie bytes")

	res := h.scan()
	if res.Added != 1 || res.Unmatched != 0 || len(res.Errors) != 0 {
		t.Fatalf("result = %+v (errors %v), want the movie imported", res, res.Errors)
	}
	if got := h.read(organizedRel); got != "movie bytes" {
		t.Fatalf("organized movie %q missing (content %q)", organizedRel, got)
	}
	series, err := h.st.ListSeries(context.Background())
	if err != nil {
		t.Fatalf("ListSeries: %v", err)
	}
	if len(series) != 0 {
		t.Errorf("series rows = %+v, want none: a movie root files movies", series)
	}
}

// A file parked for its numbering has to have a way out. The manual match is
// that way: the user names the series, and the series' own order places the
// number the file already claimed — without it a reasonNoAbsoluteMatch park
// could only ever be re-parked.
func TestImportUnmatchedResolvesAnAbsoluteNumber(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	// The scan meets a library with no provider that can place the number.
	absoluteShowTree(h, false)
	raw := "library/TV/[Group] Show - 105.mkv"
	h.parser[filepath.Base(raw)] = absoluteParse("Show", 105)
	h.writeVideo(raw, "episode bytes")
	h.scan()

	parked := h.unmatched()
	if len(parked) != 1 || parked[0].Reason != reasonNoAbsoluteMatch {
		t.Fatalf("unmatched queue = %+v, want one park for %q", parked, reasonNoAbsoluteMatch)
	}

	// The user answers "this is that series" — and this time the provider
	// publishes the order.
	absoluteShowTree(h, true)
	res, err := h.mgr.ImportUnmatched(ctx, parked[0].ID, core.TMDBRef(77), MediaTypeSeries)
	if err != nil {
		t.Fatalf("ImportUnmatched: %v", err)
	}
	const want = "library/TV/Show (2023)/Season 05/Show (2023) - S05E03 - Episode 105.mkv"
	if res.Path != want {
		t.Fatalf("manual import landed at %q, want %q", res.Path, want)
	}
	if parked := h.unmatched(); len(parked) != 0 {
		t.Errorf("unmatched queue = %+v, want empty after the manual match", parked)
	}
}

// The same door refuses rather than guesses: a series that cannot place the
// number is not a series the file may be filed against, and the caller is a
// person who can pick another one.
func TestImportUnmatchedRefusesAnUnplaceableAbsoluteNumber(t *testing.T) {
	ctx := context.Background()
	h := newHarness(t)
	absoluteShowTree(h, false)
	raw := "library/TV/[Group] Show - 105.mkv"
	h.parser[filepath.Base(raw)] = absoluteParse("Show", 105)
	h.writeVideo(raw, "episode bytes")
	h.scan()

	parked := h.unmatched()
	if len(parked) != 1 {
		t.Fatalf("unmatched queue = %+v, want the file parked", parked)
	}
	_, err := h.mgr.ImportUnmatched(ctx, parked[0].ID, core.TMDBRef(77), MediaTypeSeries)
	if err == nil {
		t.Fatal("ImportUnmatched placed a number the series does not publish")
	}
	if !strings.Contains(err.Error(), reasonNoAbsoluteMatch) {
		t.Errorf("error = %v, want it to name %q", err, reasonNoAbsoluteMatch)
	}
	if !h.exists(raw) {
		t.Errorf("refused import moved %s", raw)
	}
}
