// Package parse turns scene release names and messy existing filenames into
// core.ParsedRelease (SPEC §5.1, "Release Parser").
//
// Parse is pure: no I/O, no globals, no network. It is deliberately
// conservative — anything it cannot recognize lowers Confidence rather than
// producing an invented guess, because SPEC §13 requires low-confidence files
// to park in the unmatched review queue instead of being imported silently.
//
// Out of scope for v1 (SPEC §16): anime absolute numbering and daily-dated
// episodes. Those names must degrade to a low-confidence result, never panic.
package parse

import (
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/watzon/caravan/internal/core"
)

// rule maps a token pattern to the canonical value Caravan stores for it.
type rule struct {
	re    *regexp.Regexp
	value string
}

// Quality rules, best-first: the first rule that matches wins, so a name
// carrying both "UHD" and "1080p" resolves to the better claim.
var qualityRules = []rule{
	{regexp.MustCompile(`(?i)\b(2160p|4k|uhd|3840x2160)\b`), core.Quality2160p},
	{regexp.MustCompile(`(?i)\b(1080[pi]|fullhd|1920x1080)\b`), core.Quality1080p},
	{regexp.MustCompile(`(?i)\b(720[pi]|1280x720)\b`), core.Quality720p},
	{regexp.MustCompile(`(?i)\b(480[pi]|576[pi]|640x480)\b`), core.Quality480p},
}

// Source rules. Order matters twice over: WEBRip must be tested before the bare
// "WEB" fallback, and the more specific spellings come before the loose ones.
var sourceRules = []rule{
	{regexp.MustCompile(`(?i)\bweb[\s._-]?rip\b`), core.SourceWebRip},
	{regexp.MustCompile(`(?i)\bweb[\s._-]?dl\b`), core.SourceWebDL},
	{regexp.MustCompile(`(?i)\b(blu[\s._-]?ray|bdrip|brrip|bd(25|50)|bdremux|remux)\b`), core.SourceBluray},
	{regexp.MustCompile(`(?i)\b(hdtv|pdtv|dsr)\b`), core.SourceHDTV},
	{regexp.MustCompile(`(?i)\b(dvd(rip|scr|r)?|ntsc|pal)\b`), core.SourceDVD},
	{regexp.MustCompile(`(?i)\b(camrip|cam|hdcam|hdts|telesync|telecine|ts)\b`), core.SourceCam},
	{regexp.MustCompile(`(?i)\bweb\b`), core.SourceWebDL},
}

// Codec rules. The tag is kept as parsed (x265 and HEVC stay distinct) because
// SPEC §8 surfaces it verbatim in the release picker.
var codecRules = []rule{
	{regexp.MustCompile(`(?i)\bx[\s._-]?264\b`), "x264"},
	{regexp.MustCompile(`(?i)\bx[\s._-]?265\b`), "x265"},
	{regexp.MustCompile(`(?i)\bh[\s._-]?264\b`), "h264"},
	{regexp.MustCompile(`(?i)\bh[\s._-]?265\b`), "h265"},
	{regexp.MustCompile(`(?i)\bhevc\b`), "hevc"},
	{regexp.MustCompile(`(?i)\bavc\b`), "h264"},
	{regexp.MustCompile(`(?i)\bav1\b`), "av1"},
	{regexp.MustCompile(`(?i)\bvp9\b`), "vp9"},
	{regexp.MustCompile(`(?i)\bxvid\b`), "xvid"},
	{regexp.MustCompile(`(?i)\bdivx\b`), "divx"},
	{regexp.MustCompile(`(?i)\bmpeg-?2\b`), "mpeg2"},
}

// Audio rules, most specific first: "TrueHD 7.1 Atmos" is an Atmos release and
// "DTS-HD MA" is not plain DTS.
var audioRules = []rule{
	{regexp.MustCompile(`(?i)\batmos\b`), "Atmos"},
	{regexp.MustCompile(`(?i)\btrue[\s._-]?hd\b`), "TrueHD"},
	{regexp.MustCompile(`(?i)\bdts[\s._-]?x\b`), "DTS-X"},
	{regexp.MustCompile(`(?i)\bdts[\s._-]?hd\b`), "DTS-HD"},
	{regexp.MustCompile(`(?i)\bdts\b`), "DTS"},
	{regexp.MustCompile(`(?i)(\be[\s._-]?ac[\s._-]?3\b|\bdd\+|\bddp\d?\b)`), "EAC3"},
	{regexp.MustCompile(`(?i)(\bac[\s._-]?3\b|\bdd\d?\b)`), "AC3"},
	{regexp.MustCompile(`(?i)\bflac\b`), "FLAC"},
	{regexp.MustCompile(`(?i)\baac\d?\b`), "AAC"},
	{regexp.MustCompile(`(?i)\bmp3\b`), "MP3"},
	{regexp.MustCompile(`(?i)\bopus\b`), "Opus"},
	{regexp.MustCompile(`(?i)\bpcm\b`), "PCM"},
}

