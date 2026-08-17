package searchql

import (
	"strconv"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/parse"
)

// maxBranches bounds how many alternatives an OR is allowed to fan out into,
// and maxQueries bounds the searches that reach the indexers.
//
// The caps exist because the cost of a query is not Caravan's to pay: every
// branch is a request to every enabled indexer, and a user who types five ORs
// has not asked for that. Beyond the cap the first branches in written order
// win, so the query stays predictable — the user reads their own expression
// left to right and sees which parts were used.
const (
	maxBranches = 4
	maxQueries  = 6
)

// UpstreamQueries is the free text to send to the indexers, most specific
// first.
//
// Only positive terms appear. A negation narrows results and there is no way
// to spell it in a Torznab search, so it stays local; sending its text would
// ask for exactly what the user rejected.
//
// Inside each branch the fan-out is the one the rest of Caravan already does —
// core.MovieSearches, core.SeriesSearches and the dated form of
// core.SceneSearches — because an item page and a typed query that mean the
// same thing have to ask the indexers the same questions. If they did not, the
// search box would find releases the item page never sees.
func (q *Query) UpstreamQueries() []string {
	var out []string
	seen := make(map[string]bool)
	for _, terms := range positiveBranches(q.root) {
		for _, text := range collect(terms).queries() {
			if seen[text] {
				continue
			}
			seen[text] = true
			out = append(out, text)
			if len(out) == maxQueries {
				return out
			}
		}
	}
	return out
}

// HasUpstreamText reports whether the query says anything an indexer can act
// on. `quality:1080p` alone does not: it is a filter over results that were
// never asked for. The API layer refuses those rather than fanning out a
// search for the empty string.
func (q *Query) HasUpstreamText() bool { return len(q.UpstreamQueries()) > 0 }

// positiveBranches expands the expression into its disjunctive branches, each
// a list of the terms that must all hold. A negated subtree contributes an
// empty branch rather than terms: it constrains results locally and says
// nothing about what to ask for.
func positiveBranches(n node) [][]*termNode {
	switch t := n.(type) {
	case *termNode:
		return [][]*termNode{{t}}
	case *notNode:
		return [][]*termNode{nil}
	case *orNode:
		var out [][]*termNode
		for _, kid := range t.kids {
			for _, branch := range positiveBranches(kid) {
				if len(out) == maxBranches {
					return out
				}
				out = append(out, branch)
			}
		}
		return out
	case *andNode:
		out := [][]*termNode{nil}
		for _, kid := range t.kids {
			var next [][]*termNode
			for _, left := range out {
				for _, right := range positiveBranches(kid) {
					if len(next) == maxBranches {
						break
					}
					next = append(next, append(append([]*termNode(nil), left...), right...))
				}
			}
			out = next
		}
		return out
	}
	return [][]*termNode{nil}
}

// branch is one alternative reduced to the parts a query text is built from.
// Only the first value of each field counts: `year:2020 year:2021` cannot be
// spelled as one search, and the first is what the user wrote first.
type branch struct {
	words   []string
	title   string
	site    string
	year    int
	season  int
	episode int
	date    time.Time
	hasDate bool
}

func collect(terms []*termNode) branch {
	// Season -1 is core.SeriesSearches' "no season named", which is a
	// different search from season 0 (specials).
	b := branch{season: -1}
	setInt := func(target *int, value string, absent int) {
		if *target != absent {
			return
		}
		if n, err := strconv.Atoi(value); err == nil {
			*target = n
		}
	}
	for _, term := range terms {
		switch term.field {
		case "":
			// Text that slugs to nothing is punctuation, and punctuation in a
			// query only narrows what an indexer will match.
			if parse.TitleSlug(term.text) != "" {
				b.words = append(b.words, term.text)
			}
		case fieldTitle:
			if b.title == "" {
				b.title = term.text
			}
		case fieldSite:
			if b.site == "" {
				b.site = term.text
			}
		case fieldYear:
			setInt(&b.year, term.text, 0)
		case fieldSeason:
			setInt(&b.season, term.text, -1)
		case fieldEpisode:
			setInt(&b.episode, term.text, 0)
		case fieldDate:
			if !b.hasDate {
				if when, ok := parseDate(term.text); ok {
					b.date, b.hasDate = when, true
				}
			}
		}
	}
	return b
}

// queries is the search text one branch turns into.
//
// The three expansions are mutually exclusive because the identifiers are: a
// scene is named by its date, a television episode by SxxEyy, a movie by its
// year. An episode number with no season is dropped — there is no way to write
// it in a release name — but it still filters the results locally.
func (b branch) queries() []string {
	parts := make([]string, 0, len(b.words)+2)
	for _, value := range append(append([]string{}, b.words...), b.title, b.site) {
		if value = strings.TrimSpace(value); value != "" {
			parts = append(parts, value)
		}
	}
	base := strings.Join(parts, " ")
	if base == "" {
		return nil
	}
	switch {
	case b.hasDate:
		return []string{base + " " + b.date.UTC().Format(core.SceneDateLayout)}
	case b.season >= 0:
		return core.SeriesSearches(base, b.season, b.episode)
	case b.year > 0:
		return core.MovieSearches(base, b.year)
	default:
		return []string{base}
	}
}
