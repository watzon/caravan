package library

import (
	"strings"
	"unicode"
)

// Match scores. A candidate must clear minMatchScore to be accepted; anything
// below parks in the unmatched queue for a human (SPEC §10.1).
const (
	scoreTitleExact = 3
	scoreTitlePart  = 1
	scoreYearExact  = 3
	scoreYearNear   = 1
	scoreYearWrong  = -2

	minMatchScore = 3
)

// candidate is the shape scoreCandidates works on, so movie and series
// matching share one scoring rule instead of drifting apart.
type candidate struct {
	title         string
	originalTitle string
	year          int
}

// bestMatch returns the index of the best-scoring candidate, or -1 when none
// clears minMatchScore.
//
// The rule: an exact normalized title match is worth a match on its own; a
// partial title match needs the year to agree. A wrong year is penalized
// rather than disqualifying, because parsed years are frequently the release
// year rather than the production year.
func bestMatch(cands []candidate, title string, year int) int {
	want := normalizeTitle(title)
	if want == "" {
		return -1
	}

	best, bestScore := -1, 0
	for i, c := range cands {
		score := titleScore(c, want) + yearScore(c.year, year)
		if score > bestScore {
			best, bestScore = i, score
		}
	}
	if bestScore < minMatchScore {
		return -1
	}
	return best
}

func titleScore(c candidate, want string) int {
	for _, have := range []string{normalizeTitle(c.title), normalizeTitle(c.originalTitle)} {
		if have == "" {
			continue
		}
		if have == want {
			return scoreTitleExact
		}
	}
	for _, have := range []string{normalizeTitle(c.title), normalizeTitle(c.originalTitle)} {
		if have == "" {
			continue
		}
		if strings.Contains(have, want) || strings.Contains(want, have) {
			return scoreTitlePart
		}
	}
	return 0
}

// yearScore is neutral when the filename carried no year: most episode
// filenames do not, and that is not evidence against a candidate.
func yearScore(have, want int) int {
	if want == 0 || have == 0 {
		return 0
	}
	switch diff := have - want; {
	case diff == 0:
		return scoreYearExact
	case diff == 1 || diff == -1:
		return scoreYearNear
	default:
		return scoreYearWrong
	}
}

// normalizeTitle folds a title to comparable form: lowercase, letters and
// digits only, single-spaced. Apostrophes are dropped rather than turned into
// separators, which is what makes "Marvel's Daredevil" and "Marvels Daredevil"
// the same title; every other separator collapses to one space.
func normalizeTitle(s string) string {
	var b strings.Builder
	space := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r == '\'' || r == '’':
			// deliberately neither a separator nor a character
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if space && b.Len() > 0 {
				b.WriteByte(' ')
			}
			space = false
			b.WriteRune(r)
		default:
			space = true
		}
	}
	return b.String()
}