// Bit-depth rules. "Hi10P" is the anime spelling of the same claim. The values
// are the digits themselves so the caller parses one integer and no table.
var bitDepthRules = []rule{
	{regexp.MustCompile(`(?i)\b(10[\s._-]?bits?|hi10p?)\b`), "10"},
	{regexp.MustCompile(`(?i)\b8[\s._-]?bits?\b`), "8"},
}

// Edition rules (SPEC §7 keeps Edition free text; these are the canonical
// spellings Caravan renders into filenames).
var editionRules = []rule{
	{regexp.MustCompile(`(?i)\bdirector'?s?[\s._-]?cut\b`), "Director's Cut"},
	{regexp.MustCompile(`(?i)\bextended([\s._-](cut|edition|version))?\b`), "Extended"},
	{regexp.MustCompile(`(?i)\b(the[\s._-])?final[\s._-]?cut\b`), "Final Cut"},
	{regexp.MustCompile(`(?i)\btheatrical([\s._-](cut|edition|version))?\b`), "Theatrical"},
	{regexp.MustCompile(`(?i)\bultimate([\s._-](cut|edition))?\b`), "Ultimate Edition"},
	{regexp.MustCompile(`(?i)\bspecial[\s._-]?edition\b`), "Special Edition"},
	{regexp.MustCompile(`(?i)\b\d+th[\s._-]?anniversary([\s._-]?edition)?\b`), "Anniversary Edition"},
	{regexp.MustCompile(`(?i)\bcriterion([\s._-]?collection)?\b`), "Criterion"},
	{regexp.MustCompile(`(?i)\bredux\b`), "Redux"},
	{regexp.MustCompile(`(?i)\bimax\b`), "IMAX"},
	{regexp.MustCompile(`(?i)\bremastered\b`), "Remastered"},
	{regexp.MustCompile(`(?i)\buncut\b`), "Uncut"},
	{regexp.MustCompile(`(?i)\bunrated\b`), "Unrated"},
}

var (
	// Season/episode forms, tried in this order. The span form has to run
	// before the list form, otherwise S01E01-E03 parses as episodes 1 and 3.
	reEpisodeSpan = regexp.MustCompile(`(?i)\bs(\d{1,2})[\s._-]?e(\d{1,3})[\s._-]*[-~][\s._-]*e?(\d{1,3})\b`)
	reEpisodeList = regexp.MustCompile(`(?i)\bs(\d{1,2})((?:[\s._-]?e\d{1,3})+)\b`)
	reEpisodeX    = regexp.MustCompile(`\b(\d{1,2})x(\d{2,3})\b`)
	reSeasonWord  = regexp.MustCompile(`(?i)\bseason[\s._-]?(\d{1,2})\b`)
	reSeasonShort = regexp.MustCompile(`(?i)\bs(\d{1,2})\b`)
	reEpisodeNum  = regexp.MustCompile(`(?i)e(\d{1,3})`)

	reYear   = regexp.MustCompile(`\b(19|20)\d{2}\b`)
	reProper = regexp.MustCompile(`(?i)\bproper\b`)
	reRepack = regexp.MustCompile(`(?i)\brepack\d?\b`)

	// Tokens that carry no value we store but must never end up in the title.
	reNoise = regexp.MustCompile(`(?i)\b(complete|multi|internal|limited|hybrid|hdr10plus|hdr10|hdr|dovi|dv|sdr|10-?bit|8-?bit|dual[\s._-]?audio|subbed|dubbed|readnfo|open[\s._-]?matte|sample|extras|3d|amzn|nf|dsnp|atvp|hmax|hulu|pcok|itunes)\b`)

	reBracketPrefix = regexp.MustCompile(`^\[([^\]]{1,30})\]\s*`)
	// A release-group suffix: "-GROUP" at the very end, with no whitespace
	// around the dash (that rules out " - Episode Title").
	reDashGroup  = regexp.MustCompile(`[^\s]-([A-Za-z0-9_]{1,20})$`)
	reGroupShape = regexp.MustCompile(`^[A-Za-z0-9_.\-]{1,25}$`)
	reHasLetter  = regexp.MustCompile(`[A-Za-z]`)
	// A title has to carry a word or a number to count as a title: "1917" and
	// "2012" are titles, "!!!" is not.
	reHasAlnum = regexp.MustCompile(`[A-Za-z0-9]`)
	reCRC32    = regexp.MustCompile(`^[0-9A-Fa-f]{8}$`)

	// Dotted acronyms ("S.W.A.T.", "S.H.I.E.L.D.") survive title normalization
	// as one word instead of dissolving into single letters.
	reAcronym = regexp.MustCompile(`(?:\b[A-Za-z]\.){2,}`)
)

