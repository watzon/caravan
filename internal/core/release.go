package core

import (
	"strings"
	"time"
	"unicode"
)

// Indexer types. Torznab carries torrent results, Newznab carries Usenet
// results; both speak the same XML dialect, which is why one client covers
// them (SPEC §5.1).
const (
	IndexerTypeTorznab = "torznab"
	IndexerTypeNewznab = "newznab"
)

// IndexerConfig is a configured search source (SPEC §5.1, `indexers`).
//
// Caravan ships with none preconfigured (SPEC §12): every entry here is
// something the user added.
const IndexerDefaultPriority = 25

type IndexerConfig struct {
	ID int64
	// Name is the user-facing label, unique across indexers.
	Name string
	// URL is the indexer's base API URL, without the `/api` path or query.
	URL string
	// APIKey is the credential. It lives in the database, never in the
	// bootstrap YAML and never in logs (SPEC §12).
	APIKey string
	// Type is one of the IndexerType* constants.
	Type string
	// Categories are the indexer category ids every search sends — exactly
	// these, never an inferred default. Empty searches the indexer
	// unfiltered.
	Categories []int
	// Priority orders search sources and breaks otherwise equal release ties.
	// Lower values run first. Existing and omitted configurations use 25.
	Priority int
	// Enabled excludes the indexer from search fan-out when false, without
	// losing its configuration.
	Enabled bool
}

// AdultCategoryBase is the Newznab/Torznab category block adult releases are
// published in: 6000 is "XXX", and 6010–6090 are its subcategories.
//
// It is a constant here rather than a setting because it is not Caravan's
// choice — it is the category id an indexer publishes an adult release under,
// the same way 2000 is movies and 5000 is TV. What the owner configures is
// which of those ids the adult library asks each indexer for (PLAN phase 8
// task 4); what this constant answers is the different question of how to read
// a release that came back.
const AdultCategoryBase = 6000

// SceneDateLayout is how a scene's release date is written in an indexer query.
//
// Scene releases are named "Site YY.MM.DD" rather than with a season and
// episode number, so the date IS the identifier a search can use — Caravan's
// own season (release year) and episode (sequence within that year) are a
// mapping no indexer has heard of. Both search paths, the automatic one and
// the interactive picker, have to spell the date the same way or they would
// look for different releases for the same scene, so they spell it here.
const SceneDateLayout = "06.01.02"

// SceneSearchVariant names one way of asking an indexer for a scene.
type SceneSearchVariant string

const (
	// SceneSearchByDate is "Site YY.MM.DD", how scene releases are named and
	// therefore the query that finds them when they are.
	SceneSearchByDate SceneSearchVariant = "date"
	// SceneSearchByTitle is "Site Scene Title", the fallback for the releases
	// the date query cannot find: a packager who named a release after its
	// title or its performers rather than its date is invisible to the first
	// query no matter how good the matching downstream is.
	//
	// This is the improvement Whisparr's own scene search does not have (its
	// issue #115): it sends the date form and nothing else, so a title-named
	// release is simply never seen.
	SceneSearchByTitle SceneSearchVariant = "title"
)

// SceneSearch is one search to send for a scene, and which variant it is. It is
// not SceneQuery, which is the provider-side scene lookup in adult.go: this one
// is a string headed for an indexer.
type SceneSearch struct {
	Variant SceneSearchVariant
	Query   string
}

// SceneSearches is every search worth making for one scene, best first.
//
// It lives here, next to SceneDateLayout and for the same reason: the automatic
// search and the interactive picker have to ask the indexers the same questions
// or the picker would show a user candidates the automatic path never sees, and
// vice versa.
//
// The date query comes first because it is the one with an exact answer — a
// scene release named the standard way carries the date, and matching it needs
// no guessing. The title query is a fallback, and everything downstream treats
// it as one: what it returns is accepted only on a much stricter test (see the
// automation runner's matchesSceneTitle).
//
// A site with no name, a scene with no date and no title, or a title that is
// nothing but punctuation all yield fewer queries — or none, which callers read
// as "there is nothing to search for" rather than as "search for everything".
func SceneSearches(site string, airDate time.Time, title string) []SceneSearch {
	site = strings.TrimSpace(site)
	if site == "" {
		return nil
	}
	out := make([]SceneSearch, 0, 2)
	if !airDate.IsZero() {
		out = append(out, SceneSearch{
			Variant: SceneSearchByDate,
			Query:   site + " " + airDate.UTC().Format(SceneDateLayout),
		})
	}
	if clean := sceneQueryText(title); clean != "" {
		out = append(out, SceneSearch{Variant: SceneSearchByTitle, Query: site + " " + clean})
	}
	return out
}

