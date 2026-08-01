package parse

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/watzon/caravan/internal/core"
)

// minCorpusEntries pins PLAN.md's "the parser corpus only ever grows" rule:
// deleting cases to make a change pass has to fail the build.
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
	path := filepath.Join("testdata", "corpus.txt")
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
			got := Parse(name)
			checkParsed(t, name, got, want)
			if got := Container(name); got != want.container {
				t.Errorf("Container(%q) = %q, want %q", name, got, want.container)
			}
		})
	}

	if entries < minCorpusEntries {
		t.Errorf("corpus has %d entries, want at least %d (the corpus only grows)", entries, minCorpusEntries)
	}
}

func checkParsed(t *testing.T, name string, got core.ParsedRelease, want expectation) {
	t.Helper()

	w := want.rel
	if got.Title != w.Title {
		t.Errorf("Parse(%q).Title = %q, want %q", name, got.Title, w.Title)
	}
	if got.Year != w.Year {
		t.Errorf("Parse(%q).Year = %d, want %d", name, got.Year, w.Year)
	}
	if got.Season != w.Season {
		t.Errorf("Parse(%q).Season = %d, want %d", name, got.Season, w.Season)
	}
	if !slices.Equal(got.Episodes, w.Episodes) {
		t.Errorf("Parse(%q).Episodes = %v, want %v", name, got.Episodes, w.Episodes)
	}
	if got.Quality != w.Quality {
		t.Errorf("Parse(%q).Quality = %q, want %q", name, got.Quality, w.Quality)
	}
	if got.Source != w.Source {
		t.Errorf("Parse(%q).Source = %q, want %q", name, got.Source, w.Source)
	}
	if got.Codec != w.Codec {
		t.Errorf("Parse(%q).Codec = %q, want %q", name, got.Codec, w.Codec)
	}
	if got.Audio != w.Audio {
		t.Errorf("Parse(%q).Audio = %q, want %q", name, got.Audio, w.Audio)
	}
	if got.Group != w.Group {
		t.Errorf("Parse(%q).Group = %q, want %q", name, got.Group, w.Group)
	}
	if got.Proper != w.Proper {
		t.Errorf("Parse(%q).Proper = %v, want %v", name, got.Proper, w.Proper)
	}
	if got.Repack != w.Repack {
		t.Errorf("Parse(%q).Repack = %v, want %v", name, got.Repack, w.Repack)
	}
	if got.Edition != w.Edition {
		t.Errorf("Parse(%q).Edition = %q, want %q", name, got.Edition, w.Edition)
	}
	if want.hasMin && got.Confidence < want.minConf {
		t.Errorf("Parse(%q).Confidence = %.2f, want >= %.2f", name, got.Confidence, want.minConf)
	}
	if want.hasMax && got.Confidence > want.maxConf {
		t.Errorf("Parse(%q).Confidence = %.2f, want <= %.2f", name, got.Confidence, want.maxConf)
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
		case "group":
			want.rel.Group = value
		case "edition":
			want.rel.Edition = value
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

func FuzzParse(f *testing.F) {
	seeds := []string{
		"Big.Buck.Bunny.2008.1080p.BluRay.x264-GROUP",
		"Planet Earth II (2016) - S01E01 - Islands.mkv",
		"[SubsPlease] Frieren - 12 (1080p) [F0E9D2A1].mkv",
		"S01E01-E03", "(((", "]]]", "S99E999-E001", "1x99", "____", "..mkv",
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
	})
}