// trackerTags are bracket suffixes added by trackers, not release groups.
var trackerTags = map[string]bool{
	"eztv": true, "ettv": true, "rartv": true, "tgx": true, "1337x": true,
	"glodls": true, "torrentgalaxy": true, "publichd": true,
	"nogroup": true, "nogrp": true,
}

// containers are the file extensions Caravan treats as media containers.
var containers = map[string]bool{
	"mkv": true, "mp4": true, "m4v": true, "avi": true, "mov": true,
	"wmv": true, "mpg": true, "mpeg": true, "m2ts": true, "ts": true,
	"flv": true, "webm": true, "ogm": true, "vob": true, "iso": true,
	"divx": true, "rmvb": true,
}

// maxEpisodeSpan bounds S01E01-E99 style ranges; anything wider is a
// misparse, not a release.
const maxEpisodeSpan = 99

// Container returns the lowercase media container extension of name without
// the dot ("mkv"), or "" when name has no recognized container extension.
//
// core.ParsedRelease has no container field (SPEC §7 keeps it out of the
// stored release shape), so the container hint is exposed here for callers
// that need it — the scanner's file filter and the TV-compatibility check.
func Container(name string) string {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return ""
	}
	ext := strings.ToLower(strings.TrimSpace(name[i+1:]))
	if containers[ext] {
		return ext
	}
	return ""
}

// Parse extracts everything a release name claims about itself. It never
// panics and never returns an error: an unparseable name yields a
// near-empty ParsedRelease with a low Confidence.
func Parse(name string) core.ParsedRelease {
	p := core.ParsedRelease{Quality: core.QualityUnknown, Source: core.SourceUnknown}

	work := strings.TrimSpace(name)
	if Container(work) != "" {
		work = work[:strings.LastIndexByte(work, '.')]
	}
	if work == "" {
		return p
	}

	titleStart, prefixGroup := bracketPrefix(work)
	tailEnd, suffixGroups := bracketSuffixes(work)

	// '_' is always a separator in release names and never part of a token, so
	// swapping it for '.' makes \b behave. The swap is byte-for-byte, which
	// keeps every index below valid against work.
	scan := strings.ReplaceAll(work, "_", ".")

	season, episodes, seLoc := parseSeasonEpisodes(scan)
	p.Season, p.Episodes = season, episodes

	quality, qualityIdx := matchRules(qualityRules, scan)
	source, sourceIdx := matchRules(sourceRules, scan)
	codec, codecIdx := matchRules(codecRules, scan)
	audio, audioIdx := matchRules(audioRules, scan)
	bitDepth, bitDepthIdx := matchRules(bitDepthRules, scan)
	edition, editionIdx := matchRules(editionRules, scan)

	if quality != "" {
		p.Quality = quality
	}
	if source != "" {
		p.Source = source
	}
	// A DVD release with no resolution tag is standard definition by
	// construction; that is a determination, not a guess.
	if p.Quality == core.QualityUnknown && p.Source == core.SourceDVD {
		p.Quality = core.Quality480p
	}
	p.Codec, p.Audio, p.Edition = codec, audio, edition
	p.BitDepth = atoi(bitDepth)

	properIdx := firstIndex(reProper, scan)
	repackIdx := firstIndex(reRepack, scan)
	p.Proper, p.Repack = properIdx >= 0, repackIdx >= 0
	noiseIdx := firstIndex(reNoise, scan)

	// The year lives before the first "strong" marker — a season/episode tag,
	// a quality tag or a source tag. Everything after one of those is release
	// metadata, not a title year.
	strong := minIndex(seLoc[0], qualityIdx, sourceIdx)
	year, yearIdx := parseYear(scan, titleStart, strong)
	p.Year = year

	// The title ends at the earliest marker that leaves something behind. A
	// marker sitting at the very start is ignored so titles that legitimately
	// open with a token word ("Extended Family") survive.
	titleCut := minIndexAfter(titleStart,
		seLoc[0], qualityIdx, sourceIdx, codecIdx, audioIdx, bitDepthIdx,
		editionIdx, properIdx, repackIdx, noiseIdx, yearIdx)
	if titleCut < 0 {
		titleCut = len(work)
	}
	p.Title = normalizeTitle(work[titleStart:titleCut])

	p.Group = parseGroup(work, tailEnd, prefixGroup, suffixGroups, seLoc)
	p.Confidence = confidence(p)
	return p
}

