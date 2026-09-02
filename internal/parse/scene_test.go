package parse

import (
	"reflect"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// minSceneCorpusEntries is the scene corpus' floor, for the same reason
// minCorpusEntries is. It starts lower because the corpus starts near-empty, but
// it only ever moves up.
const minSceneCorpusEntries = 24

func TestSceneCorpus(t *testing.T) {
	runCorpus(t, "scene_corpus.txt", "Scene", Scene, minSceneCorpusEntries)
}

// A scene parse and an episode parse are mutually exclusive shapes. Anything
// that reads one as the other files a scene under a season that is really a
// year, or a television episode under a date, so the exclusivity is pinned
// rather than left to the corpus' field-by-field assertions.
func TestSceneAndEpisodeShapesAreExclusive(t *testing.T) {
	scene := Scene("Brazzers.22.03.14.Abella.Danger.Deep.Impact.XXX.1080p.MP4-KTR")
	if !scene.IsScene() {
		t.Fatalf("Scene(...).IsScene() = false, want true")
	}
	if scene.IsEpisode() {
		t.Errorf("Scene(...).IsEpisode() = true, want false (episodes = %v)", scene.Episodes)
	}

	episode := Parse("Some.Show.S01E02.1080p.WEB-DL.x264-GROUP")
	if episode.IsScene() {
		t.Errorf("Parse(...).IsScene() = true, want false")
	}
	if !episode.IsEpisode() {
		t.Errorf("Parse(...).IsEpisode() = false, want true")
	}
}

// Parse must never invent a scene date, whatever the name looks like. This is
// what keeps the selector honest: a date-shaped television name means what it
// always meant, and only the adult library and the 6000-series categories
// change how a name is read.
func TestParseNeverProducesASceneDate(t *testing.T) {
	names := []string{
		"Brazzers.22.03.14.Abella.Danger.Deep.Impact.XXX.1080p.MP4-KTR",
		"The.Daily.Show.2023.08.11.1080p.WEB.h264-GROUP",
		"Blacked.2021.05.15.Emily.Willis.2160p.MP4-GALAXY",
	}
	for _, name := range names {
		if got := Parse(name); got.IsScene() {
			t.Errorf("Parse(%q).SceneDate = %v, want zero", name, got.SceneDate)
		}
	}
}

func TestSceneDate(t *testing.T) {
	tests := []struct {
		name  string
		in    string
		want  string // "" means no date
		start int
	}{
		{name: "two digit year", in: "Site.22.03.14.Rest", want: "2022-03-14", start: 5},
		{name: "four digit year", in: "Site.2022.03.14.Rest", want: "2022-03-14", start: 5},
		{name: "iso dashes", in: "Site.2022-03-14.Rest", want: "2022-03-14", start: 5},
		{name: "spaces", in: "Site 22 03 14 Rest", want: "2022-03-14", start: 5},
		{name: "site ending in digits", in: "BangBros18.19.07.02.Rest", want: "2019-07-02", start: 11},
		{name: "site starting with digits", in: "21Naturals.20.09.09.Rest", want: "2020-09-09", start: 11},
		{name: "leap day in a leap year", in: "Site.20.02.29.Rest", want: "2020-02-29", start: 5},
		{name: "leap day in a common year", in: "Site.21.02.29.Rest", want: ""},
		{name: "month 13", in: "Site.22.13.14.Rest", want: ""},
		{name: "month 00", in: "Site.22.00.14.Rest", want: ""},
		{name: "day 32", in: "Site.22.02.32.Rest", want: ""},
		{name: "day 00", in: "Site.22.02.00.Rest", want: ""},
		{name: "no date at all", in: "Site.Some.Scene.1080p", want: ""},
		{name: "resolution is not a date", in: "Site.1080.60.24.Rest", want: ""},
		// The first valid candidate wins: a scene name puts the date right
		// after the site, and a later number is part of the title.
		{name: "first valid candidate wins", in: "Site.22.03.14.Vol.05.06.07.Rest", want: "2022-03-14", start: 5},
		// …but an invalid candidate does not stop the search.
		{name: "skips past an invalid candidate", in: "Site.22.13.14.20.09.09.Rest", want: "2020-09-09", start: 14},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, start, ok := sceneDate(tc.in)
			if tc.want == "" {
				if ok {
					t.Fatalf("sceneDate(%q) = %v, want no date", tc.in, got)
				}
				return
			}
			if !ok {
				t.Fatalf("sceneDate(%q) found no date, want %s", tc.in, tc.want)
			}
			if got.Format("2006-01-02") != tc.want {
				t.Errorf("sceneDate(%q) = %s, want %s", tc.in, got.Format("2006-01-02"), tc.want)
			}
			if start != tc.start {
				t.Errorf("sceneDate(%q) start = %d, want %d", tc.in, start, tc.start)
			}
			if got.Location() != time.UTC {
				t.Errorf("sceneDate(%q) location = %v, want UTC", tc.in, got.Location())
			}
		})
	}
}

// The two-digit pivot is a decision about what a name means, so it is pinned
// at its edges rather than only in the middle.
func TestSceneTwoDigitYearPivot(t *testing.T) {
	tests := map[string]int{
		"Site.69.06.15.Rest": 2069,
		"Site.70.06.15.Rest": 1970,
		"Site.99.06.15.Rest": 1999,
		"Site.00.06.15.Rest": 2000,
	}
	for in, want := range tests {
		got, _, ok := sceneDate(in)
		if !ok {
			t.Fatalf("sceneDate(%q) found no date", in)
		}
		if got.Year() != want {
			t.Errorf("sceneDate(%q).Year() = %d, want %d", in, got.Year(), want)
		}
	}
}

// A scene release describes its video with exactly the tables an ordinary
// release does. If the two ever drift, an adult file and a television file
// would disagree about what "1080p WEB-DL x265" means, and the quality profile
// that grades them is shared.
func TestSceneSharesTheTechnicalRulesWithParse(t *testing.T) {
	const tags = "2160p.WEB-DL.h265.10bit.Atmos-GROUP"
	scene := Scene("Site.22.08.19.Some.Scene." + tags)
	episode := Parse("Some.Show.S01E01." + tags)

	if scene.Quality != episode.Quality || scene.Source != episode.Source ||
		scene.Codec != episode.Codec || scene.Audio != episode.Audio ||
		scene.BitDepth != episode.BitDepth || scene.Group != episode.Group {
		t.Errorf("scene tags %+v differ from episode tags %+v",
			core.ParsedRelease{Quality: scene.Quality, Source: scene.Source, Codec: scene.Codec,
				Audio: scene.Audio, BitDepth: scene.BitDepth, Group: scene.Group},
			core.ParsedRelease{Quality: episode.Quality, Source: episode.Source, Codec: episode.Codec,
				Audio: episode.Audio, BitDepth: episode.BitDepth, Group: episode.Group})
	}
}

// A name with no date must be handed to Parse untouched: including its
// container extension, which Scene strips from its own working copy.
func TestSceneFallsThroughToParse(t *testing.T) {
	const name = "Some.Show.S01E02.1080p.WEB-DL.x264-GROUP.mkv"
	got, want := Scene(name), Parse(name)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Scene(%q) = %+v, want Parse's %+v", name, got, want)
	}
}

func TestSceneNeverPanics(t *testing.T) {
	names := []string{
		"", " ", ".", "..", ".mp4", "22.03.14", "22.03.14.mp4",
		"----", "[]", "22-03-14-", "00.00.00", "9999.99.99",
	}
	for _, name := range names {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Scene(%q) panicked: %v", name, r)
				}
			}()
			_ = Scene(name)
		}()
	}
}
