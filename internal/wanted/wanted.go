// Package wanted is Caravan's automation brain: given a quality profile and a
// set of candidate releases, it decides what to grab
// and records why everything else lost. It is pure logic over core types: no
// I/O, no database, so the whole decision table is unit-testable.
//
// The vocabulary here is what lands in `grabs.reason` and the activity feed,
// so rejection strings are written for the user, not the debugger.
package wanted

import (
	"fmt"
	"strings"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/parse"
)

// Scoring weights. Quality dominates by an order of magnitude, source breaks
// quality ties, and the proper/repack and seeder terms break what's left.
// The gaps are deliberate: a 100-point quality step can never be overtaken by
// a better source on a lower quality, and no number of seeders turns a 720p
// into a 1080p.
const (
	qualityWeight = 1000
	sourceWeight  = 100
	properBonus   = 40
	repackBonus   = 20
	seedersCap    = 50

	// MaxCustomFormatScore bounds one custom format and their total score.
	// This keeps invalid stored profiles from reversing release order by
	// overflowing an integer.
	MaxCustomFormatScore = 1_000_000
)

// ScoreContributions is the score made by every input dimension. Its fields
// add up to ScoreRelease's score for acceptable releases, so callers can
// explain a result without reconstructing or changing the scoring rule.
type ScoreContributions struct {
	Quality         int
	Source          int
	Proper          int
	Repack          int
	Seeders         int
	CustomFormats   int
	TVCompatibility int
}

// Total returns the score contributed by all dimensions.
func (c ScoreContributions) Total() int {
	return c.Quality + c.Source + c.Proper + c.Repack + c.Seeders + c.CustomFormats + c.TVCompatibility
}

// Decision is the scored outcome for one candidate release. Reject is empty
// when the candidate is acceptable; otherwise it is the user-facing reason the
// candidate was skipped, recorded on the grab history.
type Decision struct {
	Release core.Release
	Score   int
	Reject  string
}

// ScoreRelease evaluates one candidate against a quality profile.
//
// A candidate whose parsed quality is not in the profile's item list is
// rejected outright; everything else earns a score where higher is better.
// Unknown qualities are never in a profile's items (they are not selectable
// rungs), so an unparseable release is rejected rather than gambled on.
func ScoreRelease(r core.Release, p *core.QualityProfile) (score int, reject string) {
	score, reject, _ = ScoreReleaseWithContributions(r, p)
	return score, reject
}

// ScoreReleaseWithContributions evaluates a release with the same rules as
// ScoreRelease and returns the terms that made up its score.
func ScoreReleaseWithContributions(r core.Release, p *core.QualityProfile) (score int, reject string, contributions ScoreContributions) {
	q := r.Parsed.Quality
	idx := indexOf(p.Items, q)
	if idx < 0 {
		return 0, fmt.Sprintf("quality %s is not in profile %q", qualityLabel(q), p.Name), ScoreContributions{}
	}
	if p.MinSeeders > 0 && r.Protocol == core.ProtocolTorrent && r.Seeders < p.MinSeeders {
		return 0, fmt.Sprintf("torrent has %d seeders; profile requires at least %d", r.Seeders, p.MinSeeders), ScoreContributions{}
	}
	if r.Size > 0 {
		if p.MinSizeMB > 0 && sizeBelowMB(r.Size, p.MinSizeMB) {
			return 0, fmt.Sprintf("release size is below the profile minimum of %d MB", p.MinSizeMB), ScoreContributions{}
		}
		if p.MaxSizeMB > 0 && sizeAboveMB(r.Size, p.MaxSizeMB) {
			return 0, fmt.Sprintf("release size exceeds the profile maximum of %d MB", p.MaxSizeMB), ScoreContributions{}
		}
	}
	tvPolicy := effectiveTVCompatibilityPolicy(p)
	compatibility := core.TVCompatibility{Verdict: core.TVCompatUnknown}
	if tvPolicy != core.TVCompatibilityPolicyIgnore {
		compatibility = core.ResolveTVProfile(effectiveTVProfile(p)).Check(releaseMediaTags(r))
	}
	if tvPolicy == core.TVCompatibilityPolicyRequire &&
		(compatibility.Verdict == core.TVCompatNeedsRemux || compatibility.Verdict == core.TVCompatIncompatible) {
		return 0, fmt.Sprintf(
			"release is %s for required playback target %q",
			compatibility.Verdict,
			effectiveTVProfile(p),
		), ScoreContributions{}
	}

	// Earlier items are better, so invert the position: the first item scores
	// len(items) rungs, the last scores one.
	rungs := len(p.Items) - idx
	contributions.Quality = rungs * qualityWeight
	contributions.Source = sourceScore(p.PreferredSources, r.Parsed.Source)
	if properRepackPreference(p) == core.ProperRepackPreferencePrefer {
		if r.Parsed.Proper {
			contributions.Proper = properBonus
		}
		if r.Parsed.Repack {
			contributions.Repack = repackBonus
		}
	}
	contributions.Seeders = min(r.Seeders, seedersCap)
	contributions.CustomFormats = customFormatScore(r.Title, p.CustomFormats)
	if tvPolicy == core.TVCompatibilityPolicyPrefer {
		contributions.TVCompatibility = tvCompatibilityScore(compatibility.Verdict)
	}
	return contributions.Total(), "", contributions
}

