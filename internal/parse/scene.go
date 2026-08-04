package parse

import (
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
)

// reSceneDate is the date a scene release is named by: two- or four-digit
// year, month, day, in the separator set release names use.
//
// The word boundaries are what keep a site's own digits out of the date.
// "BangBros18.19.07.02" and "21Naturals.20.09.09" both put a number adjacent to
// the date, and in both the site's digits are welded to letters, so the first
// boundary-delimited candidate is the date and not the tail of the name.
//
// The four-digit alternative is tried first so "Blacked.2021.05.15" reads as a
// 2021 date rather than as year 20 followed by 21.05.
var reSceneDate = regexp.MustCompile(`\b(\d{4}|\d{2})[._\-\s](\d{2})[._\-\s](\d{2})\b`)

// sceneCentury is the pivot for two-digit years: 70–99 read as 19xx, 00–69 as
// 20xx. Scene sites did not exist before the late 1990s and this code will not
// outlive 2069, so every real name lands in the 2000s either way; the pivot
// exists so the rule is stated rather than assumed.
const sceneCentury = 70

// Scene parses a date-based adult release name — "Site.YY.MM.DD.Performers.And.
// Title.quality…" — into the same core.ParsedRelease every other release
// produces (PLAN phase 9 task 4).
//
// Two things make it a separate entry point rather than a branch inside Parse.
// The first is selection: a name is read this way because of where it came
// from — a file under the adult library root, or a search result an indexer
// filed under a 6000-series category — never because of how it looks. A date
// in a television name is a daily episode, which is explicitly out of scope
// (SPEC §16), and letting Parse guess would change what existing names mean.
// The second is what identity means here: a scene has no season/episode tag,
// because the number it lands on is its sequence within the site's release
// year, which is a fact about the site's catalogue rather than about the file.
// So this parser reports the DATE and lets the library layer, which can see
// every scene at once, turn it into (season, episode).
//
// Everything else is the ordinary parser's work, reused verbatim: quality,
// source, codec, audio, bit depth, edition, PROPER/REPACK and the release
// group all come from the same rule tables, so an adult release and a
// television release describe their video the same way.
//
// A name that carries no usable date falls through to Parse. That is what
// makes the category selector safe: an indexer that files a television or
// movie release under XXX still yields a correct parse, and the caller tells
// the two apart with ParsedRelease.IsScene.
func Scene(name string) core.ParsedRelease {
	p := core.ParsedRelease{Quality: core.QualityUnknown, Source: core.SourceUnknown}

	work := strings.TrimSpace(name)
	if Container(work) != "" {
		work = work[:strings.LastIndexByte(work, '.')]
	}
	if work == "" {
		return p
	}

	// Same substitution Parse makes, and for the same reason: '_' is always a
	// separator in a release name, and the swap is byte-for-byte so every index
	// below is valid against work.
	scan := strings.ReplaceAll(work, "_", ".")

	date, dateStart, ok := sceneDate(scan)
	if !ok {
		return Parse(name)
	}

	p.SceneDate = date
	p.Year = date.Year()
	// The season a scene lands in is its release year, which is the whole of
	// the site-as-series mapping that a filename can answer for. The episode
	// number deliberately stays empty — see the doc comment.
	p.Season = date.Year()

	quality, _ := matchRules(qualityRules, scan)
	source, _ := matchRules(sourceRules, scan)
	codec, _ := matchRules(codecRules, scan)
	audio, _ := matchRules(audioRules, scan)
	bitDepth, _ := matchRules(bitDepthRules, scan)
	edition, _ := matchRules(editionRules, scan)

	if quality != "" {
		p.Quality = quality
	}
	if source != "" {
		p.Source = source
	}
	// The same determination Parse makes: a DVD release with no resolution tag
	// is standard definition by construction.
	if p.Quality == core.QualityUnknown && p.Source == core.SourceDVD {
		p.Quality = core.Quality480p
	}
	p.Codec, p.Audio, p.Edition = codec, audio, edition
	p.BitDepth = atoi(bitDepth)
	p.Proper = reProper.MatchString(scan)
	p.Repack = reRepack.MatchString(scan)

	// The title is the site, and the site is everything before the date. There
	// is no cut to compute: a scene name puts the site first and the date
	// immediately after it, and what follows the date is performers and a scene
	// title that no rule can reliably separate — and does not need to, because
	// the date is what identifies the scene.
	p.Title = normalizeTitle(work[:dateStart])

	tailEnd, suffixGroups := bracketSuffixes(work)
	p.Group = parseGroup(work, tailEnd, "", suffixGroups, [2]int{-1, -1})
	p.Confidence = sceneConfidence(p)
	return p
}

// sceneDate returns the first candidate in s that is a real calendar date,
// along with where it starts.
//
// Validation is the whole job. "Site.22.13.14" and "Site.22.02.32" are
// date-SHAPED and are not dates, and 2021 has no February 29th — treating any
// of them as a date would file a scene under a season that does not exist, so
// each is skipped and the scan continues rather than being rolled forward the
// way time.Date would.
func sceneDate(s string) (time.Time, int, bool) {
	for _, m := range reSceneDate.FindAllStringSubmatchIndex(s, -1) {
		year := atoi(s[m[2]:m[3]])
		if m[3]-m[2] == 2 {
			if year >= sceneCentury {
				year += 1900
			} else {
				year += 2000
			}
		}
		month, day := atoi(s[m[4]:m[5]]), atoi(s[m[6]:m[7]])
		if month < 1 || month > 12 || day < 1 || day > 31 {
			continue
		}
		date := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
		// time.Date normalizes an impossible day instead of failing, so the
		// round trip is the test: February 29th of a common year comes back as
		// March 1st, and that is a different date than the name claimed.
		if date.Day() != day || date.Month() != time.Month(month) {
			continue
		}
		return date, m[0], true
	}
	return time.Time{}, 0, false
}

// sceneConfidence scores a scene parse. The weights differ from confidence()'s
// because the shape does: a full release date is stronger identity than the
// bare year a movie name carries, and there is no season/episode tag to score.
//
// A name with a date but no site scores 0.40 plus whatever technical tags it
// carries, which keeps it under the 0.5 import threshold: a date alone names
// nothing to match against.
func sceneConfidence(p core.ParsedRelease) float64 {
	score := 0.0
	if reHasAlnum.MatchString(p.Title) {
		score += 0.35
	}
	if p.IsScene() {
		score += 0.40
	}
	if p.Quality != core.QualityUnknown {
		score += 0.10
	}
	if p.Source != core.SourceUnknown {
		score += 0.05
	}
	if p.Group != "" {
		score += 0.10
	}
	return math.Round(math.Min(score, 1)*100) / 100
}
