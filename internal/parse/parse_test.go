package parse

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// minCorpusEntries pins the "the parser corpus only ever grows" rule: deleting
// cases to make a change pass has to fail the build.
const minCorpusEntries = 60

// expectation is one corpus line: the fields a name must parse into, plus an
// optional confidence band.
type expectation struct {
	rel       core.ParsedRelease
	container string
	minConf   float64
	maxConf   float64
	hasMin    bool
	hasMax    bool
}

func TestCorpus(t *testing.T) {
	runCorpus(t, "corpus.txt", "Parse", Parse, minCorpusEntries)
}

// runCorpus drives one corpus file through one parser entry point. Both
// corpora share it so a field added to the expectation format is checked by
// every corpus at once, instead of one of them silently ignoring it.
func runCorpus(t *testing.T, file, fn string, parse func(string) core.ParsedRelease, minEntries int) {
	t.Helper()

	path := filepath.Join("testdata", file)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}

	entries := 0
	for i, line := range strings.Split(string(data), "\n") {
		lineNo := i + 1
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, spec, ok := strings.Cut(line, " | ")
		if !ok {
			t.Fatalf("%s:%d: missing %q separator", path, lineNo, " | ")
		}
		want, err := parseExpectation(spec)
		if err != nil {
			t.Fatalf("%s:%d: %v", path, lineNo, err)
		}
		entries++

		t.Run(fmt.Sprintf("line%d", lineNo), func(t *testing.T) {
			got := parse(name)
			checkParsed(t, fn, name, got, want)
			if got := Container(name); got != want.container {
				t.Errorf("Container(%q) = %q, want %q", name, got, want.container)
			}
		})
	}

	if entries < minEntries {
		t.Errorf("%s has %d entries, want at least %d (the corpus only grows)", file, entries, minEntries)
	}
}

func checkParsed(t *testing.T, fn, name string, got core.ParsedRelease, want expectation) {
	t.Helper()

	w := want.rel
	if got.Title != w.Title {
		t.Errorf("%s(%q).Title = %q, want %q", fn, name, got.Title, w.Title)
	}
	if got.Year != w.Year {
		t.Errorf("%s(%q).Year = %d, want %d", fn, name, got.Year, w.Year)
	}
	if got.Season != w.Season {
		t.Errorf("%s(%q).Season = %d, want %d", fn, name, got.Season, w.Season)
	}
	if !slices.Equal(got.Episodes, w.Episodes) {
		t.Errorf("%s(%q).Episodes = %v, want %v", fn, name, got.Episodes, w.Episodes)
	}
	// Asserted by omission on every other line in the corpus, which is what
	// makes the whole corpus a false-positive guard for the recognizer.
	if got.Absolute != w.Absolute {
		t.Errorf("%s(%q).Absolute = %d, want %d", fn, name, got.Absolute, w.Absolute)
	}
	if got.Quality != w.Quality {
		t.Errorf("%s(%q).Quality = %q, want %q", fn, name, got.Quality, w.Quality)
	}
	if got.Source != w.Source {
		t.Errorf("%s(%q).Source = %q, want %q", fn, name, got.Source, w.Source)
	}
	if got.Codec != w.Codec {
		t.Errorf("%s(%q).Codec = %q, want %q", fn, name, got.Codec, w.Codec)
	}
	if got.Audio != w.Audio {
		t.Errorf("%s(%q).Audio = %q, want %q", fn, name, got.Audio, w.Audio)
	}
	if got.BitDepth != w.BitDepth {
		t.Errorf("%s(%q).BitDepth = %d, want %d", fn, name, got.BitDepth, w.BitDepth)
	}
	if got.Group != w.Group {
		t.Errorf("%s(%q).Group = %q, want %q", fn, name, got.Group, w.Group)
	}
	if got.Proper != w.Proper {
		t.Errorf("%s(%q).Proper = %v, want %v", fn, name, got.Proper, w.Proper)
	}
	if got.Repack != w.Repack {
		t.Errorf("%s(%q).Repack = %v, want %v", fn, name, got.Repack, w.Repack)
	}
	if got.Edition != w.Edition {
		t.Errorf("%s(%q).Edition = %q, want %q", fn, name, got.Edition, w.Edition)
	}
	if !got.SceneDate.Equal(w.SceneDate) {
		t.Errorf("%s(%q).SceneDate = %v, want %v", fn, name, got.SceneDate, w.SceneDate)
	}
	if want.hasMin && got.Confidence < want.minConf {
		t.Errorf("%s(%q).Confidence = %.2f, want >= %.2f", fn, name, got.Confidence, want.minConf)
	}
	if want.hasMax && got.Confidence > want.maxConf {
		t.Errorf("%s(%q).Confidence = %.2f, want <= %.2f", fn, name, got.Confidence, want.maxConf)
	}
}