// sceneQueryText strips a scene title down to the words an indexer can match.
//
// Release names separate words with dots and carry no punctuation, so a title's
// apostrophes, colons and dashes are noise that only narrows the search — a
// query for "Don't Look Back" finds nothing an indexer filed as
// "Dont.Look.Back". Case is left alone: Torznab searches are case-insensitive,
// and lowercasing a title would only make the query harder to read in a log.
func sceneQueryText(title string) string {
	var b strings.Builder
	for _, r := range title {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			continue
		}
		b.WriteByte(' ')
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// IsAdultCategory reports whether an indexer category id is in the adult
// block, parent id or subcategory alike.
func IsAdultCategory(id int) bool {
	return id >= AdultCategoryBase && id < AdultCategoryBase+1000
}

// HasAdultCategory reports whether any of the ids is in the adult block. It is
// what selects the date-based scene parser for a search result (PLAN phase 9
// task 4): a release an indexer filed under XXX is named the way scenes are
// named, whichever library asked for it.
func HasAdultCategory(ids []int) bool {
	for _, id := range ids {
		if IsAdultCategory(id) {
			return true
		}
	}
	return false
}

// IndexerCategory is one node of the category tree an indexer advertises in
// its capabilities document. The tree is what the settings UI renders as a
// picker; the ids are what IndexerConfig.Categories stores.
type IndexerCategory struct {
	ID      int               `json:"id"`
	Name    string            `json:"name"`
	Subcats []IndexerCategory `json:"subcats"`
}

// Release protocols. The protocol decides which engine a grab is routed to
// (SPEC §5.1).
const (
	ProtocolTorrent = "torrent"
	ProtocolUsenet  = "usenet"
)

// Release is one search result from an indexer, already run through the
// release parser.
//
// A Release is a claim, not a fact: everything except ID and IndexerID comes
// from the indexer, and Parsed is the parser's reading of Title. It is cached
// in the `releases` table so later searches can recognize something already
// seen, but losing that cache costs nothing but a re-search.
type Release struct {
	// ID is the `releases` row id, 0 for a result that has not been cached.
	ID int64
	// IndexerID references indexers.id. It is a soft reference: the indexer
	// may have been deleted since the result was cached.
	IndexerID int64
	// Indexer is the indexer's display name, denormalized so a cached result
	// still says where it came from after the indexer is deleted.
	Indexer string
	// Title is the release name as the indexer published it.
	Title string
	// GUID is the indexer's identifier for this result. Unique per indexer,
	// and what deduplicates repeat sightings.
	GUID string
	// DownloadURL is the .torrent/.nzb URL, or a magnet link.
	DownloadURL string
	// InfoHash is the torrent info hash when the indexer supplied one; empty
	// for Usenet and for indexers that do not publish it.
	InfoHash string
	// Protocol is one of the Protocol* constants.
	Protocol string
	// Size is the release size in bytes, 0 when the indexer did not say.
	Size int64
	// Seeders and Leechers are torrent swarm counts, 0 for Usenet.
	Seeders  int
	Leechers int
	// PublishedAt is when the indexer published the release, zero when unknown.
	PublishedAt time.Time
	// Categories are the indexer category ids the item was published in, empty
	// when the indexer published none.
	//
	// They exist for the one decision that cannot be made by the `cat`
	// parameter: an RSS cycle fetches each indexer once with the union of every
	// library's categories (PLAN phase 8 task 5), so the per-library narrowing
	// has to be re-applied to the results. Empty means the indexer said
	// nothing, which is not the same as "no category" — a filter cannot reject
	// what it cannot see.
	//
	// They ARE cached in the `releases` table (0023). They used not to be —
	// nothing after the match needed them — but the untied universal-search
	// grab must answer "is this cached release adult" for a caller without
	// the adult grant, without re-searching.
	Categories []int
	// Parsed is what the release parser made of Title.
	Parsed ParsedRelease
}

// InCategories reports whether this release satisfies a category filter.
//
// An empty filter accepts everything, which is what an empty category list has
// always meant. A release the indexer published no categories for is also
// accepted: dropping it would silently narrow every indexer that does not
// publish categories down to nothing.
//
// A wanted parent category matches its children, because that is what sending
// it as `cat` does — asking Torznab for 5000 returns 5040, and a filter that
// then rejected 5040 would reject the very releases the fetch asked for.
func (r Release) InCategories(wanted []int) bool {
	if len(wanted) == 0 || len(r.Categories) == 0 {
		return true
	}
	for _, want := range wanted {
		for _, got := range r.Categories {
			if got == want || (want%1000 == 0 && got > want && got < want+1000) {
				return true
			}
		}
	}
	return false
}
