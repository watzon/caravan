package library

import "github.com/watzon/caravan/internal/core"

// resolveAbsolute turns an absolute-only parse — a name that claims "the 105th
// episode" and nothing more — into a (season, episode) one, using the
// provider's own episode tree as the only authority on where that number lands.
//
// It is pure: the tree is already in hand wherever this is called, so placing a
// file costs no request and no store read.
//
// false means the tree cannot answer. That is a park, not an error: the file
// names an episode identity nothing on hand can place, and the review queue is
// where a claim nobody can resolve belongs. The returned parse is unchanged in
// that case, so a caller may report what the name said.
func resolveAbsolute(meta *core.SeriesMeta, p core.ParsedRelease) (core.ParsedRelease, bool) {
	// Already resolved. A name that said S05E03 answered the question itself,
	// and saying so here is what lets callers run this unconditionally on the
	// episode path rather than branching around it.
	if len(p.Episodes) > 0 {
		return p, true
	}
	// Nothing to place. A parse with neither an episode list nor an absolute
	// number has made no claim this function can act on.
	if p.Absolute == 0 {
		return p, false
	}

	// The provider said so. Season 0 is skipped because specials are numbered
	// inside their own season and hold no place in the running order a release
	// name counts along — TheTVDB serves them with absolute 0 for exactly that
	// reason, and a special that carried a stray one would otherwise outrank
	// the real episode.
	//
	// First hit wins. The absolute column is not unique (migration 0025 says
	// why), and the tree arrives in season then episode order, so the earliest
	// episode claiming the number is the one a renumbering has not moved yet.
	//
	// p.Absolute is kept: it is what the name claimed, and the claim stays
	// readable after the placement.
	seasons := 0
	var only *core.SeasonMeta
	hasAbsolutes := false
	for i := range meta.Seasons {
		sm := &meta.Seasons[i]
		if sm.Number == 0 {
			continue
		}
		seasons++
		only = sm
		for _, em := range sm.Episodes {
			if em.Absolute == 0 {
				continue
			}
			hasAbsolutes = true
			if em.Absolute == p.Absolute {
				p.Season = em.Season
				p.Episodes = []int{em.Number}
				return p, true
			}
		}
	}

	// One season and no absolute numbering anywhere in it: the episode number
	// IS the series-wide number, because nothing this tree describes comes
	// before its own episode 1. That is a statement about the shape of the
	// answer, not about the show — it is how AniList models a cour, and it is
	// equally true of any single-season series.
	//
	// A provider that DOES serve absolute numbers is not second-guessed here.
	// Its silence about this particular number is an answer — "not in my
	// order" — and overriding that with a positional guess is precisely the
	// misfiling below.
	if seasons == 1 && !hasAbsolutes {
		for _, em := range only.Episodes {
			if em.Number == p.Absolute {
				p.Season = em.Season
				p.Episodes = []int{em.Number}
				return p, true
			}
		}
	}
	return p, false
}

// There is deliberately no fallback that counts episodes across seasons until
// it reaches p.Absolute. Counting is wrong wherever the real order is: a
// special promoted into it, a cour the provider files as two seasons or as one,
// a series renumbered upstream after half of it was catalogued. And counting
// fails silently — it always produces a season and an episode, and a wrong one
// files a real file against an episode that already exists, overwriting the
// library's own truth about it. That is the exact failure the review queue
// exists to prevent, so a number nothing can place parks instead.