// parseExpectation reads "key=value; key=value". Omitted keys assert the zero
// value; quality and source default to unknown, which is what Parse returns
// when it recognizes neither.
func parseExpectation(spec string) (expectation, error) {
	want := expectation{rel: core.ParsedRelease{
		Quality: core.QualityUnknown,
		Source:  core.SourceUnknown,
	}}

	for _, field := range strings.Split(spec, ";") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		key, value, ok := strings.Cut(field, "=")
		if !ok {
			return want, fmt.Errorf("field %q is not key=value", field)
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)

		var err error
		switch key {
		case "title":
			want.rel.Title = value
		case "year":
			want.rel.Year, err = strconv.Atoi(value)
		case "season":
			want.rel.Season, err = strconv.Atoi(value)
		case "absolute":
			want.rel.Absolute, err = strconv.Atoi(value)
		case "episodes":
			for _, e := range strings.Split(value, ",") {
				n, convErr := strconv.Atoi(strings.TrimSpace(e))
				if convErr != nil {
					err = convErr
					break
				}
				want.rel.Episodes = append(want.rel.Episodes, n)
			}
		case "quality":
			want.rel.Quality = value
		case "source":
			want.rel.Source = value
		case "codec":
			want.rel.Codec = value
		case "audio":
			want.rel.Audio = value
		case "bitdepth":
			want.rel.BitDepth, err = strconv.Atoi(value)
		case "group":
			want.rel.Group = value
		case "edition":
			want.rel.Edition = value
		case "scenedate":
			want.rel.SceneDate, err = time.Parse("2006-01-02", value)
		case "proper":
			want.rel.Proper, err = strconv.ParseBool(value)
		case "repack":
			want.rel.Repack, err = strconv.ParseBool(value)
		case "container":
			want.container = value
		case "minconf":
			want.minConf, err = strconv.ParseFloat(value, 64)
			want.hasMin = true
		case "maxconf":
			want.maxConf, err = strconv.ParseFloat(value, 64)
			want.hasMax = true
		default:
			return want, fmt.Errorf("unknown key %q", key)
		}
		if err != nil {
			return want, fmt.Errorf("field %q: %w", field, err)
		}
	}
	return want, nil
}

// Confidence is what decides auto-import versus the unmatched queue
// (SPEC §13), so the weights are pinned rather than only banded.
func TestConfidenceWeights(t *testing.T) {
	tests := []struct {
		name string
		want float64
	}{
		{name: "Big.Buck.Bunny.2008.1080p.BluRay.DTS.x264-GROUP", want: 1},
		{name: "Some.Show.S01E01.1080p.BluRay.DTS.x264-GROUP", want: 1},
		{name: "Big Buck Bunny (2008)", want: 0.55},
		{name: "Some.Show.S01E01", want: 0.55},
		{name: "Some.Show.S01", want: 0.47},
		{name: "Just A Title", want: 0.15},
		{name: "", want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Parse(tt.name).Confidence; got != tt.want {
				t.Errorf("Parse(%q).Confidence = %.2f, want %.2f", tt.name, got, tt.want)
			}
		})
	}
}

