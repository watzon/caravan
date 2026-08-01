// Package wanted is Caravan's automation brain (PLAN phase 3): given a
// quality profile and a set of candidate releases, it decides what to grab
// and records why everything else lost. It is pure logic over core types: no
// I/O, no database, so the whole decision table is unit-testable.
//
// The vocabulary here is what lands in `grabs.reason` and the activity feed,
// so rejection strings are written for the user, not the debugger.
package wanted

import (
	"fmt"

	"github.com/watzon/caravan/internal/core"
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
)

// Decision is the scored outcome for one candidate release. Reject is empty
// when the candidate is acceptable; otherwise it is the user-facing reason the
// candidate was skipped, recorded on the grab history (PLAN phase 3, task 3).
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
	q := r.Parsed.Quality
	idx := indexOf(p.Items, q)
	if idx < 0 {
		return 0, fmt.Sprintf("quality %s is not in profile %q", qualityLabel(q), p.Name)
	}
	// Earlier items are better, so invert the position: the first item scores
	// len(items) rungs, the last scores one.
	rungs := len(p.Items) - idx
	score = rungs * qualityWeight
	score += (len(core.SourceLadder) - core.SourceRank(r.Parsed.Source)) * sourceWeight
	if r.Parsed.Proper {
		score += properBonus
	}
	if r.Parsed.Repack {
		score += repackBonus
	}
	score += min(r.Seeders, seedersCap)
	return score, ""
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
// so the top rows answer "why was this skipped" (PLAN phase 3, task 3).
func SelectBest(candidates []core.Release, p *core.QualityProfile) (best *core.Release, rejected []Decision) {
	bestScore := -1
	for _, r := range candidates {
		score, reject := ScoreRelease(r, p)
		if reject != "" {
			rejected = append(rejected, Decision{Release: r, Reject: reject})
			continue
		}
		if score > bestScore {
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

func indexOf(items []string, v string) int {
	for i, item := range items {
		if item == v {
			return i
		}
	}
	return -1
}
