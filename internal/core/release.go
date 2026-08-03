package core

import "time"

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
	// Enabled excludes the indexer from search fan-out when false, without
	// losing its configuration.
	Enabled bool
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
	// They are not cached in the `releases` table: nothing after the match
	// needs them, and a search sends the categories rather than filtering on
	// them.
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