// Garbage in must never panic and must never claim structure (SPEC §13: it
// parks in the review queue instead).
func TestParseDegradesGracefully(t *testing.T) {
	names := []string{
		"", " ", ".", "...", "-", "[", "[]", "[]-", "()", "S", "E", "SE",
		"S00E", "E00", "-.-", "___", ".mkv", "[].mkv", "----GROUP",
		"\x00\x01", "日本語のファイル名", strings.Repeat("a.", 500),
		strings.Repeat("S01E01", 100), "1x", "x1", "0x00",
	}

	for _, name := range names {
		t.Run(strconv.Quote(name), func(t *testing.T) {
			got := Parse(name)
			if got.Confidence < 0 || got.Confidence > 1 {
				t.Errorf("Confidence = %v, want within [0,1]", got.Confidence)
			}
			if !slices.IsSorted(got.Episodes) {
				t.Errorf("Episodes = %v, want ascending", got.Episodes)
			}
		})
	}
}

func TestContainer(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "Movie.2019.1080p.BluRay.x264-GROUP.mkv", want: "mkv"},
		{name: "Movie (2019).MP4", want: "mp4"},
		{name: "Movie.2019.ts", want: "ts"},
		{name: "Movie.2019.1080p.BluRay.x264-GROUP", want: ""},
		{name: "Movie.2019.1080p.WEB-DL-[YTS.MX]", want: ""},
		{name: "Movie.2019.en.srt", want: ""},
		{name: "noextension", want: ""},
		{name: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Container(tt.name); got != tt.want {
				t.Errorf("Container(%q) = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// The multi-episode forms are the parser's sharpest edge (a wrong expansion
// links a file to the wrong episodes), so they get direct coverage on top of
// the corpus.
func TestMultiEpisodeForms(t *testing.T) {
	tests := []struct {
		name   string
		season int
		want   []int
	}{
		{name: "Show.S01E01.1080p.WEB-DL.x264-G", season: 1, want: []int{1}},
		{name: "Show.S01E01E02.1080p.WEB-DL.x264-G", season: 1, want: []int{1, 2}},
		{name: "Show.S01E02E01.1080p.WEB-DL.x264-G", season: 1, want: []int{1, 2}},
		{name: "Show.S01E01E01.1080p.WEB-DL.x264-G", season: 1, want: []int{1}},
		{name: "Show.S01E01-E03.1080p.WEB-DL.x264-G", season: 1, want: []int{1, 2, 3}},
		{name: "Show.S01E01-03.1080p.WEB-DL.x264-G", season: 1, want: []int{1, 2, 3}},
		{name: "Show S01E01 - E02 1080p WEB-DL x264-G", season: 1, want: []int{1, 2}},
		{name: "Show.S01E03-E01.1080p.WEB-DL.x264-G", season: 1, want: []int{3}},
		{name: "Show.S10E100.1080p.WEB-DL.x264-G", season: 10, want: []int{100}},
		{name: "Show.2x05.1080p.WEB-DL.x264-G", season: 2, want: []int{5}},
		{name: "Show.S04.1080p.WEB-DL.x264-G", season: 4, want: nil},
		{name: "Show.Season.4.1080p.WEB-DL.x264-G", season: 4, want: nil},
		{name: "Movie.2019.1080p.WEB-DL.x264-G", season: 0, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse(tt.name)
			if got.Season != tt.season {
				t.Errorf("Season = %d, want %d", got.Season, tt.season)
			}
			if !slices.Equal(got.Episodes, tt.want) {
				t.Errorf("Episodes = %v, want %v", got.Episodes, tt.want)
			}
			if got.Group != "G" {
				t.Errorf("Group = %q, want %q", got.Group, "G")
			}
		})
	}
}

// Group detection is where a parser most easily invents data: an episode title
// or an episode-range tail must never be read as a release group.
func TestGroupIsNotInvented(t *testing.T) {
	tests := []struct {
		name string
		want string
	}{
		{name: "Show - S01E01 - Episode Title.mkv", want: ""},
		{name: "Show.S01E01-E03.mkv", want: ""},
		{name: "Spider-Man.2002.1080p.BluRay.x264.mkv", want: ""},
		{name: "Show.S01E01.720p.HDTV.x264-KILLERS[eztv]", want: "KILLERS"},
		{name: "Movie.2019.1080p.WEBRip.x264.AAC-[YTS.MX]", want: "YTS.MX"},
		{name: "[SubsPlease] Show - 01 (1080p) [A1B2C3D4].mkv", want: "SubsPlease"},
		{name: "Movie.2019.1080p.BluRay.x264-1080p", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Parse(tt.name).Group; got != tt.want {
				t.Errorf("Parse(%q).Group = %q, want %q", tt.name, got, tt.want)
			}
		})
	}
}

// The anime shape, end to end. Title is asserted on every row: cutting the
// number out of the title is what makes the derived search query right, and it
// is the half of this feature the library layer cannot repair later.
func TestParseAbsoluteEpisode(t *testing.T) {
	tests := []struct {
		name string
		want core.ParsedRelease
	}{{
		name: "[SubsPlease] Show - 105 (1080p) [A1B2C3D4].mkv",
		want: core.ParsedRelease{Title: "Show", Absolute: 105, Quality: core.Quality1080p, Group: "SubsPlease"},
	}, {
		// No dash and no tags at all: the bare form carries the whole name.
		name: "Show 105.mkv",
		want: core.ParsedRelease{Title: "Show", Absolute: 105},
	}, {
		// Dots for separators, the same claim.
		name: "Show.-.05.mkv",
		want: core.ParsedRelease{Title: "Show", Absolute: 5},
	}, {
		// A version suffix marks a re-encode of one episode, not another
		// episode; it belongs in neither the number nor the title.
		name: "Show - 105v2",
		want: core.ParsedRelease{Title: "Show", Absolute: 105},
	}, {
		// Zero-padded and under 100: the padding is what tells this apart from
		// a movie title ending in a number, so the bare form accepts it.
		name: "Show 012.mkv",
		want: core.ParsedRelease{Title: "Show", Absolute: 12},
	}, {
		// The quality/absolute collision, pinned deliberately. "1080" in the
		// dash form is a number the name offers as an episode: bare 1080 is not
		// a resolution (the resolution rules all require the trailing p or i,
		// and "[1080p]" is where this name states its resolution), so reading
		// it as episode 1080 is reading the name as written. The alternative,
		// suppressing any number that looks like a resolution, would lose One
		// Piece's genuine four-digit episodes to a coincidence.
		name: "[Erai-raws] Show - 1080 [1080p]",
		want: core.ParsedRelease{Title: "Show", Absolute: 1080, Quality: core.Quality1080p, Group: "Erai-raws"},
	}}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse(tt.name)
			if got.Title != tt.want.Title {
				t.Errorf("Title = %q, want %q", got.Title, tt.want.Title)
			}
			if got.Absolute != tt.want.Absolute {
				t.Errorf("Absolute = %d, want %d", got.Absolute, tt.want.Absolute)
			}
			if got.Season != 0 || len(got.Episodes) != 0 {
				t.Errorf("Season/Episodes = %d/%v, want 0/none: an absolute number is never a placed episode", got.Season, got.Episodes)
			}
			if !got.IsAbsoluteEpisode() {
				t.Error("IsAbsoluteEpisode() = false, want true")
			}
			if got.IsEpisode() {
				t.Error("IsEpisode() = true, want false: the name named no season")
			}
			if got.Quality != tt.want.Quality && tt.want.Quality != "" {
				t.Errorf("Quality = %q, want %q", got.Quality, tt.want.Quality)
			}
			if got.Group != tt.want.Group {
				t.Errorf("Group = %q, want %q", got.Group, tt.want.Group)
			}
		})
	}
}