// matchRules returns the value of the first rule that matches s and the
// earliest byte index any rule in the set matched at (-1 when none did). The
// value comes from rule priority; the index is used for title cutting, where
// only "how early does release metadata start" matters.
func matchRules(rules []rule, s string) (value string, idx int) {
	idx = -1
	for _, r := range rules {
		loc := r.re.FindStringIndex(s)
		if loc == nil {
			continue
		}
		if value == "" {
			value = r.value
		}
		if idx < 0 || loc[0] < idx {
			idx = loc[0]
		}
	}
	return value, idx
}

func firstIndex(re *regexp.Regexp, s string) int {
	if loc := re.FindStringIndex(s); loc != nil {
		return loc[0]
	}
	return -1
}

// minIndex returns the smallest non-negative value, or -1 when there is none.
func minIndex(idx ...int) int {
	return minIndexAfter(-1, idx...)
}

// minIndexAfter returns the smallest value strictly greater than after, or -1.
func minIndexAfter(after int, idx ...int) int {
	best := -1
	for _, i := range idx {
		if i <= after {
			continue
		}
		if best < 0 || i < best {
			best = i
		}
	}
	return best
}

// parseSeasonEpisodes recognizes SxxEyy, multi-episode files (S01E01E02 and
// S01E01-E03 spans), the 1x05 form, and season packs (S04, "Season 4"). It
// returns the byte span of whatever it matched so the group parser can tell
// "-E03" apart from "-GROUP".
func parseSeasonEpisodes(s string) (season int, episodes []int, loc [2]int) {
	loc = [2]int{-1, -1}

	if m := reEpisodeSpan.FindStringSubmatchIndex(s); m != nil {
		season = atoi(s[m[2]:m[3]])
		first, last := atoi(s[m[4]:m[5]]), atoi(s[m[6]:m[7]])
		if last >= first && last-first <= maxEpisodeSpan {
			for e := first; e <= last; e++ {
				episodes = append(episodes, e)
			}
		} else {
			episodes = []int{first}
		}
		return season, episodes, [2]int{m[0], m[1]}
	}

	if m := reEpisodeList.FindStringSubmatchIndex(s); m != nil {
		season = atoi(s[m[2]:m[3]])
		for _, em := range reEpisodeNum.FindAllStringSubmatch(s[m[4]:m[5]], -1) {
			episodes = append(episodes, atoi(em[1]))
		}
		slices.Sort(episodes)
		return season, slices.Compact(episodes), [2]int{m[0], m[1]}
	}

	if m := reEpisodeX.FindStringSubmatchIndex(s); m != nil {
		return atoi(s[m[2]:m[3]]), []int{atoi(s[m[4]:m[5]])}, [2]int{m[0], m[1]}
	}

	for _, re := range []*regexp.Regexp{reSeasonWord, reSeasonShort} {
		if m := re.FindStringSubmatchIndex(s); m != nil {
			return atoi(s[m[2]:m[3]]), nil, [2]int{m[0], m[1]}
		}
	}
	return 0, nil, loc
}

// parseYear picks the release year from the candidates that sit between the
// title start and the first strong marker. A parenthesized year wins outright
// ("Blade Runner 2049 (2017)"); otherwise the last candidate wins, which is
// what keeps a number in the title ("2012 (2009)", "1917") out of the field.
func parseYear(scan string, titleStart, strong int) (year, idx int) {
	var lastAny, lastParen [2]int
	lastAny[1], lastParen[1] = -1, -1

	for _, m := range reYear.FindAllStringIndex(scan, -1) {
		if m[0] < titleStart || (strong >= 0 && m[0] >= strong) {
			continue
		}
		lastAny = [2]int{atoi(scan[m[0]:m[1]]), m[0]}
		if m[0] > 0 && scan[m[0]-1] == '(' && m[1] < len(scan) && scan[m[1]] == ')' {
			lastParen = lastAny
		}
	}
	if lastParen[1] >= 0 {
		return lastParen[0], lastParen[1]
	}
	if lastAny[1] >= 0 {
		return lastAny[0], lastAny[1]
	}
	return 0, -1
}