// BelowCutoff reports whether a file's quality is below the profile's cutoff,
// meaning Caravan should keep looking for something better (SPEC §9). The
// comparison is on the global ladder, not the profile's item order: a cutoff
// of 1080p is met by a 2160p file whether or not 2160p is in the items.
func BelowCutoff(fileQuality string, p *core.QualityProfile) bool {
	return core.QualityRank(fileQuality) > core.QualityRank(p.Cutoff)
}

// IsUpgrade reports whether replacing a file of quality current with one of
// quality next would be an improvement on the global ladder.
func IsUpgrade(next, current string) bool {
	return core.QualityRank(next) < core.QualityRank(current)
}

// SelectBest scores every candidate and returns the winner plus the full
// decision table. A nil winner means nothing was acceptable; the rejected
// slice then carries the explanation for every candidate, best-scoring first
// so the top rows answer "why was this skipped".
func SelectBest(candidates []core.Release, p *core.QualityProfile) (best *core.Release, rejected []Decision) {
	bestScore := 0
	for _, r := range candidates {
		score, reject := ScoreRelease(r, p)
		if reject != "" {
			rejected = append(rejected, Decision{Release: r, Reject: reject})
			continue
		}
		if best == nil || score > bestScore {
			if best != nil {
				rejected = append(rejected, Decision{
					Release: *best,
					Score:   bestScore,
					Reject:  fmt.Sprintf("scored lower than %q", r.Title),
				})
			}
			r := r
			best = &r
			bestScore = score
			continue
		}
		rejected = append(rejected, Decision{Release: r, Score: score, Reject: "scored lower than the winning candidate"})
	}
	return best, rejected
}

// qualityLabel renders a quality for a rejection message. The parser's
// "unknown" reads as jargon to a user; say what actually happened.
func qualityLabel(q string) string {
	if q == core.QualityUnknown || q == "" {
		return "could not be determined"
	}
	return q
}

const bytesPerMB int64 = 1024 * 1024

func sourceScore(preferredSources []string, source string) int {
	sources := preferredSources
	if len(sources) == 0 {
		sources = core.SourceLadder
	}
	for i, candidate := range sources {
		if source == candidate {
			return (len(sources) - i) * sourceWeight
		}
	}
	return 0
}

func properRepackPreference(p *core.QualityProfile) string {
	if p.ProperRepackPreference == "" {
		return core.ProperRepackPreferencePrefer
	}
	return p.ProperRepackPreference
}

func sizeBelowMB(size, minimumMB int64) bool {
	return size/bytesPerMB < minimumMB
}

func sizeAboveMB(size, maximumMB int64) bool {
	sizeMB := size / bytesPerMB
	return sizeMB > maximumMB || (sizeMB == maximumMB && size%bytesPerMB != 0)
}

func customFormatScore(title string, formats []core.CustomFormat) int {
	title = strings.ToLower(title)
	score := 0
	for _, format := range formats {
		if !customFormatMatches(title, format) {
			continue
		}
		formatScore := boundedCustomFormatScore(format.Score)
		if formatScore > 0 && score > MaxCustomFormatScore-formatScore {
			score = MaxCustomFormatScore
			continue
		}
		if formatScore < 0 && score < -MaxCustomFormatScore-formatScore {
			score = -MaxCustomFormatScore
			continue
		}
		score += formatScore
	}
	return score
}

func boundedCustomFormatScore(score int) int {
	if score > MaxCustomFormatScore {
		return MaxCustomFormatScore
	}
	if score < -MaxCustomFormatScore {
		return -MaxCustomFormatScore
	}
	return score
}

func customFormatMatches(title string, format core.CustomFormat) bool {
	included := false
	for _, term := range format.IncludeTerms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		included = true
		if !strings.Contains(title, strings.ToLower(term)) {
			return false
		}
	}
	if !included {
		return false
	}
	for _, term := range format.ExcludeTerms {
		term = strings.TrimSpace(term)
		if term != "" && strings.Contains(title, strings.ToLower(term)) {
			return false
		}
	}
	return true
}

func effectiveTVProfile(p *core.QualityProfile) string {
	if p.TVProfile == "" {
		return core.TVProfileSafe
	}
	return p.TVProfile
}

func effectiveTVCompatibilityPolicy(p *core.QualityProfile) string {
	if p.TVCompatibilityPolicy == "" {
		return core.TVCompatibilityPolicyIgnore
	}
	return p.TVCompatibilityPolicy
}

func releaseMediaTags(r core.Release) core.MediaTags {
	return core.MediaTags{
		Codec:     r.Parsed.Codec,
		BitDepth:  r.Parsed.BitDepth,
		Audio:     r.Parsed.Audio,
		Container: parse.Container(r.Title),
		Quality:   r.Parsed.Quality,
	}
}

func tvCompatibilityScore(verdict string) int {
	switch verdict {
	case core.TVCompatCompatible:
		return 60
	case core.TVCompatNeedsRemux:
		return 20
	case core.TVCompatIncompatible:
		return -60
	default:
		return 0
	}
}

func indexOf(items []string, v string) int {
	for i, item := range items {
		if item == v {
			return i
		}
	}
	return -1
}