// The important half. Every row asserts the number is refused AND that Title
// and Year are byte-for-byte what the parser produced before the recognizer
// existed: a false positive here does not merely miss a number, it cuts a movie
// title in half and sends the wrong search query to the provider.
//
// The wanted values were captured from the pre-change parser, not written from
// memory.
func TestParseAbsoluteNegatives(t *testing.T) {
	tests := []struct {
		name  string
		title string
		year  int
		why   string
	}{
		{name: "Ocean's 11 (2001) 1080p BluRay", title: "Ocean's 11", year: 2001,
			why: "unpadded and under 100"},
		{name: "Apollo 13 1995", title: "Apollo 13", year: 1995,
			why: "unpadded and under 100"},
		{name: "Cars 3 2017", title: "Cars 3", year: 2017,
			why: "single digit"},
		{name: "Blade Runner 2049 (2017)", title: "Blade Runner 2049", year: 2017,
			why: "2049 is a year shape, and the name carries a year besides"},
		{name: "1917 (2019)", title: "1917", year: 2019,
			why: "the title IS a year shape"},
		{name: "1917.2019.1080p.BluRay.x264-VETO", title: "1917", year: 2019,
			why: "same title, full scene name"},
		{name: "2012.2009.720p.BluRay.x264-METiS", title: "2012", year: 2009,
			why: "same again, and the year sits in the title span"},
		{name: "Show S01E05", title: "Show", year: 0,
			why: "the name already named a season and an episode"},
		{name: "Show - 105-106", title: "Show - 105-106", year: 0,
			why: "a range is refused whole, title included"},
		{name: "Show 2 - 05", title: "Show 2 - 05", year: 0,
			why: "the number in front of the dash is a season"},
		{name: "Spider-Man.Into.the.Spider-Verse.2018.1080p.BluRay.x264-SPARKS",
			title: "Spider-Man Into the Spider-Verse", year: 2018,
			why: "a hyphenated title is not the dash form"},
		{name: "Movie.With.No.Year.1080p.WEB-DL.DDP5.1.H.264-NTb",
			title: "Movie With No Year", year: 0,
			why: "H.264 and DDP5.1 are technical tags, and they sit past the title span"},
		{name: "The.Departed.2006.1080p.BluRay.x264.anoXmous", title: "The Departed", year: 2006,
			why: "no candidate at all — the guard is that it stays that way"},
		{name: "Se7en.1995.REMASTERED.1080p.BluRay.DD5.1.x264-playHD", title: "Se7en", year: 1995,
			why: "digits inside title and audio tags alike"},
		{name: "Fahrenheit 451 (1966) 1080p BluRay", title: "Fahrenheit 451", year: 1966,
			why: "over 100 and unpadded: only the year rule keeps this title whole"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Parse(tt.name)
			if got.Absolute != 0 {
				t.Errorf("Absolute = %d, want 0 (%s)", got.Absolute, tt.why)
			}
			if got.IsAbsoluteEpisode() {
				t.Error("IsAbsoluteEpisode() = true, want false")
			}
			if got.Title != tt.title {
				t.Errorf("Title = %q, want %q (unchanged by the recognizer)", got.Title, tt.title)
			}
			if got.Year != tt.year {
				t.Errorf("Year = %d, want %d (unchanged by the recognizer)", got.Year, tt.year)
			}
		})
	}
}