// bracketPrefix reports where the title starts (after an anime-style "[Group]"
// prefix, if any) and the bracket's contents.
func bracketPrefix(s string) (start int, group string) {
	m := reBracketPrefix.FindStringSubmatchIndex(s)
	if m == nil {
		return 0, ""
	}
	return m[1], s[m[2]:m[3]]
}

// bracketSuffixes strips trailing "[...]" blocks off the end, returning where
// the run starts and the contents right-to-left. The blocks are only removed
// for group detection: token scanning still sees them, because quality often
// hides in there ("[1080p]").
func bracketSuffixes(s string) (tailEnd int, candidates []string) {
	end := len(s)
	for {
		t := strings.TrimRight(s[:end], " ._-")
		if t == "" || t[len(t)-1] != ']' {
			return end, candidates
		}
		open := strings.LastIndexByte(t, '[')
		if open <= 0 {
			return end, candidates
		}
		candidates = append(candidates, t[open+1:len(t)-1])
		end = open
	}
}

// parseGroup prefers a "-GROUP" suffix, then an anime-style "[Group]" prefix,
// then a trailing bracket ("-[YTS.MX]").
func parseGroup(work string, tailEnd int, prefix string, suffixes []string, seLoc [2]int) string {
	tail := strings.TrimRight(work[:tailEnd], " .")
	if m := reDashGroup.FindStringSubmatchIndex(tail); m != nil {
		dash := m[2] - 1
		inEpisodeTag := seLoc[0] >= 0 && dash >= seLoc[0] && dash < seLoc[1]
		if cand := tail[m[2]:m[3]]; !inEpisodeTag && validGroup(cand) {
			return cand
		}
	}
	if validGroup(prefix) {
		return prefix
	}
	for _, c := range suffixes {
		if validGroup(c) {
			return c
		}
	}
	return ""
}

func validGroup(c string) bool {
	c = strings.TrimSpace(c)
	switch {
	case c == "",
		!reGroupShape.MatchString(c),
		!reHasLetter.MatchString(c),
		reCRC32.MatchString(c),
		trackerTags[strings.ToLower(c)],
		isKnownToken(c):
		return false
	}
	return true
}

// isKnownToken reports whether s is release metadata rather than a name. It is
// the guard that stops "1080p" or "E03" from being read as a release group.
func isKnownToken(s string) bool {
	for _, rules := range [][]rule{qualityRules, sourceRules, codecRules, audioRules, bitDepthRules, editionRules} {
		if v, _ := matchRules(rules, s); v != "" {
			return true
		}
	}
	if reProper.MatchString(s) || reRepack.MatchString(s) || reNoise.MatchString(s) {
		return true
	}
	_, eps, loc := parseSeasonEpisodes(s)
	return len(eps) > 0 || loc[0] >= 0
}

// normalizeTitle turns the raw title span into display form: separators become
// spaces, dotted acronyms survive, and leading/trailing punctuation goes.
func normalizeTitle(s string) string {
	s = reAcronym.ReplaceAllStringFunc(s, func(m string) string {
		return strings.ReplaceAll(m, ".", "")
	})
	s = strings.NewReplacer(".", " ", "_", " ").Replace(s)
	s = strings.Join(strings.Fields(s), " ")
	return strings.Trim(s, " -([{")
}

// confidence scores how much structure the parser actually recognized. The
// weights sum to 1.0 for a complete scene name; identity (episode numbers or a
// year) is weighted heaviest because that is what a metadata match needs.
func confidence(p core.ParsedRelease) float64 {
	score := 0.0
	if reHasAlnum.MatchString(p.Title) {
		score += 0.25
	}
	switch {
	case len(p.Episodes) > 0:
		score += 0.30
	case p.Season > 0:
		score += 0.22
	case p.Year > 0:
		score += 0.30
	}
	if p.Quality != core.QualityUnknown {
		score += 0.12
	}
	if p.Source != core.SourceUnknown {
		score += 0.12
	}
	if p.Codec != "" {
		score += 0.06
	}
	if p.Audio != "" {
		score += 0.05
	}
	if p.Group != "" {
		score += 0.10
	}

	// A name with no identity and no technical tags is a bare string. Whatever
	// it scored for having a title, it is not something to import on.
	if p.Year == 0 && p.Season == 0 && len(p.Episodes) == 0 &&
		p.Quality == core.QualityUnknown && p.Source == core.SourceUnknown &&
		p.Codec == "" && p.Audio == "" {
		score = math.Min(score, 0.15)
	}
	return math.Round(math.Min(score, 1)*100) / 100
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
