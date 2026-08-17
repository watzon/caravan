package searchql

import (
	"strconv"
	"strings"
	"time"

	"github.com/watzon/caravan/internal/core"
	"github.com/watzon/caravan/internal/parse"
)

// isoDateLayout is how a date is written in a query, and what the seed
// builders emit. core.SceneDateLayout is accepted alongside it so that a date
// copied straight out of a release name also works.
const isoDateLayout = "2006-01-02"

var dateLayouts = []string{isoDateLayout, core.SceneDateLayout}

// Matches reports whether a release survives the query's local filters.
//
// It is not the whole query: the free text already went to the indexers, and
// what comes back is theirs to justify. See the package comment for why a
// positive keyword is true here regardless of the release name.
func (q *Query) Matches(rel core.Release) bool { return eval(q.root, rel, false) }

// eval walks the tree. negated says whether an odd number of NOTs stand
// between this node and the root, which only bare keywords care about — they
// are the one term whose meaning differs by direction.
func eval(n node, rel core.Release, negated bool) bool {
	switch t := n.(type) {
	case *termNode:
		if t.field == "" {
			return matchKeyword(t.text, rel, negated)
		}
		return matchField(t.field, t.text, rel)
	case *notNode:
		return !eval(t.child, rel, !negated)
	case *andNode:
		for _, kid := range t.kids {
			if !eval(kid, rel, negated) {
				return false
			}
		}
		return true
	case *orNode:
		for _, kid := range t.kids {
			if eval(kid, rel, negated) {
				return true
			}
		}
		return false
	}
	return true
}

// matchKeyword is where the honest split lives.
//
// A positive keyword was sent upstream, and an indexer matches on more than
// the release name — a description, a tag, an alternate title Caravan has
// never heard of. Re-testing it against the name here would silently drop
// results the user did ask for, so it passes.
//
// A negated keyword is the opposite case: nothing was sent upstream for it, so
// the local test is the only one there will ever be. It runs against the
// release name, which is the only text Caravan is sure of.
func matchKeyword(text string, rel core.Release, negated bool) bool {
	if !negated {
		return true
	}
	if parse.TitleSlug(text) == "" {
		// Punctuation with no letters or digits excludes nothing. The NOT
		// above inverts this answer, so false is what leaves the release in.
		return false
	}
	return slugContains(rel.Title, text)
}

// slugContains compares two pieces of text the way the rest of Caravan
// compares titles: as slugs, so "Marvel's S.H.I.E.L.D." and "Marvels SHIELD"
// agree. A needle that slugs to nothing — punctuation only — carries no
// constraint and matches, rather than matching everything or nothing by
// accident.
func slugContains(haystack, needle string) bool {
	wanted := parse.TitleSlug(needle)
	if wanted == "" {
		return true
	}
	return strings.Contains(parse.TitleSlug(haystack), wanted)
}

// siteContains is slugContains with the word breaks taken out of both sides.
//
// It is looser than the title comparison on purpose, and that looseness is the
// reason site: exists as its own field. A site writes itself run-together in
// the release name it publishes — "CreampieThais.26.01.19", "BangBros18" —
// and with spaces everywhere else, including in the seed expression a scene
// page produces. Compared as titles the two never agree, so every scene search
// would return nothing.
func siteContains(haystack, needle string) bool {
	wanted := strings.ReplaceAll(parse.TitleSlug(needle), " ", "")
	if wanted == "" {
		return true
	}
	return strings.Contains(strings.ReplaceAll(parse.TitleSlug(haystack), " ", ""), wanted)
}

// matchField evaluates one field term.
//
// A value that cannot be read as what the field wants — year:abc — makes the
// term false rather than an error. The user sees an empty result list, which
// says "that filter found nothing" on its own; refusing the search would
// instead throw away every result for one mistyped character.
func matchField(field, value string, rel core.Release) bool {
	parsed := rel.Parsed
	switch field {
	case fieldTitle:
		// The parser's title is the better target, but it is empty for a name
		// it could make nothing of, and then the raw name is all there is.
		target := parsed.Title
		if target == "" {
			target = rel.Title
		}
		return slugContains(target, value)
	case fieldSite:
		// A scene release leads with its site, so the site is the parsed title
		// — except when the name did not parse, where it is still the first
		// thing in the raw name.
		return siteContains(parsed.Title, value) || siteContains(rel.Title, value)
	case fieldYear:
		want, err := strconv.Atoi(value)
		return err == nil && parsed.Year != 0 && parsed.Year == want
	case fieldSeason:
		want, err := strconv.Atoi(value)
		return err == nil && parsed.Season == want
	case fieldEpisode:
		want, err := strconv.Atoi(value)
		if err != nil {
			return false
		}
		for _, episode := range parsed.Episodes {
			if episode == want {
				return true
			}
		}
		return false
	case fieldDate:
		want, ok := parseDate(value)
		if !ok || parsed.SceneDate.IsZero() {
			return false
		}
		got := parsed.SceneDate.UTC()
		return got.Year() == want.Year() && got.Month() == want.Month() && got.Day() == want.Day()
	case fieldQuality:
		return strings.EqualFold(normalizeQuality(value), parsed.Quality)
	case fieldSource:
		return strings.EqualFold(value, parsed.Source)
	case fieldCodec:
		return strings.EqualFold(value, parsed.Codec)
	case fieldAudio:
		return strings.EqualFold(value, parsed.Audio)
	case fieldGroup:
		return strings.EqualFold(value, parsed.Group)
	case fieldBitDepth:
		want, err := strconv.Atoi(value)
		return err == nil && parsed.BitDepth == want
	case fieldEdition:
		return slugContains(parsed.Edition, value)
	case fieldIndexer:
		return strings.Contains(strings.ToLower(rel.Indexer), strings.ToLower(value))
	case fieldIs:
		switch strings.ToLower(value) {
		case isProper:
			return parsed.Proper
		case isRepack:
			return parsed.Repack
		case isSeasonPack:
			// The same reading the picker's season-pack flag uses: a
			// television release that names no episode covers the season.
			return !parsed.IsEpisode()
		}
		return false
	}
	return false
}

// parseDate reads either accepted spelling. The result is a calendar day in
// UTC, which is the only precision a release name ever carries.
func parseDate(value string) (time.Time, bool) {
	for _, layout := range dateLayouts {
		if when, err := time.Parse(layout, value); err == nil {
			return when.UTC(), true
		}
	}
	return time.Time{}, false
}

// normalizeQuality maps the spellings people type onto the rungs of
// core.QualityLadder. Nothing else is accepted: a quality Caravan does not
// have a rung for cannot match a parsed release either way.
func normalizeQuality(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "4k", "uhd", "2160", "2160p":
		return core.Quality2160p
	case "1080", "1080p":
		return core.Quality1080p
	case "720", "720p":
		return core.Quality720p
	case "480", "480p":
		return core.Quality480p
	}
	return value
}