func FuzzParse(f *testing.F) {
	seeds := []string{
		"Big.Buck.Bunny.2008.1080p.BluRay.x264-GROUP",
		"Planet Earth II (2016) - S01E01 - Islands.mkv",
		"[SubsPlease] Frieren - 12 (1080p) [F0E9D2A1].mkv",
		"S01E01-E03", "(((", "]]]", "S99E999-E001", "1x99", "____", "..mkv",
		"[SubsPlease] Show - 105 (1080p)", "Ocean's 11", "Show - 105-106",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, name string) {
		got := Parse(name)
		if got.Confidence < 0 || got.Confidence > 1 {
			t.Fatalf("Parse(%q).Confidence = %v, want within [0,1]", name, got.Confidence)
		}
		if !slices.IsSorted(got.Episodes) {
			t.Fatalf("Parse(%q).Episodes = %v, want ascending", name, got.Episodes)
		}
		if got.Season < 0 {
			t.Fatalf("Parse(%q).Season = %d, want >= 0", name, got.Season)
		}
		if got.Absolute < 0 {
			t.Fatalf("Parse(%q).Absolute = %d, want >= 0", name, got.Absolute)
		}
		// The two numbering claims are exclusive by construction: the resolver
		// reads one or the other and never has to reconcile both.
		if got.Absolute > 0 && len(got.Episodes) > 0 {
			t.Fatalf("Parse(%q) = absolute %d with episodes %v, want at most one of the two", name, got.Absolute, got.Episodes)
		}
	})
}
